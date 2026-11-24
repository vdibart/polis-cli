package styleguide

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ⛔ The embedded copy is the published page, byte for byte.
func TestTheEmbeddedGuideIsThePublishedPage(t *testing.T) {
	page := filepath.Join("..", "..", "..", "docs", "webapp", "designer", "style-guide.md")
	want, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("POLIS_UPDATE_STYLE_GUIDE") == "1" {
		if err := os.WriteFile("style-guide.txt", want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if text != string(want) {
		t.Fatal("cli-go/pkg/styleguide/style-guide.txt differs from docs/webapp/designer/style-guide.md: " +
			"edit the docs page, then refresh with POLIS_UPDATE_STYLE_GUIDE=1 go test ./pkg/styleguide/")
	}
}

// The links an agent reads work where it reads them: relative ones resolved
// against the published page, anchors and absolute ones left alone.
func TestTheGuidesLinksAreAbsolute(t *testing.T) {
	got := Text()
	for _, bad := range []string{"](../", "](./"} {
		if strings.Contains(got, bad) {
			t.Errorf("a relative link is left: %s", bad)
		}
	}
	if !strings.Contains(got, "](https://github.com/vdibart/polis-cli/blob/main/docs/general/concepts/themes.md)") {
		t.Error("the themes link was not resolved against the page")
	}
	if !strings.Contains(got, "](#4-alignment)") {
		t.Error("an in-page anchor was rewritten")
	}
}
