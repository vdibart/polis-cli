package judge

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"time"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// ---------- Test helpers ----------

// setupValidTenant creates a minimal valid tenant site in a temp directory.
func setupValidTenant(t *testing.T, name string) (string, []byte, []byte) {
	t.Helper()

	base := t.TempDir()
	siteDir := filepath.Join(base, name)

	privKey, pubKey, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	for _, dir := range []string{
		".well-known",
		filepath.Join(".polis", "keys"),
		filepath.Join("content", "pub.polis.core", "post"),
		filepath.Join("content", "pub.polis.core", "comment"),
	} {
		if err := os.MkdirAll(filepath.Join(siteDir, dir), 0755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}

	wk := map[string]interface{}{
		"public_key": strings.TrimSpace(string(pubKey)),
	}
	wkData, _ := json.MarshalIndent(wk, "", "  ")
	writeTestFile(t, filepath.Join(siteDir, ".well-known", "polis"), wkData, 0644)
	writeTestFile(t, filepath.Join(siteDir, ".polis", "keys", "id_ed25519"), privKey, 0600)
	writeTestFile(t, filepath.Join(siteDir, ".polis", "keys", "id_ed25519.pub"), pubKey, 0644)

	return siteDir, pubKey, privKey
}

// createSignedPost creates a signed post and returns the relative path.
func createSignedPost(t *testing.T, siteDir string, privKey, pubKey []byte, title, body string) string {
	t.Helper()

	canonical := canonicalizeContent(body)
	hash := sha256Hex([]byte(canonical))
	timestamp := "2026-01-15T10:00:00Z"

	unsignedFM := fmt.Sprintf("---\ntitle: %s\npublished: %s\ngenerator: judge-test\ncurrent-version: sha256:%s\nversion-history:\n  - sha256:%s (%s)\n---", title, timestamp, hash, hash, timestamp)

	fullUnsigned := unsignedFM + "\n\n" + canonical
	canonicalForSigning := canonicalizeContent(fullUnsigned)

	sig, err := signing.SignContent([]byte(canonicalForSigning), privKey)
	if err != nil {
		t.Fatalf("SignContent: %v", err)
	}

	sigBase64 := extractSigBase64(sig)

	finalFM := fmt.Sprintf("---\ntitle: %s\npublished: %s\ngenerator: judge-test\ncurrent-version: sha256:%s\nversion-history:\n  - sha256:%s (%s)\nsignature: %s\n---", title, timestamp, hash, hash, timestamp, sigBase64)
	finalContent := finalFM + "\n\n" + canonical

	dateDir := "20260115"
	postsDir := filepath.Join(siteDir, "content", "pub.polis.core", "post", dateDir)
	os.MkdirAll(postsDir, 0755)

	slug := strings.ToLower(strings.ReplaceAll(title, " ", "-"))
	postPath := filepath.Join(postsDir, slug+".md")
	writeTestFile(t, postPath, []byte(finalContent), 0644)

	return filepath.Join("content", "pub.polis.core", "post", dateDir, slug+".md")
}

// createSignedComment creates a signed comment file and returns the relative path.
func createSignedComment(t *testing.T, siteDir string, privKey []byte, authorDomain, commentID, body string) string {
	t.Helper()

	canonical := canonicalizeContent(body)
	hash := sha256Hex([]byte(canonical))
	timestamp := "2026-01-15T10:00:00Z"

	// Sign without author field (matches real comment signing behavior)
	unsignedFM := fmt.Sprintf("---\ntitle: Re: test\ntype: comment\npublished: %s\ngenerator: judge-test\nin-reply-to:\n  url: https://example.com/posts/test.md\n  root-post: https://example.com/posts/test.md\ncurrent-version: sha256:%s\nversion-history:\n  - sha256:%s (%s)\n---", timestamp, hash, hash, timestamp)

	fullUnsigned := unsignedFM + "\n\n" + canonical
	canonicalForSigning := canonicalizeContent(fullUnsigned)

	sig, err := signing.SignContent([]byte(canonicalForSigning), privKey)
	if err != nil {
		t.Fatalf("SignContent: %v", err)
	}

	sigBase64 := extractSigBase64(sig)

	// Final file includes author (added after signing) and signature
	finalFM := fmt.Sprintf("---\ntitle: Re: test\ntype: comment\npublished: %s\nauthor: %s\ngenerator: judge-test\nin-reply-to:\n  url: https://example.com/posts/test.md\n  root-post: https://example.com/posts/test.md\ncurrent-version: sha256:%s\nversion-history:\n  - sha256:%s (%s)\nsignature: %s\n---", timestamp, authorDomain, hash, hash, timestamp, sigBase64)
	finalContent := finalFM + "\n\n" + canonical

	dateDir := "20260115"
	commentsDir := filepath.Join(siteDir, "content", "pub.polis.core", "comment", dateDir)
	os.MkdirAll(commentsDir, 0755)

	commentPath := filepath.Join(commentsDir, commentID+".md")
	writeTestFile(t, commentPath, []byte(finalContent), 0644)

	return filepath.Join("content", "pub.polis.core", "comment", dateDir, commentID+".md")
}

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

func writeTestFile(t *testing.T, path string, data []byte, perm os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, perm); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func createIndexEntry(path, title, hash string) metadata.IndexEntry {
	return metadata.IndexEntry{
		Type:           "post",
		Path:           path,
		Title:          title,
		Published:      "2026-01-15T10:00:00Z",
		CurrentVersion: "sha256:" + hash,
	}
}

// ---------- Item 1: HTTP content verification ----------

func TestCheckHTTPContent_Valid(t *testing.T) {
	siteDir, pubKey, privKey := setupValidTenant(t, "alice")
	relPath := createSignedPost(t, siteDir, privKey, pubKey, "Hello", "# Hello\n\nWorld.\n")

	// Serve the post file via HTTP — path is just /content/... (Host header has subdomain)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filePath := filepath.Join(siteDir, strings.TrimPrefix(r.URL.Path, "/"))
		data, err := os.ReadFile(filePath)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	defer server.Close()

	status, checked, failed := checkHTTPContent(siteDir, "alice", server.URL, "test.local", nil)
	if !status.OK {
		t.Errorf("expected OK, got: %s", status.Message)
	}
	if checked != 1 {
		t.Errorf("expected 1 post checked, got %d", checked)
	}
	if failed != 0 {
		t.Errorf("expected 0 failed, got %d", failed)
	}
	_ = relPath
}

func TestCheckHTTPContent_Tampered(t *testing.T) {
	siteDir, pubKey, privKey := setupValidTenant(t, "alice")
	createSignedPost(t, siteDir, privKey, pubKey, "Hello", "# Hello\n\nWorld.\n")

	// Serve tampered content — path is just /content/... (Host header has subdomain)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filePath := filepath.Join(siteDir, strings.TrimPrefix(r.URL.Path, "/"))
		data, err := os.ReadFile(filePath)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		// Tamper with body
		tampered := strings.Replace(string(data), "World.", "TAMPERED.", 1)
		w.Write([]byte(tampered))
	}))
	defer server.Close()

	status, _, failed := checkHTTPContent(siteDir, "alice", server.URL, "test.local", nil)
	if status.OK {
		t.Error("expected failure for tampered content")
	}
	if failed != 1 {
		t.Errorf("expected 1 failed, got %d", failed)
	}
}

func TestCheckHTTPContent_NoBaseURL(t *testing.T) {
	status, checked, _ := checkHTTPContent("/nonexistent", "alice", "", "", nil)
	if !status.OK {
		t.Error("expected OK when no base URL")
	}
	if checked != 0 {
		t.Error("expected 0 checked")
	}
}

// ---------- Item 3: Key continuity ----------

func TestCheckKeyContinuity_FirstRun(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "alice")
	stateDir := t.TempDir()

	status := checkKeyContinuity(siteDir, "alice", stateDir)
	if !status.OK {
		t.Errorf("expected OK on first run, got: %s", status.Message)
	}
	if status.Message != "baseline recorded" {
		t.Errorf("expected 'baseline recorded', got: %s", status.Message)
	}

	// Verify baseline file was created
	baselinePath := filepath.Join(stateDir, "alice", "key_baseline.json")
	if _, err := os.Stat(baselinePath); os.IsNotExist(err) {
		t.Error("baseline file not created")
	}
}

func TestCheckKeyContinuity_Unchanged(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "alice")
	stateDir := t.TempDir()

	// First run
	checkKeyContinuity(siteDir, "alice", stateDir)

	// Second run (same key)
	status := checkKeyContinuity(siteDir, "alice", stateDir)
	if !status.OK {
		t.Errorf("expected OK for unchanged key, got: %s", status.Message)
	}
}

func TestCheckKeyContinuity_Changed(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "alice")
	stateDir := t.TempDir()

	// First run
	checkKeyContinuity(siteDir, "alice", stateDir)

	// Change the public key
	_, newPubKey, _ := signing.GenerateKeypair()
	wk := map[string]interface{}{
		"public_key": strings.TrimSpace(string(newPubKey)),
	}
	wkData, _ := json.MarshalIndent(wk, "", "  ")
	os.WriteFile(filepath.Join(siteDir, ".well-known", "polis"), wkData, 0644)

	// Second run (different key)
	status := checkKeyContinuity(siteDir, "alice", stateDir)
	if status.OK {
		t.Error("expected failure for changed key")
	}
	if !strings.Contains(status.Message, "key changed") {
		t.Errorf("expected 'key changed' in message, got: %s", status.Message)
	}
}

func TestCheckKeyContinuity_NoStateDir(t *testing.T) {
	status := checkKeyContinuity("/nonexistent", "alice", "")
	if !status.OK {
		t.Error("expected OK when no state dir")
	}
}

// ---------- Item 4: Policy snapshot ----------

func TestCheckPolicySnapshot_FirstRun(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "alice")
	stateDir := t.TempDir()

	// Create a policy file
	policyDir := filepath.Join(siteDir, "policies")
	os.MkdirAll(policyDir, 0755)
	os.WriteFile(filepath.Join(policyDir, "rules.jsonl"), []byte(`{"active":true,"policy":"allow all from following"}`+"\n"), 0644)

	status := checkPolicySnapshot(siteDir, "alice", stateDir)
	if !status.OK {
		t.Errorf("expected OK on first run, got: %s", status.Message)
	}
	if status.Message != "snapshot recorded" {
		t.Errorf("expected 'snapshot recorded', got: %s", status.Message)
	}
}

func TestCheckPolicySnapshot_Unchanged(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "alice")
	stateDir := t.TempDir()

	policyDir := filepath.Join(siteDir, "policies")
	os.MkdirAll(policyDir, 0755)
	os.WriteFile(filepath.Join(policyDir, "rules.jsonl"), []byte(`{"active":true,"policy":"allow all from following"}`+"\n"), 0644)

	checkPolicySnapshot(siteDir, "alice", stateDir)
	status := checkPolicySnapshot(siteDir, "alice", stateDir)
	if !status.OK {
		t.Errorf("expected OK for unchanged policy, got: %s", status.Message)
	}
}

func TestCheckPolicySnapshot_Changed(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "alice")
	stateDir := t.TempDir()

	policyDir := filepath.Join(siteDir, "policies")
	os.MkdirAll(policyDir, 0755)
	os.WriteFile(filepath.Join(policyDir, "rules.jsonl"), []byte(`{"active":true,"policy":"allow all from following"}`+"\n"), 0644)

	// First run
	checkPolicySnapshot(siteDir, "alice", stateDir)

	// Change policy
	os.WriteFile(filepath.Join(policyDir, "rules.jsonl"), []byte(`{"active":true,"policy":"deny all from all"}`+"\n"), 0644)

	// Second run
	status := checkPolicySnapshot(siteDir, "alice", stateDir)
	if status.OK {
		t.Error("expected failure for changed policy")
	}
	if !strings.Contains(status.Message, "changed") {
		t.Errorf("expected 'changed' in message, got: %s", status.Message)
	}
}

// ---------- Item 5: Index consistency ----------

func TestCheckIndexConsistency_Valid(t *testing.T) {
	siteDir, pubKey, privKey := setupValidTenant(t, "alice")
	relPath := createSignedPost(t, siteDir, privKey, pubKey, "Hello", "# Hello\n\nWorld.\n")

	// Compute the hash as the index would store it
	body := "# Hello\n\nWorld.\n"
	canonical := canonicalizeContent(body)
	hash := sha256Hex([]byte(canonical))

	entry := createIndexEntry(relPath, "Hello", hash)
	metadata.AppendToPublicIndex(siteDir, &entry)

	status := checkIndexConsistency(siteDir, "alice")
	if !status.OK {
		t.Errorf("expected OK, got: %s", status.Message)
	}
}

func TestCheckIndexConsistency_Orphan(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "alice")

	// Create index entry pointing to nonexistent file
	entry := createIndexEntry("content/pub.polis.core/post/20260115/gone.md", "Gone", "abc123")
	metadata.AppendToPublicIndex(siteDir, &entry)

	status := checkIndexConsistency(siteDir, "alice")
	if status.OK {
		t.Error("expected failure for orphan entry")
	}
	if !strings.Contains(status.Message, "orphan") {
		t.Errorf("expected 'orphan' in message, got: %s", status.Message)
	}
}

func TestCheckIndexConsistency_Phantom(t *testing.T) {
	siteDir, pubKey, privKey := setupValidTenant(t, "alice")
	createSignedPost(t, siteDir, privKey, pubKey, "Hello", "# Hello\n\nWorld.\n")

	// Add a different post to the index so it's non-empty, but not the one on disk
	otherEntry := createIndexEntry("content/pub.polis.core/post/20260115/other.md", "Other", "abc123")
	metadata.AppendToPublicIndex(siteDir, &otherEntry)

	status := checkIndexConsistency(siteDir, "alice")
	if status.OK {
		t.Error("expected failure for phantom file")
	}
	if !strings.Contains(status.Message, "phantom") {
		t.Errorf("expected 'phantom' in message, got: %s", status.Message)
	}
	_ = pubKey
}

func TestCheckIndexConsistency_HashMismatch(t *testing.T) {
	siteDir, pubKey, privKey := setupValidTenant(t, "alice")
	relPath := createSignedPost(t, siteDir, privKey, pubKey, "Hello", "# Hello\n\nWorld.\n")

	// Create index entry with wrong hash
	entry := createIndexEntry(relPath, "Hello", "0000000000000000000000000000000000000000000000000000000000000000")
	metadata.AppendToPublicIndex(siteDir, &entry)

	status := checkIndexConsistency(siteDir, "alice")
	if status.OK {
		t.Error("expected failure for hash mismatch")
	}
	if !strings.Contains(status.Message, "hash_mismatch") {
		t.Errorf("expected 'hash_mismatch' in message, got: %s", status.Message)
	}
}

func TestCheckIndexConsistency_NoIndex(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "alice")
	status := checkIndexConsistency(siteDir, "alice")
	if !status.OK {
		t.Errorf("expected OK with no index, got: %s", status.Message)
	}
}

// ---------- Item 6: Cross-site comment verification ----------

func TestCheckCrossSiteComments_Valid(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "alice")

	// Generate a keypair for the comment author
	authorPrivKey, authorPubKey, _ := signing.GenerateKeypair()

	// Set up a mock server for the author's .well-known/polis
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wk := map[string]interface{}{
			"public_key": strings.TrimSpace(string(authorPubKey)),
		}
		json.NewEncoder(w).Encode(wk)
	}))
	defer server.Close()

	// Extract the server's host for use as domain
	domain := strings.TrimPrefix(server.URL, "https://")

	// Create a signed comment using the author's key
	createSignedComment(t, siteDir, authorPrivKey, domain, "comment-1", "Great post!\n")

	// Use the test server's client (trusts its TLS cert)
	status, checked, failed := checkCrossSiteCommentsWithClient(siteDir, "alice", server.Client(), domain, authorPubKey)
	if !status.OK {
		t.Errorf("expected OK, got: %s", status.Message)
	}
	if checked != 1 {
		t.Errorf("expected 1 checked, got %d", checked)
	}
	if failed != 0 {
		t.Errorf("expected 0 failed, got %d", failed)
	}
}

func TestCheckCrossSiteComments_InvalidSignature(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "alice")

	// Generate two different keypairs
	authorPrivKey, _, _ := signing.GenerateKeypair()
	_, wrongPubKey, _ := signing.GenerateKeypair()

	domain := "author.example.com"
	createSignedComment(t, siteDir, authorPrivKey, domain, "comment-1", "Great post!\n")

	// Verify with wrong public key
	status, checked, failed := checkCrossSiteCommentsWithClient(siteDir, "alice", nil, domain, wrongPubKey)
	if status.OK {
		t.Error("expected failure for wrong key")
	}
	if checked != 1 {
		t.Errorf("expected 1 checked, got %d", checked)
	}
	if failed != 1 {
		t.Errorf("expected 1 failed, got %d", failed)
	}
}

func TestCheckCrossSiteComments_NoComments(t *testing.T) {
	siteDir, _, _ := setupValidTenant(t, "alice")
	// Remove comment dir
	os.RemoveAll(filepath.Join(siteDir, "content", "pub.polis.core", "comment"))

	status, checked, _ := checkCrossSiteComments(siteDir, "alice", 5*time.Second, "", "", "", nil)
	if !status.OK {
		t.Errorf("expected OK with no comments, got: %s", status.Message)
	}
	if checked != 0 {
		t.Error("expected 0 checked")
	}
}

// checkCrossSiteCommentsWithClient is a test helper that bypasses HTTP fetching
// by providing the public key directly, simulating what the real function does.
func checkCrossSiteCommentsWithClient(siteDir, handle string, client *http.Client, domain string, pubKey []byte) (CheckStatus, int, int) {
	commentsDir := filepath.Join(siteDir, "content", "pub.polis.core", "comment")
	if _, err := os.Stat(commentsDir); os.IsNotExist(err) {
		return CheckStatus{OK: true, Message: "no comments"}, 0, 0
	}

	keyCache := map[string]string{
		domain: strings.TrimSpace(string(pubKey)),
	}
	var checked, failed int

	filepath.WalkDir(commentsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		content := string(data)
		fm := parseCommentFrontmatter(content)
		if fm.Author == "" || fm.Signature == "" {
			return nil
		}

		checked++

		authorDomain := extractDomain(fm.Author)
		pk, ok := keyCache[authorDomain]
		if !ok || pk == "" {
			return nil
		}

		sshSig := reconstructSSHSignature(fm.Signature)
		contentToSign := extractCommentContentToSign(content)
		valid, verifyErr := signing.VerifySignature([]byte(contentToSign), []byte(pk), sshSig)
		if verifyErr != nil || !valid {
			failed++
		}

		return nil
	})

	if failed > 0 {
		return CheckStatus{OK: false, Message: fmt.Sprintf("%d/%d comments failed", failed, checked)}, checked, failed
	}
	return CheckStatus{OK: true}, checked, failed
}

// ---------- Sweep tests ----------

func TestJudgeAll_MultiTenant(t *testing.T) {
	base := t.TempDir()
	tenantsDir := filepath.Join(base, "tenants")
	os.MkdirAll(tenantsDir, 0755)

	// Create two tenants — one valid, one with issues
	siteDir1 := filepath.Join(tenantsDir, "alice")
	privKey1, pubKey1, _ := signing.GenerateKeypair()
	setupTenantAt(t, siteDir1, privKey1, pubKey1)
	createSignedPostAt(t, siteDir1, privKey1, pubKey1, "Hello", "# Hello\n\nWorld.\n")

	siteDir2 := filepath.Join(tenantsDir, "bob")
	privKey2, pubKey2, _ := signing.GenerateKeypair()
	setupTenantAt(t, siteDir2, privKey2, pubKey2)

	stateDir := t.TempDir()
	opts := Options{StateDir: stateDir}

	sweep := JudgeAll(tenantsDir, opts)
	if sweep.Tenants != 2 {
		t.Errorf("expected 2 tenants, got %d", sweep.Tenants)
	}
	if sweep.DurationMs < 0 {
		t.Error("duration should be non-negative")
	}
}

func TestJudgeAll_Empty(t *testing.T) {
	tenantsDir := t.TempDir()
	sweep := JudgeAll(tenantsDir, Options{})
	if sweep.Tenants != 0 {
		t.Errorf("expected 0 tenants, got %d", sweep.Tenants)
	}
}

func TestJudgeTenant_AllChecks(t *testing.T) {
	siteDir, pubKey, privKey := setupValidTenant(t, "alice")
	createSignedPost(t, siteDir, privKey, pubKey, "Hello", "# Hello\n\nWorld.\n")

	stateDir := t.TempDir()

	result := JudgeTenant(siteDir, "alice", Options{StateDir: stateDir})
	if result.Handle != "alice" {
		t.Errorf("expected handle 'alice', got '%s'", result.Handle)
	}
	if result.DurationMs < 0 {
		t.Error("duration should be non-negative")
	}
	// Key continuity should pass (first run = baseline)
	if !result.KeyContinuity.OK {
		t.Errorf("key continuity should pass: %s", result.KeyContinuity.Message)
	}
	// Policy snapshot should pass (no policies = nothing to snapshot, or first run)
	if !result.PolicySnapshot.OK {
		t.Errorf("policy snapshot should pass: %s", result.PolicySnapshot.Message)
	}
}

// ---------- Helper functions for multi-tenant tests ----------

func setupTenantAt(t *testing.T, siteDir string, privKey, pubKey []byte) {
	t.Helper()

	for _, dir := range []string{
		".well-known",
		filepath.Join(".polis", "keys"),
		filepath.Join("content", "pub.polis.core", "post"),
		filepath.Join("content", "pub.polis.core", "comment"),
	} {
		os.MkdirAll(filepath.Join(siteDir, dir), 0755)
	}

	wk := map[string]interface{}{
		"public_key": strings.TrimSpace(string(pubKey)),
	}
	wkData, _ := json.MarshalIndent(wk, "", "  ")
	writeTestFile(t, filepath.Join(siteDir, ".well-known", "polis"), wkData, 0644)
	writeTestFile(t, filepath.Join(siteDir, ".polis", "keys", "id_ed25519"), privKey, 0600)
	writeTestFile(t, filepath.Join(siteDir, ".polis", "keys", "id_ed25519.pub"), pubKey, 0644)
}

func createSignedPostAt(t *testing.T, siteDir string, privKey, pubKey []byte, title, body string) string {
	t.Helper()

	canonical := canonicalizeContent(body)
	h := sha256.Sum256([]byte(canonical))
	hash := fmt.Sprintf("%x", h)
	timestamp := "2026-01-15T10:00:00Z"

	unsignedFM := fmt.Sprintf("---\ntitle: %s\npublished: %s\ngenerator: judge-test\ncurrent-version: sha256:%s\n---", title, timestamp, hash)
	fullUnsigned := unsignedFM + "\n\n" + canonical
	canonicalForSigning := canonicalizeContent(fullUnsigned)

	sig, err := signing.SignContent([]byte(canonicalForSigning), privKey)
	if err != nil {
		t.Fatalf("SignContent: %v", err)
	}

	sigBase64 := extractSigBase64(sig)
	finalFM := fmt.Sprintf("---\ntitle: %s\npublished: %s\ngenerator: judge-test\ncurrent-version: sha256:%s\nsignature: %s\n---", title, timestamp, hash, sigBase64)
	finalContent := finalFM + "\n\n" + canonical

	postsDir := filepath.Join(siteDir, "content", "pub.polis.core", "post", "20260115")
	os.MkdirAll(postsDir, 0755)

	slug := strings.ToLower(strings.ReplaceAll(title, " ", "-"))
	postPath := filepath.Join(postsDir, slug+".md")
	writeTestFile(t, postPath, []byte(finalContent), 0644)

	return filepath.Join("content", "pub.polis.core", "post", "20260115", slug+".md")
}

// ---------- Unit tests for helpers ----------

func TestExtractDomain(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"https://alice.polis.pub", "alice.polis.pub"},
		{"https://alice.polis.pub/posts/hello.md", "alice.polis.pub"},
		{"http://localhost:8080/path", "localhost:8080"},
		{"alice.polis.pub", "alice.polis.pub"},
	}

	for _, tt := range tests {
		got := extractDomain(tt.input)
		if got != tt.expected {
			t.Errorf("extractDomain(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestExtractLocalHandle(t *testing.T) {
	tests := []struct {
		domain, base, expected string
	}{
		{"alice.polis.pub", "polis.pub", "alice"},
		{"bob.polis.pub", "polis.pub", "bob"},
		{"polis.pub", "polis.pub", ""},             // root domain, no handle
		{"alice.other.com", "polis.pub", ""},        // different base
		{"alice.sub.polis.pub", "polis.pub", ""},    // nested subdomain
		{"alice.polis.pub", "", ""},                  // empty base
	}
	for _, tt := range tests {
		got := extractLocalHandle(tt.domain, tt.base)
		if got != tt.expected {
			t.Errorf("extractLocalHandle(%q, %q) = %q, want %q", tt.domain, tt.base, got, tt.expected)
		}
	}
}

func TestSha256Fingerprint(t *testing.T) {
	fp := sha256Fingerprint("test")
	if len(fp) != 64 {
		t.Errorf("expected 64 char hex fingerprint, got %d", len(fp))
	}
}

func TestVerifyHash(t *testing.T) {
	content := "# Hello\n\nWorld.\n"
	canonical := canonicalizeContent(content)
	hash := sha256Hex([]byte(canonical))

	if !verifyHash(content, hash) {
		t.Error("expected hash to verify")
	}
	if verifyHash(content, "0000000000000000000000000000000000000000000000000000000000000000") {
		t.Error("expected wrong hash to fail")
	}
}

// ---------- Co-tenant HTTP fetch tests ----------

func TestFetchCoTenantPublicKey(t *testing.T) {
	// Set up a mock server that serves .well-known/polis based on Host header
	testKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKey"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/polis" {
			http.NotFound(w, r)
			return
		}
		// Route by Host header
		host := r.Host
		if !strings.HasSuffix(host, ".test.local") {
			http.Error(w, "unknown host", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"public_key": testKey,
		})
	}))
	defer server.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	pubKey, err := fetchCoTenantPublicKey(client, server.URL, "alice", "test.local")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pubKey != testKey {
		t.Errorf("expected %q, got %q", testKey, pubKey)
	}
}

func TestFetchCoTenantPublicKey_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	_, err := fetchCoTenantPublicKey(client, server.URL, "nobody", "test.local")
	if err == nil {
		t.Error("expected error for 404 response")
	}
}

func TestCheckCrossSiteComments_CoTenantHTTP(t *testing.T) {
	// Set up a tenant with a cross-site comment from a co-tenant
	siteDir, _, _ := setupValidTenant(t, "bob")

	// Create a comment that appears to be from alice.test.local
	_, alicePubKey, _ := signing.GenerateKeypair()
	commentDir := filepath.Join(siteDir, "content", "pub.polis.core", "comment", "20250101")
	os.MkdirAll(commentDir, 0755)
	// No signature — just test the routing logic (key fetch, not signature verify)
	commentContent := fmt.Sprintf("---\nauthor: https://alice.test.local/\nsignature: none\n---\nHello from alice\n")
	os.WriteFile(filepath.Join(commentDir, "test.md"), []byte(commentContent), 0644)

	// Mock server that serves alice's public key
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/polis" && strings.HasPrefix(r.Host, "alice.") {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"public_key": strings.TrimSpace(string(alicePubKey)),
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	// With baseURL set, should use HTTP instead of disk
	status, checked, _ := checkCrossSiteComments(siteDir, "bob", 5*time.Second, "test.local", "", server.URL, client)
	if checked != 1 {
		t.Errorf("expected 1 comment checked, got %d", checked)
	}
	// The comment will fail signature verification (signature is "none"),
	// but the key fetch should succeed via HTTP
	_ = status
}
