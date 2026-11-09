package site

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⭐ THE IDENTITY DOCUMENT ON A STORE WITH NO FILESYSTEM.
//
// The same genesis, rotation and DID projection run against a POSIX site and
// an object-backed one, from the same starting bytes. Every byte each writes
// must be the same: a conversion that changed what .well-known/polis carries
// would break a chain no key still exists to remake. The object-backed site's
// directory does not exist, so anything still reading a path would fail.
func TestTheIdentityDocumentIsTheSameOnAStoreWithNoFilesystem(t *testing.T) {
	ctx := context.Background()
	k0, k1 := newKeypair(t), newKeypair(t)
	posixDir := writeSite(t, k0.pub, "2026-03-03T05:35:09Z")
	start, err := os.ReadFile(filepath.Join(posixDir, ".well-known", "polis"))
	if err != nil {
		t.Fatal(err)
	}

	missing := filepath.Join(t.TempDir(), "no-such-dir")
	sites := map[string]storage.Site{
		"posix":  storage.At(posixDir),
		"object": storage.On(missing, storage.NewObjectStore("tenants/alice")),
	}
	if err := sites["object"].Store().Put(ctx, WellKnownKey, start); err != nil {
		t.Fatal(err)
	}

	const ts = "2026-09-15T10:22:03Z"
	canonical, err := discovery.MakeKeyRotationCanonicalJSON("alice.polis.pub", k0.pub, k1.pub, ts)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := signing.SignContent(canonical, k0.priv)
	if err != nil {
		t.Fatal(err)
	}

	written := map[string]map[string][]byte{}
	for name, s := range sites {
		if wrote, err := WriteGenesisKeyHistory(s); err != nil || !wrote {
			t.Fatalf("%s: genesis wrote=%v err=%v", name, wrote, err)
		}
		if !HasKeyHistory(s) {
			t.Errorf("%s: HasKeyHistory is false after genesis", name)
		}
		if err := RecordKeyRotation(s, k1.pub, sig, ts, ""); err != nil {
			t.Fatalf("%s: RecordKeyRotation: %v", name, err)
		}
		if err := PublishDIDDocument(s, "alice.polis.pub"); err != nil {
			t.Fatalf("%s: PublishDIDDocument: %v", name, err)
		}
		if err := WriteFavicon(s); err != nil {
			t.Fatalf("%s: WriteFavicon: %v", name, err)
		}
		block, err := LoadKeyHistory(s)
		if err != nil || block == nil {
			t.Fatalf("%s: LoadKeyHistory: %v %v", name, block, err)
		}
		if err := VerifyChain(block, "alice.polis.pub"); err != nil {
			t.Errorf("%s: the chain does not verify: %v", name, err)
		}
		written[name] = map[string][]byte{}
		for _, key := range []string{WellKnownKey, DIDDocumentKey, "favicon.svg"} {
			b, err := s.Store().Get(ctx, key)
			if err != nil {
				t.Fatalf("%s: read %s: %v", name, key, err)
			}
			written[name][key] = b
		}
	}
	for key, want := range written["posix"] {
		if got := written["object"][key]; string(got) != string(want) {
			t.Errorf("%s differs between the two stores\n--- posix ---\n%s\n--- object ---\n%s", key, want, got)
		}
	}
	if !strings.Contains(string(written["object"][WellKnownKey]), `"valid_from": "`+ts+`"`) {
		t.Errorf("valid_from is not the signed string, byte for byte:\n%s", written["object"][WellKnownKey])
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the object-backed site touched its directory: %v", err)
	}
}

// ⛔ The identity check compares a write against what the store holds. On a
// store with no filesystem, a check that read a local path would find
// nothing, and "nothing there" lets a write drop the key history.
func TestAWriteThatDropsTheChainIsRefusedOnAStoreWithNoFilesystem(t *testing.T) {
	ctx := context.Background()
	k0 := newKeypair(t)
	start, err := os.ReadFile(filepath.Join(writeSite(t, k0.pub, "2026-03-03T05:35:09Z"), ".well-known", "polis"))
	if err != nil {
		t.Fatal(err)
	}
	s := storage.On(filepath.Join(t.TempDir(), "no-such-dir"), storage.NewObjectStore("tenants/alice"))
	if err := s.Store().Put(ctx, WellKnownKey, start); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteGenesisKeyHistory(s); err != nil {
		t.Fatal(err)
	}

	err = SaveWellKnown(s, &WellKnown{PublicKey: k0.pub, AuthorName: "Alice"})
	var refusal *IdentityChangeError
	if !errors.As(err, &refusal) {
		t.Fatalf("a write from scratch over a site with a chain was not refused: %v", err)
	}
	after, _ := s.Store().Get(ctx, WellKnownKey)
	var raw map[string]json.RawMessage
	if json.Unmarshal(after, &raw) != nil || raw[keyHistoryField] == nil {
		t.Errorf("the chain is gone after a refused write:\n%s", after)
	}
}

// LoadWellKnownRaw's three cases: absent is (nil, nil), unreadable is an
// error, present parses.
func TestLoadWellKnownRawTellsAbsenceFromAFailedRead(t *testing.T) {
	dir := t.TempDir()
	raw, err := LoadWellKnownRaw(storage.At(dir))
	if raw != nil || err != nil {
		t.Fatalf("absent: %v, %v; want (nil, nil)", raw, err)
	}
	dir = writeSite(t, newKeypair(t).pub, "2026-03-03T05:35:09Z")
	unreadable(t, dir, WellKnownKey)
	if _, err := LoadWellKnownRaw(storage.At(dir)); err == nil {
		t.Error("unreadable .well-known/polis returned no error; it could not look and must say so")
	}
	if _, err := LoadWellKnown(storage.At(dir)); err == nil {
		t.Error("LoadWellKnown on an unreadable file returned no error")
	}
}

// The not-found refusal names the site root on a filesystem, exactly as it
// always has, and still says what is missing on a store with no root.
func TestNoWellKnownNamesWhereItLooked(t *testing.T) {
	dir := t.TempDir()
	err := SetLicensePointer(storage.At(dir), "/x")
	if err == nil || err.Error() != ".well-known/polis not found in "+dir {
		t.Errorf("POSIX: %v", err)
	}
	obj := storage.On(filepath.Join(t.TempDir(), "gone"), storage.NewObjectStore("t"))
	err = SetLicensePointer(obj, "/x")
	if err == nil || err.Error() != ".well-known/polis not found in this site" {
		t.Errorf("object store: %v", err)
	}
}

// The legacy private-content location on a store with no filesystem: it
// exists exactly when something is stored under it, and the migration, which
// moves directories, refuses to leave that state where nothing reads it.
func TestLegacyPrivateContentOnAStoreWithNoFilesystem(t *testing.T) {
	ctx := context.Background()
	s := storage.On(filepath.Join(t.TempDir(), "gone"), storage.NewObjectStore("t"))
	if LegacyPrivateContentExists(s) {
		t.Error("an empty store reports legacy content")
	}
	if err := MigratePrivateBundlesPath(s); err != nil {
		t.Errorf("nothing to migrate: %v", err)
	}
	if err := s.Store().Put(ctx, ".polis/content/pub.polis.core/dm/x.json", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if !LegacyPrivateContentExists(s) {
		t.Error("stored legacy content is not reported")
	}
	if err := MigratePrivateBundlesPath(s); err == nil {
		t.Error("legacy content on a store with no filesystem migrated silently")
	}
	if ok, _ := s.Store().Exists(ctx, ".polis/content/pub.polis.core/dm/x.json"); !ok {
		t.Error("the refused migration removed the legacy content")
	}
}
