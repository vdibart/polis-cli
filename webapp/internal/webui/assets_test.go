package webui

import (
	"strings"
	"testing"
)

// The brand is written lowercase on every surface, the SPA's tab title included.
func TestSPATitleWritesTheBrandLowercase(t *testing.T) {
	b, err := Assets.ReadFile("www/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "<title>polis</title>") {
		t.Error("index.html does not carry <title>polis</title>")
	}
}
