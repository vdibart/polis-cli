package server

import (
	"net"
	"net/http"
	"strings"
)

// Cloudflare sits in front of Fly for polis.pub, so the client Fly's proxy
// reports (Fly-Client-IP) is a Cloudflare EDGE, not the visitor. Keying rate
// limits or security logs on it lumps everyone routed through one edge into a
// single client. Cloudflare names the visitor in CF-Connecting-IP — but anyone
// who reaches the Fly app directly can set that header too, so it is believed
// ONLY when the peer that handed us the request is itself a Cloudflare address.
//
// cloudflareRanges is Cloudflare's published list, from
// https://www.cloudflare.com/ips-v4 and https://www.cloudflare.com/ips-v6
// (fetched 2026-09-27).
//
// ⚠️ Refresh it when Cloudflare announces a change. A range missing here only
// means requests through it are keyed on the edge again (today's behaviour);
// it never lets a spoofed header through.
var cloudflareRanges = mustParseCIDRs(
	// IPv4
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
	"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
	"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
	"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
	// IPv6
	"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32",
	"2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32",
)

func mustParseCIDRs(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic("cloudflare range " + c + ": " + err.Error())
		}
		out = append(out, n)
	}
	return out
}

// isCloudflare reports whether ip is inside Cloudflare's published ranges.
func isCloudflare(ip net.IP) bool {
	for _, n := range cloudflareRanges {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// visitorBehindCloudflare returns the visitor's address when peer is a
// Cloudflare edge and the request carries a valid CF-Connecting-IP; otherwise
// it returns peer unchanged. peer is the address the trusted proxy (Fly)
// reported.
func visitorBehindCloudflare(r *http.Request, peer string) string {
	p := net.ParseIP(strings.TrimSpace(peer))
	if p == nil || !isCloudflare(p) {
		return peer
	}
	cf := net.ParseIP(strings.TrimSpace(r.Header.Get("CF-Connecting-IP")))
	if cf == nil {
		return peer
	}
	return cf.String()
}

// TrustedProxyClientIP is the client address for a server behind Fly's proxy
// (and, for polis.pub, Cloudflare in front of that): remoteIP with
// ProxyFly. Hosted uses it for its own rate limits and security events,
// so the tenant server and hosted agree on who a client is.
func TrustedProxyClientIP(r *http.Request) string {
	return remoteIP(r, ProxyFly)
}
