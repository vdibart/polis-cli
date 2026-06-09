package resolve

import (
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
)

func testResolver(t *testing.T) *Resolver {
	t.Helper()
	b := bundle.DefaultCoreBundle()
	return New("/home/alice/mysite", map[string]*bundle.Bundle{
		b.Name: b,
	})
}

func TestWellKnownPath(t *testing.T) {
	r := testResolver(t)
	want := "/home/alice/mysite/.well-known/polis"
	if got := r.WellKnownPath(); got != want {
		t.Errorf("WellKnownPath() = %q, want %q", got, want)
	}
}

func TestKeysDir(t *testing.T) {
	r := testResolver(t)
	want := "/home/alice/mysite/.polis/keys"
	if got := r.KeysDir(); got != want {
		t.Errorf("KeysDir() = %q, want %q", got, want)
	}
}

func TestSnippetsDir(t *testing.T) {
	r := testResolver(t)
	want := "/home/alice/mysite/site/snippets"
	if got := r.SnippetsDir(); got != want {
		t.Errorf("SnippetsDir() = %q, want %q", got, want)
	}
}

func TestThemesDir(t *testing.T) {
	r := testResolver(t)
	want := "/home/alice/mysite/site/themes"
	if got := r.ThemesDir(); got != want {
		t.Errorf("ThemesDir() = %q, want %q", got, want)
	}
}

func TestContentPath(t *testing.T) {
	r := testResolver(t)

	tests := []struct {
		typeName string
		want     string
	}{
		{"pub.polis.post", "/home/alice/mysite/content/pub.polis.core/post"},
		{"pub.polis.comment", "/home/alice/mysite/content/pub.polis.core/comment"},
		{"pub.polis.follow", "/home/alice/mysite/content/pub.polis.core/follow"},
	}

	for _, tt := range tests {
		got, err := r.ContentPath(tt.typeName)
		if err != nil {
			t.Errorf("ContentPath(%q): %v", tt.typeName, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ContentPath(%q) = %q, want %q", tt.typeName, got, tt.want)
		}
	}
}

func TestMountPath(t *testing.T) {
	r := testResolver(t)

	tests := []struct {
		typeName string
		want     string
	}{
		{"pub.polis.post", "/home/alice/mysite/posts"},
		{"pub.polis.comment", "/home/alice/mysite/comments"},
		{"pub.polis.follow", "/home/alice/mysite/follow"},
	}

	for _, tt := range tests {
		got, err := r.MountPath(tt.typeName)
		if err != nil {
			t.Errorf("MountPath(%q): %v", tt.typeName, err)
			continue
		}
		if got != tt.want {
			t.Errorf("MountPath(%q) = %q, want %q", tt.typeName, got, tt.want)
		}
	}
}

func TestPrivatePath(t *testing.T) {
	r := testResolver(t)

	tests := []struct {
		typeName string
		subdirs  []string
		want     string
	}{
		{"pub.polis.post", nil, "/home/alice/mysite/.polis/bundles/pub.polis.core/posts"},
		{"pub.polis.post", []string{"drafts"}, "/home/alice/mysite/.polis/bundles/pub.polis.core/posts/drafts"},
		{"pub.polis.comment", []string{"pending"}, "/home/alice/mysite/.polis/bundles/pub.polis.core/comments/pending"},
		{"pub.polis.comment", []string{"denied"}, "/home/alice/mysite/.polis/bundles/pub.polis.core/comments/denied"},
	}

	for _, tt := range tests {
		got, err := r.PrivatePath(tt.typeName, tt.subdirs...)
		if err != nil {
			t.Errorf("PrivatePath(%q, %v): %v", tt.typeName, tt.subdirs, err)
			continue
		}
		if got != tt.want {
			t.Errorf("PrivatePath(%q, %v) = %q, want %q", tt.typeName, tt.subdirs, got, tt.want)
		}
	}
}

func TestArtifact(t *testing.T) {
	r := testResolver(t)
	want := "/home/alice/mysite/content/pub.polis.core/index.jsonl"
	got := r.Artifact("pub.polis.core", "index.jsonl")
	if got != want {
		t.Errorf("Artifact() = %q, want %q", got, want)
	}
}

func TestTypeArtifact(t *testing.T) {
	r := testResolver(t)

	got, err := r.TypeArtifact("pub.polis.comment", "blessed.json")
	if err != nil {
		t.Fatalf("TypeArtifact: %v", err)
	}
	want := "/home/alice/mysite/content/pub.polis.core/comment/blessed.json"
	if got != want {
		t.Errorf("TypeArtifact() = %q, want %q", got, want)
	}
}

func TestDSPaths(t *testing.T) {
	r := testResolver(t)
	domain := "ds.polis.pub"

	tests := []struct {
		name string
		fn   func() string
		want string
	}{
		{"DSBundleDir", func() string { return r.DSBundleDir("pub.polis.core", domain) }, "/home/alice/mysite/.polis/ds/ds.polis.pub/pub.polis.core"},
		{"DSStatePath", func() string { return r.DSStatePath("pub.polis.core", domain) }, "/home/alice/mysite/.polis/ds/ds.polis.pub/pub.polis.core/state"},
		{"DSConfigPath", func() string { return r.DSConfigPath("pub.polis.core", domain) }, "/home/alice/mysite/.polis/ds/ds.polis.pub/pub.polis.core/config"},
	}

	for _, tt := range tests {
		got := tt.fn()
		if got != tt.want {
			t.Errorf("%s() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestIndexPath(t *testing.T) {
	r := testResolver(t)
	want := "/home/alice/mysite/content/pub.polis.core/index.jsonl"
	if got := r.IndexPath("pub.polis.core"); got != want {
		t.Errorf("IndexPath() = %q, want %q", got, want)
	}
}

func TestFollowingPath(t *testing.T) {
	r := testResolver(t)
	got, err := r.FollowingPath()
	if err != nil {
		t.Fatalf("FollowingPath: %v", err)
	}
	want := "/home/alice/mysite/content/pub.polis.core/follow/following.json"
	if got != want {
		t.Errorf("FollowingPath() = %q, want %q", got, want)
	}
}

func TestBlessedCommentsPath(t *testing.T) {
	r := testResolver(t)
	got, err := r.BlessedCommentsPath()
	if err != nil {
		t.Fatalf("BlessedCommentsPath: %v", err)
	}
	want := "/home/alice/mysite/content/pub.polis.core/comment/blessed.json"
	if got != want {
		t.Errorf("BlessedCommentsPath() = %q, want %q", got, want)
	}
}

func TestVersionsDir(t *testing.T) {
	r := testResolver(t)
	got, err := r.VersionsDir("pub.polis.post", "20260301")
	if err != nil {
		t.Fatalf("VersionsDir: %v", err)
	}
	want := "/home/alice/mysite/content/pub.polis.core/post/20260301/.versions"
	if got != want {
		t.Errorf("VersionsDir() = %q, want %q", got, want)
	}
}

func TestWebappConfigPath(t *testing.T) {
	r := testResolver(t)
	want := "/home/alice/mysite/.polis/webapp/config.json"
	if got := r.WebappConfigPath(); got != want {
		t.Errorf("WebappConfigPath() = %q, want %q", got, want)
	}
}

func TestPoliciesPaths(t *testing.T) {
	r := testResolver(t)
	if got := r.PublicPoliciesPath(); got != "/home/alice/mysite/policies/rules.jsonl" {
		t.Errorf("PublicPoliciesPath() = %q", got)
	}
	if got := r.PrivatePoliciesPath(); got != "/home/alice/mysite/.polis/policies/rules.jsonl" {
		t.Errorf("PrivatePoliciesPath() = %q", got)
	}
}

func TestUnknownTypeFails(t *testing.T) {
	r := testResolver(t)
	_, err := r.ContentPath("com.example.unknown")
	if err == nil {
		t.Error("expected error for unknown type")
	}
}

func TestMountToType(t *testing.T) {
	r := testResolver(t)

	tests := []struct {
		mount string
		want  string
	}{
		{"/posts", "pub.polis.post"},
		{"/comments", "pub.polis.comment"},
		{"/follow", "pub.polis.follow"},
		{"posts", "pub.polis.post"}, // without leading slash
	}

	for _, tt := range tests {
		got, err := r.MountToType(tt.mount)
		if err != nil {
			t.Errorf("MountToType(%q): %v", tt.mount, err)
			continue
		}
		if got != tt.want {
			t.Errorf("MountToType(%q) = %q, want %q", tt.mount, got, tt.want)
		}
	}
}

func TestBundleForType(t *testing.T) {
	r := testResolver(t)
	got, err := r.BundleForType("pub.polis.post")
	if err != nil {
		t.Fatalf("BundleForType: %v", err)
	}
	if got != "pub.polis.core" {
		t.Errorf("BundleForType() = %q, want pub.polis.core", got)
	}
}

func TestRel(t *testing.T) {
	r := testResolver(t)
	got := r.Rel("/home/alice/mysite/content/pub.polis.core/post")
	if got != "content/pub.polis.core/post" {
		t.Errorf("Rel() = %q, want content/pub.polis.core/post", got)
	}
}

func TestAbs(t *testing.T) {
	r := testResolver(t)
	got := r.Abs("content/pub.polis.core/post")
	if got != "/home/alice/mysite/content/pub.polis.core/post" {
		t.Errorf("Abs() = %q", got)
	}
}

func TestContentTypeNames(t *testing.T) {
	r := testResolver(t)
	names := r.ContentTypeNames()
	if len(names) != 6 {
		t.Errorf("got %d content types, want 6", len(names))
	}
}
