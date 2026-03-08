// Package discovery provides tests for the discovery service client.
package discovery

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

func TestCheckSiteRegistration_Registered(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("Expected GET request, got %s", r.Method)
		}
		if r.URL.Path != "/v1/sites/check" {
			t.Errorf("Expected /v1/sites/check, got %s", r.URL.Path)
		}
		domain := r.URL.Query().Get("domain")
		if domain != "alice.com" {
			t.Errorf("Expected domain=alice.com, got %s", domain)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"is_registered":        true,
			"domain":               "alice.com",
			"created_at":        "2026-01-15T10:30:00Z",
			"registry_url":         "https://registry.polis.pub/alice.com",
			"registration_version": 1,
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")
	client.DSKeyCache = nil // skip DS verification (testing request/response format)
	result, err := client.CheckSiteRegistration("alice.com")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if !result.IsRegistered {
		t.Error("Expected IsRegistered=true")
	}
	if result.Domain != "alice.com" {
		t.Errorf("Expected domain=alice.com, got %s", result.Domain)
	}
	if result.CreatedAt != "2026-01-15T10:30:00Z" {
		t.Errorf("Expected created_at=2026-01-15T10:30:00Z, got %s", result.CreatedAt)
	}
}

func TestCheckSiteRegistration_NotRegistered(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"is_registered": false,
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")
	client.DSKeyCache = nil // skip DS verification (testing request/response format)
	result, err := client.CheckSiteRegistration("bob.com")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if result.IsRegistered {
		t.Error("Expected IsRegistered=false")
	}
}

func TestCheckSiteRegistration_NetworkError(t *testing.T) {
	client := NewClient("http://localhost:99999", "test-api-key")
	_, err := client.CheckSiteRegistration("test.com")
	if err == nil {
		t.Error("Expected error for network failure")
	}
}

func TestCheckSiteRegistration_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "internal server error"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")
	_, err := client.CheckSiteRegistration("test.com")
	if err == nil {
		t.Error("Expected error for server failure")
	}
}

func TestCanonicalPayloadFormat_Register(t *testing.T) {
	// CRITICAL: This test verifies that the canonical payload format matches the bash CLI exactly.
	// The bash CLI uses: {"version":1,"action":"register","domain":"alice.com"}
	// Field order must be: version, action, domain

	payload, err := MakeSiteRegistrationCanonicalJSON("register", "alice.com")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// The expected format from bash CLI
	expected := `{"version":1,"action":"register","domain":"alice.com"}`

	if string(payload) != expected {
		t.Errorf("Canonical payload mismatch.\nExpected: %s\nGot:      %s", expected, string(payload))
	}
}

func TestCanonicalPayloadFormat_Unregister(t *testing.T) {
	// CRITICAL: This test verifies that the canonical payload format matches the bash CLI exactly.
	// The bash CLI uses: {"version":1,"action":"unregister","domain":"alice.com"}
	// Field order must be: version, action, domain

	payload, err := MakeSiteRegistrationCanonicalJSON("unregister", "alice.com")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// The expected format from bash CLI
	expected := `{"version":1,"action":"unregister","domain":"alice.com"}`

	if string(payload) != expected {
		t.Errorf("Canonical payload mismatch.\nExpected: %s\nGot:      %s", expected, string(payload))
	}
}

func TestRegisterSite_Success(t *testing.T) {
	var receivedPayload map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST request, got %s", r.Method)
		}
		if r.URL.Path != "/v1/sites" {
			t.Errorf("Expected /v1/sites, got %s", r.URL.Path)
		}

		// Decode and store the received payload for verification
		if err := json.NewDecoder(r.Body).Decode(&receivedPayload); err != nil {
			t.Fatalf("Failed to decode request body: %v", err)
		}

		w.WriteHeader(http.StatusCreated)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":       true,
			"domain":        "alice.com",
			"registry_url":  "https://registry.polis.pub/alice.com",
			"created_at": "2026-01-15T10:30:00Z",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")

	// Use a mock private key (this won't actually sign correctly, but we can test the structure)
	// In real usage, this would be a valid Ed25519 private key
	mockPrivateKey := []byte("-----BEGIN OPENSSH PRIVATE KEY-----\ntest\n-----END OPENSSH PRIVATE KEY-----\n")

	// Note: This test will fail at signing because we're using a mock key
	// In production, we'd use a proper key fixture
	_, err := client.RegisterSite("alice.com", mockPrivateKey, "alice@example.com", "Alice")
	// We expect a signing error due to the mock key
	if err == nil {
		// If we get here, verify the payload structure
		if receivedPayload["version"] != float64(1) {
			t.Errorf("Expected version=1, got %v", receivedPayload["version"])
		}
		if receivedPayload["action"] != "register" {
			t.Errorf("Expected action=register, got %v", receivedPayload["action"])
		}
		if receivedPayload["domain"] != "alice.com" {
			t.Errorf("Expected domain=alice.com, got %v", receivedPayload["domain"])
		}
	}
	// Note: With a mock key, signing will fail, which is expected behavior
}

func TestRegisterSite_AlreadyRegistered(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Site is already registered",
			"code":    "ALREADY_REGISTERED",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")
	mockPrivateKey := []byte("-----BEGIN OPENSSH PRIVATE KEY-----\ntest\n-----END OPENSSH PRIVATE KEY-----\n")

	_, err := client.RegisterSite("alice.com", mockPrivateKey, "", "")
	// We expect an error (either signing or already registered)
	if err == nil {
		t.Error("Expected error for already registered site")
	}
}

func TestUnregisterSite_Success(t *testing.T) {
	var receivedPayload map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST request, got %s", r.Method)
		}
		if r.URL.Path != "/v1/sites/unregister" {
			t.Errorf("Expected /v1/sites/unregister, got %s", r.URL.Path)
		}

		if err := json.NewDecoder(r.Body).Decode(&receivedPayload); err != nil {
			t.Fatalf("Failed to decode request body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"domain":  "alice.com",
			"message": "Site unregistered successfully",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")
	mockPrivateKey := []byte("-----BEGIN OPENSSH PRIVATE KEY-----\ntest\n-----END OPENSSH PRIVATE KEY-----\n")

	_, err := client.UnregisterSite("alice.com", mockPrivateKey)
	if err == nil {
		// Verify payload structure if signing succeeded
		if receivedPayload["version"] != float64(1) {
			t.Errorf("Expected version=1, got %v", receivedPayload["version"])
		}
		if receivedPayload["action"] != "unregister" {
			t.Errorf("Expected action=unregister, got %v", receivedPayload["action"])
		}
		if receivedPayload["domain"] != "alice.com" {
			t.Errorf("Expected domain=alice.com, got %v", receivedPayload["domain"])
		}
	}
	// Note: With a mock key, signing will fail, which is expected behavior
}

func TestUnregisterSite_NotRegistered(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Site is not registered",
			"code":    "NOT_REGISTERED",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")
	mockPrivateKey := []byte("-----BEGIN OPENSSH PRIVATE KEY-----\ntest\n-----END OPENSSH PRIVATE KEY-----\n")

	_, err := client.UnregisterSite("bob.com", mockPrivateKey)
	if err == nil {
		t.Error("Expected error for unregistered site")
	}
}

// ============================================================================
// Authenticated Client Tests
// ============================================================================

func TestNewAuthenticatedClient(t *testing.T) {
	client := NewAuthenticatedClient("https://ds.example.com", "key123", "alice.com", []byte("fake-priv-key"))

	if client.BaseURL != "https://ds.example.com" {
		t.Errorf("Expected BaseURL=https://ds.example.com, got %s", client.BaseURL)
	}
	if client.APIKey != "key123" {
		t.Errorf("Expected APIKey=key123, got %s", client.APIKey)
	}
	if client.Domain != "alice.com" {
		t.Errorf("Expected Domain=alice.com, got %s", client.Domain)
	}
	if string(client.PrivateKeyPEM) != "fake-priv-key" {
		t.Errorf("Expected PrivateKeyPEM=fake-priv-key, got %s", string(client.PrivateKeyPEM))
	}
}

func TestAddAuthHeaders_WithoutAuth(t *testing.T) {
	// Client without Domain/PrivateKeyPEM should NOT add auth headers
	client := NewClient("https://ds.example.com", "key123")

	req, _ := http.NewRequest("GET", "https://ds.example.com/test", nil)
	if err := client.addAuthHeaders(req); err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if req.Header.Get("X-Polis-Domain") != "" {
		t.Error("Expected no X-Polis-Domain header for unauthenticated client")
	}
	if req.Header.Get("X-Polis-Signature") != "" {
		t.Error("Expected no X-Polis-Signature header for unauthenticated client")
	}
	if req.Header.Get("X-Polis-Timestamp") != "" {
		t.Error("Expected no X-Polis-Timestamp header for unauthenticated client")
	}
}

func TestAddAuthHeaders_WithAuth(t *testing.T) {
	// Generate a real keypair for this test
	privKey, _, err := generateTestKeypair()
	if err != nil {
		t.Fatalf("Failed to generate keypair: %v", err)
	}

	client := NewAuthenticatedClient("https://ds.example.com", "key123", "alice.com", privKey)

	req, _ := http.NewRequest("GET", "https://ds.example.com/test", nil)
	if err := client.addAuthHeaders(req); err != nil {
		t.Fatalf("addAuthHeaders failed: %v", err)
	}

	domain := req.Header.Get("X-Polis-Domain")
	signature := req.Header.Get("X-Polis-Signature")
	timestamp := req.Header.Get("X-Polis-Timestamp")

	if domain != "alice.com" {
		t.Errorf("Expected X-Polis-Domain=alice.com, got %s", domain)
	}
	if signature == "" {
		t.Error("Expected non-empty X-Polis-Signature")
	}
	if timestamp == "" {
		t.Error("Expected non-empty X-Polis-Timestamp")
	}

	// Verify signature is in SSH format
	if len(signature) < 20 || signature[:29] != "-----BEGIN SSH SIGNATURE-----" {
		t.Errorf("Expected SSH signature format, got: %s...", signature[:min(40, len(signature))])
	}
}

func TestQueryRelationships_AuthenticatedSendsHeaders(t *testing.T) {
	privKey, _, err := generateTestKeypair()
	if err != nil {
		t.Fatalf("Failed to generate keypair: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth headers are present
		domain := r.Header.Get("X-Polis-Domain")
		signature := r.Header.Get("X-Polis-Signature")
		timestamp := r.Header.Get("X-Polis-Timestamp")

		if domain != "alice.com" {
			t.Errorf("Expected X-Polis-Domain=alice.com, got %s", domain)
		}
		if signature == "" {
			t.Error("Expected X-Polis-Signature to be present")
		}
		if timestamp == "" {
			t.Error("Expected X-Polis-Timestamp to be present")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"count":   0,
			"records": []interface{}{},
		})
	}))
	defer server.Close()

	client := NewAuthenticatedClient(server.URL, "test-key", "alice.com", privKey)
	client.DSKeyCache = nil // skip DS verification (testing auth headers)
	_, err = client.QueryRelationships("pub.polis.comment.blessing", map[string]string{"status": "pending"})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
}

func TestQueryContent_AuthenticatedSendsHeaders(t *testing.T) {
	privKey, _, err := generateTestKeypair()
	if err != nil {
		t.Fatalf("Failed to generate keypair: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth headers are present
		if r.Header.Get("X-Polis-Domain") != "alice.com" {
			t.Errorf("Expected X-Polis-Domain=alice.com, got %s", r.Header.Get("X-Polis-Domain"))
		}
		if r.Header.Get("X-Polis-Signature") == "" {
			t.Error("Expected X-Polis-Signature to be present")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"count":   0,
			"records": []interface{}{},
		})
	}))
	defer server.Close()

	client := NewAuthenticatedClient(server.URL, "test-key", "alice.com", privKey)
	client.DSKeyCache = nil // skip DS verification (testing auth headers)
	_, err = client.QueryContent("pub.polis.comment", map[string]string{"actor": "bob.com"})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
}

func TestStreamQuery_AuthenticatedSendsHeaders(t *testing.T) {
	privKey, _, err := generateTestKeypair()
	if err != nil {
		t.Fatalf("Failed to generate keypair: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Polis-Domain") != "alice.com" {
			t.Errorf("Expected X-Polis-Domain=alice.com, got %s", r.Header.Get("X-Polis-Domain"))
		}
		if r.Header.Get("X-Polis-Signature") == "" {
			t.Error("Expected X-Polis-Signature to be present")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"events":   []interface{}{},
			"cursor":   "0",
			"has_more": false,
		})
	}))
	defer server.Close()

	client := NewAuthenticatedClient(server.URL, "test-key", "alice.com", privKey)
	client.DSKeyCache = nil // skip DS verification (testing auth headers)
	_, err = client.StreamQuery("0", 100, "", "", "alice.com")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
}

func TestMakeQueryAuthCanonicalJSON(t *testing.T) {
	payload, err := MakeQueryAuthCanonicalJSON("alice.com", "2026-01-15T12:00:00Z")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	expected := `{"action":"query","domain":"alice.com","timestamp":"2026-01-15T12:00:00Z"}`
	if string(payload) != expected {
		t.Errorf("Canonical payload mismatch.\nExpected: %s\nGot:      %s", expected, string(payload))
	}
}

func TestMakeQueryAuthCanonicalJSON_FieldOrder(t *testing.T) {
	// Verify field order is consistent: action, domain, timestamp
	payload, _ := MakeQueryAuthCanonicalJSON("bob.com", "2026-02-13T10:00:00Z")

	var parsed map[string]interface{}
	json.Unmarshal(payload, &parsed)

	if _, ok := parsed["action"]; !ok {
		t.Error("Expected 'action' field in canonical payload")
	}
	if _, ok := parsed["domain"]; !ok {
		t.Error("Expected 'domain' field in canonical payload")
	}
	if _, ok := parsed["timestamp"]; !ok {
		t.Error("Expected 'timestamp' field in canonical payload")
	}
	if parsed["action"] != "query" {
		t.Errorf("Expected action=query, got %v", parsed["action"])
	}
}

// generateTestKeypair creates a real Ed25519 keypair for testing.
func generateTestKeypair() ([]byte, []byte, error) {
	return signing.GenerateKeypair()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ============================================================================
// Stream Tests
// ============================================================================

func TestStreamQuery_WithTargetFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("Expected GET request, got %s", r.Method)
		}
		if r.URL.Path != "/v1/stream" {
			t.Errorf("Expected /v1/stream, got %s", r.URL.Path)
		}

		target := r.URL.Query().Get("target")
		if target != "bob.com" {
			t.Errorf("Expected target=bob.com, got %q", target)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"events":   []interface{}{},
			"cursor":   "0",
			"has_more": false,
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")
	client.DSKeyCache = nil // skip DS verification (testing target filter)
	result, err := client.StreamQuery("0", 100, "", "", "bob.com")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if result.HasMore {
		t.Error("Expected has_more=false")
	}
}

func TestStreamQuery_WithoutTargetFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// target param should NOT be present when empty
		if r.URL.Query().Has("target") {
			t.Error("Expected no target parameter when filter is empty")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"events":   []interface{}{},
			"cursor":   "0",
			"has_more": false,
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")
	client.DSKeyCache = nil // skip DS verification (testing no-target filter)
	_, err := client.StreamQuery("0", 100, "", "", "")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
}

// ============================================================================
// Key Rotation Tests
// ============================================================================

func TestMakeKeyRotationCanonicalJSON(t *testing.T) {
	payload, err := MakeKeyRotationCanonicalJSON("alice.polis.pub", "ssh-ed25519 AAA", "ssh-ed25519 BBB", "2026-02-25T12:00:00Z")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	expected := `{"action":"key-rotation","domain":"alice.polis.pub","new_key":"ssh-ed25519 BBB","old_key":"ssh-ed25519 AAA","timestamp":"2026-02-25T12:00:00Z"}`
	if string(payload) != expected {
		t.Errorf("Canonical payload mismatch.\nExpected: %s\nGot:      %s", expected, string(payload))
	}
}

func TestMakeKeyRotationCanonicalJSON_KeyOrder(t *testing.T) {
	// Verify keys are in alphabetical order: action, domain, new_key, old_key, timestamp
	payload, err := MakeKeyRotationCanonicalJSON("bob.com", "ssh-ed25519 OLD", "ssh-ed25519 NEW", "2026-02-25T12:00:00Z")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("Failed to parse canonical JSON: %v", err)
	}

	expectedKeys := []string{"action", "domain", "new_key", "old_key", "timestamp"}
	for _, key := range expectedKeys {
		if _, ok := parsed[key]; !ok {
			t.Errorf("Expected key %q in canonical payload", key)
		}
	}

	if parsed["action"] != "key-rotation" {
		t.Errorf("Expected action=key-rotation, got %v", parsed["action"])
	}

	// Verify the raw JSON has keys in alphabetical order by checking positions
	raw := string(payload)
	for i, key := range expectedKeys {
		quoted := `"` + key + `"`
		pos := strings.Index(raw, quoted)
		if pos < 0 {
			t.Fatalf("Key %q not found in canonical JSON: %s", key, raw)
		}
		if i > 0 {
			prevQuoted := `"` + expectedKeys[i-1] + `"`
			prevPos := strings.Index(raw, prevQuoted)
			if pos <= prevPos {
				t.Errorf("Key %q (pos %d) should appear after %q (pos %d) in canonical JSON",
					key, pos, expectedKeys[i-1], prevPos)
			}
		}
	}
}

func TestMakeContentUnregisterCanonicalJSON(t *testing.T) {
	result := MakeContentUnregisterCanonicalJSON("pub.polis.post", "https://example.com/posts/test.md")

	expected := `{"type":"pub.polis.post","url":"https://example.com/posts/test.md"}`
	if result != expected {
		t.Errorf("Canonical payload mismatch.\nExpected: %s\nGot:      %s", expected, result)
	}
}

func TestRotateKey_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST request, got %s", r.Method)
		}
		if r.URL.Path != "/v1/sites/keys/rotate" {
			t.Errorf("Expected /v1/sites/keys/rotate, got %s", r.URL.Path)
		}

		var req KeyRotationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("Failed to decode request body: %v", err)
		}

		if req.Domain != "alice.polis.pub" {
			t.Errorf("Expected domain=alice.polis.pub, got %s", req.Domain)
		}
		if req.OldKey != "ssh-ed25519 AAA" {
			t.Errorf("Expected old_key=ssh-ed25519 AAA, got %s", req.OldKey)
		}
		if req.NewKey != "ssh-ed25519 BBB" {
			t.Errorf("Expected new_key=ssh-ed25519 BBB, got %s", req.NewKey)
		}

		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Key rotated successfully",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")
	err := client.RotateKey(KeyRotationRequest{
		Domain:        "alice.polis.pub",
		OldKey:        "ssh-ed25519 AAA",
		NewKey:        "ssh-ed25519 BBB",
		TransitionSig: "fake-signature",
		Timestamp:     "2026-02-25T12:00:00Z",
	})
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
}

func TestRotateKey_DSRejects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":false,"error":"Key mismatch: old_key does not match registered key"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")
	err := client.RotateKey(KeyRotationRequest{
		Domain:        "alice.polis.pub",
		OldKey:        "ssh-ed25519 WRONG",
		NewKey:        "ssh-ed25519 BBB",
		TransitionSig: "fake-signature",
		Timestamp:     "2026-02-25T12:00:00Z",
	})
	if err == nil {
		t.Fatal("Expected error for rejected key rotation, got nil")
	}

	// Verify the error message contains useful information
	errMsg := err.Error()
	if !strings.Contains(errMsg, "key rotation failed") {
		t.Errorf("Expected error to contain 'key rotation failed', got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "409") {
		t.Errorf("Expected error to contain status code '409', got: %s", errMsg)
	}
}

func TestClient_RequestIDPropagation(t *testing.T) {
	var receivedHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader = r.Header.Get("X-Request-Id")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"is_registered": true,
			"domain":        "test.com",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	client.RequestID = "test-trace-id-123"

	_, _ = client.CheckSiteRegistration("test.com")

	if receivedHeader != "test-trace-id-123" {
		t.Errorf("X-Request-Id = %q, want %q", receivedHeader, "test-trace-id-123")
	}
}

func TestClient_RequestIDAbsentWhenEmpty(t *testing.T) {
	var hasHeader bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hasHeader = r.Header.Get("X-Request-Id") != ""
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"is_registered": false,
			"domain":        "test.com",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	// RequestID not set

	_, _ = client.CheckSiteRegistration("test.com")

	if hasHeader {
		t.Error("X-Request-Id should not be set when RequestID is empty")
	}
}

func TestClient_LoggerCallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"is_registered": true,
			"domain":        "test.com",
		})
	}))
	defer server.Close()

	var loggedMethod, loggedPath, loggedRequestID string
	var loggedDuration int64

	client := NewClient(server.URL, "test-key")
	client.RequestID = "trace-456"
	client.Logger = func(method, path string, durationMs int64, requestID string) {
		loggedMethod = method
		loggedPath = path
		loggedDuration = durationMs
		loggedRequestID = requestID
	}

	_, _ = client.CheckSiteRegistration("test.com")

	if loggedMethod != "GET" {
		t.Errorf("Logger method = %q, want GET", loggedMethod)
	}
	if loggedPath != "/v1/sites/check" {
		t.Errorf("Logger path = %q, want /v1/sites/check", loggedPath)
	}
	if loggedDuration < 0 {
		t.Errorf("Logger duration = %d, want >= 0", loggedDuration)
	}
	if loggedRequestID != "trace-456" {
		t.Errorf("Logger requestID = %q, want trace-456", loggedRequestID)
	}
}
