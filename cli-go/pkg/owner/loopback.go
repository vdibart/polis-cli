package owner

import (
	"net/http"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
)

// The loopback provider: owner-trust by construction, said out loud.
//
// ⭐⭐⭐ THIS PROVIDER CHANGES NOTHING AND THAT IS ITS ENTIRE PURPOSE. Before it,
// `polis serve` trusted every request because the listener was bound to
// loopback, and the justification lived in a comment beside the bind string.
// Now the same justification is a predicate the seam evaluates, so:
//
//   - a laptop behaves exactly as it did — no enrolment, no prompt, no step;
//   - a remote instance gets Available() == false, so the disjunction is empty
//     until the owner enrols something real.
//
// ⭐ The security property of `remote` therefore FALLS OUT of the seam rather
// than being bolted on, and the migration risk for existing users is as close
// to zero as a change this size can be.
//
// ⚠️ THE WHOLE THING RESTS ON ONE FACT BEING TAKEN FROM THE LISTENER. See the
// comment on Instance.Loopback: a request header, RemoteAddr or
// X-Forwarded-For can each be forged, and deriving loopback from any of them
// converts this provider from a safety property into a universal bypass. It is
// set once, from the address the process bound to, and never recomputed.

// LoopbackProvider recognises the owner by the fact that the server is only
// reachable from the machine it runs on.
type LoopbackProvider struct{}

// Name implements Provider.
func (LoopbackProvider) Name() string { return "loopback" }

// Available reports whether loopback trust holds here.
//
// ⛔ BOTH CONDITIONS ARE REQUIRED, and the mode check is not redundant.
// Loopback alone would be true of a custodied deployment reached through a
// local proxy, and of a remote instance someone port-forwards to while
// debugging. ⭐ The mode says what this deployment IS; the bind says what is
// currently true of it. Only when both agree is "anyone who can reach me is
// the owner" a safe thing to believe.
//
// ⭐ A DEV SERVER (`polis serve --dev`) is the one exception to the mode check:
// it runs as remote-client-signed so its messaging UI is live, but it is still
// a laptop bound to loopback, and Dev is set only from its command line.
// Without this, --dev answered 403 to every private API call.
func (LoopbackProvider) Available(inst Instance) bool {
	return inst.Loopback && (inst.Mode == mode.Local || inst.Mode == "" || inst.Dev)
}

// Enrolled is true wherever the provider is available.
//
// ⭐ There is nothing to set up: the owner does not enrol their own laptop, and
// requiring them to would be exactly the new step this provider exists to
// avoid. ⚠️ It is still listed in Statuses, so an owner who moves the site to a
// server can see that it stopped applying rather than wondering why they are
// suddenly asked to log in.
func (p LoopbackProvider) Enrolled(inst Instance) bool { return p.Available(inst) }

// IsOwner accepts the request.
//
// ⭐ There is no per-request test, and adding one would be theatre: Available()
// already established that the only way to reach this process is from the
// machine it runs on. ⛔ In particular, do NOT "harden" this by checking
// r.RemoteAddr — that reintroduces a request-controlled input to a decision
// that is deliberately made from the listener alone, and a proxy in front of
// the server would make it fail for the owner while protecting nobody.
func (LoopbackProvider) IsOwner(Instance, *http.Request) (bool, error) { return true, nil }

func init() {
	// ⭐ Compile-time registration. The binary's capabilities are a property of
	// the binary, not of its configuration.
	Register(LoopbackProvider{})
}
