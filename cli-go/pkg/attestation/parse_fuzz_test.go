package attestation

import (
	"encoding/json"
	"testing"
)

// A signed attestation is fetched from other people's sites, so Parse reads bytes
// nobody here wrote. It must never panic, and what it accepts must survive
// being written back out and read again (tolerant reading must not produce a
// value its own writer cannot express).
func FuzzAttestationParse(f *testing.F) {
	for _, s := range []string{goldenAttestation, goldenAttestationMinimal, `{}`, `null`, `[]`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		v, err := Parse(raw)
		if err != nil {
			return
		}
		out, err := json.Marshal(v)
		if err != nil {
			return
		}
		if _, err := Parse(out); err != nil {
			t.Fatalf("Parse accepts %q, but not its own re-encoding %q: %v", raw, out, err)
		}
	})
}
