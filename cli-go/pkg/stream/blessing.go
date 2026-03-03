package stream

import (
	"fmt"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
)

// BlessingHandler is a built-in projection handler for blessing events.
// It maintains a map of granted/denied blessings keyed by source_url (comment URL).
type BlessingHandler struct {
	// MyDomain is the domain to filter events for. Only events where
	// the target_url belongs to MyDomain are processed.
	MyDomain string
}

// BlessingEntry represents a single blessing decision.
type BlessingEntry struct {
	SourceURL string `json:"source_url"`
	TargetURL string `json:"target_url"`
	Status    string `json:"status"` // "granted" or "denied"
	Actor     string `json:"actor"`
	UpdatedAt string `json:"updated_at"`
}

// BlessingState is the materialized state for the blessing projection.
type BlessingState struct {
	Blessings []BlessingEntry `json:"blessings"`
	Granted   int             `json:"granted"`
	Denied    int             `json:"denied"`
}

func (h *BlessingHandler) TypePrefix() string { return "pub.polis.comment.blessing" }

func (h *BlessingHandler) EventTypes() []string {
	return []string{"pub.polis.comment.blessing.requested", "pub.polis.comment.blessing.granted", "pub.polis.comment.blessing.denied"}
}

func (h *BlessingHandler) NewState() interface{} {
	return &BlessingState{}
}

func (h *BlessingHandler) Process(events []discovery.StreamEvent, state interface{}) (interface{}, error) {
	bs, ok := state.(*BlessingState)
	if !ok {
		return nil, fmt.Errorf("blessing handler: unexpected state type %T", state)
	}

	// Build a map from source_url -> BlessingEntry for O(1) lookup
	blessingMap := make(map[string]*BlessingEntry, len(bs.Blessings))
	for i := range bs.Blessings {
		blessingMap[bs.Blessings[i].SourceURL] = &bs.Blessings[i]
	}

	for _, evt := range events {
		sourceURL, _ := evt.Payload["source_url"].(string)
		targetURL, _ := evt.Payload["target_url"].(string)
		if sourceURL == "" || targetURL == "" {
			continue
		}

		// Only process events for our domain (we're the target/post owner)
		targetDomain, _ := evt.Payload["target_domain"].(string)
		if targetDomain == "" {
			targetDomain = discovery.ExtractDomainFromURL(targetURL)
		}
		if targetDomain != h.MyDomain {
			continue
		}

		switch evt.Type {
		case "pub.polis.comment.blessing.requested":
			if _, exists := blessingMap[sourceURL]; !exists {
				entry := &BlessingEntry{
					SourceURL: sourceURL,
					TargetURL: targetURL,
					Status:    "pending",
					Actor:     evt.Actor,
					UpdatedAt: evt.Timestamp,
				}
				blessingMap[sourceURL] = entry
			}

		case "pub.polis.comment.blessing.granted":
			if entry, exists := blessingMap[sourceURL]; exists {
				entry.Status = "granted"
				entry.UpdatedAt = evt.Timestamp
			} else {
				blessingMap[sourceURL] = &BlessingEntry{
					SourceURL: sourceURL,
					TargetURL: targetURL,
					Status:    "granted",
					Actor:     evt.Actor,
					UpdatedAt: evt.Timestamp,
				}
			}

		case "pub.polis.comment.blessing.denied":
			if entry, exists := blessingMap[sourceURL]; exists {
				entry.Status = "denied"
				entry.UpdatedAt = evt.Timestamp
			} else {
				blessingMap[sourceURL] = &BlessingEntry{
					SourceURL: sourceURL,
					TargetURL: targetURL,
					Status:    "denied",
					Actor:     evt.Actor,
					UpdatedAt: evt.Timestamp,
				}
			}
		}
	}

	// Rebuild slice and counts
	blessings := make([]BlessingEntry, 0, len(blessingMap))
	granted, denied := 0, 0
	for _, entry := range blessingMap {
		blessings = append(blessings, *entry)
		switch entry.Status {
		case "granted":
			granted++
		case "denied":
			denied++
		}
	}

	return &BlessingState{
		Blessings: blessings,
		Granted:   granted,
		Denied:    denied,
	}, nil
}
