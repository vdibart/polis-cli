package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// promptWith runs PromptForLicense against `answer` as if it were typed, and
// returns the choice plus what the user saw.
//
// Real pipes rather than a stubbed reader: PromptForLicense takes *os.File
// because it reads a terminal, and the empty-input case is exactly the one a
// fake would get wrong.
func promptWith(t *testing.T, answer string) (choice, shown string) {
	t.Helper()

	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	if _, err := inW.WriteString(answer); err != nil {
		t.Fatalf("write answer: %v", err)
	}
	inW.Close()

	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		buf.ReadFrom(outR)
		done <- buf.String()
	}()

	choice = PromptForLicense(inR, outW)
	outW.Close()
	inR.Close()
	return choice, <-done
}

// Pressing enter must state NOTHING.
//
// It used to state `reserved`, on the argument that a pre-selected
// recommendation makes enter a choice rather than a silent default. It does
// not: the user who presses enter to get past a question has not read the
// question, and a signed statement of intent is the last thing that should
// come out of dismissing one.
func TestPressingEnterAtTheLicensePromptStatesNothing(t *testing.T) {
	choice, _ := promptWith(t, "\n")
	if choice != "none" {
		t.Errorf("empty input at the licence prompt = %q; want %q — enter must never state terms", choice, "none")
	}
}

// EOF is the same case with no keystroke at all — a closed stdin, a piped
// heredoc that ran out. It reached a separate `return "reserved"` on the read
// error, so fixing only the empty-string arm would have left it behind.
func TestClosedInputAtTheLicensePromptStatesNothing(t *testing.T) {
	choice, _ := promptWith(t, "")
	if choice != "none" {
		t.Errorf("EOF at the licence prompt = %q; want %q", choice, "none")
	}
}

// A typo must never publish a licence.
//
// The old `default:` arm returned `reserved`, so "r", "y", "yes", a stray
// paste — anything at all — stated terms. The two failure directions do not
// cost the same: an unwanted grant is irreversible for everything published
// under it, an unwanted silence costs one `polis license reserved`.
func TestUnrecognisedInputAtTheLicensePromptStatesNothing(t *testing.T) {
	for _, answer := range []string{"r", "y", "yes", "reservd", "4", "-", "🙂"} {
		choice, _ := promptWith(t, answer+"\n")
		if choice == "reserved" || choice == "open" {
			t.Errorf("unrecognised input %q = %q; want no terms stated", answer, choice)
		}
		if choice != "none" {
			t.Errorf("unrecognised input %q = %q; want %q", answer, choice, "none")
		}
	}
}

// Removing the default must not remove the ability to choose. Every explicit
// answer, by number and by name, still means what it said.
func TestExplicitLicenceChoicesStillWork(t *testing.T) {
	for answer, want := range map[string]string{
		"1":        "reserved",
		"reserved": "reserved",
		" 1 ":      "reserved",
		"2":        "open",
		"open":     "open",
		"3":        "none",
		"none":     "none",
		"unstated": "none",
	} {
		if choice, _ := promptWith(t, answer+"\n"); choice != want {
			t.Errorf("answer %q = %q; want %q", answer, choice, want)
		}
	}
}

// The prompt must still ASK, and must still show the recommendation.
//
// This epic removes the pre-selection, not the question. A prompt that stopped
// offering `reserved` would be the opposite error — stating nothing is a
// choice, and a choice needs the alternatives visible.
func TestTheLicensePromptStillAsksAndStillRecommends(t *testing.T) {
	_, shown := promptWith(t, "\n")
	for _, want := range []string{
		"Reserved",
		"(recommended)",
		"Open",
		"Unstated",
		"not retroactive",
		"Press enter to state nothing",
		"Choice:",
	} {
		if !strings.Contains(shown, want) {
			t.Errorf("licence prompt does not show %q:\n%s", want, shown)
		}
	}
	// ⛔ The pre-selection is what went. "[1]" as the prompt suffix is how it
	// was expressed, and its return would silently restore the old behaviour.
	if strings.Contains(shown, "Choice [1]") {
		t.Error("licence prompt still pre-selects a recommendation")
	}
}

// Declining is not the same as never being told there was a choice.
//
// `polis init` with no terms is now the DEFAULT outcome, not an edge case, so
// the run has to end with the user knowing how to state terms later.
func TestInitWithoutTermsSaysHowToStateThemLater(t *testing.T) {
	dir := t.TempDir()

	prevDataDir, prevJSON := dataDir, jsonOutput
	t.Cleanup(func() { dataDir, jsonOutput = prevDataDir, prevJSON })
	dataDir, jsonOutput = dir, false

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	handleInit([]string{"--license", "none"})
	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "none stated") {
		t.Errorf("init did not report that no terms were stated:\n%s", output)
	}
	if !strings.Contains(output, "polis license reserved") {
		t.Errorf("init did not say how to state terms later:\n%s", output)
	}

	if _, err := os.Stat(site.LicensePath(dir)); !os.IsNotExist(err) {
		t.Errorf("`polis init --license none` wrote a licence document")
	}
	if p := site.LicensePointer(dir); p != "" {
		t.Errorf("`polis init --license none` wrote a licence pointer: %q", p)
	}
}

// `polis init --license reserved` is untouched by this epic. A user who says
// what they want gets exactly what they got before.
func TestInitWithAnExplicitLicenceStillStatesIt(t *testing.T) {
	dir := t.TempDir()

	prevDataDir, prevJSON := dataDir, jsonOutput
	t.Cleanup(func() { dataDir, jsonOutput = prevDataDir, prevJSON })
	dataDir, jsonOutput = dir, false

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w
	handleInit([]string{"--license", "reserved"})
	w.Close()
	os.Stdout = old

	terms, err := site.SiteTerms(dir)
	if err != nil || terms == nil {
		t.Fatalf("SiteTerms after --license reserved = %v, %v; want terms", terms, err)
	}
	if terms.Profile != "pub.polis.license.reserved/1" {
		t.Errorf("profile = %q; want the reserved profile", terms.Profile)
	}
	if p := site.LicensePointer(dir); p == "" {
		t.Error(".well-known/polis has no licence pointer")
	}
	if _, err := os.Stat(filepath.Join(dir, site.LicenseURLPath(dir))); err != nil {
		t.Errorf("licence document missing: %v", err)
	}
}
