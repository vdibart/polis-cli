package stream

import (
	"fmt"
	"log"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
)

// BlessingHandler is a built-in projection handler for blessing events.
// It maintains a map of granted/denied blessings keyed by source_url (comment URL).
type BlessingHandler struct {
	// MyDomain is the domain to filter events for. Only events where
	// the target_url belongs to MyDomain are processed.
	MyDomain string

	// DSKeyCache is used to verify DS attestation signatures on autoblessed
	// comments. If nil, attestation verification is skipped.
	DSKeyCache *discovery.DSKeyCache
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
		// Auto-blessed events use comment_url/in_reply_to; manual grants use source_url/target_url
		sourceURL, _ := evt.Payload["source_url"].(string)
		if sourceURL == "" {
			sourceURL, _ = evt.Payload["comment_url"].(string)
		}
		targetURL, _ := evt.Payload["target_url"].(string)
		if targetURL == "" {
			targetURL, _ = evt.Payload["in_reply_to"].(string)
		}
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
			// Verify DS attestation on autoblessed events (warn-only)
			if autoBlessed, _ := evt.Payload["auto_blessed"].(bool); autoBlessed && h.DSKeyCache != nil {
				dsAttestation, _ := evt.Payload["ds_attestation"].(string)
				dsKeyID, _ := evt.Payload["ds_key_id"].(string)
				policyRule, _ := evt.Payload["policy_rule"].(string)
				policySource, _ := evt.Payload["policy_source"].(string)
				commentURL, _ := evt.Payload["comment_url"].(string)
				inReplyTo, _ := evt.Payload["in_reply_to"].(string)
				if err := discovery.VerifyAutoblessAttestation(
					h.DSKeyCache, commentURL, inReplyTo, policyRule, policySource, dsKeyID, dsAttestation,
				); err != nil {
					log.Printf("[!] Autobless attestation verification failed for %s: %v", sourceURL, err)
				}
			}

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
