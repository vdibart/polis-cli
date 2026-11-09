// Package capability derives what an instance can do from the mode it runs in,
// and publishes that as a versioned descriptor.
//
// ⭐⭐ DERIVE, NEVER SET. Nobody assigns a capability: one function maps a mode
// to the whole set, so an inconsistent combination cannot be expressed. This is
// Law 1's corollary — the mode is the SOURCE, the capability set is a
// PROJECTION.
//
// ⛔ WHAT CAPABILITIES BUY IS NOT EXPRESSIVENESS. With a four-valued mode,
// `mode == Custodied || mode == RemoteServerSigned` is a single test and every
// branch is expressible that way. What they buy is WHAT CROSSES THE BOUNDARY:
//
//   - a mode is a CLOSED set — every consumer must know all of it, and a fifth
//     mode breaks anyone who enumerated four.
//   - a capability set is OPEN — a consumer reads what it understands and
//     ignores the rest, the same principle as a tolerant signature verifier.
//
// ⛔ And telling a third-party surface `mode = "remote-server-signed"`
// publishes our deployment topology as a contract. Telling it
// `canRunHooks: false` does not.
//
// ⛔ NO CAPABILITY NAME MAY CONTAIN hosted, remote, local OR custodied. A name
// that leaks the topology defeats the point above, and a test enforces it.
//
// ⛔⛔ THE FIVE ARE DERIVED FROM *MODE*. A BACKEND PROPERTY IS A DIFFERENT AXIS
// AND DOES NOT BELONG HERE — whether storage can give a real POSIX path, carry
// file modes or report Stat metadata varies by STORAGE BACKEND, not by mode.
// pkg/storage declares its own; folding them in would reopen a closed audit.
//
// ⭐ THE MERGE RULE, because two capabilities agreeing today is not evidence
// they are one thing: MERGE WHEN IT IS THE SAME FACT. KEEP SEPARATE WHEN THE
// PATTERNS MERELY COINCIDE. hasPublicApi and actorsRunOnSchedule share a column
// pattern today and must stay separate; account, provisioning, login redirect
// and a shared parent domain are one fact, which is why they are hasOperator
// alone.
package capability

import (
	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
)

// Version is the descriptor's schema version.
//
// ⭐ VERSIONED FROM DAY ONE, while only our own surface reads it. It is the
// cheapest moment to do it: once a third party fetches this, adding a version
// afterwards means guessing what an unversioned response meant.
const Version = "pub.polis.capabilities.v1"

// Set is what an instance can do. ⭐ Every field is derived; none is settable
// from outside this package, which is what makes an inconsistent set
// impossible rather than merely discouraged.
type Set struct {
	// V is the descriptor schema version.
	V string `json:"v"`

	// HasPublicApi reports whether the instance exposes an API reachable from
	// outside the machine it runs on.
	//
	// ⚠️ REACHABILITY, NOT UPTIME. A serverless function does not stay up but
	// IS reachable; conflating the two is what gave a remote instance the
	// wrong answer about direct messaging.
	HasPublicApi bool `json:"hasPublicApi"`

	// HasOperator reports whether somebody other than the owner runs the
	// instance — and therefore whether there are accounts, provisioning, a
	// login redirect and a shared parent domain.
	//
	// ⭐ THOSE FOUR ARE ONE FACT, not four that happen to agree. That is why
	// this is a single capability and not a family of them.
	HasOperator bool `json:"hasOperator"`

	// CanRunHooks reports whether the instance can execute the owner's
	// post-action hooks. ⭐ True only where the owner controls the machine.
	CanRunHooks bool `json:"canRunHooks"`

	// ActorsRunOnSchedule reports whether background actors run on a schedule
	// here, rather than only when the owner invokes them.
	ActorsRunOnSchedule bool `json:"actorsRunOnSchedule"`

	// InstanceCanSignUnattended reports whether the instance holds an identity
	// key and may sign without the owner present.
	//
	// ⭐⭐ THE IMPORTANT ONE. It is the only capability that distinguishes the
	// two remote flavours, and that the split earns exactly one capability is
	// evidence it is load-bearing and narrow. It gates whether blessing
	// happens in real time or at login, whether there is a
	// reconcile-on-presence queue, whether the console shows work waiting for
	// the owner, and whether the publish interlock can ever fire.
	InstanceCanSignUnattended bool `json:"instanceCanSignUnattended"`
}

// For derives the capability set for a mode.
//
// ⛔ THIS IS THE ONLY PLACE THE TABLE EXISTS. A second mapping anywhere — a
// helper that "just needs one of them", a copy in JavaScript — is the
// scattering this replaced.
//
//	                             local   remote-client   remote-server   custodied
//	hasPublicApi                  no        yes            yes            yes
//	hasOperator                   no        no             no             yes
//	canRunHooks                   yes       no             no             no
//	actorsRunOnSchedule           no        yes            yes            yes
//	instanceCanSignUnattended     no        no             yes            yes
func For(m mode.Mode) Set {
	s := Set{V: Version}
	switch m {
	case mode.Local:
		s.CanRunHooks = true
	case mode.RemoteClientSigned:
		s.HasPublicApi = true
		s.ActorsRunOnSchedule = true
	case mode.RemoteServerSigned:
		s.HasPublicApi = true
		s.ActorsRunOnSchedule = true
		s.InstanceCanSignUnattended = true
	case mode.Custodied:
		s.HasPublicApi = true
		s.HasOperator = true
		s.ActorsRunOnSchedule = true
		s.InstanceCanSignUnattended = true
	}
	return s
}
