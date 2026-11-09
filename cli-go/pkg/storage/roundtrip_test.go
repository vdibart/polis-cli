package storage

import (
	"context"
	"sort"
	"strings"
	"testing"
)

// ⭐⭐⭐ THE SHARPEST TEST EITHER BACKEND HAS: copy a site from one to the other
// and back, and require the FILES to be byte-identical.
//
// It catches drift, reconstruction creep and key-mapping mistakes at once —
// any of which would otherwise show up as a corrupted site after a migration
// rather than as a failing test.
//
// ⛔ IT COMPARES FILES, NOT THE DIRECTORY SHAPE, AND THAT IS THE DECISION
// AN OBJECT BACKEND HAD TO MAKE. An object store cannot express a deliberately empty
// directory: there is no object meaning "this folder exists and holds nothing".
// `pkg/clone` creates two such directories so an empty site's tree LOOKS like a
// populated one, and a round trip through blob loses them.
//
// ⭐ That loss is accepted rather than hidden, because nothing reads a
// directory: a consumer walks index.jsonl and the pointers in
// .well-known/polis. The alternative — a marker object per empty prefix —
// invents a file a POSIX site does not have, which breaks this comparison in
// the other direction and leaves litter a user would ask about.
func TestASiteRoundTripsBetweenBackendsByteForByte(t *testing.T) {
	ctx := context.Background()

	// A site with the shapes that actually occur: nested content, a dotfile
	// tree, an empty file, binary bytes, and a deep path.
	content := map[string]string{
		".well-known/polis":                            `{"public_key":"ssh-ed25519 AAAAC3Nz"}`,
		".well-known/did.json":                         `{"id":"did:web:alice.example"}`,
		"content/pub.polis.core/index.jsonl":           "{\"path\":\"a.md\"}\n{\"path\":\"b.md\"}\n",
		"content/pub.polis.core/post/20260101/a.md":    "---\ntitle: A\n---\n\nbody a\n",
		"content/pub.polis.core/post/20260102/b.md":    "---\ntitle: B\n---\n\nbody b\n",
		"content/pub.polis.core/follow/following.json": `{"following":[]}`,
		".polis/keys/id_ed25519":                       "PRIVATE KEY BYTES",
		".polis/bundles/registry.json":                 `{"active_shape":"pub.polis.shapes.v4"}`,
		"site/snippets/about.md":                       "about\n",
		"empty.txt":                                    "",
		"binary.bin":                                   "\x00\x01\x02\xff\xfe",
	}

	posix := At(t.TempDir())
	for k, v := range content {
		if err := posix.Store().Put(ctx, k, []byte(v)); err != nil {
			t.Fatalf("seeding %s: %v", k, err)
		}
	}

	// POSIX → blob
	f := newFakeBlobAPI(t)
	blob := f.store(t, "tenants/alice")
	if err := copyAll(ctx, posix.Store(), blob); err != nil {
		t.Fatalf("posix → blob: %v", err)
	}

	// blob → a fresh POSIX
	back := At(t.TempDir())
	if err := copyAll(ctx, blob, back.Store()); err != nil {
		t.Fatalf("blob → posix: %v", err)
	}

	// ⭐ Every file, byte for byte.
	for k, want := range content {
		got, err := back.Store().Get(ctx, k)
		if err != nil {
			t.Errorf("after the round trip, %s is missing: %v", k, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s differs after the round trip:\n  got  %q\n  want %q", k, got, want)
		}
	}

	// ⭐ And nothing extra arrived. A marker object for an empty prefix would
	// show up here, which is why the empty-directory decision is tested rather
	// than only written down.
	gotKeys, err := fileKeys(ctx, back.Store())
	if err != nil {
		t.Fatal(err)
	}
	var wantKeys []string
	for k := range content {
		wantKeys = append(wantKeys, k)
	}
	sort.Strings(wantKeys)
	if strings.Join(gotKeys, "\n") != strings.Join(wantKeys, "\n") {
		t.Errorf("the round trip changed the file set:\n  got  %v\n  want %v", gotKeys, wantKeys)
	}
}

// ⚠️ THE EMPTY-DIRECTORY LOSS, STATED AS A TEST. It is the one asymmetry, and
// pinning it means a future change that starts representing empty prefixes
// fails here and has to say why — rather than quietly adding marker objects.
func TestAnEmptyDirectoryDoesNotSurviveABlobRoundTrip(t *testing.T) {
	ctx := context.Background()

	posix := At(t.TempDir())
	// pkg/clone creates these so an empty site's tree looks populated.
	if err := posix.Store().Put(ctx, "content/pub.polis.core/comment/.keep", []byte("")); err != nil {
		t.Fatal(err)
	}
	if err := posix.Store().Delete(ctx, "content/pub.polis.core/comment/.keep"); err != nil {
		t.Fatal(err)
	}
	// The directory now exists on POSIX and holds nothing.
	entries, err := ListDir(posix.Path("content", "pub.polis.core", "comment"))
	if err != nil {
		t.Fatalf("the empty directory is not there to begin with: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected an empty directory, found %d entries", len(entries))
	}

	f := newFakeBlobAPI(t)
	blob := f.store(t, "")
	if err := copyAll(ctx, posix.Store(), blob); err != nil {
		t.Fatal(err)
	}

	// ⛔ EXPECTED: the prefix is simply not there. If this ever starts
	// returning something, a marker object has been invented — see
	// EmptyPrefixPolicy before changing it.
	got, err := blob.List(ctx, "content/pub.polis.core/comment")
	if err != nil {
		t.Fatalf("listing an absent prefix must not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("the blob store represents an empty directory as %v — if that is now "+
			"deliberate, EmptyPrefixPolicy and the round-trip test have to change together", got)
	}
	if EmptyPrefixPolicy == "" {
		t.Error("EmptyPrefixPolicy is empty; the decision must stay written down")
	}
}

// copyAll moves every file from one store to another, by key.
//
// ⭐ It is the migration primitive the modes round trip depends on, and it is
// deliberately this short: a recursive List plus Get plus Put is the whole of
// it, because the interface is whole-file.
func copyAll(ctx context.Context, from, to Store) error {
	entries, err := from.List(ctx, "", Recursive())
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		data, gerr := from.Get(ctx, e.Key)
		if gerr != nil {
			return gerr
		}
		if perr := to.Put(ctx, e.Key, data); perr != nil {
			return perr
		}
	}
	return nil
}

func fileKeys(ctx context.Context, s Store) ([]string, error) {
	entries, err := s.List(ctx, "", Recursive())
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir {
			out = append(out, e.Key)
		}
	}
	sort.Strings(out)
	return out, nil
}
