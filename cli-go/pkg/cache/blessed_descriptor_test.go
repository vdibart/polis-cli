package cache

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/verify"
)

const (
	bdDS    = "ds.polis.pub"
	bdURL1  = "https://alice.polis.pub/content/pub.polis.core/comment/20260601/c1.md"
	bdURL2  = "https://bob.polis.pub/content/pub.polis.core/comment/20260601/c2.md"
	bdBody1 = "---\ntype: comment\nauthor: alice.polis.pub\n---\nhello from alice"
)

// blessedDescForTest builds a blessed descriptor with an injected verifyContent
// (so tests are deterministic and need no real author HTTP server). The blessed
// descriptor sources its DesiredSet from blessed.json and needs no DS client.
func blessedDescForTest(vc func(string) (*verify.VerificationResult, error)) Descriptor {
	return NewBlessedDescriptor(DescriptorConfig{VerifyContent: vc})
}

func validVerify(body string) func(string) (*verify.VerificationResult, error) {
	return func(url string) (*verify.VerificationResult, error) {
		return &verify.VerificationResult{
			URL:            url,
			CurrentVersion: "sha256:pinnedhash",
			Signature:      verify.SignatureResult{Status: "valid"},
			Body:           body,
		}, nil
	}
}

// TestBlessedIngest_ValidStoresPinAndVerifiedAt: a valid fetch stores the body,
// pins the blessed version, and records VerifiedAt. The on-disk format is the
// unchanged BlessedSidecar (additive generalization).
func TestBlessedIngest_ValidStoresPinAndVerifiedAt(t *testing.T) {
	dir := t.TempDir()
	d := blessedDescForTest(validVerify(bdBody1))
	store := Store{DataDir: dir, DSDomain: bdDS}

	res := Ingest(store, d, bdURL1, IngestOptions{})
	if !res.Stored || res.Verdict != "valid" {
		t.Fatalf("expected stored+valid, got %+v", res)
	}
	// On-disk sidecar is the legacy BlessedSidecar (compat).
	body, bsc, ok := ReadBlessed(dir, bdDS, bdURL1)
	if !ok {
		t.Fatal("entry not readable via ReadBlessed (on-disk compat broken)")
	}
	if string(body) != bdBody1 {
		t.Errorf("body mismatch: %q", body)
	}
	if bsc.BlessedVersionHash != "sha256:pinnedhash" {
		t.Errorf("pin not stored: %q", bsc.BlessedVersionHash)
	}
	if bsc.VerifiedAt == "" {
		t.Error("VerifiedAt must be set on a valid signature")
	}
	if bsc.AuthorDomain != "alice.polis.pub" {
		t.Errorf("author_domain = %q, want alice.polis.pub", bsc.AuthorDomain)
	}
}

// TestBlessedIngest_RefusesInvalidSignature: a present-but-invalid signature is
// REFUSED (tamper/MITM) — nothing written.
func TestBlessedIngest_RefusesInvalidSignature(t *testing.T) {
	dir := t.TempDir()
	vc := func(string) (*verify.VerificationResult, error) {
		return &verify.VerificationResult{Signature: verify.SignatureResult{Status: "invalid"}, Body: "tampered"}, nil
	}
	d := blessedDescForTest(vc)
	store := Store{DataDir: dir, DSDomain: bdDS}

	res := Ingest(store, d, bdURL1, IngestOptions{})
	if res.Stored || !res.Refused || res.Verdict != "invalid" {
		t.Fatalf("invalid signature must be refused, got %+v", res)
	}
	if _, _, ok := ReadBlessed(dir, bdDS, bdURL1); ok {
		t.Error("a refused entry must not be on disk")
	}
}

// TestBlessedIngest_MissingSignatureStillCaches: unsigned legacy content caches
// (availability) with VerifiedAt unset.
func TestBlessedIngest_MissingSignatureStillCaches(t *testing.T) {
	dir := t.TempDir()
	vc := func(url string) (*verify.VerificationResult, error) {
		return &verify.VerificationResult{CurrentVersion: "sha256:x", Signature: verify.SignatureResult{Status: "missing"}, Body: "legacy"}, nil
	}
	d := blessedDescForTest(vc)
	store := Store{DataDir: dir, DSDomain: bdDS}

	if res := Ingest(store, d, bdURL1, IngestOptions{}); !res.Stored {
		t.Fatalf("missing-signature content should still cache, got %+v", res)
	}
	_, bsc, _ := ReadBlessed(dir, bdDS, bdURL1)
	if bsc.VerifiedAt != "" {
		t.Error("VerifiedAt must be empty for an unverified entry")
	}
}

// TestBlessedIntegrity_KeyRotationNoFalseAlarm: a cached entry whose author
// rotated keys and RE-SIGNED verifies "valid" against the current key — it must
// NOT raise an integrity failure.
func TestBlessedIntegrity_KeyRotationNoFalseAlarm(t *testing.T) {
	dir := t.TempDir()
	store := Store{DataDir: dir, DSDomain: bdDS}
	// Cache it under the old key.
	Ingest(store, blessedDescForTest(validVerify(bdBody1)), bdURL1, IngestOptions{})

	// Now the author has rotated + re-signed: re-verify still returns "valid".
	d := blessedDescForTest(validVerify(bdBody1))
	v := VerifyIntegrity(store, d, bdURL1)
	if v.Tampered() || v.Status != "valid" {
		t.Fatalf("a clean rotation+resign must verify, got %+v", v)
	}
}

// TestBlessedIntegrity_FailOnTamper: a cached entry whose signature fails the
// author's current key is a tamper signal.
func TestBlessedIntegrity_FailOnTamper(t *testing.T) {
	dir := t.TempDir()
	store := Store{DataDir: dir, DSDomain: bdDS}
	Ingest(store, blessedDescForTest(validVerify(bdBody1)), bdURL1, IngestOptions{})

	tamper := func(string) (*verify.VerificationResult, error) {
		return &verify.VerificationResult{Signature: verify.SignatureResult{Status: "invalid", Message: "SIGNATURE DOES NOT MATCH"}}, nil
	}
	v := VerifyIntegrity(store, blessedDescForTest(tamper), bdURL1)
	if !v.Tampered() {
		t.Fatalf("a signature failing the current key must be Tampered, got %+v", v)
	}
}

// TestBlessedIntegrity_UnreachableIsNotTamper: an unreachable author yields
// "error", never a tamper signal (durability).
func TestBlessedIntegrity_UnreachableIsNotTamper(t *testing.T) {
	dir := t.TempDir()
	store := Store{DataDir: dir, DSDomain: bdDS}
	Ingest(store, blessedDescForTest(validVerify(bdBody1)), bdURL1, IngestOptions{})

	offline := func(string) (*verify.VerificationResult, error) { return nil, os.ErrDeadlineExceeded }
	v := VerifyIntegrity(store, blessedDescForTest(offline), bdURL1)
	if v.Tampered() || v.Status != "error" {
		t.Fatalf("an unreachable author must be 'error', not tamper, got %+v", v)
	}
}

// TestBlessedRelocate_FallbackFromContentCopy: with the author unreachable AND
// no cache entry yet, the migration relocate fallback builds the entry from the
// tenant's local content/ cross-tenant copy.
func TestBlessedRelocate_FallbackFromContentCopy(t *testing.T) {
	dir := t.TempDir()
	store := Store{DataDir: dir, DSDomain: bdDS}
	// Seed a pre-existing local content/ copy (the Defect-3 byte the migration relocates).
	rel := "content/pub.polis.core/comment/20260601/c1.md"
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, rel), []byte("relocated body"), 0o644); err != nil {
		t.Fatal(err)
	}

	offline := func(string) (*verify.VerificationResult, error) { return nil, os.ErrDeadlineExceeded }
	d := blessedDescForTest(offline)

	// Without the flag: a fetch failure stays a fetch failure (no relocate).
	if res := Ingest(store, d, bdURL1, IngestOptions{}); res.Stored {
		t.Fatalf("steady-state must not relocate, got %+v", res)
	}
	// With the migration flag: relocate from the content/ copy.
	res := Ingest(store, d, bdURL1, IngestOptions{RelocateFromContent: true})
	if !res.Stored || !res.Relocated {
		t.Fatalf("migration relocate should store from the content/ copy, got %+v", res)
	}
	body, _, ok := ReadBlessed(dir, bdDS, bdURL1)
	if !ok || string(body) != "relocated body" {
		t.Errorf("relocated body mismatch: %q ok=%v", body, ok)
	}
}

// TestBlessedDesiredSet_FromBlessedJSON: the desired set is exactly the
// blessed.json entries — no DS client, no query.
func TestBlessedDesiredSet_FromBlessedJSON(t *testing.T) {
	dir := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(metadata.AddBlessedComment(dir, "posts/20260601/p.md", metadata.BlessedComment{URL: bdURL1}))
	must(metadata.AddBlessedComment(dir, "posts/20260601/p.md", metadata.BlessedComment{URL: bdURL2}))

	d := blessedDescForTest(validVerify(bdBody1))
	desired, err := d.(*blessedDescriptor).DesiredSet(Store{DataDir: dir, DSDomain: bdDS})
	if err != nil {
		t.Fatalf("DesiredSet: %v", err)
	}
	if len(desired) != 2 {
		t.Fatalf("expected 2 desired (all of blessed.json), got %d: %+v", len(desired), desired)
	}
	for _, u := range []string{bdURL1, bdURL2} {
		if _, ok := desired[blessedNormKey(u)]; !ok {
			t.Errorf("desired must contain %q", u)
		}
	}
}

// TestBlessedDesiredSet_AbsentIsEmpty: no blessed.json → empty desired, no error
// (a tenant with nothing blessed), and no DS client required.
func TestBlessedDesiredSet_AbsentIsEmpty(t *testing.T) {
	dir := t.TempDir()
	d := blessedDescForTest(validVerify(bdBody1))
	desired, err := d.(*blessedDescriptor).DesiredSet(Store{DataDir: dir, DSDomain: bdDS})
	if err != nil {
		t.Fatalf("absent blessed.json must be empty, not error: %v", err)
	}
	if len(desired) != 0 {
		t.Fatalf("expected empty desired, got %+v", desired)
	}
}

// TestBlessedDesiredSet_MalformedErrors: a corrupt blessed.json makes DesiredSet
// error — and therefore Reconcile does NOTHING (never evict on uncertainty).
func TestBlessedDesiredSet_MalformedErrors(t *testing.T) {
	dir := t.TempDir()
	store := Store{DataDir: dir, DSDomain: bdDS}
	d := blessedDescForTest(validVerify(bdBody1))

	// Seed a real cached entry so we can prove a malformed index never evicts it.
	Ingest(store, d, bdURL1, IngestOptions{})

	// Corrupt blessed.json.
	bjPath := filepath.Join(dir, "content/pub.polis.core/comment/blessed.json")
	if err := os.MkdirAll(filepath.Dir(bjPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bjPath, []byte("{ this is not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := d.(*blessedDescriptor).DesiredSet(store); err == nil {
		t.Fatal("malformed blessed.json must error")
	}
	rep, err := Reconcile(store, d, ReconcileOptions{})
	if err == nil {
		t.Fatal("Reconcile must surface the DesiredSet error")
	}
	if rep.Evicted != 0 {
		t.Errorf("Reconcile must evict NOTHING on a malformed index, got %+v", rep)
	}
	if _, _, ok := ReadBlessed(dir, bdDS, bdURL1); !ok {
		t.Error("the cached entry must survive a malformed index (never evict on uncertainty)")
	}
}

// TestBlessedReconcile_EndToEnd: reconcile keeps the still-blessed entry, evicts
// the one removed from blessed.json, and is idempotent on a second run.
func TestBlessedReconcile_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	store := Store{DataDir: dir, DSDomain: bdDS}
	// blessed.json lists ONLY bdURL1; bdURL2 was denied/withdrawn → already removed
	// from the index by the real-time path or Chaplain, so it is no longer desired.
	if err := metadata.AddBlessedComment(dir, "posts/20260601/p.md", metadata.BlessedComment{URL: bdURL1}); err != nil {
		t.Fatal(err)
	}

	d := blessedDescForTest(validVerify(bdBody1))

	// Pre-seed BOTH in the cache (bdURL2 as a stale entry left after withdrawal).
	Ingest(store, d, bdURL1, IngestOptions{})
	Ingest(store, d, bdURL2, IngestOptions{})

	rep, err := Reconcile(store, d, ReconcileOptions{})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if rep.Evicted != 1 {
		t.Errorf("the entry no longer in blessed.json must be evicted, report=%+v", rep)
	}
	if _, _, ok := ReadBlessed(dir, bdDS, bdURL2); ok {
		t.Error("evicted entry still cached")
	}
	if _, _, ok := ReadBlessed(dir, bdDS, bdURL1); !ok {
		t.Error("still-blessed entry must remain")
	}

	// Idempotent: a second run changes nothing.
	rep2, err := Reconcile(store, d, ReconcileOptions{})
	if err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	if rep2.Evicted != 0 || rep2.Refetched != 0 {
		t.Errorf("second reconcile must be a no-op, got %+v", rep2)
	}
}

// TestBlessedReconcile_KeepsUnreachable: a blessed.json entry whose author is
// unreachable is KEPT — its cached body survives (durability: never evict on
// uncertainty, and a failed refetch is not an eviction).
func TestBlessedReconcile_KeepsUnreachable(t *testing.T) {
	dir := t.TempDir()
	store := Store{DataDir: dir, DSDomain: bdDS}
	if err := metadata.AddBlessedComment(dir, "posts/20260601/p.md", metadata.BlessedComment{URL: bdURL1}); err != nil {
		t.Fatal(err)
	}
	// Cache it while the author is reachable.
	Ingest(store, blessedDescForTest(validVerify(bdBody1)), bdURL1, IngestOptions{})

	// Author now unreachable; reconcile must KEEP the cached entry.
	offline := func(string) (*verify.VerificationResult, error) { return nil, os.ErrDeadlineExceeded }
	rep, err := Reconcile(store, blessedDescForTest(offline), ReconcileOptions{})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if rep.Evicted != 0 {
		t.Errorf("an unreachable-but-still-blessed entry must be kept, got %+v", rep)
	}
	if _, _, ok := ReadBlessed(dir, bdDS, bdURL1); !ok {
		t.Error("cached entry must survive an unreachable author")
	}
}
