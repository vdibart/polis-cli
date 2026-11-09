package site

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// unreadable makes key under dir unreadable for the rest of the test, so a
// read of it fails for a reason other than absence. Skips when running as
// root, which reads a mode-000 file anyway.
func unreadable(t *testing.T, dir, key string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file; the failed-read half needs a non-root user")
	}
	p := filepath.Join(dir, filepath.FromSlash(key))
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(p, 0o644) })
}

// An absent did.json needs writing. An unreadable one is an error: the store
// could not look, and "write it" would be an answer to a question nobody
// could ask.
func TestDIDDocumentNeedsWriteTellsAbsenceFromAFailedRead(t *testing.T) {
	dir := t.TempDir()
	s := storage.At(dir)
	if _, err := Init(s, InitOptions{Author: "Alice"}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, filepath.FromSlash(DIDDocumentKey))); err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}

	needs, want, err := DIDDocumentNeedsWrite(s, "alice.polis.pub")
	if err != nil {
		t.Fatalf("absent did.json: %v", err)
	}
	if !needs || len(want) == 0 {
		t.Fatalf("absent did.json: needs=%v, %d bytes; want a write with the document", needs, len(want))
	}

	if err := PublishDIDDocument(s, "alice.polis.pub"); err != nil {
		t.Fatal(err)
	}
	unreadable(t, dir, DIDDocumentKey)
	if _, _, err := DIDDocumentNeedsWrite(s, "alice.polis.pub"); err == nil {
		t.Error("unreadable did.json returned no error; it could not look and must say so")
	}
}

// A pointer to a licence that is gone reads as no terms. One whose file
// cannot be read is an error, not silence. One that escapes the site leads
// nowhere, like a dangling one, and reads nothing outside the site.
func TestSiteTermsTellsAbsenceFromAFailedRead(t *testing.T) {
	dir, _ := initSite(t, InitOptions{License: "reserved", BaseURL: "https://maya.example"})
	s := storage.At(dir)
	key := LicenseKey(s)

	unreadable(t, dir, key)
	if _, err := SiteTerms(s); err == nil {
		t.Error("unreadable licence returned no error; it could not look and must say so")
	}
	if err := os.Chmod(filepath.Join(dir, filepath.FromSlash(key)), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filepath.Join(dir, filepath.FromSlash(key))); err != nil {
		t.Fatal(err)
	}
	if terms, err := SiteTerms(s); err != nil || terms != nil {
		t.Errorf("dangling pointer: terms=%v err=%v, want no terms and no error", terms, err)
	}

	// A licence that verifies sits just outside the site, and the pointer
	// names it.
	outside := filepath.Join(filepath.Dir(dir), "outside-license.json")
	other, _ := initSite(t, InitOptions{License: "reserved", BaseURL: "https://other.example"})
	data, err := os.ReadFile(filepath.Join(other, filepath.FromSlash(LicenseKey(storage.At(other)))))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, data, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(outside) })
	if err := SetLicensePointer(s, "/../outside-license.json"); err != nil {
		t.Fatal(err)
	}
	if terms, err := SiteTerms(s); err != nil || terms != nil {
		t.Errorf("pointer outside the site: terms=%v err=%v, want no terms and no error", terms, err)
	}
}

// A mount that cannot be listed is an error; a missing one is nothing to do.
func TestRemoveLicenseSurfacesTellsAbsenceFromAFailedRead(t *testing.T) {
	dir := t.TempDir()
	s := storage.At(dir)
	if err := RemoveLicenseSurfaces(s); err != nil {
		t.Fatalf("no mount: %v", err)
	}

	if os.Geteuid() == 0 {
		t.Skip("root lists a mode-000 directory; the failed-read half needs a non-root user")
	}
	mount := filepath.Join(dir, "license")
	if err := os.MkdirAll(mount, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mount, "index.html"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(mount, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(mount, 0o755) })
	if err := RemoveLicenseSurfaces(s); err == nil {
		t.Error("an unlistable mount returned no error; it could not look and must say so")
	}
}

// openPost is a post whose own terms diverge from a site stating "reserved".
func openPost(title string) string {
	return "---\ntitle: " + title + "\npublished: 2026-08-28T12:00:00Z\nlicense:\n" +
		"  v: " + license.SchemaVersion + "\n" +
		"  profile: " + license.ProfileOpen + "\n" +
		"  train-ai: y\n  search: y\n  ai-input: y\n" +
		"  terms: https://maya.example/license\n" +
		"  asserted: 2026-08-28T12:00:00Z\n---\n\nBody.\n"
}

// The per-work rules: one for each post whose signal differs from the site's,
// keyed by its mount path at any depth, and none for an earlier version kept
// in a .versions side-car or for a file that is not a post. A missing post directory is no rules and no error.
func TestLicenseOverridesFindDivergingWorksAndSkipVersions(t *testing.T) {
	dir, _ := initSite(t, InitOptions{License: "reserved", BaseURL: "https://maya.example"})
	s := storage.At(dir)
	terms, err := SiteTerms(s)
	if err != nil || terms == nil {
		t.Fatalf("SiteTerms: %v %v", terms, err)
	}

	if err := os.RemoveAll(filepath.Join(dir, filepath.FromSlash(corePostsDir))); err != nil {
		t.Fatal(err)
	}
	got, err := licenseOverrides(s, terms)
	if err != nil || len(got) != 0 {
		t.Fatalf("no post directory: %v, %v; want no rules and no error", got, err)
	}

	for key, body := range map[string]string{
		corePostsDir + "/20260828/open.md":                  openPost("Open"),
		corePostsDir + "/2026/08/deep.md":                   openPost("Deep"),
		corePostsDir + "/20260828/plain.md":                 "---\ntitle: Plain\n---\n\nBody.\n",
		corePostsDir + "/20260828/.versions/open.md":        openPost("Old"),
		corePostsDir + "/20260828/notes.txt":                openPost("Not a post"),
		corePostsDir + "/2026/.versions/08/deep-history.md": openPost("Old, deeper"),
	} {
		if err := s.Store().Put(context.Background(), key, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	got, err = licenseOverrides(s, terms)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// ⚠️ plain.md carries no licence block, so its signal is empty and differs
	// from the site's: it gets a rule too. That is what the filesystem walk
	// this replaced produced, and this pins it rather than changing it.
	want := []string{"/posts/2026/08/deep.html", "/posts/20260828/open.html", "/posts/20260828/plain.html"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Errorf("override rules for %v, want %v", keys, want)
	}

	if os.Geteuid() == 0 {
		return
	}
	sub := filepath.Join(dir, filepath.FromSlash(corePostsDir), "2026")
	if err := os.Chmod(sub, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(sub, 0o755) })
	if _, err := licenseOverrides(s, terms); err == nil {
		t.Error("a post directory that could not be read in full returned no error; the rules would be incomplete")
	}
}

// Withdrawal on a store with no directories: the document and the rendered
// pages go, the author's own file in the mount stays, and the mount itself —
// which such a store never held — is not an error.
func TestWithdrawLicenseOnAStoreWithNoFilesystem(t *testing.T) {
	ctx := context.Background()
	s := storage.On(t.TempDir(), storage.NewObjectStore("tenants/maya"))
	put := func(key, body string) {
		t.Helper()
		if err := s.Store().Put(ctx, key, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	put(WellKnownKey, `{"public_key":"ssh-ed25519 AAAA","license":"/content/pub.polis.core/license/license.json"}`)
	key := LicenseKey(s)
	put(key, `{}`)
	put("robots.txt", "User-agent: *\n")
	put("license/index.html", "<p>terms</p>")
	put("license/notes.html", "<p>mine</p>")

	if err := WithdrawLicense(s); err != nil {
		t.Fatalf("WithdrawLicense: %v", err)
	}
	for _, gone := range []string{key, "robots.txt", "license/index.html"} {
		if ok, err := s.Store().Exists(ctx, gone); err != nil || ok {
			t.Errorf("%s: exists=%v err=%v, want removed", gone, ok, err)
		}
	}
	if ok, err := s.Store().Exists(ctx, "license/notes.html"); err != nil || !ok {
		t.Errorf("the author's own page: exists=%v err=%v, want kept", ok, err)
	}
}
