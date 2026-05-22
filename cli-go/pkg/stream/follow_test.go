package stream

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
)

func TestFollowHandler_TypePrefix(t *testing.T) {
	h := &FollowHandler{MyDomain: "bob.com"}
	if h.TypePrefix() != "pub.polis.follow" {
		t.Errorf("TypePrefix() = %q, want %q", h.TypePrefix(), "pub.polis.follow")
	}
}

func TestFollowHandler_EventTypes(t *testing.T) {
	h := &FollowHandler{MyDomain: "bob.com"}
	types := h.EventTypes()
	if len(types) != 2 {
		t.Fatalf("EventTypes() len = %d, want 2", len(types))
	}
	if types[0] != "pub.polis.follow.announced" {
		t.Errorf("EventTypes()[0] = %q, want %q", types[0], "pub.polis.follow.announced")
	}
	if types[1] != "pub.polis.follow.removed" {
		t.Errorf("EventTypes()[1] = %q, want %q", types[1], "pub.polis.follow.removed")
	}
}

func TestFollowHandler_ProcessFollow(t *testing.T) {
	h := &FollowHandler{MyDomain: "bob.com"}
	state := h.NewState()

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.follow.announced",
			Actor: "alice.com",
			Payload: map[string]interface{}{
				"target_domain": "bob.com",
			},
		},
		{
			ID:    json.Number("2"),
			Type:  "pub.polis.follow.announced",
			Actor: "charlie.com",
			Payload: map[string]interface{}{
				"target_domain": "bob.com",
			},
		},
	}

	result, err := h.Process(events, state)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	fs := result.(*FollowerState)
	if fs.Count != 2 {
		t.Errorf("Count = %d, want 2", fs.Count)
	}

	sort.Strings(fs.Followers)
	if fs.Followers[0] != "alice.com" || fs.Followers[1] != "charlie.com" {
		t.Errorf("Followers = %v, want [alice.com charlie.com]", fs.Followers)
	}
}

func TestFollowHandler_ProcessUnfollow(t *testing.T) {
	h := &FollowHandler{MyDomain: "bob.com"}

	// Start with existing followers
	state := &FollowerState{
		Followers: []string{"alice.com", "charlie.com"},
		Count:     2,
	}

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("3"),
			Type:  "pub.polis.follow.removed",
			Actor: "alice.com",
			Payload: map[string]interface{}{
				"target_domain": "bob.com",
			},
		},
	}

	result, err := h.Process(events, state)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	fs := result.(*FollowerState)
	if fs.Count != 1 {
		t.Errorf("Count = %d, want 1", fs.Count)
	}
	if len(fs.Followers) != 1 || fs.Followers[0] != "charlie.com" {
		t.Errorf("Followers = %v, want [charlie.com]", fs.Followers)
	}
}

func TestFollowHandler_IgnoresOtherDomains(t *testing.T) {
	h := &FollowHandler{MyDomain: "bob.com"}
	state := h.NewState()

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.follow.announced",
			Actor: "alice.com",
			Payload: map[string]interface{}{
				"target_domain": "someone-else.com", // not bob.com
			},
		},
	}

	result, err := h.Process(events, state)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	fs := result.(*FollowerState)
	if fs.Count != 0 {
		t.Errorf("Count = %d, want 0 (should ignore events for other domains)", fs.Count)
	}
}

func TestFollowHandler_Idempotent(t *testing.T) {
	h := &FollowHandler{MyDomain: "bob.com"}
	state := h.NewState()

	// Same actor follows twice
	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.follow.announced",
			Actor: "alice.com",
			Payload: map[string]interface{}{
				"target_domain": "bob.com",
			},
		},
		{
			ID:    json.Number("2"),
			Type:  "pub.polis.follow.announced",
			Actor: "alice.com",
			Payload: map[string]interface{}{
				"target_domain": "bob.com",
			},
		},
	}

	result, err := h.Process(events, state)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	fs := result.(*FollowerState)
	if fs.Count != 1 {
		t.Errorf("Count = %d, want 1 (duplicate follow should be idempotent)", fs.Count)
	}
}

func TestFollowHandler_EmptyEvents(t *testing.T) {
	h := &FollowHandler{MyDomain: "bob.com"}

	state := &FollowerState{
		Followers: []string{"alice.com"},
		Count:     1,
	}

	result, err := h.Process([]discovery.StreamEvent{}, state)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	fs := result.(*FollowerState)
	if fs.Count != 1 {
		t.Errorf("Count = %d, want 1 (empty events should preserve state)", fs.Count)
	}
}

func TestFollowHandler_FullCycle(t *testing.T) {
	// Test with Store integration
	dir := t.TempDir()
	store := NewStore(dir, "test.supabase.co", "pub.polis.core")
	h := &FollowHandler{MyDomain: "bob.com"}

	// Initial state
	state := h.NewState()
	store.LoadState(h.TypePrefix(), state)

	// First batch: alice follows
	events1 := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.follow.announced",
			Actor: "alice.com",
			Payload: map[string]interface{}{
				"target_domain": "bob.com",
			},
		},
	}

	result, err := h.Process(events1, state)
	if err != nil {
		t.Fatalf("Process batch 1: %v", err)
	}
	if err := store.SaveState(h.TypePrefix(), result); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	if err := store.SetCursor(h.TypePrefix(), "1"); err != nil {
		t.Fatalf("SetCursor: %v", err)
	}

	// Reload from disk
	var loaded FollowerState
	if err := store.LoadState(h.TypePrefix(), &loaded); err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if loaded.Count != 1 {
		t.Errorf("after batch 1: Count = %d, want 1", loaded.Count)
	}

	// Second batch: charlie follows, alice unfollows
	events2 := []discovery.StreamEvent{
		{
			ID:    json.Number("2"),
			Type:  "pub.polis.follow.announced",
			Actor: "charlie.com",
			Payload: map[string]interface{}{
				"target_domain": "bob.com",
			},
		},
		{
			ID:    json.Number("3"),
			Type:  "pub.polis.follow.removed",
			Actor: "alice.com",
			Payload: map[string]interface{}{
				"target_domain": "bob.com",
			},
		},
	}

	result2, err := h.Process(events2, &loaded)
	if err != nil {
		t.Fatalf("Process batch 2: %v", err)
	}

	fs2 := result2.(*FollowerState)
	if fs2.Count != 1 {
		t.Errorf("after batch 2: Count = %d, want 1", fs2.Count)
	}
	if len(fs2.Followers) != 1 || fs2.Followers[0] != "charlie.com" {
		t.Errorf("after batch 2: Followers = %v, want [charlie.com]", fs2.Followers)
	}

	// Verify cursor advanced
	cursor, _ := store.GetCursor(h.TypePrefix())
	if cursor != "1" {
		t.Errorf("cursor = %q, want %q", cursor, "1")
	}
}

func TestFollowHandler_FollowedAtTimestamps(t *testing.T) {
	h := &FollowHandler{MyDomain: "bob.com"}
	state := h.NewState()

	events := []discovery.StreamEvent{
		{
			ID:        json.Number("1"),
			Type:      "pub.polis.follow.announced",
			Actor:     "alice.com",
			Timestamp: "2026-03-01T10:00:00Z",
			Payload:   map[string]interface{}{"target_domain": "bob.com"},
		},
		{
			ID:        json.Number("2"),
			Type:      "pub.polis.follow.announced",
			Actor:     "charlie.com",
			Timestamp: "2026-04-15T18:30:00Z",
			Payload:   map[string]interface{}{"target_domain": "bob.com"},
		},
	}

	result, err := h.Process(events, state)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	fs := result.(*FollowerState)
	if fs.FollowedAt["alice.com"] != "2026-03-01T10:00:00Z" {
		t.Errorf("alice timestamp = %q, want %q", fs.FollowedAt["alice.com"], "2026-03-01T10:00:00Z")
	}
	if fs.FollowedAt["charlie.com"] != "2026-04-15T18:30:00Z" {
		t.Errorf("charlie timestamp = %q, want %q", fs.FollowedAt["charlie.com"], "2026-04-15T18:30:00Z")
	}
}

func TestFollowHandler_RefollowOverwritesTimestamp(t *testing.T) {
	// "Current relationship started at" semantics: the latest announce
	// wins. A user who unfollowed and re-follows shows the recent date.
	h := &FollowHandler{MyDomain: "bob.com"}
	state := &FollowerState{
		Followers:  []string{"alice.com"},
		FollowedAt: map[string]string{"alice.com": "2026-01-01T00:00:00Z"},
		Count:      1,
	}

	events := []discovery.StreamEvent{
		{
			ID:        json.Number("1"),
			Type:      "pub.polis.follow.removed",
			Actor:     "alice.com",
			Timestamp: "2026-02-01T00:00:00Z",
			Payload:   map[string]interface{}{"target_domain": "bob.com"},
		},
		{
			ID:        json.Number("2"),
			Type:      "pub.polis.follow.announced",
			Actor:     "alice.com",
			Timestamp: "2026-03-01T00:00:00Z",
			Payload:   map[string]interface{}{"target_domain": "bob.com"},
		},
	}

	result, err := h.Process(events, state)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	fs := result.(*FollowerState)
	if fs.FollowedAt["alice.com"] != "2026-03-01T00:00:00Z" {
		t.Errorf("after unfollow+refollow, timestamp = %q, want re-follow time %q",
			fs.FollowedAt["alice.com"], "2026-03-01T00:00:00Z")
	}
}

func TestFollowHandler_LegacyStateNoTimestamps(t *testing.T) {
	// Legacy on-disk state predates timestamps: Followers is populated
	// but FollowedAt is nil. Process must accept that and not panic.
	// A subsequent announce records its timestamp without backfilling
	// the legacy entries (we don't know when they followed).
	h := &FollowHandler{MyDomain: "bob.com"}
	state := &FollowerState{
		Followers: []string{"alice.com"},
		Count:     1,
	}

	events := []discovery.StreamEvent{
		{
			ID:        json.Number("1"),
			Type:      "pub.polis.follow.announced",
			Actor:     "charlie.com",
			Timestamp: "2026-04-15T18:30:00Z",
			Payload:   map[string]interface{}{"target_domain": "bob.com"},
		},
	}

	result, err := h.Process(events, state)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	fs := result.(*FollowerState)
	if _, hasAlice := fs.FollowedAt["alice.com"]; hasAlice {
		t.Errorf("legacy alice should have no timestamp; got %q", fs.FollowedAt["alice.com"])
	}
	if fs.FollowedAt["charlie.com"] != "2026-04-15T18:30:00Z" {
		t.Errorf("charlie new entry should record timestamp")
	}
}
