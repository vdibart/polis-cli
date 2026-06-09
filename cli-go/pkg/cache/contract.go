// Kind-agnostic cache contract (Rosie's substrate).
//
// WS-R1 (plans/rosie-cache-custodian-design.md) generalizes the concrete
// blessed-comment cache (WS3 of plans/comment-registration-severe-bug.md) into
// a kind-agnostic descriptor/registry contract, so future caches (avatar,
// reply-context, feed) plug into the SAME machinery and Rosie never
// special-cases a type. The blessed kind is the only descriptor registered
// today (YAGNI) — adopting another cache is *registering a descriptor*, not
// editing Rosie.
//
// The anti-drift property: the real-time sync handlers ingest/evict THROUGH the
// generic ops here, and Rosie's periodic backstop reconciles THROUGH the same
// ops — one code path, two triggers, guaranteed not to drift.
package cache

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/verify"
)

// Store identifies where a kind's cache lives for one tenant under one DS.
type Store struct {
	DataDir  string // the tenant's data directory
	DSDomain string // the discovery-service domain scope (.polis/ds/<DSDomain>/…)
}

// IntegrityMode describes how an entry's authenticity is established.
type IntegrityMode string

const (
	IntegritySignature IntegrityMode = "signature" // author Ed25519 signature (blessed)
	IntegrityHash      IntegrityMode = "hash"      // a known content hash (e.g. avatar)
	IntegrityNone      IntegrityMode = "none"      // unverifiable (best-effort cache)
)

// Integrity records how a cached entry is verified. signer_domain/expected_hash
// are optional so a signed-pinned cache (blessed) and a hash/TTL-only cache
// (avatar) share one schema.
type Integrity struct {
	Mode         IntegrityMode `json:"mode"`
	SignerDomain string        `json:"signer_domain,omitempty"`
	ExpectedHash string        `json:"expected_hash,omitempty"`
}

// Provenance attributes a cached foreign artifact back to its origin.
type Provenance struct {
	AuthorDomain string `json:"author_domain,omitempty"`
	CanonicalURL string `json:"canonical_url,omitempty"`
}

// Sidecar is the kind-agnostic provenance/lifecycle metadata for one cached
// entry. Rosie reads this uniformly for EVERY kind. Each descriptor owns the
// on-disk serialization — the blessed kind persists the WS3 BlessedSidecar (a
// forward-compatible subset of these fields), so generalizing was additive and
// old blessed sidecars still load. fetched_at/verified_at are recorded
// EXPLICITLY (never inferred from mtime, fragile across tar/rsync/backup).
type Sidecar struct {
	Kind        string     `json:"kind"`
	Key         string     `json:"key,omitempty"`
	SourceURL   string     `json:"source_url"`
	FetchedAt   string     `json:"fetched_at"`
	VerifiedAt  string     `json:"verified_at,omitempty"`
	ExpiresAt   string     `json:"expires_at,omitempty"`
	Integrity   Integrity  `json:"integrity,omitempty"`
	Pin         string     `json:"pin,omitempty"` // blessed = blessed version hash
	Provenance  Provenance `json:"provenance,omitempty"`
	EvictOn     []string   `json:"evict_on,omitempty"`     // DS event types that evict
	DesiredFrom string     `json:"desired_from,omitempty"` // the authoritative "should this exist"
}

// IntegrityVerdict is the outcome of (re)verifying a cached entry.
type IntegrityVerdict struct {
	Status string // "valid" | "invalid" | "missing" | "error"
	Detail string
}

// Tampered reports a signature that fails the author's CURRENT key — a
// possible tamper/MITM. A clean key-rotation that re-signs is "valid", and a
// transient key-fetch failure is "error"; only "invalid" is security-relevant.
func (v IntegrityVerdict) Tampered() bool { return v.Status == "invalid" }

// DesiredEntry is one item the authoritative source says SHOULD be cached.
type DesiredEntry struct {
	Key     string            // the fetchable key (== source URL for blessed)
	Pin     string            // expected version hash, if the kind pins
	Context map[string]string // descriptor-specific extras (e.g. blessed: in_reply_to)
}

// CachedItem is one entry currently present on disk.
type CachedItem struct {
	Key        string  // normalized comparison key (matches DesiredSet keys)
	MDPath     string  // absolute path to the cached body
	Sidecar    Sidecar // best-effort; zero value if HasSidecar is false
	HasSidecar bool    // false when the body exists but the sidecar is missing/unreadable
}

// IngestOptions tune a single ingest.
type IngestOptions struct {
	// RelocateFromContent enables the one-time migration fallback: when the
	// author is unreachable AND no cache entry exists yet, build from a local
	// content/ cross-tenant copy's bytes (with a relocate marker) rather than
	// skipping it. The daily actor leaves this false (steady-state has no
	// content/ copies to relocate from). See the parent plan's WS4 migration.
	RelocateFromContent bool
	// Throttle, if set, is called immediately before a real network Fetch (never
	// on an idempotent cache hit). The migration/actor sets it to a ~1/sec
	// limiter so a cache rebuild doesn't self-DoS or trip author rate limits.
	Throttle func()
}

// FetchOutcome is what a descriptor's Fetch returns.
type FetchOutcome struct {
	Body      []byte
	Sidecar   Sidecar
	Cacheable bool   // false → do not store (e.g. refused invalid signature)
	Verdict   string // valid|invalid|missing|error|fetch_failed|relocated|unsupported_key
	Source    string // descriptor-specific (blessed: content|mount|relocate)
	Refused   bool   // present-but-invalid signature → refused (security-relevant)
	Relocated bool   // built from the local content/ copy (migration fallback)
	Err       error  // hard fetch failure (network); never set with Cacheable
}

// IngestResult is the outcome of the generic Ingest op.
type IngestResult struct {
	Stored    bool   // a new entry was written
	Skipped   bool   // already cached (pinned kinds never refresh) — no-op
	Relocated bool   // built from the local content/ copy (migration fallback)
	Verdict   string // valid|invalid|missing|error|fetch_failed|store_failed|unsupported_key
	Source    string
	Refused   bool
	Err       error
}

// Descriptor is the ONLY per-kind code. Everything else (Ingest/Evict/
// Reconcile/GC/…) is generic and runs over this interface unchanged.
type Descriptor interface {
	Kind() string
	// Paths returns the body + sidecar paths for a key under the store, or
	// ok=false if the key is not valid for this kind.
	Paths(st Store, key string) (mdPath, metaPath string, ok bool)
	// Fetch retrieves + verifies the payload for a key.
	Fetch(st Store, key string, opts IngestOptions) FetchOutcome
	// Store persists a fetched body + sidecar (descriptor owns the on-disk format).
	Store(st Store, sc Sidecar, body []byte) error
	// Read returns a cached body + sidecar, or ok=false if absent.
	Read(st Store, key string) (body []byte, sc Sidecar, ok bool)
	// Evict removes a cached entry. Idempotent (a missing entry is not an error).
	Evict(st Store, key string) error
	// VerifyCached re-verifies an already-cached entry against the author's
	// CURRENT key (key-rotation aware).
	VerifyCached(st Store, key string, body []byte, sc Sidecar) IntegrityVerdict
	// List enumerates currently-cached entries under the store (including
	// sidecar-less orphan bodies, for GC).
	List(st Store) ([]CachedItem, error)
	// DesiredSet computes the authoritative set of keys that SHOULD be cached,
	// keyed by the same normalized form List() returns.
	DesiredSet(st Store) (map[string]DesiredEntry, error)
	EvictionSignals() []string
	TTL() time.Duration
}

// ----------------------------------------------------------------------------
// Generic operations — both the real-time path AND Rosie call these. No
// per-kind branching anywhere below this line.
// ----------------------------------------------------------------------------

// Ingest fetches+verifies+stores one key via the descriptor, idempotently.
// Returns Skipped (a no-op) when the key is already cached — pinned kinds never
// refresh, so a second call is free. Both the real-time sync grant path and
// Rosie's Reconcile call THIS function, which is what guarantees they cannot
// drift onto separate fetch/verify/store logic.
func Ingest(st Store, d Descriptor, key string, opts IngestOptions) IngestResult {
	if _, _, ok := d.Paths(st, key); !ok {
		return IngestResult{Verdict: "unsupported_key"}
	}
	// Pinned-by-version: an existing entry is authoritative; an edit never
	// refreshes it (a re-bless of a new version is a fresh ingest after evict).
	if _, _, ok := d.Read(st, key); ok {
		return IngestResult{Skipped: true}
	}
	if opts.Throttle != nil {
		opts.Throttle() // rate-limit only real fetches, never cache hits
	}
	out := d.Fetch(st, key, opts)
	if out.Err != nil {
		return IngestResult{Verdict: out.Verdict, Source: out.Source, Err: out.Err}
	}
	if !out.Cacheable {
		return IngestResult{Verdict: out.Verdict, Refused: out.Refused, Source: out.Source}
	}
	if err := d.Store(st, out.Sidecar, out.Body); err != nil {
		return IngestResult{Verdict: "store_failed", Source: out.Source, Err: err}
	}
	return IngestResult{Stored: true, Relocated: out.Relocated, Verdict: out.Verdict, Source: out.Source}
}

// Refresh forces a re-fetch even if the key is present. For pinned kinds
// (blessed) this is rarely used; it is part of the generic contract for
// TTL/non-pinned kinds. Evict-then-Ingest so the new fetch isn't short-circuited
// by the idempotency check.
func Refresh(st Store, d Descriptor, key string, opts IngestOptions) IngestResult {
	_ = d.Evict(st, key)
	return Ingest(st, d, key, opts)
}

// Evict removes a cached entry via the descriptor (idempotent).
func Evict(st Store, d Descriptor, key string) error {
	return d.Evict(st, key)
}

// VerifyIntegrity re-verifies one cached entry against the author's CURRENT
// key. A clean key-rotation that re-signs verifies fine (Status "valid") and
// must NOT raise an integrity failure; only a signature failing the current key
// (Tampered) is security-relevant.
func VerifyIntegrity(st Store, d Descriptor, key string) IntegrityVerdict {
	body, sc, ok := d.Read(st, key)
	if !ok {
		return IntegrityVerdict{Status: "missing"}
	}
	return d.VerifyCached(st, key, body, sc)
}

// EnforceTTL evicts entries whose sidecar ExpiresAt is in the past. Kinds with
// TTL()==0 (blessed — pinned, never expires) are a no-op.
func EnforceTTL(st Store, d Descriptor, now time.Time) (evicted int, err error) {
	if d.TTL() == 0 {
		return 0, nil
	}
	items, err := d.List(st)
	if err != nil {
		return 0, err
	}
	for _, it := range items {
		if !it.HasSidecar || it.Sidecar.ExpiresAt == "" {
			continue
		}
		exp, perr := time.Parse(time.RFC3339, it.Sidecar.ExpiresAt)
		if perr != nil {
			continue
		}
		if now.After(exp) {
			if e := d.Evict(st, it.Sidecar.SourceURL); e == nil {
				evicted++
			}
		}
	}
	return evicted, nil
}

// GCReport summarizes a garbage-collection pass.
type GCReport struct {
	Kind    string
	Removed int      // orphan bodies/sidecars removed
	Paths   []string // removed paths (for logging)
	Errors  []string
}

// GC repairs the cache's internal consistency: it removes structurally-orphaned
// entries — a body with no readable sidecar. It does NOT consult the
// authoritative desired set (that is Reconcile's job), so it never evicts a
// healthy, still-desired entry. An orphan body is unsafe to display (no
// provenance/verification record) and regenerable by a later reconcile.
func GC(st Store, d Descriptor) (GCReport, error) {
	rep := GCReport{Kind: d.Kind()}
	items, err := d.List(st)
	if err != nil {
		return rep, err
	}
	for _, it := range items {
		if it.HasSidecar {
			continue
		}
		if e := os.Remove(it.MDPath); e != nil && !os.IsNotExist(e) {
			rep.Errors = append(rep.Errors, fmt.Sprintf("%s: %v", it.MDPath, e))
			continue
		}
		_ = os.Remove(it.MDPath + ".meta.json")
		rep.Removed++
		rep.Paths = append(rep.Paths, it.MDPath)
	}
	return rep, nil
}

// ReconcileOptions tune a reconcile pass.
type ReconcileOptions struct {
	// RelocateFromContent is threaded to Ingest for the one-time migration
	// (build a missing entry from the local content/ copy when the author is
	// unreachable). The daily actor leaves this false.
	RelocateFromContent bool
	// Throttle is threaded to each missing-key Ingest's fetch (see
	// IngestOptions.Throttle). nil = no throttle.
	Throttle func()
}

// ReconcileReport summarizes one kind's reconcile pass on one store.
type ReconcileReport struct {
	Kind      string
	Desired   int      // authoritative count (DesiredSet)
	Present   int      // entries currently on disk
	Refetched int      // missing-desired keys fetched + stored fresh
	Relocated int      // missing-desired keys built from the local content/ copy
	Evicted   int      // present keys no longer desired (signed withdrawal/deny)
	Kept      int      // present + still desired (incl. unreachable authors kept)
	Evictions []string // evicted keys (for logging)
	Refetches []string // refetched keys (for logging)
	Errors    []string
}

// Reconcile brings the cache for ONE kind in ONE store into agreement with the
// descriptor's authoritative DesiredSet:
//   - re-fetch+store missing desired keys (via the SAME Ingest the real-time
//     path uses),
//   - evict present keys that are no longer desired — i.e. a signed
//     withdrawal/deny removed them from the authoritative set,
//   - keep everything else.
//
// DURABILITY (the load-bearing rule): eviction is driven ONLY by "present but
// not desired". An unreachable author is still in the desired set (its blessing
// is still granted on the DS), so its entry is KEPT, never dropped —
// unreachability is evidence for durability, never eviction (parent plan
// Decision-2 corollary). A failed re-fetch of a missing desired key likewise
// never evicts; it simply stays missing until the author is reachable.
//
// If the authoritative DesiredSet itself can't be computed (DS unreachable),
// Reconcile does NOTHING and returns the error — it never evicts on
// uncertainty.
//
// IDEMPOTENT: a second run with the same DS state is a no-op — all desired keys
// are already present + pinned (Ingest skips), and nothing is newly undesired.
func Reconcile(st Store, d Descriptor, opts ReconcileOptions) (ReconcileReport, error) {
	rep := ReconcileReport{Kind: d.Kind()}

	desired, err := d.DesiredSet(st)
	if err != nil {
		return rep, fmt.Errorf("cache: reconcile %s: desired set: %w", d.Kind(), err)
	}
	rep.Desired = len(desired)

	present, err := d.List(st)
	if err != nil {
		return rep, fmt.Errorf("cache: reconcile %s: list: %w", d.Kind(), err)
	}
	rep.Present = len(present)

	presentKeys := make(map[string]bool, len(present))
	for _, it := range present {
		presentKeys[it.Key] = true
	}

	// 1. Re-fetch missing desired keys. A failed fetch (unreachable author)
	//    leaves the key missing — it is NEVER evicted, because it is still
	//    desired.
	for normKey, de := range desired {
		if presentKeys[normKey] {
			rep.Kept++
			continue
		}
		res := Ingest(st, d, de.Key, IngestOptions{RelocateFromContent: opts.RelocateFromContent, Throttle: opts.Throttle})
		switch {
		case res.Stored && res.Relocated:
			rep.Relocated++
			rep.Refetches = append(rep.Refetches, de.Key)
		case res.Stored:
			rep.Refetched++
			rep.Refetches = append(rep.Refetches, de.Key)
		case res.Err != nil:
			rep.Errors = append(rep.Errors, fmt.Sprintf("refetch %s: %v", de.Key, res.Err))
		case res.Refused:
			rep.Errors = append(rep.Errors, fmt.Sprintf("refetch %s: refused (invalid signature)", de.Key))
		}
	}

	// 2. Evict present keys that are no longer desired — the authoritative set
	//    dropped them, which happens ONLY on a signed withdrawal/deny.
	for _, it := range present {
		if isDesired(desired, it.Key) {
			continue
		}
		// A sidecar-less orphan has no source URL to evict by — leave it for GC.
		if !it.HasSidecar || it.Sidecar.SourceURL == "" {
			continue
		}
		if e := d.Evict(st, it.Sidecar.SourceURL); e != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("evict %s: %v", it.Sidecar.SourceURL, e))
			continue
		}
		rep.Evicted++
		rep.Evictions = append(rep.Evictions, it.Sidecar.SourceURL)
	}

	return rep, nil
}

// isDesired reports whether a present key is in the desired set.
func isDesired(desired map[string]DesiredEntry, key string) bool {
	_, ok := desired[key]
	return ok
}

// ----------------------------------------------------------------------------
// Registry — mirrors the ops.Engine/Handler content-type registry. A descriptor
// needs per-sweep config (a DS client, the tenant domain), so the registry
// stores constructors, not instances.
// ----------------------------------------------------------------------------

// DescriptorConfig is the per-tenant/per-sweep configuration a descriptor needs.
type DescriptorConfig struct {
	Client *discovery.Client // for DesiredSet (granted-blessing query)
	Domain string            // the tenant's public domain (relationship actor)
	// VerifyContent is injectable for tests; defaults to verify.VerifyContent.
	VerifyContent func(url string) (*verify.VerificationResult, error)
	// Now is injectable for tests; defaults to time.Now.
	Now func() time.Time
}

// DescriptorFactory builds a descriptor from per-sweep config.
type DescriptorFactory func(DescriptorConfig) Descriptor

// Registry maps a kind name to its descriptor factory.
type Registry struct {
	factories map[string]DescriptorFactory
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{factories: map[string]DescriptorFactory{}}
}

// Register adds a descriptor factory for a kind.
func (r *Registry) Register(kind string, f DescriptorFactory) {
	r.factories[kind] = f
}

// Kinds returns the registered kind names, sorted for deterministic sweeps.
func (r *Registry) Kinds() []string {
	out := make([]string, 0, len(r.factories))
	for k := range r.factories {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Build instantiates a descriptor for a kind with the given config.
func (r *Registry) Build(kind string, cfg DescriptorConfig) (Descriptor, bool) {
	f, ok := r.factories[kind]
	if !ok {
		return nil, false
	}
	return f(cfg), true
}

// DefaultRegistry has exactly the blessed descriptor registered. YAGNI: adopting
// avatar/reply-context/feed later means REGISTERING a descriptor here, not
// editing Rosie's sweep.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register(KindBlessed, NewBlessedDescriptor)
	return r
}
