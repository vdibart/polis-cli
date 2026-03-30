package clone

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
)

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"example.com", "https://example.com"},
		{"https://example.com", "https://example.com"},
		{"https://example.com/", "https://example.com"},
		{"http://example.com", "http://example.com"},
		{"http://example.com/", "http://example.com"},
	}

	for _, tt := range tests {
		result := normalizeURL(tt.input)
		if result != tt.expected {
			t.Errorf("normalizeURL(%q) = %q, expected %q", tt.input, result, tt.expected)
		}
	}
}

func TestExtractDomainForDir(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"https://example.com", "example.com"},
		{"https://example.com/", "example.com"},
		{"https://example.com/path", "example.com"},
		{"http://example.com", "example.com"},
		{"example.com", "example.com"},
		{"sub.example.com", "sub.example.com"},
	}

	for _, tt := range tests {
		result := ExtractDomainForDir(tt.input)
		if result != tt.expected {
			t.Errorf("ExtractDomainForDir(%q) = %q, expected %q", tt.input, result, tt.expected)
		}
	}
}

func TestURLToLocalPath(t *testing.T) {
	tests := []struct {
		remoteURL string
		baseURL   string
		localDir  string
		expected  string
	}{
		{
			"https://example.com/posts/2025/01/hello.md",
			"https://example.com",
			"/local/posts",
			"/local/posts/posts/2025/01/hello.md",
		},
	}

	for _, tt := range tests {
		result := urlToLocalPath(tt.remoteURL, tt.baseURL, tt.localDir)
		if result != tt.expected {
			t.Errorf("urlToLocalPath() = %q, expected %q", result, tt.expected)
		}
	}
}

// newTestServer creates a mock polis site HTTP server. The handler routes
// requests based on path and uses the provided baseURL in index entries so
// the clone client fetches from the test server.
func newTestServer(t *testing.T, opts testSiteOpts) *httptest.Server {
	t.Helper()
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/polis":
			wk := remote.WellKnown{
				Version:   "1.0",
				Author:    "testauthor",
				PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest",
				Created:   "2025-01-01T00:00:00Z",
				SiteTitle: "Test Site",
			}
			json.NewEncoder(w).Encode(wk)

		case "/content/pub.polis.core/index.jsonl":
			if opts.indexError {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			for _, entry := range opts.indexEntries {
				// Replace placeholder base URL with actual test server URL
				entry.URL = strings.Replace(entry.URL, "BASEURL", ts.URL, 1)
				data, _ := json.Marshal(entry)
				fmt.Fprintln(w, string(data))
			}

		case "/content/pub.polis.core/comment/blessed.json":
			if opts.blessedJSON != "" {
				fmt.Fprint(w, opts.blessedJSON)
			} else {
				http.NotFound(w, r)
			}

		case "/content/pub.polis.core/follow/following.json":
			if opts.followingJSON != "" {
				fmt.Fprint(w, opts.followingJSON)
			} else {
				http.NotFound(w, r)
			}

		default:
			// Serve post content if it exists in the posts map
			if content, ok := opts.postContent[r.URL.Path]; ok {
				fmt.Fprint(w, content)
			} else {
				http.NotFound(w, r)
			}
		}
	}))
	return ts
}

type testSiteOpts struct {
	indexEntries []remote.PublicIndexEntry
	postContent  map[string]string // path -> content
	blessedJSON  string
	followingJSON string
	indexError   bool
}

func TestClone_SuccessfulFullClone(t *testing.T) {
	posts := map[string]string{
		"/content/pub.polis.core/post/2025/01/hello.md": "# Hello\n\nFirst post.",
		"/content/pub.polis.core/post/2025/02/world.md": "# World\n\nSecond post.",
	}

	ts := newTestServer(t, testSiteOpts{
		indexEntries: []remote.PublicIndexEntry{
			{Type: "post", Title: "Hello", URL: "BASEURL/content/pub.polis.core/post/2025/01/hello.md", Published: "2025-01-15T10:00:00Z"},
			{Type: "post", Title: "World", URL: "BASEURL/content/pub.polis.core/post/2025/02/world.md", Published: "2025-02-20T10:00:00Z"},
		},
		postContent: posts,
	})
	defer ts.Close()

	targetDir := t.TempDir()
	result, err := Clone(ts.URL, targetDir, CloneOptions{FullClone: true})
	if err != nil {
		t.Fatalf("Clone() unexpected error: %v", err)
	}

	if result.PostsDownloaded != 2 {
		t.Errorf("PostsDownloaded = %d, want 2", result.PostsDownloaded)
	}
	if result.Errors != 0 {
		t.Errorf("Errors = %d, want 0", result.Errors)
	}
	if result.TargetDir != targetDir {
		t.Errorf("TargetDir = %q, want %q", result.TargetDir, targetDir)
	}

	// Verify .well-known/polis was written
	wkPath := filepath.Join(targetDir, ".well-known", "polis")
	wkData, err := os.ReadFile(wkPath)
	if err != nil {
		t.Fatalf("failed to read .well-known/polis: %v", err)
	}
	if !strings.Contains(string(wkData), "testauthor") {
		t.Error(".well-known/polis does not contain expected author")
	}

	// Verify index.jsonl was written
	indexPath := filepath.Join(targetDir, "content", "pub.polis.core", "index.jsonl")
	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("failed to read index.jsonl: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(indexData)), "\n")
	if len(lines) != 2 {
		t.Errorf("index.jsonl has %d lines, want 2", len(lines))
	}

	// Verify clone state was saved
	statePath := filepath.Join(targetDir, ".polis-clone-state.json")
	stateData, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("failed to read clone state: %v", err)
	}
	var state CloneState
	if err := json.Unmarshal(stateData, &state); err != nil {
		t.Fatalf("failed to parse clone state: %v", err)
	}
	if state.PostsCount != 2 {
		t.Errorf("state.PostsCount = %d, want 2", state.PostsCount)
	}
	if state.SourceURL != ts.URL {
		t.Errorf("state.SourceURL = %q, want %q", state.SourceURL, ts.URL)
	}
}

func TestClone_DirectoryStructure(t *testing.T) {
	ts := newTestServer(t, testSiteOpts{
		indexEntries: []remote.PublicIndexEntry{
			{Type: "post", Title: "Hello", URL: "BASEURL/content/pub.polis.core/post/2025/01/hello.md", Published: "2025-01-15T10:00:00Z"},
		},
		postContent: map[string]string{
			"/content/pub.polis.core/post/2025/01/hello.md": "# Hello",
		},
	})
	defer ts.Close()

	targetDir := t.TempDir()
	_, err := Clone(ts.URL, targetDir, CloneOptions{FullClone: true})
	if err != nil {
		t.Fatalf("Clone() unexpected error: %v", err)
	}

	// Verify expected directories exist
	expectedDirs := []string{
		".well-known",
		"content/pub.polis.core",
		"content/pub.polis.core/comment",
		"content/pub.polis.core/follow",
	}
	for _, dir := range expectedDirs {
		fullPath := filepath.Join(targetDir, dir)
		info, err := os.Stat(fullPath)
		if err != nil {
			t.Errorf("expected directory %q to exist: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("expected %q to be a directory", dir)
		}
	}
}

func TestClone_PostContentWritten(t *testing.T) {
	postBody := "# My Post\n\nSome content here."
	ts := newTestServer(t, testSiteOpts{
		indexEntries: []remote.PublicIndexEntry{
			{Type: "post", Title: "My Post", URL: "BASEURL/content/pub.polis.core/post/2025/03/my-post.md", Published: "2025-03-01T10:00:00Z"},
		},
		postContent: map[string]string{
			"/content/pub.polis.core/post/2025/03/my-post.md": postBody,
		},
	})
	defer ts.Close()

	targetDir := t.TempDir()
	_, err := Clone(ts.URL, targetDir, CloneOptions{FullClone: true})
	if err != nil {
		t.Fatalf("Clone() unexpected error: %v", err)
	}

	// The post URL relative to baseURL is "content/pub.polis.core/post/2025/03/my-post.md"
	// urlToLocalPath strips the base URL, so the file goes under postsDir with the remainder
	// postsDir = targetDir/content/pub.polis.core/post
	// remainder after stripping baseURL+"/": "content/pub.polis.core/post/2025/03/my-post.md"
	// final path: postsDir/content/pub.polis.core/post/2025/03/my-post.md
	// This is a quirk of the current implementation - URL path gets appended to postsDir
	postPath := filepath.Join(targetDir, "content", "pub.polis.core", "post",
		"content", "pub.polis.core", "post", "2025", "03", "my-post.md")
	data, err := os.ReadFile(postPath)
	if err != nil {
		t.Fatalf("failed to read cloned post: %v", err)
	}
	if string(data) != postBody {
		t.Errorf("post content = %q, want %q", string(data), postBody)
	}
}

func TestClone_BlessedCommentsDownloaded(t *testing.T) {
	blessedJSON := `{"comments":[{"post":"hello","blessed":[{"author":"alice","body":"great"},{"author":"bob","body":"nice"}]},{"post":"world","blessed":[{"author":"carol","body":"wow"}]}]}`
	ts := newTestServer(t, testSiteOpts{
		indexEntries: []remote.PublicIndexEntry{},
		postContent:  map[string]string{},
		blessedJSON:  blessedJSON,
	})
	defer ts.Close()

	targetDir := t.TempDir()
	result, err := Clone(ts.URL, targetDir, CloneOptions{FullClone: true})
	if err != nil {
		t.Fatalf("Clone() unexpected error: %v", err)
	}

	if result.BlessedCommentsSynced != 3 {
		t.Errorf("BlessedCommentsSynced = %d, want 3", result.BlessedCommentsSynced)
	}

	// Verify blessed.json was written
	blessedPath := filepath.Join(targetDir, "content", "pub.polis.core", "comment", "blessed.json")
	data, err := os.ReadFile(blessedPath)
	if err != nil {
		t.Fatalf("failed to read blessed.json: %v", err)
	}
	if string(data) != blessedJSON {
		t.Error("blessed.json content does not match")
	}
}

func TestClone_FollowingDownloaded(t *testing.T) {
	followJSON := `{"version":"1.0","following":[{"url":"https://alice.com"},{"url":"https://bob.com"}]}`
	ts := newTestServer(t, testSiteOpts{
		indexEntries:  []remote.PublicIndexEntry{},
		postContent:   map[string]string{},
		followingJSON: followJSON,
	})
	defer ts.Close()

	targetDir := t.TempDir()
	_, err := Clone(ts.URL, targetDir, CloneOptions{FullClone: true})
	if err != nil {
		t.Fatalf("Clone() unexpected error: %v", err)
	}

	followPath := filepath.Join(targetDir, "content", "pub.polis.core", "follow", "following.json")
	data, err := os.ReadFile(followPath)
	if err != nil {
		t.Fatalf("failed to read following.json: %v", err)
	}
	if string(data) != followJSON {
		t.Error("following.json content does not match")
	}
}

func TestClone_WellKnownFetchFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer ts.Close()

	targetDir := t.TempDir()
	_, err := Clone(ts.URL, targetDir, CloneOptions{FullClone: true})
	if err == nil {
		t.Fatal("Clone() expected error when .well-known/polis fails, got nil")
	}
	if !strings.Contains(err.Error(), ".well-known/polis") {
		t.Errorf("error should mention .well-known/polis, got: %v", err)
	}
}

func TestClone_IndexFetchFailure(t *testing.T) {
	ts := newTestServer(t, testSiteOpts{
		indexError: true,
	})
	defer ts.Close()

	targetDir := t.TempDir()
	_, err := Clone(ts.URL, targetDir, CloneOptions{FullClone: true})
	if err == nil {
		t.Fatal("Clone() expected error when index.jsonl fails, got nil")
	}
	if !strings.Contains(err.Error(), "index.jsonl") {
		t.Errorf("error should mention index.jsonl, got: %v", err)
	}
}

func TestClone_PostFetchError_CountedNotFatal(t *testing.T) {
	ts := newTestServer(t, testSiteOpts{
		indexEntries: []remote.PublicIndexEntry{
			{Type: "post", Title: "Good", URL: "BASEURL/content/pub.polis.core/post/good.md", Published: "2025-01-01T10:00:00Z"},
			{Type: "post", Title: "Missing", URL: "BASEURL/content/pub.polis.core/post/missing.md", Published: "2025-01-02T10:00:00Z"},
		},
		postContent: map[string]string{
			"/content/pub.polis.core/post/good.md": "# Good post",
			// missing.md intentionally absent - will 404
		},
	})
	defer ts.Close()

	targetDir := t.TempDir()
	result, err := Clone(ts.URL, targetDir, CloneOptions{FullClone: true})
	if err != nil {
		t.Fatalf("Clone() should not return error for partial post failures: %v", err)
	}
	if result.PostsDownloaded != 1 {
		t.Errorf("PostsDownloaded = %d, want 1", result.PostsDownloaded)
	}
	if result.Errors != 1 {
		t.Errorf("Errors = %d, want 1", result.Errors)
	}
}

func TestClone_NonPostEntriesSkipped(t *testing.T) {
	ts := newTestServer(t, testSiteOpts{
		indexEntries: []remote.PublicIndexEntry{
			{Type: "post", Title: "A Post", URL: "BASEURL/content/pub.polis.core/post/a.md", Published: "2025-01-01T10:00:00Z"},
			{Type: "page", Title: "A Page", URL: "BASEURL/content/pub.polis.core/page/about.md", Published: "2025-01-01T10:00:00Z"},
			{Type: "snippet", Title: "A Snippet", URL: "BASEURL/content/pub.polis.core/snippet/bio.md", Published: "2025-01-01T10:00:00Z"},
		},
		postContent: map[string]string{
			"/content/pub.polis.core/post/a.md": "# A Post",
		},
	})
	defer ts.Close()

	targetDir := t.TempDir()
	result, err := Clone(ts.URL, targetDir, CloneOptions{FullClone: true})
	if err != nil {
		t.Fatalf("Clone() unexpected error: %v", err)
	}
	if result.PostsDownloaded != 1 {
		t.Errorf("PostsDownloaded = %d, want 1 (non-post types should be skipped)", result.PostsDownloaded)
	}
	if result.Errors != 0 {
		t.Errorf("Errors = %d, want 0", result.Errors)
	}
}

func TestClone_IncrementalSkipsOldPosts(t *testing.T) {
	ts := newTestServer(t, testSiteOpts{
		indexEntries: []remote.PublicIndexEntry{
			{Type: "post", Title: "Old", URL: "BASEURL/content/pub.polis.core/post/old.md", Published: "2025-01-01T10:00:00Z"},
			{Type: "post", Title: "New", URL: "BASEURL/content/pub.polis.core/post/new.md", Published: "2025-06-01T10:00:00Z"},
		},
		postContent: map[string]string{
			"/content/pub.polis.core/post/old.md": "# Old",
			"/content/pub.polis.core/post/new.md": "# New",
		},
	})
	defer ts.Close()

	targetDir := t.TempDir()

	// Pre-seed a clone state with a timestamp between old and new posts
	statePath := filepath.Join(targetDir, ".polis-clone-state.json")
	state := CloneState{
		SourceURL:   ts.URL,
		ClonedAt:    "2025-01-01T00:00:00Z",
		LastUpdated: "2025-03-01T00:00:00Z",
		PostsCount:  1,
	}
	stateData, _ := json.MarshalIndent(state, "", "  ")
	os.WriteFile(statePath, append(stateData, '\n'), 0644)

	result, err := Clone(ts.URL, targetDir, CloneOptions{FullClone: false})
	if err != nil {
		t.Fatalf("Clone() unexpected error: %v", err)
	}

	// Old post (published 2025-01-01) should be skipped because it's <= LastUpdated (2025-03-01)
	// New post (published 2025-06-01) should be downloaded
	if result.PostsDownloaded != 1 {
		t.Errorf("PostsDownloaded = %d, want 1 (old post should be skipped in incremental mode)", result.PostsDownloaded)
	}

	// Verify cumulative post count in updated state
	updatedState, err := loadState(statePath)
	if err != nil {
		t.Fatalf("failed to load updated state: %v", err)
	}
	if updatedState.PostsCount != 2 {
		t.Errorf("state.PostsCount = %d, want 2 (1 old + 1 new)", updatedState.PostsCount)
	}
	// ClonedAt should be preserved from original state
	if updatedState.ClonedAt != "2025-01-01T00:00:00Z" {
		t.Errorf("state.ClonedAt = %q, want preserved original", updatedState.ClonedAt)
	}
}

func TestClone_FullCloneIgnoresExistingState(t *testing.T) {
	ts := newTestServer(t, testSiteOpts{
		indexEntries: []remote.PublicIndexEntry{
			{Type: "post", Title: "Old", URL: "BASEURL/content/pub.polis.core/post/old.md", Published: "2025-01-01T10:00:00Z"},
		},
		postContent: map[string]string{
			"/content/pub.polis.core/post/old.md": "# Old",
		},
	})
	defer ts.Close()

	targetDir := t.TempDir()

	// Pre-seed a clone state that would normally cause the old post to be skipped
	statePath := filepath.Join(targetDir, ".polis-clone-state.json")
	state := CloneState{
		SourceURL:   ts.URL,
		ClonedAt:    "2025-01-01T00:00:00Z",
		LastUpdated: "2025-12-31T00:00:00Z",
		PostsCount:  1,
	}
	stateData, _ := json.MarshalIndent(state, "", "  ")
	os.WriteFile(statePath, append(stateData, '\n'), 0644)

	result, err := Clone(ts.URL, targetDir, CloneOptions{FullClone: true})
	if err != nil {
		t.Fatalf("Clone() unexpected error: %v", err)
	}

	// FullClone should ignore the state and re-download everything
	if result.PostsDownloaded != 1 {
		t.Errorf("PostsDownloaded = %d, want 1 (full clone should ignore state)", result.PostsDownloaded)
	}
}

func TestClone_EmptyIndex(t *testing.T) {
	ts := newTestServer(t, testSiteOpts{
		indexEntries: []remote.PublicIndexEntry{},
		postContent:  map[string]string{},
	})
	defer ts.Close()

	targetDir := t.TempDir()
	result, err := Clone(ts.URL, targetDir, CloneOptions{FullClone: true})
	if err != nil {
		t.Fatalf("Clone() unexpected error: %v", err)
	}
	if result.PostsDownloaded != 0 {
		t.Errorf("PostsDownloaded = %d, want 0", result.PostsDownloaded)
	}
	if result.Errors != 0 {
		t.Errorf("Errors = %d, want 0", result.Errors)
	}
}

func TestClone_MissingBlessedAndFollowing_NotFatal(t *testing.T) {
	// Neither blessed.json nor following.json exist (404)
	ts := newTestServer(t, testSiteOpts{
		indexEntries: []remote.PublicIndexEntry{},
		postContent:  map[string]string{},
		// blessedJSON and followingJSON left empty -> 404
	})
	defer ts.Close()

	targetDir := t.TempDir()
	result, err := Clone(ts.URL, targetDir, CloneOptions{FullClone: true})
	if err != nil {
		t.Fatalf("Clone() should not fail when blessed/following are missing: %v", err)
	}
	if result.BlessedCommentsSynced != 0 {
		t.Errorf("BlessedCommentsSynced = %d, want 0", result.BlessedCommentsSynced)
	}
}

func TestClone_InvalidServerURL(t *testing.T) {
	// Use a URL that will fail to connect
	targetDir := t.TempDir()
	_, err := Clone("http://127.0.0.1:1", targetDir, CloneOptions{FullClone: true})
	if err == nil {
		t.Fatal("Clone() expected error for unreachable server, got nil")
	}
}

func TestClone_CreatesTargetDirectory(t *testing.T) {
	ts := newTestServer(t, testSiteOpts{
		indexEntries: []remote.PublicIndexEntry{},
		postContent:  map[string]string{},
	})
	defer ts.Close()

	// Use a non-existent subdirectory
	baseDir := t.TempDir()
	targetDir := filepath.Join(baseDir, "nested", "clone", "dir")

	_, err := Clone(ts.URL, targetDir, CloneOptions{FullClone: true})
	if err != nil {
		t.Fatalf("Clone() unexpected error: %v", err)
	}

	info, err := os.Stat(targetDir)
	if err != nil {
		t.Fatalf("target directory was not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("target path is not a directory")
	}
}

func TestLoadState_NonExistent(t *testing.T) {
	_, err := loadState(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err == nil {
		t.Error("loadState() expected error for non-existent file, got nil")
	}
}

func TestLoadState_InvalidJSON(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(tmpFile, []byte("not json"), 0644)
	_, err := loadState(tmpFile)
	if err == nil {
		t.Error("loadState() expected error for invalid JSON, got nil")
	}
}

func TestSaveAndLoadState_Roundtrip(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	original := &CloneState{
		SourceURL:   "https://example.com",
		ClonedAt:    "2025-01-01T00:00:00Z",
		LastUpdated: "2025-06-15T12:00:00Z",
		PostsCount:  42,
	}

	if err := saveState(statePath, original); err != nil {
		t.Fatalf("saveState() error: %v", err)
	}

	loaded, err := loadState(statePath)
	if err != nil {
		t.Fatalf("loadState() error: %v", err)
	}

	if loaded.SourceURL != original.SourceURL {
		t.Errorf("SourceURL = %q, want %q", loaded.SourceURL, original.SourceURL)
	}
	if loaded.ClonedAt != original.ClonedAt {
		t.Errorf("ClonedAt = %q, want %q", loaded.ClonedAt, original.ClonedAt)
	}
	if loaded.LastUpdated != original.LastUpdated {
		t.Errorf("LastUpdated = %q, want %q", loaded.LastUpdated, original.LastUpdated)
	}
	if loaded.PostsCount != original.PostsCount {
		t.Errorf("PostsCount = %d, want %d", loaded.PostsCount, original.PostsCount)
	}
}
