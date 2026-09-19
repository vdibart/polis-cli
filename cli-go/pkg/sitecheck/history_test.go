package sitecheck

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// SIGNET epic 31 — the chain has a reader.
//
// ⛔ These are the tests whose absence let the defect survive epic 16: every
// rotation test proved the CHAIN verified, and none proved a POST signed before
// the rotation still did. Every fixture here ACTUALLY ROTATES through
// site.RecordKeyRotation, the seam all three rotation paths call.

const (
	genesisAt  = "2026-03-03T05:35:09Z" // khSite's `created`
	rotatedAt  = "2026-09-15T10:22:03Z"
	beforeRot  = "2026-05-01T09:00:00Z"
	afterRot   = "2026-09-20T00:00:00Z"
	oldPostRel = "content/pub.polis.core/post/20260501/old.md"
	newPostRel = "content/pub.polis.core/post/20260920/new.md"
)

// signedAt signs a post or comment the way publish does, claiming `published`.
// An empty published omits the line.
func signedAt(t *testing.T, priv []byte, body, published string, isComment bool) string {
	t.Helper()
	canonical := signing.CanonicalizeContent(body)
	hash := SHA256Hex([]byte(canonical))

	head := "---\ntitle: Test\n"
	if published != "" {
		head += "published: " + published + "\n"
	}
	if isComment {
		head += "type: comment\n"
	}
	unsigned := head + fmt.Sprintf("current-version: sha256:%s\n---", hash)
	sig, err := signing.SignContent([]byte(signing.CanonicalizeContent(unsigned+"\n\n"+canonical)), priv)
	if err != nil {
		t.Fatal(err)
	}
	final := head + fmt.Sprintf("current-version: sha256:%s\nsignature: %s\n---", hash, extractSigBase64(sig))
	if isComment {
		final = head + fmt.Sprintf("current-version: sha256:%s\nauthor: alice.example\nsignature: %s\n---", hash, extractSigBase64(sig))
	}
	return final + "\n\n" + canonical
}

type rotatedFixture struct {
	dir    string
	k0, k1 kp
	old    string // signed by k0, claiming a time inside k0's window
	new    string // signed by k1, after the rotation
}

// rotatedSite builds a site with a real two-entry chain — genesis k0, rotated to
// k1 — signed over domain, with one post from each key on disk.
func rotatedSite(t *testing.T, domain string, withDID bool) rotatedFixture {
	t.Helper()
	f := rotatedFixture{k0: genKey(t), k1: genKey(t)}
	f.dir = khSite(t, f.k0, false)
	f.old = signedAt(t, f.k0.priv, "Written before the rotation.\n", beforeRot, false)

	khRotate(t, f.dir, domain, f.k0, f.k1, rotatedAt)
	if withDID {
		if err := site.PublishDIDDocument(f.dir, domain); err != nil {
			t.Fatal(err)
		}
	}
	f.new = signedAt(t, f.k1.priv, "Written after the rotation.\n", afterRot, false)

	for rel, content := range map[string]string{oldPostRel: f.old, newPostRel: f.new} {
		path := filepath.Join(f.dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f rotatedFixture) chain(t *testing.T, domain string) *site.KeyHistoryBlock {
	t.Helper()
	chain, note := ResolvingChain(loadChain(t, f.dir), domain, f.k1.pub)
	if chain == nil {
		t.Fatalf("a correctly rotated chain must be usable: %s", note)
	}
	return chain
}

// ⭐ THE DONE-WHEN: a post signed before a rotation verifies after it.
func TestAPostSignedBeforeARotationVerifiesAfterIt(t *testing.T) {
	f := rotatedSite(t, "alice.polis.pub", true)

	// The test is only meaningful if the defect is real: the key the site
	// publishes NOW rejects the old post.
	if res := VerifyContent(f.old, []byte(f.k1.pub), signing.TypePost); res.Signature != SigInvalid {
		t.Fatalf("setup: the current key must reject a post signed before the rotation, got %s", res.Signature)
	}

	res := VerifyContentWithHistory(f.old, []byte(f.k1.pub), f.chain(t, "alice.polis.pub"), signing.TypePost)
	if res.Signature != SigValid {
		t.Fatalf("a post signed before the rotation does not verify through the history: %v", res.SigError)
	}
	if res.Key == nil || res.Key.Source != KeyRetired || res.Key.Epoch == nil || *res.Key.Epoch != 0 {
		t.Fatalf("the result must say a RETIRED key at epoch 0 verified it, got %+v", res.Key)
	}
	if res.Key.ClaimedSigningTime != beforeRot || res.Key.ValidFrom != genesisAt || res.Key.ValidUntil != rotatedAt {
		t.Errorf("the result must carry the claimed time and the key's window verbatim, got %+v", res.Key)
	}

	// And a comment, which has a different signing base and the same `published:`.
	comment := signedAt(t, f.k0.priv, "A reply from before.\n", beforeRot, true)
	if cres := VerifyContentWithHistory(comment, []byte(f.k1.pub), f.chain(t, "alice.polis.pub"), signing.TypeComment); cres.Signature != SigValid || cres.Key.Source != KeyRetired {
		t.Errorf("a comment signed before the rotation must verify through the history too: %s %v", cres.Signature, cres.SigError)
	}
}

// D4, in what a human reads: `polis validate <dir>` passes, and says a retired
// key did it.
func TestValidateLocalNamesAPostVerifiedByARetiredKey(t *testing.T) {
	f := rotatedSite(t, "alice.polis.pub", true)
	c := checkByID(t, RunLocal(f.dir), "content.posts")

	if c.Outcome != OutcomePassed {
		t.Fatalf("content.posts must pass on a correctly rotated site: %+v", c)
	}
	if !strings.Contains(c.Detail, "2 signature(s) verified — 1 of them against a RETIRED key") {
		t.Errorf("the census must count the retired-key pass separately, got %q", c.Detail)
	}
	if len(c.Findings) != 1 || !strings.Contains(c.Findings[0], "old.md") ||
		!strings.Contains(c.Findings[0], "CLAIMED signing time "+beforeRot) {
		t.Errorf("the retired-key pass must be named with its claimed time, got %q", c.Findings)
	}
}

// The same, the way a stranger asks: over HTTP, site and single artifact.
func TestValidateRemoteNamesAPostVerifiedByARetiredKey(t *testing.T) {
	// httptest serves on 127.0.0.1:<port>; the rotation signs the port-less host,
	// exactly as polis rotate-key does.
	f := rotatedSite(t, "127.0.0.1", false)
	wk, err := os.ReadFile(filepath.Join(f.dir, ".well-known", "polis"))
	if err != nil {
		t.Fatal(err)
	}
	srv := &remoteSite{files: map[string]string{
		"/.well-known/polis": string(wk),
		"/" + oldPostRel:     f.old,
		"/" + newPostRel:     f.new,
		"/content/pub.polis.core/index.jsonl": string(mustJSONLine(map[string]string{"type": "post", "path": oldPostRel, "published": beforeRot})) + "\n" +
			string(mustJSONLine(map[string]string{"type": "post", "path": newPostRel, "published": afterRot})) + "\n",
	}}
	ts := srv.serve(t)

	c := checkByID(t, NewRemote().RunSite(ts.URL), "content.posts")
	if c.Outcome != OutcomePassed || !strings.Contains(c.Detail, "1 of them against a RETIRED key") {
		t.Fatalf("remote content.posts must pass and name the retired-key pass: %+v", c)
	}

	a := checkByID(t, NewRemote().RunArtifact(ts.URL+"/"+oldPostRel), "content.artifact")
	if a.Outcome != OutcomePassed || !strings.Contains(a.Detail, "RETIRED key (epoch 0") || !strings.Contains(a.Detail, "CLAIMED signing time "+beforeRot) {
		t.Fatalf("a single old artifact must pass and say a retired key verified it: %+v", a)
	}
	n := checkByID(t, NewRemote().RunArtifact(ts.URL+"/"+newPostRel), "content.artifact")
	if n.Outcome != OutcomePassed || strings.Contains(n.Detail, "RETIRED") {
		t.Fatalf("a post signed by the current key must pass as a CURRENT-key pass: %+v", n)
	}
}

// D2: the key the site publishes now is tried first, and a pass there is a
// current-key pass even when the artifact claims a time in an older window.
func TestTheCurrentKeyIsTriedFirst(t *testing.T) {
	f := rotatedSite(t, "alice.polis.pub", true)
	chain := f.chain(t, "alice.polis.pub")

	res := VerifyContentWithHistory(f.new, []byte(f.k1.pub), chain, signing.TypePost)
	if res.Signature != SigValid || res.Key.Source != KeyCurrent || res.Key.Epoch == nil || *res.Key.Epoch != 1 || res.Key.ClaimedSigningTime != "" {
		t.Fatalf("a current-key post must report the current key at epoch 1 and no claimed time, got %s %+v", res.Signature, res.Key)
	}

	// Signed by the current key, claiming a time before the rotation.
	backdated := signedAt(t, f.k1.priv, "Claims to be old.\n", beforeRot, false)
	if res := VerifyContentWithHistory(backdated, []byte(f.k1.pub), chain, signing.TypePost); res.Signature != SigValid || res.Key.Source != KeyCurrent {
		t.Fatalf("the current key must win before the history is consulted, got %s %+v", res.Signature, res.Key)
	}

	// No history at all: exactly VerifyContent, and the epoch is unknown rather
	// than guessed.
	plain := VerifyContent(f.new, []byte(f.k1.pub), signing.TypePost)
	if plain.Signature != SigValid || plain.Key.Source != KeyCurrent || plain.Key.Epoch != nil {
		t.Fatalf("with no history the epoch must be nil, got %+v", plain.Key)
	}
}

// ⭐ THE OTHER DONE-WHEN: the window is enforced even though the claim inside it
// is not checkable.
func TestARetiredKeyOutsideItsWindowFails(t *testing.T) {
	f := rotatedSite(t, "alice.polis.pub", true)
	chain := f.chain(t, "alice.polis.pub")

	cases := []struct {
		name, published, want string
	}{
		{"after the key was retired", "2026-10-01T00:00:00Z", "falls inside the current key's window"},
		{"before the site existed", "2026-01-01T00:00:00Z", "outside every key's window"},
		{"the instant it was retired", rotatedAt, "falls inside the current key's window"},
		{"in a form the history cannot be compared with", "2026-05-01", "not in the key history's timestamp form"},
		{"claiming no time at all", "", "claims no signing time"},
	}
	for _, c := range cases {
		post := signedAt(t, f.k0.priv, "Signed by the retired key.\n", c.published, false)
		res := VerifyContentWithHistory(post, []byte(f.k1.pub), chain, signing.TypePost)
		if res.Signature != SigInvalid {
			t.Errorf("%s: a retired key must not verify outside its window, got %s", c.name, res.Signature)
			continue
		}
		if res.SigError == nil || !strings.Contains(res.SigError.Error(), c.want) {
			t.Errorf("%s: the reason must say why no key applied (%q), got %v", c.name, c.want, res.SigError)
		}
	}

	// The instant before retirement is still inside k0's window.
	edge := signedAt(t, f.k0.priv, "Last moment.\n", "2026-09-15T10:22:02Z", false)
	if res := VerifyContentWithHistory(edge, []byte(f.k1.pub), chain, signing.TypePost); res.Signature != SigValid {
		t.Errorf("the second before retirement is inside the window: %v", res.SigError)
	}
}

// A history resolves a retired key only when its handovers verify and its head
// is the published key. Otherwise nothing is resolved, and a failure says why.
func TestAChainThatCannotBeTrustedResolvesNothing(t *testing.T) {
	f := rotatedSite(t, "alice.polis.pub", true)
	block := loadChain(t, f.dir)

	if chain, note := ResolvingChain(block, "mallory.polis.pub", f.k1.pub); chain != nil || !strings.Contains(note, "could not be used") {
		t.Errorf("a chain whose handover does not verify for this domain must resolve nothing, got %v %q", chain != nil, note)
	}
	if chain, note := ResolvingChain(block, "alice.polis.pub", f.k0.pub); chain != nil || !strings.Contains(note, "head is not the key the site publishes") {
		t.Errorf("a chain whose head is not the published key must resolve nothing, got %v %q", chain != nil, note)
	}

	genesis := loadChain(t, khSite(t, f.k0, false))
	if chain, note := ResolvingChain(genesis, "", f.k0.pub); chain == nil || note != "" {
		t.Errorf("a genesis-only chain verifies without a domain and has nothing to explain, got %v %q", chain != nil, note)
	}
	if chain, note := ResolvingChain(nil, "alice.polis.pub", f.k0.pub); chain != nil || note != "" {
		t.Errorf("no chain is honest absence, not a finding, got %v %q", chain != nil, note)
	}
}

// ⚠️ The local form's one gap, stated rather than hidden: a site that rotated and
// publishes no did.json cannot have its handovers checked from a directory, so
// the old post fails — with the reason, not as tampering.
func TestValidateLocalWithoutADomainSaysWhyAnOldPostFails(t *testing.T) {
	f := rotatedSite(t, "alice.polis.pub", false)
	c := checkByID(t, RunLocal(f.dir), "content.posts")
	if c.Outcome != OutcomeFailed {
		t.Fatalf("with no domain to check the handover against, the old post cannot verify: %+v", c)
	}
	joined := strings.Join(c.Findings, "\n")
	if !strings.Contains(joined, "old.md: signature does not verify against the current key") ||
		!strings.Contains(joined, "domain is unknown") {
		t.Errorf("the finding must say the history could not be used and why, got %q", c.Findings)
	}
}

// The finding for a retired key outside its window reads as one sentence and
// names the window rule, in the report a person actually sees.
func TestValidateLocalSaysWhyARetiredKeyOutsideItsWindowFails(t *testing.T) {
	f := rotatedSite(t, "alice.polis.pub", true)
	late := signedAt(t, f.k0.priv, "Claims a time after retirement.\n", "2026-10-01T00:00:00Z", false)
	path := filepath.Join(f.dir, "content/pub.polis.core/post/20261001/late.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(late), 0644); err != nil {
		t.Fatal(err)
	}

	c := checkByID(t, RunLocal(f.dir), "content.posts")
	if c.Outcome != OutcomeFailed {
		t.Fatalf("a retired key outside its window must fail the check: %+v", c)
	}
	want := "late.md: signature does not verify against the current key, and the claimed signing time 2026-10-01T00:00:00Z falls inside the current key's window"
	if !strings.Contains(strings.Join(c.Findings, "\n"), want) {
		t.Errorf("finding must read %q, got %q", want, c.Findings)
	}
}
