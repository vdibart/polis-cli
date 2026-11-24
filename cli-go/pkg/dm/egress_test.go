package dm

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

func TestIsPublicAddressRefusesEveryInternalRange(t *testing.T) {
	refused := []string{
		"127.0.0.1", "127.8.9.10", "::1", // loopback
		"10.0.0.1", "172.16.0.1", "172.31.255.255", "192.168.1.1", // RFC 1918
		"fc00::1", "fdaa:0:1::2", // unique local, Fly 6PN among them
		"169.254.169.254", "169.254.0.1", "fe80::1", // link-local, cloud metadata
		"100.64.0.1", "100.127.255.255", // carrier-grade NAT
		"0.0.0.0", "0.1.2.3", "::", // unspecified, "this network"
		"224.0.0.1", "ff02::1", "255.255.255.255", // multicast, broadcast
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:169.254.169.254", // IPv4-mapped
		"64:ff9b::7f00:1", // NAT64 of 127.0.0.1
		"198.18.0.1",      // benchmarking
	}
	for _, a := range refused {
		if IsPublicAddress(netip.MustParseAddr(a)) {
			t.Errorf("%s was taken for a public address", a)
		}
	}
	for _, a := range []string{"1.1.1.1", "104.16.0.1", "2606:4700::1", "::ffff:8.8.8.8", "172.32.0.1", "100.128.0.1"} {
		if !IsPublicAddress(netip.MustParseAddr(a)) {
			t.Errorf("%s was refused, and it is public", a)
		}
	}
}

// ⛔ The check is on the address dialled, not the name: "localhost" stands in
// for a public name whose DNS points inward.
func TestPublicOnlyRefusesANameThatResolvesInward(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer srv.Close()
	inward := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)

	_, err := PublicOnly(nil).Get(inward + "/.well-known/polis")
	if !errors.Is(err, ErrNonPublicAddress) || hit {
		t.Fatalf("a fetch to %s: err %v, reached %v", inward, err, hit)
	}

	// The send path: the recipient's key fetch never reaches it.
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	s := NewSenderWithHTTP(priv, pub, "me.example", storage.At(t.TempDir()), PublicOnly(srv.Client()))
	if _, _, _, err := s.fetchRecipientKey(inward); !errors.Is(err, ErrNonPublicAddress) || hit {
		t.Fatalf("send's key fetch: err %v, reached %v", err, hit)
	}
}

// ⛔ The receiving side fetches a sender's keys from a domain anyone names.
func TestTheReceiveSideFetchesOnlyPublicAddresses(t *testing.T) {
	if peerClient.Transport != publicTransport {
		t.Fatal("the receive side's client is not limited to public addresses")
	}
}
