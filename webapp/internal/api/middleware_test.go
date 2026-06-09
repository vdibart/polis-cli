package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ── corsMiddleware ─────────────────────────────────────────────────────

func TestCorsMiddlewarePassthrough(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := corsMiddleware(next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if !called {
		t.Error("expected next handler to be called")
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestCorsMiddlewareNoCORSHeaders(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := corsMiddleware(next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("expected no CORS headers (same-origin policy)")
	}
	if w.Header().Get("Access-Control-Allow-Methods") != "" {
		t.Error("expected no CORS methods header")
	}
	if w.Header().Get("Access-Control-Allow-Headers") != "" {
		t.Error("expected no CORS headers header")
	}
}

func TestCorsMiddlewarePreflightNoCORS(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := corsMiddleware(next)
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://other.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()
	handler(w, req)

	// No CORS headers should be set - same-origin only
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("expected no CORS headers on preflight")
	}
}

// ── limitBody ──────────────────────────────────────────────────────────

func TestLimitBodyUnderLimit(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		// Read the body
		var payload map[string]any
		err := json.NewDecoder(r.Body).Decode(&payload)
		if err != nil {
			t.Errorf("unexpected error reading body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	})

	handler := limitBody(next, 1024)
	body := strings.NewReader(`{"key":"value"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	handler(w, req)

	if !called {
		t.Error("expected next handler to be called")
	}
}

func TestLimitBodyOverLimit(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to read the body - should fail
		var payload map[string]any
		err := json.NewDecoder(r.Body).Decode(&payload)
		if err == nil {
			t.Error("expected error reading oversized body")
		}
	})

	handler := limitBody(next, 10) // 10 bytes limit
	bigBody := strings.NewReader(`{"key":"this is a much longer value that exceeds the limit"}`)
	req := httptest.NewRequest(http.MethodPost, "/", bigBody)
	w := httptest.NewRecorder()
	handler(w, req)
}

func TestLimitBodyExactLimit(t *testing.T) {
	payload := `{"a":"b"}`
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		var data map[string]any
		err := json.NewDecoder(r.Body).Decode(&data)
		if err != nil {
			t.Errorf("unexpected error at exact limit: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	})

	handler := limitBody(next, int64(len(payload)+1)) // Just enough room
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(payload))
	w := httptest.NewRecorder()
	handler(w, req)

	if !called {
		t.Error("expected next handler to be called at exact limit")
	}
}

func TestLimitBodyZeroLength(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := limitBody(next, 1024)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if !called {
		t.Error("expected next handler to be called with nil body")
	}
}

// ── maxAPIBodySize constant ────────────────────────────────────────────

func TestMaxAPIBodySizeIs1MB(t *testing.T) {
	expected := int64(1 << 20) // 1MB
	if maxAPIBodySize != expected {
		t.Errorf("expected maxAPIBodySize to be %d (1MB), got %d", expected, maxAPIBodySize)
	}
}

// ── Integration: auth through routeContent ─────────────────────────────

func TestRouteContentAuthMissingHeader(t *testing.T) {
	mux, _, _ := testSetup(t)

	body := jsonBody(t, map[string]any{"markdown": "test"})
	req := httptest.NewRequest(http.MethodPost, "/v1/content/post", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "error" {
		t.Error("expected error status")
	}
}

func TestRouteContentAuthInvalidScheme(t *testing.T) {
	mux, _, _ := testSetup(t)

	body := jsonBody(t, map[string]any{"markdown": "test"})
	req := httptest.NewRequest(http.MethodPost, "/v1/content/post", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Token abc123")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestRouteContentAuthInvalidKey(t *testing.T) {
	mux, _, _ := testSetup(t)

	body := jsonBody(t, map[string]any{"markdown": "test"})
	req := httptest.NewRequest(http.MethodPost, "/v1/content/post", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer polis_definitely_not_valid")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestRouteContentGETIsPublic(t *testing.T) {
	mux, _, _ := testSetup(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/content/post", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// GET should not require auth
	if w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden {
		t.Errorf("GET should be public, got %d", w.Code)
	}
}

func TestRouteContentPUTRequiresAuth(t *testing.T) {
	mux, _, _ := testSetup(t)

	body := jsonBody(t, map[string]any{"markdown": "test"})
	req := httptest.NewRequest(http.MethodPut, "/v1/content/post/some-id", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for PUT without auth, got %d", w.Code)
	}
}

func TestRouteContentDELETERequiresAuth(t *testing.T) {
	mux, _, _ := testSetup(t)

	req := httptest.NewRequest(http.MethodDelete, "/v1/content/post/some-id", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for DELETE without auth, got %d", w.Code)
	}
}

// ── DM deliver signed request path ─────────────────────────────────────

func TestDMDeliverWithInvalidSignedRequest(t *testing.T) {
	mux, _, _ := testSetup(t)

	body := jsonBody(t, map[string]any{"message": "hello"})
	req := httptest.NewRequest(http.MethodPost, "/v1/content/dm/actions/deliver", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Polis-Domain", "attacker.example.com")
	req.Header.Set("X-Polis-Signature", "invalid-sig")
	req.Header.Set("X-Polis-Timestamp", "2026-01-01T00:00:00Z")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for invalid signed request, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDMDeliverWithoutSignedHeadersRejected(t *testing.T) {
	mux, _, _ := testSetup(t)

	body := jsonBody(t, map[string]any{"message": "hello"})
	req := httptest.NewRequest(http.MethodPost, "/v1/content/dm/actions/deliver", body)
	req.Header.Set("Content-Type", "application/json")
	// No X-Polis-Domain, no Authorization
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// DM-7: deliver is signed-only — no fall-through to Bearer. Refused with 403.
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 when no signed request, got %d: %s", w.Code, w.Body.String())
	}
}

// R18-13: the FQN form `pub.polis.dm` must enter the signed-request branch
// just like the short form `dm`; pre-fix the FQN path fell through to Bearer
// auth (which always 401'd remote senders) — same outcome, but the gate vs.
// Engine.resolveTypeName mismatch was a smell. Asserts the FQN form returns
// 403 (signed-request verification failed) not 401 (no Bearer).
func TestDMDeliverFQNFormEntersSignedRequestBranch(t *testing.T) {
	mux, _, _ := testSetup(t)

	body := jsonBody(t, map[string]any{"message": "hello"})
	req := httptest.NewRequest(http.MethodPost, "/v1/content/pub.polis.dm/actions/deliver", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Polis-Domain", "attacker.example.com")
	req.Header.Set("X-Polis-Signature", "invalid-sig")
	req.Header.Set("X-Polis-Timestamp", "2026-01-01T00:00:00Z")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 (signed-request branch) for FQN path with invalid sig, got %d: %s", w.Code, w.Body.String())
	}
}

// ── Body limit integration through content routes ──────────────────────

func TestContentRouteHasBodyLimit(t *testing.T) {
	mux, _, apiKey := testSetup(t)

	// Create a body that exceeds 1MB
	bigPayload := strings.Repeat("x", 1<<20+100)
	req := httptest.NewRequest(http.MethodPost, "/v1/content/post", strings.NewReader(bigPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Should fail with 400 (bad request from JSON parse on limited body)
	// or the body reader error propagates
	if w.Code == http.StatusOK || w.Code == http.StatusCreated {
		t.Error("expected request to fail with oversized body")
	}
}

func TestBundleRoutesHaveNoBodyLimit(t *testing.T) {
	mux, _, _ := testSetup(t)

	// Bundle routes are GET-only, body limit shouldn't matter
	req := httptest.NewRequest(http.MethodGet, "/v1/bundles", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// ── Security logger integration in routeContent ────────────────────────

func TestSecurityLoggerOnMissingAuth(t *testing.T) {
	mux, siteDir, _ := testSetup(t)
	_ = siteDir

	// The default testSetup doesn't configure a security logger,
	// so auth failures should still work (logger is nil-safe)
	body := jsonBody(t, map[string]any{"markdown": "test"})
	req := httptest.NewRequest(http.MethodPost, "/v1/content/post", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestSecurityLoggerOnInvalidKey(t *testing.T) {
	mux, _, _ := testSetup(t)

	body := jsonBody(t, map[string]any{"markdown": "test"})
	req := httptest.NewRequest(http.MethodPost, "/v1/content/post", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer polis_bad_key")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}
