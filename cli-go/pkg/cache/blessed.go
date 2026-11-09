// Package cache holds isolated, non-canonical local mirrors of foreign content.
//
// Phase 1 implements only the "blessed" kind: a verified local copy of a
// blessed comment authored by ANOTHER tenant, kept so the post author can
// display it without re-fetching on every render and WITHOUT polluting their
// canonical content tree. The old code wrote the foreign comment byte-for-byte into the
// blesser's content/pub.polis.core/comment/ tree, where it masqueraded as the
// blesser's own content and was served from the wrong domain.
//
// Layout (Decision 5 — DS-scoped, signals "disposable/regenerable/not-mine"):
//
//	<dataDir>/.polis/ds/<dsDomain>/pub.polis.core/cache/blessed/<authorDomain>/<date>/<id>.md
//	<dataDir>/.polis/ds/<dsDomain>/pub.polis.core/cache/blessed/<authorDomain>/<date>/<id>.md.meta.json
//
// The mirror is NEVER added to index.jsonl and NEVER registered with the DS.
// The kind-agnostic contract (descriptor/registry, contract.go) generalizes
// this concrete cache; the blessed kind is registered through it
// (blessed_descriptor.go), which lifts BlessedSidecar into its Sidecar.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vdibart/polis-cli/cli-go/pkg/sitecheck"
	"io/fs"
	neturl "net/url"
	"strings"

	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// BlessedSidecar is the provenance metadata written alongside each cached
// blessed comment. fetched_at/verified_at are recorded EXPLICITLY (never
// inferred from mtime, which is fragile across tar restore/export, rsync, and
// backup). blessed_descriptor.go lifts it into the generic Sidecar (contract.go).
type BlessedSidecar struct {
	Kind               string `json:"kind"`                 // always "blessed"
	SourceURL          string `json:"source_url"`           // the comment's canonical URL (the dereferenceable artifact)
	AuthorDomain       string `json:"author_domain"`        // the comment author (provenance)
	BlessedVersionHash string `json:"blessed_version_hash"` // the pin: edits don't appear until re-blessed
	Signature          string `json:"signature,omitempty"`  // the comment's embedded signature, for audit
	FetchedAt          string `json:"fetched_at"`           // when the body was fetched
	VerifiedAt         string `json:"verified_at"`          // when sig+hash last verified
	EventID            string `json:"event_id,omitempty"`   // triggering DS event id, if any

	// KeyUsed — which of the author's keys verified the comment when it was
	// cached (see Sidecar.KeyUsed). Omitted when unknown, so sidecars written
	// before it existed load unchanged.
	KeyUsed *sitecheck.KeyUsed `json:"key_used,omitempty"`
}

// blessedKey is the cache root for the blessed kind under a DS scope, relative
// to the site root.
//
// ⭐ A KEY, so every reader and writer of this cache addresses it the same way
// on any backend.
func blessedKey(dsDomain string) string {
	return ".polis/ds/" + dsDomain + "/pub.polis.core/cache/blessed"
}

// safeSegment reports whether s can be ONE path segment: a host or a DS domain
// taken from a URL. ⛔ A host of ".." would otherwise walk out of the cache.
func safeSegment(s string) bool {
	return s != "" && !strings.HasPrefix(s, ".") && !strings.ContainsAny(s, "/\\\x00")
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

// blessedKeys derives the cache .md and .meta.json sidecar KEYS for a blessed
// comment from its URL (canonical or mount form — tolerant during the Defect-1
// transition). Returns ok=false if the URL isn't a recognizable comment URL or
// lacks a host.
//
// ⭐ KEYS, NOT PATHS, and no site is needed to compute them: the layout is a
// fact about the cache, not about where the site happens to be stored. This
// replaced an exported path builder whose callers read and wrote the bytes by
// path, which is the pair that cannot cross a backend boundary.
func blessedKeys(dsDomain, commentURL string) (mdKey, metaKey string, ok bool) {
	suffix, author, ok := blessedSuffix(commentURL)
	if !ok || !safeSegment(dsDomain) {
		return "", "", false
	}
	mdKey = blessedKey(dsDomain) + "/" + author + "/" + suffix
	// ⛔ Proven still inside this DS's cache before anything uses it.
	if !storage.Within(mdKey, blessedKey(dsDomain)) {
		return "", "", false
	}
	return mdKey, mdKey + ".meta.json", true
}

// blessedSuffix is the part of a cache key below the DS scope: the author
// domain and the comment's <date>/<id>.md.
func blessedSuffix(commentURL string) (suffix, author string, ok bool) {
	rel := polisurl.CommentURLToContentRel(commentURL) // content/pub.polis.core/comment/<date>/<id>.md
	if rel == "" {
		return "", "", false
	}
	author = AuthorDomainFromURL(commentURL)
	if !safeSegment(author) {
		return "", "", false
	}
	return strings.TrimPrefix(rel, "content/pub.polis.core/comment/"), author, true
}

// StoreBlessed writes a verified blessed-comment body + its provenance sidecar
// to the isolated cache. The caller is responsible for having verified the
// body's signature against the author's key and that its hash matches the
// blessed version BEFORE calling — the sidecar records the result. The cache
// key is derived from sc.SourceURL.
func StoreBlessed(site storage.Site, dsDomain string, sc BlessedSidecar, body []byte) error {
	mdKey, metaKey, ok := blessedKeys(dsDomain, sc.SourceURL)
	if !ok {
		return fmt.Errorf("cache: unrecognized comment URL %q", sc.SourceURL)
	}
	// ⛔ A SITE STORE, which TIGHTENS the parents from 0755 to 0700 — a declared
	// change, not an accident. The blessed cache lives under .polis
	// (.polis/ds/<domain>/pub.polis.core/cache/blessed), where the invariant is
	// 0700 and every other writer already obeys it; this path was creating
	// world-readable directories inside a tree whose gate is closed. The FILE
	// modes are unchanged at 0644, because the cache holds blessed comments,
	// which are published content rather than secrets.
	st := site.Store()
	if err := st.Put(context.Background(), mdKey, body, storage.WithMode(0o644)); err != nil {
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
	if err := st.Put(context.Background(), metaKey, meta, storage.WithMode(0o644)); err != nil {
		return fmt.Errorf("cache: write sidecar: %w", err)
	}
	return nil
}

// ReadBlessed returns the cached body + sidecar for a blessed comment, or
// ok=false if not cached. A missing/corrupt sidecar still returns the body with
// a zero-value sidecar (best-effort), so display degrades gracefully.
//
// ⚠️ ANY read error is "not cached", as it was when this read by path: the
// caller re-fetches, which is the safe direction.
func ReadBlessed(site storage.Site, dsDomain, commentURL string) (body []byte, sc BlessedSidecar, ok bool) {
	mdKey, metaKey, derived := blessedKeys(dsDomain, commentURL)
	if !derived {
		return nil, BlessedSidecar{}, false
	}
	return readBlessedKey(site, mdKey, metaKey)
}

func readBlessedKey(site storage.Site, mdKey, metaKey string) (body []byte, sc BlessedSidecar, ok bool) {
	ctx := context.Background()
	body, err := site.Store().Get(ctx, mdKey)
	if err != nil {
		return nil, BlessedSidecar{}, false
	}
	if data, err := site.Store().Get(ctx, metaKey); err == nil {
		_ = json.Unmarshal(data, &sc)
	}
	return body, sc, true
}

// EvictBlessed removes a cached blessed comment + its sidecar. Idempotent: a
// missing entry is not an error. Used to honor a signed withdrawal/unpublish or
// a post-author un-bless.
func EvictBlessed(site storage.Site, dsDomain, commentURL string) error {
	mdKey, metaKey, ok := blessedKeys(dsDomain, commentURL)
	if !ok {
		return nil
	}
	return evictBlessedKey(site, mdKey, metaKey)
}

func evictBlessedKey(site storage.Site, mdKey, metaKey string) error {
	ctx := context.Background()
	_ = site.Store().Delete(ctx, metaKey, storage.IgnoreMissing())
	if err := site.Store().Delete(ctx, mdKey); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("cache: evict: %w", err)
	}
	return nil
}

// blessedKeysAnyDS returns a comment's cache .md key under EVERY DS scope that
// exists on the site (there is effectively one DS today). Lets read/evict
// callers that don't have the DS domain handy — e.g. the render path — avoid
// threading it everywhere.
//
// ⭐ This was a filesystem glob over ".polis/ds/*/…". A glob is not something a
// store can answer, but the only wildcard was the DS scope, and that is one
// shallow List of .polis/ds. A missing .polis/ds is an empty result, which is
// what the glob returned too.
func blessedKeysAnyDS(site storage.Site, commentURL string) []string {
	suffix, author, ok := blessedSuffix(commentURL)
	if !ok {
		return nil
	}
	entries, _ := site.Store().List(context.Background(), ".polis/ds")
	var keys []string
	for _, e := range entries {
		if !e.IsDir {
			continue
		}
		base := e.Key + "/pub.polis.core/cache/blessed"
		k := base + "/" + author + "/" + suffix
		// ⛔ Only keys proven inside a cache: a forged event can then delete
		// nothing but a cache entry.
		if storage.Within(k, base) {
			keys = append(keys, k)
		}
	}
	return keys
}

// FindBlessed locates a cached blessed comment across DS scopes without needing
// the DS domain (convenient for render-time reads). Returns body + sidecar or
// ok=false.
func FindBlessed(site storage.Site, commentURL string) (body []byte, sc BlessedSidecar, ok bool) {
	for _, mdKey := range blessedKeysAnyDS(site, commentURL) {
		if body, sc, ok := readBlessedKey(site, mdKey, mdKey+".meta.json"); ok {
			return body, sc, true
		}
	}
	return nil, BlessedSidecar{}, false
}

// EvictBlessedAnyDS evicts a cached blessed comment across all DS scopes.
func EvictBlessedAnyDS(site storage.Site, commentURL string) error {
	for _, mdKey := range blessedKeysAnyDS(site, commentURL) {
		if err := evictBlessedKey(site, mdKey, mdKey+".meta.json"); err != nil {
			return err
		}
	}
	return nil
}
