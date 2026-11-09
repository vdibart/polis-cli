package site

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⛔ INIT NEVER OVERWRITES AN IDENTITY, ON ANY BACKEND.
//
// The site's directory does not exist, so a check that looked at a local path
// would find nothing there and let init go ahead over a site that already has
// keys. Each identity file is planted in the store alone; init must refuse, and
// must leave the planted bytes exactly as they were.
func TestInitRefusesAnExistingIdentityOnAStoreWithNoFilesystem(t *testing.T) {
	for _, key := range []string{privateKeyKey, publicKeyKey, WellKnownKey} {
		t.Run(key, func(t *testing.T) {
			ctx := context.Background()
			missing := filepath.Join(t.TempDir(), "no-such-dir")
			s := storage.On(missing, storage.NewObjectStore("tenants/alice"))
			planted := []byte("already here\n")
			if err := s.Store().Put(ctx, key, planted); err != nil {
				t.Fatal(err)
			}

			_, err := Init(s, InitOptions{})
			if err == nil || !strings.Contains(err.Error(), "already exists at "+key) {
				t.Fatalf("Init over an existing %s: err = %v, want a refusal naming the key", key, err)
			}
			got, err := s.Store().Get(ctx, key)
			if err != nil || !bytes.Equal(got, planted) {
				t.Errorf("%s after a refused init = %q, %v; want it untouched", key, got, err)
			}
			if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("a refused init touched the local path %s: %v", missing, err)
			}
		})
	}
}

// ⭐ A new site on a store with no filesystem: init writes the identity into
// the store and creates no local directory.
func TestInitOnAStoreWithNoFilesystem(t *testing.T) {
	ctx := context.Background()
	missing := filepath.Join(t.TempDir(), "no-such-dir")
	s := storage.On(missing, storage.NewObjectStore("tenants/alice"))

	res, err := Init(s, InitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.DirsCreated) != 0 {
		t.Errorf("DirsCreated = %v on a store with no directories", res.DirsCreated)
	}
	for _, key := range []string{privateKeyKey, publicKeyKey, WellKnownKey, "policies/rules.jsonl", ".polis/policies/rules.jsonl"} {
		if ok, err := s.Store().Exists(ctx, key); err != nil || !ok {
			t.Errorf("%s not in the store after init (%v)", key, err)
		}
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("init on an object store touched the local path %s: %v", missing, err)
	}
}

// On a filesystem the refusal names the real path, as it always has.
func TestInitRefusalNamesTheRealPathOnAFilesystem(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, ".polis", "keys", "id_ed25519")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Init(storage.At(dir), InitOptions{})
	want := "private key already exists at " + keyPath + " — refusing to overwrite"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

// existsFails is a store whose existence check cannot answer.
type existsFails struct{ storage.Store }

var errCannotLook = errors.New("cannot look")

func (existsFails) Exists(context.Context, string) (bool, error) { return false, errCannotLook }

// ⛔ "Could not look" is not "nothing there". A failed check refuses init, and
// a failed absence check is never read as absent, because that is what makes
// a caller write.
func TestAFailedExistenceCheckNeverReadsAsAbsent(t *testing.T) {
	s := storage.On(filepath.Join(t.TempDir(), "no-such-dir"), existsFails{storage.NewObjectStore("t")})

	if _, err := Init(s, InitOptions{}); err == nil || !errors.Is(err, errCannotLook) ||
		!strings.Contains(err.Error(), "refusing to overwrite") {
		t.Errorf("Init with a failing existence check: err = %v, want a refusal wrapping the failure", err)
	}

	absent, err := absentKey(s, "policies/rules.jsonl")
	if absent || !errors.Is(err, errCannotLook) {
		t.Errorf("absentKey with a failing check = %v, %v; want false and the error", absent, err)
	}
}

// The skeleton is unchanged on a filesystem: the same directories, the same
// modes, and the same list in the result.
func TestInitSkeletonOnAFilesystem(t *testing.T) {
	dir := t.TempDir()
	res, err := Init(storage.At(dir), InitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.DirsCreated) != len(initDirKeys)-1 {
		t.Errorf("DirsCreated has %d entries, want %d", len(res.DirsCreated), len(initDirKeys)-1)
	}
	for _, key := range initDirKeys[1:] {
		fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(key)))
		if err != nil || !fi.IsDir() {
			t.Errorf("%s: not a directory after init (%v)", key, err)
			continue
		}
		want := os.FileMode(0o755)
		if key == ".polis" || strings.HasPrefix(key, ".polis/") {
			want = 0o700
		}
		// The umask can only take bits away; a private directory must never gain any.
		if got := fi.Mode().Perm(); got&^want != 0 {
			t.Errorf("%s mode = %o, want at most %o", key, got, want)
		}
	}
	for key, want := range map[string]os.FileMode{
		privateKeyKey:                 0o600,
		".polis/policies/rules.jsonl": 0o600,
	} {
		fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(key)))
		if err != nil {
			t.Errorf("%s: %v", key, err)
			continue
		}
		if got := fi.Mode().Perm(); got&^want != 0 {
			t.Errorf("%s mode = %o, want at most %o", key, got, want)
		}
	}
}
