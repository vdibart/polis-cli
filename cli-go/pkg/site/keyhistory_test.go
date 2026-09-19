package site

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// ---------- helpers ----------

type keypair struct {
	priv []byte
	pub  string
}

func newKeypair(t *testing.T) keypair {
	t.Helper()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	return keypair{priv: priv, pub: strings.TrimSpace(string(pub))}
}

// writeSite lays down the minimum .well-known/polis a key-history caller needs.
func writeSite(t *testing.T, pubKey, created string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".well-known"), 0755); err != nil {
		t.Fatal(err)
	}
	wk := map[string]interface{}{
		"version":     "test",
		"author_name": "Alice",
		"public_key":  pubKey,
		"created":     created,
		// An unmodelled field, present to prove every write preserves it.
		"public_key_messages": map[string]interface{}{"current": map[string]interface{}{"epoch": 0}},
	}
	data, _ := json.MarshalIndent(wk, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, ".well-known", "polis"), append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// rotate performs one rotation on the site, signing the transition with `from`.
func rotate(t *testing.T, dir, domain string, from keypair, to keypair, timestamp string) {
	t.Helper()
	canonical, err := discovery.MakeKeyRotationCanonicalJSON(domain, from.pub, to.pub, timestamp)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	sig, err := signing.SignContent(canonical, from.priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := RecordKeyRotation(dir, to.pub, sig, timestamp); err != nil {
		t.Fatalf("RecordKeyRotation: %v", err)
	}
}

func mustLoad(t *testing.T, dir string) *KeyHistoryBlock {
	t.Helper()
	b, err := LoadKeyHistory(dir)
	if err != nil {
		t.Fatalf("LoadKeyHistory: %v", err)
	}
	if b == nil {
		t.Fatal("expected a key history, got none")
	}
	return b
}

// ---------- shape ----------

func TestGenesisMarshalsHistoryAsEmptyArrayAndSigAsNull(t *testing.T) {
	// Both are wire-format claims a stranger's verifier reads. An absent
	// `history` and an empty one are different statements, and so are an absent
	// `transition_sig` and an explicit null.
	data, err := json.Marshal(GenesisKeyHistory("ssh-ed25519 AAAA", "2026-03-03T05:35:09Z"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, `"history":[]`) {
		t.Errorf("history must marshal as an empty array, got %s", got)
	}
	if !strings.Contains(got, `"transition_sig":null`) {
		t.Errorf("genesis transition_sig must marshal as null, got %s", got)
	}
	if strings.Contains(got, `"valid_until"`) {
		t.Errorf("current must carry no valid_until, got %s", got)
	}
}

func TestWriteGenesisUsesTheSitesOwnCreatedAndPreservesOtherFields(t *testing.T) {
	kp := newKeypair(t)
	dir := writeSite(t, kp.pub, "2026-03-03T05:35:09Z")

	wrote, err := WriteGenesisKeyHistory(dir)
	if err != nil {
		t.Fatalf("WriteGenesisKeyHistory: %v", err)
	}
	if !wrote {
		t.Fatal("expected the genesis entry to be written")
	}

	b := mustLoad(t, dir)
	if b.Current.Epoch != 0 || b.Current.Key != kp.pub {
		t.Errorf("genesis must publish the site's own current key at epoch 0, got %+v", b.Current)
	}
	if b.Current.ValidFrom != "2026-03-03T05:35:09Z" {
		t.Errorf("valid_from must be the site's own `created`, got %q", b.Current.ValidFrom)
	}
	if b.Current.TransitionSig != nil {
		t.Error("genesis must carry a null transition_sig — nothing preceded it")
	}
	if len(b.History) != 0 {
		t.Errorf("genesis history must be empty, got %d", len(b.History))
	}

	raw, err := LoadWellKnownRaw(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["public_key_messages"]; !ok {
		t.Error("writing the key history dropped public_key_messages")
	}
}

func TestWriteGenesisNeverOverwritesAChain(t *testing.T) {
	// A chain is append-only. Rebuilding one from the current key would
	// silently discard every rotation it records.
	k0, k1 := newKeypair(t), newKeypair(t)
	dir := writeSite(t, k0.pub, "2026-03-03T05:35:09Z")
	if _, err := WriteGenesisKeyHistory(dir); err != nil {
		t.Fatal(err)
	}
	rotate(t, dir, "alice.polis.pub", k0, k1, "2026-09-15T10:22:03Z")

	wrote, err := WriteGenesisKeyHistory(dir)
	if err != nil {
		t.Fatalf("WriteGenesisKeyHistory: %v", err)
	}
	if wrote {
		t.Fatal("WriteGenesisKeyHistory overwrote an existing chain")
	}
	if b := mustLoad(t, dir); len(b.History) != 1 || b.Current.Epoch != 1 {
		t.Errorf("the rotation was discarded: %+v", b)
	}
}

func TestWriteGenesisRefusesWithoutACreatedTimestamp(t *testing.T) {
	kp := newKeypair(t)
	dir := writeSite(t, kp.pub, "")
	if _, err := WriteGenesisKeyHistory(dir); err == nil {
		t.Fatal("expected a refusal: without `created` there is no honest valid_from")
	}
}

// ---------- the seam ----------

func TestRecordKeyRotationMovesPublicKeyAndChainHeadTogether(t *testing.T) {
	k0, k1 := newKeypair(t), newKeypair(t)
	dir := writeSite(t, k0.pub, "2026-03-03T05:35:09Z")
	if _, err := WriteGenesisKeyHistory(dir); err != nil {
		t.Fatal(err)
	}

	rotate(t, dir, "alice.polis.pub", k0, k1, "2026-09-15T10:22:03Z")

	wk, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatal(err)
	}
	b := mustLoad(t, dir)
	if wk.PublicKey != k1.pub {
		t.Error("public_key was not updated by the rotation seam")
	}
	if b.Current.Key != wk.PublicKey {
		t.Error("the chain head and public_key disagree after a rotation — they must move in one write")
	}
	if b.Current.Epoch != 1 {
		t.Errorf("epoch must advance, got %d", b.Current.Epoch)
	}
	if len(b.History) != 1 || b.History[0].Key != k0.pub {
		t.Fatalf("the retired key must land in history, got %+v", b.History)
	}
	if b.History[0].ValidUntil != "2026-09-15T10:22:03Z" {
		t.Errorf("the retired key must end when the new one begins, got %q", b.History[0].ValidUntil)
	}
	if b.Current.ValidFrom != "2026-09-15T10:22:03Z" {
		t.Errorf("valid_from must be the signed rotation timestamp, got %q", b.Current.ValidFrom)
	}
}

func TestRecordKeyRotationStoresValidFromByteIdentically(t *testing.T) {
	// The sharpest gotcha in the design: valid_from is the validity boundary
	// AND the signed timestamp. Any reformatting breaks every signature.
	k0, k1 := newKeypair(t), newKeypair(t)
	dir := writeSite(t, k0.pub, "2026-03-03T05:35:09Z")
	if _, err := WriteGenesisKeyHistory(dir); err != nil {
		t.Fatal(err)
	}
	const ts = "2026-09-15T10:22:03Z"
	rotate(t, dir, "alice.polis.pub", k0, k1, ts)

	data, err := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"valid_from": "`+ts+`"`) {
		t.Errorf("valid_from was reformatted on the way to disk; file was:\n%s", data)
	}
}

func TestRecordKeyRotationSynthesisesGenesisWhenNoChainExists(t *testing.T) {
	// A rotation must never produce a chain that starts in the middle.
	k0, k1 := newKeypair(t), newKeypair(t)
	dir := writeSite(t, k0.pub, "2026-03-03T05:35:09Z")

	rotate(t, dir, "alice.polis.pub", k0, k1, "2026-09-15T10:22:03Z")

	b := mustLoad(t, dir)
	if len(b.History) != 1 || b.History[0].Epoch != 0 || b.History[0].Key != k0.pub {
		t.Fatalf("expected a synthesised genesis for the retired key, got %+v", b.History)
	}
	if b.History[0].ValidFrom != "2026-03-03T05:35:09Z" {
		t.Errorf("the synthesised genesis must be dated from the site's own `created`, got %q", b.History[0].ValidFrom)
	}
	if err := VerifyChain(b, "alice.polis.pub"); err != nil {
		t.Errorf("a synthesised-genesis chain must still verify: %v", err)
	}
}

// ---------- verification ----------

func TestVerifyChainAcceptsGenesisOnlyWithoutADomain(t *testing.T) {
	// Every site on the fleet is in this state. There is no rotation, so there
	// is nothing signed and the host is not needed to check it.
	b := GenesisKeyHistory("ssh-ed25519 AAAA", "2026-03-03T05:35:09Z")
	if err := VerifyChain(b, ""); err != nil {
		t.Errorf("genesis-only chain must verify with no domain: %v", err)
	}
}

func TestVerifyChainWalksRotationsBackToGenesis(t *testing.T) {
	k0, k1, k2 := newKeypair(t), newKeypair(t), newKeypair(t)
	dir := writeSite(t, k0.pub, "2026-03-03T05:35:09Z")
	if _, err := WriteGenesisKeyHistory(dir); err != nil {
		t.Fatal(err)
	}
	rotate(t, dir, "alice.polis.pub", k0, k1, "2026-09-15T10:22:03Z")
	rotate(t, dir, "alice.polis.pub", k1, k2, "2026-10-01T08:00:00Z")

	b := mustLoad(t, dir)
	if err := VerifyChain(b, "alice.polis.pub"); err != nil {
		t.Fatalf("a two-rotation chain must verify: %v", err)
	}
	if got := b.Current.Epoch; got != 2 {
		t.Errorf("epoch = %d, want 2", got)
	}
}

func TestVerifyChainRejectsAForeignDomain(t *testing.T) {
	// The domain is inside the canonical rotation message, so a chain lifted
	// onto another host does not verify there.
	k0, k1 := newKeypair(t), newKeypair(t)
	dir := writeSite(t, k0.pub, "2026-03-03T05:35:09Z")
	if _, err := WriteGenesisKeyHistory(dir); err != nil {
		t.Fatal(err)
	}
	rotate(t, dir, "alice.polis.pub", k0, k1, "2026-09-15T10:22:03Z")

	if err := VerifyChain(mustLoad(t, dir), "impostor.example.com"); err == nil {
		t.Fatal("a chain must not verify under a domain it was not signed for")
	}
}

func TestVerifyChainRejectsATamperedTimestamp(t *testing.T) {
	// valid_from is inside the signature. Backdating an entry to make an old
	// artifact look covered breaks the transition signature.
	k0, k1 := newKeypair(t), newKeypair(t)
	dir := writeSite(t, k0.pub, "2026-03-03T05:35:09Z")
	if _, err := WriteGenesisKeyHistory(dir); err != nil {
		t.Fatal(err)
	}
	rotate(t, dir, "alice.polis.pub", k0, k1, "2026-09-15T10:22:03Z")

	b := mustLoad(t, dir)
	b.Current.ValidFrom = "2026-01-01T00:00:00Z"
	b.History[0].ValidUntil = "2026-01-01T00:00:00Z"
	if err := VerifyChain(b, "alice.polis.pub"); err == nil {
		t.Fatal("a backdated valid_from must break the transition signature")
	}
}

func TestVerifyChainRejectsAnOmittedEntry(t *testing.T) {
	// The naive omission: drop a middle key and renumber nothing. The epoch
	// gap and the broken signature both catch it.
	k0, k1, k2 := newKeypair(t), newKeypair(t), newKeypair(t)
	dir := writeSite(t, k0.pub, "2026-03-03T05:35:09Z")
	if _, err := WriteGenesisKeyHistory(dir); err != nil {
		t.Fatal(err)
	}
	rotate(t, dir, "alice.polis.pub", k0, k1, "2026-09-15T10:22:03Z")
	rotate(t, dir, "alice.polis.pub", k1, k2, "2026-10-01T08:00:00Z")

	b := mustLoad(t, dir)
	b.History = b.History[:1] // drop epoch 1
	if err := VerifyChain(b, "alice.polis.pub"); err == nil {
		t.Fatal("a chain that skips an entry must not verify")
	}
}

func TestVerifyChainRejectsAGenesisThatClaimsASignature(t *testing.T) {
	sig := "-----BEGIN SSH SIGNATURE-----\nnope\n-----END SSH SIGNATURE-----\n"
	b := GenesisKeyHistory("ssh-ed25519 AAAA", "2026-03-03T05:35:09Z")
	b.Current.TransitionSig = &sig
	if err := VerifyChain(b, "alice.polis.pub"); err == nil {
		t.Fatal("nothing precedes genesis, so a transition_sig on it is a lie")
	}
}

func TestVerifyChainRejectsACurrentKeyWithAnEnd(t *testing.T) {
	b := GenesisKeyHistory("ssh-ed25519 AAAA", "2026-03-03T05:35:09Z")
	b.Current.ValidUntil = "2026-09-15T10:22:03Z"
	if err := VerifyChain(b, "alice.polis.pub"); err == nil {
		t.Fatal("being current is the absence of an end")
	}
}

func TestVerifyChainRejectsRotationsWhenTheDomainIsUnknown(t *testing.T) {
	// A caller that cannot determine the host must be told it could not check,
	// never that the chain passed.
	k0, k1 := newKeypair(t), newKeypair(t)
	dir := writeSite(t, k0.pub, "2026-03-03T05:35:09Z")
	if _, err := WriteGenesisKeyHistory(dir); err != nil {
		t.Fatal(err)
	}
	rotate(t, dir, "alice.polis.pub", k0, k1, "2026-09-15T10:22:03Z")

	if err := VerifyChain(mustLoad(t, dir), ""); err == nil {
		t.Fatal("a chain with rotations must not silently pass without a domain")
	}
}

// ---------- the point of the whole epic ----------

func TestKeyAtResolvesToTheKeyThatWasCurrentWhenSomethingWasSigned(t *testing.T) {
	k0, k1, k2 := newKeypair(t), newKeypair(t), newKeypair(t)
	dir := writeSite(t, k0.pub, "2026-03-03T05:35:09Z")
	if _, err := WriteGenesisKeyHistory(dir); err != nil {
		t.Fatal(err)
	}
	rotate(t, dir, "alice.polis.pub", k0, k1, "2026-09-15T10:22:03Z")
	rotate(t, dir, "alice.polis.pub", k1, k2, "2026-10-01T08:00:00Z")
	b := mustLoad(t, dir)

	cases := []struct {
		when string
		want string
		name string
	}{
		{"2026-03-03T05:35:09Z", k0.pub, "the instant of genesis"},
		{"2026-06-01T00:00:00Z", k0.pub, "before any rotation"},
		{"2026-09-15T10:22:03Z", k1.pub, "the instant of the first rotation"},
		{"2026-09-20T00:00:00Z", k1.pub, "between rotations"},
		{"2026-11-01T00:00:00Z", k2.pub, "after the last rotation"},
	}
	for _, c := range cases {
		got, ok := b.KeyAt(c.when)
		if !ok {
			t.Errorf("%s: chain does not cover %s", c.name, c.when)
			continue
		}
		if got != c.want {
			t.Errorf("%s: KeyAt(%s) resolved to the wrong key", c.name, c.when)
		}
	}

	if _, ok := b.KeyAt("2020-01-01T00:00:00Z"); ok {
		t.Error("a moment before the site existed must not resolve to a key")
	}
}

func TestKeysReturnsTheSequenceOldestFirst(t *testing.T) {
	// Clerk zips this against ds_key_history, which the DS serves ordered by
	// valid_from ASC. The orders must match or every parity check is a false
	// positive.
	k0, k1 := newKeypair(t), newKeypair(t)
	dir := writeSite(t, k0.pub, "2026-03-03T05:35:09Z")
	if _, err := WriteGenesisKeyHistory(dir); err != nil {
		t.Fatal(err)
	}
	rotate(t, dir, "alice.polis.pub", k0, k1, "2026-09-15T10:22:03Z")

	keys := mustLoad(t, dir).Keys()
	if len(keys) != 2 || keys[0] != k0.pub || keys[1] != k1.pub {
		t.Errorf("Keys() must be oldest-first, got %v", keys)
	}
}

func TestLoadKeyHistoryReportsAbsenceWithoutAnError(t *testing.T) {
	kp := newKeypair(t)
	dir := writeSite(t, kp.pub, "2026-03-03T05:35:09Z")
	b, err := LoadKeyHistory(dir)
	if err != nil {
		t.Fatalf("absence must not be an error: %v", err)
	}
	if b != nil {
		t.Fatal("expected no key history")
	}
	if HasKeyHistory(dir) {
		t.Error("HasKeyHistory must be false before provisioning")
	}
}

// ---------- cross-implementation parity ----------

// The bash CLI is a REFERENCE IMPLEMENTATION: what it produces defines what the
// protocol is for anyone building their own client from it. It maintains the
// chain with a jq expression rather than this package, so the two could drift —
// a differently-named field, a reordered history, a reformatted timestamp — and
// the drift would be invisible until somebody's signature failed.
//
// The fixture is REAL OUTPUT: `polis init` followed by two `polis rotate-key`
// runs against the bash CLI, captured verbatim. Regenerate it the same
// way if the format ever changes on purpose.
func TestChainProducedByTheBashCLIVerifiesHere(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "bash_rotated_wellknown.json"))
	if err != nil {
		t.Fatal(err)
	}
	block, err := KeyHistoryFromWellKnown(data)
	if err != nil {
		t.Fatalf("the bash CLI produced a public_key_history this package cannot parse: %v", err)
	}
	if block == nil {
		t.Fatal("the bash fixture publishes no key history")
	}
	if block.Current.Epoch != 2 || len(block.History) != 2 {
		t.Fatalf("expected two rotations, got epoch=%d history=%d", block.Current.Epoch, len(block.History))
	}
	// The chain is what a stranger checks, so the strangers' check must pass.
	if err := VerifyChain(block, "example.com"); err != nil {
		t.Fatalf("a chain built by the bash CLI does not verify with this verifier: %v", err)
	}

	// And the head is the key the same document publishes — the head-agreement
	// invariant, asserted across the implementation boundary.
	var wk struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal(data, &wk); err != nil {
		t.Fatal(err)
	}
	if wk.PublicKey != block.Current.Key {
		t.Error("the bash CLI left public_key and the chain head disagreeing")
	}
}

// ---------- the standard projection ----------

func TestDIDDocumentCarriesTheHistoryAfterARotation(t *testing.T) {
	k0, k1 := newKeypair(t), newKeypair(t)
	dir := writeSite(t, k0.pub, "2026-03-03T05:35:09Z")
	if _, err := WriteGenesisKeyHistory(dir); err != nil {
		t.Fatal(err)
	}

	// Before the rotation the document is the single-key one polis has always
	// published — no churn for the 32 tenants that have never rotated.
	before, err := BuildDIDDocument(dir, "alice.polis.pub")
	if err != nil {
		t.Fatal(err)
	}
	var beforeDoc struct {
		VerificationMethod []struct {
			ID string `json:"id"`
		} `json:"verificationMethod"`
	}
	if err := json.Unmarshal(before, &beforeDoc); err != nil {
		t.Fatal(err)
	}
	if len(beforeDoc.VerificationMethod) != 1 || beforeDoc.VerificationMethod[0].ID != "did:web:alice.polis.pub#key-1" {
		t.Fatalf("a genesis-only site must publish exactly one verification method at key-1, got %+v", beforeDoc.VerificationMethod)
	}

	rotate(t, dir, "alice.polis.pub", k0, k1, "2026-09-15T10:22:03Z")

	after, err := BuildDIDDocument(dir, "alice.polis.pub")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		VerificationMethod []struct {
			ID           string `json:"id"`
			PublicKeyJwk struct {
				X string `json:"x"`
			} `json:"publicKeyJwk"`
		} `json:"verificationMethod"`
		Authentication  []string `json:"authentication"`
		AssertionMethod []string `json:"assertionMethod"`
	}
	if err := json.Unmarshal(after, &doc); err != nil {
		t.Fatal(err)
	}

	// The retired key STAYS — a signature it made is still checkable through
	// the DID, which is the hole this epic exists to close.
	if len(doc.VerificationMethod) != 2 {
		t.Fatalf("the retired key must remain in verificationMethod, got %d", len(doc.VerificationMethod))
	}
	// …and STOPS ASSERTING. Leaving it in assertionMethod would let a retired
	// key keep speaking for this identity, which would make rotation pointless.
	if len(doc.AssertionMethod) != 1 || doc.AssertionMethod[0] != "did:web:alice.polis.pub#key-2" {
		t.Errorf("only the current key may assert, got %v", doc.AssertionMethod)
	}
	if len(doc.Authentication) != 1 || doc.Authentication[0] != "did:web:alice.polis.pub#key-2" {
		t.Errorf("only the current key may authenticate, got %v", doc.Authentication)
	}
	if doc.VerificationMethod[1].ID != "did:web:alice.polis.pub#key-1" {
		t.Errorf("the retired key must keep its own fragment, got %q", doc.VerificationMethod[1].ID)
	}
	if doc.VerificationMethod[0].PublicKeyJwk.X == doc.VerificationMethod[1].PublicKeyJwk.X {
		t.Error("the document publishes the same key twice")
	}
}

func TestDIDDocumentIgnoresAChainItsOwnPublicKeyContradicts(t *testing.T) {
	// Head disagreement is a real finding that Judge reports. Building a
	// plausible-looking document from the losing side would bury it.
	k0, k1 := newKeypair(t), newKeypair(t)
	dir := writeSite(t, k0.pub, "2026-03-03T05:35:09Z")
	if _, err := WriteGenesisKeyHistory(dir); err != nil {
		t.Fatal(err)
	}
	rotate(t, dir, "alice.polis.pub", k0, k1, "2026-09-15T10:22:03Z")

	// Someone edits public_key back without touching the chain.
	raw, err := LoadWellKnownRaw(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw["public_key"] = k0.pub
	if err := SaveWellKnownRaw(dir, raw, ChangePublicKey); err != nil {
		t.Fatal(err)
	}

	data, err := BuildDIDDocument(dir, "alice.polis.pub")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		VerificationMethod []struct{} `json:"verificationMethod"`
		AssertionMethod    []string   `json:"assertionMethod"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.VerificationMethod) != 1 {
		t.Fatalf("a contradicted chain must be ignored, not half-applied; got %d methods", len(doc.VerificationMethod))
	}
	if doc.AssertionMethod[0] != "did:web:alice.polis.pub#key-1" {
		t.Errorf("the document must state the key .well-known/polis publishes, got %v", doc.AssertionMethod)
	}
}

// ---------- the epic's whole point, end to end ----------

// ⭐ THIS IS WHAT THE EPIC IS FOR. Something signed before a rotation is still
// verifiable afterwards, FROM THE SITE ALONE, with no discovery service in the
// loop — which is the sentence "verify this yourself, from my site, forever"
// depends on. Before this, rotating a key made every artifact signed under the
// old one fail against what the site published, and the only surviving evidence
// was a row in a central database.
//
// The test ACTUALLY ROTATES rather than hand-building a chain, so it exercises
// the real seam both rotation paths call.
func TestAnArtifactSignedBeforeARotationStillVerifiesFromTheSiteAlone(t *testing.T) {
	k0, k1, k2 := newKeypair(t), newKeypair(t), newKeypair(t)
	dir := writeSite(t, k0.pub, "2026-03-03T05:35:09Z")
	if _, err := WriteGenesisKeyHistory(dir); err != nil {
		t.Fatal(err)
	}

	// Alice signs a post while k0 is current.
	const post = "# On rotating keys\n\nSomething worth keeping.\n"
	const signedAt = "2026-05-01T09:00:00Z"
	sig, err := signing.SignContent([]byte(post), k0.priv)
	if err != nil {
		t.Fatal(err)
	}

	// Sanity: it verifies against the key that is current right now.
	if ok, err := signing.VerifySignature([]byte(post), []byte(k0.pub), sig); err != nil || !ok {
		t.Fatalf("the post does not verify against the key that signed it: %v", err)
	}

	// Two rotations later…
	rotate(t, dir, "alice.polis.pub", k0, k1, "2026-09-15T10:22:03Z")
	rotate(t, dir, "alice.polis.pub", k1, k2, "2026-10-01T08:00:00Z")

	// …the key the site PUBLISHES no longer verifies it. This is the failure
	// the epic exists to fix, and it must still be true — otherwise the test
	// below proves nothing.
	wk, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := signing.VerifySignature([]byte(post), []byte(wk.PublicKey), sig); ok {
		t.Fatal("the current key verified an old signature; the test setup is wrong")
	}

	// A stranger with nothing but this site's .well-known/polis:
	block, err := LoadKeyHistory(dir)
	if err != nil || block == nil {
		t.Fatalf("the site publishes no history: %v", err)
	}
	// 1. The chain proves itself, with no service consulted.
	if err := VerifyChain(block, "alice.polis.pub"); err != nil {
		t.Fatalf("the published chain does not verify: %v", err)
	}
	// 2. Resolve the key that was authoritative when the post was signed.
	keyThen, ok := block.KeyAt(signedAt)
	if !ok {
		t.Fatalf("the chain does not cover %s", signedAt)
	}
	if keyThen != k0.pub {
		t.Fatal("the chain resolved the wrong key for the moment the post was signed")
	}
	// 3. And the signature verifies against it.
	verified, err := signing.VerifySignature([]byte(post), []byte(keyThen), sig)
	if err != nil || !verified {
		t.Fatalf("an artifact signed before the rotation does not verify against the key the chain resolves: %v", err)
	}

	// The same resolution for something signed between the two rotations.
	betweenSig, err := signing.SignContent([]byte(post), k1.priv)
	if err != nil {
		t.Fatal(err)
	}
	keyBetween, ok := block.KeyAt("2026-09-20T00:00:00Z")
	if !ok || keyBetween != k1.pub {
		t.Fatalf("the chain resolved the wrong key for a moment between rotations")
	}
	if verified, err := signing.VerifySignature([]byte(post), []byte(keyBetween), betweenSig); err != nil || !verified {
		t.Fatalf("an artifact signed between two rotations does not verify: %v", err)
	}
}

// EntryAt carries KeyAt's walk and adds the epoch — which a verifier reporting
// WHICH key verified something needs (SIGNET epic 31 D4). ⚠️ The epoch cannot be
// recovered by looking the key up afterwards: nothing forbids rotating A → B → A.
func TestEntryAtNamesTheEpochEvenWhenAKeyComesBack(t *testing.T) {
	k0, k1 := newKeypair(t), newKeypair(t)
	dir := writeSite(t, k0.pub, "2026-03-03T05:35:09Z")
	if _, err := WriteGenesisKeyHistory(dir); err != nil {
		t.Fatal(err)
	}
	rotate(t, dir, "alice.polis.pub", k0, k1, "2026-09-15T10:22:03Z")
	rotate(t, dir, "alice.polis.pub", k1, k0, "2026-10-01T08:00:00Z")
	b := mustLoad(t, dir)
	if err := VerifyChain(b, "alice.polis.pub"); err != nil {
		t.Fatalf("A → B → A is a valid chain: %v", err)
	}

	for _, c := range []struct {
		when  string
		epoch int
		key   string
	}{
		{"2026-06-01T00:00:00Z", 0, k0.pub},
		{"2026-09-20T00:00:00Z", 1, k1.pub},
		{"2026-11-01T00:00:00Z", 2, k0.pub},
	} {
		e, ok := b.EntryAt(c.when)
		if !ok || e.Epoch != c.epoch || e.Key != c.key {
			t.Errorf("EntryAt(%s) = epoch %d, want epoch %d", c.when, e.Epoch, c.epoch)
		}
		if k, _ := b.KeyAt(c.when); k != c.key {
			t.Errorf("KeyAt(%s) must be EntryAt's key", c.when)
		}
	}
}
