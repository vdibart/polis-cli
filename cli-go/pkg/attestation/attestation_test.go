package attestation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// goldenCanonical is the signing base for sampleRecord(), byte for byte.
//
// ⚠️ It is exported through this const rather than inlined so the spec-vs-code
// check in spec_test.go can compare the DOCUMENTED bytes against the SAME string
// this test pins. Prose and code drift the moment a human transcribes between
// them; a shared constant plus a mechanical comparison is what stops it.
const goldenCanonical = `{"type":"pub.polis.attestation",` +
	`"issuer":"https://vdibart.polis.pub",` +
	`"predicate":"pub.polis.attestation.same-as",` +
	`"subject":{"type":"identity","id":"https://vincent.example.com"},` +
	`"asserted":"2026-08-28T00:00:00Z",` +
	`"generator":"polis-cli-go/test"}`

func sampleRecord() *Record {
	return &Record{
		Type:      TypeName,
		Issuer:    "https://vdibart.polis.pub",
		Predicate: PredicateSameAs,
		Subject:   Subject{Type: SubjectIdentity, ID: "https://vincent.example.com"},
		Asserted:  "2026-08-28T00:00:00Z",
		Generator: "polis-cli-go/test",
	}
}

func newTestKeys(t *testing.T) ([]byte, []byte) {
	t.Helper()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	return priv, pub
}

// writeWellKnown drops a minimal .well-known/polis carrying pub, so the
// published-key verification paths have an identity key to check against.
func writeWellKnown(t *testing.T, siteDir string, pub []byte) {
	t.Helper()
	dir := filepath.Join(siteDir, ".well-known")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir .well-known: %v", err)
	}
	body, _ := json.Marshal(map[string]string{"public_key": string(pub)})
	if err := os.WriteFile(filepath.Join(dir, "polis"), body, 0644); err != nil {
		t.Fatalf("write .well-known/polis: %v", err)
	}
}

// ---------- the signable set ----------

// TestCanonicalJSONIsTheSpec pins the exact bytes a second implementation must
// reproduce. A diff here is a protocol break, not a refactor: every signature
// ever written stops verifying.
func TestCanonicalJSONIsTheSpec(t *testing.T) {
	got, err := CanonicalJSON(sampleRecord())
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	if string(got) != goldenCanonical {
		t.Errorf("canonical JSON drifted.\n got: %s\nwant: %s", got, goldenCanonical)
	}
}

// TestCanonicalJSONExcludesSignatureAndVersion is the ordering rule in one
// assertion, and it is the thing a second implementer is most likely to get
// wrong.
//
// `current_version` is the hash OF the canonical bytes, so it cannot be inside
// them, and `signature` cannot cover itself. Setting either must leave the
// signing base untouched.
func TestCanonicalJSONExcludesSignatureAndVersion(t *testing.T) {
	r := sampleRecord()
	before, _ := CanonicalJSON(r)

	r.Signature = "-----BEGIN SSH SIGNATURE-----"
	r.Version = "sha256:" + strings.Repeat("a", 64)
	after, _ := CanonicalJSON(r)

	if string(before) != string(after) {
		t.Errorf("signature/current_version leaked into the signing base:\n%s\n%s", before, after)
	}
}

// TestVersionIsTheHashOfTheSignedBytes proves the ordering actually ran the way
// the doc claims: canonicalise, hash into current_version, sign those bytes.
func TestVersionIsTheHashOfTheSignedBytes(t *testing.T) {
	priv, _ := newTestKeys(t)
	dir := t.TempDir()

	r := sampleRecord()
	if _, err := Issue(dir, r, priv); err != nil {
		t.Fatalf("Issue: %v", err)
	}

	canonical, _ := CanonicalJSON(r)
	if want := ContentVersion(canonical); r.Version != want {
		t.Errorf("current_version = %q, want %q — it is not the hash of the signed bytes",
			r.Version, want)
	}
}

// TestEmptyPayloadIsStable: an absent payload and a present-but-empty one must
// have ONE byte sequence, or "I said nothing extra" has two signatures.
func TestEmptyPayloadIsStable(t *testing.T) {
	absent := sampleRecord()
	empty := sampleRecord()
	empty.Payload = map[string]string{}

	a, _ := CanonicalJSON(absent)
	b, _ := CanonicalJSON(empty)
	if string(a) != string(b) {
		t.Errorf("absent and empty payloads canonicalize differently:\n%s\n%s", a, b)
	}
	if strings.Contains(string(a), "payload") {
		t.Errorf("an empty payload was serialised rather than omitted: %s", a)
	}
}

// TestPayloadKeysSortLexicographically pins the ordering rule the spec states.
// A stranger building the signing base from an insertion-ordered map would
// produce different bytes and never know why.
func TestPayloadKeysSortLexicographically(t *testing.T) {
	r := sampleRecord()
	r.Payload = map[string]string{"zeta": "3", "alpha": "1", "mu": "2"}

	got, _ := CanonicalJSON(r)
	want := `"payload":{"alpha":"1","mu":"2","zeta":"3"}`
	if !strings.Contains(string(got), want) {
		t.Errorf("payload keys are not lexicographic.\ngot:  %s\nwant to contain: %s", got, want)
	}
}

// ---------- sign / verify ----------

func TestIssueThenVerify(t *testing.T) {
	priv, pub := newTestKeys(t)
	dir := t.TempDir()

	r := sampleRecord()
	id, err := Issue(dir, r, priv)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if r.Signature == "" {
		t.Fatal("Issue left the signature empty")
	}

	status, err := Verify(r, pub)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if status != StatusValid {
		t.Errorf("status = %q, want %q", status, StatusValid)
	}
	if _, err := os.Stat(Path(dir, id)); err != nil {
		t.Errorf("record not on disk at %s: %v", Path(dir, id), err)
	}
}

// TestSignatureSurvivesTheFile is the round trip that matters: issue, write,
// read back off disk, verify. An in-memory test would miss a marshalling change
// that alters the bytes the signature covers.
func TestSignatureSurvivesTheFile(t *testing.T) {
	priv, pub := newTestKeys(t)
	dir := t.TempDir()

	id, err := Issue(dir, sampleRecord(), priv)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	loaded, err := Load(Path(dir, id))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	status, err := Verify(loaded, pub)
	if err != nil {
		t.Fatalf("Verify after round trip: %v", err)
	}
	if status != StatusValid {
		t.Errorf("status after round trip = %q, want %q", status, StatusValid)
	}
}

// TestTamperedRecordIsInvalidAndStillLoads — a bad signature is evidence, not a
// verdict. Report it; keep the record readable so a human can see what the claim
// said.
func TestTamperedRecordIsInvalidAndStillLoads(t *testing.T) {
	priv, pub := newTestKeys(t)
	dir := t.TempDir()

	id, err := Issue(dir, sampleRecord(), priv)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	path := Path(dir, id)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	tampered := strings.Replace(string(raw),
		"https://vincent.example.com", "https://mallory.example.com", 1)
	if tampered == string(raw) {
		t.Fatal("tamper failed to change the file — the fixture shape moved")
	}
	if err := os.WriteFile(path, []byte(tampered), 0644); err != nil {
		t.Fatalf("write tampered: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("a tampered record must STILL LOAD, got error: %v", err)
	}
	if loaded.Subject.ID != "https://mallory.example.com" {
		t.Error("the tampered subject should be readable — the record is reported, not filtered")
	}

	status, verr := Verify(loaded, pub)
	if status != StatusInvalid {
		t.Errorf("status = %q, want %q", status, StatusInvalid)
	}
	if verr == nil {
		t.Error("an invalid signature should carry an explanation for the human chasing it")
	}
}

// TestVersionPinIsInsideTheSignature is D7, and it is the assertion the whole
// "a correction survives an edit" claim rests on.
//
// If the pin were outside the signature, anyone could repoint a correction at
// different bytes and the claim would still verify — which destroys exactly the
// property the pin exists for.
func TestVersionPinIsInsideTheSignature(t *testing.T) {
	priv, pub := newTestKeys(t)
	dir := t.TempDir()

	pin := "sha256:" + strings.Repeat("9f2a", 16)
	r := sampleRecord()
	r.Predicate = PredicateCorrection
	r.Subject = Subject{
		Type:    SubjectURI,
		ID:      "https://site.example/posts/20260901-claim.md",
		Version: pin,
	}
	id, err := Issue(dir, r, priv)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	path := Path(dir, id)

	raw, _ := os.ReadFile(path)
	repointed := strings.Replace(string(raw), pin, "sha256:"+strings.Repeat("dead", 16), 1)
	if repointed == string(raw) {
		t.Fatal("failed to repoint the pin — the fixture shape moved")
	}
	if err := os.WriteFile(path, []byte(repointed), 0644); err != nil {
		t.Fatalf("write repointed: %v", err)
	}

	loaded, _ := Load(path)
	status, _ := Verify(loaded, pub)
	if status != StatusInvalid {
		t.Errorf("repointing the version pin left the record %q — the pin is OUTSIDE the "+
			"signature, which makes it worthless", status)
	}
}

// TestUnsignedIsUnsignedNotInvalid — absence is a fact, never a failure.
func TestUnsignedIsUnsignedNotInvalid(t *testing.T) {
	_, pub := newTestKeys(t)

	status, err := Verify(sampleRecord(), pub) // never signed
	if err != nil {
		t.Errorf("an unsigned record is a FACT, not an error: %v", err)
	}
	if status != StatusUnsigned {
		t.Errorf("status = %q, want %q", status, StatusUnsigned)
	}
}

func TestVerifyWrongKeyIsInvalid(t *testing.T) {
	priv, _ := newTestKeys(t)
	_, otherPub := newTestKeys(t)
	dir := t.TempDir()

	r := sampleRecord()
	if _, err := Issue(dir, r, priv); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if status, _ := Verify(r, otherPub); status != StatusInvalid {
		t.Errorf("status = %q, want %q", status, StatusInvalid)
	}
}

// TestVerifyWithoutKeyIsUnknown — having failed to look is not having looked.
func TestVerifyWithoutKeyIsUnknown(t *testing.T) {
	priv, _ := newTestKeys(t)
	dir := t.TempDir()

	r := sampleRecord()
	if _, err := Issue(dir, r, priv); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if status, _ := Verify(r, nil); status != StatusUnknown {
		t.Errorf("status = %q, want %q", status, StatusUnknown)
	}
}

// ---------- tolerance: the extensibility hinge ----------

// TestUnknownPredicateVerifiesAndRenders is the rule that decides whether this
// format can ever grow. A reader that rejects a predicate it does not recognise
// means no predicate can be added without breaking every deployed reader.
//
// The record must verify normally, parse normally, and expose issuer, subject
// and date so it can be rendered as an opaque claim.
func TestUnknownPredicateVerifiesAndRenders(t *testing.T) {
	priv, pub := newTestKeys(t)
	dir := t.TempDir()

	r := sampleRecord()
	r.Predicate = "com.example.reviewed" // nothing in polis knows this
	id, err := Issue(dir, r, priv)
	if err != nil {
		t.Fatalf("a third party's predicate must be issuable: %v", err)
	}

	loaded, err := Load(Path(dir, id))
	if err != nil {
		t.Fatalf("a record with an unknown predicate must load: %v", err)
	}
	if status, _ := Verify(loaded, pub); status != StatusValid {
		t.Errorf("status = %q, want %q — an unknown predicate must not affect verification", status, StatusValid)
	}
	if loaded.Issuer == "" || loaded.Subject.ID == "" || loaded.Asserted == "" {
		t.Error("issuer, subject and date must all survive so the claim can be rendered opaquely")
	}
	if loaded.Predicate != "com.example.reviewed" {
		t.Errorf("the predicate must be preserved verbatim, got %q", loaded.Predicate)
	}
}

// TestUnknownSubjectTypeIsReadable is the same rule one level down, and it is
// why the referent lives under a uniform `id` key.
//
// A reader meeting a subject type it does not know must still be able to say
// WHAT the claim is about. With a per-type key (`uri:` / `identity:`) it could
// not — it would not know which key to read.
func TestUnknownSubjectTypeIsReadable(t *testing.T) {
	priv, pub := newTestKeys(t)
	path := filepath.Join(t.TempDir(), "future.json")

	r := sampleRecord()
	r.Subject = Subject{Type: "relationship", ID: "https://a.example/follows/b.example"}
	if err := signAndStamp(r, priv); err != nil {
		t.Fatalf("signAndStamp: %v", err)
	}
	if err := write(path, r); err != nil {
		t.Fatalf("write: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("a record with an unknown subject type must load: %v", err)
	}
	if status, _ := Verify(loaded, pub); status != StatusValid {
		t.Errorf("status = %q, want %q", status, StatusValid)
	}
	if loaded.Subject.ID != "https://a.example/follows/b.example" {
		t.Error("the referent must be readable without understanding the subject type")
	}
}

// TestUnknownTopLevelKeyDoesNotBreakVerification — the signing base is rebuilt
// from the parsed fields, so a key we do not model is simply not covered.
//
// The important half is the WARNING this pins: a verifier must not infer that
// everything in the file was signed.
func TestUnknownTopLevelKeyDoesNotBreakVerification(t *testing.T) {
	priv, pub := newTestKeys(t)
	dir := t.TempDir()

	id, err := Issue(dir, sampleRecord(), priv)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	path := Path(dir, id)

	raw, _ := os.ReadFile(path)
	withExtra := strings.Replace(string(raw), "{\n", "{\n  \"future_field\": \"hello\",\n", 1)
	if withExtra == string(raw) {
		t.Fatal("failed to splice an extra key — the fixture shape moved")
	}
	if err := os.WriteFile(path, []byte(withExtra), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("an unknown top-level key must not break parsing: %v", err)
	}
	if status, _ := Verify(loaded, pub); status != StatusValid {
		t.Errorf("status = %q, want %q — unknown keys are outside the signature, not fatal to it",
			status, StatusValid)
	}
}

// ---------- the write path constrains only what WE write ----------

func TestIssueRejectsBarePredicate(t *testing.T) {
	priv, _ := newTestKeys(t)
	r := sampleRecord()
	r.Predicate = "same-as" // the prose form — never legal in a file
	if _, err := Issue(t.TempDir(), r, priv); err == nil {
		t.Error("issuing a bare predicate should fail — the namespace is what makes tolerance meaningful")
	}
}

// TestIssueRefusesTheWithdrawalPredicate — a withdrawal written through Issue
// would skip all three of Withdraw's safeguards. The refusal must name the
// command that does it properly, and must write nothing.
func TestIssueRefusesTheWithdrawalPredicate(t *testing.T) {
	priv, pub := newTestKeys(t)
	dir := t.TempDir()
	writeWellKnown(t, dir, pub)

	r := sampleRecord()
	r.Predicate = PredicateWithdrawal
	r.Subject = Subject{
		Type:    SubjectURI,
		ID:      "https://someone-else.example/content/pub.polis.core/attestation/x.json",
		Version: "sha256:" + strings.Repeat("a", 64),
	}
	_, err := Issue(dir, r, priv)
	if err == nil {
		t.Fatal("Issue accepted pub.polis.attestation.withdrawal — a retraction of a claim this site never made, with no checks")
	}
	if !strings.Contains(err.Error(), "polis attest withdraw") {
		t.Errorf("the refusal should point at `polis attest withdraw`, got: %v", err)
	}
	if records, _ := List(dir); len(records) != 0 {
		t.Errorf("a refused issue wrote %d record(s)", len(records))
	}
}

// TestWithdrawStillWritesAWithdrawal — the guard is on Issue only; the one
// legitimate writer of the predicate must be unaffected.
func TestWithdrawStillWritesAWithdrawal(t *testing.T) {
	priv, pub := newTestKeys(t)
	dir := t.TempDir()
	writeWellKnown(t, dir, pub)

	id, err := Issue(dir, sampleRecord(), priv)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	wid, err := Withdraw(dir, id, priv)
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	w, err := Load(Path(dir, wid))
	if err != nil {
		t.Fatalf("load withdrawal: %v", err)
	}
	if w.Predicate != PredicateWithdrawal {
		t.Errorf("predicate = %q, want %q", w.Predicate, PredicateWithdrawal)
	}
}

func TestIssueRejectsUnknownSubjectType(t *testing.T) {
	priv, _ := newTestKeys(t)
	r := sampleRecord()
	r.Subject.Type = "relationship"
	if _, err := Issue(t.TempDir(), r, priv); err == nil {
		t.Error("issuing an unmodelled subject type should fail on the WRITE path")
	}
}

func TestIssueRejectsPinOnIdentitySubject(t *testing.T) {
	priv, _ := newTestKeys(t)
	r := sampleRecord()
	r.Subject.Version = "sha256:" + strings.Repeat("a", 64)
	if _, err := Issue(t.TempDir(), r, priv); err == nil {
		t.Error("a version pin on an identity subject is meaningless and should be refused")
	}
}

func TestIssueRejectsMalformedPin(t *testing.T) {
	priv, _ := newTestKeys(t)
	r := sampleRecord()
	r.Subject = Subject{Type: SubjectURI, ID: "https://site.example/x.md", Version: "9f2a"}
	if _, err := Issue(t.TempDir(), r, priv); err == nil {
		t.Error("a pin that is not sha256:<64 hex> should be refused")
	}
}

// ---------- cardinality: one file, one subject ----------

// TestOneClaimPerSubjectWritesOneFileEach is D3 made visible. Issuing the same
// predicate about five subjects produces five files with five signatures and
// five independent timestamps — which is the point, not an inefficiency.
func TestOneClaimPerSubjectWritesOneFileEach(t *testing.T) {
	priv, _ := newTestKeys(t)
	dir := t.TempDir()

	subjects := []string{
		"https://a.example", "https://b.example", "https://c.example",
		"https://d.example", "https://e.example",
	}
	ids := map[string]bool{}
	for _, s := range subjects {
		r := sampleRecord()
		r.Predicate = PredicateEndorsement
		r.Subject = Subject{Type: SubjectIdentity, ID: s}
		id, err := Issue(dir, r, priv)
		if err != nil {
			t.Fatalf("Issue %s: %v", s, err)
		}
		ids[id] = true
	}

	if len(ids) != len(subjects) {
		t.Errorf("got %d distinct ids for %d subjects — records collided", len(ids), len(subjects))
	}
	records, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != len(subjects) {
		t.Errorf("got %d files, want %d", len(records), len(subjects))
	}
	for _, r := range records {
		if r.Signature == "" {
			t.Error("every record carries its own signature")
		}
	}
}

// ---------- an attestation about an attestation ----------

// TestSubjectMayBeAnotherAttestation is D5, and it is the decision that buys
// countersigning, revocation, disputes and delegation with no new primitives.
//
// A record is addressable at a permanent URL and carries a content hash, so it
// already satisfies the URI subject type. Stating that is what keeps a reader
// from reasonably assuming subjects are only "real" objects — an assumption that
// would be a schema break to undo later.
func TestSubjectMayBeAnotherAttestation(t *testing.T) {
	priv, pub := newTestKeys(t)
	dir := t.TempDir()

	first := sampleRecord()
	firstID, err := Issue(dir, first, priv)
	if err != nil {
		t.Fatalf("Issue first: %v", err)
	}

	about := &Record{
		Issuer:    "https://disputes.example",
		Predicate: PredicateCorrection,
		Subject: Subject{
			Type:    SubjectURI,
			ID:      RecordURL("https://vdibart.polis.pub", firstID),
			Version: first.Version, // pin the exact claim being talked about
		},
		Payload:  map[string]string{"note": "the party named here changed hands"},
		Asserted: "2026-08-29T00:00:00Z",
	}
	secondID, err := Issue(dir, about, priv)
	if err != nil {
		t.Fatalf("an attestation about an attestation must issue: %v", err)
	}

	loaded, err := Load(Path(dir, secondID))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if status, _ := Verify(loaded, pub); status != StatusValid {
		t.Errorf("status = %q, want %q", status, StatusValid)
	}
	if !strings.HasSuffix(loaded.Subject.ID, firstID+".json") {
		t.Errorf("subject %q should address the first record", loaded.Subject.ID)
	}
	if loaded.Subject.Version != first.Version {
		t.Error("the pin must name the exact bytes of the record being talked about")
	}
}

// ---------- identity continuity ----------

// TestSameAsIsAPairNotADoubleSignature is the Done-when box, and the shape is
// the whole argument.
//
// did:web's known hole is that losing the domain loses the identity, and the DID
// spec's `alsoKnownAs` is self-asserted only — whoever hijacks the old domain can
// claim it just as loudly. Two INDEPENDENT records, each signed by a different
// key, each naming the other party, is a countersigned migration with no
// registry and no authority to ask.
//
// ⚠️ Not one record with two signatures. The envelope has one signature slot and
// must keep exactly one.
func TestSameAsIsAPairNotADoubleSignature(t *testing.T) {
	oldPriv, oldPub := newTestKeys(t)
	newPriv, newPub := newTestKeys(t)

	oldSite, newSite := t.TempDir(), t.TempDir()
	writeWellKnown(t, oldSite, oldPub)
	writeWellKnown(t, newSite, newPub)

	const oldURL = "https://vdibart.polis.pub"
	const newURL = "https://vincent.example.com"

	forward := &Record{
		Issuer:    oldURL,
		Predicate: PredicateSameAs,
		Subject:   Subject{Type: SubjectIdentity, ID: newURL},
		Asserted:  "2026-08-28T00:00:00Z",
	}
	forwardID, err := Issue(oldSite, forward, oldPriv)
	if err != nil {
		t.Fatalf("forward: %v", err)
	}

	back := &Record{
		Issuer:    newURL,
		Predicate: PredicateSameAs,
		Subject:   Subject{Type: SubjectIdentity, ID: oldURL},
		Asserted:  "2026-08-28T00:05:00Z",
	}
	backID, err := Issue(newSite, back, newPriv)
	if err != nil {
		t.Fatalf("back: %v", err)
	}

	// Each half verifies against ITS OWN site's published key, independently.
	if status, err := VerifyFile(oldSite, Path(oldSite, forwardID)); status != StatusValid {
		t.Errorf("forward half: status = %q (%v), want %q", status, err, StatusValid)
	}
	if status, err := VerifyFile(newSite, Path(newSite, backID)); status != StatusValid {
		t.Errorf("back half: status = %q (%v), want %q", status, err, StatusValid)
	}

	// And the halves genuinely reference each other.
	f, _ := Load(Path(oldSite, forwardID))
	b, _ := Load(Path(newSite, backID))
	if f.Subject.ID != b.Issuer || b.Subject.ID != f.Issuer {
		t.Error("the pair does not close the loop — each record must name the other party")
	}

	// Half a pair proves nothing on its own: the forward record does NOT verify
	// against the new site's key. That asymmetry is what makes the pair mean
	// something a self-assertion does not.
	if status, _ := VerifyAgainst(f, [][]byte{newPub}); status == StatusValid {
		t.Error("the forward half verified against the wrong key — the pair would be worthless")
	}
}

// ---------- the generator stamp ----------

// TestIssueStampsGeneratorInsideTheSignature is epic 02's E4 as a regression
// test, and the SECOND assertion is the one that matters.
//
// Stamping the generator after signing produces a record that writes, parses,
// and looks right in an editor — and never verifies. Asserting the field alone
// would pass either way; asserting the resulting STATUS is what catches it.
func TestIssueStampsGeneratorInsideTheSignature(t *testing.T) {
	priv, pub := newTestKeys(t)
	dir := t.TempDir()

	old := Version
	Version = "9.9.9"
	defer func() { Version = old }()

	r := sampleRecord()
	r.Generator = "polis-cli-go/0.56.0" // a stale value the writer must replace
	id, err := Issue(dir, r, priv)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	got, err := Load(Path(dir, id))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Generator != "polis-cli-go/9.9.9" {
		t.Errorf("generator = %q, want %q — the writer was not stamped",
			got.Generator, "polis-cli-go/9.9.9")
	}
	status, err := Verify(got, pub)
	if status != StatusValid {
		t.Fatalf("status = %q (%v), want valid — the stamp landed OUTSIDE the signature",
			status, err)
	}
}

func TestGetGenerator(t *testing.T) {
	old := Version
	Version = "0.67.0"
	defer func() { Version = old }()

	if got := GetGenerator(); got != "polis-cli-go/0.67.0" {
		t.Errorf("GetGenerator() = %q, want %q", got, "polis-cli-go/0.67.0")
	}
}

// ---------- identity, addressing, listing ----------

// TestIDIsDerivedAndRecomputable — the id is not stored in the record, so a
// verifier recomputes it. That is what makes a record served at a URL that
// disagrees with its own bytes detectable.
func TestIDIsDerivedAndRecomputable(t *testing.T) {
	priv, _ := newTestKeys(t)
	dir := t.TempDir()

	r := sampleRecord()
	id, err := Issue(dir, r, priv)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	loaded, _ := Load(Path(dir, id))
	if got := ID(loaded); got != id {
		t.Errorf("recomputed id = %q, want %q", got, id)
	}
	if !strings.HasPrefix(id, "20260828T000000Z-") {
		t.Errorf("id = %q, want a compact-timestamp prefix", id)
	}
	if len(strings.TrimPrefix(id, "20260828T000000Z-")) != 16 {
		t.Errorf("id = %q, want 16 hex characters of digest", id)
	}

	raw, _ := os.ReadFile(Path(dir, id))
	if strings.Contains(string(raw), id) {
		t.Errorf("the id %q appears in the record body — it must be computed from the signed "+
			"bytes, never stored beside them", id)
	}
}

func TestRecordURLIsTheContentPath(t *testing.T) {
	got := RecordURL("https://vdibart.polis.pub/", "20260828T000000Z-abcdef0123456789")
	want := "https://vdibart.polis.pub/content/pub.polis.core/attestation/20260828T000000Z-abcdef0123456789.json"
	if got != want {
		t.Errorf("RecordURL = %q, want %q", got, want)
	}
}

// TestListSkipsMalformedRecords — one bad file must not hide every good one.
func TestListSkipsMalformedRecords(t *testing.T) {
	priv, _ := newTestKeys(t)
	dir := t.TempDir()

	if _, err := Issue(dir, sampleRecord(), priv); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := os.WriteFile(filepath.Join(Dir(dir), "broken.json"), []byte("{not json"), 0644); err != nil {
		t.Fatalf("write broken: %v", err)
	}

	records, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("got %d records, want 1 — a malformed file must be skipped, not fatal", len(records))
	}
}

func TestListOnSiteWithNoAttestations(t *testing.T) {
	records, err := List(t.TempDir())
	if err != nil {
		t.Errorf("a site that has issued nothing is not an error: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("got %d records, want 0", len(records))
	}
}

// ---------- verification against the published key ----------

func TestVerifyFileAndSite(t *testing.T) {
	priv, pub := newTestKeys(t)

	t.Run("signed record verifies against the published key", func(t *testing.T) {
		site := t.TempDir()
		writeWellKnown(t, site, pub)
		id, err := Issue(site, sampleRecord(), priv)
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		status, err := VerifyFile(site, Path(site, id))
		if err != nil {
			t.Fatalf("VerifyFile: %v", err)
		}
		if status != StatusValid {
			t.Errorf("status = %q, want %q", status, StatusValid)
		}
	})

	t.Run("signed but no well-known is unknown", func(t *testing.T) {
		site := t.TempDir()
		id, err := Issue(site, sampleRecord(), priv)
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		if status, _ := VerifyFile(site, Path(site, id)); status != StatusUnknown {
			t.Errorf("status = %q, want %q — could not look is not a finding", status, StatusUnknown)
		}
	})

	t.Run("VerifySite reports one status per record", func(t *testing.T) {
		site := t.TempDir()
		writeWellKnown(t, site, pub)
		for _, s := range []string{"https://a.example", "https://b.example"} {
			r := sampleRecord()
			r.Subject = Subject{Type: SubjectIdentity, ID: s}
			if _, err := Issue(site, r, priv); err != nil {
				t.Fatalf("Issue: %v", err)
			}
		}
		statuses, err := VerifySite(site)
		if err != nil {
			t.Fatalf("VerifySite: %v", err)
		}
		if len(statuses) != 2 {
			t.Fatalf("got %d statuses, want 2", len(statuses))
		}
		for id, st := range statuses {
			if st != StatusValid {
				t.Errorf("%s: status = %q, want %q", id, st, StatusValid)
			}
		}
	})

	t.Run("a site with no attestations is not a finding", func(t *testing.T) {
		site := t.TempDir()
		writeWellKnown(t, site, pub)
		statuses, err := VerifySite(site)
		if err != nil {
			t.Errorf("having issued nothing is an ordinary state: %v", err)
		}
		if len(statuses) != 0 {
			t.Errorf("got %d statuses, want 0", len(statuses))
		}
	})
}

// TestIdentityKeysIsTheOnePlaceToWiden pins the shape publishing a key history
// will need. It returns a SLICE today so that widening it is a body change and
// every caller already handles "more than one".
func TestIdentityKeysIsTheOnePlaceToWiden(t *testing.T) {
	_, pub := newTestKeys(t)
	site := t.TempDir()
	writeWellKnown(t, site, pub)

	keys, err := IdentityKeys(site)
	if err != nil {
		t.Fatalf("IdentityKeys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("got %d keys, want 1 — a site publishes one key today", len(keys))
	}
	if string(keys[0]) != string(pub) {
		t.Error("IdentityKeys returned something other than the published key")
	}

	if _, err := IdentityKeys(t.TempDir()); err == nil {
		t.Error("a site with no .well-known/polis should report that it could not be checked")
	}
}

// TestVerifyAgainstTriesEveryKey — the loop a published key history will rely
// on. A record signed by any published key verifies.
func TestVerifyAgainstTriesEveryKey(t *testing.T) {
	priv, pub := newTestKeys(t)
	_, otherPub := newTestKeys(t)
	dir := t.TempDir()

	r := sampleRecord()
	if _, err := Issue(dir, r, priv); err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// The right key second: a rotated site would list the current key first and
	// the retired one after it.
	if status, err := VerifyAgainst(r, [][]byte{otherPub, pub}); status != StatusValid {
		t.Errorf("status = %q (%v), want %q — the second key should have verified it",
			status, err, StatusValid)
	}
	if status, _ := VerifyAgainst(r, nil); status != StatusUnknown {
		t.Errorf("no keys at all should be %q", StatusUnknown)
	}
}

// ---------- withdrawal (D6) ----------

// TestWithdrawAddsARecordAndDeletesNothing is the whole of D6 in one assertion.
// If this ever fails by the target going missing, the durability rule has been
// broken and a 404 has become the way retraction is communicated.
func TestWithdrawAddsARecordAndDeletesNothing(t *testing.T) {
	dir := t.TempDir()
	priv, pub := newTestKeys(t)
	writeWellKnown(t, dir, pub)

	id, err := Issue(dir, &Record{
		Issuer:    "https://alice.example",
		Predicate: PredicateEndorsement,
		Subject:   Subject{Type: SubjectIdentity, ID: "https://bob.example"},
	}, priv)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	wid, err := Withdraw(dir, id, priv)
	if err != nil {
		t.Fatalf("withdraw: %v", err)
	}

	// The retracted record is still there, still parses, still verifies.
	target, err := Load(Path(dir, id))
	if err != nil {
		t.Fatalf("the withdrawn record was destroyed: %v", err)
	}
	if status, err := VerifyRecord(dir, target); status != StatusValid {
		t.Fatalf("the withdrawn record no longer verifies: %v (%v)", status, err)
	}

	w, err := Load(Path(dir, wid))
	if err != nil {
		t.Fatalf("load withdrawal: %v", err)
	}
	if w.Predicate != PredicateWithdrawal {
		t.Errorf("predicate = %q, want %q", w.Predicate, PredicateWithdrawal)
	}
	if w.Subject.Type != SubjectURI {
		t.Errorf("subject type = %q, want %q", w.Subject.Type, SubjectURI)
	}
	if w.Subject.ID != RecordURL("https://alice.example", id) {
		t.Errorf("subject id = %q, want the withdrawn record's URL", w.Subject.ID)
	}
	if w.Subject.Version != target.Version {
		t.Errorf("pin = %q, want the target's version %q", w.Subject.Version, target.Version)
	}
	if status, err := VerifyRecord(dir, w); status != StatusValid {
		t.Fatalf("the withdrawal does not verify: %v (%v)", status, err)
	}
}

// TestWithdrawRefusesAWithdrawal — retracting a retraction says nothing, and
// allowing it invites a chain nobody can interpret.
func TestWithdrawRefusesAWithdrawal(t *testing.T) {
	dir := t.TempDir()
	priv, pub := newTestKeys(t)
	writeWellKnown(t, dir, pub)

	id, _ := Issue(dir, &Record{
		Issuer:    "https://alice.example",
		Predicate: PredicateEndorsement,
		Subject:   Subject{Type: SubjectIdentity, ID: "https://bob.example"},
	}, priv)
	wid, err := Withdraw(dir, id, priv)
	if err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	if _, err := Withdraw(dir, wid, priv); err == nil {
		t.Error("withdrawing a withdrawal was allowed")
	}
}

// TestWithdrawUnknownIDIsAnError — and specifically NOT a silent success, which
// would tell someone their claim was retracted when it was not.
func TestWithdrawUnknownIDIsAnError(t *testing.T) {
	dir := t.TempDir()
	priv, pub := newTestKeys(t)
	writeWellKnown(t, dir, pub)

	if _, err := Withdraw(dir, "20200101T000000Z-deadbeefdeadbeef", priv); err == nil {
		t.Error("withdrawing a record that does not exist reported success")
	}
}

// TestWithdrawRefusesARecordWeDidNotSign — the issuer check, and the reason it
// cannot be a check on the `issuer` FIELD.
//
// A record can sit in this directory without being ours: cached, copied,
// hand-written, or placed by some future feature that stores foreign records
// locally. Its `issuer` field is a string anyone can write, so trusting it
// would let us publish a record CLAIMING to be another party's retraction,
// signed with our key. That never verifies for a reader following the spec, so
// it is not a working impersonation — it is worse in a quieter way: a
// permanently invalid record on our own site that reads as tampering.
func TestWithdrawRefusesARecordWeDidNotSign(t *testing.T) {
	dir := t.TempDir()
	priv, pub := newTestKeys(t)
	writeWellKnown(t, dir, pub)

	// Signed by somebody else entirely, claiming their own issuer.
	otherPriv, _ := newTestKeys(t)
	foreign := &Record{
		Issuer:    "https://bob.example",
		Predicate: PredicateEndorsement,
		Subject:   Subject{Type: SubjectIdentity, ID: "https://carol.example"},
	}
	fid, err := Issue(dir, foreign, otherPriv)
	if err != nil {
		t.Fatalf("seed foreign record: %v", err)
	}

	if _, err := Withdraw(dir, fid, priv); err == nil {
		t.Fatal("withdrew a record this site did not sign")
	}

	// And nothing was written: no record anywhere claims to retract it.
	all, _ := List(dir)
	for _, r := range all {
		if r.Predicate == PredicateWithdrawal {
			t.Errorf("a withdrawal was written anyway: %s (issuer %q)", ID(r), r.Issuer)
		}
	}
}

// TestWithdrawRefusesASecondWithdrawal — one withdrawal per claim.
//
// A second says nothing the first did not, and it is PERMANENT: withdrawals
// cannot themselves be withdrawn, so a duplicate is litter nobody can clear.
// Refusing beats silently succeeding, which would report an action that did not
// happen.
func TestWithdrawRefusesASecondWithdrawal(t *testing.T) {
	dir := t.TempDir()
	priv, pub := newTestKeys(t)
	writeWellKnown(t, dir, pub)

	id, err := Issue(dir, &Record{
		Issuer:    "https://alice.example",
		Predicate: PredicateEndorsement,
		Subject:   Subject{Type: SubjectIdentity, ID: "https://bob.example"},
	}, priv)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := Withdraw(dir, id, priv); err != nil {
		t.Fatalf("first withdraw: %v", err)
	}
	if _, err := Withdraw(dir, id, priv); err == nil {
		t.Fatal("a second withdrawal was allowed")
	}

	withdrawals := 0
	all, _ := List(dir)
	for _, r := range all {
		if r.Predicate == PredicateWithdrawal {
			withdrawals++
		}
	}
	if withdrawals != 1 {
		t.Errorf("withdrawal records = %d, want exactly 1", withdrawals)
	}
}

// TestWithdrawRefusesAnUnsignedRecord — nothing to retract, and the message
// should say that rather than "does not verify", which would suggest tampering.
func TestWithdrawRefusesAnUnsignedRecord(t *testing.T) {
	dir := t.TempDir()
	priv, pub := newTestKeys(t)
	writeWellKnown(t, dir, pub)

	r := &Record{
		Type:      TypeName,
		Issuer:    "https://alice.example",
		Predicate: PredicateEndorsement,
		Subject:   Subject{Type: SubjectIdentity, ID: "https://bob.example"},
		Asserted:  "2026-01-01T00:00:00Z",
		Version:   "sha256:" + strings.Repeat("a", 64),
	}
	if err := write(Path(dir, "20260101T000000Z-aaaaaaaaaaaaaaaa"), r); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := Withdraw(dir, "20260101T000000Z-aaaaaaaaaaaaaaaa", priv)
	if err == nil {
		t.Fatal("withdrew an unsigned record")
	}
	if !strings.Contains(err.Error(), "unsigned") {
		t.Errorf("error = %q, want it to say the record is unsigned", err)
	}
}

// TestKnownBug_PathsHardcodeInsteadOfResolvingTheBundle is a SKIPPED test that
// keeps an open question visible in every test run until epic 06 answers it.
//
// ⚠️ DO NOT DELETE THIS TO MAKE THE OUTPUT TIDY. Turn it into a real assertion
// when epic 06 (record views) lands.
//
// ✅ SETTLED, and no longer the bug the name describes: Dir() and RecordURL()
// use the literal `content/pub.polis.core/attestation`, and that is correct —
// the core bundle's layout is fixed by design, as tag.TagPath's is (settled
// 2026-09-02). The name is kept because plans cite it.
//
// ❓ STILL OPEN: the declared `mount` (/attestations) renders no record view.
// Verified against production 2026-08-29 — /attestations/<id> 404s while the
// raw content path returns 200. Epic 11 now writes agents.json and its page
// under that mount (agent.MountDir), so the mount serves something; what it
// lacks is a rendered record.
func TestKnownBug_PathsHardcodeInsteadOfResolvingTheBundle(t *testing.T) {
	t.Skip("OPEN QUESTION, epic 06 (record views): the declared mount /attestations " +
		"renders no record view (/attestations/<id> 404s; epic 11's agents.json lives under it). " +
		"The literal content path in Dir/RecordURL is SETTLED as correct, like tag.TagPath.")
}
