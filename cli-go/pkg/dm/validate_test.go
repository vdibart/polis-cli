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

		// Path separators / traversal / null bytes (DM-5): a verified sender domain is used
		// as a conversation directory name, so these must never pass.
		{"evil.com/../../etc", true},
		{"evil.com/..", true},
		{"a/b.example.com", true},
		{"example.com\\foo", true},
		{"example..com", true},
		{"example.com\x00.evil", true},
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

// TestValidateRecipientURL is the DM-4 outbound SSRF guard: https + a safe host only.
func TestValidateRecipientURL(t *testing.T) {
	tests := []struct {
		url     string
		wantErr bool
	}{
		{"https://alice.example.com", false},
		{"https://alice.example.com/", false},
		{"https://sub.polis.pub/path", false},
		// scheme
		{"http://alice.example.com", true}, // not https
		{"ftp://alice.example.com", true},  // not https
		{"https://", true},                 // empty host
		// SSRF hosts
		{"https://169.254.169.254/latest", true}, // cloud metadata
		{"https://127.0.0.1", true},              // loopback
		{"https://localhost", true},              // localhost
		{"https://metadata.google.internal", true},
		{"https://db.internal", true},
		{"https://alice.example.com:8080", true}, // port
		{"https://redis", true},                  // single-label
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			err := ValidateRecipientURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateRecipientURL(%q) = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}
