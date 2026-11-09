package following

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"os"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// A following.json carrying a member this build does not model is refused,
// never rewritten, except by the explicit unsigned escape hatch.

func followWithFutureMember(t *testing.T) (storage.Site, []byte, []byte) {
	t.Helper()
	site := storage.At(t.TempDir())
	path := site.Path(DefaultKey)
	priv, _ := newTestKeys(t)
	if err := SaveSigned(site, sampleFile(), priv); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var m map[string]interface{}
	_ = json.Unmarshal(raw, &m)
	entries := m["following"].([]interface{})
	entries[0].(map[string]interface{})["muted_until"] = "2027-01-01T00:00:00Z"
	patched, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(path, patched, 0644); err != nil {
		t.Fatal(err)
	}
	return site, patched, priv
}

func TestAFollowFileWithAnUnknownMemberRefusesEveryRewrite(t *testing.T) {
	for name, write := range map[string]func(site storage.Site, f *FollowingFile, priv []byte) error{
		"SaveSigned": func(s storage.Site, f *FollowingFile, k []byte) error { return SaveSigned(s, f, k) },
		"Save":       func(s storage.Site, f *FollowingFile, _ []byte) error { return Save(s, f) },
	} {
		t.Run(name, func(t *testing.T) {
			site, patched, priv := followWithFutureMember(t)
			f, err := LoadSite(site)
			if err != nil {
				t.Fatal(err)
			}
			f.Following = f.Following[:len(f.Following)-1] // an unfollow
			err = write(site, f, priv)
			var refusal *signing.RewriteRefusedError
			if !errors.As(err, &refusal) || strings.Join(refusal.Fields, ",") != "following[0].muted_until" {
				t.Fatalf("want a refusal naming following[0].muted_until, got %v", err)
			}
			if after, _ := os.ReadFile(site.Path(DefaultKey)); !bytes.Equal(after, patched) {
				t.Fatal("a refused write changed following.json")
			}
		})
	}
}

func TestFollowRewriteUnsignedIsTheOnlyWayPast(t *testing.T) {
	site, _, priv := followWithFutureMember(t)
	// ⚠️ RewriteUnsigned still takes a path: it is the explicit escape hatch
	// and never signs, so it was left on the path API for now.
	dropped, err := RewriteUnsigned(site)
	if err != nil || strings.Join(dropped, ",") != "following[0].muted_until" {
		t.Fatalf("RewriteUnsigned = %v, %v", dropped, err)
	}
	f, _ := LoadSite(site)
	if f.Signature != "" {
		t.Fatal("⛔ the escape hatch signed")
	}
	if len(f.UnrecognisedFields()) != 0 {
		t.Fatalf("the escape hatch kept %v", f.UnrecognisedFields())
	}
	if err := SaveSigned(site, f, priv); err != nil {
		t.Fatalf("after the escape hatch, a normal signed write must work: %v", err)
	}
}
