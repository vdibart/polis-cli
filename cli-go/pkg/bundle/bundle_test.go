package bundle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultCoreBundleIsValid(t *testing.T) {
	b := DefaultCoreBundle()
	if err := b.Validate(); err != nil {
		t.Fatalf("default core bundle should be valid: %v", err)
	}
}

func TestLoadBundle(t *testing.T) {
	dir := t.TempDir()
	b := DefaultCoreBundle()
	path := filepath.Join(dir, "bundle.json")
	if err := SaveBundle(path, b); err != nil {
		t.Fatalf("save bundle: %v", err)
	}

	loaded, err := LoadBundle(path)
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	if loaded.Name != "pub.polis.core" {
		t.Errorf("got name %q, want pub.polis.core", loaded.Name)
	}
	if len(loaded.Types) != 5 {
		t.Errorf("got %d types, want 5", len(loaded.Types))
	}
}

func TestContentDir(t *testing.T) {
	b := DefaultCoreBundle()

	tests := []struct {
		typeName string
		want     string
	}{
		{"pub.polis.post", "content/pub.polis.core/post"},
		{"pub.polis.comment", "content/pub.polis.core/comment"},
		{"pub.polis.follow", "content/pub.polis.core/follow"},
		{"pub.polis.feed", "content/pub.polis.core/feed"},
	}

	for _, tt := range tests {
		got, err := b.ContentDir(tt.typeName)
		if err != nil {
			t.Errorf("ContentDir(%q): %v", tt.typeName, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ContentDir(%q) = %q, want %q", tt.typeName, got, tt.want)
		}
	}
}

func TestMountDir(t *testing.T) {
	b := DefaultCoreBundle()

	tests := []struct {
		typeName string
		want     string
	}{
		{"pub.polis.post", "posts"},
		{"pub.polis.comment", "comments"},
		{"pub.polis.follow", "follow"},
		{"pub.polis.feed", "feed"},
	}

	for _, tt := range tests {
		got, err := b.MountDir(tt.typeName)
		if err != nil {
			t.Errorf("MountDir(%q): %v", tt.typeName, err)
			continue
		}
		if got != tt.want {
			t.Errorf("MountDir(%q) = %q, want %q", tt.typeName, got, tt.want)
		}
	}
}

func TestPrivateDir(t *testing.T) {
	b := DefaultCoreBundle()

	tests := []struct {
		typeName string
		want     string
	}{
		{"pub.polis.post", ".polis/content/pub.polis.core/posts"},
		{"pub.polis.comment", ".polis/content/pub.polis.core/comments"},
		{"pub.polis.follow", ".polis/content/pub.polis.core/follows"},
		{"pub.polis.feed", ".polis/content/pub.polis.core/feeds"},
	}

	for _, tt := range tests {
		got, err := b.PrivateDir(tt.typeName)
		if err != nil {
			t.Errorf("PrivateDir(%q): %v", tt.typeName, err)
			continue
		}
		if got != tt.want {
			t.Errorf("PrivateDir(%q) = %q, want %q", tt.typeName, got, tt.want)
		}
	}
}

func TestSourceToMountPath(t *testing.T) {
	b := DefaultCoreBundle()

	tests := []struct {
		input string
		want  string
	}{
		// Post source → mount
		{"content/pub.polis.core/post/20260302/slug.html", "posts/20260302/slug.html"},
		{"content/pub.polis.core/post/20260302/slug.md", "posts/20260302/slug.md"},
		// Comment source → mount
		{"content/pub.polis.core/comment/20260302/reply.html", "comments/20260302/reply.html"},
		// Feed source → mount
		{"content/pub.polis.core/feed/index.html", "feed/index.html"},
		// No match — returned unchanged
		{"some/other/path.html", "some/other/path.html"},
		{"content/com.example/post/foo.html", "content/com.example/post/foo.html"},
		// Exact content dir (no trailing file) — no match (no trailing slash after dir)
		{"content/pub.polis.core/post", "content/pub.polis.core/post"},
	}

	for _, tt := range tests {
		got := b.SourceToMountPath(tt.input)
		if got != tt.want {
			t.Errorf("SourceToMountPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSourceToMountPathNoMount(t *testing.T) {
	// Bundle with a type that has no mount — should return path unchanged
	b := &Bundle{
		Name:    "test.bundle",
		Version: "1.0.0",
		Handler: Handler{Type: "builtin"},
		Types: map[string]ContentType{
			"test.type": {Dir: "data"},
		},
	}
	input := "content/test.bundle/data/file.html"
	got := b.SourceToMountPath(input)
	if got != input {
		t.Errorf("SourceToMountPath(%q) = %q, want unchanged", input, got)
	}
}

func TestMatchesEvent(t *testing.T) {
	b := DefaultCoreBundle()

	tests := []struct {
		event string
		want  bool
	}{
		{"pub.polis.post.published", true},
		{"pub.polis.comment.blessing.granted", true},
		{"pub.polis.follow.announced", true},
		{"pub.polis.site.registered", true},
		{"com.example.recipes.recipe.published", false},
	}

	for _, tt := range tests {
		got := b.MatchesEvent(tt.event)
		if got != tt.want {
			t.Errorf("MatchesEvent(%q) = %v, want %v", tt.event, got, tt.want)
		}
	}
}

func TestTypeForEvent(t *testing.T) {
	b := DefaultCoreBundle()

	tests := []struct {
		event string
		want  string
	}{
		{"pub.polis.post.published", "pub.polis.post"},
		{"pub.polis.comment.blessing.requested", "pub.polis.comment"},
		{"pub.polis.follow.announced", "pub.polis.follow"},
		{"pub.polis.site.registered", ""},
		{"unknown.event", ""},
	}

	for _, tt := range tests {
		got := b.TypeForEvent(tt.event)
		if got != tt.want {
			t.Errorf("TypeForEvent(%q) = %q, want %q", tt.event, got, tt.want)
		}
	}
}

func TestNotificationRuleEnabled(t *testing.T) {
	b := DefaultCoreBundle()
	rules := b.AllNotificationRules()

	// Default core bundle has new-post enabled, updated-post disabled
	foundNewPost := false
	for _, r := range rules {
		if r.ID == "new-post" {
			foundNewPost = true
		}
		if r.ID == "updated-post" {
			t.Error("updated-post should not appear in AllNotificationRules (disabled)")
		}
	}
	if !foundNewPost {
		t.Error("new-post should appear in AllNotificationRules")
	}
}

func TestValidateRejectsMissingName(t *testing.T) {
	b := &Bundle{
		Version: "1.0.0",
		Handler: Handler{Type: "builtin"},
		Types:   map[string]ContentType{},
	}
	if err := b.Validate(); err == nil {
		t.Error("expected validation error for missing name")
	}
}

func TestValidateRejectsDuplicateDirs(t *testing.T) {
	b := &Bundle{
		Name:    "test",
		Version: "1.0.0",
		Handler: Handler{Type: "builtin"},
		Types: map[string]ContentType{
			"a": {Dir: "same"},
			"b": {Dir: "same"},
		},
	}
	if err := b.Validate(); err == nil {
		t.Error("expected validation error for duplicate dirs")
	}
}

func TestValidateRejectsDuplicateMounts(t *testing.T) {
	b := &Bundle{
		Name:    "test",
		Version: "1.0.0",
		Handler: Handler{Type: "builtin"},
		Types: map[string]ContentType{
			"a": {Dir: "adir", Mount: "/same"},
			"b": {Dir: "bdir", Mount: "/same"},
		},
	}
	if err := b.Validate(); err == nil {
		t.Error("expected validation error for duplicate mounts")
	}
}

func TestSaveBundleCreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "bundle.json")
	b := DefaultCoreBundle()
	if err := SaveBundle(path, b); err != nil {
		t.Fatalf("save bundle to nested path: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("bundle file should exist: %v", err)
	}
}

func TestAllEmittedEvents(t *testing.T) {
	b := DefaultCoreBundle()
	events := b.AllEmittedEvents()

	// pub.polis.core has 10 events total
	if len(events) != 10 {
		t.Errorf("got %d events, want 10", len(events))
	}

	// Check a few key events exist
	eventSet := make(map[string]bool)
	for _, e := range events {
		eventSet[e] = true
	}
	for _, expected := range []string{
		"pub.polis.post.published",
		"pub.polis.comment.blessing.requested",
		"pub.polis.follow.announced",
	} {
		if !eventSet[expected] {
			t.Errorf("missing event: %s", expected)
		}
	}
}
