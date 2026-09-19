package attestation

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Signet epic 17 — custody, declared. Two predicates, one concept, two
// directions; and a forward reference that must survive every Go writer.

func custodyDeclaration() *Record {
	return &Record{
		Issuer:    "https://polis.polis.pub",
		Predicate: PredicateCustody,
		Subject:   Subject{Type: SubjectIdentity, ID: "https://alice.polis.pub"},
		Payload: map[string]string{
			CustodyKeyHolds:       CustodyHoldsIdentityKey,
			CustodyKeyAttribution: CustodyAttributionAsTenant,
		},
	}
}

func custodyGrant() *Record {
	return &Record{
		Issuer:    "https://alice.polis.pub",
		Predicate: PredicateCustodyGrant,
		Subject:   Subject{Type: SubjectIdentity, ID: "https://polis.polis.pub"},
		Payload: map[string]string{
			CustodyKeyScope: CustodyScopeCustodial,
			CustodyKeyBasis: CustodyBasisUserSigned,
		},
	}
}

func TestCustodyDeclarationIssuesAndVerifies(t *testing.T) {
	for _, attribution := range []string{CustodyAttributionAsTenant, CustodyAttributionCoSigned} {
		t.Run(attribution, func(t *testing.T) {
			dir := t.TempDir()
			priv, pub := newTestKeys(t)
			writeWellKnown(t, dir, pub)

			r := custodyDeclaration()
			r.Payload[CustodyKeyAttribution] = attribution
			id, err := Issue(dir, r, priv)
			if err != nil {
				t.Fatalf("Issue: %v", err)
			}
			if st, err := VerifyFile(dir, Path(dir, id)); st != StatusValid {
				t.Fatalf("status = %s (%v), want valid", st, err)
			}
		})
	}
}

// ⛔ D2: `as-tenant` is the honest value for an operator that signs as the
// tenant. If the writer refused it, that operator would not declare at all.
func TestCustodyDeclarationAcceptsAsTenant(t *testing.T) {
	dir := t.TempDir()
	priv, _ := newTestKeys(t)
	if _, err := Issue(dir, custodyDeclaration(), priv); err != nil {
		t.Fatalf("as-tenant must be a legal attribution: %v", err)
	}
}

func TestCustodyWriterRulesHaveNoDefaults(t *testing.T) {
	priv, _ := newTestKeys(t)
	cases := []struct {
		name   string
		record func() *Record
		want   string
	}{
		{"declaration without attribution", func() *Record {
			r := custodyDeclaration()
			delete(r.Payload, CustodyKeyAttribution)
			return r
		}, "attribution"},
		{"declaration with an unknown attribution", func() *Record {
			r := custodyDeclaration()
			r.Payload[CustodyKeyAttribution] = "trusted"
			return r
		}, "attribution"},
		{"declaration without holds", func() *Record {
			r := custodyDeclaration()
			delete(r.Payload, CustodyKeyHolds)
			return r
		}, "holds"},
		{"grant without basis", func() *Record {
			r := custodyGrant()
			delete(r.Payload, CustodyKeyBasis)
			return r
		}, "basis"},
		{"grant with an enumerated scope", func() *Record {
			r := custodyGrant()
			r.Payload[CustodyKeyScope] = "pub.polis.follow.announced"
			return r
		}, "scope"},
		{"grant about a uri", func() *Record {
			r := custodyGrant()
			r.Subject.Type = SubjectURI
			return r
		}, "subject type"},
		{"terms that are not https", func() *Record {
			r := custodyGrant()
			r.Payload[CustodyKeyTerms] = "http://polis.pub/terms"
			return r
		}, "https"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Issue(t.TempDir(), c.record(), priv)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want one naming %q", err, c.want)
			}
		})
	}
}

// ⚠️ A WRITER rule only. A reader must still load and verify a custody record
// that breaks it — tolerance is the extensibility hinge.
func TestAMalformedCustodyRecordStillLoadsAndVerifies(t *testing.T) {
	dir := t.TempDir()
	priv, pub := newTestKeys(t)
	writeWellKnown(t, dir, pub)

	r := custodyGrant()
	r.Type = TypeName
	r.Payload = map[string]string{CustodyKeyScope: "everything"}
	r.Asserted = "2026-09-14T00:00:00Z"
	if err := signAndStamp(r, priv); err != nil {
		t.Fatal(err)
	}
	if err := write(Path(dir, ID(r)), r); err != nil {
		t.Fatal(err)
	}
	if st, err := VerifyFile(dir, Path(dir, ID(r))); st != StatusValid {
		t.Fatalf("status = %s (%v), want valid — readers accept what writers refuse", st, err)
	}
}

// ---------- D7: the forward reference ----------

func TestWithdrawingAGrantWritesAForwardReference(t *testing.T) {
	dir := t.TempDir()
	priv, pub := newTestKeys(t)
	writeWellKnown(t, dir, pub)

	grantID, err := Issue(dir, custodyGrant(), priv)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	before, err := Load(Path(dir, grantID))
	if err != nil {
		t.Fatal(err)
	}

	wID, err := Withdraw(dir, grantID, priv)
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}

	after, err := Load(Path(dir, grantID))
	if err != nil {
		t.Fatal(err)
	}
	if want := RecordURL(before.Issuer, wID); after.WithdrawnBy != want {
		t.Fatalf("withdrawn_by = %q, want %q", after.WithdrawnBy, want)
	}

	// The signed region did not move.
	if after.Version != before.Version || after.Signature != before.Signature || ID(after) != grantID {
		t.Fatal("the forward reference changed the signed record's version, signature or id")
	}
	if st, err := VerifyRecord(dir, after); st != StatusValid {
		t.Fatalf("a withdrawn grant must still verify: %s (%v)", st, err)
	}

	// The pointer is discovery: following it reaches a withdrawal that is signed
	// and names this exact record and version.
	w, err := Load(Path(dir, wID))
	if err != nil {
		t.Fatal(err)
	}
	if w.Predicate != PredicateWithdrawal || w.Subject.ID != RecordURL(before.Issuer, grantID) || w.Subject.Version != before.Version {
		t.Fatalf("the pointer leads to %+v, which does not name the grant", w)
	}
	if st, _ := VerifyRecord(dir, w); st != StatusValid {
		t.Fatalf("the withdrawal the pointer names does not verify: %s", st)
	}
}

// ⛔ D7's trap, asserted. Every Go writer round-trips through Record, so the
// field survives only because it is DECLARED.
func TestForwardReferenceSurvivesAGoRoundTrip(t *testing.T) {
	dir := t.TempDir()
	priv, pub := newTestKeys(t)
	writeWellKnown(t, dir, pub)

	grantID, _ := Issue(dir, custodyGrant(), priv)
	if _, err := Withdraw(dir, grantID, priv); err != nil {
		t.Fatal(err)
	}
	r, err := Load(Path(dir, grantID))
	if err != nil {
		t.Fatal(err)
	}
	want := r.WithdrawnBy

	// A writer that loads and saves — what Medic, Patrol or a later rebuild
	// would do.
	if err := write(Path(dir, grantID), r); err != nil {
		t.Fatal(err)
	}
	// And the listing path every reader uses.
	listed, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range listed {
		if ID(l) == grantID {
			found = true
			if l.WithdrawnBy != want {
				t.Fatalf("withdrawn_by after a round-trip = %q, want %q — a Go writer un-withdrew the grant", l.WithdrawnBy, want)
			}
		}
	}
	if !found {
		t.Fatal("grant missing from List")
	}
}

// D7's PREDICTION, checked rather than assumed: an UNDECLARED field does not
// survive Load — so a Go writer that rewrote the record would drop it. If the
// first half ever passes without the drop, the model of Load that D7 is built on
// is wrong, and that matters for every other record type.
//
// ⛔ The second half: because Load drops it, the writer REFUSES rather than
// sign a file missing it, and the file on disk is untouched.
func TestAnUndeclaredFieldMakesTheGoWriterRefuse(t *testing.T) {
	dir := t.TempDir()
	priv, _ := newTestKeys(t)
	id, _ := Issue(dir, custodyGrant(), priv)

	raw, _ := os.ReadFile(Path(dir, id))
	var m map[string]interface{}
	_ = json.Unmarshal(raw, &m)
	m["superseded_by"] = "https://alice.polis.pub/content/pub.polis.core/attestation/x.json"
	patched, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(Path(dir, id), patched, 0644); err != nil {
		t.Fatal(err)
	}

	r, err := Load(Path(dir, id))
	if err != nil {
		t.Fatal(err)
	}
	if out, _ := json.Marshal(r); bytes.Contains(out, []byte("superseded_by")) {
		t.Fatal("an undeclared field survived Load — D7's model of Load is wrong")
	}
	if err := write(Path(dir, id), r); err == nil {
		t.Fatal("the writer rewrote a record carrying a field it could not read")
	}
	after, _ := os.ReadFile(Path(dir, id))
	if !bytes.Equal(after, patched) {
		t.Fatal("a refused write still changed the file")
	}
}

func TestForwardReferenceIsOutsideTheSignature(t *testing.T) {
	r := sampleRecord()
	before, _ := CanonicalJSON(r)
	r.WithdrawnBy = "https://vdibart.polis.pub/content/pub.polis.core/attestation/20260914T000000Z-0000000000000000.json"
	after, _ := CanonicalJSON(r)
	if !bytes.Equal(before, after) {
		t.Fatalf("withdrawn_by entered the signing base:\n%s", after)
	}
}

// A new record cannot already be retracted, whatever the caller passed in.
func TestIssueNeverWritesAForwardReference(t *testing.T) {
	dir := t.TempDir()
	priv, _ := newTestKeys(t)
	r := custodyGrant()
	r.WithdrawnBy = "https://example.com/forged.json"
	id, err := Issue(dir, r, priv)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := Load(Path(dir, id))
	if got.WithdrawnBy != "" {
		t.Fatalf("Issue wrote withdrawn_by=%q onto a new record", got.WithdrawnBy)
	}
}
