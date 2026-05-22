package httppool

import (
	"net/http"
	"testing"
	"time"
)

func TestDefaultTransportPoolConfig(t *testing.T) {
	tr := defaultTransport
	if tr.MaxIdleConns != 200 {
		t.Errorf("MaxIdleConns = %d, want 200", tr.MaxIdleConns)
	}
	if tr.MaxIdleConnsPerHost != 20 {
		t.Errorf("MaxIdleConnsPerHost = %d, want 20", tr.MaxIdleConnsPerHost)
	}
	if tr.IdleConnTimeout != 90*time.Second {
		t.Errorf("IdleConnTimeout = %v, want 90s", tr.IdleConnTimeout)
	}
	if !tr.ForceAttemptHTTP2 {
		t.Error("ForceAttemptHTTP2 should be true")
	}
	if tr.TLSClientConfig.MinVersion != 0x0303 { // tls.VersionTLS12
		t.Errorf("TLS MinVersion = %x, want TLS 1.2 (0x0303)", tr.TLSClientConfig.MinVersion)
	}
}

func TestNewClientUsesSharedTransport(t *testing.T) {
	c := NewClient(10 * time.Second)
	if c.Transport != defaultTransport {
		t.Error("NewClient should use the shared transport")
	}
	if c.Timeout != 10*time.Second {
		t.Errorf("Timeout = %v, want 10s", c.Timeout)
	}
}

func TestNewClientDifferentTimeouts(t *testing.T) {
	c1 := NewClient(5 * time.Second)
	c2 := NewClient(30 * time.Second)

	// Different clients, same transport
	if c1 == c2 {
		t.Error("NewClient should return distinct *http.Client instances")
	}
	if c1.Transport != c2.Transport {
		t.Error("both clients should share the same transport")
	}
	if c1.Timeout != 5*time.Second {
		t.Errorf("c1.Timeout = %v, want 5s", c1.Timeout)
	}
	if c2.Timeout != 30*time.Second {
		t.Errorf("c2.Timeout = %v, want 30s", c2.Timeout)
	}
}

func TestNewClientImplementsInterface(t *testing.T) {
	c := NewClient(10 * time.Second)
	// Verify it's a valid *http.Client (compile-time check mostly,
	// but also ensures the returned value is non-nil and usable)
	var _ *http.Client = c
	if c == nil {
		t.Error("NewClient should not return nil")
	}
}
