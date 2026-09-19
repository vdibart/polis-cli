package clone

import (
	"os"
	"path/filepath"
	"testing"
)

// A clone copies a STRANGER's site, so every path it writes comes from bytes
// the stranger controls: an index entry's `path`, a bundle declaration's
// `path` and `dir`, the licence pointer. None of them may write outside the
// clone folder. An unsafe entry is skipped and reported, never fatal — one
// hostile line must not cost the reader the rest of the site.

func TestClone_RejectsIndexPathTraversal(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "clone")

	site := &fullSite{
		wellKnown: `{"version":"2.0","public_key":"k"}`,
		fallback:  "pwned",
		index: []string{
			`{"type":"post","path":"../escaped.md","published":"2026-01-01T00:00:00Z"}`,
			`{"type":"post","path":"content/pub.polis.core/post/../../../../escaped2.md","published":"2026-01-01T00:00:00Z"}`,
			`{"type":"post","path":"content/pub.polis.core/post/20260101/ok.md","published":"2026-01-01T00:00:00Z"}`,
		},
		files: map[string]string{
			"/escaped.md":  "pwned",
			"/escaped2.md": "pwned",
			"/content/pub.polis.core/post/20260101/ok.md": "---\ntitle: ok\n---\n",
		},
	}
	ts := site.serve(t)
	defer ts.Close()

	res, err := Clone(ts.URL, target, CloneOptions{FullClone: true})
	if err != nil {
		t.Fatalf("a hostile entry must not abort the clone: %v", err)
	}
	for _, name := range []string{"escaped.md", "escaped2.md"} {
		if _, err := os.Stat(filepath.Join(parent, name)); err == nil {
			t.Errorf("%s was written OUTSIDE the clone folder", name)
		}
	}
	if res.PostsDownloaded != 1 {
		t.Errorf("PostsDownloaded = %d, want 1 (the safe entry)", res.PostsDownloaded)
	}
	if len(res.RejectedPaths) != 2 {
		t.Errorf("RejectedPaths = %v, want the 2 unsafe paths reported", res.RejectedPaths)
	}
}

func TestClone_RejectsAbsoluteIndexPath(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "clone")
	abs := filepath.Join(parent, "absolute.md")

	site := &fullSite{
		wellKnown: `{"version":"2.0","public_key":"k"}`,
		fallback:  "pwned",
		index: []string{
			`{"type":"post","path":"` + abs + `","published":"2026-01-01T00:00:00Z"}`,
		},
		files: map[string]string{abs: "pwned"},
	}
	ts := site.serve(t)
	defer ts.Close()

	res, err := Clone(ts.URL, target, CloneOptions{FullClone: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abs); err == nil {
		t.Errorf("absolute index path %s was written", abs)
	}
	if len(res.RejectedPaths) != 1 {
		t.Errorf("RejectedPaths = %v, want the absolute path reported", res.RejectedPaths)
	}
}

// A symlink already inside the target (left by the user, or by anything else)
// must not become a way out: the check is on where the write LANDS.
func TestClone_RejectsWriteThroughSymlinkInTarget(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "clone")
	outside := filepath.Join(parent, "outside")
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(target, "link")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	site := &fullSite{
		wellKnown: `{"version":"2.0","public_key":"k"}`,
		fallback:  "pwned",
		index: []string{
			`{"type":"post","path":"link/evil.md","published":"2026-01-01T00:00:00Z"}`,
		},
		files: map[string]string{"/link/evil.md": "pwned"},
	}
	ts := site.serve(t)
	defer ts.Close()

	res, err := Clone(ts.URL, target, CloneOptions{FullClone: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "evil.md")); err == nil {
		t.Error("write followed a symlink out of the clone folder")
	}
	if len(res.RejectedPaths) != 1 {
		t.Errorf("RejectedPaths = %v, want the symlinked path reported", res.RejectedPaths)
	}
}

// The other remote-controlled paths: the bundle declaration's location, its
// content `dir`, and the licence pointer.
func TestClone_RejectsTraversalInBundleAndLicencePointers(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "clone")

	site := &fullSite{
		wellKnown: `{"version":"2.0","public_key":"k",
  "license": "/../licence-escaped.json",
  "bundles": {"pub.polis.core": {"path": "../bundle-escaped.json"}, "x": {"path": "content/pub.polis.core/bundle.json"}}}`,
		fallback: "{}",
		files: map[string]string{
			"/licence-escaped.json": "{}",
			"/bundle-escaped.json":  bundleJSON(),
			"/content/pub.polis.core/bundle.json": `{"name":"pub.polis.core","version":"1","handler":{"type":"builtin"},
  "types":{"pub.polis.follow":{"dir":"../../../roster-escaped"},"pub.polis.comment":{"dir":"../../../replies-escaped"}}}`,
			"/content/pub.polis.core/../../../roster-escaped/following.json": "{}",
		},
	}
	ts := site.serve(t)
	defer ts.Close()

	res, err := Clone(ts.URL, target, CloneOptions{FullClone: true})
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(parent)
	for _, e := range entries {
		if e.Name() != "clone" {
			t.Errorf("clone wrote %q outside its folder", e.Name())
		}
	}
	if len(res.RejectedPaths) == 0 {
		t.Error("unsafe bundle/licence paths were not reported")
	}
}

// ⛔ THE SEPARATOR IS LOAD-BEARING. A sibling directory whose name merely
// STARTS with the target's — `<target>-evil` for `<target>` — is outside the
// clone folder, and a containment check written as a string prefix accepts it.
// Nothing else in this file pins that: swapping filepath.Rel for a bare
// strings.HasPrefix passes every other case here (round 1, R1-1).
func TestClone_RejectsSiblingDirectoryWithTheTargetsNameAsPrefix(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "clone")
	sibling := filepath.Join(parent, "clone-evil") // NOT inside target
	if err := os.MkdirAll(sibling, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sibling, filepath.Join(target, "link")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	site := &fullSite{
		wellKnown: `{"version":"2.0","public_key":"k"}`,
		fallback:  "pwned",
		index: []string{
			`{"type":"post","path":"link/evil.md","published":"2026-01-01T00:00:00Z"}`,
		},
	}
	ts := site.serve(t)
	defer ts.Close()

	res, err := Clone(ts.URL, target, CloneOptions{FullClone: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(sibling, "evil.md")); err == nil {
		t.Error("wrote into a SIBLING directory whose name starts with the target's — the containment check compares strings, not paths")
	}
	if len(res.RejectedPaths) != 1 {
		t.Errorf("RejectedPaths = %v, want the sibling path reported", res.RejectedPaths)
	}
}
