package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
)

// headings returns the text of every ATX heading in a draft body. ⚠️ Counting
// only the headings that MATCH the expected title hides the defect this file
// exists for: a mismatched strip leaves the original heading in place and adds
// a differently-spelled one, and both counts stay at one.
func headings(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		out = append(out, strings.TrimSpace(strings.TrimLeft(trimmed, "#")))
	}
	return out
}

// A published comment keeps its heading in the body, and its frontmatter title
// is YAML-quoted — every auto-generated comment title is `Re: <slug>`, which
// contains a colon, so it is ALWAYS quoted. The strip compared the quoted
// frontmatter title against the unquoted body heading, never matched, and the
// draft came back with a second copy of the heading — one more per
// publish → unpublish cycle (close-out R2-1).
func TestRunUnpublish_CommentDraftDoesNotDuplicateItsHeading(t *testing.T) {
	dir, _ := setupUnpublishSite(t)

	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = false

	t.Setenv("POLIS_BASE_URL", "https://example.com")
	t.Setenv("DISCOVERY_SERVICE_URL", "http://localhost:1/fake-ds")

	// As SignComment writes it: the title quoted in frontmatter, the heading
	// unquoted in the body.
	rel := filepath.Join("content", "pub.polis.core", "comment", "20260201", "comment-h.md")
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	published := "---\ntitle: \"Re: a-post\"\ntype: comment\npublished: 2026-02-01T00:00:00Z\nauthor: test.example.com\ngenerator: polis-cli-go/0.68.0\nin-reply-to:\n  url: https://target.com/posts/post.md\n  root-post: https://target.com/posts/post.md\ncurrent-version: sha256:def456\nversion-history:\n  - sha256:def456 (2026-02-01T00:00:00Z)\nsignature: BBBB\n---\n\n# Re: a-post\n\nThis is a comment.\n"
	if err := os.WriteFile(full, []byte(published), 0644); err != nil {
		t.Fatal(err)
	}

	if err := RunUnpublish(dir, rel, true); err != nil {
		t.Fatalf("RunUnpublish: %v", err)
	}

	draft, err := os.ReadFile(filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "comments", "drafts", "comment-h.md"))
	if err != nil {
		t.Fatalf("read draft: %v", err)
	}
	body := string(draft)
	if got := headings(body); len(got) != 1 || got[0] != "Re: a-post" {
		t.Errorf("draft headings = %q, want exactly [\"Re: a-post\"]:\n%s", got, body)
	}
}

// ⛔ THE FIXTURE IS BUILT BY THE REAL PUBLISHER, over TWO cycles.
//
// The round-3 version of this test hand-wrote `title: "Foo: Bar"` beside a body
// heading `# "Foo: Bar"` — a state no publish path can produce — and so it
// defended the bug instead of catching it. `publish.PublishPost` passes the
// title through `escapeYAMLString`, whose rule is the same as the comment
// side's: quote on ": ", a trailing ":", a newline, a quote, edge whitespace or
// a leading YAML sigil. So a post titled `Foo: Bar` is stored quoted while its
// body heading is not, the strip misses, and unpublish leaves a second heading.
//
// ONE cycle hides the worst of it. Republishing takes the title from the first
// heading, so the quoting compounds: `Foo: Bar` → `"Foo: Bar"` →
// `"\"Foo: Bar\""`, with a heading added each time (close-out R3-1).
func TestRunUnpublish_PostSurvivesTwoPublishUnpublishCycles(t *testing.T) {
	dir, privKey := setupUnpublishSite(t)

	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = false

	t.Setenv("POLIS_BASE_URL", "https://example.com")
	t.Setenv("DISCOVERY_SERVICE_URL", "http://localhost:1/fake-ds")

	const title = "Foo: Bar" // needs YAML quoting — and every `Re:`-style title does
	markdown := "# " + title + "\n\nHello world.\n"

	draftPath := filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "posts", "drafts", "foo-bar.md")
	for cycle := 1; cycle <= 2; cycle++ {
		result, err := publish.PublishPost(dir, markdown, "foo-bar", privKey)
		if err != nil {
			t.Fatalf("cycle %d: PublishPost: %v", cycle, err)
		}
		if result.Title != title {
			t.Fatalf("cycle %d: published title = %q, want %q — the title escalated before unpublish", cycle, result.Title, title)
		}

		if err := RunUnpublish(dir, result.Path, true); err != nil {
			t.Fatalf("cycle %d: RunUnpublish: %v", cycle, err)
		}
		data, err := os.ReadFile(draftPath)
		if err != nil {
			t.Fatalf("cycle %d: read draft: %v", cycle, err)
		}
		markdown = string(data) // what the author would republish

		if got := headings(markdown); len(got) != 1 || got[0] != title {
			t.Fatalf("cycle %d: draft headings = %q, want exactly [%q]:\n%s", cycle, got, title, markdown)
		}
		if !strings.Contains(markdown, "Hello world.") {
			t.Fatalf("cycle %d: the body was lost:\n%s", cycle, markdown)
		}
	}
}

// A title whose quoting the publisher never added survives untouched. ⛔ `\'Foo\'`
// is the case the first UnquoteYAMLString regressed (close-out R4-1): the
// escaper leaves apostrophes bare, the tolerant unquote stripped them, and the
// post came back renamed with two headings — the R3-1 symptom, reintroduced by
// the fix for it.
func TestRunUnpublish_PostKeepsTitlePunctuationTheEscaperLeavesAlone(t *testing.T) {
	dir, privKey := setupUnpublishSite(t)

	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = false

	t.Setenv("POLIS_BASE_URL", "https://example.com")
	t.Setenv("DISCOVERY_SERVICE_URL", "http://localhost:1/fake-ds")

	for i, title := range []string{`'Foo'`, `it's`, `a\\b: c`} {
		markdown := "# " + title + "\n\nHello world.\n"
		slug := fmt.Sprintf("punct-%d", i)
		draftPath := filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "posts", "drafts", slug+".md")

		for cycle := 1; cycle <= 2; cycle++ {
			result, err := publish.PublishPost(dir, markdown, slug, privKey)
			if err != nil {
				t.Fatalf("%q cycle %d: PublishPost: %v", title, cycle, err)
			}
			if err := RunUnpublish(dir, result.Path, true); err != nil {
				t.Fatalf("%q cycle %d: RunUnpublish: %v", title, cycle, err)
			}
			data, err := os.ReadFile(draftPath)
			if err != nil {
				t.Fatalf("%q cycle %d: read draft: %v", title, cycle, err)
			}
			markdown = string(data)
			if got := headings(markdown); len(got) != 1 || got[0] != title {
				t.Fatalf("%q cycle %d: draft headings = %q, want exactly [%q]:\n%s", title, cycle, got, title, markdown)
			}
		}
	}
}

// A title the author really did type with double quotes keeps them: the
// unquote reverses the publisher's escaping, and nothing else.
func TestRunUnpublish_PostKeepsQuotesTheAuthorTyped(t *testing.T) {
	dir, privKey := setupUnpublishSite(t)

	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = false

	t.Setenv("POLIS_BASE_URL", "https://example.com")
	t.Setenv("DISCOVERY_SERVICE_URL", "http://localhost:1/fake-ds")

	const title = `The "quoted" one`
	result, err := publish.PublishPost(dir, "# "+title+"\n\nHello world.\n", "quoted", privKey)
	if err != nil {
		t.Fatalf("PublishPost: %v", err)
	}
	if err := RunUnpublish(dir, result.Path, true); err != nil {
		t.Fatalf("RunUnpublish: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "posts", "drafts", "quoted.md"))
	if err != nil {
		t.Fatalf("read draft: %v", err)
	}
	if got := headings(string(data)); len(got) != 1 || got[0] != title {
		t.Errorf("draft headings = %q, want exactly [%q]:\n%s", got, title, data)
	}
}
