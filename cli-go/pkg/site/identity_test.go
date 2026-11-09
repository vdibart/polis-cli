package site

import (
	"bytes"
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/capability"
	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

func TestAnInstanceWithNoKeyGeneratesOneIntoItsOwnStore(t *testing.T) {
	dir := t.TempDir()
	site := storage.At(dir)
	k, err := EnsureIdentityKey(context.Background(), site)
	if err != nil {
		t.Fatal(err)
	}
	if !k.Created {
		t.Error("a site with no key reported the key as pre-existing")
	}
	if err := signing.ValidatePrivateKey(k.Private); err != nil {
		t.Fatalf("generated key does not parse: %v", err)
	}
	// ⛔ 0600 on POSIX: it is a private key.
	fi, err := os.Stat(site.Path(PrivateKeyKey))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("private key mode %o, want 600", fi.Mode().Perm())
	}
	// ⭐ The public half is derived from the private one.
	want, _ := signing.PublicKeyFromPrivate(k.Private)
	if !bytes.Equal(k.Public, want) {
		t.Error("public key does not match the private key")
	}

	again, err := EnsureIdentityKey(context.Background(), site)
	if err != nil {
		t.Fatal(err)
	}
	if again.Created || !bytes.Equal(again.Private, k.Private) {
		t.Error("a second call made a new key; it must return the existing one")
	}
}

// ⛔⛔ THE FIRST-BOOT RACE. Each goroutine gets its OWN store handle on the same
// site, standing in for separate cold starts: an in-process lock would not
// reach between them, so this passes only if the store's conditional create
// does the excluding.
func TestConcurrentColdStartsProduceOneIdentity(t *testing.T) {
	for name, open := range map[string]func() storage.Site{
		"posix": func() storage.Site { return storage.At(sharedDir(t)) },
		"object": func() storage.Site {
			return storage.On("/site", sharedObject(t))
		},
	} {
		t.Run(name, func(t *testing.T) {
			resetShared()
			const n = 12
			var wg sync.WaitGroup
			keys := make([]IdentityKey, n)
			errs := make([]error, n)
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					keys[i], errs[i] = EnsureIdentityKey(context.Background(), open())
				}(i)
			}
			wg.Wait()
			created := 0
			for i := 0; i < n; i++ {
				if errs[i] != nil {
					t.Fatalf("start %d: %v", i, errs[i])
				}
				if keys[i].Created {
					created++
				}
				if !bytes.Equal(keys[i].Private, keys[0].Private) {
					t.Fatalf("start %d holds a DIFFERENT identity: two keys for one site", i)
				}
			}
			if created != 1 {
				t.Errorf("%d starts reported creating the key; exactly one may", created)
			}
		})
	}
}

var (
	sharedMu   sync.Mutex
	sharedPath string
	sharedObj  *storage.ObjectStore
)

func resetShared() { sharedMu.Lock(); sharedPath, sharedObj = "", nil; sharedMu.Unlock() }

func sharedDir(t *testing.T) string {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if sharedPath == "" {
		sharedPath = t.TempDir()
	}
	return sharedPath
}

func sharedObject(t *testing.T) *storage.ObjectStore {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if sharedObj == nil {
		sharedObj = storage.NewObjectStore("")
	}
	return sharedObj
}

// ⚠️ IT NEVER OVERWRITES, and a stale public half heals to the key that signs.
func TestAnExistingKeyIsKeptAndAStalePublicHalfHeals(t *testing.T) {
	site := storage.At(t.TempDir())
	ctx := context.Background()
	priv, _, _ := signing.GenerateKeypair()
	_, otherPub, _ := signing.GenerateKeypair()
	if err := site.Store().Put(ctx, PrivateKeyKey, priv, storage.WithMode(0o600)); err != nil {
		t.Fatal(err)
	}
	if err := site.Store().Put(ctx, PublicKeyKey, otherPub); err != nil {
		t.Fatal(err)
	}
	k, err := EnsureIdentityKey(ctx, site)
	if err != nil {
		t.Fatal(err)
	}
	if k.Created || !bytes.Equal(k.Private, priv) {
		t.Fatal("an existing private key was replaced")
	}
	onDisk, _ := site.Store().Get(ctx, PublicKeyKey)
	want, _ := signing.PublicKeyFromPrivate(priv)
	if !bytes.Equal(onDisk, want) {
		t.Error("a public half that disagreed with the private key was left in place")
	}
}

// ⛔ "Could not look" is not "absent": a read failure must not mint a key.
type failingGet struct{ storage.Store }

func (failingGet) Get(context.Context, string) ([]byte, error) {
	return nil, errors.New("store unreachable")
}

func TestAStoreThatCannotBeReadGeneratesNothing(t *testing.T) {
	inner := storage.NewObjectStore("")
	site := storage.On("/site", failingGet{inner})
	if _, err := EnsureIdentityKey(context.Background(), site); err == nil {
		t.Fatal("an unreadable store produced a key")
	}
	if ok, _ := inner.Exists(context.Background(), PrivateKeyKey); ok {
		t.Error("a key was written although the store could not be read")
	}
}

// ⭐ Only remote/server-signed bootstraps a key. Client-signed needs none —
// the owner signs — so it generates nothing; local and custodied make keys
// elsewhere (polis init; the operator at signup).
func TestOnlyAServerSignedInstanceBootstrapsAKey(t *testing.T) {
	for _, c := range []struct {
		m    mode.Mode
		want bool
	}{
		{mode.Local, false},
		{mode.RemoteClientSigned, false},
		{mode.RemoteServerSigned, true},
		{mode.Custodied, false},
	} {
		if got := NeedsIdentityKey(capability.For(c.m)); got != c.want {
			t.Errorf("%s: NeedsIdentityKey = %v, want %v", c.m, got, c.want)
		}
	}
}
