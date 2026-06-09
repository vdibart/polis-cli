// =============================================================================
// HANDBOOK TRAIL MARKER — DS-to-stream thread (continuous sync, Path A)
// =============================================================================
// This file is the *producer* of the continuous sync path. runUnifiedSync()
// pulls a cursor-paginated batch of events from the DS, fans them out to
// per-content-type handlers (feed / follow / blessing / notification), and
// advances the cursor. Runs every ~30 seconds while any browser tab has an
// SSE connection open.
//
// Trail across files (DS-to-stream, continuous sync):
//   producer (this file)  — webapp/internal/server/sync.go: runUnifiedSync()
//   DS endpoint           — discovery-service/core/handlers/stream.ts (queryStream)
//   event-to-FeedItem     — cli-go/pkg/feed/handler.go (FeedHandler.Process)
//   cursor + cache        — cli-go/pkg/stream/store.go (Store)
//   webapp HTTP surface   — webapp/internal/server/handlers_stream.go
//
// Aggregation path (Path B, complementary): handlers_stream.go batches URLs
// to discovery-service/core/handlers/counts.ts for cross-tenant decoration.
//
// Pull the thread:
//   github.com/vdibart/polis-cli/blob/main/docs/handbook/ds-to-stream.md      (tour)
//   github.com/vdibart/polis-cli/blob/main/docs/ds/developer/stream-architecture.md
//   github.com/vdibart/polis-cli/blob/main/docs/ds/developer/api-reference.md
//   github.com/vdibart/polis-cli/blob/main/AGENTS.md                          (map)
// =============================================================================

package server

import (
	"crypto/rand"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/blessing"
	"github.com/vdibart/polis-cli/cli-go/pkg/cache"
	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/feed"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
)

// SyncResult aggregates results from all handlers in a unified sync run.
type SyncResult struct {
	NewNotifications int
	NewFeedItems     int
	FollowersChanged bool
	CommentsChanged  bool
	FilesChanged     bool // any handler reported file changes -> triggers RenderSite
}

// runUnifiedSync performs a single unified sync cycle: queries the DS stream
// with a single cursor, fans out events to all registered handlers, then
// advances the cursor and renders if needed.
// generateBackgroundRequestID creates a prefixed UUIDv4 for background DS calls
// that aren't part of the unified sync cycle (e.g., comment-sync, feed-sync).
func generateBackgroundRequestID(prefix string) string {
	return prefix + "-" + generateSyncID()
}

// generateSyncID creates a UUIDv4 for sync cycle correlation.
func generateSyncID() string {
	var uuid [16]byte
	_, _ = rand.Read(uuid[:])
	uuid[6] = (uuid[6] & 0x0f) | 0x40
	uuid[8] = (uuid[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
}

func (s *Server) runUnifiedSync() SyncResult {
	result := SyncResult{}

	if s.DiscoveryURL == "" {
		return result
	}
	baseURL := s.GetBaseURL()
	if baseURL == "" || s.PrivateKey == nil {
		return result
	}

	myDomain := extractDomainFromURL(baseURL)
	if myDomain == "" {
		return result
	}

	syncID := generateSyncID()
	syncStart := time.Now()

	discoveryDomain := s.GetDiscoveryDomain()
	store := stream.NewStore(s.DataDir, discoveryDomain, "pub.polis.core")

	// Get unified cursor, migrating from old per-handler cursors on first run
	cursor := s.getUnifiedCursor(store)

	s.LogEvent("pub.polis.sync.start", map[string]interface{}{
		"sync_id":       syncID,
		"cursor":        cursor,
		"handler_count": len(s.syncHandlers),
	})

	// Collect events from targeted queries (2-3 DS calls with a shared cursor)
	allEvents, newCursor := s.queryStreamEvents(myDomain, cursor, syncID)
	if len(allEvents) == 0 {
		// Still update cursor timestamp even if no new events
		if newCursor != "" && cursorGreater(newCursor, cursor) {
			_ = store.SetCursor("pub.polis.sync", newCursor)
		}
		// Broadcast counts even with 0 events — feed items may have been
		// added by other paths (e.g. manual feed refresh) since the last
		// broadcast, and SSE clients need the updated counts.
		s.broadcastCounts(result)
		s.LogEvent("pub.polis.sync.complete", map[string]interface{}{
			"sync_id":          syncID,
			"events_processed": 0,
			"duration_ms":      time.Since(syncStart).Milliseconds(),
		})
		return result
	}

	s.LogDebug("unified sync: processing %d events from cursor %s", len(allEvents), cursor)

	// Fan out to registered handlers
	for _, h := range s.syncHandlers {
		filtered := stream.FilterEvents(allEvents, h.EventTypes())
		if len(filtered) == 0 {
			continue
		}

		hr := h.Process(filtered)
		if hr.Error != nil {
			s.LogWarn("unified sync: %s handler error: %v", h.Name(), hr.Error)
		}
		if hr.FilesChanged {
			result.FilesChanged = true
		}

		switch h.Name() {
		case "notifications":
			result.NewNotifications = hr.NewItems
		case "feed":
			result.NewFeedItems = hr.NewItems
		case "followers":
			result.FollowersChanged = hr.FilesChanged
		case "comment-status":
			result.CommentsChanged = hr.FilesChanged
		}
	}

	// Advance unified cursor
	if newCursor != "" {
		_ = store.SetCursor("pub.polis.sync", newCursor)
	}

	// Single RenderSite if any files changed
	if result.FilesChanged {
		if err := s.RenderSite(); err != nil {
			log.Printf("[warning] unified sync render failed: %v", err)
		}
	}

	// Broadcast counts via SSE
	s.broadcastCounts(result)

	// Log sync completion
	s.LogEvent("pub.polis.sync.complete", map[string]interface{}{
		"sync_id":           syncID,
		"cursor_before":     cursor,
		"cursor_after":      newCursor,
		"events_processed":  len(allEvents),
		"new_notifications": result.NewNotifications,
		"new_feed_items":    result.NewFeedItems,
		"followers_changed": result.FollowersChanged,
		"files_changed":     result.FilesChanged,
		"duration_ms":       time.Since(syncStart).Milliseconds(),
	})

	return result
}

// getUnifiedCursor returns the pub.polis.sync cursor position.
// Returns "0" on first run to replay the full stream.
func (s *Server) getUnifiedCursor(store *stream.Store) string {
	cursor, _ := store.GetCursor("pub.polis.sync")
	if cursor != "" && cursor != "0" {
		return cursor
	}
	return "0"
}

// queryStreamEvents uses the unified DS endpoint to fetch all relevant events
// in a single HTTP request and single DB query. Combines:
// - Events involving our domain (target OR source) via involvedDomain
// - Events from followed authors via actors[]
// This replaces the previous 3-query approach (target + source + followed).
func (s *Server) queryStreamEvents(myDomain, cursor, syncID string) ([]discovery.StreamEvent, string) {
	// Check suspension before making any DS queries
	discoveryDomain := s.GetDiscoveryDomain()
	vStore := stream.NewStore(s.DataDir, discoveryDomain, "pub.polis.core")
	vCfg := stream.LoadVerificationConfig(vStore)
	var vState stream.VerificationState
	_ = vState.Load(vStore)

	cooldown := time.Duration(vCfg.SuspendCooldownMin) * time.Minute
	if vState.ShouldSuspendSync(vCfg.MaxConsecutiveDSFails, cooldown) {
		s.LogWarn("unified sync paused — %d consecutive DS query failures (next retry after %v cooldown). Check DS connectivity; if this is a self-hosted tenant that is not publicly reachable, registration is not required for read sync but may be needed for publishing.", vState.DSFailures.Consecutive, cooldown)
		return nil, cursor
	}

	// Read-only stream query: NewReadDSClient sends auth headers only
	// if this tenant is registered with the DS. Unregistered local
	// tenants (e.g. developing on localhost) sync anonymously — the
	// DS allows anonymous reads on /v1/stream/unified, and sending
	// auth headers from a non-publicly-reachable origin would force
	// the DS into a live well-known fetch that necessarily fails.
	client := s.NewReadDSClient(nil)
	client.RequestID = syncID

	// Build followed authors list
	var followedDomains []string
	followingPath := following.DefaultPath(s.DataDir)
	f, err := following.Load(followingPath)
	if err == nil && f.Count() > 0 {
		for _, entry := range f.All() {
			d := discovery.ExtractDomainFromURL(entry.URL)
			if d != "" {
				followedDomains = append(followedDomains, d)
			}
		}
	}

	// Single unified query: involved domain OR followed actors
	// Limit 3000 to match combined capacity of the previous 3×1000 model
	result, err := client.StreamQueryUnified(cursor, 3000, myDomain, followedDomains)

	var allEvents []discovery.StreamEvent
	newCursor := cursor

	if err != nil {
		vState.RecordDSFailure()
		s.LogWarn("unified sync: DS query failed (will retry; %d consecutive failures so far). Enable LOG_LEVEL=2 for the underlying error.", vState.DSFailures.Consecutive)
		s.LogDebug("unified sync: unified query failed: %v", err)
	} else {
		vState.RecordDSSuccess()
		allEvents = result.Events
		if cursorGreater(result.Cursor, newCursor) {
			newCursor = result.Cursor
		}
	}

	// Verify author signatures on all events before processing
	allEvents = discovery.FilterVerifiedEvents(
		s.AuthorKeyCache,
		allEvents,
		vCfg.RequireAuthorSignatures,
		func(evt discovery.StreamEvent, msg string) {
			s.LogWarn("unified sync: [!] %s (event %v)", msg, evt.ID)
			vState.RecordAuthorFailure(evt.Actor, evt.Type)
		},
	)

	// Check for repeated failures and warn
	recent := vState.RecentAuthorFailures(24 * time.Hour)
	for domain, stat := range recent {
		if stat.Total >= 5 {
			s.LogWarn("unified sync: [!] Repeated verification failures from %s (%d in 24h) — consider blocking", domain, stat.Total)
		}
	}

	_ = vState.Save(vStore)

	return allEvents, newCursor
}

// --- Feed Sync Handler ---

type feedSyncHandler struct {
	server *Server
}

func (h *feedSyncHandler) Name() string { return "feed" }

func (h *feedSyncHandler) EventTypes() []string {
	return []string{
		"pub.polis.post.published",
		"pub.polis.post.republished",
		"pub.polis.comment.published",
		"pub.polis.comment.republished",
		"pub.polis.comment.blessing.granted",
		"pub.polis.comment.blessing.requested",
		"pub.polis.follow.announced",
		"pub.polis.site.registered",
	}
}

func (h *feedSyncHandler) Process(events []discovery.StreamEvent) stream.HandlerResult {
	s := h.server
	baseURL := s.GetBaseURL()
	if baseURL == "" {
		return stream.HandlerResult{}
	}

	myDomain := extractDomainFromURL(baseURL)
	if myDomain == "" {
		return stream.HandlerResult{}
	}

	// Load followed domains for feed filtering
	followingPath := following.DefaultPath(s.DataDir)
	f, err := following.Load(followingPath)
	if err != nil || f.Count() == 0 {
		return stream.HandlerResult{}
	}

	followedDomains := make(map[string]bool)
	for _, entry := range f.All() {
		d := discovery.ExtractDomainFromURL(entry.URL)
		if d != "" {
			followedDomains[d] = true
		}
	}

	// Feed is a local projection for display — skip policy evaluation here.
	// Policy enforcement happens at content acceptance (blessings, follows).
	// This matches syncFeed() in server.go which also skips policies.
	handler := &feed.FeedHandler{
		MyDomain:        myDomain,
		FollowedDomains: followedDomains,
	}

	items := handler.Process(events)
	if len(items) == 0 {
		return stream.HandlerResult{}
	}

	cm := s.feedCacheForScope("network")
	newCount, err := cm.MergeItems(items)
	if err != nil {
		return stream.HandlerResult{Error: err}
	}

	// step-06/6.l: kick off excerpt fetch immediately on sync, NOT
	// later on dashboard load. Without this, items without excerpts
	// wait until handleFeedGrouped's tail-trigger fires when the user
	// opens the dashboard — yielding a flicker where titles render
	// without preview text on first paint, then populate on a refresh.
	// Sync-time is the right moment: the items are fresh, the user
	// hasn't seen them yet, and the network is being exercised anyway.
	// kickOffExcerptFetch is idempotent (skips items that already have
	// an excerpt), so dashboard-load fallback still works for items
	// that didn't come through this sync handler (e.g., older items
	// whose excerpts failed earlier).
	if newCount > 0 {
		fullItems, listErr := cm.List()
		if listErr == nil {
			s.kickOffExcerptFetch(cm, fullItems)
		} else {
			s.LogDebug("excerpt fetch on sync skipped (list failed): %v", listErr)
		}
	}

	return stream.HandlerResult{NewItems: newCount}
}

// --- Follow Sync Handler ---

type followSyncHandler struct {
	server *Server
}

func (h *followSyncHandler) Name() string { return "followers" }

func (h *followSyncHandler) EventTypes() []string {
	return []string{"pub.polis.follow.announced", "pub.polis.follow.removed"}
}

func (h *followSyncHandler) Process(events []discovery.StreamEvent) stream.HandlerResult {
	s := h.server
	baseURL := s.GetBaseURL()
	if baseURL == "" {
		return stream.HandlerResult{}
	}

	myDomain := extractDomainFromURL(baseURL)
	if myDomain == "" {
		return stream.HandlerResult{}
	}

	discoveryDomain := s.GetDiscoveryDomain()
	store := stream.NewStore(s.DataDir, discoveryDomain, "pub.polis.core")

	handler := &stream.FollowHandler{MyDomain: myDomain}

	state := handler.NewState()
	_ = store.LoadState(handler.TypePrefix(), state)

	newState, err := handler.Process(events, state)
	if err != nil {
		return stream.HandlerResult{Error: err}
	}

	_ = store.SaveState(handler.TypePrefix(), newState)

	// Check if follower count changed
	oldFs := state.(*stream.FollowerState)
	newFs := newState.(*stream.FollowerState)
	changed := oldFs.Count != newFs.Count

	return stream.HandlerResult{FilesChanged: changed}
}

// --- Blessing Sync Handler ---

type blessingSyncHandler struct {
	server *Server
}

func (h *blessingSyncHandler) Name() string { return "blessings" }

func (h *blessingSyncHandler) EventTypes() []string {
	// BlessingHandler covers requested/granted/denied; add comment.unpublished so
	// a comment author's withdrawal evicts our cached mirror (WS3·d). The DS now
	// stamps comment.unpublished with target_domain = the post author (derived from
	// in_reply_to) so the unified stream filter routes it to us — WS6 routing fix,
	// canary commentUnpublishTargetDomain.
	return append((&stream.BlessingHandler{}).EventTypes(), "pub.polis.comment.unpublished")
}

func (h *blessingSyncHandler) Process(events []discovery.StreamEvent) stream.HandlerResult {
	s := h.server
	baseURL := s.GetBaseURL()
	if baseURL == "" {
		return stream.HandlerResult{}
	}

	myDomain := extractDomainFromURL(baseURL)
	if myDomain == "" {
		return stream.HandlerResult{}
	}

	discoveryDomain := s.GetDiscoveryDomain()
	store := stream.NewStore(s.DataDir, discoveryDomain, "pub.polis.core")

	handler := &stream.BlessingHandler{MyDomain: myDomain}

	state := handler.NewState()
	_ = store.LoadState(handler.TypePrefix(), state)

	newState, err := handler.Process(events, state)
	if err != nil {
		return stream.HandlerResult{Error: err}
	}

	_ = store.SaveState(handler.TypePrefix(), newState)

	// Policy-driven auto-decisions for blessing requests
	privPath, pubPath := policy.DefaultPaths(s.DataDir)
	policies, _ := policy.LoadPolicies(privPath, pubPath)

	if len(policies) > 0 && s.PrivateKey != nil {
		// Build eval context
		followedDomains := make(map[string]bool)
		followingPath := following.DefaultPath(s.DataDir)
		if fl, err := following.Load(followingPath); err == nil {
			for _, entry := range fl.All() {
				if d := discovery.ExtractDomainFromURL(entry.URL); d != "" {
					followedDomains[d] = true
				}
			}
		}
		var followerState stream.FollowerState
		_ = store.LoadState("pub.polis.follow", &followerState)
		followerDomains := make(map[string]bool, len(followerState.Followers))
		for _, f := range followerState.Followers {
			followerDomains[f] = true
		}
		ctx := policy.EvalContext{
			MyDomain:         myDomain,
			FollowingDomains: followedDomains,
			FollowerDomains:  followerDomains,
		}

		// Read-only: fetches blessing-requested event details. See
		// NewReadDSClient comment in server.go for why this is gated
		// on local DS registration.
		client := s.NewReadDSClient(nil)
		client.RequestID = generateSyncID()
		hc := s.getHookConfig()

		for _, evt := range events {
			if evt.Type != "pub.polis.comment.blessing.requested" {
				continue
			}
			targetDomain, _ := evt.Payload["target_domain"].(string)
			if targetDomain != myDomain {
				continue
			}

			commentURL := firstNonEmptyString(evt.Payload, "comment_url", "source_url")
			inReplyTo := firstNonEmptyString(evt.Payload, "in_reply_to", "target_url")
			if commentURL == "" || inReplyTo == "" {
				continue
			}

			pEvt := policy.Event{
				Type:         evt.Type,
				ActorDomain:  evt.Actor,
				TargetDomain: targetDomain,
				TargetPath:   inReplyTo,
			}

			evalResult := policy.EvaluateWithLog(policies, pEvt, ctx)
			if !evalResult.Matched {
				continue // no match -> manual review
			}

			// Unified decision event — fires for every matched blessing
			// evaluation so Axiom can aggregate decision distributions
			// (how often policies bless / review / deny). Action-specific
			// events (auto_grant / auto_deny / review_queued) fire below
			// inside the switch for the specific outcome taken.
			s.LogEvent("pub.polis.policy.decision", map[string]interface{}{
				"decision":     string(evalResult.Decision),
				"content_type": pEvt.Type,
				"actor_domain": evt.Actor,
				"rule_matched": evalResult.Rule,
				"layer":        "tenant-inbound",
			})

			switch evalResult.Decision {
			case policy.Bless, policy.Allow, policy.Emit:
				// Auto-grant (Bless is the new grammar; Allow/Emit retained for legacy)
				commentVersion, _ := evt.Payload["comment_version"].(string)
				_, err := blessing.GrantByVersion(s.DataDir, commentVersion, commentURL, inReplyTo, client, hc, s.PrivateKey)
				if err != nil {
					s.LogWarn("policy auto-grant failed for %s: %v", commentURL, err)
				} else {
					s.LogEvent("pub.polis.comment.blessing.auto_grant", map[string]interface{}{
						"comment_url": commentURL,
						"actor":       evt.Actor,
						"policy_rule": evalResult.Rule,
					})
				}
			case policy.Review:
				// Explicit pending — leave for human review, no action.
				s.LogEvent("pub.polis.policy.review_queued", map[string]interface{}{
					"comment_url": commentURL,
					"actor":       evt.Actor,
					"policy_rule": evalResult.Rule,
				})
			case policy.Deny:
				// Auto-deny
				_, err := blessing.Deny(commentURL, inReplyTo, client, s.PrivateKey)
				if err != nil {
					s.LogWarn("policy auto-deny failed for %s: %v", commentURL, err)
				} else {
					s.LogEvent("pub.polis.comment.blessing.auto_deny", map[string]interface{}{
						"comment_url": commentURL,
						"actor":       evt.Actor,
						"policy_rule": evalResult.Rule,
					})
				}
			}
		}
	}

	// For granted blessings targeting our domain, fetch and store the comment
	// so it appears on our post pages (same as handleBlessingGrant does manually).
	filesChanged := false
	for _, evt := range events {
		// EVICTION (WS3·d): a post-author revoke/deny, or a comment author's
		// unpublish, removes our cached mirror + drops it from blessed.json so it
		// stops rendering. We act only on comments we actually cached (FindBlessed
		// gates it). This honors a signed revocation — a 404 / unreachable author
		// alone never evicts (the cache exists precisely to survive that).
		if evt.Type == "pub.polis.comment.blessing.denied" || evt.Type == "pub.polis.comment.unpublished" {
			cu := firstNonEmptyString(evt.Payload, "comment_url", "source_url", "url")
			if cu == "" {
				continue
			}
			if _, _, ok := cache.FindBlessed(s.DataDir, cu); !ok {
				continue
			}
			// Evict through the SAME generic op Rosie's Reconcile uses (WS-R1
			// anti-drift) — the blessed descriptor evicts across DS scopes.
			evStore := cache.Store{DataDir: s.DataDir, DSDomain: extractDomainFromURL(s.DiscoveryURL)}
			evDesc := cache.NewBlessedDescriptor(cache.DescriptorConfig{})
			_ = cache.Evict(evStore, evDesc, cu)
			if err := metadata.RemoveBlessedComment(s.DataDir, cu); err != nil {
				s.LogWarn("blessing sync: evict %s: failed to update blessed.json: %v", cu, err)
			}
			filesChanged = true
			s.LogEvent("pub.polis.comment.blessing.evict", map[string]interface{}{"comment_url": cu})
			continue
		}

		if evt.Type != "pub.polis.comment.blessing.granted" {
			continue
		}
		targetDomain, _ := evt.Payload["target_domain"].(string)
		if targetDomain != myDomain {
			continue
		}

		// comment_url (current DS) with source_url fallback (legacy DS)
		commentURL := firstNonEmptyString(evt.Payload, "comment_url", "source_url")
		inReplyTo := firstNonEmptyString(evt.Payload, "in_reply_to", "target_url")
		if commentURL == "" || inReplyTo == "" {
			continue
		}

		// Fetch + VERIFY + store into the ISOLATED blessed cache (never under
		// content/ — Defect 3). Idempotent: a no-op (false) if not a comment URL,
		// already cached, or the fetch failed. Shared with handleBlessingGrant via
		// cacheBlessedComment so the two Defect-3 write sites can't drift.
		if !s.cacheBlessedComment(commentURL) {
			continue
		}

		// Update blessed-comments.json index
		postPath := extractPostPathFromURL(inReplyTo)
		if err := metadata.AddBlessedComment(s.DataDir, postPath, metadata.BlessedComment{URL: commentURL}); err != nil {
			s.LogWarn("blessing sync: failed to update blessed-comments for %s: %v", commentURL, err)
		}

		filesChanged = true
		s.LogEvent("pub.polis.comment.blessing.store", map[string]interface{}{
			"comment_url": commentURL,
		})
	}

	return stream.HandlerResult{FilesChanged: filesChanged}
}

// cacheBlessedComment fetches, verifies, and stores a blessed comment authored by
// another tenant into the ISOLATED blessed cache
// (.polis/ds/<ds>/pub.polis.core/cache/blessed/<author>/…) — NEVER under our
// content/ tree (Defect 3). The post-page renderer reads the body from this cache
// (render.LoadLocalCommentContent).
//
// It is idempotent: a no-op returning false when commentURL is not a comment path,
// the comment is already cached, or the fetch/store fails. Returns true only when a
// new entry was written. Both the real-time grant path (handleBlessingGrant) and the
// background sync handler (blessingSyncHandler.Process) call this, so the two
// Defect-3 write sites can never drift.
func (s *Server) cacheBlessedComment(commentURL string) bool {
	// Route through the SAME generic ingest Rosie's Reconcile uses (WS-R1
	// anti-drift): the blessed descriptor owns the fetch + verify-and-gate
	// policy (refuse invalid sig; cache missing/error). Here we only translate
	// the result into the existing observability events.
	store := cache.Store{DataDir: s.DataDir, DSDomain: extractDomainFromURL(s.DiscoveryURL)}
	desc := cache.NewBlessedDescriptor(cache.DescriptorConfig{})
	res := cache.Ingest(store, desc, commentURL, cache.IngestOptions{})

	switch {
	case res.Stored:
		if res.Verdict != "valid" {
			// missing (unsigned legacy) / error (transient key-fetch) cached with
			// VerifiedAt unset — preserved availability, flagged for the canary.
			s.LogWarn("blessed-cache: cached comment %s with UNVERIFIED signature (%s)", commentURL, res.Verdict)
		}
		// Emit the ingest verdict + which source path resolved, so the canary
		// ("ingest with non-valid signature > 0") and the cache-ingest health
		// panel have a structured signal.
		s.LogEvent("pub.polis.comment.cache_ingest", map[string]interface{}{
			"comment_url": commentURL,
			"verdict":     res.Verdict,
			"source":      res.Source,
		})
		return true
	case res.Skipped:
		// Already cached — pinned to the blessed version, edits never refresh.
		return false
	case res.Refused:
		s.LogWarn("blessed-cache: REFUSED comment %s — signature INVALID (tampering/MITM signal)", commentURL)
		s.LogEvent("pub.polis.comment.cache_ingest", map[string]interface{}{
			"comment_url": commentURL,
			"verdict":     "invalid",
			"refused":     true,
		})
		return false
	case res.Err != nil:
		// fetch_failed (network) or store_failed (disk). Feeds the cache-ingest
		// health panel + the canary.
		s.LogWarn("blessed-cache: failed to cache comment %s: %v", commentURL, res.Err)
		s.LogEvent("pub.polis.comment.cache_ingest", map[string]interface{}{
			"comment_url": commentURL,
			"verdict":     res.Verdict,
			"error":       res.Err.Error(),
		})
		return false
	default:
		// unsupported_key — not a comment URL. Silent no-op (as before).
		return false
	}
}

// firstNonEmptyString returns the first non-empty string value from payload
// for the given keys, or empty string if none found.
func firstNonEmptyString(payload map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := payload[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// extractPostPathFromURL extracts the relative post path from a full URL.
// e.g., "https://alice.polis.site/posts/20260127/hello.md" -> "posts/20260127/hello.md"
func extractPostPathFromURL(u string) string {
	idx := strings.Index(u, "/posts/")
	if idx >= 0 {
		return u[idx+1:] // "posts/..."
	}
	return u
}

// --- Comment Status Sync Handler ---

type commentStatusSyncHandler struct {
	server *Server
}

func (h *commentStatusSyncHandler) Name() string { return "comment-status" }

func (h *commentStatusSyncHandler) EventTypes() []string {
	return []string{"pub.polis.comment.blessing.granted", "pub.polis.comment.blessing.denied"}
}

func (h *commentStatusSyncHandler) Process(events []discovery.StreamEvent) stream.HandlerResult {
	s := h.server
	baseURL := s.GetBaseURL()
	if baseURL == "" || s.PrivateKey == nil {
		return stream.HandlerResult{}
	}

	hc := s.getHookConfig()

	result, err := comment.SyncFromEvents(s.DataDir, baseURL, events, hc)
	if err != nil {
		return stream.HandlerResult{Error: err}
	}

	filesChanged := len(result.Blessed) > 0 || len(result.Denied) > 0
	if filesChanged {
		s.LogEvent("pub.polis.comment.sync_auto", map[string]interface{}{
			"blessed": len(result.Blessed),
			"denied":  len(result.Denied),
		})
	}

	return stream.HandlerResult{
		FilesChanged: filesChanged,
		NewItems:     len(result.Blessed) + len(result.Denied),
	}
}
