package discovery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheWitnessSpecQuotesTheGoldenBytesVerbatim keeps docs/signet/spec/witness.md
// honest: a second implementation is built from that page, so the canonical
// strings it quotes must be exactly the ones both implementations are tested
// against. A spec example transcribed by hand drifts; this one cannot.
func TestTheWitnessSpecQuotesTheGoldenBytesVerbatim(t *testing.T) {
	root := repoRoot(t)
	spec, err := os.ReadFile(filepath.Join(root, "docs", "signet", "spec", "witness.md"))
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "discovery-service", "core", "contract-fixtures", "witness-canonical.json"))
	if err != nil {
		skipWithoutDS(t)
		t.Fatalf("read fixture: %v", err)
	}
	var fixture struct {
		Cases []struct {
			Name      string `json:"name"`
			Canonical string `json:"canonical"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, c := range fixture.Cases {
		if !strings.Contains(string(spec), c.Canonical) {
			t.Errorf("docs/signet/spec/witness.md does not quote the %s canonical bytes verbatim:\n%s", c.Name, c.Canonical)
		}
	}
}
