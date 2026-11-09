package storage

import (
	"context"
	"testing"
)

// ⭐⭐⭐ THE FIRST REAL TEST OF THE SEAM. The detach was justified on the claim
// that a second backend drops in without touching callers, and until this
// existed there was only one implementation, so the claim was untested.
//
// ⚠️ An object store is not a weaker filesystem — it is a flat keyspace with no
// directories, no modes and no path. If the contract suite passes against both
// it and POSIX, the interface is describing something both can honour rather
// than describing POSIX in abstract words.
func TestContractAgainstObjectStore(t *testing.T) {
	r := RunContract(context.Background(), func() (Store, func()) {
		return NewObjectStore(""), nil
	})
	t.Log(r.String())
	if !r.OK() {
		t.Errorf("object store fails the contract:\n%s", r.String())
	}
}

// ⭐ The same store namespaced inside a bucket, which is how a hosted
// deployment would run many tenants. ⛔ The prefix must be invisible through
// the interface: a caller's keys are its own, and a tenant that can see its
// prefix can guess a neighbour's.
func TestContractAgainstPrefixedObjectStore(t *testing.T) {
	r := RunContract(context.Background(), func() (Store, func()) {
		return NewObjectStore("tenants/alice"), nil
	})
	t.Log(r.String())
	if !r.OK() {
		t.Errorf("prefixed object store fails the contract:\n%s", r.String())
	}
}

// ⛔ A key must not escape its prefix. On a shared bucket this check is the
// only boundary between one tenant's data and another's — a POSIX store is also
// bounded by the filesystem, but an object store has nothing else.
func TestObjectStoreRefusesToEscapeItsPrefix(t *testing.T) {
	ctx := context.Background()
	alice := NewObjectStore("tenants/alice")
	for _, bad := range []string{
		"../bob/secret.txt",
		"../../tenants/bob/secret.txt",
		"a/../../bob/secret.txt",
		`..\bob\secret.txt`,
	} {
		if err := alice.Put(ctx, bad, []byte("x")); err == nil {
			t.Errorf("Put(%q) succeeded — a traversing key escaped the tenant's prefix", bad)
		}
		if _, err := alice.Get(ctx, bad); err == nil {
			t.Errorf("Get(%q) succeeded — a traversing key escaped the tenant's prefix", bad)
		}
	}
}

// ⭐ Two tenants in one bucket cannot see each other.
func TestTwoPrefixedStoresAreIsolated(t *testing.T) {
	ctx := context.Background()
	// ⚠️ Sharing one backing map is the point: this is one bucket.
	shared := NewObjectStore("")
	alice := &ObjectStore{objects: shared.objects, prefix: "tenants/alice"}
	bob := &ObjectStore{objects: shared.objects, prefix: "tenants/bob"}

	if err := alice.Put(ctx, "secret.txt", []byte("alice's")); err != nil {
		t.Fatal(err)
	}
	if ok, _ := bob.Exists(ctx, "secret.txt"); ok {
		t.Error("bob can see alice's key at the same relative name")
	}
	got, err := alice.Get(ctx, "secret.txt")
	if err != nil || string(got) != "alice's" {
		t.Errorf("alice cannot read her own key: %q, %v", got, err)
	}
	entries, err := bob.List(ctx, "", Recursive())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("bob's listing shows %d entries from a bucket where only alice has written", len(entries))
	}
}
