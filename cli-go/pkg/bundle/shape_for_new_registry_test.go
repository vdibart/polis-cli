package bundle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// A site with posts and no registry predates the stream shape, so the registry
// created for it records the blog shape it was rendering, and reading the
// shape before the registry exists agrees. A new site starts on the stream.
func TestARegistryCreatedForAnOldSiteKeepsTheBlogShape(t *testing.T) {
	for _, tc := range []struct {
		name, post, want string
	}{
		{"new site", "", "v5"},
		{"pre-0.58 layout", "posts/20250101/hello.md", "v3"},
		{"0.58-0.62 layout", "content/pub.polis.core/post/20250101/hello.md", "v3"},
		{"only a version file", "posts/20250101/.versions/hello.md", "v5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.post != "" {
				p := filepath.Join(dir, filepath.FromSlash(tc.post))
				os.MkdirAll(filepath.Dir(p), 0o755)
				os.WriteFile(p, []byte("# hi\n"), 0o644)
			}
			site := storage.At(dir)
			if got, _ := GetActiveShapeName(site); got != tc.want {
				t.Errorf("before the registry: %s, want %s", got, tc.want)
			}
			if err := EnsureReferencePayload(site, "pub.polis.core"); err != nil {
				t.Fatal(err)
			}
			reg, err := LoadRegistry(site)
			if err != nil {
				t.Fatal(err)
			}
			if reg.ActiveShape != QualifyShape(tc.want) {
				t.Errorf("created registry: %s, want %s", reg.ActiveShape, QualifyShape(tc.want))
			}
		})
	}
}

// An existing registry is never second-guessed: a site already on the stream
// stays there, posts or not.
func TestAnExistingRegistryKeepsItsShape(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "content", "pub.polis.core", "post", "20250101", "hello.md")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("# hi\n"), 0o644)
	site := storage.At(dir)
	if err := SetActiveShapeName(site, "v4"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureReferencePayload(site, "pub.polis.core"); err != nil {
		t.Fatal(err)
	}
	if got, _ := GetActiveShapeName(site); got != "v4" {
		t.Errorf("got %s, want v4", got)
	}
}
