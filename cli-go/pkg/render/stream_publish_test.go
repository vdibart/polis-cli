package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/sitemap"
)

// TestPublishStream_BoundedWriteCount asserts that publishing into a corpus of ≥10
// posts touches only the affected post + cascade neighbors + index — never the
// entire corpus. This is the central design goal of step 3.a (vs. 2.c's
// brute-force renderStreamAll).
func TestPublishStream_BoundedWriteCount(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)

	// Prime the corpus with 10 posts on consecutive dates, all rendered.
	for i := 0; i < 10; i++ {
		date := dateForOffset(i)
		writeStreamPost(t, tempDir, date, slugForOffset(i),
			titleForOffset(i), bodyForOffset(i))
	}
	primeStreamRenderer(t, tempDir).RenderAll(true)

	// Snapshot mtimes of every rendered file so we can detect untouched ones.
	snapshot := mtimeSnapshot(t, tempDir)

	// Sleep long enough for filesystem mtime resolution to register a change
	// on rewrite. (Linux ext4 typically has nanosecond precision but tmpfs may
	// fall back to coarser; 50ms is conservative.)
	time.Sleep(50 * time.Millisecond)

	// Publish an 11th post; its source path mirrors what publish.PublishPost
	// would produce.
	newDate := dateForOffset(10)
	newSlug := slugForOffset(10)
	writeStreamPost(t, tempDir, newDate, newSlug,
		titleForOffset(10), bodyForOffset(10))

	r := primeStreamRenderer(t, tempDir)
	newPostPath := "content/pub.polis.core/post/" + newDate + "/" + newSlug + ".md"
	if err := r.PublishStream(newPostPath); err != nil {
		t.Fatalf("PublishStream: %v", err)
	}

	touched := touchedFiles(t, tempDir, snapshot)

	// Expected: the new post itself + the index + cascade neighbors whose
	// sibling rails include the new post.
	//
	// Post-2026-04-29 (bidirectional siblings, default 1 newer above):
	// pickStreamSiblings's above rail picks the IMMEDIATELY-newer post (focus
	// idx-1), padded with nearest-newer when the older rail can't fill
	// the budget. The new post at position 0 only appears in position 1's
	// above rail (as posts[0]). Positions 2..10's above rails pick their
	// own immediately-newer (posts[i-1]), and their below rails fill from
	// older. Pad-back at the corpus end (positions where older < budget)
	// pads from NEAREST-newer first, so it walks toward the focus, not
	// toward posts[0]. Result: the new post lands in only position 1's
	// rail.
	//
	// Total touched: 1 (new post) + 1 (cascade: position 1) + 1 (index) = 3.
	expectedTouched := 3
	if len(touched) != expectedTouched {
		t.Errorf("touched %d files, want %d. Touched: %v", len(touched), expectedTouched, touched)
	}

	// The new post's HTML must be among the touched.
	expectNewPath := filepath.Join(tempDir, "posts", newDate, newSlug+".html")
	if !containsString(touched, expectNewPath) {
		t.Errorf("new post HTML not touched: expected %s in %v", expectNewPath, touched)
	}
	// The index must be among the touched.
	if !containsString(touched, filepath.Join(tempDir, "index.html")) {
		t.Errorf("index.html not touched: %v", touched)
	}
}

// TestPublishStream_RepublishCascade asserts that republishing a mid-corpus post
// re-renders the post + every neighbor whose sibling rail references it, but
// NOT unrelated posts.
func TestPublishStream_RepublishCascade(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)

	for i := 0; i < 10; i++ {
		date := dateForOffset(i)
		writeStreamPost(t, tempDir, date, slugForOffset(i),
			titleForOffset(i), bodyForOffset(i))
	}
	primeStreamRenderer(t, tempDir).RenderAll(true)

	snapshot := mtimeSnapshot(t, tempDir)
	time.Sleep(50 * time.Millisecond)

	// Republish the *oldest* post (highest index in newest-first order).
	// Its position doesn't change; cascade catches the posts that show it as
	// a sibling. With pickStreamSiblings's pad-back rule, the oldest post is in
	// the pad-back tail of every short-older post (positions 6..9 in 10-post
	// corpus). So it appears as a sibling of itself's-not-included posts.
	// Specifically: positions n-4..n-1 (6..9) have pad-back; position 9 IS
	// the oldest, so positions 6, 7, 8 may include it; pos 0..5 do not.
	oldestDate := dateForOffset(0) // earliest date = oldest post in corpus
	oldestSlug := slugForOffset(0)
	oldestSourcePath := "content/pub.polis.core/post/" + oldestDate + "/" + oldestSlug + ".md"

	// Modify the source so re-render isn't a no-op (and so RenderV4File picks
	// up new mtime / content).
	mdPath := filepath.Join(tempDir, oldestSourcePath)
	original, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read oldest md: %v", err)
	}
	if err := os.WriteFile(mdPath, append(original, []byte("\n\nEdited content for republish test.\n")...), 0644); err != nil {
		t.Fatalf("rewrite oldest md: %v", err)
	}

	r := primeStreamRenderer(t, tempDir)
	if err := r.PublishStream(oldestSourcePath); err != nil {
		t.Fatalf("PublishStream (republish path): %v", err)
	}

	touched := touchedFiles(t, tempDir, snapshot)

	// Expected touched: oldest post itself + neighbors whose sibling rail
	// includes it + index. In a 10-post corpus, the oldest post is at
	// position 9 (newest-first indexing). With maxStreamSiblings=6 (post-
	// 2026-04-29 bump for tall-monitor /index.html fix), the cascade is:
	//   i=4: above=[3], below=[5,6,7,8,9] (5). Total 6. Includes 9 ✓
	//   i=5: above=[4], below=[6,7,8,9] (4). Total 5, pad above with
	//        idx=3 → above=[3,4]. Total 6. Includes 9 ✓
	//   i=6: above=[5], below=[7,8,9] (3). Pad: idx=4,3 →
	//        above=[3,4,5]. Total 6. Includes 9 ✓
	//   i=7: above=[6], below=[8,9] (2). Pad: 5,4,3 →
	//        above=[3,4,5,6]. Total 6. Includes 9 ✓
	//   i=8: above=[7], below=[9] (1). Pad: 6,5,4,3 →
	//        above=[3,4,5,6,7]. Total 6. Includes 9 ✓
	//   i=9: focus itself; doesn't appear in its own siblings.
	//   i=0..3: older rail has 5+ entries below the focus (within
	//        budget=6 minus 1 for above), padding doesn't kick in,
	//        pos 9 not in window.
	// So cascade = {4, 5, 6, 7, 8} (5 posts). Plus oldest itself (i=9).
	// Plus index. Total touched: 7.
	//
	// Pre-2026-04-29 with maxStreamSiblings=4 the cascade was {6, 7, 8}
	// (3 posts) → 5 total touched. The bump to 6 widens the rail, so
	// more positions reference any given oldest entry.
	expectedTouched := 7
	if len(touched) != expectedTouched {
		t.Errorf("republish touched %d files, want %d. Touched: %v", len(touched), expectedTouched, touched)
	}

	// Assert the oldest's own HTML was re-rendered.
	oldestHTML := filepath.Join(tempDir, "posts", oldestDate, oldestSlug+".html")
	if !containsString(touched, oldestHTML) {
		t.Errorf("oldest post HTML not touched: %v", touched)
	}

	// Assert an unrelated mid-corpus post was NOT touched (e.g. position 3 of
	// 10 — has 4+ older siblings, no pad-back, doesn't reach the oldest).
	unrelatedDate := dateForOffset(6) // pos 3 in newest-first (10-1-6 = 3)
	unrelatedSlug := slugForOffset(6)
	unrelatedHTML := filepath.Join(tempDir, "posts", unrelatedDate, unrelatedSlug+".html")
	if containsString(touched, unrelatedHTML) {
		t.Errorf("unrelated post should NOT be touched: %s in %v", unrelatedHTML, touched)
	}
}

// TestPublishStream_SmallCorpusFullRecascade asserts the documented edge case:
// publishing into a small corpus (n ≤ maxStreamSiblings+1) re-renders ALL posts
// because pickStreamSiblings's pad-back rearranges everyone's sibling window.
func TestPublishStream_SmallCorpusFullRecascade(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)

	// Prime 4 posts. After a 5th publish, n=5 — every existing post has
	// olderCount < maxStreamSiblings (=6 post-2026-04-29) so the pad-above
	// loop runs for everyone, walking toward focus and eventually
	// reaching position 0 (the new post). All 5 posts re-render.
	for i := 0; i < 4; i++ {
		date := dateForOffset(i)
		writeStreamPost(t, tempDir, date, slugForOffset(i),
			titleForOffset(i), bodyForOffset(i))
	}
	primeStreamRenderer(t, tempDir).RenderAll(true)

	snapshot := mtimeSnapshot(t, tempDir)
	time.Sleep(50 * time.Millisecond)

	// Publish a 5th post.
	newDate := dateForOffset(4)
	newSlug := slugForOffset(4)
	writeStreamPost(t, tempDir, newDate, newSlug,
		titleForOffset(4), bodyForOffset(4))

	r := primeStreamRenderer(t, tempDir)
	newPostPath := "content/pub.polis.core/post/" + newDate + "/" + newSlug + ".md"
	if err := r.PublishStream(newPostPath); err != nil {
		t.Fatalf("PublishStream: %v", err)
	}

	touched := touchedFiles(t, tempDir, snapshot)

	// 5 posts (all of them) + index = 6 touched files.
	wantTouched := 6
	if len(touched) != wantTouched {
		t.Errorf("small-corpus publish touched %d files, want %d. Touched: %v", len(touched), wantTouched, touched)
	}
}

// TestUnpublishStream_DeletesAndCascades asserts that unpublishing removes the
// post's HTML and re-renders the pad-back-zone neighbors that may have shown
// it as a sibling.
func TestUnpublishStream_DeletesAndCascades(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)

	for i := 0; i < 10; i++ {
		date := dateForOffset(i)
		writeStreamPost(t, tempDir, date, slugForOffset(i),
			titleForOffset(i), bodyForOffset(i))
	}
	primeStreamRenderer(t, tempDir).RenderAll(true)

	// Unpublish the *newest* post (position 0 in newest-first). Caller
	// contract: index entry already removed. Mirror that here.
	newestDate := dateForOffset(9)
	newestSlug := slugForOffset(9)
	newestSourcePath := "content/pub.polis.core/post/" + newestDate + "/" + newestSlug + ".md"
	removeIndexEntry(t, tempDir, newestSourcePath)
	// Also delete the source markdown, mirroring what unpublish.go does.
	if err := os.Remove(filepath.Join(tempDir, newestSourcePath)); err != nil {
		t.Fatalf("delete newest md: %v", err)
	}

	snapshot := mtimeSnapshot(t, tempDir)
	time.Sleep(50 * time.Millisecond)

	r := primeStreamRenderer(t, tempDir)
	if err := r.UnpublishStream(newestSourcePath); err != nil {
		t.Fatalf("UnpublishStream: %v", err)
	}

	// The unpublished post's HTML must be gone.
	deletedHTML := filepath.Join(tempDir, "posts", newestDate, newestSlug+".html")
	if _, err := os.Stat(deletedHTML); !os.IsNotExist(err) {
		t.Errorf("unpublished post HTML should be deleted, but stat err = %v", err)
	}

	// Touched: pad-back-zone posts (positions where olderCount <
	// maxStreamSiblings=6) + index. Remaining corpus has 9 posts, so
	// positions where (n-1-i) < 6 are i ∈ {3,4,5,6,7,8} = 6 posts.
	// Plus index = 7 files. (Pre-2026-04-29 with maxStreamSiblings=4 it
	// was 4 positions {5,6,7,8} + index = 5; the bump widens the rail
	// and so the unpublish cascade.)
	touched := touchedFiles(t, tempDir, snapshot)
	wantTouched := 7
	if len(touched) != wantTouched {
		t.Errorf("unpublish touched %d files, want %d. Touched: %v", len(touched), wantTouched, touched)
	}
}

// TestUnpublishStream_LastPost asserts that unpublishing the only post in a v4
// tenant produces the empty-state index and deletes the post HTML.
func TestUnpublishStream_LastPost(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)

	writeStreamPost(t, tempDir, "20260315", "solo", "Solo", "Only post body.")
	primeStreamRenderer(t, tempDir).RenderAll(true)

	soloSourcePath := "content/pub.polis.core/post/20260315/solo.md"
	removeIndexEntry(t, tempDir, soloSourcePath)
	if err := os.Remove(filepath.Join(tempDir, soloSourcePath)); err != nil {
		t.Fatalf("delete solo md: %v", err)
	}

	r := primeStreamRenderer(t, tempDir)
	if err := r.UnpublishStream(soloSourcePath); err != nil {
		t.Fatalf("UnpublishStream last post: %v", err)
	}

	// Solo post HTML deleted.
	if _, err := os.Stat(filepath.Join(tempDir, "posts", "20260315", "solo.html")); !os.IsNotExist(err) {
		t.Error("solo post HTML should be deleted")
	}

	// Index now shows the welcoming empty/getting-started state.
	indexHTML := mustReadFile(t, filepath.Join(tempDir, "index.html"))
	assertContains(t, indexHTML, "stream-getting-started", "empty state after last unpublish")
}

// TestPublishStream_SitemapIncrementalAndRemove asserts the sitemap evolves with
// publishes (insert), republishes (LastMod updates without duplicating), and
// unpublishes (entry removed). End-to-end coverage of step-03/3.c integration.
func TestPublishStream_SitemapIncrementalAndRemove(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)

	// Seed two posts via full RenderAll so a baseline sitemap exists. Sleep
	// briefly between writes so mtimes are distinct even at coarse FS
	// granularity (some filesystems round mtimes to seconds).
	writeStreamPost(t, tempDir, "20260101", "alpha", "Alpha", "First.")
	time.Sleep(1100 * time.Millisecond)
	writeStreamPost(t, tempDir, "20260102", "bravo", "Bravo", "Second.")
	primeStreamRenderer(t, tempDir).RenderAll(true)

	// Baseline: full rebuild produced a sitemap with 2 entries containing
	// alpha + bravo. Order is mtime-driven; bravo's source was written after
	// alpha's so bravo SHOULD be newer, but assert membership rather than
	// strict ordering — robust against mtime ties.
	smPath := filepath.Join(tempDir, "sitemap.xml")
	body := mustReadFile(t, smPath)
	entries := mustParseSitemap(t, []byte(body))
	if len(entries) != 2 {
		t.Fatalf("baseline sitemap has %d entries, want 2", len(entries))
	}
	if !sitemapHasURL(entries, "/posts/20260101/alpha.html") || !sitemapHasURL(entries, "/posts/20260102/bravo.html") {
		t.Fatalf("baseline sitemap missing alpha or bravo: %v", urlList(entries))
	}

	// Publish a third post. Sleep to ensure mtime advances.
	time.Sleep(1100 * time.Millisecond)
	writeStreamPost(t, tempDir, "20260103", "charlie", "Charlie", "Third.")
	r := primeStreamRenderer(t, tempDir)
	if err := r.PublishStream("content/pub.polis.core/post/20260103/charlie.md"); err != nil {
		t.Fatalf("PublishStream: %v", err)
	}
	body = mustReadFile(t, smPath)
	entries = mustParseSitemap(t, []byte(body))
	if len(entries) != 3 {
		t.Fatalf("after publish, sitemap has %d entries, want 3", len(entries))
	}
	if !sitemapHasURL(entries, "/posts/20260103/charlie.html") {
		t.Errorf("after publish, charlie missing from sitemap: %v", urlList(entries))
	}
	// charlie has the newest mtime so it must sort first under Build's
	// newest-first rule.
	if !strings.HasSuffix(entries[0].URL, "/posts/20260103/charlie.html") {
		t.Errorf("after publish, entry[0] = %q, want charlie (newest-mtime first)", entries[0].URL)
	}

	// Republish (rewrite source markdown to bump mtime). Sitemap should still
	// have 3 entries; Bravo's LastMod should advance.
	bravoMD := filepath.Join(tempDir, "content", "pub.polis.core", "post", "20260102", "bravo.md")
	bravoBefore, _ := os.ReadFile(bravoMD)
	var bravoLastModBefore time.Time
	for _, e := range entries {
		if strings.HasSuffix(e.URL, "/posts/20260102/bravo.html") {
			bravoLastModBefore = e.LastMod
		}
	}
	time.Sleep(1100 * time.Millisecond)
	if err := os.WriteFile(bravoMD, append(bravoBefore, []byte("\n\nrepublish edit\n")...), 0644); err != nil {
		t.Fatalf("rewrite bravo: %v", err)
	}
	r = primeStreamRenderer(t, tempDir)
	if err := r.PublishStream("content/pub.polis.core/post/20260102/bravo.md"); err != nil {
		t.Fatalf("PublishStream republish: %v", err)
	}
	body = mustReadFile(t, smPath)
	entries = mustParseSitemap(t, []byte(body))
	if len(entries) != 3 {
		t.Errorf("after republish, sitemap has %d entries (must NOT duplicate), want 3", len(entries))
	}
	// Find Bravo by URL suffix; confirm its LastMod advanced.
	for _, e := range entries {
		if strings.HasSuffix(e.URL, "/posts/20260102/bravo.html") {
			if !e.LastMod.After(bravoLastModBefore) {
				t.Errorf("bravo LastMod did not advance on republish: was %v, now %v", bravoLastModBefore, e.LastMod)
			}
		}
	}

	// Unpublish Alpha → sitemap drops to 2 entries; Alpha URL gone.
	alphaPath := "content/pub.polis.core/post/20260101/alpha.md"
	removeIndexEntry(t, tempDir, alphaPath)
	if err := os.Remove(filepath.Join(tempDir, alphaPath)); err != nil {
		t.Fatalf("delete alpha md: %v", err)
	}
	r = primeStreamRenderer(t, tempDir)
	if err := r.UnpublishStream(alphaPath); err != nil {
		t.Fatalf("UnpublishStream: %v", err)
	}
	body = mustReadFile(t, smPath)
	entries = mustParseSitemap(t, []byte(body))
	if len(entries) != 2 {
		t.Errorf("after unpublish, sitemap has %d entries, want 2", len(entries))
	}
	for _, e := range entries {
		if strings.HasSuffix(e.URL, "/posts/20260101/alpha.html") {
			t.Errorf("alpha URL should have been removed; still present: %q", e.URL)
		}
	}
}

func mustParseSitemap(t *testing.T, body []byte) []sitemap.Entry {
	t.Helper()
	out, err := sitemap.Parse(body)
	if err != nil {
		t.Fatalf("parse sitemap: %v", err)
	}
	return out
}

func sitemapHasURL(entries []sitemap.Entry, suffix string) bool {
	for _, e := range entries {
		if strings.HasSuffix(e.URL, suffix) {
			return true
		}
	}
	return false
}

func urlList(entries []sitemap.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.URL
	}
	return out
}

// --- helpers ---

func primeStreamRenderer(t *testing.T, dir string) *PageRenderer {
	t.Helper()
	r, err := NewPageRenderer(PageConfig{
		DataDir:        dir,
		BaseURL:        "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post",
		PostsMountDir:  "posts",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	return r
}

// dateForOffset emits dates 20260101, 20260102, ... — i=0 is OLDEST.
func dateForOffset(i int) string {
	day := 1 + i
	return "202601" + twoDigit(day)
}

func twoDigit(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	tens := n / 10
	ones := n % 10
	return string(rune('0'+tens)) + string(rune('0'+ones))
}

func slugForOffset(i int) string {
	return "post-" + twoDigit(i)
}

func titleForOffset(i int) string {
	return "Post " + twoDigit(i)
}

func bodyForOffset(i int) string {
	return "Body for post " + twoDigit(i) + ". Some content for excerpt extraction."
}

// mtimeSnapshot records mtimes for every .html file under dir.
func mtimeSnapshot(t *testing.T, dir string) map[string]time.Time {
	t.Helper()
	out := map[string]time.Time{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.HasSuffix(path, ".html") {
			out[path] = info.ModTime()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot walk: %v", err)
	}
	return out
}

// touchedFiles returns the .html file paths whose mtime is newer than the
// snapshot (or files that didn't exist in the snapshot).
func touchedFiles(t *testing.T, dir string, snapshot map[string]time.Time) []string {
	t.Helper()
	var touched []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() || !strings.HasSuffix(path, ".html") {
			return nil
		}
		prev, existed := snapshot[path]
		if !existed || info.ModTime().After(prev) {
			touched = append(touched, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("touched walk: %v", err)
	}
	return touched
}

func containsString(s []string, want string) bool {
	for _, x := range s {
		if x == want {
			return true
		}
	}
	return false
}

// removeIndexEntry strips a post entry from index.jsonl, mirroring what
// metadata.RemoveIndexEntry does (used to set up the post-unpublish state for
// UnpublishStream tests).
func removeIndexEntry(t *testing.T, dataDir, postPath string) {
	t.Helper()
	indexPath := filepath.Join(dataDir, "content", "pub.polis.core", "index.jsonl")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var keep []string
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		if strings.Contains(line, `"path":"`+postPath+`"`) {
			continue
		}
		keep = append(keep, line)
	}
	out := strings.Join(keep, "\n")
	if out != "" {
		out += "\n"
	}
	if err := os.WriteFile(indexPath, []byte(out), 0644); err != nil {
		t.Fatalf("rewrite index: %v", err)
	}
}
