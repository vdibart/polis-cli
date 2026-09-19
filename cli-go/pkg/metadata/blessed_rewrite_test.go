package metadata

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// A blessing list carrying a member this
// build does not declare is never rewritten — signed or unsigned — except by
// the explicit, unsigned escape hatch.

func blessedWithFutureMember(t *testing.T) (string, []byte, []byte) {
	t.Helper()
	dir := t.TempDir()
	priv, _ := newTestKeys(t)
	if err := AddBlessedCommentSigned(dir, "posts/a.md", BlessedComment{URL: "https://bob.example/c/1.md"}, priv); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(blessedPath(dir))
	var m map[string]interface{}
	_ = json.Unmarshal(raw, &m)
	m["com.example.future"] = "a newer writer said this"
	patched, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(blessedPath(dir), patched, 0644); err != nil {
		t.Fatal(err)
	}
	return dir, patched, priv
}

func TestABlessingListWithAnUnknownMemberRefusesEveryRewrite(t *testing.T) {
	writes := map[string]func(dir string, priv []byte) error{
		"AddBlessedCommentSigned": func(d string, k []byte) error {
			return AddBlessedCommentSigned(d, "posts/a.md", BlessedComment{URL: "https://carol.example/c/2.md"}, k)
		},
		"AddBlessedComment (unsigned)": func(d string, _ []byte) error {
			return AddBlessedComment(d, "posts/a.md", BlessedComment{URL: "https://carol.example/c/2.md"})
		},
		"RemoveBlessedCommentSigned": func(d string, k []byte) error {
			return RemoveBlessedCommentSigned(d, "https://bob.example/c/1.md", k)
		},
		"SaveBlessedCommentsSigned(fresh list)": func(d string, k []byte) error {
			return SaveBlessedCommentsSigned(d, sampleBlessed(), k)
		},
	}
	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			dir, patched, priv := blessedWithFutureMember(t)
			err := write(dir, priv)
			var refusal *signing.RewriteRefusedError
			if !errors.As(err, &refusal) || strings.Join(refusal.Fields, ",") != "com.example.future" {
				t.Fatalf("want a refusal naming com.example.future, got %v", err)
			}
			if after, _ := os.ReadFile(blessedPath(dir)); !bytes.Equal(after, patched) {
				t.Fatal("a refused write changed blessed.json — the member was dropped")
			}
		})
	}
}

func TestRewriteBlessedUnsignedIsTheOnlyWayPast(t *testing.T) {
	dir, _, priv := blessedWithFutureMember(t)
	dropped, err := RewriteBlessedUnsigned(dir)
	if err != nil || strings.Join(dropped, ",") != "com.example.future" {
		t.Fatalf("RewriteBlessedUnsigned = %v, %v", dropped, err)
	}
	after, _ := os.ReadFile(blessedPath(dir))
	if bytes.Contains(after, []byte("com.example.future")) {
		t.Fatal("the escape hatch kept the member it reported dropping")
	}
	bc, _ := LoadBlessedComments(dir)
	if bc.Signature != "" {
		t.Fatal("⛔ the escape hatch signed a list it rewrote because it could not read it")
	}
	if len(bc.Comments) != 1 || bc.Comments[0].Blessed[0].URL != "https://bob.example/c/1.md" {
		t.Fatalf("the escape hatch lost modelled content: %+v", bc.Comments)
	}
	// Nothing unreadable remains, so an ordinary signed write proceeds.
	if err := AddBlessedCommentSigned(dir, "posts/a.md", BlessedComment{URL: "https://carol.example/c/2.md"}, priv); err != nil {
		t.Fatalf("after the escape hatch, a normal bless must work: %v", err)
	}
	if again, err := RewriteBlessedUnsigned(dir); err != nil || again != nil {
		t.Fatalf("a list with nothing unrecognised must be left alone: %v, %v", again, err)
	}
}
