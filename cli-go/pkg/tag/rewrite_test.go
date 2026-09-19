package tag

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// A tag file carrying a member this build does not model is refused, never
// rewritten, except by the explicit unsigned escape hatch.

func tagWithFutureMember(t *testing.T) (string, string, []byte, []byte) {
	t.Helper()
	dir := t.TempDir()
	priv, _ := generateTestKey(t)
	if _, err := ApplyTag(dir, "reading", "https://alice.example/p/1.md", priv); err != nil {
		t.Fatal(err)
	}
	path := TagPath(dir, "reading")
	raw, _ := os.ReadFile(path)
	var m map[string]interface{}
	_ = json.Unmarshal(raw, &m)
	m["colour"] = "#ff0000"
	patched, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(path, patched, 0644); err != nil {
		t.Fatal(err)
	}
	return dir, path, patched, priv
}

func TestATagFileWithAnUnknownMemberRefusesEveryRewrite(t *testing.T) {
	for name, write := range map[string]func(dir string, priv []byte) error{
		"ApplyTag": func(d string, k []byte) error {
			_, err := ApplyTag(d, "reading", "https://bob.example/p/2.md", k)
			return err
		},
		"RemoveTarget": func(d string, k []byte) error {
			_, err := RemoveTarget(d, "reading", "https://alice.example/p/1.md", k)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir, path, patched, priv := tagWithFutureMember(t)
			err := write(dir, priv)
			var refusal *signing.RewriteRefusedError
			if !errors.As(err, &refusal) || strings.Join(refusal.Fields, ",") != "colour" {
				t.Fatalf("want a refusal naming colour, got %v", err)
			}
			if after, _ := os.ReadFile(path); !bytes.Equal(after, patched) {
				t.Fatal("a refused write changed the tag file")
			}
		})
	}
}

func TestTagRewriteUnsignedIsTheOnlyWayPast(t *testing.T) {
	dir, path, _, priv := tagWithFutureMember(t)
	dropped, err := RewriteUnsigned(dir, path)
	if err != nil || strings.Join(dropped, ",") != "colour" {
		t.Fatalf("RewriteUnsigned = %v, %v", dropped, err)
	}
	tf, _ := Load(path)
	if tf.Signature != "" {
		t.Fatal("⛔ the escape hatch signed")
	}
	if len(tf.UnrecognisedFields()) != 0 || len(tf.Targets) != 1 {
		t.Fatalf("escape hatch result: unrecognised=%v targets=%v", tf.UnrecognisedFields(), tf.Targets)
	}
	canonical, _ := CanonicalJSON(tf)
	sum := sha256.Sum256(canonical)
	if tf.Version != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatal("current_version was not recomputed over the rewritten bytes")
	}
	if _, err := ApplyTag(dir, "reading", "https://bob.example/p/2.md", priv); err != nil {
		t.Fatalf("after the escape hatch, a normal tag write must work: %v", err)
	}
}
