package storage

import (
	"context"
	"testing"
)

// ⭐ The POSIX store is the reference implementation, so the contract must pass
// against it. A check that fails here is a bug in the CONTRACT, not in the
// backend — which is why this runs first and why the suite was written against
// the interface document rather than against the POSIX code.
func TestContractAgainstPOSIX(t *testing.T) {
	r := RunContract(context.Background(), func() (Store, func()) {
		return NewSite(t.TempDir()), nil
	})
	t.Log(r.String())
	if !r.OK() {
		t.Errorf("POSIX store fails its own contract:\n%s", r.String())
	}
}

// ⭐ A POSIX store seen through Sub, the prefix decorator that makes a hosted
// tenant and a standalone site the same store from different roots. It must
// pass the same suite unchanged: a tenant cannot tell it lives under a prefix.
func TestContractAgainstSubRootedPOSIX(t *testing.T) {
	r := RunContract(context.Background(), func() (Store, func()) {
		s, err := Sub(NewSite(t.TempDir()), "tenants/alice")
		if err != nil {
			t.Fatal(err)
		}
		return s, nil
	})
	t.Log(r.String())
	if !r.OK() {
		t.Errorf("a Sub-rooted POSIX store fails the contract:\n%s", r.String())
	}
}
