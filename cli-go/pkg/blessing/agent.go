package blessing

import (
	"fmt"

	"github.com/vdibart/polis-cli/cli-go/pkg/agent"
	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/hooks"
)

// A user agent's blessing decisions (Signet epic 11).
//
// ⭐ SIGNED WITH THE USER'S KEY, MARKED INSIDE THE SIGNATURE. The relationship
// request and, for a grant, the blessed.json entry both carry {agent, grant},
// so every decision an agent made is distinguishable from the user's own and
// traceable to the record she made it under.
//
// ⛔ NO FALLBACK. Both functions REQUIRE a *attestation.LiveGrant — a type only
// the resolver can construct — and refuse a nil or zero one with
// agent.ErrNoLiveGrant, signing nothing. There is deliberately no path from
// here to the unmarked Grant/Deny: an agent that cannot cite a live grant does
// not act, even where the key is sitting in memory.

func markerFor(g *attestation.LiveGrant) discovery.AgentMarker {
	return discovery.AgentMarker{Agent: g.Agent(), Grant: g.URL()}
}

// GrantAsAgent approves a blessing request as a user agent under a live grant.
func GrantAsAgent(siteDir string, request *IncomingRequest, client *discovery.Client, hookConfig *hooks.HookConfig, privateKey []byte, live *attestation.LiveGrant) (*GrantResult, error) {
	if !live.Valid() {
		return nil, agent.ErrNoLiveGrant
	}
	return grant(siteDir, request, client, hookConfig, privateKey, markerFor(live))
}

// DenyAsAgent turns a blessing request away as a user agent under a live grant.
// Like Deny, it writes nothing locally; its marker lives in the relationship
// request (and, once epic 45's DS records it, on the DS event).
func DenyAsAgent(commentURL, targetURL string, client *discovery.Client, privateKey []byte, live *attestation.LiveGrant) (*DenyResult, error) {
	if !live.Valid() {
		return nil, agent.ErrNoLiveGrant
	}
	if err := client.UpdateRelationshipMarked("pub.polis.comment.blessing", commentURL, targetURL, "deny", markerFor(live), privateKey); err != nil {
		return nil, fmt.Errorf("failed to deny blessing: %w", err)
	}
	return &DenyResult{Success: true}, nil
}
