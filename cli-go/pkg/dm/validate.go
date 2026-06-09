package dm

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ValidateRecipientURL is the outbound SSRF guard for the DM send path (DM-4): it requires
// an https URL whose host passes ValidateDomain (no IPs, loopback, .internal, .local, ports,
// path separators, single-label hosts). The inbound deliver path has guarded its sender
// domain since R11-8; the outbound path (recipient_url chosen by a — possibly hostile,
// multi-tenant — caller) had no equivalent, letting a tenant drive the shared server to GET
// cloud-metadata / internal hosts. Enforce this at the request boundary (webapp handlers /
// ops dispatch) rather than deep in the package, so localhost dev + the interop tests, which
// legitimately target http://127.0.0.1, are unaffected.
func ValidateRecipientURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid recipient URL: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("recipient URL must use https, got %q", u.Scheme)
	}
	if u.Port() != "" {
		// url.Hostname() strips the port, so ValidateDomain never sees it; reject explicitly.
		return fmt.Errorf("recipient URL must not specify a port")
	}
	if err := ValidateDomain(u.Hostname()); err != nil {
		return fmt.Errorf("unsafe recipient host: %w", err)
	}
	return nil
}

// ValidateDomain checks that a domain string is safe to use in URL construction.
// It rejects IP addresses, private/loopback ranges, domains with ports, and
// other patterns that could enable SSRF attacks.
func ValidateDomain(domain string) error {
	if domain == "" {
		return fmt.Errorf("empty domain")
	}

	// Reject path separators, traversal sequences, and null bytes (DM-5). A verified sender
	// domain is used directly as a conversation directory name (Mailbox.convDir →
	// filepath.Join), and built into URLs; an honest url.Hostname() never contains these, so
	// rejecting them is unconditional defense-in-depth against path traversal / injection.
	if strings.ContainsAny(domain, "/\\\x00") || strings.Contains(domain, "..") {
		return fmt.Errorf("domain must not contain path separators, traversal, or null bytes: %q", domain)
	}

	// Reject domains containing port numbers
	if strings.Contains(domain, ":") {
		return fmt.Errorf("domain must not contain port: %q", domain)
	}

	// Reject IPv6 bracket notation
	if strings.ContainsAny(domain, "[]") {
		return fmt.Errorf("domain must not contain brackets: %q", domain)
	}

	// Reject bare IP addresses (v4 and v6)
	if ip := net.ParseIP(domain); ip != nil {
		return fmt.Errorf("domain must not be an IP address: %q", domain)
	}

	// Reject localhost and variants
	lower := strings.ToLower(domain)
	if lower == "localhost" || strings.HasPrefix(lower, "localhost.") {
		return fmt.Errorf("domain must not be localhost: %q", domain)
	}

	// Reject .local mDNS domains
	if strings.HasSuffix(lower, ".local") {
		return fmt.Errorf("domain must not be a .local address: %q", domain)
	}

	// Reject .internal domains (cloud metadata, Fly.io internal routing)
	if strings.HasSuffix(lower, ".internal") {
		return fmt.Errorf("domain must not be an .internal address: %q", domain)
	}

	// Must contain at least one dot (reject single-label hostnames like "redis", "postgres")
	if !strings.Contains(domain, ".") {
		return fmt.Errorf("domain must be fully qualified: %q", domain)
	}

	return nil
}
