package site

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/license"
)

func initSite(t *testing.T, opts InitOptions) (dir string, priv []byte) {
	t.Helper()
	dir = t.TempDir()
	if _, err := Init(dir, opts); err != nil {
		t.Fatalf("init: %v", err)
	}
	priv, err := os.ReadFile(filepath.Join(dir, ".polis", "keys", "id_ed25519"))
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	return dir, priv
}

func TestInitStatesNoTermsUnlessAsked(t *testing.T) {
	// The conservative option turns out to be not choosing at all. A
	// license.json is a signed statement of intent; creating one the author
	// never made asserts they said something they did not.
	dir, _ := initSite(t, InitOptions{})

	if _, err := os.Stat(LicensePath(dir)); !os.IsNotExist(err) {
		t.Error("init created a licence nobody chose")
	}
	if p := LicensePointer(dir); p != "" {
		t.Errorf("init wrote a licence pointer nobody chose: %q", p)
	}
	terms, err := SiteTerms(dir)
	if err != nil {
		t.Fatal(err)
	}
	if terms != nil {
		t.Errorf("a site that stated nothing reports terms: %+v", terms)
	}
}

func TestInitWithAChoiceStatesTermsAndPointsAtThem(t *testing.T) {
	dir, _ := initSite(t, InitOptions{License: "reserved", BaseURL: "https://maya.example"})

	if _, err := os.Stat(LicensePath(dir)); err != nil {
		t.Fatalf("licence document not written: %v", err)
	}
	pointer := LicensePointer(dir)
	if pointer == "" {
		t.Fatal("licence written but .well-known/polis does not point at it")
	}

	terms, err := SiteTerms(dir)
	if err != nil {
		t.Fatal(err)
	}
	if terms == nil {
		t.Fatal("SiteTerms found nothing through the pointer")
	}
	if terms.Profile != license.ProfileReserved {
		t.Errorf("profile = %q, want %q", terms.Profile, license.ProfileReserved)
	}
}

func TestTheLicenceDocumentIsSigned(t *testing.T) {
	dir, _ := initSite(t, InitOptions{License: "reserved", BaseURL: "https://maya.example"})

	f, err := license.Load(LicensePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	pub, err := os.ReadFile(filepath.Join(dir, ".polis", "keys", "id_ed25519.pub"))
	if err != nil {
		t.Fatal(err)
	}
	ok, err := license.Verify(f, pub)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Error("the site licence does not verify against the site's own key")
	}
}

func TestMovingTheLicenceAndUpdatingThePointerKeepsEverythingWorking(t *testing.T) {
	// This is the property the pointer exists for. `dir` and `mount` are
	// per-type declarations in bundle.json, so site layout is user-configurable
	// — a hardcoded discovery path would contradict a decision already made.
	dir, _ := initSite(t, InitOptions{License: "reserved", BaseURL: "https://maya.example"})

	before, err := SiteTerms(dir)
	if err != nil || before == nil {
		t.Fatalf("setup: %v %+v", err, before)
	}

	// The author reorganises: the licence now lives somewhere else entirely.
	moved := filepath.Join(dir, "legal", "terms.json")
	if err := os.MkdirAll(filepath.Dir(moved), 0755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(LicensePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(moved, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(LicensePath(dir)); err != nil {
		t.Fatal(err)
	}

	// Before the pointer is updated, discovery correctly reports nothing —
	// it does not go hunting down assumed paths.
	if terms, _ := SiteTerms(dir); terms != nil {
		t.Error("discovery found the licence at a path the pointer no longer names")
	}

	if err := SetLicensePointer(dir, "/legal/terms.json"); err != nil {
		t.Fatal(err)
	}

	after, err := SiteTerms(dir)
	if err != nil {
		t.Fatal(err)
	}
	if after == nil {
		t.Fatal("following the updated pointer found nothing")
	}
	if *after != *before {
		t.Errorf("terms changed when the file moved:\n got %+v\nwant %+v", *after, *before)
	}
}

func TestDiscoveryIsByPointerNotByConvention(t *testing.T) {
	// A licence file sitting at the default path with no pointer is not a
	// statement. The pointer IS the statement.
	dir, priv := initSite(t, InitOptions{})

	f, err := license.New(license.ProfileReserved, "https://maya.example")
	if err != nil {
		t.Fatal(err)
	}
	if err := license.SignAndWrite(f, LicensePath(dir), priv); err != nil {
		t.Fatal(err)
	}

	terms, err := SiteTerms(dir)
	if err != nil {
		t.Fatal(err)
	}
	if terms != nil {
		t.Error("an unreferenced file was treated as a statement of terms")
	}
}

func TestLicensePathFollowsTheSitesOwnBundleDeclaration(t *testing.T) {
	dir, _ := initSite(t, InitOptions{})

	bundlePath := filepath.Join(dir, "content", "pub.polis.core", "bundle.json")
	b, err := bundle.LoadBundle(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	ct := b.Types[license.TypeName]
	ct.Dir = "terms"
	b.Types[license.TypeName] = ct
	if err := bundle.SaveBundle(bundlePath, b); err != nil {
		t.Fatal(err)
	}

	got := LicensePath(dir)
	want := filepath.Join(dir, "content", "pub.polis.core", "terms", LicenseFilename)
	if got != want {
		t.Errorf("LicensePath = %q, want %q — the declaration must win over the default", got, want)
	}
}

func TestWithdrawReturnsTheSiteToUnstated(t *testing.T) {
	dir, _ := initSite(t, InitOptions{License: "reserved", BaseURL: "https://maya.example"})

	if err := WithdrawLicense(dir); err != nil {
		t.Fatal(err)
	}
	if p := LicensePointer(dir); p != "" {
		t.Errorf("pointer survived withdrawal: %q", p)
	}
	if _, err := os.Stat(LicensePath(dir)); !os.IsNotExist(err) {
		t.Error("licence document survived withdrawal")
	}

	// And the key must actually be gone from the document, not present-but-empty.
	raw, err := LoadWellKnownRaw(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := raw["license"]; present {
		t.Error("the license key is still present in .well-known/polis after withdrawal")
	}
}

func TestThePointerIsAPointerAndNotACopyOfTheTerms(t *testing.T) {
	// Inlining the terms would make .well-known/polis a second copy that can
	// diverge from the signed source. A pointer can only go stale in location.
	dir, _ := initSite(t, InitOptions{License: "reserved", BaseURL: "https://maya.example"})

	data, err := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, isString := raw["license"].(string); !isString {
		t.Errorf("license field is not a plain pointer string: %T", raw["license"])
	}
	for _, leaked := range []string{"train-ai", "search", "attribution", "profile"} {
		if bytesContain(data, leaked) {
			t.Errorf(".well-known/polis inlines %q instead of pointing at the terms", leaked)
		}
	}
}

func bytesContain(b []byte, s string) bool {
	return len(b) >= len(s) && (string(b) != "" && indexOf(string(b), s) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

func TestNothingNewIsCreatedUnderDotPolisOrWellKnown(t *testing.T) {
	// The only .well-known change is one field in the existing document.
	//
	// Both sites get the same BaseURL so the licence is the ONLY difference
	// between them. Without that, the licensed site would also be the only one
	// with a canonical host, and it would gain a did.json for that reason
	// rather than for the licence — which is not what this test is about.
	plain := t.TempDir()
	if _, err := Init(plain, InitOptions{BaseURL: "https://maya.example"}); err != nil {
		t.Fatal(err)
	}
	licensed := t.TempDir()
	if _, err := Init(licensed, InitOptions{License: "reserved", BaseURL: "https://maya.example"}); err != nil {
		t.Fatal(err)
	}

	for _, sub := range []string{".well-known", filepath.Join(".polis", "bundles")} {
		a := listNames(t, filepath.Join(plain, sub))
		b := listNames(t, filepath.Join(licensed, sub))
		if len(a) != len(b) {
			t.Errorf("%s: stating a licence changed the file set: %v vs %v", sub, a, b)
		}
	}
}

func listNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestRestatingKeepsTheOriginalCreatedDate — `created` means created.
//
// license.New builds a fresh file with created, updated and asserted all set to
// now, which is right for a first statement and wrong for a restatement: it
// would silently move the date this site first stated terms every time the
// author changed their mind. The other two dates SHOULD move — the author is
// restating, now — so this asserts the split rather than just the one field.
func TestRestatingKeepsTheOriginalCreatedDate(t *testing.T) {
	dir, priv := initSite(t, InitOptions{License: "reserved"})

	first, err := license.Load(LicensePath(dir))
	if err != nil {
		t.Fatalf("load first: %v", err)
	}
	if first.Created == "" {
		t.Fatal("first statement wrote no created date")
	}

	// Backdate the file so a preserved value is visibly different from a fresh
	// one. Without this the two are seconds apart and the test proves nothing.
	const original = "2020-01-01T00:00:00Z"
	first.Created = original
	if err := license.SignAndWrite(first, LicensePath(dir), priv); err != nil {
		t.Fatalf("rewrite backdated: %v", err)
	}

	if _, err := StateLicense(dir, "open", "https://alice.example", priv); err != nil {
		t.Fatalf("restate: %v", err)
	}

	second, err := license.Load(LicensePath(dir))
	if err != nil {
		t.Fatalf("load second: %v", err)
	}
	if second.Created != original {
		t.Errorf("created = %q, want %q — a restatement moved the date the site first stated terms",
			second.Created, original)
	}
	if second.Updated == original {
		t.Error("updated did not move; a restatement IS an update")
	}
	if second.Terms == nil || second.Terms.Asserted == original {
		t.Error("asserted did not move; the author is asserting these terms now")
	}
	if second.Terms.Profile == first.Terms.Profile {
		t.Errorf("profile did not change: still %q", second.Terms.Profile)
	}
}

// TestFirstStatementOnASiteWithNoLicenceStillGetsAFreshCreated — the carry-forward
// must not turn "no previous file" into a failure or an empty date.
func TestFirstStatementOnASiteWithNoLicenceStillGetsAFreshCreated(t *testing.T) {
	dir, priv := initSite(t, InitOptions{})
	if _, err := os.Stat(LicensePath(dir)); !os.IsNotExist(err) {
		t.Fatalf("expected no licence to start from, got err=%v", err)
	}

	if _, err := StateLicense(dir, "reserved", "https://alice.example", priv); err != nil {
		t.Fatalf("state: %v", err)
	}
	f, err := license.Load(LicensePath(dir))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if f.Created == "" {
		t.Error("first statement left created empty")
	}
}
