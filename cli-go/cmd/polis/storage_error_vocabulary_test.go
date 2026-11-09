package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The detach moved filesystem reads behind pkg/storage, whose absence error is
// storage.ErrNotFound. It WRAPS fs.ErrNotExist, so errors.Is sees it — but
// os.IsNotExist does not unwrap and answers false, sending a caller down the
// "a real failure happened" arm for an ordinary absence.
//
// ⛔ That mistake was made FOUR times during the detach, and the compiler was
// silent every time. Its worst reach: the webapp returning 500 instead of 404
// for an unprovisioned DM keyring, Patrol publishing a wrong fleet status, and
// key provisioning that refused to provision.
//
// So the build checks it. A file that can see a storage error must not ask
// os.IsNotExist whether something is missing.
func TestNoStorageAwareFileUsesOsIsNotExist(t *testing.T) {
	// ⛔ repoRoot, not "..": this test lives in cmd/polis, so ".." is cmd/ and a
	// walk from there sees ten files and no pkg/ at all. The first version of
	// this guard did exactly that and passed on an injected violation.
	roots := []string{filepath.Join(repoRoot, "cli-go"), filepath.Join(repoRoot, "webapp")}
	// ⚠️ Also catches an ALIASED os import and a direct sentinel comparison, both
	// of which the first version missed.
	callRe := regexp.MustCompile(`\b\w+\.IsNotExist\(|==\s*os\.ErrNotExist|os\.ErrNotExist\s*==`)

	var bad []string
	scanned := 0
	for _, root := range roots {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			src := string(b)
			// ⛔ NO IMPORT FILTER. The first version only scanned files importing
			// pkg/storage, so a storage error arriving THROUGH another package was
			// invisible — which is most of them, and is exactly how cas.go escaped the
			// sweep that prompted this test. Every non-test Go file is in scope.
			scanned++
			for i, line := range strings.Split(src, "\n") {
				trimmed := strings.TrimSpace(line)
				// A comment naming the hazard is the point, not a violation.
				if strings.HasPrefix(trimmed, "//") {
					continue
				}
				if callRe.MatchString(line) {
					bad = append(bad, filepath.ToSlash(path)+":"+strconv.Itoa(i+1))
				}
			}
			return nil
		})
	}
	// ⛔ A guard that scans nothing passes forever. Say so loudly.
	if scanned == 0 {
		t.Fatal("scanned no storage-importing files; this check would pass on an empty tree")
	}
	if len(bad) > 0 {
		t.Errorf("these files import pkg/storage and still call os.IsNotExist, which cannot see "+
			"storage.ErrNotFound and will take the wrong branch for a missing key — use "+
			"errors.Is(err, fs.ErrNotExist):\n  %s", strings.Join(bad, "\n  "))
	}
}

// ⛔ The harder half, and the reason a sweep is not enough: one caller matched
// the error MESSAGE for "no such file". No search for os.IsNotExist could find
// it, and it broke silently the moment the read moved behind the interface,
// because the store says "storage: not found".
func TestNoFileMatchesAFilesystemErrorByItsMessage(t *testing.T) {
	roots := []string{filepath.Join(repoRoot, "cli-go"), filepath.Join(repoRoot, "webapp")}
	// ⛔ "not found" IS the phrase the storage interface now emits, and the first
	// version of this list omitted it — so the one real instance in the corpus
	// (an ops handler matching "not found" to choose a 404) would not have been
	// caught by the guard written in response to it.
	// ⚠️ COMPLETE phrases, closing quote included. `"not found` alone also matches
	// `"not found in pending"`, which is a DOMAIN phrase about a comment and not a
	// filesystem error — the guard has to tell those apart or it cries wolf.
	// ⚠️ ENUMERATE THE PHRASES; do not try to be clever with the quoting. An
	// earlier version used `"not exist` open-ended, caught `"does not exist"`, and
	// was then closed to `"not exist"` to stop a false positive on
	// `"not found in pending"` — which silently let `"does not exist"` through, as
	// a reviewer found. The closing quote was never the distinction: the
	// distinction is WHICH PHRASE, so each filesystem one is listed.
	phrases := []string{
		`"no such file`,
		`"not exist"`,
		`"does not exist`,
		`"cannot find the file`,
		`"The system cannot find`,
	}

	// ⛔ `"not found"` IS DELIBERATELY ABSENT from that list, and it is the one
	// judgement in this guard. webapp/internal/api maps an error containing those
	// words to HTTP 404, that mapping is asserted by its own test, and it is the
	// handler's intended contract rather than an accident of wording — so flagging
	// it would be the guard crying wolf about a decision. The sentinel check added
	// beside it is what makes the common case independent of the text.

	var bad []string
	scanned := 0
	for _, root := range roots {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			scanned++
			b, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			lines := strings.Split(string(b), "\n")
			for i, line := range lines {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "//") {
					continue
				}
				// ⚠️ Look at this line AND the next: the first version matched one line
				// only, which is not the shape the real instance had.
				window := line
				if i+1 < len(lines) {
					window = line + "\n" + lines[i+1]
				}
				if !strings.Contains(window, ".Error()") {
					continue
				}
				line = window
				for _, ph := range phrases {
					if strings.Contains(line, ph) {
						bad = append(bad, filepath.ToSlash(path)+":"+strconv.Itoa(i+1))
					}
				}
			}
			return nil
		})
	}
	if scanned == 0 {
		t.Fatal("scanned no Go files; this check would pass on an empty tree")
	}
	if len(bad) > 0 {
		t.Errorf("these lines decide whether a file is missing by matching the error TEXT, which "+
			"changes whenever a read moves behind a different backend — use "+
			"errors.Is(err, fs.ErrNotExist):\n  %s", strings.Join(bad, "\n  "))
	}
}
