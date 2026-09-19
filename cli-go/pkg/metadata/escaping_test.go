package metadata

import (
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// TestCanonicalBlessedJSON_HTMLEscaping pins Go's `encoding/json` HTML escaping into
// the specification of the blessing list's signing base — Signet epic 08, D3.
//
// ⛔ THIS BEHAVIOUR HAS NEVER RUN IN PRODUCTION. Swept 2026-09-04: not one
// signed artifact on the fleet contains an escaped sequence, so the first
// post or comment URL containing `&`, `<` or `>` is the first time it executes.
//
// ⭐ `&` is not a JSON metacharacter and no other language's serialiser escapes
// it. Go does, and the escaping is INSIDE the bytes that were hashed — so a
// second implementation writing a raw `&` produces a different digest and
// concludes our signature is invalid. The full statement lives in
// docs/signet/spec/signing-base.md; this test is what stops the statement from
// going stale. See pkg/license/escaping_test.go for the same pin on the licence.
func TestCanonicalBlessedJSON_HTMLEscaping(t *testing.T) {
	bc := &BlessedComments{
		Version: "polis-cli-go/test",
		Comments: []PostComments{{
			Post: "https://x.example/p?a=1&b=2",
			Blessed: []BlessedComment{{
				URL:       "https://who.example/c?ref=<x>&y",
				Version:   "sha256:abc",
				BlessedAt: "2026-09-04T10:00:00Z",
			}},
		}},
	}

	canonical, err := canonicalBlessedJSON(bc)
	if err != nil {
		t.Fatalf("canonicalBlessedJSON: %v", err)
	}
	assertEscaped(t, string(canonical))

	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	if err := SignBlessed(bc, priv); err != nil {
		t.Fatalf("SignBlessed: %v", err)
	}
	status, err := VerifyBlessed(bc, pub)
	if status != StatusValid {
		t.Fatalf("a blessing list whose URLs contain & < > must verify: %s (%v)", status, err)
	}
}

// assertEscaped is the shared assertion: every `&`, `<` and `>` appears as its
// \uXXXX escape and none appears raw.
func assertEscaped(t *testing.T, got string) {
	t.Helper()
	for _, want := range []string{`\u0026`, `\u003c`, `\u003e`} {
		if !strings.Contains(got, want) {
			t.Errorf("canonical bytes do not contain %s — the escaping rule changed:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"&", "<", ">"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("canonical bytes contain a raw %q; every one must be escaped:\n%s", unwanted, got)
		}
	}
}
