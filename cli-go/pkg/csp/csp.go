// Package csp holds the Content-Security-Policy a site's public pages are
// served with.
//
// ⭐ IT LIVES IN cli-go BECAUSE TWO PROGRAMS SEND IT. The instance's own server
// sets it on every page it serves, and provisioning writes it into the routing
// of a deployment whose pages are static files the server never sees. The
// provisioner cannot import the server, so the policy lives here and both read
// it. ⛔ A page must get the same policy whichever of the two served it.
package csp

import "strings"

// TenantContent returns the CSP header value for tenant-served public
// HTML (rendered posts, comments, root pages). Allows the polis-apex
// widget JS plus Google Fonts (loaded by every shape + theme); blocks
// everything else.
func TenantContent() string {
	return strings.Join([]string{
		"default-src 'self'",
		"script-src 'self' https://polis.pub",
		// Google Fonts CSS is loaded via <link rel="stylesheet"> in every
		// shape + theme (v3/v4 stream, studio13-nk, etc.). 'unsafe-inline'
		// permits inline <style> blocks and style="" attributes that
		// theme rendering relies on.
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com",
		"img-src 'self' data: https:",
		// Google Fonts serves the actual font files (woff2) from gstatic.
		"font-src 'self' data: https://fonts.gstatic.com",
		// connect-src must include *.polis.pub so cross-tenant client
		// scripts can fetch sibling tenants' public surfaces. Two
		// known consumers: (a) nav.js fetches the visitor's own
		// /api/nav/state to hydrate the cross-visit nav widget;
		// (b) widget.js (the inline comment widget) reads the
		// visitor's following.json to decide its auth-aware mount
		// state. Both targets are public/auth-gated APIs on a polis
		// tenant — the same trust surface frame-ancestors already
		// admits via the *.polis.pub allow-list below.
		"connect-src 'self' https://polis.pub https://*.polis.pub",
		"frame-src 'none'",
		// R20-D-S1 (2026-05-18): bound who can iframe tenant content.
		// Without `frame-ancestors`, only `X-Frame-Options` would stop
		// clickjacking — but hosted only sets that header for /_/*
		// (admin SPA), not for tenant public pages. An attacker iframes
		// `victim.polis.pub/posts/x.html` invisibly under their own UI
		// and clicks through to the widget auth controls. Allowing self
		// + polis.pub + any *.polis.pub keeps the widget's legitimate
		// embed pattern working.
		"frame-ancestors 'self' https://polis.pub https://*.polis.pub",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
	}, "; ")
}
