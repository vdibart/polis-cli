package site

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func wellKnownFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	wkDir := filepath.Join(dir, ".well-known")
	if err := os.MkdirAll(wkDir, 0755); err != nil {
		t.Fatal(err)
	}
	body := `{"version":"2.0","public_key":"ssh-ed25519 AAAA","author_name":"Operator","created":"2026-09-05T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(wkDir, "polis"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func rawWellKnown(t *testing.T, dir string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestActorRegistryPointer_AbsentIsADefinedState — most sites run no actors, so
// "no pointer" is the normal case and must not read as an error.
func TestActorRegistryPointer_AbsentIsADefinedState(t *testing.T) {
	dir := wellKnownFixture(t)
	if got := ActorRegistryPointer(dir); got != "" {
		t.Errorf("a fresh site should publish no actor registry pointer, got %q", got)
	}
	if _, present := rawWellKnown(t, dir)["actor_registry"]; present {
		t.Error("the key should be absent, not empty — absent and empty are different claims")
	}
}

// TestSetActorRegistryPointer_ClearingDeletesTheKey — withdrawal has to be as
// reliable as statement. An operator that stops running actors and leaves a
// pointer behind publishes a 404 where a fact used to be.
func TestSetActorRegistryPointer_ClearingDeletesTheKey(t *testing.T) {
	dir := wellKnownFixture(t)
	if err := SetActorRegistryPointer(dir, "/actors/registry.json"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := ActorRegistryPointer(dir); got != "/actors/registry.json" {
		t.Fatalf("pointer = %q", got)
	}

	if err := SetActorRegistryPointer(dir, ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, present := rawWellKnown(t, dir)["actor_registry"]; present {
		t.Error("clearing must delete the key, not write an empty string")
	}
}

// TestSetPointers_PreserveEverythingElse — these go through the raw map, so the
// risk is a round trip that silently drops a field the struct does not model.
func TestSetPointers_PreserveEverythingElse(t *testing.T) {
	dir := wellKnownFixture(t)
	if err := SetActorRegistryPointer(dir, "/actors/registry.json"); err != nil {
		t.Fatal(err)
	}
	if err := SetOperatorPointer(dir, "polis.polis.pub"); err != nil {
		t.Fatal(err)
	}

	raw := rawWellKnown(t, dir)
	for _, key := range []string{"version", "public_key", "author_name", "created"} {
		if _, present := raw[key]; !present {
			t.Errorf("%s was dropped by a pointer write", key)
		}
	}
	if raw["public_key"] != "ssh-ed25519 AAAA" {
		t.Errorf("public_key changed: %v", raw["public_key"])
	}
	if got := OperatorPointer(dir); got != "polis.polis.pub" {
		t.Errorf("operator = %q", got)
	}
}

// TestWellKnownStruct_RoundTripsBothPointers guards the struct tags. A typo in
// `json:"actor_registry"` would be silent: the field would marshal under a name
// nothing reads, and discovery would simply never find the registry.
func TestWellKnownStruct_RoundTripsBothPointers(t *testing.T) {
	wk := &WellKnown{
		Version:       "2.0",
		PublicKey:     "ssh-ed25519 AAAA",
		AuthorName:    "Operator",
		Created:       "2026-09-05T00:00:00Z",
		ActorRegistry: "/actors/registry.json",
		Operator:      "polis.polis.pub",
	}
	data, err := json.Marshal(wk)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	if raw["actor_registry"] != "/actors/registry.json" {
		t.Errorf("actor_registry did not marshal under its wire name: %s", data)
	}
	if raw["operator"] != "polis.polis.pub" {
		t.Errorf("operator did not marshal under its wire name: %s", data)
	}

	// Absent on an ordinary tenant, not present-and-empty.
	plain, _ := json.Marshal(&WellKnown{Version: "2.0", PublicKey: "k", AuthorName: "a", Created: "c"})
	var plainRaw map[string]interface{}
	json.Unmarshal(plain, &plainRaw)
	for _, key := range []string{"actor_registry", "operator"} {
		if _, present := plainRaw[key]; present {
			t.Errorf("%s should be omitted on a site that runs no actors: %s", key, plain)
		}
	}
}
