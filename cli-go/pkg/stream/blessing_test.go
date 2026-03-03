package stream

import (
	"encoding/json"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
)

func TestBlessingHandler_EventTypes(t *testing.T) {
	h := &BlessingHandler{MyDomain: "bob.com"}
	types := h.EventTypes()

	if len(types) != 3 {
		t.Fatalf("EventTypes() len = %d, want 3", len(types))
	}

	expected := map[string]bool{
		"pub.polis.comment.blessing.requested": true,
		"pub.polis.comment.blessing.granted":   true,
		"pub.polis.comment.blessing.denied":    true,
	}
	for _, typ := range types {
		if !expected[typ] {
			t.Errorf("unexpected event type: %q", typ)
		}
	}
}

func TestBlessingHandler_ProcessRequested_WithTargetDomain(t *testing.T) {
	h := &BlessingHandler{MyDomain: "bob.com"}
	state := h.NewState()

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.comment.blessing.requested",
			Actor: "alice.com",
			Payload: map[string]interface{}{
				"source_url":    "https://alice.com/comments/1.md",
				"target_url":    "https://bob.com/posts/1.md",
				"target_domain": "bob.com",
			},
			Timestamp: "2026-02-10T10:00:00Z",
		},
	}

	result, err := h.Process(events, state)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	bs := result.(*BlessingState)
	if len(bs.Blessings) != 1 {
		t.Fatalf("expected 1 blessing, got %d", len(bs.Blessings))
	}
	if bs.Blessings[0].Status != "pending" {
		t.Errorf("status = %q, want %q", bs.Blessings[0].Status, "pending")
	}
	if bs.Blessings[0].SourceURL != "https://alice.com/comments/1.md" {
		t.Errorf("source_url = %q, want %q", bs.Blessings[0].SourceURL, "https://alice.com/comments/1.md")
	}
}

func TestBlessingHandler_ProcessRequested_FallbackToTargetURL(t *testing.T) {
	h := &BlessingHandler{MyDomain: "bob.com"}
	state := h.NewState()

	// No target_domain — should fall back to extracting from target_url
	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.comment.blessing.requested",
			Actor: "alice.com",
			Payload: map[string]interface{}{
				"source_url": "https://alice.com/comments/1.md",
				"target_url": "https://bob.com/posts/1.md",
			},
			Timestamp: "2026-02-10T10:00:00Z",
		},
	}

	result, err := h.Process(events, state)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	bs := result.(*BlessingState)
	if len(bs.Blessings) != 1 {
		t.Fatalf("expected 1 blessing (via fallback), got %d", len(bs.Blessings))
	}
	if bs.Blessings[0].Status != "pending" {
		t.Errorf("status = %q, want %q", bs.Blessings[0].Status, "pending")
	}
}

func TestBlessingHandler_ProcessGranted(t *testing.T) {
	h := &BlessingHandler{MyDomain: "bob.com"}
	state := h.NewState()

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.comment.blessing.granted",
			Actor: "bob.com",
			Payload: map[string]interface{}{
				"source_url":    "https://alice.com/comments/1.md",
				"target_url":    "https://bob.com/posts/1.md",
				"target_domain": "bob.com",
			},
			Timestamp: "2026-02-10T10:00:00Z",
		},
	}

	result, err := h.Process(events, state)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	bs := result.(*BlessingState)
	if len(bs.Blessings) != 1 {
		t.Fatalf("expected 1 blessing, got %d", len(bs.Blessings))
	}
	if bs.Blessings[0].Status != "granted" {
		t.Errorf("status = %q, want %q", bs.Blessings[0].Status, "granted")
	}
	if bs.Granted != 1 {
		t.Errorf("granted = %d, want 1", bs.Granted)
	}
}

func TestBlessingHandler_ProcessDenied(t *testing.T) {
	h := &BlessingHandler{MyDomain: "bob.com"}
	state := h.NewState()

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.comment.blessing.denied",
			Actor: "bob.com",
			Payload: map[string]interface{}{
				"source_url":    "https://alice.com/comments/1.md",
				"target_url":    "https://bob.com/posts/1.md",
				"target_domain": "bob.com",
			},
			Timestamp: "2026-02-10T10:00:00Z",
		},
	}

	result, err := h.Process(events, state)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	bs := result.(*BlessingState)
	if len(bs.Blessings) != 1 {
		t.Fatalf("expected 1 blessing, got %d", len(bs.Blessings))
	}
	if bs.Blessings[0].Status != "denied" {
		t.Errorf("status = %q, want %q", bs.Blessings[0].Status, "denied")
	}
	if bs.Denied != 1 {
		t.Errorf("denied = %d, want 1", bs.Denied)
	}
}

func TestBlessingHandler_IgnoresOtherDomains(t *testing.T) {
	h := &BlessingHandler{MyDomain: "bob.com"}
	state := h.NewState()

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.comment.blessing.requested",
			Actor: "alice.com",
			Payload: map[string]interface{}{
				"source_url":    "https://alice.com/comments/1.md",
				"target_url":    "https://charlie.com/posts/1.md",
				"target_domain": "charlie.com",
			},
			Timestamp: "2026-02-10T10:00:00Z",
		},
	}

	result, err := h.Process(events, state)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	bs := result.(*BlessingState)
	if len(bs.Blessings) != 0 {
		t.Errorf("expected 0 blessings for other domain, got %d", len(bs.Blessings))
	}
}

func TestBlessingHandler_FullLifecycle(t *testing.T) {
	h := &BlessingHandler{MyDomain: "bob.com"}
	state := h.NewState()

	// Step 1: Request
	events1 := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.comment.blessing.requested",
			Actor: "alice.com",
			Payload: map[string]interface{}{
				"source_url":    "https://alice.com/comments/1.md",
				"target_url":    "https://bob.com/posts/1.md",
				"target_domain": "bob.com",
			},
			Timestamp: "2026-02-10T10:00:00Z",
		},
	}

	result1, err := h.Process(events1, state)
	if err != nil {
		t.Fatalf("Process batch 1: %v", err)
	}

	bs1 := result1.(*BlessingState)
	if len(bs1.Blessings) != 1 || bs1.Blessings[0].Status != "pending" {
		t.Fatalf("after request: expected 1 pending blessing, got %d with status %q",
			len(bs1.Blessings), bs1.Blessings[0].Status)
	}

	// Step 2: Grant
	events2 := []discovery.StreamEvent{
		{
			ID:    json.Number("2"),
			Type:  "pub.polis.comment.blessing.granted",
			Actor: "bob.com",
			Payload: map[string]interface{}{
				"source_url":    "https://alice.com/comments/1.md",
				"target_url":    "https://bob.com/posts/1.md",
				"target_domain": "bob.com",
			},
			Timestamp: "2026-02-10T10:01:00Z",
		},
	}

	result2, err := h.Process(events2, bs1)
	if err != nil {
		t.Fatalf("Process batch 2: %v", err)
	}

	bs2 := result2.(*BlessingState)
	if len(bs2.Blessings) != 1 {
		t.Fatalf("after grant: expected 1 blessing, got %d", len(bs2.Blessings))
	}
	if bs2.Blessings[0].Status != "granted" {
		t.Errorf("after grant: status = %q, want %q", bs2.Blessings[0].Status, "granted")
	}
	if bs2.Granted != 1 {
		t.Errorf("after grant: granted = %d, want 1", bs2.Granted)
	}
}
