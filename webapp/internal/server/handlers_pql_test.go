package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pqlRequest performs a GET against /pql/<path-tail> with the given Accept
// header and returns the status + decoded body.
func pqlRequest(t *testing.T, s *Server, pathTail, accept string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/pql/"+pathTail, nil)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	w := httptest.NewRecorder()
	s.handleStreamPQL(w, req)
	var resp map[string]interface{}
	if w.Body.Len() > 0 {
		_ = json.NewDecoder(w.Body).Decode(&resp)
	}
	return w.Code, resp
}

func TestStreamPQL_MethodNotAllowed(t *testing.T) {
	s := newConfiguredServer(t)
	req := httptest.NewRequest(http.MethodPost, "/pql/all+posts+by+date", nil)
	w := httptest.NewRecorder()
	s.handleStreamPQL(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestStreamPQL_JSONEnvelope_TenantRelative(t *testing.T) {
	s := newConfiguredServer(t)
	code, body := pqlRequest(t, s, "all+posts+by+date", "application/json")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", code, body)
	}
	if body["version"] != "pql.v1" {
		t.Errorf("version = %v, want pql.v1", body["version"])
	}
	if body["tenant"] != "test-site.polis.pub" {
		t.Errorf("tenant = %v", body["tenant"])
	}
	// query echoes the RESOLVED sentence (explicit scope); url is the short
	// shareable tenant-relative form (clause omitted).
	if body["query"] != "all posts from test-site.polis.pub by date" {
		t.Errorf("query = %v", body["query"])
	}
	if body["url"] != "/pql/all+posts+by+date" {
		t.Errorf("url = %v, want /pql/all+posts+by+date", body["url"])
	}
	parsed, _ := body["parsed"].(map[string]interface{})
	if parsed["type"] != "posts" || parsed["relation"] != "from" {
		t.Errorf("parsed = %v", parsed)
	}
	// tenant-relative: omitted clause defaults scope to the serving tenant
	if parsed["scope"] != "@test-site.polis.pub" {
		t.Errorf("parsed.scope = %v, want @test-site.polis.pub", parsed["scope"])
	}
	if _, ok := body["items"]; !ok {
		t.Errorf("missing items")
	}
	if _, ok := body["pagination"].(map[string]interface{}); !ok {
		t.Errorf("missing pagination object: %v", body["pagination"])
	}
}

func TestStreamPQL_FormatJSONOverride(t *testing.T) {
	s := newConfiguredServer(t)
	// No Accept header — ?format=json forces the JSON branch.
	req := httptest.NewRequest(http.MethodGet, "/pql/all+posts+by+date?format=json", nil)
	w := httptest.NewRecorder()
	s.handleStreamPQL(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&body)
	if body["version"] != "pql.v1" {
		t.Errorf("expected JSON envelope, got %v", body)
	}
}

// htmlShellRequest hits the HTML branch (no Accept: application/json).
func htmlShellRequest(t *testing.T, s *Server, pathTail string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/pql/"+pathTail, nil)
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	s.handleStreamPQL(w, req)
	return w.Code, w.Body.String()
}

func TestStreamPQL_HTMLShell_SeedsNonDefault(t *testing.T) {
	s := newConfiguredServer(t)
	// Write a minimal rendered shell the HTML branch can serve.
	if err := os.WriteFile(filepath.Join(s.DataDir, "index.html"),
		[]byte("<html><head><title>x</title></head><body>stream</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Non-default (comments) → shell carries the injected seed.
	code, body := htmlShellRequest(t, s, "all+comments+by+date")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if !strings.Contains(body, "window.__POLIS_INITIAL_PQL") {
		t.Errorf("expected seed injection for comments view, body: %s", body)
	}
	if !strings.Contains(body, "\"type\":\"comments\"") {
		t.Errorf("expected type=comments in seed, body: %s", body)
	}

	// Default (posts/by-date at tenant scope) → no seed (the shell IS that view).
	code2, body2 := htmlShellRequest(t, s, "all+posts+by+date")
	if code2 != http.StatusOK {
		t.Fatalf("expected 200, got %d", code2)
	}
	if strings.Contains(body2, "__POLIS_INITIAL_PQL") {
		t.Errorf("default posts view must not inject a seed, body: %s", body2)
	}
}

func TestStreamPQL_HTMLShell_InjectsBaseHref(t *testing.T) {
	s := newConfiguredServer(t)
	// Shell with a bare relative stylesheet ref — exactly what breaks under
	// /pql/<sentence> without a <base>.
	if err := os.WriteFile(filepath.Join(s.DataDir, "index.html"),
		[]byte(`<html><head><link rel="stylesheet" href="base.css"></head><body>stream</body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Both the default and a seeded view must carry the <base> so relative
	// assets resolve against the site root, not /pql/.
	for _, tail := range []string{"all+posts+by+date", "all+comments+by+date"} {
		code, body := htmlShellRequest(t, s, tail)
		if code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", tail, code)
		}
		if !strings.Contains(body, `<base href="/">`) {
			t.Errorf("%s: expected <base href=\"/\"> injected, body: %s", tail, body)
		}
		// The base must precede the relative <link> so the browser applies it.
		if strings.Index(body, `<base href="/">`) > strings.Index(body, `href="base.css"`) {
			t.Errorf("%s: <base> must precede relative <link>", tail)
		}
	}
}

func TestStreamPQL_HTMLShell_404WhenNoShell(t *testing.T) {
	s := newConfiguredServer(t) // no index.html written
	code, _ := htmlShellRequest(t, s, "all+posts+by+date")
	if code != http.StatusNotFound {
		t.Errorf("expected 404 when no rendered shell exists, got %d", code)
	}
}

// TestTenantRoot_HTMLRedirectsToPQL covers the public-surface landing redirect
// (reader mode / hosted serve.ServeTenantPublic): a browser hitting the bare
// tenant root is sent to the canonical PQL URL; non-HTML clients are not.
func TestTenantRoot_HTMLRedirectsToPQL(t *testing.T) {
	s := newConfiguredServer(t)
	h := tenantStaticHandler(s)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("expected 302 for HTML root, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/pql/all+posts+by+date" {
		t.Errorf("Location = %q, want /pql/all+posts+by+date", loc)
	}

	// Non-HTML client → no redirect (serves index.html, or 404 if absent here).
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	w2 := httptest.NewRecorder()
	h(w2, req2)
	if w2.Code == http.StatusFound {
		t.Errorf("non-HTML root must not redirect")
	}

	// Explicit /index.html (HTML GET) → same canonical PQL redirect.
	req3 := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	req3.Header.Set("Accept", "text/html")
	w3 := httptest.NewRecorder()
	h(w3, req3)
	if w3.Code != http.StatusFound {
		t.Fatalf("expected 302 for HTML /index.html, got %d", w3.Code)
	}
	if loc := w3.Header().Get("Location"); loc != "/pql/all+posts+by+date" {
		t.Errorf("/index.html Location = %q, want /pql/all+posts+by+date", loc)
	}

	// Non-HTML /index.html → no redirect (serves the file).
	req4 := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	w4 := httptest.NewRecorder()
	h(w4, req4)
	if w4.Code == http.StatusFound {
		t.Errorf("non-HTML /index.html must not redirect")
	}
}

func TestStreamPQL_Malformed(t *testing.T) {
	s := newConfiguredServer(t)
	code, body := pqlRequest(t, s, "all+from+my+network", "application/json") // missing type
	if code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", code)
	}
	if body["error"] == nil {
		t.Errorf("expected error message, got %v", body)
	}
}

func TestStreamPQL_AboutRelationRejectedOnTenant(t *testing.T) {
	s := newConfiguredServer(t)
	code, body := pqlRequest(t, s, "all+activity+about+me", "application/json")
	if code != http.StatusBadRequest {
		t.Errorf("expected 400 for about relation, got %d (%v)", code, body)
	}
}

func TestStreamPQL_FQEventTypeRejectedOnTenant(t *testing.T) {
	s := newConfiguredServer(t)
	code, body := pqlRequest(t, s, "all+pub.polis.follow.announced+from+alice.polis.pub", "application/json")
	if code != http.StatusBadRequest {
		t.Errorf("expected 400 for FQ event type, got %d (%v)", code, body)
	}
}

func TestStreamPQL_ExplicitScopePassesThrough(t *testing.T) {
	s := newConfiguredServer(t)
	code, body := pqlRequest(t, s, "all+comments+from+all+polis+to+bless", "application/json")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", code, body)
	}
	parsed, _ := body["parsed"].(map[string]interface{})
	if parsed["scope"] != "all-polis" || parsed["modifier"] != "to-bless" {
		t.Errorf("parsed = %v", parsed)
	}
}

func TestStreamPQL_UnderlyingErrorPassThrough(t *testing.T) {
	s := newConfiguredServer(t)
	// drafts require scope=me; my-network must surface the handler's 400.
	code, body := pqlRequest(t, s, "all+drafts+from+my+network", "application/json")
	if code != http.StatusBadRequest {
		t.Errorf("expected 400 passthrough, got %d (%v)", code, body)
	}
}
