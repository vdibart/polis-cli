package comment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

const publishedComment = `---
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

// Unpublishing a comment returns it to a draft, and the confirmation promises
// that republishing it asks for blessing again. That was impossible: the draft
// kept no reply fields, so SignComment refused it with "in_reply_to is
// required" (close-out E2).
func TestAnUnpublishedCommentCanBeSignedAgain(t *testing.T) {
	dir := t.TempDir()
	draftsDir := filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "comments", "drafts")
	if err := os.MkdirAll(draftsDir, 0755); err != nil {
		t.Fatal(err)
	}

	draft := UnpublishedDraftContent(publishedComment, `"Re: a-post"`, "My reply.\n")
	if err := os.WriteFile(filepath.Join(draftsDir, "a-comment.md"), []byte(draft), 0644); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadDraft(dir, "a-comment")
	if err != nil {
		t.Fatalf("LoadDraft: %v", err)
	}
	if loaded.InReplyTo != "https://someone.example/posts/20260901/a-post.md" {
		t.Errorf("in_reply_to = %q", loaded.InReplyTo)
	}
	if loaded.RootPost != "https://someone.example/posts/20260901/root.md" {
		t.Errorf("root_post = %q — a reply in a thread must keep its root", loaded.RootPost)
	}
	if !strings.Contains(loaded.Content, "My reply.") {
		t.Errorf("draft body lost the comment: %q", loaded.Content)
	}

	priv, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	signed, err := SignComment(dir, loaded, "owner.example", "https://owner.example", priv)
	if err != nil {
		t.Fatalf("SignComment on the unpublished draft: %v", err)
	}
	// The title round-trips as a title, not as a quoted string of one.
	if strings.Contains(signed.Meta.Title, `"`) {
		t.Errorf("title = %q — the YAML quoting was carried into the title", signed.Meta.Title)
	}
	// Nothing published survives: a republish is a fresh publication.
	if strings.Contains(draft, "signature:") || strings.Contains(draft, "current-version:") ||
		strings.Contains(draft, "version-history:") {
		t.Errorf("draft carries published identity:\n%s", draft)
	}
}

// The reply fields are read by structural position. A comment whose BODY has a
// line starting `in-reply-to:` must not be read as the frontmatter's.
func TestReplyFieldsComeFromTheFrontmatterNotTheBody(t *testing.T) {
	body := "Here is how a comment declares its target:\n\nin-reply-to:\n  url: https://attacker.example/posts/evil.md\n  root-post: https://attacker.example/posts/evil.md\n"
	url, root := ParseNestedInReplyTo(publishedComment[:strings.Index(publishedComment, "\n\nMy reply.")] + "\n\n" + body)
	if url != "https://someone.example/posts/20260901/a-post.md" {
		t.Errorf("url = %q, want the frontmatter's", url)
	}
	if root != "https://someone.example/posts/20260901/root.md" {
		t.Errorf("root-post = %q, want the frontmatter's", root)
	}

	// And with no frontmatter at all, a body line claims nothing.
	if url, root := ParseNestedInReplyTo(body); url != "" || root != "" {
		t.Errorf("body-only content yielded url=%q root=%q, want empty", url, root)
	}
}

// The builder owns the unquote and the heading strip, so a body that still
// carries its heading comes back with exactly one — and a heading that is not
// the title is left alone.
func TestUnpublishedDraftContentHandlesTheBodysOwnHeading(t *testing.T) {
	frontmatter := func(title string) string {
		return "---\ntitle: " + title + "\ntype: comment\npublished: 2026-09-17T10:00:00Z\nin-reply-to:\n  url: https://someone.example/posts/20260901/a-post.md\n  root-post: https://someone.example/posts/20260901/a-post.md\n---\n"
	}
	cases := []struct {
		name        string
		title, body string
		want        []string // headings expected in the draft body, in order
	}{
		{
			// The dominant case: every auto-generated title is `Re: <slug>`,
			// which contains a colon and is therefore always quoted.
			name:  "quoted title, body repeats it",
			title: `"Re: a-post"`,
			body:  "# Re: a-post\n\nMy reply.\n",
			want:  []string{"Re: a-post"},
		},
		{
			name:  "unquoted title, body repeats it",
			title: "Short one",
			body:  "# Short one\n\nMy reply.\n",
			want:  []string{"Short one"},
		},
		{
			name:  "body has no heading",
			title: `"Re: a-post"`,
			body:  "My reply.\n",
			want:  []string{"Re: a-post"},
		},
		{
			// A leading heading that is not the title is the author's own
			// subhead; stripping it would eat their content.
			name:  "body leads with a different heading",
			title: `"Re: a-post"`,
			body:  "# On second thought\n\nMy reply.\n",
			want:  []string{"Re: a-post", "On second thought"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			draft := UnpublishedDraftContent(frontmatter(tc.title)+"\n"+tc.body, tc.title, tc.body)
			var got []string
			for _, line := range strings.Split(StripFrontmatter(draft), "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "#") {
					got = append(got, strings.TrimSpace(strings.TrimLeft(trimmed, "#")))
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("headings = %q, want %q:\n%s", got, tc.want, draft)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("heading %d = %q, want %q:\n%s", i, got[i], tc.want[i], draft)
				}
			}
			if !strings.Contains(draft, "My reply.") {
				t.Errorf("the body was lost:\n%s", draft)
			}
		})
	}
}
