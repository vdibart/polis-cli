package api

import (
	"net/http"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
)

const (
	maxAPIBodySize = 1 << 20 // 1MB default
)

// authMiddleware checks for Bearer token authentication on write routes.
func authMiddleware(siteDir string, requireAuth bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAuth {
			next(w, r)
			return
		}

		auth := r.Header.Get("Authorization")
		if auth == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Authorization header required")
			return
		}

		if !strings.HasPrefix(auth, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Bearer token required")
			return
		}

		token := strings.TrimPrefix(auth, "Bearer ")
		_, valid := ops.ValidateAPIKey(siteDir, token)
		if !valid {
			writeError(w, http.StatusForbidden, "forbidden", "Invalid API key")
			return
		}

		next(w, r)
	}
}

// corsMiddleware adds CORS headers for API access from external tools.
func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Polis-Domain, X-Polis-Signature, X-Polis-Timestamp")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next(w, r)
	}
}

// limitBody wraps a handler to cap request body size.
func limitBody(next http.HandlerFunc, maxBytes int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		next(w, r)
	}
}

// verifySignedRequest checks X-Polis-Domain/Signature/Timestamp headers.
// Returns the verified sender domain or an error.
func verifySignedRequest(r *http.Request) (string, error) {
	return dm.VerifySignedRequestWithLogger(r, fetchRemotePublicKey, nil)
}

// verifySignedRequestWithLogger checks signed request headers with structured logging.
func verifySignedRequestWithLogger(r *http.Request, logger dm.Logger) (string, error) {
	return dm.VerifySignedRequestWithLogger(r, fetchRemotePublicKey, logger)
}

// fetchRemotePublicKey fetches the public key for a domain from .well-known/polis.
// Used as the key-fetching callback for signed request verification.
func fetchRemotePublicKey(domain string) ([]byte, error) {
	return dm.FetchPublicKey(domain)
}
