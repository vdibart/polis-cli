package discovery

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// repoRoot finds the repository root from this test file, so the shared
// contract fixture is read from where the DS test reads it.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}

// skipWithoutDS skips a contract test in the public repository, where the
// discovery service source (and the shared fixtures beside it) is not present.
// Anywhere the DS tree exists, a missing fixture still fails.
func skipWithoutDS(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(repoRoot(t), "discovery-service")); os.IsNotExist(err) {
		t.Skip("the shared contract fixtures live with the discovery service source, which is not in this repository")
	}
}

// TestWitnessCanonicalMatchesTheSharedContractFixture pins the witness signing
// bytes to the same fixture the DS signer is tested against. If the two sides
// ever disagree about a field list or the canonical form, every witness the DS
// issues stops verifying here, and this is where it shows.
func TestWitnessCanonicalMatchesTheSharedContractFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "discovery-service", "core", "contract-fixtures", "witness-canonical.json"))
	if err != nil {
		skipWithoutDS(t)
		t.Fatalf("read fixture: %v", err)
	}
	var fixture struct {
		Cases []struct {
			Name      string          `json:"name"`
			Record    json.RawMessage `json:"record"`
			Canonical string          `json:"canonical"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if len(fixture.Cases) < 2 {
		t.Fatalf("fixture has %d cases; expected content and rotation", len(fixture.Cases))
	}
	for _, c := range fixture.Cases {
		var w Witness
		if err := json.Unmarshal(c.Record, &w); err != nil {
			t.Fatalf("%s: parse record: %v", c.Name, err)
		}
		got, err := w.Canonical()
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if string(got) != c.Canonical {
			t.Errorf("%s:\n got  %s\n want %s", c.Name, got, c.Canonical)
		}
	}
}

// signedWitness signs a witness the way the DS does: the DS key over Canonical.
func signedWitness(t *testing.T, w Witness) (Witness, string) {
	t.Helper()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := w.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	sig, err := signing.SignContent(canonical, priv)
	if err != nil {
		t.Fatal(err)
	}
	w.Signature = sig
	return w, strings.TrimSpace(string(pub))
}

func contentWitness() Witness {
	return Witness{
		Action:       WitnessActionContent,
		Type:         "pub.polis.post",
		URL:          "https://alice.polis.pub/content/pub.polis.core/post/20260913/hello.md",
		Version:      "sha256:" + strings.Repeat("a", 64),
		Author:       "alice.polis.pub",
		Actor:        "alice.polis.pub",
		PublicKey:    "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample",
		ArtifactHash: "sha256:" + strings.Repeat("b", 64),
		DS:           "https://ds.polis.pub",
		DSKeyID:      "ds-primary",
		WitnessedAt:  "2026-09-13T12:00:00.123Z",
	}
}

func TestAWitnessVerifiesOfflineAgainstTheDSKey(t *testing.T) {
	w, dsKey := signedWitness(t, contentWitness())
	if err := w.VerifySignature(dsKey); err != nil {
		t.Fatalf("a witness signed by the DS key must verify against it: %v", err)
	}
}

func TestAWitnessMovedOntoOtherValuesDoesNotVerify(t *testing.T) {
	w, dsKey := signedWitness(t, contentWitness())
	other := "sha256:" + strings.Repeat("c", 64)
	for name, mutate := range map[string]func(*Witness){
		"version":               func(x *Witness) { x.Version = other },
		"artifact_hash":         func(x *Witness) { x.ArtifactHash = other },
		"artifact_hash dropped": func(x *Witness) { x.ArtifactHash = "" },
		"witnessed_at":          func(x *Witness) { x.WitnessedAt = "2020-01-01T00:00:00.000Z" },
		"url":                   func(x *Witness) { x.URL = "https://mallory.polis.pub/x.md" },
		"ds_key_id":             func(x *Witness) { x.DSKeyID = "ds-other" },
	} {
		moved := w
		mutate(&moved)
		if err := moved.VerifySignature(dsKey); err == nil {
			t.Errorf("changing %s must break the witness", name)
		}
	}
}

func TestAWitnessDoesNotVerifyAgainstAnotherKey(t *testing.T) {
	w, _ := signedWitness(t, contentWitness())
	_, otherKey := signedWitness(t, contentWitness())
	if err := w.VerifySignature(otherKey); err == nil {
		t.Fatal("a witness must not verify against a key that did not sign it")
	}
}

func TestAnUnknownWitnessActionHasNoCanonicalForm(t *testing.T) {
	if _, err := (Witness{Action: "something-else"}).Canonical(); err == nil {
		t.Fatal("an unknown action must not be given signing bytes by guesswork")
	}
}

func TestMergeWitnessesNeverRemovesAndKeepsTheEarliest(t *testing.T) {
	v1 := contentWitness()
	v2 := contentWitness()
	v2.Version = "sha256:" + strings.Repeat("d", 64)
	v2.WitnessedAt = "2026-09-14T00:00:00.000Z"

	set, changed := MergeWitnesses(nil, v1)
	if !changed || len(set) != 1 {
		t.Fatalf("first witness must be added: %+v", set)
	}

	// A later source that only knows the CURRENT version must not erase v1.
	set, changed = MergeWitnesses(set, v2)
	if !changed || len(set) != 2 {
		t.Fatalf("v2 must be added beside v1, never replace it: %+v", set)
	}

	// Merging an empty source is a no-op, not a reset.
	if got, changed := MergeWitnesses(set); changed || len(got) != 2 {
		t.Fatalf("merging nothing must change nothing: %+v", got)
	}

	// The same testimony witnessed again later adds nothing.
	later := v1
	later.WitnessedAt = "2026-09-20T00:00:00.000Z"
	if got, changed := MergeWitnesses(set, later); changed || got[0].WitnessedAt != v1.WitnessedAt {
		t.Fatalf("a later witness of the same bytes must not displace the earlier one: %+v", got)
	}

	// ...but an EARLIER witness of the same testimony replaces the later one.
	earlierV2 := v2
	earlierV2.WitnessedAt = "2026-09-13T13:00:00.000Z"
	got, changed := MergeWitnesses(set, earlierV2)
	if !changed || len(got) != 2 {
		t.Fatalf("an earlier witness of the same bytes must be kept: %+v", got)
	}
	for _, w := range got {
		if w.Version == v2.Version && w.WitnessedAt != earlierV2.WitnessedAt {
			t.Fatalf("expected the earliest witnessed_at for v2, got %s", w.WitnessedAt)
		}
	}

	// A second DS's witness of the same bytes is separate testimony (D2).
	otherDS := v1
	otherDS.DS = "https://ds.example.org"
	if got, changed := MergeWitnesses(got, otherDS); !changed || len(got) != 3 {
		t.Fatalf("a second DS's witness must be added: %+v", got)
	}
}

func TestArtifactHashIsSha256OfTheSigningBase(t *testing.T) {
	base := "---\ntitle: Hello\npublished: 2026-09-13T12:00:00Z\n---\nbody\n"
	got := ArtifactHash(base)
	if !strings.HasPrefix(got, "sha256:") || len(got) != len("sha256:")+64 {
		t.Fatalf("malformed artifact hash %q", got)
	}
	if got == ArtifactHash(strings.Replace(base, "2026-09-13", "2026-01-01", 1)) {
		t.Fatal("a frontmatter change must change the artifact hash — that is its whole reason to exist")
	}
}

func TestFetchDSPublicKeyAsksForTheNamedKey(t *testing.T) {
	_, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	var askedFor, requestID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		askedFor = r.URL.Query().Get("key_id")
		requestID = r.Header.Get("X-Request-Id")
		if r.URL.Path != "/v1/sites/public-key" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"public_key": strings.TrimSpace(string(pub)), "key_id": askedFor})
	}))
	defer srv.Close()

	key, err := FetchDSPublicKey(srv.Client(), srv.URL, "ds-2025", "3f2b0c1e-0000-4000-8000-000000000032")
	if err != nil {
		t.Fatal(err)
	}
	if askedFor != "ds-2025" {
		t.Fatalf("asked for key %q, want ds-2025 — a retired DS key must be requested by id", askedFor)
	}
	if requestID != "3f2b0c1e-0000-4000-8000-000000000032" {
		t.Fatalf("X-Request-Id = %q — the DS key fetch is a boundary and must carry the run's id (E8)", requestID)
	}
	if key != strings.TrimSpace(string(pub)) {
		t.Fatalf("got key %q", key)
	}
}
