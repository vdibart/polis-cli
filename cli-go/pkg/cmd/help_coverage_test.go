package cmd

import (
	"bytes"
	"os"
	"regexp"
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
	// ⚠️ This list is HAND-MAINTAINED, which is the reason `polis license`
	// stayed invisible in help from epic 01 until epic 03's E3 found it: a
	// command absent from BOTH printUsage and this slice is a command no test
	// can miss, because nothing asserts the two are the same set. Add every new
	// command here. See the epic 03 review log.
	wantPresent := []string{
		"polis license",
		"polis attest withdraw",
		"polis post",
		"polis comment",
		"polis blessing",
		"polis follow",
		"polis unfollow",
		"polis discover",
		"polis notifications",
		"polis dm",
		"polis tag",
		"polis attest",
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
		"polis did",
		"polis site set author-name",
		"polis site set avatar",
		"polis site rewrite-unsigned",
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

// commandsExemptFromHelp are dispatch cases that deliberately do not appear in
// `polis help`. Keep this list SHORT and justified — every entry is a command a
// user cannot discover.
var commandsExemptFromHelp = map[string]string{
	"version":   "meta — its own output is the documentation",
	"--version": "flag alias for version",
	"-v":        "flag alias for version",
	"help":      "meta — prints the very text this test checks",
	"--help":    "flag alias for help",
	"-h":        "flag alias for help",
}

// TestHelp_EveryDispatchedCommandIsInHelp derives its expectations from the
// dispatch switch in root.go instead of a hand-maintained list.
//
// ⚠️ THIS IS THE TEST THE HAND-MAINTAINED LIST COULD NOT BE. `polis license`
// shipped documented, dispatched and audit-logged, and was missing from BOTH
// printUsage and TestHelp_DocumentsAllCommands' wantPresent slice — so it was
// invisible to `polis help` for two epics and no test could notice, because a
// command absent from both lists is absent from nothing. Adding a command to
// the switch now fails this test until help mentions it or the exemption above
// says why not.
//
// It reads root.go as source text rather than reflecting over the switch,
// because Go gives no runtime handle on case labels. That makes the test
// slightly brittle to reformatting and worth exactly that cost: the failure it
// prevents shipped twice.
func TestHelp_EveryDispatchedCommandIsInHelp(t *testing.T) {
	src, err := os.ReadFile("root.go")
	if err != nil {
		t.Fatalf("read root.go: %v", err)
	}

	// Narrow to the dispatch switch so the audit-log switch (which lists a
	// SUBSET of commands, and legitimately so) cannot contribute names.
	body := string(src)
	start := strings.Index(body, "switch command {")
	if start < 0 {
		t.Fatal("could not find `switch command {` in root.go — has dispatch been restructured? " +
			"If so, update this test rather than deleting it.")
	}
	body = body[start:]
	if end := strings.Index(body, "\n\tdefault:"); end > 0 {
		body = body[:end]
	}

	caseRe := regexp.MustCompile(`(?m)^\tcase (.+):$`)
	nameRe := regexp.MustCompile(`"([^"]+)"`)

	var dispatched []string
	for _, line := range caseRe.FindAllStringSubmatch(body, -1) {
		for _, n := range nameRe.FindAllStringSubmatch(line[1], -1) {
			dispatched = append(dispatched, n[1])
		}
	}
	if len(dispatched) < 10 {
		t.Fatalf("only found %d dispatched commands; the parse is wrong, not the code", len(dispatched))
	}

	help := captureHelp(t)
	for _, cmd := range dispatched {
		if why, exempt := commandsExemptFromHelp[cmd]; exempt {
			t.Logf("skipping %q: %s", cmd, why)
			continue
		}
		if !strings.Contains(help, "polis "+cmd) {
			t.Errorf("`polis %s` is dispatched in root.go but never mentioned in help — "+
				"add it to printUsage, or to commandsExemptFromHelp with a reason", cmd)
		}
	}
}

// TestHelp_AdvertisesOnlyFormsTheHandlersAccept — help used to advertise
// `polis comment <file> [url]` (the bash form; the Go handler takes only
// draft/sign/list/sync), `discover --since`, and init's bash-only path flags.
// A user who copies a line from help must get a command that runs.
func TestHelp_AdvertisesOnlyFormsTheHandlersAccept(t *testing.T) {
	output := captureHelp(t)
	for _, sub := range []string{"comment draft", "comment sign", "comment list", "comment sync"} {
		if !strings.Contains(output, "polis "+sub) {
			t.Errorf("expected help to mention 'polis %s'", sub)
		}
	}
	for _, stale := range []string{
		"polis comment <file>",
		"polis comment my-comment.md",
		"--since",
		"--keys-dir", "--posts-dir", "--comments-dir", "--snippets-dir", "--versions-dir",
		"polis clone <url> --full", "polis clone <url> --diff",
	} {
		if strings.Contains(output, stale) {
			t.Errorf("help advertises %q, which the Go CLI does not accept", stale)
		}
	}
}
