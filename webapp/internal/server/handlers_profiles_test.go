// Tests for the /api/profiles endpoint and the buildProfilesList helper
// (06-profiles Phase 2). Coverage focuses on the contract pieces the
// frontend depends on:
//   - response shape (type, fields, JSON keys)
//   - relationship derivation (mutual / follows-you / you-follow / none)
//   - sort behavior under both modifiers
//   - recent-post enrichment from the network feed cache
//   - scope=all-polis returning 501 (Phase 3 placeholder)
//   - method/scope/sort validation
package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/feed"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
)

// profilesRequest runs handleProfiles with the given query string and
// decodes the response body as JSON. Mirrors streamRequest's shape so
// the two endpoints' tests read similarly.
func profilesRequest(t *testing.T, s *Server, query string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/profiles?"+query, nil)
	w := httptest.NewRecorder()
	s.handleProfiles(w, req)
	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, w.Body.String())
	}
	return w.Code, body
}

// TestProfiles_MethodGuard locks in the GET-only contract. POST/PUT/DELETE
// land on /api/following (the mutation endpoint); /api/profiles is purely
// a read surface.
func TestProfiles_MethodGuard(t *testing.T) {
	s := newConfiguredServer(t)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req := httptest.NewRequest(method, "/api/profiles", nil)
		w := httptest.NewRecorder()
		s.handleProfiles(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: expected 405, got %d", method, w.Code)
		}
	}
}

// TestProfiles_EmptyTenant returns an empty list for a tenant with no
// outbound follows and no inbound followers. The endpoint must not 500
// on a missing following.json — fresh installs start at zero.
func TestProfiles_EmptyTenant(t *testing.T) {
	s := newConfiguredServer(t)
	code, resp := profilesRequest(t, s, "")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 0 {
		t.Errorf("expected empty items, got %d", len(items))
	}
	if cnt, _ := resp["count"].(float64); cnt != 0 {
		t.Errorf("expected count=0, got %v", cnt)
	}
}

// TestProfiles_RelationshipEnum walks the full relationship truth table.
// Each test case seeds the local-follow + projection state and asserts
// the relationship field on the returned profile.
func TestProfiles_RelationshipEnum(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()

	// alice — outbound only (you-follow)
	// bob   — inbound only (follows-you)
	// carol — both (mutual)
	ff := &following.FollowingFile{Following: []following.FollowingEntry{
		{URL: "https://alice.polis.pub", AddedAt: now.Format(time.RFC3339), AuthorName: "Alice"},
		{URL: "https://carol.polis.pub", AddedAt: now.Format(time.RFC3339), AuthorName: "Carol"},
	}}
	if err := following.Save(following.DefaultPath(s.DataDir), ff); err != nil {
		t.Fatalf("save following: %v", err)
	}
	store := stream.NewStore(s.DataDir, s.GetDiscoveryDomain(), "pub.polis.core")
	fs := &stream.FollowerState{
		Followers: []string{"bob.polis.pub", "carol.polis.pub"},
		Count:     2,
	}
	if err := store.SaveState("pub.polis.follow", fs); err != nil {
		t.Fatalf("save follower state: %v", err)
	}

	code, resp := profilesRequest(t, s, "")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})

	want := map[string]struct {
		relationship string
		following    bool
	}{
		"alice.polis.pub": {"you-follow", true},
		"bob.polis.pub":   {"follows-you", false},
		"carol.polis.pub": {"mutual", true},
	}
	got := make(map[string]map[string]interface{}, len(items))
	for _, it := range items {
		m := it.(map[string]interface{})
		got[m["author_domain"].(string)] = m
	}
	for domain, exp := range want {
		m, ok := got[domain]
		if !ok {
			t.Errorf("missing profile for %s (got %v)", domain, got)
			continue
		}
		if rel, _ := m["relationship"].(string); rel != exp.relationship {
			t.Errorf("relationship for %s: want %q, got %q", domain, exp.relationship, rel)
		}
		if fol, _ := m["following"].(bool); fol != exp.following {
			t.Errorf("following for %s: want %v, got %v", domain, exp.following, fol)
		}
		if typ, _ := m["type"].(string); typ != "profile" {
			t.Errorf("type for %s: want \"profile\", got %q", domain, typ)
		}
	}
}

// TestProfiles_AvatarPopulated verifies attachProfileAvatars stamps every
// profile row with an avatar config. Test domains are unreachable, so the
// fetch falls back to the deterministic hue config — which always carries a
// non-empty bg — so each row's author_avatar must be present.
func TestProfiles_AvatarPopulated(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()
	ff := &following.FollowingFile{Following: []following.FollowingEntry{
		{URL: "https://alice.polis.pub", AddedAt: now.Format(time.RFC3339), AuthorName: "Alice"},
		{URL: "https://bob.polis.pub", AddedAt: now.Format(time.RFC3339), AuthorName: "Bob"},
	}}
	if err := following.Save(following.DefaultPath(s.DataDir), ff); err != nil {
		t.Fatalf("save following: %v", err)
	}

	code, resp := profilesRequest(t, s, "")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) == 0 {
		t.Fatal("expected profile items, got none")
	}
	for _, it := range items {
		m := it.(map[string]interface{})
		domain, _ := m["author_domain"].(string)
		av, ok := m["author_avatar"].(map[string]interface{})
		if !ok {
			t.Errorf("profile %s: missing author_avatar (got %v)", domain, m["author_avatar"])
			continue
		}
		if bg, _ := av["bg"].(string); bg == "" {
			t.Errorf("profile %s: author_avatar has empty bg (%v)", domain, av)
		}
	}
}

// TestProfiles_DeriveRelationshipTable pins the helper's truth table
// directly so refactors to the storage path can't silently flip the
// enum. Same matrix as TestProfiles_RelationshipEnum but at the unit
// level.
func TestProfiles_DeriveRelationshipTable(t *testing.T) {
	cases := []struct {
		isOutbound, isInbound bool
		want                  string
	}{
		{true, true, "mutual"},
		{true, false, "you-follow"},
		{false, true, "follows-you"},
		{false, false, "none"},
	}
	for _, c := range cases {
		got := deriveRelationship(c.isOutbound, c.isInbound)
		if got != c.want {
			t.Errorf("deriveRelationship(out=%v, in=%v): want %q, got %q",
				c.isOutbound, c.isInbound, c.want, got)
		}
	}
}

// TestProfiles_DisplayNameFallback covers the by-name sort key fallback:
// when display_name is empty, the sort key falls back to author_domain.
// The renderer makes the same choice for display.
func TestProfiles_DisplayNameFallback(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()
	// alice has a saved AuthorName; zane doesn't. Both should be
	// findable by handle in the response.
	ff := &following.FollowingFile{Following: []following.FollowingEntry{
		{URL: "https://zane.polis.pub", AddedAt: now.Format(time.RFC3339)},
		{URL: "https://alice.polis.pub", AddedAt: now.Format(time.RFC3339), AuthorName: "Alice"},
	}}
	if err := following.Save(following.DefaultPath(s.DataDir), ff); err != nil {
		t.Fatalf("save following: %v", err)
	}
	code, resp := profilesRequest(t, s, "sort=by-name")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	// Alice has display_name="Alice" → key "alice"; zane has no name →
	// key "zane.polis.pub". Lexicographic order: alice < zane. So
	// "Alice" comes first.
	first := items[0].(map[string]interface{})
	if dn, _ := first["display_name"].(string); dn != "Alice" {
		t.Errorf("first item should be Alice, got display_name=%q", dn)
	}
	second := items[1].(map[string]interface{})
	if ad, _ := second["author_domain"].(string); ad != "zane.polis.pub" {
		t.Errorf("second item should be zane, got %q", ad)
	}
	// zane has no display_name in the response (omitempty).
	if _, present := second["display_name"]; present {
		t.Errorf("zane has no AuthorName; display_name should be omitted, got %v", second["display_name"])
	}
}

// TestProfiles_RecentPostEnrichment covers the per-profile recent-post
// snapshot. Seeds the network feed cache with a post from a followed
// author and verifies the response includes the title/excerpt/published.
func TestProfiles_RecentPostEnrichment(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()

	ff := &following.FollowingFile{Following: []following.FollowingEntry{
		{URL: "https://alice.polis.pub", AddedAt: now.Format(time.RFC3339), AuthorName: "Alice"},
	}}
	if err := following.Save(following.DefaultPath(s.DataDir), ff); err != nil {
		t.Fatalf("save following: %v", err)
	}

	// Seed the network feed cache with two posts from alice; the most
	// recent should win. (Excerpts on cached items are populated by a
	// separate enrichment path — kickOffExcerptFetch — that runs at
	// sync time; the wire-format feed.FeedItem used by the seeder
	// doesn't carry one. Test asserts title + freshness only.)
	seedNetworkFeed(t, s, []feed.FeedItem{
		{
			Type:         "post",
			Title:        "Older note",
			URL:          "https://alice.polis.pub/posts/old.html",
			Published:    now.Add(-48 * time.Hour).Format(time.RFC3339),
			AuthorURL:    "https://alice.polis.pub",
			AuthorDomain: "alice.polis.pub",
		},
		{
			Type:         "post",
			Title:        "Latest note",
			URL:          "https://alice.polis.pub/posts/new.html",
			Published:    now.Add(-1 * time.Hour).Format(time.RFC3339),
			AuthorURL:    "https://alice.polis.pub",
			AuthorDomain: "alice.polis.pub",
		},
	})

	code, resp := profilesRequest(t, s, "")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(items))
	}
	first := items[0].(map[string]interface{})
	rp, ok := first["recent_post"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected recent_post to be set, got %v", first["recent_post"])
	}
	if title, _ := rp["title"].(string); title != "Latest note" {
		t.Errorf("recent_post.title: want %q, got %q", "Latest note", title)
	}
	// last_active should reflect the most-recent feed event, not AddedAt
	// (which would be `now` — newer than the post's Published timestamp).
	if la, _ := first["last_active"].(string); la == "" {
		t.Errorf("last_active should be populated from feed cache")
	}
}

// TestProfiles_RecentPostAbsent locks in the omitempty behavior: when a
// profile has no posts in the feed cache, the recent_post field is
// absent from the response (renderer hides the block).
func TestProfiles_RecentPostAbsent(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()
	ff := &following.FollowingFile{Following: []following.FollowingEntry{
		{URL: "https://orin.polis.pub", AddedAt: now.Format(time.RFC3339)},
	}}
	if err := following.Save(following.DefaultPath(s.DataDir), ff); err != nil {
		t.Fatalf("save following: %v", err)
	}
	code, resp := profilesRequest(t, s, "")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(items))
	}
	first := items[0].(map[string]interface{})
	if rp, present := first["recent_post"]; present && rp != nil {
		t.Errorf("recent_post should be absent (omitempty); got %v", rp)
	}
}

// TestProfiles_ScopeAllPolisDegradesWhenNoDS documents the Phase 3
// graceful-degradation path: /api/profiles?scope=all-polis returns
// 503 when the DS isn't configured (or when the DS hasn't shipped
// /v1/sites/list yet — same response, ErrListSitesNotDeployed). The
// SPA's "all of polis" qualifier can still load; an empty-state CTA
// covers the UX.
//
// (Phase 2 returned 501 here. Phase 3 wires the proxy, and a missing
// DS / missing endpoint surface as 503 so the SPA distinguishes
// "feature not deployed" from "request invalid".)
func TestProfiles_ScopeAllPolisDegradesWhenNoDS(t *testing.T) {
	s := newConfiguredServer(t)
	// newConfiguredServer doesn't configure a DiscoveryURL, so the
	// all-polis path hits the "no DS configured" branch and returns
	// ErrListSitesNotDeployed → 503.
	code, resp := profilesRequest(t, s, "scope=all-polis")
	if code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 (no DS configured), got %d", code)
	}
	if errMsg, _ := resp["error"].(string); errMsg == "" {
		t.Errorf("expected error message in body, got %v", resp)
	}
}

// TestProfiles_ScopeValidation rejects unknown scope values with 400.
func TestProfiles_ScopeValidation(t *testing.T) {
	s := newConfiguredServer(t)
	code, _ := profilesRequest(t, s, "scope=invalid-scope")
	if code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid scope, got %d", code)
	}
}

// TestProfiles_SortValidation rejects unknown sort values with 400.
// Documents that /api/profiles accepts ONLY by-name and by-activity —
// /api/v1/stream/items has the same constraint at the stream layer.
func TestProfiles_SortValidation(t *testing.T) {
	s := newConfiguredServer(t)
	for _, bad := range []string{"by-date", "by-color", "by-relevance"} {
		code, _ := profilesRequest(t, s, "sort="+bad)
		if code != http.StatusBadRequest {
			t.Errorf("sort=%s: expected 400, got %d", bad, code)
		}
	}
}

// TestProfiles_ScopeAllPolisProxiesDS exercises the Phase 3 happy
// path: with a configured DS that returns a populated /v1/sites/list,
// /api/profiles?scope=all-polis returns enriched profile items. The
// test also confirms the relationship/following enrichment from local
// follow state is applied per-row.
func TestProfiles_ScopeAllPolisProxiesDS(t *testing.T) {
	s := newConfiguredServer(t)
	myDomain := extractDomainFromURL(s.GetBaseURL())

	// Fake DS that returns three sites + ensures sort/search/cursor
	// are forwarded correctly. Set up BEFORE the follower-state save
	// so GetDiscoveryDomain returns the same hostname both at save
	// time and at handler-read time (the projection state path is
	// keyed by hostname).
	var lastQuery string
	dsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sites/list" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		lastQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"rows": [
				{"id": 1, "domain": "alice.polis.pub", "registry_url": "https://alice.polis.pub/.well-known/polis", "author_name": "Alice", "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z", "last_active_at": "2026-05-10T10:00:00Z", "post_count": 5, "recent_post_url": "https://alice.polis.pub/posts/x.html", "recent_post_title": "Snap-off", "recent_post_published_at": "2026-05-10T10:00:00Z"},
				{"id": 2, "domain": "bob.polis.pub", "registry_url": "https://bob.polis.pub/.well-known/polis", "author_name": null, "created_at": "2026-01-02T00:00:00Z", "updated_at": "2026-01-02T00:00:00Z", "last_active_at": null, "post_count": 0, "recent_post_url": null, "recent_post_title": null, "recent_post_published_at": null},
				{"id": 3, "domain": "cyrus.polis.pub", "registry_url": "https://cyrus.polis.pub/.well-known/polis", "author_name": "Cyrus", "created_at": "2026-02-01T00:00:00Z", "updated_at": "2026-02-01T00:00:00Z", "last_active_at": "2026-05-15T12:00:00Z", "post_count": 12, "recent_post_url": "", "recent_post_title": "", "recent_post_published_at": ""}
			],
			"next_cursor": "OPAQUECURSOR"
		}`))
	}))
	defer dsServer.Close()
	s.DiscoveryURL = dsServer.URL

	// Local follow state: I follow alice; bob follows me.
	now := time.Now().UTC()
	ff := &following.FollowingFile{Following: []following.FollowingEntry{
		{URL: "https://alice.polis.pub", AddedAt: now.Format(time.RFC3339)},
	}}
	if err := following.Save(following.DefaultPath(s.DataDir), ff); err != nil {
		t.Fatalf("save following: %v", err)
	}
	store := stream.NewStore(s.DataDir, s.GetDiscoveryDomain(), "pub.polis.core")
	fs := &stream.FollowerState{Followers: []string{"bob.polis.pub"}, Count: 1}
	if err := store.SaveState("pub.polis.follow", fs); err != nil {
		t.Fatalf("save follower state: %v", err)
	}

	code, resp := profilesRequest(t, s, "scope=all-polis&sort=by-activity&search=foo&cursor=AB")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (resp=%v)", code, resp)
	}
	// Query forwarding — DS got sort=activity, search=foo, cursor=AB.
	if !strings.Contains(lastQuery, "sort=activity") {
		t.Errorf("expected DS to receive sort=activity, got query=%q", lastQuery)
	}
	if !strings.Contains(lastQuery, "search=foo") {
		t.Errorf("expected DS to receive search=foo, got query=%q", lastQuery)
	}
	if !strings.Contains(lastQuery, "cursor=AB") {
		t.Errorf("expected DS to receive cursor=AB, got query=%q", lastQuery)
	}

	items, _ := resp["items"].([]interface{})
	if len(items) != 3 {
		t.Fatalf("expected 3 profile items, got %d (resp=%v)", len(items), resp)
	}
	byDomain := make(map[string]map[string]interface{})
	for _, it := range items {
		m := it.(map[string]interface{})
		byDomain[m["author_domain"].(string)] = m
	}

	// alice: I follow her, she doesn't follow me → you-follow
	alice, ok := byDomain["alice.polis.pub"]
	if !ok {
		t.Fatal("missing alice in response")
	}
	if rel, _ := alice["relationship"].(string); rel != "you-follow" {
		t.Errorf("alice relationship: want you-follow, got %q", rel)
	}
	if fol, _ := alice["following"].(bool); !fol {
		t.Errorf("alice following: want true, got false")
	}
	if dn, _ := alice["display_name"].(string); dn != "Alice" {
		t.Errorf("alice display_name: want Alice, got %q", dn)
	}
	// recent_post enrichment from DS.
	rp, _ := alice["recent_post"].(map[string]interface{})
	if rp == nil {
		t.Errorf("alice recent_post missing")
	} else if title, _ := rp["title"].(string); title != "Snap-off" {
		t.Errorf("alice recent_post.title: want Snap-off, got %q", title)
	}

	// bob: he follows me, I don't follow back → follows-you
	bob, ok := byDomain["bob.polis.pub"]
	if !ok {
		t.Fatal("missing bob in response")
	}
	if rel, _ := bob["relationship"].(string); rel != "follows-you" {
		t.Errorf("bob relationship: want follows-you, got %q", rel)
	}
	if fol, _ := bob["following"].(bool); fol {
		t.Errorf("bob following: want false, got true")
	}

	// cyrus: no relationship → none
	cyrus, ok := byDomain["cyrus.polis.pub"]
	if !ok {
		t.Fatal("missing cyrus in response")
	}
	if rel, _ := cyrus["relationship"].(string); rel != "none" {
		t.Errorf("cyrus relationship: want none, got %q", rel)
	}

	// myDomain (self) is filtered defensively — even if the DS
	// somehow returned it, the response wouldn't include it. Sanity-
	// check the filter is wired (no row for myDomain in the DS
	// fixture, but the filter is in the data path).
	if _, present := byDomain[myDomain]; present {
		t.Errorf("self %s should be filtered from response", myDomain)
	}

	// Pagination cursor passes through.
	if cur, _ := resp["next_cursor"].(string); cur != "OPAQUECURSOR" {
		t.Errorf("next_cursor: want OPAQUECURSOR, got %q", cur)
	}
}

// TestProfiles_ScopeAllPolisDS404 covers the DS-not-deployed
// degradation path: a 404 from the DS endpoint surfaces as a 503
// from the webapp so the SPA can render a "DS upgrade needed" hint.
func TestProfiles_ScopeAllPolisDS404(t *testing.T) {
	s := newConfiguredServer(t)
	dsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer dsServer.Close()
	s.DiscoveryURL = dsServer.URL

	code, _ := profilesRequest(t, s, "scope=all-polis")
	if code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 (DS endpoint not deployed), got %d", code)
	}
}

// TestProfiles_SelfFiltered documents that the local tenant never
// appears in their own profile list, even if (somehow) self-follow
// data made it into following.json (manual edit, restore from backup
// of another tenant, etc.). Defense in depth.
func TestProfiles_SelfFiltered(t *testing.T) {
	s := newConfiguredServer(t)
	myDomain := extractDomainFromURL(s.GetBaseURL())
	now := time.Now().UTC()
	ff := &following.FollowingFile{Following: []following.FollowingEntry{
		{URL: "https://" + myDomain, AddedAt: now.Format(time.RFC3339)},
		{URL: "https://alice.polis.pub", AddedAt: now.Format(time.RFC3339)},
	}}
	if err := following.Save(following.DefaultPath(s.DataDir), ff); err != nil {
		t.Fatalf("save following: %v", err)
	}
	code, resp := profilesRequest(t, s, "")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 profile (self filtered), got %d", len(items))
	}
	if d, _ := items[0].(map[string]interface{})["author_domain"].(string); d != "alice.polis.pub" {
		t.Errorf("expected alice (self filtered), got %q", d)
	}
}
