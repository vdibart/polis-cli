package publish

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A first line that is a heading gives its text, whatever its level, and never
// the #s themselves: "## Notes" used to become the title "## Notes".
func TestATitleFromAHeadingOfAnyLevelCarriesNoHashes(t *testing.T) {
	cases := map[string]string{
		"# One":                 "One",
		"## Two":                "Two",
		"###### Six":            "Six",
		"##\tTabbed level two":  "Tabbed level two",
		"# \tTab-led":           "\tTab-led", // one separator is removed, as before
		"## Closed ##":          "Closed",
		"# C#":                  "C#", // a # inside the text is not a closing sequence
		"\n\n## After blanks\n": "After blanks",
		"####### Seven":         "####### Seven", // not a heading: the line itself
		"#hashtag":              "#hashtag",      // no space: not a heading
		"Plain first line":      "Plain first line",
		"":                      "Untitled",
	}
	for in, want := range cases {
		if got := ExtractTitle(in); got != want {
			t.Errorf("ExtractTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

// The fallback cut counts characters, not bytes, so a long first line in a
// non-ASCII script is never split inside a character.
func TestTheTitleFallbackNeverSplitsACharacter(t *testing.T) {
	line := strings.Repeat("é", 59) + "日本語の文章"
	got := ExtractTitle(line)
	if !utf8.ValidString(got) {
		t.Fatalf("title is not valid UTF-8: %q", got)
	}
	if n := utf8.RuneCountInString(got); n != titleFallbackRunes {
		t.Errorf("title has %d characters, want %d", n, titleFallbackRunes)
	}
	ascii := strings.Repeat("a", 70)
	if got := ExtractTitle(ascii); got != ascii[:60] {
		t.Errorf("an ASCII line is cut as before: got %q", got)
	}
}
