package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/feed"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
)

// ============================================================================
// generateSyncID Tests
// ============================================================================

func TestGenerateSyncID_ValidUUIDv4(t *testing.T) {
	for i := 0; i < 100; i++ {
		id := generateSyncID()

		// UUIDv4 format: 8-4-4-4-12 hex chars
		parts := strings.Split(id, "-")
		if len(parts) != 5 {
			t.Fatalf("expected 5 dash-separated parts, got %d: %q", len(parts), id)
		}
		expectedLens := []int{8, 4, 4, 4, 12}
		for j, part := range parts {
			if len(part) != expectedLens[j] {
				t.Errorf("part %d: expected %d chars, got %d in %q", j, expectedLens[j], len(part), id)
			}
		}

		// Version nibble (char index 0 of 3rd segment) must be '4'
		if parts[2][0] != '4' {
			t.Errorf("expected version nibble '4', got %q in %q", string(parts[2][0]), id)
		}

		// Variant nibble (char index 0 of 4th segment) must be 8, 9, a, or b
		v := parts[3][0]
		if v != '8' && v != '9' && v != 'a' && v != 'b' {
			t.Errorf("expected variant nibble in [89ab], got %q in %q", string(v), id)
		}
	}

	// Uniqueness: two calls should not produce the same ID
	a := generateSyncID()
	b := generateSyncID()
	if a == b {
		t.Errorf("two consecutive generateSyncID() calls returned the same value: %q", a)
	}
}

// ============================================================================
// generateBackgroundRequestID Tests
// ============================================================================

func TestGenerateBackgroundRequestID_HasPrefix(t *testing.T) {
	id := generateBackgroundRequestID("feed-sync")
	if !strings.HasPrefix(id, "feed-sync-") {
		t.Errorf("expected prefix 'feed-sync-', got %q", id)
	}

	// The part after the prefix should be a valid UUID
	uuid := strings.TrimPrefix(id, "feed-sync-")
	parts := strings.Split(uuid, "-")
	if len(parts) != 5 {
		t.Errorf("expected UUID after prefix with 5 parts, got %d in %q", len(parts), uuid)
	}
}

func TestGenerateBackgroundRequestID_EmptyPrefix(t *testing.T) {
	id := generateBackgroundRequestID("")
	if !strings.HasPrefix(id, "-") {
		t.Errorf("expected leading '-' with empty prefix, got %q", id)
	}
}

func TestGenerateBackgroundRequestID_Uniqueness(t *testing.T) {
	a := generateBackgroundRequestID("sync")
	b := generateBackgroundRequestID("sync")
	if a == b {
		t.Errorf("two calls with same prefix returned same ID: %q", a)
	}
}


// ============================================================================
// cursorGreater Tests (defined in server.go but used heavily in sync.go)
// ============================================================================

func TestCursorGreater_NumericComparison(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"1", "0", true},
		{"0", "1", false},
		{"20", "10", true},
		{"10", "20", false},
		{"10", "10", false},
		{"10", "9", true},
	}
	for _, tt := range tests {
		got := cursorGreater(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("cursorGreater(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestCursorGreater_StringFallback(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"def", "abc", true},
		{"abc", "def", false},
		{"abc", "abc", false},
	}
	for _, tt := range tests {
		got := cursorGreater(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("cursorGreater(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

// ============================================================================
// SyncResult Tests
// ============================================================================

func TestSyncResult_ZeroValue(t *testing.T) {
	r := SyncResult{}
	if r.NewNotifications != 0 || r.NewFeedItems != 0 || r.FollowersChanged || r.CommentsChanged || r.FilesChanged {
		t.Error("zero-value SyncResult should have all fields at zero/false")
	}
}

// ============================================================================
// firstNonEmptyString Tests
// ============================================================================

func TestFirstNonEmptyString_ReturnsFirstNonEmpty(t *testing.T) {
	payload := map[string]interface{}{
		"comment_url": "https://example.com/comments/1.md",
		"source_url":  "https://fallback.com/comments/1.md",
	}

	got := firstNonEmptyString(payload, "comment_url", "source_url")
	if got != "https://example.com/comments/1.md" {
		t.Errorf("expected comment_url value, got %q", got)
	}
}

func TestFirstNonEmptyString_SkipsEmpty(t *testing.T) {
	payload := map[string]interface{}{
		"comment_url": "",
		"source_url":  "https://fallback.com/comments/1.md",
	}

	got := firstNonEmptyString(payload, "comment_url", "source_url")
	if got != "https://fallback.com/comments/1.md" {
		t.Errorf("expected source_url fallback, got %q", got)
	}
}

func TestFirstNonEmptyString_MissingKey(t *testing.T) {
	payload := map[string]interface{}{
		"source_url": "https://fallback.com/comments/1.md",
	}

	got := firstNonEmptyString(payload, "comment_url", "source_url")
	if got != "https://fallback.com/comments/1.md" {
		t.Errorf("expected source_url when comment_url missing, got %q", got)
	}
}

func TestFirstNonEmptyString_AllEmpty(t *testing.T) {
	payload := map[string]interface{}{
		"comment_url": "",
		"source_url":  "",
	}

	got := firstNonEmptyString(payload, "comment_url", "source_url")
	if got != "" {
		t.Errorf("expected empty string when all values empty, got %q", got)
	}
}

func TestFirstNonEmptyString_NoKeys(t *testing.T) {
	payload := map[string]interface{}{}
	got := firstNonEmptyString(payload)
	if got != "" {
		t.Errorf("expected empty string with no keys, got %q", got)
	}
}

func TestFirstNonEmptyString_NilPayload(t *testing.T) {
	got := firstNonEmptyString(nil, "key1")
	if got != "" {
		t.Errorf("expected empty string with nil payload, got %q", got)
	}
}

func TestFirstNonEmptyString_NonStringValue(t *testing.T) {
	payload := map[string]interface{}{
		"number": 42,
		"real":   "found",
	}

	got := firstNonEmptyString(payload, "number", "real")
	if got != "found" {
		t.Errorf("expected to skip non-string value, got %q", got)
	}
}

// ============================================================================
// extractPostPathFromURL Tests
// ============================================================================

func TestExtractPostPathFromURL_StandardURL(t *testing.T) {
	got := extractPostPathFromURL("https://alice.polis.site/posts/20260127/hello.md")
	if got != "posts/20260127/hello.md" {
		t.Errorf("expected 'posts/20260127/hello.md', got %q", got)
	}
}

func TestExtractPostPathFromURL_NoPostsPath(t *testing.T) {
	input := "https://alice.polis.site/comments/20260127/hello.md"
	got := extractPostPathFromURL(input)
	if got != input {
		t.Errorf("expected original URL returned for non-posts path, got %q", got)
	}
}

func TestExtractPostPathFromURL_PostsAtRoot(t *testing.T) {
	got := extractPostPathFromURL("/posts/20260127/hello.md")
	if got != "posts/20260127/hello.md" {
		t.Errorf("expected 'posts/20260127/hello.md', got %q", got)
	}
}

func TestExtractPostPathFromURL_EmptyString(t *testing.T) {
	got := extractPostPathFromURL("")
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestExtractPostPathFromURL_MultiplePostsSegments(t *testing.T) {
	// Should find the first /posts/ occurrence
	got := extractPostPathFromURL("https://example.com/posts/20260101/posts/nested.md")
	if got != "posts/20260101/posts/nested.md" {
		t.Errorf("expected first /posts/ match, got %q", got)
	}
}

// ============================================================================
// getUnifiedCursor Tests
// ============================================================================

func TestGetUnifiedCursor_ExistingCursor(t *testing.T) {
	s := newTestServer(t)
	s.DiscoveryURL = "https://ds.polis.pub"
	discoveryDomain := s.GetDiscoveryDomain()
	store := stream.NewStore(s.DataDir, discoveryDomain, "pub.polis.core")

	_ = store.SetCursor("pub.polis.sync", "42")

	got := s.getUnifiedCursor(store)
	if got != "42" {
		t.Errorf("expected cursor '42', got %q", got)
	}
}

func TestGetUnifiedCursor_NoCursors(t *testing.T) {
	s := newTestServer(t)
	s.DiscoveryURL = "https://ds.polis.pub"
	discoveryDomain := s.GetDiscoveryDomain()
	store := stream.NewStore(s.DataDir, discoveryDomain, "pub.polis.core")

	got := s.getUnifiedCursor(store)
	if got != "0" {
		t.Errorf("expected '0' when no cursors exist, got %q", got)
	}
}

func TestGetUnifiedCursor_ZeroTreatedAsEmpty(t *testing.T) {
	s := newTestServer(t)
	s.DiscoveryURL = "https://ds.polis.pub"
	discoveryDomain := s.GetDiscoveryDomain()
	store := stream.NewStore(s.DataDir, discoveryDomain, "pub.polis.core")

	_ = store.SetCursor("pub.polis.sync", "0")

	got := s.getUnifiedCursor(store)
	if got != "0" {
		t.Errorf("expected '0', got %q", got)
	}
}

// ============================================================================
// runUnifiedSync Tests (early return paths)
// ============================================================================

func TestRunUnifiedSync_NoDiscoveryURL(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "" // No DS configured

	result := s.runUnifiedSync()
	if result.NewNotifications != 0 || result.NewFeedItems != 0 || result.FilesChanged {
		t.Error("expected empty result when no discovery URL configured")
	}
}

func TestRunUnifiedSync_NoBaseURL(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://ds.polis.pub"
	s.BaseURL = "" // No base URL

	result := s.runUnifiedSync()
	if result.NewNotifications != 0 || result.NewFeedItems != 0 || result.FilesChanged {
		t.Error("expected empty result when no base URL configured")
	}
}

func TestRunUnifiedSync_NoPrivateKey(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://ds.polis.pub"
	s.PrivateKey = nil // No key

	result := s.runUnifiedSync()
	if result.NewNotifications != 0 || result.NewFeedItems != 0 || result.FilesChanged {
		t.Error("expected empty result when no private key configured")
	}
}

func TestRunUnifiedSync_InvalidBaseURL(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://ds.polis.pub"
	s.BaseURL = "://invalid" // extractDomainFromURL returns ""

	result := s.runUnifiedSync()
	if result.NewNotifications != 0 || result.NewFeedItems != 0 || result.FilesChanged {
		t.Error("expected empty result when base URL domain extraction fails")
	}
}

// ============================================================================
// Handler Name/EventTypes Tests
// ============================================================================

func TestFeedSyncHandler_Name(t *testing.T) {
	h := &feedSyncHandler{}
	if h.Name() != "feed" {
		t.Errorf("expected 'feed', got %q", h.Name())
	}
}

func TestFeedSyncHandler_EventTypes(t *testing.T) {
	h := &feedSyncHandler{}
	types := h.EventTypes()
	expected := map[string]bool{
		"pub.polis.post.published":              true,
		"pub.polis.post.republished":            true,
		"pub.polis.comment.published":           true,
		"pub.polis.comment.republished":         true,
		"pub.polis.comment.blessing.granted":    true,
		"pub.polis.comment.blessing.requested":  true,
		"pub.polis.follow.announced":            true,
		"pub.polis.site.registered":             true,
	}
	if len(types) != len(expected) {
		t.Errorf("expected %d event types, got %d", len(expected), len(types))
	}
	for _, et := range types {
		if !expected[et] {
			t.Errorf("unexpected event type: %q", et)
		}
	}
}

func TestFollowSyncHandler_Name(t *testing.T) {
	h := &followSyncHandler{}
	if h.Name() != "followers" {
		t.Errorf("expected 'followers', got %q", h.Name())
	}
}

func TestFollowSyncHandler_EventTypes(t *testing.T) {
	h := &followSyncHandler{}
	types := h.EventTypes()
	expected := map[string]bool{
		"pub.polis.follow.announced": true,
		"pub.polis.follow.removed":   true,
	}
	if len(types) != len(expected) {
		t.Errorf("expected %d event types, got %d", len(expected), len(types))
	}
	for _, et := range types {
		if !expected[et] {
			t.Errorf("unexpected event type: %q", et)
		}
	}
}

func TestBlessingSyncHandler_Name(t *testing.T) {
	h := &blessingSyncHandler{}
	if h.Name() != "blessings" {
		t.Errorf("expected 'blessings', got %q", h.Name())
	}
}

func TestBlessingSyncHandler_EventTypes(t *testing.T) {
	h := &blessingSyncHandler{}
	types := h.EventTypes()
	if len(types) == 0 {
		t.Fatal("expected non-empty event types for blessings handler")
	}
	// Should contain blessing-related events
	found := false
	for _, et := range types {
		if strings.Contains(et, "blessing") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected blessing-related event types")
	}
}

func TestCommentStatusSyncHandler_Name(t *testing.T) {
	h := &commentStatusSyncHandler{}
	if h.Name() != "comment-status" {
		t.Errorf("expected 'comment-status', got %q", h.Name())
	}
}

func TestCommentStatusSyncHandler_EventTypes(t *testing.T) {
	h := &commentStatusSyncHandler{}
	types := h.EventTypes()
	expected := map[string]bool{
		"pub.polis.comment.blessing.granted": true,
		"pub.polis.comment.blessing.denied":  true,
	}
	if len(types) != len(expected) {
		t.Errorf("expected %d event types, got %d", len(expected), len(types))
	}
	for _, et := range types {
		if !expected[et] {
			t.Errorf("unexpected event type: %q", et)
		}
	}
}

// ============================================================================
// Handler Process - early return paths
// ============================================================================

func TestFeedSyncHandler_Process_NoBaseURL(t *testing.T) {
	s := newTestServer(t)
	s.BaseURL = ""
	h := &feedSyncHandler{server: s}

	events := []discovery.StreamEvent{
		{Type: "pub.polis.post.published", Actor: "alice.polis.pub"},
	}
	result := h.Process(events)
	if result.NewItems != 0 || result.Error != nil {
		t.Error("expected empty result when no base URL")
	}
}

func TestFeedSyncHandler_Process_NoFollowing(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://ds.polis.pub"
	h := &feedSyncHandler{server: s}

	// No following.json exists, so should return empty result
	events := []discovery.StreamEvent{
		{Type: "pub.polis.post.published", Actor: "alice.polis.pub"},
	}
	result := h.Process(events)
	if result.NewItems != 0 {
		t.Error("expected 0 new items when no following list")
	}
}

func TestFollowSyncHandler_Process_NoBaseURL(t *testing.T) {
	s := newTestServer(t)
	s.BaseURL = ""
	h := &followSyncHandler{server: s}

	events := []discovery.StreamEvent{
		{Type: "pub.polis.follow.announced", Actor: "bob.polis.pub"},
	}
	result := h.Process(events)
	if result.FilesChanged || result.Error != nil {
		t.Error("expected empty result when no base URL")
	}
}

func TestBlessingSyncHandler_Process_NoBaseURL(t *testing.T) {
	s := newTestServer(t)
	s.BaseURL = ""
	h := &blessingSyncHandler{server: s}

	events := []discovery.StreamEvent{
		{Type: "pub.polis.comment.blessing.requested", Actor: "carol.polis.pub"},
	}
	result := h.Process(events)
	if result.FilesChanged || result.Error != nil {
		t.Error("expected empty result when no base URL")
	}
}

func TestCommentStatusSyncHandler_Process_NoBaseURL(t *testing.T) {
	s := newTestServer(t)
	s.BaseURL = ""
	s.PrivateKey = nil
	h := &commentStatusSyncHandler{server: s}

	events := []discovery.StreamEvent{
		{Type: "pub.polis.comment.blessing.granted", Actor: "alice.polis.pub"},
	}
	result := h.Process(events)
	if result.FilesChanged || result.Error != nil {
		t.Error("expected empty result when not configured")
	}
}

// ============================================================================
// Follow handler: follower count change detection
// ============================================================================

func TestFollowSyncHandler_Process_FollowAnnounced(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://ds.polis.pub"
	h := &followSyncHandler{server: s}

	myDomain := extractDomainFromURL(s.BaseURL)
	events := []discovery.StreamEvent{
		{
			Type:  "pub.polis.follow.announced",
			Actor: "bob.polis.pub",
			Payload: map[string]interface{}{
				"target_domain": myDomain,
			},
		},
	}
	result := h.Process(events)
	// The stream.FollowHandler should add the new follower
	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}
	// FilesChanged should be true since follower count went from 0 to 1
	if !result.FilesChanged {
		t.Error("expected FilesChanged=true when follower count changes")
	}
}

func TestFollowSyncHandler_Process_FollowRemoved(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://ds.polis.pub"

	myDomain := extractDomainFromURL(s.BaseURL)
	discoveryDomain := s.GetDiscoveryDomain()
	store := stream.NewStore(s.DataDir, discoveryDomain, "pub.polis.core")

	// Pre-populate follower state
	existingState := &stream.FollowerState{
		Followers: []string{"bob.polis.pub"},
		Count:     1,
	}
	_ = store.SaveState("pub.polis.follow", existingState)

	h := &followSyncHandler{server: s}
	events := []discovery.StreamEvent{
		{
			Type:  "pub.polis.follow.removed",
			Actor: "bob.polis.pub",
			Payload: map[string]interface{}{
				"target_domain": myDomain,
			},
		},
	}
	result := h.Process(events)
	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}
	if !result.FilesChanged {
		t.Error("expected FilesChanged=true when follower removed")
	}
}

// ============================================================================
// Blessing handler: comment storage on blessing.granted
// ============================================================================

func TestBlessingSyncHandler_Process_SkipsAlreadyStoredComment(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://ds.polis.pub"

	myDomain := extractDomainFromURL(s.BaseURL)

	// Pre-create the comment file so the handler skips the fetch
	commentRelPath := "content/pub.polis.core/comment/20260301/abc123.md"
	localPath := filepath.Join(s.DataDir, commentRelPath)
	_ = os.MkdirAll(filepath.Dir(localPath), 0755)
	_ = os.WriteFile(localPath, []byte("# existing comment"), 0644)

	h := &blessingSyncHandler{server: s}
	events := []discovery.StreamEvent{
		{
			Type:  "pub.polis.comment.blessing.granted",
			Actor: "alice.polis.pub",
			Payload: map[string]interface{}{
				"target_domain": myDomain,
				"comment_url":   "https://alice.polis.pub/comments/20260301/abc123.md",
				"in_reply_to":   "https://" + myDomain + "/posts/20260301/hello.md",
			},
		},
	}
	result := h.Process(events)
	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}
	// Should NOT report files changed because the comment was already stored
	if result.FilesChanged {
		t.Error("expected FilesChanged=false when comment already exists locally")
	}
}

func TestBlessingSyncHandler_Process_SkipsNonTargetDomain(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://ds.polis.pub"

	h := &blessingSyncHandler{server: s}
	events := []discovery.StreamEvent{
		{
			Type:  "pub.polis.comment.blessing.granted",
			Actor: "alice.polis.pub",
			Payload: map[string]interface{}{
				"target_domain": "someone-else.polis.pub",
				"comment_url":   "https://alice.polis.pub/comments/20260301/abc123.md",
				"in_reply_to":   "https://someone-else.polis.pub/posts/20260301/hello.md",
			},
		},
	}
	result := h.Process(events)
	if result.FilesChanged {
		t.Error("expected FilesChanged=false when target domain is not ours")
	}
}

func TestBlessingSyncHandler_Process_SkipsMissingCommentURL(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://ds.polis.pub"
	myDomain := extractDomainFromURL(s.BaseURL)

	h := &blessingSyncHandler{server: s}
	events := []discovery.StreamEvent{
		{
			Type:  "pub.polis.comment.blessing.granted",
			Actor: "alice.polis.pub",
			Payload: map[string]interface{}{
				"target_domain": myDomain,
				// No comment_url or source_url
				"in_reply_to": "https://" + myDomain + "/posts/20260301/hello.md",
			},
		},
	}
	result := h.Process(events)
	if result.FilesChanged {
		t.Error("expected FilesChanged=false when comment_url is missing")
	}
}

// ============================================================================
// Comment status handler: process with configured server
// ============================================================================

func TestCommentStatusSyncHandler_Process_NoMatchingEvents(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://ds.polis.pub"

	h := &commentStatusSyncHandler{server: s}
	// Events that target a different domain should produce no changes
	events := []discovery.StreamEvent{
		{
			Type:  "pub.polis.comment.blessing.granted",
			Actor: "alice.polis.pub",
			Payload: map[string]interface{}{
				"target_domain": "other.polis.pub",
				"source_domain": "other.polis.pub",
			},
		},
	}
	result := h.Process(events)
	// No files changed since none of our comments are involved
	if result.FilesChanged {
		t.Error("expected no files changed for events targeting other domains")
	}
}

// ============================================================================
// Feed handler with following list
// ============================================================================

func TestFeedSyncHandler_Process_WithFollowing(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://ds.polis.pub"

	// Create a following.json with an entry
	followDir := filepath.Join(s.DataDir, "content", "pub.polis.core", "follow")
	_ = os.MkdirAll(followDir, 0755)
	followData := map[string]interface{}{
		"following": []map[string]interface{}{
			{"url": "https://alice.polis.pub"},
		},
	}
	data, _ := json.Marshal(followData)
	_ = os.WriteFile(filepath.Join(followDir, "following.json"), data, 0644)

	h := &feedSyncHandler{server: s}
	events := []discovery.StreamEvent{
		{
			Type:      "pub.polis.post.published",
			Actor:     "alice.polis.pub",
			Timestamp: "2026-03-28T10:00:00Z",
			Payload: map[string]interface{}{
				"source_domain": "alice.polis.pub",
				"source_url":    "https://alice.polis.pub/posts/20260328/test.md",
				"title":         "A test post",
			},
		},
	}
	result := h.Process(events)
	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}
	// The feed handler should have merged this item into the feed cache
	if result.NewItems < 1 {
		t.Error("expected at least 1 new feed item from followed author's post")
	}
}

func TestFeedSyncHandler_Process_IgnoresSelfEvents(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://ds.polis.pub"
	myDomain := extractDomainFromURL(s.BaseURL)

	// Create a following.json
	followDir := filepath.Join(s.DataDir, "content", "pub.polis.core", "follow")
	_ = os.MkdirAll(followDir, 0755)
	followData := map[string]interface{}{
		"following": []map[string]interface{}{
			{"url": "https://alice.polis.pub"},
		},
	}
	data, _ := json.Marshal(followData)
	_ = os.WriteFile(filepath.Join(followDir, "following.json"), data, 0644)

	h := &feedSyncHandler{server: s}
	// Event from ourselves (self-events are filtered out by the FeedHandler)
	events := []discovery.StreamEvent{
		{
			Type:      "pub.polis.post.published",
			Actor:     myDomain,
			Timestamp: "2026-03-28T10:00:00Z",
			Payload: map[string]interface{}{
				"source_domain": myDomain,
				"source_url":    "https://" + myDomain + "/posts/20260328/test.md",
				"title":         "My own post",
			},
		},
	}
	result := h.Process(events)
	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}
	// Self-events should be filtered out by the feed handler's legacy self-skip
	if result.NewItems != 0 {
		t.Errorf("expected 0 new feed items from self, got %d", result.NewItems)
	}
}

// ============================================================================
// Edge cases: empty events
// ============================================================================

func TestFeedSyncHandler_Process_EmptyEvents(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://ds.polis.pub"

	// Need following.json to get past the early return
	followDir := filepath.Join(s.DataDir, "content", "pub.polis.core", "follow")
	_ = os.MkdirAll(followDir, 0755)
	followData := map[string]interface{}{
		"following": []map[string]interface{}{
			{"url": "https://alice.polis.pub"},
		},
	}
	data, _ := json.Marshal(followData)
	_ = os.WriteFile(filepath.Join(followDir, "following.json"), data, 0644)

	h := &feedSyncHandler{server: s}
	result := h.Process([]discovery.StreamEvent{})
	if result.NewItems != 0 || result.Error != nil {
		t.Error("expected clean empty result for empty events")
	}
}

func TestFollowSyncHandler_Process_EmptyEvents(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://ds.polis.pub"
	h := &followSyncHandler{server: s}

	result := h.Process([]discovery.StreamEvent{})
	if result.FilesChanged || result.Error != nil {
		t.Error("expected clean empty result for empty events")
	}
}

func TestBlessingSyncHandler_Process_EmptyEvents(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://ds.polis.pub"
	h := &blessingSyncHandler{server: s}

	result := h.Process([]discovery.StreamEvent{})
	if result.FilesChanged || result.Error != nil {
		t.Error("expected clean empty result for empty events")
	}
}

func TestCommentStatusSyncHandler_Process_EmptyEvents(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://ds.polis.pub"
	h := &commentStatusSyncHandler{server: s}

	result := h.Process([]discovery.StreamEvent{})
	if result.FilesChanged || result.Error != nil {
		t.Error("expected clean empty result for empty events")
	}
}

// ============================================================================
// Multiple follow events in a single batch
// ============================================================================

func TestFollowSyncHandler_Process_MultipleFolowers(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://ds.polis.pub"
	myDomain := extractDomainFromURL(s.BaseURL)

	h := &followSyncHandler{server: s}
	events := []discovery.StreamEvent{
		{
			Type:  "pub.polis.follow.announced",
			Actor: "bob.polis.pub",
			Payload: map[string]interface{}{
				"target_domain": myDomain,
			},
		},
		{
			Type:  "pub.polis.follow.announced",
			Actor: "carol.polis.pub",
			Payload: map[string]interface{}{
				"target_domain": myDomain,
			},
		},
	}
	result := h.Process(events)
	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}
	if !result.FilesChanged {
		t.Error("expected FilesChanged=true with new followers")
	}
}


// ============================================================================
// broadcastCounts on zero-event sync
// ============================================================================

func TestBroadcastCounts_ZeroEvents_StillBroadcasts(t *testing.T) {
	s := newConfiguredServer(t)
	s.sseClients = make(map[chan SSEEvent]struct{})
	s.syncTrigger = make(chan struct{}, 1)

	// Register an SSE client
	ch := make(chan SSEEvent, 10)
	s.addSSEClient(ch)
	// Drain trigger from first-client connect
	select {
	case <-s.syncTrigger:
	default:
	}

	// Call broadcastCounts with an empty SyncResult (simulates 0-event sync)
	s.broadcastCounts(SyncResult{})

	// SSE client should receive a counts event
	select {
	case evt := <-ch:
		if evt.Event != "counts" {
			t.Errorf("expected event type 'counts', got %q", evt.Event)
		}
		var counts CountsPayload
		if err := json.Unmarshal([]byte(evt.Data), &counts); err != nil {
			t.Fatalf("failed to decode SSE counts data: %v", err)
		}
		// Verify we got valid counts data
		if counts.FeedUnread < 0 {
			t.Error("expected non-negative feed unread count in SSE broadcast")
		}
	default:
		t.Error("SSE client did not receive a counts event from broadcastCounts")
	}
}

// ============================================================================
// Verify handler registration patterns
// ============================================================================

func TestSyncHandlersImplementInterface(t *testing.T) {
	s := newTestServer(t)

	// Verify each handler satisfies the stream.SyncHandler interface
	var _ stream.SyncHandler = &feedSyncHandler{server: s}
	var _ stream.SyncHandler = &followSyncHandler{server: s}
	var _ stream.SyncHandler = &blessingSyncHandler{server: s}
	var _ stream.SyncHandler = &commentStatusSyncHandler{server: s}
}

// TestFeedSyncHandler_Process_TriggersExcerptKickOff — step-06/6.l.
// Source-level drift assertion: feedSyncHandler.Process must call
// kickOffExcerptFetch after a successful merge, so items arrive in
// the cache with excerpts populated rather than waiting for the
// dashboard-load fallback to fire. Catches a regression where the
// post-merge call gets dropped in a future refactor.
//
// (An integration test that exercises the actual fetch goroutine
// would require an HTTPS test server — kickOffExcerptFetch filters
// out non-https URLs as a security measure, and httptest.NewTLSServer
// requires SharedHTTPClient cert-trust setup that isn't worth the
// complexity for one wiring check. The idempotency test below covers
// the kickOff helper's filtering behavior end-to-end.)
func TestFeedSyncHandler_Process_TriggersExcerptKickOff(t *testing.T) {
	// Read the sync.go source and assert the call is wired in.
	syncSrc, err := os.ReadFile("sync.go")
	if err != nil {
		t.Fatalf("read sync.go: %v", err)
	}
	src := string(syncSrc)

	// Find the feedSyncHandler.Process function.
	procStart := strings.Index(src, "func (h *feedSyncHandler) Process(")
	if procStart == -1 {
		t.Fatal("feedSyncHandler.Process not found in sync.go")
	}
	// Look for kickOffExcerptFetch within the function body — bounded
	// search to ~80 lines of body content.
	end := procStart + 4000
	if end > len(src) {
		end = len(src)
	}
	body := src[procStart:end]
	if !strings.Contains(body, "kickOffExcerptFetch") {
		t.Errorf("feedSyncHandler.Process must call s.kickOffExcerptFetch after merge — sync-time excerpt fetch wiring missing")
	}
	// And it must be conditional on newCount > 0 — we don't want to
	// list the entire cache on every sync that produced no new items.
	if !strings.Contains(body, "newCount > 0") {
		t.Errorf("kickOffExcerptFetch should be guarded by `if newCount > 0` to avoid wasted cache reads")
	}
}

// TestExcerptFetchIndices — step-06/6.l review follow-up. The pure
// filter underlying kickOffExcerptFetch: returns indices of items
// that need an excerpt (missing Excerpt + post/comment + https URL).
// Tested directly without firing a goroutine — replaces the earlier
// sleep-and-poll idempotency test that depended on timing.
func TestExcerptFetchIndices(t *testing.T) {
	cases := []struct {
		name  string
		items []feed.CachedFeedItem
		want  []int
	}{
		{
			name: "needs excerpt — post + https + empty excerpt",
			items: []feed.CachedFeedItem{
				{Type: "post", URL: "https://alice.polis.pub/posts/foo.md"},
			},
			want: []int{0},
		},
		{
			name: "skips already-excerpted",
			items: []feed.CachedFeedItem{
				{Type: "post", URL: "https://alice.polis.pub/posts/foo.md", Excerpt: "already there"},
			},
			want: nil,
		},
		{
			name: "skips non-post / non-comment types",
			items: []feed.CachedFeedItem{
				{Type: "follow", URL: "https://alice.polis.pub/.well-known/polis"},
				{Type: "blessing", URL: "https://alice.polis.pub/comments/bar.md"},
			},
			want: nil,
		},
		{
			name: "skips http (security: only fetch over TLS)",
			items: []feed.CachedFeedItem{
				{Type: "post", URL: "http://insecure.example/posts/foo.md"},
			},
			want: nil,
		},
		{
			name: "comments included",
			items: []feed.CachedFeedItem{
				{Type: "comment", URL: "https://alice.polis.pub/comments/bar.md"},
			},
			want: []int{0},
		},
		{
			name: "mixed — only matching items returned",
			items: []feed.CachedFeedItem{
				{Type: "post", URL: "https://a.pub/p.md"},                          // index 0 — match
				{Type: "post", URL: "https://b.pub/p.md", Excerpt: "has it"},       // index 1 — has excerpt
				{Type: "follow", URL: "https://c.pub/wk"},                          // index 2 — wrong type
				{Type: "comment", URL: "https://d.pub/c.md"},                       // index 3 — match
				{Type: "post", URL: "http://e.pub/p.md"},                           // index 4 — http
			},
			want: []int{0, 3},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := excerptFetchIndices(tc.items)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("index %d: got %d, want %d", i, got[i], tc.want[i])
				}
			}
		})
	}
}
