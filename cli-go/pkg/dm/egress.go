package dm

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// ErrNonPublicAddress is a connection refused because the address a name
// resolved to is not a public one.
var ErrNonPublicAddress = errors.New("refused: the address is not public")

// ⛔ THE NAME CHECK IS NOT ENOUGH. ValidateDomain refuses names that are
// plainly internal (an IP, localhost, .internal, a single label), but a public
// name can resolve to 127.0.0.1, 10.x, 169.254.169.254 or a Fly 6PN address,
// and it can resolve differently when checked and when dialled. The dialler's
// Control hook sees the exact address about to be connected to, after
// resolution, so the check is made there, on every connection, redirects
// included.
var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),     // "this network"
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),  // IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),   // reserved, and broadcast
	netip.MustParsePrefix("64:ff9b::/96"),  // NAT64: an IPv4 address in disguise
}

// IsPublicAddress reports whether a connection to addr may be made by a fetch
// whose address someone else chose.
func IsPublicAddress(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsInterfaceLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() {
		return false
	}
	for _, p := range nonPublicPrefixes {
		if p.Contains(addr) {
			return false
		}
	}
	return true
}

func refuseNonPublic(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrNonPublicAddress, address)
	}
	if !IsPublicAddress(ap.Addr()) {
		return fmt.Errorf("%w: %s", ErrNonPublicAddress, ap.Addr())
	}
	return nil
}

// publicTransport is shared, so the guarded fetches still pool connections.
// No proxy: through one, the dialler would check the proxy's address, not the
// destination's.
var publicTransport = &http.Transport{
	DialContext: (&net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   refuseNonPublic,
	}).DialContext,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          100,
	MaxIdleConnsPerHost:   10,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   5 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
	TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
}

// PublicOnly is hc (or a 30-second client, when nil) with its connections
// limited to public addresses. Its timeout and redirect policy are kept; its
// transport is replaced.
func PublicOnly(hc *http.Client) *http.Client {
	out := &http.Client{Transport: publicTransport, Timeout: 30 * time.Second}
	if hc != nil {
		out.Timeout, out.CheckRedirect, out.Jar = hc.Timeout, hc.CheckRedirect, hc.Jar
	}
	return out
}
