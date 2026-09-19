package did

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// rfc8032TestKey is the Ed25519 key from RFC 8032 §7.1 TEST 1 — the same key
// RFC 8037 §A.1 publishes as a worked JWK example. Using it means the golden
// document below is checked against an EXTERNAL source of truth rather than
// against our own output: if the projection is wrong, `x` will not match the
// value the RFC prints, and no amount of regenerating the fixture will hide it.
func rfc8032TestKey(t *testing.T) ed25519.PublicKey {
	t.Helper()
	seed, err := hex.DecodeString("9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60")
	if err != nil {
		t.Fatalf("decode seed: %v", err)
	}
	return ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
}

// rfc8037X is the `x` member RFC 8037 §A.1 prints for that key.
const rfc8037X = "11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"

func TestTheJWKProjectionMatchesTheRFC(t *testing.T) {
	jwk, err := PublicKeyJWK(rfc8032TestKey(t))
	if err != nil {
		t.Fatalf("PublicKeyJWK: %v", err)
	}
	if jwk.Kty != "OKP" || jwk.Crv != "Ed25519" {
		t.Errorf("kty/crv = %q/%q, want OKP/Ed25519", jwk.Kty, jwk.Crv)
	}
	if jwk.X != rfc8037X {
		t.Errorf("x = %q, want %q (RFC 8037 §A.1)", jwk.X, rfc8037X)
	}
	if strings.Contains(jwk.X, "=") {
		t.Errorf("x = %q is padded; RFC 8037 requires unpadded base64url", jwk.X)
	}
}

// The golden document. Written out in full, on purpose: this is the artifact a
// stranger's resolver reads, and a diff here should be loud.
const goldenDocument = `{
  "@context": [
    "https://www.w3.org/ns/did/v1",
    "https://w3id.org/security/suites/jws-2020/v1"
  ],
  "id": "did:web:alice.polis.pub",
  "verificationMethod": [
    {
      "id": "did:web:alice.polis.pub#key-1",
      "type": "JsonWebKey2020",
      "controller": "did:web:alice.polis.pub",
      "publicKeyJwk": {
        "kty": "OKP",
        "crv": "Ed25519",
        "x": "11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"
      }
    }
  ],
  "authentication": [
    "did:web:alice.polis.pub#key-1"
  ],
  "assertionMethod": [
    "did:web:alice.polis.pub#key-1"
  ]
}
`

func TestBuildProducesTheGoldenDocument(t *testing.T) {
	got, err := Build("alice.polis.pub", rfc8032TestKey(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if string(got) != goldenDocument {
		t.Errorf("document drifted.\n--- got ---\n%s\n--- want ---\n%s", got, goldenDocument)
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	// Medic decides whether to rewrite a tenant's file by comparing the bytes
	// it would write against the bytes on disk. Non-determinism here would
	// make every sweep rewrite every tenant's document forever.
	pub := rfc8032TestKey(t)
	first, err := Build("alice.polis.pub", pub)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := Build("alice.polis.pub", pub)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if string(again) != string(first) {
			t.Fatalf("run %d differs from the first", i)
		}
	}
}

func TestAssertionMethodIsPresent(t *testing.T) {
	// It is the relationship a VC verifier checks when the DID is a credential
	// subject or issuer — the whole point of being resolvable.
	doc, err := BuildDocument("alice.example", rfc8032TestKey(t))
	if err != nil {
		t.Fatalf("BuildDocument: %v", err)
	}
	if len(doc.AssertionMethod) != 1 || doc.AssertionMethod[0] != doc.VerificationMethod[0].ID {
		t.Errorf("assertionMethod = %v, want [%s]", doc.AssertionMethod, doc.VerificationMethod[0].ID)
	}
	if len(doc.Authentication) != 1 || doc.Authentication[0] != doc.VerificationMethod[0].ID {
		t.Errorf("authentication = %v, want [%s]", doc.Authentication, doc.VerificationMethod[0].ID)
	}
}

func TestTheJWKRoundTripsBackToTheKey(t *testing.T) {
	pub := rfc8032TestKey(t)
	doc, err := BuildDocument("alice.example", pub)
	if err != nil {
		t.Fatalf("BuildDocument: %v", err)
	}
	back, err := base64.RawURLEncoding.DecodeString(doc.VerificationMethod[0].PublicKeyJwk.X)
	if err != nil {
		t.Fatalf("decode x: %v", err)
	}
	if !ed25519.PublicKey(back).Equal(pub) {
		t.Error("x does not round-trip back to the key it was built from")
	}
}

func TestTheContextDefinesTheVerificationMethodType(t *testing.T) {
	// A regression guard for the one thing a strict resolver rejects: the
	// JWS-2020 context defines JsonWebKey2020; the neighbouring
	// w3id.org/security/jwk/v1 defines JsonWebKey and would leave our type
	// undefined under JSON-LD expansion.
	doc, err := BuildDocument("alice.example", rfc8032TestKey(t))
	if err != nil {
		t.Fatalf("BuildDocument: %v", err)
	}
	if doc.VerificationMethod[0].Type != "JsonWebKey2020" {
		t.Fatalf("type = %q", doc.VerificationMethod[0].Type)
	}
	want := "https://w3id.org/security/suites/jws-2020/v1"
	found := false
	for _, c := range doc.Context {
		if c == want {
			found = true
		}
		if c == "https://w3id.org/security/jwk/v1" {
			t.Error("the jwk/v1 context does not define JsonWebKey2020 — see did.Contexts")
		}
	}
	if !found {
		t.Errorf("@context = %v, missing %s", doc.Context, want)
	}
	if doc.Context[0] != "https://www.w3.org/ns/did/v1" {
		t.Errorf("@context[0] = %q, want the DID Core context first", doc.Context[0])
	}
}

func TestHostsThatCannotBecomeADID(t *testing.T) {
	for _, host := range []string{"", "   ", "https://alice.example", "alice.example/path", "alice example"} {
		if _, err := Build(host, rfc8032TestKey(t)); err == nil {
			t.Errorf("Build(%q) succeeded; want an error", host)
		}
	}
}

func TestHostsAreNormalized(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Alice.Polis.Pub", "did:web:alice.polis.pub"},
		{" alice.polis.pub ", "did:web:alice.polis.pub"},
		{"alice.polis.pub.", "did:web:alice.polis.pub"},
	} {
		doc, err := BuildDocument(tc.in, rfc8032TestKey(t))
		if err != nil {
			t.Fatalf("BuildDocument(%q): %v", tc.in, err)
		}
		if doc.ID != tc.want {
			t.Errorf("BuildDocument(%q).id = %q, want %q", tc.in, doc.ID, tc.want)
		}
	}
}

func TestAShortKeyIsRejected(t *testing.T) {
	if _, err := Build("alice.example", ed25519.PublicKey([]byte{1, 2, 3})); err == nil {
		t.Error("Build accepted a 3-byte key")
	}
}

func TestTheDocumentIsValidJSON(t *testing.T) {
	data, err := Build("alice.example", rfc8032TestKey(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("the published document does not parse: %v", err)
	}
	for _, key := range []string{"@context", "id", "verificationMethod", "authentication", "assertionMethod"} {
		if _, ok := back[key]; !ok {
			t.Errorf("document is missing %q", key)
		}
	}
	// No generator marker, no polis-specific field: the format is not ours to
	// decorate.
	for key := range back {
		switch key {
		case "@context", "id", "verificationMethod", "authentication", "assertionMethod":
		default:
			t.Errorf("unexpected top-level field %q in the DID Document", key)
		}
	}
}

// ---------- key history projection (SIGNET epic 16) ----------

func mustKey(t *testing.T, seed byte) ed25519.PublicKey {
	t.Helper()
	b := make([]byte, ed25519.SeedSize)
	for i := range b {
		b[i] = seed
	}
	return ed25519.NewKeyFromSeed(b).Public().(ed25519.PublicKey)
}

// ⭐ THE FLEET SAFETY PROPERTY. Every tenant today has a genesis-only chain, and
// a document that changed for all of them would churn 32 files, re-baseline
// Judge's snapshots, and make a routine upgrade look like tampering.
func TestGenesisOnlyDocumentIsUnchangedByHistorySupport(t *testing.T) {
	pub := mustKey(t, 7)
	withoutHistory, err := Build("alice.polis.pub", pub)
	if err != nil {
		t.Fatal(err)
	}
	withEmptyHistory, err := BuildWithHistory("alice.polis.pub", pub, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(withoutHistory) != string(withEmptyHistory) {
		t.Errorf("a site that has never rotated must publish exactly the document it always did:\n%s\nvs\n%s",
			withoutHistory, withEmptyHistory)
	}
	if !strings.Contains(string(withoutHistory), "#key-1") {
		t.Error("genesis must keep the key-1 fragment every polis DID document has carried")
	}
}

func TestRetiredKeysStayVerifiableAndStopAsserting(t *testing.T) {
	k0, k1, k2 := mustKey(t, 1), mustKey(t, 2), mustKey(t, 3)
	doc, err := BuildDocumentWithHistory("alice.polis.pub", k2, 2, []RetiredKey{
		{Epoch: 0, Key: k0},
		{Epoch: 1, Key: k1},
	})
	if err != nil {
		t.Fatal(err)
	}

	// ⛔ The whole design is which LIST a retired key appears in.
	if len(doc.VerificationMethod) != 3 {
		t.Fatalf("every key must stay resolvable, got %d verification methods", len(doc.VerificationMethod))
	}
	if len(doc.AssertionMethod) != 1 || len(doc.Authentication) != 1 {
		t.Fatalf("only the current key may speak for the identity, got assertion=%v authentication=%v",
			doc.AssertionMethod, doc.Authentication)
	}

	current := "did:web:alice.polis.pub#key-3"
	if doc.AssertionMethod[0] != current || doc.Authentication[0] != current {
		t.Errorf("the current key must be the one that asserts and authenticates, got %v / %v",
			doc.AssertionMethod, doc.Authentication)
	}

	// Newest first after the current key: reading down walks backwards in time.
	wantIDs := []string{
		"did:web:alice.polis.pub#key-3",
		"did:web:alice.polis.pub#key-2",
		"did:web:alice.polis.pub#key-1",
	}
	for i, want := range wantIDs {
		if doc.VerificationMethod[i].ID != want {
			t.Errorf("verificationMethod[%d] = %q, want %q", i, doc.VerificationMethod[i].ID, want)
		}
	}

	// And a retired method must genuinely carry the retired key, not a repeat.
	x0, _ := PublicKeyJWK(k0)
	if doc.VerificationMethod[2].PublicKeyJwk.X != x0.X {
		t.Error("the oldest verification method does not carry the oldest key")
	}
	if doc.VerificationMethod[0].PublicKeyJwk.X == x0.X {
		t.Error("the current verification method is publishing a retired key")
	}
}

func TestFragmentsCountFromOneSoGenesisIsKeyOne(t *testing.T) {
	for epoch, want := range map[int]string{0: "key-1", 1: "key-2", 7: "key-8"} {
		if got := FragmentForEpoch(epoch); got != want {
			t.Errorf("FragmentForEpoch(%d) = %q, want %q", epoch, got, want)
		}
	}
	if FragmentForEpoch(0) != KeyFragment {
		t.Error("epoch 0's fragment must be the KeyFragment constant every existing document uses")
	}
}

func TestDocumentWithHistoryMarshalsDeterministically(t *testing.T) {
	// Medic decides whether to rewrite by comparing bytes, so any
	// non-determinism here would make every sweep rewrite every tenant's file.
	k0, k1 := mustKey(t, 4), mustKey(t, 5)
	retired := []RetiredKey{{Epoch: 0, Key: k0}}
	a, err := BuildWithHistory("alice.polis.pub", k1, 1, retired)
	if err != nil {
		t.Fatal(err)
	}
	b, err := BuildWithHistory("alice.polis.pub", k1, 1, retired)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Error("two builds of the same document produced different bytes")
	}
}
