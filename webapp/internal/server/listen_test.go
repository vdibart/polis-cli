package server

import "testing"

// A header-read timeout bounds slow-header clients; no write timeout, or a
// large download (the site's archive) would be cut mid-stream.
func TestHTTPServerBoundsHeadersButNotStreams(t *testing.T) {
	s := NewHTTPServer(":0", nil)
	if s.ReadHeaderTimeout <= 0 || s.IdleTimeout <= 0 {
		t.Errorf("ReadHeaderTimeout %v, IdleTimeout %v: both must be set", s.ReadHeaderTimeout, s.IdleTimeout)
	}
	if s.WriteTimeout != 0 || s.ReadTimeout != 0 {
		t.Errorf("WriteTimeout %v, ReadTimeout %v: must stay 0 (SSE streams, slow uploads)", s.WriteTimeout, s.ReadTimeout)
	}
}
