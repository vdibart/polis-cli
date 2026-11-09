package streammark

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

func rules(t *testing.T, rs ...Rule) File {
	t.Helper()
	f, err := Normalize(File{Rules: rs})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestMatchIsTheAuthorAndAWholeWordInAnyCase(t *testing.T) {
	f := rules(t, Rule{AuthorDomain: "Discover.polis.pub", AnyOf: []string{"Platform", "own domain"}})
	cases := []struct {
		author, text, want string
	}{
		{"discover.polis.pub", "What if social didn't require a platform?", MarkAlert},
		{"discover.polis.pub", "PLATFORM-free", MarkAlert},                // a hyphen is a boundary
		{"discover.polis.pub", "Your own domain, your rules.", MarkAlert}, // a phrase
		{"discover.polis.pub", "platforms everywhere", ""},                // not the word
		{"discover.polis.pub", "replatform", ""},
		{"other.polis.pub", "a platform", ""}, // another author
		{"", "a platform", ""},
	}
	for _, c := range cases {
		if got := f.Match(c.author, c.text); got != c.want {
			t.Errorf("Match(%q, %q) = %q, want %q", c.author, c.text, got, c.want)
		}
	}
	if got := f.Match("discover.polis.pub", "nothing here", "but the body mentions a platform"); got != MarkAlert {
		t.Errorf("a keyword in a later text did not match")
	}
}

func TestNormalizeRefusesWhatIsNotPlainData(t *testing.T) {
	ok := Rule{AuthorDomain: "discover.polis.pub", AnyOf: []string{"platform"}}
	bad := []Rule{
		{AuthorDomain: "", AnyOf: []string{"x"}},
		{AuthorDomain: "https://discover.polis.pub", AnyOf: []string{"x"}},
		{AuthorDomain: "discover.polis.pub"},
		{AuthorDomain: "discover.polis.pub", AnyOf: []string{"plat.*form"}},
		{AuthorDomain: "discover.polis.pub", AnyOf: []string{"<script>"}},
		{AuthorDomain: "discover.polis.pub", AnyOf: []string{strings.Repeat("a", MaxKeywordLen+1)}},
		{AuthorDomain: "discover.polis.pub", AnyOf: []string{"x"}, Mark: "html"},
	}
	for _, r := range bad {
		if _, err := Normalize(File{Rules: []Rule{r}}); err == nil {
			t.Errorf("Normalize accepted %+v", r)
		}
	}
	many := make([]Rule, MaxRules+1)
	for i := range many {
		many[i] = ok
	}
	if _, err := Normalize(File{Rules: many}); err == nil {
		t.Error("Normalize accepted more than MaxRules")
	}
	if _, err := Normalize(File{Rules: []Rule{{ID: "a", AuthorDomain: "x.pub", AnyOf: []string{"y"}}, {ID: "a", AuthorDomain: "x.pub", AnyOf: []string{"z"}}}}); err == nil {
		t.Error("Normalize accepted two rules with one id")
	}
	f := rules(t, ok)
	if f.Rules[0].Mark != MarkAlert || f.Rules[0].ID == "" {
		t.Errorf("defaults not filled: %+v", f.Rules[0])
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	site := storage.At(dir)
	if f, err := Load(site); err != nil || len(f.Rules) != 0 {
		t.Fatalf("Load with no file = %+v, %v", f, err)
	}
	if _, err := Save(site, File{Rules: []Rule{{AuthorDomain: "discover.polis.pub", AnyOf: []string{"Platform"}}}}); err != nil {
		t.Fatal(err)
	}
	f, err := Load(site)
	if err != nil || len(f.Rules) != 1 || f.Rules[0].AnyOf[0] != "platform" {
		t.Fatalf("Load after Save = %+v, %v", f, err)
	}
	info, err := os.Stat(filepath.Join(dir, Key))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the rules file = %v, %v; want mode 600", info, err)
	}
	if _, err := Save(site, File{Rules: []Rule{{AuthorDomain: "bad domain", AnyOf: []string{"x"}}}}); err == nil {
		t.Error("Save wrote an invalid rule")
	}
	if f, _ := Load(site); len(f.Rules) != 1 {
		t.Error("a refused Save changed the file")
	}
}

// A file that did not come through Save is checked on the way in.
func TestLoadRefusesAFileSaveWouldHaveRefused(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, filepath.FromSlash(Key))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"rules":[{"author_domain":"not a domain <b>","any_of":["x"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(storage.At(dir)); err == nil {
		t.Error("Load accepted a rule Save refuses")
	}
}
