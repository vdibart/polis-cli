package sitecheck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/did"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

type kp struct {
	priv []byte
	pub  string
}

func genKey(t *testing.T) kp {
	t.Helper()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	return kp{priv: priv, pub: strings.TrimSpace(string(pub))}
}

// khSite lays down a site with a genesis chain and, optionally, a DID document.
func khSite(t *testing.T, k kp, withDID bool) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".well-known"), 0755); err != nil {
		t.Fatal(err)
	}
	wk := map[string]interface{}{
		"version": "test", "author_name": "Alice",
		"public_key": k.pub, "created": "2026-03-03T05:35:09Z",
	}
	data, _ := json.MarshalIndent(wk, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, ".well-known", "polis"), append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := site.WriteGenesisKeyHistory(dir); err != nil {
		t.Fatal(err)
	}
	if withDID {
		if err := site.PublishDIDDocument(dir, "alice.polis.pub"); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func khRotate(t *testing.T, dir, domain string, from, to kp, ts string) {
	t.Helper()
	canonical, err := discovery.MakeKeyRotationCanonicalJSON(domain, from.pub, to.pub, ts)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := signing.SignContent(canonical, from.priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := site.RecordKeyRotation(dir, to.pub, sig, ts); err != nil {
		t.Fatal(err)
	}
}

func loadChain(t *testing.T, dir string) *site.KeyHistoryBlock {
	t.Helper()
	b, err := site.LoadKeyHistory(dir)
	if err != nil || b == nil {
		t.Fatalf("no chain: %v", err)
	}
	return b
}

// ---------- ChainValidity ----------

func TestChainValidityDistinguishesGenesisOnlyFromVerifiedRotations(t *testing.T) {
	// A pass must SAY WHAT IT CHECKED. "genesis only" and "3 keys verified"
	// are different facts, and a reader of a clean report needs to know which
	// one they got.
	k0, k1 := genKey(t), genKey(t)
	dir := khSite(t, k0, false)

	st := ChainValidity(loadChain(t, dir), "alice.polis.pub")
	if !st.OK || !strings.Contains(st.Message, "genesis only") {
		t.Errorf("genesis-only chain: %+v", st)
	}

	khRotate(t, dir, "alice.polis.pub", k0, k1, "2026-09-15T10:22:03Z")
	st = ChainValidity(loadChain(t, dir), "alice.polis.pub")
	if !st.OK || !strings.Contains(st.Message, "2 key(s)") {
		t.Errorf("after one rotation: %+v", st)
	}
}

func TestChainValidityFailsARotationItCannotRebuild(t *testing.T) {
	// The domain is inside the transition signature. Without it a chain with
	// rotations must FAIL rather than pass — "could not check" must never look
	// like "checked and fine".
	k0, k1 := genKey(t), genKey(t)
	dir := khSite(t, k0, false)
	khRotate(t, dir, "alice.polis.pub", k0, k1, "2026-09-15T10:22:03Z")

	if st := ChainValidity(loadChain(t, dir), ""); st.OK {
		t.Error("a chain with rotations passed without a domain to rebuild the signature over")
	}
}

func TestChainValidityTreatsNoChainAsNotADefect(t *testing.T) {
	// Most sites published none until this shipped. Absence is honest.
	if st := ChainValidity(nil, "alice.polis.pub"); !st.OK {
		t.Errorf("an absent chain must not be a failure: %+v", st)
	}
}

// ---------- ChainHead ----------

func TestChainHeadCatchesAPublishedKeyTheChainDoesNotKnow(t *testing.T) {
	// The exact failure a client that rotates without maintaining the chain
	// produces — and the one that makes every artifact signed under the new key
	// unverifiable against the site's own history.
	k0, k1 := genKey(t), genKey(t)
	dir := khSite(t, k0, false)
	block := loadChain(t, dir)

	if st := ChainHead(block, k0.pub, nil); !st.OK {
		t.Fatalf("an agreeing head must pass: %+v", st)
	}
	st := ChainHead(block, k1.pub, nil)
	if st.OK {
		t.Fatal("public_key and the chain head are different keys and the check passed")
	}
	// The message must not suggest a repair. Which key is your identity is not
	// a question an actor may answer.
	if !strings.Contains(st.Message, "not a question an actor may answer") {
		t.Errorf("the message must refuse to pick a winner, got %q", st.Message)
	}
}

func TestChainHeadFollowsAssertionMethodRatherThanTheFirstKey(t *testing.T) {
	// ⚠️ Since the DID document carries retired keys, "the first
	// verificationMethod" and "the authoritative key" are no longer the same
	// thing. A check that took the first entry would pass a document whose
	// assertionMethod named a RETIRED key.
	k0, k1 := genKey(t), genKey(t)
	dir := khSite(t, k0, true)
	khRotate(t, dir, "alice.polis.pub", k0, k1, "2026-09-15T10:22:03Z")
	if err := site.PublishDIDDocument(dir, "alice.polis.pub"); err != nil {
		t.Fatal(err)
	}
	block := loadChain(t, dir)
	didDoc, err := os.ReadFile(site.DIDDocumentPath(dir))
	if err != nil {
		t.Fatal(err)
	}

	if st := ChainHead(block, k1.pub, didDoc); !st.OK {
		t.Fatalf("a correctly projected document must pass: %+v", st)
	}

	// Now point assertionMethod at the RETIRED key, leaving both keys present.
	var doc did.Document
	if err := json.Unmarshal(didDoc, &doc); err != nil {
		t.Fatal(err)
	}
	doc.AssertionMethod = []string{"did:web:alice.polis.pub#key-1"}
	tampered, _ := json.Marshal(doc)

	st := ChainHead(block, k1.pub, tampered)
	if st.OK {
		t.Fatal("assertionMethod names a retired key and the check passed — a resolver would treat it as authoritative")
	}
	if !strings.Contains(st.Message, "assertionMethod") {
		t.Errorf("the message must name what disagreed, got %q", st.Message)
	}
}

func TestChainHeadReportsADIDDocumentPointingAtNothing(t *testing.T) {
	k0 := genKey(t)
	dir := khSite(t, k0, true)
	block := loadChain(t, dir)

	broken := []byte(`{"id":"did:web:alice.polis.pub","verificationMethod":[],"assertionMethod":["did:web:alice.polis.pub#key-9"]}`)
	if st := ChainHead(block, k0.pub, broken); st.OK {
		t.Error("a document whose assertionMethod points at a method it does not contain must not pass")
	}
}

func TestChainHeadIgnoresAnAbsentDIDDocument(t *testing.T) {
	// Most sites publish none, and absence is honest.
	k0 := genKey(t)
	dir := khSite(t, k0, false)
	if st := ChainHead(loadChain(t, dir), k0.pub, nil); !st.OK {
		t.Errorf("no did.json must not be a finding: %+v", st)
	}
}

// ---------- domain resolution ----------

func TestDomainComesFromTheSitesOwnDIDDocument(t *testing.T) {
	// .well-known/polis has no host field, so this is the only site-AUTHORED
	// statement of where the site lives. It works on a clone of somebody else's
	// site, where our own environment would name the wrong host.
	k0 := genKey(t)
	dir := khSite(t, k0, true)
	doc, err := os.ReadFile(site.DIDDocumentPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if got := DomainFromDIDDocument(doc); got != "alice.polis.pub" {
		t.Errorf("DomainFromDIDDocument = %q, want alice.polis.pub", got)
	}
	if got := DomainFromDIDDocument(nil); got != "" {
		t.Errorf("no document must yield no domain, got %q", got)
	}
	if got := DomainFromDIDDocument([]byte(`{"id":"https://alice.polis.pub"}`)); got != "" {
		t.Errorf("a non-did:web id must yield no domain, got %q", got)
	}
}

// ---------- the report ----------

func TestValidateReportsThatDSParityDidNotRun(t *testing.T) {
	// ⛔ D8. A published chain proves it is internally consistent and proves
	// nothing about whether it is COMPLETE. Silence here would let a clean
	// report be read as "my history was checked against the record".
	k0 := genKey(t)
	dir := khSite(t, k0, true)

	r := RunLocal(dir)
	witness := findCheck(t, r, "identity.key_history_witness")
	if witness.Outcome != OutcomeNotApplicable {
		t.Fatalf("the DS-parity gap must be not-applicable, never passed: %+v", witness)
	}
	if !strings.Contains(witness.Reason, "omitted") || !strings.Contains(witness.Reason, "backdated") {
		t.Errorf("the reason must name what goes unchecked, got %q", witness.Reason)
	}

	// And the checks that DID run must have run.
	if c := findCheck(t, r, "identity.key_history"); c.Outcome != OutcomePassed {
		t.Errorf("chain validity should have passed: %+v", c)
	}
	if c := findCheck(t, r, "identity.key_history_head"); c.Outcome != OutcomePassed {
		t.Errorf("head agreement should have passed: %+v", c)
	}
}

func TestValidateReportsASiteWithNoChainAsUnchecked(t *testing.T) {
	// Not-applicable, never passed: a site publishing no history has not been
	// found healthy, it has been found silent.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".well-known"), 0755); err != nil {
		t.Fatal(err)
	}
	k0 := genKey(t)
	wk := map[string]interface{}{"public_key": k0.pub, "created": "2026-03-03T05:35:09Z", "author_name": "A"}
	data, _ := json.MarshalIndent(wk, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, ".well-known", "polis"), data, 0644); err != nil {
		t.Fatal(err)
	}

	r := RunLocal(dir)
	c := findCheck(t, r, "identity.key_history")
	if c.Outcome != OutcomeNotApplicable {
		t.Fatalf("no chain must be not-applicable, got %+v", c)
	}
	if !strings.Contains(c.Reason, "rotation") {
		t.Errorf("the reason should say what the site is exposed to, got %q", c.Reason)
	}
}

func TestValidateFailsATamperedChain(t *testing.T) {
	k0, k1 := genKey(t), genKey(t)
	dir := khSite(t, k0, true)
	khRotate(t, dir, "alice.polis.pub", k0, k1, "2026-09-15T10:22:03Z")
	if err := site.PublishDIDDocument(dir, "alice.polis.pub"); err != nil {
		t.Fatal(err)
	}

	// Backdate the rotation. valid_from is inside the signature.
	raw, err := site.LoadWellKnownRaw(dir)
	if err != nil {
		t.Fatal(err)
	}
	block := loadChain(t, dir)
	block.Current.ValidFrom = "2026-01-01T00:00:00Z"
	block.History[0].ValidUntil = "2026-01-01T00:00:00Z"
	raw["public_key_history"] = block
	if err := site.SaveWellKnownRaw(dir, raw, site.ChangeKeyHistory); err != nil {
		t.Fatal(err)
	}

	r := RunLocal(dir)
	if c := findCheck(t, r, "identity.key_history"); c.Outcome != OutcomeFailed {
		t.Fatalf("a backdated chain must FAIL, got %+v", c)
	}
	if r.OK() {
		t.Error("a report containing a broken key chain must not be OK")
	}
}

func findCheck(t *testing.T, r *Report, id string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("report has no check %q", id)
	return Check{}
}
