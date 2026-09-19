// Package metadata provides management for polis public metadata files.
package metadata

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// Version is set at startup by the cmd package.
var Version = "dev"

// GetGenerator returns the generator identifier for metadata files.
func GetGenerator() string {
	return "polis-cli-go/" + Version
}

const (
	// BlessedCommentsFilename is the name of the blessed comments index file.
	BlessedCommentsFilename = "blessed.json"
)

// blessedMu serializes load + modify + save of blessed.json across all
// entry points in this package. Without it, AddBlessedComment,
// RemoveBlessedComment, and SaveBlessedComments callers race on the
// shared on-disk file: each reads the same snapshot, modifies its
// copy, and writes back — last writer wins, dropping the other's
// entries. Same shape as the reply-context cache race.
//
// All public mutators (Add, Remove, Save) take this lock. Internal
// helpers that already hold it use the saveLocked variant to avoid
// re-entrancy.
var blessedMu sync.Mutex

// BlessedComments represents the blessed-comments.json file structure.
// This file is the public index of comments that the site owner has blessed,
// grouped by the post they're replying to.
//
// SIGNET epic 14 — the blessing list is signed. It is AUTHORED by the user (the
// blesser decides; the DS learns afterward), so it is a source, not a projection
// of DS state, and signing it is exactly right under Law 1. See epic 14, D1/D2.
//
// ⚠️ `Version` is NOT a version. It holds the GENERATOR string
// ("polis-cli-go/0.67.0") — the same misnomer as following.json's field, kept
// for the same reason: the field is published and several packages read this
// file, so renaming it breaks old readers for no functional gain.
//
// It is re-stamped by stampBlessedGenerator on every write, BEFORE signing.
// Until this epic it was written only when the file was CREATED, so a
// long-lived file's generator was frozen at whichever CLI first blessed
// anything — the same defect epic 02 found in following.json.
type BlessedComments struct {
	Version  string         `json:"version"`
	Comments []PostComments `json:"comments"`

	// Signature is an SSH signature (ssh-keygen -Y compatible) over
	// canonicalBlessedJSON(bc) — the {version, comments} pair, with Signature
	// itself excluded. Empty means UNSIGNED, which is a FACT, not a defect:
	// files written before epic 14, or by a path with no key, legitimately
	// have none. Never treat absence as invalid (Signet Law 2).
	Signature string `json:"signature,omitempty"`

	// unrecognised are the JSON members of the parsed document that this build
	// does not declare — captured by ParseBlessed, consulted by VerifyBlessed
	// (SIGNET epic 47, tolerant verifiers).
	//
	// ⚠️ Nil for a list this process built in memory, and that is correct
	// rather than a gap: there were no foreign bytes to meet.
	//
	// ⛔ It is NOT written back out. Round-tripping an unknown member would mean
	// this build re-signing a claim it cannot read — saveBlessedLocked rebuilds
	// the file from the declared struct, which is exactly why the epic-11 marker
	// fields had to be DECLARED here rather than tolerated (epic 46 R6).
	unrecognised []string
}

// UnrecognisedFields returns the JSON members this build does not declare, for
// a list that came from ParseBlessed or a load path. Nil for anything built in
// memory.
//
// A caller that reports a `valid` status MUST also report this when it is
// non-empty: the signature verified over the fields this build knows, and these
// are NOT covered by it (SIGNET epic 47, row 2 of the table).
func (bc *BlessedComments) UnrecognisedFields() []string {
	if bc == nil {
		return nil
	}
	return bc.unrecognised
}

// ParseBlessed reads a blessing list from raw JSON bytes, recording any members
// this build does not declare.
//
// ⭐ EVERY PATH THAT TURNS FOREIGN BYTES INTO A BlessedComments MUST COME
// THROUGH HERE — the local load path, and sitecheck for a file fetched over
// HTTP. A plain json.Unmarshal silently drops the unknown members, and then
// VerifyBlessed cannot tell "this signature is wrong" from "this file was
// written by something newer than me".
func ParseBlessed(raw []byte) (*BlessedComments, error) {
	var bc BlessedComments
	if err := json.Unmarshal(raw, &bc); err != nil {
		return nil, err
	}
	bc.unrecognised = signing.UnrecognisedFields(raw, BlessedComments{})
	return &bc, nil
}

// stampBlessedGenerator records the writer into bc.Version.
//
// ⚠️ CALL THIS BEFORE SignBlessed, NEVER AFTER. `version` is the FIRST field in
// the signing base (see canonicalBlessedJSON), so stamping after signing
// produces a file whose signature covers a different version string than the one
// on disk — it writes cleanly and then fails to verify forever.
func stampBlessedGenerator(bc *BlessedComments, generator string) {
	if generator == "" {
		generator = GetGenerator()
	}
	bc.Version = generator
}

// PostComments groups blessed comments for a single post.
type PostComments struct {
	Post    string           `json:"post"`
	Blessed []BlessedComment `json:"blessed"`
}

// BlessedComment represents a single blessed comment entry.
type BlessedComment struct {
	URL       string `json:"url"`
	Version   string `json:"version"`
	BlessedAt string `json:"blessed_at"`

	// Agent and Grant are the MARKER (Signet epic 11): set only on an entry a
	// user agent added under the user's live grant. Agent is the user's name for
	// it ("rosie"); Grant is the grant record's source URL, never a page.
	//
	// ⛔ BOTH ARE `omitempty`, AND THAT IS WHAT KEEPS EVERY EXISTING SIGNATURE
	// VALID: canonicalBlessedJSON marshals this struct, so an unmarked entry
	// produces exactly the bytes it did before these fields existed.
	//
	// ⛔ DECLARED HERE ON PURPOSE. Every Go writer reloads and rewrites the whole
	// list through this struct, so a writer built without these fields drops
	// every marker and then SIGNS the list with the user's key — turning an
	// agent's blessings into the user's own (epic 46 R6).
	// TestMarkedBlessedListRoundTripsThroughTheGoWriter.
	Agent string `json:"agent,omitempty"`
	Grant string `json:"grant,omitempty"`
}

// LoadBlessedComments reads the blessed-comments.json file from the metadata directory.
// Returns an error if the file doesn't exist.
//
// Reads are not synchronized against concurrent writers — the file is
// updated atomically via rename in saveBlessedCommentsLocked, so a
// reader observes either the pre-write or post-write contents but
// never a partial write. For load+modify+save callers, use
// AddBlessedComment / RemoveBlessedComment to get the right
// serialization; direct LoadBlessedComments is for read-only paths.
func LoadBlessedComments(siteDir string) (*BlessedComments, error) {
	return loadBlessedCommentsRaw(siteDir)
}

// loadBlessedCommentsRaw is the unsynchronized read path; reused from
// inside the locked Add/Remove flows.
func loadBlessedCommentsRaw(siteDir string) (*BlessedComments, error) {
	data, err := os.ReadFile(blessedPath(siteDir))
	if err != nil {
		return nil, fmt.Errorf("failed to read blessed-comments.json: %w", err)
	}

	bc, err := ParseBlessed(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse blessed-comments.json: %w", err)
	}

	return bc, nil
}

// blessedPath is where the blessing list lives.
func blessedPath(siteDir string) string {
	return filepath.Join(siteDir, BundleContentDir, "comment", BlessedCommentsFilename)
}

// RewriteBlessedUnsigned is the ESCAPE HATCH for a user stranded by the refusal
// in saveBlessedLocked: it rewrites blessed.json from the members this build
// declares, WITHOUT a signature, and returns what it dropped.
//
// ⛔ It never signs. A signature must not cover content the signer's software
// could not read, and this write exists precisely because it could not. The
// next ordinary bless or unbless signs the list again — by then the list holds
// nothing this build cannot read.
//
// A list with nothing unrecognised is left untouched, and nil is returned.
func RewriteBlessedUnsigned(siteDir string) ([]string, error) {
	blessedMu.Lock()
	defer blessedMu.Unlock()
	bc, err := loadBlessedCommentsRaw(siteDir)
	if err != nil {
		return nil, err
	}
	dropped := bc.UnrecognisedFields()
	if len(dropped) == 0 {
		return nil, nil
	}
	stampBlessedGenerator(bc, "")
	bc.Signature = ""
	if err := writeBlessedFile(siteDir, bc); err != nil {
		return nil, err
	}
	return dropped, nil
}

// SaveBlessedComments writes the blessing list UNSIGNED, atomically.
//
// ⚠️ It CLEARS any existing signature rather than carrying it over. A signature
// covers the bytes it was made for; re-writing possibly-changed content under the
// old one produces a file that loads and does not verify — a finding a human has
// to chase. Degrading to "unsigned" is honest and costs nobody anything;
// degrading to "invalid" is a false alarm. Callers holding a key want
// SaveBlessedCommentsSigned.
//
// Takes blessedMu so callers doing their own load+modify+save sequence are
// serialized against AddBlessedComment / RemoveBlessedComment.
func SaveBlessedComments(siteDir string, bc *BlessedComments) error {
	blessedMu.Lock()
	defer blessedMu.Unlock()
	return saveBlessedCommentsLocked(siteDir, bc)
}

// SaveBlessedCommentsSigned signs the blessing list with privateKeyPEM and
// writes it. An empty key falls through to the unsigned path — the safe
// direction (see SaveBlessedComments).
func SaveBlessedCommentsSigned(siteDir string, bc *BlessedComments, privateKeyPEM []byte) error {
	blessedMu.Lock()
	defer blessedMu.Unlock()
	return saveBlessedLocked(siteDir, bc, "", privateKeyPEM)
}

// saveBlessedCommentsLocked is the unsigned write path. Caller must hold
// blessedMu.
func saveBlessedCommentsLocked(siteDir string, bc *BlessedComments) error {
	return saveBlessedLocked(siteDir, bc, "", nil)
}

// saveBlessedLocked is THE single write point for blessed.json. Every path that
// puts bytes on disk goes through here, which is what makes "every write signs,
// and an unsigned write clears a stale signature" a property of the file rather
// than a habit of its callers. Caller must hold blessedMu.
//
// A nil/empty key writes UNSIGNED and clears the signature field. That is
// deliberate and is the safe direction — see SaveBlessedComments.
//
// ⛔ IT REFUSES to rewrite a file carrying members this build does not declare
// (signing.GuardRewrite), signed or not: either write would drop them. The one
// way past is RewriteBlessedUnsigned, which the user asks for by name.
func saveBlessedLocked(siteDir string, bc *BlessedComments, generator string, privateKeyPEM []byte) error {
	if err := signing.GuardRewrite(BlessedCommentsFilename, blessedPath(siteDir), BlessedComments{}); err != nil {
		return err
	}
	// BEFORE signing, so the signature covers the version string that lands on
	// disk. See stampBlessedGenerator.
	stampBlessedGenerator(bc, generator)

	if len(privateKeyPEM) == 0 {
		bc.Signature = ""
		return writeBlessedFile(siteDir, bc)
	}
	if err := SignBlessed(bc, privateKeyPEM); err != nil {
		return err
	}
	return writeBlessedFile(siteDir, bc)
}

// writeBlessedFile marshals and atomically writes the blessing list as-is,
// signature field included if set.
func writeBlessedFile(siteDir string, bc *BlessedComments) error {
	metadataDir := filepath.Join(siteDir, BundleContentDir, "comment")
	if err := os.MkdirAll(metadataDir, 0755); err != nil {
		return fmt.Errorf("failed to create metadata directory: %w", err)
	}

	data, err := json.MarshalIndent(bc, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal blessed comments: %w", err)
	}

	// Write atomically via temp file. The shared `.tmp` filename is
	// safe under blessedMu — only one goroutine holds it at a time.
	filePath := filepath.Join(metadataDir, BlessedCommentsFilename)
	tmpPath := filePath + ".tmp"

	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tmpPath, filePath); err != nil {
		os.Remove(tmpPath) // Clean up temp file on failure
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// AddBlessedComment adds a comment to the blessing list, writing it UNSIGNED.
// Creates the post entry if it doesn't exist.
//
// Holds blessedMu for the full load+modify+save cycle to prevent
// concurrent callers from losing each other's entries.
//
// ⚠️ Callers holding the site's private key want AddBlessedCommentSigned. This
// one clears any existing signature — see SaveBlessedComments for why that is
// the safe direction rather than an omission.
func AddBlessedComment(siteDir string, postPath string, comment BlessedComment, generator ...string) error {
	gen := ""
	if len(generator) > 0 {
		gen = generator[0]
	}
	return addBlessedComment(siteDir, postPath, comment, gen, nil)
}

// AddBlessedCommentSigned adds a comment to the blessing list and signs the
// result with the site's identity key. This is the path a user-initiated
// blessing takes, which is why epic 14 needs no migration: anything the author
// touches after this ships is signed BECAUSE THE AUTHOR ACTED.
func AddBlessedCommentSigned(siteDir string, postPath string, comment BlessedComment, privateKeyPEM []byte) error {
	return addBlessedComment(siteDir, postPath, comment, "", privateKeyPEM)
}

func addBlessedComment(siteDir string, postPath string, comment BlessedComment, generator string, privateKeyPEM []byte) error {
	blessedMu.Lock()
	defer blessedMu.Unlock()

	gen := GetGenerator()
	if generator != "" {
		gen = generator
	}
	// Load current state. loadBlessedCommentsRaw bypasses the mutex
	// since we already hold it.
	bc, err := loadBlessedCommentsRaw(siteDir)
	if err != nil {
		// If file doesn't exist, create new structure
		if errors.Is(err, os.ErrNotExist) {
			bc = &BlessedComments{
				Version:  gen,
				Comments: []PostComments{},
			}
		} else {
			return err
		}
	}

	// Set blessed_at if not provided
	if comment.BlessedAt == "" {
		comment.BlessedAt = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	}

	// Find or create post entry
	found := false
	for i, pc := range bc.Comments {
		if pc.Post == postPath {
			// Check if comment already exists (by URL or version)
			for _, existing := range pc.Blessed {
				if existing.URL == comment.URL || (comment.Version != "" && existing.Version == comment.Version) {
					// Already blessed, nothing to do
					return nil
				}
			}
			// Add to existing post entry
			bc.Comments[i].Blessed = append(bc.Comments[i].Blessed, comment)
			found = true
			break
		}
	}

	if !found {
		// Create new post entry
		bc.Comments = append(bc.Comments, PostComments{
			Post:    postPath,
			Blessed: []BlessedComment{comment},
		})
	}

	return saveBlessedLocked(siteDir, bc, gen, privateKeyPEM)
}

// RemoveBlessedComment removes a comment from the blessing list, writing the
// result UNSIGNED. Matches by URL.
//
// Holds blessedMu for the full load+modify+save cycle.
//
// ⚠️ Callers holding the site's private key want RemoveBlessedCommentSigned.
func RemoveBlessedComment(siteDir string, commentURL string) error {
	return removeBlessedComment(siteDir, commentURL, nil)
}

// RemoveBlessedCommentSigned removes a comment from the blessing list and signs
// the result. An unbless is as much an authored act as a bless.
func RemoveBlessedCommentSigned(siteDir string, commentURL string, privateKeyPEM []byte) error {
	return removeBlessedComment(siteDir, commentURL, privateKeyPEM)
}

func removeBlessedComment(siteDir string, commentURL string, privateKeyPEM []byte) error {
	blessedMu.Lock()
	defer blessedMu.Unlock()

	bc, err := loadBlessedCommentsRaw(siteDir)
	if err != nil {
		return err
	}

	// Find and remove the comment
	for i, pc := range bc.Comments {
		for j, c := range pc.Blessed {
			if c.URL == commentURL {
				// Remove this comment
				bc.Comments[i].Blessed = append(pc.Blessed[:j], pc.Blessed[j+1:]...)

				// If post has no more blessed comments, remove the post entry
				if len(bc.Comments[i].Blessed) == 0 {
					bc.Comments = append(bc.Comments[:i], bc.Comments[i+1:]...)
				}

				return saveBlessedLocked(siteDir, bc, "", privateKeyPEM)
			}
		}
	}

	// Comment not found, nothing to do
	return nil
}

// GetBlessedCommentsForPost returns all blessed comments for a specific post.
// Uses flexible path matching: tries exact match, .md/.html swap, and URL-to-path extraction.
func GetBlessedCommentsForPost(siteDir string, postPath string) ([]BlessedComment, error) {
	bc, err := LoadBlessedComments(siteDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []BlessedComment{}, nil
		}
		return nil, err
	}

	for _, pc := range bc.Comments {
		if MatchesPostPath(pc.Post, postPath) {
			return pc.Blessed, nil
		}
	}

	return []BlessedComment{}, nil
}

// MatchesPostPath checks if two post paths refer to the same post.
// Handles exact match, .md/.html extension swaps, full URL vs relative path,
// and source content path (singular /post/) vs mount path (plural /posts/).
func MatchesPostPath(stored, query string) bool {
	if stored == query {
		return true
	}

	// Try .md <-> .html swap
	storedBase := strings.TrimSuffix(strings.TrimSuffix(stored, ".md"), ".html")
	queryBase := strings.TrimSuffix(strings.TrimSuffix(query, ".md"), ".html")
	if storedBase == queryBase {
		return true
	}

	// Try extracting relative path from full URL or source content path
	// e.g., "https://alice.polis.pub/posts/20260101/hello.md" matches "posts/20260101/hello.md"
	// e.g., "content/pub.polis.core/post/20260101/hello.md" matches "posts/20260101/hello.md"
	extractPath := func(s string) string {
		if idx := strings.Index(s, "/posts/"); idx >= 0 {
			return s[idx+1:] // "posts/20260101/hello.md"
		}
		// Also match source content path: .../post/YYYYMMDD/slug.md → posts/YYYYMMDD/slug.md
		if idx := strings.Index(s, "/post/"); idx >= 0 {
			return "posts/" + s[idx+len("/post/"):]
		}
		return s
	}
	storedRel := extractPath(stored)
	queryRel := extractPath(query)
	if storedRel == queryRel {
		return true
	}

	// Compare without extensions after extraction
	storedRelBase := strings.TrimSuffix(strings.TrimSuffix(storedRel, ".md"), ".html")
	queryRelBase := strings.TrimSuffix(strings.TrimSuffix(queryRel, ".md"), ".html")
	return storedRelBase == queryRelBase
}
