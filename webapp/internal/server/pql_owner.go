package server

import (
	"net/http"

	"github.com/vdibart/polis-cli/cli-go/pkg/pql"
)

// PQLRequiresOwner reports whether a /pql/ request asks for owner-private
// content: a first-person scope (me, my-*) or the drafts or dms types.
//
// ⛔⛔ ONE CHECK, USED BY EVERY SURFACE. The owner gate called /pql/ public
// because it was "scope-gated at its own layer", and the PQL handler said the
// gate lived "at the routing layer" — each trusted the other and nobody
// checked, so outside hosted /pql/ served drafts and decrypted DMs to anyone.
// Hosted's version was right; it now lives here, and hosted calls this.
//
// A placeholder default scope makes a tenant-relative sentence (no from/about
// clause) resolve to a non-first-person scope, so it stays public. An
// unparseable sentence is not owner-private: the handler answers 400.
func PQLRequiresOwner(r *http.Request) bool {
	f, err := pql.ParseURL(r.URL.Path, "@__tenant__")
	if err != nil || f == nil {
		return false
	}
	switch f.Type {
	case "dms", "drafts":
		return true
	}
	// ⭐ "to bless" is NOT the owner's alone: anyone may ask which comments
	// are waiting (Vincent, 2026-11-14: "no reason to prevent it in the
	// grammar or in the API"). Blessing controls who is notified and what the
	// author's page shows, not who may ask.
	return pql.IsFirstPerson(f.Scope)
}
