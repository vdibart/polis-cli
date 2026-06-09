package ops

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/resolve"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// newTestEngine creates an Engine with a temporary site directory for testing.
func newTestEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	siteDir := t.TempDir()

	// Create minimal site structure
	dirs := []string{
		".polis/keys",
		".well-known",
		"content/pub.polis.core/post",
		"content/pub.polis.core/comment",
		"content/pub.polis.core/follow",
		".polis/bundles/pub.polis.core/comments/pending",
		".polis/bundles/pub.polis.core/comments/drafts",
		"posts",
		"comments",
		"site/snippets",
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(siteDir, d), 0755); err != nil {
			t.Fatal(err)
		}
	}

	// Generate keys
	privKey, pubKey, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(siteDir, ".polis/keys/id_ed25519"), privKey, 0600)
	os.WriteFile(filepath.Join(siteDir, ".polis/keys/id_ed25519.pub"), pubKey, 0644)

	// Write .well-known/polis
	wk := map[string]any{
		"version":    "1.0",
		"public_key": string(pubKey),
		"base_url":   "https://test.example.com",
	}
	wkData, _ := json.Marshal(wk)
	os.WriteFile(filepath.Join(siteDir, ".well-known/polis"), wkData, 0644)

	// Use default core bundle
	coreBundle := bundle.DefaultCoreBundle()
	bundles := map[string]*bundle.Bundle{
		"pub.polis.core": coreBundle,
	}

	resolver := resolve.New(siteDir, bundles)

	engine, err := NewEngine(EngineConfig{
		Resolver:   resolver,
		Bundles:    bundles,
		PrivateKey: privKey,
		PublicKey:  pubKey,
		BaseURL:    "https://test.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}

	return engine, siteDir
}

func TestDispatchUnknownType(t *testing.T) {
	engine, _ := newTestEngine(t)

	_, err := engine.Dispatch(context.Background(), ActionRequest{
		Action:      "list",
		ContentType: "pub.unknown.type",
	})
	if err == nil {
		t.Fatal("expected error for unknown content type")
	}
	if !strings.Contains(err.Error(), "unknown content type") {
		t.Errorf("expected 'unknown content type' error, got: %s", err.Error())
	}
}

func TestDispatchUnsupportedAction(t *testing.T) {
	engine, _ := newTestEngine(t)

	_, err := engine.Dispatch(context.Background(), ActionRequest{
		Action:      "frobnicate",
		ContentType: "pub.polis.post",
	})
	if err == nil {
		t.Fatal("expected error for unsupported action")
	}
	if !strings.Contains(err.Error(), "unsupported action") {
		t.Errorf("expected 'unsupported action' error, got: %s", err.Error())
	}
}

func TestResolveShortTypeName(t *testing.T) {
	engine, _ := newTestEngine(t)

	tests := []struct {
		input    string
		expected string
	}{
		{"post", "pub.polis.post"},
		{"comment", "pub.polis.comment"},
		{"follow", "pub.polis.follow"},
		{"pub.polis.post", "pub.polis.post"},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		got := engine.resolveTypeName(tt.input)
		if got != tt.expected {
			t.Errorf("resolveTypeName(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestListPostsEmpty(t *testing.T) {
	engine, _ := newTestEngine(t)

	result, err := engine.Dispatch(context.Background(), ActionRequest{
		Action:      "list",
		ContentType: "pub.polis.post",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Errorf("expected status 'success', got %q", result.Status)
	}
	// Empty posts can be []map[string]any{} or []any{} depending on JSON round-trip
	switch posts := result.Data["posts"].(type) {
	case []map[string]any:
		if len(posts) != 0 {
			t.Errorf("expected 0 posts, got %d", len(posts))
		}
	case []any:
		if len(posts) != 0 {
			t.Errorf("expected 0 posts, got %d", len(posts))
		}
	default:
		t.Fatalf("expected posts to be a slice, got %T", result.Data["posts"])
	}
}

func TestListPostsWithIndex(t *testing.T) {
	engine, siteDir := newTestEngine(t)

	// Write a test index.jsonl with mixed post and comment entries
	indexPath := filepath.Join(siteDir, "content", "pub.polis.core", "index.jsonl")
	entries := []map[string]any{
		{"path": "posts/20260301/hello.md", "title": "Hello World", "published": "2026-03-01T00:00:00Z", "current_version": "1"},
		{"path": "comments/20260301/reply.md", "title": "A Reply", "published": "2026-03-01T01:00:00Z", "current_version": "1"},
		{"path": "posts/20260302/second.md", "title": "Second Post", "published": "2026-03-02T00:00:00Z", "current_version": "1"},
	}
	var lines []string
	for _, e := range entries {
		data, _ := json.Marshal(e)
		lines = append(lines, string(data))
	}
	os.WriteFile(indexPath, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	result, err := engine.Dispatch(context.Background(), ActionRequest{
		Action:      "list",
		ContentType: "post", // test short name resolution
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Errorf("expected status 'success', got %q", result.Status)
	}
	posts := result.Data["posts"].([]map[string]any)
	if len(posts) != 2 {
		t.Fatalf("expected 2 posts (comments filtered), got %d", len(posts))
	}
	// Should be newest first (reversed)
	if posts[0]["title"] != "Second Post" {
		t.Errorf("expected newest first, got %q", posts[0]["title"])
	}
	if posts[1]["title"] != "Hello World" {
		t.Errorf("expected oldest last, got %q", posts[1]["title"])
	}
	count := result.Data["count"].(int)
	if count != 2 {
		t.Errorf("expected count 2, got %d", count)
	}
}

func TestPublishPost(t *testing.T) {
	engine, siteDir := newTestEngine(t)

	// Write a minimal theme so render can find templates
	// (publish itself doesn't require render, it just writes the .md + updates index)
	result, err := engine.Dispatch(context.Background(), ActionRequest{
		Action:      "create",
		ContentType: "pub.polis.post",
		Payload: map[string]any{
			"markdown": "# Test Post\n\nThis is a test.",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Errorf("expected status 'success', got %q", result.Status)
	}
	if result.Data["title"] != "Test Post" {
		t.Errorf("expected title 'Test Post', got %q", result.Data["title"])
	}
	if result.Data["path"] == nil || result.Data["path"] == "" {
		t.Error("expected non-empty path")
	}
	if result.Data["version"] == nil {
		t.Error("expected version")
	}
	if result.Data["signature"] == nil || result.Data["signature"] == "" {
		t.Error("expected non-empty signature")
	}

	// Verify the post appears in the index
	indexPath := filepath.Join(siteDir, "content", "pub.polis.core", "index.jsonl")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("index file should exist: %v", err)
	}
	if !strings.Contains(string(data), "Test Post") {
		t.Error("index should contain the published post title")
	}
}

func TestPublishPostEmptyMarkdown(t *testing.T) {
	engine, _ := newTestEngine(t)

	_, err := engine.Dispatch(context.Background(), ActionRequest{
		Action:      "create",
		ContentType: "pub.polis.post",
		Payload: map[string]any{
			"markdown": "   ",
		},
	})
	if err == nil {
		t.Fatal("expected error for empty markdown")
	}
	if !strings.Contains(err.Error(), "markdown content required") {
		t.Errorf("expected 'markdown content required' error, got: %s", err.Error())
	}
}

func TestPublishPostWithExistingFrontmatter(t *testing.T) {
	engine, _ := newTestEngine(t)

	result, err := engine.Dispatch(context.Background(), ActionRequest{
		Action:      "create",
		ContentType: "pub.polis.post",
		Payload: map[string]any{
			"markdown": "---\ntitle: Old Title\n---\n# Actual Title\n\nContent here.",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Should strip the old frontmatter and use the extracted title
	if result.Data["title"] != "Actual Title" {
		t.Errorf("expected title 'Actual Title', got %q", result.Data["title"])
	}
}

func TestListFollowingEmpty(t *testing.T) {
	engine, _ := newTestEngine(t)

	result, err := engine.Dispatch(context.Background(), ActionRequest{
		Action:      "list",
		ContentType: "pub.polis.follow",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Errorf("expected status 'success', got %q", result.Status)
	}
	following := result.Data["following"].([]map[string]any)
	if len(following) != 0 {
		t.Errorf("expected 0 following entries, got %d", len(following))
	}
	count := result.Data["count"].(int)
	if count != 0 {
		t.Errorf("expected count 0, got %d", count)
	}
}

func TestListFollowingWithEntries(t *testing.T) {
	engine, siteDir := newTestEngine(t)

	// Write a following.json
	followingPath := filepath.Join(siteDir, "content", "pub.polis.core", "follow", "following.json")
	followingData := map[string]any{
		"version": "1",
		"following": []map[string]any{
			{"url": "https://alice.example.com", "added_at": "2026-01-01T00:00:00Z", "site_title": "Alice's Blog", "author_name": "Alice"},
			{"url": "https://bob.example.com", "added_at": "2026-02-01T00:00:00Z"},
		},
	}
	data, _ := json.MarshalIndent(followingData, "", "  ")
	os.WriteFile(followingPath, data, 0644)

	result, err := engine.Dispatch(context.Background(), ActionRequest{
		Action:      "list",
		ContentType: "follow", // test short name
	})
	if err != nil {
		t.Fatal(err)
	}
	following := result.Data["following"].([]map[string]any)
	if len(following) != 2 {
		t.Fatalf("expected 2 following entries, got %d", len(following))
	}
	if following[0]["url"] != "https://alice.example.com" {
		t.Errorf("expected alice, got %q", following[0]["url"])
	}
	if following[0]["site_title"] != "Alice's Blog" {
		t.Errorf("expected site_title, got %q", following[0]["site_title"])
	}
	// Bob has no metadata
	if _, has := following[1]["site_title"]; has {
		t.Error("expected bob to not have site_title")
	}
	count := result.Data["count"].(int)
	if count != 2 {
		t.Errorf("expected count 2, got %d", count)
	}
}

func TestWriteRequiresPrivateKey(t *testing.T) {
	siteDir := t.TempDir()
	os.MkdirAll(filepath.Join(siteDir, "content/pub.polis.core/post"), 0755)

	coreBundle := bundle.DefaultCoreBundle()
	bundles := map[string]*bundle.Bundle{"pub.polis.core": coreBundle}
	resolver := resolve.New(siteDir, bundles)

	engine, err := NewEngine(EngineConfig{
		Resolver: resolver,
		Bundles:  bundles,
		// No private key
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = engine.Dispatch(context.Background(), ActionRequest{
		Action:      "create",
		ContentType: "pub.polis.post",
		Payload:     map[string]any{"markdown": "# Test"},
	})
	if err == nil {
		t.Fatal("expected error when no private key")
	}
	if !strings.Contains(err.Error(), "no private key") {
		t.Errorf("expected 'no private key' error, got: %s", err.Error())
	}
}

func TestListBundles(t *testing.T) {
	engine, _ := newTestEngine(t)

	bundles := engine.ListBundles()
	if len(bundles) != 1 {
		t.Fatalf("expected 1 bundle, got %d", len(bundles))
	}
	b := bundles[0]
	if b.Name != "pub.polis.core" {
		t.Errorf("expected bundle name 'pub.polis.core', got %q", b.Name)
	}
	if len(b.Types) == 0 {
		t.Error("expected types in bundle info")
	}

	// Check that pub.polis.post has expected actions
	for _, ti := range b.Types {
		if ti.Name == "pub.polis.post" {
			if len(ti.Actions) == 0 {
				t.Error("expected actions for pub.polis.post")
			}
			found := false
			for _, a := range ti.Actions {
				if a == "create" {
					found = true
				}
			}
			if !found {
				t.Error("expected 'create' action for pub.polis.post")
			}
			return
		}
	}
	t.Error("pub.polis.post type not found in bundle info")
}

func TestOnContentChangedCallback(t *testing.T) {
	engine, _ := newTestEngine(t)

	called := false
	engine.OnContentChanged = func() {
		called = true
	}

	// Reads should NOT trigger callback
	engine.Dispatch(context.Background(), ActionRequest{
		Action:      "list",
		ContentType: "pub.polis.post",
	})
	if called {
		t.Error("OnContentChanged should not be called for read operations")
	}

	// Writes SHOULD trigger callback
	engine.Dispatch(context.Background(), ActionRequest{
		Action:      "create",
		ContentType: "pub.polis.post",
		Payload:     map[string]any{"markdown": "# Callback Test\n\nContent."},
	})
	if !called {
		t.Error("OnContentChanged should be called after write operations")
	}
}

// ── Engine creation edge cases ──────────────────────────────────────

func TestNewEngineNilResolver(t *testing.T) {
	_, err := NewEngine(EngineConfig{})
	if err == nil {
		t.Fatal("expected error for nil resolver")
	}
	if !strings.Contains(err.Error(), "resolver is required") {
		t.Errorf("expected 'resolver is required', got: %s", err.Error())
	}
}

func TestNewEngineNilBundles(t *testing.T) {
	siteDir := t.TempDir()
	resolver := resolve.New(siteDir, nil)

	engine, err := NewEngine(EngineConfig{
		Resolver: resolver,
		// Bundles nil — should default to empty map
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(engine.ListBundles()) != 0 {
		t.Error("expected no bundles")
	}
}

// ── Comment operations ──────────────────────────────────────────────

func TestCommentUnsupportedAction(t *testing.T) {
	engine, _ := newTestEngine(t)

	_, err := engine.Dispatch(context.Background(), ActionRequest{
		Action:      "list",
		ContentType: "pub.polis.comment",
	})
	if err == nil {
		t.Fatal("expected error for unsupported comment action")
	}
	if !strings.Contains(err.Error(), "unsupported action") {
		t.Errorf("expected 'unsupported action' error, got: %s", err.Error())
	}
}

func TestCommentCreateMissingID(t *testing.T) {
	engine, _ := newTestEngine(t)

	_, err := engine.Dispatch(context.Background(), ActionRequest{
		Action:      "create",
		ContentType: "pub.polis.comment",
		Payload:     map[string]any{},
	})
	if err == nil {
		t.Fatal("expected error for missing comment_id")
	}
	if !strings.Contains(err.Error(), "comment_id is required") {
		t.Errorf("expected 'comment_id is required' error, got: %s", err.Error())
	}
}

// ── Feed operations ─────────────────────────────────────────────────

// ── Follow unsupported action ───────────────────────────────────────

func TestFollowUnsupportedAction(t *testing.T) {
	engine, _ := newTestEngine(t)

	_, err := engine.Dispatch(context.Background(), ActionRequest{
		Action:      "create",
		ContentType: "pub.polis.follow",
		Payload:     map[string]any{"url": "https://example.com"},
	})
	if err == nil {
		t.Fatal("expected error for unsupported follow action")
	}
	if !strings.Contains(err.Error(), "unsupported action") {
		t.Errorf("expected 'unsupported action' error, got: %s", err.Error())
	}
}

// ── Dispatch with nil payload ───────────────────────────────────────

func TestDispatchNilPayload(t *testing.T) {
	engine, _ := newTestEngine(t)

	// Read operations should work with nil payload
	result, err := engine.Dispatch(context.Background(), ActionRequest{
		Action:      "list",
		ContentType: "pub.polis.post",
		// Payload is nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %q", result.Status)
	}
}

// ── isWriteAction coverage ──────────────────────────────────────────

func TestIsWriteAction(t *testing.T) {
	writes := []string{"create", "update", "delete", "bless", "deny", "revoke", "sync", "draft.save", "draft.delete", "refresh"}
	reads := []string{"list", "get", "draft.list", "draft.get", "render", "anything_else"}

	for _, a := range writes {
		if !isWriteAction(a) {
			t.Errorf("expected %q to be a write action", a)
		}
	}
	for _, a := range reads {
		if isWriteAction(a) {
			t.Errorf("expected %q to NOT be a write action", a)
		}
	}
}

// ── Handler Actions introspection ───────────────────────────────────

func TestBuiltinCoreHandlerActions(t *testing.T) {
	h := NewBuiltinCoreHandler()

	tests := []struct {
		contentType string
		expectNil   bool
		contains    string
	}{
		{"pub.polis.post", false, "create"},
		{"pub.polis.comment", false, "bless"},
		{"pub.polis.follow", false, "list"},
		{"pub.unknown", true, ""},
	}
	for _, tt := range tests {
		actions := h.Actions(tt.contentType)
		if tt.expectNil {
			if actions != nil {
				t.Errorf("Actions(%q) should return nil, got %v", tt.contentType, actions)
			}
			continue
		}
		found := false
		for _, a := range actions {
			if a == tt.contains {
				found = true
			}
		}
		if !found {
			t.Errorf("Actions(%q) should contain %q, got %v", tt.contentType, tt.contains, actions)
		}
	}
}

// ── ListBundles detail coverage ─────────────────────────────────────

func TestListBundlesTypeDetails(t *testing.T) {
	engine, _ := newTestEngine(t)

	bundles := engine.ListBundles()
	if len(bundles) != 1 {
		t.Fatalf("expected 1 bundle, got %d", len(bundles))
	}

	info := bundles[0]
	if info.Version == "" {
		t.Error("expected non-empty version")
	}

	// Check all expected types are present
	typeNames := map[string]bool{}
	for _, ti := range info.Types {
		typeNames[ti.Name] = true
	}
	for _, expected := range []string{"pub.polis.post", "pub.polis.comment", "pub.polis.follow"} {
		if !typeNames[expected] {
			t.Errorf("expected type %q in bundle info", expected)
		}
	}
}

func TestDispatchThemeList(t *testing.T) {
	engine, _ := newTestEngine(t)

	result, err := engine.Dispatch(context.Background(), ActionRequest{
		Action:      "list",
		ContentType: "pub.polis.theme",
	})
	if err != nil {
		t.Fatalf("dispatch theme/list: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("status = %q, want success", result.Status)
	}
	count, _ := result.Data["count"].(int)
	if count < 3 {
		t.Errorf("expected at least 3 themes (_shared, especial-light, vice), got %d", count)
	}

	themes, ok := result.Data["themes"].([]map[string]any)
	if !ok {
		t.Fatalf("themes should be []map[string]any, got %T", result.Data["themes"])
	}
	names := make(map[string]bool)
	for _, th := range themes {
		if name, ok := th["name"].(string); ok {
			names[name] = true
		}
	}
	for _, expected := range []string{"_shared", "especial-light", "vice"} {
		if !names[expected] {
			t.Errorf("expected theme %q in list", expected)
		}
	}
}

func TestDispatchThemeGet(t *testing.T) {
	engine, _ := newTestEngine(t)

	result, err := engine.Dispatch(context.Background(), ActionRequest{
		Action:      "get",
		ContentType: "pub.polis.theme",
		Payload:     map[string]any{"name": "vice"},
	})
	if err != nil {
		t.Fatalf("dispatch theme/get: %v", err)
	}
	if result.Data["name"] != "vice" {
		t.Errorf("name = %v, want vice", result.Data["name"])
	}
	if result.Data["css"] != "vice.css" {
		t.Errorf("css = %v, want vice.css", result.Data["css"])
	}
}

func TestDispatchThemeGetUnknown(t *testing.T) {
	engine, _ := newTestEngine(t)

	_, err := engine.Dispatch(context.Background(), ActionRequest{
		Action:      "get",
		ContentType: "pub.polis.theme",
		Payload:     map[string]any{"name": "nonexistent"},
	})
	if err == nil {
		t.Fatal("expected error for unknown theme")
	}
}

func TestDispatchThemeUnsupportedAction(t *testing.T) {
	engine, _ := newTestEngine(t)

	_, err := engine.Dispatch(context.Background(), ActionRequest{
		Action:      "delete",
		ContentType: "pub.polis.theme",
	})
	if err == nil {
		t.Fatal("expected error for unsupported theme action")
	}
	if !strings.Contains(err.Error(), "unsupported action") {
		t.Errorf("expected 'unsupported action' error, got: %v", err)
	}
}
