// Package owner answers one question: did this request come from the person
// whose site this is?
//
// ⭐⭐⭐ WHY IT EXISTS, AND IT IS NOT "ADD LOGIN TO THE WEBAPP". `polis serve` is
// OWNER-TRUST BY CONSTRUCTION: it hardcodes a loopback bind and serves every
// privileged endpoint with no auth, because reaching loopback on a single-user
// machine means the owner. The bind site says so, and warns:
//
//	"Do NOT add a --host / --bind option without also gating sensitive
//	 endpoints behind auth. The handlers themselves trust loopback
//	 unconditionally."
//
// A remote instance IS that forbidden thing — the same binary, reachable from
// the internet. This package is the precondition that warning names.
//
// ⭐ AND LOOPBACK TRUST WAS ALREADY A PROVIDER IN DISGUISE. Making it one
// explicitly is what lets a laptop keep behaving exactly as it did: see
// LoopbackProvider, whose Available() is false the moment the listener is not
// loopback, which is precisely when the old assumption stopped being true.
//
// ⛔ SELECTION IS A DISJUNCTION, NOT A CHOICE: any ENROLLED provider may answer
// yes. That looks like a weakness and is not — every provider asserts the SAME
// single fact about the SAME single owner, and there are no privilege levels to
// escalate between. ⚠️ But the WEAKEST enrolled provider sets the floor, which
// is why the owner must be able to see the list and remove any of it.
package owner

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// Instance is what a provider needs to know about where it is running, to
// decide whether it can work here at all.
//
// ⚠️ IT DESCRIBES THE DEPLOYMENT, NEVER THE REQUEST. Anything per-request
// belongs in IsOwner's *http.Request; mixing them is how a provider ends up
// deciding availability from something an attacker controls.
type Instance struct {
	// Mode is how this instance is deployed.
	Mode mode.Mode

	// SiteID distinguishes two sites that share a base URL (two servers on one
	// machine, a copy of a site). ⛔ Sessions are keyed by it as well as the
	// base URL, so one cannot open the other.
	SiteID string

	// Store addresses THIS SITE's files, and is how a stateful provider
	// reaches its enrolment record.
	//
	// ⛔ IT IS ON THE INSTANCE RATHER THAN ON THE PROVIDER BECAUSE THE REGISTRY
	// IS PROCESS-GLOBAL AND HOSTED RUNS MANY TENANTS IN ONE PROCESS. A provider
	// holding its own store would share one site's enrolment with every other
	// tenant — the worst bug an auth system can have, and one that appears only
	// in production.
	//
	// ⭐ Nil is legitimate: loopback and operator are stateless, and a caller
	// that only asks those does not need a store.
	Store storage.Store

	// BaseURL is the site's own origin, as configured.
	//
	// ⛔ IT IS CONFIGURATION, NEVER A REQUEST HEADER. A provider that binds a
	// credential to an origin — WebAuthn does — would otherwise bind it to
	// whatever Host an attacker sent, producing a credential usable from the
	// attacker's site. ⚠️ And the failure is not at enrolment: it shows up at
	// sign-in, later, for an owner who has already discarded their old way in.
	BaseURL string

	// Loopback reports whether the listener is bound to a loopback address.
	//
	// ⛔ IT IS SET FROM THE LISTENER, NEVER FROM A REQUEST. A header, a
	// RemoteAddr or a forwarded-for can all be forged; the address the process
	// bound to cannot. Deriving this per request is the one mistake that turns
	// the loopback provider from a safety property into a bypass.
	Loopback bool

	// Dev reports a developer server (`polis serve --dev`). ⭐ It lets loopback
	// trust hold on a dev server, which runs as remote-client-signed so its
	// messaging UI is live. ⛔ SET FROM THE COMMAND LINE ONLY, by the
	// localhost-bound server that parsed `--dev`, never from a request or the
	// environment — so a real remote instance, port-forwarded or not, never
	// has it.
	Dev bool

	// Cache is the serving process's memory of what Store last said: the MAC
	// key, the session generations and the enrolment records.
	//
	// ⛔ COPIES, NEVER STATE. See Cache: everything in it can vanish between
	// two requests and the answers must not change. ⭐ Nil means "read the
	// store every time", which is correct and only slower.
	Cache *Cache
}

// Provider is one way of recognising the owner.
//
// ⭐ The three predicates are deliberately separate, and each answers a
// different question at a different time:
//
//	Available  can it work HERE at all?        — deployment-time fact
//	Enrolled   has the owner set it up?        — per-site state
//	IsOwner    is THIS request from the owner? — per-request
//
// ⛔ Collapsing Available into Enrolled is the obvious simplification and it is
// wrong: a provider the owner enrolled on a laptop must not silently answer on
// a server where its premise no longer holds.
type Provider interface {
	// Name identifies the provider. ⚠️ It is a stable key: enrolment state is
	// stored under it, so renaming one orphans what the owner enrolled.
	Name() string

	// Available reports whether this provider can work on this instance.
	Available(Instance) bool

	// Enrolled reports whether the owner has set this provider up.
	Enrolled(Instance) bool

	// IsOwner decides whether the request came from the owner.
	//
	// ⛔ IT TAKES THE INSTANCE FOR THE SAME REASON Available AND Enrolled DO,
	// and the asymmetry was a design error caught while building passkey
	// sign-in. The registry is process-global and hosted runs many tenants in
	// one process, so a provider that must consult per-site state — a signed-in
	// session, a stored credential — has no other way to know WHICH site is
	// asking. Without it, a stateful provider either cannot work or shares one
	// tenant's session with every other.
	//
	// ⛔ AN ERROR IS NOT A DENIAL AND IS NOT AN APPROVAL. Return (false, err)
	// when the provider could not decide; the disjunction keeps asking the
	// others and surfaces the error either way. A provider that returns
	// (false, nil) is saying "this is definitely not the owner".
	IsOwner(Instance, *http.Request) (bool, error)
}

var (
	mu        sync.RWMutex
	providers = map[string]Provider{}
)

// Register adds a provider.
//
// ⛔ REGISTRATION IS COMPILE-TIME, by package init or explicit wiring. There is
// no config file of providers and no dynamic loading: the set of things that
// can call you the owner is a property of the binary, and a deployment that
// could add one at runtime has a much larger attack surface than one that
// cannot. ⭐ Available() is the runtime half, and it is what makes the seam
// honest across modes.
func Register(p Provider) {
	if p == nil {
		panic("owner: Register(nil)")
	}
	name := p.Name()
	if name == "" {
		panic("owner: provider with an empty name")
	}
	mu.Lock()
	defer mu.Unlock()
	if _, dup := providers[name]; dup {
		// ⛔ Two providers under one name means enrolment state is ambiguous,
		// and the loser would silently never be consulted.
		panic(fmt.Sprintf("owner: provider %q registered twice", name))
	}
	providers[name] = p
}

// Registered returns every registered provider, name-ordered.
//
// ⭐ Sorted so the enrolled-provider list the owner sees has a stable order,
// rather than Go's randomised map iteration making it jump around.
func Registered() []Provider {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Provider, 0, len(providers))
	for _, p := range providers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Status is one provider's standing on this instance, for the list the owner
// sees and for diagnostics.
type Status struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Enrolled  bool   `json:"enrolled"`

	// Enrollable reports whether the owner sets this provider up (and so can
	// remove it). ⭐ Loopback and operator trust are not: their premise is the
	// deployment itself, and there is nothing stored to delete.
	Enrollable bool `json:"enrollable"`

	// Kind names the browser-side ceremony the provider's begin/complete
	// steps carry (see Ceremonial), or is empty when there is none.
	//
	// ⛔ THIS IS WHAT A CLIENT BRANCHES ON, NEVER Name. A client that keys its
	// code by provider name has to change for every provider added; one keyed
	// by ceremony kind renders a new provider of a known kind with no change,
	// and an unknown kind as a listed row it cannot run.
	Kind string `json:"kind,omitempty"`
}

// Ceremonial is implemented by a provider whose enrolment and sign-in carry a
// standard browser ceremony, and names it ("webauthn").
//
// ⭐ Optional, and kept apart from Provider on purpose: most of what makes a
// provider a provider has no client side at all, and the kind is a fact for
// clients, not for the disjunction.
type Ceremonial interface {
	Ceremony() string
}

// Statuses reports every registered provider's standing.
//
// ⭐ IT LISTS UNAVAILABLE ONES TOO, on purpose. An owner moving a site from a
// laptop to a server needs to see that the provider they relied on no longer
// applies — showing only what is usable would make it vanish silently, which
// is the moment they most need telling.
func Statuses(inst Instance) []Status {
	out := []Status{}
	for _, p := range Registered() {
		avail := p.Available(inst)
		st := Status{
			Name:      p.Name(),
			Available: avail,
			// ⚠️ Enrolment is only meaningful where the provider applies.
			Enrolled: avail && p.Enrolled(inst),
		}
		_, st.Enrollable = p.(Enroller)
		if c, ok := p.(Ceremonial); ok {
			st.Kind = c.Ceremony()
		}
		out = append(out, st)
	}
	return out
}

// Active returns the providers that may answer on this instance: available AND
// enrolled. ⭐ This is the disjunction's membership, and the thing the owner is
// really looking at when they review what can let someone in.
func Active(inst Instance) []Provider {
	var out []Provider
	for _, p := range Registered() {
		if p.Available(inst) && p.Enrolled(inst) {
			out = append(out, p)
		}
	}
	return out
}

// ErrNoProvider is returned when nothing is enrolled on this instance.
//
// ⭐ IT IS DISTINCT FROM "not the owner" BECAUSE THE REMEDIES DIFFER: a request
// that failed every provider needs different credentials; a request on an
// instance with no providers needs the owner to prove ownership with the recovery key, then enrol one.
// A caller that cannot tell them apart cannot say anything useful.
var ErrNoProvider = errors.New("owner: no provider is enrolled on this instance")

// IsOwner asks every active provider, and is satisfied by any one of them.
//
// ⛔ THE TWO RULES THAT MATTER:
//
//  1. AN ERROR IS NEVER A YES. A provider that fails is skipped, not trusted,
//     and the others still get asked — one broken provider must not be able to
//     lock the owner out of their own site, and must not be able to let anyone
//     in either.
//  2. NO PROVIDERS MEANS NO, with ErrNoProvider. ⚠️ The tempting alternative —
//     "nothing is configured, so allow everything until setup" — is a fully
//     open instance during the exact window an attacker would most like one.
func IsOwner(inst Instance, r *http.Request) (bool, error) {
	active := Active(inst)
	if len(active) == 0 {
		return false, ErrNoProvider
	}
	var failures []error
	for _, p := range active {
		ok, err := p.IsOwner(inst, r)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", p.Name(), err))
			continue
		}
		if ok {
			// ⭐ First yes wins and the rest are not consulted: they would all
			// be asserting the same fact about the same owner.
			return true, nil
		}
	}
	if len(failures) > 0 {
		// ⚠️ Reported alongside the denial, never instead of it. A caller that
		// treats "some provider errored" as a reason to allow has inverted it.
		return false, errors.Join(failures...)
	}
	return false, nil
}

// reset clears the registry. Test-only; there is no production path that
// unregisters a provider.
func reset() {
	mu.Lock()
	defer mu.Unlock()
	providers = map[string]Provider{}
}
