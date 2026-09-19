package actor

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// An actor registry carrying a member this build does not model is refused,
// never rewritten, except by the explicit unsigned escape hatch.

func registryWithFutureMember(t *testing.T) (string, []byte, []byte) {
	t.Helper()
	opPriv, _ := keypair(t)
	path := DefaultPath(t.TempDir())
	r := vectorRegistry()
	if err := SignAndWrite(r, path, opPriv); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var m map[string]interface{}
	_ = json.Unmarshal(raw, &m)
	m["actors"].([]interface{})[0].(map[string]interface{})["region"] = "iad"
	patched, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(path, patched, 0644); err != nil {
		t.Fatal(err)
	}
	return path, patched, opPriv
}

func TestARegistryWithAnUnknownMemberRefusesToBeRewritten(t *testing.T) {
	path, patched, opPriv := registryWithFutureMember(t)
	r, _ := Load(path)
	r.Asserted = "2026-09-16T00:00:00Z"
	err := SignAndWrite(r, path, opPriv)
	var refusal *signing.RewriteRefusedError
	if !errors.As(err, &refusal) || strings.Join(refusal.Fields, ",") != "actors[0].region" {
		t.Fatalf("want a refusal naming actors[0].region, got %v", err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, patched) {
		t.Fatal("a refused write changed the registry")
	}
}

func TestRegistryRewriteUnsignedIsTheOnlyWayPast(t *testing.T) {
	path, _, opPriv := registryWithFutureMember(t)
	dropped, err := RewriteUnsigned(path)
	if err != nil || strings.Join(dropped, ",") != "actors[0].region" {
		t.Fatalf("RewriteUnsigned = %v, %v", dropped, err)
	}
	r, _ := Load(path)
	if r.Signature != "" {
		t.Fatal("⛔ the escape hatch signed")
	}
	if len(r.UnrecognisedFields()) != 0 || len(r.Actors) != 1 {
		t.Fatalf("escape hatch result: unrecognised=%v actors=%v", r.UnrecognisedFields(), r.Actors)
	}
	if err := SignAndWrite(r, path, opPriv); err != nil {
		t.Fatalf("after the escape hatch, a normal signed write must work: %v", err)
	}
}
