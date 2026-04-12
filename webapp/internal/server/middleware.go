package server

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// contextKey is an unexported type for context keys in this package.
type contextKey int

const (
	requestIDKey contextKey = iota
)

// RequestIDFromContext extracts the request ID from the context.
// Returns empty string if not set.
func RequestIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

// generateRequestID creates a UUIDv4 using crypto/rand.
func generateRequestID() string {
	var uuid [16]byte
	_, _ = rand.Read(uuid[:])
	uuid[6] = (uuid[6] & 0x0f) | 0x40 // version 4
	uuid[8] = (uuid[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
}

// statusResponseWriter wraps http.ResponseWriter to capture the status code.
type statusResponseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (w *statusResponseWriter) WriteHeader(code int) {
	if !w.written {
		w.statusCode = code
		w.written = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusResponseWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.statusCode = http.StatusOK
		w.written = true
	}
	return w.ResponseWriter.Write(b)
}

// shouldSkipRequestLog returns true for paths that are too noisy to log.
func shouldSkipRequestLog(path string) bool {
	if path == "/api/sse" || path == "/favicon.ico" || path == "/favicon.svg" {
		return true
	}
	if strings.HasPrefix(path, "/assets/") {
		return true
	}
	return false
}

// securityHeadersMiddleware adds standard security headers to all responses.
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		next.ServeHTTP(w, r)
	})
}

// requestLoggingMiddleware logs every HTTP request as structured JSON and
// propagates X-Request-Id through the request context.
func requestLoggingMiddleware(logger *Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read or generate request ID
		requestID := r.Header.Get("X-Request-Id")
		if requestID == "" {
			requestID = generateRequestID()
		}

		// Set on response header for client correlation
		w.Header().Set("X-Request-Id", requestID)

		// Store in context
		ctx := context.WithValue(r.Context(), requestIDKey, requestID)
		r = r.WithContext(ctx)

		path := r.URL.Path

		// Skip noisy paths
		if shouldSkipRequestLog(path) {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		sw := &statusResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(sw, r)

		duration := time.Since(start)
		logger.Request(r.Method, path, sw.statusCode, duration, requestID)
	})
}
