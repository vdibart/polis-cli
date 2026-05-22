package server

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"
)

func TestGenerateRequestID(t *testing.T) {
	id := generateRequestID()
	// Must match UUIDv4 format: 8-4-4-4-12 hex digits
	uuidRegex := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuidRegex.MatchString(id) {
		t.Errorf("generateRequestID() = %q, not a valid UUIDv4", id)
	}

	// Two calls should produce different IDs
	id2 := generateRequestID()
	if id == id2 {
		t.Errorf("generateRequestID() returned same value twice: %q", id)
	}
}

func TestRequestIDFromContextEmpty(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	id := RequestIDFromContext(req.Context())
	if id != "" {
		t.Errorf("RequestIDFromContext on empty context = %q, want empty", id)
	}
}

func TestRequestLoggingMiddleware_SetsRequestIDHeader(t *testing.T) {
	logger := NewLogger(LogLevelBasic, t.TempDir())

	handler := requestLoggingMiddleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	rid := w.Header().Get("X-Request-Id")
	if rid == "" {
		t.Error("X-Request-Id not set on response")
	}
}

func TestRequestLoggingMiddleware_PreservesIncomingRequestID(t *testing.T) {
	logger := NewLogger(LogLevelBasic, t.TempDir())

	incomingID := "test-request-id-12345"

	var capturedID string
	handler := requestLoggingMiddleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("X-Request-Id", incomingID)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if capturedID != incomingID {
		t.Errorf("RequestIDFromContext = %q, want %q", capturedID, incomingID)
	}
	if w.Header().Get("X-Request-Id") != incomingID {
		t.Errorf("response X-Request-Id = %q, want %q", w.Header().Get("X-Request-Id"), incomingID)
	}
}

func TestStatusResponseWriter_CapturesStatusCode(t *testing.T) {
	w := httptest.NewRecorder()
	sw := &statusResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

	sw.WriteHeader(http.StatusNotFound)

	if sw.statusCode != http.StatusNotFound {
		t.Errorf("statusCode = %d, want %d", sw.statusCode, http.StatusNotFound)
	}
}

func TestStatusResponseWriter_DefaultOK(t *testing.T) {
	w := httptest.NewRecorder()
	sw := &statusResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

	sw.Write([]byte("hello"))

	if sw.statusCode != http.StatusOK {
		t.Errorf("statusCode = %d, want %d", sw.statusCode, http.StatusOK)
	}
}

func TestShouldSkipRequestLog(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/api/sse", true},
		{"/favicon.ico", true},
		{"/favicon.svg", true},
		{"/assets/style.css", true},
		{"/assets/app.js", true},
		{"/api/status", false},
		{"/api/posts", false},
		{"/v1/content/pub.polis.post", false},
	}

	for _, tt := range tests {
		got := shouldSkipRequestLog(tt.path)
		if got != tt.want {
			t.Errorf("shouldSkipRequestLog(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestRequestLoggingMiddleware_SkipsNoisyPaths(t *testing.T) {
	logger := NewLogger(LogLevelBasic, t.TempDir())

	var called bool
	handler := requestLoggingMiddleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/sse", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if !called {
		t.Error("handler was not called for skipped path")
	}
	// The request should still have X-Request-Id set even if logging is skipped
	if w.Header().Get("X-Request-Id") == "" {
		t.Error("X-Request-Id not set even on skipped path")
	}
}

// TestRemoteIP_PrefersFlyClientIPOverXFF — F1 regression guard
// (2026-05-18). Pre-fix `remoteIP` read the LEFTMOST X-Forwarded-For
// entry, which is the attacker-controlled client header under Fly's
// proxy (Fly APPENDS to XFF rather than replacing it). An attacker
// could blast unbounded traffic past the structured-query rate cap
// by sending a different XFF value per request. The fix:
// (1) Trust `Fly-Client-IP` if present (Fly sets it directly, not
// spoofable). (2) Otherwise read the RIGHTMOST XFF entry (what the
// trusted proxy set). (3) Fall back to RemoteAddr.
func TestRemoteIP_PrefersFlyClientIPOverXFF(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "attacker.spoofed, 198.51.100.7")
	req.Header.Set("Fly-Client-IP", "203.0.113.42")

	if got := remoteIP(req); got != "203.0.113.42" {
		t.Errorf("remoteIP with Fly-Client-IP present = %q, want 203.0.113.42", got)
	}
}

// TestRemoteIP_ReadsRightmostXFF — F1 regression guard. When
// Fly-Client-IP is absent, the rightmost X-Forwarded-For entry is the
// trusted proxy's value; the leftmost (and any intermediate values)
// are attacker-controlled.
func TestRemoteIP_ReadsRightmostXFF(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "attacker.spoofed, 198.51.100.7")

	if got := remoteIP(req); got != "198.51.100.7" {
		t.Errorf("remoteIP rightmost-XFF = %q, want 198.51.100.7", got)
	}
}

// TestRemoteIP_SingleXFFEntry covers the localhost / single-proxy
// case where there's no comma in the header.
func TestRemoteIP_SingleXFFEntry(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.99")

	if got := remoteIP(req); got != "203.0.113.99" {
		t.Errorf("remoteIP single-XFF = %q, want 203.0.113.99", got)
	}
}

// TestRemoteIP_NoXFFFallsBackToRemoteAddr — localhost mode without
// any proxy headers must read RemoteAddr's host portion.
func TestRemoteIP_NoXFFFallsBackToRemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.50:9999"

	if got := remoteIP(req); got != "192.0.2.50" {
		t.Errorf("remoteIP no-XFF = %q, want 192.0.2.50", got)
	}
}

// TestPublicContentLimiter_Cleanup — F2 regression guard. The
// singleton must support draining stale entries so an attacker can't
// memory-exhaust the process by populating one entry per request.
// Reaper sweeps this every 24h; until F1 was fixed, the unbounded
// growth was reachable via XFF spoofing.
func TestPublicContentLimiter_Cleanup(t *testing.T) {
	l := newPublicContentLimiter(10, 50*time.Millisecond)

	// Populate 5 entries.
	for i := 0; i < 5; i++ {
		ip := "203.0.113." + string(rune('0'+i))
		if allowed, _ := l.Allow(ip); !allowed {
			t.Fatalf("seed entry %d: expected allowed", i)
		}
	}

	// Cleanup before window expires: no entries pruned.
	if removed := l.Cleanup(); removed != 0 {
		t.Errorf("pre-expiry cleanup: expected 0 removed, got %d", removed)
	}

	// Wait past the window.
	time.Sleep(60 * time.Millisecond)

	// Cleanup after: all 5 pruned.
	if removed := l.Cleanup(); removed != 5 {
		t.Errorf("post-expiry cleanup: expected 5 removed, got %d", removed)
	}
}
