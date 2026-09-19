package publish

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// licensedSite builds a site whose terms are stated, and returns its dir and key.
func licensedSite(t *testing.T, choice string) (dir string, priv, pub []byte) {
	t.Helper()
	dir = t.TempDir()
	if _, err := site.Init(dir, site.InitOptions{
		License: choice,
		BaseURL: "https://maya.example",
	}); err != nil {
		t.Fatalf("init: %v", err)
	}
	priv, err := os.ReadFile(filepath.Join(dir, ".polis", "keys", "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	pub, err = os.ReadFile(filepath.Join(dir, ".polis", "keys", "id_ed25519.pub"))
	if err != nil {
		t.Fatal(err)
	}
	return dir, priv, pub
}

func cfg() *DiscoveryConfig {
	return &DiscoveryConfig{BaseURL: "https://maya.example", Generator: "polis-cli-go/test"}
}

// verifyPost re-derives the signed bytes the way every verifier does — by
// deleting the signature line — and checks them against the site's key.
func verifyPost(t *testing.T, path string, pub []byte) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fm := ParseFrontmatter(string(raw))
	sig := fm["signature"]
	if sig == "" {
		t.Fatal("post carries no signature")
	}
	base := signing.MarkdownSigningBase(string(raw), signing.TypePost)
	ok, err := signing.VerifySignature([]byte(base), pub, sshSigPEM(sig))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Errorf("signature does not verify over the reconstructed base:\n%s", base)
	}
}

// sshSigPEM rebuilds the SSHSIG armour around the bare base64 body that
// frontmatter carries — the same reconstruction verify, judge, and patrol do.
func sshSigPEM(base64Body string) string {
	var lines []string
	for len(base64Body) > 76 {
		lines = append(lines, base64Body[:76])
		base64Body = base64Body[76:]
	}
	lines = append(lines, base64Body)
	return "-----BEGIN SSH SIGNATURE-----\n" + strings.Join(lines, "\n") + "\n-----END SSH SIGNATURE-----"
}

func TestPublishedPostCarriesTermsInsideTheSignature(t *testing.T) {
	// The whole point: the terms travel with the bytes because they are inside
	// the signed payload, not beside it.
	dir, priv, pub := licensedSite(t, "reserved")

	res, err := PublishPost(dir, "# Hello\n\nBody text.\n", "hello", priv, cfg())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, res.Path)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	terms := license.ParseBlock(string(raw))
	if terms == nil {
		t.Fatal("published post carries no licence block")
	}
	if terms.TrainAI != license.Disallow || terms.Search != license.Allow {
		t.Errorf("terms not materialised from the site licence: %+v", terms)
	}
	if terms.Asserted == "" {
		t.Error("no asserted date — 'what were the terms in March?' must be answerable")
	}

	verifyPost(t, path, pub)
}

func TestLicenceKeysAreSignedNotStripped(t *testing.T) {
	// Nothing this epic adds may be removed from the signing base. If a licence
	// key were stripped, the terms would be beside the signature rather than
	// inside it — which is the entire property being claimed.
	dir, priv, _ := licensedSite(t, "reserved")

	res, err := PublishPost(dir, "# Hello\n\nBody.\n", "hello", priv, cfg())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, res.Path))
	if err != nil {
		t.Fatal(err)
	}

	base := signing.MarkdownSigningBase(string(raw), signing.TypePost)
	for _, want := range []string{"license:", "train-ai: n", "search: y", "profile:"} {
		if !strings.Contains(base, want) {
			t.Errorf("signing base is missing %q — the terms are not inside the signature", want)
		}
	}
	if strings.Contains(base, "signature:") {
		t.Error("signing base still contains the signature line")
	}
}

func TestAWorkOverridesTheSiteDefault(t *testing.T) {
	dir, priv, pub := licensedSite(t, "reserved")

	c := cfg()
	c.LicenseProfile = "open"
	res, err := PublishPost(dir, "# Open post\n\nTake it.\n", "open-post", priv, c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, res.Path)

	raw, _ := os.ReadFile(path)
	terms := license.ParseBlock(string(raw))
	if terms == nil {
		t.Fatal("no licence block")
	}
	if terms.Profile != license.ProfileOpen {
		t.Errorf("profile = %q, want the work's %q", terms.Profile, license.ProfileOpen)
	}
	verifyPost(t, path, pub)
}

func TestAWorkCanOptOutOfTheSiteDefault(t *testing.T) {
	dir, priv, pub := licensedSite(t, "reserved")

	c := cfg()
	c.LicenseProfile = "none"
	res, err := PublishPost(dir, "# Quiet post\n\nNo terms here.\n", "quiet", priv, c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, res.Path)

	raw, _ := os.ReadFile(path)
	if terms := license.ParseBlock(string(raw)); terms != nil {
		t.Errorf("an explicit opt-out still carried terms: %+v", terms)
	}
	verifyPost(t, path, pub)
}

func TestASiteWithNoTermsPublishesExactlyAsBefore(t *testing.T) {
	// No regression for anyone who has not chosen. Their posts must be
	// byte-identical to what they were before this feature existed.
	dir, priv, pub := licensedSite(t, "")

	res, err := PublishPost(dir, "# Hello\n\nBody.\n", "hello", priv, cfg())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, res.Path)

	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "license") {
		t.Errorf("a site that stated nothing published licence data:\n%s", raw)
	}
	verifyPost(t, path, pub)
}

func TestEditingTheSiteLicenceDoesNotAlterPublishedWorks(t *testing.T) {
	// Non-retroactivity, shown rather than asserted. A newer licence never
	// reaches back into an older grant.
	dir, priv, pub := licensedSite(t, "reserved")

	res, err := PublishPost(dir, "# March post\n\nWritten under reserved terms.\n", "march", priv, cfg())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, res.Path)

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// The author changes her mind and opens everything up.
	if _, err := site.StateLicense(dir, "open", "https://maya.example", priv); err != nil {
		t.Fatal(err)
	}
	nowTerms, err := site.SiteTerms(dir)
	if err != nil || nowTerms.Profile != license.ProfileOpen {
		t.Fatalf("setup: site licence did not change: %v %+v", err, nowTerms)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("editing license.json changed an already-published work")
	}

	terms := license.ParseBlock(string(after))
	if terms == nil || terms.TrainAI != license.Disallow {
		t.Errorf("the March post no longer carries the terms it was signed with: %+v", terms)
	}
	// And it still verifies — the old signature stands over the old terms.
	verifyPost(t, path, pub)
}

func TestRepublishRestampsTermsAndStillVerifies(t *testing.T) {
	dir, priv, pub := licensedSite(t, "reserved")

	res, err := PublishPost(dir, "# Draft\n\nFirst take.\n", "draft", priv, cfg())
	if err != nil {
		t.Fatal(err)
	}

	res2, err := RepublishPost(dir, res.Path, "# Draft\n\nSecond take, revised.\n", priv, cfg())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, res2.Path)

	raw, _ := os.ReadFile(path)
	terms := license.ParseBlock(string(raw))
	if terms == nil {
		t.Fatal("republished post lost its licence block")
	}
	if strings.Count(string(raw), "license:") != 1 {
		t.Errorf("republish stacked licence blocks:\n%s", raw)
	}
	verifyPost(t, path, pub)

	// The index line is a projection of those bytes, so it follows them rather
	// than lagging until a rebuild.
	entries, _ := metadata.LoadPublicIndex(dir)
	entry := findEntry(t, entries, res2.Path)
	if entry.License == nil || *entry.License != *terms {
		t.Errorf("index terms did not follow the republished file:\n index: %+v\n  file: %+v", entry.License, terms)
	}
}

func TestPublishFailsRatherThanSilentlyWideningTheGrant(t *testing.T) {
	// A site whose licence is unreadable must not quietly publish under NO
	// terms — that would widen the grant without the author doing anything.
	dir, priv, _ := licensedSite(t, "reserved")

	if err := os.WriteFile(site.LicensePath(dir), []byte("{ not json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishPost(dir, "# Hello\n\nBody.\n", "hello", priv, cfg()); err == nil {
		t.Error("publish succeeded with an unreadable licence, silently dropping the terms")
	}
}

// ⭐ RIDE-ALONG (R24-11). index.jsonl is the machine-readable face of the site,
// and a consumer reading it should not have to fetch the markdown to learn what
// it may do with a work. The entry's terms are a PROJECTION of the frontmatter
// block, so they must appear the moment the post does — not after a rebuild.
func TestAPublishedPostReachesTheIndexWithItsTerms(t *testing.T) {
	dir, priv, _ := licensedSite(t, "reserved")

	res, err := PublishPost(dir, "# Hello\n\nBody text.\n", "hello", priv, cfg())
	if err != nil {
		t.Fatal(err)
	}

	entries, err := metadata.LoadPublicIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	entry := findEntry(t, entries, res.Path)

	if entry.License == nil {
		t.Fatal("the index entry carries no terms, so a consumer must fetch the markdown to learn them")
	}
	// The index must agree with the signed file, key for key — it is a
	// projection of those bytes and not a second statement.
	raw, err := os.ReadFile(filepath.Join(dir, res.Path))
	if err != nil {
		t.Fatal(err)
	}
	if want := license.ParseBlock(string(raw)); *entry.License != *want {
		t.Errorf("index terms disagree with the signed file:\n index: %+v\n  file: %+v", entry.License, want)
	}
}

// ⛔ The terms come off the signed file, never from the caller. A caller can
// pass terms that disagree with what was signed; the file cannot.
func TestTheIndexTakesTermsFromTheFileNotTheCaller(t *testing.T) {
	dir, priv, _ := licensedSite(t, "reserved")

	res, err := PublishPost(dir, "# Hello\n\nBody text.\n", "hello", priv, cfg())
	if err != nil {
		t.Fatal(err)
	}

	// A caller re-indexing the same work while claiming it may be trained on.
	if err := metadata.AppendToPublicIndex(dir, &metadata.IndexEntry{
		Type:           "post",
		Path:           res.Path,
		Title:          "Hello",
		Published:      "2026-08-28T12:00:00Z",
		CurrentVersion: res.Version,
		License:        &license.Terms{V: license.SchemaVersion, Profile: license.ProfileOpen, TrainAI: license.Allow},
	}); err != nil {
		t.Fatal(err)
	}

	entries, _ := metadata.LoadPublicIndex(dir)
	entry := findEntry(t, entries, res.Path)
	if entry.License == nil || entry.License.TrainAI != license.Disallow {
		t.Errorf("the index published terms the author never signed: %+v", entry.License)
	}
}

// A site that has stated nothing publishes an index entry with no terms —
// absent means unstated, and an invented default would speak for the author.
func TestAnUnstatedSiteIndexesNoTerms(t *testing.T) {
	dir, priv, _ := licensedSite(t, "none")

	res, err := PublishPost(dir, "# Hello\n\nBody text.\n", "hello", priv, cfg())
	if err != nil {
		t.Fatal(err)
	}

	entries, _ := metadata.LoadPublicIndex(dir)
	if entry := findEntry(t, entries, res.Path); entry.License != nil {
		t.Errorf("terms appeared for a site that stated none: %+v", entry.License)
	}
}

func findEntry(t *testing.T, entries []metadata.IndexEntry, path string) metadata.IndexEntry {
	t.Helper()
	for _, e := range entries {
		if e.Path == path {
			return e
		}
	}
	t.Fatalf("no index entry for %s", path)
	return metadata.IndexEntry{}
}
