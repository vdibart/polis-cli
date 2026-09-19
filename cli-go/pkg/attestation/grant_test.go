package attestation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// Signet epic 11 — the grant: a user's record of what an agent is to her.

const grantSite = "https://alice.example"

func grantFixture(t *testing.T) (string, []byte) {
	t.Helper()
	dir := t.TempDir()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".well-known"), 0755); err != nil {
		t.Fatal(err)
	}
	wk := `{"public_key":` + quoteJSON(strings.TrimSpace(string(pub))) + `}`
	if err := os.WriteFile(filepath.Join(dir, ".well-known", "polis"), []byte(wk), 0644); err != nil {
		t.Fatal(err)
	}
	return dir, priv
}

func quoteJSON(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

func grantRecord(behaviours, basis string) *Record {
	return &Record{
		Issuer:    grantSite,
		Predicate: PredicateGrant,
		Subject:   Subject{Type: SubjectIdentity, ID: grantSite},
		Payload: map[string]string{
			GrantKeyAgent:      "rosie",
			GrantKeyProvider:   "polis.pub",
			GrantKeyBehaviours: behaviours,
			CustodyKeyBasis:    basis,
		},
	}
}

func TestGrantWriterRuleRefusesEveryMissingField(t *testing.T) {
	dir, priv := grantFixture(t)
	for _, key := range []string{GrantKeyAgent, GrantKeyProvider, GrantKeyBehaviours, CustodyKeyBasis} {
		r := grantRecord("rosie/1", CustodyBasisHostingTerms)
		delete(r.Payload, key)
		if _, err := Issue(dir, r, priv); err == nil {
			t.Errorf("a grant without %s was issued — there must be no default", key)
		}
	}
}

func TestGrantWriterRuleRefusesASubjectOtherThanTheIssuer(t *testing.T) {
	dir, priv := grantFixture(t)
	r := grantRecord("rosie/1", CustodyBasisUserSigned)
	r.Subject.ID = "https://bob.example"
	if _, err := Issue(dir, r, priv); err == nil {
		t.Fatal("a grant about someone else's site was issued")
	}
}

func TestGrantWriterRuleRefusesMalformedValues(t *testing.T) {
	dir, priv := grantFixture(t)
	cases := map[string]func(*Record){
		"unversioned behaviours": func(r *Record) { r.Payload[GrantKeyBehaviours] = "rosie" },
		"version zero":           func(r *Record) { r.Payload[GrantKeyBehaviours] = "rosie/0" },
		"unknown basis":          func(r *Record) { r.Payload[CustodyKeyBasis] = "implied" },
		"http terms":             func(r *Record) { r.Payload[CustodyKeyTerms] = "http://polis.pub/terms" },
		"uri subject":            func(r *Record) { r.Subject.Type = SubjectURI },
	}
	for name, mutate := range cases {
		r := grantRecord("rosie/1", CustodyBasisHostingTerms)
		mutate(r)
		if _, err := Issue(dir, r, priv); err == nil {
			t.Errorf("%s: issued", name)
		}
	}
}

func TestLiveGrantIsTheHighestStandingVersion(t *testing.T) {
	dir, priv := grantFixture(t)
	if _, err := Issue(dir, grantRecord("rosie/1", CustodyBasisHostingTerms), priv); err != nil {
		t.Fatal(err)
	}
	r2 := grantRecord("rosie/2", CustodyBasisHostingTerms)
	r2.Asserted = "2030-01-01T00:00:00Z"
	id2, err := Issue(dir, r2, priv)
	if err != nil {
		t.Fatal(err)
	}

	g, err := ResolveLiveGrant(dir, "rosie", "rosie", 1)
	if err != nil || !g.Valid() {
		t.Fatalf("no live grant: %v", err)
	}
	if g.URL() != RecordURL(grantSite, id2) || g.Behaviours() != "rosie/2" {
		t.Errorf("live grant = %s %s, want the rosie/2 record", g.URL(), g.Behaviours())
	}
	if g2, _ := ResolveLiveGrant(dir, "rosie", "rosie", 3); g2 != nil {
		t.Errorf("a grant below the minimum version resolved: %s", g2.Behaviours())
	}
	if g3, _ := ResolveLiveGrant(dir, "someone-else", "rosie", 1); g3 != nil {
		t.Error("a grant for another agent resolved")
	}
}

func TestLiveGrantFailsClosedOnWithdrawal(t *testing.T) {
	dir, priv := grantFixture(t)
	id, err := Issue(dir, grantRecord("rosie/1", CustodyBasisUserSigned), priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Withdraw(dir, id, priv); err != nil {
		t.Fatal(err)
	}
	if g, _ := ResolveLiveGrant(dir, "rosie", "rosie", 1); g != nil {
		t.Fatal("a withdrawn grant resolved as live")
	}

	// The withdrawal record alone is enough — even with the forward pointer
	// stripped by some writer that did not know the field.
	rec, _ := Load(Path(dir, id))
	rec.WithdrawnBy = ""
	if err := write(Path(dir, id), rec); err != nil {
		t.Fatal(err)
	}
	if g, _ := ResolveLiveGrant(dir, "rosie", "rosie", 1); g != nil {
		t.Fatal("a grant with its pointer stripped resolved as live despite the withdrawal on disk")
	}
}

func TestLiveGrantFailsClosedOnADanglingPointer(t *testing.T) {
	dir, priv := grantFixture(t)
	id, err := Issue(dir, grantRecord("rosie/1", CustodyBasisUserSigned), priv)
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := Load(Path(dir, id))
	rec.WithdrawnBy = grantSite + "/content/pub.polis.core/attestation/missing.json"
	if err := write(Path(dir, id), rec); err != nil {
		t.Fatal(err)
	}
	if g, _ := ResolveLiveGrant(dir, "rosie", "rosie", 1); g != nil {
		t.Fatal("a grant pointing at a withdrawal resolved as live")
	}
}

func TestLiveGrantRequiresAVerifyingSignature(t *testing.T) {
	dir, priv := grantFixture(t)
	id, err := Issue(dir, grantRecord("rosie/1", CustodyBasisUserSigned), priv)
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := Load(Path(dir, id))
	rec.Payload[GrantKeyBehaviours] = "rosie/9" // tampered after signing
	if err := write(Path(dir, id), rec); err != nil {
		t.Fatal(err)
	}
	if g, _ := ResolveLiveGrant(dir, "rosie", "rosie", 1); g != nil {
		t.Fatal("a grant that does not verify resolved as live")
	}
}

func TestAZeroLiveGrantIsNotValid(t *testing.T) {
	var nilGrant *LiveGrant
	if nilGrant.Valid() || (&LiveGrant{}).Valid() {
		t.Fatal("a nil or zero LiveGrant reports valid")
	}
}

func TestNoGrantsIsNotAnError(t *testing.T) {
	dir, _ := grantFixture(t)
	gs, err := Grants(dir, "rosie")
	if err != nil || len(gs) != 0 {
		t.Fatalf("Grants on an empty site = %v, %v", gs, err)
	}
}
