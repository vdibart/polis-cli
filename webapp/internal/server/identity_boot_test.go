package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// ⭐ A server-signed instance with no key generates one on first boot and is
// ready to sign — with no token or secret supplied from outside.
func TestAServerSignedInstanceBootsWithItsOwnKey(t *testing.T) {
	s := newTestServer(t)
	s.Mode = mode.RemoteServerSigned
	if err := s.EnsureIdentityKey(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.PrivateKey) == 0 || len(s.PublicKey) == 0 {
		t.Fatal("the instance generated a key but did not load it")
	}
	if _, err := os.Stat(filepath.Join(s.DataDir, site.PrivateKeyKey)); err != nil {
		t.Errorf("the key is not in the site's own store: %v", err)
	}
}

// ⛔ A client-signed instance needs no key, because the owner signs — so it
// generates NOTHING. A key appearing here would be a server key nobody asked
// for, on exactly the deployment chosen so the instance never holds one.
func TestAClientSignedInstanceGeneratesNothing(t *testing.T) {
	for _, m := range []mode.Mode{mode.RemoteClientSigned, mode.Local, ""} {
		s := newTestServer(t)
		s.Mode = m
		if err := s.EnsureIdentityKey(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(s.DataDir, site.PrivateKeyKey)); !os.IsNotExist(err) {
			t.Errorf("mode %q wrote an identity key (stat err %v)", m, err)
		}
		if len(s.PrivateKey) != 0 {
			t.Errorf("mode %q holds a private key", m)
		}
	}
}
