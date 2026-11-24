package ops

import (
	"context"
	"sync"

	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
)

// The live grant a request acts under, carried from the one place a token is
// checked to every place that signs.
//
// ⭐ A SLOT, NOT A VALUE. The token is checked after the request's context is
// made, so the API installs an empty slot when a request arrives and the check
// fills it. Code that signs reads it back, in-process forwards included
// (they clone the context, so they share the slot).
//
// ⛔ FAIL CLOSED. A write under a personal token that finds no slot to fill is
// refused by the check: an act that cannot carry its marker is not made.

type grantSlotKey struct{}

type grantSlot struct {
	mu   sync.Mutex
	live *attestation.LiveGrant
}

// WithGrantSlot returns ctx with an empty slot for the request's grant.
func WithGrantSlot(ctx context.Context) context.Context {
	return context.WithValue(ctx, grantSlotKey{}, &grantSlot{})
}

// SetGrant fills the request's slot. It reports false when ctx has no slot
// (the caller must then refuse the act) or live is not a resolved grant.
func SetGrant(ctx context.Context, live *attestation.LiveGrant) bool {
	slot, ok := ctx.Value(grantSlotKey{}).(*grantSlot)
	if !ok || slot == nil || !live.Valid() {
		return false
	}
	slot.mu.Lock()
	slot.live = live
	slot.mu.Unlock()
	return true
}

// GrantFrom is the live grant the request acts under, or nil for the owner's
// own act.
func GrantFrom(ctx context.Context) *attestation.LiveGrant {
	slot, ok := ctx.Value(grantSlotKey{}).(*grantSlot)
	if !ok || slot == nil {
		return nil
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	return slot.live
}

// MarkerOf is the marker an act under live carries; the zero marker for nil.
func MarkerOf(live *attestation.LiveGrant) discovery.AgentMarker {
	if !live.Valid() {
		return discovery.AgentMarker{}
	}
	return discovery.AgentMarker{Agent: live.Agent(), Grant: live.URL()}
}
