package sitecheck

import (
	"os"
	"path/filepath"
	"testing"
)

// ⛔ THE FLEET GUARD.
//
// These predicates were extracted from Patrol and Judge, which run hourly over
// 31 tenants and compare against stored baselines. A changed message or a
// changed status is not a cosmetic diff — it is a false positive on every
// tenant at once, on the next sweep.
//
// So the exact strings are pinned here. If one of these tests fails, the
// question is not "update the expectation"; it is "did I mean to change what
// the fleet reports, and what is the rollout?"
func TestExtractedPredicates_MessagesArePinnedForTheFleet(t *testing.T) {
	t.Run("KeyPerms", func(t *testing.T) {
		dir := t.TempDir()
		if got := KeyPerms(dir); got.OK || got.Message != "private key not found" {
			t.Errorf("missing key: %+v", got)
		}

		keyPath := filepath.Join(dir, ".polis", "keys", "id_ed25519")
		if err := os.MkdirAll(filepath.Dir(keyPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(keyPath, []byte("k"), 0644); err != nil {
			t.Fatal(err)
		}
		if got := KeyPerms(dir); got.OK || got.Message != "unsafe permissions 0644 (expected 0600 or 0400)" {
			t.Errorf("loose perms: %+v", got)
		}

		os.Chmod(keyPath, 0600)
		if got := KeyPerms(dir); !got.OK || got.Message != "" {
			t.Errorf("0600: %+v — a passing check must stay silent, as the fleet expects", got)
		}
		os.Chmod(keyPath, 0400)
		if got := KeyPerms(dir); !got.OK {
			t.Errorf("0400: %+v", got)
		}
	})

	t.Run("BundleJSON", func(t *testing.T) {
		dir := t.TempDir()
		if got := BundleJSON(dir); got.OK || got.Message != "missing" {
			t.Errorf("absent: %+v", got)
		}

		path := filepath.Join(dir, "content", "pub.polis.core", "bundle.json")
		os.MkdirAll(filepath.Dir(path), 0755)

		os.WriteFile(path, []byte("{nope"), 0644)
		if got := BundleJSON(dir); got.OK || got.Message[:12] != "invalid JSON" {
			t.Errorf("malformed: %+v", got)
		}

		os.WriteFile(path, []byte(`{"version":"1.0.0"}`), 0644)
		if got := BundleJSON(dir); got.OK || got.Message != "missing required field: name" {
			t.Errorf("no name: %+v", got)
		}

		os.WriteFile(path, []byte(`{"name":"pub.polis.core"}`), 0644)
		if got := BundleJSON(dir); got.OK || got.Message != "missing required field: version" {
			t.Errorf("no version: %+v", got)
		}

		os.WriteFile(path, []byte(`{"name":"pub.polis.core","version":"1.0.0"}`), 0644)
		if got := BundleJSON(dir); !got.OK || got.Message != "" {
			t.Errorf("valid: %+v", got)
		}
	})

	t.Run("IndexConsistency", func(t *testing.T) {
		b := newSite(t)
		if got := IndexConsistency(b.dir); !got.OK || got.Message != "empty index" {
			t.Errorf("no entries: %+v", got)
		}

		// ⚠️ CHANGED DELIBERATELY — Signet epic 42 V1. A clean result used to be
		// silent, and it was silent for three entry types it never examined. It now
		// NAMES what it checked (epic 20 D8). Rollout: an OK status raises no alert
		// (judge.fail.index fires on !OK only), so this reaches Judge's stored
		// result and `polis validate`'s detail and nothing else. Every failure
		// message below is unchanged.
		b.addPost(t, "hello", "Hello.\n", true)
		if got := IndexConsistency(b.dir); !got.OK || got.Message != "1 entry checked (post 1)" {
			t.Errorf("consistent: %+v", got)
		}

		os.Remove(filepath.Join(b.dir, "content", "pub.polis.core", "post", "20260101", "hello.md"))
		got := IndexConsistency(b.dir)
		want := "1 issues: orphan: content/pub.polis.core/post/20260101/hello.md (indexed but missing)"
		if got.OK || got.Message != want {
			t.Errorf("orphan:\n got %q\nwant %q", got.Message, want)
		}
	})

	t.Run("PolicySyntax", func(t *testing.T) {
		dir := t.TempDir()
		if got := PolicySyntax(dir); len(got) != 0 {
			t.Errorf("no policy files must yield no warnings, got %+v", got)
		}

		path := filepath.Join(dir, "policies", "rules.jsonl")
		os.MkdirAll(filepath.Dir(path), 0755)
		os.WriteFile(path, []byte(
			`{"policy":"allow pub.polis.dm from following"}`+"\n"+
				`{"policy":"allow pub.polis.dm from following"}`+"\n"+
				`{"policy":"deny pub.polis.dm from following"}`+"\n"), 0644)

		warnings := PolicySyntax(dir)
		var dup, contra bool
		for _, w := range warnings {
			if w.File != filepath.Join("policies", "rules.jsonl") {
				t.Errorf("warning file = %q, want a site-relative path", w.File)
			}
			if w.Error == "duplicate of policies/rules.jsonl:1" {
				dup = true
			}
			if len(w.Error) > 10 && w.Error[:10] == "contradict" {
				contra = true
			}
		}
		if !dup {
			t.Errorf("duplicate detection lost in extraction: %+v", warnings)
		}
		if !contra {
			t.Errorf("contradiction detection lost in extraction: %+v", warnings)
		}
	})

	t.Run("LicenseIntegrity", func(t *testing.T) {
		b := newSite(t)
		// No pointer, no claim. Silence is a defined state, and this exact
		// message is what tells Patrol not to alert on the majority of tenants.
		if got := LicenseIntegrity(b.dir, b.pub); !got.OK || got.Message != "no terms stated" {
			t.Errorf("no pointer: %+v", got)
		}

		b.write(filepath.Join(".well-known", "polis"), mustJSON(map[string]interface{}{
			"version": "2.0", "public_key": string(b.pub),
			"license": "/content/pub.polis.core/license/license.json",
		}))
		got := LicenseIntegrity(b.dir, b.pub)
		want := "licence pointer /content/pub.polis.core/license/license.json resolves to nothing"
		if got.OK || got.Message != want {
			t.Errorf("dangling pointer:\n got %q\nwant %q", got.Message, want)
		}
	})
}
