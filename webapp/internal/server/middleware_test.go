package server

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
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
