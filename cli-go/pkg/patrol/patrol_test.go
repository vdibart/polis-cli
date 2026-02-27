package patrol

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// setupValidTenant creates a minimal valid tenant site in a temp directory.
// Returns (siteDir, publicKeySSH, privateKeyPEM).
func setupValidTenant(t *testing.T, name string) (string, []byte, []byte) {
	t.Helper()

	base := t.TempDir()
	siteDir := filepath.Join(base, name)

	// Generate keypair
	privKey, pubKey, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	// Create directories
	for _, dir := range []string{
		".well-known",
		filepath.Join(".polis", "keys"),
		"posts",
	} {
		if err := os.MkdirAll(filepath.Join(siteDir, dir), 0755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}

	// Write .well-known/polis
	wk := map[string]interface{}{
		"public_key": strings.TrimSpace(string(pubKey)),
	}
	wkData, _ := json.MarshalIndent(wk, "", "  ")
	if err := os.WriteFile(filepath.Join(siteDir, ".well-known", "polis"), wkData, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Write key files
	if err := os.WriteFile(filepath.Join(siteDir, ".polis", "keys", "id_ed25519"), privKey, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(siteDir, ".polis", "keys", "id_ed25519.pub"), pubKey, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	return siteDir, pubKey, privKey
}

// createSignedPost creates a signed post in the site directory and returns the relative path.
func createSignedPost(t *testing.T, siteDir string, privKey, pubKey []byte, title, body string) string {
	t.Helper()

	// Canonicalize body
	canonical := canonicalizeContent(body)
	hash := sha256Hex([]byte(canonical))
	timestamp := "2026-01-15T10:00:00Z"

	// Build unsigned frontmatter (without signature line)
	unsignedFM := fmt.Sprintf("---\ntitle: %s\npublished: %s\ngenerator: patrol-test\ncurrent-version: sha256:%s\nversion-history:\n  - sha256:%s (%s)\n---", title, timestamp, hash, hash, timestamp)

	// Build content to sign (everything that would appear before signature: line)
	fullUnsigned := unsignedFM + "\n\n" + canonical
	canonicalForSigning := canonicalizeContent(fullUnsigned)

	// Sign
	sig, err := signing.SignContent([]byte(canonicalForSigning), privKey)
	if err != nil {
		t.Fatalf("SignContent: %v", err)
	}

	// Extract base64 signature (strip PEM headers)
	sigBase64 := extractSigBase64(sig)

	// Build final content with signature in frontmatter
	finalFM := fmt.Sprintf("---\ntitle: %s\npublished: %s\ngenerator: patrol-test\ncurrent-version: sha256:%s\nversion-history:\n  - sha256:%s (%s)\nsignature: %s\n---", title, timestamp, hash, hash, timestamp, sigBase64)
	finalContent := finalFM + "\n\n" + canonical

	// Write to posts directory
	dateDir := "20260115"
	postsDir := filepath.Join(siteDir, "posts", dateDir)
	if err := os.MkdirAll(postsDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	slug := strings.ToLower(strings.ReplaceAll(title, " ", "-"))
	postPath := filepath.Join(postsDir, slug+".md")
	if err := os.WriteFile(postPath, []byte(finalContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	return filepath.Join("posts", dateDir, slug+".md")
}

// extractSigBase64 strips PEM headers from an SSH signature.
func extractSigBase64(sig string) string {
	lines := strings.Split(sig, "\n")
	var b64Lines []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-----") {
			continue
		}
		b64Lines = append(b64Lines, line)
	}
	return strings.Join(b64Lines, "")
}

func TestCheckTenant_Valid(t *testing.T) {
	siteDir, pubKey, privKey := setupValidTenant(t, "alice")
	createSignedPost(t, siteDir, privKey, pubKey, "Hello World", "# Hello World\n\nThis is my first post.\n")

	result := CheckTenant(siteDir)

	if !result.WellKnown.OK {
		t.Errorf("WellKnown should be OK: %s", result.WellKnown.Message)
	}
	if !result.PublicKey.OK {
		t.Errorf("PublicKey should be OK: %s", result.PublicKey.Message)
	}
	if !result.PrivateKey.OK {
		t.Errorf("PrivateKey should be OK: %s", result.PrivateKey.Message)
	}
	if !result.KeyMatch.OK {
		t.Errorf("KeyMatch should be OK: %s", result.KeyMatch.Message)
	}
	if !result.KeyLeak.OK {
		t.Errorf("KeyLeak should be OK: %s", result.KeyLeak.Message)
	}
	if result.PostCount != 1 {
		t.Errorf("expected PostCount=1, got %d", result.PostCount)
	}
	if result.PostsVerified != 1 {
		t.Errorf("expected PostsVerified=1, got %d", result.PostsVerified)
	}
	if result.PostsFailed != 0 {
		t.Errorf("expected PostsFailed=0, got %d", result.PostsFailed)
	}
	if !result.Passed() {
		t.Errorf("expected Passed()=true, got false")
	}
}

func TestCheckTenant_MissingWellKnown(t *testing.T) {
	base := t.TempDir()
	siteDir := filepath.Join(base, "bob")
	os.MkdirAll(siteDir, 0755)

	result := CheckTenant(siteDir)

	if result.WellKnown.OK {
		t.Error("WellKnown should fail for missing file")
	}
	if result.Passed() {
		t.Error("should not pass with missing well-known")
	}
}

func TestCheckTenant_KeyMismatch(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "carol")

	// Generate a different keypair and overwrite the pub key file
	_, otherPub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	pubKeyPath := filepath.Join(siteDir, ".polis", "keys", "id_ed25519.pub")
	os.WriteFile(pubKeyPath, otherPub, 0644)

	result := CheckTenant(siteDir)

	if !result.WellKnown.OK {
		t.Error("WellKnown should be OK")
	}
	if !result.PublicKey.OK {
		t.Error("PublicKey should be OK (file is valid, just different)")
	}
	if result.KeyMatch.OK {
		t.Error("KeyMatch should fail for mismatched keys")
	}
}

func TestCheckTenant_TamperedPost(t *testing.T) {
	siteDir, pubKey, privKey := setupValidTenant(t, "dave")
	relPath := createSignedPost(t, siteDir, privKey, pubKey, "Tampered Post", "# Original Content\n\nThis is the original.\n")

	// Tamper with the post body (after signature)
	fullPath := filepath.Join(siteDir, relPath)
	content, _ := os.ReadFile(fullPath)
	tampered := strings.Replace(string(content), "This is the original.", "This has been TAMPERED.", 1)
	os.WriteFile(fullPath, []byte(tampered), 0644)

	result := CheckTenant(siteDir)

	if result.PostsFailed != 1 {
		t.Errorf("expected PostsFailed=1, got %d", result.PostsFailed)
	}
	if result.Passed() {
		t.Error("should not pass with tampered post")
	}
	if len(result.PostErrors) == 0 {
		t.Error("expected post errors")
	}
}

func TestCheckTenant_NoPosts(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "eve")

	result := CheckTenant(siteDir)

	if !result.Passed() {
		t.Errorf("should pass with valid keys and no posts: %+v", result)
	}
	if result.PostCount != 0 {
		t.Errorf("expected PostCount=0, got %d", result.PostCount)
	}
}

func TestCheckTenant_KeyLeak(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "frank")

	// Plant a private key header in a post file
	postsDir := filepath.Join(siteDir, "posts", "20260115")
	os.MkdirAll(postsDir, 0755)
	leakedContent := "---\ntitle: Leaked Key\n---\n\nOops:\n-----BEGIN OPENSSH PRIVATE KEY-----\nfakedata\n-----END OPENSSH PRIVATE KEY-----\n"
	os.WriteFile(filepath.Join(postsDir, "leaked.md"), []byte(leakedContent), 0644)

	result := CheckTenant(siteDir)

	if result.KeyLeak.OK {
		t.Error("KeyLeak should fail when private key header found in posts")
	}
	if !strings.Contains(result.KeyLeak.Message, "posts/") {
		t.Errorf("KeyLeak message should contain file path, got: %s", result.KeyLeak.Message)
	}
}

func TestCheckTenant_KeyLeakNotFalsePositive(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "grace")

	// The private key is already in .polis/keys/ from setup — should NOT trigger leak
	result := CheckTenant(siteDir)

	if !result.KeyLeak.OK {
		t.Errorf("KeyLeak should not flag .polis/keys/ directory: %s", result.KeyLeak.Message)
	}
}

func TestCheckAllTenants(t *testing.T) {
	tenantsDir := t.TempDir()

	// Create two valid tenants
	for _, name := range []string{"alice", "bob"} {
		siteDir := filepath.Join(tenantsDir, name)
		privKey, pubKey, err := signing.GenerateKeypair()
		if err != nil {
			t.Fatalf("GenerateKeypair: %v", err)
		}

		for _, dir := range []string{".well-known", filepath.Join(".polis", "keys"), "posts"} {
			os.MkdirAll(filepath.Join(siteDir, dir), 0755)
		}

		wk := map[string]interface{}{"public_key": strings.TrimSpace(string(pubKey))}
		wkData, _ := json.MarshalIndent(wk, "", "  ")
		os.WriteFile(filepath.Join(siteDir, ".well-known", "polis"), wkData, 0644)
		os.WriteFile(filepath.Join(siteDir, ".polis", "keys", "id_ed25519"), privKey, 0600)
		os.WriteFile(filepath.Join(siteDir, ".polis", "keys", "id_ed25519.pub"), pubKey, 0644)
	}

	// Create one invalid tenant (missing well-known)
	os.MkdirAll(filepath.Join(tenantsDir, "charlie"), 0755)

	sweep := CheckAllTenants(tenantsDir)

	if sweep.Tenants != 3 {
		t.Errorf("expected 3 tenants, got %d", sweep.Tenants)
	}
	if sweep.Passed != 2 {
		t.Errorf("expected 2 passed, got %d", sweep.Passed)
	}
	if sweep.Failed != 1 {
		t.Errorf("expected 1 failed, got %d", sweep.Failed)
	}
	if sweep.DurationMs < 0 {
		t.Error("duration should be non-negative")
	}
}

func TestCheckTenant_MultiplePosts(t *testing.T) {
	siteDir, pubKey, privKey := setupValidTenant(t, "heidi")
	createSignedPost(t, siteDir, privKey, pubKey, "Post One", "# Post One\n\nFirst post.\n")
	createSignedPost(t, siteDir, privKey, pubKey, "Post Two", "# Post Two\n\nSecond post.\n")

	result := CheckTenant(siteDir)

	if result.PostCount != 2 {
		t.Errorf("expected PostCount=2, got %d", result.PostCount)
	}
	if result.PostsVerified != 2 {
		t.Errorf("expected PostsVerified=2, got %d", result.PostsVerified)
	}
	if !result.Passed() {
		t.Errorf("should pass with valid posts: errors=%v", result.PostErrors)
	}
}

func TestCheckTenant_SkipsVersionsDir(t *testing.T) {
	siteDir, pubKey, privKey := setupValidTenant(t, "ivan")
	createSignedPost(t, siteDir, privKey, pubKey, "Real Post", "# Real Post\n\nContent.\n")

	// Create a .versions directory with a .md file that should be skipped
	versionsDir := filepath.Join(siteDir, "posts", "20260115", ".versions")
	os.MkdirAll(versionsDir, 0755)
	os.WriteFile(filepath.Join(versionsDir, "real-post.md"), []byte("version history data"), 0644)

	result := CheckTenant(siteDir)

	if result.PostCount != 1 {
		t.Errorf("expected PostCount=1 (skipping .versions), got %d", result.PostCount)
	}
}

func TestReconstructSSHSignature(t *testing.T) {
	// Test that reconstructed PEM can round-trip through signing.VerifySignature
	privKey, pubKey, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	content := "test content\n"
	sig, err := signing.SignContent([]byte(content), privKey)
	if err != nil {
		t.Fatalf("SignContent: %v", err)
	}

	// Extract base64 and reconstruct
	b64 := extractSigBase64(sig)
	reconstructed := reconstructSSHSignature(b64)

	// Verify with reconstructed PEM
	valid, err := signing.VerifySignature([]byte(content), pubKey, reconstructed)
	if err != nil {
		t.Fatalf("VerifySignature: %v", err)
	}
	if !valid {
		t.Error("reconstructed signature should verify successfully")
	}
}

func TestVerifyHash(t *testing.T) {
	body := "# Hello\n\nContent here.\n"
	canonical := canonicalizeContent(body)
	hash := sha256Hex([]byte(canonical))

	if !verifyHash(body, hash) {
		t.Error("hash should match canonical content")
	}

	// Direct hash (no canonicalization needed)
	directHash := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
	if !verifyHash(body, directHash) {
		t.Error("hash should match direct content")
	}

	if verifyHash(body, "0000000000000000000000000000000000000000000000000000000000000000") {
		t.Error("wrong hash should not match")
	}
}

func TestCheckTenant_MissingPublicKeyField(t *testing.T) {
	base := t.TempDir()
	siteDir := filepath.Join(base, "judy")
	os.MkdirAll(filepath.Join(siteDir, ".well-known"), 0755)

	// Write well-known without public_key
	wk := map[string]interface{}{"author": "Judy"}
	wkData, _ := json.MarshalIndent(wk, "", "  ")
	os.WriteFile(filepath.Join(siteDir, ".well-known", "polis"), wkData, 0644)

	result := CheckTenant(siteDir)

	if result.WellKnown.OK {
		t.Error("WellKnown should fail when public_key is missing")
	}
	if strings.Contains(result.WellKnown.Message, "failed to load") {
		t.Errorf("should report missing public_key, not load failure: %s", result.WellKnown.Message)
	}
}
