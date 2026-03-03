package stream

import (
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
)

// ProjectionHandler defines how a projection processes stream events.
type ProjectionHandler interface {
	// TypePrefix returns the event type prefix this handler owns (e.g., "pub.polis.follow").
	TypePrefix() string

	// EventTypes returns the specific event types this handler consumes.
	EventTypes() []string

	// NewState returns a zero-value state for this projection.
	NewState() interface{}

	// Process applies events to the current state and returns the updated state.
	Process(events []discovery.StreamEvent, state interface{}) (interface{}, error)
}

// BuiltinHandlers maps projection names to their built-in handler implementations.
// Note: NotificationHandler is not included here because it uses a different
// processing model (rule-driven, writes to state.jsonl instead of projection state).
var BuiltinHandlers = map[string]ProjectionHandler{
	"pub.polis.follow":            &FollowHandler{},
	"pub.polis.comment.blessing":  &BlessingHandler{},
}

// SyncHandler processes batches of stream events as part of the unified sync loop.
// Implementations are registered with the webapp's sync loop and receive
// only the event types they declare interest in.
type SyncHandler interface {
	// Name returns a human-readable handler name (for logging).
	Name() string
	// EventTypes returns the event types this handler processes.
	EventTypes() []string
	// Process handles a batch of filtered events.
	// Returns a HandlerResult indicating what changed.
	Process(events []discovery.StreamEvent) HandlerResult
}

// HandlerResult reports the outcome of a SyncHandler.Process call.
type HandlerResult struct {
	FilesChanged bool  // triggers RenderSite
	NewItems     int   // count of items created (notifications, feed entries, etc.)
	Error        error // non-fatal; logged but doesn't block other handlers
}

// FilterEvents returns events whose Type matches any of the given types.
// If types contains "*", all events are returned.
func FilterEvents(events []discovery.StreamEvent, types []string) []discovery.StreamEvent {
	if len(types) == 0 {
		return nil
	}
	for _, t := range types {
		if t == "*" {
			return events
		}
	}
	typeSet := make(map[string]bool, len(types))
	for _, t := range types {
		typeSet[t] = true
	}
	var filtered []discovery.StreamEvent
	for _, evt := range events {
		if typeSet[evt.Type] {
			filtered = append(filtered, evt)
		}
	}
	return filtered
}
