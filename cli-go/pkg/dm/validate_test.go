package dm

import "testing"

func TestValidateDomain(t *testing.T) {
	tests := []struct {
		domain  string
		wantErr bool
	}{
		// Valid domains
		{"example.com", false},
		{"sub.example.com", false},
		{"my-site.polis.pub", false},
		{"a.b.c.d.example.org", false},

		// Empty
		{"", true},

		// IP addresses (v4)
		{"127.0.0.1", true},
		{"192.168.1.1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"169.254.169.254", true}, // AWS metadata

		// IP addresses (v6)
		{"::1", true},
		{"[::1]", true},
		{"fe80::1", true},

		// Localhost
		{"localhost", true},
		{"LOCALHOST", true},
		{"localhost.localdomain", true},

		// .local (mDNS)
		{"myhost.local", true},
		{"printer.local", true},

		// .internal (cloud)
		{"metadata.google.internal", true},
		{"postgres.internal", true},
		{"fly-local-6pn.internal", true},

		// Ports
		{"example.com:8080", true},
		{"127.0.0.1:6379", true},

		// Brackets (IPv6 literal)
		{"[2001:db8::1]", true},

		// Single-label hostnames
		{"redis", true},
		{"postgres", true},
		{"intranet", true},
	}

	for _, tt := range tests {
		t.Run(tt.domain, func(t *testing.T) {
			err := ValidateDomain(tt.domain)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateDomain(%q) = %v, wantErr %v", tt.domain, err, tt.wantErr)
			}
		})
	}
}
