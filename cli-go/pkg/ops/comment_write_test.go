package ops

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

func commentSite(t *testing.T) CommentSite {
	t.Helper()
	dir := t.TempDir()
	if _, err := site.Init(storage.At(dir), site.InitOptions{BaseURL: "https://maya.example"}); err != nil {
		t.Fatal(err)
	}
	priv, err := os.ReadFile(filepath.Join(dir, ".polis", "keys", "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	return CommentSite{Site: storage.At(dir), PrivateKey: priv, BaseURL: "https://maya.example"}
}

// ⭐ Every caller signs a comment the same way: the site's domain as its
// author, this build's generator, the post's .md address.
func TestACommentIsSignedTheSameWayWhoeverSignsIt(t *testing.T) {
	cs := commentSite(t)
	signed, err := SignComment(cs, &comment.CommentDraft{InReplyTo: "https://alice.example/posts/20261121/x.html", Content: "Agreed."})
	if err != nil {
		t.Fatal(err)
	}
	if signed.Meta.Author != "maya.example" {
		t.Errorf("author = %q, want the site's domain", signed.Meta.Author)
	}
	if signed.Meta.InReplyTo != "https://alice.example/posts/20261121/x.md" {
		t.Errorf("in_reply_to = %q, want the post's .md source", signed.Meta.InReplyTo)
	}
	raw, err := os.ReadFile(cs.Site.Path(".polis", "bundles", "pub.polis.core", "comments", comment.StatusPending, signed.Meta.ID+".md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "generator: "+comment.GetGenerator()) {
		t.Errorf("the comment does not carry this build's generator %q:\n%s", comment.GetGenerator(), raw)
	}
}

func TestACommentThatCannotBeWrittenIsRefusedBeforeSigning(t *testing.T) {
	cs := commentSite(t)
	for name, d := range map[string]*comment.CommentDraft{
		"no post":   {Content: "x"},
		"no text":   {InReplyTo: "https://alice.example/posts/a.md", Content: "  "},
		"too large": {InReplyTo: "https://alice.example/posts/a.md", Content: strings.Repeat("x", maxCommentMarkdown+1)},
	} {
		if _, err := SignComment(cs, d); !errors.Is(err, ErrCommentInvalid) {
			t.Errorf("%s: err = %v, want ErrCommentInvalid", name, err)
		}
	}
	cs.BaseURL = ""
	if _, err := SignComment(cs, &comment.CommentDraft{InReplyTo: "https://a.example/p.md", Content: "x"}); err == nil {
		t.Error("a site with no address signed a comment")
	}
}

// A comment that signs but cannot be sent is answered the same way on every
// path: sent false, the reason, and the comment kept (resendable by its id).
func TestACommentNotSentIsAnsweredTheSameWay(t *testing.T) {
	f := CommentSentFields(CommentSent{Err: errors.New("discovery service down")}, map[string]any{"comment": "kept"})
	if f["sent"] != false || f["send_error"] != "discovery service down" || f["status"] != nil || f["comment"] != "kept" {
		t.Errorf("fields = %v", f)
	}
	f = CommentSentFields(CommentSent{Sent: true, Status: "pending"}, nil)
	if f["sent"] != true || f["status"] != "pending" || f["send_error"] != nil {
		t.Errorf("fields = %v", f)
	}
}
