package server

import (
	"encoding/json"
	"net/http"

	"github.com/vdibart/polis-cli/cli-go/pkg/theme"
)

// handleCustomCSS serves the site's own CSS, layered after the theme on every
// render (theme.CustomCSSKey). GET returns it; POST {"css": "..."} replaces it,
// keeping the version it replaced, and re-renders, as a theme switch does. An
// empty css removes it. Owner-only, like every /api/ route.
func (s *Server) handleCustomCSS(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		css, err := theme.GetCustomCSS(s.Site())
		if err != nil {
			s.LogError("custom css: read failed: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "could not read the custom CSS")
			return
		}
		writeJSON(w, map[string]interface{}{"css": css})
	case http.MethodPost:
		var req struct {
			CSS *string `json:"css"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CSS == nil {
			writeJSONError(w, http.StatusBadRequest, `invalid request body: expected {"css": "..."}`)
			return
		}
		if err := theme.ValidateCustomCSS(*req.CSS); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := theme.SetCustomCSS(s.Site(), *req.CSS); err != nil {
			s.LogError("custom css: write failed: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "could not save the custom CSS")
			return
		}
		// The render writes styles.css, which is where the custom CSS lands.
		if err := s.RenderSite(); err != nil {
			s.LogError("custom css: render site failed: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "the custom CSS is saved but the site did not re-render; re-render to apply it")
			return
		}
		s.LogEvent("pub.polis.site.custom_css", map[string]interface{}{"bytes": len(*req.CSS)})
		writeJSON(w, map[string]interface{}{"success": true, "bytes": len(*req.CSS)})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSitePalette serves GET /api/site/palette: the public site's active
// theme, the custom properties it declares on :root, and those the site's
// own CSS sets over them. ⭐ The app's chrome follows the site's look from this:
// nav-themes.css derives each theme's app palette from that theme's colours,
// so a colour the site's CSS replaced is replaced in the app too.
func (s *Server) handleSitePalette(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	active, err := theme.GetActiveTheme(s.Site())
	if err != nil {
		s.LogError("site palette: active theme: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "could not read the active theme")
		return
	}
	props, err := theme.ThemeProperties(s.Site(), active)
	if err != nil {
		props = nil // a theme without a readable :root has no palette to follow
	}
	css, err := theme.GetCustomCSS(s.Site())
	if err != nil {
		s.LogError("site palette: custom css: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "could not read the custom CSS")
		return
	}
	custom := theme.RootProperties(css)
	if props == nil {
		props = []theme.Property{}
	}
	if custom == nil {
		custom = []theme.Property{}
	}
	writeJSON(w, map[string]interface{}{"theme": active, "properties": props, "custom": custom})
}
