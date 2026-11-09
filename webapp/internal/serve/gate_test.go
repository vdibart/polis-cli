package serve

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestGate(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("posts/a.html", `<html><head><meta name="content-usage" content="train-ai=n, search=y"></head><body>x</body></html>`)
	write("robots.txt", "User-Agent: *\n")
	write(".well-known/polis", "{}")

	get := func(path string, gate GateFunc) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("User-Agent", "GPTBot/1.2")
		w := httptest.NewRecorder()
		ServeTenantPublicGated(w, req, dirStorage{root}, "", nil, gate)
		return w
	}

	// ⛔ No gate, no refusal. The self-hosted server passes nil, so it never
	// refuses anybody by user agent.
	if w := get("/posts/a", nil); w.Code != http.StatusOK {
		t.Errorf("nil gate: status %d, want 200", w.Code)
	}

	// The gate is handed the resolved path and the terms derived from the
	// bytes about to be served, and its refusal is the whole response.
	var gotClean, gotUsage string
	refuse := func(w http.ResponseWriter, r *http.Request, clean, usage string) bool {
		gotClean, gotUsage = clean, usage
		w.WriteHeader(http.StatusForbidden)
		return true
	}
	w := get("/posts/a", refuse)
	if w.Code != http.StatusForbidden || w.Body.Len() != 0 {
		t.Errorf("refusing gate: status %d, body %q", w.Code, w.Body.String())
	}
	if gotClean != "posts/a.html" || gotUsage != "train-ai=n, search=y" {
		t.Errorf("gate saw clean=%q usage=%q", gotClean, gotUsage)
	}

	// ⛔ robots.txt is served before the gate is consulted: a crawler has to be
	// able to read a site's terms to obey them.
	gotClean = ""
	if w := get("/robots.txt", refuse); w.Code != http.StatusOK || gotClean != "" {
		t.Errorf("robots.txt: status %d, gate consulted for %q", w.Code, gotClean)
	}

	// An extensionless file is offered to the gate too, which decides.
	if w := get("/.well-known/polis", refuse); w.Code != http.StatusForbidden || gotClean != ".well-known/polis" {
		t.Errorf(".well-known/polis: status %d, gate saw %q", w.Code, gotClean)
	}
}
