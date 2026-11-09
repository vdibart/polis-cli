package branch

import "testing"

// ⛔ Credentials the origin's owner issued to other parties never cross into a
// branch, with or without the private residue.
func TestIssuedCredentialsStayBehind(t *testing.T) {
	for _, key := range []string{".polis/api-keys.json", ".polis/kay/v0-key", ".polis/kay/state.json", ".polis/mcp/calls.jsonl"} {
		for _, withResidue := range []bool{true, false} {
			if _, left := leftBehind(key, withResidue); !left {
				t.Errorf("%s crosses into the branch (withResidue=%v)", key, withResidue)
			}
		}
	}
}
