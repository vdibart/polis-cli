package server

import (
	"encoding/json"
	"net/http"

	"github.com/vdibart/polis-cli/cli-go/pkg/navshortcut"
	"github.com/vdibart/polis-cli/cli-go/pkg/pql"
)

// handleNavShortcuts serves GET /api/nav-shortcuts (the owner's nav-bar
// shortcuts, plus the icons a shortcut may use and the PQL vocabulary) and
// POST /api/nav-shortcuts {"shortcuts": [...]} (replaces them; an empty list
// removes them all).
// Owner-only, as every /api/ route is.
func (s *Server) handleNavShortcuts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		f, err := navshortcut.Load(s.Site())
		if err != nil {
			s.LogError("nav shortcuts: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "could not read the nav shortcuts")
			return
		}
		writeJSON(w, map[string]interface{}{"shortcuts": f.Shortcuts, "icons": navshortcut.Icons, "pql": pql.Vocabulary()})
	case http.MethodPost:
		var f navshortcut.File
		if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
			writeJSONError(w, http.StatusBadRequest, `invalid request body: expected {"shortcuts": [{"label": "...", "icon": "...", "sentence": "..."}]}`)
			return
		}
		saved, err := navshortcut.Save(s.Site(), f)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.LogEvent("pub.polis.nav.shortcuts_set", map[string]interface{}{"shortcuts": len(saved.Shortcuts)})
		writeJSON(w, map[string]interface{}{"success": true, "shortcuts": saved.Shortcuts})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
