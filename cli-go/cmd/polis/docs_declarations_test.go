package main

// The docs-vs-declaration check.
//
// ⛔ ONE RULE: an `"emits": [...]` array printed in a doc lists only events the
// core bundle really declares. A doc example of `bundle.json` is read as the
// canonical declaration — people copy it — so a name that lives only in the
// example is a false statement about what a site's software does.
//
// This is the second time a declaration edit left docs behind: Signet epic 41
// found five declared-and-never-emitted events, and close-out E1's removal of
// them was still contradicted by three pages afterwards.
//
// ⚠️ Scope, deliberately narrow: the arrays, not the prose. Docs legitimately
// name retired events in history and migration tables, and a scan that failed
// on any mention would have to be taught about those — a rule with exceptions
// is one nobody keeps. Prose tables stay a human's job.

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
)

// emitsArray matches an `"emits": [ … ]` array, across lines, non-greedily to
// the first closing bracket. JSON arrays of strings cannot contain a `]`, so
// the first one always ends the array.
var emitsArray = regexp.MustCompile(`(?s)"emits"\s*:\s*\[(.*?)\]`)

var quotedEvent = regexp.MustCompile(`"(pub\.polis\.[a-z0-9.-]+)"`)

func TestDocsEmitsExamplesMatchTheDeclaration(t *testing.T) {
	declared := map[string]bool{}
	for _, e := range bundle.DefaultCoreBundle().AllEmittedEvents() {
		declared[e] = true
	}
	if len(declared) == 0 {
		t.Fatal("the default bundle declares no events — the check would pass by reading nothing")
	}

	checkedArrays := 0
	for _, page := range publicDocs(t) {
		data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(page)))
		if err != nil {
			t.Fatalf("read %s: %v", page, err)
		}
		for _, arr := range emitsArray.FindAllStringSubmatch(string(data), -1) {
			checkedArrays++
			for _, m := range quotedEvent.FindAllStringSubmatch(arr[1], -1) {
				if !declared[m[1]] {
					t.Errorf("%s: an \"emits\" example lists %s, which the core bundle does not declare", page, m[1])
				}
			}
		}
	}
	if checkedArrays == 0 {
		t.Fatal("no \"emits\" arrays found in docs — the scan has stopped working, which is worse than a failure")
	}
}
