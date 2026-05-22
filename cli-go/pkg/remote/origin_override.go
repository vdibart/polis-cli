package remote

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// EnvOriginOverrides is the env var name read at HTTP-client construction
// time. Format: comma-separated host=URL pairs, e.g.
//
//	POLIS_ORIGIN_OVERRIDES=discover.polis.pub=http://localhost:9000/origin/discover.polis.pub,alice.polis.pub=http://localhost:9000/origin/alice.polis.pub
//
// When set, outbound HTTP requests whose URL host matches a key get rewritten
// to the corresponding base URL (path appended). When unset, no overrides are
// applied — production code paths are unaffected.
//
// Test infrastructure only. Never set in production.
const EnvOriginOverrides = "POLIS_ORIGIN_OVERRIDES"

// originOverrideTransport wraps an http.RoundTripper, rewriting outbound
// request URLs whose host matches a configured override.
type originOverrideTransport struct {
	overrides map[string]*url.URL // host -> upstream base URL
	inner     http.RoundTripper
}

// MaybeWrapTransport returns inner wrapped with origin-override behavior if
// POLIS_ORIGIN_OVERRIDES is set in the environment, or inner unchanged. Safe
// to call from any HTTP-client constructor — the env-var lookup is one-shot
// at call time.
func MaybeWrapTransport(inner http.RoundTripper) http.RoundTripper {
	raw := os.Getenv(EnvOriginOverrides)
	if raw == "" {
		return inner
	}
	overrides, err := ParseOriginOverrides(raw)
	if err != nil || len(overrides) == 0 {
		// Malformed env var: log to stderr (test infra is rarely silent
		// about config errors) and return inner unchanged.
		fmt.Fprintf(os.Stderr, "[polis] %s: %v (overrides disabled)\n", EnvOriginOverrides, err)
		return inner
	}
	if inner == nil {
		inner = http.DefaultTransport
	}
	return &originOverrideTransport{overrides: overrides, inner: inner}
}

// ParseOriginOverrides parses the env-var format. Exposed for tests + for
// the webapp's /_/overrides.js endpoint (which serves the same map to the
// browser-side stream controller).
func ParseOriginOverrides(raw string) (map[string]*url.URL, error) {
	out := map[string]*url.URL{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		eq := strings.Index(pair, "=")
		if eq < 0 {
			return nil, fmt.Errorf("malformed override pair %q (want host=url)", pair)
		}
		host := strings.TrimSpace(pair[:eq])
		raw := strings.TrimSpace(pair[eq+1:])
		if host == "" || raw == "" {
			return nil, fmt.Errorf("override %q has empty host or url", pair)
		}
		u, err := url.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("override %q: parse url: %w", pair, err)
		}
		if u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("override %q: url must include scheme and host", pair)
		}
		out[host] = u
	}
	return out, nil
}

func (t *originOverrideTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	dst, ok := t.overrides[req.URL.Host]
	if !ok {
		return t.inner.RoundTrip(req)
	}
	// Build the rewritten URL: dst.Scheme://dst.Host + dst.Path + req.URL.Path
	// (paths are joined with a separator if needed). Query string preserved.
	rewritten := *req.URL
	rewritten.Scheme = dst.Scheme
	rewritten.Host = dst.Host
	rewritten.Path = joinPath(dst.Path, req.URL.Path)

	// Clone the request — never mutate the caller's request directly. Update
	// Host to match destination so the upstream sees its own hostname (or
	// whatever the override URL's host is).
	rewritten2 := req.Clone(req.Context())
	rewritten2.URL = &rewritten
	rewritten2.Host = dst.Host
	return t.inner.RoundTrip(rewritten2)
}

// joinPath concatenates two URL paths, trimming the duplicate slash at the
// boundary. Empty inputs are tolerated.
func joinPath(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	a = strings.TrimRight(a, "/")
	if !strings.HasPrefix(b, "/") {
		b = "/" + b
	}
	return a + b
}

