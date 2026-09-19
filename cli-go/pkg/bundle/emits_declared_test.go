package bundle

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// An event name has at least four segments (pub.polis.<type>.<verb>); a bare
// `pub.polis.comment` is a type name, and matching it as an event was how a
// first cut of this scan read a `type === …` test beside a call as an emit.
var eventNameGo = regexp.MustCompile(`"(pub\.polis\.[a-z0-9-]+\.[a-z0-9.-]+)"`)
var eventNameTS = regexp.MustCompile(`'(pub\.polis\.[a-z0-9-]+\.[a-z0-9.-]+)'`)

// goEmitMarkers are the Go call sites that publish an event, or name the event
// a publish is about to make. ⚠️ `LogEvent` is deliberately NOT here: it writes
// a local log line, not an event on the network, and treating the two as one is
// how `pub.polis.license.stated` came to be declared as an emitted event.
var goEmitMarkers = []string{
	"stream.PublishEvent(",
	"PublishEvent(",
	"LogSuppressedEmit(",
	"announceActorEvent(",
	"eventType :=",
	"eventType =",
	"const eventType",
}

// scanGoEmittedEventTypes collects the event names Go code publishes, reading
// the marker lines rather than only literal arguments — several publishers pass
// a variable assigned a line or two above (`eventType := …`), which a
// literal-argument scan cannot see.
func scanGoEmittedEventTypes(t *testing.T, roots []string) map[string][]string {
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
			for _, line := range strings.Split(string(data), "\n") {
				code := line
				if i := strings.Index(strings.TrimSpace(code), "//"); i == 0 {
					continue // a comment naming an event is not an emit
				}
				marked := false
				for _, m := range goEmitMarkers {
					if strings.Contains(code, m) {
						marked = true
						break
					}
				}
				if !marked {
					continue
				}
				for _, m := range eventNameGo.FindAllStringSubmatch(code, -1) {
					found[m[1]] = append(found[m[1]], path)
				}
			}
			return nil
		})
	}
	return found
}

// The discovery service emits with `emitEvent(storage, <name>, …)`, where
// <name> is a literal, an inline ternary, or a variable assigned just above the
// call. The scan follows all three: the names for post and comment
// registration are computed into a variable, and a window-based scan both
// missed them and read neighbouring type-name literals as events.
var emitEventCall = regexp.MustCompile(`(?s)emitEvent\(\s*storage,\s*([^,]+),`)
var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func scanDSEmittedEventTypes(t *testing.T, dsDir string) map[string][]string {
	t.Helper()
	found := map[string][]string{}
	filepath.WalkDir(dsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".ts") || strings.Contains(name, "_test.") || strings.Contains(name, ".test.") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		src := string(data)
		for _, call := range emitEventCall.FindAllStringSubmatchIndex(src, -1) {
			arg := strings.TrimSpace(src[call[2]:call[3]])
			text := arg
			if identifier.MatchString(arg) {
				// A name computed above the call. Take the NEAREST preceding
				// assignment: the same variable name is used for several
				// different events in one file.
				assign, err := regexp.Compile(fmt.Sprintf(`(?s)const\s+%s\s*=(.*?);`, regexp.QuoteMeta(arg)))
				if err != nil {
					continue
				}
				text = ""
				for _, a := range assign.FindAllStringSubmatchIndex(src[:call[0]], -1) {
					text = src[a[2]:a[3]]
				}
				if text == "" {
					t.Errorf("%s: emitEvent is passed %q and no assignment precedes it — the scan is blind to it", path, arg)
					continue
				}
			}
			for _, m := range eventNameTS.FindAllStringSubmatch(text, -1) {
				found[m[1]] = append(found[m[1]], path)
			}
		}
		return nil
	})
	return found
}

// allEmittedEventTypes is every event name anything in the system publishes:
// the Go writers and the discovery service.
func allEmittedEventTypes(t *testing.T, root string) map[string][]string {
	t.Helper()
	emitted := scanGoEmittedEventTypes(t, []string{
		filepath.Join(root, "cli-go"),
		filepath.Join(root, "webapp"),
	})
	for typ, sites := range scanDSEmittedEventTypes(t, filepath.Join(root, "discovery-service")) {
		emitted[typ] = append(emitted[typ], sites...)
	}
	return emitted
}

func requireRepoWithDS(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	if root == "" {
		t.Skip("not the planning repo layout — the cross-module scan cannot run here")
	}
	if _, err := os.Stat(filepath.Join(root, "discovery-service")); err != nil {
		t.Skip("discovery-service is not present in this checkout")
	}
	return root
}

// The other direction of the declaration check, and the one nothing performed:
// an event a type DECLARES that nothing emits. A declaration is a promise about
// what a site's software does, and five were false — `pub.polis.post.removed`
// (the DS emits `.unpublished`), the two licence events and the two theme
// events (Signet epic 41, close-out E1).
//
// ⚠️ The scan has to include the discovery service: the events a content
// registration produces are emitted THERE, in TypeScript, which is exactly why
// the Go-only scan beside this one never saw the disagreement.
func TestEveryDeclaredEventIsActuallyEmitted(t *testing.T) {
	root := requireRepoWithDS(t)

	emitted := allEmittedEventTypes(t, root)
	// Both halves must be working, or a declaration would read as false for the
	// wrong reason.
	for _, canary := range []string{
		"pub.polis.post.published",   // DS, via a variable
		"pub.polis.follow.announced", // Go, via a literal
		"pub.polis.actor.registered", // Go, via a variable
	} {
		if _, ok := emitted[canary]; !ok {
			t.Fatalf("the scan did not find %s — it is blind", canary)
		}
	}

	for _, declared := range DefaultCoreBundle().AllEmittedEvents() {
		if _, ok := emitted[declared]; !ok {
			t.Errorf("%s is declared in the core bundle and nothing emits it", declared)
		}
	}
}

// eventsNotYetDeclared are emitted by the discovery service and deliberately
// absent from the core bundle's Emits. EMPTY — keep the mechanism for the next
// one.
//
// ⛔ Adding an emit to an EXISTING type is not a free edit: Patrol and Tailor
// compare a tenant's declarations against the defaults as a superset test
// (`want ⊆ got`), `MergeDefaults` only adds whole missing TYPES, and both
// remediations flag per-field drift instead of repairing it. An event parked
// here waits for a named migration in Medic and Tailor that ships a deploy
// BEFORE the declaration — as `pub.polis.post.unpublished` and
// `pub.polis.comment.unpublished` did (close-out E1-R2, medic.UnpublishedEmits).
var eventsNotYetDeclared = map[string]bool{}

// Every event the DS emits for a declared content type is either declared or
// named above: the exception list cannot grow silently, and cannot go stale.
//
// Site-lifecycle events (`pub.polis.site.*`) are out of scope by construction —
// they belong to no content type, so no `Emits` list could hold them.
func TestDiscoveryServiceEventsAreDeclaredOrNamedAsPending(t *testing.T) {
	root := requireRepoWithDS(t)

	core := DefaultCoreBundle()
	declared := map[string]bool{}
	for _, e := range core.AllEmittedEvents() {
		declared[e] = true
	}
	emitted := scanDSEmittedEventTypes(t, filepath.Join(root, "discovery-service"))
	if len(emitted) == 0 {
		t.Fatal("the DS scanner found no emitEvent call sites — it has stopped working")
	}

	for typ, sites := range emitted {
		ofDeclaredType := false
		for typeName := range core.Types {
			if strings.HasPrefix(typ, typeName+".") {
				ofDeclaredType = true
				break
			}
		}
		if !ofDeclaredType {
			continue
		}
		switch {
		case declared[typ] && eventsNotYetDeclared[typ]:
			t.Errorf("%s is now declared — drop it from eventsNotYetDeclared and close E1", typ)
		case !declared[typ] && !eventsNotYetDeclared[typ]:
			t.Errorf("%s is emitted by %v and declared by no content type", typ, sites)
		}
	}
	for typ := range eventsNotYetDeclared {
		if _, ok := emitted[typ]; !ok {
			t.Errorf("%s is listed as pending but the DS no longer emits it — drop the entry", typ)
		}
	}
}
