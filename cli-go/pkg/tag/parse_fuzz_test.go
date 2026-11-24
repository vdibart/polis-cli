package tag

import (
	"encoding/json"
	"testing"
)

// A signed tag file is fetched from other people's sites, so Parse reads bytes
// nobody here wrote. It must never panic, and what it accepts must survive
// being written back out and read again (tolerant reading must not produce a
// value its own writer cannot express).
func FuzzTagParse(f *testing.F) {
	for _, s := range []string{goldenTag, `{}`, `null`, `[]`} {
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
