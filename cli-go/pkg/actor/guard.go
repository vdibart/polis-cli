package actor

import (
	"os"
	"path/filepath"

	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// A Guard is an actor's answer to "is this site one of ours?", built from the
// operator's published registry and used before operating on any site.
//
// # Why this exists at all
//
// D7's per-actor mapping — Rosie never on an actor site, Reaper never, Judge
// never on itself — was, until this type, enforced by NOTHING. It was a table
// in a document. This is what turns it from documentary into real, and it is
// defence in depth rather than the primary control: the operator holds every
// key on the box, so a Guard stops mistakes, not an operator who has decided to
// misbehave.
//
// # ⛔ IT READS FROM LOCAL DISK. NEVER AN HTTP FETCH.
//
// The registry is a published file, and the temptation is to fetch it. Three
// reasons that is disqualifying, not merely slower:
//
//  1. It puts a NETWORK DEPENDENCY inside Patrol, Medic and Judge sweeps, which
//     today run entirely off local state.
//  2. An unreachable operator forces a fail-open/fail-closed choice and BOTH
//     are bad — fail-open operates on actor sites, fail-closed stops the fleet.
//  3. ⛔ IT IS CIRCULAR. The operator site is itself one of the swept sites, so
//     Judge sweeping the operator would need the registry FROM the operator.
//
// ⭐ None of those apply to reading the same file off local disk, and the
// artifact the world fetches over HTTPS and the one an actor reads here are ONE
// FILE. Two artifacts kept in step is a drift problem you then have to build
// detection for; with one, drift is impossible rather than detectable.
//
// # ⭐ THE DEFENCE IN DEPTH IS THE SIGNATURE CHECK, not the file's location
//
// The registry sits in a tenant-shaped directory that Medic and Patrol write
// to. An actor must not trust those bytes merely because they are on our disk,
// so every registry is verified against its own operator site's published key —
// which is local too, so verification needs no network either.
//
// # Discovery is by pointer, and it has a useful side effect
//
// A site is an OPERATOR site iff its .well-known/polis carries an
// `actor_registry` pointer. There is no hardcoded handle anywhere here, which
// is what makes the check work IDENTICALLY SELF-HOSTED: a self-hoster running
// their own actors is an operator, publishes their own registry, and gets the
// same behaviour with no hosted-only special case.
type Guard struct {
	// byDomain maps an actor's domain to its verified registry entry.
	byDomain map[string]*Entry
	// operators lists the operator site handles whose registries were loaded
	// and verified.
	operators []string
	// registries is the count of registry files found, verified or not.
	registries int
	// verified is the count that verified against their operator's key.
	verified int
	// Problems holds registries found and NOT trusted, one message each. They
	// are LOUD by design — see Bootstrap.
	Problems []string
}

// Bootstrap reports that no trusted registry was found anywhere.
//
// ⚠️ A bootstrapping Guard excludes nothing, which is exactly what happens
// today with no registry at all, so the degradation is graceful. ⛔ But an
// actor that silently proceeds because the registry is missing is THE SAME
// FAILURE as one that never checked — so the caller must say so out loud, once
// per sweep. Fail-open, but never quiet.
func (g *Guard) Bootstrap() bool { return g.verified == 0 }

// IsActorDomain reports whether domain is listed in a trusted registry.
func (g *Guard) IsActorDomain(domain string) bool {
	_, ok := g.byDomain[domain]
	return ok
}

// EntryFor returns the trusted registry entry for a domain, or nil.
func (g *Guard) EntryFor(domain string) *Entry { return g.byDomain[domain] }

// Operators returns the handles of the operator sites whose registries were
// loaded and verified.
func (g *Guard) Operators() []string { return g.operators }

// Registries returns how many registry files were found, and how many of those
// were trusted. A gap between the two is the tampering or wrong-key case and is
// always reported in Problems.
func (g *Guard) Registries() (found, trusted int) { return g.registries, g.verified }

// LoadGuard walks tenantsDir, finds every site that publishes an
// `actor_registry` pointer, verifies each registry against that site's own
// published key, and indexes the trusted entries by domain.
//
// It never returns an error. Every failure mode — no registry, an unreadable
// one, a bad signature — resolves to a Guard that excludes fewer sites, plus a
// message in Problems. That is deliberate: an actor sweep must not be stoppable
// by a malformed file in one tenant directory.
func LoadGuard(tenantsDir string) *Guard {
	g := &Guard{byDomain: map[string]*Entry{}}

	entries, err := os.ReadDir(tenantsDir)
	if err != nil {
		g.Problems = append(g.Problems, "cannot read tenants directory: "+err.Error())
		return g
	}

	for _, e := range entries {
		if !e.IsDir() || e.Name() == "" || e.Name()[0] == '.' {
			continue
		}
		handle := e.Name()
		siteDir := filepath.Join(tenantsDir, handle)

		pointer := site.ActorRegistryPointer(siteDir)
		if pointer == "" {
			continue // an ordinary tenant: runs no actors, publishes no registry
		}
		g.registries++

		path := registryPathFromPointer(siteDir, pointer)
		r, err := Load(path)
		if err != nil {
			// ⛔ A POINTER TO A MISSING FILE IS THE DELETION CASE, and it must
			// be loud. Without this the checks would quietly stop and look
			// exactly like a fleet that has no actors.
			g.Problems = append(g.Problems,
				"registry missing for operator "+handle+" (pointer "+pointer+"): "+err.Error())
			continue
		}

		wk, err := site.LoadWellKnown(siteDir)
		if err != nil || wk.PublicKey == "" {
			g.Problems = append(g.Problems,
				"no published key for operator "+handle+" — its registry cannot be trusted")
			continue
		}

		status, err := Verify(r, []byte(wk.PublicKey))
		if status != StatusValid {
			msg := "registry for operator " + handle + " is " + string(status)
			if err != nil {
				msg += ": " + err.Error()
			}
			// ⛔ NOT TRUSTED. An untrusted registry excludes nothing — the
			// alternative would let an attacker who can write one file remove
			// any tenant from Rosie's or Reaper's reach by naming it an actor.
			g.Problems = append(g.Problems, msg)
			continue
		}

		g.verified++
		g.operators = append(g.operators, handle)
		for i := range r.Actors {
			g.byDomain[r.Actors[i].Domain] = &r.Actors[i]
		}
	}

	return g
}

// registryPathFromPointer resolves a published pointer to a path on disk.
//
// A site-relative pointer is the SIGNED SOURCE path, not the rendered mount —
// RegistryURLPath writes it that way for the same reason the licence pointer
// does: a permanent address points at the bytes that were signed. An absolute
// URL names the operator's own origin and cannot be followed off disk, so it
// falls back to the type's declared content path.
//
// filepath.Clean on an absolute pointer collapses any "..", so a pointer
// cannot walk out of the site directory.
func registryPathFromPointer(siteDir, pointer string) string {
	if len(pointer) > 0 && pointer[0] == '/' {
		return filepath.Join(siteDir, filepath.Clean(pointer))
	}
	return RegistryPath(siteDir)
}
