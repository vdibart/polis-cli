package sitecheck

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/tag"
)

// SIGNET epic 44 D6 — THE PARITY GUARD.
//
// ⭐ One fixture site on disk, served by httptest from that same directory, run
// BOTH ways and compared check by check. No network, so CI runs it on every push.
//
// ⛔ Clean fixtures alone would not catch F1: remote index.consistency PASSED a
// site it examined one type of, and so did local. So every fixture is also run
// with ONE entry type's bytes tampered — the form that did not look is the one
// that still says PASS.

// servedSite is a four-type site whose attestation names the server it is served
// from, so a remote run verifies it against the key it actually fetches.
func servedSite(t *testing.T) (*siteBuilder, *httptest.Server, map[string]string) {
	t.Helper()
	b := newSite(t)
	ts := httptest.NewServer(http.FileServer(http.Dir(b.dir)))
	t.Cleanup(ts.Close)

	if _, err := site.WriteGenesisKeyHistory(b.dir); err != nil {
		t.Fatal(err)
	}
	b.addPost(t, "hello", "Hello.\n", true)
	paths := map[string]string{
		"comment":     b.addComment(t, "reply", "A reply.\n"),
		"tag":         b.addTag(t, "essays"),
		"attestation": b.addAttestationBy(t, ts.URL),
	}
	paths["post"] = "content/pub.polis.core/post/20260101/hello.md"
	return b, ts, paths
}

// addAttestationBy is addAttestation with the issuer named.
func (b *siteBuilder) addAttestationBy(t *testing.T, issuer string) string {
	t.Helper()
	r := &attestation.Record{
		Type:      attestation.TypeName,
		Issuer:    issuer,
		Predicate: "pub.polis.attestation.same-as",
		Subject:   attestation.Subject{Type: "identity", ID: "https://alice.other.example"},
		Asserted:  "2026-01-15T10:00:00Z",
		Generator: "polis-cli-go/test",
	}
	canonical, err := attestation.CanonicalJSON(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Version = attestation.ContentVersion(canonical)
	if r.Signature, err = signing.SignContent(canonical, b.priv); err != nil {
		t.Fatal(err)
	}
	rel := "content/pub.polis.core/attestation/" + attestation.ID(r) + ".json"
	b.write(rel, mustJSON(r))
	b.appendIndex(t, map[string]string{
		"type": "attestation", "path": rel, "title": r.Predicate, "published": r.Asserted, "current_version": r.Version,
	})
	return rel
}

func assertFormsAgree(t *testing.T, dir, url string) {
	t.Helper()
	local := RunLocalWith(dir, nil)
	remote := NewRemote()
	remote.DSKeys = nil
	rep := remote.RunSite(url)
	if local.FatalError != "" || rep.FatalError != "" {
		t.Fatalf("a run could not start: local=%q remote=%q", local.FatalError, rep.FatalError)
	}
	for _, d := range CompareForms(local, rep) {
		t.Error(d)
	}
}

func TestTheTwoFormsAgreeOnACleanSite(t *testing.T) {
	b, ts, _ := servedSite(t)
	assertFormsAgree(t, b.dir, ts.URL)
}

// Each entry type tampered in turn, AFTER it was indexed and signed.
func TestTheTwoFormsAgreeOnATamperedSite(t *testing.T) {
	tamper := map[string]func(t *testing.T, b *siteBuilder, rel string){
		"post": func(t *testing.T, b *siteBuilder, rel string) {
			appendTo(t, b, rel, "\nappended after signing\n")
		},
		"comment": func(t *testing.T, b *siteBuilder, rel string) {
			appendTo(t, b, rel, "\nappended after signing\n")
		},
		"tag": func(t *testing.T, b *siteBuilder, rel string) {
			var tf tag.TagFile
			readJSON(t, b, rel, &tf)
			tf.Targets = append(tf.Targets, tag.TagTarget{URI: "https://alice.example/added-after.md"})
			b.write(rel, mustJSON(tf))
		},
		"attestation": func(t *testing.T, b *siteBuilder, rel string) {
			var r attestation.Record
			readJSON(t, b, rel, &r)
			r.Predicate = "pub.polis.attestation.integrity"
			b.write(rel, mustJSON(r))
		},
	}
	for typ, fn := range tamper {
		t.Run(typ, func(t *testing.T) {
			b, ts, paths := servedSite(t)
			fn(t, b, paths[typ])
			assertFormsAgree(t, b.dir, ts.URL)
		})
	}
}

// ⛔ The self-maintaining half: a check nobody declared fails, exactly as
// TestEveryIndexContributorHasARule fails for an undeclared entry type.
func TestAnUndeclaredCheckFailsTheComparison(t *testing.T) {
	local := &Report{}
	local.pass("content.brand_new", FamilyContent, "", 1)
	remote := &Report{}
	remote.pass("content.brand_new", FamilyContent, "", 1)
	got := CompareForms(local, remote)
	if len(got) == 0 || !strings.Contains(strings.Join(got, "\n"), "content.brand_new: not declared") {
		t.Fatalf("an undeclared check must fail the comparison; got %q", got)
	}
}

// Part 2, asserted directly: a form that cannot run a check and says PASS is
// the defect, even when nothing else differs.
func TestAPassFromAFormThatCannotRunTheCheckFails(t *testing.T) {
	local := &Report{}
	local.pass("identity.key_perms", FamilyIdentity, "", 1)
	remote := &Report{}
	remote.pass("identity.key_perms", FamilyIdentity, "", 1)
	got := CompareForms(local, remote)
	if len(got) == 0 || !strings.Contains(strings.Join(got, "\n"), "must say NOT CHECKED") {
		t.Fatalf("a remote PASS on a local-only check must fail; got %q", got)
	}
}

func TestEveryOneFormCheckSaysWhy(t *testing.T) {
	for id, reach := range formParity {
		if !reach.local && !reach.remote {
			t.Errorf("%s: declared as run by neither form", id)
		}
		if (reach.local != reach.remote) && reach.why == "" {
			t.Errorf("%s: runs in one form only and gives no reason", id)
		}
	}
}

func appendTo(t *testing.T, b *siteBuilder, rel, s string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(b.dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	b.write(rel, append(data, s...))
}

func readJSON(t *testing.T, b *siteBuilder, rel string, v interface{}) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(b.dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatal(err)
	}
}
