package ops

import (
	"os"
	"regexp"
	"testing"
)

// ⛔ Every tag.DiscoveryConfig built in ops names its site. Without Site the
// tag package falls back to the process-wide tag.DefaultSite, which on hosted
// is whichever tenant initialised last.
func TestEveryTagDiscoveryConfigNamesItsSite(t *testing.T) {
	b, err := os.ReadFile("builtin_core.go")
	if err != nil {
		t.Fatal(err)
	}
	lits := regexp.MustCompile(`(?s)tag\.DiscoveryConfig\{.*?\n\t*\}`).FindAll(b, -1)
	if len(lits) == 0 {
		t.Fatal("no tag.DiscoveryConfig literal found; the test proves nothing")
	}
	for _, l := range lits {
		if !regexp.MustCompile(`\bSite:`).Match(l) {
			t.Errorf("a tag.DiscoveryConfig without Site:\n%s", l)
		}
	}
}
