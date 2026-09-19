package following

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// A following.json carrying a member this build does not model is refused,
// never rewritten, except by the explicit unsigned escape hatch.

func followWithFutureMember(t *testing.T) (string, []byte, []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "following.json")
	priv, _ := newTestKeys(t)
	if err := SaveSigned(path, sampleFile(), priv); err != nil {
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
	return path, patched, priv
}

func TestAFollowFileWithAnUnknownMemberRefusesEveryRewrite(t *testing.T) {
	for name, write := range map[string]func(path string, f *FollowingFile, priv []byte) error{
		"SaveSigned": func(p string, f *FollowingFile, k []byte) error { return SaveSigned(p, f, k) },
		"Save":       func(p string, f *FollowingFile, _ []byte) error { return Save(p, f) },
	} {
		t.Run(name, func(t *testing.T) {
			path, patched, priv := followWithFutureMember(t)
			f, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			f.Following = f.Following[:len(f.Following)-1] // an unfollow
			err = write(path, f, priv)
			var refusal *signing.RewriteRefusedError
			if !errors.As(err, &refusal) || strings.Join(refusal.Fields, ",") != "following[0].muted_until" {
				t.Fatalf("want a refusal naming following[0].muted_until, got %v", err)
			}
			if after, _ := os.ReadFile(path); !bytes.Equal(after, patched) {
				t.Fatal("a refused write changed following.json")
			}
		})
	}
}

func TestFollowRewriteUnsignedIsTheOnlyWayPast(t *testing.T) {
	path, _, priv := followWithFutureMember(t)
	dropped, err := RewriteUnsigned(path)
	if err != nil || strings.Join(dropped, ",") != "following[0].muted_until" {
		t.Fatalf("RewriteUnsigned = %v, %v", dropped, err)
	}
	f, _ := Load(path)
	if f.Signature != "" {
		t.Fatal("⛔ the escape hatch signed")
	}
	if len(f.UnrecognisedFields()) != 0 {
		t.Fatalf("the escape hatch kept %v", f.UnrecognisedFields())
	}
	if err := SaveSigned(path, f, priv); err != nil {
		t.Fatalf("after the escape hatch, a normal signed write must work: %v", err)
	}
}
