package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// ============================================================================
// Site Validation Tests (replaces recoverConfig tests)
// ============================================================================

func TestSiteValidation_EmptyDirectory(t *testing.T) {
	dataDir := t.TempDir()

	// Create required directories but no polis files
	os.MkdirAll(filepath.Join(dataDir, ".polis", "keys"), 0755)

	result := site.Validate(dataDir)

	// Empty directory should return not_found or incomplete
	if result.Status == site.StatusValid {
		t.Error("Expected status to be not_found or incomplete, got valid")
	}
}

func TestSiteValidation_ValidSite(t *testing.T) {
	dataDir := t.TempDir()

	// Create directories
	os.MkdirAll(filepath.Join(dataDir, ".polis", "keys"), 0755)
	os.MkdirAll(filepath.Join(dataDir, ".well-known"), 0755)

	// Create keys (use dummy values for testing)
	pubKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest polis-local"
	privKeyPath := filepath.Join(dataDir, ".polis", "keys", "id_ed25519")
	pubKeyPath := filepath.Join(dataDir, ".polis", "keys", "id_ed25519.pub")
	os.WriteFile(privKeyPath, []byte("fake-private-key"), 0600)
	os.WriteFile(pubKeyPath, []byte(pubKey), 0644)

	// Create .well-known/polis with matching public key
	wellKnown := map[string]string{
		"subdomain":  "testsite",
		"base_url":   "https://testsite.polis.pub",
		"public_key": pubKey,
	}
	wellKnownData, _ := json.Marshal(wellKnown)
	os.WriteFile(filepath.Join(dataDir, ".well-known", "polis"), wellKnownData, 0644)

	result := site.Validate(dataDir)

	if result.Status != site.StatusValid {
		t.Errorf("Expected status valid, got %s", result.Status)
		for _, err := range result.Errors {
			t.Logf("Error: %s - %s", err.Code, err.Message)
		}
	}
}

func TestSiteValidation_MissingPrivateKey(t *testing.T) {
	dataDir := t.TempDir()

	// Create directories
	os.MkdirAll(filepath.Join(dataDir, ".polis", "keys"), 0755)
	os.MkdirAll(filepath.Join(dataDir, ".well-known"), 0755)

	// Create only public key (no private key)
	pubKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest polis-local"
	pubKeyPath := filepath.Join(dataDir, ".polis", "keys", "id_ed25519.pub")
	os.WriteFile(pubKeyPath, []byte(pubKey), 0644)

	// Create .well-known/polis
	wellKnown := map[string]string{
		"public_key": pubKey,
	}
	wellKnownData, _ := json.Marshal(wellKnown)
	os.WriteFile(filepath.Join(dataDir, ".well-known", "polis"), wellKnownData, 0644)

	result := site.Validate(dataDir)

	if result.Status == site.StatusValid {
		t.Error("Expected incomplete status when private key missing")
	}

	// Check for specific error
	found := false
	for _, err := range result.Errors {
		if err.Code == "PRIVATE_KEY_MISSING" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected PRIVATE_KEY_MISSING error")
	}
}

func TestSiteValidation_MissingWellKnown(t *testing.T) {
	dataDir := t.TempDir()

	// Create directories
	os.MkdirAll(filepath.Join(dataDir, ".polis", "keys"), 0755)

	// Create keys but no .well-known/polis
	privKeyPath := filepath.Join(dataDir, ".polis", "keys", "id_ed25519")
	pubKeyPath := filepath.Join(dataDir, ".polis", "keys", "id_ed25519.pub")
	os.WriteFile(privKeyPath, []byte("fake-private-key"), 0600)
	os.WriteFile(pubKeyPath, []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest polis-local"), 0644)

	result := site.Validate(dataDir)

	if result.Status == site.StatusValid {
		t.Error("Expected incomplete status when .well-known/polis missing")
	}

	// Check for specific error
	found := false
	for _, err := range result.Errors {
		if err.Code == "WELLKNOWN_MISSING" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected WELLKNOWN_MISSING error")
	}
}

func TestSiteValidation_PublicKeyMismatch(t *testing.T) {
	dataDir := t.TempDir()

	// Create directories
	os.MkdirAll(filepath.Join(dataDir, ".polis", "keys"), 0755)
	os.MkdirAll(filepath.Join(dataDir, ".well-known"), 0755)

	// Create keys
	pubKeyFile := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKeyFile polis-local"
	pubKeyWellKnown := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDifferent polis-local"
	privKeyPath := filepath.Join(dataDir, ".polis", "keys", "id_ed25519")
	pubKeyPath := filepath.Join(dataDir, ".polis", "keys", "id_ed25519.pub")
	os.WriteFile(privKeyPath, []byte("fake-private-key"), 0600)
	os.WriteFile(pubKeyPath, []byte(pubKeyFile), 0644)

	// Create .well-known/polis with DIFFERENT public key
	wellKnown := map[string]string{
		"public_key": pubKeyWellKnown,
	}
	wellKnownData, _ := json.Marshal(wellKnown)
	os.WriteFile(filepath.Join(dataDir, ".well-known", "polis"), wellKnownData, 0644)

	result := site.Validate(dataDir)

	if result.Status == site.StatusValid {
		t.Error("Expected incomplete status when public keys don't match")
	}

	// Check for specific error
	found := false
	for _, err := range result.Errors {
		if err.Code == "PUBLIC_KEY_MISMATCH" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected PUBLIC_KEY_MISMATCH error")
	}
}

// ============================================================================
// getSiteTitle Tests
// ============================================================================

func TestGetSiteTitle_FromWellKnownPolis(t *testing.T) {
	dataDir := t.TempDir()
	os.MkdirAll(filepath.Join(dataDir, ".well-known"), 0755)

	// Create .well-known/polis with site_title set
	wellKnown := map[string]string{
		"subdomain":  "testsite",
		"base_url":   "https://testsite.polis.pub",
		"site_title": "My Awesome Blog",
		"public_key": "ssh-ed25519 test",
	}
	wellKnownData, _ := json.Marshal(wellKnown)
	os.WriteFile(filepath.Join(dataDir, ".well-known", "polis"), wellKnownData, 0644)

	server := &Server{DataDir: dataDir}
	title := server.GetSiteTitle()

	if title != "My Awesome Blog" {
		t.Errorf("Expected site_title 'My Awesome Blog', got '%s'", title)
	}
}

func TestGetSiteTitle_FallbackToBaseURL(t *testing.T) {
	dataDir := t.TempDir()
	os.MkdirAll(filepath.Join(dataDir, ".well-known"), 0755)

	// Create .well-known/polis with empty site_title (should fall back to base_url)
	wellKnown := map[string]string{
		"subdomain":  "testsite",
		"base_url":   "https://testsite.polis.pub",
		"site_title": "",
		"public_key": "ssh-ed25519 test",
	}
	wellKnownData, _ := json.Marshal(wellKnown)
	os.WriteFile(filepath.Join(dataDir, ".well-known", "polis"), wellKnownData, 0644)

	server := &Server{DataDir: dataDir}
	title := server.GetSiteTitle()

	if title != "https://testsite.polis.pub" {
		t.Errorf("Expected fallback to base_url 'https://testsite.polis.pub', got '%s'", title)
	}
}

func TestGetSiteTitle_FallbackToSubdomain(t *testing.T) {
	dataDir := t.TempDir()
	os.MkdirAll(filepath.Join(dataDir, ".well-known"), 0755)

	// Create .well-known/polis with no site_title and no base_url
	wellKnown := map[string]string{
		"subdomain":  "testsite",
		"public_key": "ssh-ed25519 test",
	}
	wellKnownData, _ := json.Marshal(wellKnown)
	os.WriteFile(filepath.Join(dataDir, ".well-known", "polis"), wellKnownData, 0644)

	server := &Server{DataDir: dataDir}
	title := server.GetSiteTitle()

	if title != "https://testsite.polis.pub" {
		t.Errorf("Expected fallback to constructed URL 'https://testsite.polis.pub', got '%s'", title)
	}
}

func TestGetSiteTitle_NoWellKnownPolis_FallbackToConfig(t *testing.T) {
	dataDir := t.TempDir()
	// No .well-known/polis file

	server := &Server{
		DataDir: dataDir,
		Config: &Config{
			Subdomain: "configsite",
		},
	}
	title := server.GetSiteTitle()

	if title != "https://configsite.polis.pub" {
		t.Errorf("Expected fallback to config subdomain 'https://configsite.polis.pub', got '%s'", title)
	}
}

func TestGetSiteTitle_FallbackToPolisBaseURL(t *testing.T) {
	dataDir := t.TempDir()
	os.MkdirAll(filepath.Join(dataDir, ".well-known"), 0755)

	// Create .well-known/polis with no site_title, no base_url, no subdomain
	wellKnown := map[string]string{
		"public_key": "ssh-ed25519 test",
	}
	wellKnownData, _ := json.Marshal(wellKnown)
	os.WriteFile(filepath.Join(dataDir, ".well-known", "polis"), wellKnownData, 0644)

	server := &Server{DataDir: dataDir, BaseURL: "https://mysite.example.com"}
	title := server.GetSiteTitle()

	if title != "https://mysite.example.com" {
		t.Errorf("Expected fallback to POLIS_BASE_URL 'https://mysite.example.com', got '%s'", title)
	}
}

func TestGetSiteTitle_NoWellKnownPolis_NoConfig(t *testing.T) {
	dataDir := t.TempDir()
	// No .well-known/polis file, no config

	server := &Server{DataDir: dataDir}
	title := server.GetSiteTitle()

	if title != "" {
		t.Errorf("Expected empty string when no .well-known/polis and no config, got '%s'", title)
	}
}

// ============================================================================
// Sync Helper Tests
// ============================================================================

func TestFirstNonEmptyString(t *testing.T) {
	payload := map[string]interface{}{
		"comment_url": "https://example.com/comments/20260222/abc.md",
		"source_url":  "https://fallback.com/comments/20260222/abc.md",
		"empty_key":   "",
		"int_key":     42,
	}

	// Returns first matching key
	got := firstNonEmptyString(payload, "comment_url", "source_url")
	if got != "https://example.com/comments/20260222/abc.md" {
		t.Errorf("expected comment_url value, got %q", got)
	}

	// Falls back to second key when first is missing
	got = firstNonEmptyString(payload, "missing_key", "source_url")
	if got != "https://fallback.com/comments/20260222/abc.md" {
		t.Errorf("expected source_url fallback, got %q", got)
	}

	// Falls back when first key is empty string
	got = firstNonEmptyString(payload, "empty_key", "source_url")
	if got != "https://fallback.com/comments/20260222/abc.md" {
		t.Errorf("expected source_url fallback for empty key, got %q", got)
	}

	// Returns empty when no keys match
	got = firstNonEmptyString(payload, "no_such_key", "also_missing")
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}

	// Skips non-string values
	got = firstNonEmptyString(payload, "int_key", "source_url")
	if got != "https://fallback.com/comments/20260222/abc.md" {
		t.Errorf("expected source_url fallback for non-string, got %q", got)
	}
}

func TestExtractPostPathFromURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://alice.polis.pub/posts/20260127/hello.md", "posts/20260127/hello.md"},
		{"https://example.com/posts/20260222/test.md", "posts/20260222/test.md"},
		{"https://example.com/some/path", "https://example.com/some/path"}, // no /posts/ -> returns as-is
		{"", ""},
	}
	for _, tt := range tests {
		got := extractPostPathFromURL(tt.input)
		if got != tt.want {
			t.Errorf("extractPostPathFromURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBlessingSyncHandler_StoresAutoBlessedComment(t *testing.T) {
	dataDir := t.TempDir()

	// Create required directories
	os.MkdirAll(filepath.Join(dataDir, ".well-known"), 0755)
	os.MkdirAll(filepath.Join(dataDir, "metadata"), 0755)
	os.MkdirAll(filepath.Join(dataDir, "posts", "20260222"), 0755)

	// Create .well-known/polis
	wellKnown := map[string]string{
		"subdomain":  "follower1",
		"base_url":   "https://follower1.polis.pub",
		"public_key": "ssh-ed25519 test",
	}
	wkData, _ := json.Marshal(wellKnown)
	os.WriteFile(filepath.Join(dataDir, ".well-known", "polis"), wkData, 0644)

	// Start a test HTTP server that serves the comment markdown
	commentContent := "---\ntitle: Great post!\n---\nI really enjoyed this."
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(commentContent))
	}))
	defer ts.Close()

	s := &Server{
		DataDir: dataDir,
		BaseURL: "https://follower1.polis.pub",
	}

	handler := &blessingSyncHandler{server: s}

	// Simulate a polis.blessing.granted event targeting our domain
	// with comment_url pointing to our test server
	events := []discovery.StreamEvent{
		{
			ID:   json.Number("1"),
			Type: "polis.blessing.granted",
			Payload: map[string]interface{}{
				"target_domain": "follower1.polis.pub",
				"source_domain": "testpilot.polis.pub",
				"comment_url":   ts.URL + "/comments/20260222/abc123.md",
				"in_reply_to":   "https://follower1.polis.pub/posts/20260222/my-post.md",
			},
		},
	}

	result := handler.Process(events)
	if !result.FilesChanged {
		t.Error("expected FilesChanged=true after storing auto-blessed comment")
	}

	// Verify comment file was written
	commentPath := filepath.Join(dataDir, "comments", "20260222", "abc123.md")
	data, err := os.ReadFile(commentPath)
	if err != nil {
		t.Fatalf("expected comment file at %s, got error: %v", commentPath, err)
	}
	if string(data) != commentContent {
		t.Errorf("comment content = %q, want %q", string(data), commentContent)
	}

	// Verify blessed-comments.json was updated
	bcPath := filepath.Join(dataDir, "metadata", "blessed-comments.json")
	bcData, err := os.ReadFile(bcPath)
	if err != nil {
		t.Fatalf("expected blessed-comments.json, got error: %v", err)
	}
	var bc map[string]interface{}
	if err := json.Unmarshal(bcData, &bc); err != nil {
		t.Fatalf("invalid blessed-comments.json: %v", err)
	}
	comments, ok := bc["comments"].([]interface{})
	if !ok || len(comments) == 0 {
		t.Error("expected at least one post entry in blessed-comments.json")
	}
}

func TestBlessingSyncHandler_SkipsExistingComment(t *testing.T) {
	dataDir := t.TempDir()

	os.MkdirAll(filepath.Join(dataDir, ".well-known"), 0755)
	os.MkdirAll(filepath.Join(dataDir, "comments", "20260222"), 0755)

	wellKnown := map[string]string{
		"subdomain":  "follower1",
		"base_url":   "https://follower1.polis.pub",
		"public_key": "ssh-ed25519 test",
	}
	wkData, _ := json.Marshal(wellKnown)
	os.WriteFile(filepath.Join(dataDir, ".well-known", "polis"), wkData, 0644)

	// Pre-create the comment file
	commentPath := filepath.Join(dataDir, "comments", "20260222", "abc123.md")
	os.WriteFile(commentPath, []byte("existing content"), 0644)

	// Server that should NOT be called
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Write([]byte("new content"))
	}))
	defer ts.Close()

	s := &Server{
		DataDir: dataDir,
		BaseURL: "https://follower1.polis.pub",
	}

	handler := &blessingSyncHandler{server: s}

	events := []discovery.StreamEvent{
		{
			ID:   json.Number("1"),
			Type: "polis.blessing.granted",
			Payload: map[string]interface{}{
				"target_domain": "follower1.polis.pub",
				"comment_url":   ts.URL + "/comments/20260222/abc123.md",
				"in_reply_to":   "https://follower1.polis.pub/posts/20260222/my-post.md",
			},
		},
	}

	result := handler.Process(events)
	if result.FilesChanged {
		t.Error("expected FilesChanged=false when comment already exists")
	}
	if called {
		t.Error("expected no HTTP fetch when comment file already exists")
	}

	// Verify original content preserved
	data, _ := os.ReadFile(commentPath)
	if string(data) != "existing content" {
		t.Errorf("existing file was overwritten: got %q", string(data))
	}
}

func TestBlessingSyncHandler_IgnoresNonTargetDomain(t *testing.T) {
	dataDir := t.TempDir()

	os.MkdirAll(filepath.Join(dataDir, ".well-known"), 0755)

	wellKnown := map[string]string{
		"subdomain":  "follower1",
		"base_url":   "https://follower1.polis.pub",
		"public_key": "ssh-ed25519 test",
	}
	wkData, _ := json.Marshal(wellKnown)
	os.WriteFile(filepath.Join(dataDir, ".well-known", "polis"), wkData, 0644)

	s := &Server{
		DataDir: dataDir,
		BaseURL: "https://follower1.polis.pub",
	}

	handler := &blessingSyncHandler{server: s}

	// Event targeting a different domain — should be ignored
	events := []discovery.StreamEvent{
		{
			ID:   json.Number("1"),
			Type: "polis.blessing.granted",
			Payload: map[string]interface{}{
				"target_domain": "someone-else.polis.pub",
				"comment_url":   "https://testpilot.polis.pub/comments/20260222/abc123.md",
				"in_reply_to":   "https://someone-else.polis.pub/posts/20260222/post.md",
			},
		},
	}

	result := handler.Process(events)
	if result.FilesChanged {
		t.Error("expected FilesChanged=false for events targeting another domain")
	}
}

func TestCursorGreater(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"5", "4", true},
		{"4", "5", false},
		{"30", "4", true},  // was broken with string comparison
		{"4", "30", false},
		{"100", "9", true}, // multi-digit > single-digit
		{"9", "100", false},
		{"0", "0", false},
		{"", "", false},
		{"abc", "def", false}, // non-numeric fallback
	}
	for _, tt := range tests {
		got := cursorGreater(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("cursorGreater(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

// ============================================================================
// SPA Fallback Handler Tests
// ============================================================================

func newTestFS() fs.FS {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>SPA</html>")},
		"app.js":     &fstest.MapFile{Data: []byte("// app")},
		"style.css":  &fstest.MapFile{Data: []byte("body{}")},
	}
}

func TestSPAHandler_RootServesIndex(t *testing.T) {
	handler := spaHandler(newTestFS())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if body := w.Body.String(); body != "<html>SPA</html>" {
		t.Errorf("expected index.html content, got %q", body)
	}
}

func TestSPAHandler_ExistingAsset(t *testing.T) {
	handler := spaHandler(newTestFS())

	for _, path := range []string{"/app.js", "/style.css"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d", path, w.Code)
		}
	}
}

func TestSPAHandler_DeepLinkFallsBackToIndex(t *testing.T) {
	handler := spaHandler(newTestFS())

	deepPaths := []string{
		"/_/posts",
		"/_/social/feed",
		"/_/posts/20260218/hello",
		"/_/posts/drafts/my-draft",
		"/_/comments/new",
		"/_/settings",
		"/_/snippets/global/header.html",
	}

	for _, path := range deepPaths {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d", path, w.Code)
		}
		if body := w.Body.String(); body != "<html>SPA</html>" {
			t.Errorf("%s: expected index.html content for SPA fallback, got %q", path, body)
		}
	}
}

// ============================================================================
// Logger Tests
// ============================================================================

func TestPruneLogs_BasicPrune(t *testing.T) {
	logsDir := filepath.Join(t.TempDir(), "logs")
	os.MkdirAll(logsDir, 0755)

	// Create an old log file (10 days ago)
	oldFile := filepath.Join(logsDir, "2026-02-15.log")
	os.WriteFile(oldFile, []byte("old"), 0644)
	oldTime := time.Now().AddDate(0, 0, -10)
	os.Chtimes(oldFile, oldTime, oldTime)

	// Create a recent log file (today)
	recentFile := filepath.Join(logsDir, "2026-02-25.log")
	os.WriteFile(recentFile, []byte("recent"), 0644)

	logger := NewLogger(LogLevelBasic, logsDir)
	logger.retentionDays = 7

	logger.PruneLogs()

	// Old file should be deleted
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("expected old log file to be deleted")
	}

	// Recent file should still exist
	if _, err := os.Stat(recentFile); err != nil {
		t.Error("expected recent log file to still exist")
	}
}

func TestPruneLogs_TimeGuard(t *testing.T) {
	logsDir := filepath.Join(t.TempDir(), "logs")
	os.MkdirAll(logsDir, 0755)

	logger := NewLogger(LogLevelBasic, logsDir)
	logger.retentionDays = 7

	// First call should succeed
	logger.PruneLogs()

	// Create an old file after first prune
	oldFile := filepath.Join(logsDir, "2026-02-10.log")
	os.WriteFile(oldFile, []byte("old"), 0644)
	oldTime := time.Now().AddDate(0, 0, -20)
	os.Chtimes(oldFile, oldTime, oldTime)

	// Second call within 24h should be a no-op (time guard)
	logger.PruneLogs()

	// File should still exist because prune was skipped
	if _, err := os.Stat(oldFile); os.IsNotExist(err) {
		t.Error("expected old file to still exist due to time guard")
	}
}

func TestPruneLogs_NilSafety(t *testing.T) {
	// Should not panic on nil logger
	var logger *Logger
	logger.PruneLogs()
}

func TestEnvLogLevel(t *testing.T) {
	dataDir := t.TempDir()
	envPath := filepath.Join(dataDir, ".env")

	os.WriteFile(envPath, []byte("LOG_LEVEL=2\nLOG_RETENTION_DAYS=14\n"), 0644)

	s := NewServer(dataDir, "")
	s.LoadEnv()

	if s.LogLevel != 2 {
		t.Errorf("expected LogLevel=2, got %d", s.LogLevel)
	}
	if s.LogRetentionDays != 14 {
		t.Errorf("expected LogRetentionDays=14, got %d", s.LogRetentionDays)
	}
}

func TestEnvDefaultLevel(t *testing.T) {
	dataDir := t.TempDir()

	// Create minimal site structure so Initialize doesn't fail
	os.MkdirAll(filepath.Join(dataDir, ".polis", "keys"), 0755)

	s := NewServer(dataDir, "")
	s.Initialize()
	defer s.Close()

	if s.LogLevel != LogLevelBasic {
		t.Errorf("expected default LogLevel=%d, got %d", LogLevelBasic, s.LogLevel)
	}
	if s.LogRetentionDays != DefaultLogRetentionDays {
		t.Errorf("expected default LogRetentionDays=%d, got %d", DefaultLogRetentionDays, s.LogRetentionDays)
	}

	// Verify Logger was created
	if s.Logger == nil {
		t.Error("expected Logger to be created with default level")
	}
}

func TestEnvWriteBack(t *testing.T) {
	dataDir := t.TempDir()
	os.MkdirAll(filepath.Join(dataDir, ".polis", "keys"), 0755)

	s := NewServer(dataDir, "")
	s.Initialize()
	defer s.Close()

	// Verify .env was written with defaults
	envPath := filepath.Join(dataDir, ".env")
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("expected .env to exist: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "LOG_LEVEL=1") {
		t.Errorf("expected LOG_LEVEL=1 in .env, got:\n%s", content)
	}
	if !strings.Contains(content, "LOG_RETENTION_DAYS=7") {
		t.Errorf("expected LOG_RETENTION_DAYS=7 in .env, got:\n%s", content)
	}
}

func TestKeyAudit_NoPrivateKeyInLogFormats(t *testing.T) {
	// Read all .go files in the server package and verify that
	// LogInfo/LogError/LogWarn/LogDebug format strings don't contain
	// patterns that could leak private key material.
	files, err := filepath.Glob(filepath.Join(".", "*.go"))
	if err != nil {
		t.Fatalf("failed to glob: %v", err)
	}

	dangerPatterns := []string{
		"PrivateKey",
		"privKey",
		"private_key",
		"priv_key",
	}

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		content := string(data)
		// Find all log format strings (LogInfo, LogError, etc.)
		for _, line := range strings.Split(content, "\n") {
			trimmed := strings.TrimSpace(line)
			isLogCall := strings.Contains(trimmed, "s.LogInfo(") ||
				strings.Contains(trimmed, "s.LogError(") ||
				strings.Contains(trimmed, "s.LogWarn(") ||
				strings.Contains(trimmed, "s.LogDebug(") ||
				strings.Contains(trimmed, "l.log(")
			if !isLogCall {
				continue
			}
			for _, pattern := range dangerPatterns {
				if strings.Contains(trimmed, pattern) {
					t.Errorf("potential private key leak in log call in %s: %s", file, trimmed)
				}
			}
		}
	}
}
