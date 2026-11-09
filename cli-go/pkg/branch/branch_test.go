package branch

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/agent"
	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/sitecheck"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

const (
	originURL    = "https://alice.polis.pub"
	originHost   = "alice.polis.pub"
	targetURL    = "https://alice-two.example.net"
	targetDomain = "alice-two.example.net"
)

// noNetwork fails the test on any outbound HTTP request made through the
// default transport, which every client a branch could reach uses.
func noNetwork(t *testing.T) *int32 {
	t.Helper()
	// ⛔ A fresh home per test: the owner's recovery key goes under
	// ~/.polis/recovery by default, never into the real one.
	t.Setenv("HOME", t.TempDir())
	// The fixture signs everything seconds before the branch; the rotation is
	// dated after it, as it would be for any export made before a branch.
	prevTime := rotationTime
	rotationTime = func() string { return time.Now().UTC().Add(2 * time.Second).Format("2006-01-02T15:04:05Z") }
	t.Cleanup(func() { rotationTime = prevTime })
	var calls int32
	prev := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return nil, fmt.Errorf("network call during a branch: %s", r.URL)
	})
	t.Cleanup(func() { http.DefaultTransport = prev })
	return &calls
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// buildOrigin makes a site with everything a real export carries: a post, the
// signed follow and blessing lists, a licence, Rosie's default grant, a DM
// keyring, a discovery-service registration with state and preferences, logs
// and a .env.
func buildOrigin(t *testing.T) (dir string, k1 []byte) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "origin")
	s := storage.At(dir)
	if _, err := site.Init(s, site.InitOptions{BaseURL: originURL, SiteTitle: "alice", License: "reserved"}); err != nil {
		t.Fatal(err)
	}
	k1 = readFile(t, filepath.Join(dir, ".polis", "keys", "id_ed25519"))
	pr, err := publish.PublishPost(s, "# Hello\n\nThe first post, signed at the origin.\n", "hello.md", k1)
	if err != nil {
		t.Fatal(err)
	}
	if err := following.SaveSigned(s, &following.FollowingFile{Following: []following.FollowingEntry{
		{URL: "https://bob.polis.pub", AddedAt: "2026-09-01T00:00:00Z"},
	}}, k1); err != nil {
		t.Fatal(err)
	}
	if err := metadata.AddBlessedCommentSigned(s, pr.Path, metadata.BlessedComment{
		URL: "https://bob.polis.pub/comments/20260901/reply.md",
	}, k1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := agent.IssueDefault(agent.Config{Site: s, BaseURL: originURL, PrivateKey: k1}); err != nil {
		t.Fatal(err)
	}
	write(t, dir, ".polis/ds/ds.polis.pub/registration.json", `{"registered":true}`)
	write(t, dir, ".polis/ds/ds.polis.pub/pub.polis.core/state/cursors.json", `{"cursor":"42"}`)
	write(t, dir, ".polis/ds/ds.polis.pub/pub.polis.core/config/feed.json", `{"hidden":["x"]}`)
	write(t, dir, ".polis/logs/2026-10-16.log", "a log line\n")
	write(t, dir, ".polis/bundles/pub.polis.core/posts/drafts/idea.md", "a draft\n")
	write(t, dir, ".env", "POLIS_BASE_URL=https://alice.polis.pub\nDISCOVERY_SERVICE_URL=https://ds.polis.pub\n")
	if ok, _ := s.Store().Exists(context.Background(), "content/pub.polis.core/follow/following.json"); !ok {
		t.Fatal("fixture has no follow list")
	}
	return dir, k1
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// zipDir makes an export archive the way the export endpoints do: site-relative
// names at the root.
func zipDir(t *testing.T, dir string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "export.zip")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		info, _ := d.Info()
		h, _ := zip.FileInfoHeader(info)
		h.Name = filepath.ToSlash(rel)
		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = w.Write(readFile(t, p))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return out
}

// tree hashes every file under dir by its relative path.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	got := map[string]string{}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		sum := sha256.Sum256(readFile(t, p))
		got[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	return got
}

func branchOut(t *testing.T) string { return filepath.Join(t.TempDir(), "branched") }

// The whole cycle, from an archive: the criteria a fixture can show.
func TestBranchFromAnArchive(t *testing.T) {
	calls := noNetwork(t)
	origin, k1 := buildOrigin(t)
	archive := zipDir(t, origin)
	archiveBefore := tree(t, filepath.Dir(archive))
	originBefore := tree(t, origin)
	out := branchOut(t)

	res, err := Run(Options{Source: archive, To: targetURL, Out: out})
	if err != nil {
		t.Fatalf("branch: %v", err)
	}
	if n := atomic.LoadInt32(calls); n != 0 {
		t.Errorf("the branch made %d network call(s)", n)
	}

	// Criterion 1: the inputs and the origin are untouched.
	if !equalTrees(archiveBefore, tree(t, filepath.Dir(archive))) {
		t.Error("the export archive changed")
	}
	if !equalTrees(originBefore, tree(t, origin)) {
		t.Error("the origin site changed")
	}
	if res.Output != out {
		t.Errorf("output = %s, want %s", res.Output, out)
	}

	s := storage.At(out)
	in := map[string]string{}
	for k, v := range originBefore {
		in[k] = v
	}
	outTree := tree(t, out)

	// Criterion 5: only the new key.
	assertOnlyNewKey(t, out, k1)

	// Criterion 3: a new entry, signed by the exported key over the canonical
	// message naming the ORIGIN, recording it.
	block, err := site.LoadKeyHistory(s)
	if err != nil || block == nil {
		t.Fatalf("key history: %v", err)
	}
	if block.Current.Epoch != 1 || len(block.History) != 1 {
		t.Fatalf("chain = epoch %d with %d retired; want epoch 1 with 1", block.Current.Epoch, len(block.History))
	}
	if block.Current.SigningDomain != originHost {
		t.Errorf("signing_domain = %q, want %q", block.Current.SigningDomain, originHost)
	}
	k1pub, _ := signing.PublicKeyFromPrivate(k1)
	if !site.SameSSHKey(block.History[0].Key, string(k1pub)) {
		t.Error("the retired entry is not the exported key")
	}
	// Read at the NEW address, the handover still verifies.
	if err := site.VerifyChain(block, targetDomain); err != nil {
		t.Errorf("the chain read at %s: %v", targetDomain, err)
	}
	wk, _ := site.LoadWellKnown(s)
	if wk.PublicKey != block.Current.Key || wk.PublicKey != res.NewPublicKey {
		t.Error("public_key is not the chain's head")
	}

	// Criterion 4: no signed file changes, except the two lists, re-signed.
	for k, sum := range in {
		if !strings.HasPrefix(k, "content/") {
			continue
		}
		if k == following.DefaultKey || k == metadata.BlessedKey {
			if outTree[k] == sum {
				t.Errorf("%s was not re-signed", k)
			}
			continue
		}
		if outTree[k] != sum {
			t.Errorf("signed source %s changed", k)
		}
	}
	if st, err := following.VerifySite(s); st != following.StatusValid {
		t.Errorf("follow list at the new site: %s %v", st, err)
	}
	if st, err := metadata.VerifyBlessedSite(s); st != metadata.StatusValid {
		t.Errorf("blessing list at the new site: %s %v", st, err)
	}
	if res.FollowResign != signing.ResignDone || res.BlessedResign != signing.ResignDone {
		t.Errorf("re-sign outcomes = %s, %s", res.FollowResign, res.BlessedResign)
	}
	// ...and derived output is regenerated for the new address.
	didDoc := readFile(t, filepath.Join(out, ".well-known", "did.json"))
	if got := sitecheck.DomainFromDIDDocument(didDoc); got != targetDomain {
		t.Errorf("did.json names %q, want %q", got, targetDomain)
	}
	if n := site.DIDDocumentKeyCount(didDoc); n != 2 {
		t.Errorf("did.json names %d keys, want 2 (the retired one stays verifiable)", n)
	}
	page := findRenderedPost(t, out)
	if !strings.Contains(page, targetURL) {
		t.Error("the rendered post does not name the new address")
	}
	env := string(readFile(t, filepath.Join(out, ".env")))
	if !strings.Contains(env, "POLIS_BASE_URL="+targetURL+"\n") || strings.Contains(env, "POLIS_BASE_URL="+originURL) {
		t.Errorf(".env = %q", env)
	}
	if !strings.Contains(env, "DISCOVERY_SERVICE_URL=https://ds.polis.pub") {
		t.Error(".env lost a setting the export carried")
	}

	// Criterion 6: none of the origin's DS registration or state.
	for k := range outTree {
		if strings.HasPrefix(k, ".polis/ds/") && !strings.Contains(k, "/config/") {
			t.Errorf("DS state crossed: %s", k)
		}
	}
	if _, ok := outTree[".polis/ds/ds.polis.pub/pub.polis.core/config/feed.json"]; !ok {
		t.Error("the user's DS preferences did not cross")
	}

	// The DM keyring and conversations stay behind, and the block goes.
	for k := range outTree {
		if strings.HasPrefix(k, ".polis/bundles/pub.polis.core/dm/") || strings.HasPrefix(k, ".polis/logs/") {
			t.Errorf("crossed: %s", k)
		}
	}
	if _, ok := wk.Extra["public_key_messages"]; ok {
		t.Error("public_key_messages is still published")
	}
	if !res.MessagesKeyRemoved {
		t.Error("the result does not say the messages key was removed")
	}

	// Rosie's grant crosses unchanged (covered above) and still verifies.
	grants, err := attestation.Grants(s, agent.Rosie)
	if err != nil || len(grants) != 1 || grants[0].Signature != attestation.StatusValid {
		t.Errorf("Rosie's grant at the new site: %+v %v", grants, err)
	}

	// Criterion 7: polis validate finds nothing wrong, and the post signed
	// before the branch verifies under the exported key.
	report := sitecheck.RunLocalWith(out, nil)
	if !report.OK() {
		t.Errorf("validate on the branched site: %s", failures(report))
	}
	if !checkPassed(report, "content.posts") {
		t.Errorf("the pre-branch post did not verify: %s", failures(report))
	}
}

// A directory export, an empty output directory, and an archive output.
func TestBranchFromADirectoryToAnArchive(t *testing.T) {
	noNetwork(t)
	origin, k1 := buildOrigin(t)
	before := tree(t, origin)
	archive := filepath.Join(t.TempDir(), "branched.zip")

	res, err := Run(Options{Source: origin, To: targetURL, Archive: archive})
	if err != nil {
		t.Fatalf("branch: %v", err)
	}
	if !equalTrees(before, tree(t, origin)) {
		t.Error("the export directory changed")
	}
	if !res.IsArchive || res.Output != archive {
		t.Errorf("result = %+v", res)
	}
	unpacked := filepath.Join(t.TempDir(), "unpacked")
	if err := unzip(archive, unpacked); err != nil {
		t.Fatal(err)
	}
	assertOnlyNewKey(t, unpacked, k1)
	if r := sitecheck.RunLocalWith(unpacked, nil); !r.OK() {
		t.Errorf("validate on the unpacked archive: %s", failures(r))
	}
	// Nothing is left beside the output.
	entries, _ := os.ReadDir(filepath.Dir(archive))
	if len(entries) != 1 {
		t.Errorf("left beside the archive: %v", entries)
	}
}

func TestBranchIntoAnEmptyDirectory(t *testing.T) {
	noNetwork(t)
	origin, _ := buildOrigin(t)
	out := branchOut(t)
	if err := os.Mkdir(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(Options{Source: origin, To: targetURL, Out: out}); err != nil {
		t.Fatalf("branch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, ".well-known", "polis")); err != nil {
		t.Error("nothing written into the empty directory")
	}
}

// A handover made before entries recorded their domain keeps meaning the
// origin once the chain is read at the new address.
func TestBranchAnnotatesEarlierHandoversWithTheOrigin(t *testing.T) {
	noNetwork(t)
	origin, _ := buildOrigin(t)
	s := storage.At(origin)
	keys, err := site.PrepareRotation(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := site.CompleteRotation(context.Background(), s, keys, site.RotationOptions{SigningDomain: originHost}); err != nil {
		t.Fatal(err)
	}
	// Strip the field, as every handover written before it existed has none.
	raw := string(readFile(t, filepath.Join(origin, ".well-known", "polis")))
	stripped := regexp.MustCompile(`\s*"signing_domain": "[^"]*",?`).ReplaceAllString(raw, "")
	if stripped == raw {
		t.Fatal("fixture: no signing_domain to strip")
	}
	write(t, origin, ".well-known/polis", stripped)
	if b, _ := site.LoadKeyHistory(s); b.Current.SigningDomain != "" || site.VerifyChain(b, targetDomain) == nil {
		t.Fatal("fixture: the legacy chain should fail when read at the new address")
	}

	out := branchOut(t)
	res, err := Run(Options{Source: origin, To: targetURL, Out: out})
	if err != nil {
		t.Fatalf("branch: %v", err)
	}
	if res.Annotated != 1 {
		t.Errorf("annotated %d entries, want 1", res.Annotated)
	}
	block, _ := site.LoadKeyHistory(storage.At(out))
	if block.Current.Epoch != 2 {
		t.Fatalf("epoch = %d, want 2", block.Current.Epoch)
	}
	if block.History[0].SigningDomain != "" {
		t.Error("genesis was given a signing_domain; it signed nothing")
	}
	if block.History[1].SigningDomain != originHost || block.Current.SigningDomain != originHost {
		t.Errorf("signing domains = %q, %q", block.History[1].SigningDomain, block.Current.SigningDomain)
	}
	if err := site.VerifyChain(block, targetDomain); err != nil {
		t.Errorf("the chain read at %s: %v", targetDomain, err)
	}
}

// Criterion 2 and 10: the refusals. Each writes nothing.
func TestBranchRefusals(t *testing.T) {
	noNetwork(t)
	cases := []struct {
		name   string
		break_ func(t *testing.T, dir string)
		opts   func(o *Options)
		want   error
		text   string
	}{
		{name: "no private key", break_: func(t *testing.T, dir string) {
			os.Remove(filepath.Join(dir, ".polis", "keys", "id_ed25519"))
		}, want: ErrNoPrivateKey, text: "mirror"},
		{name: "a key that is not the published one", break_: func(t *testing.T, dir string) {
			other, _, _ := signing.GenerateKeypair()
			write(t, dir, ".polis/keys/id_ed25519", string(other))
		}, want: ErrKeyNotHead},
		{name: "no origin domain", break_: func(t *testing.T, dir string) {
			os.Remove(filepath.Join(dir, ".well-known", "did.json"))
		}, want: ErrNoOriginDomain, text: "--from-domain"},
		{name: "no private residue", break_: func(t *testing.T, dir string) {
			for _, r := range Residue {
				os.RemoveAll(filepath.Join(dir, filepath.FromSlash(r.Key)))
			}
		}, want: ErrNoResidue, text: "--without-private"},
		{name: "the origin's own address", opts: func(o *Options) { o.To = originURL }, text: "origin's own"},
		{name: "a target with a path", opts: func(o *Options) { o.To = targetURL + "/blog" }, text: "site root"},
		{name: "a non-empty output", opts: func(o *Options) {
			os.MkdirAll(o.Out, 0o755)
			os.WriteFile(filepath.Join(o.Out, "x"), []byte("x"), 0o644)
		}, want: ErrOutputExists},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			origin, _ := buildOrigin(t)
			if c.break_ != nil {
				c.break_(t, origin)
			}
			before := tree(t, origin)
			opts := Options{Source: origin, To: targetURL, Out: branchOut(t)}
			if c.opts != nil {
				c.opts(&opts)
			}
			outBefore := tree(t, filepath.Dir(opts.Out))
			_, err := Run(opts)
			if err == nil {
				t.Fatal("branch did not refuse")
			}
			if c.want != nil && !errors.Is(err, c.want) {
				t.Errorf("err = %v, want %v", err, c.want)
			}
			if c.text != "" && !strings.Contains(err.Error(), c.text) {
				t.Errorf("err = %v, want it to mention %q", err, c.text)
			}
			if strings.Contains(err.Error(), "PRIVATE KEY") || strings.Contains(err.Error(), "ssh-ed25519") {
				t.Errorf("the refusal prints a key: %v", err)
			}
			if !equalTrees(before, tree(t, origin)) {
				t.Error("the export changed")
			}
			if !equalTrees(outBefore, tree(t, filepath.Dir(opts.Out))) {
				t.Error("a refused branch wrote something")
			}
		})
	}
}

// --from-domain stands in for a missing did.json.
func TestBranchFromDomainFlag(t *testing.T) {
	noNetwork(t)
	origin, _ := buildOrigin(t)
	os.Remove(filepath.Join(origin, ".well-known", "did.json"))
	res, err := Run(Options{Source: origin, To: targetURL, Out: branchOut(t), FromDomain: originHost})
	if err != nil {
		t.Fatalf("branch: %v", err)
	}
	if res.FromDomain != originHost {
		t.Errorf("from = %q", res.FromDomain)
	}
}

// Criterion 10: --without-private resets the residue, and says what, policies
// first.
func TestBranchWithoutPrivateListsWhatItResets(t *testing.T) {
	noNetwork(t)
	origin, _ := buildOrigin(t)
	write(t, origin, ".polis/policies/rules.jsonl", `{"rule":"deny comment from spam.example"}`+"\n")
	out := branchOut(t)
	res, err := Run(Options{Source: origin, To: targetURL, Out: out, WithoutPrivate: true})
	if err != nil {
		t.Fatalf("branch: %v", err)
	}
	if len(res.ResidueReset) != len(Residue) || res.ResidueReset[0].Key != ".polis/policies/rules.jsonl" {
		t.Errorf("reset = %+v, want every residue item, policies first", res.ResidueReset)
	}
	got := string(readFile(t, filepath.Join(out, ".polis", "policies", "rules.jsonl")))
	if strings.Contains(got, "spam.example") {
		t.Error("the private policy crossed under --without-private")
	}
	if _, err := os.Stat(filepath.Join(out, ".polis", "bundles", "pub.polis.core", "posts", "drafts", "idea.md")); err == nil {
		t.Error("a draft crossed under --without-private")
	}
	if r := sitecheck.RunLocalWith(out, nil); !r.OK() {
		t.Errorf("validate: %s", failures(r))
	}
}

// Criterion 8: nothing in the command names a host.
func TestBranchNamesNoHost(t *testing.T) {
	for _, f := range []string{"branch.go", filepath.Join("..", "cmd", "branch.go")} {
		src := strings.ToLower(string(readFile(t, f)))
		for _, host := range []string{"vercel", "fly.io", "flyctl", "polis.pub", "netlify", "cloudflare", "github.io"} {
			if strings.Contains(src, host) {
				t.Errorf("%s names a host: %q", f, host)
			}
		}
	}
}

// An archive entry that escapes the export is refused.
func TestUnzipRefusesAnEscape(t *testing.T) {
	p := filepath.Join(t.TempDir(), "evil.zip")
	f, _ := os.Create(p)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("../outside")
	w.Write([]byte("x"))
	zw.Close()
	f.Close()
	if err := unzip(p, filepath.Join(t.TempDir(), "in")); err == nil {
		t.Fatal("an escaping entry was unpacked")
	}
}

func assertOnlyNewKey(t *testing.T, dir string, k1 []byte) {
	t.Helper()
	keys, _ := os.ReadDir(filepath.Join(dir, ".polis", "keys"))
	var names []string
	for _, k := range keys {
		names = append(names, k.Name())
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "id_ed25519,id_ed25519.pub" {
		t.Errorf("keys = %v, want only the new pair", names)
	}
	k2 := readFile(t, filepath.Join(dir, ".polis", "keys", "id_ed25519"))
	if bytes.Equal(k2, k1) {
		t.Fatal("the output's key is the exported key")
	}
	// The exported private key appears in no file at all.
	body := strings.TrimSpace(string(k1))
	inner := strings.Join(strings.Split(body, "\n")[1:len(strings.Split(body, "\n"))-1], "")
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b := readFile(t, p)
		if bytes.Contains(b, []byte(body)) || bytes.Contains(bytes.ReplaceAll(b, []byte("\n"), nil), []byte(inner)) {
			t.Errorf("the exported private key is in %s", p)
		}
		return nil
	})
}

func findRenderedPost(t *testing.T, dir string) string {
	t.Helper()
	var page string
	filepath.WalkDir(filepath.Join(dir, "posts"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".html") {
			page = string(readFile(t, p))
		}
		return nil
	})
	if page == "" {
		t.Fatal("no rendered post")
	}
	return page
}

func equalTrees(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func checkPassed(r *sitecheck.Report, id string) bool {
	for _, c := range r.Checks {
		if c.ID == id {
			return c.Outcome == sitecheck.OutcomePassed
		}
	}
	return false
}

func failures(r *sitecheck.Report) string {
	if r.FatalError != "" {
		return r.FatalError
	}
	var out []string
	for _, c := range r.Checks {
		if c.Outcome == sitecheck.OutcomeFailed {
			b, _ := json.Marshal(c)
			out = append(out, string(b))
		}
	}
	return strings.Join(out, "\n")
}
