package server

import (
	"net/http/httptest"
	"testing"
)

// polis.pub is behind Cloudflare, so the peer Fly reports is a Cloudflare
// edge. CF-Connecting-IP names the visitor, and is believed only from a
// Cloudflare peer: anyone reaching the server another way can set it too.
func TestRemoteIPBehindCloudflare(t *testing.T) {
	const visitor = "203.0.113.9"
	cases := []struct {
		name       string
		trustProxy bool
		remoteAddr string
		fly, xff   string
		cf         string
		want       string
	}{
		{name: "Cloudflare peer names the visitor", trustProxy: true, fly: "162.158.63.120", cf: visitor, want: visitor},
		{name: "spoof from a non-Cloudflare peer is ignored", trustProxy: true, fly: "198.51.100.7", cf: visitor, want: "198.51.100.7"},
		{name: "Cloudflare peer with no header stays the peer", trustProxy: true, fly: "104.23.190.92", want: "104.23.190.92"},
		{name: "Cloudflare peer with a garbage header stays the peer", trustProxy: true, fly: "104.23.190.92", cf: "not-an-ip", want: "104.23.190.92"},
		{name: "IPv6 Cloudflare peer and IPv6 visitor", trustProxy: true, fly: "2606:4700::1", cf: "2001:db8::5", want: "2001:db8::5"},
		{name: "Cloudflare peer via rightmost X-Forwarded-For", trustProxy: true, xff: "1.2.3.4, 172.69.122.181", cf: visitor, want: visitor},
		{name: "attacker-chosen leftmost X-Forwarded-For is never the peer", trustProxy: true, xff: "162.158.0.1, 198.51.100.7", cf: visitor, want: "198.51.100.7"},
		{name: "no proxy: socket peer, spoofed header ignored", remoteAddr: "198.51.100.7:1234", cf: visitor, want: "198.51.100.7"},
		{name: "no proxy: self-hosted behind Cloudflare", remoteAddr: "[2606:4700::1]:443", cf: visitor, want: visitor},
		{name: "no proxy: Fly header not trusted", remoteAddr: "192.0.2.50:1", fly: "162.158.63.120", cf: visitor, want: "192.0.2.50"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if c.remoteAddr != "" {
				req.RemoteAddr = c.remoteAddr
			}
			if c.fly != "" {
				req.Header.Set("Fly-Client-IP", c.fly)
			}
			if c.xff != "" {
				req.Header.Set("X-Forwarded-For", c.xff)
			}
			if c.cf != "" {
				req.Header.Set("CF-Connecting-IP", c.cf)
			}
			proxy := ProxyNone
			if c.trustProxy {
				proxy = ProxyFly
			}
			if got := remoteIP(req, proxy); got != c.want {
				t.Errorf("remoteIP = %q, want %q", got, c.want)
			}
		})
	}
}

// The published ranges parse, and the Cloudflare edges seen in production
// logs (2026-09) are inside them.
func TestCloudflareRangesCoverObservedEdges(t *testing.T) {
	for _, ip := range []string{"104.23.190.92", "162.158.63.120", "172.70.108.20", "172.69.122.181", "104.23.253.3"} {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Fly-Client-IP", ip)
		req.Header.Set("CF-Connecting-IP", "203.0.113.9")
		if got := remoteIP(req, ProxyFly); got != "203.0.113.9" {
			t.Errorf("%s is a logged Cloudflare edge but was not recognised (got %q)", ip, got)
		}
	}
}
