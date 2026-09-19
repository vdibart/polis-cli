package attestation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// Signet epic 11 review F1 — a key rotation must not invalidate the records a
// site signed before it, or block withdrawing them.

// rotateSiteKey performs a real rotation: the transition is signed by the old
// key, the new key is published and written to disk. It returns the new private
// key. validFrom is in the near future so records asserted "now" fall inside
// the retired key's window.
func rotateSiteKey(t *testing.T, dir, domain string) []byte {
	t.Helper()
	oldPriv, err := os.ReadFile(filepath.Join(dir, ".polis", "keys", "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	oldPub, err := os.ReadFile(filepath.Join(dir, ".polis", "keys", "id_ed25519.pub"))
	if err != nil {
		t.Fatal(err)
	}
	newPriv, newPubRaw, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	newPub := strings.TrimSpace(string(newPubRaw))
	validFrom := time.Now().UTC().Add(2 * time.Minute).Format("2006-01-02T15:04:05Z")
	canonical, err := discovery.MakeKeyRotationCanonicalJSON(domain, strings.TrimSpace(string(oldPub)), newPub, validFrom)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := signing.SignContent(canonical, oldPriv)
	if err != nil {
		t.Fatal(err)
	}
	if err := site.RecordKeyRotation(dir, newPub, sig, validFrom); err != nil {
		t.Fatalf("RecordKeyRotation: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".polis", "keys", "id_ed25519"), newPriv, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".polis", "keys", "id_ed25519.pub"), newPubRaw, 0644); err != nil {
		t.Fatal(err)
	}
	return newPriv
}

func TestARecordSignedBeforeARotationStillVerifiesAndCanBeWithdrawn(t *testing.T) {
	dir := t.TempDir()
	const base = "https://alice.polis.pub"
	if _, err := site.Init(dir, site.InitOptions{BaseURL: base, SiteTitle: "alice"}); err != nil {
		t.Fatal(err)
	}
	oldPriv, _ := os.ReadFile(filepath.Join(dir, ".polis", "keys", "id_ed25519"))
	rec := &Record{
		Issuer: base, Predicate: PredicateGrant,
		Subject: Subject{Type: SubjectIdentity, ID: base},
		Payload: map[string]string{
			GrantKeyAgent: "rosie", GrantKeyProvider: "polis.pub",
			GrantKeyBehaviours: "rosie/1", CustodyKeyBasis: CustodyBasisHostingTerms,
		},
	}
	id, err := Issue(dir, rec, oldPriv)
	if err != nil {
		t.Fatal(err)
	}

	newPriv := rotateSiteKey(t, dir, "alice.polis.pub")

	loaded, _ := Load(Path(dir, id))
	status, retired, err := VerifyRecordResolved(dir, loaded)
	if status != StatusValid || retired == nil {
		t.Fatalf("pre-rotation record = %s (retired entry %v, err %v), want valid via the retired key", status, retired, err)
	}
	if g, _ := ResolveLiveGrant(dir, "rosie", "rosie", 1); !g.Valid() {
		t.Fatal("a grant signed before the rotation no longer resolves as live")
	}
	if _, err := Withdraw(dir, id, newPriv); err != nil {
		t.Fatalf("withdrawing a pre-rotation record failed: %v", err)
	}
	if g, _ := ResolveLiveGrant(dir, "rosie", "rosie", 1); g != nil {
		t.Fatal("the withdrawn grant still resolves")
	}
}

// A chain that does not verify is never used: the answer is the current-key one.
func TestAnUntrustedKeyHistoryResolvesNothing(t *testing.T) {
	dir := t.TempDir()
	const base = "https://alice.polis.pub"
	if _, err := site.Init(dir, site.InitOptions{BaseURL: base, SiteTitle: "alice"}); err != nil {
		t.Fatal(err)
	}
	oldPriv, _ := os.ReadFile(filepath.Join(dir, ".polis", "keys", "id_ed25519"))
	rec := &Record{Issuer: base, Predicate: PredicateEndorsement, Subject: Subject{Type: SubjectIdentity, ID: "https://bob.example"}}
	id, err := Issue(dir, rec, oldPriv)
	if err != nil {
		t.Fatal(err)
	}
	// A rotation whose transition signature is junk.
	newPriv, newPubRaw, _ := signing.GenerateKeypair()
	_ = newPriv
	validFrom := time.Now().UTC().Add(2 * time.Minute).Format("2006-01-02T15:04:05Z")
	if err := site.RecordKeyRotation(dir, strings.TrimSpace(string(newPubRaw)), "not-a-signature", validFrom); err != nil {
		t.Fatal(err)
	}
	loaded, _ := Load(Path(dir, id))
	if status, _, _ := VerifyRecordResolved(dir, loaded); status == StatusValid {
		t.Fatal("an untrusted key history resolved a retired key")
	}
}
