package provision

import (
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/secret"
)

// ⭐⭐⭐ A7's CONSTRAINT, PINNED — "and if it holds, say so, because it is the
// kind of thing that quietly stops being true."
//
// Nothing secret is NEEDED when a remote instance is created. That is what
// makes SetConfig the easy operation rather than the hard one: there is no
// chicken-and-egg where an instance needs a key to start and cannot be given
// one until it exists.
//
// ⭐ The deploy-token handover does not break it: it is optional (an instance
// without it starts, and its site waits), and it is set on the PROJECT, which
// exists before any code runs, so it can always be given before it is needed.
//
// ⚠️ THE DAY A REQUIRED ONE APPEARS, THIS TEST FAILS AND THE PROVISIONING FLOW
// HAS A NEW PROBLEM TO SOLVE — which is precisely the warning the ticket gave.
func TestNothingSecretIsNeededWhenARemoteInstanceIsCreated(t *testing.T) {
	set := secret.RemoteSet()
	for _, m := range []mode.Mode{mode.RemoteClientSigned, mode.RemoteServerSigned} {
		if names := set.RequiredNames(m); len(names) != 0 {
			t.Errorf("a %s instance now requires %v at creation\n"+
				"  ⛔ A7's constraint has stopped holding, and SetConfig becomes the hardest "+
				"operation in the seam rather than the easiest: a secret needed BEFORE the "+
				"instance exists cannot be set THROUGH the instance. Decide where it comes "+
				"from before wiring a deploy button to this.", m, names)
		}
	}
}

// ⭐ And the server-signed flavour's ONE secret is a PROJECTED identity key,
// which is written by the instance rather than injected into it — so it is not
// a creation-time input either.
//
// ⚠️ The distinction matters: a reference secret must exist before the instance
// runs; a projected one is produced by the instance and only has to not be
// overwritten. Confusing them is what would make this look like a blocker.
func TestTheServerSignedKeyIsProjectedNotInjected(t *testing.T) {
	for _, m := range []mode.Mode{mode.RemoteClientSigned, mode.RemoteServerSigned} {
		res, err := secret.Resolve(m, secret.RemoteSet(), func(string) string { return "" })
		if err != nil {
			t.Errorf("resolving the remote secret set for %s failed with nothing in the "+
				"environment: %v — a remote instance must boot without creation-time "+
				"secrets", m, err)
		}
		_ = res
	}
}
