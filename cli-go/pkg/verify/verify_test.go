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
			result := signing.CanonicalizeContent(tt.input)
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
	hash := sha256.Sum256([]byte(signing.CanonicalizeContent(body)))
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
	// The signer (publish.PublishPost) signs the canonicalized frontmatter
	// WITHOUT the signature line, INCLUDING the closing "---" and the full body.
	// extractContentToSign must reproduce exactly those bytes by dropping only
	// the signature line. A previous version stopped at the signature line,
	// discarding the closing "---" and body; these tests guard against that
	// regression (see TestVerifySignature_PublishRoundTrip for the e2e proof).

	t.Run("drops only the signature line, keeps closing --- and body", func(t *testing.T) {
		content := "---\ntitle: Hello\nsignature: SIGDATA\n---\nBody\n"
		result := signing.ContentToSign(content, false)
		expected := "---\ntitle: Hello\n---\nBody\n"
		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	})

	t.Run("canonicalizes when no signature present", func(t *testing.T) {
		content := "---\ntitle: Hello\n---\nBody\n"
		result := signing.ContentToSign(content, false)
		expected := "---\ntitle: Hello\n---\nBody\n"
		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	})

	t.Run("keeps fields and body that follow the signature line", func(t *testing.T) {
		content := "---\ntitle: Test Post\npublished: 2024-01-01\ncurrent-version: sha256:abc\nsignature: MYSIG\ngenerator: polis-cli/0.50.0\n---\nContent body\n"
		result := signing.ContentToSign(content, false)
		if !strings.Contains(result, "title: Test Post") {
			t.Error("expected result to contain title")
		}
		if !strings.Contains(result, "current-version: sha256:abc") {
			t.Error("expected result to contain current-version")
		}
		if strings.Contains(result, "signature:") {
			t.Error("result should not contain the signature line")
		}
		// Lines AFTER the signature line (generator, closing ---, body) must be
		// preserved — they were part of the signed bytes.
		if !strings.Contains(result, "generator: polis-cli/0.50.0") {
			t.Error("expected result to retain the generator line after the signature")
		}
		if !strings.Contains(result, "Content body") {
			t.Error("expected result to retain the body after the signature")
		}
	})
}

func TestVerifySignature(t *testing.T) {
	// Generate a real keypair for testing. (End-to-end signing round-trips live
	// in TestVerifySignature_PublishRoundTrip.)
	_, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("failed to generate keypair: %v", err)
	}
	publicKey := string(pubSSH)

	t.Run("invalid signature", func(t *testing.T) {
		content := "---\ntitle: Test\nsignature: BADSIG\n---\nBody\n"
		result := verifySignature(content, publicKey, "BADSIG", false)
		if result.Status != "invalid" {
			t.Errorf("expected 'invalid', got %q", result.Status)
		}
	})

	t.Run("missing signature", func(t *testing.T) {
		result := verifySignature("content", publicKey, "", false)
		if result.Status != "missing" {
			t.Errorf("expected 'missing', got %q", result.Status)
		}
	})

	t.Run("missing public key", func(t *testing.T) {
		result := verifySignature("content", "", "somesig", false)
		if result.Status != "error" {
			t.Errorf("expected 'error', got %q", result.Status)
		}
	})
}

// signPublishStyle reproduces publish.PublishPost's signing recipe and returns
// the published file bytes (frontmatter WITH a bare-base64 signature line +
// blank line + body) plus the bare-base64 signature, exactly as a real polis
// post is stored on disk and served over HTTP.
func signPublishStyle(t *testing.T, privPEM []byte, body string) (publishedFile, bareSig string) {
	t.Helper()
	canonicalBody := signing.CanonicalizeContent(body)
	unsignedFrontmatter := "---\n" +
		"title: Test Post\n" +
		"published: 2024-01-01T00:00:00Z\n" +
		"generator: polis-cli-go/test\n" +
		"current-version: sha256:0000\n" +
		"---"
	canonicalizedForSigning := signing.CanonicalizeContent(unsignedFrontmatter + "\n\n" + canonicalBody)
	sigPEM, err := signing.SignContent([]byte(canonicalizedForSigning), privPEM)
	if err != nil {
		t.Fatalf("failed to sign: %v", err)
	}
	bareSig = bareBase64FromPEM(sigPEM)

	// Insert the signature line before the closing "---", as buildFrontmatter does.
	signedFrontmatter := "---\n" +
		"title: Test Post\n" +
		"published: 2024-01-01T00:00:00Z\n" +
		"generator: polis-cli-go/test\n" +
		"current-version: sha256:0000\n" +
		"signature: " + bareSig + "\n" +
		"---"
	publishedFile = signedFrontmatter + "\n\n" + canonicalBody + "\n"
	return publishedFile, bareSig
}

// bareBase64FromPEM strips the PEM armor and joins the base64 body into a single
// line, matching how publish.extractSignatureBase64 stores the signature.
func bareBase64FromPEM(pemStr string) string {
	var parts []string
	for _, ln := range strings.Split(pemStr, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "-----") {
			continue
		}
		parts = append(parts, ln)
	}
	return strings.Join(parts, "")
}

// TestVerifySignature_PublishRoundTrip is the regression test for the verifier
// bug: published posts store the signature as bare base64 over the canonicalized
// frontmatter+body, and the verifier must reconstruct exactly those bytes. The
// old verifier (a) stopped at the signature line, dropping the body, and (b)
// never rewrapped the bare-base64 signature into PEM — so it reported every
// genuinely-valid signature as INVALID. This test signs the real way, verifies
// the real way, and would fail under either of those bugs.
func TestVerifySignature_PublishRoundTrip(t *testing.T) {
	privPEM, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("failed to generate keypair: %v", err)
	}
	publicKey := string(pubSSH)

	t.Run("multi-line body verifies", func(t *testing.T) {
		body := "First paragraph of the post.\n\nSecond paragraph with more text.\n"
		published, bareSig := signPublishStyle(t, privPEM, body)

		result := verifySignature(published, publicKey, bareSig, false)
		if result.Status != "valid" {
			t.Errorf("expected 'valid', got %q: %s", result.Status, result.Message)
		}
	})

	t.Run("single-line body verifies", func(t *testing.T) {
		body := "The web is already federated. Every domain is a node.\n"
		published, bareSig := signPublishStyle(t, privPEM, body)

		result := verifySignature(published, publicKey, bareSig, false)
		if result.Status != "valid" {
			t.Errorf("expected 'valid', got %q: %s", result.Status, result.Message)
		}
	})

	t.Run("tampered body is rejected", func(t *testing.T) {
		body := "Original content that was signed.\n"
		published, bareSig := signPublishStyle(t, privPEM, body)
		tampered := strings.Replace(published, "Original content", "Tampered content", 1)

		result := verifySignature(tampered, publicKey, bareSig, false)
		if result.Status != "invalid" {
			t.Errorf("expected 'invalid' for tampered body, got %q", result.Status)
		}
	})

	t.Run("tampered frontmatter is rejected", func(t *testing.T) {
		body := "Body stays the same.\n"
		published, bareSig := signPublishStyle(t, privPEM, body)
		tampered := strings.Replace(published, "title: Test Post", "title: Forged Title", 1)

		result := verifySignature(tampered, publicKey, bareSig, false)
		if result.Status != "invalid" {
			t.Errorf("expected 'invalid' for tampered frontmatter, got %q", result.Status)
		}
	})
}

// TestVerifySignature_CommentRoundTrip is the regression test for the
// blessed-comment cache bug: comments inject an `author:` line into the written
// frontmatter AFTER signing (comment.go), so the verifier MUST drop that line
// (isComment=true) to reproduce the signed bytes. Verifying a comment with the
// post extraction (isComment=false) leaves the unsigned author line in the hash
// and wrongly reports INVALID — which made the cache refuse every blessed comment.
func TestVerifySignature_CommentRoundTrip(t *testing.T) {
	privPEM, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("failed to generate keypair: %v", err)
	}
	publicKey := string(pubSSH)

	// Sign WITHOUT the author line (as comment.go does)...
	canonicalBody := signing.CanonicalizeContent("Markdown is the lingua franca of LLMs\n")
	unsignedFrontmatter := "---\n" +
		"title: \"Re: hello\"\n" +
		"type: comment\n" +
		"published: 2026-05-31T02:37:03Z\n" +
		"generator: polis-cli-go/test\n" +
		"in-reply-to:\n" +
		"  url: https://discover.polis.pub/x.md\n" +
		"  root-post: https://discover.polis.pub/x.md\n" +
		"current-version: sha256:0000\n" +
		"---"
	sigPEM, err := signing.SignContent([]byte(signing.CanonicalizeContent(unsignedFrontmatter+"\n\n"+canonicalBody)), privPEM)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	bareSig := bareBase64FromPEM(sigPEM)

	// ...then write the file WITH author (after published:) + signature injected.
	published := "---\n" +
		"title: \"Re: hello\"\n" +
		"type: comment\n" +
		"published: 2026-05-31T02:37:03Z\n" +
		"author: alice.polis.pub\n" +
		"generator: polis-cli-go/test\n" +
		"in-reply-to:\n" +
		"  url: https://discover.polis.pub/x.md\n" +
		"  root-post: https://discover.polis.pub/x.md\n" +
		"current-version: sha256:0000\n" +
		"signature: " + bareSig + "\n" +
		"---\n\n" + canonicalBody

	if res := verifySignature(published, publicKey, bareSig, true); res.Status != "valid" {
		t.Errorf("comment must verify valid with isComment=true, got %q: %s", res.Status, res.Message)
	}
	if res := verifySignature(published, publicKey, bareSig, false); res.Status != "invalid" {
		t.Errorf("the injected author line must break post-style verification (isComment=false), got %q", res.Status)
	}
}
