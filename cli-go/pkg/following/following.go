// Package following manages the following.json file for tracking followed authors.
package following

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// normalizeFollowURL ensures consistent URL comparison by lowercasing and trimming trailing slashes.
func normalizeFollowURL(u string) string {
	return strings.TrimRight(strings.ToLower(u), "/")
}

// Version is set at init time by cmd package.
var Version = "dev"

// GetGenerator returns the generator string stamped into following.json's
// `version` field on every write.
//
// ⚠️ Use this, never bare Version — the field records WHO WROTE THE FILE, and a
// bare version number does not say that. Bash writes "polis-cli/$VERSION" for
// the same field, which is how the two implementations stay distinguishable in
// a published artifact.
func GetGenerator() string {
	return "polis-cli-go/" + Version
}

// stampGenerator records the writer into f.Version.
//
// ⚠️ CALL THIS BEFORE Sign(), NEVER AFTER. `version` is the FIRST field in the
// signing base (see canonicalJSON), so stamping after signing produces a file
// whose signature covers a different version string than the one on disk — it
// writes cleanly and then fails to verify forever. This is the same trap as the
// licence block being hand-written twice in the publish path: one value, spliced
// in one place, or the artifact does not verify.
func stampGenerator(f *FollowingFile) {
	f.Version = GetGenerator()
}

// FollowingFile represents the following.json structure.
//
// SIGNET epic 02 — the follow list is signed. `following.json` is AUTHORED by
// the user (their action adds and removes entries) and is the source of truth
// the DS mirrors; it is not a projection of DS state. So the signature is the
// user asserting "these are my follows", and it is the foundation under every
// trust computation that weights by the follow graph.
//
// ⚠️ `Version` is NOT a version. It holds the GENERATOR string
// ("polis-cli-go/0.67.0") — what pub.polis.tag calls `generator`. The name is
// wrong and is deliberately left alone: the field is published and ten packages
// read this file, so renaming it breaks old readers for no functional gain.
// See Signet epic 02, D2b.
//
// It is re-stamped by stampGenerator on every write, BEFORE signing. Until
// 2026-08-28 it was never written at all — the package-level Version had two
// assigners and no reader, so every file's generator string was frozen at
// whichever CLI first created it, and once epic 02 put the field first in the
// signing base the site began SIGNING that stale claim. See E4.
type FollowingFile struct {
	Version   string           `json:"version"`
	Following []FollowingEntry `json:"following"`

	// Signature is an SSH signature (ssh-keygen -Y compatible) over
	// canonicalJSON(f) — the {version, following} pair, with Signature itself
	// excluded. Empty means UNSIGNED, which is a FACT, not a defect: files
	// written before epic 02, or by a path with no key, legitimately have none.
	// Never treat absence as invalid (Signet Law 2).
	Signature string `json:"signature,omitempty"`

	// unrecognised are the JSON members of the parsed document that this build
	// does not declare — captured by Parse, consulted by Verify (SIGNET epic
	// 47, tolerant verifiers).
	//
	// ⚠️ Nil for a file this process built in memory, and that is correct
	// rather than a gap: there were no foreign bytes to meet. It is populated
	// only where raw JSON came in from somewhere else.
	unrecognised []string
}

// UnrecognisedFields returns the JSON members this build does not declare, for
// a file that came from Parse or Load. Nil for anything built in memory.
//
// A caller that reports a `valid` status MUST also report this when it is
// non-empty: the signature verified over the fields this build knows, and these
// are NOT covered by it (SIGNET epic 47, row 2 of the table).
func (f *FollowingFile) UnrecognisedFields() []string {
	if f == nil {
		return nil
	}
	return f.unrecognised
}

// Parse reads a follow file from raw JSON bytes, recording any members this
// build does not declare.
//
// ⭐ EVERY PATH THAT TURNS FOREIGN BYTES INTO A FollowingFile MUST COME THROUGH
// HERE — Load for a file on disk, sitecheck for a file fetched over HTTP. A
// plain json.Unmarshal silently drops the unknown members, and then Verify
// cannot tell "this signature is wrong" from "this file was written by
// something newer than me".
func Parse(raw []byte) (*FollowingFile, error) {
	var f FollowingFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	f.unrecognised = signing.UnrecognisedFields(raw, FollowingFile{})
	return &f, nil
}

// FollowingEntry represents a single followed author.
type FollowingEntry struct {
	URL        string `json:"url"`
	AddedAt    string `json:"added_at"`
	SiteTitle  string `json:"site_title,omitempty"`
	AuthorName string `json:"author_name,omitempty"`
}

// SignatureStatus is the outcome of checking a follow file's signature.
//
// FOUR values, and the distinctions are load-bearing — a consumer that collapses
// them makes a judgment the protocol is not allowed to make for it (Law 2):
//
//   - StatusUnsigned — no signature. A fact. Most sites until the epic-11
//     backfill runs. Reporting this as a failure turns the fleet red.
//   - StatusValid    — signature present and verifies.
//   - StatusInvalid  — signature present and does NOT verify. Report it; the
//     file is still loaded and still used (D4). A wrong signature is evidence,
//     not a verdict — refusing to load would disconnect someone from their
//     whole network as a "security response", and the likeliest cause is a bug.
//   - StatusUnknown  — could not check (no public key available). Not a finding.
type SignatureStatus string

const (
	StatusUnsigned SignatureStatus = "unsigned"
	StatusValid    SignatureStatus = "valid"
	StatusInvalid  SignatureStatus = "invalid"
	StatusUnknown  SignatureStatus = "unknown"
)

// canonicalJSON produces the deterministic byte sequence that the signature
// covers. THIS FUNCTION IS THE SPEC — a second implementation must reproduce
// these exact bytes or its signatures will not verify against ours.
//
// The signable set, in order:
//
//	{"version":<generator string>,"following":[{"url":…,"added_at":…,
//	 "site_title":…,"author_name":…}, …]}
//
// Compact `encoding/json` output (no indentation, no trailing newline), fields
// in Go struct-declaration order, `site_title` / `author_name` omitted when
// empty. `signature` is excluded — it cannot cover itself.
//
// Mechanism copied from pkg/tag's canonicalJSON (build a signing-only struct,
// sign those exact bytes, keep the signature out of it). ⚠️ The mechanism ONLY:
// tag's field list does not transfer. There is no `current_version` here and
// there must not be — it exists in tag to let the DS identify a changed content
// row, and the follow file is not registered with the DS as content, so the
// field would be unconsumed, unsigned and derivable. See D2.
func canonicalJSON(f *FollowingFile) ([]byte, error) {
	signable := struct {
		Version   string           `json:"version"`
		Following []FollowingEntry `json:"following"`
	}{
		Version:   f.Version,
		Following: f.Following,
	}

	// A nil slice marshals as null; an empty one as []. Pin it so "I follow
	// nobody" has one byte sequence rather than two.
	if signable.Following == nil {
		signable.Following = []FollowingEntry{}
	}

	return json.Marshal(signable)
}

// Sign computes the signature over canonicalJSON(f) and sets f.Signature.
func Sign(f *FollowingFile, privateKeyPEM []byte) error {
	canonical, err := canonicalJSON(f)
	if err != nil {
		return fmt.Errorf("canonical JSON: %w", err)
	}
	sig, err := signing.SignContent(canonical, privateKeyPEM)
	if err != nil {
		return fmt.Errorf("sign following.json: %w", err)
	}
	f.Signature = sig
	return nil
}

// Verify checks f.Signature against publicKeySSH. It reports a status and never
// an opinion: the caller decides what the status means (Law 2).
//
// The returned error explains a StatusInvalid / StatusUnknown; it is context for
// a human, not a second channel of truth. Read the status.
func Verify(f *FollowingFile, publicKeySSH []byte) (SignatureStatus, error) {
	if f == nil || f.Signature == "" {
		return StatusUnsigned, nil
	}
	if len(publicKeySSH) == 0 {
		return StatusUnknown, fmt.Errorf("no public key to verify against")
	}

	canonical, err := canonicalJSON(f)
	if err != nil {
		return StatusUnknown, fmt.Errorf("canonical JSON: %w", err)
	}

	ok, err := signing.VerifySignature(canonical, publicKeySSH, f.Signature)

	// SIGNET epic 47 — the tolerance rule. A rebuild that FAILS while the
	// document carries members this build does not declare is "I could not
	// check this", never "this is forged": the signature covers a wider field
	// set than the one reconstructed above, so the bytes were never going to
	// match and nothing here is evidence of tampering.
	//
	// ⚠️ The `valid` row is silent on purpose. Unrecognised members alongside a
	// signature that DOES verify leave the status alone — they are simply not
	// covered by it — and the caller says so from UnrecognisedFields().
	switch out := signing.Resolve(ok && err == nil, f.unrecognised); out.Status {
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

// VerifySite loads a site's follow file and checks its signature against the
// identity key published in .well-known/polis.
//
// It verifies against the PUBLISHED key rather than .polis/keys/id_ed25519.pub
// because that is the key a third party on the network would fetch and use —
// so this answers the same question the network asks, not a locally convenient
// approximation. Same source as judge check 8 (public_key_messages).
//
// A missing follow file, a missing .well-known/polis, or an absent public_key
// all yield a non-finding status, never an error-shaped failure: they are
// ordinary states of a site that has not done a thing yet.
func VerifySite(siteDir string) (SignatureStatus, error) {
	f, err := Load(DefaultPath(siteDir))
	if err != nil {
		return StatusUnknown, fmt.Errorf("load following.json: %w", err)
	}
	// Load() invents an empty file when none exists; an absent follow file is
	// unsigned, which is exactly what it should report.
	if f.Signature == "" {
		return StatusUnsigned, nil
	}

	data, err := os.ReadFile(filepath.Join(siteDir, ".well-known", "polis"))
	if err != nil {
		return StatusUnknown, fmt.Errorf("no .well-known/polis to verify against")
	}
	var wk struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal(data, &wk); err != nil {
		return StatusUnknown, fmt.Errorf("unparseable .well-known/polis")
	}
	if wk.PublicKey == "" {
		return StatusUnknown, fmt.Errorf("no public_key in .well-known/polis")
	}

	return Verify(f, []byte(wk.PublicKey))
}

// DefaultPath returns the default path to following.json.
func DefaultPath(dataDir string) string {
	return filepath.Join(dataDir, "content", "pub.polis.core", "follow", "following.json")
}

// Load loads the following.json file from the given path.
func Load(path string) (*FollowingFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &FollowingFile{
				Following: []FollowingEntry{},
			}, nil
		}
		return nil, fmt.Errorf("failed to read following.json: %w", err)
	}

	f, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse following.json: %w", err)
	}

	return f, nil
}

// SaveSigned signs the follow file with privateKeyPEM and writes it.
//
// This is the write path every user-initiated follow/unfollow takes, which is
// why epic 02 needs no migration: anything the user touches after this ships is
// signed BECAUSE THE USER ACTED. Files nobody touches stay unsigned (fine —
// unsigned is a fact) until the epic-11 backfill.
//
// A nil/empty key falls through to Save, i.e. writes UNSIGNED. That is the safe
// direction: an unsigned file reports OK, whereas keeping a now-stale signature
// over changed content would manufacture a spurious "does not verify" finding.
func SaveSigned(path string, f *FollowingFile, privateKeyPEM []byte) error {
	if len(privateKeyPEM) == 0 {
		return Save(path, f)
	}
	if err := guardRewrite(path); err != nil {
		return err
	}
	// BEFORE Sign, so the signature covers the version string that lands on
	// disk. See stampGenerator.
	stampGenerator(f)
	if err := Sign(f, privateKeyPEM); err != nil {
		return err
	}
	return writeFile(path, f)
}

// Save saves the following.json file to the given path, UNSIGNED.
//
// ⚠️ It clears any existing signature rather than carrying it over. A signature
// covers the bytes it was made for; re-writing possibly-changed content under
// the old one produces a file that loads and does not verify — a finding a human
// has to chase. Degrading to "unsigned" is honest and costs nobody anything;
// degrading to "invalid" is a false alarm. Callers holding a key want
// SaveSigned.
func Save(path string, f *FollowingFile) error {
	if err := guardRewrite(path); err != nil {
		return err
	}
	stampGenerator(f)
	f.Signature = ""
	return writeFile(path, f)
}

// guardRewrite is the refusal both writers share: a follow file carrying
// members this build does not declare is not rewritten, signed or unsigned —
// either would drop them (signing.GuardRewrite). The way past is
// RewriteUnsigned.
func guardRewrite(path string) error {
	return signing.GuardRewrite("following.json", path, FollowingFile{})
}

// RewriteUnsigned is the ESCAPE HATCH for a user stranded by that refusal: it
// rewrites the follow file from the members this build declares, WITHOUT a
// signature, and returns what it dropped. ⛔ It never signs — a signature must
// not cover content the signer's software could not read. A file with nothing
// unrecognised is left untouched.
func RewriteUnsigned(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read following.json: %w", err)
	}
	f, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse following.json: %w", err)
	}
	dropped := f.UnrecognisedFields()
	if len(dropped) == 0 {
		return nil, nil
	}
	stampGenerator(f)
	f.Signature = ""
	if err := writeFile(path, f); err != nil {
		return nil, err
	}
	return dropped, nil
}

// writeFile marshals and atomically writes the follow file as-is, signature
// field included if set. The single write point shared by Save and SaveSigned.
func writeFile(path string, f *FollowingFile) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal following.json: %w", err)
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// R23-6: write atomically (tmp+rename) so a crash mid-write can't leave a
	// truncated following.json. Mode stays 0644 — this is PUBLIC served content
	// (content/pub.polis.core/follow/), so self-hosters serving it via nginx/
	// Apache under a different user must be able to read it (the R11-17 lesson:
	// don't tighten public-facing files to 0600).
	if err := atomicfile.WriteFile(path, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("failed to write following.json: %w", err)
	}

	return nil
}

// Add adds an author to the following list.
// URL comparison ignores trailing slashes.
func (f *FollowingFile) Add(authorURL string) bool {
	norm := normalizeFollowURL(authorURL)
	for _, entry := range f.Following {
		if normalizeFollowURL(entry.URL) == norm {
			return false // Already following
		}
	}

	f.Following = append(f.Following, FollowingEntry{
		URL:     authorURL,
		AddedAt: time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	})

	return true
}

// Remove removes an author from the following list.
// Removes ALL matching entries (handles legacy duplicates).
// URL comparison ignores trailing slashes.
func (f *FollowingFile) Remove(authorURL string) bool {
	norm := normalizeFollowURL(authorURL)
	found := false
	filtered := f.Following[:0]
	for _, entry := range f.Following {
		if normalizeFollowURL(entry.URL) == norm {
			found = true
		} else {
			filtered = append(filtered, entry)
		}
	}
	f.Following = filtered
	return found
}

// IsFollowing checks if an author is in the following list.
// URL comparison ignores trailing slashes.
func (f *FollowingFile) IsFollowing(authorURL string) bool {
	norm := normalizeFollowURL(authorURL)
	for _, entry := range f.Following {
		if normalizeFollowURL(entry.URL) == norm {
			return true
		}
	}
	return false
}

// Get retrieves a following entry by URL.
// URL comparison ignores trailing slashes.
func (f *FollowingFile) Get(authorURL string) *FollowingEntry {
	norm := normalizeFollowURL(authorURL)
	for i := range f.Following {
		if normalizeFollowURL(f.Following[i].URL) == norm {
			return &f.Following[i]
		}
	}
	return nil
}

// UpdateMetadata sets the site title and author name for a matching entry.
// Returns true if the entry was found and updated.
func (f *FollowingFile) UpdateMetadata(url, siteTitle, authorName string) bool {
	entry := f.Get(url)
	if entry == nil {
		return false
	}
	entry.SiteTitle = siteTitle
	entry.AuthorName = authorName
	return true
}

// EntriesMissingMetadata returns entries that have neither site_title nor author_name.
func (f *FollowingFile) EntriesMissingMetadata() []FollowingEntry {
	var missing []FollowingEntry
	for _, e := range f.Following {
		if e.SiteTitle == "" && e.AuthorName == "" {
			missing = append(missing, e)
		}
	}
	return missing
}

// Count returns the number of followed authors.
func (f *FollowingFile) Count() int {
	return len(f.Following)
}

// All returns all following entries.
func (f *FollowingFile) All() []FollowingEntry {
	return f.Following
}
