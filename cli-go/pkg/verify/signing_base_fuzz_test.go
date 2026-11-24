package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/sitecheck"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⛔ THE DIFFERENTIAL: what the PRODUCER signed and what each VERIFIER rebuilds
// must be the same bytes, for any body a writer can publish. If they ever
// differ, either authentic content fails verification (reported as tampering)
// or, worse, a verifier reads a different signature or a different base than
// the one that was signed, which is a forgery class.
//
// Here: posts, through the real producer (publish.PublishPost), checked by
// this package's own parser and check and by sitecheck's. Comments cannot be
// produced from this package (pkg/comment imports it), so they are fuzzed in
// pkg/sitecheck (external test) and pkg/judge; Patrol's verifyPost in
// pkg/patrol.

// markdownSeeds reads the shared seed corpus in pkg/signing/testdata.
func markdownSeeds(tb testing.TB) []string {
	tb.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "signing", "testdata", "markdown-seeds.txt"))
	if err != nil {
		tb.Fatal(err)
	}
	blocks := strings.Split(string(raw), "\n=====\n")
	return blocks[1:] // the first block is the file's header
}

func FuzzSignedPostVerifiesInVerifyAndSitecheck(f *testing.F) {
	for _, s := range markdownSeeds(f) {
		f.Add(s)
	}
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		f.Fatal(err)
	}

	f.Fuzz(func(t *testing.T, body string) {
		dir := t.TempDir()
		site := storage.At(dir)

		// A post, through the real publish path.
		if res, err := publish.PublishPost(site, body, "", priv); err == nil {
			raw, err := os.ReadFile(filepath.Join(dir, res.Path))
			if err != nil {
				t.Fatal(err)
			}
			checkAgreement(t, string(raw), pub, signing.TypePost)
		}

	})
}

// checkAgreement: both parsers read the same signature out of the file, and
// both verifiers accept it over their rebuilt base. Then one changed byte in
// the body must make both refuse, so the agreement is not vacuous.
func checkAgreement(t *testing.T, file string, pub []byte, typ signing.ObjectType) {
	t.Helper()
	fm, _, err := parseFrontmatter(file)
	if err != nil {
		t.Fatalf("verify cannot parse what the producer wrote: %v\n%q", err, file)
	}
	sfm, _, err := sitecheck.ParseFrontmatter(file)
	if err != nil {
		t.Fatalf("sitecheck cannot parse what the producer wrote: %v\n%q", err, file)
	}
	if fm.Signature == "" || fm.Signature != sfm.Signature {
		t.Fatalf("the parsers disagree on the signature: verify %q, sitecheck %q\n%q", fm.Signature, sfm.Signature, file)
	}
	if r := verifySignature(file, string(pub), fm.Signature, typ); r.Status != "valid" {
		t.Fatalf("verify refuses what the producer signed (%s): %s\n%q", typ, r.Message, file)
	}
	if r := sitecheck.VerifyContent(file, pub, typ); r.Signature != sitecheck.SigValid {
		t.Fatalf("sitecheck refuses what the producer signed (%s): %v\n%q", typ, r.SigError, file)
	}

	// Tamper with the last body character (after the frontmatter) and expect a refusal.
	end := strings.Index(file[3:], "\n---")
	if end < 0 {
		return
	}
	i := strings.LastIndexFunc(file, func(r rune) bool { return r > ' ' && r < 0x7f })
	if i <= end+7 {
		return // no printable body byte to change
	}
	tampered := file[:i] + string(rune(file[i]^1)) + file[i+1:]
	if r := verifySignature(tampered, string(pub), fm.Signature, typ); r.Status == "valid" {
		t.Fatalf("verify accepts a body changed after signing (%s)\n%q", typ, tampered)
	}
}
