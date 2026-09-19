package tag_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/tag"
)

// SIGNET epic 44, E5-wide — a tag file signed before a rotation verifies after
// it, resolved by its claimed signing time `updated`. The rotation goes through
// site.RecordKeyRotation, the seam every rotation path calls.

func TestATagFileSignedBeforeARotationVerifiesAfterIt(t *testing.T) {
	const domain = "alice.polis.pub"
	k0priv, k0pub := keypair(t)
	_, k1pub := keypair(t)

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".well-known"), 0755); err != nil {
		t.Fatal(err)
	}
	wk, _ := json.Marshal(map[string]string{"public_key": k0pub, "created": "2026-03-03T05:35:09Z"})
	if err := os.WriteFile(filepath.Join(dir, ".well-known", "polis"), wk, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := site.WriteGenesisKeyHistory(dir); err != nil {
		t.Fatal(err)
	}
	canonical, _ := discovery.MakeKeyRotationCanonicalJSON(domain, k0pub, k1pub, "2026-09-15T10:22:03Z")
	sig, err := signing.SignContent(canonical, k0priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := site.RecordKeyRotation(dir, k1pub, sig, "2026-09-15T10:22:03Z"); err != nil {
		t.Fatal(err)
	}
	chain, _ := site.LoadKeyHistory(dir)
	if err := site.VerifyChain(chain, domain); err != nil {
		t.Fatalf("setup: chain must verify: %v", err)
	}

	sign := func(priv []byte, updated string) *tag.TagFile {
		tf := &tag.TagFile{Tag: "essays", Created: "2026-04-01T00:00:00Z", Updated: updated, Generator: "polis-cli-go/test",
			Targets: []tag.TagTarget{{URI: "https://alice.polis.pub/content/pub.polis.core/post/20260401/a.md"}}}
		c, err := tag.CanonicalJSON(tf)
		if err != nil {
			t.Fatal(err)
		}
		if tf.Signature, err = signing.SignContent(c, priv); err != nil {
			t.Fatal(err)
		}
		return tf
	}

	old := sign(k0priv, "2026-05-01T09:00:00Z")
	if s, _ := tag.Verify(old, []byte(k1pub)); s != tag.StatusInvalid {
		t.Fatalf("setup: the current key must reject the old tag file, got %s", s)
	}
	s, retired, err := tag.VerifyWithHistory(old, []byte(k1pub), chain)
	if s != tag.StatusValid || retired == nil || retired.Epoch != 0 {
		t.Fatalf("a tag file signed before the rotation must verify against the retired key: %s %+v %v", s, retired, err)
	}

	// And its window holds.
	late := sign(k0priv, "2026-10-01T00:00:00Z")
	if s, _, err := tag.VerifyWithHistory(late, []byte(k1pub), chain); s != tag.StatusInvalid || !strings.Contains(errString(err), "current key's window") {
		t.Fatalf("a retired key claiming a later time must fail: %s %v", s, err)
	}
}

func keypair(t *testing.T) ([]byte, string) {
	t.Helper()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	return priv, strings.TrimSpace(string(pub))
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
