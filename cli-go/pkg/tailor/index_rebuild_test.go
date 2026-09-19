package tailor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedSiteWithBlessedComment writes a site whose index.jsonl carries both a
// post and a blessed comment, in the shape publish and comment actually write.
func seedSiteWithBlessedComment(t *testing.T) (siteDir, commentLine string) {
	t.Helper()

	siteDir = t.TempDir()
	core := filepath.Join(siteDir, "content", "pub.polis.core")
	postDir := filepath.Join(core, "post", "20260101")
	commentDir := filepath.Join(core, "comment", "20260102")
	for _, d := range []string{postDir, commentDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	if err := os.WriteFile(filepath.Join(postDir, "hello.md"), []byte(`---
title: Hello World
published: 2026-01-01T00:00:00Z
current-version: sha256:aaaa
---

Hello.
`), 0644); err != nil {
		t.Fatalf("write post: %v", err)
	}

	if err := os.WriteFile(filepath.Join(commentDir, "reply.md"), []byte(`---
type: comment
title: A reply
published: 2026-01-02T00:00:00Z
current-version: sha256:bbbb
in-reply-to:
  url: https://alice.polis.pub/posts/20260101/hello.html
  version: sha256:aaaa
---

Nice.
`), 0644); err != nil {
		t.Fatalf("write comment: %v", err)
	}

	postLine := `{"type":"post","path":"content/pub.polis.core/post/20260101/hello.md","title":"Hello World","published":"2026-01-01T00:00:00Z","current_version":"sha256:aaaa"}`
	commentLine = `{"type":"comment","path":"content/pub.polis.core/comment/20260102/reply.md","title":"A reply","published":"2026-01-02T00:00:00Z","current_version":"sha256:bbbb","in_reply_to":{"url":"https://alice.polis.pub/posts/20260101/hello.html","version":"sha256:aaaa"}}`

	if err := os.WriteFile(filepath.Join(core, "index.jsonl"),
		[]byte(postLine+"\n"+commentLine+"\n"), 0644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	return siteDir, commentLine
}

// R24-9's second half. Tailor carried its own posts-only rebuilder and compared
// whole-file content against it, so a site with blessed comments mismatched
// FOREVER — "Content index needs rebuild" on every cycle, permanently.
func TestCheckIndexRebuild_PassesOnASiteWithBlessedComments(t *testing.T) {
	siteDir, _ := seedSiteWithBlessedComment(t)

	result := checkIndexRebuild(&runContext{siteDir: siteDir, dryRun: true})
	if result.Status != StatusPass {
		t.Fatalf("status = %v, want pass; message = %q", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "up to date") {
		t.Errorf("message = %q, want the up-to-date message", result.Message)
	}
}

// Delegation means Tailor cannot truncate what it does not know about.
func TestCheckIndexRebuild_ApplyPreservesCommentEntries(t *testing.T) {
	siteDir, commentLine := seedSiteWithBlessedComment(t)

	// Force a rebuild by adding a post the index does not mention.
	extra := filepath.Join(siteDir, "content", "pub.polis.core", "post", "20260103")
	os.MkdirAll(extra, 0755)
	os.WriteFile(filepath.Join(extra, "second.md"), []byte(`---
title: Second
published: 2026-01-03T00:00:00Z
current-version: sha256:cccc
---

Second.
`), 0644)

	result := checkIndexRebuild(&runContext{siteDir: siteDir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("status = %v, want fail (a rebuild happened)", result.Status)
	}

	data, err := os.ReadFile(filepath.Join(siteDir, "content", "pub.polis.core", "index.jsonl"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if !strings.Contains(string(data), commentLine) {
		t.Errorf("comment entry did not survive Tailor's rebuild byte-identically:\n%s", data)
	}
	if !strings.Contains(string(data), `"path":"content/pub.polis.core/post/20260103/second.md"`) {
		t.Errorf("the new post was not indexed:\n%s", data)
	}
}

// The Action payloads Patrol baselines read must not move.
func TestCheckIndexRebuild_ActionPayloadsUnchanged(t *testing.T) {
	siteDir, _ := seedSiteWithBlessedComment(t)
	os.Remove(filepath.Join(siteDir, "content", "pub.polis.core", "index.jsonl"))

	result := checkIndexRebuild(&runContext{siteDir: siteDir, dryRun: true})
	if len(result.Actions) != 1 {
		t.Fatalf("actions = %+v, want exactly one", result.Actions)
	}
	got := result.Actions[0]
	if got.Op != "create" || got.Path != "content/pub.polis.core/index.jsonl" {
		t.Errorf("action = %+v, want {create content/pub.polis.core/index.jsonl}", got)
	}
	if !strings.HasPrefix(result.Message, "Content index needs rebuild (") {
		t.Errorf("message = %q, want the unchanged needs-rebuild shape", result.Message)
	}
}

// Done when #3: a rebuilt index passes the site's own F6 content-integrity
// check. Before this epic it failed on every line — the rebuild emitted
// url/hash and F6 requires type/path/published/current_version.
func TestRebuiltIndexPassesF6(t *testing.T) {
	siteDir, _ := seedSiteWithBlessedComment(t)
	os.Remove(filepath.Join(siteDir, "content", "pub.polis.core", "index.jsonl"))

	if r := checkIndexRebuild(&runContext{siteDir: siteDir, dryRun: false}); r.Status != StatusFail {
		t.Fatalf("rebuild did not run: %+v", r)
	}

	result := checkIndexEntries(&runContext{siteDir: siteDir, dryRun: true})
	if result.Status != StatusPass {
		t.Fatalf("F6 on a freshly rebuilt index: status = %v, message = %q", result.Status, result.Message)
	}
}
