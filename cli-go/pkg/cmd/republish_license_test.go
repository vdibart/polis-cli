package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// `polis republish <post> <new.md>` stripped the new file's frontmatter
// without reading the author's `license:` line, so a new version took the
// site's terms instead of the ones written on it. `polis publish` always read
// it; republish now does the same. Re-signing the post as it stands (no new
// file) still takes the site's current terms: its block is the stamped form,
// which is not an authored override.
func TestRepublishCarriesTheLicenceWrittenOnTheNewVersion(t *testing.T) {
	if os.Getenv("POLIS_TEST_REPUBLISH_HELPER") == "1" {
		dataDir = os.Getenv("POLIS_TEST_DIR")
		args := []string{os.Getenv("POLIS_TEST_POST")}
		if f := os.Getenv("POLIS_TEST_NEW"); f != "" {
			args = append(args, f)
		}
		handleRepublish(args)
		os.Exit(0)
	}

	dir := t.TempDir()
	if _, err := site.Init(storage.At(dir), site.InitOptions{License: "reserved", BaseURL: "https://maya.example"}); err != nil {
		t.Fatal(err)
	}
	priv, err := os.ReadFile(filepath.Join(dir, ".polis", "keys", "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := publish.PublishPost(storage.At(dir), "# Notes\n\nFirst take.\n", "notes", priv,
		&publish.DiscoveryConfig{BaseURL: "https://maya.example"})
	if err != nil {
		t.Fatal(err)
	}
	profile := func() string {
		raw, err := os.ReadFile(filepath.Join(dir, first.Path))
		if err != nil {
			t.Fatal(err)
		}
		terms := license.ParseBlock(string(raw))
		if terms == nil {
			t.Fatal("no licence block")
		}
		return terms.Profile
	}
	republish := func(newFile string) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestRepublishCarriesTheLicenceWrittenOnTheNewVersion$")
		cmd.Env = append(os.Environ(), "POLIS_TEST_REPUBLISH_HELPER=1", "POLIS_TEST_DIR="+dir,
			"POLIS_TEST_POST="+first.Path, "POLIS_TEST_NEW="+newFile,
			"POLIS_BASE_URL=https://maya.example", "DISCOVERY_SERVICE_URL=")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("republish: %v\n%s", err, out)
		}
	}
	if got := profile(); got != license.ProfileReserved {
		t.Fatalf("first version = %q, want the site default", got)
	}

	newFile := filepath.Join(t.TempDir(), "new.md")
	if err := os.WriteFile(newFile, []byte("---\nlicense: open\n---\n# Notes\n\nSecond take, now open.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	republish(newFile)
	if got := profile(); got != license.ProfileOpen {
		t.Errorf("after republishing with license: open, profile = %q, want %q", got, license.ProfileOpen)
	}

	republish("")
	if got := profile(); got != license.ProfileReserved {
		t.Errorf("after re-signing as it stands, profile = %q, want the site's current %q", got, license.ProfileReserved)
	}
}
