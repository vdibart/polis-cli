// Package cache holds isolated, non-canonical local mirrors of foreign content.
//
// Phase 1 implements only the "blessed" kind: a verified local copy of a
// blessed comment authored by ANOTHER tenant, kept so the post author can
// display it without re-fetching on every render and WITHOUT polluting their
// canonical content tree. See plans/comment-registration-severe-bug.md
// (Defect 3): the old code wrote the foreign comment byte-for-byte into the
// blesser's content/pub.polis.core/comment/ tree, where it masqueraded as the
// blesser's own content and was served from the wrong domain.
//
// Layout (Decision 5 — DS-scoped, signals "disposable/regenerable/not-mine"):
//
//	<dataDir>/.polis/ds/<dsDomain>/pub.polis.core/cache/blessed/<authorDomain>/<date>/<id>.md
//	<dataDir>/.polis/ds/<dsDomain>/pub.polis.core/cache/blessed/<authorDomain>/<date>/<id>.md.meta.json
//
// The mirror is NEVER added to index.jsonl and NEVER registered with the DS.
// The generic, kind-agnostic cache contract (descriptor/registry) that lets
// Rosie manage many cache kinds uniformly is deferred — see
// plans/rosie-cache-custodian-design.md. This package is the concrete blessed
// cache it will later generalize (the sidecar here is a forward-compatible
// subset of that schema, so generalizing is additive).
package cache

import (
	"encoding/json"
	"fmt"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"

	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// BlessedSidecar is the provenance metadata written alongside each cached
// blessed comment. fetched_at/verified_at are recorded EXPLICITLY (never
// inferred from mtime, which is fragile across tar restore/export, rsync, and
// backup). Fields are a forward-compatible subset of the future generic cache
// schema (plans/rosie-cache-custodian-design.md).
type BlessedSidecar struct {
	Kind               string `json:"kind"`                 // always "blessed"
	SourceURL          string `json:"source_url"`           // the comment's canonical URL (the dereferenceable artifact)
	AuthorDomain       string `json:"author_domain"`        // the comment author (provenance)
	BlessedVersionHash string `json:"blessed_version_hash"` // the pin: edits don't appear until re-blessed
	Signature          string `json:"signature,omitempty"`  // the comment's embedded signature, for audit
	FetchedAt          string `json:"fetched_at"`           // when the body was fetched
	VerifiedAt         string `json:"verified_at"`          // when sig+hash last verified
	EventID            string `json:"event_id,omitempty"`   // triggering DS event id, if any
}

// blessedDir is the cache root for the blessed kind under a DS scope.
func blessedDir(dataDir, dsDomain string) string {
	return filepath.Join(dataDir, ".polis", "ds", dsDomain, "pub.polis.core", "cache", "blessed")
}

// AuthorDomainFromURL returns the host of a comment URL (the author's domain),
// or "" if the URL has no host.
func AuthorDomainFromURL(commentURL string) string {
	parsed, err := neturl.Parse(commentURL)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return parsed.Host
}

// BlessedPaths derives the cache .md and .meta.json sidecar paths for a blessed
// comment from its URL (canonical or mount form — tolerant during the Defect-1
// transition). Returns ok=false if the URL isn't a recognizable comment URL or
// lacks a host.
func BlessedPaths(dataDir, dsDomain, commentURL string) (mdPath, metaPath string, ok bool) {
	rel := polisurl.CommentURLToContentRel(commentURL) // content/pub.polis.core/comment/<date>/<id>.md
	if rel == "" {
		return "", "", false
	}
	author := AuthorDomainFromURL(commentURL)
	if author == "" {
		return "", "", false
	}
	suffix := strings.TrimPrefix(rel, "content/pub.polis.core/comment/") // <date>/<id>.md
	mdPath = filepath.Join(blessedDir(dataDir, dsDomain), author, filepath.FromSlash(suffix))
	metaPath = mdPath + ".meta.json"
	return mdPath, metaPath, true
}

// StoreBlessed writes a verified blessed-comment body + its provenance sidecar
// to the isolated cache. The caller is responsible for having verified the
// body's signature against the author's key and that its hash matches the
// blessed version BEFORE calling — the sidecar records the result. The cache
// path is derived from sc.SourceURL.
func StoreBlessed(dataDir, dsDomain string, sc BlessedSidecar, body []byte) error {
	mdPath, metaPath, ok := BlessedPaths(dataDir, dsDomain, sc.SourceURL)
	if !ok {
		return fmt.Errorf("cache: unrecognized comment URL %q", sc.SourceURL)
	}
	if err := os.MkdirAll(filepath.Dir(mdPath), 0o755); err != nil {
		return fmt.Errorf("cache: mkdir: %w", err)
	}
	if err := os.WriteFile(mdPath, body, 0o644); err != nil {
		return fmt.Errorf("cache: write body: %w", err)
	}
	sc.Kind = "blessed"
	if sc.AuthorDomain == "" {
		sc.AuthorDomain = AuthorDomainFromURL(sc.SourceURL)
	}
	meta, err := json.MarshalIndent(&sc, "", "  ")
	if err != nil {
		return fmt.Errorf("cache: marshal sidecar: %w", err)
	}
	if err := os.WriteFile(metaPath, meta, 0o644); err != nil {
		return fmt.Errorf("cache: write sidecar: %w", err)
	}
	return nil
}

// ReadBlessed returns the cached body + sidecar for a blessed comment, or
// ok=false if not cached. A missing/corrupt sidecar still returns the body with
// a zero-value sidecar (best-effort), so display degrades gracefully.
func ReadBlessed(dataDir, dsDomain, commentURL string) (body []byte, sc BlessedSidecar, ok bool) {
	mdPath, metaPath, derived := BlessedPaths(dataDir, dsDomain, commentURL)
	if !derived {
		return nil, BlessedSidecar{}, false
	}
	body, err := os.ReadFile(mdPath)
	if err != nil {
		return nil, BlessedSidecar{}, false
	}
	if data, err := os.ReadFile(metaPath); err == nil {
		_ = json.Unmarshal(data, &sc)
	}
	return body, sc, true
}

// EvictBlessed removes a cached blessed comment + its sidecar. Idempotent: a
// missing entry is not an error. Used to honor a signed withdrawal/unpublish or
// a post-author un-bless.
func EvictBlessed(dataDir, dsDomain, commentURL string) error {
	mdPath, metaPath, ok := BlessedPaths(dataDir, dsDomain, commentURL)
	if !ok {
		return nil
	}
	_ = os.Remove(metaPath)
	if err := os.Remove(mdPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cache: evict: %w", err)
	}
	return nil
}

// blessedGlob returns a glob matching a comment's cache .md across ALL DS scopes
// (there is effectively one DS today). Lets read/evict callers that don't have
// the DS domain handy — e.g. the render path — avoid threading it everywhere.
func blessedGlob(dataDir, commentURL string) (string, bool) {
	rel := polisurl.CommentURLToContentRel(commentURL)
	author := AuthorDomainFromURL(commentURL)
	if rel == "" || author == "" {
		return "", false
	}
	suffix := strings.TrimPrefix(rel, "content/pub.polis.core/comment/")
	return filepath.Join(dataDir, ".polis", "ds", "*", "pub.polis.core", "cache", "blessed", author, filepath.FromSlash(suffix)), true
}

// FindBlessed locates a cached blessed comment across DS scopes without needing
// the DS domain (convenient for render-time reads). Returns body + sidecar or
// ok=false.
func FindBlessed(dataDir, commentURL string) (body []byte, sc BlessedSidecar, ok bool) {
	glob, derived := blessedGlob(dataDir, commentURL)
	if !derived {
		return nil, BlessedSidecar{}, false
	}
	matches, _ := filepath.Glob(glob)
	for _, mdPath := range matches {
		data, err := os.ReadFile(mdPath)
		if err != nil {
			continue
		}
		if m, err := os.ReadFile(mdPath + ".meta.json"); err == nil {
			_ = json.Unmarshal(m, &sc)
		}
		return data, sc, true
	}
	return nil, BlessedSidecar{}, false
}

// EvictBlessedAnyDS evicts a cached blessed comment across all DS scopes.
func EvictBlessedAnyDS(dataDir, commentURL string) error {
	glob, ok := blessedGlob(dataDir, commentURL)
	if !ok {
		return nil
	}
	matches, _ := filepath.Glob(glob)
	for _, mdPath := range matches {
		_ = os.Remove(mdPath + ".meta.json")
		if err := os.Remove(mdPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("cache: evict: %w", err)
		}
	}
	return nil
}
