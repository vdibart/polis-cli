package cmd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// `polis dm list` in a directory that is not a site rebuilt dm/inbox.json
// there and exited 0: the directory gained a .polis tree, and a stray run from
// the wrong directory wrote a site into the repository. It now refuses first.
func TestDMListRefusesADirectoryThatIsNotASite(t *testing.T) {
	if os.Getenv("POLIS_TEST_DM_HELPER") == "1" {
		dataDir = os.Getenv("POLIS_TEST_DIR")
		handleDM([]string{"list"})
		os.Exit(0)
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestDMListRefusesADirectoryThatIsNotASite$")
	cmd.Env = append(os.Environ(), "POLIS_TEST_DM_HELPER=1", "POLIS_TEST_DIR="+dir)
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
		t.Errorf("dm list outside a site exited 0; want non-zero\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".polis")); !os.IsNotExist(err) {
		t.Error("dm list wrote a .polis tree into a directory that is not a site")
	}
}
