package owner

import (
	"net/http"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
)

// The operator provider: in custodied mode, somebody already checked.
//
// ⛔⛔ THIS EXISTS TO PREVENT A DOUBLE GATE, which is the failure the seam would
// otherwise cause on the one deployment that already works.
//
// On a custodied deployment the operator's front door authenticates a tenant
// session and only then hands the request to that tenant's server. If the
// tenant server now applied the seam and found nothing enrolled, a logged-in
// tenant would be refused by a provider they never heard of — locked out of a
// site they were already inside.
//
// ⭐ So the operator's check IS a provider: one more entry in the disjunction,
// available only in custodied. ⛔ NOT an early return and NOT a bypass. The
// difference matters because an early return is invisible to the enrolled-list
// UI, to Statuses, and to anyone reasoning about who can let someone in; a
// provider is none of those things.
//
// ⚠️ IT TRUSTS THE WRAPPER, AND SAYS SO RATHER THAN HIDING IT. The tenant server
// cannot see the operator's session cookie — it is checked upstream — so this
// provider asserts what is already true today, where the tenant server has no
// auth of its own at all. That makes it a FAITHFUL description of the existing
// trust rather than a new grant of it. ⭐ The same shape as loopback: both say
// "the reason a request got here is itself the evidence".
//
// ⛔ WHICH IS WHY Available() IS MODE-GATED AND MUST STAY SO. If this provider
// were ever available outside custodied, it would assert ownership for any
// request that reached the server — a universal bypass with a reassuring name.

// OperatorProvider recognises the owner on a custodied deployment, where the
// operator's front door has already authenticated the tenant's session.
type OperatorProvider struct{}

// Name implements Provider.
func (OperatorProvider) Name() string { return "operator" }

// Available reports whether an operator front door is in front of this server.
//
// ⛔ Custodied and nothing else. See the package comment: outside this mode the
// premise — that something upstream authenticated the request — is simply false.
func (OperatorProvider) Available(inst Instance) bool { return inst.Mode == mode.Custodied }

// Enrolled is true wherever available: the tenant did not choose this and
// cannot remove it, because it is the operator's arrangement rather than
// theirs.
//
// ⚠️ It is still LISTED, so a tenant reading their provider list sees that the
// operator can admit them. ⭐ That is the honest disclosure the registry's
// "expected, never allowed" principle asks for — the operator holds the keys
// either way, and hiding the row would not change that, only who knows it.
func (p OperatorProvider) Enrolled(inst Instance) bool { return p.Available(inst) }

// IsOwner accepts the request.
//
// ⭐ There is no per-request test here for the same reason as loopback: the
// check already happened, upstream, and repeating it would mean reimplementing
// the operator's session handling inside the tenant server — two
// implementations of one decision, which is the drift this seam exists to
// remove.
func (OperatorProvider) IsOwner(Instance, *http.Request) (bool, error) { return true, nil }

func init() { Register(OperatorProvider{}) }
