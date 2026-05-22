package stream

import (
	"fmt"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
)

// FollowHandler is a built-in projection handler for follow/unfollow events.
// It maintains a set of followers for the local domain.
type FollowHandler struct {
	// MyDomain is the domain to filter events for. Only events where
	// payload.target_domain matches MyDomain are processed.
	MyDomain string
}

// FollowerState is the materialized state for the follow projection.
//
// Followers carries the list of follower domains (lowercased). FollowedAt
// is a parallel map from domain → ISO 8601 timestamp of the most recent
// pub.polis.follow.announced event for that domain. The map is omitempty
// so older state files (pre-2026-05-02, when timestamps were added) load
// cleanly without backfill — entries from the old format simply have no
// FollowedAt value, and new follow events stamp the map going forward.
type FollowerState struct {
	Followers  []string          `json:"followers"`
	FollowedAt map[string]string `json:"followed_at,omitempty"`
	Count      int               `json:"count"`
}

func (h *FollowHandler) TypePrefix() string { return "pub.polis.follow" }

func (h *FollowHandler) EventTypes() []string {
	return []string{"pub.polis.follow.announced", "pub.polis.follow.removed"}
}

func (h *FollowHandler) NewState() interface{} {
	return &FollowerState{}
}

func (h *FollowHandler) Process(events []discovery.StreamEvent, state interface{}) (interface{}, error) {
	fs, ok := state.(*FollowerState)
	if !ok {
		return nil, fmt.Errorf("follow handler: unexpected state type %T", state)
	}

	// Build a set from current followers for O(1) lookup. Carry the
	// existing FollowedAt map forward so timestamps for old entries
	// (recorded by previous syncs) survive a sync cycle that doesn't
	// touch them.
	followerSet := make(map[string]bool, len(fs.Followers))
	for _, f := range fs.Followers {
		followerSet[f] = true
	}
	followedAt := make(map[string]string, len(fs.FollowedAt))
	for k, v := range fs.FollowedAt {
		followedAt[k] = v
	}

	for _, evt := range events {
		// Only process events targeted at our domain (case-insensitive)
		targetDomain, _ := evt.Payload["target_domain"].(string)
		if targetDomain == "" || strings.ToLower(targetDomain) != strings.ToLower(h.MyDomain) {
			continue
		}

		actor := strings.ToLower(evt.Actor)
		switch evt.Type {
		case "pub.polis.follow.announced":
			followerSet[actor] = true
			// Record the event timestamp as the follow time. Subsequent
			// announce events overwrite — represents "current relationship
			// started at" rather than "first ever followed at", which
			// matches the UI's intent ("when did this user follow me?").
			if evt.Timestamp != "" {
				followedAt[actor] = evt.Timestamp
			}
		case "pub.polis.follow.removed":
			delete(followerSet, actor)
			delete(followedAt, actor)
		}
	}

	// Rebuild slice from set
	followers := make([]string, 0, len(followerSet))
	for f := range followerSet {
		followers = append(followers, f)
	}

	return &FollowerState{
		Followers:  followers,
		FollowedAt: followedAt,
		Count:      len(followers),
	}, nil
}
