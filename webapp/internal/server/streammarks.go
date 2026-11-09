package server

import (
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/vdibart/polis-cli/cli-go/pkg/streammark"
)

// The owner's stream marks: rules (author + keywords) that pick out posts in
// the owner's stream. A rule is data; the server evaluates it while building
// the stream, and the app draws the mark. Owner-only, as every /api/ route is.

var htmlTag = regexp.MustCompile(`<[^>]*>`)

// applyStreamMarks sets each post's mark from the owner's rules. A rules file
// that cannot be read marks nothing and is logged: a mark is a convenience,
// never a reason to fail the stream.
func (s *Server) applyStreamMarks(items []streamItemResponse) {
	f, err := streammark.Load(s.Site())
	if err != nil {
		s.LogError("stream marks: %v", err)
		return
	}
	if len(f.Rules) == 0 {
		return
	}
	for i := range items {
		it := &items[i]
		if it.Type != "post" {
			continue
		}
		it.Mark = f.Match(it.AuthorDomain, it.Title, it.Excerpt, htmlTag.ReplaceAllString(it.BodyHTML, " "))
	}
}

// handleStreamMarks serves GET /api/stream-marks (the rules) and POST
// /api/stream-marks {"rules": [...]} (replaces them; an empty list removes
// every mark).
func (s *Server) handleStreamMarks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		f, err := streammark.Load(s.Site())
		if err != nil {
			s.LogError("stream marks: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "could not read the stream marks")
			return
		}
		writeJSON(w, f)
	case http.MethodPost:
		var f streammark.File
		if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
			writeJSONError(w, http.StatusBadRequest, `invalid request body: expected {"rules": [{"author_domain": "...", "any_of": ["..."]}]}`)
			return
		}
		saved, err := streammark.Save(s.Site(), f)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.LogEvent("pub.polis.stream.marks_set", map[string]interface{}{"rules": len(saved.Rules)})
		writeJSON(w, map[string]interface{}{"success": true, "rules": saved.Rules})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
