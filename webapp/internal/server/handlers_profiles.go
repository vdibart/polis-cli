// Profile surface: 06-profiles Phase 2.
//
// Splits the conflated FOLLOWS event from the PROFILES entity. This file
// owns the new /api/profiles endpoint and the buildProfilesList helper,
// which is also called by the v4 stream items dispatch for type=profiles.
//
// Phase 2 covers scope=my-network only. scope=all-polis is reserved for
// Phase 3 (DS site directory) and returns a 400 from here. The data shape
// is identical across scopes — Phase 3 just adds a new source for the
// item list (DS) and lets the same enrichment + sort code do its work.
//
// Each profile carries:
//   - author handle + display name (fallback to handle)
//   - relationship: mutual | follows-you | you-follow | none
//   - last_active timestamp (most-recent feed-cache event by this
//     author; falls back to the follow add/announce timestamp; falls
//     back further to empty)
//   - optional recent_post: title/url/excerpt/published of the
//     author's most-recent post in the local feed cache
//   - following bool — drives the state-line label + CTA variant
//
// Sort is type-conditional (validated upstream in handlers_stream.go):
// by-name (alphabetic by display_name → handle) is the default;
// by-activity sorts by last_active descending.
package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/feed"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
	"github.com/vdibart/polis-cli/cli-go/pkg/template"
)

// Relationship enum values. Kept as constants so the JSON contract is
// auditable — the SPA's state line + CTA wiring keys off these strings.
const (
	relationshipMutual     = "mutual"      // I follow them AND they follow me
	relationshipFollowsYou = "follows-you" // they follow me, I don't follow back
	relationshipYouFollow  = "you-follow"  // I follow them, they don't follow back
	relationshipNone       = "none"        // neither direction (only possible under all-polis scope)
)

// profileItem is the JSON shape emitted for each profile in the response.
// The renderProfile function in stream.js reads these fields by name.
// Fields are kept flat (no embed) so JSON consumers can rely on the
// stability of the contract without leaking the shape of upstream caches.
type profileItem struct {
	ID             string             `json:"id"`
	Type           string             `json:"type"` // always "profile"
	AuthorDomain   string             `json:"author_domain"`
	AuthorURL      string             `json:"author_url"`
	DisplayName    string             `json:"display_name,omitempty"`
	Following      bool               `json:"following"`
	Relationship   string             `json:"relationship"`
	LastActive     string             `json:"last_active,omitempty"`
	Published      string             `json:"published,omitempty"`       // alias of LastActive for renderer parity
	PublishedHuman string             `json:"published_human,omitempty"` // relative form ("2h ago")
	RecentPost     *profileRecentPost `json:"recent_post,omitempty"`
}

// profileRecentPost is the nested most-recent-post snapshot. Used by the
// SPA's .recent-post-attached block. Empty (nil) when the author has no
// posts in the local feed cache — the SPA hides the block in that case.
type profileRecentPost struct {
	Title          string `json:"title,omitempty"`
	URL            string `json:"url"`
	Excerpt        string `json:"excerpt,omitempty"`
	Published      string `json:"published,omitempty"`
	PublishedHuman string `json:"published_human,omitempty"`
}

// buildProfilesList constructs the profile list for the given scope and
// sort. scope must already be resolved to one of {"my-network",
// "all-polis"} by the caller. sort must already be validated to one of
// {"by-name", "by-activity"}. Returns the materialized item list (caller
// applies pagination/limit and writes the response).
//
// scope=my-network: union of outbound follows (following.json) and
// inbound followers (FollowerState projection). Empty when the tenant
// has neither.
//
// scope=all-polis: returns an explicit not-implemented sentinel via
// the boolean second return; the caller decides whether to 400 or
// return empty (Phase 3 wires the DS directory call here).
// loadFollowSets reads the two follow data sources used by both the
// my-network and all-polis paths: following.json (outbound follows
// keyed by target domain) and the FollowerState projection (inbound
// followers keyed by domain, value is FollowedAt). Missing/corrupt
// sources return empty maps — never errors — so a fresh tenant with
// no follow data renders an empty list rather than a 500.
//
// Extracted in Phase 3 so the all-polis path can compute relationship
// + following flags per profile without duplicating the loader.
func (s *Server) loadFollowSets() (outbound map[string]*following.FollowingEntry, inbound map[string]string) {
	followingPath := following.DefaultPath(s.DataDir)
	followFile, ferr := following.Load(followingPath)
	if ferr != nil {
		s.LogDebug("loadFollowSets: following.Load: %v", ferr)
		followFile = &following.FollowingFile{}
	}
	outbound = make(map[string]*following.FollowingEntry, len(followFile.Following))
	if followFile != nil {
		for i := range followFile.Following {
			entry := &followFile.Following[i]
			d := extractDomainFromURL(entry.URL)
			if d != "" {
				outbound[d] = entry
			}
		}
	}

	inbound = make(map[string]string)
	discoveryDomain := s.GetDiscoveryDomain()
	if discoveryDomain != "" {
		store := stream.NewStore(s.DataDir, discoveryDomain, "pub.polis.core")
		var fs stream.FollowerState
		if err := store.LoadState("pub.polis.follow", &fs); err == nil {
			for _, follower := range fs.Followers {
				ts := ""
				if fs.FollowedAt != nil {
					ts = fs.FollowedAt[follower]
				}
				inbound[follower] = ts
			}
		}
	}
	return outbound, inbound
}

// buildProfilesList constructs the my-network profile list.
// scope=all-polis is no longer routed through here in Phase 3 —
// callers route to buildAllPolisProfiles directly. The bool return
// is retained for source compatibility with the Phase 2 callers
// (always true under the my-network path; pre-3 it signaled the
// unimplemented all-polis branch).
func (s *Server) buildProfilesList(scope, sortBy string) ([]profileItem, bool, error) {
	myDomain := extractDomainFromURL(s.GetBaseURL())

	// Defensive: if a caller hasn't yet been updated to route
	// all-polis through buildAllPolisProfiles, signal "not handled".
	if scope == "all-polis" {
		return nil, false, nil
	}

	outboundByDomain, inboundByDomain := s.loadFollowSets()

	// Union the two sources into a deduped domain set.
	domainSet := make(map[string]struct{}, len(outboundByDomain)+len(inboundByDomain))
	for d := range outboundByDomain {
		domainSet[d] = struct{}{}
	}
	for d := range inboundByDomain {
		domainSet[d] = struct{}{}
	}

	// --- Source 3: network feed cache (for recent-post + last-active) -
	// One pass over the cache builds two maps keyed by author domain:
	//   - mostRecentPost[domain]  → CachedFeedItem (Type:"post" only)
	//   - lastActiveByDomain[domain] → timestamp (max across all types)
	// Cache miss / empty cache is non-fatal; we fall back to follow
	// timestamps below.
	mostRecentPost := make(map[string]*feed.CachedFeedItem, len(domainSet))
	lastActiveByDomain := make(map[string]string, len(domainSet))
	cm := s.feedCacheForScope("network")
	if cm != nil {
		all, err := cm.List()
		if err != nil {
			s.LogDebug("buildProfilesList: feed cache List: %v", err)
		} else {
			for i := range all {
				it := &all[i]
				d := it.AuthorDomain
				if d == "" {
					continue
				}
				if _, want := domainSet[d]; !want {
					continue
				}
				if it.Published > lastActiveByDomain[d] {
					lastActiveByDomain[d] = it.Published
				}
				if it.Type == "post" {
					if mostRecentPost[d] == nil || it.Published > mostRecentPost[d].Published {
						mostRecentPost[d] = it
					}
				}
			}
		}
	}

	// --- Materialize the item list -----------------------------------
	items := make([]profileItem, 0, len(domainSet))
	for domain := range domainSet {
		if domain == myDomain {
			// Defensive: don't surface the local tenant in their own
			// profile list (cycles + UX confusion). loadOwnFollowsAsFeedItems
			// already filters self by construction, but the union path
			// here recombines sources that could in theory carry self.
			continue
		}

		_, isOutbound := outboundByDomain[domain]
		_, isInbound := inboundByDomain[domain]
		item := profileItem{
			ID:           "profile:" + domain,
			Type:         "profile",
			AuthorDomain: domain,
			AuthorURL:    "https://" + domain,
			Following:    isOutbound,
			Relationship: deriveRelationship(isOutbound, isInbound),
		}

		// Display name: prefer the saved author_name from following.json
		// (UpdateMetadata fills this from the remote's .well-known); fall
		// back to site_title; final fallback is the bare handle (renderer
		// chooses what to show when display_name == handle).
		if entry := outboundByDomain[domain]; entry != nil {
			if entry.AuthorName != "" {
				item.DisplayName = entry.AuthorName
			} else if entry.SiteTitle != "" {
				item.DisplayName = entry.SiteTitle
			}
		}

		// last_active: feed-cache observation wins (captures real
		// activity); fall back to follow add/announce timestamp.
		if ts, ok := lastActiveByDomain[domain]; ok && ts != "" {
			item.LastActive = ts
		} else if entry := outboundByDomain[domain]; entry != nil && entry.AddedAt != "" {
			item.LastActive = entry.AddedAt
		} else if ts := inboundByDomain[domain]; ts != "" {
			item.LastActive = ts
		}
		// Surface LastActive under Published too so the SPA renderer's
		// generic published_human field works without per-type branching.
		item.Published = item.LastActive
		if item.LastActive != "" {
			item.PublishedHuman = template.FormatHumanDateTime(item.LastActive)
		}

		if post := mostRecentPost[domain]; post != nil {
			item.RecentPost = &profileRecentPost{
				Title:          post.Title,
				URL:            post.URL,
				Excerpt:        post.Excerpt,
				Published:      post.Published,
				PublishedHuman: template.FormatHumanDateTime(post.Published),
			}
		}

		items = append(items, item)
	}

	// --- Sort --------------------------------------------------------
	sortProfileItems(items, sortBy)

	return items, true, nil
}

// deriveRelationship returns the relationship enum value for a profile,
// given which directions of the follow relationship exist with the local
// tenant. Kept as a small standalone helper so the test suite can pin
// the table directly without faking storage.
func deriveRelationship(isOutbound, isInbound bool) string {
	switch {
	case isOutbound && isInbound:
		return relationshipMutual
	case isInbound:
		return relationshipFollowsYou
	case isOutbound:
		return relationshipYouFollow
	default:
		return relationshipNone
	}
}

// sortProfileItems applies the by-name or by-activity ordering in place.
// by-name: case-insensitive on display_name → falls back to author_domain
// when display_name is empty. Stable tiebreak on author_domain so equal
// names produce deterministic pages.
// by-activity: last_active descending. Items with empty last_active sort
// last (we have no signal for them).
func sortProfileItems(items []profileItem, sortBy string) {
	if sortBy == "by-activity" {
		sort.SliceStable(items, func(i, j int) bool {
			li, lj := items[i].LastActive, items[j].LastActive
			if li == lj {
				return items[i].AuthorDomain < items[j].AuthorDomain
			}
			if li == "" {
				return false
			}
			if lj == "" {
				return true
			}
			return li > lj
		})
		return
	}
	// Default: by-name. Sort key falls back to handle when display
	// name is missing (Phase 1 design decision documented in the plan).
	sortKey := func(p profileItem) string {
		if p.DisplayName != "" {
			return strings.ToLower(p.DisplayName)
		}
		return strings.ToLower(p.AuthorDomain)
	}
	sort.SliceStable(items, func(i, j int) bool {
		ki, kj := sortKey(items[i]), sortKey(items[j])
		if ki != kj {
			return ki < kj
		}
		return items[i].AuthorDomain < items[j].AuthorDomain
	})
}

// buildAllPolisProfiles proxies the DS site directory listing
// (/v1/sites/list) and enriches each row with the local tenant's
// follow state (relationship + following bool) so the SPA can render
// uniform .entry--profile entries under both qualifiers.
//
// The DS view already carries last_active_at and the most-recent post
// title/url/published_at, so no second round-trip is needed per row.
// Pagination cursor passes straight through from the DS response.
//
// Returns ErrListSitesNotDeployed when the DS at the configured URL
// hasn't shipped the /v1/sites/list endpoint yet; the caller turns
// that into a 503 with a human-readable message so the SPA can
// degrade gracefully during DS upgrade windows.
func (s *Server) buildAllPolisProfiles(r *http.Request, sortBy, search, cursor string, limit int) (items []profileItem, nextCursor string, err error) {
	if s.DiscoveryURL == "" {
		// No DS configured: behaves the same as "not deployed" since
		// there's nowhere to fetch from. Surface as the same error so
		// the SPA's degradation path is uniform.
		return nil, "", discovery.ErrListSitesNotDeployed
	}

	// Map sort name to the DS endpoint's vocabulary. SPA/grammar
	// uses "by-name"/"by-activity" (the third-clause modifier);
	// DS uses "name"/"activity".
	dsSort := "name"
	if sortBy == "by-activity" {
		dsSort = "activity"
	}

	client := s.NewDSClient(r)
	resp, derr := client.ListSites(discovery.SiteListOptions{
		Sort:   dsSort,
		Limit:  limit,
		Cursor: cursor,
		Search: search,
	})
	if derr != nil {
		return nil, "", derr
	}

	// Local follow state — used to enrich each DS row with the
	// relationship + following flag the SPA reads.
	outboundByDomain, inboundByDomain := s.loadFollowSets()
	myDomain := extractDomainFromURL(s.GetBaseURL())

	items = make([]profileItem, 0, len(resp.Rows))
	for i := range resp.Rows {
		row := &resp.Rows[i]
		if row.Domain == "" {
			continue
		}
		if row.Domain == myDomain {
			// Don't surface the local tenant in their own directory
			// listing. Matches the self-filter in buildProfilesList.
			continue
		}
		_, isOutbound := outboundByDomain[row.Domain]
		_, isInbound := inboundByDomain[row.Domain]
		item := profileItem{
			ID:           "profile:" + row.Domain,
			Type:         "profile",
			AuthorDomain: row.Domain,
			AuthorURL:    "https://" + row.Domain,
			Following:    isOutbound,
			Relationship: deriveRelationship(isOutbound, isInbound),
		}
		if row.AuthorName != "" {
			item.DisplayName = row.AuthorName
		}
		// last_active: DS view value (any-event timestamp). Falls
		// back to created_at when the actor has no events yet —
		// gives the user something to anchor "joined recently".
		if row.LastActiveAt != "" {
			item.LastActive = row.LastActiveAt
		} else if row.CreatedAt != "" {
			item.LastActive = row.CreatedAt
		}
		item.Published = item.LastActive
		if item.LastActive != "" {
			item.PublishedHuman = template.FormatHumanDateTime(item.LastActive)
		}
		// Recent post if the DS view supplied one.
		if row.RecentPostURL != "" || row.RecentPostTitle != "" {
			rp := &profileRecentPost{
				Title:     row.RecentPostTitle,
				URL:       row.RecentPostURL,
				Published: row.RecentPostPublishedAt,
			}
			if rp.Published != "" {
				rp.PublishedHuman = template.FormatHumanDateTime(rp.Published)
			}
			item.RecentPost = rp
		}
		items = append(items, item)
	}

	return items, resp.NextCursor, nil
}

// handleProfiles serves GET /api/profiles.
//
// Defaults (matching the sentence-filter grammar):
//   - scope: my-network
//   - sort:  by-name
//
// Phase 3 supports both scopes:
//   - my-network: union of outbound follows + inbound followers,
//     enriched from the local feed cache.
//   - all-polis: proxies to the DS /v1/sites/list endpoint, intersects
//     each row with local follow state for relationship/following.
//     Accepts &search=<q> and &cursor=<opaque> query params for
//     debounced search + pagination.
//
// Authentication: none. This is owner-only data exposed through the
// owner-trust model — the webapp binds to localhost or runs behind the
// hosted auth gate; either way, profile data is read-only and identical
// to what the existing /api/following already returns.
func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	scope := strings.ToLower(q.Get("scope"))
	sortBy := strings.ToLower(q.Get("sort"))
	if scope == "" {
		scope = "my-network"
	}
	if sortBy == "" {
		sortBy = "by-name"
	}

	// Validate scope.
	if scope != "my-network" && scope != "all-polis" {
		writeJSONError(w, http.StatusBadRequest,
			"scope must be 'my-network' or 'all-polis'")
		return
	}
	// Validate sort.
	if sortBy != "by-name" && sortBy != "by-activity" {
		writeJSONError(w, http.StatusBadRequest,
			"sort must be 'by-name' or 'by-activity'")
		return
	}

	if scope == "all-polis" {
		search := q.Get("search")
		cursor := q.Get("cursor")
		limit := parseStreamLimit(q.Get("limit"))
		items, nextCursor, err := s.buildAllPolisProfiles(r, sortBy, search, cursor, limit)
		if err != nil {
			if errors.Is(err, discovery.ErrListSitesNotDeployed) {
				// Graceful degradation during a DS upgrade window:
				// the SPA can still load /profiles?my-network and
				// surface a "all-of-polis requires DS upgrade" hint.
				writeJSONError(w, http.StatusServiceUnavailable,
					"all-of-polis requires a discovery-service upgrade (no /v1/sites/list)")
				return
			}
			s.LogError("buildAllPolisProfiles failed: %v", err)
			writeJSONError(w, http.StatusBadGateway, "discovery-service unavailable")
			return
		}
		if items == nil {
			items = []profileItem{}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		resp := map[string]interface{}{
			"items": items,
			"count": len(items),
		}
		if nextCursor != "" {
			resp["next_cursor"] = nextCursor
		}
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	// scope=my-network
	items, _, err := s.buildProfilesList(scope, sortBy)
	if err != nil {
		s.LogError("buildProfilesList failed: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to build profile list")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if items == nil {
		items = []profileItem{}
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"items": items,
		"count": len(items),
	})
}

// writeJSONError is a small helper matching writeStreamError's shape so
// /api/profiles responses are consistent with the v4 stream API. Kept
// local to avoid pulling in the streamError type (which is internal to
// handlers_stream.go) for a one-line response.
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
