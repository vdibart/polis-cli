package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// The artifacts a site publishes (SIGNET epic 26), asked for over HTTP from the
// SHIPPED handler.
//
// ⚠️ robots.txt was generated correctly for weeks and refused by this handler;
// every write-side test stayed green because they all asserted the file was
// WRITTEN. Only the real handler, asked for the real artifacts, can fail on the
// thing that broke. So: real files, real ServeTenantPublic. If anyone removes
// robots.txt from rootFiles, or .xml or .json from the extension allowlist, or
// breaks extensionless directory-index resolution, this goes red. (The hosted
// service's served-artifact check runs against this same handler where that
// check lives.)

// alicePolisPub = the host the fixture site is served at.
const fixtureHandle, fixtureBaseDomain = "alice", "polis.pub"

// siteWithEveryArtifact builds a tenant that has stated terms, so ALL FIVE
// artifacts exist — the derived-universal two and the authored-conditional three.
func siteWithEveryArtifact(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	host := fixtureHandle + "." + fixtureBaseDomain
	baseURL := "https://" + host

	privKey, pubKey, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	mkdir(t, root, filepath.Join(".polis", "keys"))
	mkdir(t, root, ".well-known")
	mkdir(t, root, filepath.Join("content", "pub.polis.core"))
	write(t, root, filepath.Join(".polis", "keys", "id_ed25519"), privKey, 0600)
	write(t, root, filepath.Join(".polis", "keys", "id_ed25519.pub"), pubKey, 0644)

	wk, _ := json.MarshalIndent(map[string]interface{}{
		"version":     "pub.polis.site.v1",
		"public_key":  strings.TrimSpace(string(pubKey)),
		"author_name": "Alice",
	}, "", "  ")
	write(t, root, filepath.Join(".well-known", "polis"), wk, 0644)

	if err := bundle.SaveBundle(filepath.Join(root, "content", "pub.polis.core", "bundle.json"), bundle.DefaultCoreBundle()); err != nil {
		t.Fatal(err)
	}

	// Derived-universal: the DID Document, from the key the site already publishes.
	if err := site.PublishDIDDocument(root, host); err != nil {
		t.Fatal(err)
	}

	// Authored-conditional: an author stating terms is what brings the rest
	// into existence.
	if stated, err := site.StateLicense(root, "reserved", baseURL, privKey); err != nil || !stated {
		t.Fatalf("StateLicense: stated=%v err=%v", stated, err)
	}
	robots, rsl, err := site.LicenseProjections(root, baseURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(robots) == 0 || len(rsl) == 0 {
		t.Fatal("stated terms produced no projections")
	}
	write(t, root, "robots.txt", robots, 0644)
	write(t, root, "rsl.xml", rsl, 0644)
	mkdir(t, root, "license")
	write(t, root, filepath.Join("license", "index.html"),
		[]byte("<!doctype html><html><body><h1>Terms of use</h1></body></html>"), 0644)

	return root
}

func mkdir(t *testing.T, root, rel string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, rel), 0755); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, root, rel string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, rel), data, mode); err != nil {
		t.Fatal(err)
	}
}

// shippedHandler serves the fixture through the real ServeTenantPublic.
func shippedHandler(t *testing.T, root string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeTenantPublic(w, r, dirStorage{root}, fixtureHandle, nil)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestEachArtifactIndividually names the surfaces, so a regression says WHICH
// one went dark rather than "something did".
func TestEachArtifactIndividually(t *testing.T) {
	root := siteWithEveryArtifact(t)
	srv := shippedHandler(t, root)

	for _, tc := range []struct{ path, ctype string }{
		{"/.well-known/did.json", "application/json"},
		{"/.well-known/polis", "application/json"},
		{"/robots.txt", "text/plain"},
		{"/rsl.xml", "application/xml"},
		{"/license", "text/html"},
	} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+tc.path, nil)
		req.Host = fixtureHandle + "." + fixtureBaseDomain
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Errorf("GET %s: %v", tc.path, err)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200 — generated and unreachable is the failure this epic exists for", tc.path, resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, tc.ctype) {
			t.Errorf("GET %s Content-Type = %q, want %s", tc.path, got, tc.ctype)
		}
	}
}

// TestWithdrawingTermsRemovesTheirArtifacts: the three authored-conditional
// artifacts go with the terms, and the handler then 404s each (absent is
// correct for a site that states nothing). The hosted service's sweep over the
// same state is asserted where that sweep lives.
func TestWithdrawingTermsRemovesTheirArtifacts(t *testing.T) {
	root := siteWithEveryArtifact(t)
	if err := site.WithdrawLicense(root); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"robots.txt", "rsl.xml", filepath.Join("license", "index.html")} {
		if _, err := os.Stat(filepath.Join(root, rel)); !os.IsNotExist(err) {
			t.Fatalf("%s survived withdrawal — the tenant still publishes terms it no longer states", rel)
		}
	}
	srv := shippedHandler(t, root)
	for _, path := range []string{"/robots.txt", "/rsl.xml", "/license"} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s after withdrawal = %d, want 404", path, resp.StatusCode)
		}
	}
}
