package theme

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// The shape tokens (corner-radius, spacing, stroke-weight) are geometry the
// style guide measures around, so each is a short fixed scale: these tests
// hold the scale, its defaults and what each value writes. The measured
// alignment for every value is a screenshot check's (pixels, at 1x and 2x,
// from the fixture TestWriteV5ShapeFixtures writes), which needs a browser.

func themeCSS(t *testing.T, tokens string) string {
	t.Helper()
	th, err := ParseV5Theme([]byte(`{"tokens":{` + tokens + `}}`))
	if err != nil {
		t.Fatalf("%s: %v", tokens, err)
	}
	return V5ThemeCSS("t", th)
}

// TestV5ShapeTokensAtTheirDefaultsWriteNothing: a theme that names a shape
// token's default writes exactly what a theme that names none does, so every
// existing theme draws as it did.
func TestV5ShapeTokensAtTheirDefaultsWriteNothing(t *testing.T) {
	if got, want := themeCSS(t, `"corner-radius":"medium","spacing":"default","stroke-weight":"thin"`), themeCSS(t, ``); got != want {
		t.Errorf("defaults wrote something:\n%s", got)
	}
}

// TestV5ShapeTokensTakeOnlyTheirScale: a number, a length or any other word is
// refused; only the scale's values pass.
func TestV5ShapeTokensTakeOnlyTheirScale(t *testing.T) {
	for _, bad := range []string{
		`"corner-radius":"12px"`, `"corner-radius":"0"`, `"corner-radius":"round"`,
		`"spacing":"1.5"`, `"spacing":"tight"`, `"spacing":"24px"`,
		`"stroke-weight":"2px"`, `"stroke-weight":"bold"`, `"stroke-weight":"hairline"`,
	} {
		if _, err := ParseV5Theme([]byte(`{"tokens":{` + bad + `}}`)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

// TestV5ShapeTokensWriteTheirGeometry: each value other than the default
// writes its custom properties, and spacing writes whole pixels only.
func TestV5ShapeTokensWriteTheirGeometry(t *testing.T) {
	cases := map[string][]string{
		`"corner-radius":"square"`: {"--corner: 0;"},
		`"corner-radius":"small"`:  {"--corner: .5;"},
		`"corner-radius":"large"`:  {"--corner: 1.5;"},
		`"stroke-weight":"medium"`: {"--stroke: 2px;"},
		`"stroke-weight":"thick"`:  {"--stroke: 3px;"},
		`"spacing":"compact"`:      {"--pad-t: 18px;", "--pad-r: 26px;", "--pad-b: 16px;", "--pad-edge: 16px;", "--gap: 6px;", "--rule-gap: 16px;", "--rule-pad: 12px;"},
		`"spacing":"roomy"`:        {"--pad-t: 30px;", "--pad-r: 42px;", "--pad-b: 28px;", "--pad-edge: 28px;", "--gap: 16px;", "--rule-gap: 28px;", "--rule-pad: 20px;"},
	}
	for tokens, wants := range cases {
		css := themeCSS(t, tokens)
		for _, w := range wants {
			if !strings.Contains(css, w) {
				t.Errorf("%s: missing %q in\n%s", tokens, w, css)
			}
		}
	}
}

// TestV5ShapeDefaultsMatchTheShape: the shape's stylesheet declares each
// shape token at the contract's default, so a site with none set draws as
// the stylesheet alone does.
func TestV5ShapeDefaultsMatchTheShape(t *testing.T) {
	css, err := os.ReadFile("../bundle/fixtures/pub.polis.core/shapes/v5/v5.css")
	if err != nil {
		t.Fatal(err)
	}
	d := v5Spacing["default"]
	wants := []string{"--corner: 1;", "--stroke: 1px;"}
	for i, prop := range []string{"pad-t", "pad-r", "pad-b", "pad-edge", "gap", "rule-gap", "rule-pad"} {
		wants = append(wants, fmt.Sprintf("--%s: %dpx;", prop, d[i]))
	}
	for _, w := range wants {
		if !strings.Contains(string(css), w) {
			t.Errorf("v5.css does not declare %s", w)
		}
	}
	for _, n := range []string{"corner-radius", "spacing", "stroke-weight"} {
		tok, ok := v5Token(n)
		if !ok {
			t.Fatalf("%s is not in the contract", n)
		}
		if tok.Kind != V5Choice {
			t.Errorf("%s must be a choice from a fixed scale, never a free value", n)
		}
	}
}
