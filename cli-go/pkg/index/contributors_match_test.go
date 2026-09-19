package index_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/index"
	"github.com/vdibart/polis-cli/cli-go/pkg/tag"
)

// The tag and attestation contributors read their records through local
// structs, because those packages now import this one (epic 37 D1). This holds
// the local spellings to the owning packages, so a renamed JSON key fails here
// instead of silently emptying the index.
func TestContributorsMatchTheirPackages(t *testing.T) {
	c, ok := index.ContributorFor(index.EntryTypeAttestation)
	if !ok || c.TypeName != attestation.TypeName {
		t.Errorf("attestation contributor TypeName = %q, want %q", c.TypeName, attestation.TypeName)
	}

	for _, tc := range []struct {
		name string
		v    any
		keys []string
	}{
		{"attestation.Record", &attestation.Record{Predicate: "p", Asserted: "a", Version: "v"}, []string{"predicate", "asserted", "current_version"}},
		{"tag.TagFile", &tag.TagFile{Tag: "t", Created: "c", Updated: "u", Version: "v"}, []string{"tag", "created", "updated", "current_version"}},
	} {
		data, _ := json.Marshal(tc.v)
		var fields map[string]any
		_ = json.Unmarshal(data, &fields)
		for _, k := range tc.keys {
			if _, ok := fields[k]; !ok {
				t.Errorf("%s no longer marshals %q — the index contributor reads that key", tc.name, k)
			}
		}
	}
}

// A record our writers did not produce can carry values the hosted service's
// index check rejects, and that check fails the WHOLE SITE on one bad line.
// Since epic 37 these entries are written on every issue and healed across the
// fleet, so such a record must cost one skipped entry and nothing more. (That
// the check accepts the result is asserted where the check lives.)
func TestRecordsPatrolWouldRejectAreSkippedNotIndexed(t *testing.T) {
	dataDir := t.TempDir()
	core := filepath.Join(dataDir, "content", "pub.polis.core")
	writeFile(t, filepath.Join(core, "attestation", "hand-written.json"),
		`{"type":"pub.polis.attestation","predicate":"com.example.x","asserted":"yesterday","current_version":"sha256:dddd"}`)
	writeFile(t, filepath.Join(core, "tag", "odd.json"),
		`{"tag":"odd","created":"2026-01-03T00:00:00Z","current_version":"dddd"}`)

	result, err := index.RebuildContentIndex(dataDir, nil)
	if err != nil {
		t.Fatalf("RebuildContentIndex: %v", err)
	}
	if result.Skipped[index.EntryTypeAttestation] != 1 || result.Skipped[index.EntryTypeTag] != 1 {
		t.Errorf("skipped = %v, want one attestation and one tag reported", result.Skipped)
	}
	if result.Total != 0 {
		t.Errorf("total = %d, want 0 lines indexed", result.Total)
	}
	if data, _ := os.ReadFile(filepath.Join(core, "index.jsonl")); len(data) != 0 {
		t.Errorf("index should be empty, got:\n%s", data)
	}
}
