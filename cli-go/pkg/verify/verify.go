// Package verify provides remote signature verification for polis content.
package verify

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/sitecheck"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// ContentType represents the type of content (post or comment).
type ContentType string

const (
	TypePost    ContentType = "post"
	TypeComment ContentType = "comment"
)

// VerificationResult contains the results of content verification.
type VerificationResult struct {
	URL              string          `json:"url"`
	Type             ContentType     `json:"type"`
	Title            string          `json:"title"`
	Published        string          `json:"published"`
	CurrentVersion   string          `json:"current_version"`
	Generator        string          `json:"generator,omitempty"`
	InReplyTo        string          `json:"in_reply_to,omitempty"`
	Author           string          `json:"author,omitempty"`
	Signature        SignatureResult `json:"signature"`
	Hash             HashResult      `json:"hash"`
	ValidationIssues []string        `json:"validation_issues,omitempty"`
	Body             string          `json:"body"`

	// Witness is the second axis beside Signature.Key (SIGNET epic 32): did a
	// discovery service countersign THESE bytes, and when. Read from the site's
	// published witness set. ⛔ Never affects Signature or Hash — an unwitnessed
	// artifact is a weaker claim, not an invalid one.
	Witness *sitecheck.WitnessResult `json:"witness,omitempty"`
}

// SignatureResult contains signature verification status.
type SignatureResult struct {
	Status  string `json:"status"` // valid, invalid, missing, error
	Message string `json:"message"`

	// Key says WHICH of the site's keys verified a valid signature — the one the
	// site publishes now, or a retired one its published key history resolves for
	// the artifact's claimed signing time (SIGNET epic 31 D4). Nil unless valid.
	//
	// ⚠️ Status stays "valid" for a retired key. The distinction lives HERE, so
	// a consumer applying policy reads Key.Source rather than a second status
	// word every existing caller would have to learn.
	Key *sitecheck.KeyUsed `json:"key,omitempty"`
}

// HashResult contains hash verification status.
type HashResult struct {
	Status string `json:"status"` // valid, mismatch, unknown
}

// Frontmatter holds parsed frontmatter fields.
type Frontmatter struct {
	Title            string
	Type             string
	Published        string
	CurrentVersion   string
	Signature        string
	Generator        string
	InReplyTo        string
	InReplyToVersion string
}

// VerifyContent verifies the signature and hash of remote polis content.
//
// ⛔ It does NOT evaluate the witness axis, and that is deliberate (SIGNET epic
// 32). Rosie's blessed-comment cache and the webapp's sync call this for every
// comment they ingest; witness evaluation costs a witness-file fetch per site
// and a discovery-service key fetch, which on the hosted fleet would be an actor
// behaviour change no epic scoped — and would run into the DS public-key rate
// limit. Result.Witness is nil here. A caller that wants the axis — `polis
// preview` — calls VerifyContentWitnessed.
func VerifyContent(contentURL string) (*VerificationResult, error) {
	return verifyContent(contentURL, false, "")
}

// VerifyContentWitnessed is VerifyContent plus the witness axis (SIGNET epic 32
// D8): whether a discovery service countersigned these bytes, read from the
// site's published witness set. It never changes Signature or Hash.
//
// requestID is sent as X-Request-Id on the discovery-service key fetch the
// witness check may make; "" sends none.
func VerifyContentWitnessed(contentURL, requestID string) (*VerificationResult, error) {
	return verifyContent(contentURL, true, requestID)
}

func verifyContent(contentURL string, withWitness bool, requestID ...string) (*VerificationResult, error) {
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
	wk, chain, chainNote, witnessesPointer, err := fetchIdentity(client, baseURL)

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
	sigResult := verifySignatureWithHistory(content, publicKey, fm.Signature, signing.MarkdownObjectTypeFor(string(contentType), false), chain, chainNote, fm.Published)

	// Verify hash
	hashResult := verifyHash(body, fm.CurrentVersion)

	// Witness axis (SIGNET epic 32 D8). The artifact is already verified or not;
	// this only adds or withholds a claim. The set comes from the site; the DS
	// key comes from the DS the witness names, and an unreachable DS leaves the
	// witness "unverifiable" rather than failing anything.
	var witness *sitecheck.WitnessResult
	if withWitness {
		witness = witnessFor(client, baseURL, witnessesPointer, actualURL, content, signing.MarkdownObjectTypeFor(string(contentType), false), fm.CurrentVersion, requestID[0])
	}

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
		Witness:          witness,
	}, nil
}

// witnessFor evaluates the witness axis for one fetched artifact.
func witnessFor(client *remote.Client, baseURL, pointer, artifactURL, content string, typ signing.ObjectType, currentVersion, requestID string) *sitecheck.WitnessResult {
	artifactHash := discovery.ArtifactHash(signing.MarkdownSigningBase(content, typ))
	if pointer == "" || strings.Contains(pointer, "://") {
		return sitecheck.CheckContentWitnesses(nil, artifactHash, currentVersion, nil)
	}
	body, err := client.FetchContent(strings.TrimSuffix(baseURL, "/") + "/" + strings.TrimPrefix(pointer, "/"))
	if err != nil {
		return sitecheck.CheckContentWitnesses(nil, artifactHash, currentVersion, nil)
	}
	file, err := site.WitnessesFromBytes([]byte(body))
	if err != nil {
		return sitecheck.CheckContentWitnesses(nil, artifactHash, currentVersion, nil)
	}
	var set []discovery.Witness
	path := artifactURL
	if i := strings.Index(path, "://"); i >= 0 {
		if j := strings.Index(path[i+3:], "/"); j >= 0 {
			path = path[i+3+j:]
		}
	}
	for key, ws := range file.Witnesses {
		if strings.HasSuffix(key, path) {
			set = append(set, ws...)
		}
	}
	return sitecheck.CheckContentWitnesses(set, artifactHash, currentVersion, sitecheck.NewDSKeyLookup(nil, requestID))
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
			// Parse key: value pairs. Keys are matched by STRUCTURAL POSITION:
			// an indented line is a child of the key above it (the licence
			// block, in-reply-to's url) and never a field of its own. Trimming
			// first — as this did until epic 08 — is the same defect class as
			// the signing base's old document-wide prefix scan.
			if idx := strings.Index(line, ":"); idx > 0 && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
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

// fetchIdentity reads the author's .well-known/polis ONCE, for both the key and
// the key history (SIGNET epic 31): the history is inside the identity document,
// so resolving a retired key costs no second request.
//
// The domain the history's handovers are checked against is the host fetched
// from, port stripped — the same derivation `polis validate <url>` uses and the
// rotation paths sign. Nothing inside the content chooses it (epic 31: "the
// verifier resolves the key from where the content was SERVED").
//
// It also returns the site's `witnesses` pointer (SIGNET epic 32), from the same
// document, so finding the witness set costs no extra identity fetch.
func fetchIdentity(client *remote.Client, baseURL string) (*remote.WellKnown, *site.KeyHistoryBlock, string, string, error) {
	body, err := client.FetchContent(strings.TrimSuffix(baseURL, "/") + "/.well-known/polis")
	if err != nil {
		return nil, nil, "", "", fmt.Errorf("failed to fetch .well-known/polis: %w", err)
	}
	var wk remote.WellKnown
	if err := json.Unmarshal([]byte(body), &wk); err != nil {
		return nil, nil, "", "", fmt.Errorf("failed to parse .well-known/polis: %w", err)
	}
	pointer := site.WitnessesPointerFromWellKnown([]byte(body))
	block, herr := site.KeyHistoryFromWellKnown([]byte(body))
	if herr != nil {
		return &wk, nil, "", pointer, nil
	}
	chain, note := sitecheck.ResolvingChain(block, polisurl.ExtractDomain(baseURL), wk.PublicKey)
	return &wk, chain, note, pointer, nil
}

// verifySignature verifies the content signature against the public key alone.
func verifySignature(content, publicKey, signature string, typ signing.ObjectType) SignatureResult {
	return verifySignatureWithHistory(content, publicKey, signature, typ, nil, "", "")
}

// verifySignatureWithHistory verifies the content signature against the site's
// current key and, only if that fails, against the key its published history
// resolves for published — the one rule in sitecheck.VerifySignatureWithHistory.
func verifySignatureWithHistory(content, publicKey, signature string, typ signing.ObjectType, chain *site.KeyHistoryBlock, chainNote, published string) SignatureResult {
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

	// Published files store the signature as bare base64 in the `signature:`
	// frontmatter field. signing.VerifySignature -> parseSSHSignature requires a
	// PEM-armored SSH SIGNATURE block, so rewrap it (matching judge/patrol's
	// reconstructSSHSignature). If it already looks like a PEM block, leave it.
	sshSig := strings.TrimSpace(signature)
	if !strings.HasPrefix(sshSig, "-----BEGIN") {
		sshSig = reconstructSSHSignature(sshSig)
	}

	// Reconstruct the exact bytes that were signed and verify. typ selects the
	// base: comments drop the after-signing-injected author line.
	contentToVerify := signing.MarkdownSigningBase(content, typ)
	status, used, err := sitecheck.VerifySignatureWithHistory([]byte(contentToVerify), sshSig, []byte(publicKey), chain, published)
	if status != sitecheck.SigValid {
		msg := "SIGNATURE DOES NOT MATCH - content may have been tampered with"
		// Only when the key history had something to say. A plain current-key
		// failure keeps its message byte-for-byte.
		switch {
		case chain != nil && len(chain.History) > 0 && err != nil:
			msg += " (" + err.Error() + ")"
		case chainNote != "":
			msg += " (" + chainNote + ")"
		}
		return SignatureResult{Status: "invalid", Message: msg}
	}

	if used.Source == sitecheck.KeyRetired {
		return SignatureResult{
			Status:  "valid",
			Message: "Signature " + used.Describe(),
			Key:     used,
		}
	}
	return SignatureResult{
		Status:  "valid",
		Message: "Signature verified against author's public key",
		Key:     used,
	}
}

// reconstructSSHSignature rewraps a bare-base64 signature into the PEM-armored
// SSH SIGNATURE block signing.VerifySignature expects. An ALIAS of the shared
// predicate — the same function value Judge and Patrol call.
var reconstructSSHSignature = sitecheck.ReconstructSSHSignature

// verifyHash verifies the content hash against the current-version field.
func verifyHash(body, currentVersion string) HashResult {
	if currentVersion == "" {
		return HashResult{Status: "unknown"}
	}

	// Remove sha256: prefix if present
	expectedHash := strings.TrimPrefix(currentVersion, "sha256:")

	// ⚠️ DELIBERATELY NOT sitecheck.VerifyHash. That predicate — the one Patrol
	// and Judge run — also accepts a raw-byte hash; this one accepts only the
	// canonical form (R20-C-F9, below). The divergence is real and unreconciled;
	// see epic 20's Escalations.

	// Canonicalize and hash the body. The non-canonical raw-byte fallback that
	// once lived here was removed (R20-C-F9): dual-accepting both canonical and
	// raw-byte hashes widened the collision surface without serving a real
	// backwards-compatibility need — every signing path canonicalizes before
	// hashing, so any historical raw-byte hash on disk was a bug to be surfaced,
	// not silently accepted.
	canonicalBody := signing.CanonicalizeContent(body)
	hash := sha256Hash([]byte(canonicalBody))

	if hash == expectedHash {
		return HashResult{Status: "valid"}
	}

	return HashResult{Status: "mismatch"}
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
