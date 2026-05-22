package discovery

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// ============================================================================
// BuildCanonicalJSON
// ============================================================================

func TestBuildCanonicalJSON_SortsKeys(t *testing.T) {
	data := map[string]interface{}{
		"zebra": 1.0,
		"apple": 2.0,
		"mango": 3.0,
	}
	got := BuildCanonicalJSON(data)
	want := `{"apple":2,"mango":3,"zebra":1}`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestBuildCanonicalJSON_NestedObjects(t *testing.T) {
	data := map[string]interface{}{
		"b": map[string]interface{}{"z": 1.0, "a": 2.0},
		"a": "hello",
	}
	got := BuildCanonicalJSON(data)
	want := `{"a":"hello","b":{"a":2,"z":1}}`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestBuildCanonicalJSON_PreservesArrayOrder(t *testing.T) {
	data := map[string]interface{}{
		"items": []interface{}{3.0, 1.0, 2.0},
	}
	got := BuildCanonicalJSON(data)
	want := `{"items":[3,1,2]}`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestBuildCanonicalJSON_SortsKeysInsideArrayObjects(t *testing.T) {
	data := map[string]interface{}{
		"events": []interface{}{
			map[string]interface{}{"type": "follow", "actor": "alice"},
			map[string]interface{}{"type": "post", "actor": "bob"},
		},
	}
	got := BuildCanonicalJSON(data)
	want := `{"events":[{"actor":"alice","type":"follow"},{"actor":"bob","type":"post"}]}`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestBuildCanonicalJSON_NullAndEmpty(t *testing.T) {
	data := map[string]interface{}{
		"b": nil,
		"a": "",
		"c": []interface{}{},
	}
	got := BuildCanonicalJSON(data)
	want := `{"a":"","b":null,"c":[]}`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestBuildCanonicalJSON_Deterministic(t *testing.T) {
	data := map[string]interface{}{
		"cursor":   "123",
		"events":   []interface{}{map[string]interface{}{"id": 1.0, "type": "test"}},
		"has_more": false,
	}
	r1 := BuildCanonicalJSON(data)
	r2 := BuildCanonicalJSON(data)
	if r1 != r2 {
		t.Errorf("not deterministic: %s != %s", r1, r2)
	}
}

// ============================================================================
// ensureSSHFormat
// ============================================================================

func TestEnsureSSHFormat_AlreadySSH(t *testing.T) {
	key := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKqITeWFdSUH8LNf7qj2iLfNCr/K5o//yInLPgOEwMXh"
	result, err := ensureSSHFormat(key)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != key {
		t.Errorf("expected key unchanged, got %q", result)
	}
}

func TestEnsureSSHFormat_RawBase64(t *testing.T) {
	// Raw 32-byte Ed25519 public key in base64
	rawKey := "qohN5YV1JQfws1/uqPaIt80Kv8rmj//Icks+A4TAxeE="
	result, err := ensureSSHFormat(rawKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(result, "ssh-ed25519 ") {
		t.Errorf("expected ssh-ed25519 prefix, got %q", result)
	}

	// Verify the converted key can be parsed by signing.VerifySignature's key parser
	// (the key should be valid SSH format now)
	if len(result) < 20 {
		t.Error("converted key is too short")
	}
}

func TestEnsureSSHFormat_InvalidBase64(t *testing.T) {
	_, err := ensureSSHFormat("not-valid-anything!!!")
	if err == nil {
		t.Fatal("expected error for invalid input")
	}
}

func TestEnsureSSHFormat_WrongKeySize(t *testing.T) {
	// 16 bytes instead of 32
	shortKey := base64.StdEncoding.EncodeToString(make([]byte, 16))
	_, err := ensureSSHFormat(shortKey)
	if err == nil {
		t.Fatal("expected error for wrong key size")
	}
}

// ============================================================================
// DSKeyCache
// ============================================================================

func TestDSKeyCache_FetchesFromWellKnown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/polis" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"public_key": "ssh-ed25519 AAAA test-key",
				"key_id":     "key-1",
			})
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	cache := NewDSKeyCache(server.URL, time.Hour)
	pubKey, keyID, err := cache.GetKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pubKey != "ssh-ed25519 AAAA test-key" {
		t.Errorf("got pubKey=%q", pubKey)
	}
	if keyID != "key-1" {
		t.Errorf("got keyID=%q", keyID)
	}
}

func TestDSKeyCache_FallsBackToPublicKeyEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/polis" {
			w.WriteHeader(404)
			return
		}
		if r.URL.Path == "/v1/sites/public-key" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"public_key": "ssh-ed25519 BBBB fallback-key",
				"key_id":     "key-2",
			})
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	cache := NewDSKeyCache(server.URL, time.Hour)
	pubKey, keyID, err := cache.GetKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pubKey != "ssh-ed25519 BBBB fallback-key" {
		t.Errorf("got pubKey=%q", pubKey)
	}
	if keyID != "key-2" {
		t.Errorf("got keyID=%q", keyID)
	}
}

func TestDSKeyCache_CachesKey(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/polis" {
			callCount++
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"public_key": "ssh-ed25519 AAAA cached",
				"key_id":     "key-1",
			})
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	cache := NewDSKeyCache(server.URL, time.Hour)
	cache.GetKey()
	cache.GetKey()
	cache.GetKey()

	if callCount != 1 {
		t.Errorf("expected 1 fetch, got %d", callCount)
	}
}

func TestDSKeyCache_RefreshForcesRefetch(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/polis" {
			callCount++
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"public_key": fmt.Sprintf("ssh-ed25519 AAAA key-%d", callCount),
				"key_id":     "key-1",
			})
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	cache := NewDSKeyCache(server.URL, time.Hour)
	cache.GetKey()
	cache.Refresh()

	if callCount != 2 {
		t.Errorf("expected 2 fetches after Refresh, got %d", callCount)
	}
}

// ============================================================================
// VerifyDSResponse (round-trip sign + verify)
// ============================================================================

func TestVerifyDSResponse_ValidSignature(t *testing.T) {
	privPEM, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	// Serve the public key
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"public_key": string(pubSSH),
			"key_id":     "test-key",
		})
	}))
	defer server.Close()

	cache := NewDSKeyCache(server.URL, time.Hour)

	// Build data and sign it (simulating DS side)
	data := map[string]interface{}{
		"cursor":   "42",
		"events":   []interface{}{},
		"has_more": false,
	}
	canonical := BuildCanonicalJSON(data)
	sig, err := signing.SignContent([]byte(canonical), privPEM)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	err = VerifyDSResponse(cache, data, SignedResponse{
		DSSignature: sig,
		DSKeyID:     "test-key",
	})
	if err != nil {
		t.Fatalf("verification failed: %v", err)
	}
}

func TestVerifyDSResponse_MissingSignature(t *testing.T) {
	cache := NewDSKeyCache("http://localhost:1", time.Hour)
	err := VerifyDSResponse(cache, map[string]interface{}{}, SignedResponse{})
	if err == nil {
		t.Fatal("expected error for missing signature")
	}
}

func TestVerifyDSResponse_InvalidSignature(t *testing.T) {
	_, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	// A different key signs
	otherPrivPEM, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen2: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"public_key": string(pubSSH),
			"key_id":     "test-key",
		})
	}))
	defer server.Close()

	cache := NewDSKeyCache(server.URL, time.Hour)

	data := map[string]interface{}{"cursor": "0", "events": []interface{}{}, "has_more": false}
	canonical := BuildCanonicalJSON(data)
	sig, err := signing.SignContent([]byte(canonical), otherPrivPEM)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	err = VerifyDSResponse(cache, data, SignedResponse{
		DSSignature: sig,
		DSKeyID:     "test-key",
	})
	if err == nil {
		t.Fatal("expected verification failure with wrong key")
	}
}

func TestVerifyDSResponse_TamperedData(t *testing.T) {
	privPEM, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"public_key": string(pubSSH),
			"key_id":     "test-key",
		})
	}))
	defer server.Close()

	cache := NewDSKeyCache(server.URL, time.Hour)

	// Sign original data
	original := map[string]interface{}{"cursor": "42", "events": []interface{}{}, "has_more": false}
	canonical := BuildCanonicalJSON(original)
	sig, err := signing.SignContent([]byte(canonical), privPEM)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// Tamper with data
	tampered := map[string]interface{}{"cursor": "99", "events": []interface{}{}, "has_more": false}

	err = VerifyDSResponse(cache, tampered, SignedResponse{
		DSSignature: sig,
		DSKeyID:     "test-key",
	})
	if err == nil {
		t.Fatal("expected verification failure with tampered data")
	}
}

// ============================================================================
// Client.verifyResponse integration
// ============================================================================

func TestClient_VerifyResponse_SignedStream(t *testing.T) {
	privPEM, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	// Build the response data and sign it
	data := map[string]interface{}{
		"cursor":   "0",
		"events":   []interface{}{},
		"has_more": false,
	}
	canonical := BuildCanonicalJSON(data)
	sig, err := signing.SignContent([]byte(canonical), privPEM)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// Server that serves signed stream responses and the DS public key
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/polis":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"public_key": string(pubSSH),
				"key_id":     "test-key",
			})
		case "/v1/stream":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"cursor":       "0",
				"events":       []interface{}{},
				"has_more":     false,
				"ds_signature": sig,
				"ds_key_id":    "test-key",
			})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	result, err := client.StreamQuery("0", 100, "", "", "")
	if err != nil {
		t.Fatalf("stream query with valid signature failed: %v", err)
	}
	if result.Cursor != "0" {
		t.Errorf("expected cursor=0, got %s", result.Cursor)
	}
}

func TestClient_VerifyResponse_NilCache_Skips(t *testing.T) {
	client := &Client{
		BaseURL:    "http://localhost:1",
		DSKeyCache: nil,
	}
	// Should return nil (skip verification) when cache is nil
	err := client.verifyResponse([]byte(`{"test":true}`), "", "")
	if err != nil {
		t.Fatalf("expected nil error when DSKeyCache is nil, got: %v", err)
	}
}

// ============================================================================
// End-to-end: raw base64 key (like real DS) + SSH signature
// ============================================================================

func TestVerifyDSResponse_RawBase64Key(t *testing.T) {
	// Simulate what the real DS does: publish raw base64 public key,
	// sign with SSH format signature
	privPEM, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	// Extract raw 32-byte public key from SSH format and base64-encode it
	// (this is what the DS stores in DISCOVERY_SERVICE_PUBLIC_KEY)
	pubSSHStr := strings.TrimSpace(string(pubSSH))
	parts := strings.Fields(pubSSHStr)
	if len(parts) < 2 {
		t.Fatalf("unexpected pubSSH format: %s", pubSSHStr)
	}
	sshBlob, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode ssh blob: %v", err)
	}
	// SSH blob: [4-byte len]["ssh-ed25519"][4-byte len][32-byte key]
	// Skip key type: 4 bytes length + 11 bytes "ssh-ed25519" = 15 bytes
	rawPubKey := sshBlob[15+4:] // skip type + key length prefix
	rawBase64 := base64.StdEncoding.EncodeToString(rawPubKey)

	// Serve raw base64 key (like real DS)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"public_key":        rawBase64,
			"key_id":            "ds-test",
			"signing_algorithm": "ed25519-ssh",
		})
	}))
	defer server.Close()

	cache := NewDSKeyCache(server.URL, time.Hour)

	// Sign data with SSH format (like DS does)
	data := map[string]interface{}{
		"cursor":   "0",
		"events":   []interface{}{},
		"has_more": false,
	}
	canonical := BuildCanonicalJSON(data)
	sig, err := signing.SignContent([]byte(canonical), privPEM)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// Verify — this is the exact flow that was failing against the real DS
	err = VerifyDSResponse(cache, data, SignedResponse{
		DSSignature: sig,
		DSKeyID:     "ds-test",
	})
	if err != nil {
		t.Fatalf("verification failed with raw base64 key: %v", err)
	}
}

// ============================================================================
// Cross-compatibility with TS canonical JSON
// ============================================================================

func TestBuildCanonicalJSON_MatchesTSStreamResponse(t *testing.T) {
	// This test verifies Go canonical JSON matches the TS buildCanonicalJSON
	// output for a typical stream response shape.
	data := map[string]interface{}{
		"has_more": false,
		"cursor":   "42",
		"events": []interface{}{
			map[string]interface{}{
				"id":         1.0,
				"type":       "pub.polis.follow.announced",
				"actor":      "alice.com",
				"signature":  "sig1",
				"payload":    map[string]interface{}{"target_domain": "bob.com"},
				"created_at": "2026-01-01",
			},
		},
	}

	got := BuildCanonicalJSON(data)

	// Verify top-level key order
	var parsed map[string]interface{}
	json.Unmarshal([]byte(got), &parsed)

	// Re-parse to check structure is valid
	if parsed["cursor"] != "42" {
		t.Errorf("expected cursor=42, got %v", parsed["cursor"])
	}

	// Verify the output is a valid JSON string that starts with cursor (alphabetically first)
	expected := `{"cursor":"42","events":[{"actor":"alice.com","created_at":"2026-01-01","id":1,"payload":{"target_domain":"bob.com"},"signature":"sig1","type":"pub.polis.follow.announced"}],"has_more":false}`
	if got != expected {
		t.Errorf("canonical JSON mismatch.\nGot:  %s\nWant: %s", got, expected)
	}
}

// ============================================================================
// Autobless Attestation Verification
// ============================================================================

func TestVerifyAutoblessAttestation_Valid(t *testing.T) {
	privPEM, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"public_key": string(pubSSH),
			"key_id":     "ds-primary",
		})
	}))
	defer server.Close()

	cache := NewDSKeyCache(server.URL, time.Hour)

	// Build and sign attestation (simulating DS side)
	commentURL := "https://commenter.example/content/pub.polis.core/comment/reply.md"
	targetURL := "https://author.example/content/pub.polis.core/post/hello.md"
	policyRule := "emit pub.polis.comment.blessing from following"
	policySource := "user-published"
	dsKeyID := "ds-primary"

	canonical := BuildCanonicalJSON(map[string]interface{}{
		"type":          "pub.polis.comment.blessing",
		"action":        "autobless",
		"comment_url":   commentURL,
		"target_url":    targetURL,
		"policy_rule":   policyRule,
		"policy_source": policySource,
		"ds_key_id":     dsKeyID,
	})
	attestation, err := signing.SignContent([]byte(canonical), privPEM)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	err = VerifyAutoblessAttestation(cache, commentURL, targetURL, policyRule, policySource, dsKeyID, attestation)
	if err != nil {
		t.Fatalf("verification failed: %v", err)
	}
}

// TestVerifyAutoblessAttestation_EmptyAttestation — R20-C-F4 (2026-05-18).
// Empty attestation must now FAIL verification. Pre-fix this returned
// nil as a legacy-record concession, but the caller in stream/blessing.go
// only invokes this function for autoblessed events — where attestation
// is mandatory. An attacker could emit a fake autobless event with empty
// ds_attestation and the local cache accepted it.
func TestVerifyAutoblessAttestation_EmptyAttestation(t *testing.T) {
	cache := NewDSKeyCache("http://localhost:1", time.Hour)

	err := VerifyAutoblessAttestation(cache, "https://a.example/c.md", "https://b.example/p.md", "rule", "source", "key", "")
	if err == nil {
		t.Fatal("R20-C-F4 regression: expected error for empty attestation, got nil")
	}
	if !strings.Contains(err.Error(), "required") && !strings.Contains(err.Error(), "missing") {
		t.Errorf("expected error to mention 'required' or 'missing'; got %q", err.Error())
	}
}

func TestVerifyAutoblessAttestation_InvalidSignature(t *testing.T) {
	_, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	// Sign with a different key
	otherPrivPEM, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen2: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"public_key": string(pubSSH),
			"key_id":     "ds-primary",
		})
	}))
	defer server.Close()

	cache := NewDSKeyCache(server.URL, time.Hour)

	canonical := BuildCanonicalJSON(map[string]interface{}{
		"type":          "pub.polis.comment.blessing",
		"action":        "autobless",
		"comment_url":   "https://a.example/c.md",
		"target_url":    "https://b.example/p.md",
		"policy_rule":   "emit pub.polis.comment.blessing from following",
		"policy_source": "user-published",
		"ds_key_id":     "ds-primary",
	})
	attestation, err := signing.SignContent([]byte(canonical), otherPrivPEM)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	err = VerifyAutoblessAttestation(cache, "https://a.example/c.md", "https://b.example/p.md",
		"emit pub.polis.comment.blessing from following", "user-published", "ds-primary", attestation)
	if err == nil {
		t.Fatal("expected verification failure with wrong key")
	}
}

func TestVerifyAutoblessAttestation_CanonicalJSONMatchesTS(t *testing.T) {
	// This test verifies the Go autobless canonical JSON matches the TS buildCanonicalJSON
	// output for the same input. Keys must be sorted alphabetically.
	got := BuildCanonicalJSON(map[string]interface{}{
		"type":          "pub.polis.comment.blessing",
		"action":        "autobless",
		"comment_url":   "https://commenter.example/content/pub.polis.core/comment/reply.md",
		"target_url":    "https://author.example/content/pub.polis.core/post/hello.md",
		"policy_rule":   "emit pub.polis.comment.blessing from following",
		"policy_source": "user-published",
		"ds_key_id":     "ds-primary",
	})

	expected := `{"action":"autobless","comment_url":"https://commenter.example/content/pub.polis.core/comment/reply.md","ds_key_id":"ds-primary","policy_rule":"emit pub.polis.comment.blessing from following","policy_source":"user-published","target_url":"https://author.example/content/pub.polis.core/post/hello.md","type":"pub.polis.comment.blessing"}`
	if got != expected {
		t.Errorf("autobless canonical JSON mismatch with expected TS output.\nGot:  %s\nWant: %s", got, expected)
	}
}

func TestNewDSKeyCacheWithHTTP_Nil(t *testing.T) {
	cache := NewDSKeyCacheWithHTTP("https://ds.example.com", 0, nil)
	if cache == nil {
		t.Fatal("returned nil")
	}
	if cache.client == nil {
		t.Error("expected fallback client")
	}
	if cache.ttl != time.Hour {
		t.Errorf("ttl = %v, want 1h", cache.ttl)
	}
}

func TestNewDSKeyCacheWithHTTP_Shared(t *testing.T) {
	shared := &http.Client{}
	cache := NewDSKeyCacheWithHTTP("https://ds.example.com", 30*time.Minute, shared)
	if cache.client != shared {
		t.Error("expected shared client")
	}
	if cache.ttl != 30*time.Minute {
		t.Errorf("ttl = %v, want 30m", cache.ttl)
	}
	if cache.dsURL != "https://ds.example.com" {
		t.Errorf("dsURL = %q, want https://ds.example.com", cache.dsURL)
	}
}
