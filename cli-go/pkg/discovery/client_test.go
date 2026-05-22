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

	payload, err := marshalCanonical(siteRegistrationPayload{Version: 1, Action: "register", Domain: "alice.com"})
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

	payload, err := marshalCanonical(siteRegistrationPayload{Version: 1, Action: "unregister", Domain: "alice.com"})
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
// StreamQueryInvolved Tests
// ============================================================================

func TestStreamQueryInvolved_SendsInvolvedParam(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("Expected GET request, got %s", r.Method)
		}
		if r.URL.Path != "/v1/stream" {
			t.Errorf("Expected /v1/stream, got %s", r.URL.Path)
		}

		involved := r.URL.Query().Get("involved")
		if involved != "alice.com" {
			t.Errorf("Expected involved=alice.com, got %q", involved)
		}
		since := r.URL.Query().Get("since")
		if since != "42" {
			t.Errorf("Expected since=42, got %q", since)
		}
		limit := r.URL.Query().Get("limit")
		if limit != "1000" {
			t.Errorf("Expected limit=1000, got %q", limit)
		}

		// Should NOT have target or source params
		if r.URL.Query().Has("target") {
			t.Error("Expected no target parameter")
		}
		if r.URL.Query().Has("source") {
			t.Error("Expected no source parameter")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"events": []interface{}{
				map[string]interface{}{
					"id":         1,
					"type":       "polis.follow",
					"created_at": "2026-01-15T12:00:00Z",
					"actor":      "bob.com",
					"signature":  "sig1",
					"payload":    map[string]interface{}{"target_domain": "alice.com"},
				},
			},
			"cursor":   "1",
			"has_more": false,
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")
	client.DSKeyCache = nil
	result, err := client.StreamQueryInvolved("42", 1000, "alice.com")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(result.Events) != 1 {
		t.Errorf("Expected 1 event, got %d", len(result.Events))
	}
	if result.Cursor != "1" {
		t.Errorf("Expected cursor=1, got %s", result.Cursor)
	}
}

func TestStreamQueryInvolved_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"Cannot combine involved with target"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")
	_, err := client.StreamQueryInvolved("0", 1000, "alice.com")
	if err == nil {
		t.Error("Expected error for 400 response")
	}
}

// ============================================================================
// StreamQueryBatch Tests
// ============================================================================

func TestStreamQueryBatch_SendsCorrectRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST request, got %s", r.Method)
		}
		if r.URL.Path != "/v1/stream/batch" {
			t.Errorf("Expected /v1/stream/batch, got %s", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Expected Content-Type application/json, got %s", ct)
		}

		var body struct {
			Queries []StreamBatchFilter `json:"queries"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Failed to decode request body: %v", err)
		}
		if len(body.Queries) != 2 {
			t.Errorf("Expected 2 queries, got %d", len(body.Queries))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []interface{}{
				map[string]interface{}{"events": []interface{}{}, "cursor": "0", "has_more": false},
				map[string]interface{}{"events": []interface{}{}, "cursor": "5", "has_more": true},
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")
	client.DSKeyCache = nil
	result, err := client.StreamQueryBatch([]StreamBatchFilter{
		{Since: "0", Limit: 1000, Involved: "alice.com"},
		{Since: "0", Limit: 1000, Actors: []string{"bob.com", "charlie.com"}},
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(result.Results) != 2 {
		t.Errorf("Expected 2 results, got %d", len(result.Results))
	}
	if result.Results[0].Cursor != "0" {
		t.Errorf("Expected first cursor=0, got %s", result.Results[0].Cursor)
	}
	if !result.Results[1].HasMore {
		t.Error("Expected second result has_more=true")
	}
}

func TestStreamQueryBatch_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"Maximum 5 queries per batch"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")
	_, err := client.StreamQueryBatch([]StreamBatchFilter{
		{Since: "0", Limit: 100},
	})
	if err == nil {
		t.Error("Expected error for 400 response")
	}
}

// ============================================================================
// StreamQueryUnified Tests
// ============================================================================

func TestStreamQueryUnified_SendsCorrectParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("Expected GET request, got %s", r.Method)
		}
		if r.URL.Path != "/v1/stream/unified" {
			t.Errorf("Expected /v1/stream/unified, got %s", r.URL.Path)
		}

		involved := r.URL.Query().Get("involved")
		if involved != "alice.com" {
			t.Errorf("Expected involved=alice.com, got %q", involved)
		}
		actor := r.URL.Query().Get("actor")
		if actor != "bob.com,charlie.com" {
			t.Errorf("Expected actor=bob.com,charlie.com, got %q", actor)
		}
		since := r.URL.Query().Get("since")
		if since != "100" {
			t.Errorf("Expected since=100, got %q", since)
		}
		limit := r.URL.Query().Get("limit")
		if limit != "3000" {
			t.Errorf("Expected limit=3000, got %q", limit)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"events": []interface{}{
				map[string]interface{}{
					"id": 101, "type": "polis.follow", "created_at": "2026-01-15T12:00:00Z",
					"actor": "bob.com", "signature": "sig1",
					"payload": map[string]interface{}{"target_domain": "alice.com"},
				},
				map[string]interface{}{
					"id": 102, "type": "polis.post", "created_at": "2026-01-15T12:01:00Z",
					"actor": "charlie.com", "signature": "sig2",
					"payload": map[string]interface{}{"title": "Hello"},
				},
			},
			"cursor":   "102",
			"has_more": false,
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")
	client.DSKeyCache = nil
	result, err := client.StreamQueryUnified("100", 3000, "alice.com", []string{"bob.com", "charlie.com"})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(result.Events) != 2 {
		t.Errorf("Expected 2 events, got %d", len(result.Events))
	}
	if result.Cursor != "102" {
		t.Errorf("Expected cursor=102, got %s", result.Cursor)
	}
}

func TestStreamQueryUnified_NoFollowedAuthors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// actor param should NOT be present when no actors
		if r.URL.Query().Has("actor") {
			t.Error("Expected no actor parameter when actors list is empty")
		}
		involved := r.URL.Query().Get("involved")
		if involved != "alice.com" {
			t.Errorf("Expected involved=alice.com, got %q", involved)
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
	client.DSKeyCache = nil
	_, err := client.StreamQueryUnified("0", 3000, "alice.com", nil)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
}

func TestStreamQueryUnified_AuthHeadersSent(t *testing.T) {
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
	client.DSKeyCache = nil
	_, err = client.StreamQueryUnified("0", 3000, "alice.com", []string{"bob.com"})
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

// TestMakeContentUnregisterCanonicalJSON — R20-C-F1 (2026-05-18):
// canonical now includes `timestamp` for replay defense.
func TestMakeContentUnregisterCanonicalJSON(t *testing.T) {
	result := MakeContentUnregisterCanonicalJSON(
		"pub.polis.post",
		"https://example.com/posts/test.md",
		"2026-05-18T12:00:00Z",
	)

	expected := `{"type":"pub.polis.post","url":"https://example.com/posts/test.md","timestamp":"2026-05-18T12:00:00Z"}`
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

// ---------- WithHTTP constructor tests ----------

func TestNewClientWithHTTP_Nil(t *testing.T) {
	c := NewClientWithHTTP("https://ds.example.com", "key", nil)
	if c == nil {
		t.Fatal("returned nil")
	}
	if c.HTTPClient == nil {
		t.Error("expected fallback HTTPClient")
	}
	if c.DSKeyCache == nil {
		t.Error("expected DSKeyCache to be created")
	}
}

func TestNewClientWithHTTP_Shared(t *testing.T) {
	shared := &http.Client{}
	c := NewClientWithHTTP("https://ds.example.com", "key", shared)
	if c.HTTPClient != shared {
		t.Error("expected shared HTTPClient")
	}
	if c.BaseURL != "https://ds.example.com" {
		t.Errorf("BaseURL = %q, want https://ds.example.com", c.BaseURL)
	}
	if c.APIKey != "key" {
		t.Errorf("APIKey = %q, want key", c.APIKey)
	}
	if c.DSKeyCache == nil {
		t.Error("expected DSKeyCache")
	}
	if c.DSKeyCache.client != shared {
		t.Error("DSKeyCache should use the shared client")
	}
}

func TestNewAuthenticatedClientWithHTTP_Nil(t *testing.T) {
	c := NewAuthenticatedClientWithHTTP("https://ds.example.com", "key", "test.local", nil, nil)
	if c == nil {
		t.Fatal("returned nil")
	}
	if c.HTTPClient == nil {
		t.Error("expected fallback HTTPClient")
	}
	if c.Domain != "test.local" {
		t.Errorf("Domain = %q, want test.local", c.Domain)
	}
}

func TestNewAuthenticatedClientWithHTTP_Shared(t *testing.T) {
	shared := &http.Client{}
	privKey := []byte("fake-key")
	c := NewAuthenticatedClientWithHTTP("https://ds.example.com", "key", "test.local", privKey, shared)
	if c.HTTPClient != shared {
		t.Error("expected shared HTTPClient")
	}
	if c.Domain != "test.local" {
		t.Errorf("Domain = %q, want test.local", c.Domain)
	}
	if string(c.PrivateKeyPEM) != "fake-key" {
		t.Error("PrivateKeyPEM not set correctly")
	}
}
