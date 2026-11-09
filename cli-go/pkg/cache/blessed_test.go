package cache

import (
	"encoding/json"
	"errors"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testDS     = "ds.polis.pub"
	canonURL   = "https://alice.polis.pub/content/pub.polis.core/comment/20260601/reply-20260601.md"
	mountURL   = "https://alice.polis.pub/comments/20260601/reply-20260601.md"
	wantSuffix = "alice.polis.pub/20260601/reply-20260601.md"
)

func TestBlessedKeys_BothURLForms_SameLocation(t *testing.T) {
	mdC, metaC, okC := blessedKeys(testDS, canonURL)
	mdM, _, okM := blessedKeys(testDS, mountURL)
	if !okC || !okM {
		t.Fatalf("expected both URL forms to derive paths (canon=%v mount=%v)", okC, okM)
	}
	if mdC != mdM {
		t.Errorf("canonical and mount URLs must map to the SAME cache path:\n  %s\n  %s", mdC, mdM)
	}
	// Lives under the DS-scoped blessed cache, keyed by author domain.
	wantPrefix := ".polis/ds/" + testDS + "/pub.polis.core/cache/blessed/"
	if !strings.HasPrefix(mdC, wantPrefix) {
		t.Errorf("md path %q not under blessed cache root %q", mdC, wantPrefix)
	}
	if !strings.HasSuffix(mdC, wantSuffix) {
		t.Errorf("md path %q does not end with %q", mdC, wantSuffix)
	}
	if metaC != mdC+".meta.json" {
		t.Errorf("sidecar path = %q, want %q", metaC, mdC+".meta.json")
	}
	// NOT under the canonical content tree (the whole point of Defect 3).
	if strings.Contains(mdC, "content/pub.polis.core/comment") {
		t.Errorf("blessed mirror must NOT live under content/: %q", mdC)
	}
}

func TestBlessedKeys_RejectsNonComment(t *testing.T) {
	if _, _, ok := blessedKeys(testDS, "https://alice.polis.pub/posts/20260601/x.md"); ok {
		t.Error("a post URL must not derive a blessed-comment cache path")
	}
	if _, _, ok := blessedKeys(testDS, "/comments/20260601/x.md"); ok {
		t.Error("a hostless URL must not derive a cache path (no author domain)")
	}
}

func TestStoreReadEvictBlessed(t *testing.T) {
	dir := t.TempDir()
	sc := BlessedSidecar{
		SourceURL:          canonURL,
		BlessedVersionHash: "sha256:deadbeef",
		Signature:          "SSHSIG...",
		FetchedAt:          "2026-06-01T15:04:05Z",
		VerifiedAt:         "2026-06-01T15:04:05Z",
		EventID:            "evt-123",
	}
	body := []byte("---\ntype: comment\nauthor: alice.polis.pub\n---\nhi there")

	if err := StoreBlessed(storage.At(dir), testDS, sc, body); err != nil {
		t.Fatalf("StoreBlessed: %v", err)
	}

	// Read back via the OTHER URL form (mount) to confirm form-tolerance.
	gotBody, gotSC, ok := ReadBlessed(storage.At(dir), testDS, mountURL)
	if !ok {
		t.Fatal("ReadBlessed returned ok=false after store")
	}
	if string(gotBody) != string(body) {
		t.Errorf("body roundtrip mismatch: got %q", gotBody)
	}
	if gotSC.Kind != "blessed" {
		t.Errorf("sidecar kind = %q, want blessed", gotSC.Kind)
	}
	if gotSC.AuthorDomain != "alice.polis.pub" {
		t.Errorf("sidecar author_domain = %q, want alice.polis.pub (derived)", gotSC.AuthorDomain)
	}
	if gotSC.BlessedVersionHash != "sha256:deadbeef" || gotSC.FetchedAt == "" {
		t.Errorf("sidecar fields not persisted: %+v", gotSC)
	}

	// The sidecar is valid JSON on disk.
	mdKey, metaKey, _ := blessedKeys(testDS, canonURL)
	mdPath, metaPath := filepath.Join(dir, filepath.FromSlash(mdKey)), filepath.Join(dir, filepath.FromSlash(metaKey))
	raw, _ := os.ReadFile(metaPath)
	var check map[string]any
	if err := json.Unmarshal(raw, &check); err != nil {
		t.Errorf("sidecar is not valid JSON: %v", err)
	}

	// Evict removes both files; idempotent on repeat.
	if err := EvictBlessed(storage.At(dir), testDS, mountURL); err != nil {
		t.Fatalf("EvictBlessed: %v", err)
	}
	if _, err := os.Stat(mdPath); !errors.Is(err, fs.ErrNotExist) {
		t.Error("md file still present after evict")
	}
	if _, err := os.Stat(metaPath); !errors.Is(err, fs.ErrNotExist) {
		t.Error("sidecar still present after evict")
	}
	if err := EvictBlessed(storage.At(dir), testDS, canonURL); err != nil {
		t.Errorf("evict must be idempotent on a missing entry, got %v", err)
	}
	if _, _, ok := ReadBlessed(storage.At(dir), testDS, canonURL); ok {
		t.Error("ReadBlessed must return ok=false after evict")
	}
}

func TestFindBlessed_GlobAcrossDS(t *testing.T) {
	dir := t.TempDir()
	sc := BlessedSidecar{SourceURL: canonURL, BlessedVersionHash: "sha256:abc", FetchedAt: "t", VerifiedAt: "t"}
	if err := StoreBlessed(storage.At(dir), testDS, sc, []byte("body!")); err != nil {
		t.Fatalf("store: %v", err)
	}
	// FindBlessed locates it WITHOUT knowing the DS domain, via either URL form.
	if body, got, ok := FindBlessed(storage.At(dir), mountURL); !ok || string(body) != "body!" || got.AuthorDomain != "alice.polis.pub" {
		t.Errorf("FindBlessed(storage.At(mount)) = %q, %+v, ok=%v", body, got, ok)
	}
	// EvictBlessedAnyDS removes it; FindBlessed then misses.
	if err := EvictBlessedAnyDS(storage.At(dir), canonURL); err != nil {
		t.Fatalf("evict any-DS: %v", err)
	}
	if _, _, ok := FindBlessed(storage.At(dir), canonURL); ok {
		t.Error("FindBlessed must miss after EvictBlessedAnyDS")
	}
}

// TestBlessedCacheAbsence pins what each reader returns when nothing is
// cached, including when the site has no .polis/ds at all. ⚠️ These were
// filesystem reads, a glob and a walk before they went through the store; each
// treated absence as "not cached" (or an empty list) and never as an error, and
// the callers rely on it: render falls back to a fetch, GC and Reconcile treat
// an empty list as an empty cache.
func TestBlessedCacheAbsence(t *testing.T) {
	site := storage.At(t.TempDir())
	if _, _, ok := ReadBlessed(site, testDS, canonURL); ok {
		t.Error("ReadBlessed on an empty site reported a cached entry")
	}
	if _, _, ok := FindBlessed(site, canonURL); ok {
		t.Error("FindBlessed with no .polis/ds reported a cached entry")
	}
	if err := EvictBlessed(site, testDS, canonURL); err != nil {
		t.Errorf("EvictBlessed of an absent entry is not an error, got %v", err)
	}
	if err := EvictBlessedAnyDS(site, canonURL); err != nil {
		t.Errorf("EvictBlessedAnyDS of an absent entry is not an error, got %v", err)
	}
	items, err := NewBlessedDescriptor(DescriptorConfig{}).List(Store{Site: site, DSDomain: testDS})
	if err != nil || len(items) != 0 {
		t.Errorf("List of an absent cache = %v, %v; want empty and no error", items, err)
	}

	// A DS scope that exists but holds no entry for this comment is still a miss.
	if err := StoreBlessed(site, "other.ds", BlessedSidecar{SourceURL: "https://bob.polis.pub/comments/20260601/x.md"}, []byte("b")); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := FindBlessed(site, canonURL); ok {
		t.Error("FindBlessed found an entry that was never stored")
	}
}
