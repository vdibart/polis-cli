package sitecheck

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// rotatedIssuerWellKnown returns the .well-known/polis of issuer.example after
// one real rotation (site.RecordKeyRotation), and both private keys.
func rotatedIssuerWellKnown(t *testing.T) (wk []byte, oldKey, newKey []byte) {
	t.Helper()
	k0, p0, _ := signing.GenerateKeypair()
	k1, p1, _ := signing.GenerateKeypair()
	pub0, pub1 := strings.TrimSpace(string(p0)), strings.TrimSpace(string(p1))
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".well-known"), 0755)
	body, _ := json.Marshal(map[string]string{"public_key": pub0, "created": "2026-03-03T05:35:09Z"})
	os.WriteFile(filepath.Join(dir, ".well-known", "polis"), body, 0644)
	if _, err := site.WriteGenesisKeyHistory(dir); err != nil {
		t.Fatal(err)
	}
	const rotatedAt = "2026-09-15T10:22:03Z"
	canonical, _ := discovery.MakeKeyRotationCanonicalJSON("issuer.example", pub0, pub1, rotatedAt)
	sig, _ := signing.SignContent(canonical, k0)
	if err := site.RecordKeyRotation(dir, pub1, sig, rotatedAt); err != nil {
		t.Fatal(err)
	}
	wk, err := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))
	if err != nil {
		t.Fatal(err)
	}
	return wk, k0, k1
}

func issuedBy(t *testing.T, key []byte, asserted string) *attestation.Record {
	t.Helper()
	r := &attestation.Record{
		Type:      attestation.TypeName,
		Issuer:    "https://issuer.example",
		Predicate: attestation.PredicateIntegrity,
		Subject:   attestation.Subject{Type: attestation.SubjectIdentity, ID: "https://alice.example"},
		Asserted:  asserted,
		Generator: "polis-cli-go/test",
	}
	c, err := attestation.CanonicalJSON(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Version = attestation.ContentVersion(c)
	if r.Signature, err = signing.SignContent(c, key); err != nil {
		t.Fatal(err)
	}
	return r
}

func serve(files map[string][]byte) IssuerFetch {
	return func(u string) ([]byte, error) {
		if b, ok := files[u]; ok {
			return b, nil
		}
		return nil, errors.New("HTTP 404")
	}
}

// Close-out F4c: a record signed before its issuer rotated verifies against
// the ISSUER's history, and says it was a retired key; one the retired key
// signs after the rotation does not.
func TestVerifyIssuedAttestation_RotatedIssuer(t *testing.T) {
	wk, oldKey, newKey := rotatedIssuerWellKnown(t)
	fetch := serve(map[string][]byte{"https://issuer.example/.well-known/polis": wk})

	v := VerifyIssuedAttestation(issuedBy(t, oldKey, "2026-09-13T00:00:00Z"), fetch)
	if v.Status != attestation.StatusValid || v.KeyUsed == nil || v.KeyUsed.Source != KeyRetired {
		t.Fatalf("pre-rotation record: %+v, want valid by a retired key", v)
	}
	if v := VerifyIssuedAttestation(issuedBy(t, newKey, "2026-09-16T00:00:00Z"), fetch); v.Status != attestation.StatusValid || v.KeyUsed.Source != KeyCurrent {
		t.Fatalf("current-key record: %+v", v)
	}
	if v := VerifyIssuedAttestation(issuedBy(t, oldKey, "2026-09-16T00:00:00Z"), fetch); v.Status != attestation.StatusInvalid {
		t.Fatalf("retired key signing after its window: %+v, want invalid", v)
	}
}

// Could not look is never looked-and-found: no issuer, a non-https issuer, and
// an unreachable one are all unknown.
func TestVerifyIssuedAttestation_CouldNotLookIsUnknown(t *testing.T) {
	_, k, _ := rotatedIssuerWellKnown(t)
	r := issuedBy(t, k, "2026-09-13T00:00:00Z")
	if v := VerifyIssuedAttestation(r, serve(nil)); v.Status != attestation.StatusUnknown {
		t.Errorf("unreachable issuer: %+v", v)
	}
	r.Issuer = "http://issuer.example"
	if v := VerifyIssuedAttestation(r, serve(nil)); v.Status != attestation.StatusUnknown || !strings.Contains(v.Err.Error(), "https") {
		t.Errorf("http issuer: %+v", v)
	}
	r.Signature = ""
	if v := VerifyIssuedAttestation(r, serve(nil)); v.Status != attestation.StatusUnsigned {
		t.Errorf("unsigned: %+v", v)
	}
}
