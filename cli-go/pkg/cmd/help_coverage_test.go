package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestHelp_DocumentsAllCommands ensures every command handler that
// exists in this package is mentioned in printUsage. New commands
// that ship without help-text trip this guard.
//
// The test is intentionally narrow: it doesn't validate the help
// text quality, only that each command name is reachable from
// `polis help`. Users who can't see a command in help can't run it.
func TestHelp_DocumentsAllCommands(t *testing.T) {
	output := captureHelp(t)

	// Commands that have no test coverage today and could easily
	// drift out of help text on rename.
	wantPresent := []string{
		"polis post",
		"polis comment",
		"polis blessing",
		"polis follow",
		"polis unfollow",
		"polis discover",
		"polis notifications",
		"polis dm",
		"polis tag",
		"polis clone",
		"polis init",
		"polis register",
		"polis unregister",
		"polis render",
		"polis rebuild",
		"polis index",
		"polis about",
		"polis unpublish",
		"polis rotate-key",
		"polis serve",
		"polis preview",
		"polis extract",
		"polis republish",
	}
	for _, cmd := range wantPresent {
		if !strings.Contains(output, cmd) {
			t.Errorf("expected help to mention %q", cmd)
		}
	}
}

// TestHelp_DMSubcommandsListed checks that each DM subcommand is
// reachable from help — a previous rename of `polis dm send` to
// `polis dm publish` (or similar) would silently strand users.
func TestHelp_DMSubcommandsListed(t *testing.T) {
	output := captureHelp(t)
	for _, sub := range []string{"dm list", "dm read", "dm send", "dm retry"} {
		if !strings.Contains(output, sub) {
			t.Errorf("expected help to mention 'polis %s'", sub)
		}
	}
}

// TestHelp_TagSubcommandsListed mirrors the DM check for tag.
func TestHelp_TagSubcommandsListed(t *testing.T) {
	output := captureHelp(t)
	for _, sub := range []string{"tag list", "tag show", "tag apply", "tag remove", "tag delete"} {
		if !strings.Contains(output, sub) {
			t.Errorf("expected help to mention 'polis %s'", sub)
		}
	}
}

// TestHelp_BlessingSubcommandsListed mirrors the DM check for blessing.
func TestHelp_BlessingSubcommandsListed(t *testing.T) {
	output := captureHelp(t)
	for _, sub := range []string{"blessing requests", "blessing grant", "blessing deny", "blessing beseech", "blessing sync"} {
		if !strings.Contains(output, sub) {
			t.Errorf("expected help to mention 'polis %s'", sub)
		}
	}
}

// TestHelp_GlobalFlagsMentioned ensures users see --json and --data-dir
// from the top-level help.
func TestHelp_GlobalFlagsMentioned(t *testing.T) {
	output := captureHelp(t)
	if !strings.Contains(output, "--json") {
		t.Error("expected help to mention --json global flag")
	}
	if !strings.Contains(output, "--data-dir") {
		t.Error("expected help to mention --data-dir global flag")
	}
}

// captureHelp returns the output of printUsage().
func captureHelp(t *testing.T) string {
	t.Helper()
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	printUsage()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.String()
}
