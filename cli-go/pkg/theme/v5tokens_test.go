package theme

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const themesDir = "../bundle/fixtures/pub.polis.core/themes"

// v5Definitions is every theme's v5 definition in the bundle.
func v5Definitions(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	entries, err := os.ReadDir(themesDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if b, err := os.ReadFile(filepath.Join(themesDir, e.Name(), V5TokensFile)); err == nil {
			out[e.Name()] = b
		}
	}
	return out
}

// TestEveryV5ThemeKeepsTheContract: every theme's v5 definition parses, names
// only tokens the contract declares, with values it allows, and passes the
// contrast check (spec 6.1: legible or refused).
func TestEveryV5ThemeKeepsTheContract(t *testing.T) {
	defs := v5Definitions(t)
	if len(defs) < 10 {
		t.Fatalf("found %d v5 definitions, want every converted theme", len(defs))
	}
	for name, b := range defs {
		th, err := ParseV5Theme(b)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		for _, p := range CheckV5Contrast(th) {
			t.Errorf("%s: %s", name, p)
		}
	}
}

// TestV5ContractRefusesWhatItDoesNotAllow: an unknown token, a value outside
// a choice, and anything shaped like CSS (in a font token too).
func TestV5ContractRefusesWhatItDoesNotAllow(t *testing.T) {
	for _, bad := range []string{
		`{"tokens":{"colour":"#fff"}}`,
		`{"tokens":{"rollover":"spin"}}`,
		`{"tokens":{"font-ui":"Inter; color: red"}}`,
		`{"tokens":{"font-heading":"*/ body {} /*"}}`,
		`{"tokens":{"site":"red; background: url(https://x.example/a)"}}`,
		`{"tokens":{"bg":"url(https://x.example/a.png)"}}`,
		`{"note":"*/ body { display: none } /*","tokens":{}}`,
		`{"tokens":{},"css":"body{}"}`,
	} {
		if _, err := ParseV5Theme([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

// TestV5UnknownFontFallsBack: a font the catalog lacks draws in the default,
// and the theme keeps its other values, so a catalog refresh breaks nothing.
func TestV5UnknownFontFallsBack(t *testing.T) {
	th, err := ParseV5Theme([]byte(`{"tokens":{"font-ui":"Comic Sans MS","font-heading":"Gone Font","site":"#336699"}}`))
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	if th.Get("font-ui") != "Inter" || th.Get("font-heading") != "content" {
		t.Errorf("want the defaults, got %q and %q", th.Get("font-ui"), th.Get("font-heading"))
	}
	if th.Get("site") != "#336699" {
		t.Errorf("the theme's other values were lost")
	}
	if len(th.FellBack) != 2 {
		t.Errorf("want both fonts named, got %v", th.FellBack)
	}
}

// TestV5ContrastNamesThePair: a combination that fails says which, and how far.
func TestV5ContrastNamesThePair(t *testing.T) {
	th, _ := ParseV5Theme([]byte(`{"tokens":{"text-soft":"#cccccc"}}`))
	got := CheckV5Contrast(th)
	if len(got) == 0 || !strings.Contains(got[0].String(), "text-soft on surface") {
		t.Errorf("want the failing pair named, got %v", got)
	}
}

// TestV5ThemeCSSFetchesNothing: whatever a theme sets, the CSS it becomes
// names no address but the data: images polis draws itself.
func TestV5ThemeCSSFetchesNothing(t *testing.T) {
	for name, b := range v5Definitions(t) {
		th, err := ParseV5Theme(b)
		if err != nil {
			continue
		}
		css := comments.ReplaceAllString(ownDrawing.ReplaceAllString(V5ThemeCSS(name, th), ""), "")
		if strings.Contains(css, "url(") || strings.Contains(css, "@import") || strings.Contains(css, "//") {
			t.Errorf("%s: the theme's CSS names something to fetch: %s", name, css)
		}
	}
}

// ownDrawing is an image polis draws itself: an inline SVG, as a data: URL.
var ownDrawing = regexp.MustCompile(`url\("data:image/svg\+xml;utf8,[^"]*"\)`)

// comments are the notes, which may say anything a reader can read.
var comments = regexp.MustCompile(`(?s)/\*.*?\*/`)

// TestV5DefaultsMatchTheShape: the contract's defaults are the polis default
// the shape's stylesheet declares, so a theme that sets nothing draws exactly
// as the shape does.
func TestV5DefaultsMatchTheShape(t *testing.T) {
	css, err := os.ReadFile("../bundle/fixtures/pub.polis.core/shapes/v5/v5.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"site", "bg", "surface", "card-hover", "panel", "text", "text-soft", "text-muted", "ink", "wordmark"} {
		tok, _ := v5Token(n)
		if !strings.Contains(string(css), "--"+n+": "+tok.Default+";") {
			t.Errorf("the shape does not declare --%s: %s, the contract's default", n, tok.Default)
		}
	}
}

// TestTheElwoodPairIsReservedUntilItJoinsV5: defined in the bundle, picked by
// no one yet.
func TestTheElwoodPairIsReservedUntilItJoinsV5(t *testing.T) {
	for _, n := range []string{"elwood-signal", "elwood-noise"} {
		if Selectable(n) {
			t.Errorf("%s must not be selectable yet", n)
		}
	}
	if Selectable("classic") || Selectable("pub.polis.themes.classic") {
		t.Error("classic is retired (renamed daryl): an old site's bundle.json still declares it, and nothing may offer it")
	}
	if !Selectable("daryl") {
		t.Error("daryl is offered (to v5 sites, by the shape-aware picker)")
	}
}

// bg-gradient-angle: "down" (the default) writes exactly the gradient it
// always did; any other direction is named; anything else is refused.
func TestV5GradientRunsTheWayItsAngleSays(t *testing.T) {
	css := func(tokens string) string {
		th, err := ParseV5Theme([]byte(`{"tokens":{` + tokens + `}}`))
		if err != nil {
			t.Fatalf("%s: %v", tokens, err)
		}
		return V5ThemeCSS("t", th)
	}
	if got := css(`"bg":"#101010","bg-gradient":"#202020"`); !strings.Contains(got, "linear-gradient(#101010, #202020)") {
		t.Errorf("the default direction moved: %s", got)
	}
	if got := css(`"bg":"#101010","bg-gradient":"#202020","bg-gradient-angle":"down"`); !strings.Contains(got, "linear-gradient(#101010, #202020)") {
		t.Errorf("down is not the default's bytes: %s", got)
	}
	if got := css(`"bg":"#101010","bg-gradient":"#202020","bg-gradient-angle":"up-left"`); !strings.Contains(got, "linear-gradient(to top left, #101010, #202020)") {
		t.Errorf("up-left: %s", got)
	}
	if strings.Contains(css(`"bg-gradient-angle":"right"`), "linear-gradient") {
		t.Error("an angle without a gradient drew one")
	}
	for _, bad := range []string{`"bg-gradient-angle":"45deg"`, `"bg-gradient-angle":"to right"`, `"bg-gradient-angle":"sideways"`} {
		if _, err := ParseV5Theme([]byte(`{"tokens":{` + bad + `}}`)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}
