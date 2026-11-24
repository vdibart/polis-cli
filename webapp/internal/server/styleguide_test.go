package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/styleguide"
)

// ⭐ Every instance serves the v5 style guide at the same path, to anyone,
// on the full routes and the reader-only ones alike.
func TestEveryInstanceServesTheStyleGuide(t *testing.T) {
	s := newConfiguredServer(t)
	for name, setup := range map[string]func(*http.ServeMux, *Server){"full": SetupRoutes, "reader": SetupReaderRoutes} {
		mux := http.NewServeMux()
		setup(mux, s)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, styleguide.Path, nil))
		if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Type"), "text/markdown") || w.Body.String() != styleguide.Text() {
			t.Errorf("%s routes: %s = %d %q", name, styleguide.Path, w.Code, w.Header().Get("Content-Type"))
		}
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, styleguide.Path, nil))
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s routes: POST = %d, want 405", name, w.Code)
		}
	}
}
