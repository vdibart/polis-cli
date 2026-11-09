package storage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// The claim Sub exists to make true: a tenant under a prefix and an instance
// at the root are the same store seen from different roots, so the SAME keys
// work in both and an export is a copy either way.
func TestSubMakesATenantAndAStandaloneSiteIdentical(t *testing.T) {
	ctx := context.Background()
	hostRoot := storage.NewPOSIX(t.TempDir()) // a multi-tenant host
	standalone := storage.NewPOSIX(t.TempDir())

	tenant, err := storage.Sub(hostRoot, "tenants/alice")
	if err != nil {
		t.Fatal(err)
	}

	// The same site paths, written through both.
	keys := []string{
		"content/pub.polis.core/post/20261001/hello.md",
		".well-known/polis",
	}
	for _, k := range keys {
		if err := tenant.Put(ctx, k, []byte("same bytes")); err != nil {
			t.Fatalf("tenant Put(%q): %v", k, err)
		}
		if err := standalone.Put(ctx, k, []byte("same bytes")); err != nil {
			t.Fatalf("standalone Put(%q): %v", k, err)
		}
	}

	// A listing of each yields the same key set — no prefix leaks out.
	for _, st := range []struct {
		name  string
		store storage.Store
	}{{"tenant", tenant}, {"standalone", standalone}} {
		entries, err := st.store.List(ctx, "", storage.Recursive())
		if err != nil {
			t.Fatalf("%s List: %v", st.name, err)
		}
		var files []string
		for _, e := range entries {
			if !e.IsDir {
				files = append(files, e.Key)
			}
		}
		if len(files) != len(keys) {
			t.Fatalf("%s: got %d files, want %d: %v", st.name, len(files), len(keys), files)
		}
		for _, f := range files {
			if got, err := st.store.Get(ctx, f); err != nil || string(got) != "same bytes" {
				t.Errorf("%s: Get(%q) = %q, %v", st.name, f, got, err)
			}
		}
	}

	// And the host sees them under the prefix, which the tenant never does.
	if _, err := hostRoot.Get(ctx, "tenants/alice/.well-known/polis"); err != nil {
		t.Errorf("host cannot see the tenant's file at its prefixed key: %v", err)
	}
}

func TestSubCannotEscapeItsPrefix(t *testing.T) {
	ctx := context.Background()
	host := storage.NewPOSIX(t.TempDir())
	if err := host.Put(ctx, "tenants/bob/secret.md", []byte("bob's")); err != nil {
		t.Fatal(err)
	}
	alice, err := storage.Sub(host, "tenants/alice")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"../bob/secret.md", "../../etc/passwd", "/etc/passwd"} {
		if _, err := alice.Get(ctx, k); !errors.Is(err, storage.ErrInvalidKey) {
			t.Errorf("Get(%q) = %v, want ErrInvalidKey", k, err)
		}
	}
	// Deleting its own root is refused, as it is on POSIX.
	if err := alice.Delete(ctx, "", storage.DeleteRecursive()); !errors.Is(err, storage.ErrInvalidKey) {
		t.Errorf("Delete(root) = %v, want ErrInvalidKey", err)
	}
}

func TestSubWithEmptyPrefixIsTheParent(t *testing.T) {
	host := storage.NewPOSIX(t.TempDir())
	got, err := storage.Sub(host, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != storage.Store(host) {
		t.Error("Sub with an empty prefix should return the parent unchanged")
	}
}
