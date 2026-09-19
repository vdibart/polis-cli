package actor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

func siteWithPublishedKey(t *testing.T, pub []byte) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".well-known"), 0755)
	wk, _ := json.Marshal(map[string]string{
		"version": "2.0", "public_key": strings.TrimSpace(string(pub)),
		"author_name": "judge", "created": "2026-09-05T00:00:00Z",
	})
	os.WriteFile(filepath.Join(dir, ".well-known", "polis"), wk, 0644)
	return dir
}

func TestProjectKey_RestoresAnAbsentKey(t *testing.T) {
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	dir := siteWithPublishedKey(t, pub)

	outcome, err := ProjectKey(dir, string(priv))
	if err != nil {
		t.Fatalf("ProjectKey: %v", err)
	}
	if outcome != ProjectionWritten {
		t.Errorf("outcome = %q, want written", outcome)
	}

	keyPath := filepath.Join(dir, ".polis", "keys", "id_ed25519")
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	// ⛔ Patrol checks key-file permissions on every sweep; 0644 turns the
	// fleet red on the first one.
	if info.Mode().Perm() != 0600 {
		t.Errorf("mode = %04o, want 0600", info.Mode().Perm())
	}

	// The public half too, or Patrol's key-match check has nothing to compare.
	got, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		t.Fatalf("no public key written: %v", err)
	}
	if !sameSSHKey(string(got), string(pub)) {
		t.Errorf("projected public key differs from the generated one:\n%s\n%s", got, pub)
	}
}

// TestProjectKey_NeverOverwrites — the single most important property. Rotation
// signs the handover with the OLD key before destroying it, so a projection
// that overwrote would destroy an identity with no possible repair: a chain is
// append-only and is never rebuilt.
func TestProjectKey_NeverOverwrites(t *testing.T) {
	oldPriv, oldPub, _ := signing.GenerateKeypair()
	newPriv, _, _ := signing.GenerateKeypair()
	dir := siteWithPublishedKey(t, oldPub)

	if _, err := ProjectKey(dir, string(oldPriv)); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, ".polis", "keys", "id_ed25519")
	before, _ := os.ReadFile(keyPath)
	beforeInfo, _ := os.Stat(keyPath)

	// A different secret arrives — the "fly secrets set" gesture.
	outcome, err := ProjectKey(dir, string(newPriv))
	if err != nil {
		t.Fatalf("ProjectKey: %v", err)
	}
	if outcome != ProjectionPresent {
		t.Errorf("outcome = %q, want present", outcome)
	}

	after, _ := os.ReadFile(keyPath)
	if string(before) != string(after) {
		t.Fatal("the projection overwrote an existing private key")
	}

	// ⭐ And the file is not merely unchanged in content — it is not REWRITTEN,
	// which is what keeps Patrol's mtime alert quiet.
	afterInfo, _ := os.Stat(keyPath)
	if !beforeInfo.ModTime().Equal(afterInfo.ModTime()) {
		t.Error("the key file was rewritten with identical bytes — its mtime moved and Patrol will alert")
	}
}

// TestProjectKey_AbsentSecretIsFatalAndNeverGenerates — the failure that
// silently replaces an identity.
func TestProjectKey_AbsentSecretIsFatalAndNeverGenerates(t *testing.T) {
	_, pub, _ := signing.GenerateKeypair()
	dir := siteWithPublishedKey(t, pub)

	for _, secret := range []string{"", "   ", "\n"} {
		if _, err := ProjectKey(dir, secret); err == nil {
			t.Fatalf("an empty secret (%q) was accepted", secret)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".polis", "keys", "id_ed25519")); !os.IsNotExist(err) {
		t.Fatal("a key file exists — the projection generated an identity")
	}
}

// TestProjectKey_StaleSecretIsRefused is the worker task's fifth edge case, and
// the answer is NOT the one the plan assumed.
//
// ⛔ THE KEY-HISTORY CHAIN DOES NOT CATCH THIS. Judge's chain-head check
// compares public_key_history's head against .well-known/polis's public_key —
// two fields in the SAME published document, neither of which is the key file.
// After a rotation both are correct and stay correct while a stale secret
// restores the RETIRED private key. What would eventually notice is a signature
// failing to verify, i.e. after the actor has already published under a key
// nobody accepts.
//
// So the comparison happens at projection time, against what the site itself
// publishes, before anything is written.
func TestProjectKey_StaleSecretIsRefused(t *testing.T) {
	stalePriv, _, _ := signing.GenerateKeypair()
	_, currentPub, _ := signing.GenerateKeypair()
	dir := siteWithPublishedKey(t, currentPub) // the site rotated; the secret did not

	_, err := ProjectKey(dir, string(stalePriv))
	if err == nil {
		t.Fatal("a retired key was projected over a rotated identity")
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Errorf("the error should name the cause a human can act on, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".polis", "keys", "id_ed25519")); !os.IsNotExist(err) {
		t.Fatal("the retired key was written anyway")
	}
}

// TestProjectKey_MalformedSecretIsFatal — a secret store round-trip that
// mangled the value must not produce a site that half-boots.
func TestProjectKey_MalformedSecretIsFatal(t *testing.T) {
	_, pub, _ := signing.GenerateKeypair()
	dir := siteWithPublishedKey(t, pub)

	if _, err := ProjectKey(dir, "not a private key"); err == nil {
		t.Fatal("a malformed secret was accepted")
	}
}

// TestProjectKey_ToleratesAMissingTrailingNewline — secret stores round-trip
// values without one more often than not, and the PEM parser wants one.
func TestProjectKey_ToleratesAMissingTrailingNewline(t *testing.T) {
	priv, pub, _ := signing.GenerateKeypair()
	dir := siteWithPublishedKey(t, pub)

	if _, err := ProjectKey(dir, strings.TrimRight(string(priv), "\n")); err != nil {
		t.Fatalf("a secret without a trailing newline was rejected: %v", err)
	}
}

// TestProjectKey_BootstrapWithNoWellKnown — the very first boot of a brand new
// actor site, before anything is published. There is nothing to compare
// against, so the projection proceeds.
func TestProjectKey_BootstrapWithNoWellKnown(t *testing.T) {
	priv, _, _ := signing.GenerateKeypair()
	dir := t.TempDir()

	outcome, err := ProjectKey(dir, string(priv))
	if err != nil {
		t.Fatalf("ProjectKey: %v", err)
	}
	if outcome != ProjectionWritten {
		t.Errorf("outcome = %q", outcome)
	}
}
