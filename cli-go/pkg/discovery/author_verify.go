package discovery

import (
	"fmt"
	"sync"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// AuthorKeyCache caches author public keys fetched from .well-known/polis.
// Thread-safe. Supports negative caching for unreachable domains.
type AuthorKeyCache struct {
	mu      sync.Mutex
	entries map[string]*authorKeyEntry
	ttl     time.Duration
	negTTL  time.Duration // negative cache TTL for unreachable domains
	client  *remote.Client
}

type authorKeyEntry struct {
	publicKey string
	err       error // non-nil for negative cache entries
	fetchedAt time.Time
}

// NewAuthorKeyCache creates a new author key cache.
func NewAuthorKeyCache(ttl, negTTL time.Duration) *AuthorKeyCache {
	if ttl == 0 {
		ttl = time.Hour
	}
	if negTTL == 0 {
		negTTL = 5 * time.Minute
	}
	return &AuthorKeyCache{
		entries: make(map[string]*authorKeyEntry),
		ttl:     ttl,
		negTTL:  negTTL,
		client:  remote.NewClient(),
	}
}

// GetPublicKey returns the cached public key for a domain, fetching if needed.
func (c *AuthorKeyCache) GetPublicKey(domain string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, ok := c.entries[domain]; ok {
		ttl := c.ttl
		if entry.err != nil {
			ttl = c.negTTL
		}
		if time.Since(entry.fetchedAt) < ttl {
			return entry.publicKey, entry.err
		}
	}

	baseURL := "https://" + domain
	key, err := c.client.FetchPublicKey(baseURL)
	c.entries[domain] = &authorKeyEntry{
		publicKey: key,
		err:       err,
		fetchedAt: time.Now(),
	}
	return key, err
}

// VerifyAuthorSignature verifies a stream event's author signature.
// Returns nil if valid, an error describing the failure otherwise.
func VerifyAuthorSignature(cache *AuthorKeyCache, event StreamEvent) error {
	if event.Signature == "" {
		return fmt.Errorf("missing author signature")
	}

	if event.Actor == "" {
		return fmt.Errorf("missing actor domain")
	}

	// Reconstruct the canonical JSON the author signed
	canonical, err := MakeStreamCanonicalJSON(event.Type, event.Payload)
	if err != nil {
		return fmt.Errorf("failed to build canonical JSON: %w", err)
	}

	pubKey, err := cache.GetPublicKey(event.Actor)
	if err != nil {
		return fmt.Errorf("failed to fetch public key for %s: %w", event.Actor, err)
	}

	valid, err := signing.VerifySignature(canonical, []byte(pubKey), event.Signature)
	if err != nil {
		return fmt.Errorf("signature verification error for %s: %w", event.Actor, err)
	}
	if !valid {
		return fmt.Errorf("invalid author signature from %s on %s event", event.Actor, event.Type)
	}

	return nil
}

// FilterVerifiedEvents filters stream events, keeping only those with valid
// author signatures. Events that fail verification are logged via the provided
// callback. If requireSignatures is false, events without signatures are
// passed through with a warning (for legacy event support).
func FilterVerifiedEvents(
	cache *AuthorKeyCache,
	events []StreamEvent,
	requireSignatures bool,
	onWarning func(event StreamEvent, msg string),
) []StreamEvent {
	if cache == nil {
		return events
	}

	verified := make([]StreamEvent, 0, len(events))
	for _, evt := range events {
		if evt.Signature == "" {
			if requireSignatures {
				if onWarning != nil {
					onWarning(evt, fmt.Sprintf("missing author signature on %s from %s — skipping", evt.Type, evt.Actor))
				}
				continue
			}
			// Legacy event without signature — pass through with warning
			if onWarning != nil {
				onWarning(evt, fmt.Sprintf("missing author signature on %s from %s — allowing (legacy)", evt.Type, evt.Actor))
			}
			verified = append(verified, evt)
			continue
		}

		if err := VerifyAuthorSignature(cache, evt); err != nil {
			if onWarning != nil {
				onWarning(evt, fmt.Sprintf("author signature verification failed: %v", err))
			}
			continue
		}

		verified = append(verified, evt)
	}
	return verified
}
