package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/tailor"
)

// runMain re-executes the test binary into main() with the given arguments,
// so a test can observe the exit code and stderr of the real command.
func runMain(t *testing.T, args ...string) (int, string) {
	t.Helper()
	if os.Getenv("TAILOR_TEST_RUN_MAIN") == "1" {
		return 0, ""
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainHelper$")
	cmd.Env = append(os.Environ(), "TAILOR_TEST_RUN_MAIN=1", "TAILOR_TEST_ARGS="+strings.Join(args, "\x1f"))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), stderr.String()
	}
	if err != nil {
		t.Fatalf("running main: %v", err)
	}
	return 0, stderr.String()
}

// TestMainHelper is the child side of runMain; it does nothing in a normal run.
func TestMainHelper(t *testing.T) {
	if os.Getenv("TAILOR_TEST_RUN_MAIN") != "1" {
		t.Skip("helper for runMain")
	}
	os.Args = append([]string{"tailor"}, strings.Split(os.Getenv("TAILOR_TEST_ARGS"), "\x1f")...)
	main()
}

// An empty argument is what an unset shell variable expands to (tailor "$DIR");
// it must be a usage error, never a panic and never a directory.
func TestEmptyArgumentIsAUsageError(t *testing.T) {
	for _, args := range [][]string{{""}, {"--json", ""}, {"", "."}} {
		code, stderr := runMain(t, args...)
		if strings.Contains(stderr, "panic") {
			t.Fatalf("args %q panicked:\n%s", args, stderr)
		}
		if code != 2 {
			t.Errorf("args %q: exit %d, want 2\n%s", args, code, stderr)
		}
		if !strings.Contains(stderr, "empty argument") || !strings.Contains(stderr, "Usage: tailor") {
			t.Errorf("args %q: stderr does not name the empty argument and the usage:\n%s", args, stderr)
		}
	}
}

// A selection naming an unknown check, using both lists, or missing its ids is
// a usage error that names the problem; --list needs no site.
func TestSelectionFlags(t *testing.T) {
	site := t.TempDir()
	if err := os.MkdirAll(site+"/.well-known", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(site+"/.well-known/polis", []byte(`{"public_key":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--only", "no-such-check", site}, "unknown check id(s): no-such-check"},
		{[]string{"--only", "tag-directory", "--skip", "cli-update", site}, "cannot be used together"},
		{[]string{"--only"}, "needs a comma-separated list"},
		{[]string{"--apply", "--only", "index-rebuild", "--skip-nothing"}, "unknown flag"},
	} {
		code, stderr := runMain(t, c.args...)
		if code != 2 || !strings.Contains(stderr, c.want) {
			t.Errorf("%q: exit %d, stderr %q; want exit 2 naming %q", c.args, code, stderr, c.want)
		}
	}
	if code, stderr := runMain(t, "--list"); code != 0 {
		t.Errorf("--list: exit %d\n%s", code, stderr)
	}
}

// The binary points at the upgrade guide (each check as a recipe, written for
// working through with an AI assistant) from --help, --list and a dry run that
// finds something, so a person who wants to choose changes finds it.
func TestHelpAndListPointAtTheUpgradeGuide(t *testing.T) {
	url := tailor.GuideURL(Version)
	if _, stderr := runMain(t, "--help"); !strings.Contains(stderr, url) || !strings.Contains(stderr, "AI assistant") {
		t.Errorf("--help does not point at the guide %s for an AI assistant:\n%s", url, stderr)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainHelper$")
	cmd.Env = append(os.Environ(), "TAILOR_TEST_RUN_MAIN=1", "TAILOR_TEST_ARGS=--list")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), url) {
		t.Errorf("--list does not point at the guide %s", url)
	}
}
