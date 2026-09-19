package cmd

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
	"github.com/vdibart/polis-cli/cli-go/pkg/sitecheck"
)

// SIGNET epic 44 C4 — `polis attest verify` checked records against the current
// key only, so after a rotation it called a record invalid that `polis validate`
// passed. It now uses the same resolving predicate.
func TestAttestVerifyResolvesARecordSignedBeforeARotation(t *testing.T) {
	const domain = "alice.example"
	gen := func() ([]byte, string) {
		priv, pub, err := signing.GenerateKeypair()
		if err != nil {
			t.Fatal(err)
		}
		return priv, strings.TrimSpace(string(pub))
	}
	k0priv, k0pub := gen()
	_, k1pub := gen()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".well-known"), 0755); err != nil {
		t.Fatal(err)
	}
	wk, _ := json.Marshal(map[string]string{"public_key": k0pub, "created": "2026-03-03T05:35:09Z"})
	if err := os.WriteFile(filepath.Join(dir, ".well-known", "polis"), wk, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := site.WriteGenesisKeyHistory(dir); err != nil {
		t.Fatal(err)
	}

	// The record, signed by the first key before the rotation.
	r := &attestation.Record{
		Type: attestation.TypeName, Issuer: "https://" + domain,
		Predicate: "pub.polis.attestation.endorsement",
		Subject:   attestation.Subject{Type: "identity", ID: "https://bob.example"},
		Asserted:  "2026-05-01T09:00:00Z", Generator: "polis-cli-go/test",
	}
	canonical, err := attestation.CanonicalJSON(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Version = attestation.ContentVersion(canonical)
	if r.Signature, err = signing.SignContent(canonical, k0priv); err != nil {
		t.Fatal(err)
	}

	canon, _ := discovery.MakeKeyRotationCanonicalJSON(domain, k0pub, k1pub, "2026-09-15T10:22:03Z")
	sig, err := signing.SignContent(canon, k0priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := site.RecordKeyRotation(dir, k1pub, sig, "2026-09-15T10:22:03Z"); err != nil {
		t.Fatal(err)
	}

	// Only meaningful if the history is needed: the current key alone rejects it.
	// ⚠️ Signet epic 11 review F1 made attestation.VerifyRecord resolve retired
	// keys too, so this guard checks the current key directly rather than
	// through VerifyRecord, which used to be the current-key-only path.
	if s, _ := attestation.Verify(r, []byte(k1pub)); s != attestation.StatusInvalid {
		t.Fatalf("setup: the current key alone must reject the record, got %s", s)
	}

	t.Setenv("POLIS_BASE_URL", "https://"+domain)
	status, used, verr := newAttestVerifier(dir).verify(r)
	if status != attestation.StatusValid {
		t.Fatalf("attest verify must resolve a record signed before the rotation: %s %v", status, verr)
	}
	if used == nil || used.Source != sitecheck.KeyRetired {
		t.Errorf("the pass must say a RETIRED key verified it, got %+v", used)
	}
}
