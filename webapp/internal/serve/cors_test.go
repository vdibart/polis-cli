package serve

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The site's discovery documents are read by other people's browsers: the v4
// stream and the comment/follow widgets fetch a stranger's .well-known/polis
// cross-origin, and a browser-side licence reader does the same for
// robots.txt and rsl.xml. did.json carried Access-Control-Allow-Origin: * and
// .well-known/polis — the document every other pointer starts from — did not,
// because it and robots.txt leave the handler through early returns that
// skipped the CORS block (epic 09 E4).
func TestPublicDiscoveryDocumentsAllowCrossOriginReads(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		".well-known/polis":    `{"public_key":"ssh-ed25519 AAAA"}`,
		".well-known/did.json": `{"id":"did:web:alice.polis.pub"}`,
		"robots.txt":           "User-agent: *\n",
		"rsl.xml":              "<rsl/>\n",
	}
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}

	for rel := range files {
		for _, method := range []string{http.MethodGet, http.MethodOptions} {
			w := httptest.NewRecorder()
			ServeTenantPublic(w, httptest.NewRequest(method, "/"+rel, nil), dirStorage{root}, "alice", nil)
			if w.Code != http.StatusOK {
				t.Errorf("%s /%s = %d, want 200", method, rel, w.Code)
				continue
			}
			if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
				t.Errorf("%s /%s: Access-Control-Allow-Origin = %q, want *", method, rel, got)
			}
		}
	}
}

// The allowance is by name: a root file that is not a discovery document gets
// no CORS header just because it sits beside one.
func TestCORSIsNotGrantedToEveryRootFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.xml"), []byte("<x/>"), 0644); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	ServeTenantPublic(w, httptest.NewRequest(http.MethodGet, "/notes.xml", nil), dirStorage{root}, "alice", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /notes.xml = %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("/notes.xml: Access-Control-Allow-Origin = %q, want none", got)
	}
}
