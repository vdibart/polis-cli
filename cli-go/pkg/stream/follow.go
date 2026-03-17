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
type FollowerState struct {
	Followers []string `json:"followers"`
	Count     int      `json:"count"`
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

	// Build a set from current followers for O(1) lookup
	followerSet := make(map[string]bool, len(fs.Followers))
	for _, f := range fs.Followers {
		followerSet[f] = true
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
		case "pub.polis.follow.removed":
			delete(followerSet, actor)
		}
	}

	// Rebuild slice from set
	followers := make([]string, 0, len(followerSet))
	for f := range followerSet {
		followers = append(followers, f)
	}

	return &FollowerState{
		Followers: followers,
		Count:     len(followers),
	}, nil
}
