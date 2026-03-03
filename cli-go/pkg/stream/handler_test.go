package stream

import (
	"encoding/json"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
)

func TestFilterEvents_MatchingTypes(t *testing.T) {
	events := []discovery.StreamEvent{
		{ID: json.Number("1"), Type: "pub.polis.follow.announced"},
		{ID: json.Number("2"), Type: "pub.polis.post.published"},
		{ID: json.Number("3"), Type: "pub.polis.follow.removed"},
		{ID: json.Number("4"), Type: "pub.polis.comment.blessing.granted"},
	}

	filtered := FilterEvents(events, []string{"pub.polis.follow.announced", "pub.polis.follow.removed"})
	if len(filtered) != 2 {
		t.Fatalf("expected 2 events, got %d", len(filtered))
	}
	if filtered[0].Type != "pub.polis.follow.announced" {
		t.Errorf("filtered[0].Type = %q, want %q", filtered[0].Type, "pub.polis.follow.announced")
	}
	if filtered[1].Type != "pub.polis.follow.removed" {
		t.Errorf("filtered[1].Type = %q, want %q", filtered[1].Type, "pub.polis.follow.removed")
	}
}

func TestFilterEvents_Wildcard(t *testing.T) {
	events := []discovery.StreamEvent{
		{ID: json.Number("1"), Type: "pub.polis.follow.announced"},
		{ID: json.Number("2"), Type: "pub.polis.post.published"},
	}

	filtered := FilterEvents(events, []string{"*"})
	if len(filtered) != 2 {
		t.Fatalf("expected 2 events with wildcard, got %d", len(filtered))
	}
}

func TestFilterEvents_EmptyTypes(t *testing.T) {
	events := []discovery.StreamEvent{
		{ID: json.Number("1"), Type: "pub.polis.follow.announced"},
	}

	filtered := FilterEvents(events, nil)
	if filtered != nil {
		t.Errorf("expected nil for empty types, got %v", filtered)
	}

	filtered = FilterEvents(events, []string{})
	if filtered != nil {
		t.Errorf("expected nil for empty slice, got %v", filtered)
	}
}

func TestFilterEvents_NoMatches(t *testing.T) {
	events := []discovery.StreamEvent{
		{ID: json.Number("1"), Type: "pub.polis.follow.announced"},
		{ID: json.Number("2"), Type: "pub.polis.post.published"},
	}

	filtered := FilterEvents(events, []string{"pub.polis.comment.blessing.granted"})
	if len(filtered) != 0 {
		t.Errorf("expected 0 events, got %d", len(filtered))
	}
}

func TestFilterEvents_EmptyEvents(t *testing.T) {
	filtered := FilterEvents(nil, []string{"pub.polis.follow.announced"})
	if len(filtered) != 0 {
		t.Errorf("expected 0 events for nil input, got %d", len(filtered))
	}
}
