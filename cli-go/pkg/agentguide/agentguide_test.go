package agentguide

import (
	"io/fs"
	"strings"
	"testing"
)

// Every guide has its text, a title that is its first heading, and a polis://
// URI of its own; and every embedded file is a guide, so none is shipped that
// no server serves.
func TestEveryGuideIsServedAndEveryFileIsAGuide(t *testing.T) {
	seen := map[string]bool{}
	named := map[string]bool{}
	for _, g := range Guides() {
		if !strings.HasPrefix(g.URI, "polis://guides/") || seen[g.URI] {
			t.Errorf("%s: URI not polis://guides/… or repeated", g.URI)
		}
		seen[g.URI] = true
		named[g.file] = true
		text := g.Text()
		if !strings.HasPrefix(text, "# "+g.Title+"\n") {
			t.Errorf("%s: the text does not open with its title %q", g.URI, g.Title)
		}
		if g.Name == "" || g.Description == "" {
			t.Errorf("%s: no name or description", g.URI)
		}
	}
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !named[e.Name()] {
			t.Errorf("%s is embedded but is no guide", e.Name())
		}
	}
}

// ⭐ The four things the skill used to carry on its own are each a guide.
func TestTheSkillsKnowHowIsServed(t *testing.T) {
	for _, want := range []string{"untrusted-content", "titles", "replies", "blessing-review"} {
		found := false
		for _, g := range Guides() {
			found = found || g.Name == want
		}
		if !found {
			t.Errorf("no %s guide", want)
		}
	}
	if !strings.Contains(URIs(), "polis://guides/untrusted-content") {
		t.Errorf("URIs() = %q", URIs())
	}
}
