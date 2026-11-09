package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
	"github.com/vdibart/polis-cli/cli-go/pkg/verify"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// KindBlessed is the cache kind for blessed comments authored by another tenant
// — the first (and, today, only) registered descriptor.
const KindBlessed = "blessed"

// blessedDescriptor adapts the concrete blessed-comment cache (blessed.go) to
// the kind-agnostic Descriptor contract. It is the ONLY per-kind code; every
// generic op in contract.go runs over it unchanged.
//
// Fetch carries the exact verify-and-gate semantics the real-time sync path
// used to inline (WS3·b2): refuse a present-but-INVALID signature
// (tamper/MITM), cache missing/error with VerifiedAt unset (availability).
// Re-pointing the real-time path at this descriptor is the anti-drift wiring.
type blessedDescriptor struct {
	verifyContent func(url string) (*verify.VerificationResult, error)
	now           func() time.Time
}

// NewBlessedDescriptor is the registry factory for the blessed kind.
func NewBlessedDescriptor(cfg DescriptorConfig) Descriptor {
	vc := cfg.VerifyContent
	if vc == nil {
		vc = verify.VerifyContent
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &blessedDescriptor{verifyContent: vc, now: now}
}

func (b *blessedDescriptor) Kind() string { return KindBlessed }

// EvictionSignals are the DS events whose arrival evicts a blessed entry — a
// signed withdrawal (comment.unpublished) or a post-author deny (blessing.denied).
func (b *blessedDescriptor) EvictionSignals() []string {
	return []string{"pub.polis.comment.unpublished", "pub.polis.comment.blessing.denied"}
}

// TTL is zero: blessed entries are pinned to the blessed version and never
// expire by time — they leave only on a signed revocation.
func (b *blessedDescriptor) TTL() time.Duration { return 0 }

func (b *blessedDescriptor) Keys(st Store, key string) (string, string, bool) {
	return blessedKeys(st.DSDomain, key)
}

func (b *blessedDescriptor) Read(st Store, key string) ([]byte, Sidecar, bool) {
	body, bsc, ok := ReadBlessed(st.Site, st.DSDomain, key)
	if !ok {
		return nil, Sidecar{}, false
	}
	return body, blessedToSidecar(bsc), true
}

func (b *blessedDescriptor) Store(st Store, sc Sidecar, body []byte) error {
	return StoreBlessed(st.Site, st.DSDomain, sidecarToBlessed(sc), body)
}

// Evict removes the entry across ALL DS scopes (EvictBlessedAnyDS), preserving
// the WS3·d real-time eviction behavior where the caller may not know the DS
// domain. Idempotent.
func (b *blessedDescriptor) Evict(st Store, key string) error {
	return EvictBlessedAnyDS(st.Site, key)
}

// Fetch fetches + verifies the author's canonical comment and prepares a
// generic sidecar. It is the single source of the verify-and-gate policy that
// both the real-time grant path and the cache upkeep's Reconcile now share.
func (b *blessedDescriptor) Fetch(st Store, key string, opts IngestOptions) FetchOutcome {
	if _, _, ok := blessedKeys(st.DSDomain, key); !ok {
		return FetchOutcome{Verdict: "unsupported_key"}
	}
	mdURL := polisurl.NormalizeToMD(key)
	source := "content"
	result, verr := b.verifyContent(commentSourceURL(mdURL))
	if verr != nil {
		source = "mount"
		result, verr = b.verifyContent(mdURL)
	}
	if verr != nil {
		return FetchOutcome{Verdict: "fetch_failed", Source: source, Err: verr}
	}

	now := b.now().UTC().Format(time.RFC3339)
	verdict := result.Signature.Status // valid | invalid | missing | error

	// WS3·b2 gate: REFUSE to cache a present-but-INVALID signature. An
	// actively-wrong signature is tampering or a key-swap (MITM), not a
	// transient hiccup — never store or display it. (security-relevant, HIGH.)
	if verdict == "invalid" {
		return FetchOutcome{Verdict: "invalid", Refused: true, Source: source}
	}

	verifiedAt := ""
	if verdict == "valid" {
		verifiedAt = now
	}
	sc := b.makeSidecar(key, result.CurrentVersion, now, verifiedAt)
	if verdict == "valid" {
		sc.KeyUsed = result.Signature.Key
	}
	return FetchOutcome{Body: []byte(result.Body), Sidecar: sc, Cacheable: true, Verdict: verdict, Source: source}
}

// makeSidecar builds the generalized sidecar for a blessed entry. pin is the
// blessed version hash; verifiedAt is set only when the signature checked out.
func (b *blessedDescriptor) makeSidecar(key, pin, fetchedAt, verifiedAt string) Sidecar {
	author := AuthorDomainFromURL(key)
	return Sidecar{
		Kind:        KindBlessed,
		Key:         blessedNormKey(key),
		SourceURL:   key,
		FetchedAt:   fetchedAt,
		VerifiedAt:  verifiedAt,
		Integrity:   Integrity{Mode: IntegritySignature, SignerDomain: author},
		Pin:         pin,
		Provenance:  Provenance{AuthorDomain: author, CanonicalURL: key},
		EvictOn:     b.EvictionSignals(),
		DesiredFrom: "blessed.json",
	}
}

// VerifyCached re-verifies a cached entry against the author's CURRENT key by
// re-dereferencing the canonical artifact (which re-fetches the live
// well-known key). Key-rotation handling (basic, in scope): a clean rotation
// re-signs the artifact → "valid" → NOT an integrity failure. Only a signature
// that fails the current key → "invalid" → Tampered (possible MITM). A transient
// fetch/key error → "error" → not a failure (durability). A signature made by a
// retired key resolves through the author's PUBLISHED key history (the verify
// package reads public_key_history). What is not built is continuity of our own:
// remembering the keys we have observed for an author, so a history the site
// rewrites could be told from a clean rotation.
func (b *blessedDescriptor) VerifyCached(st Store, key string, body []byte, sc Sidecar) IntegrityVerdict {
	mdURL := polisurl.NormalizeToMD(sourceURLOf(key, sc))
	result, verr := b.verifyContent(commentSourceURL(mdURL))
	if verr != nil {
		result, verr = b.verifyContent(mdURL)
	}
	if verr != nil {
		// Unreachable / transient — never a tamper signal (durability rule).
		return IntegrityVerdict{Status: "error", Detail: verr.Error()}
	}
	switch result.Signature.Status {
	case "valid":
		return IntegrityVerdict{Status: "valid"}
	case "invalid":
		return IntegrityVerdict{Status: "invalid", Detail: result.Signature.Message}
	default:
		return IntegrityVerdict{Status: result.Signature.Status, Detail: result.Signature.Message}
	}
}

// List walks the blessed cache under the store and returns every cached entry,
// including sidecar-less orphan bodies (HasSidecar=false) so GC can reclaim
// them. Keys are normalized to match DesiredSet.
func (b *blessedDescriptor) List(st Store) ([]CachedItem, error) {
	root := blessedKey(st.DSDomain)
	ctx := context.Background()
	// ⚠️ A recursive List returns what it could read plus the first error.
	// The error is ignored ON PURPOSE: the walk this replaced skipped every
	// unreadable entry and a missing root alike and never failed, and GC and
	// Reconcile were written against that. A missing root is an empty result.
	entries, _ := st.Site.Store().List(ctx, root, storage.Recursive())
	var items []CachedItem
	for _, e := range entries {
		if e.IsDir || !strings.HasSuffix(e.Key, ".md") {
			continue
		}
		normKey := strings.TrimPrefix(e.Key, root+"/") // <author>/<date>/<id>.md — matches blessedNormKey
		item := CachedItem{Key: normKey, MDKey: e.Key}
		if metaRaw, err := st.Site.Store().Get(ctx, e.Key+".meta.json"); err == nil {
			var bsc BlessedSidecar
			if json.Unmarshal(metaRaw, &bsc) == nil {
				item.Sidecar = blessedToSidecar(bsc)
				item.HasSidecar = true
			}
		}
		items = append(items, item)
	}
	return items, nil
}

// DesiredSet is the authoritative set of blessed comments that SHOULD be
// cached: every entry in the post author's local blessed.json render index,
// keyed by the normalized form List() returns. blessed.json IS the source of
// truth — there is NO DS query.
//
// Why blessed.json (not a DS query): blessed.json is the post author's own
// decision about what to display, and it is kept correct + complete by the
// real-time sync path (grants/denials) and the periodic Clerk(detect) +
// Chaplain(repair) reconcile. Sourcing the desired set from it lets the cache upkeep
// reconstitute the cache offline of the DS and at any scale (no per-comment
// query, no `maxDSResponseRecords` cliff). The descriptor's Fetch/VerifyCached
// still dereference the comment author's canonical URL to fetch+verify the
// body — the DS is simply not needed to know WHAT to cache.
//
// DURABILITY: an entry leaves the desired set ONLY when it leaves blessed.json,
// which happens only on a signed withdrawal/deny (real-time eviction in the
// webapp sync path, or the periodic symmetric removal in Chaplain). An
// unreachable author stays in blessed.json → stays desired → Reconcile KEEPS
// its cached entry, never drops it. A malformed/unreadable blessed.json returns
// an error → Reconcile does NOTHING (never evict on uncertainty); an absent
// blessed.json is an empty desired set (nothing blessed locally).
func (b *blessedDescriptor) DesiredSet(st Store) (map[string]DesiredEntry, error) {
	blessed, err := metadata.LoadBlessedComments(st.Site)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]DesiredEntry{}, nil // nothing blessed locally
		}
		return nil, fmt.Errorf("load blessed.json: %w", err)
	}
	desired := make(map[string]DesiredEntry)
	for _, pc := range blessed.Comments {
		for _, c := range pc.Blessed {
			nk := blessedNormKey(c.URL)
			if nk == "" {
				continue // not a recognizable comment URL — skip
			}
			desired[nk] = DesiredEntry{Key: c.URL, Pin: c.Version}
		}
	}
	return desired, nil
}

// ----------------------------------------------------------------------------
// blessed helpers
// ----------------------------------------------------------------------------

// blessedNormKey maps a comment URL (canonical OR mount form) to the cache's
// relative key "<author>/<date>/<id>.md" — the same layout blessedKeys writes,
// so List() entries and DesiredSet() entries compare equal. Returns "" for a
// non-comment / hostless URL.
func blessedNormKey(commentURL string) string {
	rel := polisurl.CommentURLToContentRel(commentURL)
	author := AuthorDomainFromURL(commentURL)
	if rel == "" || !safeSegment(author) {
		return ""
	}
	suffix := strings.TrimPrefix(rel, "content/pub.polis.core/comment/")
	return author + "/" + suffix
}

// commentSourceURL converts a comment URL to its canonical content-source URL.
// A mount-path URL (/comments/<…>) becomes /content/pub.polis.core/comment/<…>;
// a URL already in source form is returned unchanged. Mirrors the webapp helper
// of the same name (kept in sync — both must agree).
func commentSourceURL(commentURL string) string {
	const mount = "/comments/"
	idx := strings.Index(commentURL, mount)
	if idx < 0 {
		return commentURL
	}
	return commentURL[:idx] + "/content/pub.polis.core/comment/" + commentURL[idx+len(mount):]
}

// sourceURLOf returns the best source URL for a cached entry: the sidecar's if
// present, else the provided key (which may be the normalized relative key, in
// which case the sidecar SourceURL is authoritative).
func sourceURLOf(key string, sc Sidecar) string {
	if sc.SourceURL != "" {
		return sc.SourceURL
	}
	return key
}

// blessedToSidecar projects the on-disk BlessedSidecar (the WS3 format, a
// forward-compatible subset) up into the generalized Sidecar the cache upkeep reads. This
// is what keeps the generalization additive: the disk format is unchanged, so
// old blessed sidecars still load.
func blessedToSidecar(bsc BlessedSidecar) Sidecar {
	kind := bsc.Kind
	if kind == "" {
		kind = KindBlessed
	}
	return Sidecar{
		Kind:        kind,
		Key:         blessedNormKey(bsc.SourceURL),
		SourceURL:   bsc.SourceURL,
		FetchedAt:   bsc.FetchedAt,
		VerifiedAt:  bsc.VerifiedAt,
		Integrity:   Integrity{Mode: IntegritySignature, SignerDomain: bsc.AuthorDomain},
		Pin:         bsc.BlessedVersionHash,
		Provenance:  Provenance{AuthorDomain: bsc.AuthorDomain, CanonicalURL: bsc.SourceURL},
		EvictOn:     []string{"pub.polis.comment.unpublished", "pub.polis.comment.blessing.denied"},
		DesiredFrom: "blessed.json",
		KeyUsed:     bsc.KeyUsed,
	}
}

// sidecarToBlessed projects the generalized Sidecar back down to the on-disk
// BlessedSidecar so StoreBlessed writes the unchanged WS3 format.
func sidecarToBlessed(sc Sidecar) BlessedSidecar {
	author := sc.Provenance.AuthorDomain
	if author == "" {
		author = sc.Integrity.SignerDomain
	}
	return BlessedSidecar{
		Kind:               KindBlessed,
		SourceURL:          sc.SourceURL,
		AuthorDomain:       author,
		BlessedVersionHash: sc.Pin,
		FetchedAt:          sc.FetchedAt,
		VerifiedAt:         sc.VerifiedAt,
		KeyUsed:            sc.KeyUsed,
	}
}
