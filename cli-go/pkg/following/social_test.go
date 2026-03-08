package following

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
)

// testPolicies returns the default public policies for testing.
func testPolicies(t *testing.T) []policy.Policy {
	t.Helper()
	dir := t.TempDir()
	pubPath := filepath.Join(dir, "public.jsonl")
	os.WriteFile(pubPath, []byte(policy.DefaultPublicPolicyContent()), 0644)
	policies, err := policy.LoadPolicies("/nonexistent", pubPath)
	if err != nil {
		t.Fatalf("failed to load test policies: %v", err)
	}
	return policies
}

// mockDiscoveryServer creates a minimal discovery service mock.
func mockDiscoveryServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Return empty arrays for comment queries, success for grant/deny
		json.NewEncoder(w).Encode([]interface{}{})
	}))
}

// mockRemoteSite creates a mock polis site that serves .well-known/polis.
func mockRemoteSite(t *testing.T, email string) *httptest.Server {
	t.Helper()
	wk := map[string]interface{}{
		"email":      email,
		"public_key": "ssh-ed25519 AAAA...",
		"author":     "Test Author",
	}
	wkJSON, _ := json.Marshal(wk)

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/polis" {
			w.Header().Set("Content-Type", "application/json")
			w.Write(wkJSON)
		} else {
			http.NotFound(w, r)
		}
	}))
}

func TestFollowWithBlessing_AddsToFollowing(t *testing.T) {
	remoteSite := mockRemoteSite(t, "author@example.com")
	defer remoteSite.Close()

	discoverySrv := mockDiscoveryServer(t)
	defer discoverySrv.Close()

	tmpDir := t.TempDir()
	followingPath := filepath.Join(tmpDir, "following.json")

	discoveryClient := discovery.NewClient(discoverySrv.URL, "test-key")
	remoteClient := remote.NewClient()

	result, err := FollowWithBlessing(followingPath, remoteSite.URL, discoveryClient, remoteClient, []byte("fake-key"), testPolicies(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.AuthorURL != remoteSite.URL {
		t.Errorf("expected author_url %q, got %q", remoteSite.URL, result.AuthorURL)
	}
	if result.AuthorEmail != "author@example.com" {
		t.Errorf("expected author_email 'author@example.com', got %q", result.AuthorEmail)
	}
	if result.AlreadyFollowed {
		t.Error("expected AlreadyFollowed=false for new follow")
	}

	// Verify following.json was updated
	f, err := Load(followingPath)
	if err != nil {
		t.Fatalf("failed to load following.json: %v", err)
	}
	if !f.IsFollowing(remoteSite.URL) {
		t.Error("expected author to be in following list")
	}
}

func TestFollowWithBlessing_AlreadyFollowed(t *testing.T) {
	remoteSite := mockRemoteSite(t, "author@example.com")
	defer remoteSite.Close()

	discoverySrv := mockDiscoveryServer(t)
	defer discoverySrv.Close()

	tmpDir := t.TempDir()
	followingPath := filepath.Join(tmpDir, "following.json")

	discoveryClient := discovery.NewClient(discoverySrv.URL, "test-key")
	remoteClient := remote.NewClient()

	// Follow first time
	_, err := FollowWithBlessing(followingPath, remoteSite.URL, discoveryClient, remoteClient, []byte("fake-key"), testPolicies(t))
	if err != nil {
		t.Fatalf("first follow failed: %v", err)
	}

	// Follow second time
	result, err := FollowWithBlessing(followingPath, remoteSite.URL, discoveryClient, remoteClient, []byte("fake-key"), testPolicies(t))
	if err != nil {
		t.Fatalf("second follow failed: %v", err)
	}
	if !result.AlreadyFollowed {
		t.Error("expected AlreadyFollowed=true for duplicate follow")
	}
}

func TestUnfollowWithDenial_RemovesFromFollowing(t *testing.T) {
	remoteSite := mockRemoteSite(t, "author@example.com")
	defer remoteSite.Close()

	discoverySrv := mockDiscoveryServer(t)
	defer discoverySrv.Close()

	tmpDir := t.TempDir()
	followingPath := filepath.Join(tmpDir, "following.json")

	// Add author first
	f := &FollowingFile{Version: Version, Following: []FollowingEntry{}}
	f.Add(remoteSite.URL)
	Save(followingPath, f)

	discoveryClient := discovery.NewClient(discoverySrv.URL, "test-key")
	remoteClient := remote.NewClient()

	result, err := UnfollowWithDenial(followingPath, remoteSite.URL, discoveryClient, remoteClient, []byte("fake-key"), testPolicies(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.WasFollowing {
		t.Error("expected WasFollowing=true")
	}

	// Verify removed from following.json
	f, err = Load(followingPath)
	if err != nil {
		t.Fatalf("failed to load following.json: %v", err)
	}
	if f.IsFollowing(remoteSite.URL) {
		t.Error("expected author to be removed from following list")
	}
}

func TestUnfollowWithDenial_NotFollowing(t *testing.T) {
	remoteSite := mockRemoteSite(t, "author@example.com")
	defer remoteSite.Close()

	discoverySrv := mockDiscoveryServer(t)
	defer discoverySrv.Close()

	tmpDir := t.TempDir()
	followingPath := filepath.Join(tmpDir, "following.json")

	discoveryClient := discovery.NewClient(discoverySrv.URL, "test-key")
	remoteClient := remote.NewClient()

	result, err := UnfollowWithDenial(followingPath, remoteSite.URL, discoveryClient, remoteClient, []byte("fake-key"), testPolicies(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.WasFollowing {
		t.Error("expected WasFollowing=false for author not in following list")
	}
}

func TestFollowWithBlessing_UnreachableSite(t *testing.T) {
	discoverySrv := mockDiscoveryServer(t)
	defer discoverySrv.Close()

	tmpDir := t.TempDir()
	followingPath := filepath.Join(tmpDir, "following.json")

	discoveryClient := discovery.NewClient(discoverySrv.URL, "test-key")
	remoteClient := remote.NewClient()

	_, err := FollowWithBlessing(followingPath, "https://127.0.0.1:1", discoveryClient, remoteClient, []byte("fake-key"), testPolicies(t))
	if err == nil {
		t.Error("expected error for unreachable site")
	}
}
