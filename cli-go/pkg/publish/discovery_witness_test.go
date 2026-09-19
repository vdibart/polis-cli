// SIGNET epic 32 — a post registration binds the WHOLE signed artifact, and the
// DS's witness is published by the site.

package publish

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

const witnessTestPost = `---
title: Hello
published: 2026-09-13T12:00:00Z
generator: polis-cli-go/test
current-version: sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef
signature: AAAA
---

Hello, witness.
`

func witnessDS(t *testing.T, withWitness bool, gotMetadata *map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req discovery.ContentRegisterRequest
		json.NewDecoder(r.Body).Decode(&req)
		*gotMetadata = req.Metadata
		resp := map[string]interface{}{"success": true, "status": "created", "type": req.Type, "url": req.URL}
		if withWitness {
			hash, _ := req.Metadata[discovery.MetadataArtifactHash].(string)
			resp["witness"] = discovery.Witness{
				Action: discovery.WitnessActionContent, Type: req.Type, URL: req.URL,
				Version: req.Version, Author: req.Author, Actor: "alice.example",
				PublicKey: "ssh-ed25519 AAAA", ArtifactHash: hash,
				DS: "https://ds.polis.pub", DSKeyID: "ds-primary",
				WitnessedAt: "2026-09-13T12:00:01.000Z", Signature: "ds-sig",
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

func writeWitnessTestPost(t *testing.T, dataDir string) *PublishResult {
	t.Helper()
	rel := "content/pub.polis.core/post/20260913/hello.md"
	path := filepath.Join(dataDir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(witnessTestPost), 0644); err != nil {
		t.Fatal(err)
	}
	return &PublishResult{
		Success: true,
		Path:    rel,
		Title:   "Hello",
		Version: "sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
	}
}

func TestRegisterPost_BindsTheWholeSignedArtifactAndPublishesTheWitness(t *testing.T) {
	var metadata map[string]interface{}
	server := witnessDS(t, true, &metadata)
	defer server.Close()

	dataDir, privKey := setupDataDir(t, server.URL)
	result := writeWitnessTestPost(t, dataDir)
	cfg := &DiscoveryConfig{DiscoveryURL: server.URL, BaseURL: "https://alice.example"}

	if err := RegisterPost(dataDir, result, privKey, cfg); err != nil {
		t.Fatal(err)
	}

	want := discovery.ArtifactHash(signing.MarkdownSigningBase(witnessTestPost, signing.TypePost))
	if got := metadata[discovery.MetadataArtifactHash]; got != want {
		t.Fatalf("metadata.artifact_hash = %v, want the hash of the post's signing base %s", got, want)
	}

	postURL := "https://alice.example/" + result.Path
	f, err := site.LoadWitnesses(dataDir)
	if err != nil || f == nil {
		t.Fatalf("expected a published witness file: %v", err)
	}
	set := f.For(postURL)
	if len(set) != 1 || set[0].ArtifactHash != want {
		t.Fatalf("expected the DS's witness published under %s, got %+v", postURL, f.Witnesses)
	}
}

func TestRegisterPost_ADSThatDoesNotWitnessChangesNothingOnTheSite(t *testing.T) {
	var metadata map[string]interface{}
	server := witnessDS(t, false, &metadata)
	defer server.Close()

	dataDir, privKey := setupDataDir(t, server.URL)
	result := writeWitnessTestPost(t, dataDir)
	cfg := &DiscoveryConfig{DiscoveryURL: server.URL, BaseURL: "https://alice.example"}

	if err := RegisterPost(dataDir, result, privKey, cfg); err != nil {
		t.Fatalf("registration must succeed without a witness (D1): %v", err)
	}
	if site.WitnessesPointer(dataDir) != "" {
		t.Fatal("no witness returned, so the site must publish no witnesses pointer")
	}
}
