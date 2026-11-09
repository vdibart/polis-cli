package server

import (
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⛔ Passkeys are not offered where there is an operator: a custodied tenant
// signs in through the operator's accounts, and the plan keeps custodied
// untouched by owner auth.
func TestPasskeysAreUnavailableOnACustodiedTenant(t *testing.T) {
	p := NewPasskeyProvider()
	st := storage.NewObjectStore("")
	if p.Available(owner.Instance{Mode: mode.Custodied, Store: st, BaseURL: "https://a.polis.pub"}) {
		t.Error("passkeys are available on a custodied tenant")
	}
	if !p.Available(owner.Instance{Mode: mode.RemoteServerSigned, Store: st, BaseURL: "https://a.example"}) {
		t.Error("passkeys are unavailable on a remote instance, where they are the point")
	}
}
