package attestation_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// SIGNET epic 44, E5-wide — ⛔ epic 05's blocker.
//
// attestation.Verify checks the issuer's CURRENT key only, so the day an issuer
// rotates, every record it ever signed reads as invalid. Every fixture here
// ACTUALLY ROTATES through site.RecordKeyRotation, the seam all three rotation
// paths call.

const (
	issuerDomain = "judge.polis.pub"
	genesisAt    = "2026-03-03T05:35:09Z"
	rotatedAt    = "2026-09-15T10:22:03Z"
)

type keypair struct {
	priv []byte
	pub  string
}

func newKeypair(t *testing.T) keypair {
	t.Helper()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	return keypair{priv: priv, pub: strings.TrimSpace(string(pub))}
}

// rotatedIssuer lays down an issuer site on k0, rotates it to k1, and returns
// the verified chain a verifier would resolve from.
func rotatedIssuer(t *testing.T, k0, k1 keypair) *site.KeyHistoryBlock {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".well-known"), 0755); err != nil {
		t.Fatal(err)
	}
	wk, _ := json.Marshal(map[string]string{"public_key": k0.pub, "created": genesisAt})
	if err := os.WriteFile(filepath.Join(dir, ".well-known", "polis"), wk, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := site.WriteGenesisKeyHistory(dir); err != nil {
		t.Fatal(err)
	}
	canonical, err := discovery.MakeKeyRotationCanonicalJSON(issuerDomain, k0.pub, k1.pub, rotatedAt)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := signing.SignContent(canonical, k0.priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := site.RecordKeyRotation(dir, k1.pub, sig, rotatedAt); err != nil {
		t.Fatal(err)
	}
	chain, err := site.LoadKeyHistory(dir)
	if err != nil || chain == nil {
		t.Fatalf("no chain: %v", err)
	}
	if err := site.VerifyChain(chain, issuerDomain); err != nil {
		t.Fatalf("setup: the rotated chain must verify: %v", err)
	}
	return chain
}

func signedRecord(t *testing.T, k keypair, asserted string) *attestation.Record {
	t.Helper()
	r := &attestation.Record{
		Type:      attestation.TypeName,
		Issuer:    "https://" + issuerDomain,
		Predicate: "pub.polis.attestation.integrity",
		Subject:   attestation.Subject{Type: attestation.SubjectURI, ID: "https://alice.polis.pub"},
		Asserted:  asserted,
		Generator: "polis-cli-go/test",
	}
	canonical, err := attestation.CanonicalJSON(r)
	if err != nil {
		t.Fatal(err)
	}
	if r.Signature, err = signing.SignContent(canonical, k.priv); err != nil {
		t.Fatal(err)
	}
	return r
}

// ⭐ THE DONE-WHEN: an attestation issued before a rotation still verifies after it.
func TestAnAttestationIssuedBeforeARotationVerifiesAfterIt(t *testing.T) {
	k0, k1 := newKeypair(t), newKeypair(t)
	chain := rotatedIssuer(t, k0, k1)
	old := signedRecord(t, k0, "2026-05-01T09:00:00Z")

	// Only meaningful if the defect is real: the key the issuer publishes NOW
	// rejects the record.
	if s, _ := attestation.Verify(old, []byte(k1.pub)); s != attestation.StatusInvalid {
		t.Fatalf("setup: the current key must reject a record signed before the rotation, got %s", s)
	}

	s, retired, err := attestation.VerifyWithHistory(old, []byte(k1.pub), chain)
	if s != attestation.StatusValid {
		t.Fatalf("a record issued before the rotation does not verify through the history: %s %v", s, err)
	}
	if retired == nil || retired.Epoch != 0 || retired.ValidFrom != genesisAt || retired.ValidUntil != rotatedAt {
		t.Fatalf("the result must name the RETIRED epoch-0 key and its window, got %+v", retired)
	}
}

func TestAnAttestationSignedByTheCurrentKeyIsACurrentKeyPass(t *testing.T) {
	k0, k1 := newKeypair(t), newKeypair(t)
	chain := rotatedIssuer(t, k0, k1)
	s, retired, err := attestation.VerifyWithHistory(signedRecord(t, k1, "2026-09-20T00:00:00Z"), []byte(k1.pub), chain)
	if s != attestation.StatusValid || retired != nil {
		t.Fatalf("a current-key record must pass as a CURRENT-key pass: %s %+v %v", s, retired, err)
	}
}

// The window is the constraint: a retired key does not speak for moments after
// it was retired, whatever the record claims.
func TestAnOldKeyClaimingATimeAfterItsRetirementFails(t *testing.T) {
	k0, k1 := newKeypair(t), newKeypair(t)
	chain := rotatedIssuer(t, k0, k1)
	s, _, err := attestation.VerifyWithHistory(signedRecord(t, k0, "2026-10-01T00:00:00Z"), []byte(k1.pub), chain)
	if s != attestation.StatusInvalid || err == nil || !strings.Contains(err.Error(), "falls inside the current key's window") {
		t.Fatalf("a retired key claiming a time after its retirement must fail and say why: %s %v", s, err)
	}
}

func TestATamperedOldAttestationStillFails(t *testing.T) {
	k0, k1 := newKeypair(t), newKeypair(t)
	chain := rotatedIssuer(t, k0, k1)
	old := signedRecord(t, k0, "2026-05-01T09:00:00Z")
	old.Subject.ID = "https://mallory.polis.pub"
	if s, _, _ := attestation.VerifyWithHistory(old, []byte(k1.pub), chain); s != attestation.StatusInvalid {
		t.Fatalf("a tampered record must not verify through the history: %s", s)
	}
}

func TestWithoutAChainVerifyWithHistoryIsVerify(t *testing.T) {
	k0, k1 := newKeypair(t), newKeypair(t)
	old := signedRecord(t, k0, "2026-05-01T09:00:00Z")
	s, _, _ := attestation.VerifyWithHistory(old, []byte(k1.pub), nil)
	want, _ := attestation.Verify(old, []byte(k1.pub))
	if s != want {
		t.Fatalf("with no chain the two must agree: history=%s current=%s", s, want)
	}
}
