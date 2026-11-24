package server

import (
	"net/http"

	"github.com/vdibart/polis-cli/cli-go/pkg/styleguide"
)

// handleStyleGuide serves the v5 style guide at styleguide.Path, on every
// instance, for an agent changing how a site looks. Public: it is the same
// text as the published docs page and says nothing about this site.
func (s *Server) handleStyleGuide(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ServeStyleGuide(w, r)
}

// ServeStyleGuide writes the guide; polis.pub's own root serves it the same way.
func ServeStyleGuide(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", styleguide.MIMEType)
	w.Header().Set("Cache-Control", "public, max-age=300")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(styleguide.Text()))
}
