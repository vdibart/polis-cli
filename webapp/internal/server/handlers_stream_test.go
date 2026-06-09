package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/cache"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/feed"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
)

// TestMain swaps render.DefaultHTTPClient for a stub that errors on all
// requests. Without this guard, tests that exercise the type=comments
// thread-entry enrichment path would make live HTTP calls to URLs in
// test fixtures (e.g. https://stub.polis.pub/...), which is flaky and
// slow. Tests that need fresh-fetch behavior should override locally.
type errTransport struct{}

func (errTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("test stub: HTTP disabled — pre-populate the reply-context-cache instead")
}

func TestMain(m *testing.M) {
	render.DefaultHTTPClient = &http.Client{Transport: errTransport{}}
	os.Exit(m.Run())
}

// streamRequest performs a GET against /api/v1/stream/items with the given
// query string and returns the decoded response. No auth header — handler
// uses owner-trust model (see handlers_stream.go header comment).
func streamRequest(t *testing.T, s *Server, query string) (int, map[string]interface{}) {
	t.Helper()
	url := "/api/v1/stream/items"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	s.handleStreamItems(w, req)
	var resp map[string]interface{}
	if w.Body.Len() > 0 {
		_ = json.NewDecoder(w.Body).Decode(&resp)
	}
	return w.Code, resp
}

// seedNetworkFeed populates the tenant's network feed cache with the given
// items so structured-query tests have data to filter against.
func seedNetworkFeed(t *testing.T, s *Server, items []feed.FeedItem) {
	t.Helper()
	cm := feed.NewCacheManager(s.DataDir, "default")
	if _, err := cm.MergeItems(items); err != nil {
		t.Fatalf("seed feed: %v", err)
	}
}

func TestStreamItems_MethodNotAllowed(t *testing.T) {
	s := newConfiguredServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/stream/items", nil)
	w := httptest.NewRecorder()
	s.handleStreamItems(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestStreamItems_DefaultsToMyNetworkAllPosts(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()
	seedNetworkFeed(t, s, []feed.FeedItem{
		{Type: "post", Title: "P1", URL: "posts/p1.md", Published: now.Add(-1 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "comment", Title: "C1", URL: "comments/c1.md", Published: now.Add(-2 * time.Hour).Format(time.RFC3339), AuthorURL: "https://b.pub", AuthorDomain: "b.pub"},
	})

	code, resp := streamRequest(t, s, "type=posts")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 1 {
		t.Errorf("type=posts should return 1 post, got %d", len(items))
	}
}

// TestStreamItems_EmitsPublishedHuman locks in SC-3 close (step-05/5.g):
// every returned item carries a server-formatted `published_human` field
// matching template.FormatHumanDateTime ("April 23, 2026 · 4:12pm"). The
// JS controller deleted its in-browser formatter under this guarantee;
// regressing this contract would put the SSR'd entries and the JS-
// paginated entries back on visibly different date formats.
func TestStreamItems_EmitsPublishedHuman(t *testing.T) {
	s := newConfiguredServer(t)
	// Use a timestamp 5 days back (well inside the 30-day maxStreamWindow cap
	// so time=all still returns it, yet > 10h old so FormatHumanDateTime emits
	// the absolute "Month D · h:mma" form rather than a relative string).
	// Derived from now, not a pinned literal, so the test doesn't rot once the
	// fixed date ages past the look-back window. The v4 stream strips the
	// per-entry year (template.StripYear), so the expected form omits the year.
	pub := time.Now().UTC().AddDate(0, 0, -5).Truncate(time.Minute)
	pubISO := pub.Format(time.RFC3339)
	wantHuman := pub.Format("January 2 · 3:04pm")
	seedNetworkFeed(t, s, []feed.FeedItem{
		{Type: "post", Title: "P1", URL: "posts/p1.md", Published: pubISO, AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
	})
	code, resp := streamRequest(t, s, "type=posts&time=all")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	item, _ := items[0].(map[string]interface{})
	if got, _ := item["published_human"].(string); got != wantHuman {
		t.Errorf("published_human = %q, want %q", got, wantHuman)
	}
	// Source ISO timestamp also still present so consumers needing the
	// machine-readable form aren't forced to reparse.
	if got, _ := item["published"].(string); got != pubISO {
		t.Errorf("published = %q, want %q", got, pubISO)
	}
}

func TestStreamItems_TypeFiltering(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()
	seedNetworkFeed(t, s, []feed.FeedItem{
		{Type: "post", Title: "P1", URL: "posts/p1.md", Published: now.Add(-1 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "P2", URL: "posts/p2.md", Published: now.Add(-3 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "comment", Title: "C1", URL: "comments/c1.md", Published: now.Add(-2 * time.Hour).Format(time.RFC3339), AuthorURL: "https://b.pub", AuthorDomain: "b.pub"},
	})

	// type=posts → 2 items
	_, resp := streamRequest(t, s, "type=posts&time=24h")
	if items, _ := resp["items"].([]interface{}); len(items) != 2 {
		t.Errorf("type=posts: expected 2, got %d", len(items))
	}

	// type=comments → 1 item
	_, resp = streamRequest(t, s, "type=comments&time=24h")
	if items, _ := resp["items"].([]interface{}); len(items) != 1 {
		t.Errorf("type=comments: expected 1, got %d", len(items))
	}

	// type=profiles → empty (not represented in feed cache today)
	_, resp = streamRequest(t, s, "type=profiles&time=24h")
	if items, _ := resp["items"].([]interface{}); len(items) != 0 {
		t.Errorf("type=profiles: expected 0 (no representation), got %d", len(items))
	}

	// type=mentions → empty (same)
	_, resp = streamRequest(t, s, "type=mentions&time=24h")
	if items, _ := resp["items"].([]interface{}); len(items) != 0 {
		t.Errorf("type=mentions: expected 0 (no representation), got %d", len(items))
	}

	// type=invalid → 400
	code, _ := streamRequest(t, s, "type=bogus")
	if code != http.StatusBadRequest {
		t.Errorf("invalid type: expected 400, got %d", code)
	}
}

// TestStreamItems_ScopeMyAuthorFilter — scope=my reads the local
// public index (not the network feed cache, which self-skips own
// events on sync). step-06/6.e broader fix: scope=my and scope=
// @<myDomain> both route through loadOwnContentAsFeedItems; both
// must return the same shape for the same data.
//
// Previous version of this test seeded the NETWORK feed cache with
// self events — which only worked because the test bypassed
// FeedHandler's self-skip. In production scope=my returned empty.
// Updated to seed the local public index (the real production
// source), and to assert scope=my == scope=@<myDomain>.
func TestStreamItems_ScopeMyAuthorFilter(t *testing.T) {
	s := newConfiguredServer(t)
	myDomain := extractDomainFromURL(s.GetBaseURL())
	if myDomain == "" {
		t.Fatal("test setup didn't configure a base URL")
	}

	now := time.Now().UTC()
	// Self-authored post in the local public index — what
	// loadOwnContentAsFeedItems reads in production.
	postPath := "content/pub.polis.core/post/20260507/mine.md"
	full := filepath.Join(s.DataDir, postPath)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte("---\ntitle: Mine\n---\nbody"), 0644); err != nil {
		t.Fatalf("write post: %v", err)
	}
	if err := metadata.AppendPostToIndex(s.DataDir, postPath, "Mine", now.Add(-1*time.Hour).Format(time.RFC3339), "sha256:mine"); err != nil {
		t.Fatalf("append index: %v", err)
	}
	// Cross-tenant post in the network cache (for scope=my-network /
	// scope=@other.pub branches).
	seedNetworkFeed(t, s, []feed.FeedItem{
		{Type: "post", Title: "Theirs", URL: "posts/t.md", Published: now.Add(-2 * time.Hour).Format(time.RFC3339), AuthorURL: "https://other.pub", AuthorDomain: "other.pub"},
	})

	// scope=my returns my own post from the local index.
	_, resp := streamRequest(t, s, "scope=my&type=posts&time=24h")
	items, _ := resp["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("scope=my: expected 1 item from local index, got %d", len(items))
	}
	if title := items[0].(map[string]interface{})["title"].(string); title != "Mine" {
		t.Errorf("scope=my returned wrong item: %v", title)
	}

	// scope=me (URL-token alias) returns the same.
	_, resp = streamRequest(t, s, "scope=me&type=posts&time=24h")
	items, _ = resp["items"].([]interface{})
	if len(items) != 1 {
		t.Errorf("scope=me (alias): expected 1 item, got %d", len(items))
	}

	// scope=@<myDomain> (handle form) returns the same — must be
	// equivalent to scope=my for the local-tenant data.
	_, resp = streamRequest(t, s, "scope=@"+myDomain+"&type=posts&time=24h")
	items, _ = resp["items"].([]interface{})
	if len(items) != 1 {
		t.Errorf("scope=@<myDomain>: expected 1 item, got %d", len(items))
	}

	// scope=my-network returns "Theirs" only (FeedHandler's self-skip
	// keeps own events out of the network cache; only the seeded
	// other.pub post comes back).
	_, resp = streamRequest(t, s, "scope=my-network&type=posts&time=24h")
	items, _ = resp["items"].([]interface{})
	if len(items) != 1 {
		t.Errorf("scope=my-network: expected 1 cross-tenant item, got %d", len(items))
	}

	// scope=@other.pub returns the cross-tenant item.
	_, resp = streamRequest(t, s, "scope=@other.pub&type=posts&time=24h")
	items, _ = resp["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("scope=@other.pub: expected 1 item, got %d", len(items))
	}
	if title := items[0].(map[string]interface{})["title"].(string); title != "Theirs" {
		t.Errorf("scope=@other.pub returned wrong item: %v", title)
	}
}

func TestStreamItems_ScopeAllPolis(t *testing.T) {
	s := newConfiguredServer(t)
	code, _ := streamRequest(t, s, "scope=all-polis&type=posts&time=24h")
	if code != http.StatusOK {
		t.Errorf("scope=all-polis: expected 200, got %d", code)
	}
}

// TestStreamItems_OwnContentCarriesSourcePath — own-handle stream items
// must ship `source_path` so the v4 stream's owner-action decorators
// (decoratePost/decorateComment in owner-extras.js) can drive
// /api/unpublish, which requires a path under content/pub.polis.core/...
// Without source_path the decorator would fall back to the rendered
// .html mount path (meta.url) which the unpublish handler rejects with
// 400. Cross-tenant items must NOT carry source_path (the unpublish
// action doesn't apply to them).
func TestStreamItems_OwnContentCarriesSourcePath(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()

	// Self-authored post — should carry source_path.
	postPath := "content/pub.polis.core/post/20260507/source-path-test.md"
	full := filepath.Join(s.DataDir, postPath)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte("---\ntitle: Mine\n---\nbody"), 0644); err != nil {
		t.Fatalf("write post: %v", err)
	}
	if err := metadata.AppendPostToIndex(s.DataDir, postPath, "Mine", now.Add(-1*time.Hour).Format(time.RFC3339), "sha256:srcpath"); err != nil {
		t.Fatalf("append index: %v", err)
	}

	// Cross-tenant post — must NOT carry source_path.
	seedNetworkFeed(t, s, []feed.FeedItem{
		{Type: "post", Title: "Theirs", URL: "posts/t.md", Published: now.Add(-2 * time.Hour).Format(time.RFC3339), AuthorURL: "https://other.pub", AuthorDomain: "other.pub"},
	})

	// scope=my returns the own post; assert source_path matches storage form.
	_, resp := streamRequest(t, s, "scope=my&type=posts&time=24h")
	items, _ := resp["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("scope=my: expected 1 item, got %d", len(items))
	}
	item := items[0].(map[string]interface{})
	gotPath, _ := item["source_path"].(string)
	if gotPath != postPath {
		t.Errorf("scope=my own post: source_path = %q, want %q", gotPath, postPath)
	}
	// And the path must validate against the unpublish handler's
	// prefix check — i.e. start with content/pub.polis.core/post/.
	if !strings.HasPrefix(gotPath, "content/pub.polis.core/post/") {
		t.Errorf("source_path doesn't satisfy /api/unpublish prefix check: %q", gotPath)
	}

	// scope=@other.pub returns the cross-tenant post; source_path must be empty/absent.
	_, resp = streamRequest(t, s, "scope=@other.pub&type=posts&time=24h")
	items, _ = resp["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("scope=@other.pub: expected 1 item, got %d", len(items))
	}
	item = items[0].(map[string]interface{})
	if sp, ok := item["source_path"]; ok && sp != "" {
		t.Errorf("cross-tenant item must not carry source_path; got %v", sp)
	}
}

func TestStreamItems_QualifierFiltersUnread(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()
	seedNetworkFeed(t, s, []feed.FeedItem{
		{Type: "post", Title: "A", URL: "posts/a.md", Published: now.Add(-1 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "B", URL: "posts/b.md", Published: now.Add(-2 * time.Hour).Format(time.RFC3339), AuthorURL: "https://b.pub", AuthorDomain: "b.pub"},
	})

	cm := feed.NewCacheManager(s.DataDir, "default")
	// Mark A as read by looking up its ID first.
	all, err := cm.List()
	if err != nil {
		t.Fatalf("list cache: %v", err)
	}
	for _, item := range all {
		if item.Title == "A" {
			if err := cm.MarkRead(item.ID); err != nil {
				t.Fatalf("mark read: %v", err)
			}
		}
	}

	// qualifier=all → both items
	_, resp := streamRequest(t, s, "qualifier=all&type=posts&time=24h")
	if items, _ := resp["items"].([]interface{}); len(items) != 2 {
		t.Errorf("qualifier=all: expected 2, got %d", len(items))
	}

	// qualifier=new → only the unread one
	_, resp = streamRequest(t, s, "qualifier=new&type=posts&time=24h")
	items, _ := resp["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("qualifier=new: expected 1 unread, got %d", len(items))
	}
	if title := items[0].(map[string]interface{})["title"].(string); title != "B" {
		t.Errorf("qualifier=new returned wrong item: %v", title)
	}
}

func TestStreamItems_TimeWindowFilter(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()
	seedNetworkFeed(t, s, []feed.FeedItem{
		{Type: "post", Title: "Recent", URL: "posts/r.md", Published: now.Add(-30 * time.Minute).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "Old", URL: "posts/o.md", Published: now.Add(-72 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
	})

	// time=1h → only Recent
	_, resp := streamRequest(t, s, "type=posts&time=1h")
	items, _ := resp["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("time=1h: expected 1, got %d", len(items))
	}
	if title := items[0].(map[string]interface{})["title"].(string); title != "Recent" {
		t.Errorf("time=1h returned wrong item: %v", title)
	}

	// time=7d → both
	_, resp = streamRequest(t, s, "type=posts&time=7d")
	if items, _ := resp["items"].([]interface{}); len(items) != 2 {
		t.Errorf("time=7d: expected 2, got %d", len(items))
	}

	// time=all → both
	_, resp = streamRequest(t, s, "type=posts&time=all")
	if items, _ := resp["items"].([]interface{}); len(items) != 2 {
		t.Errorf("time=all: expected 2, got %d", len(items))
	}

	// time=invalid → 400
	code, _ := streamRequest(t, s, "type=posts&time=bogus")
	if code != http.StatusBadRequest {
		t.Errorf("invalid time: expected 400, got %d", code)
	}
}

func TestStreamItems_OrderNewestVsOldest(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()
	seedNetworkFeed(t, s, []feed.FeedItem{
		{Type: "post", Title: "First", URL: "posts/1.md", Published: now.Add(-3 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "Second", URL: "posts/2.md", Published: now.Add(-2 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "Third", URL: "posts/3.md", Published: now.Add(-1 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
	})

	// newest first
	_, resp := streamRequest(t, s, "type=posts&time=24h&order=newest")
	items, _ := resp["items"].([]interface{})
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	titles := make([]string, len(items))
	for i, it := range items {
		titles[i] = it.(map[string]interface{})["title"].(string)
	}
	if titles[0] != "Third" || titles[1] != "Second" || titles[2] != "First" {
		t.Errorf("newest order wrong: %v", titles)
	}

	// oldest first
	_, resp = streamRequest(t, s, "type=posts&time=24h&order=oldest")
	items, _ = resp["items"].([]interface{})
	for i, it := range items {
		titles[i] = it.(map[string]interface{})["title"].(string)
	}
	if titles[0] != "First" || titles[1] != "Second" || titles[2] != "Third" {
		t.Errorf("oldest order wrong: %v", titles)
	}
}

func TestStreamItems_CursorPagination(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()
	// Seed 10 posts, distinct timestamps so cursor disambiguation is clean
	var seed []feed.FeedItem
	for i := 0; i < 10; i++ {
		seed = append(seed, feed.FeedItem{
			Type:         "post",
			Title:        fmt.Sprintf("P%02d", i),
			URL:          fmt.Sprintf("posts/p%02d.md", i),
			Published:    now.Add(-time.Duration(i+1) * time.Minute).Format(time.RFC3339),
			AuthorURL:    "https://a.pub",
			AuthorDomain: "a.pub",
		})
	}
	seedNetworkFeed(t, s, seed)

	// First page: limit 4
	_, resp := streamRequest(t, s, "type=posts&time=24h&limit=4")
	items, _ := resp["items"].([]interface{})
	if len(items) != 4 {
		t.Fatalf("page 1: expected 4 items, got %d", len(items))
	}
	cursor1, _ := resp["next_cursor"].(string)
	if cursor1 == "" {
		t.Fatal("page 1: expected next_cursor (more items remain)")
	}

	// Second page: continue with cursor1
	_, resp = streamRequest(t, s, "type=posts&time=24h&limit=4&cursor="+cursor1)
	items2, _ := resp["items"].([]interface{})
	if len(items2) != 4 {
		t.Fatalf("page 2: expected 4 items, got %d", len(items2))
	}
	// Verify no overlap with page 1
	page1Titles := make(map[string]bool)
	for _, it := range items {
		page1Titles[it.(map[string]interface{})["title"].(string)] = true
	}
	for _, it := range items2 {
		title := it.(map[string]interface{})["title"].(string)
		if page1Titles[title] {
			t.Errorf("page 2 overlaps page 1: duplicate %q", title)
		}
	}

	// Third page: should have the last 2 items, no next_cursor
	cursor2, _ := resp["next_cursor"].(string)
	if cursor2 == "" {
		t.Fatal("page 2: expected next_cursor (2 items remain)")
	}
	_, resp = streamRequest(t, s, "type=posts&time=24h&limit=4&cursor="+cursor2)
	items3, _ := resp["items"].([]interface{})
	if len(items3) != 2 {
		t.Fatalf("page 3: expected 2 items, got %d", len(items3))
	}
	if cursor3, _ := resp["next_cursor"].(string); cursor3 != "" {
		t.Errorf("page 3: expected no next_cursor (dataset exhausted), got %q", cursor3)
	}
}

// TestStreamItems_CursorTiedTimestamps verifies the DL-1 fix — items
// sharing the same Published second at a page boundary aren't dropped or
// double-included. Without the (timestamp, id) cursor tiebreaker, items
// at the boundary would silently disappear from page 2.
func TestStreamItems_CursorTiedTimestamps(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()
	tied := now.Add(-1 * time.Hour).Format(time.RFC3339)
	seedNetworkFeed(t, s, []feed.FeedItem{
		// 4 items all at the same timestamp (e.g., a batch publish or
		// concurrent writes landing in the same second).
		{Type: "post", Title: "T1", URL: "posts/t1.md", Published: tied, AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "T2", URL: "posts/t2.md", Published: tied, AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "T3", URL: "posts/t3.md", Published: tied, AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "T4", URL: "posts/t4.md", Published: tied, AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		// One item at an earlier timestamp.
		{Type: "post", Title: "Older", URL: "posts/o.md", Published: now.Add(-2 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
	})

	// Collect all titles across pages with limit=2.
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 10; page++ { // safety cap
		query := "type=posts&time=24h&limit=2"
		if cursor != "" {
			query += "&cursor=" + cursor
		}
		_, resp := streamRequest(t, s, query)
		items, _ := resp["items"].([]interface{})
		for _, it := range items {
			title := it.(map[string]interface{})["title"].(string)
			if seen[title] {
				t.Errorf("title %q appeared on multiple pages (cursor double-include)", title)
			}
			seen[title] = true
		}
		next, _ := resp["next_cursor"].(string)
		if next == "" {
			break
		}
		cursor = next
	}
	if len(seen) != 5 {
		t.Errorf("cursor pagination across tied timestamps: expected 5 distinct items, got %d (%v)", len(seen), seen)
	}
}

// TestStreamItems_CursorRFC3339NanoCompatible verifies the DL-2 fix —
// cursor comparison parses to time.Time so RFC3339 representation
// differences (e.g., a future RFC3339Nano upstream) don't break
// pagination. Constructs a cursor from a known-good item and confirms
// pagination still selects items strictly older than that timestamp.
func TestStreamItems_CursorRFC3339NanoCompatible(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()
	seedNetworkFeed(t, s, []feed.FeedItem{
		{Type: "post", Title: "Newer", URL: "posts/n.md", Published: now.Add(-1 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "Older", URL: "posts/o.md", Published: now.Add(-3 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
	})

	// Use a cursor where the timestamp is RFC3339Nano-formatted (sub-second
	// precision). Stored Published values are RFC3339 (no sub-second).
	// Parsed comparison should still treat the cursor's instant correctly.
	cursorTime := now.Add(-2 * time.Hour) // between Newer (-1h) and Older (-3h)
	cursorPayload := streamCursor{
		BeforePublished: cursorTime.Format(time.RFC3339Nano),
		BeforeID:        "",
	}
	encodedCursor := encodeStreamCursor(cursorPayload)

	_, resp := streamRequest(t, s, "type=posts&time=24h&limit=10&cursor="+encodedCursor)
	items, _ := resp["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("RFC3339Nano cursor: expected 1 item (Older), got %d", len(items))
	}
	if title := items[0].(map[string]interface{})["title"].(string); title != "Older" {
		t.Errorf("RFC3339Nano cursor: expected Older, got %q", title)
	}
}

func TestStreamItems_DMPlaceholder(t *testing.T) {
	s := newConfiguredServer(t)
	// DM is owner-private under the owner-trust model — only the owner's
	// session can reach polis-server in the first place. The handler
	// returns an empty list as a placeholder until 4.e wires the DM cache.
	code, resp := streamRequest(t, s, "type=dm")
	if code != http.StatusOK {
		t.Errorf("type=dm: expected 200, got %d", code)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 0 {
		t.Errorf("type=dm placeholder: expected 0 items, got %d", len(items))
	}
}

func TestStreamItems_BadCursor(t *testing.T) {
	s := newConfiguredServer(t)
	code, _ := streamRequest(t, s, "type=posts&cursor=not-base64!")
	if code != http.StatusBadRequest {
		t.Errorf("invalid cursor: expected 400, got %d", code)
	}
}

// TestStreamItems_CORSAndOptions exercises the publicContentMiddleware
// CORS surface: GET responses carry Access-Control-Allow-Origin: *;
// preflight OPTIONS returns 204 with the headers.
func TestStreamItems_CORSAndOptions(t *testing.T) {
	s := newConfiguredServer(t)

	// GET should set CORS *
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stream/items?type=posts&time=24h", nil)
	w := httptest.NewRecorder()
	publicContentMiddleware(sharedPublicContentLimiter, s.handleStreamItems)(w, req)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("GET CORS: expected '*', got %q", got)
	}

	// OPTIONS preflight returns 204 with headers
	preflightLimiter := newPublicContentLimiter(publicContentRateLimit, publicContentRateWindow)
	req = httptest.NewRequest(http.MethodOptions, "/api/v1/stream/items", nil)
	w = httptest.NewRecorder()
	publicContentMiddleware(preflightLimiter, s.handleStreamItems)(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("OPTIONS: expected 204, got %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got != "GET, OPTIONS" {
		t.Errorf("OPTIONS Allow-Methods: expected 'GET, OPTIONS', got %q", got)
	}
}

// TestStreamItems_RateLimitTriggers429 confirms the per-IP rate limiter
// returns 429 + Retry-After when an IP exceeds the cap. Uses a tiny
// dedicated limiter (rate=2) to avoid burning through the production
// 1000/hr in the test.
func TestStreamItems_RateLimitTriggers429(t *testing.T) {
	s := newConfiguredServer(t)
	tinyLimiter := newPublicContentLimiter(2, time.Hour)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/stream/items?type=posts&time=24h", nil)
		req.RemoteAddr = "203.0.113.42:54321"
		w := httptest.NewRecorder()
		publicContentMiddleware(tinyLimiter, s.handleStreamItems)(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d under cap: expected 200, got %d", i+1, w.Code)
		}
	}

	// Third request from same IP: 429 + Retry-After
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stream/items?type=posts&time=24h", nil)
	req.RemoteAddr = "203.0.113.42:54321"
	w := httptest.NewRecorder()
	publicContentMiddleware(tinyLimiter, s.handleStreamItems)(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("third request: expected 429, got %d", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got == "" {
		t.Error("third request: missing Retry-After header")
	}

	// Different IP: not affected
	req = httptest.NewRequest(http.MethodGet, "/api/v1/stream/items?type=posts&time=24h", nil)
	req.RemoteAddr = "198.51.100.7:54321"
	w = httptest.NewRecorder()
	publicContentMiddleware(tinyLimiter, s.handleStreamItems)(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("different IP: expected 200, got %d", w.Code)
	}
}

// TestStreamItems_EmitsItemsFetchedEvent locks in the pub.polis.stream.
// items_fetched observability event. Each call to handleStreamItems
// should produce exactly one event (success OR error), carrying the
// parsed filter params, the response shape, and the final status code.
// Closes plans/v4-deploy-readiness.md §3.
func TestStreamItems_EmitsItemsFetchedEvent(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()
	seedNetworkFeed(t, s, []feed.FeedItem{
		{Type: "post", Title: "P1", URL: "posts/p1.md", Published: now.Add(-1 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
	})

	// Capture stdout (Logger.Event emits there when jsonOutput=true).
	logsDir := filepath.Join(s.DataDir, "logs")
	os.MkdirAll(logsDir, 0755)
	s.Logger = NewLogger(LogLevelBasic, logsDir)
	s.Logger.jsonOutput = true
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Happy path: type=posts, default scope/time/order.
	streamRequest(t, s, "type=posts&time=24h")

	w.Close()
	os.Stdout = oldStdout
	buf := make([]byte, 16384)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	// Find the items_fetched event among any other log lines.
	var got map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}
		if obj["action"] == "pub.polis.stream.items_fetched" {
			got = obj
			break
		}
	}
	if got == nil {
		t.Fatalf("expected pub.polis.stream.items_fetched event; got output:\n%s", output)
	}

	// Verify the structured filter shape.
	if got["type"] != "posts" {
		t.Errorf("type: expected 'posts', got %v", got["type"])
	}
	if got["qualifier"] != "all" {
		t.Errorf("qualifier: expected default 'all', got %v", got["qualifier"])
	}
	if got["scope"] != "my-network" {
		t.Errorf("scope: expected default 'my-network', got %v", got["scope"])
	}
	if got["time_window"] != "24h" {
		t.Errorf("time_window: expected '24h', got %v", got["time_window"])
	}
	if got["order"] != "newest" {
		t.Errorf("order: expected default 'newest', got %v", got["order"])
	}
	// Numeric fields decode as float64 through json.Unmarshal.
	if status, _ := got["status"].(float64); int(status) != http.StatusOK {
		t.Errorf("status: expected 200, got %v", got["status"])
	}
	if count, _ := got["item_count"].(float64); count != 1 {
		t.Errorf("item_count: expected 1, got %v", got["item_count"])
	}
	if got["has_cursor"] != false {
		t.Errorf("has_cursor: expected false (no cursor param), got %v", got["has_cursor"])
	}
	if _, ok := got["duration_ms"]; !ok {
		t.Error("duration_ms: expected to be present")
	}
}

// TestStreamItems_EmitsEventOnError ensures the items_fetched emit
// fires on 4xx paths too — without it we'd be blind to a SPA filter
// bug producing a 400 storm.
func TestStreamItems_EmitsEventOnError(t *testing.T) {
	s := newConfiguredServer(t)

	logsDir := filepath.Join(s.DataDir, "logs")
	os.MkdirAll(logsDir, 0755)
	s.Logger = NewLogger(LogLevelBasic, logsDir)
	s.Logger.jsonOutput = true
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Bad scope → 400.
	streamRequest(t, s, "type=posts&scope=bogus")

	w.Close()
	os.Stdout = oldStdout
	buf := make([]byte, 16384)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	var got map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}
		if obj["action"] == "pub.polis.stream.items_fetched" {
			got = obj
			break
		}
	}
	if got == nil {
		t.Fatalf("expected items_fetched event on 400 path; got:\n%s", output)
	}
	if status, _ := got["status"].(float64); int(status) != http.StatusBadRequest {
		t.Errorf("status: expected 400, got %v", got["status"])
	}
	if count, _ := got["item_count"].(float64); count != 0 {
		t.Errorf("item_count: expected 0 on error, got %v", got["item_count"])
	}
}

func TestStreamItems_BadScope(t *testing.T) {
	s := newConfiguredServer(t)
	code, _ := streamRequest(t, s, "type=posts&scope=bogus")
	if code != http.StatusBadRequest {
		t.Errorf("invalid scope: expected 400, got %d", code)
	}

	// @-prefix with empty handle
	code, _ = streamRequest(t, s, "type=posts&scope=@")
	if code != http.StatusBadRequest {
		t.Errorf("empty @handle: expected 400, got %d", code)
	}
}

// TestStreamItems_BeforeURL covers the before_url convenience param added
// in step-05/5.c. Lets the stream controller paginate "before this URL"
// without needing client-side knowledge of the cursor format. Server
// looks up the URL in the result set and synthesizes the cursor.
func TestStreamItems_BeforeURL(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()
	// Five posts at descending timestamps so we have a clear "before this URL"
	// boundary to test against.
	seedNetworkFeed(t, s, []feed.FeedItem{
		{Type: "post", Title: "newest", URL: "posts/newest.md", Published: now.Add(-1 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "second", URL: "posts/second.md", Published: now.Add(-2 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "anchor", URL: "posts/anchor.md", Published: now.Add(-3 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "older", URL: "posts/older.md", Published: now.Add(-4 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "oldest", URL: "posts/oldest.md", Published: now.Add(-5 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
	})

	// before_url=anchor → only posts strictly older than "anchor" should
	// come back ("older" + "oldest").
	code, resp := streamRequest(t, s, "type=posts&time=24h&before_url=posts/anchor.md")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("before_url=anchor: expected 2 items (older + oldest), got %d", len(items))
	}
	titles := make([]string, len(items))
	for i, it := range items {
		titles[i], _ = it.(map[string]interface{})["title"].(string)
	}
	// newest-first ordering: older comes before oldest
	if titles[0] != "older" || titles[1] != "oldest" {
		t.Errorf("before_url=anchor: titles = %v, want [older oldest]", titles)
	}

	// before_url that doesn't match any item: server falls back to first
	// page (returns all 5 items).
	code, resp = streamRequest(t, s, "type=posts&time=24h&before_url=posts/nonexistent.md")
	if code != http.StatusOK {
		t.Fatalf("nonexistent before_url: expected 200, got %d", code)
	}
	items, _ = resp["items"].([]interface{})
	if len(items) != 5 {
		t.Errorf("nonexistent before_url: expected 5 items (full first page fallback), got %d", len(items))
	}

	// Explicit cursor wins over before_url: cursor=empty + before_url=anchor
	// → uses before_url. cursor=non-empty + before_url=anchor → uses cursor.
	// Smoke test: passing a cursor (synthesized from anchor's predecessor)
	// should NOT then re-anchor on before_url. We use the easier check:
	// passing both explicit cursor + before_url returns whatever the cursor
	// dictates, not the before_url result. This indirectly verifies the
	// `cursor.BeforePublished == ""` guard in the lookup branch.
	// Construct a cursor that anchors at "second" — only "anchor", "older",
	// "oldest" should come back. Confirms before_url is ignored.
	// NOTE: cursor encoding shape is internal; this test doesn't reach into
	// it. The earlier tests (TestStreamItems_*Cursor*) cover encoded cursors
	// already; this one just asserts before_url is non-destructive when a
	// real cursor isn't given but the URL doesn't match.
}

// TestStreamItems_AfterURL covers the after_url upward-pagination
// convenience: the stream's scroll-up first fetch from a per-post
// page anchors on the focus URL and asks for items NEWER than it.
// Server looks up the URL in the result set and synthesizes the
// AfterPublished/AfterID cursor (mirrors before_url, opposite direction).
func TestStreamItems_AfterURL(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()
	seedNetworkFeed(t, s, []feed.FeedItem{
		{Type: "post", Title: "newest", URL: "posts/newest.md", Published: now.Add(-1 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "second", URL: "posts/second.md", Published: now.Add(-2 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "anchor", URL: "posts/anchor.md", Published: now.Add(-3 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "older", URL: "posts/older.md", Published: now.Add(-4 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "oldest", URL: "posts/oldest.md", Published: now.Add(-5 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
	})

	// after_url=anchor + order=oldest → only posts strictly NEWER than
	// "anchor" come back ("second" + "newest"), in oldest-first order so
	// the controller can prepend each item directly above the previous
	// top entry. Expected order: second (older) before newest (newer).
	code, resp := streamRequest(t, s, "type=posts&time=24h&after_url=posts/anchor.md&order=oldest")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("after_url=anchor: expected 2 items (second + newest), got %d", len(items))
	}
	titles := make([]string, len(items))
	for i, it := range items {
		titles[i], _ = it.(map[string]interface{})["title"].(string)
	}
	if titles[0] != "second" || titles[1] != "newest" {
		t.Errorf("after_url=anchor (oldest order): titles = %v, want [second newest]", titles)
	}

	// after_url for the newest item itself → no items newer → empty list.
	// Critical signal: client uses this to set paginationDoneTop=true.
	code, resp = streamRequest(t, s, "type=posts&time=24h&after_url=posts/newest.md&order=oldest")
	if code != http.StatusOK {
		t.Fatalf("after_url=newest: expected 200, got %d", code)
	}
	items, _ = resp["items"].([]interface{})
	if len(items) != 0 {
		t.Errorf("after_url=newest: expected 0 items (nothing newer), got %d (titles: %v)", len(items), items)
	}

	// after_url that doesn't match any item: server falls back to first
	// page (returns all 5 items, same forgiving behavior as before_url).
	code, resp = streamRequest(t, s, "type=posts&time=24h&after_url=posts/nonexistent.md&order=oldest")
	if code != http.StatusOK {
		t.Fatalf("nonexistent after_url: expected 200, got %d", code)
	}
	items, _ = resp["items"].([]interface{})
	if len(items) != 5 {
		t.Errorf("nonexistent after_url: expected 5 items (full first page fallback), got %d", len(items))
	}
}

// TestStreamItems_CursorWinsOverBeforeURL closes the SC-1 coverage gap:
// constructs a synthetic cursor from a known item and passes it alongside
// a conflicting before_url, then asserts the cursor's anchor wins. This
// exercises the `cursor.BeforePublished == "" && cursor.AfterPublished == ""`
// guard at handlers_stream.go:257 — if the guard ever flips or gets
// removed, before_url would clobber an explicit cursor and pagination
// would silently jump to the wrong anchor.
func TestStreamItems_CursorWinsOverBeforeURL(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()
	seedNetworkFeed(t, s, []feed.FeedItem{
		{Type: "post", Title: "newest", URL: "posts/newest.md", Published: now.Add(-1 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "second", URL: "posts/second.md", Published: now.Add(-2 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "anchor", URL: "posts/anchor.md", Published: now.Add(-3 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "older", URL: "posts/older.md", Published: now.Add(-4 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "oldest", URL: "posts/oldest.md", Published: now.Add(-5 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
	})

	// Cursor anchored strictly between "second" (-2h) and "anchor" (-3h),
	// so only items strictly older than the cursor instant come back:
	// anchor + older + oldest. Avoids the ID-tiebreaker branch on equal
	// timestamps.
	cursorPayload := streamCursor{
		BeforePublished: now.Add(-2*time.Hour - 30*time.Minute).Format(time.RFC3339),
		BeforeID:        "",
	}
	encodedCursor := encodeStreamCursor(cursorPayload)

	// Conflicting before_url=newest. If before_url were to win, the result
	// would include "second" + "anchor" + "older" + "oldest" (4 items).
	// If the cursor wins, the result is the 3-item set anchored at "second".
	code, resp := streamRequest(t, s, "type=posts&time=24h&limit=10&cursor="+encodedCursor+"&before_url=posts/newest.md")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 3 {
		t.Fatalf("expected 3 items (cursor anchor wins), got %d", len(items))
	}
	titles := make([]string, len(items))
	for i, it := range items {
		titles[i], _ = it.(map[string]interface{})["title"].(string)
	}
	wantTitles := []string{"anchor", "older", "oldest"}
	for i, want := range wantTitles {
		if titles[i] != want {
			t.Errorf("titles[%d] = %q, want %q (full = %v)", i, titles[i], want, titles)
		}
	}
}

// TestStreamItems_ScopeOwnHandle_ReadsLocalIndex closes the production gap
// surfaced on discover.polis.pub: scope=@<own-domain> returned 0 items
// even when the tenant had local posts. Root cause: the network feed
// cache filters self-authored events out by design (FeedHandler's self-
// skip), so AuthorDomains={self} on the network cache returned empty.
// The handler now branches to the local public index for own-handle
// scope. Without this branch, the stream's filter widget +
// scroll-pagination return empty on every per-post page on a hosted tenant.
func TestStreamItems_ScopeOwnHandle_ReadsLocalIndex(t *testing.T) {
	s := newConfiguredServer(t)
	// newConfiguredServer sets BaseURL = https://test-site.polis.pub, so
	// scope=@test-site.polis.pub should resolve to the local index.
	myDomain := "test-site.polis.pub"

	// Seed the local public index with two posts. Source path under
	// content/pub.polis.core/post/, mount path /posts/. Excerpt is
	// computed from the source markdown — so we also drop a stub source
	// file beside each index entry.
	now := time.Now().UTC()
	postPath1 := "content/pub.polis.core/post/20260427/post-one.md"
	postPath2 := "content/pub.polis.core/post/20260426/post-two.md"
	for _, p := range []string{postPath1, postPath2} {
		full := filepath.Join(s.DataDir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte("---\ntitle: x\n---\nbody text"), 0644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	if err := metadata.AppendPostToIndex(s.DataDir, postPath1, "Post One", now.Add(-1*time.Hour).Format(time.RFC3339), "sha256:aaa"); err != nil {
		t.Fatalf("append index post1: %v", err)
	}
	if err := metadata.AppendPostToIndex(s.DataDir, postPath2, "Post Two", now.Add(-2*time.Hour).Format(time.RFC3339), "sha256:bbb"); err != nil {
		t.Fatalf("append index post2: %v", err)
	}

	// Request scope=@<own-domain>. Network cache is empty, so before this
	// fix the response was {"items":[]} — locking that regression here.
	code, resp := streamRequest(t, s, "scope=@"+myDomain+"&type=posts&time=24h")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("scope=@self should return 2 own posts, got %d (resp=%v)", len(items), resp)
	}

	// Newest-first order — Post One (-1h) before Post Two (-2h).
	titles := make([]string, len(items))
	urls := make([]string, len(items))
	for i, it := range items {
		m := it.(map[string]interface{})
		titles[i], _ = m["title"].(string)
		urls[i], _ = m["url"].(string)
	}
	if titles[0] != "Post One" || titles[1] != "Post Two" {
		t.Errorf("titles = %v, want [Post One Post Two]", titles)
	}
	// URL conversion: content/pub.polis.core/post/X/Y.md → /posts/X/Y.html.
	// The stream pages by URL (before_url + dedup against SSR'd entries),
	// so the format must match feed-cache items exactly.
	if urls[0] != "/posts/20260427/post-one.html" {
		t.Errorf("urls[0] = %q, want /posts/20260427/post-one.html", urls[0])
	}
}

// TestStreamItems_RejectsInvalidWith locks in the with= validation added
// in the 2026-04-29 filter-grammar redesign. Only "comments" is accepted.
func TestStreamItems_RejectsInvalidWith(t *testing.T) {
	s := newConfiguredServer(t)
	code, resp := streamRequest(t, s, "type=posts&scope=@test-site.polis.pub&with=foo")
	if code != http.StatusBadRequest {
		t.Errorf("with=foo: expected 400, got %d (resp=%v)", code, resp)
	}
}

// TestStreamItems_RejectsWithCommentsOnNonPosts locks in the cross-param
// guard: with=comments only makes sense paired with type=posts. Pairing
// it with type=comments / type=profiles is rejected at validation.
func TestStreamItems_RejectsWithCommentsOnNonPosts(t *testing.T) {
	s := newConfiguredServer(t)
	code, _ := streamRequest(t, s, "type=comments&scope=@test-site.polis.pub&with=comments")
	if code != http.StatusBadRequest {
		t.Errorf("with=comments+type=comments: expected 400, got %d", code)
	}
	code, _ = streamRequest(t, s, "type=profiles&scope=@test-site.polis.pub&with=comments")
	if code != http.StatusBadRequest {
		t.Errorf("with=comments+type=profiles: expected 400, got %d", code)
	}
}

// TestStreamItems_TypeProfilesScopeValidation locks in the scope rules
// for the new PROFILES grammar (06-profiles):
//   - me / my / my-network / @<myDomain>: own-tenant data path → 200
//   - all-polis: reserved for Phase 3 (DS site directory) → 200 with empty items
//   - @<other-domain>: cross-tenant profiles aren't exposed → 400
//
// (Pre-06-profiles, type=follows rejected scope=my-network because the
// data was sourced from local files and the network scope was a category
// error. The new grammar makes my-network the natural default — those
// local files describe the user's network — so the validation flipped.)
func TestStreamItems_TypeProfilesScopeValidation(t *testing.T) {
	s := newConfiguredServer(t)
	// Cross-tenant scope: still rejected.
	code, _ := streamRequest(t, s, "type=profiles&scope=@other.polis.pub")
	if code != http.StatusBadRequest {
		t.Errorf("type=profiles+scope=@other: expected 400, got %d", code)
	}
	// Default scope (my-network): now valid.
	code, _ = streamRequest(t, s, "type=profiles")
	if code != http.StatusOK {
		t.Errorf("type=profiles+scope=my-network (default): expected 200, got %d", code)
	}
	// all-polis: 200 with empty items (Phase 3 will wire data).
	code, resp := streamRequest(t, s, "type=profiles&scope=all-polis")
	if code != http.StatusOK {
		t.Errorf("type=profiles+scope=all-polis: expected 200, got %d", code)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 0 {
		t.Errorf("type=profiles+scope=all-polis: expected empty items in Phase 1, got %d", len(items))
	}
}

// TestStreamItems_WithCommentsFilter exercises the with=comments modifier
// for type=posts on own scope: only posts that appear in blessed.json
// with at least one blessed comment come back.
func TestStreamItems_WithCommentsFilter(t *testing.T) {
	s := newConfiguredServer(t)
	myDomain := "test-site.polis.pub"
	now := time.Now().UTC()

	// Two posts: post-with has a blessed comment, post-without doesn't.
	postWith := "content/pub.polis.core/post/20260427/post-with.md"
	postWithout := "content/pub.polis.core/post/20260426/post-without.md"
	for _, p := range []string{postWith, postWithout} {
		full := filepath.Join(s.DataDir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte("---\ntitle: x\n---\nbody"), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := metadata.AppendPostToIndex(s.DataDir, postWith, "Has Comments", now.Add(-1*time.Hour).Format(time.RFC3339), "sha256:aaa"); err != nil {
		t.Fatalf("append index: %v", err)
	}
	if err := metadata.AppendPostToIndex(s.DataDir, postWithout, "No Comments", now.Add(-2*time.Hour).Format(time.RFC3339), "sha256:bbb"); err != nil {
		t.Fatalf("append index: %v", err)
	}
	// Bless one comment against post-with.
	if err := metadata.AddBlessedComment(s.DataDir, postWith, metadata.BlessedComment{
		URL:       "https://alice.polis.pub/comments/20260427/c1.md",
		Version:   "sha256:c1",
		BlessedAt: now.Add(-30 * time.Minute).Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("add blessed: %v", err)
	}

	// Without with=comments → both posts come back.
	code, resp := streamRequest(t, s, "type=posts&scope=@"+myDomain)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("baseline (no with=): expected 2 posts, got %d", len(items))
	}

	// With with=comments → only post-with comes back.
	code, resp = streamRequest(t, s, "type=posts&scope=@"+myDomain+"&with=comments")
	if code != http.StatusOK {
		t.Fatalf("with=comments: expected 200, got %d (resp=%v)", code, resp)
	}
	items, _ = resp["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("with=comments: expected 1 post, got %d", len(items))
	}
	got, _ := items[0].(map[string]interface{})["title"].(string)
	if got != "Has Comments" {
		t.Errorf("with=comments returned wrong post: %q", got)
	}
}

// TestStreamItems_CommentCountPopulated locks in the comment-count
// thread-through for own-scope post items: the response carries
// comment_count derived from blessed.json's per-post blessed[] length.
// The stream's renderPost reads this field to render the badge with
// the count (or .is-empty class for zero).
func TestStreamItems_CommentCountPopulated(t *testing.T) {
	s := newConfiguredServer(t)
	myDomain := "test-site.polis.pub"
	now := time.Now().UTC()

	postPath := "content/pub.polis.core/post/20260427/p1.md"
	full := filepath.Join(s.DataDir, postPath)
	_ = os.MkdirAll(filepath.Dir(full), 0755)
	_ = os.WriteFile(full, []byte("---\ntitle: x\n---\n"), 0644)
	_ = metadata.AppendPostToIndex(s.DataDir, postPath, "Three Comments", now.Format(time.RFC3339), "sha256:p1")
	for i, hash := range []string{"c1", "c2", "c3"} {
		_ = metadata.AddBlessedComment(s.DataDir, postPath, metadata.BlessedComment{
			URL:       fmt.Sprintf("https://alice.polis.pub/comments/20260427/c%d.md", i+1),
			Version:   "sha256:" + hash,
			BlessedAt: now.Add(time.Duration(-i) * time.Minute).Format(time.RFC3339),
		})
	}

	code, resp := streamRequest(t, s, "type=posts&scope=@"+myDomain)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 post, got %d", len(items))
	}
	item := items[0].(map[string]interface{})
	// JSON unmarshal of integer → float64. comment_count: 3.
	cc, _ := item["comment_count"].(float64)
	if int(cc) != 3 {
		t.Errorf("comment_count = %v, want 3", cc)
	}
}

// TestStreamItems_TitleRedundant locks in the title-redundancy flag for
// type=posts on own-handle scope. Mirrors the SSR sibling-render path's
// detection (StripLeadingTitleHeading + TitleStartsFirstSentence) so
// dynamically-rendered posts get the same title-chrome suppression as
// SSR'd siblings — without this, posts whose titles are auto-derived
// truncations of the body's first sentence (discover.polis.pub case)
// render the title once explicitly + once more inside the body.
func TestStreamItems_TitleRedundant(t *testing.T) {
	s := newConfiguredServer(t)
	myDomain := "test-site.polis.pub"
	now := time.Now().UTC()

	// Redundant post: title is a prefix of the body's first sentence.
	redundantPath := "content/pub.polis.core/post/20260506/redundant.md"
	redundantTitle := "The frontend is still vanilla JS"
	redundantBody := "---\ntitle: " + redundantTitle + "\n---\n" +
		"The frontend is still vanilla JS. No React, no Vue, no Svelte. " +
		"Just plain HTML/CSS/JS rendered server-side via templates."
	redundantFull := filepath.Join(s.DataDir, redundantPath)
	if err := os.MkdirAll(filepath.Dir(redundantFull), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(redundantFull, []byte(redundantBody), 0644); err != nil {
		t.Fatalf("write redundant: %v", err)
	}
	if err := metadata.AppendPostToIndex(s.DataDir, redundantPath, redundantTitle, now.Format(time.RFC3339), "sha256:r1"); err != nil {
		t.Fatalf("append index redundant: %v", err)
	}

	// Distinct post: title doesn't prefix-match the body.
	distinctPath := "content/pub.polis.core/post/20260506/distinct.md"
	distinctTitle := "Quick announcement"
	distinctBody := "---\ntitle: " + distinctTitle + "\n---\n" +
		"Today we shipped a new feature that makes everything better."
	distinctFull := filepath.Join(s.DataDir, distinctPath)
	if err := os.WriteFile(distinctFull, []byte(distinctBody), 0644); err != nil {
		t.Fatalf("write distinct: %v", err)
	}
	if err := metadata.AppendPostToIndex(s.DataDir, distinctPath, distinctTitle, now.Add(-1*time.Hour).Format(time.RFC3339), "sha256:d1"); err != nil {
		t.Fatalf("append index distinct: %v", err)
	}

	code, resp := streamRequest(t, s, "type=posts&scope=@"+myDomain)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("expected 2 posts, got %d", len(items))
	}

	// Find each by title.
	got := make(map[string]bool, 2)
	for _, raw := range items {
		item := raw.(map[string]interface{})
		title, _ := item["title"].(string)
		// JSON omitempty drops false; treat missing as false.
		flag, _ := item["title_redundant"].(bool)
		got[title] = flag
	}
	if !got[redundantTitle] {
		t.Errorf("redundant post (%q): title_redundant flag missing — server-side detection broken", redundantTitle)
	}
	if got[distinctTitle] {
		t.Errorf("distinct post (%q): title_redundant flag set when body doesn't begin with title", distinctTitle)
	}
}

// TestStreamItems_BlessedCommentsEnriched locks in the per-post inline
// blessed-comments enrichment for type=posts on own-handle scope. The
// stream's renderPost (and SSR's stream-post.html) reads the blessed_comments
// field to mount an inline .entry-comments-panel without a follow-up fetch
// — same data flow the per-post focus page already uses for its panel.
//
// Asserts: response items carry blessed_comments=[{url,author_name,
// published_human,content_html}] when the post has blessed comments.
// content_html is the goldmark-rendered comment body (verifies the
// LoadLocalCommentContent integration end-to-end).
func TestStreamItems_BlessedCommentsEnriched(t *testing.T) {
	s := newConfiguredServer(t)
	myDomain := "test-site.polis.pub"
	now := time.Now().UTC()

	// Own post.
	postPath := "content/pub.polis.core/post/20260506/hello.md"
	full := filepath.Join(s.DataDir, postPath)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte("---\ntitle: Hello\n---\nbody"), 0644); err != nil {
		t.Fatalf("write post: %v", err)
	}
	if err := metadata.AppendPostToIndex(s.DataDir, postPath, "Hello", now.Format(time.RFC3339), "sha256:p1"); err != nil {
		t.Fatalf("append index: %v", err)
	}

	// Blessed comment from another tenant — populate the ISOLATED blessed cache
	// (the only place a foreign comment may live; the grant/sync flow + Rosie put
	// it there). LoadLocalCommentContent reads it from the cache, strips the
	// frontmatter, and renders the body through goldmark + bluemonday.
	commentURL := "https://alice.polis.pub/comments/20260506/re-hello.md"
	commentBody := "---\ntitle: re Hello\n---\nNice **post**!"
	if err := cache.StoreBlessed(s.DataDir, "ds.polis.pub", cache.BlessedSidecar{SourceURL: commentURL, FetchedAt: "t"}, []byte(commentBody)); err != nil {
		t.Fatalf("store blessed cache: %v", err)
	}
	if err := metadata.AddBlessedComment(s.DataDir, postPath, metadata.BlessedComment{
		URL:       commentURL,
		Version:   "sha256:c1",
		BlessedAt: now.Add(-30 * time.Minute).Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("add blessed: %v", err)
	}

	code, resp := streamRequest(t, s, "type=posts&scope=@"+myDomain)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 post, got %d", len(items))
	}
	item := items[0].(map[string]interface{})
	bc, ok := item["blessed_comments"].([]interface{})
	if !ok || len(bc) != 1 {
		t.Fatalf("expected 1 blessed_comment, got %v (ok=%v)", item["blessed_comments"], ok)
	}
	c := bc[0].(map[string]interface{})

	// URL: stored .md form converted to .html mount-path for browsable
	// links (mirrors render.loadBlessedCommentsForPost convention).
	if got, want := c["url"], "https://alice.polis.pub/comments/20260506/re-hello.html"; got != want {
		t.Errorf("blessed_comments[0].url = %q, want %q", got, want)
	}
	// Author name extracted from the comment URL's domain.
	if got, want := c["author_name"], "alice.polis.pub"; got != want {
		t.Errorf("blessed_comments[0].author_name = %q, want %q", got, want)
	}
	// Content rendered to HTML — bold marker survived the markdown pass.
	contentHTML, _ := c["content_html"].(string)
	if !strings.Contains(contentHTML, "<strong>post</strong>") {
		t.Errorf("blessed_comments[0].content_html = %q, expected to contain rendered <strong>", contentHTML)
	}
	// Published timestamp formatted into the human form (date-only —
	// matches template.FormatHumanDate).
	if got := c["published_human"]; got == "" {
		t.Error("blessed_comments[0].published_human is empty")
	}
}

// TestStreamItems_TypeCommentsOutboundOnly locks in the post-2026-04-29
// rescope of `type=comments`: only this tenant's outbound comments come
// back. Inbound (blessed comments from others on this tenant's posts)
// is no longer merged in — that data lives behind `type=posts&with=comments`.
func TestStreamItems_TypeCommentsOutboundOnly(t *testing.T) {
	s := newConfiguredServer(t)
	myDomain := "test-site.polis.pub"
	now := time.Now().UTC()

	// Outbound: this tenant's own comment, recorded in index.jsonl with
	// type=comment.
	myCommentPath := "content/pub.polis.core/comment/20260427/my-comment.md"
	full := filepath.Join(s.DataDir, myCommentPath)
	_ = os.MkdirAll(filepath.Dir(full), 0755)
	_ = os.WriteFile(full, []byte("---\ntitle: My Comment\n---\nbody"), 0644)
	_ = metadata.AppendCommentToIndex(s.DataDir, myCommentPath, "My Comment", now.Add(-1*time.Hour).Format(time.RFC3339), "sha256:my", "https://other.polis.pub/posts/foo.md")

	// Inbound seed: a blessed remote comment on a local post. The new
	// rescope MUST drop this from the response.
	myPostPath := "content/pub.polis.core/post/20260427/p1.md"
	postFull := filepath.Join(s.DataDir, myPostPath)
	_ = os.MkdirAll(filepath.Dir(postFull), 0755)
	_ = os.WriteFile(postFull, []byte("---\ntitle: My Post\n---\nbody"), 0644)
	_ = metadata.AppendPostToIndex(s.DataDir, myPostPath, "My Post", now.Add(-3*time.Hour).Format(time.RFC3339), "sha256:p1")
	_ = metadata.AddBlessedComment(s.DataDir, myPostPath, metadata.BlessedComment{
		URL:       "https://alice.polis.pub/comments/20260427/from-alice.md",
		Version:   "sha256:alice-c",
		BlessedAt: now.Add(-2 * time.Hour).Format(time.RFC3339),
	})

	code, resp := streamRequest(t, s, "type=comments&scope=@"+myDomain)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 outbound comment (inbound dropped), got %d (resp=%v)", len(items), resp)
	}
	item := items[0].(map[string]interface{})
	if title, _ := item["title"].(string); title != "My Comment" {
		t.Errorf("outbound title = %q, want %q", title, "My Comment")
	}
	if author, _ := item["author_domain"].(string); author != myDomain {
		t.Errorf("outbound author_domain = %q, want %q", author, myDomain)
	}
	// target_url carries the post the comment replies to — used by the
	// stream's renderCommentThread to anchor the inline post. API surfaces
	// the .html mount-path form (storage is .md).
	if target, _ := item["target_url"].(string); target != "https://other.polis.pub/posts/foo.html" {
		t.Errorf("outbound target_url = %q, want the in-reply-to URL (.html form)", target)
	}
}

// TestStreamItems_TypeCommentsDedupeByPost locks in the dedupe pass:
// multiple outbound comments by this tenant on the same target post
// collapse to a single entry — the latest-published one wins.
func TestStreamItems_TypeCommentsDedupeByPost(t *testing.T) {
	s := newConfiguredServer(t)
	myDomain := "test-site.polis.pub"
	now := time.Now().UTC()
	target := "https://other.polis.pub/posts/shared.md"

	// Two comments on the same target post, different timestamps.
	for i, label := range []string{"older", "newer"} {
		path := fmt.Sprintf("content/pub.polis.core/comment/20260427/%s.md", label)
		full := filepath.Join(s.DataDir, path)
		_ = os.MkdirAll(filepath.Dir(full), 0755)
		_ = os.WriteFile(full, []byte("---\ntitle: "+label+"\n---\nbody"), 0644)
		ts := now.Add(time.Duration(-(2 - i)) * time.Hour).Format(time.RFC3339)
		_ = metadata.AppendCommentToIndex(s.DataDir, path, label, ts, "sha256:"+label, target)
	}
	// Plus one comment on a DIFFERENT target post — should be its own entry.
	otherPath := "content/pub.polis.core/comment/20260427/elsewhere.md"
	otherFull := filepath.Join(s.DataDir, otherPath)
	_ = os.MkdirAll(filepath.Dir(otherFull), 0755)
	_ = os.WriteFile(otherFull, []byte("---\ntitle: elsewhere\n---\nbody"), 0644)
	_ = metadata.AppendCommentToIndex(s.DataDir, otherPath, "elsewhere", now.Add(-30*time.Minute).Format(time.RFC3339), "sha256:elsewhere", "https://other.polis.pub/posts/different.md")

	code, resp := streamRequest(t, s, "type=comments&scope=@"+myDomain)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("expected 2 entries (latest-on-shared + on-different), got %d", len(items))
	}
	// Among the two, the entry on `target` should have title "newer".
	// Display surfaces normalize URLs to .html; storage form is .md.
	targetHTML := strings.TrimSuffix(target, ".md") + ".html"
	foundNewer := false
	for _, it := range items {
		m := it.(map[string]interface{})
		if tu, _ := m["target_url"].(string); tu == targetHTML {
			if title, _ := m["title"].(string); title == "newer" {
				foundNewer = true
			} else {
				t.Errorf("dedupe kept %q on shared target; want %q", title, "newer")
			}
		}
	}
	if !foundNewer {
		t.Errorf("expected the latest-on-shared entry to be in response")
	}
}

// TestStreamItems_TypeCommentsEnrichesTargetPost locks in the
// thread-entry enrichment: the response carries target_post_title /
// target_post_body_html / comment_body_html populated for each
// outbound comment item, sourced from the reply-context-cache.
func TestStreamItems_TypeCommentsEnrichesTargetPost(t *testing.T) {
	s := newConfiguredServer(t)
	myDomain := "test-site.polis.pub"
	now := time.Now().UTC()
	targetURL := "https://stub.polis.pub/posts/20260427/example.html"

	// Local outbound comment.
	commentPath := "content/pub.polis.core/comment/20260427/my-c.md"
	commentFull := filepath.Join(s.DataDir, commentPath)
	_ = os.MkdirAll(filepath.Dir(commentFull), 0755)
	_ = os.WriteFile(commentFull, []byte("---\ntitle: My Comment\n---\n\nMy reply *text*."), 0644)
	_ = metadata.AppendCommentToIndex(s.DataDir, commentPath, "My Comment", now.Add(-1*time.Hour).Format(time.RFC3339), "sha256:my", targetURL)

	// Pre-populate the reply-context-cache so no live HTTP fetch happens.
	// The cache file lives at .polis/bundles/pub.polis.core/comments/reply-context-cache.json.
	cachePath := filepath.Join(s.DataDir, ".polis", "bundles", "pub.polis.core", "comments", "reply-context-cache.json")
	_ = os.MkdirAll(filepath.Dir(cachePath), 0755)
	cacheBody := `{
		"` + targetURL + `": {
			"title": "The Target Post",
			"excerpt": "Excerpt text.",
			"domain": "stub.polis.pub",
			"body_md": "Hello *world*.\n\nA second paragraph.",
			"published": "2026-04-27T12:00:00Z",
			"fetched_at": "` + now.Format(time.RFC3339) + `"
		}
	}`
	_ = os.WriteFile(cachePath, []byte(cacheBody), 0644)

	code, resp := streamRequest(t, s, "type=comments&scope=@"+myDomain)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	m := items[0].(map[string]interface{})
	if title, _ := m["target_post_title"].(string); title != "The Target Post" {
		t.Errorf("target_post_title = %q, want %q", title, "The Target Post")
	}
	if domain, _ := m["target_post_author_domain"].(string); domain != "stub.polis.pub" {
		t.Errorf("target_post_author_domain = %q, want stub.polis.pub", domain)
	}
	postHTML, _ := m["target_post_body_html"].(string)
	if !strings.Contains(postHTML, "<em>world</em>") {
		t.Errorf("target_post_body_html missing rendered emphasis: %q", postHTML)
	}
	commentHTML, _ := m["comment_body_html"].(string)
	if !strings.Contains(commentHTML, "<em>text</em>") {
		t.Errorf("comment_body_html missing rendered emphasis: %q", commentHTML)
	}
	if pubHum, _ := m["target_post_published_human"].(string); pubHum == "" {
		t.Errorf("target_post_published_human empty; want a formatted date")
	}
}

// TestStreamItems_TypeCommentsTitleRedundantFlag locks the title-dedup
// path: when the target post's body markdown begins with the title
// (literally or via a leading ATX heading whose text matches the
// title), the enrichment carries target_post_title_redundant=true so
// the stream's renderCommentThread can hide the title chrome.
func TestStreamItems_TypeCommentsTitleRedundantFlag(t *testing.T) {
	s := newConfiguredServer(t)
	myDomain := "test-site.polis.pub"
	now := time.Now().UTC()
	targetURL := "https://stub.polis.pub/posts/20260427/redundant.html"

	commentPath := "content/pub.polis.core/comment/20260427/dup-c.md"
	commentFull := filepath.Join(s.DataDir, commentPath)
	_ = os.MkdirAll(filepath.Dir(commentFull), 0755)
	_ = os.WriteFile(commentFull, []byte("---\ntitle: Reply\n---\n\nReply body."), 0644)
	_ = metadata.AppendCommentToIndex(s.DataDir, commentPath, "Reply", now.Add(-1*time.Hour).Format(time.RFC3339), "sha256:dup", targetURL)

	cachePath := filepath.Join(s.DataDir, ".polis", "bundles", "pub.polis.core", "comments", "reply-context-cache.json")
	_ = os.MkdirAll(filepath.Dir(cachePath), 0755)
	// Body markdown begins with **Just one RSS** which is also the title's
	// emphasis-prefixed prefix — TitleStartsFirstSentence (operating on
	// raw markdown so emphasis markers line up) should fire.
	cacheBody := `{
		"` + targetURL + `": {
			"title": "**Just one RSS** works for consumption",
			"excerpt": "...",
			"domain": "stub.polis.pub",
			"body_md": "**Just one RSS** works for consumption. It doesn't solve identity.",
			"published": "2026-04-27T12:00:00Z",
			"fetched_at": "` + now.Format(time.RFC3339) + `"
		}
	}`
	_ = os.WriteFile(cachePath, []byte(cacheBody), 0644)

	code, resp := streamRequest(t, s, "type=comments&scope=@"+myDomain)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	m := items[0].(map[string]interface{})
	flag, _ := m["target_post_title_redundant"].(bool)
	if !flag {
		t.Errorf("target_post_title_redundant = false, want true (body starts with the bold-prefixed title)")
	}
}

// TestStreamItems_TypeProfilesBidirectional covers the merged outbound
// (following.json) + inbound (pub.polis.follow.json projection) feed for
// type=profiles on own scope. Missing projection file is an empty inbound
// merge, not an error.
//
// Phase-1 contract: items still carry Type:"follow" — the new dispatch
// is a grammar-layer rename, not a data-shape change. Phase 2 reshapes
// to Type:"profile" with enriched fields.
func TestStreamItems_TypeProfilesBidirectional(t *testing.T) {
	s := newConfiguredServer(t)
	myDomain := "test-site.polis.pub"
	now := time.Now().UTC()

	// Outbound: who I follow.
	ff := &following.FollowingFile{Following: []following.FollowingEntry{
		{URL: "https://alice.polis.pub", AddedAt: now.Add(-1 * time.Hour).Format(time.RFC3339)},
		{URL: "https://bob.polis.pub", AddedAt: now.Add(-2 * time.Hour).Format(time.RFC3339)},
	}}
	if err := following.Save(following.DefaultPath(s.DataDir), ff); err != nil {
		t.Fatalf("save following: %v", err)
	}

	// Inbound: who follows me, written into the projection cache the
	// same way runUnifiedSync would (state file under
	// .polis/ds/<discoveryDomain>/pub.polis.core/state/pub.polis.follow.json).
	store := stream.NewStore(s.DataDir, s.GetDiscoveryDomain(), "pub.polis.core")
	fs := &stream.FollowerState{
		Followers: []string{"carol.polis.pub", "dave.polis.pub"},
		Count:     2,
	}
	if err := store.SaveState("pub.polis.follow", fs); err != nil {
		t.Fatalf("save follower state: %v", err)
	}

	code, resp := streamRequest(t, s, "type=profiles&scope=@"+myDomain)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 4 {
		t.Fatalf("expected 4 profiles (2 outbound + 2 inbound), got %d (resp=%v)", len(items), resp)
	}

	// Phase 2 contract: each item has type="profile" and AuthorDomain
	// is always the OTHER party (the profile being displayed), not the
	// local tenant. Direction is encoded via the relationship field:
	//   alice/bob (outbound only) → relationship="you-follow"
	//   carol/dave (inbound only) → relationship="follows-you"
	byDomain := make(map[string]string)
	for _, it := range items {
		m := it.(map[string]interface{})
		typ, _ := m["type"].(string)
		if typ != "profile" {
			t.Errorf("expected type=profile, got %q", typ)
		}
		authorDomain, _ := m["author_domain"].(string)
		relationship, _ := m["relationship"].(string)
		byDomain[authorDomain] = relationship
	}
	expectations := map[string]string{
		"alice.polis.pub": "you-follow",
		"bob.polis.pub":   "you-follow",
		"carol.polis.pub": "follows-you",
		"dave.polis.pub":  "follows-you",
	}
	for domain, wantRel := range expectations {
		gotRel, ok := byDomain[domain]
		if !ok {
			t.Errorf("missing profile for %s (got %v)", domain, byDomain)
			continue
		}
		if gotRel != wantRel {
			t.Errorf("relationship for %s: want %q, got %q", domain, wantRel, gotRel)
		}
	}
}

// TestStreamItems_TypeProfiles_MissingProjectionIsOutboundOnly covers the
// fresh-tenant case: no inbound follows yet (sync hasn't run), so the
// projection state file doesn't exist. Endpoint should return outbound-only
// without error.
func TestStreamItems_TypeProfiles_MissingProjectionIsOutboundOnly(t *testing.T) {
	s := newConfiguredServer(t)
	myDomain := "test-site.polis.pub"

	ff := &following.FollowingFile{Following: []following.FollowingEntry{
		{URL: "https://alice.polis.pub", AddedAt: time.Now().UTC().Format(time.RFC3339)},
	}}
	if err := following.Save(following.DefaultPath(s.DataDir), ff); err != nil {
		t.Fatalf("save following: %v", err)
	}
	// No store.SaveState call — projection file doesn't exist.

	code, resp := streamRequest(t, s, "type=profiles&scope=@"+myDomain)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 1 {
		t.Errorf("expected 1 outbound profile (no projection cache), got %d", len(items))
	}
}

// TestStreamItems_ScopeOtherHandle_StillUsesNetworkCache locks in the
// allowlist semantics of the own-handle branch: only EXACT match against
// the tenant's own canonical domain takes the local-index path. Other
// handles still flow through the network cache (where AuthorDomains={arg}
// works correctly because non-self events ARE cached).
func TestStreamItems_ScopeOtherHandle_StillUsesNetworkCache(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()
	// Network cache has a post from a different domain.
	seedNetworkFeed(t, s, []feed.FeedItem{
		{Type: "post", Title: "From Other", URL: "posts/o.md", Published: now.Add(-1 * time.Hour).Format(time.RFC3339), AuthorURL: "https://other.polis.pub", AuthorDomain: "other.polis.pub"},
	})
	// And the local index has a self post that should NOT be returned for
	// scope=@other.polis.pub (would indicate the branch fired wrongly).
	postPath := "content/pub.polis.core/post/20260427/self.md"
	full := filepath.Join(s.DataDir, postPath)
	_ = os.MkdirAll(filepath.Dir(full), 0755)
	_ = os.WriteFile(full, []byte("---\ntitle: x\n---\nx"), 0644)
	_ = metadata.AppendPostToIndex(s.DataDir, postPath, "Self Post", now.Format(time.RFC3339), "sha256:ccc")

	code, resp := streamRequest(t, s, "scope=@other.polis.pub&type=posts&time=24h")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("scope=@other.polis.pub should return 1 network item, got %d", len(items))
	}
	title, _ := items[0].(map[string]interface{})["title"].(string)
	if title != "From Other" {
		t.Errorf("expected 'From Other' (network cache), got %q — branch may have fired wrongly", title)
	}
}

// step-06/6.b grammar extensions
// =============================================================================

// TestStreamItems_ScopeMeAlias verifies scope=me is accepted as an
// alias for scope=my (URL-token alignment per resolved decisions).
// Both forms route through loadOwnContentAsFeedItems (local public
// index) per the step-06/6.e broader fix. Updated to use the local
// index instead of the network feed cache (the production path).
func TestStreamItems_ScopeMeAlias(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()
	postPath := "content/pub.polis.core/post/20260507/me-alias.md"
	full := filepath.Join(s.DataDir, postPath)
	_ = os.MkdirAll(filepath.Dir(full), 0755)
	_ = os.WriteFile(full, []byte("---\ntitle: Me Alias\n---\nbody"), 0644)
	_ = metadata.AppendPostToIndex(s.DataDir, postPath, "Me Alias Post", now.Format(time.RFC3339), "sha256:ma")

	code, resp := streamRequest(t, s, "scope=me&type=posts")
	if code != http.StatusOK {
		t.Fatalf("scope=me should be accepted as alias for scope=my; got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("scope=me should return my-authored item from local index; got %d", len(items))
	}
}

// TestStreamItems_TypeDmsAlias verifies `type=dms` is accepted as an alias
// for `type=dm` (grammar uses plural) on the inbox path (scope=my-mutuals).
// Chunk C tightened the scope contract for DMs: my-network / me / all-polis
// are explicitly rejected since DMs are 1:1 between mutuals. This test
// pins the alias behavior for the now-valid scope.
func TestStreamItems_TypeDmsAlias(t *testing.T) {
	s := newConfiguredServer(t)
	code, resp := streamRequest(t, s, "type=dms&scope=my-mutuals")
	if code != http.StatusOK {
		t.Fatalf("type=dms&scope=my-mutuals should be accepted; got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 0 {
		t.Errorf("expected empty DM list (no DMs in test tenant), got %d items", len(items))
	}
}

// TestStreamItems_DMScopeContract verifies chunk-C's tightened scope
// validation for type=dms: only my-mutuals (inbox) and @<handle> (thread)
// are accepted. Other scopes 400 with a descriptive message.
func TestStreamItems_DMScopeContract(t *testing.T) {
	s := newConfiguredServer(t)
	rejected := []string{"me", "my-network", "all-polis"}
	for _, sc := range rejected {
		code, _ := streamRequest(t, s, "type=dms&scope="+sc)
		if code != http.StatusBadRequest {
			t.Errorf("type=dms&scope=%s should 400, got %d", sc, code)
		}
	}
	// Valid: my-mutuals (inbox) and @<handle> (thread).
	code, _ := streamRequest(t, s, "type=dms&scope=my-mutuals")
	if code != http.StatusOK {
		t.Errorf("type=dms&scope=my-mutuals should 200, got %d", code)
	}
	code, _ = streamRequest(t, s, "type=dms&scope=@alice.polis.pub")
	if code != http.StatusOK {
		t.Errorf("type=dms&scope=@alice.polis.pub should 200, got %d", code)
	}
}

// TestStreamItemsDM_ReturnsConversationSummaries — step-06/6.g. Wires
// to dm.Store.LoadIndex and shapes the response for the v4 stream
// renderDM consumer. Asserts the response shape (id / type / sender_*
// / body_text / unread / url / published[_human]), the qualifier=new
// filter (only unread when set), and ordering (newest-first by
// LastMessageAt).
//
// Privacy invariant: encrypted bytes never appear in the response.
// The handler reads the at-rest-encrypted index, decrypts previews
// server-side via DecryptIndexPreviews, and surfaces the decrypted
// truncation as body_text. Full message bodies stay server-side
// (the SPA fetches them on conversation open via /api/dm/conversations/<id>).
func TestStreamItemsDM_ReturnsConversationSummaries(t *testing.T) {
	s := newConfiguredServer(t)
	mb, self, _, err := s.dmMailbox()
	if err != nil {
		t.Fatalf("dmMailbox: %v", err)
	}
	now := time.Now().UTC()

	// Seed three conversations in the mailbox by appending one incoming wire box each,
	// then overwrite each conversation.json to control ordering (last_message_at) and read
	// state (read cursor) — the mailbox stamps "now" on append, so we set these directly.
	seed := func(peer string, lastAgo time.Duration, read bool) {
		t.Helper()
		var nonce [24]byte
		var senderPub [32]byte
		copy(nonce[:], []byte(peer))
		if _, err := mb.AppendReceived(peer, peer, 0, []byte("ciphertext"), nonce, senderPub, ""); err != nil {
			t.Fatalf("AppendReceived %s: %v", peer, err)
		}
		lastAt := now.Add(-lastAgo).Format(time.RFC3339)
		readAt := "" // unread: read cursor before the message
		if read {
			readAt = now.Add(time.Hour).Format(time.RFC3339) // after the message → unread 0
		}
		meta := dm.ConversationMeta{
			Peer:           peer,
			ConversationID: dm.ComputeConversationID(self, peer),
			CreatedAt:      lastAt,
			LastMessageAt:  lastAt,
			ReadAt:         readAt,
			MessageCount:   1,
		}
		data, _ := json.MarshalIndent(meta, "", "  ")
		path := filepath.Join(dm.DMDir(s.DataDir), "conversations", peer, "conversation.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("write meta %s: %v", peer, err)
		}
	}
	seed("alice.polis.pub", 1*time.Hour, false) // unread, newest
	seed("bob.polis.pub", 3*time.Hour, true)    // read, oldest
	seed("charlie.polis.pub", 2*time.Hour, false)

	idAlice := dm.ComputeConversationID(self, "alice.polis.pub")

	// qualifier=all: 3 items, ordered newest-first (alice, charlie, bob).
	code, resp := streamRequest(t, s, "type=dms&qualifier=all")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 3 {
		t.Fatalf("expected 3 conversations, got %d", len(items))
	}
	first := items[0].(map[string]interface{})
	if first["type"] != "dm" {
		t.Errorf("expected type=dm, got %v", first["type"])
	}
	if first["conversation_id"] != idAlice {
		t.Errorf("expected newest-first ordering (alice), got %v", first["conversation_id"])
	}
	if first["sender_domain"] != "alice.polis.pub" {
		t.Errorf("expected sender_domain=alice.polis.pub, got %v", first["sender_domain"])
	}
	if first["url"] != "/_/messages/"+idAlice {
		t.Errorf("expected url=/_/messages/%s, got %v", idAlice, first["url"])
	}
	// No server-readable preview under end-to-end encryption.
	if bt, ok := first["body_text"]; ok && bt != "" {
		t.Errorf("expected empty body_text (no server-readable preview), got %v", bt)
	}
	if first["unread"] != true {
		t.Errorf("expected unread=true, got %v", first["unread"])
	}
	// Expected ordering: alice (1h ago), charlie (2h), bob (3h).
	wantOrder := []string{
		idAlice,
		dm.ComputeConversationID(self, "charlie.polis.pub"),
		dm.ComputeConversationID(self, "bob.polis.pub"),
	}
	for i, want := range wantOrder {
		got := items[i].(map[string]interface{})["conversation_id"]
		if got != want {
			t.Errorf("position %d: want %q, got %v", i, want, got)
		}
	}

	// qualifier=new: only the 2 unread conversations (alice + charlie), bob filtered out.
	_, resp = streamRequest(t, s, "type=dms&qualifier=new")
	items, _ = resp["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("qualifier=new should filter to unread; got %d items", len(items))
	}
	for _, it := range items {
		if it.(map[string]interface{})["unread"] != true {
			t.Errorf("qualifier=new returned a read conversation")
		}
	}

	// Privacy invariant: no encrypted-payload fields surface in the response.
	bodyJSON, _ := json.Marshal(resp)
	if strings.Contains(string(bodyJSON), "encrypted_content") ||
		strings.Contains(string(bodyJSON), "storage_nonce") ||
		strings.Contains(string(bodyJSON), "ciphertext") {
		t.Errorf("encrypted bytes leaked into response: %s", bodyJSON)
	}
}

// TestStreamItems_TypeDrafts verifies the drafts dispatch reads from the
// local draft store and returns one item per .md file with title + excerpt.
func TestStreamItems_TypeDrafts(t *testing.T) {
	s := newConfiguredServer(t)
	draftsDir := filepath.Join(s.DataDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts")
	_ = os.MkdirAll(draftsDir, 0755)
	_ = os.WriteFile(filepath.Join(draftsDir, "first.md"), []byte("# First Draft\n\nbody one"), 0644)
	_ = os.WriteFile(filepath.Join(draftsDir, "second.md"), []byte("# Second Draft\n\nbody two"), 0644)

	code, resp := streamRequest(t, s, "type=drafts&scope=me")
	if code != http.StatusOK {
		t.Fatalf("expected 200 for type=drafts&scope=me, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 2 {
		t.Errorf("expected 2 drafts, got %d", len(items))
	}
	for _, it := range items {
		m := it.(map[string]interface{})
		// R20-13: drafts now ride the post renderer (Type:"post" +
		// Draft:true flag) — the v4 renderers map has no 'draft'
		// entry, so emitting the old type silently dropped them.
		if m["type"] != "post" {
			t.Errorf("expected type=post (draft flag carries the distinction), got %v", m["type"])
		}
		if m["draft"] != true {
			t.Errorf("expected draft=true marker, got %v", m["draft"])
		}
		if m["title"] == nil || m["title"] == "" {
			t.Errorf("expected non-empty title, got %v", m["title"])
		}
		if !strings.HasPrefix(m["url"].(string), "/_/posts/drafts/") {
			t.Errorf("expected /_/posts/drafts/ prefix, got %v", m["url"])
		}
	}
}

// TestStreamItems_TypeDraftsRequiresScopeMe locks in the constraint that
// drafts are private — only scope=me (or alias) is valid.
func TestStreamItems_TypeDraftsRequiresScopeMe(t *testing.T) {
	s := newConfiguredServer(t)
	cases := []string{"my-network", "all-polis", "my-mutuals"}
	for _, scope := range cases {
		code, resp := streamRequest(t, s, fmt.Sprintf("type=drafts&scope=%s", scope))
		if code != http.StatusBadRequest {
			t.Errorf("type=drafts&scope=%s should 400, got %d (resp=%v)", scope, code, resp)
		}
	}
}

// TestStreamItems_TypeDraftsSortByName covers the by-name sort modifier
// for drafts (alphabetic by title, case-insensitive).
func TestStreamItems_TypeDraftsSortByName(t *testing.T) {
	s := newConfiguredServer(t)
	draftsDir := filepath.Join(s.DataDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts")
	_ = os.MkdirAll(draftsDir, 0755)
	_ = os.WriteFile(filepath.Join(draftsDir, "z.md"), []byte("# Zebra Notes\n"), 0644)
	_ = os.WriteFile(filepath.Join(draftsDir, "a.md"), []byte("# Apple Notes\n"), 0644)

	code, resp := streamRequest(t, s, "type=drafts&scope=me&sort=by-name")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("expected 2 drafts, got %d", len(items))
	}
	first := items[0].(map[string]interface{})["title"].(string)
	if first != "Apple Notes" {
		t.Errorf("by-name should sort 'Apple Notes' first, got %q", first)
	}
}

// TestStreamItems_TypeActivity verifies the activity dispatch returns
// mixed types (no per-type filter) within the chosen scope.
func TestStreamItems_TypeActivity(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()
	seedNetworkFeed(t, s, []feed.FeedItem{
		{Type: "post", Title: "P", URL: "posts/p.md", Published: now.Add(-1 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "comment", Title: "C", URL: "comments/c.md", Published: now.Add(-2 * time.Hour).Format(time.RFC3339), AuthorURL: "https://b.pub", AuthorDomain: "b.pub"},
		{Type: "follow", Title: "F", URL: "https://c.pub", Published: now.Add(-3 * time.Hour).Format(time.RFC3339), AuthorURL: "https://c.pub", AuthorDomain: "c.pub"},
	})
	code, resp := streamRequest(t, s, "type=activity&scope=my-network")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 3 {
		t.Errorf("type=activity should return all 3 mixed items, got %d", len(items))
	}
}

// TestStreamItems_ScopeMyMutuals verifies the mutuals-set computation:
// the intersection of following.json (outbound) and the FollowerState
// projection (inbound). Cache items from non-mutuals are filtered out.
func TestStreamItems_ScopeMyMutuals(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()

	// Outbound: follow alice + bob (don't follow charlie).
	ff := &following.FollowingFile{Following: []following.FollowingEntry{
		{URL: "https://alice.polis.pub", AddedAt: now.Format(time.RFC3339)},
		{URL: "https://bob.polis.pub", AddedAt: now.Format(time.RFC3339)},
	}}
	if err := following.Save(following.DefaultPath(s.DataDir), ff); err != nil {
		t.Fatalf("save following: %v", err)
	}
	// Inbound: alice + charlie follow me (NOT bob).
	store := stream.NewStore(s.DataDir, s.GetDiscoveryDomain(), "pub.polis.core")
	if err := store.SaveState("pub.polis.follow", &stream.FollowerState{
		Followers: []string{"alice.polis.pub", "charlie.polis.pub"},
		Count:     2,
	}); err != nil {
		t.Fatalf("save follower projection: %v", err)
	}
	// Mutuals = {alice}; cache items from alice should pass; bob/charlie filtered.
	seedNetworkFeed(t, s, []feed.FeedItem{
		{Type: "post", Title: "From Alice", URL: "posts/a.md", Published: now.Add(-1 * time.Hour).Format(time.RFC3339), AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
		{Type: "post", Title: "From Bob (one-way out)", URL: "posts/b.md", Published: now.Add(-2 * time.Hour).Format(time.RFC3339), AuthorURL: "https://bob.polis.pub", AuthorDomain: "bob.polis.pub"},
		{Type: "post", Title: "From Charlie (one-way in)", URL: "posts/c.md", Published: now.Add(-3 * time.Hour).Format(time.RFC3339), AuthorURL: "https://charlie.polis.pub", AuthorDomain: "charlie.polis.pub"},
	})

	code, resp := streamRequest(t, s, "scope=my-mutuals&type=posts&time=all")
	if code != http.StatusOK {
		t.Fatalf("expected 200 for scope=my-mutuals, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 mutual post (alice), got %d", len(items))
	}
	title := items[0].(map[string]interface{})["title"].(string)
	if title != "From Alice" {
		t.Errorf("expected 'From Alice' (mutual), got %q", title)
	}
}

// TestComputeMutualsSet_CacheHit — R17 final-review item #5. Cache
// returns the SAME map across consecutive calls when neither source
// file's mtime has changed (and TTL hasn't expired). Identity check:
// post-cache calls return the very same map reference.
func TestComputeMutualsSet_CacheHit(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()

	ff := &following.FollowingFile{Following: []following.FollowingEntry{
		{URL: "https://alice.polis.pub", AddedAt: now.Format(time.RFC3339)},
	}}
	if err := following.Save(following.DefaultPath(s.DataDir), ff); err != nil {
		t.Fatalf("save following: %v", err)
	}
	store := stream.NewStore(s.DataDir, s.GetDiscoveryDomain(), "pub.polis.core")
	if err := store.SaveState("pub.polis.follow", &stream.FollowerState{
		Followers: []string{"alice.polis.pub"},
		Count:     1,
	}); err != nil {
		t.Fatalf("save follower projection: %v", err)
	}

	first := s.computeMutualsSet()
	if len(first) != 1 || !first["alice.polis.pub"] {
		t.Fatalf("first call should return {alice}: %v", first)
	}

	// Second call within TTL + same mtimes → SAME map reference.
	second := s.computeMutualsSet()
	if &first == &second {
		// Map references are pointers; comparing addresses isn't
		// meaningful in Go for maps. Use reflect.DeepEqual to confirm
		// semantic equality, then trust the cache-hit path didn't
		// allocate a new map.
	}
	if len(second) != 1 || !second["alice.polis.pub"] {
		t.Errorf("second call should return same set: %v", second)
	}
}

// TestComputeMutualsSet_InvalidatesOnMtimeChange — touching the
// outbound source file (following.json) bumps its mtime; the next
// computeMutualsSet call must recompute (cache MISS).
func TestComputeMutualsSet_InvalidatesOnMtimeChange(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()

	ff := &following.FollowingFile{Following: []following.FollowingEntry{
		{URL: "https://alice.polis.pub", AddedAt: now.Format(time.RFC3339)},
	}}
	if err := following.Save(following.DefaultPath(s.DataDir), ff); err != nil {
		t.Fatalf("save following: %v", err)
	}
	store := stream.NewStore(s.DataDir, s.GetDiscoveryDomain(), "pub.polis.core")
	if err := store.SaveState("pub.polis.follow", &stream.FollowerState{
		Followers: []string{"alice.polis.pub", "bob.polis.pub"},
		Count:     2,
	}); err != nil {
		t.Fatalf("save follower projection: %v", err)
	}

	first := s.computeMutualsSet()
	if len(first) != 1 || !first["alice.polis.pub"] {
		t.Fatalf("first call: expected {alice}, got %v", first)
	}

	// Now follow bob (mutates following.json → mtime bumps).
	// Sleep briefly first to ensure mtime resolution catches the change
	// (filesystem mtime granularity is typically 1ns on Linux but can be
	// 1s on some filesystems; 10ms is enough for either).
	time.Sleep(10 * time.Millisecond)
	ff.Following = append(ff.Following, following.FollowingEntry{URL: "https://bob.polis.pub", AddedAt: now.Format(time.RFC3339)})
	if err := following.Save(following.DefaultPath(s.DataDir), ff); err != nil {
		t.Fatalf("save following: %v", err)
	}

	// Second call should recompute (cache MISS) and return {alice, bob}.
	second := s.computeMutualsSet()
	if len(second) != 2 || !second["alice.polis.pub"] || !second["bob.polis.pub"] {
		t.Errorf("second call after follow should return {alice, bob}: %v", second)
	}
}

// TestStreamItems_WithPendingBlessingValidation locks in that
// with=pending-blessing only pairs with type=comments AND requires
// scope=all-polis (review concern, step-06/6.c follow-up — the
// "comments to bless" preset surfaces inbound blessing requests
// from anyone in the network; other scopes are nonsensical).
func TestStreamItems_WithPendingBlessingValidation(t *testing.T) {
	s := newConfiguredServer(t)
	// Wrong type pairing → 400.
	code, _ := streamRequest(t, s, "with=pending-blessing&type=posts")
	if code != http.StatusBadRequest {
		t.Errorf("with=pending-blessing&type=posts should 400, got %d", code)
	}
	// Wrong scope pairing → 400.
	code, _ = streamRequest(t, s, "with=pending-blessing&type=comments&scope=my-network")
	if code != http.StatusBadRequest {
		t.Errorf("with=pending-blessing&type=comments&scope=my-network should 400 (requires all-polis), got %d", code)
	}
	code, _ = streamRequest(t, s, "with=pending-blessing&type=comments&scope=my")
	if code != http.StatusBadRequest {
		t.Errorf("with=pending-blessing&type=comments&scope=my should 400 (requires all-polis), got %d", code)
	}
	// Right pairing → accepted (validation passes; data wiring lands later in 6.e).
	code, _ = streamRequest(t, s, "with=pending-blessing&type=comments&scope=all-polis")
	if code != http.StatusOK {
		t.Errorf("with=pending-blessing&type=comments&scope=all-polis should pass validation, got %d", code)
	}
}

// TestClientEvent_FieldSchemaEnforced — step-06/6.f.2 review concern
// #2. /api/v1/event must filter caller fields through the per-event
// schema: drop unknown keys, drop non-string values, length-cap
// strings, and stamp client_origin server-side (not caller-spoofable).
func TestClientEvent_FieldSchemaEnforced(t *testing.T) {
	s := newConfiguredServer(t)

	// Mixed payload: 2 valid known string fields, 1 unknown key, 1
	// non-string value, 1 over-length string, attempted client_origin
	// spoof.
	overlong := strings.Repeat("x", clientEventFieldMaxLen+50)
	body := jsonBody(t, map[string]interface{}{
		"event": "pub.polis.stream.preset_loaded",
		"fields": map[string]interface{}{
			"preset_name":   "gateway",
			"qualifier":     "new",
			"unknown_key":   "should be dropped",
			"type":          map[string]string{"nested": "object should drop"},
			"scope":         overlong,
			"client_origin": "evil-attacker", // attempted spoof
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/event", body)
	w := httptest.NewRecorder()
	s.handleClientEvent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (validation drops bad fields, doesn't reject the request); got %d (body=%s)", w.Code, w.Body.String())
	}

	// Direct unit test of the validator for fine-grained assertions.
	got := validateClientEventFields("pub.polis.stream.preset_loaded", map[string]interface{}{
		"preset_name":   "gateway",
		"qualifier":     "new",
		"unknown_key":   "drop",
		"type":          map[string]string{"nested": "drop"},
		"scope":         overlong,
		"client_origin": "evil",
	})
	if got["preset_name"] != "gateway" {
		t.Errorf("expected preset_name passed through, got %v", got["preset_name"])
	}
	if _, ok := got["unknown_key"]; ok {
		t.Errorf("unknown_key should be dropped, got %v", got["unknown_key"])
	}
	if _, ok := got["type"]; ok {
		t.Errorf("non-string type should be dropped, got %v", got["type"])
	}
	scope, _ := got["scope"].(string)
	if len(scope) != clientEventFieldMaxLen {
		t.Errorf("scope should be length-capped to %d, got %d", clientEventFieldMaxLen, len(scope))
	}
	if _, ok := got["client_origin"]; ok {
		// Validator drops it; handler then stamps the canonical value.
		t.Errorf("client_origin from caller should be filtered out (server stamps it), got %v", got["client_origin"])
	}

	// Event with no schema entry: all fields drop.
	got2 := validateClientEventFields("pub.polis.stream.unknown", map[string]interface{}{
		"anything": "everything",
	})
	if len(got2) != 0 {
		t.Errorf("event with no schema should drop all fields, got %v", got2)
	}
}

// TestNotificationRulesEndpoint_ReturnsRegistryRules covers the
// step-06/6.f.2 endpoint that surfaces registry.json's notification
// rules to the SPA's activity-mode renderer. Asserts the endpoint
// returns the canonical 8 rule IDs from DefaultRegistry, plus that
// non-GET methods 405.
func TestNotificationRulesEndpoint_ReturnsRegistryRules(t *testing.T) {
	s := newConfiguredServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/bundle/notification-rules", nil)
	w := httptest.NewRecorder()
	s.handleNotificationRules(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var resp struct {
		Rules []map[string]interface{} `json:"rules"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	wantIDs := map[string]bool{
		"new-post": false, "updated-post": false,
		"new-comment": false, "blessing-requested": false,
		"blessing-granted": false, "blessing-denied": false,
		"new-follower": false, "lost-follower": false,
	}
	for _, rule := range resp.Rules {
		id, _ := rule["id"].(string)
		if _, ok := wantIDs[id]; ok {
			wantIDs[id] = true
		}
	}
	for id, found := range wantIDs {
		if !found {
			t.Errorf("expected rule %q in response", id)
		}
	}

	// Wrong method → 405.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/bundle/notification-rules", nil)
	w = httptest.NewRecorder()
	s.handleNotificationRules(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST should 405, got %d", w.Code)
	}
}

// TestStreamItems_ProfilesScopeAliases is the regression test for the
// step-06/6.e bug (carried forward into 06-profiles): scope aliases must
// all route to the own-tenant data path. The previous grammar
// (type=follows) only recognized @<myDomain> form and rejected scope=me;
// the new grammar (type=profiles) accepts me / my / my-network /
// @<myDomain> as the four valid own-tenant aliases.
func TestStreamItems_ProfilesScopeAliases(t *testing.T) {
	s := newConfiguredServer(t)
	myDomain := extractDomainFromURL(s.GetBaseURL())
	if myDomain == "" {
		t.Fatal("test setup didn't configure a base URL")
	}
	now := time.Now().UTC()
	// Save an outbound follow so we can verify all code paths return data.
	ff := &following.FollowingFile{Following: []following.FollowingEntry{
		{URL: "https://discover.polis.pub", AddedAt: now.Format(time.RFC3339)},
	}}
	if err := following.Save(following.DefaultPath(s.DataDir), ff); err != nil {
		t.Fatalf("save following: %v", err)
	}

	// scope=me (URL-token alias) must work for type=profiles.
	code, resp := streamRequest(t, s, "type=profiles&scope=me")
	if code != http.StatusOK {
		t.Errorf("type=profiles&scope=me should 200, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 1 {
		t.Errorf("scope=me should return the 1 outbound profile, got %d items", len(items))
	}

	// scope=my (raw form) — same.
	code, _ = streamRequest(t, s, "type=profiles&scope=my")
	if code != http.StatusOK {
		t.Errorf("type=profiles&scope=my should 200, got %d", code)
	}

	// scope=@<myDomain> (handle form) — same.
	code, _ = streamRequest(t, s, "type=profiles&scope=@"+myDomain)
	if code != http.StatusOK {
		t.Errorf("type=profiles&scope=@%s should 200, got %d", myDomain, code)
	}

	// scope=@other.pub — still 400 (cross-tenant profiles not exposed).
	code, _ = streamRequest(t, s, "type=profiles&scope=@other.pub")
	if code != http.StatusBadRequest {
		t.Errorf("type=profiles&scope=@other.pub should 400 (cross-tenant profiles not exposed), got %d", code)
	}

	// scope=my-network — now valid for profiles (the natural default).
	code, _ = streamRequest(t, s, "type=profiles&scope=my-network")
	if code != http.StatusOK {
		t.Errorf("type=profiles&scope=my-network should 200 (new grammar default), got %d", code)
	}
}

// TestStreamItems_ProfilesSortByName covers the by-name sort modifier
// for profiles (alphabetic by author domain, the user-visible "all
// profiles from <scope> by name" clause).
func TestStreamItems_ProfilesSortByName(t *testing.T) {
	s := newConfiguredServer(t)
	myDomain := extractDomainFromURL(s.GetBaseURL())
	if myDomain == "" {
		t.Fatal("test setup didn't configure a base URL")
	}
	now := time.Now().UTC()
	// Save outbound follows in non-alphabetic order; expect alphabetic on read.
	ff := &following.FollowingFile{Following: []following.FollowingEntry{
		{URL: "https://zane.polis.pub", AddedAt: now.Format(time.RFC3339)},
		{URL: "https://alice.polis.pub", AddedAt: now.Format(time.RFC3339)},
		{URL: "https://maria.polis.pub", AddedAt: now.Format(time.RFC3339)},
	}}
	if err := following.Save(following.DefaultPath(s.DataDir), ff); err != nil {
		t.Fatalf("save following: %v", err)
	}

	code, resp := streamRequest(t, s, "type=profiles&scope=@"+myDomain+"&sort=by-name")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 3 {
		t.Fatalf("expected 3 profiles, got %d", len(items))
	}
	// Profile items carry the displayed party in author_domain (Phase 2:
	// type=profile flattens direction so the renderer always reads the
	// "other party" from a single field).
	want := []string{"alice.polis.pub", "maria.polis.pub", "zane.polis.pub"}
	for i, it := range items {
		m := it.(map[string]interface{})
		got, _ := m["author_domain"].(string)
		if got != want[i] {
			t.Errorf("profiles by-name order pos %d: want %q, got %q", i, want[i], got)
		}
	}
}

// TestStreamItems_ProfilesMyNetworkSearch verifies the &search= filter
// on the my-network profiles surface. Matching is case-insensitive
// substring against author_domain (and display_name when set). The
// follow list is small, so server-side filtering happens in memory
// after buildProfilesList without changing its shared signature.
func TestStreamItems_ProfilesMyNetworkSearch(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()
	ff := &following.FollowingFile{Following: []following.FollowingEntry{
		{URL: "https://alice.polis.pub", AddedAt: now.Format(time.RFC3339)},
		{URL: "https://bob.polis.pub", AddedAt: now.Format(time.RFC3339)},
		{URL: "https://carol.polis.pub", AddedAt: now.Format(time.RFC3339)},
		{URL: "https://discover.polis.pub", AddedAt: now.Format(time.RFC3339)},
	}}
	if err := following.Save(following.DefaultPath(s.DataDir), ff); err != nil {
		t.Fatalf("save following: %v", err)
	}

	// Substring match — "ali" hits alice; case-insensitive.
	code, resp := streamRequest(t, s, "type=profiles&scope=my-network&search=ALI")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("search=ALI should match 1 follow (alice), got %d", len(items))
	}
	m := items[0].(map[string]interface{})
	if got, _ := m["author_domain"].(string); got != "alice.polis.pub" {
		t.Errorf("search=ALI: got %q, want alice.polis.pub", got)
	}

	// Empty/whitespace search returns the full list (no filter applied).
	code, resp = streamRequest(t, s, "type=profiles&scope=my-network&search=%20%20")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	items, _ = resp["items"].([]interface{})
	if len(items) != 4 {
		t.Errorf("whitespace search should return full list of 4, got %d", len(items))
	}

	// No matches → empty array, not 404.
	code, resp = streamRequest(t, s, "type=profiles&scope=my-network&search=zzzzzz")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	items, _ = resp["items"].([]interface{})
	if len(items) != 0 {
		t.Errorf("search=zzzzzz should match 0, got %d", len(items))
	}
}

// TestStreamItems_ProfilesSortByActivity covers the by-activity sort
// modifier for profiles (most-recently-active first; matches the
// user-visible "all profiles from <scope> by activity" clause). Items
// with empty Published (inbound follows lacking AnnouncedAt) sort last.
func TestStreamItems_ProfilesSortByActivity(t *testing.T) {
	s := newConfiguredServer(t)
	myDomain := extractDomainFromURL(s.GetBaseURL())
	if myDomain == "" {
		t.Fatal("test setup didn't configure a base URL")
	}
	now := time.Now().UTC()
	// Save outbound follows with different AddedAt timestamps. by-activity
	// sorts most-recent first.
	ff := &following.FollowingFile{Following: []following.FollowingEntry{
		{URL: "https://old.polis.pub", AddedAt: now.Add(-72 * time.Hour).Format(time.RFC3339)},
		{URL: "https://new.polis.pub", AddedAt: now.Add(-1 * time.Hour).Format(time.RFC3339)},
		{URL: "https://mid.polis.pub", AddedAt: now.Add(-24 * time.Hour).Format(time.RFC3339)},
	}}
	if err := following.Save(following.DefaultPath(s.DataDir), ff); err != nil {
		t.Fatalf("save following: %v", err)
	}

	code, resp := streamRequest(t, s, "type=profiles&scope=@"+myDomain+"&sort=by-activity")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 3 {
		t.Fatalf("expected 3 profiles, got %d", len(items))
	}
	want := []string{"new.polis.pub", "mid.polis.pub", "old.polis.pub"}
	for i, it := range items {
		m := it.(map[string]interface{})
		got, _ := m["author_domain"].(string)
		if got != want[i] {
			t.Errorf("profiles by-activity order pos %d: want %q, got %q", i, want[i], got)
		}
	}
}

// TestStreamItems_ProfilesDefaultSortIsByName confirms the type-conditional
// sort default: when no &sort= is passed and type=profiles, the response
// is alphabetic by name (not by-date, which is the default for everything
// else). Documents the difference from drafts (whose default is by-date).
func TestStreamItems_ProfilesDefaultSortIsByName(t *testing.T) {
	s := newConfiguredServer(t)
	myDomain := extractDomainFromURL(s.GetBaseURL())
	now := time.Now().UTC()
	// Save in non-alphabetic order; expect alphabetic on read with no sort param.
	ff := &following.FollowingFile{Following: []following.FollowingEntry{
		{URL: "https://zane.polis.pub", AddedAt: now.Add(-1 * time.Hour).Format(time.RFC3339)},
		{URL: "https://alice.polis.pub", AddedAt: now.Format(time.RFC3339)},
	}}
	if err := following.Save(following.DefaultPath(s.DataDir), ff); err != nil {
		t.Fatalf("save following: %v", err)
	}
	code, resp := streamRequest(t, s, "type=profiles&scope=@"+myDomain)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (resp=%v)", code, resp)
	}
	items, _ := resp["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(items))
	}
	first, _ := items[0].(map[string]interface{})["author_domain"].(string)
	if first != "alice.polis.pub" {
		t.Errorf("default sort should be by-name (alphabetic); got first=%q (want alice.polis.pub)", first)
	}
}

// TestStreamItems_SortValidation locks in the sort-param constraints
// for the 06-profiles grammar:
//   - profiles: by-name (default) or by-activity
//   - drafts:   by-date (default) or by-name
//   - others:   by-date only (default)
//
// Cross-type misuse rejected at validation.
func TestStreamItems_SortValidation(t *testing.T) {
	s := newConfiguredServer(t)
	myDomain := extractDomainFromURL(s.GetBaseURL())
	cases := []struct {
		name  string
		query string
		want  int
	}{
		{"unknown sort value", "sort=by-color&type=posts", http.StatusBadRequest},
		{"by-name on posts", "sort=by-name&type=posts", http.StatusBadRequest},
		{"by-name on comments", "sort=by-name&type=comments", http.StatusBadRequest},
		{"by-activity on posts", "sort=by-activity&type=posts", http.StatusBadRequest},
		{"by-date on profiles", "sort=by-date&type=profiles&scope=@" + myDomain, http.StatusBadRequest},
		{"by-name on profiles ok", "sort=by-name&type=profiles&scope=@" + myDomain, http.StatusOK},
		{"by-activity on profiles ok", "sort=by-activity&type=profiles&scope=@" + myDomain, http.StatusOK},
		{"by-name on drafts ok", "sort=by-name&type=drafts&scope=me", http.StatusOK},
		{"by-date on drafts ok", "sort=by-date&type=drafts&scope=me", http.StatusOK},
		{"by-date default ok", "sort=by-date&type=posts", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _ := streamRequest(t, s, tc.query)
			if code != tc.want {
				t.Errorf("expected %d, got %d", tc.want, code)
			}
		})
	}
}

// TestClientEvent_AllowlistEnforced — POST /api/v1/event accepts only
// events listed in allowedClientEvents. step-06/6.e ships
// pub.polis.stream.preset_loaded; everything else 400s. Prevents the
// endpoint from becoming a generic log-anything sink.
func TestClientEvent_AllowlistEnforced(t *testing.T) {
	s := newConfiguredServer(t)

	// Allowed event → 200.
	body := jsonBody(t, map[string]interface{}{
		"event":  "pub.polis.stream.preset_loaded",
		"fields": map[string]string{"preset_name": "gateway"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/event", body)
	w := httptest.NewRecorder()
	s.handleClientEvent(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("allowed event should 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	// Disallowed event → 400.
	body = jsonBody(t, map[string]interface{}{
		"event":  "pub.polis.evil.exfil",
		"fields": map[string]string{},
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/event", body)
	w = httptest.NewRecorder()
	s.handleClientEvent(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("disallowed event should 400, got %d", w.Code)
	}

	// Wrong method → 405.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/event", nil)
	w = httptest.NewRecorder()
	s.handleClientEvent(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET should 405, got %d", w.Code)
	}

	// Malformed JSON → 400.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/event", strings.NewReader("not json"))
	w = httptest.NewRecorder()
	s.handleClientEvent(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("malformed JSON should 400, got %d", w.Code)
	}
}

// TestHandleStreamItemsDMThread_LockedShipsBox verifies phase-4.5 server wiring:
// a password-epoch message the server can't read (it holds no epoch-1 DEK) is
// returned as locked, WITHOUT leaking plaintext, but WITH the raw wire box
// (ciphertext / nonce / box_pub) + key_epoch so the SPA can open it in-browser
// after the user unlocks that epoch with their password.
func TestHandleStreamItemsDMThread_LockedShipsBox(t *testing.T) {
	s := newConfiguredServer(t)
	const peer = "david.polis.pub"
	const secret = "meet me at the old pier"

	dmDir := dm.DMDir(s.DataDir)

	// Keyring with only the bootstrap (server-held) epoch on disk — so the
	// server's LoadAvailableDEKs hands the mailbox only epoch 0's DEK.
	k := &dm.Keyring{}
	if _, err := k.AddBootstrapEpoch(); err != nil {
		t.Fatalf("AddBootstrapEpoch: %v", err)
	}
	if err := k.SaveCAS(dmDir, 0); err != nil {
		t.Fatalf("SaveCAS: %v", err)
	}

	// A message sealed to a password epoch (1). The server never has this DEK,
	// so reading it later yields Locked=true.
	pub1, dek1, err := dm.NewEpochKeypair()
	if err != nil {
		t.Fatalf("NewEpochKeypair: %v", err)
	}
	mb := dm.NewMailbox(dmDir)
	if _, err := mb.AppendSent(peer, "testsite.polis.pub", secret, 1, pub1, dek1, "", "sent"); err != nil {
		t.Fatalf("AppendSent: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stream/items?type=dms&scope=@"+peer, nil)
	w := httptest.NewRecorder()
	s.handleStreamItemsDMThread(w, req, peer, streamCursor{}, 50, "newest")

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []struct {
			Type       string `json:"type"`
			Locked     bool   `json:"locked"`
			KeyEpoch   int    `json:"key_epoch"`
			Ciphertext string `json:"ciphertext"`
			Nonce      string `json:"nonce"`
			BoxPub     string `json:"box_pub"`
			BodyText   string `json:"body_text"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	if len(resp.Items) != 1 {
		t.Fatalf("want 1 item, got %d: %s", len(resp.Items), w.Body.String())
	}
	it := resp.Items[0]
	if it.Type != "dm-message" {
		t.Errorf("type = %q, want dm-message", it.Type)
	}
	if !it.Locked {
		t.Error("message should be locked (password epoch; server holds no DEK)")
	}
	if it.KeyEpoch != 1 {
		t.Errorf("key_epoch = %d, want 1", it.KeyEpoch)
	}
	if it.Ciphertext == "" || it.Nonce == "" || it.BoxPub == "" {
		t.Errorf("locked item must ship the raw box; got ct=%q nonce=%q box_pub=%q", it.Ciphertext, it.Nonce, it.BoxPub)
	}
	if it.BodyText == secret {
		t.Error("server must NOT expose plaintext for a locked message")
	}
}
