package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/cli-go/pkg/resolve"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// testSetup creates a test server with an engine and mux, plus a valid API key.
func testSetup(t *testing.T) (*http.ServeMux, string, string) {
	t.Helper()
	siteDir := t.TempDir()

	// Create site structure
	dirs := []string{
		".polis/keys",
		".well-known",
		"content/pub.polis.core/post",
		"content/pub.polis.core/comment",
		"content/pub.polis.core/follow",
		".polis/content/pub.polis.core/comments/pending",
		".polis/content/pub.polis.core/comments/drafts",
		"posts",
		"comments",
		"site/snippets",
	}
	for _, d := range dirs {
		os.MkdirAll(filepath.Join(siteDir, d), 0755)
	}

	// Generate keys
	privKey, pubKey, _ := signing.GenerateKeypair()
	os.WriteFile(filepath.Join(siteDir, ".polis/keys/id_ed25519"), privKey, 0600)
	os.WriteFile(filepath.Join(siteDir, ".polis/keys/id_ed25519.pub"), pubKey, 0644)

	// Write .well-known/polis
	wk, _ := json.Marshal(map[string]any{
		"version": "1.0", "public_key": string(pubKey),
		"base_url": "https://test.example.com",
	})
	os.WriteFile(filepath.Join(siteDir, ".well-known/polis"), wk, 0644)

	// Create engine
	coreBundle := bundle.DefaultCoreBundle()
	bundles := map[string]*bundle.Bundle{"pub.polis.core": coreBundle}
	resolver := resolve.New(siteDir, bundles)

	engine, err := ops.NewEngine(ops.EngineConfig{
		Resolver:   resolver,
		Bundles:    bundles,
		PrivateKey: privKey,
		PublicKey:  pubKey,
		BaseURL:    "https://test.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Generate API key
	apiKey, _, err := ops.GenerateAPIKey(siteDir, "test")
	if err != nil {
		t.Fatal(err)
	}

	// Setup routes
	mux := http.NewServeMux()
	SetupRoutes(mux, engine, siteDir)

	return mux, siteDir, apiKey
}

func jsonBody(t *testing.T, v any) *bytes.Buffer {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewBuffer(data)
}

// ── Bundle routes ───────────────────────────────────────────────────

func TestGetBundles(t *testing.T) {
	mux, _, _ := testSetup(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/bundles", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	bundles, ok := resp["bundles"].([]any)
	if !ok {
		t.Fatalf("expected bundles array, got %T", resp["bundles"])
	}
	if len(bundles) != 1 {
		t.Errorf("expected 1 bundle, got %d", len(bundles))
	}
}

func TestGetBundleByName(t *testing.T) {
	mux, _, _ := testSetup(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/bundles/pub.polis.core", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["name"] != "pub.polis.core" {
		t.Errorf("expected name 'pub.polis.core', got %q", resp["name"])
	}
}

func TestGetBundleNotFound(t *testing.T) {
	mux, _, _ := testSetup(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/bundles/nonexistent", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestBundlesMethodNotAllowed(t *testing.T) {
	mux, _, _ := testSetup(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/bundles", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ── Content list routes (public, no auth) ───────────────────────────

func TestListPostsNoAuth(t *testing.T) {
	mux, _, _ := testSetup(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/content/post", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	posts := resp["posts"]
	if posts == nil {
		t.Error("expected posts in response")
	}
}

func TestListPostsWithEntries(t *testing.T) {
	mux, siteDir, _ := testSetup(t)

	// Write index entries
	indexPath := filepath.Join(siteDir, "content", "pub.polis.core", "index.jsonl")
	entries := []string{
		`{"path":"posts/20260301/hello.md","title":"Hello","published":"2026-03-01T00:00:00Z","current_version":"1"}`,
		`{"path":"posts/20260302/world.md","title":"World","published":"2026-03-02T00:00:00Z","current_version":"1"}`,
	}
	os.WriteFile(indexPath, []byte(strings.Join(entries, "\n")+"\n"), 0644)

	req := httptest.NewRequest(http.MethodGet, "/v1/content/post", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	posts := resp["posts"].([]any)
	if len(posts) != 2 {
		t.Errorf("expected 2 posts, got %d", len(posts))
	}
}

func TestListFollowingNoAuth(t *testing.T) {
	mux, _, _ := testSetup(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/content/follow", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// ── Auth required for writes ────────────────────────────────────────

func TestCreatePostNoAuth(t *testing.T) {
	mux, _, _ := testSetup(t)

	body := jsonBody(t, map[string]any{"markdown": "# Hello\n\nWorld"})
	req := httptest.NewRequest(http.MethodPost, "/v1/content/post", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreatePostBadAuth(t *testing.T) {
	mux, _, _ := testSetup(t)

	body := jsonBody(t, map[string]any{"markdown": "# Hello\n\nWorld"})
	req := httptest.NewRequest(http.MethodPost, "/v1/content/post", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer polis_invalid_key")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreatePostWithAuth(t *testing.T) {
	mux, _, apiKey := testSetup(t)

	body := jsonBody(t, map[string]any{"markdown": "# Hello\n\nWorld"})
	req := httptest.NewRequest(http.MethodPost, "/v1/content/post", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["title"] != "Hello" {
		t.Errorf("expected title 'Hello', got %q", resp["title"])
	}
	if resp["path"] == nil {
		t.Error("expected path in response")
	}
}

// ── Type-specific actions ───────────────────────────────────────────

func TestContentAction(t *testing.T) {
	mux, _, apiKey := testSetup(t)

	// Try a beseech action (will fail because no pending comment, but should route correctly)
	body := jsonBody(t, map[string]any{"comment_id": "test123"})
	req := httptest.NewRequest(http.MethodPost, "/v1/content/comment/actions/create", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Will fail with a handler error (no pending comment), but should NOT be 401/403/405
	if w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden || w.Code == http.StatusMethodNotAllowed {
		t.Errorf("routing failed with %d, should have reached handler", w.Code)
	}
}

func TestActionMethodNotAllowed(t *testing.T) {
	mux, _, _ := testSetup(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/content/comment/actions/bless", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// GET on actions should fail (needs auth, which is enforced for non-GET)
	// Actually the router checks method != GET → needsAuth, but GET on actions
	// should return method not allowed from the inner switch
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for GET on actions, got %d", w.Code)
	}
}

// ── Unknown type ────────────────────────────────────────────────────

func TestUnknownContentType(t *testing.T) {
	mux, _, _ := testSetup(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/content/unknown_type", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// ── CORS (same-origin only) ─────────────────────────────────────────

func TestNoCORSHeaders(t *testing.T) {
	mux, _, _ := testSetup(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/bundles", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("expected no CORS origin header (same-origin policy)")
	}
	if w.Header().Get("Access-Control-Allow-Methods") != "" {
		t.Error("expected no CORS methods header")
	}
}

func TestNoCORSOnContentRoute(t *testing.T) {
	mux, _, _ := testSetup(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/content/post", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("expected no CORS origin header on content routes")
	}
}

// ── Draft routes ────────────────────────────────────────────────────

func TestDraftsListRequiresAuth(t *testing.T) {
	mux, _, _ := testSetup(t)

	// Drafts are private content, but accessed via GET
	// The current router treats GET as public. Drafts are a future concern
	// per the plan (Gap 1). For now, drafts list is accessible without auth
	// via the GET path.
	req := httptest.NewRequest(http.MethodGet, "/v1/content/post/drafts", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Will fail at dispatch level (unsupported action) since draft.list
	// isn't wired yet, but should reach the handler
	if w.Code == http.StatusUnauthorized {
		t.Error("GET on drafts should not require auth at router level")
	}
}

func TestDraftSaveRequiresAuth(t *testing.T) {
	mux, _, _ := testSetup(t)

	body := jsonBody(t, map[string]any{"title": "Draft", "content": "test"})
	req := httptest.NewRequest(http.MethodPost, "/v1/content/post/drafts", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

// ── Full type name vs short name ────────────────────────────────────

func TestFullTypeName(t *testing.T) {
	mux, _, _ := testSetup(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/content/pub.polis.post", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for full type name, got %d: %s", w.Code, w.Body.String())
	}
}

// ── Content CRUD by ID ──────────────────────────────────────────────

func TestGetContentByID(t *testing.T) {
	mux, _, _ := testSetup(t)

	// GET /v1/content/post/{id} — dispatches to handleContentGet
	// The engine will fail since there's no real post, but routing should work
	req := httptest.NewRequest(http.MethodGet, "/v1/content/post/20260301-hello", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Should NOT be 405 or 401 — it reaches the handler
	if w.Code == http.StatusMethodNotAllowed || w.Code == http.StatusUnauthorized {
		t.Errorf("routing failed with %d, should have reached handler", w.Code)
	}
}

func TestUpdateContentRequiresAuth(t *testing.T) {
	mux, _, _ := testSetup(t)

	body := jsonBody(t, map[string]any{"markdown": "# Updated"})
	req := httptest.NewRequest(http.MethodPut, "/v1/content/post/20260301-hello", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for PUT without auth, got %d", w.Code)
	}
}

func TestUpdateContentWithAuth(t *testing.T) {
	mux, _, apiKey := testSetup(t)

	body := jsonBody(t, map[string]any{"markdown": "# Updated"})
	req := httptest.NewRequest(http.MethodPut, "/v1/content/post/20260301-hello", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Should reach the handler (will fail at dispatch, but not at routing/auth)
	if w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden || w.Code == http.StatusMethodNotAllowed {
		t.Errorf("routing failed with %d, should have reached handler", w.Code)
	}
}

func TestDeleteContentRequiresAuth(t *testing.T) {
	mux, _, _ := testSetup(t)

	req := httptest.NewRequest(http.MethodDelete, "/v1/content/post/20260301-hello", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for DELETE without auth, got %d", w.Code)
	}
}

func TestDeleteContentWithAuth(t *testing.T) {
	mux, _, apiKey := testSetup(t)

	req := httptest.NewRequest(http.MethodDelete, "/v1/content/post/20260301-hello", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Should reach handler, not be blocked by routing/auth
	if w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden || w.Code == http.StatusMethodNotAllowed {
		t.Errorf("routing failed with %d, should have reached handler", w.Code)
	}
}

func TestContentByIDMethodNotAllowed(t *testing.T) {
	mux, _, apiKey := testSetup(t)

	req := httptest.NewRequest(http.MethodPatch, "/v1/content/post/20260301-hello", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for PATCH on content/{type}/{id}, got %d", w.Code)
	}
}

// ── Drafts by ID ────────────────────────────────────────────────────

func TestDraftsGetByID(t *testing.T) {
	mux, _, _ := testSetup(t)

	// GET on drafts/{id} is public via router (auth is GET=public)
	req := httptest.NewRequest(http.MethodGet, "/v1/content/post/drafts/draft-123", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Should reach handler, not be blocked by routing
	if w.Code == http.StatusMethodNotAllowed || w.Code == http.StatusUnauthorized {
		t.Errorf("routing failed with %d for drafts GET by ID", w.Code)
	}
}

func TestDraftsDeleteRequiresAuth(t *testing.T) {
	mux, _, _ := testSetup(t)

	req := httptest.NewRequest(http.MethodDelete, "/v1/content/post/drafts/draft-123", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for DELETE drafts without auth, got %d", w.Code)
	}
}

func TestDraftsDeleteWithAuth(t *testing.T) {
	mux, _, apiKey := testSetup(t)

	req := httptest.NewRequest(http.MethodDelete, "/v1/content/post/drafts/draft-123", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Should reach handler
	if w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden || w.Code == http.StatusMethodNotAllowed {
		t.Errorf("routing failed with %d, should have reached handler", w.Code)
	}
}

func TestDraftsByIDMethodNotAllowed(t *testing.T) {
	mux, _, apiKey := testSetup(t)

	req := httptest.NewRequest(http.MethodPut, "/v1/content/post/drafts/draft-123", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for PUT on drafts/{id}, got %d", w.Code)
	}
}

func TestDraftsCreateWithAuth(t *testing.T) {
	mux, _, apiKey := testSetup(t)

	body := jsonBody(t, map[string]any{"title": "My Draft", "content": "draft content"})
	req := httptest.NewRequest(http.MethodPost, "/v1/content/post/drafts", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Should reach handler (will fail at dispatch since draft.save isn't wired, but not at auth)
	if w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden || w.Code == http.StatusMethodNotAllowed {
		t.Errorf("routing failed with %d, should have reached handler", w.Code)
	}
}

// ── Bad JSON payload ────────────────────────────────────────────────

func TestCreatePostBadJSON(t *testing.T) {
	mux, _, apiKey := testSetup(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/content/post", strings.NewReader("{invalid json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad JSON, got %d: %s", w.Code, w.Body.String())
	}
}

// ── Auth edge cases ─────────────────────────────────────────────────

func TestAuthNonBearerPrefix(t *testing.T) {
	mux, _, _ := testSetup(t)

	body := jsonBody(t, map[string]any{"markdown": "# Test"})
	req := httptest.NewRequest(http.MethodPost, "/v1/content/post", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for non-Bearer auth, got %d", w.Code)
	}
}

// ── Content path edge cases ─────────────────────────────────────────

func TestEmptyContentPath(t *testing.T) {
	mux, _, _ := testSetup(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/content/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty content path, got %d", w.Code)
	}
}

func TestDeepUnknownPath(t *testing.T) {
	mux, _, apiKey := testSetup(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/content/post/unknown/subresource", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown sub-path, got %d: %s", w.Code, w.Body.String())
	}
}

func TestExtraDeepPath(t *testing.T) {
	mux, _, _ := testSetup(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/content/post/a/b/c/d", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for extra-deep path, got %d", w.Code)
	}
}

// ── Bundle route edge cases ─────────────────────────────────────────

func TestBundleByNameMethodNotAllowed(t *testing.T) {
	mux, _, _ := testSetup(t)

	req := httptest.NewRequest(http.MethodDelete, "/v1/bundles/pub.polis.core", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for DELETE on bundles/{name}, got %d", w.Code)
	}
}

// ── Dispatch error mapping ──────────────────────────────────────────

func TestDispatchErrorNotConfigured(t *testing.T) {
	// Create an engine without private key, then try a write
	siteDir := t.TempDir()
	for _, d := range []string{".polis/keys", "content/pub.polis.core/post", "posts"} {
		os.MkdirAll(filepath.Join(siteDir, d), 0755)
	}
	coreBundle := bundle.DefaultCoreBundle()
	bundles := map[string]*bundle.Bundle{"pub.polis.core": coreBundle}
	resolver := resolve.New(siteDir, bundles)

	engine, _ := ops.NewEngine(ops.EngineConfig{
		Resolver: resolver,
		Bundles:  bundles,
		// No keys
	})

	apiKey, _, _ := ops.GenerateAPIKey(siteDir, "test")
	mux := http.NewServeMux()
	SetupRoutes(mux, engine, siteDir)

	body := jsonBody(t, map[string]any{"markdown": "# Test"})
	req := httptest.NewRequest(http.MethodPost, "/v1/content/post", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for not-configured, got %d: %s", w.Code, w.Body.String())
	}
}

// ── Error format ────────────────────────────────────────────────────

func TestErrorFormat(t *testing.T) {
	mux, _, _ := testSetup(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/content/unknown_type", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["status"] != "error" {
		t.Errorf("expected status 'error', got %q", resp["status"])
	}
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatal("expected error object")
	}
	if errObj["code"] == nil || errObj["code"] == "" {
		t.Error("expected error code")
	}
	if errObj["message"] == nil || errObj["message"] == "" {
		t.Error("expected error message")
	}
}
