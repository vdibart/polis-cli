package publish

import "testing"

// A tab-led title is reachable (`# <tab>Title` survives ExtractTitle), and
// ParseFrontmatter trims an unquoted value, so the escaper must quote ANY
// leading or trailing whitespace, not only a space, or the tab is lost on the
// way back. Held end to end, through the real parser.
func TestEscapedTitleWithEdgeWhitespaceSurvivesTheFrontmatterRoundTrip(t *testing.T) {
	for _, title := range []string{"\tTabbed", "tabbed\t", " spaced", " nbsp", "plain"} {
		fm := ParseFrontmatter("---\ntitle: " + escapeYAMLString(title) + "\n---\n")
		if got := UnquoteYAMLString(fm["title"]); got != title {
			t.Errorf("title %q came back as %q (escaped as %q)", title, got, escapeYAMLString(title))
		}
	}
	// Nothing without edge whitespace changes its escaped form.
	if got := escapeYAMLString("plain title"); got != "plain title" {
		t.Errorf("a plain title is now escaped as %q", got)
	}
}
