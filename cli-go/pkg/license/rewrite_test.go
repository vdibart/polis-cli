package license

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// A license.json carrying a member this build does not model is refused, never
// rewritten, except by the explicit unsigned escape hatch.

// siteOf and keyOf split a test's licence path into the site and key that
// SignAndWrite and RewriteUnsigned take. ⚠️ The tests keep their PATHS because
// they read the bytes back off disk to check what was written; the API takes a
// site so the production write goes through the store.
func siteOf(path string) storage.Site { return storage.At(filepath.Dir(path)) }
func keyOf(path string) string        { return filepath.Base(path) }

func licenceWithFutureMember(t *testing.T) (string, []byte, []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "license.json")
	priv, _ := testKeys(t)
	f, err := New(ProfileReserved, "https://alice.polis.pub")
	if err != nil {
		t.Fatal(err)
	}
	if err := SignAndWrite(f, siteOf(path), keyOf(path), priv); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var m map[string]interface{}
	_ = json.Unmarshal(raw, &m)
	m["terms"].(map[string]interface{})["ai-input-scope"] = "summaries-only"
	patched, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(path, patched, 0644); err != nil {
		t.Fatal(err)
	}
	return path, patched, priv
}

// Restating terms over a file a newer polis wrote would silently lose a term
// the author stated there, and sign the rest as their whole statement.
func TestALicenceWithAnUnknownMemberRefusesToBeRestated(t *testing.T) {
	path, patched, priv := licenceWithFutureMember(t)
	f, _ := New(ProfileReserved, "https://alice.polis.pub")
	err := SignAndWrite(f, siteOf(path), keyOf(path), priv)
	var refusal *signing.RewriteRefusedError
	if !errors.As(err, &refusal) || strings.Join(refusal.Fields, ",") != "terms.ai-input-scope" {
		t.Fatalf("want a refusal naming terms.ai-input-scope, got %v", err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, patched) {
		t.Fatal("a refused write changed license.json")
	}
}

func TestLicenceRewriteUnsignedIsTheOnlyWayPast(t *testing.T) {
	path, _, priv := licenceWithFutureMember(t)
	dropped, err := RewriteUnsigned(siteOf(path), keyOf(path))
	if err != nil || strings.Join(dropped, ",") != "terms.ai-input-scope" {
		t.Fatalf("RewriteUnsigned = %v, %v", dropped, err)
	}
	f, _ := Load(path)
	if f.Signature != "" {
		t.Fatal("⛔ the escape hatch signed")
	}
	if len(f.UnrecognisedFields()) != 0 || f.Terms == nil {
		t.Fatalf("escape hatch result: unrecognised=%v terms=%v", f.UnrecognisedFields(), f.Terms)
	}
	fresh, _ := New(ProfileReserved, "https://alice.polis.pub")
	if err := SignAndWrite(fresh, siteOf(path), keyOf(path), priv); err != nil {
		t.Fatalf("after the escape hatch, restating terms must work: %v", err)
	}
}
