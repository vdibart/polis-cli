package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ⛔⛔ The serve handler denied private names on the RAW path, then the store
// TRIMMED whitespace and read the private file: GET /%20.polis/... served the
// DM keyring (with its private server_dek) on the reader surface. The store
// now refuses such a key, so every form below is a 404.
func TestAWhitespacePrefixedPrivatePathIsNotServed(t *testing.T) {
	s := newConfiguredServer(t)
	keyring := ".polis/bundles/pub.polis.core/dm/keyring.json"
	if err := s.Site().Store().Put(context.Background(), keyring, []byte(`{"server_dek":"SECRET-DEK"}`)); err != nil {
		t.Fatal(err)
	}
	h := tenantStaticHandler(s)
	for _, prefix := range []string{"%20", "%09", "%0a", "%C2%A0"} {
		p := "/" + prefix + keyring
		r := httptest.NewRequest(http.MethodGet, p, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		body, _ := io.ReadAll(w.Body)
		if w.Code != http.StatusNotFound || strings.Contains(string(body), "SECRET-DEK") {
			t.Errorf("GET %s → %d %q; want 404 and no key material", p, w.Code, body)
		}
	}
}
