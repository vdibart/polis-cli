package site

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// rotateRecording is rotate, recording the domain the handover was signed
// under as the entry's signing_domain — what both Go rotation paths do.
func rotateRecording(t *testing.T, dir, domain string, from, to keypair, timestamp string) {
	t.Helper()
	canonical, err := discovery.MakeKeyRotationCanonicalJSON(domain, from.pub, to.pub, timestamp)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	sig, err := signing.SignContent(canonical, from.priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := RecordKeyRotation(storage.At(dir), to.pub, sig, timestamp, domain); err != nil {
		t.Fatalf("RecordKeyRotation: %v", err)
	}
}

// ⭐ A chain rotated under one domain and read at another (a branch) verifies
// because each handover names the domain it was signed under — and without
// that field it does not, which is why the field exists.
func TestVerifyChainUsesEachEntrysSigningDomain(t *testing.T) {
	k0, k1 := newKeypair(t), newKeypair(t)
	dir := writeSite(t, k0.pub, "2026-03-03T05:35:09Z")
	rotateRecording(t, dir, "a.example", k0, k1, "2026-09-15T10:22:03Z")

	block := mustLoad(t, dir)
	if got := block.Current.SigningDomain; got != "a.example" {
		t.Fatalf("signing_domain = %q, want a.example", got)
	}
	if err := VerifyChain(block, "b.example"); err != nil {
		t.Errorf("a chain read at b.example whose handover names a.example must verify: %v", err)
	}
	if err := VerifyChain(block, "a.example"); err != nil {
		t.Errorf("and it still verifies where it was signed: %v", err)
	}
	// An entry that names its own domain needs none from the caller.
	if err := VerifyChain(block, ""); err != nil {
		t.Errorf("every handover names its domain, so no caller domain is needed: %v", err)
	}

	block.Current.SigningDomain = ""
	if err := VerifyChain(block, "b.example"); err == nil {
		t.Error("⛔ without signing_domain, a handover signed at a.example must not verify when read at b.example")
	}
}

// A wrong signing_domain is not a way past the check: the message is rebuilt
// with it, and the predecessor never signed that message.
func TestVerifyChainRejectsAWrongSigningDomain(t *testing.T) {
	k0, k1 := newKeypair(t), newKeypair(t)
	dir := writeSite(t, k0.pub, "2026-03-03T05:35:09Z")
	rotateRecording(t, dir, "a.example", k0, k1, "2026-09-15T10:22:03Z")

	block := mustLoad(t, dir)
	block.Current.SigningDomain = "c.example"
	if err := VerifyChain(block, "a.example"); err == nil {
		t.Error("⛔ a signing_domain the handover was not signed under must fail, even when read at the true domain")
	}
}

// A mixed chain: an entry written before signing_domain existed (absent, read
// as the domain the chain is read from) followed by one that names its domain.
func TestVerifyChainAcceptsAMixedChain(t *testing.T) {
	k0, k1, k2 := newKeypair(t), newKeypair(t), newKeypair(t)
	dir := writeSite(t, k0.pub, "2026-03-03T05:35:09Z")
	rotate(t, dir, "b.example", k0, k1, "2026-09-15T10:22:03Z") // legacy: absent
	rotateRecording(t, dir, "a.example", k1, k2, "2026-10-01T00:00:00Z")

	block := mustLoad(t, dir)
	if block.History[1].SigningDomain != "" {
		t.Fatalf("the legacy entry must carry no signing_domain, got %q", block.History[1].SigningDomain)
	}
	if err := VerifyChain(block, "b.example"); err != nil {
		t.Errorf("legacy entry read at its own domain plus a named one must verify: %v", err)
	}
	if err := VerifyChain(block, "a.example"); err == nil {
		t.Error("the legacy entry was signed at b.example, so reading the chain at a.example must fail on it")
	}
	if err := VerifyChain(block, ""); err == nil {
		t.Error("the legacy entry names no domain, so an unknown caller domain must still be refused")
	}
}

// ⛔ Every chain written before this field is byte-identical: an absent
// signing_domain marshals as nothing, genesis never carries one, and a value
// round-trips through decode and encode.
func TestSigningDomainIsAbsentWhenEmptyAndRoundTrips(t *testing.T) {
	data, err := json.Marshal(GenesisKeyHistory("ssh-ed25519 AAAA", "2026-03-03T05:35:09Z"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "signing_domain") {
		t.Errorf("genesis must carry no signing_domain: %s", data)
	}

	sig := "sig"
	e := KeyHistoryEntry{Epoch: 1, Key: "k", ValidFrom: "2026-09-15T10:22:03Z", TransitionSig: &sig}
	plain, _ := json.Marshal(e)
	if strings.Contains(string(plain), "signing_domain") {
		t.Errorf("an absent signing_domain must not appear on the wire: %s", plain)
	}

	e.SigningDomain = "a.example"
	named, _ := json.Marshal(e)
	want := `{"epoch":1,"key":"k","valid_from":"2026-09-15T10:22:03Z","transition_sig":"sig","signing_domain":"a.example"}`
	if string(named) != want {
		t.Errorf("signing_domain sits after transition_sig and witnesses:\n got %s\nwant %s", named, want)
	}
	var back KeyHistoryEntry
	if err := json.Unmarshal(named, &back); err != nil {
		t.Fatal(err)
	}
	if back.SigningDomain != "a.example" || len(back.Extra) != 0 {
		t.Errorf("round trip lost the field or pushed it into Extra: %+v", back)
	}
	again, _ := json.Marshal(back)
	if string(again) != want {
		t.Errorf("round trip changed the bytes:\n got %s\nwant %s", again, want)
	}
}
