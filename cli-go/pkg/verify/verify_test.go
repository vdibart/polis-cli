package verify

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

func TestParseFrontmatter_Valid(t *testing.T) {
	content := "---\ntitle: Hello World\npublished: 2024-01-01\ncurrent-version: sha256:abc123\nsignature: sig\ngenerator: polis-cli/0.45.0\n---\nBody content here\n"
	fm, body, err := parseFrontmatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm.Title != "Hello World" {
		t.Errorf("expected title 'Hello World', got '%s'", fm.Title)
	}
	if fm.Published != "2024-01-01" {
		t.Errorf("expected published '2024-01-01', got '%s'", fm.Published)
	}
	if fm.CurrentVersion != "sha256:abc123" {
		t.Errorf("expected version 'sha256:abc123', got '%s'", fm.CurrentVersion)
	}
	if fm.Signature != "sig" {
		t.Errorf("expected signature 'sig', got '%s'", fm.Signature)
	}
	if fm.Generator != "polis-cli/0.45.0" {
		t.Errorf("expected generator 'polis-cli/0.45.0', got '%s'", fm.Generator)
	}
	if body != "Body content here\n" {
		t.Errorf("unexpected body: %q", body)
	}
}

func TestParseFrontmatter_NoFrontmatter(t *testing.T) {
	_, _, err := parseFrontmatter("No frontmatter here")
	if err == nil {
		t.Error("expected error for content without frontmatter")
	}
}

func TestParseFrontmatter_EmptyBody(t *testing.T) {
	content := "---\ntitle: Test\n---\n"
	fm, body, err := parseFrontmatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm.Title != "Test" {
		t.Errorf("expected title 'Test', got '%s'", fm.Title)
	}
	if body != "" {
		t.Errorf("expected empty body, got %q", body)
	}
}

func TestParseFrontmatter_InReplyTo(t *testing.T) {
	content := "---\ntitle: Reply\ntype: comment\nin-reply-to:\n  url: https://alice.example.com/posts/1.md\n  version: sha256:def456\n---\nReply body\n"
	fm, _, err := parseFrontmatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm.InReplyTo != "https://alice.example.com/posts/1.md" {
		t.Errorf("expected in_reply_to URL, got %q", fm.InReplyTo)
	}
	if fm.InReplyToVersion != "sha256:def456" {
		t.Errorf("expected in_reply_to_version, got %q", fm.InReplyToVersion)
	}
	if fm.Type != "comment" {
		t.Errorf("expected type 'comment', got %q", fm.Type)
	}
}

func TestCanonicalizeContent(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "strips leading empty lines",
			input:    "\n\nHello\n",
			expected: "Hello\n",
		},
		{
			name:     "strips trailing whitespace per line",
			input:    "Hello   \nWorld\t\n",
			expected: "Hello\nWorld\n",
		},
		{
			name:     "strips trailing empty lines",
			input:    "Hello\n\n\n",
			expected: "Hello\n",
		},
		{
			name:     "ensures single trailing newline",
			input:    "Hello",
			expected: "Hello\n",
		},
		{
			name:     "no change for clean input",
			input:    "Hello\nWorld\n",
			expected: "Hello\nWorld\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := canonicalizeContent(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestExtractDomainFromBaseURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"https://alice.example.com", "alice.example.com"},
		{"http://localhost:8080", "localhost:8080"},
		{"https://example.com/path/to/something", "example.com"},
		{"example.com", "example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := extractDomainFromBaseURL(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestVerifyHash(t *testing.T) {
	body := "Hello, World!\n"
	hash := sha256.Sum256([]byte(canonicalizeContent(body)))
	hashStr := fmt.Sprintf("%x", hash)

	t.Run("valid hash", func(t *testing.T) {
		result := verifyHash(body, "sha256:"+hashStr)
		if result.Status != "valid" {
			t.Errorf("expected 'valid', got %q", result.Status)
		}
	})

	t.Run("hash mismatch", func(t *testing.T) {
		result := verifyHash(body, "sha256:0000000000000000000000000000000000000000000000000000000000000000")
		if result.Status != "mismatch" {
			t.Errorf("expected 'mismatch', got %q", result.Status)
		}
	})

	t.Run("empty version", func(t *testing.T) {
		result := verifyHash(body, "")
		if result.Status != "unknown" {
			t.Errorf("expected 'unknown', got %q", result.Status)
		}
	})

	// R20-C-F9: verifyHash used to dual-accept both canonical and raw-byte
	// hashes; the raw-byte fallback is removed. A hash computed over the
	// non-canonicalized body must now report 'mismatch' even when the body
	// canonicalization is non-trivial (here: trailing whitespace differs from
	// the canonical form).
	t.Run("raw_byte_hash_rejected", func(t *testing.T) {
		bodyWithTrailingWS := "Hello, World!\n   \n"
		rawHash := sha256.Sum256([]byte(bodyWithTrailingWS))
		result := verifyHash(bodyWithTrailingWS, "sha256:"+fmt.Sprintf("%x", rawHash))
		if result.Status != "mismatch" {
			t.Errorf("raw-byte hash should now be rejected as mismatch, got %q", result.Status)
		}
	})
}

func TestExtractContentToSign(t *testing.T) {
	t.Run("stops before signature line", func(t *testing.T) {
		content := "---\ntitle: Hello\nsignature: SIGDATA\n---\nBody\n"
		result := extractContentToSign(content, "alice.example.com")
		expected := "---\ntitle: Hello\n"
		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	})

	t.Run("includes everything when no signature", func(t *testing.T) {
		content := "---\ntitle: Hello\n---\nBody\n"
		result := extractContentToSign(content, "alice.example.com")
		expected := "---\ntitle: Hello\n---\nBody\n\n"
		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	})

	t.Run("handles content with multiple fields before signature", func(t *testing.T) {
		content := "---\ntitle: Test Post\npublished: 2024-01-01\ncurrent-version: sha256:abc\nsignature: MYSIG\ngenerator: polis-cli/0.50.0\n---\nContent body\n"
		result := extractContentToSign(content, "test.polis.pub")
		// Should include everything up to but not including the signature line
		if !strings.Contains(result, "title: Test Post") {
			t.Error("expected result to contain title")
		}
		if !strings.Contains(result, "current-version: sha256:abc") {
			t.Error("expected result to contain current-version")
		}
		if strings.Contains(result, "signature:") {
			t.Error("result should not contain signature line")
		}
		if strings.Contains(result, "generator:") {
			t.Error("result should not contain lines after signature")
		}
	})
}

func TestVerifySignature(t *testing.T) {
	// Generate a real keypair for testing
	privPEM, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("failed to generate keypair: %v", err)
	}
	publicKey := string(pubSSH)

	t.Run("valid signature", func(t *testing.T) {
		// Build content without signature, sign it, then verify
		contentWithoutSig := "---\ntitle: Test\npublished: 2024-01-01\n"
		sig, err := signing.SignContent([]byte(contentWithoutSig), privPEM)
		if err != nil {
			t.Fatalf("failed to sign: %v", err)
		}

		// Full content includes the signature line (but verifySignature extracts pre-sig content)
		fullContent := contentWithoutSig + "signature: " + sig + "\n---\nBody\n"

		result := verifySignature(fullContent, publicKey, sig, "test.polis.pub")
		if result.Status != "valid" {
			t.Errorf("expected 'valid', got %q: %s", result.Status, result.Message)
		}
	})

	t.Run("invalid signature", func(t *testing.T) {
		content := "---\ntitle: Test\nsignature: BADSIG\n---\nBody\n"
		result := verifySignature(content, publicKey, "BADSIG", "test.polis.pub")
		if result.Status != "invalid" {
			t.Errorf("expected 'invalid', got %q", result.Status)
		}
	})

	t.Run("missing signature", func(t *testing.T) {
		result := verifySignature("content", publicKey, "", "test.polis.pub")
		if result.Status != "missing" {
			t.Errorf("expected 'missing', got %q", result.Status)
		}
	})

	t.Run("missing public key", func(t *testing.T) {
		result := verifySignature("content", "", "somesig", "test.polis.pub")
		if result.Status != "error" {
			t.Errorf("expected 'error', got %q", result.Status)
		}
	})

	t.Run("tampered content", func(t *testing.T) {
		contentWithoutSig := "---\ntitle: Original\n"
		sig, err := signing.SignContent([]byte(contentWithoutSig), privPEM)
		if err != nil {
			t.Fatalf("failed to sign: %v", err)
		}

		// Tamper with the content
		tamperedContent := "---\ntitle: Tampered\nsignature: " + sig + "\n---\nBody\n"

		result := verifySignature(tamperedContent, publicKey, sig, "test.polis.pub")
		if result.Status != "invalid" {
			t.Errorf("expected 'invalid' for tampered content, got %q", result.Status)
		}
	})
}
