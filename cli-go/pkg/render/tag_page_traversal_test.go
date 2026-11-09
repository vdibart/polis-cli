package render

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⛔ A tag file whose "tag" is ".." (planted or cloned) used to render its page
// at tags/../index.html — the site's root index. It is now skipped, and a real
// tag still gets its page.
func TestRenderTagsSkipsANameThatIsNotATag(t *testing.T) {
	dir := t.TempDir()
	tags := filepath.Join(dir, "content", "pub.polis.core", "tag")
	if err := os.MkdirAll(tags, 0o755); err != nil {
		t.Fatal(err)
	}
	for file, name := range map[string]string{"evil.json": "..", "sep.json": "a/b", "go.json": "go"} {
		_ = os.WriteFile(filepath.Join(tags, file), []byte(`{"tag":"`+name+`","targets":[]}`), 0o644)
	}
	const home = "<html>the real home page</html>"
	_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte(home), 0o644)

	setupTestSite(t, dir)
	r, err := NewPageRenderer(PageConfig{Site: storage.At(dir), BaseURL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.RenderTags(); err != nil {
		t.Fatalf("RenderTags: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "index.html")); string(b) != home {
		t.Errorf("the root index.html was overwritten by a tag page")
	}
	if _, err := os.Stat(filepath.Join(dir, "tags", "a", "b", "index.html")); err == nil {
		t.Error("a tag name with a separator got a nested page")
	}
	if _, err := os.Stat(filepath.Join(dir, "tags", "go", "index.html")); err != nil {
		t.Errorf("a real tag lost its page: %v", err)
	}
}
