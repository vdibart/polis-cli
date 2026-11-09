package site

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

func errorPaths(r *ValidationResult) map[string]string {
	out := map[string]string{}
	for _, e := range r.Errors {
		out[e.Code] = e.Path
	}
	return out
}

// On a filesystem the root errors name the directory, as they always have:
// they return before the scrub, and the caller is the one who named it.
func TestValidateNamesAMissingOrNonDirectoryRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-site")
	r := Validate(storage.At(missing))
	if r.Status != StatusNotFound || errorPaths(r)["SITE_DIR_NOT_FOUND"] != missing {
		t.Fatalf("missing root: status %q, errors %+v; want not_found naming %s", r.Status, r.Errors, missing)
	}

	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = Validate(storage.At(file))
	if r.Status != StatusInvalid || errorPaths(r)["NOT_A_DIRECTORY"] != file {
		t.Fatalf("file root: status %q, errors %+v; want invalid NOT_A_DIRECTORY naming %s", r.Status, r.Errors, file)
	}
}

// Every other finding is reported by key, on a filesystem and off one alike.
func TestValidateReportsByKey(t *testing.T) {
	r := Validate(storage.At(t.TempDir()))
	p := errorPaths(r)
	if p["WELLKNOWN_MISSING"] != ".well-known/polis" || p["PRIVATE_KEY_MISSING"] != ".polis/keys/id_ed25519" {
		t.Fatalf("empty POSIX site: errors %+v; want key paths", r.Errors)
	}
}

// ⭐ A store with no filesystem has no root directory to be missing. Its
// directory does not exist, and the old os.Stat of it reported
// SITE_DIR_NOT_FOUND for a site whose bytes were all there.
func TestValidateOnAStoreWithNoFilesystem(t *testing.T) {
	ctx := context.Background()
	gone := filepath.Join(t.TempDir(), "no-such-dir")
	site := storage.On(gone, storage.NewObjectStore("tenants/alice"))

	r := Validate(site)
	p := errorPaths(r)
	if _, ok := p["SITE_DIR_NOT_FOUND"]; ok {
		t.Fatalf("an object-backed site reported a missing directory: %+v", r.Errors)
	}
	if p["WELLKNOWN_MISSING"] != ".well-known/polis" {
		t.Fatalf("empty object-backed site: errors %+v; want WELLKNOWN_MISSING by key", r.Errors)
	}

	if err := site.Store().Put(ctx, ".polis/keys/id_ed25519", []byte("k")); err != nil {
		t.Fatal(err)
	}
	if _, ok := errorPaths(Validate(site))["PRIVATE_KEY_MISSING"]; ok {
		t.Fatal("the private key is in the store, but validate reported it missing")
	}
	for _, e := range Validate(site).Errors {
		if filepath.IsAbs(e.Path) {
			t.Errorf("%s: Path %q is a filesystem path on a store with none", e.Code, e.Path)
		}
	}
}
