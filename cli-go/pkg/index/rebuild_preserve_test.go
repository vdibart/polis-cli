package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedSiteWithPostAndComment writes one post markdown file and an index.jsonl
// holding one post entry and one comment entry, in the shape `publish` and
// `comment` actually write today (path / current_version).
func seedSiteWithPostAndComment(t *testing.T) (dataDir, commentLine string) {
	t.Helper()

	dataDir = t.TempDir()
	coreDir := filepath.Join(dataDir, "content", "pub.polis.core")
	postDir := filepath.Join(coreDir, "post", "20260101")
	commentDir := filepath.Join(coreDir, "comment", "20260102")
	for _, d := range []string{postDir, commentDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	post := `---
title: Hello World
published: 2026-01-01T00:00:00Z
current-version: sha256:aaaa
---

Hello World content.
`
	if err := os.WriteFile(filepath.Join(postDir, "hello.md"), []byte(post), 0644); err != nil {
		t.Fatalf("write post: %v", err)
	}

	comment := `---
type: comment
title: A reply
published: 2026-01-02T00:00:00Z
current-version: sha256:bbbb
in-reply-to:
  url: https://alice.polis.pub/posts/20260101/hello.html
  version: sha256:aaaa
---

Nice post.
`
	if err := os.WriteFile(filepath.Join(commentDir, "reply.md"), []byte(comment), 0644); err != nil {
		t.Fatalf("write comment: %v", err)
	}

	postLine := `{"type":"post","path":"content/pub.polis.core/post/20260101/hello.md","title":"Hello World","published":"2026-01-01T00:00:00Z","current_version":"sha256:aaaa"}`
	commentLine = `{"type":"comment","path":"content/pub.polis.core/comment/20260102/reply.md","title":"A reply","published":"2026-01-02T00:00:00Z","current_version":"sha256:bbbb","in_reply_to":{"url":"https://alice.polis.pub/posts/20260101/hello.html","version":"sha256:aaaa"}}`

	if err := os.WriteFile(filepath.Join(coreDir, "index.jsonl"),
		[]byte(postLine+"\n"+commentLine+"\n"), 0644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	return dataDir, commentLine
}

func readIndexLines(t *testing.T, dataDir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dataDir, "content", "pub.polis.core", "index.jsonl"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var out []string
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// R24-9. A regenerator that knows ONE source must not own the WHOLE file.
// `--posts` walks post/ only; comments reach index.jsonl by a different route
// (comment.MoveComment appends on blessing), so a whole-file overwrite deletes
// every one of them.
func TestRebuildPosts_PreservesCommentEntriesByteIdentically(t *testing.T) {
	dataDir, commentLine := seedSiteWithPostAndComment(t)

	if _, err := RebuildIndex(dataDir, RebuildOptions{Posts: true}); err != nil {
		t.Fatalf("rebuild --posts: %v", err)
	}

	lines := readIndexLines(t, dataDir)
	for _, l := range lines {
		if l == commentLine {
			return
		}
	}
	t.Fatalf("comment entry did not survive `rebuild --posts` byte-identically.\nwant line: %s\ngot index:\n%s",
		commentLine, strings.Join(lines, "\n"))
}

// D1 — one entry schema, and it is metadata.PublicIndexEntry: path /
// current_version. Tailor's F6 content-integrity check requires non-empty
// type/path/published/current_version on every line, so an index written in the
// url/hash shape fails the site's own integrity check on every line.
func TestRebuildPosts_WritesCanonicalEntrySchema(t *testing.T) {
	dataDir, _ := seedSiteWithPostAndComment(t)

	if _, err := RebuildIndex(dataDir, RebuildOptions{Posts: true}); err != nil {
		t.Fatalf("rebuild --posts: %v", err)
	}

	for _, l := range readIndexLines(t, dataDir) {
		var entry map[string]any
		if err := json.Unmarshal([]byte(l), &entry); err != nil {
			t.Fatalf("index line is not JSON: %s", l)
		}
		for _, field := range []string{"type", "path", "published", "current_version"} {
			if s, _ := entry[field].(string); s == "" {
				t.Errorf("F6: line missing required field %q: %s", field, l)
			}
		}
		if _, bad := entry["url"]; bad {
			t.Errorf("entry carries the retired \"url\" field: %s", l)
		}
		if _, bad := entry["hash"]; bad {
			t.Errorf("entry carries the retired \"hash\" field: %s", l)
		}
	}
}

// D8 — index.jsonl is public content served over HTTP. 0600 is right for
// .polis/ state and wrong here: a self-hosted split-user nginx setup would 403
// the site's own index.
func TestRebuildPosts_IndexIsPubliclyReadable(t *testing.T) {
	dataDir, _ := seedSiteWithPostAndComment(t)

	if _, err := RebuildIndex(dataDir, RebuildOptions{Posts: true}); err != nil {
		t.Fatalf("rebuild --posts: %v", err)
	}

	info, err := os.Stat(filepath.Join(dataDir, "content", "pub.polis.core", "index.jsonl"))
	if err != nil {
		t.Fatalf("stat index: %v", err)
	}
	if got := info.Mode().Perm(); got != 0644 {
		t.Errorf("index.jsonl mode = %04o, want 0644 (public served content)", got)
	}
}
