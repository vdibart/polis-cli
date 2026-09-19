package tailor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// Signet epic 25 E5, confirmed since: a site with NO posts
// directory must still get every Tailor check, and its non-post content must
// still be indexed. The early return this guards against lived in
// checkIndexRebuild until epic 37 D2 removed it; this asserts the whole run,
// not just that one check, so a short-circuit anywhere is caught.
func TestTailorRunsEveryCheckOnASiteWithNoPostsDirectory(t *testing.T) {
	dir := t.TempDir()
	if _, err := site.Init(dir, site.InitOptions{BaseURL: "https://maya.example", SiteTitle: "maya"}); err != nil {
		t.Fatal(err)
	}
	core := filepath.Join(dir, "content", "pub.polis.core")
	if err := os.RemoveAll(filepath.Join(core, "post")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(core, "attestation"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(core, "attestation", "20260101T000000Z-abcd.json"),
		[]byte(`{"type":"pub.polis.attestation","predicate":"pub.polis.attestation.agent-disclosure","asserted":"2026-01-01T00:00:00Z","current_version":"sha256:dddd"}`), 0644); err != nil {
		t.Fatal(err)
	}

	for _, mode := range []struct {
		name string
		run  func(string, ...string) *Result
	}{{"diagnose", Diagnose}, {"apply", Apply}} {
		res := mode.run(dir)
		if got, want := len(res.Checks), len(allChecks()); got != want {
			t.Errorf("%s: ran %d checks on a site with no posts directory, want all %d", mode.name, got, want)
		}
		for _, cr := range res.Checks {
			if cr.Name == "index-rebuild" && cr.Status == StatusSkip {
				t.Errorf("%s: index-rebuild skipped: %s", mode.name, cr.Message)
			}
		}
	}

	data, err := os.ReadFile(filepath.Join(core, "index.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"type":"attestation"`) {
		t.Errorf("a site with no posts directory did not have its attestation indexed:\n%s", data)
	}
}
