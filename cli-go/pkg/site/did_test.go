package site

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInitPublishesADIDWhenItKnowsTheHost(t *testing.T) {
	dir := t.TempDir()
	result, err := Init(dir, InitOptions{
		Author:  "Alice",
		BaseURL: "https://alice.polis.pub",
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if result.DID != "did:web:alice.polis.pub" {
		t.Errorf("result.DID = %q, want did:web:alice.polis.pub", result.DID)
	}

	data, err := os.ReadFile(DIDDocumentPath(dir))
	if err != nil {
		t.Fatalf("read did.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse did.json: %v", err)
	}
	if doc["id"] != "did:web:alice.polis.pub" {
		t.Errorf("id = %v", doc["id"])
	}

	// The document must be world-readable: a DID resolver fetches it over HTTP
	// like any other public file.
	info, err := os.Stat(DIDDocumentPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}

	found := false
	for _, f := range result.FilesCreated {
		if f == ".well-known/did.json" {
			found = true
		}
	}
	if !found {
		t.Errorf("files_created does not mention .well-known/did.json: %v", result.FilesCreated)
	}
}

func TestInitPublishesNoDIDWhenTheHostIsUnknown(t *testing.T) {
	// A DID whose id names the wrong host does not resolve, and guessing one
	// is worse than publishing none. Medic and Tailor fill it in later, once
	// somebody knows where the site actually lives.
	dir := t.TempDir()
	result, err := Init(dir, InitOptions{Author: "Alice"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if result.DID != "" {
		t.Errorf("result.DID = %q, want empty", result.DID)
	}
	if _, err := os.Stat(DIDDocumentPath(dir)); !os.IsNotExist(err) {
		t.Errorf("did.json exists with no canonical host: %v", err)
	}
}

func TestInitWithoutADIDIsOtherwiseUnchanged(t *testing.T) {
	// The projection must not be load-bearing: a site with no DID is a normal,
	// complete polis site.
	dir := t.TempDir()
	if _, err := Init(dir, InitOptions{Author: "Alice"}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	for _, rel := range []string{
		filepath.Join(".well-known", "polis"),
		"favicon.svg",
		filepath.Join(".polis", "keys", "id_ed25519.pub"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("%s missing: %v", rel, err)
		}
	}
}

func TestPublishingTheDocumentIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, err := Init(dir, InitOptions{Author: "Alice", BaseURL: "https://alice.polis.pub"}); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(DIDDocumentPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := PublishDIDDocument(dir, "alice.polis.pub"); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(DIDDocumentPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Error("republishing changed the document")
	}
}

func TestNeedsWriteSpotsMissingStaleAndTamperedDocuments(t *testing.T) {
	dir := t.TempDir()
	if _, err := Init(dir, InitOptions{Author: "Alice", BaseURL: "https://alice.polis.pub"}); err != nil {
		t.Fatal(err)
	}

	needs, _, err := DIDDocumentNeedsWrite(dir, "alice.polis.pub")
	if err != nil {
		t.Fatal(err)
	}
	if needs {
		t.Error("a freshly published document reports as needing a write")
	}

	// Tampered.
	if err := os.WriteFile(DIDDocumentPath(dir), []byte(`{"id":"did:web:evil.example"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if needs, _, _ := DIDDocumentNeedsWrite(dir, "alice.polis.pub"); !needs {
		t.Error("a tampered document reports as up to date")
	}

	// Missing.
	if err := os.Remove(DIDDocumentPath(dir)); err != nil {
		t.Fatal(err)
	}
	if needs, _, _ := DIDDocumentNeedsWrite(dir, "alice.polis.pub"); !needs {
		t.Error("a missing document reports as up to date")
	}

	// Stale host — the same site served somewhere else needs a different DID.
	if err := PublishDIDDocument(dir, "alice.polis.pub"); err != nil {
		t.Fatal(err)
	}
	if needs, _, _ := DIDDocumentNeedsWrite(dir, "alice.example"); !needs {
		t.Error("a document for a different host reports as up to date")
	}
}

func TestTheDocumentStatesTheKeyTheWellKnownStates(t *testing.T) {
	dir := t.TempDir()
	if _, err := Init(dir, InitOptions{Author: "Alice", BaseURL: "https://alice.polis.pub"}); err != nil {
		t.Fatal(err)
	}
	wk, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatal(err)
	}
	built, err := BuildDIDDocument(dir, "alice.polis.pub")
	if err != nil {
		t.Fatal(err)
	}
	onDisk, err := os.ReadFile(DIDDocumentPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if string(built) != string(onDisk) {
		t.Error("the published document is not what the site's own key produces")
	}
	if wk.PublicKey == "" {
		t.Fatal("no public key in .well-known/polis")
	}
}

func TestNoDIDPointerIsAddedToTheWellKnown(t *testing.T) {
	// did:web fixes the location; a pointer would be inert and would create a
	// second source of truth for a path nobody can vary.
	dir := t.TempDir()
	if _, err := Init(dir, InitOptions{Author: "Alice", BaseURL: "https://alice.polis.pub"}); err != nil {
		t.Fatal(err)
	}
	raw, err := LoadWellKnownRaw(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"did", "did_document", "did_json"} {
		if _, ok := raw[key]; ok {
			t.Errorf(".well-known/polis gained a %q field", key)
		}
	}
}
