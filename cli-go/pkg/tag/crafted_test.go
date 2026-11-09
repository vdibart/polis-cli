package tag

import (
	"context"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⛔⛔ A TAG FILE IS ONLY EVER WRITTEN BACK TO WHERE IT WAS READ. Writing to the
// name INSIDE the file let a crafted file redirect the write — the review
// reproduced both cases below on the previous code.
func TestACraftedTagFileCannotRedirectAWrite(t *testing.T) {
	ctx := context.Background()
	site := storage.At(t.TempDir())
	st := site.Store()
	priv, _, _ := signing.GenerateKeypair()

	registry := []byte(`{"precious":true}`)
	_ = st.Put(ctx, ".polis/bundles/registry.json", registry)
	// A cloned file whose tag value walks out of the tag directory.
	_ = st.Put(ctx, TagDirKey+"/evil.json", []byte(`{"tag":"../../../.polis/bundles/registry","targets":[],"extra":1}`))

	if _, err := ApplyTag(site, "evil", "https://x.example/p", priv); err == nil {
		t.Error("ApplyTag acted on a file whose inner tag differs from its name")
	}
	if _, err := RemoveTarget(site, "evil", "https://x.example/p", priv); err == nil {
		t.Error("RemoveTarget acted on a file whose inner tag differs from its name")
	}
	if _, err := RewriteUnsigned(site, "evil.json"); err == nil {
		t.Error("RewriteUnsigned acted on a file whose inner tag differs from its name")
	}
	if got, _ := st.Get(ctx, ".polis/bundles/registry.json"); string(got) != string(registry) {
		t.Fatalf("⛔ registry.json was overwritten: %s", got)
	}
}

// The other reproduced case: rewriting one file must never overwrite a valid
// signed SIBLING named by its inner tag.
func TestRewriteUnsignedNeverOverwritesASibling(t *testing.T) {
	ctx := context.Background()
	site := storage.At(t.TempDir())
	priv, _, _ := signing.GenerateKeypair()
	if _, err := ApplyTag(site, "good", "https://x.example/p", priv); err != nil {
		t.Fatal(err)
	}
	before, _ := site.Store().Get(ctx, TagKey("good"))
	_ = site.Store().Put(ctx, TagDirKey+"/other.json", []byte(`{"tag":"good","targets":[],"extra":1}`))
	if _, err := RewriteUnsigned(site, "other.json"); err == nil {
		t.Error("RewriteUnsigned acted on a mismatched file")
	}
	if after, _ := site.Store().Get(ctx, TagKey("good")); string(after) != string(before) {
		t.Error("⛔ a valid signed sibling was overwritten")
	}
}

// ⛔ Names that could leave the tag directory are refused at every entry.
func TestTagNamesThatCouldTraverseAreRefused(t *testing.T) {
	site := storage.At(t.TempDir())
	for _, n := range []string{"../x", "a/b", ".hidden", "UPPER"} {
		if _, err := LoadTag(site, n); err == nil {
			t.Errorf("LoadTag accepted %q", n)
		}
	}
	for _, f := range []string{"../x.json", "a/b.json", ".x.json", "x.txt"} {
		if _, err := RewriteUnsigned(site, f); err == nil {
			t.Errorf("RewriteUnsigned accepted %q", f)
		}
	}
}
