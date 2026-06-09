package api

import (
	"net/http"

	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
)

const (
	maxAPIBodySize = 1 << 20 // 1MB default
)

// corsMiddleware is a no-op passthrough enforcing same-origin policy.
// No CORS headers = browser enforces same-origin. Can be relaxed later
// with configurable allowed origins if cross-origin access is needed.
func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return next
}

// limitBody wraps a handler to cap request body size.
func limitBody(next http.HandlerFunc, maxBytes int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		next(w, r)
	}
}

// fetchRemotePublicKey fetches the public key for a domain from .well-known/polis.
// Used as the key-fetching callback for signed request verification.
func fetchRemotePublicKey(domain string) ([]byte, error) {
	return dm.FetchPublicKey(domain)
}
