package bundle_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/tailor"
)

// tenantEmitsBeforeE1 is what every existing tenant's bundle.json declares: the
// defaults as they shipped before close-out E1 removed the five events nothing
// emits. It is a fixture of the fleet, not of the code — ⛔ do not "update" it
// to match a later default, or the rollout question it answers disappears.
var tenantEmitsBeforeE1 = map[string][]string{
	"pub.polis.post":        {"pub.polis.post.published", "pub.polis.post.republished", "pub.polis.post.removed"},
	"pub.polis.comment":     {"pub.polis.comment.published", "pub.polis.comment.republished", "pub.polis.comment.blessing.requested", "pub.polis.comment.blessing.granted", "pub.polis.comment.blessing.denied"},
	"pub.polis.follow":      {"pub.polis.follow.announced", "pub.polis.follow.removed"},
	"pub.polis.tag":         {"pub.polis.tag.applied", "pub.polis.tag.removed"},
	"pub.polis.license":     {"pub.polis.license.stated", "pub.polis.license.withdrawn"},
	"pub.polis.theme":       {"pub.polis.theme.installed", "pub.polis.theme.removed"},
	"pub.polis.attestation": {"pub.polis.attestation.issued", "pub.polis.attestation.withdrawn"},
	"pub.polis.actor":       {"pub.polis.actor.registered", "pub.polis.actor.reregistered", "pub.polis.actor.withdrawn"},
}

// unpublishedEmitsMigration is close-out E1-R2's named migration, the event each
// of two core types gains: what Tailor's unpublished-emits check adds to a
// self-hosted site. A fixture, like the one above; the hosted service's twin is
// held to the same two strings where it lives.
var unpublishedEmitsMigration = map[string]string{
	"pub.polis.post":    "pub.polis.post.unpublished",
	"pub.polis.comment": "pub.polis.comment.unpublished",
}

// ⛔ THE ROLLOUT QUESTION, on the self-hosted side.
//
// Tailor compares a site's declarations against the current defaults as a
// superset test (`want ⊆ got`) and never repairs per-field drift, so REMOVING
// an event from the defaults is free while ADDING one puts every site the
// migration has not reached into drift. E1-R2 added the two `unpublished`
// events behind a named migration. The hosted half of this (Patrol and Medic
// against the same fixture) is asserted where those actors live.

// Read directly: every event the defaults declare is already in what sites have
// on disk once the named migrations have run — the pre-E1 fleet plus the
// unpublished-emits migration (Tailor's, on a self-hosted site; the hosted
// service runs its twin). The next event added to an existing type fails this
// until its own migration exists and is added here.
func TestDefaultsDeclareNoEventMigratedTenantsLack(t *testing.T) {
	for name, ct := range bundle.DefaultCoreBundle().Types {
		have := map[string]bool{}
		for _, e := range tenantEmitsBeforeE1[name] {
			have[e] = true
		}
		if e, ok := unpublishedEmitsMigration[name]; ok {
			have[e] = true
		}
		for _, e := range ct.Emits {
			if !have[e] {
				t.Errorf("type %s declares %q, which a migrated tenant's bundle.json does not: every existing tenant would sit in unhealable drift. It needs a named migration in Tailor and in the hosted service, shipped a deploy first", name, e)
			}
		}
	}
}

func writePreE1Bundle(t *testing.T, siteDir string) {
	t.Helper()
	b := bundle.DefaultCoreBundle()
	for name, emits := range tenantEmitsBeforeE1 {
		ct, ok := b.Types[name]
		if !ok {
			t.Fatalf("the fixture names type %s, which the default bundle no longer declares", name)
		}
		ct.Emits = emits
		b.Types[name] = ct
	}
	path := filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	// ⚠️ Tailor skips EVERY check on a directory with no identity document, so
	// without this its half of any assertion here passes vacuously — as the
	// first version of this file's Tailor check did.
	wk := filepath.Join(siteDir, ".well-known", "polis")
	if err := os.MkdirAll(filepath.Dir(wk), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wk, []byte(`{"public_key":"x"}`), 0644); err != nil {
		t.Fatal(err)
	}
}

// declaredEmits reads a site's bundle.json and returns type → emits.
func declaredEmits(t *testing.T, siteDir string) map[string][]string {
	t.Helper()
	b, err := bundle.LoadBundle(filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for name, ct := range b.Types {
		out[name] = ct.Emits
	}
	return out
}

func hasEmit(emits []string, e string) bool {
	for _, x := range emits {
		if x == e {
			return true
		}
	}
	return false
}

func countEmit(emits []string, e string) int {
	n := 0
	for _, x := range emits {
		if x == e {
			n++
		}
	}
	return n
}

// The same migration through Tailor (self-hosted): Diagnose changes nothing,
// Apply migrates once, and the declarations check is clean afterwards.
func TestUnpublishedEmitsMigration_Tailor(t *testing.T) {
	siteDir := t.TempDir()
	writePreE1Bundle(t, siteDir)
	status := func(r *tailor.Result, name string) tailor.CheckResult {
		for _, c := range r.Checks {
			if c.Name == name {
				return c
			}
		}
		t.Fatalf("no %s check", name)
		return tailor.CheckResult{}
	}

	diagnosed := tailor.Diagnose(siteDir)
	if c := status(diagnosed, "unpublished-emits-migration"); c.Status != tailor.StatusFail {
		t.Fatalf("diagnose: %+v, want the migration reported as pending", c)
	}
	// Unmigrated, the site is in drift against defaults that declare the
	// events: the migration, not code order, is what gates the rollout.
	if c := status(diagnosed, "bundle-declarations"); c.Status != tailor.StatusFail {
		t.Errorf("Tailor reads an unmigrated site as clean against the new defaults: %+v", c)
	}
	if hasEmit(declaredEmits(t, siteDir)["pub.polis.post"], "pub.polis.post.unpublished") {
		t.Fatal("Diagnose wrote bundle.json")
	}
	first := tailor.Apply(siteDir)
	if c := status(first, "unpublished-emits-migration"); c.Status != tailor.StatusFail || len(c.Actions) != 1 {
		t.Fatalf("apply: %+v, want one migration action", c)
	}
	after := declaredEmits(t, siteDir)
	for typeName, event := range unpublishedEmitsMigration {
		if countEmit(after[typeName], event) != 1 {
			t.Errorf("%s: emits = %v, want %s exactly once", typeName, after[typeName], event)
		}
	}
	second := tailor.Apply(siteDir)
	if c := status(second, "unpublished-emits-migration"); c.Status != tailor.StatusPass {
		t.Errorf("second apply: %+v, want pass", c)
	}
	if c := status(second, "bundle-declarations"); c.Status == tailor.StatusFail {
		t.Errorf("Tailor reports drift on a migrated site: %s", c.Message)
	}
}
