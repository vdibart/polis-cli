package tailor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Signet epic 37 D2. checkIndexRebuild used to early-return when the posts
// directory was absent, so a site holding only tags or attestations — the
// operator site is one — never had them indexed and never self-healed.
func TestCheckIndexRebuild_IndexesASiteWithNoPosts(t *testing.T) {
	siteDir := t.TempDir()
	core := filepath.Join(siteDir, "content", "pub.polis.core")
	if err := os.MkdirAll(filepath.Join(core, "attestation"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(core, "attestation", "20260101T000000Z-abcd.json"),
		[]byte(`{"type":"pub.polis.attestation","predicate":"pub.polis.attestation.agent-disclosure","asserted":"2026-01-01T00:00:00Z","current_version":"sha256:dddd"}`), 0644); err != nil {
		t.Fatalf("write attestation: %v", err)
	}
	// The operator site's state: an index that exists and is empty.
	if err := os.WriteFile(filepath.Join(core, "index.jsonl"), nil, 0644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	if r := checkIndexRebuild(&runContext{siteDir: siteDir, dryRun: true}); r.Status != StatusFail {
		t.Fatalf("dry run: status = %v (%q), want fail — the attestation is not indexed", r.Status, r.Message)
	}
	if r := checkIndexRebuild(&runContext{siteDir: siteDir, dryRun: false}); r.Status != StatusFail {
		t.Fatalf("apply: status = %v (%q), want fail (a rebuild happened)", r.Status, r.Message)
	}

	data, err := os.ReadFile(filepath.Join(core, "index.jsonl"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if !strings.Contains(string(data), `"type":"attestation"`) {
		t.Errorf("the attestation was not indexed:\n%s", data)
	}

	if r := checkIndexRebuild(&runContext{siteDir: siteDir, dryRun: true}); r.Status != StatusPass {
		t.Errorf("after apply: status = %v (%q), want pass — the heal must converge", r.Status, r.Message)
	}
}

// With nothing to index, the empty-index behaviour the posts-dir branch used to
// provide is unchanged.
func TestCheckIndexRebuild_EmptySiteStillGetsAnEmptyIndex(t *testing.T) {
	siteDir := t.TempDir()
	indexPath := filepath.Join(siteDir, "content", "pub.polis.core", "index.jsonl")

	if r := checkIndexRebuild(&runContext{siteDir: siteDir, dryRun: false}); r.Message != "Created empty content index" {
		t.Fatalf("message = %q, want the empty-index creation", r.Message)
	}
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("index not created: %v", err)
	}
	if r := checkIndexRebuild(&runContext{siteDir: siteDir, dryRun: true}); r.Status != StatusPass {
		t.Errorf("status = %v (%q), want pass", r.Status, r.Message)
	}
}
