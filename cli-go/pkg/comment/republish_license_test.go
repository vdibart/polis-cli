package comment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// licensedCommenter initialises a site with the given licence choice and
// returns its directory and keys.
func licensedCommenter(t *testing.T, choice string) (dir string, priv, pub []byte) {
	t.Helper()
	dir = t.TempDir()
	if _, err := site.Init(storage.At(dir), site.InitOptions{License: choice, BaseURL: "https://bob.example"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	var err error
	if priv, err = os.ReadFile(filepath.Join(dir, ".polis", "keys", "id_ed25519")); err != nil {
		t.Fatal(err)
	}
	if pub, err = os.ReadFile(filepath.Join(dir, ".polis", "keys", "id_ed25519.pub")); err != nil {
		t.Fatal(err)
	}
	return dir, priv, pub
}

func publishLicensedComment(t *testing.T, dir string, priv []byte) string {
	t.Helper()
	signed, err := SignComment(storage.At(dir), &CommentDraft{
		InReplyTo: "https://alice.example/posts/20260101/hello.md",
		Content:   "First take.",
	}, "bob.example", "https://bob.example", priv)
	if err != nil {
		t.Fatalf("SignComment: %v", err)
	}
	if err := PublishComment(storage.At(dir), signed.Meta.ID); err != nil {
		t.Fatalf("PublishComment: %v", err)
	}
	ts, _ := time.Parse("2006-01-02T15:04:05Z", signed.Meta.Timestamp)
	return filepath.ToSlash(filepath.Join("content", "pub.polis.core", "comment", ts.Format("20060102"), signed.Meta.ID+".md"))
}

func verifyComment(t *testing.T, raw string, pub []byte) {
	t.Helper()
	sig := ParseFrontmatter(raw)["signature"]
	if sig == "" {
		t.Fatal("comment carries no signature")
	}
	var lines []string
	for len(sig) > 76 {
		lines, sig = append(lines, sig[:76]), sig[76:]
	}
	lines = append(lines, sig)
	pem := "-----BEGIN SSH SIGNATURE-----\n" + strings.Join(lines, "\n") + "\n-----END SSH SIGNATURE-----"
	base := signing.MarkdownSigningBase(raw, signing.TypeComment)
	ok, err := signing.VerifySignature([]byte(base), pub, pem)
	if err != nil || !ok {
		t.Fatalf("edited comment does not verify (err %v) over:\n%s", err, base)
	}
}

// An edited comment keeps the licence block it was published under, byte for
// byte, even after the site's terms change (terms are not retroactive), and
// the new version verifies with the block inside its signature.
func TestRepublishCommentKeepsTheOriginalLicenceBlock(t *testing.T) {
	dir, priv, pub := licensedCommenter(t, "reserved")
	path := publishLicensedComment(t, dir, priv)

	before, err := os.ReadFile(filepath.Join(dir, path))
	if err != nil {
		t.Fatal(err)
	}
	origBlock := license.ExtractBlock(string(before))
	if !strings.HasPrefix(origBlock, "license:\n") || !strings.Contains(origBlock, "train-ai: n") {
		t.Fatalf("setup: the original comment carries no reserved licence block: %q", origBlock)
	}

	// The commenter opens everything up before editing.
	if _, err := site.StateLicense(storage.At(dir), "open", "https://bob.example", priv); err != nil {
		t.Fatal(err)
	}

	if _, err := RepublishComment(storage.At(dir), path, "Second take, revised.", priv, &DiscoveryConfig{}); err != nil {
		t.Fatalf("RepublishComment: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(dir, path))
	if err != nil {
		t.Fatal(err)
	}
	if got := license.ExtractBlock(string(after)); got != origBlock {
		t.Errorf("edited comment's licence block changed:\n got %q\nwant %q", got, origBlock)
	}
	if !strings.Contains(signing.MarkdownSigningBase(string(after), signing.TypeComment), origBlock) {
		t.Error("the licence block is not inside the edited comment's signing base")
	}
	verifyComment(t, string(after), pub)
}

// A comment published with no terms gains none on edit, and still verifies.
func TestRepublishCommentWithoutTermsGetsNone(t *testing.T) {
	dir, priv, pub := licensedCommenter(t, "none")
	path := publishLicensedComment(t, dir, priv)

	// Terms stated after publishing do not reach back into the comment.
	if _, err := site.StateLicense(storage.At(dir), "reserved", "https://bob.example", priv); err != nil {
		t.Fatal(err)
	}
	if _, err := RepublishComment(storage.At(dir), path, "Second take.", priv, &DiscoveryConfig{}); err != nil {
		t.Fatalf("RepublishComment: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(dir, path))
	if err != nil {
		t.Fatal(err)
	}
	if block := license.ExtractBlock(string(after)); block != "" {
		t.Errorf("a comment published without terms gained a licence block on edit: %q", block)
	}
	verifyComment(t, string(after), pub)
}
