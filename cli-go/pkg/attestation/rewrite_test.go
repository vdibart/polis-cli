package attestation

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// An attestation record carrying a member this build does not model is refused,
// never rewritten, except by the explicit unsigned escape hatch. A new record never
// meets an unknown member; the rewrite that can is Withdraw's forward reference.

func recordWithFutureMember(t *testing.T) (dir, id string, patched, priv []byte) {
	t.Helper()
	dir = t.TempDir()
	priv, pub := newTestKeys(t)
	writeWellKnown(t, dir, pub)
	id, err := Issue(dir, custodyGrant(), priv)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(Path(dir, id))
	var m map[string]interface{}
	_ = json.Unmarshal(raw, &m)
	m["superseded_by"] = "https://alice.polis.pub/content/pub.polis.core/attestation/x.json"
	patched, _ = json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(Path(dir, id), patched, 0644); err != nil {
		t.Fatal(err)
	}
	return dir, id, patched, priv
}

func TestTheRecordWriterRefusesARecordWithAnUnknownMember(t *testing.T) {
	dir, id, patched, _ := recordWithFutureMember(t)
	r, _ := Load(Path(dir, id))
	r.WithdrawnBy = "https://alice.polis.pub/content/pub.polis.core/attestation/w.json"
	err := write(Path(dir, id), r)
	var refusal *signing.RewriteRefusedError
	if !errors.As(err, &refusal) || strings.Join(refusal.Fields, ",") != "superseded_by" {
		t.Fatalf("want a refusal naming superseded_by, got %v", err)
	}
	if after, _ := os.ReadFile(Path(dir, id)); !bytes.Equal(after, patched) {
		t.Fatal("a refused write changed the record")
	}
}

// ⭐ The withdrawal itself is a NEW record and is still issued; only the
// forward reference onto the unreadable record is skipped, which Withdraw
// already treats as non-fatal discovery rather than proof.
func TestWithdrawingARecordWithAnUnknownMemberLeavesItUntouched(t *testing.T) {
	dir, id, patched, priv := recordWithFutureMember(t)
	wID, err := Withdraw(dir, id, priv)
	if err != nil {
		t.Fatalf("the withdrawal must still be issued: %v", err)
	}
	if _, err := Load(Path(dir, wID)); err != nil {
		t.Fatalf("no withdrawal record on disk: %v", err)
	}
	if after, _ := os.ReadFile(Path(dir, id)); !bytes.Equal(after, patched) {
		t.Fatal("the forward reference was written over a record this build could not read")
	}
}

func TestRecordRewriteUnsignedIsTheOnlyWayPast(t *testing.T) {
	dir, id, _, _ := recordWithFutureMember(t)
	dropped, err := RewriteUnsigned(Path(dir, id))
	if err != nil || strings.Join(dropped, ",") != "superseded_by" {
		t.Fatalf("RewriteUnsigned = %v, %v", dropped, err)
	}
	r, _ := Load(Path(dir, id))
	if r.Signature != "" {
		t.Fatal("⛔ the escape hatch signed")
	}
	if len(r.UnrecognisedFields()) != 0 {
		t.Fatalf("the escape hatch kept %v", r.UnrecognisedFields())
	}
	if ID(r) != id {
		t.Fatalf("the escape hatch moved the record's id: %s != %s", ID(r), id)
	}
}
