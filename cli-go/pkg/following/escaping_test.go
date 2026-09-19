package following

import (
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// TestCanonicalJSON_HTMLEscaping pins Go's `encoding/json` HTML escaping into
// the specification of the follow file's signing base — Signet epic 08, D3.
//
// ⛔ THIS BEHAVIOUR HAS NEVER RUN IN PRODUCTION. Swept 2026-09-04: not one
// signed artifact on the fleet contains an escaped sequence, so the first
// followed URL containing `&`, `<` or `>` — a query string on a permalink is the first time it executes.
//
// ⭐ `&` is not a JSON metacharacter and no other language's serialiser escapes
// it. Go does, and the escaping is INSIDE the bytes that were hashed — so a
// second implementation writing a raw `&` produces a different digest and
// concludes our signature is invalid. The full statement lives in
// docs/signet/spec/signing-base.md; this test is what stops the statement from
// going stale. See pkg/license/escaping_test.go for the same pin on the licence.
func TestCanonicalJSON_HTMLEscaping(t *testing.T) {
	f := &FollowingFile{
		Version: "polis-cli-go/test",
		Following: []FollowingEntry{{
			URL:        "https://x.example/?a=1&b=2",
			AddedAt:    "2026-09-04T10:00:00Z",
			SiteTitle:  "Cats & <Dogs>",
			AuthorName: "A & B",
		}},
	}

	canonical, err := canonicalJSON(f)
	if err != nil {
		t.Fatalf("canonicalJSON: %v", err)
	}
	assertEscaped(t, string(canonical))

	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	if err := Sign(f, priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	status, err := Verify(f, pub)
	if status != StatusValid {
		t.Fatalf("a follow file whose entries contain & < > must verify: %s (%v)", status, err)
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
