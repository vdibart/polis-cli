package tag

import (
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// TestCanonicalJSON_HTMLEscaping pins Go's `encoding/json` HTML escaping into
// the specification of a tag file's signing base — Signet epic 08, D3.
//
// ⛔ THIS BEHAVIOUR HAS NEVER RUN IN PRODUCTION. Swept 2026-09-04: not one
// signed artifact on the fleet contains an escaped sequence, so the first
// tagged URI containing `&`, `<` or `>` is the first time it executes.
//
// ⭐ `&` is not a JSON metacharacter and no other language's serialiser escapes
// it. Go does, and the escaping is INSIDE the bytes that were hashed — so a
// second implementation writing a raw `&` produces a different digest and
// concludes our signature is invalid. The full statement lives in
// docs/signet/spec/signing-base.md; this test is what stops the statement from
// going stale. See pkg/license/escaping_test.go for the same pin on the licence.
func TestCanonicalJSON_HTMLEscaping(t *testing.T) {
	tf := &TagFile{
		Tag: "reading",
		Targets: []TagTarget{{
			URI:   "https://x.example/p?a=1&b=<2>",
			Added: "2026-09-04T10:00:00Z",
		}},
		Created:   "2026-09-04T10:00:00Z",
		Updated:   "2026-09-04T10:00:00Z",
		Generator: "polis-cli-go/test",
	}

	canonical, err := canonicalJSON(tf)
	if err != nil {
		t.Fatalf("canonicalJSON: %v", err)
	}
	assertEscaped(t, string(canonical))

	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	sig, err := signing.SignContent(canonical, priv)
	if err != nil {
		t.Fatalf("SignContent: %v", err)
	}
	tf.Signature = sig
	status, err := Verify(tf, pub)
	if status != StatusValid {
		t.Fatalf("a tag file whose targets contain & < > must verify: %s (%v)", status, err)
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
