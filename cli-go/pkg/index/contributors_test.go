package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// seedFourTypes writes one file of each contributing content type.
func seedFourTypes(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	core := filepath.Join(dataDir, "content", "pub.polis.core")

	write(t, filepath.Join(core, "post", "20260101", "hello.md"), `---
title: Hello
published: 2026-01-01T00:00:00Z
current-version: sha256:aaaa
---

Hello.
`)
	write(t, filepath.Join(core, "comment", "20260102", "reply.md"), `---
type: comment
title: A reply
published: 2026-01-02T00:00:00Z
current-version: sha256:bbbb
in-reply-to:
  url: https://alice.polis.pub/posts/20260101/hello.html
  version: sha256:aaaa
---

Nice.
`)
	write(t, filepath.Join(core, "tag", "reading.json"),
		`{"tag":"reading","targets":[],"created":"2026-01-03T00:00:00Z","updated":"2026-01-03T00:00:00Z","current_version":"sha256:cccc","signature":"sig"}`)
	write(t, filepath.Join(core, "attestation", "20260104T000000Z-abcd.json"),
		`{"type":"pub.polis.attestation","issuer":"https://alice.polis.pub","predicate":"pub.polis.attestation.same-as","subject":{"type":"identity","id":"https://alice.example"},"asserted":"2026-01-04T00:00:00Z","generator":"polis-cli-go/test","current_version":"sha256:dddd","signature":"sig"}`)

	return dataDir
}

func indexEntries(t *testing.T, dataDir string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dataDir, "content", "pub.polis.core", "index.jsonl"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var out []map[string]any
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e map[string]any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("index line is not JSON: %s", line)
		}
		out = append(out, e)
	}
	return out
}

// D4 — tag and attestation are contributors to the same file. Closes R24-6's
// remaining half: three signed content types were published with nothing to
// tell a third party they exist.
func TestRebuildAll_IndexesEveryContentType(t *testing.T) {
	dataDir := seedFourTypes(t)

	result, err := RebuildContentIndex(dataDir, nil)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	for _, typ := range []string{EntryTypePost, EntryTypeComment, EntryTypeTag, EntryTypeAttestation} {
		if result.Rebuilt[typ] != 1 {
			t.Errorf("Rebuilt[%q] = %d, want 1", typ, result.Rebuilt[typ])
		}
	}
	if result.Total != 4 {
		t.Errorf("Total = %d, want 4", result.Total)
	}

	// Every line must satisfy the site's own F6 integrity check.
	for _, e := range indexEntries(t, dataDir) {
		for _, field := range []string{"type", "path", "published", "current_version"} {
			if s, _ := e[field].(string); s == "" {
				t.Errorf("entry %v missing required field %q", e["path"], field)
			}
		}
		if cv, _ := e["current_version"].(string); !strings.HasPrefix(cv, "sha256:") {
			t.Errorf("entry %v current_version %q is not sha256:-prefixed", e["path"], cv)
		}
	}
}

// A partial rebuild owns ONLY its own type's lines.
func TestPartialRebuild_PreservesEveryOtherType(t *testing.T) {
	dataDir := seedFourTypes(t)
	if _, err := RebuildContentIndex(dataDir, nil); err != nil {
		t.Fatalf("seed rebuild: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(dataDir, "content", "pub.polis.core", "index.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	result, err := RebuildContentIndex(dataDir, []string{EntryTypePost})
	if err != nil {
		t.Fatalf("rebuild --posts: %v", err)
	}
	if result.Rebuilt[EntryTypePost] != 1 {
		t.Errorf("Rebuilt[post] = %d, want 1", result.Rebuilt[EntryTypePost])
	}
	for _, typ := range []string{EntryTypeComment, EntryTypeTag, EntryTypeAttestation} {
		if result.Preserved[typ] != 1 {
			t.Errorf("Preserved[%q] = %d, want 1", typ, result.Preserved[typ])
		}
	}
	if result.Changed {
		t.Errorf("a rebuild of an already-correct index reported Changed")
	}

	after, err := os.ReadFile(filepath.Join(dataDir, "content", "pub.polis.core", "index.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("partial rebuild changed the file:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// A line this build cannot read is kept, not dropped: the index is a
// projection of content we may not own, and discarding what we cannot parse is
// the destructive choice.
func TestPartialRebuild_KeepsUnreadableAndUnknownLines(t *testing.T) {
	dataDir := seedFourTypes(t)
	indexPath := filepath.Join(dataDir, "content", "pub.polis.core", "index.jsonl")

	unknown := `{"type":"com.example.recipe","path":"content/com.example/recipes/soup.json","published":"2026-01-05T00:00:00Z","current_version":"sha256:eeee"}`
	garbage := `not json at all`
	write(t, indexPath, unknown+"\n"+garbage+"\n")

	result, err := RebuildContentIndex(dataDir, []string{EntryTypePost})
	if err != nil {
		t.Fatalf("rebuild --posts: %v", err)
	}
	if result.Preserved["com.example.recipe"] != 1 {
		t.Errorf("Preserved[com.example.recipe] = %d, want 1", result.Preserved["com.example.recipe"])
	}
	if result.Preserved["(unreadable)"] != 1 {
		t.Errorf("Preserved[(unreadable)] = %d, want 1", result.Preserved["(unreadable)"])
	}

	data, _ := os.ReadFile(indexPath)
	for _, want := range []string{unknown, garbage} {
		if !strings.Contains(string(data), want) {
			t.Errorf("line dropped: %s\ngot:\n%s", want, data)
		}
	}
}

// D7 — `dir` is user-configurable, so the walk follows the bundle rather than
// assuming content/pub.polis.core/post.
func TestRebuild_ResolvesContentDirsFromTheBundlePointer(t *testing.T) {
	dataDir := t.TempDir()

	write(t, filepath.Join(dataDir, ".well-known", "polis"),
		`{"version":"2.0","public_key":"k","bundles":{"pub.polis.core":{"path":"content/pub.polis.core/bundle.json"}}}`)
	write(t, filepath.Join(dataDir, "content", "pub.polis.core", "bundle.json"), `{
  "name": "pub.polis.core",
  "version": "1.0.0",
  "handler": {"type": "builtin"},
  "types": {
    "pub.polis.post": {"dir": "writing", "mount": "/posts"},
    "pub.polis.comment": {"dir": "replies", "mount": "/comments"}
  }
}`)
	write(t, filepath.Join(dataDir, "content", "pub.polis.core", "writing", "20260101", "hello.md"), `---
title: Hello
published: 2026-01-01T00:00:00Z
current-version: sha256:aaaa
---

Hello.
`)

	result, err := RebuildContentIndex(dataDir, []string{EntryTypePost})
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if result.Rebuilt[EntryTypePost] != 1 {
		t.Fatalf("Rebuilt[post] = %d, want 1 — the walk did not follow the bundle", result.Rebuilt[EntryTypePost])
	}
	entries := indexEntries(t, dataDir)
	if got, _ := entries[0]["path"].(string); got != "content/pub.polis.core/writing/20260101/hello.md" {
		t.Errorf("path = %q, want the declared directory", got)
	}
}

// A file with no `published` cannot be ordered and fails F6, so it is skipped —
// but the skip is REPORTED rather than silent.
func TestRebuild_ReportsSkippedFiles(t *testing.T) {
	dataDir := t.TempDir()
	write(t, filepath.Join(dataDir, "content", "pub.polis.core", "post", "broken.md"),
		"---\ntitle: No date\n---\n\nbody\n")

	result, err := RebuildContentIndex(dataDir, []string{EntryTypePost})
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if result.Rebuilt[EntryTypePost] != 0 {
		t.Errorf("Rebuilt[post] = %d, want 0", result.Rebuilt[EntryTypePost])
	}
	if result.Skipped[EntryTypePost] != 1 {
		t.Errorf("Skipped[post] = %d, want 1 — a dropped file must not be silent", result.Skipped[EntryTypePost])
	}
}

// The index's canonical order is ASCENDING by published: three consumers
// reverse the file to get newest-first. The pre-fix rebuild sorted descending.
func TestRebuild_OrdersAscendingByPublished(t *testing.T) {
	dataDir := seedFourTypes(t)
	if _, err := RebuildContentIndex(dataDir, nil); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	var published []string
	for _, e := range indexEntries(t, dataDir) {
		s, _ := e["published"].(string)
		published = append(published, s)
	}
	for i := 1; i < len(published); i++ {
		if published[i] < published[i-1] {
			t.Fatalf("index is not ascending by published: %v", published)
		}
	}
}

func TestParseInReplyTo(t *testing.T) {
	cases := []struct {
		name          string
		content       string
		wantURL, want string
	}{
		{
			name:    "nested block",
			content: "---\ntitle: t\nin-reply-to:\n  url: https://a.example/p.html\n  version: sha256:aa\n---\n\nbody\n",
			wantURL: "https://a.example/p.html", want: "sha256:aa",
		},
		{
			name:    "flat webapp form",
			content: "---\ntitle: t\nin_reply_to: https://b.example/p.html\n---\n\nbody\n",
			wantURL: "https://b.example/p.html",
		},
		{
			name:    "no reply at all",
			content: "---\ntitle: t\n---\n\nbody\n",
		},
		{
			// The trap the flat parser falls into: a `license:` block's
			// children are indented and must not be read as the reply target.
			name:    "licence block does not leak into the reply",
			content: "---\ntitle: t\nlicense:\n  v: pub.polis.license.v1\n  terms: https://a.example/license\n---\n\nbody\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseInReplyTo(tc.content)
			if tc.wantURL == "" {
				if got != nil {
					t.Fatalf("got %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("got nil, want url %q", tc.wantURL)
			}
			if got.URL != tc.wantURL || got.Version != tc.want {
				t.Errorf("got {%q %q}, want {%q %q}", got.URL, got.Version, tc.wantURL, tc.want)
			}
		})
	}
}
