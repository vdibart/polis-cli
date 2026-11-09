package navshortcut

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

func TestNormalizeKeepsAValidShortcutCanonical(t *testing.T) {
	f, err := Normalize(File{Shortcuts: []Shortcut{{Label: "Discover, discussed", Icon: "chat", Sentence: "  all posts from discover.polis.pub with comments "}}})
	if err != nil {
		t.Fatal(err)
	}
	sc := f.Shortcuts[0]
	if sc.Sentence != "all posts from discover.polis.pub with comments" || sc.ID == "" || sc.Icon != "chat" {
		t.Errorf("normalized = %+v", sc)
	}
}

func TestNormalizeRefusesWhatIsNotPlainData(t *testing.T) {
	bad := []Shortcut{
		{Label: "", Sentence: "all posts from me"},
		{Label: "<b>x</b>", Sentence: "all posts from me"},
		{Label: "x", Icon: "skull", Sentence: "all posts from me"},
		{Label: "x", Sentence: "https://evil.example/"},
		{Label: "x", Sentence: "javascript:alert(1)"},
		{Label: "x", Sentence: "all posts from discover.polis.pub with comments by date"},
	}
	for _, sc := range bad {
		if _, err := Normalize(File{Shortcuts: []Shortcut{sc}}); err == nil {
			t.Errorf("Normalize accepted %+v", sc)
		}
	}
	many := make([]Shortcut, MaxShortcuts+1)
	for i := range many {
		many[i] = Shortcut{Label: "x", Sentence: "all posts from me", ID: string(rune('a' + i))}
	}
	if _, err := Normalize(File{Shortcuts: many}); err == nil {
		t.Error("Normalize accepted more than MaxShortcuts")
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	site := storage.At(dir)
	if f, err := Load(site); err != nil || len(f.Shortcuts) != 0 {
		t.Fatalf("Load with no file = %+v, %v", f, err)
	}
	if _, err := Save(site, File{Shortcuts: []Shortcut{{Label: "D", Sentence: "all posts from discover.polis.pub with comments"}}}); err != nil {
		t.Fatal(err)
	}
	if f, err := Load(site); err != nil || len(f.Shortcuts) != 1 {
		t.Fatalf("Load after Save = %+v, %v", f, err)
	}
	if info, err := os.Stat(filepath.Join(dir, Key)); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("file = %v, %v; want mode 600", info, err)
	}
}

// A file that did not come through Save is checked on the way in.
func TestLoadRefusesAFileSaveWouldHaveRefused(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, filepath.FromSlash(Key))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"shortcuts":[{"label":"<img src=x>","icon":"star","sentence":"all posts by date"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(storage.At(dir)); err == nil {
		t.Error("Load accepted a label Save refuses")
	}
}
