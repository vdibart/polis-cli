package provision

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// ⛔ Only the function and config.json are packed: a static file or a build
// leftover never reaches the code upload.
func TestTheBuildIsPackedWithOnlyTheFunctionAndItsConfig(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"config.json": `{"version":3}`,
		"functions/api/index.func/.vc-config.json": `{}`,
		"functions/api/index.func/bootstrap":       "bin",
		"static/.gitkeep":                          "",
		"static/index.html":                        "<p>",
		"builds.json":                              "{}",
	} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code, err := ArchiveFunctionBuild(dir)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(code.Archive))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	sort.Strings(names)
	want := []string{"config.json", "functions/api/index.func/.vc-config.json", "functions/api/index.func/bootstrap"}
	if len(names) != len(want) {
		t.Fatalf("packed %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("packed %v, want %v", names, want)
		}
	}
}
