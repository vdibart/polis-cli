package signing_test

import (
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// TestCanonicalizeContent_MatchesSigners locks signing.CanonicalizeContent to the
// SIGNERS' byte output. publish/comment.CanonicalizeContent produced every on-disk
// signature; the shared verifier canonicalizer (signing.CanonicalizeContent, used
// by verify/judge/patrol) MUST match them byte-for-byte or signature
// reconstruction hashes the wrong bytes. External test package so it can import
// publish+comment (which import signing) without a cycle.
func TestCanonicalizeContent_MatchesSigners(t *testing.T) {
	cases := []string{
		"",
		"hello\n",
		"hello",
		"\n\n\nleading blanks\n",
		"trailing spaces   \nand tabs\t\t\n",
		"crlf\r\nline\r\nendings\r\n",
		"cr\ronly\rlines\r",
		"trailing\nblank\nlines\n\n\n",
		"---\ntitle: X\n---\n\nbody text\n",
		"  \n\t\nmixed\r\n  trailing \t\n\n",
	}
	for _, in := range cases {
		got := signing.CanonicalizeContent(in)
		if want := publish.CanonicalizeContent(in); got != want {
			t.Errorf("signing != publish for %q:\n  signing=%q\n  publish=%q", in, got, want)
		}
		if want := comment.CanonicalizeContent(in); got != want {
			t.Errorf("signing != comment for %q:\n  signing=%q\n  comment=%q", in, got, want)
		}
	}
}
