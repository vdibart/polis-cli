package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
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
