package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
)

// The web app's Unpublish dialog promises a republish asks for blessing again.
// The draft it wrote kept no reply fields, so signing it failed with
// "in_reply_to is required" (close-out E2, found in the C12 browser walk).
func TestUnpublishedCommentDraftCanBeSignedAgain(t *testing.T) {
	s := newConfiguredServer(t)
	s.BaseURL = "https://owner.example"

	rel := filepath.Join("content", "pub.polis.core", "comment", "20260917", "a-comment.md")
	full := filepath.Join(s.DataDir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	published := `---
title: "Re: a-post"
type: comment
published: 2026-09-17T10:00:00Z
author: owner.example
generator: polis-cli-go/0.68.0
in-reply-to:
  url: https://someone.example/posts/20260901/a-post.md
  root-post: https://someone.example/posts/20260901/root.md
current-version: sha256:abc
version-history:
  - sha256:abc (2026-09-17T10:00:00Z)
signature: SIG
---

My reply.
`
	if err := os.WriteFile(full, []byte(published), 0644); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	s.handleUnpublish(w, httptest.NewRequest(http.MethodPost, "/api/unpublish",
		jsonBody(t, map[string]string{"path": rel})))
	if w.Code != http.StatusOK {
		t.Fatalf("unpublish = %d: %s", w.Code, w.Body.String())
	}

	draft, err := comment.LoadDraft(s.DataDir, "a-comment")
	if err != nil {
		t.Fatalf("LoadDraft: %v", err)
	}
	if draft.InReplyTo != "https://someone.example/posts/20260901/a-post.md" {
		t.Errorf("in_reply_to = %q", draft.InReplyTo)
	}
	if draft.RootPost != "https://someone.example/posts/20260901/root.md" {
		t.Errorf("root_post = %q — the thread root must survive", draft.RootPost)
	}
	if _, err := comment.SignComment(s.DataDir, draft, "owner.example", s.BaseURL, s.PrivateKey); err != nil {
		t.Fatalf("comment sign after unpublish: %v", err)
	}
}

// The published body keeps its heading and the frontmatter title is quoted, so
// the strip has to run against the UNQUOTED title. It did not, and the draft
// came back with the heading twice — once more per cycle (close-out R2-1).
func TestUnpublishedCommentDraftKeepsOneHeading(t *testing.T) {
	s := newConfiguredServer(t)
	s.BaseURL = "https://owner.example"

	rel := filepath.Join("content", "pub.polis.core", "comment", "20260917", "b-comment.md")
	full := filepath.Join(s.DataDir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	published := "---\ntitle: \"Re: a-post\"\ntype: comment\npublished: 2026-09-17T10:00:00Z\nauthor: owner.example\ngenerator: polis-cli-go/0.68.0\nin-reply-to:\n  url: https://someone.example/posts/20260901/a-post.md\n  root-post: https://someone.example/posts/20260901/a-post.md\ncurrent-version: sha256:abc\nversion-history:\n  - sha256:abc (2026-09-17T10:00:00Z)\nsignature: SIG\n---\n\n# Re: a-post\n\nMy reply.\n"
	if err := os.WriteFile(full, []byte(published), 0644); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	s.handleUnpublish(w, httptest.NewRequest(http.MethodPost, "/api/unpublish",
		jsonBody(t, map[string]string{"path": rel})))
	if w.Code != http.StatusOK {
		t.Fatalf("unpublish = %d: %s", w.Code, w.Body.String())
	}

	data, err := os.ReadFile(filepath.Join(s.DataDir, ".polis", "bundles", "pub.polis.core", "comments", "drafts", "b-comment.md"))
	if err != nil {
		t.Fatalf("read draft: %v", err)
	}
	body := string(data)
	if n := strings.Count(body, "# Re: a-post"); n != 1 {
		t.Errorf("draft has %d `# Re: a-post` headings, want 1:\n%s", n, body)
	}
	if strings.Contains(body, `# "Re: a-post"`) {
		t.Errorf("the YAML quoting became part of the heading:\n%s", body)
	}
}

// The web app's post branch, through the real publisher, over two cycles — the
// same experiment as the CLI's. A post titled `Foo: Bar` is stored YAML-quoted
// while its body heading is not, so an unpublish that re-prepended the raw
// value stacked a heading and re-escaped the title on every cycle
// (close-out R3-1).
func TestUnpublishedPostSurvivesTwoPublishUnpublishCycles(t *testing.T) {
	s := newConfiguredServer(t)
	s.BaseURL = "https://owner.example"
	t.Setenv("POLIS_BASE_URL", s.BaseURL)

	const title = "Foo: Bar"
	markdown := "# " + title + "\n\nHello world.\n"
	draftPath := filepath.Join(s.DataDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts", "foo-bar.md")

	for cycle := 1; cycle <= 2; cycle++ {
		result, err := publish.PublishPost(s.DataDir, markdown, "foo-bar", s.PrivateKey)
		if err != nil {
			t.Fatalf("cycle %d: PublishPost: %v", cycle, err)
		}
		if result.Title != title {
			t.Fatalf("cycle %d: published title = %q, want %q", cycle, result.Title, title)
		}

		w := httptest.NewRecorder()
		s.handleUnpublish(w, httptest.NewRequest(http.MethodPost, "/api/unpublish",
			jsonBody(t, map[string]string{"path": result.Path})))
		if w.Code != http.StatusOK {
			t.Fatalf("cycle %d: unpublish = %d: %s", cycle, w.Code, w.Body.String())
		}

		data, err := os.ReadFile(draftPath)
		if err != nil {
			t.Fatalf("cycle %d: read draft: %v", cycle, err)
		}
		markdown = string(data)
		var got []string
		for _, line := range strings.Split(markdown, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				got = append(got, strings.TrimSpace(strings.TrimLeft(trimmed, "#")))
			}
		}
		if len(got) != 1 || got[0] != title {
			t.Fatalf("cycle %d: draft headings = %q, want exactly [%q]:\n%s", cycle, got, title, markdown)
		}
	}
}
