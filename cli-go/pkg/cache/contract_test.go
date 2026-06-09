package cache

import (
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

// spyDescriptor is an in-memory Descriptor for exercising the generic ops in
// isolation (no HTTP/DS). Keys are their own normalized form.
type spyDescriptor struct {
	mu      sync.Mutex
	bodies  map[string][]byte
	cars    map[string]Sidecar
	desired map[string]DesiredEntry
	desErr  error

	// fetch returns the outcome for a key; defaults to a cacheable valid entry.
	fetch func(key string) FetchOutcome
	// verify returns the integrity verdict for VerifyCached.
	verify func(key string) IntegrityVerdict

	fetchCalls []string
	storeCalls []string
	evictCalls []string
}

func newSpy() *spyDescriptor {
	return &spyDescriptor{bodies: map[string][]byte{}, cars: map[string]Sidecar{}, desired: map[string]DesiredEntry{}}
}

func (s *spyDescriptor) Kind() string { return "spy" }

func (s *spyDescriptor) Paths(st Store, key string) (string, string, bool) {
	if key == "" || key == "bad" {
		return "", "", false
	}
	md := filepath.Join(st.DataDir, key+".md")
	return md, md + ".meta.json", true
}

func (s *spyDescriptor) Fetch(st Store, key string, opts IngestOptions) FetchOutcome {
	s.mu.Lock()
	s.fetchCalls = append(s.fetchCalls, key)
	s.mu.Unlock()
	if s.fetch != nil {
		return s.fetch(key)
	}
	return FetchOutcome{
		Body:      []byte("body:" + key),
		Sidecar:   Sidecar{Kind: "spy", Key: key, SourceURL: key, FetchedAt: "t0", VerifiedAt: "t0"},
		Cacheable: true,
		Verdict:   "valid",
		Source:    "content",
	}
}

func (s *spyDescriptor) Store(st Store, sc Sidecar, body []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.storeCalls = append(s.storeCalls, sc.Key)
	s.bodies[sc.Key] = body
	s.cars[sc.Key] = sc
	return nil
}

func (s *spyDescriptor) Read(st Store, key string) ([]byte, Sidecar, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.bodies[key]
	return b, s.cars[key], ok
}

func (s *spyDescriptor) Evict(st Store, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictCalls = append(s.evictCalls, key)
	delete(s.bodies, key)
	delete(s.cars, key)
	return nil
}

func (s *spyDescriptor) VerifyCached(st Store, key string, body []byte, sc Sidecar) IntegrityVerdict {
	if s.verify != nil {
		return s.verify(key)
	}
	return IntegrityVerdict{Status: "valid"}
}

func (s *spyDescriptor) List(st Store) ([]CachedItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var items []CachedItem
	for k, sc := range s.cars {
		items = append(items, CachedItem{Key: k, MDPath: filepath.Join(st.DataDir, k+".md"), Sidecar: sc, HasSidecar: true})
	}
	return items, nil
}

func (s *spyDescriptor) DesiredSet(st Store) (map[string]DesiredEntry, error) {
	if s.desErr != nil {
		return nil, s.desErr
	}
	return s.desired, nil
}

func (s *spyDescriptor) EvictionSignals() []string { return []string{"evict.signal"} }
func (s *spyDescriptor) TTL() time.Duration        { return 0 }

func st(t *testing.T) Store { return Store{DataDir: t.TempDir(), DSDomain: "ds.test"} }

// TestIngestIdempotent: a second Ingest of a present, pinned key is a no-op.
func TestIngestIdempotent(t *testing.T) {
	s := newSpy()
	store := st(t)
	r1 := Ingest(store, s, "k1", IngestOptions{})
	if !r1.Stored {
		t.Fatalf("first ingest should store, got %+v", r1)
	}
	r2 := Ingest(store, s, "k1", IngestOptions{})
	if !r2.Skipped || r2.Stored {
		t.Fatalf("second ingest should skip (pinned), got %+v", r2)
	}
	if len(s.fetchCalls) != 1 {
		t.Errorf("pinned re-ingest must not re-fetch; fetchCalls=%v", s.fetchCalls)
	}
}

// TestIngestRefusesInvalid: a non-cacheable outcome is not stored.
func TestIngestRefusesInvalid(t *testing.T) {
	s := newSpy()
	s.fetch = func(key string) FetchOutcome {
		return FetchOutcome{Cacheable: false, Verdict: "invalid", Refused: true}
	}
	r := Ingest(st(t), s, "tampered", IngestOptions{})
	if r.Stored || !r.Refused || r.Verdict != "invalid" {
		t.Fatalf("invalid-signature ingest must refuse, got %+v", r)
	}
	if len(s.storeCalls) != 0 {
		t.Errorf("refused ingest must not Store; storeCalls=%v", s.storeCalls)
	}
}

// TestIngestUnsupportedKey: a key the descriptor doesn't recognize is a clean no-op.
func TestIngestUnsupportedKey(t *testing.T) {
	s := newSpy()
	r := Ingest(st(t), s, "bad", IngestOptions{})
	if r.Stored || r.Skipped || r.Err != nil || r.Verdict != "unsupported_key" {
		t.Fatalf("unsupported key must be a clean no-op, got %+v", r)
	}
}

// TestRealTimeAndReconcileShareIngest is the anti-drift proof: the real-time
// path (a direct Ingest) and Reconcile produce a byte-identical stored sidecar
// for the same key, because Reconcile re-fetches missing desired keys THROUGH
// the same Ingest → Fetch → Store pipeline. If the two ever forked onto
// separate logic, S1 and S2 would diverge.
func TestRealTimeAndReconcileShareIngest(t *testing.T) {
	// Real-time path: direct Ingest of "shared".
	rt := newSpy()
	rtStore := st(t)
	if r := Ingest(rtStore, rt, "shared", IngestOptions{}); !r.Stored {
		t.Fatalf("real-time ingest failed: %+v", r)
	}
	s1 := rt.cars["shared"]

	// Reconcile path: "shared" is desired but not present → Reconcile ingests it.
	rc := newSpy()
	rc.desired = map[string]DesiredEntry{"shared": {Key: "shared"}}
	rcStore := st(t)
	rep, err := Reconcile(rcStore, rc, ReconcileOptions{})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}
	if rep.Refetched != 1 {
		t.Fatalf("reconcile should refetch the missing desired key, report=%+v", rep)
	}
	s2 := rc.cars["shared"]

	if !reflect.DeepEqual(s1, s2) {
		t.Errorf("real-time and reconcile produced DIFFERENT sidecars — paths have drifted:\n  rt=%+v\n  rc=%+v", s1, s2)
	}
	// Both routed through Fetch (no bypass).
	if len(rt.fetchCalls) != 1 || len(rc.fetchCalls) != 1 {
		t.Errorf("both paths must fetch through the descriptor: rt=%v rc=%v", rt.fetchCalls, rc.fetchCalls)
	}
}

// TestReconcileEvictsUndesired: a present key no longer in the desired set
// (withdrawn/denied on the DS) is evicted.
func TestReconcileEvictsUndesired(t *testing.T) {
	s := newSpy()
	store := st(t)
	Ingest(store, s, "keep", IngestOptions{})
	Ingest(store, s, "drop", IngestOptions{})
	s.desired = map[string]DesiredEntry{"keep": {Key: "keep"}} // "drop" no longer blessed

	rep, err := Reconcile(store, s, ReconcileOptions{})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if rep.Evicted != 1 || rep.Kept != 1 {
		t.Fatalf("expected 1 evicted + 1 kept, got %+v", rep)
	}
	if _, _, ok := s.Read(store, "drop"); ok {
		t.Error("withdrawn entry must be evicted")
	}
	if _, _, ok := s.Read(store, "keep"); !ok {
		t.Error("still-blessed entry must be kept")
	}
}

// TestReconcileDurabilityKeepsUnreachable: a desired key whose author is
// unreachable (Fetch errors) stays MISSING but is NEVER evicted — unreachability
// is never an eviction signal.
func TestReconcileDurabilityKeepsUnreachable(t *testing.T) {
	s := newSpy()
	store := st(t)
	// "cached" is present and still desired; "offline" is desired but its author
	// is unreachable (Fetch errors).
	Ingest(store, s, "cached", IngestOptions{})
	s.desired = map[string]DesiredEntry{
		"cached":  {Key: "cached"},
		"offline": {Key: "offline"},
	}
	s.fetch = func(key string) FetchOutcome {
		if key == "offline" {
			return FetchOutcome{Verdict: "fetch_failed", Err: os.ErrDeadlineExceeded}
		}
		return FetchOutcome{Body: []byte("b"), Sidecar: Sidecar{Kind: "spy", Key: key, SourceURL: key}, Cacheable: true, Verdict: "valid"}
	}

	rep, err := Reconcile(store, s, ReconcileOptions{})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if rep.Evicted != 0 {
		t.Errorf("an unreachable author must NEVER cause eviction, got %d evicted", rep.Evicted)
	}
	if len(rep.Errors) != 1 {
		t.Errorf("expected 1 refetch error logged for the offline author, got %v", rep.Errors)
	}
	if _, _, ok := s.Read(store, "cached"); !ok {
		t.Error("present + desired entry must be kept through a peer's outage")
	}
}

// TestReconcileNoOpOnDesiredSetError: if the authoritative set can't be computed
// (DS down), Reconcile does NOTHING — never evicts on uncertainty.
func TestReconcileNoOpOnDesiredSetError(t *testing.T) {
	s := newSpy()
	store := st(t)
	Ingest(store, s, "x", IngestOptions{})
	s.desErr = os.ErrDeadlineExceeded

	_, err := Reconcile(store, s, ReconcileOptions{})
	if err == nil {
		t.Fatal("expected an error when the desired set can't be computed")
	}
	if len(s.evictCalls) != 0 {
		t.Errorf("must not evict when the desired set is unknown; evictCalls=%v", s.evictCalls)
	}
}

// TestReconcileIdempotent: a second run with the same state stores/evicts nothing.
func TestReconcileIdempotent(t *testing.T) {
	s := newSpy()
	store := st(t)
	s.desired = map[string]DesiredEntry{"a": {Key: "a"}, "b": {Key: "b"}}

	if _, err := Reconcile(store, s, ReconcileOptions{}); err != nil {
		t.Fatalf("reconcile 1: %v", err)
	}
	fetchAfter1 := len(s.fetchCalls)
	evictAfter1 := len(s.evictCalls)

	rep2, err := Reconcile(store, s, ReconcileOptions{})
	if err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	if rep2.Refetched != 0 || rep2.Evicted != 0 {
		t.Errorf("second reconcile must be a no-op, got %+v", rep2)
	}
	if len(s.fetchCalls) != fetchAfter1 {
		t.Errorf("second reconcile must not re-fetch (pinned): %d -> %d", fetchAfter1, len(s.fetchCalls))
	}
	if len(s.evictCalls) != evictAfter1 {
		t.Errorf("second reconcile must not evict: %d -> %d", evictAfter1, len(s.evictCalls))
	}
}

// TestGCRemovesOrphans: a body with no readable sidecar is reclaimed; a healthy
// entry is untouched.
func TestGCRemovesOrphans(t *testing.T) {
	s := newSpy()
	store := st(t)
	// Healthy entry (has sidecar).
	Ingest(store, s, "healthy", IngestOptions{})
	// Inject an orphan: present in List with HasSidecar=false. We model this by
	// adding a body with no sidecar via a custom List entry — simulate by writing
	// the on-disk md and having List report it. Use a real file for GC to remove.
	orphanPath := filepath.Join(store.DataDir, "orphan.md")
	if err := os.WriteFile(orphanPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Override List to include the orphan.
	gc := &orphanListDescriptor{spyDescriptor: s, orphan: CachedItem{Key: "orphan", MDPath: orphanPath, HasSidecar: false}}

	rep, err := GC(store, gc)
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	if rep.Removed != 1 {
		t.Fatalf("expected 1 orphan removed, got %+v", rep)
	}
	if _, err := os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Error("orphan body must be deleted")
	}
	if _, _, ok := s.Read(store, "healthy"); !ok {
		t.Error("GC must not touch a healthy (sidecar-backed) entry")
	}
}

// orphanListDescriptor wraps the spy to inject a sidecar-less orphan into List.
type orphanListDescriptor struct {
	*spyDescriptor
	orphan CachedItem
}

func (o *orphanListDescriptor) List(store Store) ([]CachedItem, error) {
	items, err := o.spyDescriptor.List(store)
	if err != nil {
		return nil, err
	}
	return append(items, o.orphan), nil
}

// TestVerifyIntegrityMissing: verifying an absent key reports missing (not a
// tamper).
func TestVerifyIntegrityMissing(t *testing.T) {
	s := newSpy()
	v := VerifyIntegrity(st(t), s, "absent")
	if v.Status != "missing" || v.Tampered() {
		t.Fatalf("absent entry must verify as missing, got %+v", v)
	}
}

// TestRegistryDefault: the default registry has exactly the blessed descriptor.
func TestRegistryDefault(t *testing.T) {
	r := DefaultRegistry()
	kinds := r.Kinds()
	if len(kinds) != 1 || kinds[0] != KindBlessed {
		t.Fatalf("default registry must hold exactly [blessed], got %v", kinds)
	}
	d, ok := r.Build(KindBlessed, DescriptorConfig{Domain: "x.polis.pub"})
	if !ok || d.Kind() != KindBlessed {
		t.Fatalf("registry must build the blessed descriptor, ok=%v", ok)
	}
}
