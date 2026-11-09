package provision

import "testing"

// ⛔ A seed never carries credentials the source's owner issued to other
// parties, whatever the source.
func TestSeedLeavesIssuedCredentialsBehind(t *testing.T) {
	for _, key := range []string{".polis/api-keys.json", ".polis/kay/v0-key", ".polis/mcp/calls.jsonl"} {
		if seedCrosses(key) {
			t.Errorf("%s is seeded into the new instance", key)
		}
	}
}
