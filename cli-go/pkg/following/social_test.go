package following

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
)

// testKeypair returns a real (privatePEM, publicSSH) pair. The follow/unfollow
// paths SIGN following.json (SIGNET epic 02), so a placeholder key no longer
// gets these tests through — which is the point: they now exercise the signing
// write path rather than stepping around it.
func testKeypair(t *testing.T) ([]byte, []byte) {
	t.Helper()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	return priv, pub
}

func testPrivKey(t *testing.T) []byte {
	t.Helper()
	priv, _ := testKeypair(t)
	return priv
}

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

	result, err := FollowWithBlessing(followingPath, remoteSite.URL, discoveryClient, remoteClient, testPrivKey(t), testPolicies(t))
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

// TestFollowWithBlessing_SignsTheFile is the "write path signs" guarantee that
// makes epic 02 need no migration: a follow the user performed is signed
// BECAUSE THEY PERFORMED IT. If this assertion ever goes quiet, the epic
// silently degrades to "new field, nobody fills it in".
func TestFollowWithBlessing_SignsTheFile(t *testing.T) {
	remoteSite := mockRemoteSite(t, "author@example.com")
	defer remoteSite.Close()

	discoverySrv := mockDiscoveryServer(t)
	defer discoverySrv.Close()

	followingPath := filepath.Join(t.TempDir(), "following.json")
	priv, pub := testKeypair(t)

	if _, err := FollowWithBlessing(followingPath, remoteSite.URL,
		discovery.NewClient(discoverySrv.URL, "test-key"), remote.NewClient(),
		priv, testPolicies(t)); err != nil {
		t.Fatalf("follow: %v", err)
	}

	f, err := Load(followingPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	status, verr := Verify(f, pub)
	if status != StatusValid {
		t.Fatalf("follow wrote a %q file, want %q (%v)", status, StatusValid, verr)
	}

	// And unfollowing re-signs over the new roster rather than leaving the old
	// signature stranded over content it no longer covers.
	if _, err := UnfollowWithDenial(followingPath, remoteSite.URL,
		discovery.NewClient(discoverySrv.URL, "test-key"), remote.NewClient(),
		priv, testPolicies(t)); err != nil {
		t.Fatalf("unfollow: %v", err)
	}
	f, err = Load(followingPath)
	if err != nil {
		t.Fatalf("load after unfollow: %v", err)
	}
	if status, verr := Verify(f, pub); status != StatusValid {
		t.Errorf("unfollow left a %q file, want %q (%v)", status, StatusValid, verr)
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
	_, err := FollowWithBlessing(followingPath, remoteSite.URL, discoveryClient, remoteClient, testPrivKey(t), testPolicies(t))
	if err != nil {
		t.Fatalf("first follow failed: %v", err)
	}

	// Follow second time
	result, err := FollowWithBlessing(followingPath, remoteSite.URL, discoveryClient, remoteClient, testPrivKey(t), testPolicies(t))
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

	result, err := UnfollowWithDenial(followingPath, remoteSite.URL, discoveryClient, remoteClient, testPrivKey(t), testPolicies(t))
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

	result, err := UnfollowWithDenial(followingPath, remoteSite.URL, discoveryClient, remoteClient, testPrivKey(t), testPolicies(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.WasFollowing {
		t.Error("expected WasFollowing=false for author not in following list")
	}
}

// TestFollowWithBlessing_AnnouncesWithConfigDespiteCorruptGlobals is the
// regression for the interactive-follow bug: a follow triggered from the SPA
// (handleFollowing → FollowWithBlessing) failed to announce to the DS on the
// hosted service because it relied on the package-level globals
// (stream.BaseURL/DataDir, following.DataDir/DiscoveryURL). In the multi-tenant
// hosted process those globals are last-writer-wins across tenants, so the
// announce either targeted the wrong domain or found no registration marker and
// was silently suppressed (the Axiom clerk.parity drift alerts).
//
// The fix threads a per-tenant FollowConfig through to stream.PublishEvent. This
// test sets the globals to a BOGUS tenant, then verifies a follow with an
// explicit FollowConfig still reaches the DS /v1/stream announce endpoint.
func TestFollowWithBlessing_AnnouncesWithConfigDespiteCorruptGlobals(t *testing.T) {
	remoteSite := mockRemoteSite(t, "author@example.com")
	defer remoteSite.Close()

	var mu sync.Mutex
	announced := false
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/stream" {
			mu.Lock()
			announced = true
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]interface{}{})
	}))
	defer ds.Close()

	// This tenant's real data dir, with a registration marker under the DS domain.
	siteDir := t.TempDir()
	followingPath := filepath.Join(siteDir, "following.json")
	if err := discovery.WriteRegistrationMarker(siteDir, ds.URL, "alice.polis.pub", ""); err != nil {
		t.Fatalf("WriteRegistrationMarker: %v", err)
	}

	// Corrupt the package globals to mimic another tenant having initialized last
	// in the shared hosted process. A correct FollowWithBlessing must ignore these.
	defer func(dd, du, sbu, sdd string) {
		DataDir, DiscoveryURL = dd, du
		stream.BaseURL, stream.DataDir = sbu, sdd
	}(DataDir, DiscoveryURL, stream.BaseURL, stream.DataDir)
	DataDir = "/bogus/other-tenant"
	DiscoveryURL = "https://wrong-ds.example"
	stream.BaseURL = "https://bob.polis.pub"
	stream.DataDir = "/bogus/other-tenant"

	priv, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	cfg := &FollowConfig{
		DataDir:      siteDir,
		DiscoveryURL: ds.URL,
		DiscoveryKey: "test-key",
		BaseURL:      "https://alice.polis.pub",
	}

	_, err = FollowWithBlessing(followingPath, remoteSite.URL,
		discovery.NewClient(ds.URL, "test-key"), remote.NewClient(),
		priv, testPolicies(t), cfg)
	if err != nil {
		t.Fatalf("FollowWithBlessing: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !announced {
		t.Fatal("follow.announced was not sent to the DS — FollowWithBlessing must use the " +
			"per-tenant FollowConfig (BaseURL/DataDir/DiscoveryURL), not the corrupt package globals")
	}
}

func TestFollowWithBlessing_UnreachableSite(t *testing.T) {
	discoverySrv := mockDiscoveryServer(t)
	defer discoverySrv.Close()

	tmpDir := t.TempDir()
	followingPath := filepath.Join(tmpDir, "following.json")

	discoveryClient := discovery.NewClient(discoverySrv.URL, "test-key")
	remoteClient := remote.NewClient()

	_, err := FollowWithBlessing(followingPath, "https://127.0.0.1:1", discoveryClient, remoteClient, testPrivKey(t), testPolicies(t))
	if err == nil {
		t.Error("expected error for unreachable site")
	}
}
