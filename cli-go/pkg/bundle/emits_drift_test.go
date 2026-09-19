package bundle

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The DECLARATION check that nothing else performs.
//
// ⛔ Patrol's bundle drift check is `want ⊆ got`: it catches a tenant MISSING a
// declared emit, and NOT code emitting something UNDECLARED. So an event type
// that exists in code and in no bundle's Emits is invisible — the third variant
// of the defect family this workstream keeps finding, after "produced but never
// consumed" and "consumed but never produced".
//
// ⚠️ Three hand-maintained lists have to agree about event types:
//
//  1. bundle.go's per-type `Emits` — what a bundle DECLARES.
//  2. discovery-service/core/validation.ts's POLIS_CLIENT_ALLOWED_TYPES — what
//     the DS ACCEPTS from a client.
//  3. docs/general/reference/pql-vocabulary.json — view aliases only; a
//     fully-qualified type passes through, so it needs no entry per type.
//
// These tests cover 1 and 2. List 3 needs no per-type maintenance by design.

var publishEventLiteral = regexp.MustCompile(`stream\.PublishEvent\(\s*"([^"]+)"`)

// repoRoot walks up from the package directory to the directory holding both
// modules. Returns "" when the layout is not the planning repo — the public
// polis-cli mirror does not carry discovery-service/.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		return ""
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "cli-go", "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func scanPublishedEventTypes(t *testing.T, roots []string) map[string][]string {
	t.Helper()
	found := map[string][]string{}
	for _, root := range roots {
		filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			for _, m := range publishEventLiteral.FindAllStringSubmatch(string(data), -1) {
				found[m[1]] = append(found[m[1]], path)
			}
			return nil
		})
	}
	return found
}

// TestEveryPublishedEventTypeIsDeclared — code that emits an event absent from
// every bundle's Emits is undeclared, and nothing else would ever say so.
func TestEveryPublishedEventTypeIsDeclared(t *testing.T) {
	root := repoRoot(t)
	if root == "" {
		t.Skip("not the planning repo layout — the cross-module scan cannot run here")
	}

	declared := map[string]bool{}
	for _, e := range DefaultCoreBundle().AllEmittedEvents() {
		declared[e] = true
	}

	emitted := scanPublishedEventTypes(t, []string{
		filepath.Join(root, "cli-go"),
		filepath.Join(root, "webapp"),
	})
	if len(emitted) == 0 {
		t.Fatal("the scanner found no stream.PublishEvent call sites — it has stopped working, which is worse than a failure")
	}

	for typ, sites := range emitted {
		if !strings.HasPrefix(typ, "pub.polis.") {
			continue // a third party's namespace is not ours to declare
		}
		if !declared[typ] {
			t.Errorf("%s is published by %v and declared by no bundle content type", typ, sites)
		}
	}
}

// TestEveryPublishedEventTypeIsClientAllowed — the other list.
//
// ⛔ A type the DS does not allow from a client is rejected at publish time with
// "reserved for server-side emission", which reads as a bug in the publisher
// rather than a missing entry in a list. That is exactly why the DS deploys
// before the hosted service when a new type ships.
func TestEveryPublishedEventTypeIsClientAllowed(t *testing.T) {
	root := repoRoot(t)
	if root == "" {
		t.Skip("not the planning repo layout")
	}
	validation := filepath.Join(root, "discovery-service", "core", "validation.ts")
	data, err := os.ReadFile(validation)
	if err != nil {
		t.Skipf("discovery-service is not present in this checkout, so the allowlist half of the drift check did not run: %v", err)
	}

	start := strings.Index(string(data), "POLIS_CLIENT_ALLOWED_TYPES = [")
	if start < 0 {
		t.Fatal("the allowlist was renamed or restructured — this drift check is now blind")
	}
	end := strings.Index(string(data)[start:], "]")
	if end < 0 {
		t.Fatal("could not find the end of the allowlist")
	}
	block := string(data)[start : start+end]

	emitted := scanPublishedEventTypes(t, []string{
		filepath.Join(root, "cli-go"),
		filepath.Join(root, "webapp"),
	})
	for typ, sites := range emitted {
		if !strings.HasPrefix(typ, "pub.polis.") {
			continue
		}
		if !strings.Contains(block, "'"+typ+"'") {
			t.Errorf("%s is published by %v but is not in POLIS_CLIENT_ALLOWED_TYPES — the DS will reject it", typ, sites)
		}
	}
}

// TestActorEventsAreOnBothLists is the specific instance, pinned so that a
// future edit to either list is caught by a named test rather than only by the
// general scan.
func TestActorEventsAreOnBothLists(t *testing.T) {
	root := repoRoot(t)
	if root == "" {
		t.Skip("not the planning repo layout")
	}
	declared := map[string]bool{}
	for _, e := range DefaultCoreBundle().AllEmittedEvents() {
		declared[e] = true
	}
	data, err := os.ReadFile(filepath.Join(root, "discovery-service", "core", "validation.ts"))
	if err != nil {
		t.Skipf("discovery-service not present: %v", err)
	}

	for _, typ := range []string{
		"pub.polis.actor.registered",
		"pub.polis.actor.reregistered",
		"pub.polis.actor.withdrawn",
	} {
		if !declared[typ] {
			t.Errorf("%s is not declared in the core bundle", typ)
		}
		if !strings.Contains(string(data), "'"+typ+"'") {
			t.Errorf("%s is not in the DS client allowlist", typ)
		}
	}
}
