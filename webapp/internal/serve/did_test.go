package serve

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/did"
)

// dirStorage is the smallest Storage that serves a directory. The point of
// these tests is that did.json needs NO routing code — so the handler under
// test is the shipped one, untouched, and only the file source is a stand-in.
type dirStorage struct{ root string }

func (s dirStorage) StatPublicFile(handle, relPath string) (fs.FileInfo, error) {
	return os.Stat(filepath.Join(s.root, relPath))
}

func (s dirStorage) ReadPublicFile(handle, relPath string) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.root, relPath))
}

func (s dirStorage) LoadBundle(handle string) *bundle.Bundle { return nil }

// serveDIDDocument writes a real document into a temp site and asks the shipped
// static handler for it.
func serveDIDDocument(t *testing.T, method string) (*httptest.ResponseRecorder, ed25519.PublicKey) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".well-known"), 0755); err != nil {
		t.Fatal(err)
	}
	seed, _ := hex.DecodeString("9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60")
	pub := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	doc, err := did.Build("alice.polis.pub", pub)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".well-known", "did.json"), doc, 0644); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, "/.well-known/did.json", nil)
	ServeTenantPublic(w, r, dirStorage{root}, "alice", nil)
	return w, pub
}

// TestADIDResolverGetsWhatItNeeds is the whole serving requirement in one
// assertion set: 200, application/json, and the wildcard CORS header a
// browser-based resolver needs to read the document at all.
func TestADIDResolverGetsWhatItNeeds(t *testing.T) {
	w, _ := serveDIDDocument(t, http.MethodGet)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got, want := w.Header().Get("Content-Type"), "application/json; charset=utf-8"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
}

func TestTheServedKeyRoundTripsToTheSitesKey(t *testing.T) {
	w, pub := serveDIDDocument(t, http.MethodGet)

	var doc struct {
		ID                 string `json:"id"`
		VerificationMethod []struct {
			ID           string `json:"id"`
			Type         string `json:"type"`
			Controller   string `json:"controller"`
			PublicKeyJwk struct {
				Kty string `json:"kty"`
				Crv string `json:"crv"`
				X   string `json:"x"`
			} `json:"publicKeyJwk"`
		} `json:"verificationMethod"`
		AssertionMethod []string `json:"assertionMethod"`
		Authentication  []string `json:"authentication"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("the served bytes are not a parseable DID Document: %v", err)
	}

	if doc.ID != "did:web:alice.polis.pub" {
		t.Errorf("id = %q", doc.ID)
	}
	if len(doc.VerificationMethod) != 1 {
		t.Fatalf("verificationMethod has %d entries, want 1", len(doc.VerificationMethod))
	}
	vm := doc.VerificationMethod[0]
	if vm.Type != "JsonWebKey2020" || vm.PublicKeyJwk.Kty != "OKP" || vm.PublicKeyJwk.Crv != "Ed25519" {
		t.Errorf("verification method = %+v", vm)
	}
	back, err := base64.RawURLEncoding.DecodeString(vm.PublicKeyJwk.X)
	if err != nil {
		t.Fatalf("decode x: %v", err)
	}
	if !ed25519.PublicKey(back).Equal(pub) {
		t.Error("the served key does not round-trip to the site's actual key")
	}

	// assertionMethod is what a VC verifier checks when the DID is a
	// credential subject or issuer.
	if len(doc.AssertionMethod) != 1 || doc.AssertionMethod[0] != vm.ID {
		t.Errorf("assertionMethod = %v, want [%s]", doc.AssertionMethod, vm.ID)
	}
	if len(doc.Authentication) != 1 || doc.Authentication[0] != vm.ID {
		t.Errorf("authentication = %v, want [%s]", doc.Authentication, vm.ID)
	}
}

func TestThePreflightIsAnswered(t *testing.T) {
	// Some resolvers preflight before reading. The shipped handler already
	// short-circuits OPTIONS for .well-known; this pins it for did.json.
	w, _ := serveDIDDocument(t, http.MethodOptions)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got != "GET, OPTIONS" {
		t.Errorf("Access-Control-Allow-Methods = %q", got)
	}
}

func TestTheDocumentCarriesNoLicenceHeaders(t *testing.T) {
	// The licence describes terms for the author's WORK. A DID Document is
	// neither authored nor a work — it is a key in a standard encoding — and a
	// Content-Usage header on it would be a claim about the wrong thing.
	w, _ := serveDIDDocument(t, http.MethodGet)

	if got := w.Header().Get("Content-Usage"); got != "" {
		t.Errorf("Content-Usage = %q, want empty", got)
	}
	if got := w.Header().Get("Link"); got != "" {
		t.Errorf("Link = %q, want empty", got)
	}
}
