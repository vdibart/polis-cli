package render

import (
	"strings"
	"testing"
)

// bluemonday URL-scheme-checks `src` on <video>/<audio> but not `poster`, so a
// javascript: poster survived sanitization. poster is image-only in browsers,
// so this is defence in depth, but it is the same omitted-URL-attribute class
// as <source src>.
func TestPosterURLSchemeIsChecked(t *testing.T) {
	for _, el := range []string{"video", "audio"} {
		bad, err := MarkdownToHTML(`<` + el + ` poster="javascript:alert(1)" src="https://a.example/m.mp4"></` + el + `>`)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(bad, "javascript:") {
			t.Errorf("%s: a javascript: poster survived: %s", el, bad)
		}
		good, err := MarkdownToHTML(`<` + el + ` poster="https://a.example/p.jpg" src="https://a.example/m.mp4"></` + el + `>`)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(good, `poster="https://a.example/p.jpg"`) {
			t.Errorf("%s: an https: poster was dropped: %s", el, good)
		}
	}
}

// The <source src> guard is load-bearing: bluemonday's own URL pass never
// reaches <source> (linkable omits it), so without the .Matching guard a
// javascript: src would survive.
func TestSourceSrcURLSchemeIsChecked(t *testing.T) {
	out, err := MarkdownToHTML(`<video><source src="javascript:alert(1)" type="video/mp4"><source src="https://a.example/m.mp4" type="video/mp4"></video>`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "javascript:") {
		t.Errorf("a javascript: source src survived: %s", out)
	}
	if !strings.Contains(out, `src="https://a.example/m.mp4"`) {
		t.Errorf("an https: source src was dropped: %s", out)
	}
}
