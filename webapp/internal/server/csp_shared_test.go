package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/csp"
)

// ⭐ A site page carries one policy whoever serves it: this server, or a
// deployment whose static files the provisioner routed with csp.TenantContent.
// So the header this server sends on a page must be that policy, byte for byte.
func TestThePagePolicyIsTheOneProvisioningRoutes(t *testing.T) {
	if CSPTenantContent() != csp.TenantContent() {
		t.Fatal("CSPTenantContent differs from csp.TenantContent: a static page would get another policy")
	}
	h := securityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for _, path := range []string{"/", "/posts/20260101/hello.html"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if got := w.Header().Get("Content-Security-Policy"); got != csp.TenantContent() {
			t.Errorf("%s served with CSP %q, want csp.TenantContent()", path, got)
		}
	}
}
