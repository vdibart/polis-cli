package dm

import (
	"fmt"
	"net"
	"strings"
)

// ValidateDomain checks that a domain string is safe to use in URL construction.
// It rejects IP addresses, private/loopback ranges, domains with ports, and
// other patterns that could enable SSRF attacks.
func ValidateDomain(domain string) error {
	if domain == "" {
		return fmt.Errorf("empty domain")
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
