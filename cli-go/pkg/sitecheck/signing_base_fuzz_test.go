package sitecheck_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/sitecheck"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⛔ The comment half of the signing-base differential (posts are in
// pkg/verify): a comment signed by the real producer, comment.SignComment, must
// be read and accepted by sitecheck (which Judge and Patrol share for parsing),
// and a changed body must be refused. An external test package, because
// pkg/comment imports this one.
func FuzzSignedCommentVerifiesInSitecheck(f *testing.F) {
	raw, err := os.ReadFile(filepath.Join("..", "signing", "testdata", "markdown-seeds.txt"))
	if err != nil {
		f.Fatal(err)
	}
	for _, s := range strings.Split(string(raw), "\n=====\n")[1:] {
		f.Add(s)
	}
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, body string) {
		dir := t.TempDir()
		for _, d := range []string{".polis/bundles/pub.polis.core/comments/drafts", ".polis/bundles/pub.polis.core/comments/pending", "content/pub.polis.core/comment"} {
			_ = os.MkdirAll(filepath.Join(dir, d), 0o755)
		}
		draft := &comment.CommentDraft{InReplyTo: "https://alice.polis.pub/posts/20260101/hello.md", Content: body}
		signed, err := comment.SignComment(storage.At(dir), draft, "bob.polis.pub", "https://bob.polis.pub", priv)
		if err != nil {
			return // the producer refused the input; nothing was signed
		}
		// The signed file is what goes to disk (the pending store), not
		// signed.Content, which is the body alone.
		matches, _ := filepath.Glob(filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "comments", "pending", signed.Meta.ID+".md"))
		if len(matches) != 1 {
			t.Fatalf("SignComment wrote no pending file for %s", signed.Meta.ID)
		}
		b, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatal(err)
		}
		file := string(b)
		fm, _, err := sitecheck.ParseFrontmatter(file)
		if err != nil || fm.Signature == "" {
			t.Fatalf("sitecheck cannot read the signature the producer wrote (%v)\n%q", err, file)
		}
		if r := sitecheck.VerifyContent(file, pub, signing.TypeComment); r.Signature != sitecheck.SigValid {
			t.Fatalf("sitecheck refuses a comment the producer signed: %v\n%q", r.SigError, file)
		}
		i := strings.LastIndexFunc(file, func(r rune) bool { return r > ' ' && r < 0x7f })
		if close := strings.Index(file[3:], "\n---"); close < 0 || i <= close+7 {
			return
		}
		tampered := file[:i] + string(rune(file[i]^1)) + file[i+1:]
		if r := sitecheck.VerifyContent(tampered, pub, signing.TypeComment); r.Signature == sitecheck.SigValid {
			t.Fatalf("sitecheck accepts a comment changed after signing\n%q", tampered)
		}
	})
}
