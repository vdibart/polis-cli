// Package sitecheck holds the predicates that answer "is this artifact
// internally consistent?" — once, for every caller that asks.
//
// It exists because the same question was being answered by three separate
// copies of the same code: Patrol (local filesystem sweep), Judge (HTTP-served
// verification) and pkg/verify (remote URL verification) each carried their own
// frontmatter parser, SSH-signature rewrapper and hash comparator. A copy that
// agrees today is a copy that drifts tomorrow — signing.CanonicalizeContent
// exists because that already happened once.
//
// The rule this package is built to keep: THE MODE SELECTS WHICH CHECKS APPLY,
// NEVER WHICH IMPLEMENTATION RUNS. Every predicate here is pure — it takes
// bytes and a key, never a path or a URL — so a local sweep, a clone and a
// remote fetch differ only in how they OBTAIN the bytes. Enumeration differs
// legitimately (walk a directory vs. read index.jsonl); verification does not.
//
// ⚠️ Extracted behaviour-identically, on purpose. Patrol compares against
// stored baselines and Judge's findings feed alerts, so a changed message or a
// changed status is a false positive on every tenant at once. When you change a
// predicate here, you are changing it for the fleet.
package sitecheck

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// SignatureStatus reports what was learned about a signature. It is a FACT and
// never a verdict: whether "unsigned" is acceptable belongs to the consumer
// (Law 2), which is why unsigned and invalid are different values rather than
// two flavours of failure.
type SignatureStatus string

const (
	// SigValid — a signature was present and verified against the key.
	SigValid SignatureStatus = "valid"
	// SigInvalid — a signature was present and did NOT verify.
	SigInvalid SignatureStatus = "invalid"
	// SigUnsigned — no signature field. Most artifacts on most sites are
	// unsigned; this is an ordinary state, not a defect.
	SigUnsigned SignatureStatus = "unsigned"
)

// HashStatus reports what was learned about the content hash.
type HashStatus string

const (
	// HashValid — current-version was present and matched the body.
	HashValid HashStatus = "valid"
	// HashMismatch — current-version was present and did NOT match.
	HashMismatch HashStatus = "mismatch"
	// HashAbsent — no current-version field to check against.
	HashAbsent HashStatus = "absent"
)

// ContentResult is what one post or comment yielded. Signature and Hash are
// reported separately because they fail for different reasons: a bad signature
// means the bytes are not the author's, a bad hash means the body moved under
// a frontmatter that still claims the old version.
type ContentResult struct {
	Signature  SignatureStatus
	SigError   error // why the signature did not verify; context for a human
	Hash       HashStatus
	Body       string
	ParseError error

	// Key says WHICH key verified the signature (epic 31 D4). Nil unless
	// Signature is SigValid.
	Key *KeyUsed

	// CurrentVersion is the artifact's own `current-version`, and ArtifactHash
	// the hash of its signing base (discovery.ArtifactHash) — the two values a
	// discovery-service witness can bind to (SIGNET epic 32).
	CurrentVersion string
	ArtifactHash   string
}

// OK reports whether nothing was found WRONG — which is not the same as
// "everything was checked". An unsigned artifact with no hash is OK because
// nothing failed, and a caller that needs to know what was actually examined
// must read Signature and Hash rather than this. Judge's pass/fail gate wants
// exactly this question, which is why it exists.
func (r ContentResult) OK() bool {
	return r.ParseError == nil && r.Signature != SigInvalid && r.Hash != HashMismatch
}

// VerifyContent verifies a post's or comment's signature and hash from its full
// serialized form — the same bytes whether they came off disk or off the wire.
//
// typ selects the signing base. Comments are signed WITHOUT the author field
// (it is injected into the final frontmatter after signing), so a comment
// verified under the post base fails — the defect that starved Rosie's blessed
// comment cache. Callers that hold parsed frontmatter should ask it
// (Frontmatter.ObjectType) rather than deciding again.
func VerifyContent(content string, pubKey []byte, typ signing.ObjectType) ContentResult {
	return VerifyContentWithHistory(content, pubKey, nil, typ)
}

// VerifyContentWithHistory is VerifyContent for a verifier that also holds the
// site's published key history: a signature the current key rejects is tried
// against the key the history resolves for the artifact's claimed signing time.
// See VerifySignatureWithHistory for the rule, and ResolvingChain for where
// chain must come from. A nil chain is exactly VerifyContent.
//
// ⭐ Judge's served-content check calls this with the tenant's chain since
// SIGNET epic 42 V4 (epic 31 D6 had deferred the actors); Patrol and Judge's
// cross-site check call VerifySignatureWithHistory directly.
func VerifyContentWithHistory(content string, pubKey []byte, chain *site.KeyHistoryBlock, typ signing.ObjectType) ContentResult {
	fm, body, err := ParseFrontmatter(content)
	if err != nil {
		return ContentResult{ParseError: err, Signature: SigUnsigned, Hash: HashAbsent}
	}

	contentToSign := signing.MarkdownSigningBase(content, typ)
	res := ContentResult{
		Body:           body,
		Signature:      SigUnsigned,
		Hash:           HashAbsent,
		CurrentVersion: fm.CurrentVersion,
		ArtifactHash:   discovery.ArtifactHash(contentToSign),
	}

	// An empty pubKey is NOT special-cased: signing.VerifySignature errors on
	// it and the result is SigInvalid carrying that error. That is deliberate —
	// Patrol reports the verifier's own error text and Judge counts the item as
	// failed, and inventing a third "could not check" status here would change
	// both. Whether a key was available at all is a SITE-level question, answered
	// before any content is walked.
	if fm.Signature != "" {
		sshSig := ReconstructSSHSignature(fm.Signature)
		res.Signature, res.Key, res.SigError = VerifySignatureWithHistory([]byte(contentToSign), sshSig, pubKey, chain, fm.Published)
	}

	if fm.CurrentVersion != "" {
		expectedHash := strings.TrimPrefix(fm.CurrentVersion, "sha256:")
		if VerifyHash(body, expectedHash) {
			res.Hash = HashValid
		} else {
			res.Hash = HashMismatch
		}
	}

	return res
}

// Frontmatter holds the frontmatter fields verification needs. Deliberately
// narrow: this is not a general YAML parser and must not become one.
type Frontmatter struct {
	Signature      string
	CurrentVersion string

	// Published is the artifact's claimed signing time. It is INSIDE the
	// signature for both posts and comments, and it is what a verifier asks the
	// key history about — self-reported, so it selects a key and proves nothing.
	Published string

	// Type and InReplyTo select the SIGNING BASE, not the display. Comments are
	// signed without their author line, so getting this wrong reports a valid
	// signature as invalid.
	Type      string
	InReplyTo bool
}

// ObjectType reports which signing base this artifact was signed under. The
// discriminator rule lives in pkg/signing beside the rule it selects, so a
// verifier cannot answer this question differently from the base it then
// applies.
func (f *Frontmatter) ObjectType() signing.ObjectType {
	if f == nil {
		return signing.TypePost
	}
	return signing.MarkdownObjectTypeFor(f.Type, f.InReplyTo)
}

// ParseFrontmatter extracts signature and current-version from YAML
// frontmatter, returning the fields and the body.
//
// ⚠️ It matches keys by STRUCTURAL POSITION: a line whose key is indented is a
// child of the key above it and is skipped. Until epic 08 it trimmed first, so
// a nested `signature` or `current-version` — inside the licence block, say —
// read as a top-level one. That is the same defect class as the signing base's
// old document-wide prefix scan: matching a key by its shape instead of by
// where it sits.
func ParseFrontmatter(content string) (*Frontmatter, string, error) {
	lines := strings.Split(content, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return nil, "", fmt.Errorf("no frontmatter found")
	}

	fm := &Frontmatter{}
	var bodyStart int

	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			bodyStart = i + 1
			break
		}
		if isIndented(lines[i]) {
			continue // a child of the key above, not a field of its own
		}
		if idx := strings.Index(lines[i], ":"); idx > 0 {
			key := strings.TrimSpace(lines[i][:idx])
			value := strings.TrimSpace(lines[i][idx+1:])
			switch key {
			case "signature":
				fm.Signature = value
			case "current-version":
				fm.CurrentVersion = value
			case "published":
				fm.Published = value
			case "type":
				fm.Type = value
			case "in-reply-to":
				fm.InReplyTo = true
			}
		}
	}

	var body string
	if bodyStart < len(lines) {
		body = strings.Join(lines[bodyStart:], "\n")
		body = strings.TrimPrefix(body, "\n")
	}

	return fm, body, nil
}

// isIndented reports whether a frontmatter line is a child of the key above it.
// The distinction is load-bearing: a top-level `signature:` is the artifact's
// signature, an indented one is somebody else's field that happens to share the
// name.
func isIndented(line string) bool {
	return strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
}

// SplitFrontmatterBody splits content into its frontmatter block and its body.
// Returns ("", content) when there is no frontmatter.
func SplitFrontmatterBody(content string) (string, string) {
	lines := strings.Split(content, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return "", content
	}

	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			body := strings.Join(lines[i+1:], "\n")
			body = strings.TrimPrefix(body, "\n")
			return strings.Join(lines[:i+1], "\n"), body
		}
	}
	return "", content
}

// ReconstructSSHSignature rewraps a bare-base64 signature (as stored in the
// `signature:` frontmatter field) into the PEM-armored SSH SIGNATURE block that
// signing.VerifySignature expects.
func ReconstructSSHSignature(base64Body string) string {
	return "-----BEGIN SSH SIGNATURE-----\n" + WrapBase64(base64Body, 76) + "\n-----END SSH SIGNATURE-----"
}

// WrapBase64 breaks a base64 string into lines of the specified width.
// A non-positive width returns s unchanged rather than looping forever.
func WrapBase64(s string, width int) string {
	if width <= 0 {
		return s
	}
	var lines []string
	for len(s) > width {
		lines = append(lines, s[:width])
		s = s[width:]
	}
	if len(s) > 0 {
		lines = append(lines, s)
	}
	return strings.Join(lines, "\n")
}

// VerifyHash checks whether body matches an expected SHA-256 hash, accepting
// EITHER the canonicalized body or the raw bytes.
//
// ⚠️ pkg/verify deliberately accepts only the canonical form (R20-C-F9: the
// raw-byte fallback widened the collision surface for no live need). The two
// have not been reconciled, and this one is the ACTORS' behaviour — Patrol and
// Judge both accept the fallback today, so tightening it here would change what
// the fleet reports. See epic 20's Escalations.
func VerifyHash(body, expectedHash string) bool {
	canonical := signing.CanonicalizeContent(body)
	if SHA256Hex([]byte(canonical)) == expectedHash {
		return true
	}
	if SHA256Hex([]byte(body)) == expectedHash {
		return true
	}
	return false
}

// SHA256Hex computes the hex-encoded SHA-256 hash of data.
func SHA256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}
