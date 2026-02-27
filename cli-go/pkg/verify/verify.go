// Package verify provides remote signature verification for polis content.
package verify

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// ContentType represents the type of content (post or comment).
type ContentType string

const (
	TypePost    ContentType = "post"
	TypeComment ContentType = "comment"
)

// VerificationResult contains the results of content verification.
type VerificationResult struct {
	URL              string      `json:"url"`
	Type             ContentType `json:"type"`
	Title            string      `json:"title"`
	Published        string      `json:"published"`
	CurrentVersion   string      `json:"current_version"`
	Generator        string      `json:"generator,omitempty"`
	InReplyTo        string      `json:"in_reply_to,omitempty"`
	Author           string      `json:"author,omitempty"`
	Signature        SignatureResult `json:"signature"`
	Hash             HashResult      `json:"hash"`
	ValidationIssues []string        `json:"validation_issues,omitempty"`
	Body             string          `json:"body"`
}

// SignatureResult contains signature verification status.
type SignatureResult struct {
	Status  string `json:"status"`  // valid, invalid, missing, error
	Message string `json:"message"`
}

// HashResult contains hash verification status.
type HashResult struct {
	Status string `json:"status"` // valid, mismatch, unknown
}

// Frontmatter holds parsed frontmatter fields.
type Frontmatter struct {
	Title          string
	Type           string
	Published      string
	CurrentVersion string
	Signature      string
	Generator      string
	InReplyTo      string
	InReplyToVersion string
}

// VerifyContent verifies the signature and hash of remote polis content.
func VerifyContent(contentURL string) (*VerificationResult, error) {
	client := remote.NewClient()

	// Fetch content
	content, err := client.FetchContent(contentURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch content: %w", err)
	}

	actualURL := contentURL

	// Check for frontmatter - if not found, try alternate extension
	if !strings.HasPrefix(strings.TrimSpace(content), "---") {
		altContent, altURL, err := client.TryAlternateExtension(contentURL)
		if err == nil && strings.HasPrefix(strings.TrimSpace(altContent), "---") {
			content = altContent
			actualURL = altURL
		}
	}

	if !strings.HasPrefix(strings.TrimSpace(content), "---") {
		return nil, fmt.Errorf("content has no frontmatter (not a valid Polis post/comment)")
	}

	// Parse frontmatter
	fm, body, err := parseFrontmatter(content)
	if err != nil {
		return nil, err
	}

	// Detect content type
	contentType := TypePost
	if fm.Type == "comment" || fm.InReplyTo != "" {
		contentType = TypeComment
	}

	// Extract base URL and fetch author info
	baseURL := remote.ExtractBaseURL(actualURL)
	wk, err := client.FetchWellKnown(baseURL)

	var publicKey string
	var authorIdentity string
	if err == nil {
		publicKey = wk.PublicKey
		// Extract domain from the fetch URL (the requesting actor already knows the domain)
		authorIdentity = extractDomainFromBaseURL(baseURL)
		if authorIdentity == "" && wk.Email != "" {
			authorIdentity = wk.Email
		}
	}

	// Verify signature
	sigResult := verifySignature(content, publicKey, fm.Signature, authorIdentity)

	// Verify hash
	hashResult := verifyHash(body, fm.CurrentVersion)

	// Collect validation issues
	var issues []string
	if fm.Title == "" {
		issues = append(issues, "missing_title")
	}
	if fm.Published == "" {
		issues = append(issues, "missing_published")
	}
	if fm.CurrentVersion == "" {
		issues = append(issues, "missing_current_version")
	}
	if fm.Signature == "" {
		issues = append(issues, "missing_signature")
	}
	if contentType == TypeComment && fm.InReplyTo == "" {
		issues = append(issues, "missing_in_reply_to")
	}

	return &VerificationResult{
		URL:              actualURL,
		Type:             contentType,
		Title:            fm.Title,
		Published:        fm.Published,
		CurrentVersion:   fm.CurrentVersion,
		Generator:        fm.Generator,
		InReplyTo:        fm.InReplyTo,
		Author:           authorIdentity,
		Signature:        sigResult,
		Hash:             hashResult,
		ValidationIssues: issues,
		Body:             body,
	}, nil
}

// parseFrontmatter extracts frontmatter fields and body from content.
func parseFrontmatter(content string) (*Frontmatter, string, error) {
	lines := strings.Split(content, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return nil, "", fmt.Errorf("invalid frontmatter format")
	}

	var fm Frontmatter
	var bodyStart int
	inFrontmatter := true

	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "---" {
			bodyStart = i + 1
			inFrontmatter = false
			break
		}

		if inFrontmatter {
			// Parse key: value pairs
			if idx := strings.Index(line, ":"); idx > 0 {
				key := strings.TrimSpace(line[:idx])
				value := strings.TrimSpace(line[idx+1:])

				switch key {
				case "title":
					fm.Title = value
				case "type":
					fm.Type = value
				case "published":
					fm.Published = value
				case "current-version":
					fm.CurrentVersion = value
				case "signature":
					fm.Signature = value
				case "generator":
					fm.Generator = value
				}
			}

			// Handle in-reply-to block (multi-line)
			if strings.HasPrefix(line, "in-reply-to:") {
				// Look ahead for url: line
				for j := i + 1; j < len(lines) && !strings.HasPrefix(lines[j], "---"); j++ {
					trimmed := strings.TrimSpace(lines[j])
					if strings.HasPrefix(trimmed, "url:") {
						fm.InReplyTo = strings.TrimSpace(strings.TrimPrefix(trimmed, "url:"))
					} else if strings.HasPrefix(trimmed, "version:") {
						fm.InReplyToVersion = strings.TrimSpace(strings.TrimPrefix(trimmed, "version:"))
					} else if !strings.HasPrefix(trimmed, " ") && trimmed != "" && !strings.HasPrefix(trimmed, "url:") && !strings.HasPrefix(trimmed, "version:") {
						break
					}
				}
			}
		}
	}

	// Extract body (strip leading blank line if present)
	var body string
	if bodyStart < len(lines) {
		bodyLines := lines[bodyStart:]
		body = strings.Join(bodyLines, "\n")
		body = strings.TrimPrefix(body, "\n") // Remove leading blank line
	}

	return &fm, body, nil
}

// verifySignature verifies the content signature against the public key.
func verifySignature(content, publicKey, signature, authorIdentity string) SignatureResult {
	if publicKey == "" {
		return SignatureResult{
			Status:  "error",
			Message: "Could not fetch public key from .well-known/polis",
		}
	}

	if signature == "" {
		return SignatureResult{
			Status:  "missing",
			Message: "Content has no signature",
		}
	}

	// Extract the content to verify (everything before signature line)
	contentToVerify := extractContentToSign(content, authorIdentity)

	// Verify signature
	valid, err := signing.VerifySignature([]byte(contentToVerify), []byte(publicKey), signature)
	if err != nil || !valid {
		return SignatureResult{
			Status:  "invalid",
			Message: "SIGNATURE DOES NOT MATCH - content may have been tampered with",
		}
	}

	return SignatureResult{
		Status:  "valid",
		Message: "Signature verified against author's public key",
	}
}

// extractContentToSign extracts the content portion that should be signed.
// This matches the bash CLI behavior: everything up to the signature line.
func extractContentToSign(content, authorIdentity string) string {
	lines := strings.Split(content, "\n")
	var result []string

	for _, line := range lines {
		if strings.HasPrefix(line, "signature:") {
			break
		}
		result = append(result, line)
	}

	// Add trailing newline for consistency
	return strings.Join(result, "\n") + "\n"
}

// verifyHash verifies the content hash against the current-version field.
func verifyHash(body, currentVersion string) HashResult {
	if currentVersion == "" {
		return HashResult{Status: "unknown"}
	}

	// Remove sha256: prefix if present
	expectedHash := strings.TrimPrefix(currentVersion, "sha256:")

	// Canonicalize and hash the body
	canonicalBody := canonicalizeContent(body)
	hash := sha256Hash([]byte(canonicalBody))

	if hash == expectedHash {
		return HashResult{Status: "valid"}
	}

	// Try without canonicalization (backwards compatibility)
	directHash := sha256Hash([]byte(body))
	if directHash == expectedHash {
		return HashResult{Status: "valid"}
	}

	return HashResult{Status: "mismatch"}
}

// canonicalizeContent normalizes content for consistent hashing.
// Strips leading empty lines, trailing whitespace, and ensures single trailing newline.
func canonicalizeContent(content string) string {
	// Strip leading empty lines
	content = strings.TrimLeft(content, "\n")

	// Strip trailing whitespace from each line and trailing empty lines
	lines := strings.Split(content, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}

	// Remove trailing empty lines
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	return strings.Join(lines, "\n") + "\n"
}

// sha256Hash computes SHA-256 hash of content.
func sha256Hash(content []byte) string {
	hash := sha256.Sum256(content)
	return fmt.Sprintf("%x", hash)
}

// extractDomainFromBaseURL extracts the host from a base URL.
func extractDomainFromBaseURL(u string) string {
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	if idx := strings.Index(u, "/"); idx >= 0 {
		return u[:idx]
	}
	return u
}
