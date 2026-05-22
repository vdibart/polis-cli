// Package httppool provides a shared HTTP transport for connection pooling
// across all outbound HTTP clients in the polis system.
//
// In the hosted multi-tenant environment, many packages create their own
// http.Client instances. Without a shared transport, each client maintains
// its own connection pool, defeating TCP/TLS connection reuse. This package
// provides a single transport that all clients can share, enabling connection
// pooling across the entire process.
package httppool

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

// defaultTransport is the process-level shared transport with tuned pooling.
// Safe for concurrent use by multiple goroutines.
var defaultTransport = &http.Transport{
	Proxy: http.ProxyFromEnvironment,
	DialContext: (&net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          200,
	MaxIdleConnsPerHost:   20,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:  5 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
	TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS12,
	},
}

// NewClient returns an *http.Client using the shared transport with the given timeout.
// Different callers can use different timeouts while sharing the same connection pool.
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: defaultTransport,
		Timeout:   timeout,
	}
}
