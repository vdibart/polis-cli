package site

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

func witnessOf(url, version, at string) discovery.Witness {
	return discovery.Witness{
		Action:      discovery.WitnessActionContent,
		Type:        "pub.polis.post",
		URL:         url,
		Version:     version,
		Author:      "alice.polis.pub",
		Actor:       "alice.polis.pub",
		PublicKey:   "ssh-ed25519 AAAA",
		DS:          "https://ds.polis.pub",
		DSKeyID:     "ds-primary",
		WitnessedAt: at,
		Signature:   "sig",
	}
}

const postURL = "https://alice.polis.pub/content/pub.polis.core/post/20260913/hello.md"

func TestRecordWitnessPublishesTheFileAndItsPointer(t *testing.T) {
	dir := writeSite(t, newKeypair(t).pub, "2026-01-01T00:00:00Z")
	if got := WitnessesPointer(dir); got != "" {
		t.Fatalf("a fresh site publishes no witnesses pointer, got %q", got)
	}
	if f, err := LoadWitnesses(dir); err != nil || f != nil {
		t.Fatalf("no pointer must read as no witness file, not an error: %v %v", f, err)
	}

	changed, err := RecordWitness(dir, postURL, witnessOf(postURL, "sha256:v1", "2026-09-13T12:00:00.000Z"))
	if err != nil || !changed {
		t.Fatalf("RecordWitness: changed=%v err=%v", changed, err)
	}
	if got := WitnessesPointer(dir); got != DefaultWitnessesPointer {
		t.Fatalf("pointer = %q, want %q", got, DefaultWitnessesPointer)
	}
	f, err := LoadWitnesses(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.For(postURL)) != 1 {
		t.Fatalf("expected one witness for the post, got %+v", f.Witnesses)
	}
	// Other identity fields survive the pointer write.
	raw, _ := LoadWellKnownRaw(dir)
	if raw["public_key_messages"] == nil {
		t.Fatal("setting the witnesses pointer dropped an unmodelled .well-known/polis field")
	}
}

// TestRecordWitnessNeverRemovesASupersededVersion is the property the DS
// cannot provide: it holds only the CURRENT version's witness, so the site's
// published file is the only copy of every earlier one.
func TestRecordWitnessNeverRemovesASupersededVersion(t *testing.T) {
	dir := writeSite(t, newKeypair(t).pub, "2026-01-01T00:00:00Z")
	if _, err := RecordWitness(dir, postURL, witnessOf(postURL, "sha256:v1", "2026-09-13T12:00:00.000Z")); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordWitness(dir, postURL, witnessOf(postURL, "sha256:v2", "2026-09-14T12:00:00.000Z")); err != nil {
		t.Fatal(err)
	}
	f, _ := LoadWitnesses(dir)
	set := f.For(postURL)
	if len(set) != 2 || set[0].Version != "sha256:v1" || set[1].Version != "sha256:v2" {
		t.Fatalf("the v1 witness must survive v2's registration, in witnessed order: %+v", set)
	}

	// Re-registering v2 later (Chaplain's repair sweep) adds nothing.
	changed, err := RecordWitness(dir, postURL, witnessOf(postURL, "sha256:v2", "2026-09-20T00:00:00.000Z"))
	if err != nil || changed {
		t.Fatalf("a later witness of the same bytes must change nothing: changed=%v err=%v", changed, err)
	}
}

func TestRecordWitnessRefusesToOverwriteAFileItCannotRead(t *testing.T) {
	dir := writeSite(t, newKeypair(t).pub, "2026-01-01T00:00:00Z")
	if _, err := RecordWitness(dir, postURL, witnessOf(postURL, "sha256:v1", "2026-09-13T12:00:00.000Z")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, strings.TrimPrefix(DefaultWitnessesPointer, "/"))
	if err := os.WriteFile(path, []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordWitness(dir, postURL, witnessOf(postURL, "sha256:v2", "2026-09-14T12:00:00.000Z")); err == nil {
		t.Fatal("an unparseable witness file may hold the only copy of old testimony and must never be replaced")
	}
	if data, _ := os.ReadFile(path); string(data) != "{not json" {
		t.Fatalf("the unreadable file was modified: %q", data)
	}
}

func TestAWitnessesPointerCannotEscapeTheSite(t *testing.T) {
	dir := writeSite(t, newKeypair(t).pub, "2026-01-01T00:00:00Z")
	if err := setWellKnownPointer(dir, witnessesPointerField, "/../../etc/witnesses.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWitnesses(dir); err == nil {
		t.Fatal("a pointer outside the site must be refused")
	}
	if _, err := RecordWitness(dir, postURL, witnessOf(postURL, "sha256:v1", "2026-09-13T12:00:00.000Z")); err == nil {
		t.Fatal("a pointer outside the site must not be written through")
	}
}

func TestAWitnessedRotationCarriesItsWitnessAndTheChainStillVerifies(t *testing.T) {
	k0, k1 := newKeypair(t), newKeypair(t)
	dir := writeSite(t, k0.pub, "2026-03-03T05:35:09Z")
	ts := "2026-09-13T12:00:00Z"

	canonical, _ := discovery.MakeKeyRotationCanonicalJSON("alice.polis.pub", k0.pub, k1.pub, ts)
	sig, err := signing.SignContent(canonical, k0.priv)
	if err != nil {
		t.Fatal(err)
	}
	w := discovery.Witness{
		Action: discovery.WitnessActionKeyRotation, Domain: "alice.polis.pub",
		OldKey: k0.pub, NewKey: k1.pub, Timestamp: ts, TransitionSig: sig,
		DS: "https://ds.polis.pub", DSKeyID: "ds-primary", WitnessedAt: "2026-09-13T12:00:00.321Z", Signature: "ds-sig",
	}
	if err := RecordKeyRotation(dir, k1.pub, sig, ts, w); err != nil {
		t.Fatal(err)
	}
	block := mustLoad(t, dir)
	if len(block.Current.Witnesses) != 1 || block.Current.Witnesses[0].WitnessedAt != w.WitnessedAt {
		t.Fatalf("the rotation's witness must ride in the new entry: %+v", block.Current)
	}
	if len(block.History[0].Witnesses) != 0 {
		t.Fatal("genesis was witnessed by nothing and must say so")
	}
	if err := VerifyChain(block, "alice.polis.pub"); err != nil {
		t.Fatalf("carrying a witness must not change what the chain signs: %v", err)
	}
}

func TestAnUnwitnessedChainPublishesNoWitnessesKey(t *testing.T) {
	k0, k1 := newKeypair(t), newKeypair(t)
	dir := writeSite(t, k0.pub, "2026-03-03T05:35:09Z")
	rotate(t, dir, "alice.polis.pub", k0, k1, "2026-09-13T12:00:00Z")
	data, err := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"witnesses"`) {
		t.Fatalf("a chain with no witnesses must publish a byte-identical shape — no witnesses key:\n%s", data)
	}
}
