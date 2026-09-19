// Package tag manages tag files for the pub.polis.tag content type.
//
// Tags are organizational metadata that let authors tag their own content
// and content from others across the network. Each tag is stored as a JSON
// file at content/pub.polis.core/tag/<name>.json containing a list of target URIs.
package tag

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"
	"github.com/vdibart/polis-cli/cli-go/pkg/index"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// Version is set at startup by the cmd package.
var Version = "dev"

// GetGenerator returns the generator identifier for tag metadata.
func GetGenerator() string {
	return "polis-cli-go/" + Version
}

// TagFile represents a tag file on disk.
type TagFile struct {
	Tag       string      `json:"tag"`
	Targets   []TagTarget `json:"targets"`
	Created   string      `json:"created"`
	Updated   string      `json:"updated"`
	Generator string      `json:"generator"`
	Version   string      `json:"current_version"`
	Signature string      `json:"signature"`

	// unrecognised are the JSON members of the parsed document that this build
	// does not declare — captured by Parse, consulted by Verify and
	// VerifyWithHistory (SIGNET epic 47, tolerant verifiers).
	//
	// ⚠️ Nil for a tag file this process built in memory, and that is correct
	// rather than a gap: there were no foreign bytes to meet.
	unrecognised []string
}

// UnrecognisedFields returns the JSON members this build does not declare, for
// a tag file that came from Parse or Load. Nil for anything built in memory.
//
// A caller that reports a `valid` status MUST also report this when it is
// non-empty: the signature verified over the fields this build knows, and these
// are NOT covered by it (SIGNET epic 47, row 2 of the table).
func (tf *TagFile) UnrecognisedFields() []string {
	if tf == nil {
		return nil
	}
	return tf.unrecognised
}

// Parse reads a tag file from raw JSON bytes, recording any members this build
// does not declare.
//
// ⭐ EVERY PATH THAT TURNS FOREIGN BYTES INTO A TagFile MUST COME THROUGH HERE
// — Load for a file on disk, sitecheck for one fetched over HTTP. A plain
// json.Unmarshal silently drops the unknown members, and then Verify cannot
// tell "this signature is wrong" from "this file was written by something newer
// than me".
func Parse(raw []byte) (*TagFile, error) {
	var tf TagFile
	if err := json.Unmarshal(raw, &tf); err != nil {
		return nil, err
	}
	tf.unrecognised = signing.UnrecognisedFields(raw, TagFile{})
	return &tf, nil
}

// TagTarget represents a single tagged content reference.
type TagTarget struct {
	URI   string `json:"uri"`
	Added string `json:"added"`
}

var tagNameRe = regexp.MustCompile(`[^a-z0-9-]`)

// NormalizeTagName normalizes a tag name: lowercase, trim, only alphanumeric
// and hyphens, 1-64 chars. Returns an error if the result is empty or invalid.
func NormalizeTagName(name string) (string, error) {
	name = strings.TrimSpace(strings.ToLower(name))

	// Replace spaces and underscores with hyphens
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, "_", "-")

	// Remove anything that isn't a-z, 0-9, or hyphen
	name = tagNameRe.ReplaceAllString(name, "")

	// Collapse multiple hyphens
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}

	// Trim leading/trailing hyphens
	name = strings.Trim(name, "-")

	if name == "" {
		return "", fmt.Errorf("tag name is empty after normalization")
	}
	if len(name) > 64 {
		name = name[:64]
		name = strings.TrimRight(name, "-")
	}
	if name == "" {
		return "", fmt.Errorf("tag name is empty after truncation")
	}

	return name, nil
}

// TagPath returns the filesystem path for a tag file.
//
// The path is a literal on purpose. The CORE bundle's layout is fixed by design
// and not user-configurable, so `content/pub.polis.core/tag` is correct here
// and not debt (settled 2026-09-02). The same holds for
// attestation.Dir. Resolving through the bundle is right only when reading a
// FOREIGN site's declarations, as `polis clone` does.
func TagPath(dataDir, tagName string) string {
	return filepath.Join(dataDir, "content", "pub.polis.core", "tag", tagName+".json")
}

// tagDir returns the directory containing tag files. A literal for the same
// reason as TagPath.
func tagDir(dataDir string) string {
	return filepath.Join(dataDir, "content", "pub.polis.core", "tag")
}

// Load reads and parses a tag file from disk.
func Load(path string) (*TagFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read tag file: %w", err)
	}

	tf, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse tag file: %w", err)
	}

	return tf, nil
}

// ListTags returns all tag files in the site. A malformed file is skipped
// rather than failing the listing; ScanTags is the same walk and also names
// what it skipped.
func ListTags(dataDir string) ([]TagFile, error) {
	tags, _, err := ScanTags(dataDir)
	return tags, err
}

// MalformedTag names a tag file that does not parse. ⛔ Parse failure only: a
// tag file with an unrecognised field parses (SIGNET epic 47), and so does one
// whose signature fails.
type MalformedTag struct {
	Name  string `json:"name"`  // file name within the tag directory
	Error string `json:"error"` // why it did not parse
}

// ScanTags is ListTags that also returns the files it skipped because they do
// not parse, in directory order (Signet epic 44 C2 — a reporter must be able to
// say what it could not read).
func ScanTags(dataDir string) ([]TagFile, []MalformedTag, error) {
	dir := tagDir(dataDir)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("read tag directory: %w", err)
	}

	var tags []TagFile
	var malformed []MalformedTag
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue // unreadable is not malformed: it says nothing about the bytes
		}
		tf, err := Parse(raw)
		if err != nil {
			malformed = append(malformed, MalformedTag{Name: entry.Name(), Error: err.Error()})
			continue
		}
		tags = append(tags, *tf)
	}

	return tags, malformed, nil
}

// ApplyTag adds a target URI to a tag, creating the tag file if it doesn't exist.
// The file is signed after mutation.
func ApplyTag(dataDir, tagName, targetURI string, privateKey []byte, generator ...string) (*TagFile, error) {
	gen := GetGenerator()
	if len(generator) > 0 && generator[0] != "" {
		gen = generator[0]
	}
	name, err := NormalizeTagName(tagName)
	if err != nil {
		return nil, fmt.Errorf("invalid tag name: %w", err)
	}

	targetURI = strings.TrimSpace(targetURI)
	if targetURI == "" {
		return nil, fmt.Errorf("target URI is required")
	}

	path := TagPath(dataDir, name)
	now := time.Now().UTC().Format(time.RFC3339)

	var tf *TagFile

	// Load existing or create new
	if data, err := os.ReadFile(path); err == nil {
		// Through Parse, never a bare json.Unmarshal (Signet epic 47).
		if tf, err = Parse(data); err != nil {
			return nil, fmt.Errorf("parse existing tag file: %w", err)
		}
	} else if os.IsNotExist(err) {
		tf = &TagFile{
			Tag:     name,
			Created: now,
		}
	} else {
		return nil, fmt.Errorf("read tag file: %w", err)
	}

	// Check for duplicate (idempotent)
	for _, t := range tf.Targets {
		if t.URI == targetURI {
			return tf, nil // already tagged
		}
	}

	// Add target
	tf.Targets = append(tf.Targets, TagTarget{
		URI:   targetURI,
		Added: now,
	})
	tf.Updated = now
	tf.Generator = gen

	// Sign and write
	if err := signAndWrite(tf, path, privateKey); err != nil {
		return nil, err
	}
	refreshIndex(dataDir)

	return tf, nil
}

// RemoveTarget removes a target URI from a tag and re-signs.
// Returns the updated tag file, or an error if the tag or target doesn't exist.
func RemoveTarget(dataDir, tagName, targetURI string, privateKey []byte, generator ...string) (*TagFile, error) {
	gen := GetGenerator()
	if len(generator) > 0 && generator[0] != "" {
		gen = generator[0]
	}
	name, err := NormalizeTagName(tagName)
	if err != nil {
		return nil, fmt.Errorf("invalid tag name: %w", err)
	}

	path := TagPath(dataDir, name)
	tf, err := Load(path)
	if err != nil {
		return nil, fmt.Errorf("load tag: %w", err)
	}

	// Find and remove
	found := false
	var remaining []TagTarget
	for _, t := range tf.Targets {
		if t.URI == targetURI {
			found = true
			continue
		}
		remaining = append(remaining, t)
	}

	if !found {
		return nil, fmt.Errorf("target %q not found in tag %q", targetURI, name)
	}

	tf.Targets = remaining
	tf.Updated = time.Now().UTC().Format(time.RFC3339)
	tf.Generator = gen

	if err := signAndWrite(tf, path, privateKey); err != nil {
		return nil, err
	}
	refreshIndex(dataDir)

	return tf, nil
}

// DeleteTag removes an entire tag file from disk.
func DeleteTag(dataDir, tagName string) error {
	name, err := NormalizeTagName(tagName)
	if err != nil {
		return fmt.Errorf("invalid tag name: %w", err)
	}

	path := TagPath(dataDir, name)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("tag %q does not exist", name)
		}
		return fmt.Errorf("delete tag file: %w", err)
	}
	// A deleted tag must leave the index too, or the site keeps advertising a
	// file that 404s.
	refreshIndex(dataDir)

	return nil
}

// refreshIndex brings the site's tag entries in index.jsonl up to date, AT
// WRITE TIME — the way publish does for a post.
//
// ⛔ Signet epic 37 D1: tags used to reach the index only on `polis rebuild`.
// It is the SAME partial rebuild `polis rebuild --tags` runs rather than an
// append, so every write — apply, remove, delete — leaves the file exactly as a
// rebuild would, and a tag whose `current_version` changed is replaced rather
// than duplicated.
//
// ⚠️ Non-fatal: the tag file is already signed and written. Medic heals a missed
// entry, and `polis rebuild --tags` does by hand.
func refreshIndex(dataDir string) {
	if _, err := index.RebuildContentIndex(dataDir, []string{index.EntryTypeTag}); err != nil {
		fmt.Fprintf(os.Stderr, "[!] tag written, but index.jsonl was not updated: %v (run `polis rebuild --tags`)\n", err)
	}
}

// signAndWrite computes the signature and writes the tag file to disk.
//
// ⛔ It REFUSES to rewrite a tag file carrying members this build does not
// declare (signing.GuardRewrite): the rewrite would drop them and sign the rest
// with the user's key. The way past is RewriteUnsigned.
func signAndWrite(tf *TagFile, path string, privateKey []byte) error {
	if err := signing.GuardRewrite("tag file", path, TagFile{}); err != nil {
		return err
	}
	// Compute canonical JSON for signing (exclude signature and current_version)
	canonical, err := canonicalJSON(tf)
	if err != nil {
		return fmt.Errorf("canonical JSON: %w", err)
	}

	// Compute version hash
	hash := sha256.Sum256(canonical)
	tf.Version = "sha256:" + hex.EncodeToString(hash[:])

	// Sign
	sig, err := signing.SignContent(canonical, privateKey)
	if err != nil {
		return fmt.Errorf("sign tag: %w", err)
	}
	tf.Signature = sig

	return writeTagFile(tf, path)
}

// RewriteUnsigned is the ESCAPE HATCH for a user stranded by signAndWrite's
// refusal: it rewrites the tag file at path from the members this build
// declares, WITHOUT a signature, and returns what it dropped.
//
// ⛔ It never signs — a signature must not cover content the signer's software
// could not read. current_version is recomputed, because it is a hash of the
// bytes rather than a claim, and the index is refreshed to match. A file with
// nothing unrecognised is left untouched.
func RewriteUnsigned(dataDir, path string) ([]string, error) {
	tf, err := Load(path)
	if err != nil {
		return nil, err
	}
	dropped := tf.UnrecognisedFields()
	if len(dropped) == 0 {
		return nil, nil
	}
	canonical, err := canonicalJSON(tf)
	if err != nil {
		return nil, fmt.Errorf("canonical JSON: %w", err)
	}
	hash := sha256.Sum256(canonical)
	tf.Version = "sha256:" + hex.EncodeToString(hash[:])
	tf.Signature = ""
	if err := writeTagFile(tf, path); err != nil {
		return nil, err
	}
	refreshIndex(dataDir)
	return dropped, nil
}

// writeTagFile marshals and atomically writes a tag file as-is.
func writeTagFile(tf *TagFile, path string) error {
	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create tag directory: %w", err)
	}

	// Write
	data, err := json.MarshalIndent(tf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tag: %w", err)
	}
	data = append(data, '\n')

	// R23-6: write atomically (tmp+rename) so a crash mid-write can't leave a
	// truncated tag file. Mode stays 0644 — tag files are PUBLIC served content
	// (content/pub.polis.core/tag/), so don't tighten to 0600 (R11-17 lesson).
	if err := atomicfile.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write tag file: %w", err)
	}

	return nil
}

// canonicalJSON produces a deterministic JSON representation for signing.
// Excludes the signature and current_version fields.
// CanonicalJSON is canonicalJSON for a verifier outside this package that must
// recompute a tag file's current_version from its bytes — `polis validate`'s
// index-consistency check and its per-URL record check (Signet epic 42). One
// function, so the version a verifier recomputes is the one signAndWrite wrote.
func CanonicalJSON(tf *TagFile) ([]byte, error) {
	return canonicalJSON(tf)
}

func canonicalJSON(tf *TagFile) ([]byte, error) {
	// Build a signing-only structure
	signable := struct {
		Tag       string      `json:"tag"`
		Targets   []TagTarget `json:"targets"`
		Created   string      `json:"created"`
		Updated   string      `json:"updated"`
		Generator string      `json:"generator"`
	}{
		Tag:       tf.Tag,
		Targets:   tf.Targets,
		Created:   tf.Created,
		Updated:   tf.Updated,
		Generator: tf.Generator,
	}

	// Ensure nil targets serialize as empty array
	if signable.Targets == nil {
		signable.Targets = []TagTarget{}
	}

	return json.Marshal(signable)
}

// SignatureStatus is the outcome of checking a tag file's signature.
//
//   - StatusUnsigned — no signature. A fact, not a defect: tag files written
//     before signing shipped carry none.
//   - StatusValid    — signature present and verifies.
//   - StatusInvalid  — signature present and does NOT verify. Report it; what
//     it MEANS is the consumer's call.
//   - StatusUnknown  — could not check (no public key available). Not a
//     finding: "I did not look" is not "I looked and it was wrong".
//
// ⚠️ pub.polis.tag has been SIGNED since it shipped and, until now, NOTHING
// verified it — not Patrol, not Judge, not the v1 API. A signature nobody
// checks is decoration.
type SignatureStatus string

const (
	StatusUnsigned SignatureStatus = "unsigned"
	StatusValid    SignatureStatus = "valid"
	StatusInvalid  SignatureStatus = "invalid"
	StatusUnknown  SignatureStatus = "unknown"
)

// Verify checks tf.Signature against publicKeySSH. It reports a status and
// never an opinion: the caller decides what the status means (Law 2).
//
// Mirrors following.Verify and metadata.VerifyBlessed exactly, including the
// rule that the returned error is context for a human rather than a second
// channel of truth. Read the status.
func Verify(tf *TagFile, publicKeySSH []byte) (SignatureStatus, error) {
	if tf == nil || tf.Signature == "" {
		return StatusUnsigned, nil
	}
	if len(publicKeySSH) == 0 {
		return StatusUnknown, fmt.Errorf("no public key to verify against")
	}

	canonical, err := canonicalJSON(tf)
	if err != nil {
		return StatusUnknown, fmt.Errorf("canonical JSON: %w", err)
	}

	ok, err := signing.VerifySignature(canonical, publicKeySSH, tf.Signature)

	// SIGNET epic 47 — the tolerance rule. A rebuild that FAILS while the
	// document carries members this build does not declare is "I could not
	// check this", never "this is forged". ⚠️ The `valid` row is silent on
	// purpose — see UnrecognisedFields.
	switch out := signing.Resolve(ok && err == nil, tf.unrecognised); out.Status {
	case signing.StatusUnknown:
		return StatusUnknown, errors.New(out.Explain())
	case signing.StatusInvalid:
		if err != nil {
			return StatusInvalid, fmt.Errorf("signature does not verify: %w", err)
		}
		return StatusInvalid, fmt.Errorf("signature does not verify against the site identity key")
	}
	return StatusValid, nil
}

// VerifyWithHistory is Verify for a site that may have rotated its key: the
// current key first, then the key the site's published history resolves for the
// tag file's CLAIMED signing time — `updated`, which every write stamps before
// signing (SIGNET epic 44, E5-wide).
//
// chain must already be trusted (sitecheck.ResolvingChain); nil means current key
// only, which is exactly Verify. retired is the history entry that verified it,
// nil for the current key.
func VerifyWithHistory(tf *TagFile, publicKeySSH []byte, chain *site.KeyHistoryBlock) (SignatureStatus, *site.KeyHistoryEntry, error) {
	if tf == nil || tf.Signature == "" {
		return StatusUnsigned, nil, nil
	}
	if len(publicKeySSH) == 0 {
		return StatusUnknown, nil, fmt.Errorf("no public key to verify against")
	}
	canonical, err := canonicalJSON(tf)
	if err != nil {
		return StatusUnknown, nil, fmt.Errorf("canonical JSON: %w", err)
	}
	ok, retired, err := site.VerifyWithHistory(func(key []byte) (bool, error) {
		return signing.VerifySignature(canonical, key, tf.Signature)
	}, publicKeySSH, chain, tf.Updated, "updated")

	// SIGNET epic 47 — the tolerance rule, applied AFTER the history walk. A
	// file that no key in the chain verifies, carrying members this build does
	// not declare, is still "I could not check this": the reason the bytes
	// never matched may be the field set rather than the key.
	switch out := signing.Resolve(ok, tf.unrecognised); out.Status {
	case signing.StatusUnknown:
		return StatusUnknown, nil, errors.New(out.Explain())
	case signing.StatusInvalid:
		if err == nil {
			err = fmt.Errorf("signature does not verify against the site identity key")
		}
		return StatusInvalid, nil, err
	}
	return StatusValid, retired, nil
}

// VerifySite checks every tag file a site has written, against the identity key
// published in .well-known/polis.
//
// The PUBLISHED key, not .polis/keys/id_ed25519.pub, because that is the key a
// third party on the network would fetch — so this answers the question the
// network asks rather than a locally convenient approximation. Same source as
// following.VerifySite and metadata.VerifyBlessedSite.
//
// A site with no tags yields an empty map and no error. Having tagged nothing
// is an ordinary state.
func VerifySite(siteDir string) (map[string]SignatureStatus, error) {
	tags, err := ListTags(siteDir)
	if err != nil {
		return nil, err
	}
	if len(tags) == 0 {
		return map[string]SignatureStatus{}, nil
	}

	data, err := os.ReadFile(filepath.Join(siteDir, ".well-known", "polis"))
	if err != nil {
		return nil, fmt.Errorf("no .well-known/polis to verify against")
	}
	var wk struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal(data, &wk); err != nil {
		return nil, fmt.Errorf("unparseable .well-known/polis")
	}
	if wk.PublicKey == "" {
		return nil, fmt.Errorf("no public_key in .well-known/polis")
	}

	out := make(map[string]SignatureStatus, len(tags))
	for i := range tags {
		status, _ := Verify(&tags[i], []byte(wk.PublicKey))
		out[tags[i].Tag] = status
	}
	return out, nil
}
