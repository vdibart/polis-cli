package sitecheck

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func writeAuthored(t *testing.T, siteDir, rel, author string) {
	t.Helper()
	p := filepath.Join(siteDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("---\nauthor: "+author+"\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func foreignPaths(found []ForeignFile) []string {
	var out []string
	for _, f := range found {
		out = append(out, f.Path)
	}
	sort.Strings(out)
	return out
}

// ⛔ The walk this replaced refused to descend into a dot-DIRECTORY and still
// examined a dot-FILE. The key filter that replaced it matched "/." anywhere, so
// another author's ".sneaky.md" in a site's post tree stopped being reported —
// by Patrol, which raises the alert, and by Tailor, which removes the file.
func TestForeignContentReportsADotFileAndSkipsADotDirectory(t *testing.T) {
	site := t.TempDir()
	const post = "content/pub.polis.core/post/"
	writeAuthored(t, site, post+"20260101/visible.md", "bob.example")
	writeAuthored(t, site, post+"20260101/.sneaky.md", "bob.example")
	writeAuthored(t, site, post+"20260101/.versions/old.md", "bob.example")
	writeAuthored(t, site, post+".drafts/20260101/hidden.md", "bob.example")
	writeAuthored(t, site, post+"20260102/mine.md", "alice.example")

	got := foreignPaths(ForeignContent(site, "alice.example", "post"))
	want := []string{post + "20260101/.sneaky.md", post + "20260101/visible.md"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("ForeignContent = %v\nwant %v", got, want)
	}
}

// ⛔ One directory that cannot be read must not hide everything beside it. The
// walk skipped the unreadable entry and examined the rest; a listing that
// failed wholesale reported NO foreign content for a tree that held some.
func TestForeignContentStillReportsBesideAnUnreadableDirectory(t *testing.T) {
	site := t.TempDir()
	const post = "content/pub.polis.core/post/"
	writeAuthored(t, site, post+"20260101/visible.md", "bob.example")
	writeAuthored(t, site, post+"20260103/locked.md", "bob.example")

	locked := filepath.Join(site, filepath.FromSlash(post+"20260103"))
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Skipf("cannot chmod: %v", err)
	}
	defer os.Chmod(locked, 0o755)
	if _, err := os.ReadDir(locked); err == nil {
		t.Skip("mode 000 is readable here (running as root?)")
	}

	got := foreignPaths(ForeignContent(site, "alice.example", "post"))
	if len(got) != 1 || got[0] != post+"20260101/visible.md" {
		t.Errorf("ForeignContent = %v, want only the readable foreign file", got)
	}
}
