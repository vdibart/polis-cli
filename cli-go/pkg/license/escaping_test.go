package license

import (
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// TestCanonicalJSON_HTMLEscaping pins Go's `encoding/json` HTML escaping into
// the specification of the licence signing base — Signet epic 08, D3.
//
// ⛔ THIS BEHAVIOUR HAS NEVER RUN IN PRODUCTION. Swept 2026-09-04: not one
// signed artifact anywhere on the fleet — licence, follow, tag, blessing or
// attestation — contains an escaped sequence, because no value has yet held
// `&`, `<` or `>`. The first licence with an `&` in its `terms` URL
// (`?ref=a&b`, a query string, a campaign parameter) is the first time it
// executes, and it would do so unverified by anything.
//
// ⭐ It matters because it is the thing a reimplementer gets wrong. `&` is not
// a JSON metacharacter and no other language's serialiser escapes it — Go does,
// unless you explicitly disable it, and the escaping is INSIDE the bytes that
// were hashed. A second implementation that writes `?ref=a&b` produces a
// different digest and concludes our signature is invalid.
//
// ⛔ Do not "fix" this to unescaped output. It would change the signing base
// for every future artifact and split the corpus in two.
func TestCanonicalJSON_HTMLEscaping(t *testing.T) {
	f := &File{
		Type: TypeName,
		Terms: &Terms{
			V:           SchemaVersion,
			Profile:     ProfileReserved,
			TrainAI:     Disallow,
			Search:      Allow,
			Attribution: AttributionRequired,
			Terms:       "https://x.example/license?ref=a&b&lt=<x>",
			Contact:     "https://x.example/license?a=1&b=2",
			Asserted:    "2026-09-04T10:00:00Z",
		},
		Created:   "2026-09-04T10:00:00Z",
		Updated:   "2026-09-04T10:00:00Z",
		Generator: "polis-cli-go/test",
	}

	canonical, err := canonicalJSON(f)
	if err != nil {
		t.Fatalf("canonicalJSON: %v", err)
	}
	got := string(canonical)

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

	// And the escaped bytes are the bytes that get signed: a round trip over a
	// value that triggers escaping must still verify.
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	sig, err := signing.SignContent(canonical, priv)
	if err != nil {
		t.Fatalf("SignContent: %v", err)
	}
	f.Signature = sig
	ok, err := Verify(f, pub)
	if err != nil || !ok {
		t.Fatalf("a licence whose terms URL contains & < > must verify: ok=%v err=%v", ok, err)
	}
}
