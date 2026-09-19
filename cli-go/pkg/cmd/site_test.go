package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/sitecheck"
)

// `polis site set author-name` sets the name
// through the one writer, and `polis validate` is no worse afterwards.
func TestSiteSetAuthorNameKeepsTheSiteValid(t *testing.T) {
	dir := t.TempDir()
	if _, err := site.Init(dir, site.InitOptions{BaseURL: "https://alice.polis.pub", SiteTitle: "alice"}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))
	failedBefore := failedChecks(sitecheck.RunLocalWith(dir, nil))

	prevDataDir, prevJSON := dataDir, jsonOutput
	t.Cleanup(func() { dataDir, jsonOutput = prevDataDir, prevJSON })
	dataDir, jsonOutput = dir, false
	handleSite([]string{"set", "author-name", "Alice Liddell"})

	wk, err := site.LoadWellKnown(dir)
	if err != nil {
		t.Fatal(err)
	}
	if wk.AuthorName != "Alice Liddell" {
		t.Fatalf("author_name = %q", wk.AuthorName)
	}
	for _, member := range []string{"public_key_history", "public_key_messages"} {
		if _, ok := wk.Extra[member]; !ok {
			t.Errorf("`polis site set author-name` dropped %s", member)
		}
	}
	var b, a map[string]json.RawMessage
	_ = json.Unmarshal(before, &b)
	after, _ := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))
	_ = json.Unmarshal(after, &a)
	for k := range b {
		if k != "author_name" && string(a[k]) != string(b[k]) {
			t.Errorf("%s changed", k)
		}
	}

	for check := range failedChecks(sitecheck.RunLocalWith(dir, nil)) {
		if !failedBefore[check] {
			t.Errorf("`polis validate` fails %s after setting a display name", check)
		}
	}
}

func failedChecks(r *sitecheck.Report) map[string]bool {
	out := map[string]bool{}
	for _, c := range r.Checks {
		if c.Outcome == sitecheck.OutcomeFailed {
			out[c.ID] = true
		}
	}
	return out
}

// The escape hatch every refusal names resolves the path to the owning package
// and writes UNSIGNED.
func TestSiteRewriteUnsignedDropsWhatItCannotReadAndDoesNotSign(t *testing.T) {
	dir := t.TempDir()
	if _, err := site.Init(dir, site.InitOptions{BaseURL: "https://alice.polis.pub", SiteTitle: "alice"}); err != nil {
		t.Fatal(err)
	}
	path := following.DefaultPath(dir)
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	_ = os.WriteFile(path, []byte(`{"version":"polis-cli-go/9.0.0","following":[{"url":"https://bob.example","added_at":"2026-01-01T00:00:00Z","muted":true}],"signature":"-----BEGIN SSH SIGNATURE-----\nx\n-----END SSH SIGNATURE-----"}`), 0644)

	prevDataDir, prevJSON := dataDir, jsonOutput
	t.Cleanup(func() { dataDir, jsonOutput = prevDataDir, prevJSON })
	dataDir, jsonOutput = dir, false
	handleSite([]string{"rewrite-unsigned", path})

	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "muted") {
		t.Fatal("the unrecognised member survived")
	}
	f, err := following.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.Signature != "" {
		t.Fatal("⛔ rewrite-unsigned left a signature")
	}
	if len(f.Following) != 1 || f.Following[0].URL != "https://bob.example" {
		t.Fatalf("modelled content lost: %+v", f.Following)
	}
}

func TestUnsignedRewriterRefusesAPathItDoesNotOwn(t *testing.T) {
	dir := t.TempDir()
	if name, fn := unsignedRewriterFor(dir, filepath.Join(dir, ".well-known", "polis")); fn != nil {
		t.Fatalf(".well-known/polis resolved to %q — it is unsigned and preserves, it has no escape hatch", name)
	}
}
