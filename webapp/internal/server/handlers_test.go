package server

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/hooks"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
)

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

// ============================================================================
// handleRender Tests
// ============================================================================

// ============================================================================
// handleDrafts Tests
// ============================================================================

// ============================================================================
// handleDraft Tests (single draft operations)
// ============================================================================

// ============================================================================
// handlePublish Tests
// ============================================================================

// ============================================================================
// handlePosts Tests
// ============================================================================

// ============================================================================
// handlePost Tests (single post)
// ============================================================================

// ============================================================================
// handleRepublish Tests
// ============================================================================

// ============================================================================
// handleCommentDrafts Tests
// ============================================================================

// ============================================================================
// handleCommentSign Tests
// ============================================================================

// ============================================================================
// handleCommentsPending/Blessed/Denied Tests
// ============================================================================

// ============================================================================
// handleCommentsSync Tests
// ============================================================================

// ============================================================================
// handleBlessingRequests Tests
// ============================================================================

// ============================================================================
// handleBlessingGrant Tests
// ============================================================================

// ============================================================================
// handleBlessingDeny Tests
// ============================================================================

// ============================================================================
// handleBlessedComments Tests
// ============================================================================

// ============================================================================
// handleBlessingRevoke Tests
// ============================================================================

// ============================================================================
// handleSettings Tests
// ============================================================================

// ============================================================================
// handleAutomations Tests
// ============================================================================

// ============================================================================
// handleAutomationsQuick Tests
// ============================================================================

// ============================================================================
// handleAutomation Tests (single automation)
// ============================================================================

// ============================================================================
// handleTemplates Tests
// ============================================================================

// ============================================================================
// handleCommentBeseech Tests
// ============================================================================

// ============================================================================
// handleCommentDraft Tests (single comment draft)
// ============================================================================

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
POLIS_BASE_URL=https://test-api-key.example`
	envPath := filepath.Join(s.DataDir, ".env")
	os.WriteFile(envPath, []byte(envContent), 0644)

	s.LoadEnv()

	if s.DiscoveryURL != "https://test-discovery.com" {
		t.Errorf("expected DiscoveryURL from .env, got %s", s.DiscoveryURL)
	}
	if s.BaseURL != "https://test-api-key.example" {
		t.Errorf("expected BaseURL from .env, got %s", s.BaseURL)
	}
}

func TestLoadEnv_QuotedValues(t *testing.T) {
	s := newTestServer(t)

	// Create .env with quoted values
	envContent := `DISCOVERY_SERVICE_URL="https://quoted.com"
POLIS_BASE_URL='https://single-quoted-key.example'`
	envPath := filepath.Join(s.DataDir, ".env")
	os.WriteFile(envPath, []byte(envContent), 0644)

	s.LoadEnv()

	if s.DiscoveryURL != "https://quoted.com" {
		t.Errorf("expected quotes stripped from URL, got %s", s.DiscoveryURL)
	}
	if s.BaseURL != "https://single-quoted-key.example" {
		t.Errorf("expected quotes stripped from base URL, got %s", s.BaseURL)
	}
}

func TestLoadEnv_Comments(t *testing.T) {
	s := newTestServer(t)

	// Create .env with comments
	envContent := `# This is a comment
DISCOVERY_SERVICE_URL=https://actual-url.com
# Another comment
# POLIS_BASE_URL=https://commented-out.example
POLIS_BASE_URL=https://actual-key.example`
	envPath := filepath.Join(s.DataDir, ".env")
	os.WriteFile(envPath, []byte(envContent), 0644)

	s.LoadEnv()

	if s.DiscoveryURL != "https://actual-url.com" {
		t.Errorf("expected non-comment URL, got %s", s.DiscoveryURL)
	}
	if s.BaseURL != "https://actual-key.example" {
		t.Errorf("expected non-comment base URL, got %s", s.BaseURL)
	}
}

func TestLoadEnv_EmptyLines(t *testing.T) {
	s := newTestServer(t)

	// Create .env with empty lines
	envContent := `

DISCOVERY_SERVICE_URL=https://test.com

POLIS_BASE_URL=https://test-key.example

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
POLIS_BASE_URL=https://valid-key.example
=value-with-no-key`
	envPath := filepath.Join(s.DataDir, ".env")
	os.WriteFile(envPath, []byte(envContent), 0644)

	s.LoadEnv()

	// Valid lines should still be parsed
	if s.DiscoveryURL != "https://valid.com" {
		t.Errorf("expected valid URL, got %s", s.DiscoveryURL)
	}
	if s.BaseURL != "https://valid-key.example" {
		t.Errorf("expected valid base URL, got %s", s.BaseURL)
	}
}

func TestLoadEnv_OverridesConfig(t *testing.T) {
	s := newTestServer(t)

	// Set up existing config
	s.Config = &Config{
		Subdomain: "existing-site",
	}
	s.DiscoveryURL = "https://old-discovery.com"
	s.BaseURL = "https://old-key.example"

	// Create .env with new values
	envContent := `DISCOVERY_SERVICE_URL=https://new-discovery.com
POLIS_BASE_URL=https://new-key.example`
	envPath := filepath.Join(s.DataDir, ".env")
	os.WriteFile(envPath, []byte(envContent), 0644)

	s.LoadEnv()

	// .env should override previous values
	if s.DiscoveryURL != "https://new-discovery.com" {
		t.Errorf("expected .env to override URL, got %s", s.DiscoveryURL)
	}
	if s.BaseURL != "https://new-key.example" {
		t.Errorf("expected .env to override base URL, got %s", s.BaseURL)
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

// ============================================================================
// Webhook Safety Regression Tests
// ============================================================================

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
	if _, err := os.Stat(oldDir); !errors.Is(err, fs.ErrNotExist) {
		t.Error("expected old drafts dir to be removed after migration")
	}

	// New dir should exist with the file
	newDir := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts")
	if _, err := os.Stat(newDir); errors.Is(err, fs.ErrNotExist) {
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
	if _, err := os.Stat(newDir); !errors.Is(err, fs.ErrNotExist) {
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
	if _, err := os.Stat(oldDir); errors.Is(err, fs.ErrNotExist) {
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

// ============================================================================
// Security: Draft ID Sanitization Tests (M1)
// ============================================================================

// ============================================================================
// Security: Path Traversal Tests (M2)
// ============================================================================

// ============================================================================
// handleFollowing Tests
// ============================================================================

// ============================================================================
// handleFeed Tests (cache-backed)
// ============================================================================

// ============================================================================
// handleFeedRefresh Tests
// ============================================================================

// ============================================================================
// handleFeedRead Tests
// ============================================================================

// ============================================================================
// handleFeedViewed Tests
// ============================================================================

// ============================================================================
// handleFeedCounts Tests
// ============================================================================

// ============================================================================
// handleRemotePost Tests
// ============================================================================

// ============================================================================
// Remote Avatar Tests
// ============================================================================

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
// handleConversations Tests
// ============================================================================

// ============================================================================
// handlePulse Tests
// ============================================================================

// ============================================================================
// handleFollowerCount Tests
// ============================================================================

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

// ============================================================================
// handleDeployCheck Tests
// ============================================================================

// ============================================================================
// handleSetupWizardDismiss Tests
// ============================================================================

// ============================================================================
// handleInit Setup Wizard State Tests
// ============================================================================

// ============================================================================
// handleSettings Setup Wizard Dismissed Tests
// ============================================================================

// ============================================================================
// handleEditorPanelMode Tests
// ============================================================================

// ============================================================================
// handleNotifications Tests
// ============================================================================

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

	// Check the site's public facts (GET /v1/site) include the avatar
	req2 := httptest.NewRequest(http.MethodGet, "/v1/site", nil)
	w2 := httptest.NewRecorder()
	s.handlePublicSite(w2, req2)

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

func TestHandleThemeSwitch_StardustReserved(t *testing.T) {
	s := newTestServer(t)

	body := jsonBody(t, map[string]string{"theme": "stardust"})
	req := httptest.NewRequest(http.MethodPost, "/api/settings/theme", body)
	w := httptest.NewRecorder()

	s.handleThemeSwitch(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "reserved") {
		t.Errorf("expected error about stardust being reserved, got: %s", w.Body.String())
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

// ============================================================================
// handleSSE Tests
// ============================================================================

// ==================== Feed Grouped Tests ====================

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

// ============================================================================
// handleUnpublish Tests
// ============================================================================

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

	// DS call will fail since there's no real discovery service. ⛔ No answer
	// is not a refusal: it is unknown whether the DS took the key, so the
	// rotation is pending (503) and the staged key is kept.
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 (DS answer lost), got %d: %s", w.Code, w.Body.String())
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
	wk, err := site.LoadWellKnown(s.Site())
	if err != nil {
		t.Fatalf("failed to load well-known: %v", err)
	}
	wk.PublicKey = newPubKey
	if err := site.SaveWellKnown(s.Site(), wk, site.ChangePublicKey); err != nil {
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
	wk2, err := site.LoadWellKnown(s.Site())
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

func TestHandleDMKeyring(t *testing.T) {
	s := newConfiguredServer(t)

	// Not provisioned yet → 404.
	req := httptest.NewRequest(http.MethodGet, "/api/dm/keyring", nil)
	w := httptest.NewRecorder()
	s.handleDMKeyring(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 before provisioning, got %d", w.Code)
	}

	// Provision a keyring with a password epoch (bootstrap holds a server_dek; epoch 1
	// holds wrapped_dek + kdf).
	kr := &dm.Keyring{}
	if _, err := kr.AddBootstrapEpoch(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := kr.SetPassword([]byte("pw")); err != nil {
		t.Fatal(err)
	}
	if err := kr.Save(s.Site()); err != nil {
		t.Fatal(err)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/dm/keyring", nil)
	w = httptest.NewRecorder()
	s.handleDMKeyring(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()

	// The wrapped material the browser needs to unlock is present.
	if !strings.Contains(body, "wrapped_dek") || !strings.Contains(body, "argon2id") {
		t.Errorf("keyring response should expose wrapped_dek + kdf; got: %s", body)
	}
	// The server-held bootstrap private key MUST NOT be served to the browser.
	if strings.Contains(body, "server_dek") {
		t.Errorf("keyring response must never contain server_dek; got: %s", body)
	}
}

func TestHandleDMSetPassword(t *testing.T) {
	s := newConfiguredServer(t)

	// Provision the bootstrap keyring (as Medic/init would).
	kr := &dm.Keyring{}
	if _, err := kr.AddBootstrapEpoch(); err != nil {
		t.Fatal(err)
	}
	if err := kr.Save(s.Site()); err != nil {
		t.Fatal(err)
	}

	// A browser-built payload (the server never sees the password/DEK; wrapped blobs are
	// opaque to it). A real pubkey so the republished block verifies.
	epochPub := base64.StdEncoding.EncodeToString(bytesSeq(32))
	payload := map[string]interface{}{
		"public_key_messages":  epochPub,
		"wrapped_dek":          "d2hhdGV2ZXI=",
		"wrapped_dek_recovery": "cmVjb3Zlcnk=",
		"kdf":                  map[string]interface{}{"algo": "argon2id", "salt": "AAECAwQFBgcICQoLDA0ODw==", "t": 3, "m": 65536, "p": 4},
		"recovery_kdf":         map[string]interface{}{"algo": "hkdf-sha256", "salt": "EBESExQVFhcYGRobHB0eHw=="},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/dm/password", jsonBody(t, payload))
	w := httptest.NewRecorder()
	s.handleDMSetPassword(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["epoch_id"] != float64(1) {
		t.Errorf("epoch_id = %v, want 1", resp["epoch_id"])
	}

	// Keyring now has the password epoch as current.
	reloaded, err := dm.LoadKeyring(s.Site())
	if err != nil {
		t.Fatal(err)
	}
	cur, _ := reloaded.CurrentEpoch()
	if cur.ID != 1 || cur.Kind != dm.EpochKindPassword || cur.PublicKeyMessages != epochPub {
		t.Errorf("current epoch = %+v, want password epoch 1 with the provided pubkey", cur)
	}

	// The messages-key block was republished and the new epoch verifies against identity.
	wkData, err := os.ReadFile(filepath.Join(s.DataDir, ".well-known", "polis"))
	if err != nil {
		t.Fatal(err)
	}
	var wk struct {
		PublicKeyMessages *dm.MessagesKeyBlock `json:"public_key_messages"`
	}
	json.Unmarshal(wkData, &wk)
	if wk.PublicKeyMessages == nil || wk.PublicKeyMessages.Current.Epoch != 1 || wk.PublicKeyMessages.Current.Key != epochPub {
		t.Fatalf("published block should advertise epoch 1 with the new key; got %+v", wk.PublicKeyMessages)
	}
	if ok, err := dm.VerifyMessagesKeyEntry(wk.PublicKeyMessages.Current, s.PublicKey); err != nil || !ok {
		t.Errorf("republished block must verify against the identity key: ok=%v err=%v", ok, err)
	}

	// A second set-password is rejected (password already set → 409).
	req2 := httptest.NewRequest(http.MethodPost, "/api/dm/password", jsonBody(t, payload))
	w2 := httptest.NewRecorder()
	s.handleDMSetPassword(w2, req2)
	if w2.Code != http.StatusConflict {
		t.Errorf("second set-password should be 409, got %d", w2.Code)
	}
}

// TestHandleDMRewrapPassword covers the phase-4.8 O(1) re-wrap: the server
// overwrites the current epoch's wrap material in place (no new epoch, no pubkey
// change, no republish), bumping the revision; and rejects a re-wrap when there's
// no password epoch yet.
func TestHandleDMRewrapPassword(t *testing.T) {
	s := newConfiguredServer(t)

	// Re-wrap before any password epoch exists → 400.
	earlyKr := &dm.Keyring{}
	if _, err := earlyKr.AddBootstrapEpoch(); err != nil {
		t.Fatal(err)
	}
	if err := earlyKr.Save(s.Site()); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/dm/password/rewrap", jsonBody(t, map[string]interface{}{
		"wrapped_dek": "bmV3", "kdf": map[string]interface{}{"algo": "argon2id", "salt": "AAA=", "t": 3, "m": 65536, "p": 4},
	}))
	w := httptest.NewRecorder()
	s.handleDMRewrapPassword(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("rewrap with no password epoch should be 400, got %d: %s", w.Code, w.Body.String())
	}

	// Now provision a password epoch and capture its current state.
	epochPub := base64.StdEncoding.EncodeToString(bytesSeq(32))
	if _, err := earlyKr.AddPasswordEpoch(epochPub, "b2xkLXdyYXA=", "b2xkLXJlYw==",
		dm.KDFParams{Algo: "argon2id", Salt: "b2xkLXNhbHQ=", Time: 3, Memory: 65536, Threads: 4},
		dm.KDFParams{Algo: "hkdf-sha256", Salt: "b2xkLXJlYy1zYWx0"}); err != nil {
		t.Fatal(err)
	}
	if err := earlyKr.Save(s.Site()); err != nil {
		t.Fatal(err)
	}
	before, _ := dm.LoadKeyring(s.Site())
	revBefore := before.Revision

	// Change-password re-wrap: new wrapped_dek + kdf only (recovery untouched).
	req = httptest.NewRequest(http.MethodPost, "/api/dm/password/rewrap", jsonBody(t, map[string]interface{}{
		"wrapped_dek": "bmV3LXdyYXA=",
		"kdf":         map[string]interface{}{"algo": "argon2id", "salt": "bmV3LXNhbHQ=", "t": 3, "m": 65536, "p": 4},
	}))
	w = httptest.NewRecorder()
	s.handleDMRewrapPassword(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("rewrap should be 200, got %d: %s", w.Code, w.Body.String())
	}

	after, err := dm.LoadKeyring(s.Site())
	if err != nil {
		t.Fatal(err)
	}
	cur, _ := after.CurrentEpoch()
	if cur.WrappedDEK != "bmV3LXdyYXA=" {
		t.Errorf("wrapped_dek not updated: %q", cur.WrappedDEK)
	}
	if cur.KDF == nil || cur.KDF.Salt != "bmV3LXNhbHQ=" {
		t.Errorf("kdf salt not updated: %+v", cur.KDF)
	}
	// Recovery wrap + epoch pubkey + epoch id are untouched; revision bumped.
	if cur.WrappedDEKRecovery != "b2xkLXJlYw==" {
		t.Errorf("recovery wrap should be untouched, got %q", cur.WrappedDEKRecovery)
	}
	if cur.PublicKeyMessages != epochPub || cur.ID != 1 {
		t.Errorf("re-wrap must not change the epoch pubkey/id: %+v", cur)
	}
	if after.Revision <= revBefore {
		t.Errorf("revision should bump: before=%d after=%d", revBefore, after.Revision)
	}

	// Empty body → nothing to re-wrap → 400.
	req = httptest.NewRequest(http.MethodPost, "/api/dm/password/rewrap", jsonBody(t, map[string]interface{}{}))
	w = httptest.NewRecorder()
	s.handleDMRewrapPassword(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty rewrap should be 400, got %d", w.Code)
	}

	// Wrong method → 405.
	req = httptest.NewRequest(http.MethodGet, "/api/dm/password/rewrap", nil)
	w = httptest.NewRecorder()
	s.handleDMRewrapPassword(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET should be 405, got %d", w.Code)
	}
}

// bytesSeq returns n bytes 0,1,2,…
func bytesSeq(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i)
	}
	return b
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
	f := &following.FollowingFile{
		Version: "test",
		Following: []following.FollowingEntry{
			{URL: allowSite.URL, AuthorName: "Alice"},
			{URL: denySite.URL, AuthorName: "Bob"},
		},
	}
	following.Save(storage.At(s.DataDir), f)

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
			Avatar     *struct {
				BG string `json:"bg"`
			} `json:"avatar"`
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

	// Every recipient carries an avatar config so the composer can render a
	// real avatar (or the deterministic hue fallback when the site has none /
	// is unreachable, as these https-forced mock fetches are).
	for i, rec := range resp.Recipients {
		if rec.Avatar == nil || rec.Avatar.BG == "" {
			t.Errorf("recipient[%d] (%s): expected a non-empty avatar config, got %+v", i, rec.AuthorName, rec.Avatar)
		}
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

	f := &following.FollowingFile{
		Version: "test",
		Following: []following.FollowingEntry{
			{URL: remoteSite.URL, AuthorName: "RemotePeer"},
		},
	}
	following.Save(storage.At(s.DataDir), f)

	store := stream.NewStore(s.Site(), "ds.test.polis.pub", "pub.polis.core")
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

	f := &following.FollowingFile{
		Version: "test",
		Following: []following.FollowingEntry{
			{URL: followsSite.URL, AuthorName: "Mutual"},
			{URL: noFollowSite.URL, AuthorName: "OneWay"},
		},
	}
	following.Save(storage.At(s.DataDir), f)

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

func TestSecurityHeaders(t *testing.T) {
	s := newTestServer(t)

	mux := http.NewServeMux()
	mux.HandleFunc(CapabilitiesPath, s.handleCapabilities)

	handler := securityHeadersMiddleware(mux)

	req := httptest.NewRequest(http.MethodGet, CapabilitiesPath, nil)
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
		{"api path skips CSP", "/api/capabilities", false, ""},
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
	// Different nonce produces different CSP.
	csp2 := CSPAdminSPA("xyz789==")
	if csp == csp2 {
		t.Error("CSPAdminSPA should vary with nonce")
	}
}

// The owner app publishes in the owner's name, so its policy names no origin
// but its own for scripts and connections: a CDN listed there could run code
// in the page if an injection ever landed (the v4 editor's esm.sh was the last).
func TestCSPAdminSPAAllowsNoThirdPartyScriptOrConnectOrigin(t *testing.T) {
	csp := CSPAdminSPA("n0nce")
	for _, d := range []string{"default-src", "script-src", "script-src-elem", "script-src-attr", "connect-src"} {
		v := extractCSPDirective(csp, d)
		if v == "" {
			t.Errorf("%s is missing from CSPAdminSPA: %s", d, csp)
			continue
		}
		for _, src := range strings.Fields(v) {
			if strings.HasPrefix(src, "'") {
				continue // a keyword, a nonce or a hash, never an origin
			}
			t.Errorf("%s allows %q; the owner app's scripts and connections are its own ('self') only", d, src)
		}
	}
}

// The owner app attaches every handler from its own scripts, so its policy
// runs no inline event-handler attribute (script-src-attr 'none'), and a
// <script> tag still needs the per-response nonce (no 'unsafe-inline' on
// script-src-elem). An onclick= planted in rendered HTML then never runs.
func TestCSPAdminSPARunsNoInlineEventHandler(t *testing.T) {
	csp := CSPAdminSPA("test-nonce")

	scriptSrcAttr := extractCSPDirective(csp, "script-src-attr")
	if scriptSrcAttr == "" {
		t.Fatal("CSPAdminSPA must explicitly set script-src-attr")
	}
	if scriptSrcAttr != "'none'" {
		t.Errorf("script-src-attr must be 'none': the owner app attaches its handlers from script, never as markup attributes; got: %q", scriptSrcAttr)
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

// ============================================================================
// handleShowFrontmatter Tests
// ============================================================================

// ============================================================================
// handleTags Tests
// ============================================================================

// ============================================================================
// handleSnippets Tests
// ============================================================================

// ============================================================================
// handleDMDeleteConversation Tests
// ============================================================================

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

// TestHandleDMSendSealed_StoresBoxAndDelivers verifies phase-4.6 browser-seal
// send: the server relays the browser's pre-sealed delivery box to the recipient
// and stores the browser's pre-sealed box-to-self copy VERBATIM (it seals
// nothing — the sender's password-epoch DEK never reaches it).
func TestHandleDMSendSealed_StoresBoxAndDelivers(t *testing.T) {
	var delivered int
	recipient := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/v1/content/dm/actions/deliver") {
			delivered++
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer recipient.Close()

	s := newConfiguredServer(t)

	pub, dek, err := dm.NewEpochKeypair()
	if err != nil {
		t.Fatalf("NewEpochKeypair: %v", err)
	}
	boxPub := base64.StdEncoding.EncodeToString(pub[:])
	// Two boxes as the browser would produce them (here both sealed to self —
	// the server treats them as opaque ciphertext either way).
	dCt, dN, _ := dm.Encrypt([]byte("see you thursday"), &pub, &dek)
	sCt, sN, _ := dm.Encrypt([]byte("see you thursday"), &pub, &dek)
	sentCtB64 := base64.StdEncoding.EncodeToString(sCt)

	body := jsonBody(t, map[string]interface{}{
		"recipient_url":   recipient.URL,
		"sender_epoch":    1,
		"recipient_epoch": 1,
		"box_pub":         boxPub,
		"delivery": map[string]string{
			"ciphertext": base64.StdEncoding.EncodeToString(dCt),
			"nonce":      base64.StdEncoding.EncodeToString(dN[:]),
		},
		"sent": map[string]string{
			"ciphertext": sentCtB64,
			"nonce":      base64.StdEncoding.EncodeToString(sN[:]),
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/dm/send-sealed", body)
	w := httptest.NewRecorder()
	s.handleDMSendSealed(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if delivered != 1 {
		t.Errorf("recipient should have received exactly 1 delivery, got %d", delivered)
	}

	peer := dm.ExtractDomainFromURL(recipient.URL)
	msgs, err := dm.NewMailbox(s.Site()).LoadMessages(peer)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 stored message, got %d", len(msgs))
	}
	m := msgs[0]
	if m.Dir != dm.DirOut {
		t.Errorf("dir = %q, want %q", m.Dir, dm.DirOut)
	}
	if m.KeyEpoch != 1 {
		t.Errorf("key_epoch = %d, want 1", m.KeyEpoch)
	}
	if m.Ciphertext != sentCtB64 {
		t.Error("stored ciphertext must be the browser's sent box, stored verbatim (no re-seal)")
	}
	if m.BoxPub != boxPub {
		t.Errorf("box_pub = %q, want %q", m.BoxPub, boxPub)
	}
}

// TestHandleDMSendSealed_Validation rejects missing required fields and wrong methods.
func TestHandleDMSendSealed_Validation(t *testing.T) {
	s := newConfiguredServer(t)

	// Wrong method.
	req := httptest.NewRequest(http.MethodGet, "/api/dm/send-sealed", nil)
	w := httptest.NewRecorder()
	s.handleDMSendSealed(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: status %d, want 405", w.Code)
	}

	// Missing boxes.
	req = httptest.NewRequest(http.MethodPost, "/api/dm/send-sealed", jsonBody(t, map[string]interface{}{
		"recipient_url": "https://bob.polis.pub",
	}))
	w = httptest.NewRecorder()
	s.handleDMSendSealed(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing boxes: status %d, want 400", w.Code)
	}
}

// TestHandleDMRecipientKey_Validation covers method + missing-arg paths (the
// happy path needs a live recipient with a published, signed messages key,
// exercised by the dm package's interop tests).
func TestHandleDMRecipientKey_Validation(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/dm/recipient-key", nil)
	w := httptest.NewRecorder()
	s.handleDMRecipientKey(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d, want 405", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/dm/recipient-key", nil)
	w = httptest.NewRecorder()
	s.handleDMRecipientKey(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing recipient_url: status %d, want 400", w.Code)
	}
}

// TestHandleDMProtectionStatus covers the phase-4.10 endpoint: method/arg
// validation, and that a recipient who refuses / is unreachable collapses to
// status:"unknown" (so the SPA soft-warns rather than misleads). The signed
// happy path is exercised by the dm package's protection/interop tests.
func TestHandleDMProtectionStatus(t *testing.T) {
	s := newConfiguredServer(t)

	// Wrong method → 405.
	req := httptest.NewRequest(http.MethodPost, "/api/dm/protection-status", nil)
	w := httptest.NewRecorder()
	s.handleDMProtectionStatus(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d, want 405", w.Code)
	}

	// Missing recipient_url → 400.
	req = httptest.NewRequest(http.MethodGet, "/api/dm/protection-status", nil)
	w = httptest.NewRecorder()
	s.handleDMProtectionStatus(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing recipient_url: status %d, want 400", w.Code)
	}

	// Recipient refuses the signed query (e.g. not a mutual) → status:"unknown", 200.
	recip := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer recip.Close()
	req = httptest.NewRequest(http.MethodGet, "/api/dm/protection-status?recipient_url="+recip.URL, nil)
	w = httptest.NewRecorder()
	s.handleDMProtectionStatus(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("unknown path: status %d, want 200: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "unknown" {
		t.Errorf("refused recipient should yield status=unknown, got %v", resp["status"])
	}
}

// TestHandleDMSetPassword_ClosesBootstrapWindow verifies the phase-7/6 follow-up: setting
// a password re-seals prior bootstrap-epoch messages FORWARD to the new epoch and clears the
// operator-readable bootstrap server_dek. After it, the message is readable only under the
// new epoch's DEK and the bootstrap key is gone.
func TestHandleDMSetPassword_ClosesBootstrapWindow(t *testing.T) {
	s := newConfiguredServer(t)
	const peer = "alice.example.com"
	const secret = "see you in the window"

	// Provision a bootstrap keyring and capture its DEK + pubkey.
	kr := &dm.Keyring{}
	bootDEK, err := kr.AddBootstrapEpoch()
	if err != nil {
		t.Fatal(err)
	}
	if err := kr.Save(s.Site()); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := dm.LoadKeyring(s.Site())
	bootPubRaw, _ := base64.StdEncoding.DecodeString(reloaded.Epochs[0].PublicKeyMessages)
	var bootPub [32]byte
	copy(bootPub[:], bootPubRaw)

	// Store a bootstrap-epoch received message (sealed to our bootstrap pubkey).
	ephPub, ephSK, _ := dm.NewEpochKeypair()
	ct, nonce, _ := dm.Encrypt([]byte(secret), &bootPub, &ephSK)
	mb := dm.NewMailbox(s.Site())
	if _, err := mb.AppendReceived(peer, peer, 0, ct, nonce, ephPub, ""); err != nil {
		t.Fatal(err)
	}

	// Browser-minted epoch-1 keypair; the real pubkey is the re-seal target.
	newPub, newDEK, _ := dm.NewEpochKeypair()
	payload := map[string]interface{}{
		"public_key_messages":  base64.StdEncoding.EncodeToString(newPub[:]),
		"wrapped_dek":          "d2hhdGV2ZXI=",
		"wrapped_dek_recovery": "cmVjb3Zlcnk=",
		"kdf":                  map[string]interface{}{"algo": "argon2id", "salt": "AAECAwQFBgcICQoLDA0ODw==", "t": 3, "m": 65536, "p": 4},
		"recovery_kdf":         map[string]interface{}{"algo": "hkdf-sha256", "salt": "EBESExQVFhcYGRobHB0eHw=="},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/dm/password", jsonBody(t, payload))
	w := httptest.NewRecorder()
	s.handleDMSetPassword(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("set-password: %d %s", w.Code, w.Body.String())
	}

	// Bootstrap server_dek must be cleared.
	after, _ := dm.LoadKeyring(s.Site())
	if after.Epochs[0].ServerDEK != "" {
		t.Error("bootstrap server_dek must be cleared after set-password")
	}

	// The message is now readable ONLY under the new epoch DEK, tagged epoch 1.
	withNew, err := mb.ReadConversation(peer, map[int][32]byte{1: newDEK})
	if err != nil {
		t.Fatal(err)
	}
	if len(withNew) != 1 || withNew[0].Locked || withNew[0].KeyEpoch != 1 {
		t.Fatalf("message should be unlocked epoch-1: %+v", withNew)
	}
	if withNew[0].Plaintext != secret {
		t.Errorf("plaintext = %q, want %q", withNew[0].Plaintext, secret)
	}
	// The (now-cleared) bootstrap DEK no longer opens it.
	withBoot, _ := mb.ReadConversation(peer, map[int][32]byte{0: bootDEK})
	if len(withBoot) == 1 && !withBoot[0].Locked {
		t.Error("after re-seal, the bootstrap DEK must not open the message")
	}
}

// TestHookRoutesGatedByEnableHooks verifies the hook/automation routes are only
// registered when EnableHooks is true (localhost). On hosted (EnableHooks=false)
// they must 404 — not just 403 — so the surface isn't reachable at all.
func TestHookRoutesGatedByEnableHooks(t *testing.T) {
	body := func() *bytes.Buffer {
		return jsonBody(t, map[string]string{"hook_type": "post-publish", "script": "echo hi"})
	}

	// Hosted-style: hooks disabled → route not registered → 404.
	//
	// ⚠️ THE SERVER MUST BE AUTHORISED FOR THIS ASSERTION TO MEAN WHAT IT SAYS.
	// The owner gate runs before the mux, so an unauthorised caller is refused
	// by the gate and never reaches the 404 — which would make this test pass
	// or fail for the wrong reason. Authorising it keeps the original
	// property under test: with hooks off, the route is ABSENT, not merely
	// forbidden.
	s := newConfiguredServer(t)
	s.EnableHooks = false
	s.BoundToLoopback = true
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/automations", body()))
	if w.Code != http.StatusNotFound {
		t.Errorf("hooks disabled: expected 404 (route absent), got %d", w.Code)
	}

	// ⭐ AND THE NEW PROPERTY, DECLARED RATHER THAN DISCOVERED: on an instance
	// with no provider enrolled, the gate masks route existence. An
	// unauthorised caller cannot tell an absent route from a present one,
	// which is strictly more than the old behaviour gave — the 404 above
	// previously told any stranger exactly which routes this build registers.
	locked := newConfiguredServer(t)
	locked.EnableHooks = false
	locked.Mode = mode.RemoteServerSigned
	w = httptest.NewRecorder()
	locked.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/automations", body()))
	if w.Code == http.StatusNotFound {
		t.Error("a locked-down instance revealed that /api/automations is unregistered; " +
			"the gate should mask route existence from an unauthorised caller")
	}

	// Localhost: hooks enabled → route registered → reaches handler (not 404).
	s2 := newConfiguredServer(t)
	s2.EnableHooks = true
	w2 := httptest.NewRecorder()
	s2.Handler().ServeHTTP(w2, httptest.NewRequest(http.MethodPost, "/api/automations", body()))
	if w2.Code == http.StatusNotFound {
		t.Error("hooks enabled: route should be registered, got 404")
	}
}

// A theme the site's shape cannot draw is refused before anything changes:
// daryl is v5-only (its v5.json is its whole definition), so on a v3 or v4
// site the switch would set it and then fail to copy a stylesheet it lacks.
func TestHandleThemeSwitch_RefusesAThemeTheShapeCannotDraw(t *testing.T) {
	s := newConfiguredServer(t)
	setupTestTheme(t, s, "turbo")
	setupTestTheme(t, s, "daryl")
	// A v4 site (v5, the default, can draw daryl).
	if err := bundle.SetActiveShapeName(storage.At(s.DataDir), "v4"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(s.DataDir, ".polis", "bundles", "registry.json"))

	w := httptest.NewRecorder()
	s.handleThemeSwitch(w, httptest.NewRequest(http.MethodPost, "/api/settings/theme", jsonBody(t, map[string]string{"theme": "daryl"})))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "not offered for this site's shape") {
		t.Fatalf("expected the shape refusal, got %d: %s", w.Code, w.Body.String())
	}
	after, _ := os.ReadFile(filepath.Join(s.DataDir, ".polis", "bundles", "registry.json"))
	if string(before) != string(after) {
		t.Error("a refused switch must leave the site's registry as it was")
	}
}

// ⭐ The widget follows through the shared follow, so its checks apply: a site
// cannot follow itself from its own widget any more than from its app or the
// API. The widget used to call the package itself and skipped them.
func TestWidgetFollowGoesThroughTheSharedFollowAndItsChecks(t *testing.T) {
	s := newConfiguredServer(t)
	body := jsonBody(t, map[string]string{"author": "test-site.polis.pub"})
	req := httptest.NewRequest(http.MethodPost, "/api/widget/follow", body)
	w := httptest.NewRecorder()
	s.handleWidgetFollow(w, req)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Cannot follow your own site") {
		t.Errorf("a widget self-follow = %d %s, want refused as the shared follow refuses it", w.Code, w.Body.String())
	}
}
