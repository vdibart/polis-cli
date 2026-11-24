package site

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// .well-known/polis is the first thing fetched from any site, so every reader
// of the network parses it. Decoding must never panic, and (because WellKnown
// preserves the members it does not model, and a rewrite must not lose them)
// a decoded document must re-encode stably: encode, decode, encode gives the
// same bytes. Seeded from the testdata documents (bash, legacy, rotated,
// corrupt, every-member, future).
func FuzzWellKnownDecode(f *testing.F) {
	paths, _ := filepath.Glob(filepath.Join("testdata", "*wellknown.json"))
	for _, p := range paths {
		if b, err := os.ReadFile(p); err == nil {
			f.Add(b)
		}
	}
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		var wk WellKnown
		if err := json.Unmarshal(raw, &wk); err != nil {
			return
		}
		first, err := json.Marshal(&wk)
		if err != nil {
			return
		}
		var again WellKnown
		if err := json.Unmarshal(first, &again); err != nil {
			t.Fatalf("a decoded .well-known/polis re-encodes to %q, which does not decode: %v", first, err)
		}
		second, err := json.Marshal(&again)
		if err != nil {
			t.Fatalf("re-encoding failed the second time: %v", err)
		}
		if !bytes.Equal(first, second) {
			t.Fatalf("re-encoding is not stable:\n%s\n%s", first, second)
		}
	})
}
