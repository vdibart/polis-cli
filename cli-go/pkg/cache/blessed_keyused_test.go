package cache

import (
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/sitecheck"
	"github.com/vdibart/polis-cli/cli-go/pkg/verify"
)

// The cache records WHICH of the author's keys verified a comment: the
// current one, or a retired one resolved through the author's published key
// history. They are claims of different strength (epic 31 D4), and flattening
// both to "valid" hid the difference from anyone reading the cache.

func verifyWithKey(k *sitecheck.KeyUsed) func(string) (*verify.VerificationResult, error) {
	return func(url string) (*verify.VerificationResult, error) {
		return &verify.VerificationResult{
			URL:            url,
			CurrentVersion: "sha256:pinnedhash",
			Signature:      verify.SignatureResult{Status: "valid", Key: k},
			Body:           bdBody1,
		}, nil
	}
}

func TestBlessedIngest_RecordsTheCurrentKey(t *testing.T) {
	dir := t.TempDir()
	epoch := 0
	d := blessedDescForTest(verifyWithKey(&sitecheck.KeyUsed{Source: sitecheck.KeyCurrent, Epoch: &epoch}))
	if res := Ingest(Store{Site: storage.At(dir), DSDomain: bdDS}, d, bdURL1, IngestOptions{}); !res.Stored {
		t.Fatalf("not stored: %+v", res)
	}
	_, bsc, ok := ReadBlessed(storage.At(dir), bdDS, bdURL1)
	if !ok || bsc.KeyUsed == nil || bsc.KeyUsed.Source != sitecheck.KeyCurrent {
		t.Fatalf("key_used = %+v, want source current", bsc.KeyUsed)
	}
	if got := blessedToSidecar(bsc).KeyUsed; got == nil || got.Source != sitecheck.KeyCurrent {
		t.Errorf("the generic sidecar lost key_used: %+v", got)
	}
}

func TestBlessedIngest_RecordsARetiredKey(t *testing.T) {
	dir := t.TempDir()
	epoch := 0
	d := blessedDescForTest(verifyWithKey(&sitecheck.KeyUsed{
		Source: sitecheck.KeyRetired, Epoch: &epoch, ClaimedSigningTime: "2026-05-01T09:00:00Z",
	}))
	if res := Ingest(Store{Site: storage.At(dir), DSDomain: bdDS}, d, bdURL1, IngestOptions{}); !res.Stored {
		t.Fatalf("not stored: %+v", res)
	}
	_, bsc, _ := ReadBlessed(storage.At(dir), bdDS, bdURL1)
	if bsc.KeyUsed == nil || bsc.KeyUsed.Source != sitecheck.KeyRetired || bsc.KeyUsed.ClaimedSigningTime != "2026-05-01T09:00:00Z" {
		t.Fatalf("key_used = %+v, want the retired key and its claimed signing time", bsc.KeyUsed)
	}
}

// An unverified entry records no key, and a sidecar written before key_used
// existed still loads.
func TestBlessedSidecar_KeyUsedIsOptional(t *testing.T) {
	dir := t.TempDir()
	d := blessedDescForTest(func(url string) (*verify.VerificationResult, error) {
		return &verify.VerificationResult{CurrentVersion: "sha256:x", Signature: verify.SignatureResult{Status: "missing"}, Body: "legacy"}, nil
	})
	if res := Ingest(Store{Site: storage.At(dir), DSDomain: bdDS}, d, bdURL1, IngestOptions{}); !res.Stored {
		t.Fatalf("not stored: %+v", res)
	}
	_, bsc, ok := ReadBlessed(storage.At(dir), bdDS, bdURL1)
	if !ok || bsc.KeyUsed != nil {
		t.Fatalf("an unverified entry recorded key_used %+v", bsc.KeyUsed)
	}

	// Find the sidecar on disk and confirm the field is absent, which is also
	// exactly what an old sidecar looks like.
	var sidecar string
	filepath.WalkDir(dir, func(p string, e os.DirEntry, err error) error {
		if err == nil && !e.IsDir() && strings.HasSuffix(p, ".json") {
			sidecar = p
		}
		return nil
	})
	raw, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "key_used") {
		t.Errorf("sidecar carries key_used when it has none:\n%s", raw)
	}
}
