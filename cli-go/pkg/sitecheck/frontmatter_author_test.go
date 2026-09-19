package sitecheck

import (
	"os"
	"path/filepath"
	"testing"
)

// FrontmatterAuthor is the one author reader Patrol's foreign-content sensor,
// Tailor's public-path cleanup and Clerk's comment parity share. These are its
// answers, pinned: each was checked identical to all three former copies
// when they were merged. ⚠️ Two are quirks kept on purpose, because changing them
// would change what three actors conclude: an `author:` after a preamble is still
// read (late-open), and a BOM hides the frontmatter (bom).
func TestFrontmatterAuthor_Pinned(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"plain":               {"---\nauthor: plain.example\n---\n", "plain.example"},
		"padded":              {"---\nauthor:    padded.example   \n---\n", "padded.example"},
		"no-frontmatter":      {"# Title\nauthor: body.example\n", ""},
		"indented-author":     {"---\ntitle: x\n  author: nested.example\n---\nbody\n", ""},
		"body-author":         {"---\ntitle: x\n---\nauthor: body.example\n", ""},
		"crlf":                {"---\r\ntitle: x\r\nauthor: crlf.example\r\n---\r\nbody\r\n", "crlf.example"},
		"crlf-open-lf-inside": {"---\r\nauthor: mixed.example\n---\n", "mixed.example"},
		"empty-author":        {"---\nauthor:\n---\n", ""},
		"first-of-two":        {"---\nauthor: first.example\nauthor: second.example\n---\n", "first.example"},
		"unterminated":        {"---\ntitle: x\nauthor: open.example\n", "open.example"},
		"late-open":           {"preamble\n---\nauthor: late.example\n---\n", "late.example"},
		"prefix-key":          {"---\nauthority: nope.example\n---\n", ""},
		"empty-file":          {"", ""},
		"bom":                 {"\xef\xbb\xbf---\nauthor: bom.example\n---\n", ""},
	}
	dir := t.TempDir()
	for name, c := range cases {
		p := filepath.Join(dir, name+".md")
		if err := os.WriteFile(p, []byte(c.body), 0644); err != nil {
			t.Fatal(err)
		}
		if got := FrontmatterAuthor(p); got != c.want {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
	if got := FrontmatterAuthor(filepath.Join(dir, "missing.md")); got != "" {
		t.Errorf("missing file: got %q", got)
	}
}
