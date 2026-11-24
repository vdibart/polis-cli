package connection_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/connection"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/sitecheck"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

const base = "https://alice.example.com"

func newSite(t *testing.T) connection.Config {
	t.Helper()
	dir := t.TempDir()
	if _, err := site.Init(storage.At(dir), site.InitOptions{BaseURL: base, SiteTitle: "alice"}); err != nil {
		t.Fatalf("site.Init: %v", err)
	}
	key, err := os.ReadFile(filepath.Join(dir, ".polis", "keys", "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	return connection.Config{Site: storage.At(dir), BaseURL: base, PrivateKey: key}
}

// ⭐ A connection's grant is the owner's signed record, naming the scopes, and
// it resolves as live until withdrawn; then never again.
func TestAGrantIsLiveUntilWithdrawn(t *testing.T) {
	cfg := newSite(t)
	got, err := connection.Issue(cfg, "Claude Desktop", "Claude Desktop", []string{"read", "publish", "render"})
	if err != nil {
		t.Fatal(err)
	}
	live, err := connection.Resolve(cfg.Site, got.URL)
	if err != nil || !live.Valid() {
		t.Fatalf("fresh grant not live: %v %+v", err, live)
	}
	if live.Agent() != "Claude Desktop" || live.Behaviours() != "agent/1" || live.Basis() != attestation.CustodyBasisUserSigned {
		t.Errorf("grant = %s %s %s", live.Agent(), live.Behaviours(), live.Basis())
	}
	if s := strings.Join(live.Scopes(), " "); s != "read publish appearance" {
		t.Errorf("scopes = %q (render must be stored as appearance)", s)
	}
	if err := connection.Withdraw(cfg, got.ID); err != nil {
		t.Fatal(err)
	}
	if live, _ := connection.Resolve(cfg.Site, got.URL); live != nil {
		t.Fatalf("withdrawn grant still resolves: %+v", live)
	}
	if err := connection.Withdraw(cfg, got.ID); err != nil {
		t.Errorf("withdrawing twice should be quiet: %v", err)
	}
}

// ⛔ What an agent may never be granted, and names that would not survive
// being written raw into signed frontmatter.
func TestIssueRefuses(t *testing.T) {
	cfg := newSite(t)
	for _, scopes := range [][]string{{"full"}, {"tag"}, {"read", "tag"}, {"nonsense"}, {}} {
		if _, err := connection.Issue(cfg, "x", "", scopes); err == nil {
			t.Errorf("scopes %v accepted", scopes)
		}
	}
	for _, name := range []string{"", " lead", "a: b", "a#b", "line\nbreak", strings.Repeat("a", 65)} {
		if _, err := connection.Issue(cfg, name, "", []string{"read"}); err == nil {
			t.Errorf("name %q accepted", name)
		}
	}
}

// ⛔ Only the grant the token points at counts: another connection's grant,
// or Rosie's, never stands in for it.
func TestResolveIsThatGrantOnly(t *testing.T) {
	cfg := newSite(t)
	a, _ := connection.Issue(cfg, "A", "", []string{"read"})
	b, _ := connection.Issue(cfg, "B", "", []string{"read"})
	_ = connection.Withdraw(cfg, a.ID)
	if live, _ := connection.Resolve(cfg.Site, a.URL); live != nil {
		t.Error("a withdrawn grant resolved because another is live")
	}
	if live, _ := connection.Resolve(cfg.Site, b.URL); !live.Valid() {
		t.Error("b should be live")
	}
	if live, _ := connection.Resolve(cfg.Site, base+"/content/pub.polis.core/attestation/nope.json"); live != nil {
		t.Error("an unknown grant URL resolved")
	}
}

// ⭐ A post and a comment written under a grant carry agent: and grant:
// INSIDE the signature: they verify, and the signing base contains both.
func TestMarkedPostsAndCommentsVerify(t *testing.T) {
	cfg := newSite(t)
	g, err := connection.Issue(cfg, "Claude Desktop", "", []string{"publish", "comment"})
	if err != nil {
		t.Fatal(err)
	}
	live, _ := connection.Resolve(cfg.Site, g.URL)
	marker := ops.MarkerOf(live)

	res, err := publish.PublishPostAs(cfg.Site, "# Hello\n\nFrom an agent.", "", cfg.PrivateKey, marker)
	if err != nil {
		t.Fatal(err)
	}
	post, _ := cfg.Site.Store().Get(t.Context(), res.Path)
	assertMarkedAndValid(t, cfg, string(post), signing.TypePost, g.URL)

	res2, err := publish.RepublishPostAs(cfg.Site, res.Path, "# Hello\n\nEdited by an agent.", cfg.PrivateKey, marker)
	if err != nil {
		t.Fatal(err)
	}
	post2, _ := cfg.Site.Store().Get(t.Context(), res2.Path)
	assertMarkedAndValid(t, cfg, string(post2), signing.TypePost, g.URL)

	signed, err := comment.SignComment(cfg.Site, &comment.CommentDraft{
		InReplyTo: "https://bob.example.com/content/pub.polis.core/post/20260101/x.md",
		Content:   "A reply.", Agent: marker.Agent, Grant: marker.Grant,
	}, "alice.example.com", base, cfg.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	var pending []byte
	_ = filepath.WalkDir(storeDir(t, cfg), func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Name() == signed.Meta.ID+".md" {
			pending, _ = os.ReadFile(p)
		}
		return nil
	})
	assertMarkedAndValid(t, cfg, string(pending), signing.TypeComment, g.URL)

	// ⛔ The owner's own post carries no marker at all.
	own, _ := publish.PublishPost(cfg.Site, "# Mine", "", cfg.PrivateKey)
	ownBytes, _ := cfg.Site.Store().Get(t.Context(), own.Path)
	if strings.Contains(string(ownBytes), "\nagent:") || strings.Contains(string(ownBytes), "\ngrant:") {
		t.Errorf("owner's post carries a marker:\n%s", ownBytes)
	}
}

func assertMarkedAndValid(t *testing.T, cfg connection.Config, content string, typ signing.ObjectType, grantURL string) {
	t.Helper()
	if !strings.Contains(content, "\nagent: Claude Desktop\n") || !strings.Contains(content, "\ngrant: "+grantURL+"\n") {
		t.Fatalf("marker missing:\n%s", content)
	}
	b := signing.MarkdownSigningBase(content, typ)
	if !strings.Contains(string(b), "agent: Claude Desktop") || !strings.Contains(string(b), "grant: "+grantURL) {
		t.Errorf("marker is not inside the signing base")
	}
	pub, err := os.ReadFile(filepath.Join(storeDir(t, cfg), ".polis", "keys", "id_ed25519.pub"))
	if err != nil {
		t.Fatal(err)
	}
	if r := sitecheck.VerifyContent(content, pub, typ); !r.OK() || r.Signature != sitecheck.SigValid {
		t.Errorf("marked content does not verify: %+v", r)
	}
}

func storeDir(t *testing.T, cfg connection.Config) string {
	t.Helper()
	p, err := cfg.Site.Store().RealPath(".")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// ⛔ A name YAML would read as a bool, null or number is refused.
func TestNamesReadAsStrings(t *testing.T) {
	for _, n := range []string{"null", "True", "yes", "off", "123", "1.5", "~"} {
		if connection.ValidName(n) {
			t.Errorf("%q accepted", n)
		}
	}
	for _, n := range []string{"Claude Desktop", "agent 2", "rosie"} {
		if !connection.ValidName(n) {
			t.Errorf("%q refused", n)
		}
	}
}

// ⭐ A memo answers repeat checks within one request, and only that request:
// a fresh context sees a withdrawal at once.
func TestResolveMemoIsPerRequest(t *testing.T) {
	cfg := newSite(t)
	got, err := connection.Issue(cfg, "Claude Desktop", "Claude Desktop", []string{"read"})
	if err != nil {
		t.Fatal(err)
	}
	req := connection.WithResolveMemo(context.Background())
	if live, _ := connection.ResolveIn(req, cfg.Site, got.URL); live == nil {
		t.Fatal("fresh grant not live")
	}
	if err := connection.Withdraw(cfg, got.ID); err != nil {
		t.Fatal(err)
	}
	if live, _ := connection.ResolveIn(req, cfg.Site, got.URL); live == nil {
		t.Error("the memo re-read within its request")
	}
	if live, _ := connection.ResolveIn(context.Background(), cfg.Site, got.URL); live != nil {
		t.Error("without a memo, a withdrawn grant still resolves")
	}
	if live, _ := connection.ResolveIn(connection.WithResolveMemo(context.Background()), cfg.Site, got.URL); live != nil {
		t.Error("a new request's memo saw the old answer")
	}
}
