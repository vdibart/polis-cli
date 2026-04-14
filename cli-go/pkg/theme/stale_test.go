package theme

import (
	"strings"
	"testing"
)

func TestNormalizeForHash(t *testing.T) {
	// Template with theme-name comment
	input := `<!--
    Polis Theme: Turbo - Homepage Template

    Snippets loaded by this template:
    - theme:about
-->
<!DOCTYPE html>
<html lang="en">
<body>Hello</body>
</html>
`
	normalized := normalizeForHash(input)
	if strings.Contains(normalized, "Polis Theme: Turbo") {
		t.Error("normalizeForHash should strip the theme-name comment")
	}
	if !strings.Contains(normalized, "<!DOCTYPE html>") {
		t.Error("normalizeForHash should preserve the rest of the content")
	}

	// Content without comment should be unchanged
	noComment := "<!DOCTYPE html>\n<html></html>\n"
	if normalizeForHash(noComment) != noComment {
		t.Error("normalizeForHash should not modify content without leading comment")
	}
}
