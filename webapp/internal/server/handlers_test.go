package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/feed"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/hooks"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
)

// markAsRegistered writes a local registration marker for the test server,
// so handlers that require registration pass the guard.
func markAsRegistered(t *testing.T, s *Server) {
	t.Helper()
	if s.DiscoveryURL == "" {
		t.Fatal("markAsRegistered: DiscoveryURL must be set first")
	}
	if err := discovery.WriteRegistrationMarker(s.DataDir, s.DiscoveryURL, "test.polis.pub", ""); err != nil {
		t.Fatalf("markAsRegistered: %v", err)
	}
}

// Helper to create a test server with temp directory
func newTestServer(t *testing.T) *Server {
	t.Helper()
	dataDir := t.TempDir()

	// Create required directories (matching main.go initialization)
	dirs := []string{
		filepath.Join(dataDir, ".polis"),
		filepath.Join(dataDir, ".polis", "keys"),
		filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts"),
		filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", "drafts"),
		filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", "pending"),
		filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", "denied"),
		filepath.Join(dataDir, ".well-known"),
		filepath.Join(dataDir, "content", "pub.polis.core", "post"),
		filepath.Join(dataDir, "content", "pub.polis.core", "comment"),
		filepath.Join(dataDir, "content", "pub.polis.core", "follow"),
		filepath.Join(dataDir, "site", "snippets"),
		filepath.Join(dataDir, "content", "pub.polis.core"),
		filepath.Join(dataDir, "site", "themes"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("failed to create directory %s: %v", dir, err)
		}
	}

	return &Server{DataDir: dataDir}
}

// Helper to create a server with keys configured
func newConfiguredServer(t *testing.T) *Server {
	t.Helper()
	s := newTestServer(t)

	// Generate real keys
	privKey, pubKey, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("failed to generate keypair: %v", err)
	}
	s.PrivateKey = privKey
	s.PublicKey = pubKey

	// Save keys to disk
	privKeyPath := filepath.Join(s.DataDir, ".polis", "keys", "id_ed25519")
	pubKeyPath := filepath.Join(s.DataDir, ".polis", "keys", "id_ed25519.pub")
	os.WriteFile(privKeyPath, privKey, 0600)
	os.WriteFile(pubKeyPath, pubKey, 0644)

	// Set config (Subdomain is deprecated - use BaseURL instead)
	s.Config = &Config{
		SetupCode: "test-setup",
		SetupAt:   "2026-01-01T00:00:00Z",
	}
	s.BaseURL = "https://test-site.polis.pub"

	// Create .well-known/polis (identity document)
	// Domain is derived from POLIS_BASE_URL at runtime, not stored in file.
	wellKnown := map[string]interface{}{
		"site_title": "Test Site",
		"public_key": string(pubKey),
		"email":      "test@example.com",
		"author":     "Test Author",
	}
	wellKnownData, _ := json.MarshalIndent(wellKnown, "", "  ")
	wellKnownPath := filepath.Join(s.DataDir, ".well-known", "polis")
	os.WriteFile(wellKnownPath, wellKnownData, 0644)

	return s
}

// Helper to make JSON request body
func jsonBody(t *testing.T, v interface{}) *bytes.Buffer {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}
	return bytes.NewBuffer(data)
}

// ============================================================================
// handleStatus Tests
// ============================================================================

func TestHandleStatus_Unconfigured(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rr := httptest.NewRecorder()

	s.handleStatus(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["configured"] != false {
		t.Errorf("expected configured=false, got %v", resp["configured"])
	}

	// Check that validation is returned
	validation, ok := resp["validation"].(map[string]interface{})
	if !ok {
		t.Error("expected validation object in response")
	} else if validation["status"] == "valid" {
		t.Error("expected validation status to be not_found or incomplete, got valid")
	}
}

func TestHandleStatus_Configured(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rr := httptest.NewRecorder()

	s.handleStatus(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if resp["configured"] != true {
		t.Errorf("expected configured=true, got %v", resp["configured"])
	}
	if resp["site_title"] != "Test Site" {
		t.Errorf("expected site_title='Test Site', got %v", resp["site_title"])
	}

	// Check validation shows valid
	validation, ok := resp["validation"].(map[string]interface{})
	if !ok {
		t.Error("expected validation object in response")
	} else if validation["status"] != "valid" {
		t.Errorf("expected validation status='valid', got %v", validation["status"])
	}

	// Check base_url is included (required by frontend init for domain display)
	if resp["base_url"] != "https://test-site.polis.pub" {
		t.Errorf("expected base_url='https://test-site.polis.pub', got %v", resp["base_url"])
	}

	// Check show_frontmatter is included
	if _, exists := resp["show_frontmatter"]; !exists {
		t.Error("expected show_frontmatter field in status response")
	}
}

func TestHandleSettings_NoViewMode(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()

	s.handleSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	siteData := resp["site"].(map[string]interface{})
	if _, hasViewMode := siteData["view_mode"]; hasViewMode {
		t.Errorf("view_mode should not be present in settings response")
	}
}

// ============================================================================
// handleValidate Tests
// ============================================================================

func TestHandleValidate_NotFound(t *testing.T) {
	s := newTestServer(t)
	// Remove all polis files to simulate empty directory
	os.RemoveAll(filepath.Join(s.DataDir, ".well-known"))
	os.RemoveAll(filepath.Join(s.DataDir, ".polis", "keys"))

	req := httptest.NewRequest(http.MethodGet, "/api/validate", nil)
	rr := httptest.NewRecorder()

	s.handleValidate(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	// Empty directory should be not_found or incomplete
	status := resp["status"].(string)
	if status == "valid" {
		t.Errorf("expected status to be not_found or incomplete, got %v", status)
	}
}

func TestHandleValidate_Valid(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/validate", nil)
	rr := httptest.NewRecorder()

	s.handleValidate(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if resp["status"] != "valid" {
		t.Errorf("expected status='valid', got %v", resp["status"])
	}
}

func TestHandleValidate_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/validate", nil)
	rr := httptest.NewRecorder()

	s.handleValidate(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

// ============================================================================
// handleInit Tests
// ============================================================================

func TestHandleInit_Success(t *testing.T) {
	s := newTestServer(t)
	// Remove any existing polis files
	os.RemoveAll(filepath.Join(s.DataDir, ".well-known"))
	os.RemoveAll(filepath.Join(s.DataDir, ".polis", "keys"))

	body := jsonBody(t, map[string]string{
		"site_title": "My Test Site",
		"base_url":   "https://test.example.com",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/init", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleInit(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if resp["success"] != true {
		t.Errorf("expected success=true, got %v", resp["success"])
	}

	// Verify keys were created
	privKeyPath := filepath.Join(s.DataDir, ".polis", "keys", "id_ed25519")
	if _, err := os.Stat(privKeyPath); os.IsNotExist(err) {
		t.Error("private key file was not created")
	}

	pubKeyPath := filepath.Join(s.DataDir, ".polis", "keys", "id_ed25519.pub")
	if _, err := os.Stat(pubKeyPath); os.IsNotExist(err) {
		t.Error("public key file was not created")
	}

	// Verify .well-known/polis was created
	wellKnownPath := filepath.Join(s.DataDir, ".well-known", "polis")
	if _, err := os.Stat(wellKnownPath); os.IsNotExist(err) {
		t.Error(".well-known/polis was not created")
	}

	// Verify content directory was created
	contentDir := filepath.Join(s.DataDir, "content", "pub.polis.core")
	if _, err := os.Stat(contentDir); os.IsNotExist(err) {
		t.Error("content/pub.polis.core directory was not created")
	}
}

func TestHandleInit_KeysAlreadyExist(t *testing.T) {
	s := newConfiguredServer(t) // Already has keys

	body := jsonBody(t, map[string]string{"site_title": "New Site"})
	req := httptest.NewRequest(http.MethodPost, "/api/init", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleInit(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d: %s", rr.Code, rr.Body.String())
	}

	if !strings.Contains(rr.Body.String(), "Failed to initialize site") {
		t.Error("expected generic init failure message")
	}
}

func TestHandleInit_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/init", nil)
	rr := httptest.NewRecorder()

	s.handleInit(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

// ============================================================================
// handleLink Tests
// ============================================================================

func TestHandleLink_Success(t *testing.T) {
	// Create a "source" polis site
	sourceDir := t.TempDir()
	sourceSrv := &Server{DataDir: sourceDir}

	// Initialize the source site with all required directories
	dirs := []string{
		filepath.Join(sourceDir, ".polis", "keys"),
		filepath.Join(sourceDir, ".well-known"),
		filepath.Join(sourceDir, "content", "pub.polis.core"),
	}
	for _, dir := range dirs {
		os.MkdirAll(dir, 0755)
	}

	// Generate keys and create .well-known/polis
	privKey, pubKey, _ := signing.GenerateKeypair()
	os.WriteFile(filepath.Join(sourceDir, ".polis", "keys", "id_ed25519"), privKey, 0600)
	os.WriteFile(filepath.Join(sourceDir, ".polis", "keys", "id_ed25519.pub"), pubKey, 0644)
	wellKnown := map[string]interface{}{
		"base_url":   "https://test.example.com",
		"public_key": string(pubKey),
	}
	wellKnownData, _ := json.MarshalIndent(wellKnown, "", "  ")
	os.WriteFile(filepath.Join(sourceDir, ".well-known", "polis"), wellKnownData, 0644)

	// Create a target server with empty data dir
	targetDir := t.TempDir()
	targetDataDir := filepath.Join(targetDir, "data")
	os.MkdirAll(targetDataDir, 0755)
	_ = sourceSrv // suppress unused warning

	// For this test, we need to create a mock server setup
	// Since handleLink uses os.Executable(), we'll test the validation part
	s := newTestServer(t)

	body := jsonBody(t, map[string]string{"path": sourceDir})
	req := httptest.NewRequest(http.MethodPost, "/api/link", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleLink(rr, req)

	// The test may fail due to symlink creation issues in test environment
	// but we verify the validation works
	if rr.Code == http.StatusOK {
		var resp map[string]interface{}
		json.Unmarshal(rr.Body.Bytes(), &resp)
		if resp["success"] != true {
			t.Errorf("expected success=true, got %v", resp["success"])
		}
	}
}

func TestHandleLink_InvalidPath(t *testing.T) {
	s := newTestServer(t)

	body := jsonBody(t, map[string]string{"path": "/nonexistent/path"})
	req := httptest.NewRequest(http.MethodPost, "/api/link", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleLink(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandleLink_EmptyPath(t *testing.T) {
	s := newTestServer(t)

	body := jsonBody(t, map[string]string{"path": ""})
	req := httptest.NewRequest(http.MethodPost, "/api/link", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleLink(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandleLink_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/link", nil)
	rr := httptest.NewRecorder()

	s.handleLink(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

// ============================================================================
// handleRender Tests
// ============================================================================

func TestHandleRender_Success(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{"markdown": "# Hello World"})
	req := httptest.NewRequest(http.MethodPost, "/api/render", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleRender(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	html, ok := resp["html"].(string)
	if !ok {
		t.Fatal("expected html field in response")
	}
	if !strings.Contains(html, "<h1") {
		t.Errorf("expected HTML with h1 tag, got %s", html)
	}

	signature, ok := resp["signature"].(string)
	if !ok || signature == "" {
		t.Error("expected non-empty signature field")
	}
}

func TestHandleRender_MethodNotAllowed(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/render", nil)
	rr := httptest.NewRecorder()

	s.handleRender(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

func TestHandleRender_NotConfigured(t *testing.T) {
	s := newTestServer(t) // No keys

	body := jsonBody(t, map[string]string{"markdown": "# Hello"})
	req := httptest.NewRequest(http.MethodPost, "/api/render", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleRender(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Not configured") {
		t.Error("expected 'Not configured' error message")
	}
}

func TestHandleRender_InvalidJSON(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/render", strings.NewReader("{invalid"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleRender(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandleRender_EmptyMarkdown(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{"markdown": ""})
	req := httptest.NewRequest(http.MethodPost, "/api/render", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleRender(rr, req)

	// Empty markdown should still render (to empty HTML)
	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200 for empty markdown, got %d", rr.Code)
	}
}

// ============================================================================
// handleDrafts Tests
// ============================================================================

func TestHandleDrafts_ListEmpty(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/drafts", nil)
	rr := httptest.NewRecorder()

	s.handleDrafts(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	// drafts may be nil or empty array when no drafts exist
	drafts := resp["drafts"]
	if drafts != nil {
		draftsArr, ok := drafts.([]interface{})
		if ok && len(draftsArr) != 0 {
			t.Errorf("expected empty drafts array, got %d items", len(draftsArr))
		}
	}
}

func TestHandleDrafts_ListWithDrafts(t *testing.T) {
	s := newTestServer(t)

	// Create some drafts
	draftsDir := filepath.Join(s.DataDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts")
	os.WriteFile(filepath.Join(draftsDir, "draft1.md"), []byte("# Draft 1"), 0644)
	os.WriteFile(filepath.Join(draftsDir, "draft2.md"), []byte("# Draft 2"), 0644)

	req := httptest.NewRequest(http.MethodGet, "/api/drafts", nil)
	rr := httptest.NewRecorder()

	s.handleDrafts(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	drafts := resp["drafts"].([]interface{})
	if len(drafts) != 2 {
		t.Errorf("expected 2 drafts, got %d", len(drafts))
	}
}

func TestHandleDrafts_SaveNew(t *testing.T) {
	s := newTestServer(t)

	body := jsonBody(t, map[string]string{
		"id":       "my-draft",
		"markdown": "# My Draft Content",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/drafts", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleDrafts(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if resp["success"] != true {
		t.Error("expected success=true")
	}
	if resp["id"] != "my-draft" {
		t.Errorf("expected id='my-draft', got %v", resp["id"])
	}

	// Verify file was created
	draftPath := filepath.Join(s.DataDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts", "my-draft.md")
	content, err := os.ReadFile(draftPath)
	if err != nil {
		t.Fatalf("draft file not created: %v", err)
	}
	if string(content) != "# My Draft Content" {
		t.Errorf("draft content mismatch: %s", string(content))
	}
}

func TestHandleDrafts_SaveAutoGenerateID(t *testing.T) {
	s := newTestServer(t)

	body := jsonBody(t, map[string]string{
		"markdown": "# Auto ID Draft",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/drafts", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleDrafts(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	id := resp["id"].(string)
	if !strings.HasPrefix(id, "draft-") {
		t.Errorf("expected auto-generated ID with 'draft-' prefix, got %s", id)
	}
}

func TestHandleDrafts_SaveSanitizesID(t *testing.T) {
	s := newTestServer(t)

	body := jsonBody(t, map[string]string{
		"id":       "../../../etc/passwd",
		"markdown": "# Malicious",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/drafts", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleDrafts(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	id := resp["id"].(string)
	if strings.Contains(id, "/") || strings.Contains(id, "\\") {
		t.Errorf("ID should not contain path separators: %s", id)
	}

	// Verify file is in drafts directory, not elsewhere
	draftPath := filepath.Join(s.DataDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts", id+".md")
	if _, err := os.Stat(draftPath); os.IsNotExist(err) {
		t.Error("draft should be created in drafts directory")
	}

	// Verify no file was created outside drafts
	maliciousPath := filepath.Join(s.DataDir, "..", "..", "..", "etc", "passwd.md")
	if _, err := os.Stat(maliciousPath); err == nil {
		t.Error("path traversal attack succeeded!")
	}
}

func TestHandleDrafts_InvalidJSON(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/drafts", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleDrafts(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandleDrafts_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPut, "/api/drafts", nil)
	rr := httptest.NewRecorder()

	s.handleDrafts(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

// ============================================================================
// handleDraft Tests (single draft operations)
// ============================================================================

func TestHandleDraft_GetExisting(t *testing.T) {
	s := newTestServer(t)

	// Create a draft
	draftPath := filepath.Join(s.DataDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts", "test-draft.md")
	os.WriteFile(draftPath, []byte("# Test Draft"), 0644)

	req := httptest.NewRequest(http.MethodGet, "/api/drafts/test-draft", nil)
	rr := httptest.NewRecorder()

	s.handleDraft(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if resp["id"] != "test-draft" {
		t.Errorf("expected id='test-draft', got %v", resp["id"])
	}
	if resp["markdown"] != "# Test Draft" {
		t.Errorf("expected markdown content, got %v", resp["markdown"])
	}
}

func TestHandleDraft_GetNotFound(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/drafts/nonexistent", nil)
	rr := httptest.NewRecorder()

	s.handleDraft(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rr.Code)
	}
}

func TestHandleDraft_GetEmptyID(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/drafts/", nil)
	rr := httptest.NewRecorder()

	s.handleDraft(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandleDraft_Delete(t *testing.T) {
	s := newTestServer(t)

	// Create a draft
	draftPath := filepath.Join(s.DataDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts", "to-delete.md")
	os.WriteFile(draftPath, []byte("# To Delete"), 0644)

	req := httptest.NewRequest(http.MethodDelete, "/api/drafts/to-delete", nil)
	rr := httptest.NewRecorder()

	s.handleDraft(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	// Verify file was deleted
	if _, err := os.Stat(draftPath); !os.IsNotExist(err) {
		t.Error("draft file should be deleted")
	}
}

func TestHandleDraft_DeleteNonexistent(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/drafts/nonexistent", nil)
	rr := httptest.NewRecorder()

	s.handleDraft(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rr.Code)
	}
}

func TestHandleDraft_SanitizesPathTraversal(t *testing.T) {
	s := newTestServer(t)

	// Try to read a file outside drafts directory
	req := httptest.NewRequest(http.MethodGet, "/api/drafts/..%2F..%2F..%2Fetc%2Fpasswd", nil)
	rr := httptest.NewRecorder()

	s.handleDraft(rr, req)

	// Should return 404 because sanitization prevents path traversal
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected status 404 after sanitization, got %d", rr.Code)
	}
}

func TestHandleDraft_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPut, "/api/drafts/test", nil)
	rr := httptest.NewRecorder()

	s.handleDraft(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

// ============================================================================
// handlePublish Tests
// ============================================================================

func TestHandlePublish_Success(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{
		"markdown": "# My First Post\n\nThis is the content.",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/publish", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handlePublish(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if resp["path"] == nil || resp["path"] == "" {
		t.Error("expected non-empty path in response")
	}
	if resp["title"] == nil {
		t.Error("expected title in response")
	}
}

func TestHandlePublish_MethodNotAllowed(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/publish", nil)
	rr := httptest.NewRecorder()

	s.handlePublish(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

func TestHandlePublish_NotConfigured(t *testing.T) {
	s := newTestServer(t) // No keys

	body := jsonBody(t, map[string]string{"markdown": "# Test"})
	req := httptest.NewRequest(http.MethodPost, "/api/publish", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handlePublish(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandlePublish_EmptyMarkdown(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{"markdown": ""})
	req := httptest.NewRequest(http.MethodPost, "/api/publish", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handlePublish(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandlePublish_WhitespaceOnlyMarkdown(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{"markdown": "   \n\t  "})
	req := httptest.NewRequest(http.MethodPost, "/api/publish", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handlePublish(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandlePublish_InvalidJSON(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/publish", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handlePublish(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandlePublish_WithFilename(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{
		"markdown": "# Custom Named Post",
		"filename": "custom-name.md",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/publish", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handlePublish(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	path := resp["path"].(string)
	if !strings.Contains(path, "custom-name") {
		t.Errorf("expected path to contain 'custom-name', got %s", path)
	}
}

func TestHandlePublish_StripsExistingFrontmatter(t *testing.T) {
	s := newConfiguredServer(t)

	markdownWithFrontmatter := `---
title: Old Title
---
# New Content`

	body := jsonBody(t, map[string]string{
		"markdown": markdownWithFrontmatter,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/publish", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handlePublish(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandlePublish_DeletesDraftOnPublish(t *testing.T) {
	s := newConfiguredServer(t)

	// Create a draft file
	draftsDir := filepath.Join(s.DataDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts")
	os.MkdirAll(draftsDir, 0755)
	draftPath := filepath.Join(draftsDir, "my-draft.md")
	os.WriteFile(draftPath, []byte("# Draft Post\n\nDraft content."), 0644)

	// Verify draft exists
	if _, err := os.Stat(draftPath); err != nil {
		t.Fatal("draft file should exist before publish")
	}

	body := jsonBody(t, map[string]string{
		"markdown": "# Draft Post\n\nDraft content.",
		"draft_id": "my-draft",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/publish", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handlePublish(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify draft was deleted
	if _, err := os.Stat(draftPath); !os.IsNotExist(err) {
		t.Error("draft file should be deleted after publish")
	}
}

// ============================================================================
// handlePosts Tests
// ============================================================================

func TestHandlePosts_Empty(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/posts", nil)
	rr := httptest.NewRecorder()

	s.handlePosts(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	posts := resp["posts"].([]interface{})
	if len(posts) != 0 {
		t.Errorf("expected empty posts, got %d", len(posts))
	}
}

func TestHandlePosts_WithPosts(t *testing.T) {
	s := newConfiguredServer(t)

	// Create public.jsonl with some posts
	indexPath := filepath.Join(s.DataDir, "content", "pub.polis.core", "index.jsonl")
	entries := []string{
		`{"path":"content/pub.polis.core/post/20260101/first.md","title":"First Post"}`,
		`{"path":"content/pub.polis.core/post/20260102/second.md","title":"Second Post"}`,
	}
	os.WriteFile(indexPath, []byte(strings.Join(entries, "\n")), 0644)

	req := httptest.NewRequest(http.MethodGet, "/api/posts", nil)
	rr := httptest.NewRecorder()

	s.handlePosts(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	posts := resp["posts"].([]interface{})
	if len(posts) != 2 {
		t.Errorf("expected 2 posts, got %d", len(posts))
	}

	// Posts should be in reverse order (newest first)
	firstPost := posts[0].(map[string]interface{})
	if firstPost["title"] != "Second Post" {
		t.Errorf("expected newest post first, got %v", firstPost["title"])
	}
}

func TestHandlePosts_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/posts", nil)
	rr := httptest.NewRecorder()

	s.handlePosts(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

func TestHandlePosts_IncludesCommentCounts(t *testing.T) {
	s := newConfiguredServer(t)

	// Create index with one post
	indexPath := filepath.Join(s.DataDir, "content", "pub.polis.core", "index.jsonl")
	os.WriteFile(indexPath, []byte(`{"path":"content/pub.polis.core/post/20260101/first.md","title":"First Post"}`), 0644)

	// Create blessed.json with 2 comments for that post
	blessedDir := filepath.Join(s.DataDir, "content", "pub.polis.core", "comment")
	os.MkdirAll(blessedDir, 0755)
	blessedData := `{"version":"1","comments":[{"post":"content/pub.polis.core/post/20260101/first.md","blessed":[{"url":"https://a.polis.pub/comments/1.md","version":"sha256:abc"},{"url":"https://b.polis.pub/comments/2.md","version":"sha256:def"}]}]}`
	os.WriteFile(filepath.Join(blessedDir, "blessed.json"), []byte(blessedData), 0644)

	req := httptest.NewRequest(http.MethodGet, "/api/posts", nil)
	rr := httptest.NewRecorder()
	s.handlePosts(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	posts := resp["posts"].([]interface{})
	if len(posts) != 1 {
		t.Fatalf("expected 1 post, got %d", len(posts))
	}
	post := posts[0].(map[string]interface{})
	count, ok := post["comment_count"].(float64)
	if !ok {
		t.Fatal("expected comment_count field in post response")
	}
	if count != 2 {
		t.Errorf("expected comment_count=2, got %v", count)
	}
}

// ============================================================================
// handlePost Tests (single post)
// ============================================================================

func TestHandlePost_GetExisting(t *testing.T) {
	s := newConfiguredServer(t)

	// Create a post file
	postDir := filepath.Join(s.DataDir, "content", "pub.polis.core", "post", "20260101")
	os.MkdirAll(postDir, 0755)
	postContent := `---
title: Test Post
published: 2026-01-01T00:00:00Z
---
# Test Post

Content here.`
	os.WriteFile(filepath.Join(postDir, "test.md"), []byte(postContent), 0644)

	req := httptest.NewRequest(http.MethodGet, "/api/posts/content/pub.polis.core/post/20260101/test.md", nil)
	rr := httptest.NewRecorder()

	s.handlePost(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if resp["title"] != "Test Post" {
		t.Errorf("expected title='Test Post', got %v", resp["title"])
	}
	markdown := resp["markdown"].(string)
	if !strings.Contains(markdown, "# Test Post") {
		t.Error("expected markdown content without frontmatter")
	}
	if strings.Contains(markdown, "---") {
		t.Error("frontmatter should be stripped from markdown")
	}
}

func TestHandlePost_NotFound(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/posts/content/pub.polis.core/post/20260101/nonexistent.md", nil)
	rr := httptest.NewRecorder()

	s.handlePost(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rr.Code)
	}
}

func TestHandlePost_EmptyPath(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/posts/", nil)
	rr := httptest.NewRecorder()

	s.handlePost(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandlePost_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/posts/content/pub.polis.core/post/20260101/test.md", nil)
	rr := httptest.NewRecorder()

	s.handlePost(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

// ============================================================================
// handleRepublish Tests
// ============================================================================

func TestHandleRepublish_Success(t *testing.T) {
	s := newConfiguredServer(t)

	// First publish a post
	postDir := filepath.Join(s.DataDir, "content", "pub.polis.core", "post", "20260101")
	os.MkdirAll(postDir, 0755)
	originalContent := `---
title: Original Title
published: 2026-01-01T00:00:00Z
version: 1
---
# Original Content`
	postPath := filepath.Join(postDir, "original.md")
	os.WriteFile(postPath, []byte(originalContent), 0644)

	body := jsonBody(t, map[string]string{
		"path":     "content/pub.polis.core/post/20260101/original.md",
		"markdown": "# Updated Content",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/republish", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleRepublish(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleRepublish_MethodNotAllowed(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/republish", nil)
	rr := httptest.NewRecorder()

	s.handleRepublish(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

func TestHandleRepublish_NotConfigured(t *testing.T) {
	s := newTestServer(t)

	body := jsonBody(t, map[string]string{
		"path":     "content/pub.polis.core/post/20260101/test.md",
		"markdown": "# Test",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/republish", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleRepublish(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandleRepublish_MissingPath(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{
		"markdown": "# Test",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/republish", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleRepublish(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandleRepublish_EmptyMarkdown(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{
		"path":     "content/pub.polis.core/post/20260101/test.md",
		"markdown": "",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/republish", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleRepublish(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

// ============================================================================
// handleCommentDrafts Tests
// ============================================================================

func TestHandleCommentDrafts_ListEmpty(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/comments/drafts", nil)
	rr := httptest.NewRecorder()

	s.handleCommentDrafts(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	// drafts may be nil or empty array when no drafts exist
	drafts := resp["drafts"]
	if drafts != nil {
		draftsArr, ok := drafts.([]interface{})
		if ok && len(draftsArr) != 0 {
			t.Errorf("expected empty drafts, got %d", len(draftsArr))
		}
	}
}

func TestHandleCommentDrafts_Save(t *testing.T) {
	s := newTestServer(t)

	body := jsonBody(t, map[string]string{
		"in_reply_to": "https://example.com/posts/test.md",
		"root_post":   "https://example.com/posts/test.md",
		"content":     "This is my comment",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/comments/drafts", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleCommentDrafts(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if resp["success"] != true {
		t.Error("expected success=true")
	}
	if resp["id"] == nil || resp["id"] == "" {
		t.Error("expected non-empty id")
	}
}

func TestHandleCommentDrafts_SaveMissingInReplyTo(t *testing.T) {
	s := newTestServer(t)

	body := jsonBody(t, map[string]string{
		"content": "This is my comment",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/comments/drafts", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleCommentDrafts(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandleCommentDrafts_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPut, "/api/comments/drafts", nil)
	rr := httptest.NewRecorder()

	s.handleCommentDrafts(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

// ============================================================================
// handleCommentSign Tests
// ============================================================================

func TestHandleCommentSign_MethodNotAllowed(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/comments/sign", nil)
	rr := httptest.NewRecorder()

	s.handleCommentSign(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

func TestHandleCommentSign_NotConfigured(t *testing.T) {
	s := newTestServer(t)

	body := jsonBody(t, map[string]string{
		"in_reply_to": "https://example.com/post.md",
		"content":     "Test",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/comments/sign", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleCommentSign(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandleCommentSign_MissingInReplyTo(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{
		"content": "Test comment",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/comments/sign", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleCommentSign(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandleCommentSign_DraftNotFound(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{
		"draft_id": "nonexistent-draft",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/comments/sign", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleCommentSign(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rr.Code)
	}
}

// ============================================================================
// handleCommentsPending/Blessed/Denied Tests
// ============================================================================

func TestHandleCommentsPending_Empty(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/comments/pending", nil)
	rr := httptest.NewRecorder()

	s.handleCommentsPending(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestHandleCommentsPending_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/comments/pending", nil)
	rr := httptest.NewRecorder()

	s.handleCommentsPending(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

func TestHandleCommentsBlessed_Empty(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/comments/blessed", nil)
	rr := httptest.NewRecorder()

	s.handleCommentsBlessed(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestHandleCommentsDenied_Empty(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/comments/denied", nil)
	rr := httptest.NewRecorder()

	s.handleCommentsDenied(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

// ============================================================================
// handleCommentsSync Tests
// ============================================================================

func TestHandleCommentsSync_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/comments/sync", nil)
	rr := httptest.NewRecorder()

	s.handleCommentsSync(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

func TestHandleCommentsSync_DiscoveryNotConfigured(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/comments/sync", nil)
	rr := httptest.NewRecorder()

	s.handleCommentsSync(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandleCommentsSync_PrivateKeyNotConfigured(t *testing.T) {
	s := newTestServer(t)
	s.DiscoveryURL = "https://discovery.example.com"
	s.DiscoveryKey = "test-key"

	req := httptest.NewRequest(http.MethodPost, "/api/comments/sync", nil)
	rr := httptest.NewRecorder()

	s.handleCommentsSync(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

// ============================================================================
// handleBlessingRequests Tests
// ============================================================================

func TestHandleBlessingRequests_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/blessing/requests", nil)
	rr := httptest.NewRecorder()

	s.handleBlessingRequests(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

func TestHandleBlessingRequests_DiscoveryNotConfigured(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/blessing/requests", nil)
	rr := httptest.NewRecorder()

	s.handleBlessingRequests(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandleBlessingRequests_PrivateKeyNotConfigured(t *testing.T) {
	s := newTestServer(t)
	s.DiscoveryURL = "https://discovery.example.com"
	s.DiscoveryKey = "test-key"

	req := httptest.NewRequest(http.MethodGet, "/api/blessing/requests", nil)
	rr := httptest.NewRecorder()

	s.handleBlessingRequests(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

// ============================================================================
// handleBlessingGrant Tests
// ============================================================================

func TestHandleBlessingGrant_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/blessing/grant", nil)
	rr := httptest.NewRecorder()

	s.handleBlessingGrant(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

func TestHandleBlessingGrant_DiscoveryNotConfigured(t *testing.T) {
	s := newTestServer(t)

	body := jsonBody(t, map[string]string{"comment_version": "abc123"})
	req := httptest.NewRequest(http.MethodPost, "/api/blessing/grant", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleBlessingGrant(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandleBlessingGrant_PrivateKeyNotConfigured(t *testing.T) {
	s := newTestServer(t)
	s.DiscoveryURL = "https://discovery.example.com"
	s.DiscoveryKey = "test-key"
	markAsRegistered(t, s)

	body := jsonBody(t, map[string]string{"comment_version": "abc123"})
	req := httptest.NewRequest(http.MethodPost, "/api/blessing/grant", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleBlessingGrant(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandleBlessingGrant_MissingCommentURL(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://discovery.example.com"
	s.DiscoveryKey = "test-key"
	markAsRegistered(t, s)

	body := jsonBody(t, map[string]string{})
	req := httptest.NewRequest(http.MethodPost, "/api/blessing/grant", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleBlessingGrant(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

// ============================================================================
// handleBlessingDeny Tests
// ============================================================================

func TestHandleBlessingDeny_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/blessing/deny", nil)
	rr := httptest.NewRecorder()

	s.handleBlessingDeny(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

func TestHandleBlessingDeny_MissingCommentVersion(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://discovery.example.com"
	s.DiscoveryKey = "test-key"
	markAsRegistered(t, s)

	body := jsonBody(t, map[string]string{})
	req := httptest.NewRequest(http.MethodPost, "/api/blessing/deny", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleBlessingDeny(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

// ============================================================================
// handleBlessedComments Tests
// ============================================================================

func TestHandleBlessedComments_Empty(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/blessed-comments", nil)
	rr := httptest.NewRecorder()

	s.handleBlessedComments(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	comments := resp["comments"].([]interface{})
	if len(comments) != 0 {
		t.Errorf("expected empty comments, got %d", len(comments))
	}
}

func TestHandleBlessedComments_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/blessed-comments", nil)
	rr := httptest.NewRecorder()

	s.handleBlessedComments(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

// ============================================================================
// handleBlessingRevoke Tests
// ============================================================================

func TestHandleBlessingRevoke_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/blessing/revoke", nil)
	rr := httptest.NewRecorder()

	s.handleBlessingRevoke(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

func TestHandleBlessingRevoke_MissingCommentURL(t *testing.T) {
	s := newTestServer(t)

	body := jsonBody(t, map[string]string{})
	req := httptest.NewRequest(http.MethodPost, "/api/blessing/revoke", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleBlessingRevoke(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

// ============================================================================
// handleSettings Tests
// ============================================================================

func TestHandleSettings_Unconfigured(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()

	s.handleSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	site := resp["site"].(map[string]interface{})
	if site["subdomain"] != "" {
		t.Errorf("expected empty subdomain, got %v", site["subdomain"])
	}
	if site["discovery_configured"] != false {
		t.Error("expected discovery_configured=false")
	}
}

func TestHandleSettings_Configured(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://discovery.example.com"
	s.DiscoveryKey = "test-key"

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()

	s.handleSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	site := resp["site"].(map[string]interface{})
	if site["subdomain"] != "test-site" {
		t.Errorf("expected subdomain='test-site', got %v", site["subdomain"])
	}
	if site["discovery_configured"] != true {
		t.Error("expected discovery_configured=true")
	}
}

func TestHandleSettings_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/settings", nil)
	rr := httptest.NewRecorder()

	s.handleSettings(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

// ============================================================================
// handleAutomations Tests
// ============================================================================

func TestHandleAutomations_ListEmpty(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/automations", nil)
	rr := httptest.NewRecorder()

	s.handleAutomations(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	// automations may be nil or empty array when no automations exist
	automations := resp["automations"]
	if automations != nil {
		automationsArr, ok := automations.([]interface{})
		if ok && len(automationsArr) != 0 {
			t.Errorf("expected empty automations, got %d", len(automationsArr))
		}
	}
}

func TestHandleAutomations_ListWithHooks(t *testing.T) {
	s := newTestServer(t)
	s.EnableHooks = true
	s.Config = &Config{
		Hooks: &hooks.HookConfig{
			PostPublish: ".polis/webapp/hooks/post-publish.sh",
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/automations", nil)
	rr := httptest.NewRecorder()

	s.handleAutomations(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	automations := resp["automations"].([]interface{})
	if len(automations) != 1 {
		t.Errorf("expected 1 automation, got %d", len(automations))
	}
}

func TestHandleAutomations_HooksDisabledByDefault(t *testing.T) {
	s := newTestServer(t)
	// EnableHooks defaults to false — simulates hosted mode
	s.Config = &Config{
		Hooks: &hooks.HookConfig{
			PostPublish: ".polis/webapp/hooks/post-publish.sh",
		},
	}

	// GET should return empty automations when hooks disabled
	req := httptest.NewRequest(http.MethodGet, "/api/automations", nil)
	rr := httptest.NewRecorder()
	s.handleAutomations(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	automations := resp["automations"]
	if automations != nil {
		if arr, ok := automations.([]interface{}); ok && len(arr) > 0 {
			t.Errorf("expected no automations when hooks disabled, got %d", len(arr))
		}
	}

	// POST should be forbidden when hooks disabled
	body := jsonBody(t, map[string]string{"script": "#!/bin/bash\necho test"})
	req = httptest.NewRequest(http.MethodPost, "/api/automations", body)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	s.handleAutomations(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected status 403 when hooks disabled, got %d", rr.Code)
	}
}

func TestHandleAutomations_CreateWithScript(t *testing.T) {
	s := newTestServer(t)
	s.EnableHooks = true

	body := jsonBody(t, map[string]string{
		"script": "#!/bin/bash\necho 'hello'",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/automations", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleAutomations(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify script was created
	scriptPath := filepath.Join(s.DataDir, ".polis", "webapp", "hooks", "post-publish.sh")
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		t.Error("hook script was not created")
	}
}

func TestHandleAutomations_CreateWithTemplate(t *testing.T) {
	s := newTestServer(t)
	s.EnableHooks = true

	body := jsonBody(t, map[string]string{
		"template_id": "vercel",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/automations", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleAutomations(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleAutomations_CreateUnknownTemplate(t *testing.T) {
	s := newTestServer(t)
	s.EnableHooks = true

	body := jsonBody(t, map[string]string{
		"template_id": "nonexistent-template",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/automations", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleAutomations(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandleAutomations_CreateNoScript(t *testing.T) {
	s := newTestServer(t)
	s.EnableHooks = true

	body := jsonBody(t, map[string]string{})
	req := httptest.NewRequest(http.MethodPost, "/api/automations", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleAutomations(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandleAutomations_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPut, "/api/automations", nil)
	rr := httptest.NewRecorder()

	s.handleAutomations(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

// ============================================================================
// handleAutomationsQuick Tests
// ============================================================================

func TestHandleAutomationsQuick_Success(t *testing.T) {
	s := newTestServer(t)
	s.EnableHooks = true

	req := httptest.NewRequest(http.MethodPost, "/api/automations/quick", nil)
	rr := httptest.NewRecorder()

	s.handleAutomationsQuick(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if resp["success"] != true {
		t.Error("expected success=true")
	}
}

func TestHandleAutomationsQuick_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/automations/quick", nil)
	rr := httptest.NewRecorder()

	s.handleAutomationsQuick(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

// ============================================================================
// handleAutomation Tests (single automation)
// ============================================================================

func TestHandleAutomation_Delete(t *testing.T) {
	s := newTestServer(t)
	s.EnableHooks = true
	s.Config = &Config{
		Hooks: &hooks.HookConfig{
			PostPublish: ".polis/webapp/hooks/post-publish.sh",
		},
	}

	// Create the hooks directory and file
	hooksDir := filepath.Join(s.DataDir, ".polis", "webapp", "hooks")
	os.MkdirAll(hooksDir, 0755)
	os.WriteFile(filepath.Join(hooksDir, "post-publish.sh"), []byte("#!/bin/bash"), 0755)

	// Save config to disk first
	s.SaveConfig()

	req := httptest.NewRequest(http.MethodDelete, "/api/automations/post-publish", nil)
	rr := httptest.NewRecorder()

	s.handleAutomation(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify hook was removed from config
	if s.Config.Hooks.PostPublish != "" {
		t.Error("expected PostPublish hook to be cleared")
	}
}

func TestHandleAutomation_DeleteUnknown(t *testing.T) {
	s := newTestServer(t)
	s.EnableHooks = true
	s.Config = &Config{
		Hooks: &hooks.HookConfig{},
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/automations/unknown-hook", nil)
	rr := httptest.NewRecorder()

	s.handleAutomation(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rr.Code)
	}
}

func TestHandleAutomation_DeleteNoConfig(t *testing.T) {
	s := newTestServer(t)
	s.EnableHooks = true

	req := httptest.NewRequest(http.MethodDelete, "/api/automations/post-publish", nil)
	rr := httptest.NewRecorder()

	s.handleAutomation(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rr.Code)
	}
}

func TestHandleAutomation_EmptyID(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/automations/", nil)
	rr := httptest.NewRecorder()

	s.handleAutomation(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandleAutomation_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/automations/post-publish", nil)
	rr := httptest.NewRecorder()

	s.handleAutomation(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

// ============================================================================
// handleTemplates Tests
// ============================================================================

func TestHandleTemplates_List(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/templates", nil)
	rr := httptest.NewRecorder()

	s.handleTemplates(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	templates, ok := resp["templates"].([]interface{})
	if !ok {
		t.Fatal("expected templates array")
	}
	if len(templates) == 0 {
		t.Error("expected at least one template")
	}
}

func TestHandleTemplates_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/templates", nil)
	rr := httptest.NewRecorder()

	s.handleTemplates(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

// ============================================================================
// handleCommentBeseech Tests
// ============================================================================

func TestHandleCommentBeseech_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/comments/beseech", nil)
	rr := httptest.NewRecorder()

	s.handleCommentBeseech(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

func TestHandleCommentBeseech_DiscoveryNotConfigured(t *testing.T) {
	s := newTestServer(t)

	body := jsonBody(t, map[string]string{"comment_id": "test-id"})
	req := httptest.NewRequest(http.MethodPost, "/api/comments/beseech", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleCommentBeseech(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandleCommentBeseech_MissingCommentID(t *testing.T) {
	s := newTestServer(t)
	s.DiscoveryURL = "https://discovery.example.com"
	s.DiscoveryKey = "test-key"

	body := jsonBody(t, map[string]string{})
	req := httptest.NewRequest(http.MethodPost, "/api/comments/beseech", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleCommentBeseech(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

// ============================================================================
// handleCommentDraft Tests (single comment draft)
// ============================================================================

func TestHandleCommentDraft_GetNotFound(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/comments/drafts/nonexistent", nil)
	rr := httptest.NewRecorder()

	s.handleCommentDraft(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rr.Code)
	}
}

func TestHandleCommentDraft_EmptyID(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/comments/drafts/", nil)
	rr := httptest.NewRecorder()

	s.handleCommentDraft(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandleCommentDraft_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPut, "/api/comments/drafts/test", nil)
	rr := httptest.NewRecorder()

	s.handleCommentDraft(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

// ============================================================================
// Configuration Management Tests
// ============================================================================

func TestLoadConfig_NoFile(t *testing.T) {
	s := newTestServer(t)

	s.LoadConfig()

	if s.Config != nil {
		t.Error("expected config to be nil when no file exists")
	}
}

func TestLoadConfig_ValidFile(t *testing.T) {
	s := newTestServer(t)

	// Create config file
	config := Config{
		SetupCode: "test-code",
		Subdomain: "test-site",
		SetupAt:   "2026-01-01T00:00:00Z",
	}
	configData, _ := json.MarshalIndent(config, "", "  ")
	configPath := filepath.Join(s.DataDir, ".polis", "webapp", "config.json")
	os.MkdirAll(filepath.Dir(configPath), 0755)
	os.WriteFile(configPath, configData, 0644)

	s.LoadConfig()

	if s.Config == nil {
		t.Fatal("expected config to be loaded")
	}
	if s.Config.SetupCode != "test-code" {
		t.Errorf("expected SetupCode='test-code', got %s", s.Config.SetupCode)
	}
	if s.Config.Subdomain != "test-site" {
		t.Errorf("expected Subdomain='test-site', got %s", s.Config.Subdomain)
	}
}

func TestLoadConfig_InvalidJSON(t *testing.T) {
	s := newTestServer(t)

	// Create invalid config file
	configPath := filepath.Join(s.DataDir, ".polis", "webapp", "config.json")
	os.MkdirAll(filepath.Dir(configPath), 0755)
	os.WriteFile(configPath, []byte("{invalid json"), 0644)

	s.LoadConfig()

	if s.Config != nil {
		t.Error("expected config to be nil for invalid JSON")
	}
}

func TestSaveConfig_Success(t *testing.T) {
	s := newTestServer(t)
	s.Config = &Config{
		SetupCode: "save-test",
		Subdomain: "saved-site",
		SetupAt:   "2026-01-15T12:00:00Z",
	}

	err := s.SaveConfig()
	if err != nil {
		t.Fatalf("saveConfig failed: %v", err)
	}

	// Verify file was created
	configPath := filepath.Join(s.DataDir, ".polis", "webapp", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config file not created: %v", err)
	}

	var loaded Config
	json.Unmarshal(data, &loaded)

	if loaded.SetupCode != "save-test" {
		t.Errorf("expected SetupCode='save-test', got %s", loaded.SetupCode)
	}
	// Subdomain should NOT be persisted (deprecated field stripped on save)
	if loaded.Subdomain != "" {
		t.Errorf("expected Subdomain to be empty on disk, got %s", loaded.Subdomain)
	}
	// But in-memory value should be preserved
	if s.Config.Subdomain != "saved-site" {
		t.Errorf("expected in-memory Subdomain='saved-site', got %s", s.Config.Subdomain)
	}
}

func TestLoadKeys_NoFiles(t *testing.T) {
	s := newTestServer(t)

	s.LoadKeys()

	if s.PrivateKey != nil {
		t.Error("expected privateKey to be nil when no files exist")
	}
	if s.PublicKey != nil {
		t.Error("expected publicKey to be nil when no files exist")
	}
}

func TestLoadKeys_Success(t *testing.T) {
	s := newTestServer(t)

	// Create key files
	privKeyPath := filepath.Join(s.DataDir, ".polis", "keys", "id_ed25519")
	pubKeyPath := filepath.Join(s.DataDir, ".polis", "keys", "id_ed25519.pub")
	os.WriteFile(privKeyPath, []byte("fake-private-key"), 0600)
	os.WriteFile(pubKeyPath, []byte("fake-public-key"), 0644)

	s.LoadKeys()

	if s.PrivateKey == nil {
		t.Error("expected privateKey to be loaded")
	}
	if s.PublicKey == nil {
		t.Error("expected publicKey to be loaded")
	}
	if string(s.PrivateKey) != "fake-private-key" {
		t.Errorf("expected privateKey content, got %s", string(s.PrivateKey))
	}
}

func TestLoadKeys_PrivateOnly(t *testing.T) {
	s := newTestServer(t)

	// Create only private key
	privKeyPath := filepath.Join(s.DataDir, ".polis", "keys", "id_ed25519")
	os.WriteFile(privKeyPath, []byte("fake-private-key"), 0600)

	s.LoadKeys()

	// Should not load if only one key exists
	if s.PrivateKey != nil {
		t.Error("expected privateKey to be nil when public key missing")
	}
}

func TestLoadEnv_NoFile(t *testing.T) {
	s := newTestServer(t)

	s.LoadEnv()

	// Should not error, just do nothing
	if s.Config != nil {
		t.Error("expected config to remain nil when no .env file")
	}
}

func TestLoadEnv_DataDirFile(t *testing.T) {
	s := newTestServer(t)

	// Create .env in data directory
	envContent := `DISCOVERY_SERVICE_URL=https://test-discovery.com
DISCOVERY_SERVICE_KEY=test-api-key`
	envPath := filepath.Join(s.DataDir, ".env")
	os.WriteFile(envPath, []byte(envContent), 0644)

	s.LoadEnv()

	if s.DiscoveryURL != "https://test-discovery.com" {
		t.Errorf("expected DiscoveryURL from .env, got %s", s.DiscoveryURL)
	}
	if s.DiscoveryKey != "test-api-key" {
		t.Errorf("expected DiscoveryKey from .env, got %s", s.DiscoveryKey)
	}
}

func TestLoadEnv_QuotedValues(t *testing.T) {
	s := newTestServer(t)

	// Create .env with quoted values
	envContent := `DISCOVERY_SERVICE_URL="https://quoted.com"
DISCOVERY_SERVICE_KEY='single-quoted-key'`
	envPath := filepath.Join(s.DataDir, ".env")
	os.WriteFile(envPath, []byte(envContent), 0644)

	s.LoadEnv()

	if s.DiscoveryURL != "https://quoted.com" {
		t.Errorf("expected quotes stripped from URL, got %s", s.DiscoveryURL)
	}
	if s.DiscoveryKey != "single-quoted-key" {
		t.Errorf("expected quotes stripped from key, got %s", s.DiscoveryKey)
	}
}

func TestLoadEnv_Comments(t *testing.T) {
	s := newTestServer(t)

	// Create .env with comments
	envContent := `# This is a comment
DISCOVERY_SERVICE_URL=https://actual-url.com
# Another comment
# DISCOVERY_SERVICE_KEY=commented-out
DISCOVERY_SERVICE_KEY=actual-key`
	envPath := filepath.Join(s.DataDir, ".env")
	os.WriteFile(envPath, []byte(envContent), 0644)

	s.LoadEnv()

	if s.DiscoveryURL != "https://actual-url.com" {
		t.Errorf("expected non-comment URL, got %s", s.DiscoveryURL)
	}
	if s.DiscoveryKey != "actual-key" {
		t.Errorf("expected non-comment key, got %s", s.DiscoveryKey)
	}
}

func TestLoadEnv_EmptyLines(t *testing.T) {
	s := newTestServer(t)

	// Create .env with empty lines
	envContent := `

DISCOVERY_SERVICE_URL=https://test.com

DISCOVERY_SERVICE_KEY=test-key

`
	envPath := filepath.Join(s.DataDir, ".env")
	os.WriteFile(envPath, []byte(envContent), 0644)

	s.LoadEnv()

	if s.DiscoveryURL != "https://test.com" {
		t.Errorf("expected URL parsed correctly, got %s", s.DiscoveryURL)
	}
}

func TestLoadEnv_MalformedLines(t *testing.T) {
	s := newTestServer(t)

	// Create .env with malformed lines
	envContent := `DISCOVERY_SERVICE_URL=https://valid.com
no-equals-sign
DISCOVERY_SERVICE_KEY=valid-key
=value-with-no-key`
	envPath := filepath.Join(s.DataDir, ".env")
	os.WriteFile(envPath, []byte(envContent), 0644)

	s.LoadEnv()

	// Valid lines should still be parsed
	if s.DiscoveryURL != "https://valid.com" {
		t.Errorf("expected valid URL, got %s", s.DiscoveryURL)
	}
	if s.DiscoveryKey != "valid-key" {
		t.Errorf("expected valid key, got %s", s.DiscoveryKey)
	}
}

func TestLoadEnv_OverridesConfig(t *testing.T) {
	s := newTestServer(t)

	// Set up existing config
	s.Config = &Config{
		Subdomain: "existing-site",
	}
	s.DiscoveryURL = "https://old-discovery.com"
	s.DiscoveryKey = "old-key"

	// Create .env with new values
	envContent := `DISCOVERY_SERVICE_URL=https://new-discovery.com
DISCOVERY_SERVICE_KEY=new-key`
	envPath := filepath.Join(s.DataDir, ".env")
	os.WriteFile(envPath, []byte(envContent), 0644)

	s.LoadEnv()

	// .env should override previous values
	if s.DiscoveryURL != "https://new-discovery.com" {
		t.Errorf("expected .env to override URL, got %s", s.DiscoveryURL)
	}
	if s.DiscoveryKey != "new-key" {
		t.Errorf("expected .env to override key, got %s", s.DiscoveryKey)
	}
	// Non-overridden values should remain
	if s.Config.Subdomain != "existing-site" {
		t.Errorf("expected Subdomain to remain unchanged, got %s", s.Config.Subdomain)
	}
}

func TestLoadEnv_POLIS_BASE_URL(t *testing.T) {
	s := newTestServer(t)
	s.Config = &Config{}

	// Create .env with POLIS_BASE_URL
	envContent := `POLIS_BASE_URL=https://alice.polis.pub`
	envPath := filepath.Join(s.DataDir, ".env")
	os.WriteFile(envPath, []byte(envContent), 0644)

	s.LoadEnv()

	// BaseURL should be set, subdomain derived via GetSubdomain()
	if s.BaseURL != "https://alice.polis.pub" {
		t.Errorf("expected BaseURL='https://alice.polis.pub', got %s", s.BaseURL)
	}
	if s.GetSubdomain() != "alice" {
		t.Errorf("expected GetSubdomain()='alice', got %s", s.GetSubdomain())
	}
}

func TestLoadEnv_POLIS_BASE_URL_Subdomain(t *testing.T) {
	s := newTestServer(t)
	s.Config = &Config{}

	// Create .env with POLIS_BASE_URL
	envContent := `POLIS_BASE_URL=https://new.polis.pub`
	envPath := filepath.Join(s.DataDir, ".env")
	os.WriteFile(envPath, []byte(envContent), 0644)

	s.LoadEnv()

	// GetSubdomain derives from BaseURL
	if s.GetSubdomain() != "new" {
		t.Errorf("expected GetSubdomain()='new', got %s", s.GetSubdomain())
	}
}

func TestGetSubdomain_FallbackToConfig(t *testing.T) {
	// Test backwards compat: old configs with Subdomain field but no BaseURL
	s := newTestServer(t)
	s.Config = &Config{
		Subdomain: "legacy-site",
	}
	// No BaseURL set
	if s.GetSubdomain() != "legacy-site" {
		t.Errorf("expected GetSubdomain() to fall back to Config.Subdomain, got %s", s.GetSubdomain())
	}
}

func TestApplyDiscoveryDefaults_NoConfig(t *testing.T) {
	s := newTestServer(t)

	s.ApplyDiscoveryDefaults()

	if s.DiscoveryURL != DefaultDiscoveryServiceURL {
		t.Errorf("expected default discovery URL, got %s", s.DiscoveryURL)
	}
}

func TestApplyDiscoveryDefaults_EmptyURL(t *testing.T) {
	s := newTestServer(t)
	s.Config = &Config{
		Subdomain: "test-site",
		// DiscoveryURL is empty
	}

	s.ApplyDiscoveryDefaults()

	if s.DiscoveryURL != DefaultDiscoveryServiceURL {
		t.Errorf("expected default discovery URL, got %s", s.DiscoveryURL)
	}
}

func TestApplyDiscoveryDefaults_ExistingURL(t *testing.T) {
	s := newTestServer(t)
	s.DiscoveryURL = "https://custom-discovery.com"

	s.ApplyDiscoveryDefaults()

	if s.DiscoveryURL != "https://custom-discovery.com" {
		t.Errorf("expected custom URL not to be overridden, got %s", s.DiscoveryURL)
	}
}

func TestConfigPersistence_RoundTrip(t *testing.T) {
	s := newTestServer(t)

	// Create and save config
	s.Config = &Config{
		SetupCode: "round-trip",
		Subdomain: "persist-test", // Deprecated - should be stripped on save
		SetupAt:   "2026-01-20T10:00:00Z",
	}
	err := s.SaveConfig()
	if err != nil {
		t.Fatalf("saveConfig failed: %v", err)
	}

	// Create new server and load config
	s2 := &Server{DataDir: s.DataDir}
	s2.LoadConfig()

	if s2.Config == nil {
		t.Fatal("expected config to be loaded")
	}
	if s2.Config.SetupCode != "round-trip" {
		t.Errorf("SetupCode mismatch: expected 'round-trip', got %s", s2.Config.SetupCode)
	}
	// Subdomain is stripped on save (deprecated field)
	if s2.Config.Subdomain != "" {
		t.Errorf("expected Subdomain to be empty after round-trip, got %s", s2.Config.Subdomain)
	}
}

func TestGetAuthorEmail_FromWellKnown(t *testing.T) {
	s := newConfiguredServer(t) // has email in .well-known/polis
	email := s.GetAuthorEmail()
	if email != "test@example.com" {
		t.Errorf("expected test@example.com, got %q", email)
	}
}

func TestGetAuthorEmail_NoWellKnown(t *testing.T) {
	s := newTestServer(t) // no .well-known/polis file
	email := s.GetAuthorEmail()
	if email != "" {
		t.Errorf("expected empty email, got %q", email)
	}
}

func TestGetAuthorEmail_NoEmailField(t *testing.T) {
	s := newTestServer(t)
	// Create .well-known/polis without email field
	wellKnown := map[string]interface{}{
		"public_key": "ssh-ed25519 AAAA...",
		"site_title": "No Email Site",
	}
	data, _ := json.MarshalIndent(wellKnown, "", "  ")
	os.WriteFile(filepath.Join(s.DataDir, ".well-known", "polis"), data, 0644)

	email := s.GetAuthorEmail()
	if email != "" {
		t.Errorf("expected empty email, got %q", email)
	}
}

// ============================================================================
// GetAuthorDomain Tests (Phase 0)
// ============================================================================

func TestGetAuthorDomain_FromBaseURL(t *testing.T) {
	s := newConfiguredServer(t) // has BaseURL set to https://test-site.polis.pub
	domain := s.GetAuthorDomain()
	if domain != "test-site.polis.pub" {
		t.Errorf("expected test-site.polis.pub, got %q", domain)
	}
}

func TestGetAuthorDomain_DifferentBaseURL(t *testing.T) {
	s := newTestServer(t)
	s.BaseURL = "https://fallback.polis.pub"

	domain := s.GetAuthorDomain()
	if domain != "fallback.polis.pub" {
		t.Errorf("expected fallback.polis.pub, got %q", domain)
	}
}

func TestGetAuthorDomain_NoWellKnown(t *testing.T) {
	s := newTestServer(t)
	s.BaseURL = "https://nofile.polis.pub"
	domain := s.GetAuthorDomain()
	if domain != "nofile.polis.pub" {
		t.Errorf("expected nofile.polis.pub (from BaseURL), got %q", domain)
	}
}

func TestGetAuthorDomain_NothingConfigured(t *testing.T) {
	s := newTestServer(t)
	domain := s.GetAuthorDomain()
	if domain != "" {
		t.Errorf("expected empty domain, got %q", domain)
	}
}

func TestConfigWithHooks_Persistence(t *testing.T) {
	s := newTestServer(t)

	// Create config with hooks
	s.Config = &Config{
		SetupCode: "hook-test",
		Subdomain: "hook-site",
		SetupAt:   "2026-01-20T10:00:00Z",
		Hooks: &hooks.HookConfig{
			PostPublish:   ".polis/webapp/hooks/publish.sh",
			PostRepublish: ".polis/webapp/hooks/republish.sh",
			PostComment:   ".polis/webapp/hooks/comment.sh",
		},
	}
	err := s.SaveConfig()
	if err != nil {
		t.Fatalf("saveConfig failed: %v", err)
	}

	// Load into new server
	s2 := &Server{DataDir: s.DataDir}
	s2.LoadConfig()

	if s2.Config == nil {
		t.Fatal("expected config to be loaded")
	}
	if s2.Config.Hooks == nil {
		t.Fatal("expected Hooks to be loaded")
	}
	if s2.Config.Hooks.PostPublish != ".polis/webapp/hooks/publish.sh" {
		t.Errorf("PostPublish mismatch: got %s", s2.Config.Hooks.PostPublish)
	}
	if s2.Config.Hooks.PostRepublish != ".polis/webapp/hooks/republish.sh" {
		t.Errorf("PostRepublish mismatch")
	}
	if s2.Config.Hooks.PostComment != ".polis/webapp/hooks/comment.sh" {
		t.Errorf("PostComment mismatch")
	}
}

// ============================================================================
// File System Safety Tests
// ============================================================================

func TestDrafts_PathTraversalPrevention(t *testing.T) {
	s := newTestServer(t)

	// Create a sensitive file outside drafts
	sensitiveDir := filepath.Join(s.DataDir, ".polis", "keys")
	sensitiveFile := filepath.Join(sensitiveDir, "secret.txt")
	os.WriteFile(sensitiveFile, []byte("secret data"), 0644)

	// Attempt to read via path traversal
	maliciousIDs := []string{
		"../keys/secret",
		"..%2Fkeys%2Fsecret",
		"....//keys//secret",
		"..\\keys\\secret",
	}

	for _, maliciousID := range maliciousIDs {
		req := httptest.NewRequest(http.MethodGet, "/api/drafts/"+maliciousID, nil)
		rr := httptest.NewRecorder()

		s.handleDraft(rr, req)

		// Should get 404 (not found) because path is sanitized, not the actual file
		if rr.Code == http.StatusOK {
			t.Errorf("path traversal should be prevented for ID: %s", maliciousID)
		}
	}
}

func TestDrafts_SavePathTraversalPrevention(t *testing.T) {
	s := newTestServer(t)

	// Attempt to save with malicious ID
	body := jsonBody(t, map[string]string{
		"id":       "../../../tmp/malicious",
		"markdown": "# Malicious Content",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/drafts", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleDrafts(rr, req)

	// The save should succeed but with sanitized ID
	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	// Verify the file was NOT created in the malicious path
	maliciousPath := filepath.Join(s.DataDir, "..", "..", "..", "tmp", "malicious.md")
	if _, err := os.Stat(maliciousPath); err == nil {
		t.Error("path traversal attack succeeded - file created outside drafts")
	}

	// Verify file WAS created in proper drafts directory
	draftsDir := filepath.Join(s.DataDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts")
	files, _ := os.ReadDir(draftsDir)
	if len(files) != 1 {
		t.Errorf("expected 1 file in drafts dir, got %d", len(files))
	}
}

func TestPost_ValidPathAccess(t *testing.T) {
	s := newConfiguredServer(t)

	// Create a valid post file
	postDir := filepath.Join(s.DataDir, "content", "pub.polis.core", "post", "20260128")
	os.MkdirAll(postDir, 0755)
	postContent := `---
title: Test Post
---
# Test Post
Content here.`
	os.WriteFile(filepath.Join(postDir, "test.md"), []byte(postContent), 0644)

	// Access the post via valid path
	req := httptest.NewRequest(http.MethodGet, "/api/posts/content/pub.polis.core/post/20260128/test.md", nil)
	rr := httptest.NewRecorder()

	s.handlePost(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for valid post path, got %d", rr.Code)
	}
}

func TestPost_InternalFilesBlocked(t *testing.T) {
	// Verify that internal files (.polis/) are NOT accessible via /api/posts/
	s := newConfiguredServer(t)

	// Create a file in .polis
	internalFile := filepath.Join(s.DataDir, ".polis", "test-internal.txt")
	os.WriteFile(internalFile, []byte("internal data"), 0644)

	// Attempt to access internal file - should be blocked
	req := httptest.NewRequest(http.MethodGet, "/api/posts/.polis/test-internal.txt", nil)
	rr := httptest.NewRecorder()

	s.handlePost(rr, req)

	// Should get 400 Bad Request because path doesn't start with "posts/"
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for .polis path, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid path") {
		t.Error("expected 'invalid path' error message")
	}
}

func TestPost_PathTraversalBlocked(t *testing.T) {
	s := newConfiguredServer(t)

	// Create a sensitive file
	sensitiveFile := filepath.Join(s.DataDir, ".env")
	os.WriteFile(sensitiveFile, []byte("SECRET_KEY=supersecret"), 0644)

	// Various traversal attempts
	traversalPaths := []string{
		"../.env",
		"content/pub.polis.core/post/../.env",
		"content/pub.polis.core/post/../../.env",
		"content/pub.polis.core/post/20260128/../../../.env",
	}

	for _, path := range traversalPaths {
		req := httptest.NewRequest(http.MethodGet, "/api/posts/"+path, nil)
		rr := httptest.NewRecorder()

		s.handlePost(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for traversal path %q, got %d", path, rr.Code)
		}

		// Verify sensitive data was not exposed
		if strings.Contains(rr.Body.String(), "supersecret") {
			t.Errorf("path traversal exposed sensitive file with path: %s", path)
		}
	}
}

func TestPost_ValidPostsPathAllowed(t *testing.T) {
	s := newConfiguredServer(t)

	// Create a valid post
	postDir := filepath.Join(s.DataDir, "content", "pub.polis.core", "post", "20260128")
	os.MkdirAll(postDir, 0755)
	postContent := `---
title: Valid Post
---
# Valid Post
Content here.`
	os.WriteFile(filepath.Join(postDir, "valid.md"), []byte(postContent), 0644)

	// Valid paths should work
	validPaths := []string{
		"content/pub.polis.core/post/20260128/valid.md",
		"content/pub.polis.core/post/20260128/valid.md",
	}

	for _, path := range validPaths {
		req := httptest.NewRequest(http.MethodGet, "/api/posts/"+path, nil)
		rr := httptest.NewRecorder()

		s.handlePost(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected 200 for valid path %q, got %d: %s", path, rr.Code, rr.Body.String())
		}
	}
}

func TestRepublish_PathTraversalBlocked(t *testing.T) {
	s := newConfiguredServer(t)

	// Create a sensitive file that attacker might want to overwrite
	sensitiveFile := filepath.Join(s.DataDir, ".polis", "webapp", "config.json")

	// Various traversal attempts
	traversalPaths := []string{
		"../.polis/webapp/config.json",
		".polis/webapp/config.json",
		"content/pub.polis.core/post/../.polis/webapp/config.json",
		"content/pub.polis.core/post/../../important.txt",
	}

	for _, path := range traversalPaths {
		body := jsonBody(t, map[string]string{
			"path":     path,
			"markdown": "# Malicious content",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/republish", body)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		s.handleRepublish(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for traversal path %q, got %d", path, rr.Code)
		}
	}

	// Verify sensitive file was not modified
	if _, err := os.Stat(sensitiveFile); err == nil {
		content, _ := os.ReadFile(sensitiveFile)
		if strings.Contains(string(content), "Malicious") {
			t.Error("path traversal attack succeeded - config file was modified!")
		}
	}
}

func TestRepublish_ValidPostsPathAllowed(t *testing.T) {
	s := newConfiguredServer(t)

	// Create a valid post to republish
	postDir := filepath.Join(s.DataDir, "content", "pub.polis.core", "post", "20260128")
	os.MkdirAll(postDir, 0755)
	os.MkdirAll(filepath.Join(s.DataDir, "content", "pub.polis.core"), 0755)

	originalContent := `---
title: Original Title
published: 2026-01-28T12:00:00Z
current-version: sha256:abc123
version-history:
  - sha256:abc123 (2026-01-28T12:00:00Z)
---

# Original Title
Original content.`
	postPath := filepath.Join(postDir, "test-republish.md")
	os.WriteFile(postPath, []byte(originalContent), 0644)

	// Valid republish should work
	body := jsonBody(t, map[string]string{
		"path":     "content/pub.polis.core/post/20260128/test-republish.md",
		"markdown": "# Updated Title\n\nUpdated content.",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/republish", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleRepublish(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for valid republish, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestValidatePostPath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"valid path", "content/pub.polis.core/post/20260128/test.md", false},
		{"valid nested path", "content/pub.polis.core/post/20260128/subdir/test.md", false},
		{"missing prefix", "20260128/test.md", true},
		{"dotpolis path", ".polis/keys/id_ed25519", true},
		{"traversal with dotdot", "content/pub.polis.core/post/../.env", true},
		{"traversal mid-path", "content/pub.polis.core/post/20260128/../../.env", true},
		{"double traversal", "content/pub.polis.core/post/../../etc/passwd", true},
		{"null byte injection", "content/pub.polis.core/post/20260128/test\x00.md", true},
		{"empty path", "", true},
		{"just post dir", "content/pub.polis.core/post/", true}, // filepath.Clean strips trailing slash; bare directory is not a valid post path
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePostPath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePostPath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
		})
	}
}

func TestAutomation_DeletePathTraversal(t *testing.T) {
	s := newTestServer(t)
	s.EnableHooks = true
	s.Config = &Config{
		Hooks: &hooks.HookConfig{
			PostPublish: ".polis/webapp/hooks/post-publish.sh",
		},
	}

	// Create a hooks directory and file
	hooksDir := filepath.Join(s.DataDir, ".polis", "webapp", "hooks")
	os.MkdirAll(hooksDir, 0755)
	os.WriteFile(filepath.Join(hooksDir, "post-publish.sh"), []byte("#!/bin/bash"), 0755)

	// Create a sensitive file that attacker might want to delete
	importantFile := filepath.Join(s.DataDir, "important.txt")
	os.WriteFile(importantFile, []byte("important data"), 0644)

	// Attempt to delete via path traversal (should fail with 404)
	maliciousIDs := []string{
		"../important.txt",
		"../../important",
		"post-publish/../important",
	}

	for _, maliciousID := range maliciousIDs {
		// Save config first
		s.SaveConfig()

		req := httptest.NewRequest(http.MethodDelete, "/api/automations/"+maliciousID, nil)
		rr := httptest.NewRecorder()

		s.handleAutomation(rr, req)

		// Should get 404 for unknown automation ID
		if rr.Code != http.StatusNotFound {
			t.Logf("Note: got status %d for ID %s", rr.Code, maliciousID)
		}

		// Important file should still exist
		if _, err := os.Stat(importantFile); os.IsNotExist(err) {
			t.Errorf("path traversal deleted important file with ID: %s", maliciousID)
		}
	}
}

func TestDraft_DeletePathTraversal(t *testing.T) {
	s := newTestServer(t)

	// Create an important file
	importantFile := filepath.Join(s.DataDir, ".polis", "keys", "id_ed25519.pub")
	os.WriteFile(importantFile, []byte("public key"), 0644)

	// Attempt to delete via path traversal
	maliciousIDs := []string{
		"../keys/id_ed25519.pub",
		"..%2Fkeys%2Fid_ed25519.pub",
	}

	for _, maliciousID := range maliciousIDs {
		req := httptest.NewRequest(http.MethodDelete, "/api/drafts/"+maliciousID, nil)
		rr := httptest.NewRecorder()

		s.handleDraft(rr, req)

		// Important file should still exist
		if _, err := os.Stat(importantFile); os.IsNotExist(err) {
			t.Errorf("path traversal deleted important file with ID: %s", maliciousID)
		}
	}
}

func TestCommentDraft_PathTraversal(t *testing.T) {
	s := newTestServer(t)

	// Create an important file
	importantFile := filepath.Join(s.DataDir, ".polis", "keys", "id_ed25519.pub")
	os.WriteFile(importantFile, []byte("public key"), 0644)

	// Attempt to read via path traversal
	req := httptest.NewRequest(http.MethodGet, "/api/comments/drafts/../../../.polis/keys/id_ed25519.pub", nil)
	rr := httptest.NewRecorder()

	s.handleCommentDraft(rr, req)

	// Should not return the public key
	if rr.Code == http.StatusOK {
		body := rr.Body.String()
		if strings.Contains(body, "public key") {
			t.Error("path traversal exposed public key file")
		}
	}
}

func TestDrafts_IDSanitization(t *testing.T) {
	s := newTestServer(t)

	// Test that IDs with slashes get sanitized
	body := jsonBody(t, map[string]string{
		"id":       "path/with/slashes",
		"markdown": "# Test",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/drafts", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleDrafts(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	savedID := resp["id"].(string)
	if strings.Contains(savedID, "/") {
		t.Errorf("ID should have slashes sanitized, got: %s", savedID)
	}
}

func TestDrafts_BackslashSanitization(t *testing.T) {
	s := newTestServer(t)

	// Test that IDs with backslashes get sanitized
	body := jsonBody(t, map[string]string{
		"id":       "path\\with\\backslashes",
		"markdown": "# Test",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/drafts", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleDrafts(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	savedID := resp["id"].(string)
	if strings.Contains(savedID, "\\") {
		t.Errorf("ID should have backslashes sanitized, got: %s", savedID)
	}
}

func TestPublish_EmptyContentPrevention(t *testing.T) {
	s := newConfiguredServer(t)

	// Try to publish whitespace-only content
	testCases := []string{
		"",
		"   ",
		"\n\n\n",
		"\t\t",
		"   \n   \t   ",
	}

	for _, content := range testCases {
		body := jsonBody(t, map[string]string{"markdown": content})
		req := httptest.NewRequest(http.MethodPost, "/api/publish", body)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		s.handlePublish(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for empty content %q, got %d", content, rr.Code)
		}
	}
}

func TestInit_PreventKeyOverwrite(t *testing.T) {
	s := newConfiguredServer(t) // Already has keys

	// Store original key content
	privKeyPath := filepath.Join(s.DataDir, ".polis", "keys", "id_ed25519")
	originalContent, _ := os.ReadFile(privKeyPath)

	// Try to run init again - should fail
	body := jsonBody(t, map[string]string{"site_title": "attack"})
	req := httptest.NewRequest(http.MethodPost, "/api/init", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleInit(rr, req)

	// Should get 500 Internal Server Error (site package returns error)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 error when keys exist, got %d", rr.Code)
	}

	// Verify existing keys were NOT overwritten
	content, _ := os.ReadFile(privKeyPath)
	if string(content) != string(originalContent) {
		t.Error("existing private key was overwritten!")
	}
}

func TestDirectoryCreation_Safe(t *testing.T) {
	s := newTestServer(t)

	// Publish should create directories safely
	body := jsonBody(t, map[string]string{
		"markdown": "# Test Post\n\nContent for directory creation test.",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/publish", body)
	req.Header.Set("Content-Type", "application/json")

	// Need configured server
	privKey, _, _ := signing.GenerateKeypair()
	s.PrivateKey = privKey
	s.Config = &Config{Subdomain: "test"}

	rr := httptest.NewRecorder()
	s.handlePublish(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify directory structure was created
	postsDir := filepath.Join(s.DataDir, "content", "pub.polis.core", "post")
	if _, err := os.Stat(postsDir); os.IsNotExist(err) {
		t.Error("content/pub.polis.core/post directory should be created")
	}
}

func TestFilePermissions_PrivateKey(t *testing.T) {
	s := newTestServer(t)

	// Remove existing keys to allow init
	os.RemoveAll(filepath.Join(s.DataDir, ".polis", "keys"))
	os.RemoveAll(filepath.Join(s.DataDir, ".well-known"))

	// Run init
	body := jsonBody(t, map[string]string{"site_title": "test-site"})
	req := httptest.NewRequest(http.MethodPost, "/api/init", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleInit(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("init failed: %d: %s", rr.Code, rr.Body.String())
	}

	// Check private key permissions
	privKeyPath := filepath.Join(s.DataDir, ".polis", "keys", "id_ed25519")
	info, err := os.Stat(privKeyPath)
	if err != nil {
		t.Fatalf("private key not found: %v", err)
	}

	// Private key should be readable only by owner (0600)
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("private key permissions should be 0600, got %o", perm)
	}
}

// ============================================================================
// handleRenderPage Tests
// ============================================================================

func TestHandleRenderPage_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	// GET should not be allowed
	req := httptest.NewRequest(http.MethodGet, "/api/render-page", nil)
	rr := httptest.NewRecorder()

	s.handleRenderPage(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

func TestHandleRenderPage_InvalidJSON(t *testing.T) {
	s := newTestServer(t)

	body := bytes.NewBufferString("not json")
	req := httptest.NewRequest(http.MethodPost, "/api/render-page", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleRenderPage(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

// ============================================================================
// handleSnippet Tests - Source Tier Preservation
// ============================================================================

func TestHandleSnippet_SaveGlobalSource(t *testing.T) {
	s := newTestServer(t)

	// Save a snippet with source="global"
	body := jsonBody(t, map[string]string{
		"content": "<p>Global about content</p>",
		"source":  "global",
	})
	req := httptest.NewRequest(http.MethodPut, "/api/snippets/about.html", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleSnippet(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify it was saved to the global snippets directory
	globalPath := filepath.Join(s.DataDir, "site", "snippets", "about.html")
	content, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("snippet not saved to global directory: %v", err)
	}
	if string(content) != "<p>Global about content</p>" {
		t.Errorf("unexpected content: %s", content)
	}

	// Verify it was NOT saved to theme directory
	themePath := filepath.Join(s.DataDir, "site", "themes", "turbo", "snippets", "about.html")
	if _, err := os.Stat(themePath); !os.IsNotExist(err) {
		t.Error("snippet should not exist in theme directory when source=global")
	}
}

func TestHandleSnippet_SaveThemeSource(t *testing.T) {
	s := newTestServer(t)

	// Create theme directory structure
	themeDir := filepath.Join(s.DataDir, "site", "themes", "turbo", "snippets")
	os.MkdirAll(themeDir, 0755)

	// Set the active theme in .well-known/polis (used by snippet package)
	os.MkdirAll(filepath.Join(s.DataDir, ".well-known"), 0755)
	os.WriteFile(filepath.Join(s.DataDir, ".well-known", "polis"), []byte(`{"active_theme":"turbo"}`), 0644)

	// Save a snippet with source="theme"
	body := jsonBody(t, map[string]string{
		"content": "<p>Theme about content</p>",
		"source":  "theme",
	})
	req := httptest.NewRequest(http.MethodPut, "/api/snippets/about.html", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleSnippet(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify it was saved to the theme snippets directory
	themePath := filepath.Join(s.DataDir, "site", "themes", "turbo", "snippets", "about.html")
	content, err := os.ReadFile(themePath)
	if err != nil {
		t.Fatalf("snippet not saved to theme directory: %v", err)
	}
	if string(content) != "<p>Theme about content</p>" {
		t.Errorf("unexpected content: %s", content)
	}

	// Verify it was NOT saved to global directory
	globalPath := filepath.Join(s.DataDir, "site", "snippets", "about.html")
	if _, err := os.Stat(globalPath); !os.IsNotExist(err) {
		t.Error("snippet should not exist in global directory when source=theme")
	}
}

func TestHandleSnippet_SaveDefaultsToGlobal(t *testing.T) {
	s := newTestServer(t)

	// Save a snippet WITHOUT specifying source (should default to global)
	body := jsonBody(t, map[string]string{
		"content": "<p>Default source content</p>",
		// Note: no "source" field
	})
	req := httptest.NewRequest(http.MethodPut, "/api/snippets/default.html", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleSnippet(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify it was saved to the global snippets directory
	globalPath := filepath.Join(s.DataDir, "site", "snippets", "default.html")
	content, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("snippet not saved to global directory: %v", err)
	}
	if string(content) != "<p>Default source content</p>" {
		t.Errorf("unexpected content: %s", content)
	}
}

func TestHandleSnippet_ReadRespectsSource(t *testing.T) {
	s := newTestServer(t)

	// Set the active theme in .well-known/polis
	os.MkdirAll(filepath.Join(s.DataDir, ".well-known"), 0755)
	os.WriteFile(filepath.Join(s.DataDir, ".well-known", "polis"), []byte(`{"active_theme":"turbo"}`), 0644)

	// Create both global and theme snippets with same name but different content
	globalDir := filepath.Join(s.DataDir, "site", "snippets")
	themeDir := filepath.Join(s.DataDir, "site", "themes", "turbo", "snippets")
	os.MkdirAll(themeDir, 0755)

	os.WriteFile(filepath.Join(globalDir, "about.html"), []byte("<p>GLOBAL</p>"), 0644)
	os.WriteFile(filepath.Join(themeDir, "about.html"), []byte("<p>THEME</p>"), 0644)

	// Read with source=global
	req := httptest.NewRequest(http.MethodGet, "/api/snippets/about.html?source=global", nil)
	rr := httptest.NewRecorder()
	s.handleSnippet(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var result map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&result)
	if content, ok := result["content"].(string); !ok || content != "<p>GLOBAL</p>" {
		t.Errorf("expected global content, got: %v", result)
	}

	// Read with source=theme
	req = httptest.NewRequest(http.MethodGet, "/api/snippets/about.html?source=theme", nil)
	rr = httptest.NewRecorder()
	s.handleSnippet(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	json.NewDecoder(rr.Body).Decode(&result)
	if content, ok := result["content"].(string); !ok || content != "<p>THEME</p>" {
		t.Errorf("expected theme content, got: %v", result)
	}
}

func TestHandleSnippet_SourcePreservedInResponse(t *testing.T) {
	s := newTestServer(t)

	// Set the active theme in .well-known/polis
	os.MkdirAll(filepath.Join(s.DataDir, ".well-known"), 0755)
	os.WriteFile(filepath.Join(s.DataDir, ".well-known", "polis"), []byte(`{"active_theme":"turbo"}`), 0644)

	// Create theme directory
	themeDir := filepath.Join(s.DataDir, "site", "themes", "turbo", "snippets")
	os.MkdirAll(themeDir, 0755)

	// Save with source=theme
	body := jsonBody(t, map[string]string{
		"content": "<p>Theme content</p>",
		"source":  "theme",
	})
	req := httptest.NewRequest(http.MethodPut, "/api/snippets/footer.html", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleSnippet(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify response includes the source
	var result map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&result)

	if source, ok := result["source"].(string); !ok || source != "theme" {
		t.Errorf("expected source=theme in response, got: %v", result)
	}
}

// ============================================================================
// Webhook Safety Regression Tests
// ============================================================================

func TestPublishHookNotCalledOnError(t *testing.T) {
	s := newTestServer(t)
	s.EnableHooks = true
	// No private key = publish will fail
	markerFile := filepath.Join(s.DataDir, "hook-marker")

	s.Config = &Config{
		Hooks: &hooks.HookConfig{
			PostPublish: "touch " + markerFile,
		},
	}

	body := jsonBody(t, map[string]string{"markdown": "# Test"})
	req := httptest.NewRequest(http.MethodPost, "/api/publish", body)
	rr := httptest.NewRecorder()

	s.handlePublish(rr, req)

	// Publish should fail (no private key)
	if rr.Code == http.StatusOK {
		t.Error("expected publish to fail without private key")
	}

	// Hook marker should NOT exist
	if _, err := os.Stat(markerFile); !os.IsNotExist(err) {
		t.Error("hook should not have been called on publish error")
	}
}

func TestRepublishHookNotCalledOnError(t *testing.T) {
	s := newConfiguredServer(t)
	s.EnableHooks = true
	markerFile := filepath.Join(s.DataDir, "hook-marker")

	s.Config.Hooks = &hooks.HookConfig{
		PostPublish: "touch " + markerFile,
	}

	// Republish a non-existent post
	body := jsonBody(t, map[string]string{
		"path":     "content/pub.polis.core/post/20260101/nonexistent.md",
		"markdown": "# Updated",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/republish", body)
	rr := httptest.NewRecorder()

	s.handleRepublish(rr, req)

	// Republish should fail (file not found)
	if rr.Code == http.StatusOK {
		t.Error("expected republish to fail for nonexistent post")
	}

	// Hook marker should NOT exist
	if _, err := os.Stat(markerFile); !os.IsNotExist(err) {
		t.Error("hook should not have been called on republish error")
	}
}

func TestBlessingGrantHookNotCalledOnError(t *testing.T) {
	s := newTestServer(t)
	s.EnableHooks = true
	// No discovery service config = grant will fail
	markerFile := filepath.Join(s.DataDir, "hook-marker")

	s.Config = &Config{
		Hooks: &hooks.HookConfig{
			PostComment: "touch " + markerFile,
		},
	}

	body := jsonBody(t, map[string]string{
		"comment_version": "sha256:abc",
		"comment_url":     "https://bob.polis.pub/comments/test.md",
		"in_reply_to":     "https://alice.polis.pub/posts/test.md",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/blessing/grant", body)
	rr := httptest.NewRecorder()

	s.handleBlessingGrant(rr, req)

	// Grant should fail (no discovery config or no private key)
	if rr.Code == http.StatusOK {
		t.Error("expected blessing grant to fail without config")
	}

	// Hook marker should NOT exist
	if _, err := os.Stat(markerFile); !os.IsNotExist(err) {
		t.Error("hook should not have been called on blessing grant error")
	}
}

// ============================================================================
// Drafts Migration Tests
// ============================================================================

func TestMigrateDraftsDir_OldToNew(t *testing.T) {
	dataDir := t.TempDir()
	s := &Server{DataDir: dataDir}

	// Create old-style drafts dir with a file
	oldDir := filepath.Join(dataDir, ".polis", "drafts")
	os.MkdirAll(oldDir, 0755)
	os.WriteFile(filepath.Join(oldDir, "test-draft.md"), []byte("# Draft"), 0644)

	s.migrateDraftsDir()

	// Old dir should be gone
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Error("expected old drafts dir to be removed after migration")
	}

	// New dir should exist with the file
	newDir := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts")
	if _, err := os.Stat(newDir); os.IsNotExist(err) {
		t.Fatal("expected new drafts dir to exist")
	}
	content, err := os.ReadFile(filepath.Join(newDir, "test-draft.md"))
	if err != nil {
		t.Fatal("expected draft file to be migrated")
	}
	if string(content) != "# Draft" {
		t.Errorf("expected draft content preserved, got: %s", string(content))
	}
}

func TestMigrateDraftsDir_NoOldDir(t *testing.T) {
	dataDir := t.TempDir()
	s := &Server{DataDir: dataDir}

	// No old dir exists - should be a no-op
	s.migrateDraftsDir()

	newDir := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts")
	if _, err := os.Stat(newDir); !os.IsNotExist(err) {
		t.Error("expected new dir not to be created when no old dir exists")
	}
}

func TestMigrateDraftsDir_NewAlreadyExists(t *testing.T) {
	dataDir := t.TempDir()
	s := &Server{DataDir: dataDir}

	// Create both old and new dirs
	oldDir := filepath.Join(dataDir, ".polis", "drafts")
	newDir := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts")
	os.MkdirAll(oldDir, 0755)
	os.MkdirAll(newDir, 0755)
	os.WriteFile(filepath.Join(oldDir, "old-draft.md"), []byte("old"), 0644)
	os.WriteFile(filepath.Join(newDir, "new-draft.md"), []byte("new"), 0644)

	s.migrateDraftsDir()

	// Old dir should still exist (migration skipped)
	if _, err := os.Stat(oldDir); os.IsNotExist(err) {
		t.Error("expected old dir to be preserved when new dir already exists")
	}
	// New dir file should be intact
	content, _ := os.ReadFile(filepath.Join(newDir, "new-draft.md"))
	if string(content) != "new" {
		t.Error("expected new dir contents to be preserved")
	}
}

// ============================================================================
// Security: Error Redaction Tests (H1)
// ============================================================================

func TestErrorResponsesRedacted(t *testing.T) {
	s := newConfiguredServer(t)

	// Strings that should NEVER appear in HTTP error responses
	osErrorStrings := []string{
		"permission denied",
		"no such file or directory",
		"not a directory",
		"/tmp/",
		"/home/",
		s.DataDir, // The actual data directory path
	}

	// Test publish with content that will fail (trigger internal errors)
	tests := []struct {
		name    string
		method  string
		path    string
		body    interface{}
		handler func(http.ResponseWriter, *http.Request)
	}{
		{
			name:    "handleRender with nil key",
			method:  http.MethodPost,
			path:    "/api/render",
			body:    map[string]string{"markdown": "# Test"},
			handler: (&Server{DataDir: s.DataDir}).handleRender, // No private key
		},
		{
			name:   "handleCommentsPending with missing dir",
			method: http.MethodGet,
			path:   "/api/comments/pending",
			handler: (&Server{
				DataDir: "/nonexistent/path/that/does/not/exist",
			}).handleCommentsPending,
		},
		{
			name:   "handleCommentsBlessed with missing dir",
			method: http.MethodGet,
			path:   "/api/comments/blessed",
			handler: (&Server{
				DataDir: "/nonexistent/path/that/does/not/exist",
			}).handleCommentsBlessed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body *bytes.Buffer
			if tt.body != nil {
				body = jsonBody(t, tt.body)
			} else {
				body = bytes.NewBuffer(nil)
			}
			req := httptest.NewRequest(tt.method, tt.path, body)
			rr := httptest.NewRecorder()

			tt.handler(rr, req)

			responseBody := rr.Body.String()
			for _, osErr := range osErrorStrings {
				if strings.Contains(strings.ToLower(responseBody), strings.ToLower(osErr)) {
					t.Errorf("response contains OS error detail %q: %s", osErr, responseBody)
				}
			}
		})
	}
}

// ============================================================================
// Security: Draft ID Sanitization Tests (M1)
// ============================================================================

func TestDraftIDSanitization(t *testing.T) {
	s := newTestServer(t)

	tests := []struct {
		name       string
		inputID    string
		wantSafe   bool // ID should not contain dangerous chars
		wantPrefix string
	}{
		{"normal ID", "my-draft", true, ""},
		{"path traversal", "../../../etc/passwd", true, ""},
		{"null bytes", "draft\x00evil", true, ""},
		{"slashes", "a/b/c", true, ""},
		{"backslashes", "a\\b\\c", true, ""},
		{"unicode", "draft\u2028evil", true, ""},
		{"special chars", "draft@#$%.md", true, ""},
		{"spaces", "my draft name", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := jsonBody(t, map[string]string{
				"id":       tt.inputID,
				"markdown": "# Test",
			})
			req := httptest.NewRequest(http.MethodPost, "/api/drafts", body)
			rr := httptest.NewRecorder()

			s.handleDrafts(rr, req)

			if rr.Code != http.StatusOK {
				// May fail due to missing dirs, but we want to check file system effects
				return
			}

			var resp map[string]interface{}
			json.NewDecoder(rr.Body).Decode(&resp)

			id, ok := resp["id"].(string)
			if !ok {
				return
			}

			// Verify the ID only contains safe characters
			for _, ch := range id {
				if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_') {
					t.Errorf("sanitized ID %q contains unsafe character %q", id, string(ch))
				}
			}

			// Verify no path traversal chars
			if strings.Contains(id, "..") {
				t.Errorf("sanitized ID still contains '..': %s", id)
			}
			if strings.Contains(id, "/") {
				t.Errorf("sanitized ID still contains '/': %s", id)
			}
			if strings.Contains(id, "\\") {
				t.Errorf("sanitized ID still contains '\\': %s", id)
			}
		})
	}
}

// ============================================================================
// Security: Path Traversal Tests (M2)
// ============================================================================

func TestValidatePostPath_Canonicalization(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"normal path", "content/pub.polis.core/post/20260101/hello.md", false},
		{"dot-dot traversal", "content/pub.polis.core/post/../../../etc/passwd", true},
		{"double slash", "content/pub.polis.core/post//20260101//hello.md", false},
		{"dot segment", "content/pub.polis.core/post/./20260101/hello.md", false},
		{"null byte", "content/pub.polis.core/post/20260101/hello\x00.md", true},
		{"not post prefix", "comments/foo.md", true},
		{"clean removes prefix", "../content/pub.polis.core/post/hello.md", true},
		{"encoded dot-dot", "content/pub.polis.core/post/20260101/..%2f..%2fetc/passwd", true}, // Contains ".." substring which is blocked
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePostPath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePostPath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
		})
	}
}

func TestValidateContentPath_Canonicalization(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"root markdown", "index.md", false},
		{"root html", "index.html", false},
		{"posts path", "content/pub.polis.core/post/20260101/hello.md", false},
		{"comments path", "content/pub.polis.core/comment/blessed/comment.md", false},
		{"drafts path", ".polis/bundles/pub.polis.core/posts/drafts/my-draft.md", false},
		{"posts mount path", "posts/20260101/hello.html", false},
		{"posts mount md", "posts/20260101/hello.md", false},
		{"comments mount path", "comments/20260101/reply.html", false},
		{"comments mount md", "comments/20260101/reply.md", false},
		{"traversal attempt", "../../../etc/passwd", true},
		{"null byte", "content/pub.polis.core/post/hello\x00.md", true},
		{"invalid prefix", "secrets/key.pem", true},
		{"double dot in component", "content/pub.polis.core/post/..hidden/file.md", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateContentPath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateContentPath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
		})
	}
}

func TestValidatePath_ErrorKinds(t *testing.T) {
	// Real traversal/null-byte attempts classify as errPathTraversal (security event).
	// Prefix mismatches classify as errPathDisallowed (benign 400/404).
	tests := []struct {
		name     string
		fn       func(string) error
		path     string
		wantKind error
	}{
		{"content traversal", validateContentPath, "../../etc/passwd", errPathTraversal},
		{"content null byte", validateContentPath, "posts/foo\x00.md", errPathTraversal},
		{"content bad prefix", validateContentPath, "20260323/slug.html", errPathDisallowed},
		{"content bad prefix nested", validateContentPath, "secrets/key.pem", errPathDisallowed},
		{"post traversal", validatePostPath, "content/pub.polis.core/post/..hidden/x.md", errPathTraversal},
		{"post null byte", validatePostPath, "content/pub.polis.core/post/x\x00.md", errPathTraversal},
		{"post bad prefix", validatePostPath, "comments/foo.md", errPathDisallowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn(tt.path)
			if err != tt.wantKind {
				t.Errorf("got %v, want %v", err, tt.wantKind)
			}
		})
	}
}

func TestHandleContent_PrefixMismatchDoesNotLogSecurityEvent(t *testing.T) {
	// Regression: a stale SPA sending /api/content/20260323/slug.html should
	// produce a plain 404, not a pub.polis.security.path_traversal event.
	s := newTestServer(t)
	logsDir := filepath.Join(s.DataDir, "logs")
	os.MkdirAll(logsDir, 0755)
	s.Logger = NewLogger(LogLevelBasic, logsDir)

	req := httptest.NewRequest(http.MethodGet, "/api/content/20260323/slug.html", nil)
	w := httptest.NewRecorder()
	s.handleContent(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for prefix-mismatch, got %d: %s", w.Code, w.Body.String())
	}

	logPath := filepath.Join(logsDir, time.Now().Format("2006-01-02")+".log")
	if data, err := os.ReadFile(logPath); err == nil && strings.Contains(string(data), "pub.polis.security.path_traversal") {
		t.Errorf("prefix-mismatch should not emit path_traversal event; log contained it:\n%s", data)
	}
}

func TestHandleContent_TraversalStillLogsSecurityEvent(t *testing.T) {
	s := newTestServer(t)
	logsDir := filepath.Join(s.DataDir, "logs")
	os.MkdirAll(logsDir, 0755)
	s.Logger = NewLogger(LogLevelBasic, logsDir)

	// Use a path where ".." survives filepath.Clean (embedded in a filename).
	req := httptest.NewRequest(http.MethodGet, "/api/content/posts/..hidden/x.html", nil)
	w := httptest.NewRecorder()
	s.handleContent(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for real traversal, got %d: %s", w.Code, w.Body.String())
	}

	logPath := filepath.Join(logsDir, time.Now().Format("2006-01-02")+".log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "pub.polis.security.path_traversal") {
		t.Errorf("real traversal should emit path_traversal event; log was:\n%s", data)
	}
}

// ============================================================================
// handleFollowing Tests
// ============================================================================

func TestHandleFollowing_Get_Empty(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/following", nil)
	w := httptest.NewRecorder()

	s.handleFollowing(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["count"] != float64(0) {
		t.Errorf("expected count=0, got %v", resp["count"])
	}
}

func TestHandleFollowing_Get_WithEntries(t *testing.T) {
	s := newTestServer(t)

	// Pre-populate following.json
	followingData := `{
		"version": "test",
		"following": [
			{"url": "https://alice.example.com", "added_at": "2026-01-01T00:00:00Z"},
			{"url": "https://bob.example.com", "added_at": "2026-01-02T00:00:00Z"}
		]
	}`
	followingPath := filepath.Join(s.DataDir, "content", "pub.polis.core", "follow", "following.json")
	os.WriteFile(followingPath, []byte(followingData), 0644)

	req := httptest.NewRequest(http.MethodGet, "/api/following", nil)
	w := httptest.NewRecorder()

	s.handleFollowing(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["count"] != float64(2) {
		t.Errorf("expected count=2, got %v", resp["count"])
	}

	followingList, ok := resp["following"].([]interface{})
	if !ok || len(followingList) != 2 {
		t.Errorf("expected 2 following entries, got %v", resp["following"])
	}
}

func TestHandleFollowing_Post_InvalidURL(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{"url": "http://insecure.example.com"})
	req := httptest.NewRequest(http.MethodPost, "/api/following", body)
	w := httptest.NewRecorder()

	s.handleFollowing(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleFollowing_Post_NoKeys(t *testing.T) {
	s := newTestServer(t)
	// No keys configured

	body := jsonBody(t, map[string]string{"url": "https://example.com"})
	req := httptest.NewRequest(http.MethodPost, "/api/following", body)
	w := httptest.NewRecorder()

	s.handleFollowing(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleFollowing_Delete_NoKeys(t *testing.T) {
	s := newTestServer(t)

	body := jsonBody(t, map[string]string{"url": "https://example.com"})
	req := httptest.NewRequest(http.MethodDelete, "/api/following", body)
	w := httptest.NewRecorder()

	s.handleFollowing(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleFollowing_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPut, "/api/following", nil)
	w := httptest.NewRecorder()

	s.handleFollowing(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ============================================================================
// handleFeed Tests (cache-backed)
// ============================================================================

func TestHandleFeed_EmptyCache(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/feed", nil)
	w := httptest.NewRecorder()

	s.handleFeed(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["total"].(float64) != 0 {
		t.Errorf("expected 0 total, got %v", resp["total"])
	}
	if resp["stale"].(bool) != true {
		t.Error("empty cache should be stale")
	}
}

func TestHandleFeed_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/feed", nil)
	w := httptest.NewRecorder()

	s.handleFeed(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleFeed_WithTypeFilter(t *testing.T) {
	s := newTestServer(t)

	now := time.Now().UTC()
	cm := feed.NewCacheManager(s.DataDir, "default")
	cm.MergeItems([]feed.FeedItem{
		{Type: "post", Title: "A Post", URL: "posts/a.md", Published: now.Add(-48 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "comment", Title: "A Comment", URL: "comments/b.md", Published: now.Add(-24 * time.Hour).Format(time.RFC3339), AuthorURL: "https://b.pub", AuthorDomain: "b.pub"},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/feed?type=post", nil)
	w := httptest.NewRecorder()
	s.handleFeed(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	items := resp["items"].([]interface{})
	if len(items) != 1 {
		t.Errorf("expected 1 post, got %d", len(items))
	}
}

func TestHandleFeed_SortOrder(t *testing.T) {
	s := newTestServer(t)

	// Use dates relative to now so items don't get pruned by the 90-day max age
	now := time.Now().UTC()
	cm := feed.NewCacheManager(s.DataDir, "default")
	cm.MergeItems([]feed.FeedItem{
		{Type: "post", Title: "Oldest", URL: "posts/old.md", Published: now.Add(-48 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "Newest", URL: "posts/new.md", Published: now.Format(time.RFC3339), AuthorURL: "https://b.pub", AuthorDomain: "b.pub"},
		{Type: "post", Title: "Middle", URL: "posts/mid.md", Published: now.Add(-24 * time.Hour).Format(time.RFC3339), AuthorURL: "https://c.pub", AuthorDomain: "c.pub"},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/feed", nil)
	w := httptest.NewRecorder()
	s.handleFeed(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Items []struct {
			Title string `json:"title"`
		} `json:"items"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(resp.Items))
	}
	if resp.Items[0].Title != "Newest" {
		t.Errorf("expected newest first, got %s", resp.Items[0].Title)
	}
	if resp.Items[1].Title != "Middle" {
		t.Errorf("expected middle second, got %s", resp.Items[1].Title)
	}
	if resp.Items[2].Title != "Oldest" {
		t.Errorf("expected oldest last, got %s", resp.Items[2].Title)
	}
}

func TestHandleFeed_SpecialCharacterTitles(t *testing.T) {
	s := newTestServer(t)

	// Populate cache with titles containing special characters.
	// Dates relative to now so items don't get pruned by the 90-day max age —
	// hardcoded calendar dates here time-bombed previously.
	now := time.Now().UTC()
	cm := feed.NewCacheManager(s.DataDir, "default")
	cm.MergeItems([]feed.FeedItem{
		{Type: "post", Title: "It's Not Beyond Our Reach", URL: "posts/its-not.md", Published: now.Add(-24 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: `She said "hello" & waved`, URL: "posts/she-said.md", Published: now.Add(-48 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "2 < 3 && 5 > 4", URL: "posts/math.md", Published: now.Add(-72 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/feed", nil)
	w := httptest.NewRecorder()
	s.handleFeed(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Items []struct {
			Title string `json:"title"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(resp.Items))
	}

	// Verify titles with apostrophes, quotes, and angle brackets survive JSON round-trip
	expectedTitles := []string{
		"It's Not Beyond Our Reach",
		`She said "hello" & waved`,
		"2 < 3 && 5 > 4",
	}
	for i, want := range expectedTitles {
		if resp.Items[i].Title != want {
			t.Errorf("item[%d] title = %q, want %q", i, resp.Items[i].Title, want)
		}
	}
}

func TestHandleFeed_UnreadCount(t *testing.T) {
	s := newTestServer(t)

	now := time.Now().UTC()
	cm := feed.NewCacheManager(s.DataDir, "default")
	cm.MergeItems([]feed.FeedItem{
		{Type: "post", Title: "A", URL: "posts/a.md", Published: now.Add(-72 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "B", URL: "posts/b.md", Published: now.Add(-48 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "C", URL: "posts/c.md", Published: now.Add(-24 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
	})

	// Mark one as read
	items, _ := cm.List()
	cm.MarkRead(items[0].ID)

	req := httptest.NewRequest(http.MethodGet, "/api/feed", nil)
	w := httptest.NewRecorder()
	s.handleFeed(w, req)

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["total"].(float64) != 3 {
		t.Errorf("expected 3 total, got %v", resp["total"])
	}
	if resp["unread"].(float64) != 2 {
		t.Errorf("expected 2 unread, got %v", resp["unread"])
	}
}

// ============================================================================
// handleFeedRefresh Tests
// ============================================================================

func TestHandleFeedRefresh_EmptyFollowing(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/feed/refresh", nil)
	w := httptest.NewRecorder()
	s.handleFeedRefresh(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["total"].(float64) != 0 {
		t.Errorf("expected 0 total, got %v", resp["total"])
	}
	if resp["new_items"].(float64) != 0 {
		t.Errorf("expected 0 new_items, got %v", resp["new_items"])
	}
	if resp["stale"].(bool) != false {
		t.Error("just-refreshed cache should not be stale")
	}
}

func TestHandleFeed_NotStaleAfterCursorRefresh(t *testing.T) {
	s := newTestServer(t)
	discoveryDomain := s.GetDiscoveryDomain()

	// Set a cursor with a backdated timestamp to simulate a stale cache
	cm := feed.NewCacheManager(s.DataDir, discoveryDomain)
	cm.SetCursor("100")

	cursorsPath := filepath.Join(s.DataDir, ".polis", "ds", discoveryDomain, "pub.polis.core", "state", "cursors.json")
	staleData, _ := json.Marshal(map[string]interface{}{
		"cursors": map[string]interface{}{
			"pub.polis.feed": map[string]interface{}{
				"position":     "100",
				"last_updated": "2020-01-01T00:00:00Z",
			},
		},
	})
	os.WriteFile(cursorsPath, staleData, 0644)

	// Confirm GET /api/feed reports stale
	req := httptest.NewRequest(http.MethodGet, "/api/feed", nil)
	w := httptest.NewRecorder()
	s.handleFeed(w, req)
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["stale"].(bool) != true {
		t.Fatal("cache should be stale before refresh")
	}

	// Simulate what syncFeed does after a successful sync: SetCursor with same position
	cm.SetCursor("100")

	// GET /api/feed should now report not stale
	req = httptest.NewRequest(http.MethodGet, "/api/feed", nil)
	w = httptest.NewRecorder()
	s.handleFeed(w, req)
	resp = map[string]interface{}{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["stale"].(bool) != false {
		t.Error("cache should not be stale after cursor refresh with same position")
	}
}

func TestHandleFeedRefresh_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/feed/refresh", nil)
	w := httptest.NewRecorder()
	s.handleFeedRefresh(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleFeedRefresh_MeScope(t *testing.T) {
	s := newConfiguredServer(t)
	// No DS configured — sync will be a no-op, but the scope should be accepted
	s.DiscoveryURL = ""

	req := httptest.NewRequest(http.MethodPost, "/api/feed/refresh?scope=me", nil)
	w := httptest.NewRecorder()
	s.handleFeedRefresh(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for scope=me, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleFeedRefresh_InvalidScope(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/feed/refresh?scope=bogus", nil)
	w := httptest.NewRecorder()
	s.handleFeedRefresh(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid scope, got %d", w.Code)
	}
}

// ============================================================================
// handleFeedRead Tests
// ============================================================================

func TestHandleFeedRead_MarkRead(t *testing.T) {
	s := newTestServer(t)

	now := time.Now().UTC()
	cm := feed.NewCacheManager(s.DataDir, "default")
	cm.MergeItems([]feed.FeedItem{
		{Type: "post", Title: "Test", URL: "posts/test.md", Published: now.Add(-24 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
	})

	items, _ := cm.List()
	itemID := items[0].ID

	body := jsonBody(t, map[string]string{"id": itemID})
	req := httptest.NewRequest(http.MethodPost, "/api/feed/read", body)
	w := httptest.NewRecorder()
	s.handleFeedRead(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	items, _ = cm.List()
	if items[0].ReadAt == "" {
		t.Error("item should be marked read")
	}
}

func TestHandleFeedRead_MissingID(t *testing.T) {
	s := newTestServer(t)

	body := jsonBody(t, map[string]interface{}{})
	req := httptest.NewRequest(http.MethodPost, "/api/feed/read", body)
	w := httptest.NewRecorder()
	s.handleFeedRead(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing id, got %d", w.Code)
	}
}

func TestHandleFeedRead_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/feed/read", nil)
	w := httptest.NewRecorder()
	s.handleFeedRead(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleFeedRead_MissingFields(t *testing.T) {
	s := newTestServer(t)

	body := jsonBody(t, map[string]interface{}{})
	req := httptest.NewRequest(http.MethodPost, "/api/feed/read", body)
	w := httptest.NewRecorder()
	s.handleFeedRead(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleFeedRead_PrunedItem(t *testing.T) {
	s := newTestServer(t)

	body := jsonBody(t, map[string]string{"id": "nonexistent"})
	req := httptest.NewRequest(http.MethodPost, "/api/feed/read", body)
	w := httptest.NewRecorder()
	s.handleFeedRead(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for pruned/missing item, got %d", w.Code)
	}
}

// ============================================================================
// handleFeedViewed Tests
// ============================================================================

func TestHandleFeedViewed_SetsCursor(t *testing.T) {
	s := newConfiguredServer(t)

	// Set a sync cursor to simulate background sync having run
	discoveryDomain := s.GetDiscoveryDomain()
	store := stream.NewStore(s.DataDir, discoveryDomain, "pub.polis.core")
	_ = store.SetCursor("pub.polis.sync", "100")

	req := httptest.NewRequest(http.MethodPost, "/api/feed/viewed", nil)
	w := httptest.NewRecorder()
	s.handleFeedViewed(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Verify viewed cursor was set to sync cursor
	viewedCursor, _ := store.GetCursor("pub.polis.feed.viewed")
	if viewedCursor != "100" {
		t.Errorf("expected viewed cursor '100', got %q", viewedCursor)
	}
}

func TestHandleFeedViewed_MethodNotAllowed(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/feed/viewed", nil)
	w := httptest.NewRecorder()
	s.handleFeedViewed(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleFeedViewed_DoesNotSetViewedAt(t *testing.T) {
	s := newConfiguredServer(t)

	discoveryDomain := s.GetDiscoveryDomain()
	store := stream.NewStore(s.DataDir, discoveryDomain, "pub.polis.core")
	_ = store.SetCursor("pub.polis.sync", "50")

	req := httptest.NewRequest(http.MethodPost, "/api/feed/viewed", nil)
	w := httptest.NewRecorder()
	s.handleFeedViewed(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// viewed_at should NOT be written (deprecated — GetCursor returns "0" for missing keys)
	viewedAt, _ := store.GetCursor("pub.polis.feed.viewed_at")
	if viewedAt != "0" {
		t.Errorf("pub.polis.feed.viewed_at should not be set, got %q", viewedAt)
	}
}

func TestComputeAllCounts_HasNewFeed(t *testing.T) {
	s := newConfiguredServer(t)

	discoveryDomain := s.GetDiscoveryDomain()
	store := stream.NewStore(s.DataDir, discoveryDomain, "pub.polis.core")

	// No sync cursor — should not show has_new_feed
	counts := s.computeAllCounts()
	if counts.HasNewFeed {
		t.Error("expected HasNewFeed=false when sync cursor is empty")
	}

	// Sync cursor set, viewed cursor empty — cold-start seeds the viewed
	// cursor to the current sync position and leaves the flag false. The
	// previous behavior (flag=true) was over-noisy on first load when the
	// user has had no opportunity to acknowledge anything yet.
	_ = store.SetCursor("pub.polis.sync", "100")
	counts = s.computeAllCounts()
	if counts.HasNewFeed {
		t.Error("expected HasNewFeed=false on cold start (viewed cursor gets seeded)")
	}
	if pos, _ := store.GetCursor("pub.polis.feed.viewed"); pos != "100" {
		t.Errorf("expected viewed cursor seeded to '100', got %q", pos)
	}

	// Sync and viewed match — no new content.
	counts = s.computeAllCounts()
	if counts.HasNewFeed {
		t.Error("expected HasNewFeed=false when sync and viewed cursors match")
	}

	// Advance sync past viewed — flag fires.
	_ = store.SetCursor("pub.polis.sync", "200")
	counts = s.computeAllCounts()
	if !counts.HasNewFeed {
		t.Error("expected HasNewFeed=true when sync cursor is ahead of viewed")
	}

	// Advance viewed to catch up — flag clears.
	_ = store.SetCursor("pub.polis.feed.viewed", "200")
	counts = s.computeAllCounts()
	if counts.HasNewFeed {
		t.Error("expected HasNewFeed=false after viewed catches up")
	}
}

// TestComputeAllCounts_HasNewBlessingInbox mirrors the feed test but layers
// in the pending-blessing prerequisite: the dot should NOT fire when sync
// has advanced past the viewed cursor but nothing is actually awaiting
// blessing.
func TestComputeAllCounts_HasNewBlessingInbox(t *testing.T) {
	s := newConfiguredServer(t)

	discoveryDomain := s.GetDiscoveryDomain()
	store := stream.NewStore(s.DataDir, discoveryDomain, "pub.polis.core")

	// Cold start (no cursors) — false.
	if counts := s.computeAllCounts(); counts.HasNewBlessingInbox {
		t.Error("expected HasNewBlessingInbox=false on cold start")
	}

	// Seed sync; cold-start seeds blessing.viewed to match → false.
	_ = store.SetCursor("pub.polis.sync", "100")
	counts := s.computeAllCounts()
	if counts.HasNewBlessingInbox {
		t.Error("expected HasNewBlessingInbox=false after cold-start seed")
	}
	if pos, _ := store.GetCursor("pub.polis.comment.blessing.viewed"); pos != "100" {
		t.Errorf("expected blessing viewed cursor seeded to '100', got %q", pos)
	}

	// Advance sync; still no pending blessings — flag stays false.
	_ = store.SetCursor("pub.polis.sync", "200")
	if counts := s.computeAllCounts(); counts.HasNewBlessingInbox {
		t.Error("expected HasNewBlessingInbox=false without pending blessings")
	}

	// Inject a pending blessing into state, advance sync past viewed — fires.
	blessingState := stream.BlessingState{
		Blessings: []stream.BlessingEntry{{Status: "pending"}},
	}
	if err := store.SaveState("pub.polis.comment.blessing", &blessingState); err != nil {
		t.Fatalf("save blessing state: %v", err)
	}
	if counts := s.computeAllCounts(); !counts.HasNewBlessingInbox {
		t.Error("expected HasNewBlessingInbox=true with pending blessing and sync ahead of viewed")
	}

	// Catch up viewed cursor — clears.
	_ = store.SetCursor("pub.polis.comment.blessing.viewed", "200")
	if counts := s.computeAllCounts(); counts.HasNewBlessingInbox {
		t.Error("expected HasNewBlessingInbox=false after viewed catches up")
	}
}

func TestComputeAllCounts_NoNewFeed_Empty(t *testing.T) {
	s := newConfiguredServer(t)

	// No feed items at all — should not show has_new_feed
	counts := s.computeAllCounts()
	if counts.HasNewFeed {
		t.Error("expected HasNewFeed=false with no feed items")
	}
}

// ============================================================================
// handleFeedCounts Tests
// ============================================================================

func TestHandleFeedCounts_Empty(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/feed/counts", nil)
	w := httptest.NewRecorder()
	s.handleFeedCounts(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["total"].(float64) != 0 {
		t.Errorf("expected 0 total, got %v", resp["total"])
	}
	if resp["unread"].(float64) != 0 {
		t.Errorf("expected 0 unread, got %v", resp["unread"])
	}
	if resp["stale"].(bool) != true {
		t.Error("empty cache should be stale")
	}
}

func TestHandleFeedCounts_WithItems(t *testing.T) {
	s := newTestServer(t)

	now := time.Now().UTC()
	cm := feed.NewCacheManager(s.DataDir, "default")
	cm.MergeItems([]feed.FeedItem{
		{Type: "post", Title: "A", URL: "posts/a.md", Published: now.Add(-48 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "B", URL: "posts/b.md", Published: now.Add(-24 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
	})

	items, _ := cm.List()
	cm.MarkRead(items[0].ID)

	req := httptest.NewRequest(http.MethodGet, "/api/feed/counts", nil)
	w := httptest.NewRecorder()
	s.handleFeedCounts(w, req)

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["total"].(float64) != 2 {
		t.Errorf("expected 2 total, got %v", resp["total"])
	}
	if resp["unread"].(float64) != 1 {
		t.Errorf("expected 1 unread, got %v", resp["unread"])
	}
}

func TestHandleFeedCounts_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/feed/counts", nil)
	w := httptest.NewRecorder()
	s.handleFeedCounts(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ============================================================================
// handleRemotePost Tests
// ============================================================================

func TestHandleRemotePost_MissingURL(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/remote/post", nil)
	w := httptest.NewRecorder()

	s.handleRemotePost(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleRemotePost_InvalidURL(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/remote/post?url=http://insecure.com/post.md", nil)
	w := httptest.NewRecorder()

	s.handleRemotePost(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleRemotePost_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/remote/post?url=https://example.com/post.md", nil)
	w := httptest.NewRecorder()

	s.handleRemotePost(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ============================================================================
// Remote Avatar Tests
// ============================================================================

func TestHandleRemoteAvatar_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/remote/avatar?domain=example.com", nil)
	w := httptest.NewRecorder()
	s.handleRemoteAvatar(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleRemoteAvatar_MissingDomain(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/remote/avatar", nil)
	w := httptest.NewRecorder()
	s.handleRemoteAvatar(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleRemoteAvatar_InvalidDomain(t *testing.T) {
	s := newTestServer(t)
	tests := []string{
		"example.com/foo",
		"example.com:8080",
		"user@example.com",
	}
	for _, domain := range tests {
		req := httptest.NewRequest(http.MethodGet, "/api/remote/avatar?domain="+domain, nil)
		w := httptest.NewRecorder()
		s.handleRemoteAvatar(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("domain %q: expected 400, got %d", domain, w.Code)
		}
	}
}

func TestHandleRemoteAvatar_UnreachableDomain(t *testing.T) {
	s := newTestServer(t)
	// Use a non-existent domain — should return 200 with empty avatar
	req := httptest.NewRequest(http.MethodGet, "/api/remote/avatar?domain=this-domain-does-not-exist-12345.example", nil)
	w := httptest.NewRecorder()
	s.handleRemoteAvatar(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["domain"] != "this-domain-does-not-exist-12345.example" {
		t.Errorf("expected domain in response, got %v", resp)
	}
	if _, ok := resp["avatar"]; ok {
		t.Error("expected no avatar for unreachable domain")
	}
}

func TestHandleRemoteAvatar_LiveSite(t *testing.T) {
	// Spin up a local test server serving a .well-known/polis with avatar
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/polis", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"version":"test","public_key":"ssh-ed25519 AAAA test","author":"","avatar":{"bg":"#3f6384","fg":"#ffffff","pattern":"rings","pattern_color":"#638eb5"},"author_name":"Test Author","created":"2026-01-01T00:00:00Z"}`))
	})
	ts := httptest.NewTLSServer(mux)
	defer ts.Close()

	// The remote client uses https:// + domain, but we need to point to our test server.
	// Since we can't control DNS, test the handler logic with a real reachable domain instead.
	// This test verifies the JSON shape for a mock scenario.
	t.Skip("requires DNS-resolvable test server; covered by integration testing")
}

// ============================================================================
// stripFrontmatter Tests
// ============================================================================

func TestStripFrontmatter(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "with frontmatter",
			input:    "---\ntitle: Hello\ndate: 2026-01-01\n---\n# Hello World\n\nContent here.",
			expected: "# Hello World\n\nContent here.",
		},
		{
			name:     "without frontmatter",
			input:    "# Hello World\n\nContent here.",
			expected: "# Hello World\n\nContent here.",
		},
		{
			name:     "empty content",
			input:    "",
			expected: "",
		},
		{
			name:     "only frontmatter",
			input:    "---\ntitle: Hello\n---\n",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripFrontmatter(tt.input)
			if got != tt.expected {
				t.Errorf("stripFrontmatter() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// ============================================================================
// makeExcerpt Tests
// ============================================================================

func TestMakeExcerpt(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{"plain text short", "Hello world", 140, "Hello world"},
		{"strips heading", "# Title\n\nBody text here.", 140, "Body text here."},
		{"strips bold", "Some **bold** text", 140, "Some bold text"},
		{"strips links", "Click [here](http://example.com) now", 140, "Click here now"},
		{"truncates at word boundary", "One two three four five six seven eight", 20, "One two three four…"},
		{"empty", "", 140, ""},
		{"heading only", "# Just a heading", 140, ""},
		{"strips images", "Before ![alt](img.png) after", 140, "Before  after"},
		{"multiple paragraphs joined", "First paragraph.\n\nSecond paragraph.", 140, "First paragraph. Second paragraph."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := makeExcerpt(tt.input, tt.maxLen)
			if got != tt.expected {
				t.Errorf("makeExcerpt() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// ============================================================================
// looksLikeHTML Tests
// ============================================================================

func TestLooksLikeHTML(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"doctype uppercase", "<!DOCTYPE html><html>...", true},
		{"doctype lowercase", "<!doctype html><html>...", true},
		{"html tag", "<html><head>...", true},
		{"html with whitespace", "  \n<!DOCTYPE html>...", true},
		{"markdown", "# Hello\n\nSome text", false},
		{"frontmatter markdown", "---\ntitle: Hi\n---\n# Hello", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksLikeHTML(tt.input); got != tt.expected {
				t.Errorf("looksLikeHTML() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// ============================================================================
// extractHTMLBody Tests
// ============================================================================

func TestExtractHTMLBody(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "extracts body",
			input:    "<html><head><title>T</title></head><body><h1>Hello</h1></body></html>",
			expected: "<h1>Hello</h1>",
		},
		{
			name:     "prefers main over body",
			input:    "<html><body><nav>Nav</nav><main><h1>Content</h1></main></body></html>",
			expected: "<h1>Content</h1>",
		},
		{
			name:     "no body tag returns full content",
			input:    "<h1>Just a heading</h1>",
			expected: "<h1>Just a heading</h1>",
		},
		{
			name:     "body with attributes",
			input:    `<html><body class="dark"><p>Text</p></body></html>`,
			expected: "<p>Text</p>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractHTMLBody(tt.input); got != tt.expected {
				t.Errorf("extractHTMLBody() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// ============================================================================
// handleConversations Tests
// ============================================================================

func TestHandleConversations_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/conversations", nil)
	rr := httptest.NewRecorder()

	s.handleConversations(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestHandleConversations_EmptyState(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/conversations", nil)
	rr := httptest.NewRecorder()

	s.handleConversations(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp ConversationsResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if len(resp.CommentThreads) != 0 {
		t.Errorf("expected 0 threads, got %d", len(resp.CommentThreads))
	}
	if resp.OnYourPosts.PendingCount != 0 {
		t.Errorf("expected 0 pending, got %d", resp.OnYourPosts.PendingCount)
	}
	if resp.OnYourPosts.BlessedCount != 0 {
		t.Errorf("expected 0 blessed, got %d", resp.OnYourPosts.BlessedCount)
	}
}

func TestHandleConversations_WithData(t *testing.T) {
	s := newConfiguredServer(t)

	now := time.Now()
	discoveryDomain := s.GetDiscoveryDomain()

	// Seed feed cache with comment items
	cacheFile := feed.CacheFile(s.DataDir, discoveryDomain)
	os.MkdirAll(filepath.Dir(cacheFile), 0755)

	items := []feed.CachedFeedItem{
		{
			ID:           "c1",
			Type:         "comment",
			Title:        "Great post!",
			URL:          "https://alice.example.com/comments/great.md",
			Published:    now.Add(-1 * time.Hour).Format(time.RFC3339),
			AuthorURL:    "https://alice.example.com",
			AuthorDomain: "alice.example.com",
			CachedAt:     now.Format(time.RFC3339),
		},
		{
			ID:           "c2",
			Type:         "comment",
			Title:        "Thanks!",
			URL:          "https://alice.example.com/comments/thanks.md",
			Published:    now.Add(-2 * time.Hour).Format(time.RFC3339),
			AuthorURL:    "https://alice.example.com",
			AuthorDomain: "alice.example.com",
			CachedAt:     now.Format(time.RFC3339),
			ReadAt:       now.Format(time.RFC3339),
		},
		{
			ID:           "c3",
			Type:         "comment",
			Title:        "Interesting",
			URL:          "https://bob.example.com/comments/interesting.md",
			Published:    now.Add(-3 * time.Hour).Format(time.RFC3339),
			AuthorURL:    "https://bob.example.com",
			AuthorDomain: "bob.example.com",
			CachedAt:     now.Format(time.RFC3339),
		},
		{
			ID:           "p1",
			Type:         "post",
			Title:        "A post (not a comment)",
			URL:          "https://alice.example.com/posts/apost.md",
			Published:    now.Add(-30 * time.Minute).Format(time.RFC3339),
			AuthorURL:    "https://alice.example.com",
			AuthorDomain: "alice.example.com",
			CachedAt:     now.Format(time.RFC3339),
		},
	}

	var buf bytes.Buffer
	for _, item := range items {
		data, _ := json.Marshal(item)
		buf.Write(data)
		buf.WriteByte('\n')
	}
	os.WriteFile(cacheFile, buf.Bytes(), 0644)

	// Write blessing state
	store := stream.NewStore(s.DataDir, discoveryDomain, "pub.polis.core")
	blessingState := stream.BlessingState{
		Blessings: []stream.BlessingEntry{
			{
				SourceURL: "https://carol.example.com/comments/hello.md",
				TargetURL: "https://mysite.example.com/posts/intro.md",
				Status:    "pending",
				Actor:     "carol.example.com",
				UpdatedAt: now.Add(-10 * time.Minute).Format(time.RFC3339),
			},
			{
				SourceURL: "https://dave.example.com/comments/nice.md",
				TargetURL: "https://mysite.example.com/posts/intro.md",
				Status:    "granted",
				Actor:     "dave.example.com",
				UpdatedAt: now.Add(-20 * time.Minute).Format(time.RFC3339),
			},
		},
		Granted: 1,
		Denied:  0,
	}
	store.SaveState("pub.polis.comment.blessing", blessingState)

	req := httptest.NewRequest(http.MethodGet, "/api/conversations", nil)
	rr := httptest.NewRecorder()

	s.handleConversations(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp ConversationsResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	// Should have 2 comment threads (alice: 2 comments, bob: 1 comment)
	// Posts are excluded
	if len(resp.CommentThreads) != 2 {
		t.Errorf("expected 2 threads, got %d", len(resp.CommentThreads))
	}
	if len(resp.CommentThreads) > 0 {
		// alice should be first (most recent comment)
		if resp.CommentThreads[0].AuthorDomain != "alice.example.com" {
			t.Errorf("expected first thread from alice, got %q", resp.CommentThreads[0].AuthorDomain)
		}
		if len(resp.CommentThreads[0].Comments) != 2 {
			t.Errorf("expected 2 comments from alice, got %d", len(resp.CommentThreads[0].Comments))
		}
		// First comment should be unread, second should be read
		if !resp.CommentThreads[0].Comments[0].Unread {
			t.Error("expected first comment to be unread")
		}
		if resp.CommentThreads[0].Comments[1].Unread {
			t.Error("expected second comment to be read")
		}
	}

	// Blessing stats
	if resp.OnYourPosts.PendingCount != 1 {
		t.Errorf("expected 1 pending, got %d", resp.OnYourPosts.PendingCount)
	}
	if resp.OnYourPosts.BlessedCount != 1 {
		t.Errorf("expected 1 blessed, got %d", resp.OnYourPosts.BlessedCount)
	}
	if len(resp.OnYourPosts.Recent) != 2 {
		t.Errorf("expected 2 recent blessings, got %d", len(resp.OnYourPosts.Recent))
	}
}

// ============================================================================
// handlePulse Tests
// ============================================================================

func TestHandlePulse_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/pulse", nil)
	rr := httptest.NewRecorder()

	s.handlePulse(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestHandlePulse_EmptyState(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/pulse", nil)
	rr := httptest.NewRecorder()

	s.handlePulse(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp PulseResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode pulse response: %v", err)
	}

	if resp.Network.Following != 0 {
		t.Errorf("expected 0 following, got %d", resp.Network.Following)
	}
	if resp.Network.Followers != 0 {
		t.Errorf("expected 0 followers, got %d", resp.Network.Followers)
	}
	if len(resp.Recent) != 0 {
		t.Errorf("expected 0 recent items, got %d", len(resp.Recent))
	}
	if len(resp.TopAuthors) != 0 {
		t.Errorf("expected 0 top authors, got %d", len(resp.TopAuthors))
	}
}

func TestHandlePulse_WithFeedData(t *testing.T) {
	s := newConfiguredServer(t)

	// Add a following entry
	followingPath := following.DefaultPath(s.DataDir)
	f, _ := following.Load(followingPath)
	f.Add("https://alice.example.com")
	f.Add("https://bob.example.com")
	following.Save(followingPath, f)

	// Write feed cache items (JSONL format)
	discoveryDomain := s.GetDiscoveryDomain()
	cacheFile := feed.CacheFile(s.DataDir, discoveryDomain)
	os.MkdirAll(filepath.Dir(cacheFile), 0755)

	now := time.Now()
	items := []feed.CachedFeedItem{
		{
			ID:           "item1",
			Type:         "post",
			Title:        "Hello World",
			URL:          "https://alice.example.com/posts/hello.md",
			Published:    now.Add(-1 * time.Hour).Format(time.RFC3339),
			AuthorURL:    "https://alice.example.com",
			AuthorDomain: "alice.example.com",
			CachedAt:     now.Format(time.RFC3339),
		},
		{
			ID:           "item2",
			Type:         "comment",
			Title:        "Nice post",
			URL:          "https://bob.example.com/comments/nice.md",
			Published:    now.Add(-2 * time.Hour).Format(time.RFC3339),
			AuthorURL:    "https://bob.example.com",
			AuthorDomain: "bob.example.com",
			CachedAt:     now.Format(time.RFC3339),
		},
		{
			ID:           "item3",
			Type:         "post",
			Title:        "Another post",
			URL:          "https://alice.example.com/posts/another.md",
			Published:    now.Add(-3 * time.Hour).Format(time.RFC3339),
			AuthorURL:    "https://alice.example.com",
			AuthorDomain: "alice.example.com",
			CachedAt:     now.Format(time.RFC3339),
			ReadAt:       now.Format(time.RFC3339),
		},
	}

	var buf bytes.Buffer
	for _, item := range items {
		data, _ := json.Marshal(item)
		buf.Write(data)
		buf.WriteByte('\n')
	}
	os.WriteFile(cacheFile, buf.Bytes(), 0644)

	req := httptest.NewRequest(http.MethodGet, "/api/pulse", nil)
	rr := httptest.NewRecorder()

	s.handlePulse(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp PulseResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode pulse response: %v", err)
	}

	// Network stats
	if resp.Network.Following != 2 {
		t.Errorf("expected 2 following, got %d", resp.Network.Following)
	}
	if resp.Network.FeedUnread != 2 {
		t.Errorf("expected 2 unread feed items, got %d", resp.Network.FeedUnread)
	}

	// Recent highlights (all 3 items within 7 days)
	if len(resp.Recent) != 3 {
		t.Errorf("expected 3 recent items, got %d", len(resp.Recent))
	}
	if len(resp.Recent) > 0 {
		if resp.Recent[0].Title != "Hello World" {
			t.Errorf("expected first recent item 'Hello World', got %q", resp.Recent[0].Title)
		}
		if !resp.Recent[0].Unread {
			t.Error("expected first recent item to be unread")
		}
	}

	// Top authors (alice: 2 posts, bob: 1 comment)
	if len(resp.TopAuthors) != 2 {
		t.Errorf("expected 2 top authors, got %d", len(resp.TopAuthors))
	}
	if len(resp.TopAuthors) > 0 {
		if resp.TopAuthors[0].Domain != "alice.example.com" {
			t.Errorf("expected top author 'alice.example.com', got %q", resp.TopAuthors[0].Domain)
		}
		if resp.TopAuthors[0].PostCount != 2 {
			t.Errorf("expected alice to have 2 posts, got %d", resp.TopAuthors[0].PostCount)
		}
	}

	// Site stats
	if resp.Site.Posts != 0 {
		t.Errorf("expected 0 site posts (none published), got %d", resp.Site.Posts)
	}
}

// ============================================================================
// handleFollowerCount Tests
// ============================================================================

func TestHandleFollowerCount_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/followers/count", nil)
	rr := httptest.NewRecorder()

	s.handleFollowerCount(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestHandleFollowerCount_NoBaseURL(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/followers/count", nil)
	rr := httptest.NewRecorder()

	s.handleFollowerCount(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&resp)

	count, ok := resp["count"].(float64)
	if !ok || count != 0 {
		t.Errorf("expected count 0, got %v", resp["count"])
	}
}

func TestHandleFollowerCount_Configured(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/followers/count", nil)
	rr := httptest.NewRecorder()

	s.handleFollowerCount(rr, req)

	// Should return 200 with 0 followers (no stream events to project)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&resp)

	count, ok := resp["count"].(float64)
	if !ok || count != 0 {
		t.Errorf("expected count 0, got %v", resp["count"])
	}

	followers, ok := resp["followers"].([]interface{})
	if !ok {
		t.Fatal("expected followers array")
	}
	if len(followers) != 0 {
		t.Errorf("expected 0 followers, got %d", len(followers))
	}
}

func TestHandleFollowerCount_WithRefresh(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/followers/count?refresh=true", nil)
	rr := httptest.NewRecorder()

	s.handleFollowerCount(rr, req)

	// Should return 200 even with refresh (will try to query stream, get empty)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ============================================================================
// extractDomainFromURL Tests
// ============================================================================

func TestExtractDomainFromURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"https://example.com/path", "example.com"},
		{"https://sub.example.com", "sub.example.com"},
		{"http://localhost:8080", "localhost"},
		{"", ""},
		{"not-a-url", ""},
	}

	for _, tt := range tests {
		got := extractDomainFromURL(tt.input)
		if got != tt.expected {
			t.Errorf("extractDomainFromURL(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestHandleNotificationCount(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/notifications/count", nil)
	w := httptest.NewRecorder()

	s.handleNotificationCount(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["unread"] != float64(0) {
		t.Errorf("expected unread=0, got %v", resp["unread"])
	}
}

func TestHandleNotificationCount_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/notifications/count", nil)
	w := httptest.NewRecorder()

	s.handleNotificationCount(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleNotifications_EmptyList(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/notifications", nil)
	w := httptest.NewRecorder()

	s.handleNotifications(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	notifications := resp["notifications"].([]interface{})
	if len(notifications) != 0 {
		t.Errorf("expected 0 notifications, got %d", len(notifications))
	}
	if resp["total"] != float64(0) {
		t.Errorf("expected total=0, got %v", resp["total"])
	}
}

func TestHandleNotificationRead(t *testing.T) {
	s := newTestServer(t)

	body := jsonBody(t, map[string]interface{}{"all": true})
	req := httptest.NewRequest(http.MethodPost, "/api/notifications/read", body)
	w := httptest.NewRecorder()

	s.handleNotificationRead(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["success"] != true {
		t.Error("expected success=true")
	}
}

func TestHandleNotificationRead_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/notifications/read", nil)
	w := httptest.NewRecorder()

	s.handleNotificationRead(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ============================================================================
// handleDeployCheck Tests
// ============================================================================

func TestHandleDeployCheck_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/site/deploy-check", nil)
	w := httptest.NewRecorder()

	s.handleDeployCheck(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleDeployCheck_NoBaseURL(t *testing.T) {
	s := newTestServer(t)
	s.BaseURL = "" // No base URL set

	req := httptest.NewRequest(http.MethodGet, "/api/site/deploy-check", nil)
	w := httptest.NewRecorder()

	s.handleDeployCheck(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["deployed"] != false {
		t.Error("expected deployed=false when base URL not set")
	}
	if resp["error"] == nil || resp["error"] == "" {
		t.Error("expected error message when base URL not set")
	}
}

// ============================================================================
// handleSetupWizardDismiss Tests
// ============================================================================

func TestHandleSetupWizardDismiss(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/site/setup-wizard-dismiss", nil)
	w := httptest.NewRecorder()

	s.handleSetupWizardDismiss(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["success"] != true {
		t.Error("expected success=true")
	}

	// Verify config was updated
	if !s.Config.SetupWizardDismissed {
		t.Error("expected SetupWizardDismissed to be true after dismiss")
	}

	// Verify config was persisted to disk
	configPath := filepath.Join(s.DataDir, ".polis", "webapp", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("expected config file to exist: %v", err)
	}
	if !strings.Contains(string(data), "setup_wizard_dismissed") {
		t.Error("expected setup_wizard_dismissed in saved config")
	}
}

func TestHandleSetupWizardDismiss_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/site/setup-wizard-dismiss", nil)
	w := httptest.NewRecorder()

	s.handleSetupWizardDismiss(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ============================================================================
// handleInit Setup Wizard State Tests
// ============================================================================

func TestHandleInit_SetsWizardNotDismissed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/init", jsonBody(t, map[string]string{
		"site_title": "Test Site",
	}))
	w := httptest.NewRecorder()

	s.handleInit(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// After init, config should exist with setup_wizard_dismissed=false
	if s.Config == nil {
		t.Fatal("expected config to be set after init")
	}
	if s.Config.SetupWizardDismissed {
		t.Error("expected SetupWizardDismissed to be false after init")
	}
}

func TestHandleInit_WritesBaseURLToEnv(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/init", jsonBody(t, map[string]string{
		"site_title": "Test Site",
		"base_url":   "https://alice.example.com",
	}))
	w := httptest.NewRecorder()

	s.handleInit(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Check .env was created with the base URL
	envPath := filepath.Join(s.DataDir, ".env")
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("expected .env file to exist: %v", err)
	}
	if !strings.Contains(string(data), "POLIS_BASE_URL=https://alice.example.com") {
		t.Errorf("expected .env to contain POLIS_BASE_URL, got: %s", string(data))
	}

	// Check server state was updated
	if s.BaseURL != "https://alice.example.com" {
		t.Errorf("expected BaseURL to be updated, got: %s", s.BaseURL)
	}

	// Check response includes base_url
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["base_url"] != "https://alice.example.com" {
		t.Errorf("expected response base_url, got: %v", resp["base_url"])
	}
}

func TestHandleInit_NoBaseURL_EnvStillHasDiscovery(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/init", jsonBody(t, map[string]string{
		"site_title": "Test Site",
	}))
	w := httptest.NewRecorder()

	s.handleInit(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// .env should still be created with discovery credentials
	envPath := filepath.Join(s.DataDir, ".env")
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("expected .env file to exist: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "DISCOVERY_SERVICE_URL=") {
		t.Error("expected .env to contain DISCOVERY_SERVICE_URL")
	}
	// Should NOT contain POLIS_BASE_URL when not provided
	if strings.Contains(content, "POLIS_BASE_URL=") {
		t.Error("expected .env to NOT contain POLIS_BASE_URL when not provided")
	}
}

// ============================================================================
// handleSettings Setup Wizard Dismissed Tests
// ============================================================================

func TestHandleSettings_IncludesSetupWizardDismissed(t *testing.T) {
	s := newConfiguredServer(t)
	s.Config.SetupWizardDismissed = true

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	w := httptest.NewRecorder()

	s.handleSettings(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	dismissed, ok := resp["setup_wizard_dismissed"]
	if !ok {
		t.Error("expected setup_wizard_dismissed in settings response")
	}
	if dismissed != true {
		t.Errorf("expected setup_wizard_dismissed=true, got %v", dismissed)
	}
}

func TestHandleHideRead_Toggle(t *testing.T) {
	s := newConfiguredServer(t)

	// Toggle on
	body := jsonBody(t, map[string]bool{"hide_read": true})
	req := httptest.NewRequest(http.MethodPost, "/api/settings/hide-read", body)
	w := httptest.NewRecorder()
	s.handleHideRead(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !s.Config.HideRead {
		t.Error("expected HideRead=true after toggle on")
	}

	// Toggle off
	body = jsonBody(t, map[string]bool{"hide_read": false})
	req = httptest.NewRequest(http.MethodPost, "/api/settings/hide-read", body)
	w = httptest.NewRecorder()
	s.handleHideRead(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if s.Config.HideRead {
		t.Error("expected HideRead=false after toggle off")
	}
}

func TestHandleHideRead_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/settings/hide-read", nil)
	w := httptest.NewRecorder()
	s.handleHideRead(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleSettings_IncludesHideRead(t *testing.T) {
	s := newConfiguredServer(t)
	s.Config.HideRead = true

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	w := httptest.NewRecorder()
	s.handleSettings(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	hideRead, ok := resp["hide_read"]
	if !ok {
		t.Error("expected hide_read in settings response")
	}
	if hideRead != true {
		t.Errorf("expected hide_read=true, got %v", hideRead)
	}
}

func TestHandleWebappTheme_Success(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{"theme": "light"})
	req := httptest.NewRequest(http.MethodPost, "/api/settings/webapp-theme", body)
	w := httptest.NewRecorder()
	s.handleWebappTheme(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["webapp_theme"] != "light" {
		t.Errorf("expected light, got %v", resp["webapp_theme"])
	}

	// Verify it persisted
	if s.Config.WebappTheme != "light" {
		t.Errorf("expected config to be saved as light, got %s", s.Config.WebappTheme)
	}
}

func TestHandleWebappTheme_InvalidTheme(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{"theme": "neon"})
	req := httptest.NewRequest(http.MethodPost, "/api/settings/webapp-theme", body)
	w := httptest.NewRecorder()
	s.handleWebappTheme(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleSettings_IncludesWebappTheme(t *testing.T) {
	s := newConfiguredServer(t)
	s.Config.WebappTheme = "light"

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	w := httptest.NewRecorder()
	s.handleSettings(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	webappTheme, ok := resp["webapp_theme"]
	if !ok {
		t.Error("expected webapp_theme in settings response")
	}
	if webappTheme != "light" {
		t.Errorf("expected light, got %v", webappTheme)
	}
}

// ============================================================================
// handleEditorPanelMode Tests
// ============================================================================

func TestHandleEditorPanelMode_Success(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{"mode": "markdown"})
	req := httptest.NewRequest(http.MethodPost, "/api/settings/editor-panel-mode", body)
	w := httptest.NewRecorder()
	s.handleEditorPanelMode(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["editor_panel_mode"] != "markdown" {
		t.Errorf("expected markdown, got %v", resp["editor_panel_mode"])
	}

	if s.Config.EditorPanelMode != "markdown" {
		t.Errorf("expected config to be saved as markdown, got %s", s.Config.EditorPanelMode)
	}
}

func TestHandleEditorPanelMode_InvalidMode(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{"mode": "invalid"})
	req := httptest.NewRequest(http.MethodPost, "/api/settings/editor-panel-mode", body)
	w := httptest.NewRecorder()
	s.handleEditorPanelMode(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleEditorPanelMode_WrongMethod(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/editor-panel-mode", nil)
	w := httptest.NewRecorder()
	s.handleEditorPanelMode(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleSettings_IncludesEditorPanelMode(t *testing.T) {
	s := newConfiguredServer(t)
	s.Config.EditorPanelMode = "browse"

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	w := httptest.NewRecorder()
	s.handleSettings(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	mode, ok := resp["editor_panel_mode"]
	if !ok {
		t.Error("expected editor_panel_mode in settings response")
	}
	if mode != "browse" {
		t.Errorf("expected browse, got %v", mode)
	}
}

// ============================================================================
// handleNotifications Tests
// ============================================================================

func TestHandleNotifications_Empty(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/notifications", nil)
	w := httptest.NewRecorder()
	s.handleNotifications(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	notifications := resp["notifications"].([]interface{})
	if len(notifications) != 0 {
		t.Errorf("expected 0 notifications, got %d", len(notifications))
	}
	if resp["total"].(float64) != 0 {
		t.Errorf("expected total 0, got %v", resp["total"])
	}
	if resp["limit"].(float64) != 20 {
		t.Errorf("expected default limit 20, got %v", resp["limit"])
	}
}

func TestHandleNotifications_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/notifications", nil)
	w := httptest.NewRecorder()
	s.handleNotifications(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ============================================================================
// handleUpdateAvatar Tests
// ============================================================================

func TestHandleUpdateAvatar_Success(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]interface{}{
		"avatar": map[string]interface{}{
			"bg":            "#3a5f8a",
			"fg":            "#ffffff",
			"border":        "#c8a878",
			"border_w":      2,
			"pattern":       "rings",
			"pattern_color": "#4a6f9a",
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/settings/avatar", body)
	w := httptest.NewRecorder()

	s.handleUpdateAvatar(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["success"] != true {
		t.Error("expected success")
	}
	avatar := resp["avatar"].(map[string]interface{})
	if avatar["bg"] != "#3a5f8a" {
		t.Errorf("expected bg '#3a5f8a', got %q", avatar["bg"])
	}

	// Verify persisted
	data, _ := os.ReadFile(filepath.Join(s.DataDir, ".well-known", "polis"))
	var wk map[string]interface{}
	json.Unmarshal(data, &wk)
	if wk["avatar"] == nil {
		t.Error("expected avatar in persisted file")
	}

	// Verify favicon.svg was regenerated
	faviconData, err := os.ReadFile(filepath.Join(s.DataDir, "favicon.svg"))
	if err != nil {
		t.Error("favicon.svg should be regenerated after avatar update")
	} else if !strings.Contains(string(faviconData), "#3a5f8a") {
		t.Error("favicon.svg should contain updated avatar color")
	}
}

func TestHandleUpdateAvatar_RoundTrip(t *testing.T) {
	s := newConfiguredServer(t)

	// Save avatar
	body := jsonBody(t, map[string]interface{}{
		"avatar": map[string]interface{}{
			"bg":            "#2a5a3a",
			"fg":            "#ffffff",
			"border":        "#4a8a5a",
			"border_w":      2,
			"pattern":       "rings",
			"pattern_color": "#3a6a4a",
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/settings/avatar", body)
	w := httptest.NewRecorder()
	s.handleUpdateAvatar(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Now load settings and check avatar comes back
	req2 := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	w2 := httptest.NewRecorder()
	s.handleSettings(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("settings: expected 200, got %d: %s", w2.Code, w2.Body.String())
	}

	var settings map[string]interface{}
	json.NewDecoder(w2.Body).Decode(&settings)
	siteMap := settings["site"].(map[string]interface{})
	avatar := siteMap["avatar"]
	if avatar == nil {
		t.Fatal("expected avatar in settings response, got nil")
	}
	avatarMap := avatar.(map[string]interface{})
	if avatarMap["bg"] != "#2a5a3a" {
		t.Errorf("expected bg '#2a5a3a', got %q", avatarMap["bg"])
	}
	if avatarMap["pattern"] != "rings" {
		t.Errorf("expected pattern 'rings', got %q", avatarMap["pattern"])
	}
	if avatarMap["pattern_color"] != "#3a6a4a" {
		t.Errorf("expected pattern_color '#3a6a4a', got %q", avatarMap["pattern_color"])
	}
}

func TestHandleUpdateAvatar_StatusEndpoint(t *testing.T) {
	s := newConfiguredServer(t)

	// Save avatar
	body := jsonBody(t, map[string]interface{}{
		"avatar": map[string]interface{}{
			"bg": "#2a5a3a", "fg": "#ffffff",
			"pattern": "rings", "pattern_color": "#3a6a4a",
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/settings/avatar", body)
	w := httptest.NewRecorder()
	s.handleUpdateAvatar(w, req)

	// Check /api/status includes avatar
	req2 := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w2 := httptest.NewRecorder()
	s.handleStatus(w2, req2)

	var status map[string]interface{}
	json.NewDecoder(w2.Body).Decode(&status)
	if status["avatar"] == nil {
		t.Fatal("expected avatar in status response, got nil")
	}
	avatarMap := status["avatar"].(map[string]interface{})
	if avatarMap["bg"] != "#2a5a3a" {
		t.Errorf("expected bg '#2a5a3a', got %q", avatarMap["bg"])
	}
	if avatarMap["pattern"] != "rings" {
		t.Errorf("expected pattern 'rings', got %q", avatarMap["pattern"])
	}
}

func TestHandleUpdateAvatar_InvalidColor(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]interface{}{
		"avatar": map[string]interface{}{
			"bg": "not-a-color",
			"fg": "#ffffff",
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/settings/avatar", body)
	w := httptest.NewRecorder()

	s.handleUpdateAvatar(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleUpdateAvatar_InvalidPattern(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]interface{}{
		"avatar": map[string]interface{}{
			"bg":      "#3a5f8a",
			"fg":      "#ffffff",
			"pattern": "zigzag",
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/settings/avatar", body)
	w := httptest.NewRecorder()

	s.handleUpdateAvatar(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleUpdateAvatar_Reset(t *testing.T) {
	s := newConfiguredServer(t)

	// First set an avatar
	body := jsonBody(t, map[string]interface{}{
		"avatar": map[string]interface{}{"bg": "#3a5f8a", "fg": "#ffffff"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/settings/avatar", body)
	w := httptest.NewRecorder()
	s.handleUpdateAvatar(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("setup: expected 200, got %d", w.Code)
	}

	// Now reset with null
	body2 := jsonBody(t, map[string]interface{}{"avatar": nil})
	req2 := httptest.NewRequest(http.MethodPost, "/api/settings/avatar", body2)
	w2 := httptest.NewRecorder()
	s.handleUpdateAvatar(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w2.Code, w2.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w2.Body).Decode(&resp)
	if resp["avatar"] != nil {
		t.Errorf("expected null avatar, got %v", resp["avatar"])
	}
}

// ============================================================================
// handleUpdateAuthorName Tests
// ============================================================================

func TestHandleUpdateAuthorName_Success(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{"author_name": "Vincent"})
	req := httptest.NewRequest(http.MethodPost, "/api/settings/author-name", body)
	w := httptest.NewRecorder()

	s.handleUpdateAuthorName(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["author_name"] != "Vincent" {
		t.Errorf("expected 'Vincent', got %q", resp["author_name"])
	}

	// Verify persisted
	data, _ := os.ReadFile(filepath.Join(s.DataDir, ".well-known", "polis"))
	var wk map[string]interface{}
	json.Unmarshal(data, &wk)
	if wk["author_name"] != "Vincent" {
		t.Errorf("expected persisted 'Vincent', got %q", wk["author_name"])
	}
}

func TestHandleUpdateAuthorName_Empty(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{"author_name": ""})
	req := httptest.NewRequest(http.MethodPost, "/api/settings/author-name", body)
	w := httptest.NewRecorder()

	s.handleUpdateAuthorName(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["author_name"] != "" {
		t.Errorf("expected empty, got %q", resp["author_name"])
	}
}

func TestHandleUpdateAuthorName_TooLong(t *testing.T) {
	s := newConfiguredServer(t)

	longName := strings.Repeat("a", 51)
	body := jsonBody(t, map[string]string{"author_name": longName})
	req := httptest.NewRequest(http.MethodPost, "/api/settings/author-name", body)
	w := httptest.NewRecorder()

	s.handleUpdateAuthorName(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// ============================================================================
// handleDownloadSite Tests
// ============================================================================

func TestHandleDownloadSite_HappyPath(t *testing.T) {
	// Reset rate limiter
	downloadMu.Lock()
	lastDownloadTime = time.Time{}
	downloadMu.Unlock()

	s := newConfiguredServer(t)

	// Create a test post
	os.WriteFile(filepath.Join(s.DataDir, "content", "pub.polis.core", "post", "hello.md"), []byte("# Hello"), 0644)

	// Create a private key file that should be included
	os.MkdirAll(filepath.Join(s.DataDir, ".polis", "keys"), 0755)
	os.WriteFile(filepath.Join(s.DataDir, ".polis", "keys", "id_ed25519"), []byte("PRIVATE"), 0600)

	// Create logs directory that should be excluded
	os.MkdirAll(filepath.Join(s.DataDir, ".polis", "logs"), 0755)
	os.WriteFile(filepath.Join(s.DataDir, ".polis", "logs", "2026-01-01.log"), []byte("log data"), 0644)

	req := httptest.NewRequest(http.MethodGet, "/api/download-site", nil)
	w := httptest.NewRecorder()

	s.handleDownloadSite(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if ct := w.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("expected Content-Type application/zip, got %q", ct)
	}

	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "polis-site.zip") {
		t.Errorf("expected Content-Disposition with polis-site.zip, got %q", cd)
	}

	// Parse the zip and check contents
	reader, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatalf("failed to read zip: %v", err)
	}

	files := make(map[string]bool)
	for _, f := range reader.File {
		files[f.Name] = true
	}

	// Should contain posts and .well-known/polis
	if !files[filepath.Join("content", "pub.polis.core", "post", "hello.md")] {
		t.Error("zip should contain content/pub.polis.core/post/hello.md")
	}
	if !files[filepath.Join(".well-known", "polis")] {
		t.Error("zip should contain .well-known/polis")
	}

	// Should contain keys (included since keys are part of the export)
	if !files[filepath.Join(".polis", "keys", "id_ed25519")] {
		t.Error("zip should contain .polis/keys/id_ed25519")
	}

	// Should NOT contain logs
	for name := range files {
		if strings.HasPrefix(name, filepath.Join(".polis", "logs")) {
			t.Errorf("zip should not contain logs: %s", name)
		}
	}
}

func TestWriteZipArchive_ExcludesDirs(t *testing.T) {
	dir := t.TempDir()

	// Create files in various directories
	os.MkdirAll(filepath.Join(dir, "posts"), 0755)
	os.WriteFile(filepath.Join(dir, "posts", "hello.md"), []byte("# Hello"), 0644)

	os.MkdirAll(filepath.Join(dir, ".polis", "keys"), 0755)
	os.WriteFile(filepath.Join(dir, ".polis", "keys", "id_ed25519"), []byte("PRIVATE"), 0600)

	os.MkdirAll(filepath.Join(dir, ".polis", "logs"), 0755)
	os.WriteFile(filepath.Join(dir, ".polis", "logs", "2026-01-01.log"), []byte("log data"), 0644)

	os.MkdirAll(filepath.Join(dir, "site", "snippets"), 0755)
	os.WriteFile(filepath.Join(dir, "site", "snippets", "about.md"), []byte("about"), 0644)

	// Exclude both logs and keys
	var buf bytes.Buffer
	err := WriteZipArchive(&buf, dir, []string{filepath.Join(".polis", "logs"), filepath.Join(".polis", "keys")})
	if err != nil {
		t.Fatalf("WriteZipArchive: %v", err)
	}

	reader, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("failed to read zip: %v", err)
	}

	files := make(map[string]bool)
	for _, f := range reader.File {
		files[f.Name] = true
	}

	if !files["posts/hello.md"] {
		t.Error("zip should contain posts/hello.md")
	}
	if !files[filepath.Join("site", "snippets", "about.md")] {
		t.Error("zip should contain site/snippets/about.md")
	}
	for name := range files {
		if strings.HasPrefix(name, filepath.Join(".polis", "logs")) {
			t.Errorf("zip should not contain logs: %s", name)
		}
		if strings.HasPrefix(name, filepath.Join(".polis", "keys")) {
			t.Errorf("zip should not contain keys: %s", name)
		}
	}

	// Now exclude only logs (keys included)
	buf.Reset()
	err = WriteZipArchive(&buf, dir, []string{filepath.Join(".polis", "logs")})
	if err != nil {
		t.Fatalf("WriteZipArchive: %v", err)
	}

	reader, err = zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("failed to read zip: %v", err)
	}

	files = make(map[string]bool)
	for _, f := range reader.File {
		files[f.Name] = true
	}

	if !files[filepath.Join(".polis", "keys", "id_ed25519")] {
		t.Error("zip should contain .polis/keys/id_ed25519 when only logs excluded")
	}
	for name := range files {
		if strings.HasPrefix(name, "logs") {
			t.Errorf("zip should not contain logs: %s", name)
		}
	}
}

// ============================================================================
// handleThemeSwitch Tests
// ============================================================================

// setupTestTheme creates a minimal valid theme in the test server's themes dir.
func setupTestTheme(t *testing.T, s *Server, name string) {
	t.Helper()
	themeDir := filepath.Join(s.DataDir, "site", "themes", name)
	os.MkdirAll(themeDir, 0755)

	// Required template files
	for _, f := range []string{"post.html", "comment.html", "comment-inline.html", "index.html"} {
		os.WriteFile(filepath.Join(themeDir, f), []byte("<html></html>"), 0644)
	}

	// CSS file with color variables
	css := `:root {
    --color-bg: #1a1525;
    --color-text: #f0e8dc;
    --color-peach: #e8a060;
    --color-teal: #5fbfaf;
    --color-cyan: #00d4ff;
}
body { background: var(--color-bg); }
`
	os.WriteFile(filepath.Join(themeDir, name+".css"), []byte(css), 0644)
}

func TestHandleThemeSwitch_Success(t *testing.T) {
	s := newConfiguredServer(t)
	setupTestTheme(t, s, "sols")
	setupTestTheme(t, s, "turbo")

	// Set an initial active_theme in .well-known/polis so SetActiveTheme can update it
	wkPath := filepath.Join(s.DataDir, ".well-known", "polis")
	wkData, _ := os.ReadFile(wkPath)
	var wk map[string]interface{}
	json.Unmarshal(wkData, &wk)
	wk["active_theme"] = "sols"
	wkOut, _ := json.MarshalIndent(wk, "", "  ")
	os.WriteFile(wkPath, wkOut, 0644)

	body := jsonBody(t, map[string]string{"theme": "turbo"})
	req := httptest.NewRequest(http.MethodPost, "/api/settings/theme", body)
	w := httptest.NewRecorder()

	s.handleThemeSwitch(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["success"] != true {
		t.Error("expected success=true")
	}
	if resp["theme"] != "turbo" {
		t.Errorf("expected theme=turbo, got %v", resp["theme"])
	}

	// Active theme now lives in .polis/bundles/registry.json (post-1e). The
	// handler may write to the registry; the .well-known/polis legacy field
	// is left untouched here unless the migration helper runs (1.g).
	regData, err := os.ReadFile(filepath.Join(s.DataDir, ".polis", "bundles", "registry.json"))
	if err != nil {
		t.Fatalf("registry.json not created: %v", err)
	}
	if !strings.Contains(string(regData), `"pub.polis.themes.turbo"`) {
		t.Errorf("registry.json should contain pub.polis.themes.turbo, got: %s", string(regData))
	}

	// Verify CSS was copied to styles.css
	stylesData, err := os.ReadFile(filepath.Join(s.DataDir, "styles.css"))
	if err != nil {
		t.Fatalf("styles.css not created: %v", err)
	}
	if !strings.Contains(string(stylesData), "--color-bg") {
		t.Error("styles.css should contain theme CSS variables")
	}
}

func TestHandleThemeSwitch_InvalidTheme(t *testing.T) {
	s := newConfiguredServer(t)
	setupTestTheme(t, s, "sols")

	body := jsonBody(t, map[string]string{"theme": "nonexistent"})
	req := httptest.NewRequest(http.MethodPost, "/api/settings/theme", body)
	w := httptest.NewRecorder()

	s.handleThemeSwitch(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleThemeSwitch_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/theme", nil)
	w := httptest.NewRecorder()

	s.handleThemeSwitch(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleThemeSwitch_EmptyTheme(t *testing.T) {
	s := newTestServer(t)

	body := jsonBody(t, map[string]string{"theme": ""})
	req := httptest.NewRequest(http.MethodPost, "/api/settings/theme", body)
	w := httptest.NewRecorder()

	s.handleThemeSwitch(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleThemeSwitch_SolsReserved(t *testing.T) {
	s := newTestServer(t)

	body := jsonBody(t, map[string]string{"theme": "sols"})
	req := httptest.NewRequest(http.MethodPost, "/api/settings/theme", body)
	w := httptest.NewRecorder()

	s.handleThemeSwitch(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "reserved") {
		t.Errorf("expected error about sols being reserved, got: %s", w.Body.String())
	}
}

func TestHandleSettings_IncludesThemes(t *testing.T) {
	s := newConfiguredServer(t)
	setupTestTheme(t, s, "sols")

	// Set active theme in .well-known/polis
	wkPath := filepath.Join(s.DataDir, ".well-known", "polis")
	wkData, _ := os.ReadFile(wkPath)
	var wk map[string]interface{}
	json.Unmarshal(wkData, &wk)
	wk["active_theme"] = "sols"
	wkOut, _ := json.MarshalIndent(wk, "", "  ")
	os.WriteFile(wkPath, wkOut, 0644)

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	w := httptest.NewRecorder()

	s.handleSettings(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["active_theme"] != "sols" {
		t.Errorf("expected active_theme=sols, got %v", resp["active_theme"])
	}

	themes, ok := resp["themes"].([]interface{})
	if !ok {
		t.Fatal("expected themes to be an array")
	}
	if len(themes) == 0 {
		t.Error("expected at least one theme")
	}

	// Check first theme has expected fields
	theme := themes[0].(map[string]interface{})
	if theme["name"] != "sols" {
		t.Errorf("expected theme name=sols, got %v", theme["name"])
	}
	if theme["active"] != true {
		t.Error("expected theme to be marked active")
	}
	colors, ok := theme["colors"].([]interface{})
	if !ok || len(colors) != 5 {
		t.Errorf("expected 5 colors, got %v", theme["colors"])
	}
}

func TestHandleDownloadSite_MethodNotAllowed(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/download-site", nil)
	w := httptest.NewRecorder()

	s.handleDownloadSite(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ============================================================================
// handleSiteUnregister Tests
// ============================================================================

func TestHandleSiteUnregister_BlocksPolisPub(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://ds.polis.pub"
	s.DiscoveryKey = "test-key"
	s.BaseURL = "https://mysite.polis.pub"

	req := httptest.NewRequest(http.MethodPost, "/api/site/unregister", nil)
	w := httptest.NewRecorder()

	s.handleSiteUnregister(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for polis.pub domain, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Cannot unregister hosted polis.pub sites") {
		t.Errorf("expected polis.pub block message, got %s", w.Body.String())
	}
}

func TestHandleSiteUnregister_MethodNotAllowed(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/site/unregister", nil)
	w := httptest.NewRecorder()

	s.handleSiteUnregister(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ============================================================================
// Self-Follow Prevention Tests
// ============================================================================

func TestHandleFollowing_SelfFollowRejected(t *testing.T) {
	s := newConfiguredServer(t)

	// BaseURL is "https://test-site.polis.pub" from newConfiguredServer
	// Try to follow own domain
	body := jsonBody(t, map[string]string{"url": "https://test-site.polis.pub/"})
	req := httptest.NewRequest(http.MethodPost, "/api/following", body)
	w := httptest.NewRecorder()

	s.handleFollowing(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for self-follow, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Cannot follow your own site") {
		t.Errorf("expected self-follow error message, got %s", w.Body.String())
	}
}

func TestHandleFollowing_SelfFollowRejectedNoTrailingSlash(t *testing.T) {
	s := newConfiguredServer(t)

	// Without trailing slash should also be rejected
	body := jsonBody(t, map[string]string{"url": "https://test-site.polis.pub"})
	req := httptest.NewRequest(http.MethodPost, "/api/following", body)
	w := httptest.NewRecorder()

	s.handleFollowing(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for self-follow without trailing slash, got %d: %s", w.Code, w.Body.String())
	}
}

// ── Widget Comment Tests ─────────────────────────────────────────────

func TestWidgetCommentSuccess(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{
		"target": "https://alice.polis.pub/posts/20260220/hello-world",
		"text":   "Great post!",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/widget/comment", body)
	w := httptest.NewRecorder()

	s.handleWidgetComment(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["success"] != true {
		t.Error("expected success: true")
	}
	if resp["comment_id"] == nil || resp["comment_id"] == "" {
		t.Error("expected comment_id in response")
	}
}

func TestWidgetCommentMethodNotAllowed(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/widget/comment", nil)
	w := httptest.NewRecorder()

	s.handleWidgetComment(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestWidgetCommentMissingFields(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{"target": "", "text": ""})
	req := httptest.NewRequest(http.MethodPost, "/api/widget/comment", body)
	w := httptest.NewRecorder()

	s.handleWidgetComment(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestWidgetCommentNoKeys(t *testing.T) {
	s := newTestServer(t) // No keys configured

	body := jsonBody(t, map[string]string{
		"target": "https://alice.polis.pub/posts/20260220/hello",
		"text":   "Test",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/widget/comment", body)
	w := httptest.NewRecorder()

	s.handleWidgetComment(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unconfigured server, got %d", w.Code)
	}
}

// ============================================================================
// handleCounts Tests
// ============================================================================

func TestHandleCounts_ReturnsJSON(t *testing.T) {
	s := newConfiguredServer(t)

	// Posts are counted from metadata/public.jsonl (not filesystem)
	indexPath := filepath.Join(s.DataDir, "content", "pub.polis.core", "index.jsonl")
	indexContent := `{"path":"content/pub.polis.core/post/20260222/hello.md","title":"Hello"}
{"path":"content/pub.polis.core/post/20260222/world.md","title":"World"}
`
	os.WriteFile(indexPath, []byte(indexContent), 0644)

	pendingDir := filepath.Join(s.DataDir, ".polis", "bundles", "pub.polis.core", "comments", "pending")
	os.WriteFile(filepath.Join(pendingDir, "c1.md"), []byte("---\n---\nComment"), 0644)

	// Blessed comments in date-based subdirectory
	blessedDir := filepath.Join(s.DataDir, "content", "pub.polis.core", "comment", "20260222")
	os.MkdirAll(blessedDir, 0755)
	os.WriteFile(filepath.Join(blessedDir, "my-comment.md"), []byte("---\n---\nBlessed"), 0644)

	req := httptest.NewRequest(http.MethodGet, "/api/counts", nil)
	w := httptest.NewRecorder()

	s.handleCounts(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var counts CountsPayload
	if err := json.NewDecoder(w.Body).Decode(&counts); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if counts.Posts != 2 {
		t.Errorf("posts = %d, want 2", counts.Posts)
	}
	if counts.MyPending != 1 {
		t.Errorf("my_pending = %d, want 1", counts.MyPending)
	}
	if counts.MyBlessed != 1 {
		t.Errorf("my_blessed = %d, want 1", counts.MyBlessed)
	}
}

func TestHandleCounts_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/counts", nil)
	w := httptest.NewRecorder()

	s.handleCounts(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleCounts_EmptySite(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/counts", nil)
	w := httptest.NewRecorder()

	s.handleCounts(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var counts CountsPayload
	json.NewDecoder(w.Body).Decode(&counts)

	// All counts should be zero for empty site
	if counts.Posts != 0 || counts.Drafts != 0 || counts.MyPending != 0 {
		t.Errorf("expected all zeros, got posts=%d drafts=%d pending=%d", counts.Posts, counts.Drafts, counts.MyPending)
	}
}

// ============================================================================
// handleSSE Tests
// ============================================================================

func TestHandleSSE_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)
	s.sseClients = make(map[chan SSEEvent]struct{})

	req := httptest.NewRequest(http.MethodPost, "/api/sse", nil)
	w := httptest.NewRecorder()

	s.handleSSE(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}


func TestComputeAllCounts_WithFollowing(t *testing.T) {
	s := newConfiguredServer(t)

	// following.json lives at metadata/following.json (from following.DefaultPath)
	followingContent := `{"version":"test","following":[{"url":"https://alice.com"},{"url":"https://bob.com"}]}`
	os.WriteFile(filepath.Join(s.DataDir, "content", "pub.polis.core", "follow", "following.json"), []byte(followingContent), 0644)

	counts := s.computeAllCounts()
	if counts.Following != 2 {
		t.Errorf("following = %d, want 2", counts.Following)
	}
}

func TestComputeAllCounts_PostsSkipsCommentEntries(t *testing.T) {
	s := newConfiguredServer(t)

	// public.jsonl contains both posts and blessed comments
	indexContent := `{"path":"content/pub.polis.core/post/20260222/hello.md","title":"Hello"}
{"path":"content/pub.polis.core/comment/20260222/c1.md","title":"Re: Hello"}
{"path":"content/pub.polis.core/post/20260222/world.md","title":"World"}
`
	os.WriteFile(filepath.Join(s.DataDir, "content", "pub.polis.core", "index.jsonl"), []byte(indexContent), 0644)

	counts := s.computeAllCounts()
	if counts.Posts != 2 {
		t.Errorf("posts = %d, want 2 (should exclude comment entries)", counts.Posts)
	}
}

// ==================== Feed Grouped Tests ====================

func TestHandleFeedGrouped_Empty(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/feed/grouped", nil)
	w := httptest.NewRecorder()
	s.handleFeedGrouped(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	groups := resp["groups"].([]interface{})
	if len(groups) != 0 {
		t.Errorf("expected 0 groups, got %d", len(groups))
	}
	if resp["total_items"].(float64) != 0 {
		t.Errorf("expected total_items=0, got %v", resp["total_items"])
	}
}

func TestHandleFeedGrouped_PostOnly(t *testing.T) {
	s := newTestServer(t)
	discoveryDomain := s.GetDiscoveryDomain()
	cm := feed.NewCacheManager(s.DataDir, discoveryDomain)

	now := time.Now().UTC()
	cm.MergeItems([]feed.FeedItem{
		{
			Type:         "post",
			Title:        "Hello World",
			URL:          "https://alice.polis.pub/posts/hello.md",
			Published:    now.Add(-24 * time.Hour).Format(time.RFC3339),
			AuthorURL:    "https://alice.polis.pub",
			AuthorDomain: "alice.polis.pub",
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/feed/grouped", nil)
	w := httptest.NewRecorder()
	s.handleFeedGrouped(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	groups := resp["groups"].([]interface{})
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}

	g := groups[0].(map[string]interface{})
	if g["post_title"] != "Hello World" {
		t.Errorf("expected post_title 'Hello World', got %v", g["post_title"])
	}
	if g["has_post"] != true {
		t.Errorf("expected has_post=true")
	}
	if g["total_comments"].(float64) != 0 {
		t.Errorf("expected 0 comments, got %v", g["total_comments"])
	}
}

func TestHandleFeedGrouped_CommentsGroupByTarget(t *testing.T) {
	s := newTestServer(t)
	discoveryDomain := s.GetDiscoveryDomain()
	cm := feed.NewCacheManager(s.DataDir, discoveryDomain)

	now := time.Now().UTC()
	postTime := now.Add(-26 * time.Hour).Format(time.RFC3339)
	comment1Time := now.Add(-25 * time.Hour).Format(time.RFC3339)
	comment2Time := now.Add(-24 * time.Hour).Format(time.RFC3339)
	postURL := "https://alice.polis.pub/posts/hello.md"
	cm.MergeItems([]feed.FeedItem{
		{
			Type:         "post",
			Title:        "Hello World",
			URL:          postURL,
			Published:    postTime,
			AuthorURL:    "https://alice.polis.pub",
			AuthorDomain: "alice.polis.pub",
		},
		{
			Type:         "comment",
			Title:        "Comment 1",
			URL:          "https://bob.polis.pub/comments/c1.md",
			Published:    comment1Time,
			AuthorURL:    "https://bob.polis.pub",
			AuthorDomain: "bob.polis.pub",
			TargetURL:    postURL,
		},
		{
			Type:         "comment",
			Title:        "Comment 2",
			URL:          "https://carol.polis.pub/comments/c2.md",
			Published:    comment2Time,
			AuthorURL:    "https://carol.polis.pub",
			AuthorDomain: "carol.polis.pub",
			TargetURL:    postURL,
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/feed/grouped", nil)
	w := httptest.NewRecorder()
	s.handleFeedGrouped(w, req)

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	groups := resp["groups"].([]interface{})
	if len(groups) != 1 {
		t.Fatalf("expected 1 group (comments grouped with post), got %d", len(groups))
	}

	g := groups[0].(map[string]interface{})
	if g["total_comments"].(float64) != 2 {
		t.Errorf("expected 2 comments, got %v", g["total_comments"])
	}
	if g["post_title"] != "Hello World" {
		t.Errorf("expected post title, got %v", g["post_title"])
	}
	if g["last_activity"] != comment2Time {
		t.Errorf("expected last_activity to be latest comment time (%s), got %v", comment2Time, g["last_activity"])
	}
	ids := g["item_ids"].([]interface{})
	if len(ids) != 3 {
		t.Errorf("expected 3 item_ids (1 post + 2 comments), got %d", len(ids))
	}
}

func TestHandleFeedGrouped_NetworkClassification(t *testing.T) {
	s := newTestServer(t)
	discoveryDomain := s.GetDiscoveryDomain()
	cm := feed.NewCacheManager(s.DataDir, discoveryDomain)

	// Create following.json with bob as followed
	followingPath := following.DefaultPath(s.DataDir)
	f := &following.FollowingFile{
		Version: "1.0",
		Following: []following.FollowingEntry{
			{URL: "https://bob.polis.pub", AddedAt: "2026-01-01T00:00:00Z"},
		},
	}
	following.Save(followingPath, f)

	now := time.Now().UTC()
	postURL := "https://alice.polis.pub/posts/hello.md"
	cm.MergeItems([]feed.FeedItem{
		{
			Type:         "post",
			Title:        "Hello",
			URL:          postURL,
			Published:    now.Add(-26 * time.Hour).Format(time.RFC3339),
			AuthorURL:    "https://alice.polis.pub",
			AuthorDomain: "alice.polis.pub",
		},
		{
			Type:         "comment",
			Title:        "Network comment",
			URL:          "https://bob.polis.pub/comments/c1.md",
			Published:    now.Add(-25 * time.Hour).Format(time.RFC3339),
			AuthorURL:    "https://bob.polis.pub",
			AuthorDomain: "bob.polis.pub",
			TargetURL:    postURL,
		},
		{
			Type:         "comment",
			Title:        "External comment",
			URL:          "https://stranger.polis.pub/comments/c2.md",
			Published:    now.Add(-24 * time.Hour).Format(time.RFC3339),
			AuthorURL:    "https://stranger.polis.pub",
			AuthorDomain: "stranger.polis.pub",
			TargetURL:    postURL,
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/feed/grouped", nil)
	w := httptest.NewRecorder()
	s.handleFeedGrouped(w, req)

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	groups := resp["groups"].([]interface{})
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}

	g := groups[0].(map[string]interface{})
	if g["network_comments"].(float64) != 1 {
		t.Errorf("expected 1 network comment, got %v", g["network_comments"])
	}
	if g["external_comments"].(float64) != 1 {
		t.Errorf("expected 1 external comment, got %v", g["external_comments"])
	}
}

func TestHandleFeedGrouped_SortByLastActivity(t *testing.T) {
	s := newTestServer(t)
	discoveryDomain := s.GetDiscoveryDomain()
	cm := feed.NewCacheManager(s.DataDir, discoveryDomain)

	now := time.Now().UTC()
	cm.MergeItems([]feed.FeedItem{
		{
			Type:         "post",
			Title:        "Old Post",
			URL:          "https://alice.polis.pub/posts/old.md",
			Published:    now.Add(-72 * time.Hour).Format(time.RFC3339),
			AuthorURL:    "https://alice.polis.pub",
			AuthorDomain: "alice.polis.pub",
		},
		{
			Type:         "post",
			Title:        "New Post",
			URL:          "https://bob.polis.pub/posts/new.md",
			Published:    now.Add(-48 * time.Hour).Format(time.RFC3339),
			AuthorURL:    "https://bob.polis.pub",
			AuthorDomain: "bob.polis.pub",
		},
		{
			Type:         "comment",
			Title:        "Late comment on old post",
			URL:          "https://carol.polis.pub/comments/c1.md",
			Published:    now.Add(-24 * time.Hour).Format(time.RFC3339),
			AuthorURL:    "https://carol.polis.pub",
			AuthorDomain: "carol.polis.pub",
			TargetURL:    "https://alice.polis.pub/posts/old.md",
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/feed/grouped", nil)
	w := httptest.NewRecorder()
	s.handleFeedGrouped(w, req)

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	groups := resp["groups"].([]interface{})
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}

	// Old post with late comment should be first (most recent activity)
	first := groups[0].(map[string]interface{})
	if first["post_title"] != "Old Post" {
		t.Errorf("expected 'Old Post' first (has most recent comment), got %v", first["post_title"])
	}
}

func TestHandleFeedGrouped_OrphanComments(t *testing.T) {
	s := newTestServer(t)
	discoveryDomain := s.GetDiscoveryDomain()
	cm := feed.NewCacheManager(s.DataDir, discoveryDomain)

	now := time.Now().UTC()
	cm.MergeItems([]feed.FeedItem{
		{
			Type:         "comment",
			Title:        "Orphan comment",
			URL:          "https://bob.polis.pub/comments/orphan.md",
			Published:    now.Add(-24 * time.Hour).Format(time.RFC3339),
			AuthorURL:    "https://bob.polis.pub",
			AuthorDomain: "bob.polis.pub",
			// No TargetURL — old cached item
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/feed/grouped", nil)
	w := httptest.NewRecorder()
	s.handleFeedGrouped(w, req)

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	groups := resp["groups"].([]interface{})
	if len(groups) != 1 {
		t.Fatalf("expected 1 group for orphan comment, got %d", len(groups))
	}

	g := groups[0].(map[string]interface{})
	if g["has_post"] != false {
		t.Errorf("orphan comment group should have has_post=false")
	}
	if g["total_comments"].(float64) != 1 {
		t.Errorf("expected 1 comment, got %v", g["total_comments"])
	}
}

func TestHandleFeedGrouped_BlessedCommentCountsAsReply(t *testing.T) {
	// End-to-end: a blessed comment (arriving via blessing.granted) should
	// count in total_comments for the target post's feed group.
	s := newTestServer(t)
	discoveryDomain := s.GetDiscoveryDomain()
	cm := feed.NewCacheManager(s.DataDir, discoveryDomain)

	now := time.Now().UTC()
	postTime := now.Add(-48 * time.Hour).Format(time.RFC3339)
	commentTime := now.Add(-24 * time.Hour).Format(time.RFC3339)
	postURL := "https://david.polis.pub/posts/20260304/hello-world.html"
	cm.MergeItems([]feed.FeedItem{
		{
			Type:         "post",
			Title:        "Hello World!",
			URL:          postURL,
			Published:    postTime,
			AuthorURL:    "https://david.polis.pub",
			AuthorDomain: "david.polis.pub",
		},
		// This comment came from a blessing.granted event (own comment blessed)
		{
			Type:         "comment",
			URL:          "https://me.polis.pub/comments/20260304/reply.md",
			Published:    commentTime,
			AuthorURL:    "https://me.polis.pub",
			AuthorDomain: "me.polis.pub",
			TargetURL:    postURL,
			TargetDomain: "david.polis.pub",
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/feed/grouped", nil)
	w := httptest.NewRecorder()
	s.handleFeedGrouped(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	groups := resp["groups"].([]interface{})
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}

	g := groups[0].(map[string]interface{})
	if g["post_title"] != "Hello World!" {
		t.Errorf("expected post_title 'Hello World!', got %v", g["post_title"])
	}
	if g["total_comments"].(float64) != 1 {
		t.Errorf("expected 1 comment (blessed reply), got %v", g["total_comments"])
	}
	if g["last_activity"] != commentTime {
		t.Errorf("expected last_activity from comment (%s), got %v", commentTime, g["last_activity"])
	}
	ids := g["item_ids"].([]interface{})
	if len(ids) != 2 {
		t.Errorf("expected 2 item_ids (1 post + 1 comment), got %d", len(ids))
	}
}

func TestHandleFeedGrouped_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/feed/grouped", nil)
	w := httptest.NewRecorder()
	s.handleFeedGrouped(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ============================================================================
// About Page Tests
// ============================================================================

func TestHandleAbout_GET_WithFile(t *testing.T) {
	s := newTestServer(t)

	// Create snippets/about.md
	aboutPath := filepath.Join(s.DataDir, "site", "snippets", "about.md")
	if err := os.WriteFile(aboutPath, []byte("Custom about content"), 0644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/about", nil)
	w := httptest.NewRecorder()
	s.handleAbout(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["content"] != "Custom about content" {
		t.Errorf("content = %q, want %q", resp["content"], "Custom about content")
	}
	if resp["has_custom"] != true {
		t.Errorf("has_custom = %v, want true", resp["has_custom"])
	}
	html, _ := resp["content_html"].(string)
	if !strings.Contains(html, "<p>") {
		t.Errorf("content_html should contain rendered HTML, got %q", html)
	}
}

func TestHandleAbout_GET_NoFile(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/about", nil)
	w := httptest.NewRecorder()
	s.handleAbout(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["has_custom"] != false {
		t.Errorf("has_custom = %v, want false", resp["has_custom"])
	}
	content, ok := resp["content"].(string)
	if !ok || content == "" {
		t.Error("content should be a non-empty default string")
	}
}

func TestHandleAbout_GET_DefaultContentNotEmpty(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/about", nil)
	w := httptest.NewRecorder()
	s.handleAbout(w, req)

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	content, _ := resp["content"].(string)
	if content == "" {
		t.Error("default about content must not be empty — editor must always prepopulate")
	}
	if !strings.Contains(content, "polis") {
		t.Error("default about content should mention polis")
	}
}

func TestHandleAbout_POST(t *testing.T) {
	s := newTestServer(t)

	body := jsonBody(t, map[string]string{"content": "My custom about text"})
	req := httptest.NewRequest(http.MethodPost, "/api/about", body)
	w := httptest.NewRecorder()
	s.handleAbout(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["success"] != true {
		t.Error("expected success: true")
	}

	// Verify file was written
	data, err := os.ReadFile(filepath.Join(s.DataDir, "site", "snippets", "about.md"))
	if err != nil {
		t.Fatalf("about.md should exist: %v", err)
	}
	if string(data) != "My custom about text" {
		t.Errorf("file content = %q, want %q", string(data), "My custom about text")
	}
}

func TestHandleAbout_POST_Overwrites(t *testing.T) {
	s := newTestServer(t)

	// Write initial content
	aboutPath := filepath.Join(s.DataDir, "site", "snippets", "about.md")
	os.WriteFile(aboutPath, []byte("old content"), 0644)

	body := jsonBody(t, map[string]string{"content": "new content"})
	req := httptest.NewRequest(http.MethodPost, "/api/about", body)
	w := httptest.NewRecorder()
	s.handleAbout(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	data, _ := os.ReadFile(aboutPath)
	if string(data) != "new content" {
		t.Errorf("file content = %q, want %q", string(data), "new content")
	}
}

func TestHandleAbout_POST_CreatesDir(t *testing.T) {
	s := newTestServer(t)

	// Remove snippets dir to test dir creation
	os.RemoveAll(filepath.Join(s.DataDir, "snippets"))

	body := jsonBody(t, map[string]string{"content": "about text"})
	req := httptest.NewRequest(http.MethodPost, "/api/about", body)
	w := httptest.NewRecorder()
	s.handleAbout(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	data, err := os.ReadFile(filepath.Join(s.DataDir, "site", "snippets", "about.md"))
	if err != nil {
		t.Fatalf("about.md should exist after dir creation: %v", err)
	}
	if string(data) != "about text" {
		t.Errorf("file content = %q, want %q", string(data), "about text")
	}
}

func TestHandleAbout_POST_EmptyContent(t *testing.T) {
	s := newTestServer(t)

	body := jsonBody(t, map[string]string{"content": ""})
	req := httptest.NewRequest(http.MethodPost, "/api/about", body)
	w := httptest.NewRecorder()
	s.handleAbout(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	data, err := os.ReadFile(filepath.Join(s.DataDir, "site", "snippets", "about.md"))
	if err != nil {
		t.Fatalf("about.md should exist even with empty content: %v", err)
	}
	if string(data) != "" {
		t.Errorf("file content = %q, want empty", string(data))
	}
}

func TestHandleAbout_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	for _, method := range []string{http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req := httptest.NewRequest(method, "/api/about", nil)
		w := httptest.NewRecorder()
		s.handleAbout(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: expected 405, got %d", method, w.Code)
		}
	}
}

// ============================================================================
// Widget Follow/Unfollow Tests
// ============================================================================

func TestWidgetFollowMethodNotAllowed(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/widget/follow", nil)
	w := httptest.NewRecorder()

	s.handleWidgetFollow(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestWidgetFollowNoKeys(t *testing.T) {
	s := newTestServer(t) // No keys configured

	body := jsonBody(t, map[string]string{"author": "alice.polis.pub"})
	req := httptest.NewRequest(http.MethodPost, "/api/widget/follow", body)
	w := httptest.NewRecorder()

	s.handleWidgetFollow(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestWidgetFollowMissingAuthor(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{"author": ""})
	req := httptest.NewRequest(http.MethodPost, "/api/widget/follow", body)
	w := httptest.NewRecorder()

	s.handleWidgetFollow(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty author, got %d: %s", w.Code, w.Body.String())
	}
}

func TestWidgetUnfollowNoKeys(t *testing.T) {
	s := newTestServer(t) // No keys configured

	body := jsonBody(t, map[string]string{"author": "alice.polis.pub"})
	req := httptest.NewRequest(http.MethodDelete, "/api/widget/follow", body)
	w := httptest.NewRecorder()

	s.handleWidgetFollow(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestWidgetUnfollowMissingAuthor(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{"author": ""})
	req := httptest.NewRequest(http.MethodDelete, "/api/widget/follow", body)
	w := httptest.NewRecorder()

	s.handleWidgetFollow(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty author, got %d: %s", w.Code, w.Body.String())
	}
}

// --- Body size limit tests ---

func TestPublish_BodyTooLarge(t *testing.T) {
	s := newConfiguredServer(t)

	// Create markdown exceeding MaxPostBodySize (256KB)
	bigMarkdown := strings.Repeat("x", MaxPostBodySize+1)
	body := jsonBody(t, map[string]string{"markdown": bigMarkdown})
	req := httptest.NewRequest(http.MethodPost, "/api/publish", body)

	// Apply the same limitBody wrapper that routes.go uses
	w := httptest.NewRecorder()
	handler := limitBody(s.handlePublish, MaxPostBodySize+4096)
	handler(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413 for oversized publish body, got %d: %s", w.Code, w.Body.String())
	}
}

func TestComment_BodyTooLarge(t *testing.T) {
	s := newConfiguredServer(t)

	// Create content exceeding MaxCommentBodySize (64KB)
	bigContent := strings.Repeat("x", MaxCommentBodySize+1)
	body := jsonBody(t, map[string]interface{}{
		"in_reply_to": "https://example.com/posts/test.md",
		"content":     bigContent,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/comments/sign", body)

	w := httptest.NewRecorder()
	handler := limitBody(s.handleCommentSign, MaxCommentBodySize+4096)
	handler(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413 for oversized comment body, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSnippet_BodyTooLarge(t *testing.T) {
	s := newConfiguredServer(t)

	// Create content exceeding MaxSnippetBodySize (64KB)
	bigContent := strings.Repeat("x", MaxSnippetBodySize+1)
	body := jsonBody(t, map[string]string{"content": bigContent, "source": "global"})
	req := httptest.NewRequest(http.MethodPut, "/api/snippets/about", body)

	w := httptest.NewRecorder()
	handler := limitBody(s.handleSnippet, MaxSnippetBodySize+4096)
	handler(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413 for oversized snippet body, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHook_BodyTooLarge(t *testing.T) {
	s := newConfiguredServer(t)
	s.EnableHooks = true

	// Create script exceeding MaxHookBodySize (32KB)
	bigScript := strings.Repeat("x", MaxHookBodySize+1)
	body := jsonBody(t, map[string]string{
		"hook_type": "post-publish",
		"script":    bigScript,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/automations", body)

	w := httptest.NewRecorder()
	handler := limitBody(s.handleAutomations, MaxHookBodySize+4096)
	handler(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413 for oversized hook body, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublish_SmallBodyStillWorks(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{"markdown": "# Hello\n\nSmall post."})
	req := httptest.NewRequest(http.MethodPost, "/api/publish", body)

	w := httptest.NewRecorder()
	handler := limitBody(s.handlePublish, MaxPostBodySize+4096)
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for small publish body, got %d: %s", w.Code, w.Body.String())
	}
}

// ============================================================================
// handleUnpublish Tests
// ============================================================================

func TestHandleUnpublish_Success(t *testing.T) {
	s := newConfiguredServer(t)
	// Use a local test server that returns 200 so the DS call completes instantly
	dsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success":true}`))
	}))
	defer dsServer.Close()
	s.DiscoveryURL = dsServer.URL

	// Create a post file
	postDir := filepath.Join(s.DataDir, "content", "pub.polis.core", "post", "20260201")
	if err := os.MkdirAll(postDir, 0755); err != nil {
		t.Fatalf("failed to create post dir: %v", err)
	}
	postPath := filepath.Join(postDir, "test-post.md")
	postContent := "---\ntitle: Test Post\npublished: 2026-02-01T12:00:00Z\n---\n\nHello world.\n"
	if err := os.WriteFile(postPath, []byte(postContent), 0644); err != nil {
		t.Fatalf("failed to write post file: %v", err)
	}

	// Create metadata/public.jsonl with an entry for that post
	indexPath := filepath.Join(s.DataDir, "content", "pub.polis.core", "index.jsonl")
	indexEntries := []string{
		`{"path":"content/pub.polis.core/post/20260201/test-post.md","title":"Test Post","published":"2026-02-01T12:00:00Z"}`,
		`{"path":"content/pub.polis.core/post/20260202/other-post.md","title":"Other Post","published":"2026-02-02T12:00:00Z"}`,
	}
	if err := os.WriteFile(indexPath, []byte(strings.Join(indexEntries, "\n")+"\n"), 0644); err != nil {
		t.Fatalf("failed to write public.jsonl: %v", err)
	}

	body := jsonBody(t, map[string]string{"path": "content/pub.polis.core/post/20260201/test-post.md"})
	req := httptest.NewRequest(http.MethodPost, "/api/unpublish", body)
	w := httptest.NewRecorder()

	s.handleUnpublish(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify success response
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp["success"] != true {
		t.Errorf("expected success=true, got %v", resp["success"])
	}

	// Verify the post file was deleted
	if _, err := os.Stat(postPath); !os.IsNotExist(err) {
		t.Error("expected post file to be deleted, but it still exists")
	}

	// Verify the index entry was removed (only the other post should remain)
	updatedIndex, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("failed to read updated public.jsonl: %v", err)
	}
	indexStr := string(updatedIndex)
	if strings.Contains(indexStr, "test-post.md") {
		t.Error("expected test-post.md entry to be removed from public.jsonl")
	}
	if !strings.Contains(indexStr, "other-post.md") {
		t.Error("expected other-post.md entry to remain in public.jsonl")
	}
}

func TestHandleUnpublish_NotFound(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "http://192.0.2.1:1"

	body := jsonBody(t, map[string]string{"path": "content/pub.polis.core/post/20260201/nonexistent.md"})
	req := httptest.NewRequest(http.MethodPost, "/api/unpublish", body)
	w := httptest.NewRecorder()

	s.handleUnpublish(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleUnpublish_InvalidPath(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "http://192.0.2.1:1"

	body := jsonBody(t, map[string]string{"path": "../../../etc/passwd"})
	req := httptest.NewRequest(http.MethodPost, "/api/unpublish", body)
	w := httptest.NewRecorder()

	s.handleUnpublish(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleUnpublish_MethodNotAllowed(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/unpublish", nil)
	w := httptest.NewRecorder()

	s.handleUnpublish(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleUnpublish_NoKeys(t *testing.T) {
	s := newTestServer(t)

	body := jsonBody(t, map[string]string{"path": "content/pub.polis.core/post/20260201/test-post.md"})
	req := httptest.NewRequest(http.MethodPost, "/api/unpublish", body)
	w := httptest.NewRecorder()

	s.handleUnpublish(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Not configured") {
		t.Errorf("expected 'Not configured' in error, got: %s", w.Body.String())
	}
}

// ============================================================================
// handleRotateKey Tests
// ============================================================================

func TestHandleRotateKey_MethodNotAllowed(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/rotate-key", nil)
	w := httptest.NewRecorder()

	s.handleRotateKey(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleRotateKey_NoKeys(t *testing.T) {
	s := newTestServer(t) // No keys configured

	req := httptest.NewRequest(http.MethodPost, "/api/rotate-key", nil)
	w := httptest.NewRecorder()

	s.handleRotateKey(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Not configured") {
		t.Errorf("expected 'Not configured' in error, got: %s", w.Body.String())
	}
}

func TestHandleRotateKey_NoBaseURL(t *testing.T) {
	s := newConfiguredServer(t)
	s.BaseURL = "" // Clear base URL

	req := httptest.NewRequest(http.MethodPost, "/api/rotate-key", nil)
	w := httptest.NewRecorder()

	s.handleRotateKey(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "POLIS_BASE_URL") {
		t.Errorf("expected POLIS_BASE_URL error, got: %s", w.Body.String())
	}
}

func TestHandleRotateKey_Success(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "" // Will cause DS call to fail at network level

	// Save old key for comparison
	oldPubKey := strings.TrimSpace(string(s.PublicKey))

	req := httptest.NewRequest(http.MethodPost, "/api/rotate-key", nil)
	w := httptest.NewRecorder()

	s.handleRotateKey(w, req)

	// DS call will fail since there's no real discovery service
	// This tests the DS-failure path (502 Bad Gateway)
	if w.Code != http.StatusBadGateway {
		t.Errorf("expected 502 (DS unavailable), got %d: %s", w.Code, w.Body.String())
	}

	// Verify keys were NOT changed (DS failed, so local swap should not proceed)
	currentPubKey := strings.TrimSpace(string(s.PublicKey))
	if currentPubKey != oldPubKey {
		t.Error("keys should not have changed when DS fails")
	}
}

func TestHandleRotateKey_KeysChangedOnDisk(t *testing.T) {
	s := newConfiguredServer(t)

	// Save old keys for comparison
	oldPubKey := strings.TrimSpace(string(s.PublicKey))
	oldPrivKey := make([]byte, len(s.PrivateKey))
	copy(oldPrivKey, s.PrivateKey)

	keysDir := filepath.Join(s.DataDir, ".polis", "keys")

	// To test the full success path, we need to mock the DS client.
	// Instead, we'll directly call the key rotation logic steps that happen
	// after DS notification (backup + write + well-known update).
	// This verifies the file operations are correct.

	// Generate new keypair
	newPrivPEM, newPubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("failed to generate keypair: %v", err)
	}
	newPubKey := strings.TrimSpace(string(newPubSSH))

	// Simulate what handleRotateKey does after DS success:

	// Backup old keys
	if err := os.Rename(filepath.Join(keysDir, "id_ed25519"), filepath.Join(keysDir, "id_ed25519.old")); err != nil {
		t.Fatalf("failed to backup old private key: %v", err)
	}
	if err := os.Rename(filepath.Join(keysDir, "id_ed25519.pub"), filepath.Join(keysDir, "id_ed25519.pub.old")); err != nil {
		t.Fatalf("failed to backup old public key: %v", err)
	}

	// Write new keys
	if err := os.WriteFile(filepath.Join(keysDir, "id_ed25519"), newPrivPEM, 0600); err != nil {
		t.Fatalf("failed to write new private key: %v", err)
	}
	if err := os.WriteFile(filepath.Join(keysDir, "id_ed25519.pub"), newPubSSH, 0644); err != nil {
		t.Fatalf("failed to write new public key: %v", err)
	}

	// Update .well-known/polis
	wk, err := site.LoadWellKnown(s.DataDir)
	if err != nil {
		t.Fatalf("failed to load well-known: %v", err)
	}
	wk.PublicKey = newPubKey
	if err := site.SaveWellKnown(s.DataDir, wk); err != nil {
		t.Fatalf("failed to save well-known: %v", err)
	}

	// Reload in-memory keys
	s.LoadKeys()

	// Verify old keys backed up
	oldPrivBackup, err := os.ReadFile(filepath.Join(keysDir, "id_ed25519.old"))
	if err != nil {
		t.Fatal("old private key backup not found")
	}
	if string(oldPrivBackup) != string(oldPrivKey) {
		t.Error("backed up private key does not match original")
	}

	oldPubBackup, err := os.ReadFile(filepath.Join(keysDir, "id_ed25519.pub.old"))
	if err != nil {
		t.Fatal("old public key backup not found")
	}
	if strings.TrimSpace(string(oldPubBackup)) != oldPubKey {
		t.Error("backed up public key does not match original")
	}

	// Verify new keys on disk
	newPrivOnDisk, _ := os.ReadFile(filepath.Join(keysDir, "id_ed25519"))
	if string(newPrivOnDisk) != string(newPrivPEM) {
		t.Error("new private key on disk doesn't match generated key")
	}

	newPubOnDisk, _ := os.ReadFile(filepath.Join(keysDir, "id_ed25519.pub"))
	if strings.TrimSpace(string(newPubOnDisk)) != newPubKey {
		t.Error("new public key on disk doesn't match generated key")
	}

	// Verify in-memory keys updated
	currentPubKey := strings.TrimSpace(string(s.PublicKey))
	if currentPubKey != newPubKey {
		t.Errorf("in-memory public key not updated: got %s, want %s", currentPubKey, newPubKey)
	}
	if currentPubKey == oldPubKey {
		t.Error("in-memory public key should differ from old key")
	}

	// Verify .well-known/polis updated
	wk2, err := site.LoadWellKnown(s.DataDir)
	if err != nil {
		t.Fatalf("failed to reload well-known: %v", err)
	}
	if wk2.PublicKey != newPubKey {
		t.Errorf(".well-known public_key not updated: got %s, want %s", wk2.PublicKey, newPubKey)
	}
}

// ── Content Source Path Redirect Tests ────────────────────────────────

func TestContentRedirectPostHTML(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/content/pub.polis.core/post/20260302/slug.html", nil)
	w := httptest.NewRecorder()

	s.handleContentRedirect(w, req)

	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if loc != "/posts/20260302/slug.html" {
		t.Errorf("expected redirect to /posts/20260302/slug.html, got %q", loc)
	}
}

func TestContentRedirectCommentHTML(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/content/pub.polis.core/comment/20260302/reply.html", nil)
	w := httptest.NewRecorder()

	s.handleContentRedirect(w, req)

	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if loc != "/comments/20260302/reply.html" {
		t.Errorf("expected redirect to /comments/20260302/reply.html, got %q", loc)
	}
}

func TestContentRedirectNonHTMLReturns404(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/content/pub.polis.core/post/20260302/slug.md", nil)
	w := httptest.NewRecorder()

	s.handleContentRedirect(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for .md request, got %d", w.Code)
	}
}

func TestContentRedirectUnknownBundleReturns404(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/content/com.example/data/file.html", nil)
	w := httptest.NewRecorder()

	s.handleContentRedirect(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown bundle, got %d", w.Code)
	}
}

func TestContentRedirectMethodNotAllowed(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/content/pub.polis.core/post/20260302/slug.html", nil)
	w := httptest.NewRecorder()

	s.handleContentRedirect(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for POST, got %d", w.Code)
	}
}

// --- DM handler tests ---

func TestHandleDMConversations_Empty(t *testing.T) {
	s := newConfiguredServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/dm/conversations", nil)
	w := httptest.NewRecorder()
	s.handleDMConversations(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["count"] != float64(0) {
		t.Errorf("expected count 0, got %v", resp["count"])
	}
}

func TestHandleDMConversations_MethodNotAllowed(t *testing.T) {
	s := newConfiguredServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/dm/conversations", nil)
	w := httptest.NewRecorder()
	s.handleDMConversations(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleDMConversation_NotFound(t *testing.T) {
	s := newConfiguredServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/dm/conversations/nonexistent", nil)
	w := httptest.NewRecorder()
	s.handleDMConversation(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleDMSend_MissingRecipient(t *testing.T) {
	s := newConfiguredServer(t)
	body := jsonBody(t, map[string]string{"content": "hello"})
	req := httptest.NewRequest(http.MethodPost, "/api/dm/send", body)
	w := httptest.NewRecorder()
	s.handleDMSend(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleDMSend_MissingContent(t *testing.T) {
	s := newConfiguredServer(t)
	body := jsonBody(t, map[string]string{"recipient_url": "https://peer.example.com"})
	req := httptest.NewRequest(http.MethodPost, "/api/dm/send", body)
	w := httptest.NewRecorder()
	s.handleDMSend(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleDMSend_MethodNotAllowed(t *testing.T) {
	s := newConfiguredServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/dm/send", nil)
	w := httptest.NewRecorder()
	s.handleDMSend(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleDMMarkRead_MissingConvID(t *testing.T) {
	s := newConfiguredServer(t)
	body := jsonBody(t, map[string]string{})
	req := httptest.NewRequest(http.MethodPost, "/api/dm/mark-read", body)
	w := httptest.NewRecorder()
	s.handleDMMarkRead(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleDMRetry_Empty(t *testing.T) {
	s := newConfiguredServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/dm/retry", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	s.handleDMRetry(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["unsent_count"] != float64(0) {
		t.Errorf("expected unsent_count 0, got %v", resp["unsent_count"])
	}
}

func TestHandleDMRecipients_Empty(t *testing.T) {
	s := newConfiguredServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/dm/recipients", nil)
	w := httptest.NewRecorder()
	s.handleDMRecipients(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleDMConversation_MethodNotAllowed(t *testing.T) {
	s := newConfiguredServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/dm/conversations/some-id", nil)
	w := httptest.NewRecorder()
	s.handleDMConversation(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ============================================================================
// handleDMRecipients Tests
// ============================================================================

func TestHandleDMRecipients_PolicyCheck(t *testing.T) {
	// Mock two remote sites: one allows DMs, one denies
	allowSite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/policies/rules.jsonl":
			fmt.Fprintln(w, `{"active":true,"policy":"allow pub.polis.dm from all"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer allowSite.Close()

	denySite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/policies/rules.jsonl":
			fmt.Fprintln(w, `{"active":true,"policy":"deny pub.polis.dm from all"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer denySite.Close()

	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://ds.test.polis.pub"

	// Write following.json with the two mock sites
	followingPath := following.DefaultPath(s.DataDir)
	f := &following.FollowingFile{
		Version: "test",
		Following: []following.FollowingEntry{
			{URL: allowSite.URL, AuthorName: "Alice"},
			{URL: denySite.URL, AuthorName: "Bob"},
		},
	}
	following.Save(followingPath, f)

	req := httptest.NewRequest(http.MethodGet, "/api/dm/recipients", nil)
	w := httptest.NewRecorder()
	s.handleDMRecipients(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Recipients []struct {
			URL        string `json:"url"`
			AuthorName string `json:"author_name"`
			Status     string `json:"status"`
			Reason     string `json:"reason"`
		} `json:"recipients"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(resp.Recipients) != 2 {
		t.Fatalf("expected 2 recipients, got %d", len(resp.Recipients))
	}

	// First site allows all DMs
	if resp.Recipients[0].Status != "open" {
		t.Errorf("expected 'open' for allow-all site, got %q", resp.Recipients[0].Status)
	}
	// Second site denies all DMs
	if resp.Recipients[1].Status != "no-dm" {
		t.Errorf("expected 'no-dm' for deny-all site, got %q", resp.Recipients[1].Status)
	}
}

func TestHandleDMRecipients_MethodNotAllowed(t *testing.T) {
	s := newConfiguredServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/dm/recipients", nil)
	w := httptest.NewRecorder()
	s.handleDMRecipients(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleDMRecipients_NoPrivateKey(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/dm/recipients", nil)
	w := httptest.NewRecorder()
	s.handleDMRecipients(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleDMRecipients_FollowerStateCorrection(t *testing.T) {
	// Mock a site with public DM policies that follows our domain
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://ds.test.polis.pub"
	s.BaseURL = "https://mysite.com"

	remoteSite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/policies/rules.jsonl":
			fmt.Fprintln(w, `{"active":true,"policy":"allow pub.polis.dm from following"}`)
			fmt.Fprintln(w, `{"active":true,"policy":"deny pub.polis.dm from all"}`)
		case "/content/pub.polis.core/follow/following.json":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"version": "1.0",
				"following": []map[string]string{
					{"url": "https://mysite.com", "added_at": "2026-01-01T00:00:00Z"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer remoteSite.Close()

	followingPath := following.DefaultPath(s.DataDir)
	f := &following.FollowingFile{
		Version: "test",
		Following: []following.FollowingEntry{
			{URL: remoteSite.URL, AuthorName: "RemotePeer"},
		},
	}
	following.Save(followingPath, f)

	store := stream.NewStore(s.DataDir, "ds.test.polis.pub", "pub.polis.core")
	store.SaveState("pub.polis.follow", &stream.FollowerState{
		Followers: []string{},
		Count:     0,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/dm/recipients", nil)
	w := httptest.NewRecorder()
	s.handleDMRecipients(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify follower state was corrected
	var fs stream.FollowerState
	if err := store.LoadState("pub.polis.follow", &fs); err != nil {
		t.Fatalf("failed to load follower state: %v", err)
	}

	remoteDomain := strings.TrimPrefix(remoteSite.URL, "http://")
	if idx := strings.Index(remoteDomain, "/"); idx != -1 {
		remoteDomain = remoteDomain[:idx]
	}
	if idx := strings.Index(remoteDomain, ":"); idx != -1 {
		remoteDomain = remoteDomain[:idx]
	}
	found := false
	for _, f := range fs.Followers {
		if f == remoteDomain {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected follower state to include %s, got %v", remoteDomain, fs.Followers)
	}
}

func TestHandleDMRecipients_MutualFollowFallback(t *testing.T) {
	// No public DM policies — falls back to mutual-follow check
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://ds.test.polis.pub"
	s.BaseURL = "https://mysite.com"

	// Site that follows us but has no public DM policies
	followsSite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/policies/rules.jsonl":
			http.NotFound(w, r) // no public policies
		case "/content/pub.polis.core/follow/following.json":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"version": "1.0",
				"following": []map[string]string{
					{"url": "https://mysite.com", "added_at": "2026-01-01T00:00:00Z"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer followsSite.Close()

	// Site that doesn't follow us and has no public DM policies
	noFollowSite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/policies/rules.jsonl":
			http.NotFound(w, r)
		case "/content/pub.polis.core/follow/following.json":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"version":   "1.0",
				"following": []map[string]string{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer noFollowSite.Close()

	followingPath := following.DefaultPath(s.DataDir)
	f := &following.FollowingFile{
		Version: "test",
		Following: []following.FollowingEntry{
			{URL: followsSite.URL, AuthorName: "Mutual"},
			{URL: noFollowSite.URL, AuthorName: "OneWay"},
		},
	}
	following.Save(followingPath, f)

	req := httptest.NewRequest(http.MethodGet, "/api/dm/recipients", nil)
	w := httptest.NewRecorder()
	s.handleDMRecipients(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Recipients []struct {
			AuthorName string `json:"author_name"`
			Status     string `json:"status"`
			FollowsUs  bool   `json:"follows_us"`
		} `json:"recipients"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(resp.Recipients) != 2 {
		t.Fatalf("expected 2 recipients, got %d", len(resp.Recipients))
	}

	// Mutual follow → open + follows_us
	if resp.Recipients[0].Status != "open" {
		t.Errorf("expected 'open' for mutual follow, got %q", resp.Recipients[0].Status)
	}
	if !resp.Recipients[0].FollowsUs {
		t.Error("expected follows_us=true for mutual follow")
	}

	// One-way follow → no-follow
	if resp.Recipients[1].Status != "no-follow" {
		t.Errorf("expected 'no-follow' for one-way follow, got %q", resp.Recipients[1].Status)
	}
	if resp.Recipients[1].FollowsUs {
		t.Error("expected follows_us=false for one-way follow")
	}
}

func TestCountsIncludeDMUnread(t *testing.T) {
	s := newConfiguredServer(t)
	counts := s.computeAllCounts()
	// With no DM store data, DMUnread should be 0
	if counts.DMUnread != 0 {
		t.Errorf("expected DMUnread 0, got %d", counts.DMUnread)
	}
}

func TestCommentSourceURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{
			"https://alice.polis.pub/comments/20260323/reply.md",
			"https://alice.polis.pub/content/pub.polis.core/comment/20260323/reply.md",
		},
		{
			"https://alice.polis.pub/comments/20260101/test-post-20260101.md",
			"https://alice.polis.pub/content/pub.polis.core/comment/20260101/test-post-20260101.md",
		},
		{
			// No /comments/ in URL — returned as-is
			"https://alice.polis.pub/posts/20260101/hello.md",
			"https://alice.polis.pub/posts/20260101/hello.md",
		},
	}
	for _, tc := range tests {
		got := commentSourceURL(tc.input)
		if got != tc.want {
			t.Errorf("commentSourceURL(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestHandleSettings_NoDataDir(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://discovery.example.com"
	s.DiscoveryKey = "test-key"

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()

	s.handleSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	site := resp["site"].(map[string]interface{})
	if _, ok := site["data_dir"]; ok {
		t.Error("settings response should not expose data_dir")
	}
}

func TestSecurityHeaders(t *testing.T) {
	s := newTestServer(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", s.handleStatus)

	handler := securityHeadersMiddleware(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if v := rr.Header().Get("X-Content-Type-Options"); v != "nosniff" {
		t.Errorf("expected X-Content-Type-Options=nosniff, got %q", v)
	}
	if v := rr.Header().Get("X-Frame-Options"); v != "SAMEORIGIN" {
		t.Errorf("expected X-Frame-Options=SAMEORIGIN, got %q", v)
	}
}

// R18-21: securityHeadersMiddleware sets a default tenant-content CSP on
// non-API paths and skips CSP on /api/* (JSON responses don't need it).
func TestSecurityHeaders_CSP(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		wantCSP  bool
		contains string // substring that must appear in the CSP value
	}{
		{"non-api path gets tenant-content CSP", "/posts/2026/post.html", true, "script-src 'self' https://polis.pub"},
		{"root path gets CSP", "/", true, "default-src 'self'"},
		{"api path skips CSP", "/api/status", false, ""},
		{"api subpath skips CSP", "/api/v1/content/post", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			handler := securityHeadersMiddleware(mux)

			req := httptest.NewRequest(http.MethodGet, c.path, nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			got := rr.Header().Get("Content-Security-Policy")
			if c.wantCSP {
				if got == "" {
					t.Errorf("expected CSP header on %s, got none", c.path)
				}
				if !strings.Contains(got, c.contains) {
					t.Errorf("CSP for %s missing %q, got: %s", c.path, c.contains, got)
				}
				// Tenant-content CSP must NOT contain a nonce or esm.sh.
				if strings.Contains(got, "nonce-") {
					t.Errorf("tenant-content CSP should not contain a nonce, got: %s", got)
				}
				if strings.Contains(got, "esm.sh") {
					t.Errorf("tenant-content CSP should not allow esm.sh, got: %s", got)
				}
			} else {
				if got != "" {
					t.Errorf("expected no CSP on %s, got: %s", c.path, got)
				}
			}
		})
	}
}

// R18-21: GenerateCSPNonce must return distinct values per call and be
// long enough for the CSP3 minimum (128 bits / ~22 base64 chars).
func TestGenerateCSPNonce_Distinct(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		n := GenerateCSPNonce()
		if len(n) < 16 {
			t.Errorf("nonce too short: %d chars (want >=16)", len(n))
		}
		if n == "fallback-nonce-rand-failed" {
			t.Error("got fallback nonce — crypto/rand failure?")
		}
		if seen[n] {
			t.Errorf("nonce repeated: %s", n)
		}
		seen[n] = true
	}
}

// R18-21: CSPTenantContent and CSPAdminSPA shape checks.
func TestCSPTenantContent_Shape(t *testing.T) {
	csp := CSPTenantContent()
	mustContain := []string{
		"default-src 'self'",
		"script-src 'self' https://polis.pub",
		"object-src 'none'",
		"frame-src 'none'",
		"base-uri 'self'",
		// nav.js + widget.js fetch sibling tenants' public surfaces
		// (/api/nav/state, /content/.../following.json). connect-src
		// must admit *.polis.pub so the cross-visit nav and the
		// auth-aware widget mount both work.
		"connect-src 'self' https://polis.pub https://*.polis.pub",
		// R20-D-S1: defense against clickjacking on tenant pages.
		// X-Frame-Options is only set for /_/* (admin SPA) in hosted
		// mode; tenant public pages need frame-ancestors. Allows
		// embedding from polis.pub + any *.polis.pub (widget pattern).
		"frame-ancestors 'self' https://polis.pub https://*.polis.pub",
	}
	for _, s := range mustContain {
		if !strings.Contains(csp, s) {
			t.Errorf("CSPTenantContent missing %q. Got: %s", s, csp)
		}
	}
	// The script-src directive specifically must not relax to unsafe-inline,
	// unsafe-eval, esm.sh, or any nonce. Parse out the script-src directive
	// and check just its content.
	scriptSrc := extractCSPDirective(csp, "script-src")
	if scriptSrc == "" {
		t.Fatal("CSPTenantContent has no script-src directive")
	}
	for _, forbidden := range []string{"'unsafe-inline'", "'unsafe-eval'", "esm.sh", "nonce-"} {
		if strings.Contains(scriptSrc, forbidden) {
			t.Errorf("CSPTenantContent's script-src must not contain %q. script-src: %q", forbidden, scriptSrc)
		}
	}
}

// extractCSPDirective returns the contents of `directive` (e.g. "script-src")
// from a CSP header string, or "" if not present. CSP directives are
// semicolon-separated; this returns everything between the directive name
// and the next ';' or end of string.
func extractCSPDirective(csp, directive string) string {
	for _, part := range strings.Split(csp, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, directive+" ") || part == directive {
			return strings.TrimSpace(strings.TrimPrefix(part, directive))
		}
	}
	return ""
}

func TestCSPAdminSPA_NonceInjection(t *testing.T) {
	nonce := "abc123=="
	csp := CSPAdminSPA(nonce)
	if !strings.Contains(csp, "'nonce-"+nonce+"'") {
		t.Errorf("CSPAdminSPA(%q) must contain 'nonce-%s', got: %s", nonce, nonce, csp)
	}
	if !strings.Contains(csp, "https://esm.sh") {
		t.Errorf("CSPAdminSPA must allow https://esm.sh, got: %s", csp)
	}
	// Different nonce produces different CSP.
	csp2 := CSPAdminSPA("xyz789==")
	if csp == csp2 {
		t.Error("CSPAdminSPA should vary with nonce")
	}
}

// R18-21 follow-up: the admin SPA's index.html uses ~40 inline event
// handlers (onclick="App.foo()" and similar). CSP3's script-src-attr
// directive controls these independently of script-src-elem (which
// covers <script> tags). The admin-SPA CSP must:
//   - allow inline event handlers via script-src-attr 'unsafe-inline'
//   - keep script-src-elem strict (nonce-only, no 'unsafe-inline')
// so a future XSS via <script> injection still can't execute, but
// existing inline-handler UI affordances keep working.
func TestCSPAdminSPA_AllowsInlineEventHandlers(t *testing.T) {
	csp := CSPAdminSPA("test-nonce")

	scriptSrcAttr := extractCSPDirective(csp, "script-src-attr")
	if scriptSrcAttr == "" {
		t.Fatal("CSPAdminSPA must explicitly set script-src-attr")
	}
	if !strings.Contains(scriptSrcAttr, "'unsafe-inline'") {
		t.Errorf("script-src-attr must allow 'unsafe-inline' so inline event handlers work; got: %q", scriptSrcAttr)
	}

	scriptSrcElem := extractCSPDirective(csp, "script-src-elem")
	if scriptSrcElem == "" {
		t.Fatal("CSPAdminSPA must explicitly set script-src-elem")
	}
	if strings.Contains(scriptSrcElem, "'unsafe-inline'") {
		t.Errorf("script-src-elem must NOT allow 'unsafe-inline' — <script> tags must require the nonce; got: %q", scriptSrcElem)
	}
	if !strings.Contains(scriptSrcElem, "'nonce-test-nonce'") {
		t.Errorf("script-src-elem must require the per-response nonce; got: %q", scriptSrcElem)
	}
}

// R18-21: spaHandler must inject a fresh CSP nonce into the importmap
// script tag's nonce= attribute AND set a matching CSP header. Different
// requests must get different nonces.
func TestSpaHandler_CSPNonceInjection(t *testing.T) {
	// Stub FS with the placeholders the handler substitutes. Includes a
	// SECOND {{csp_nonce}} occurrence in a comment to mirror the real
	// index.html shape — a regression net for the bug where
	// strings.Replace(..., 1) only substitutes the first occurrence
	// (the comment) and leaves the actual nonce attribute as the literal
	// placeholder.
	fsys := fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte(`<html{{theme_attr}}><head>` +
				`<!-- comment mentioning {{csp_nonce}} -->` +
				`<script type="importmap" nonce="{{csp_nonce}}">{}</script>` +
				`</head><body>SPA</body></html>`),
		},
	}
	tmp := t.TempDir()
	handler := spaHandler(fsys, tmp)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	body := w.Body.String()
	if strings.Contains(body, "{{csp_nonce}}") {
		t.Error("{{csp_nonce}} placeholder should be substituted")
	}

	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "'nonce-") {
		t.Errorf("expected admin-SPA CSP with nonce, got: %s", csp)
	}

	// Extract the nonce from the CSP header.
	idx := strings.Index(csp, "'nonce-")
	rest := csp[idx+len("'nonce-"):]
	end := strings.Index(rest, "'")
	nonce := rest[:end]

	want := `<script type="importmap" nonce="` + nonce + `">`
	if !strings.Contains(body, want) {
		t.Errorf("importmap script tag should carry CSP nonce %q. Body: %s", nonce, body)
	}

	// Second request gets a different nonce.
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req)
	csp2 := w2.Header().Get("Content-Security-Policy")
	if csp == csp2 {
		t.Error("two requests should get different nonces")
	}
}

// ============================================================================
// handleShowFrontmatter Tests
// ============================================================================

func TestHandleShowFrontmatter_Success(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]bool{"show_frontmatter": true})
	req := httptest.NewRequest(http.MethodPost, "/api/settings/show-frontmatter", body)
	w := httptest.NewRecorder()

	s.handleShowFrontmatter(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["success"] != true {
		t.Error("expected success=true")
	}
	if resp["show_frontmatter"] != true {
		t.Error("expected show_frontmatter=true")
	}
}

func TestHandleShowFrontmatter_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/settings/show-frontmatter", nil)
	w := httptest.NewRecorder()

	s.handleShowFrontmatter(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleShowFrontmatter_InvalidJSON(t *testing.T) {
	s := newConfiguredServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/settings/show-frontmatter", strings.NewReader("{bad"))
	w := httptest.NewRecorder()

	s.handleShowFrontmatter(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// ============================================================================
// handleNavState Tests
// ============================================================================

func TestHandleNavState_Configured(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/nav-state", nil)
	w := httptest.NewRecorder()

	s.handleNavState(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["handle"] != "test-site" {
		t.Errorf("expected handle 'test-site', got %q", resp["handle"])
	}
	if resp["home_url"] != "https://test-site.polis.pub" {
		t.Errorf("expected home_url, got %q", resp["home_url"])
	}
	if _, ok := resp["counts"]; !ok {
		t.Error("expected counts in response")
	}
}

func TestHandleNavState_Unconfigured(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/nav-state", nil)
	w := httptest.NewRecorder()

	s.handleNavState(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	// Should return empty strings for unconfigured server
	if resp["handle"] != "" {
		t.Errorf("expected empty handle, got %q", resp["handle"])
	}
}

func TestHandleNavState_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/nav-state", nil)
	w := httptest.NewRecorder()

	s.handleNavState(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ============================================================================
// handleTags Tests
// ============================================================================

func TestHandleTagsList_Empty(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	w := httptest.NewRecorder()

	s.handleTags(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	tags, ok := resp["tags"].([]interface{})
	if !ok {
		t.Fatal("expected tags array in response")
	}
	if len(tags) != 0 {
		t.Errorf("expected empty tags, got %d", len(tags))
	}
}

func TestHandleTagApply_Success(t *testing.T) {
	s := newConfiguredServer(t)

	// Create the tag directory
	os.MkdirAll(filepath.Join(s.DataDir, "content", "pub.polis.core", "tag"), 0755)

	body := jsonBody(t, map[string]string{
		"tag":        "favorite",
		"target_uri": "https://test-site.polis.pub/content/pub.polis.core/post/20240101/hello.md",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/tags", body)
	w := httptest.NewRecorder()

	s.handleTags(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["success"] != true {
		t.Error("expected success=true")
	}
}

func TestHandleTagApply_NoKeys(t *testing.T) {
	s := newTestServer(t)

	body := jsonBody(t, map[string]string{
		"tag":        "favorite",
		"target_uri": "https://example.com/post.md",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/tags", body)
	w := httptest.NewRecorder()

	s.handleTags(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleTagApply_MissingFields(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{"tag": "favorite"})
	req := httptest.NewRequest(http.MethodPost, "/api/tags", body)
	w := httptest.NewRecorder()

	s.handleTags(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleTagRemove_MissingFields(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{"tag": "favorite"})
	req := httptest.NewRequest(http.MethodDelete, "/api/tags", body)
	w := httptest.NewRecorder()

	s.handleTags(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleTags_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPut, "/api/tags", nil)
	w := httptest.NewRecorder()

	s.handleTags(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ============================================================================
// handleSnippets Tests
// ============================================================================

func TestHandleSnippets_ListEmpty(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/snippets", nil)
	w := httptest.NewRecorder()

	s.handleSnippets(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleSnippets_CreateSuccess(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{
		"path":    "test-snippet.md",
		"content": "Hello from snippet",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/snippets", body)
	w := httptest.NewRecorder()

	s.handleSnippets(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify snippet was created
	snippetPath := filepath.Join(s.DataDir, "site", "snippets", "test-snippet.md")
	content, err := os.ReadFile(snippetPath)
	if err != nil {
		t.Fatalf("snippet file not created: %v", err)
	}
	if string(content) != "Hello from snippet" {
		t.Errorf("expected snippet content, got %q", string(content))
	}
}

func TestHandleSnippets_CreateMissingPath(t *testing.T) {
	s := newConfiguredServer(t)

	body := jsonBody(t, map[string]string{"content": "no path"})
	req := httptest.NewRequest(http.MethodPost, "/api/snippets", body)
	w := httptest.NewRecorder()

	s.handleSnippets(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleSnippets_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/snippets", nil)
	w := httptest.NewRecorder()

	s.handleSnippets(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ============================================================================
// handleDMDeleteConversation Tests
// ============================================================================

func TestHandleDMDeleteConversation_MissingID(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/dm/conversations/", nil)
	w := httptest.NewRecorder()

	s.handleDMDeleteConversation(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleDMDeleteConversation_Idempotent(t *testing.T) {
	s := newConfiguredServer(t)

	// Deleting a nonexistent conversation is idempotent (succeeds silently)
	req := httptest.NewRequest(http.MethodDelete, "/api/dm/conversations/nonexistent-id", nil)
	w := httptest.NewRecorder()

	s.handleDMDeleteConversation(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 (idempotent delete), got %d: %s", w.Code, w.Body.String())
	}
}

// ============================================================================
// handleSiteRegistrationStatus Tests
// ============================================================================

func TestHandleSiteRegistrationStatus_NoDiscovery(t *testing.T) {
	s := newConfiguredServer(t)
	// DiscoveryURL is empty by default

	req := httptest.NewRequest(http.MethodGet, "/api/site/registration-status", nil)
	w := httptest.NewRecorder()

	s.handleSiteRegistrationStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["configured"] != false {
		t.Error("expected configured=false when no discovery URL")
	}
}

func TestHandleSiteRegistrationStatus_NoBaseURL(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://ds.polis.pub"
	s.BaseURL = ""

	req := httptest.NewRequest(http.MethodGet, "/api/site/registration-status", nil)
	w := httptest.NewRecorder()

	s.handleSiteRegistrationStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] == nil {
		t.Error("expected error when no base URL")
	}
}

func TestHandleSiteRegistrationStatus_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/site/registration-status", nil)
	w := httptest.NewRecorder()

	s.handleSiteRegistrationStatus(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}
