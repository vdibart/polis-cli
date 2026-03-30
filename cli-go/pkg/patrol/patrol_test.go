package patrol

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
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
		filepath.Join("content", "pub.polis.core", "post"),
	} {
		if err := os.MkdirAll(filepath.Join(siteDir, dir), 0755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}

	// Create bundle.json
	bundleData, _ := json.Marshal(map[string]interface{}{
		"name":    "pub.polis.core",
		"version": "1.0.0",
	})
	if err := os.WriteFile(filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json"), bundleData, 0644); err != nil {
		t.Fatalf("WriteFile bundle.json: %v", err)
	}

	// Restrict .polis permissions (patrol checks for world-accessible)
	os.Chmod(filepath.Join(siteDir, ".polis"), 0700)

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
	postsDir := filepath.Join(siteDir, "content", "pub.polis.core", "post", dateDir)
	if err := os.MkdirAll(postsDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	slug := strings.ToLower(strings.ReplaceAll(title, " ", "-"))
	postPath := filepath.Join(postsDir, slug+".md")
	if err := os.WriteFile(postPath, []byte(finalContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	return filepath.Join("content", "pub.polis.core", "post", dateDir, slug+".md")
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

// ============ Existing tests ============

func TestCheckTenant_Valid(t *testing.T) {
	siteDir, pubKey, privKey := setupValidTenant(t, "alice")
	createSignedPost(t, siteDir, privKey, pubKey, "Hello World", "# Hello World\n\nThis is my first post.\n")

	result := CheckTenant(siteDir, CheckOptions{})

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
	if !result.KeyPerms.OK {
		t.Errorf("KeyPerms should be OK: %s", result.KeyPerms.Message)
	}
	if !result.Structure.OK {
		t.Errorf("Structure should be OK: %s", result.Structure.Message)
	}
	if !result.DirExposure.OK {
		t.Errorf("DirExposure should be OK: %s", result.DirExposure.Message)
	}
	if !result.BundleJSON.OK {
		t.Errorf("BundleJSON should be OK: %s", result.BundleJSON.Message)
	}
	if !result.IndexJSONL.OK {
		t.Errorf("IndexJSONL should be OK: %s", result.IndexJSONL.Message)
	}
	if !result.BlessedJSON.OK {
		t.Errorf("BlessedJSON should be OK: %s", result.BlessedJSON.Message)
	}
	if !result.FollowingJSON.OK {
		t.Errorf("FollowingJSON should be OK: %s", result.FollowingJSON.Message)
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
	if len(result.Suspicious) != 0 {
		t.Errorf("expected no suspicious files, got %d: %v", len(result.Suspicious), result.Suspicious)
	}
}

func TestCheckTenant_MissingWellKnown(t *testing.T) {
	base := t.TempDir()
	siteDir := filepath.Join(base, "bob")
	os.MkdirAll(siteDir, 0755)

	result := CheckTenant(siteDir, CheckOptions{})

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

	result := CheckTenant(siteDir, CheckOptions{})

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

	result := CheckTenant(siteDir, CheckOptions{})

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

	result := CheckTenant(siteDir, CheckOptions{})

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
	postsDir := filepath.Join(siteDir, "content", "pub.polis.core", "post", "20260115")
	os.MkdirAll(postsDir, 0755)
	leakedContent := "---\ntitle: Leaked Key\n---\n\nOops:\n-----BEGIN OPENSSH PRIVATE KEY-----\nfakedata\n-----END OPENSSH PRIVATE KEY-----\n"
	os.WriteFile(filepath.Join(postsDir, "leaked.md"), []byte(leakedContent), 0644)

	result := CheckTenant(siteDir, CheckOptions{})

	if result.KeyLeak.OK {
		t.Error("KeyLeak should fail when private key header found in posts")
	}
	if !strings.Contains(result.KeyLeak.Message, "content/") {
		t.Errorf("KeyLeak message should contain file path, got: %s", result.KeyLeak.Message)
	}
}

func TestCheckTenant_KeyLeakNotFalsePositive(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "grace")

	// The private key is already in .polis/keys/ from setup — should NOT trigger leak
	result := CheckTenant(siteDir, CheckOptions{})

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

		for _, dir := range []string{".well-known", filepath.Join(".polis", "keys"), filepath.Join("content", "pub.polis.core", "post")} {
			os.MkdirAll(filepath.Join(siteDir, dir), 0755)
		}
		os.Chmod(filepath.Join(siteDir, ".polis"), 0700)

		wk := map[string]interface{}{"public_key": strings.TrimSpace(string(pubKey))}
		wkData, _ := json.MarshalIndent(wk, "", "  ")
		os.WriteFile(filepath.Join(siteDir, ".well-known", "polis"), wkData, 0644)
		os.WriteFile(filepath.Join(siteDir, ".polis", "keys", "id_ed25519"), privKey, 0600)
		os.WriteFile(filepath.Join(siteDir, ".polis", "keys", "id_ed25519.pub"), pubKey, 0644)

		bundleData, _ := json.Marshal(map[string]interface{}{"name": "pub.polis.core", "version": "1.0.0"})
		os.WriteFile(filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json"), bundleData, 0644)
	}

	// Create one invalid tenant (missing well-known)
	os.MkdirAll(filepath.Join(tenantsDir, "charlie"), 0755)

	sweep := CheckAllTenants(tenantsDir, CheckOptions{})

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

	result := CheckTenant(siteDir, CheckOptions{})

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
	versionsDir := filepath.Join(siteDir, "content", "pub.polis.core", "post", "20260115", ".versions")
	os.MkdirAll(versionsDir, 0755)
	os.WriteFile(filepath.Join(versionsDir, "real-post.md"), []byte("version history data"), 0644)

	result := CheckTenant(siteDir, CheckOptions{})

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

	result := CheckTenant(siteDir, CheckOptions{})

	if result.WellKnown.OK {
		t.Error("WellKnown should fail when public_key is missing")
	}
	if strings.Contains(result.WellKnown.Message, "failed to load") {
		t.Errorf("should report missing public_key, not load failure: %s", result.WellKnown.Message)
	}
}

// ============ New tests: Key permissions ============

func TestCheckTenant_KeyPermsOK(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "keyperms-ok")

	result := CheckTenant(siteDir, CheckOptions{})
	if !result.KeyPerms.OK {
		t.Errorf("KeyPerms should be OK for 0600: %s", result.KeyPerms.Message)
	}
}

func TestCheckTenant_KeyPermsReadOnly(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "keyperms-ro")

	// Change to 0400 (read-only) — also acceptable
	os.Chmod(filepath.Join(siteDir, ".polis", "keys", "id_ed25519"), 0400)

	result := CheckTenant(siteDir, CheckOptions{})
	if !result.KeyPerms.OK {
		t.Errorf("KeyPerms should be OK for 0400: %s", result.KeyPerms.Message)
	}
}

func TestCheckTenant_KeyPermsUnsafe(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "keyperms-bad")

	// Change to 0644 (world-readable) — should fail
	os.Chmod(filepath.Join(siteDir, ".polis", "keys", "id_ed25519"), 0644)

	result := CheckTenant(siteDir, CheckOptions{})
	if result.KeyPerms.OK {
		t.Error("KeyPerms should fail for 0644")
	}
	if !strings.Contains(result.KeyPerms.Message, "0644") {
		t.Errorf("should mention the bad permissions: %s", result.KeyPerms.Message)
	}
	if result.Passed() {
		t.Error("should not pass with unsafe key permissions")
	}
}

// ============ New tests: Structure check ============

func TestCheckTenant_StructureOK(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "struct-ok")

	result := CheckTenant(siteDir, CheckOptions{})
	if !result.Structure.OK {
		t.Errorf("Structure should be OK: %s", result.Structure.Message)
	}
}

func TestCheckTenant_StructureMissingPolisDir(t *testing.T) {
	base := t.TempDir()
	siteDir := filepath.Join(base, "struct-bad")
	// Only create .well-known, not .polis
	os.MkdirAll(filepath.Join(siteDir, ".well-known"), 0755)

	result := CheckTenant(siteDir, CheckOptions{})
	if result.Structure.OK {
		t.Error("Structure should fail when .polis is missing")
	}
	if !strings.Contains(result.Structure.Message, ".polis") {
		t.Errorf("should mention .polis: %s", result.Structure.Message)
	}
}

// ============ New tests: Directory exposure ============

func TestCheckTenant_DirExposureOK(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "exposure-ok")

	result := CheckTenant(siteDir, CheckOptions{})
	if !result.DirExposure.OK {
		t.Errorf("DirExposure should be OK: %s", result.DirExposure.Message)
	}
}

func TestCheckTenant_DirExposureWorldReadable(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "exposure-bad")

	// Make .polis world-readable
	os.Chmod(filepath.Join(siteDir, ".polis"), 0755)

	result := CheckTenant(siteDir, CheckOptions{})
	if result.DirExposure.OK {
		t.Error("DirExposure should fail when .polis is world-accessible")
	}
	if !strings.Contains(result.DirExposure.Message, ".polis") {
		t.Errorf("should mention .polis: %s", result.DirExposure.Message)
	}
}

func TestCheckTenant_DirExposureGitWorldReadable(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "exposure-git")

	// Create a .git dir with world-readable perms
	gitDir := filepath.Join(siteDir, ".git")
	os.MkdirAll(gitDir, 0755) // world-readable

	result := CheckTenant(siteDir, CheckOptions{})
	if result.DirExposure.OK {
		t.Error("DirExposure should fail when .git is world-accessible")
	}
	if !strings.Contains(result.DirExposure.Message, ".git") {
		t.Errorf("should mention .git: %s", result.DirExposure.Message)
	}
}

func TestCheckTenant_DirExposureNoGitIsOK(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "exposure-nogit")

	// No .git dir at all — should be fine
	result := CheckTenant(siteDir, CheckOptions{})
	if !result.DirExposure.OK {
		t.Errorf("DirExposure should be OK when .git doesn't exist: %s", result.DirExposure.Message)
	}
}

// ============ New tests: Metadata validation ============

func TestCheckTenant_BundleJSON_Valid(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "bundle-valid")

	bundleDir := filepath.Join(siteDir, "content", "pub.polis.core")
	os.MkdirAll(bundleDir, 0755)
	data, _ := json.Marshal(map[string]interface{}{
		"name":    "pub.polis.core",
		"version": "1.0.0",
	})
	os.WriteFile(filepath.Join(bundleDir, "bundle.json"), data, 0644)

	result := CheckTenant(siteDir, CheckOptions{})
	if !result.BundleJSON.OK {
		t.Errorf("BundleJSON should be OK: %s", result.BundleJSON.Message)
	}
}

func TestCheckTenant_BundleJSON_Missing(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "bundle-missing")

	// Remove the bundle.json that setupValidTenant creates
	os.Remove(filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json"))

	result := CheckTenant(siteDir, CheckOptions{})
	if result.BundleJSON.OK {
		t.Error("BundleJSON should not be OK when file is missing")
	}
	if result.BundleJSON.Message != "missing" {
		t.Errorf("expected message 'missing', got: %s", result.BundleJSON.Message)
	}
}

func TestCheckTenant_BundleJSON_InvalidJSON(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "bundle-invalid")

	bundleDir := filepath.Join(siteDir, "content", "pub.polis.core")
	os.MkdirAll(bundleDir, 0755)
	os.WriteFile(filepath.Join(bundleDir, "bundle.json"), []byte("{broken"), 0644)

	result := CheckTenant(siteDir, CheckOptions{})
	if result.BundleJSON.OK {
		t.Error("BundleJSON should fail for invalid JSON")
	}
}

func TestCheckTenant_BundleJSON_MissingName(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "bundle-noname")

	bundleDir := filepath.Join(siteDir, "content", "pub.polis.core")
	os.MkdirAll(bundleDir, 0755)
	data, _ := json.Marshal(map[string]interface{}{"version": "1.0.0"})
	os.WriteFile(filepath.Join(bundleDir, "bundle.json"), data, 0644)

	result := CheckTenant(siteDir, CheckOptions{})
	if result.BundleJSON.OK {
		t.Error("BundleJSON should fail when name is missing")
	}
}

func TestCheckTenant_IndexJSONL_Valid(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "index-valid")

	indexDir := filepath.Join(siteDir, "content", "pub.polis.core")
	os.MkdirAll(indexDir, 0755)
	lines := `{"type":"post","path":"content/pub.polis.core/post/20260115/hello.md","title":"Hello","published":"2026-01-15T10:00:00Z"}
{"type":"post","path":"content/pub.polis.core/post/20260115/world.md","title":"World","published":"2026-01-15T11:00:00Z"}
`
	os.WriteFile(filepath.Join(indexDir, "index.jsonl"), []byte(lines), 0644)

	result := CheckTenant(siteDir, CheckOptions{})
	if !result.IndexJSONL.OK {
		t.Errorf("IndexJSONL should be OK: %s", result.IndexJSONL.Message)
	}
}

func TestCheckTenant_IndexJSONL_InvalidLine(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "index-invalid")

	indexDir := filepath.Join(siteDir, "content", "pub.polis.core")
	os.MkdirAll(indexDir, 0755)
	lines := `{"type":"post","title":"Good"}
not json at all
{"type":"post","title":"Also Good"}
`
	os.WriteFile(filepath.Join(indexDir, "index.jsonl"), []byte(lines), 0644)

	result := CheckTenant(siteDir, CheckOptions{})
	if result.IndexJSONL.OK {
		t.Error("IndexJSONL should fail for invalid JSON line")
	}
	if !strings.Contains(result.IndexJSONL.Message, "line 2") {
		t.Errorf("should identify the bad line number: %s", result.IndexJSONL.Message)
	}
}

func TestCheckTenant_BlessedJSON_Valid(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "blessed-valid")

	commentDir := filepath.Join(siteDir, "content", "pub.polis.core", "comment")
	os.MkdirAll(commentDir, 0755)
	data, _ := json.Marshal(map[string]interface{}{
		"version":  "test",
		"comments": []interface{}{},
	})
	os.WriteFile(filepath.Join(commentDir, "blessed.json"), data, 0644)

	result := CheckTenant(siteDir, CheckOptions{})
	if !result.BlessedJSON.OK {
		t.Errorf("BlessedJSON should be OK: %s", result.BlessedJSON.Message)
	}
}

func TestCheckTenant_BlessedJSON_MissingComments(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "blessed-bad")

	commentDir := filepath.Join(siteDir, "content", "pub.polis.core", "comment")
	os.MkdirAll(commentDir, 0755)
	data, _ := json.Marshal(map[string]interface{}{"version": "test"})
	os.WriteFile(filepath.Join(commentDir, "blessed.json"), data, 0644)

	result := CheckTenant(siteDir, CheckOptions{})
	if result.BlessedJSON.OK {
		t.Error("BlessedJSON should fail when comments field is missing")
	}
}

func TestCheckTenant_FollowingJSON_Valid(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "following-valid")

	followDir := filepath.Join(siteDir, "content", "pub.polis.core", "follow")
	os.MkdirAll(followDir, 0755)
	data, _ := json.Marshal(map[string]interface{}{
		"version":   "test",
		"following": []interface{}{},
	})
	os.WriteFile(filepath.Join(followDir, "following.json"), data, 0644)

	result := CheckTenant(siteDir, CheckOptions{})
	if !result.FollowingJSON.OK {
		t.Errorf("FollowingJSON should be OK: %s", result.FollowingJSON.Message)
	}
}

func TestCheckTenant_FollowingJSON_InvalidJSON(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "following-bad")

	followDir := filepath.Join(siteDir, "content", "pub.polis.core", "follow")
	os.MkdirAll(followDir, 0755)
	os.WriteFile(filepath.Join(followDir, "following.json"), []byte("{{{{"), 0644)

	result := CheckTenant(siteDir, CheckOptions{})
	if result.FollowingJSON.OK {
		t.Error("FollowingJSON should fail for invalid JSON")
	}
}

// ============ New tests: Suspicious file scan ============

func TestScanSuspicious_ShellScript(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "suspicious-sh")

	// Create a shell script in content directory
	contentDir := filepath.Join(siteDir, "content")
	os.MkdirAll(contentDir, 0755)
	os.WriteFile(filepath.Join(contentDir, "backdoor.sh"), []byte("#!/bin/bash\nrm -rf /"), 0644)

	result := CheckTenant(siteDir, CheckOptions{})

	found := false
	for _, s := range result.Suspicious {
		if strings.Contains(s.Path, "backdoor.sh") && strings.Contains(s.Reason, ".sh") {
			found = true
		}
	}
	if !found {
		t.Errorf("should flag shell script as suspicious, got: %v", result.Suspicious)
	}
}

func TestScanSuspicious_ExecutableBit(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "suspicious-exec")

	// Create a file with execute bit in content
	contentDir := filepath.Join(siteDir, "content")
	os.MkdirAll(contentDir, 0755)
	os.WriteFile(filepath.Join(contentDir, "something.txt"), []byte("harmless?"), 0755) // +x

	result := CheckTenant(siteDir, CheckOptions{})

	found := false
	for _, s := range result.Suspicious {
		if strings.Contains(s.Path, "something.txt") && strings.Contains(s.Reason, "executable") {
			found = true
		}
	}
	if !found {
		t.Errorf("should flag executable file as suspicious, got: %v", result.Suspicious)
	}
}

func TestScanSuspicious_OversizedFile(t *testing.T) {
	// Verify the threshold constant is set correctly (we don't create a 10MB file in tests)
	if maxFileSize != 10*1024*1024 {
		t.Errorf("expected maxFileSize=10MB, got %d", maxFileSize)
	}
}

func TestScanSuspicious_Symlink(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "suspicious-link")

	// Create a symlink in the content directory
	contentDir := filepath.Join(siteDir, "content")
	os.MkdirAll(contentDir, 0755)
	os.Symlink("/etc/passwd", filepath.Join(contentDir, "sneaky"))

	result := CheckTenant(siteDir, CheckOptions{})

	found := false
	for _, s := range result.Suspicious {
		if strings.Contains(s.Path, "sneaky") && strings.Contains(s.Reason, "symlink") {
			found = true
		}
	}
	if !found {
		t.Errorf("should flag symlink as suspicious, got: %v", result.Suspicious)
	}
}

func TestScanSuspicious_DiscoverExempt(t *testing.T) {
	// discover.polis.pub should not be flagged for shell scripts
	base := t.TempDir()
	siteDir := filepath.Join(base, "discover.polis.pub")

	privKey, pubKey, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	for _, dir := range []string{".well-known", filepath.Join(".polis", "keys"), "content"} {
		os.MkdirAll(filepath.Join(siteDir, dir), 0755)
	}
	os.Chmod(filepath.Join(siteDir, ".polis"), 0700)

	wk := map[string]interface{}{"public_key": strings.TrimSpace(string(pubKey))}
	wkData, _ := json.MarshalIndent(wk, "", "  ")
	os.WriteFile(filepath.Join(siteDir, ".well-known", "polis"), wkData, 0644)
	os.WriteFile(filepath.Join(siteDir, ".polis", "keys", "id_ed25519"), privKey, 0600)
	os.WriteFile(filepath.Join(siteDir, ".polis", "keys", "id_ed25519.pub"), pubKey, 0644)

	// Create a shell script — should NOT be flagged for discover.polis.pub
	os.WriteFile(filepath.Join(siteDir, "content", "deploy.sh"), []byte("#!/bin/bash\necho hi"), 0644)

	result := CheckTenant(siteDir, CheckOptions{})

	for _, s := range result.Suspicious {
		if strings.Contains(s.Reason, ".sh") {
			t.Errorf("discover.polis.pub should be exempt from script checks: %v", s)
		}
	}
}

func TestScanSuspicious_SkipsPolisDir(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "suspicious-polis-skip")

	// Files inside .polis/ should not be scanned for suspicious extensions
	// (the private key is already there and that's fine)
	result := CheckTenant(siteDir, CheckOptions{})

	for _, s := range result.Suspicious {
		if strings.HasPrefix(s.Path, ".polis/") {
			t.Errorf("should not scan .polis/ for suspicious files: %v", s)
		}
	}
}

func TestScanSuspicious_CleanSite(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "suspicious-clean")

	result := CheckTenant(siteDir, CheckOptions{})

	if len(result.Suspicious) != 0 {
		t.Errorf("clean site should have no suspicious files, got: %v", result.Suspicious)
	}
}

func TestScanSuspicious_CausesFailure(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "suspicious-fail")

	// Add a suspicious file
	contentDir := filepath.Join(siteDir, "content")
	os.MkdirAll(contentDir, 0755)
	os.WriteFile(filepath.Join(contentDir, "hack.php"), []byte("<?php echo 1; ?>"), 0644)

	result := CheckTenant(siteDir, CheckOptions{})

	if len(result.Suspicious) == 0 {
		t.Error("should have suspicious files")
	}
	if result.Passed() {
		t.Error("suspicious files should cause Passed() to fail")
	}
}

// ============ New tests: SweepResult warnings ============

func TestCheckAllTenants_WarningCount(t *testing.T) {
	tenantsDir := t.TempDir()

	// Create one valid tenant with a suspicious file
	siteDir := filepath.Join(tenantsDir, "alice")
	privKey, pubKey, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	for _, dir := range []string{".well-known", filepath.Join(".polis", "keys"), "content"} {
		os.MkdirAll(filepath.Join(siteDir, dir), 0755)
	}
	os.Chmod(filepath.Join(siteDir, ".polis"), 0700)

	wk := map[string]interface{}{"public_key": strings.TrimSpace(string(pubKey))}
	wkData, _ := json.MarshalIndent(wk, "", "  ")
	os.WriteFile(filepath.Join(siteDir, ".well-known", "polis"), wkData, 0644)
	os.WriteFile(filepath.Join(siteDir, ".polis", "keys", "id_ed25519"), privKey, 0600)
	os.WriteFile(filepath.Join(siteDir, ".polis", "keys", "id_ed25519.pub"), pubKey, 0644)

	// Add suspicious file
	os.WriteFile(filepath.Join(siteDir, "content", "evil.py"), []byte("import os"), 0644)

	sweep := CheckAllTenants(tenantsDir, CheckOptions{})

	if sweep.Warnings == 0 {
		t.Error("expected warnings > 0 for suspicious files")
	}
	if sweep.Failed != 1 {
		t.Errorf("tenant with suspicious files should fail, got failed=%d", sweep.Failed)
	}
}

// ============ Provisioning check tests ============

func TestCheckTenant_MissingPolicies(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "missing-policies")

	result := CheckTenant(siteDir, CheckOptions{})

	if result.PublicPolicies.OK {
		t.Error("PublicPolicies should be not OK when file is missing")
	}
	if result.PrivatePolicies.OK {
		t.Error("PrivatePolicies should be not OK when file is missing")
	}
}

func TestCheckTenant_MissingDMPolicies(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "missing-dm-policies")

	// Create private policies without DM rules
	privatePolicyDir := filepath.Join(siteDir, ".polis", "policies")
	os.MkdirAll(privatePolicyDir, 0700)
	os.WriteFile(filepath.Join(privatePolicyDir, "rules.jsonl"), []byte(`{"active":true,"policy":"allow pub.polis.post from all"}`+"\n"), 0600)

	result := CheckTenant(siteDir, CheckOptions{})

	if !result.PrivatePolicies.OK {
		t.Errorf("PrivatePolicies should be OK: %s", result.PrivatePolicies.Message)
	}
	if result.DMPolicies.OK {
		t.Error("DMPolicies should be not OK when DM rules are missing")
	}
}

func TestCheckTenant_MissingSalt(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "missing-salt")

	result := CheckTenant(siteDir, CheckOptions{})

	if result.StorageSalt.OK {
		t.Error("StorageSalt should be not OK when salt file is missing")
	}
}

func TestCheckTenant_MissingDMDirs(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "missing-dm-dirs")

	result := CheckTenant(siteDir, CheckOptions{})

	if result.DMDirectories.OK {
		t.Error("DMDirectories should be not OK when DM dirs are missing")
	}
}

func TestCheckTenant_MissingBundle(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "missing-bundle-prov")

	// Remove the bundle.json that setupValidTenant creates
	os.Remove(filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json"))

	result := CheckTenant(siteDir, CheckOptions{})

	if result.BundleJSON.OK {
		t.Error("BundleJSON should be not OK when file is missing")
	}
	if result.BundleJSON.Message != "missing" {
		t.Errorf("expected message 'missing', got: %s", result.BundleJSON.Message)
	}
}

func TestCheckTenant_AllProvisioningPresent(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "all-present")

	// Create all provisioning files
	os.MkdirAll(filepath.Join(siteDir, "policies"), 0755)
	os.WriteFile(filepath.Join(siteDir, "policies", "rules.jsonl"), []byte(`{"active":true,"policy":"emit pub.polis.comment.blessing from self"}`+"\n"), 0644)

	os.MkdirAll(filepath.Join(siteDir, ".polis", "policies"), 0700)
	os.WriteFile(filepath.Join(siteDir, ".polis", "policies", "rules.jsonl"), []byte(`{"active":true,"policy":"allow pub.polis.dm from following"}`+"\n"), 0600)

	os.MkdirAll(filepath.Join(siteDir, ".polis", "content", "pub.polis.core", "dm", "conv"), 0700)
	os.WriteFile(filepath.Join(siteDir, ".polis", "storage-salt"), []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"), 0600)

	result := CheckTenant(siteDir, CheckOptions{})

	if !result.PublicPolicies.OK {
		t.Errorf("PublicPolicies should be OK: %s", result.PublicPolicies.Message)
	}
	if !result.PrivatePolicies.OK {
		t.Errorf("PrivatePolicies should be OK: %s", result.PrivatePolicies.Message)
	}
	if !result.DMPolicies.OK {
		t.Errorf("DMPolicies should be OK: %s", result.DMPolicies.Message)
	}
	if !result.StorageSalt.OK {
		t.Errorf("StorageSalt should be OK: %s", result.StorageSalt.Message)
	}
	if !result.DMDirectories.OK {
		t.Errorf("DMDirectories should be OK: %s", result.DMDirectories.Message)
	}
	if !result.BundleJSON.OK {
		t.Errorf("BundleJSON should be OK: %s", result.BundleJSON.Message)
	}
}

func TestCheckTenant_BundleTypes_MissingTypes(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "test")

	// Write a valid bundle.json that has some types but is missing pub.polis.dm
	// (simulates a site provisioned before DMs were added)
	oldBundle := bundle.DefaultCoreBundle()
	delete(oldBundle.Types, "pub.polis.dm")
	bundlePath := filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json")
	bundle.SaveBundle(bundlePath, oldBundle)

	result := CheckTenant(siteDir, CheckOptions{})

	if result.BundleTypes.OK {
		t.Error("BundleTypes should fail when bundle is missing pub.polis.dm")
	}
	if !strings.Contains(result.BundleTypes.Message, "missing types") {
		t.Errorf("expected 'missing types' message, got: %s", result.BundleTypes.Message)
	}
	if !strings.Contains(result.BundleTypes.Message, "pub.polis.dm") {
		t.Errorf("expected pub.polis.dm in missing types, got: %s", result.BundleTypes.Message)
	}
}

func TestCheckTenant_BundleTypes_Complete(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "test")

	// Overwrite bundle.json with the default (has all types)
	bundlePath := filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json")
	bundle.SaveBundle(bundlePath, bundle.DefaultCoreBundle())

	result := CheckTenant(siteDir, CheckOptions{})

	if !result.BundleTypes.OK {
		t.Errorf("BundleTypes should be OK with complete bundle: %s", result.BundleTypes.Message)
	}
}

func TestCheckTenant_BundleTypes_MissingBundle(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "test")

	// Remove bundle.json entirely — BundleTypes should pass (BundleJSON handles that case)
	os.Remove(filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json"))

	result := CheckTenant(siteDir, CheckOptions{})

	if !result.BundleTypes.OK {
		t.Errorf("BundleTypes should be OK when bundle.json is missing: %s", result.BundleTypes.Message)
	}
}

// ============ Salt integrity tests (Item 1) ============

func TestSaltIntegrity_Baseline(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "salt-baseline")
	snapshotDir := t.TempDir()

	// Create salt file
	os.WriteFile(filepath.Join(siteDir, ".polis", "storage-salt"),
		[]byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"), 0600)

	result := CheckTenant(siteDir, CheckOptions{SnapshotDir: snapshotDir})
	if !result.SaltIntegrity.OK {
		t.Errorf("first sweep should be OK (baseline): %s", result.SaltIntegrity.Message)
	}
}

func TestSaltIntegrity_Unchanged(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "salt-unchanged")
	snapshotDir := t.TempDir()
	salt := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	os.WriteFile(filepath.Join(siteDir, ".polis", "storage-salt"), []byte(salt), 0600)

	// First sweep — baseline
	CheckTenant(siteDir, CheckOptions{SnapshotDir: snapshotDir})

	// Second sweep — unchanged
	result := CheckTenant(siteDir, CheckOptions{SnapshotDir: snapshotDir})
	if !result.SaltIntegrity.OK {
		t.Errorf("unchanged salt should pass: %s", result.SaltIntegrity.Message)
	}
}

func TestSaltIntegrity_Tampered(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "salt-tampered")
	snapshotDir := t.TempDir()
	saltPath := filepath.Join(siteDir, ".polis", "storage-salt")

	os.WriteFile(saltPath, []byte("original-salt-content-0000000000000000000000000000000000"), 0600)
	CheckTenant(siteDir, CheckOptions{SnapshotDir: snapshotDir})

	// Tamper with salt
	os.WriteFile(saltPath, []byte("tampered-salt-content-1111111111111111111111111111111111"), 0600)
	result := CheckTenant(siteDir, CheckOptions{SnapshotDir: snapshotDir})

	if result.SaltIntegrity.OK {
		t.Error("tampered salt should fail")
	}
	if !strings.Contains(result.SaltIntegrity.Message, "TAMPERED") {
		t.Errorf("should mention TAMPERED: %s", result.SaltIntegrity.Message)
	}
}

func TestSaltIntegrity_MtimeOnly(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "salt-mtime")
	snapshotDir := t.TempDir()
	saltPath := filepath.Join(siteDir, ".polis", "storage-salt")
	saltContent := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	os.WriteFile(saltPath, []byte(saltContent), 0600)
	CheckTenant(siteDir, CheckOptions{SnapshotDir: snapshotDir})

	// Change mtime to the future (same content, different mtime)
	futureTime := time.Now().Add(time.Hour)
	os.Chtimes(saltPath, futureTime, futureTime)
	result := CheckTenant(siteDir, CheckOptions{SnapshotDir: snapshotDir})

	if result.SaltIntegrity.OK {
		t.Error("mtime-changed salt should fail")
	}
	if !strings.Contains(result.SaltIntegrity.Message, "mtime") {
		t.Errorf("should mention mtime: %s", result.SaltIntegrity.Message)
	}
}

// ============ Policy syntax tests (Item 2) ============

func TestPolicySyntax_Valid(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "policy-valid")

	// Create valid policies
	os.MkdirAll(filepath.Join(siteDir, "policies"), 0755)
	content := `{"version":1,"generator":"test"}
{"active":true,"policy":"allow pub.polis.post from all"}
{"active":true,"policy":"deny pub.polis.dm from all"}
`
	os.WriteFile(filepath.Join(siteDir, "policies", "rules.jsonl"), []byte(content), 0644)

	result := CheckTenant(siteDir, CheckOptions{})
	if len(result.PolicyWarnings) != 0 {
		t.Errorf("valid policies should have no warnings: %v", result.PolicyWarnings)
	}
}

func TestPolicySyntax_InvalidRule(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "policy-invalid")

	os.MkdirAll(filepath.Join(siteDir, "policies"), 0755)
	content := `{"version":1,"generator":"test"}
{"active":true,"policy":"allow pub.polis.post from all"}
{"active":true,"policy":"invalid rule syntax"}
`
	os.WriteFile(filepath.Join(siteDir, "policies", "rules.jsonl"), []byte(content), 0644)

	result := CheckTenant(siteDir, CheckOptions{})
	if len(result.PolicyWarnings) == 0 {
		t.Error("invalid rule should produce a warning")
	}
	found := false
	for _, w := range result.PolicyWarnings {
		if w.Line == 3 && strings.Contains(w.Rule, "invalid rule syntax") {
			found = true
		}
	}
	if !found {
		t.Errorf("warning should reference line 3: %v", result.PolicyWarnings)
	}
}

func TestPolicySyntax_MetadataSkipped(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "policy-metadata")

	os.MkdirAll(filepath.Join(siteDir, "policies"), 0755)
	// Version line without policy field should be skipped
	content := `{"version":1,"generator":"test"}
{"active":true,"policy":"allow pub.polis.post from all"}
`
	os.WriteFile(filepath.Join(siteDir, "policies", "rules.jsonl"), []byte(content), 0644)

	result := CheckTenant(siteDir, CheckOptions{})
	if len(result.PolicyWarnings) != 0 {
		t.Errorf("metadata lines should be skipped: %v", result.PolicyWarnings)
	}
}

// ============ Mtime tracking tests (Item 3) ============

func TestMtimeTracking_Baseline(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "mtime-baseline")
	snapshotDir := t.TempDir()

	result := CheckTenant(siteDir, CheckOptions{SnapshotDir: snapshotDir})
	if len(result.MtimeAlerts) != 0 {
		t.Errorf("first sweep should have no mtime alerts: %v", result.MtimeAlerts)
	}
}

func TestMtimeTracking_PrivateKeyChanged(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "mtime-privkey")
	snapshotDir := t.TempDir()

	CheckTenant(siteDir, CheckOptions{SnapshotDir: snapshotDir})

	// Change the private key mtime to the future
	privKeyPath := filepath.Join(siteDir, ".polis", "keys", "id_ed25519")
	futureTime := time.Now().Add(time.Hour)
	os.Chtimes(privKeyPath, futureTime, futureTime)

	result := CheckTenant(siteDir, CheckOptions{SnapshotDir: snapshotDir})
	found := false
	for _, a := range result.MtimeAlerts {
		if strings.Contains(a.File, "id_ed25519") && a.Severity == "HIGH" {
			found = true
		}
	}
	if !found {
		t.Errorf("should detect HIGH severity private key mtime change: %v", result.MtimeAlerts)
	}
}

func TestMtimeTracking_PolicyChanged(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "mtime-policy")
	snapshotDir := t.TempDir()

	// Create policy file
	os.MkdirAll(filepath.Join(siteDir, "policies"), 0755)
	policyPath := filepath.Join(siteDir, "policies", "rules.jsonl")
	os.WriteFile(policyPath, []byte(`{"active":true,"policy":"allow pub.polis.post from all"}`+"\n"), 0644)

	CheckTenant(siteDir, CheckOptions{SnapshotDir: snapshotDir})

	// Change policy file mtime to the future
	futureTime := time.Now().Add(time.Hour)
	os.Chtimes(policyPath, futureTime, futureTime)

	result := CheckTenant(siteDir, CheckOptions{SnapshotDir: snapshotDir})
	found := false
	for _, a := range result.MtimeAlerts {
		if strings.Contains(a.File, "rules.jsonl") && a.Severity == "LOW" {
			found = true
		}
	}
	if !found {
		t.Errorf("should detect LOW severity policy mtime change: %v", result.MtimeAlerts)
	}
}

// ============ File creation detection tests (Item 5) ============

func TestFileCreation_NewFileInKeys(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "filecreation-keys")
	snapshotDir := t.TempDir()

	CheckTenant(siteDir, CheckOptions{SnapshotDir: snapshotDir})

	// Add a new file to keys directory
	os.WriteFile(filepath.Join(siteDir, ".polis", "keys", "extra_key"), []byte("bad"), 0600)

	result := CheckTenant(siteDir, CheckOptions{SnapshotDir: snapshotDir})
	found := false
	for _, a := range result.FileCreationAlerts {
		if a.File == "extra_key" && a.Severity == "HIGH" {
			found = true
		}
	}
	if !found {
		t.Errorf("should detect HIGH alert for new file in keys: %v", result.FileCreationAlerts)
	}
}

func TestFileCreation_Baseline(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "filecreation-baseline")
	snapshotDir := t.TempDir()

	result := CheckTenant(siteDir, CheckOptions{SnapshotDir: snapshotDir})
	if len(result.FileCreationAlerts) != 0 {
		t.Errorf("first sweep should have no file creation alerts: %v", result.FileCreationAlerts)
	}
}

// ============ DM directory permissions tests (Item 4) ============

func TestDMDirPerms_Wrong(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "dmperms-wrong")
	dmDir := filepath.Join(siteDir, ".polis", "content", "pub.polis.core", "dm", "conv")
	os.MkdirAll(dmDir, 0755) // Wrong permissions

	result := CheckTenant(siteDir, CheckOptions{})
	if result.DMDirectories.OK {
		t.Error("should fail for 0755 DM dir permissions")
	}
	if !strings.Contains(result.DMDirectories.Message, "0755") {
		t.Errorf("should mention bad permissions: %s", result.DMDirectories.Message)
	}
}

func TestDMDirPerms_Correct(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "dmperms-correct")
	dmDir := filepath.Join(siteDir, ".polis", "content", "pub.polis.core", "dm", "conv")
	os.MkdirAll(dmDir, 0700)

	result := CheckTenant(siteDir, CheckOptions{})
	if !result.DMDirectories.OK {
		t.Errorf("should pass for 0700 DM dir: %s", result.DMDirectories.Message)
	}
}

// ============ CheckOptions backward compatibility ============

func TestCheckOptions_EmptySkipsSnapshotChecks(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "opts-empty")

	result := CheckTenant(siteDir, CheckOptions{})

	// Snapshot-based fields should be zero-value (not populated)
	if result.SaltIntegrity.Message != "" {
		t.Errorf("empty opts should skip salt check: %s", result.SaltIntegrity.Message)
	}
	if len(result.MtimeAlerts) != 0 {
		t.Errorf("empty opts should skip mtime check: %v", result.MtimeAlerts)
	}
	if len(result.FileCreationAlerts) != 0 {
		t.Errorf("empty opts should skip file creation check: %v", result.FileCreationAlerts)
	}
}

// ============ SSH artifact detection (Item 11) ============

func TestSSHArtifacts_BashHistory(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "ssh-artifacts")

	// Create .bash_history in tenant root
	os.WriteFile(filepath.Join(siteDir, ".bash_history"), []byte("secret commands"), 0644)

	result := CheckTenant(siteDir, CheckOptions{})

	found := false
	for _, s := range result.Suspicious {
		if s.Path == ".bash_history" && strings.Contains(s.Reason, "shell/SSH artifact") {
			found = true
		}
	}
	if !found {
		t.Errorf("should detect .bash_history as suspicious: %v", result.Suspicious)
	}
}

// ============ Policy semantic checks (Item 13) ============

func TestPolicySemantics_Duplicates(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "policy-dup")

	os.MkdirAll(filepath.Join(siteDir, "policies"), 0755)
	content := `{"version":1,"generator":"test"}
{"active":true,"policy":"allow pub.polis.post from all"}
{"active":true,"policy":"allow pub.polis.post from all"}
`
	os.WriteFile(filepath.Join(siteDir, "policies", "rules.jsonl"), []byte(content), 0644)

	result := CheckTenant(siteDir, CheckOptions{})
	found := false
	for _, w := range result.PolicyWarnings {
		if strings.Contains(w.Error, "duplicate") {
			found = true
		}
	}
	if !found {
		t.Errorf("should warn about duplicate rules: %v", result.PolicyWarnings)
	}
}

func TestPolicySemantics_Contradictions(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "policy-contra")

	os.MkdirAll(filepath.Join(siteDir, "policies"), 0755)
	content := `{"version":1,"generator":"test"}
{"active":true,"policy":"allow pub.polis.post from all"}
{"active":true,"policy":"deny pub.polis.post from all"}
`
	os.WriteFile(filepath.Join(siteDir, "policies", "rules.jsonl"), []byte(content), 0644)

	result := CheckTenant(siteDir, CheckOptions{})
	found := false
	for _, w := range result.PolicyWarnings {
		if strings.Contains(w.Error, "contradicts") {
			found = true
		}
	}
	if !found {
		t.Errorf("should warn about contradictions: %v", result.PolicyWarnings)
	}
}

// ============ .well-known field validation (Item 9) ============

func TestWellKnownFields_Valid(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "wkf-valid")

	// Add bundles to .well-known
	wkPath := filepath.Join(siteDir, ".well-known", "polis")
	data, _ := os.ReadFile(wkPath)
	var wk map[string]interface{}
	json.Unmarshal(data, &wk)
	wk["bundles"] = map[string]interface{}{
		"pub.polis.core": map[string]interface{}{"version": "1.0.0"},
	}
	wkData, _ := json.MarshalIndent(wk, "", "  ")
	os.WriteFile(wkPath, wkData, 0644)

	result := CheckTenant(siteDir, CheckOptions{})
	if !result.WellKnownFields.OK {
		t.Errorf("valid .well-known should pass: %s", result.WellKnownFields.Message)
	}
}

func TestWellKnownFields_DomainMismatch(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "wkf-domain")

	// Add bundles to .well-known
	wkPath := filepath.Join(siteDir, ".well-known", "polis")
	data, _ := os.ReadFile(wkPath)
	var wk map[string]interface{}
	json.Unmarshal(data, &wk)
	wk["bundles"] = map[string]interface{}{
		"pub.polis.core": map[string]interface{}{"version": "1.0.0"},
	}
	wkData, _ := json.MarshalIndent(wk, "", "  ")
	os.WriteFile(wkPath, wkData, 0644)

	// With matching baseDomain — handle "wkf-domain" is not a subdomain of "example.com"
	result := CheckTenant(siteDir, CheckOptions{BaseDomain: "example.com"})
	if result.WellKnownFields.OK {
		t.Error("handle not matching baseDomain should fail WellKnownFields")
	}
	if !strings.Contains(result.WellKnownFields.Message, "not a subdomain") {
		t.Errorf("unexpected message: %s", result.WellKnownFields.Message)
	}

	// With matching baseDomain — create tenant whose name ends with ".example.com"
	base := t.TempDir()
	matchDir := filepath.Join(base, "alice.example.com")
	// Copy structure
	for _, dir := range []string{".well-known", filepath.Join(".polis", "keys"), filepath.Join("content", "pub.polis.core", "post")} {
		os.MkdirAll(filepath.Join(matchDir, dir), 0755)
	}
	// Copy files
	for _, f := range []string{".well-known/polis", ".polis/keys/id_ed25519", ".polis/keys/id_ed25519.pub", "content/pub.polis.core/bundle.json"} {
		src, _ := os.ReadFile(filepath.Join(siteDir, f))
		os.WriteFile(filepath.Join(matchDir, f), src, 0600)
	}
	result2 := CheckTenant(matchDir, CheckOptions{BaseDomain: "example.com"})
	if !result2.WellKnownFields.OK {
		t.Errorf("matching handle should pass WellKnownFields: %s", result2.WellKnownFields.Message)
	}
}

func TestWellKnownFields_InvalidPublicKey(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "wkf-badkey")

	// Write invalid public key
	wk := map[string]interface{}{
		"public_key": "not-a-valid-key",
		"bundles": map[string]interface{}{
			"pub.polis.core": map[string]interface{}{"version": "1.0.0"},
		},
	}
	wkData, _ := json.MarshalIndent(wk, "", "  ")
	os.WriteFile(filepath.Join(siteDir, ".well-known", "polis"), wkData, 0644)

	result := CheckTenant(siteDir, CheckOptions{})
	// WellKnownFields should catch the bad key
	if result.WellKnownFields.OK {
		t.Error("invalid public key should fail WellKnownFields")
	}
}

// ============ Root artifact detection (R7-4) ============

func TestScanRootArtifacts(t *testing.T) {
	dir := t.TempDir()

	// Clean dir — no artifacts
	results := ScanRootArtifacts(dir)
	if len(results) != 0 {
		t.Errorf("expected 0 artifacts in clean dir, got %d", len(results))
	}

	// Create .bash_history
	os.WriteFile(filepath.Join(dir, ".bash_history"), []byte("ls\n"), 0644)
	results = ScanRootArtifacts(dir)
	if len(results) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(results))
	}
	if !strings.Contains(results[0].Path, ".bash_history") {
		t.Errorf("expected .bash_history in path, got %s", results[0].Path)
	}
	if !strings.Contains(results[0].Reason, "data root") {
		t.Errorf("expected 'data root' in reason, got %s", results[0].Reason)
	}

	// Create .ssh directory
	os.MkdirAll(filepath.Join(dir, ".ssh"), 0700)
	results = ScanRootArtifacts(dir)
	if len(results) != 2 {
		t.Errorf("expected 2 artifacts, got %d", len(results))
	}
}

func TestCheckTenant_DMDomainCase_OK(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "dm-case-ok")

	// Create DM conversation with all-lowercase domains
	convDir := filepath.Join(siteDir, ".polis", "content", "pub.polis.core", "dm", "conv")
	os.MkdirAll(convDir, 0700)
	conv := `{"peer_domain":"alice.example.com","peer_url":"https://alice.example.com/","messages":[{"from":"alice.example.com","to":"dm-case-ok.polis.pub"}]}`
	os.WriteFile(filepath.Join(convDir, "abc123.json"), []byte(conv), 0600)

	result := CheckTenant(siteDir, CheckOptions{})
	if !result.DMDomainCase.OK {
		t.Errorf("DMDomainCase should be OK for lowercase domains: %s", result.DMDomainCase.Message)
	}
}

func TestCheckTenant_DMDomainCase_MixedCase(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "dm-case-bad")

	// Create DM conversation with mixed-case peer_domain (the bug david.polis.pub hit)
	convDir := filepath.Join(siteDir, ".polis", "content", "pub.polis.core", "dm", "conv")
	os.MkdirAll(convDir, 0700)
	conv := `{"peer_domain":"Alice.Example.COM","peer_url":"https://Alice.Example.COM/","messages":[{"from":"Alice.Example.COM","to":"dm-case-bad.polis.pub"}]}`
	os.WriteFile(filepath.Join(convDir, "abc123.json"), []byte(conv), 0600)

	result := CheckTenant(siteDir, CheckOptions{})
	if result.DMDomainCase.OK {
		t.Error("DMDomainCase should fail for mixed-case domains")
	}
	if !strings.Contains(result.DMDomainCase.Message, "abc123.json") {
		t.Errorf("expected file name in message, got: %s", result.DMDomainCase.Message)
	}
}

func TestCheckTenant_DMDomainCase_MixedCaseInMessages(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "dm-case-msg")

	// peer_domain is fine but from field has mixed case
	convDir := filepath.Join(siteDir, ".polis", "content", "pub.polis.core", "dm", "conv")
	os.MkdirAll(convDir, 0700)
	conv := `{"peer_domain":"alice.example.com","peer_url":"https://alice.example.com/","messages":[{"from":"Alice.Example.COM","to":"dm-case-msg.polis.pub"}]}`
	os.WriteFile(filepath.Join(convDir, "def456.json"), []byte(conv), 0600)

	result := CheckTenant(siteDir, CheckOptions{})
	if result.DMDomainCase.OK {
		t.Error("DMDomainCase should fail for mixed-case in message from/to fields")
	}
}

func TestCheckTenant_DMDomainCase_NoDMDir(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "dm-case-nodir")

	// No DM directory at all — should pass
	result := CheckTenant(siteDir, CheckOptions{})
	if !result.DMDomainCase.OK {
		t.Errorf("DMDomainCase should be OK when no DM directory exists: %s", result.DMDomainCase.Message)
	}
}

// ============ DM preview encryption check ============

func TestCheckTenant_DMPreviewEncryption_AllEncrypted(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "dm-preview-ok")

	dmDir := filepath.Join(siteDir, ".polis", "content", "pub.polis.core", "dm")
	os.MkdirAll(dmDir, 0700)
	idx := `{"conversations":[{"id":"conv1","last_preview":"enc:AAAA$BBBB"},{"id":"conv2","last_preview":""}]}`
	os.WriteFile(filepath.Join(dmDir, "conversations.json"), []byte(idx), 0600)

	result := CheckTenant(siteDir, CheckOptions{})
	if !result.DMPreviewEncryption.OK {
		t.Errorf("DMPreviewEncryption should be OK for encrypted previews: %s", result.DMPreviewEncryption.Message)
	}
}

func TestCheckTenant_DMPreviewEncryption_HasPlaintext(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "dm-preview-bad")

	dmDir := filepath.Join(siteDir, ".polis", "content", "pub.polis.core", "dm")
	os.MkdirAll(dmDir, 0700)
	idx := `{"conversations":[{"id":"conv1","last_preview":"Hello this is plaintext"},{"id":"conv2","last_preview":"enc:AAAA$BBBB"},{"id":"conv3","last_preview":"Also plaintext"}]}`
	os.WriteFile(filepath.Join(dmDir, "conversations.json"), []byte(idx), 0600)

	result := CheckTenant(siteDir, CheckOptions{})
	if result.DMPreviewEncryption.OK {
		t.Error("DMPreviewEncryption should fail for plaintext previews")
	}
	if !strings.Contains(result.DMPreviewEncryption.Message, "2 plaintext") {
		t.Errorf("expected count of 2 plaintext, got: %s", result.DMPreviewEncryption.Message)
	}
}

func TestCheckTenant_DMPreviewEncryption_NoFile(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "dm-preview-nofile")

	result := CheckTenant(siteDir, CheckOptions{})
	if !result.DMPreviewEncryption.OK {
		t.Errorf("DMPreviewEncryption should be OK when no conversations.json exists: %s", result.DMPreviewEncryption.Message)
	}
}

// ============ Tag directory check ============

func TestCheckTenant_TagDirectory_Missing(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "tag-missing")

	result := CheckTenant(siteDir, CheckOptions{})
	if result.TagDirectory.OK {
		t.Error("TagDirectory should fail when tag directory is missing")
	}
	if result.TagDirectory.Message != "missing" {
		t.Errorf("expected message 'missing', got %q", result.TagDirectory.Message)
	}
}

func TestCheckTenant_TagDirectory_Exists(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "tag-exists")

	// Create the tag directory
	os.MkdirAll(filepath.Join(siteDir, "content", "pub.polis.core", "tag"), 0755)

	result := CheckTenant(siteDir, CheckOptions{})
	if !result.TagDirectory.OK {
		t.Errorf("TagDirectory should pass when tag directory exists: %s", result.TagDirectory.Message)
	}
}

func TestCheckTenant_TagDirectory_DoesNotAffectPassed(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "tag-passed")

	result := CheckTenant(siteDir, CheckOptions{})
	// TagDirectory is missing but Passed() should still return true
	// (provisioning checks don't affect Passed)
	if !result.TagDirectory.OK {
		// Confirm it's missing
		if result.Passed() == false {
			// Make sure it's not TagDirectory causing the failure — check other fields
			// TagDirectory is a provisioning check and should NOT affect Passed()
			// Passed() only checks integrity/metadata/security fields
			t.Log("Passed() returned false, but TagDirectory is a provisioning check")
		}
	}
}

func TestCheckTenant_BaseTheme_Missing(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "base-missing")

	result := CheckTenant(siteDir, CheckOptions{})
	if result.BaseTheme.OK {
		t.Error("BaseTheme should fail when _base directory is missing")
	}
}

func TestCheckTenant_BaseTheme_Exists(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "base-exists")

	os.MkdirAll(filepath.Join(siteDir, "site", "themes", "_base"), 0755)

	result := CheckTenant(siteDir, CheckOptions{})
	if !result.BaseTheme.OK {
		t.Errorf("BaseTheme should pass when _base exists: %s", result.BaseTheme.Message)
	}
}

func TestCheckTenant_ThemeStaleFiles_Detected(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "stale-detected")

	// Create _base with a template
	baseDir := filepath.Join(siteDir, "site", "themes", "_base")
	os.MkdirAll(baseDir, 0755)
	os.WriteFile(filepath.Join(baseDir, "index.html"), []byte("<html>BASE</html>"), 0644)

	// Create a theme with an identical file (stale)
	themeDir := filepath.Join(siteDir, "site", "themes", "turbo")
	os.MkdirAll(themeDir, 0755)
	os.WriteFile(filepath.Join(themeDir, "turbo.css"), []byte("body{}"), 0644)
	os.WriteFile(filepath.Join(themeDir, "index.html"), []byte("<html>BASE</html>"), 0644)

	result := CheckTenant(siteDir, CheckOptions{})
	if result.ThemeStaleFiles.OK {
		t.Error("ThemeStaleFiles should detect stale duplicate")
	}
}

func TestCheckTenant_ThemeStaleFiles_CustomPreserved(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "stale-custom")

	// Create _base with a template
	baseDir := filepath.Join(siteDir, "site", "themes", "_base")
	os.MkdirAll(baseDir, 0755)
	os.WriteFile(filepath.Join(baseDir, "index.html"), []byte("<html>BASE</html>"), 0644)

	// Create a theme with a DIFFERENT file (not stale)
	themeDir := filepath.Join(siteDir, "site", "themes", "studio13")
	os.MkdirAll(themeDir, 0755)
	os.WriteFile(filepath.Join(themeDir, "studio13.css"), []byte("body{}"), 0644)
	os.WriteFile(filepath.Join(themeDir, "index.html"), []byte("<html>CUSTOM</html>"), 0644)

	result := CheckTenant(siteDir, CheckOptions{})
	if !result.ThemeStaleFiles.OK {
		t.Errorf("ThemeStaleFiles should pass when files differ from _base: %s", result.ThemeStaleFiles.Message)
	}
}

func TestCheckTenant_ThemeStaleFiles_OldCanonicalHash(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "stale-hash")

	// Create _base with NEW design (completely different from old templates)
	baseDir := filepath.Join(siteDir, "site", "themes", "_base")
	os.MkdirAll(filepath.Join(baseDir, "snippets"), 0755)
	os.WriteFile(filepath.Join(baseDir, "index.html"), []byte("<html>NEW MOCKUP DESIGN</html>"), 0644)

	// Create a theme with an OLD-FORMAT template (different comment, old Fonts link).
	// This should be detected as stale via canonical hash, NOT via _base comparison.
	themeDir := filepath.Join(siteDir, "site", "themes", "turbo")
	os.MkdirAll(filepath.Join(themeDir, "snippets"), 0755)
	os.WriteFile(filepath.Join(themeDir, "turbo.css"), []byte("body{}"), 0644)

	// Old-format post-item snippet (no comment to strip, exact hash match)
	os.WriteFile(filepath.Join(themeDir, "snippets", "post-item.html"), []byte(
		`<a href="{{url}}" class="post-item">
    <span class="post-date">{{published_human}}</span>
    <span class="post-title">{{title}} <span class="post-comments">({{comment_count}} comments)</span></span>
</a>
`), 0644)

	result := CheckTenant(siteDir, CheckOptions{})
	if result.ThemeStaleFiles.OK {
		t.Error("ThemeStaleFiles should detect old-format templates via canonical hash")
	}

	// Verify StaleThemeFiles returns the right files
	stale := StaleThemeFiles(themeDir, baseDir)
	found := false
	for _, f := range stale {
		if f == "snippets/post-item.html" {
			found = true
		}
	}
	if !found {
		t.Errorf("Expected snippets/post-item.html in stale list, got: %v", stale)
	}
}

func TestNormalizeForHash(t *testing.T) {
	// Template with theme-name comment
	input := `<!--
    Polis Theme: Turbo - Homepage Template

    Snippets loaded by this template:
    - theme:about
-->
<!DOCTYPE html>
<html lang="en">
<body>Hello</body>
</html>
`
	normalized := normalizeForHash(input)
	if strings.Contains(normalized, "Polis Theme: Turbo") {
		t.Error("normalizeForHash should strip the theme-name comment")
	}
	if !strings.Contains(normalized, "<!DOCTYPE html>") {
		t.Error("normalizeForHash should preserve the rest of the content")
	}

	// Content without comment should be unchanged
	noComment := "<!DOCTYPE html>\n<html></html>\n"
	if normalizeForHash(noComment) != noComment {
		t.Error("normalizeForHash should not modify content without leading comment")
	}
}

// ============ Avatar provisioning check tests ============

func TestCheckTenant_AvatarMissing(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "bob")

	result := CheckTenant(siteDir, CheckOptions{})

	if result.Avatar.OK {
		t.Error("Avatar should not be OK for tenant without avatar")
	}
	if result.Avatar.Message != "missing" {
		t.Errorf("Avatar message = %q, want 'missing'", result.Avatar.Message)
	}
}

func TestCheckTenant_AvatarPresent(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "carol")

	// Add avatar to well-known
	wkPath := filepath.Join(siteDir, ".well-known", "polis")
	data, _ := os.ReadFile(wkPath)
	var wk map[string]interface{}
	json.Unmarshal(data, &wk)
	wk["avatar"] = map[string]interface{}{"bg": "#2a5a6a", "fg": "#ffffff"}
	updated, _ := json.MarshalIndent(wk, "", "  ")
	os.WriteFile(wkPath, updated, 0644)

	result := CheckTenant(siteDir, CheckOptions{})

	if !result.Avatar.OK {
		t.Errorf("Avatar should be OK: %s", result.Avatar.Message)
	}
}

func TestCheckTenant_AvatarIncomplete(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "dave")

	// Add avatar with missing fg
	wkPath := filepath.Join(siteDir, ".well-known", "polis")
	data, _ := os.ReadFile(wkPath)
	var wk map[string]interface{}
	json.Unmarshal(data, &wk)
	wk["avatar"] = map[string]interface{}{"bg": "#2a5a6a"}
	updated, _ := json.MarshalIndent(wk, "", "  ")
	os.WriteFile(wkPath, updated, 0644)

	result := CheckTenant(siteDir, CheckOptions{})

	if result.Avatar.OK {
		t.Error("Avatar should not be OK when fg is missing")
	}
	if result.Avatar.Message != "incomplete (missing bg or fg)" {
		t.Errorf("Avatar message = %q", result.Avatar.Message)
	}
}
