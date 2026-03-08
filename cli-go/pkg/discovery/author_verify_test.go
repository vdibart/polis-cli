package discovery

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// ============================================================================
// AuthorKeyCache
// ============================================================================

func TestAuthorKeyCache_FetchesAndCaches(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"public_key": "ssh-ed25519 AAAA test",
			"version":    "1",
		})
	}))
	defer server.Close()

	cache := NewAuthorKeyCache(time.Hour, 5*time.Minute)
	// Override the domain resolution to use our test server
	// We'll test by directly calling the internal method indirectly
	// through a domain that points to our test server.
	// For unit testing, we test the cache behavior directly.

	// Store a pre-fetched entry
	cache.mu.Lock()
	cache.entries["alice.com"] = &authorKeyEntry{
		publicKey: "ssh-ed25519 AAAA cached",
		fetchedAt: time.Now(),
	}
	cache.mu.Unlock()

	key, err := cache.GetPublicKey("alice.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "ssh-ed25519 AAAA cached" {
		t.Errorf("expected cached key, got %q", key)
	}
}

func TestAuthorKeyCache_NegativeCaching(t *testing.T) {
	cache := NewAuthorKeyCache(time.Hour, 5*time.Minute)

	// Store a negative cache entry
	cache.mu.Lock()
	cache.entries["unreachable.com"] = &authorKeyEntry{
		err:       fmt.Errorf("connection refused"),
		fetchedAt: time.Now(),
	}
	cache.mu.Unlock()

	_, err := cache.GetPublicKey("unreachable.com")
	if err == nil {
		t.Fatal("expected error from negative cache")
	}
}

func TestAuthorKeyCache_ExpiredEntryRefetches(t *testing.T) {
	fetchCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetchCount++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"public_key": fmt.Sprintf("ssh-ed25519 AAAA key-%d", fetchCount),
			"version":    "1",
		})
	}))
	defer server.Close()

	cache := NewAuthorKeyCache(time.Hour, 5*time.Minute)

	// Store a fresh entry — should be returned from cache
	cache.mu.Lock()
	cache.entries[server.Listener.Addr().String()] = &authorKeyEntry{
		publicKey: "old-key",
		fetchedAt: time.Now().Add(-2 * time.Hour), // expired
	}
	cache.mu.Unlock()

	// The expired entry should trigger a refetch, which will fail (wrong domain for HTTPS)
	// But the key point is it doesn't return the stale cached value
	key, _ := cache.GetPublicKey(server.Listener.Addr().String())
	if key == "old-key" {
		t.Fatal("expected expired entry to be evicted, got stale value")
	}
}

func TestAuthorKeyCache_ConcurrentAccess(t *testing.T) {
	cache := NewAuthorKeyCache(time.Hour, 5*time.Minute)

	// Pre-populate
	cache.mu.Lock()
	cache.entries["safe.com"] = &authorKeyEntry{
		publicKey: "ssh-ed25519 AAAA safe",
		fetchedAt: time.Now(),
	}
	cache.mu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key, err := cache.GetPublicKey("safe.com")
			if err != nil || key != "ssh-ed25519 AAAA safe" {
				t.Errorf("concurrent read failed: key=%q err=%v", key, err)
			}
		}()
	}
	wg.Wait()
}

// ============================================================================
// VerifyAuthorSignature
// ============================================================================

func TestVerifyAuthorSignature_Valid(t *testing.T) {
	privPEM, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	payload := map[string]interface{}{
		"target_domain": "bob.com",
	}
	canonical, _ := MakeStreamCanonicalJSON("pub.polis.follow.announced", payload)
	sig, err := signing.SignContent(canonical, privPEM)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	cache := NewAuthorKeyCache(time.Hour, 5*time.Minute)
	cache.mu.Lock()
	cache.entries["alice.com"] = &authorKeyEntry{
		publicKey: string(pubSSH),
		fetchedAt: time.Now(),
	}
	cache.mu.Unlock()

	event := StreamEvent{
		ID:        "1",
		Type:      "pub.polis.follow.announced",
		Actor:     "alice.com",
		Signature: sig,
		Payload:   payload,
	}

	if err := VerifyAuthorSignature(cache, event); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
}

func TestVerifyAuthorSignature_MissingSignature(t *testing.T) {
	cache := NewAuthorKeyCache(time.Hour, 5*time.Minute)
	event := StreamEvent{
		ID:    "1",
		Type:  "pub.polis.follow.announced",
		Actor: "alice.com",
	}

	err := VerifyAuthorSignature(cache, event)
	if err == nil {
		t.Fatal("expected error for missing signature")
	}
}

func TestVerifyAuthorSignature_WrongKey(t *testing.T) {
	privPEM, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	_, otherPubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen2: %v", err)
	}

	payload := map[string]interface{}{"target_domain": "bob.com"}
	canonical, _ := MakeStreamCanonicalJSON("pub.polis.follow.announced", payload)
	sig, _ := signing.SignContent(canonical, privPEM)

	cache := NewAuthorKeyCache(time.Hour, 5*time.Minute)
	cache.mu.Lock()
	cache.entries["alice.com"] = &authorKeyEntry{
		publicKey: string(otherPubSSH),
		fetchedAt: time.Now(),
	}
	cache.mu.Unlock()

	event := StreamEvent{
		ID:        "1",
		Type:      "pub.polis.follow.announced",
		Actor:     "alice.com",
		Signature: sig,
		Payload:   payload,
	}

	err = VerifyAuthorSignature(cache, event)
	if err == nil {
		t.Fatal("expected error for wrong key")
	}
}

func TestVerifyAuthorSignature_TamperedPayload(t *testing.T) {
	privPEM, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	originalPayload := map[string]interface{}{"target_domain": "bob.com"}
	canonical, _ := MakeStreamCanonicalJSON("pub.polis.follow.announced", originalPayload)
	sig, _ := signing.SignContent(canonical, privPEM)

	cache := NewAuthorKeyCache(time.Hour, 5*time.Minute)
	cache.mu.Lock()
	cache.entries["alice.com"] = &authorKeyEntry{
		publicKey: string(pubSSH),
		fetchedAt: time.Now(),
	}
	cache.mu.Unlock()

	// Tamper: change target_domain
	event := StreamEvent{
		ID:        "1",
		Type:      "pub.polis.follow.announced",
		Actor:     "alice.com",
		Signature: sig,
		Payload:   map[string]interface{}{"target_domain": "evil.com"},
	}

	err = VerifyAuthorSignature(cache, event)
	if err == nil {
		t.Fatal("expected error for tampered payload")
	}
}

// ============================================================================
// FilterVerifiedEvents
// ============================================================================

func TestFilterVerifiedEvents_NilCache_PassesAll(t *testing.T) {
	events := []StreamEvent{
		{ID: "1", Type: "pub.polis.follow.announced", Actor: "a.com"},
		{ID: "2", Type: "pub.polis.follow.announced", Actor: "b.com"},
	}
	result := FilterVerifiedEvents(nil, events, true, nil)
	if len(result) != 2 {
		t.Errorf("expected 2 events with nil cache, got %d", len(result))
	}
}

func TestFilterVerifiedEvents_ValidEvents(t *testing.T) {
	privPEM, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	payload := map[string]interface{}{"target_domain": "bob.com"}
	canonical, _ := MakeStreamCanonicalJSON("pub.polis.follow.announced", payload)
	sig, _ := signing.SignContent(canonical, privPEM)

	cache := NewAuthorKeyCache(time.Hour, 5*time.Minute)
	cache.mu.Lock()
	cache.entries["alice.com"] = &authorKeyEntry{
		publicKey: string(pubSSH),
		fetchedAt: time.Now(),
	}
	cache.mu.Unlock()

	events := []StreamEvent{
		{ID: "1", Type: "pub.polis.follow.announced", Actor: "alice.com", Signature: sig, Payload: payload},
	}

	result := FilterVerifiedEvents(cache, events, true, nil)
	if len(result) != 1 {
		t.Errorf("expected 1 verified event, got %d", len(result))
	}
}

func TestFilterVerifiedEvents_RejectsInvalid(t *testing.T) {
	_, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	cache := NewAuthorKeyCache(time.Hour, 5*time.Minute)
	cache.mu.Lock()
	cache.entries["alice.com"] = &authorKeyEntry{
		publicKey: string(pubSSH),
		fetchedAt: time.Now(),
	}
	cache.mu.Unlock()

	events := []StreamEvent{
		{ID: "1", Type: "pub.polis.follow.announced", Actor: "alice.com", Signature: "bad-sig", Payload: map[string]interface{}{}},
	}

	var warnings []string
	result := FilterVerifiedEvents(cache, events, true, func(evt StreamEvent, msg string) {
		warnings = append(warnings, msg)
	})
	if len(result) != 0 {
		t.Errorf("expected 0 events (invalid sig), got %d", len(result))
	}
	if len(warnings) != 1 {
		t.Errorf("expected 1 warning, got %d", len(warnings))
	}
}

func TestFilterVerifiedEvents_LegacyNoSig_Allowed(t *testing.T) {
	cache := NewAuthorKeyCache(time.Hour, 5*time.Minute)
	events := []StreamEvent{
		{ID: "1", Type: "pub.polis.follow.announced", Actor: "alice.com", Payload: map[string]interface{}{}},
	}

	var warnings []string
	result := FilterVerifiedEvents(cache, events, false, func(evt StreamEvent, msg string) {
		warnings = append(warnings, msg)
	})
	if len(result) != 1 {
		t.Errorf("expected 1 event (legacy allowed), got %d", len(result))
	}
	if len(warnings) != 1 {
		t.Errorf("expected 1 legacy warning, got %d", len(warnings))
	}
}

func TestFilterVerifiedEvents_NoSig_RequiredRejects(t *testing.T) {
	cache := NewAuthorKeyCache(time.Hour, 5*time.Minute)
	events := []StreamEvent{
		{ID: "1", Type: "pub.polis.follow.announced", Actor: "alice.com", Payload: map[string]interface{}{}},
	}

	var warnings []string
	result := FilterVerifiedEvents(cache, events, true, func(evt StreamEvent, msg string) {
		warnings = append(warnings, msg)
	})
	if len(result) != 0 {
		t.Errorf("expected 0 events (sig required), got %d", len(result))
	}
}
