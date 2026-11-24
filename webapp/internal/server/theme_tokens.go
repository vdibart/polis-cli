package server

// The v5 token contract over the API (the v5 spec, part 6, "How the tokens
// are set"): theme.tokens reads it, site.set_tokens and site.reset_tokens
// change the site's own values, layered over the active theme.
//
// ⭐ THREE OPERATIONS, HOWEVER MANY TOKENS. The vocabulary is what
// theme.tokens returns, never part of a schema, so an agent's tools stay
// small and a new token needs no new operation.
//
// ⭐ Each change answers with the site's values from before (previous), so a
// caller can put them back exactly: set_tokens with those values and
// replace: true. enhance's Randomise, then Revert, is built on that.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/theme"
)

// tokensState is what every one of the three answers with: the contract's
// version, and the site's values (its own, and what each token draws as).
func (s *Server) tokensState() map[string]any {
	site := s.Site()
	active, _ := theme.GetActiveTheme(site)
	shape, _ := bundle.GetActiveShapeName(site)
	drawn, withheld, _ := theme.LoadV5SiteTheme(site, active)
	values := map[string]string{}
	for _, t := range theme.V5Tokens {
		values[t.Name] = drawn.Get(t.Name)
	}
	own := map[string]string{}
	if o, err := theme.ReadSiteTokens(site); err == nil {
		own = o.Tokens
	}
	out := map[string]any{
		"contract_version": theme.V5ContractVersion,
		"theme":            active,
		"site_values":      own,
		"values":           values,
		"applies":          shape == "v5",
	}
	if shape != "v5" {
		out["note"] = "This site is not on the v5 design yet, so its values are kept and take effect when it moves."
	}
	if withheld != "" {
		out["withheld"] = withheld
	}
	return out
}

// handleThemeTokens serves theme.tokens: the contract (every token, its kind,
// what it allows and changes), the fonts a font token may name, and the
// site's values.
func (s *Server) handleThemeTokens(w http.ResponseWriter, _ *http.Request) {
	out := s.tokensState()
	out["tokens"] = theme.V5Contract()
	out["fonts"] = theme.V5FontNames()
	out["how"] = "Set values with site.set_tokens ({\"tokens\": {name: value}}), put some or all back to the theme's " +
		"with site.reset_tokens. Colours are hex (#rrggbb). Fonts are a family named in fonts, never an address. " +
		"A combination that fails the contrast check is refused with the reason."
	writeJSON(w, out)
}

// handleSetTokens serves site.set_tokens.
func (s *Server) handleSetTokens(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tokens  map[string]string `json:"tokens"`
		Replace bool              `json:"replace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || (len(req.Tokens) == 0 && !req.Replace) {
		writeJSONError(w, http.StatusBadRequest, `body must be JSON: {"tokens": {"<name>": "<value>", …}}; theme.tokens lists the names`)
		return
	}
	active, _ := theme.GetActiveTheme(s.Site())
	previous, err := theme.SetSiteTokens(s.Site(), active, req.Tokens, req.Replace)
	s.answerTokens(w, "set", previous, err)
}

// handleResetTokens serves site.reset_tokens.
func (s *Server) handleResetTokens(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tokens []string `json:"tokens"`
		All    bool     `json:"all"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || (len(req.Tokens) == 0 && !req.All) {
		writeJSONError(w, http.StatusBadRequest, `body must be JSON: {"tokens": ["<name>", …]} or {"all": true}`)
		return
	}
	if req.All {
		req.Tokens = nil
	}
	active, _ := theme.GetActiveTheme(s.Site())
	previous, err := theme.ResetSiteTokens(s.Site(), active, req.Tokens)
	s.answerTokens(w, "reset", previous, err)
}

// answerTokens finishes a change: refused with every reason, or rebuilt once
// and answered with the values now and before.
func (s *Server) answerTokens(w http.ResponseWriter, what string, previous map[string]string, err error) {
	var refused *theme.V5TokenError
	if errors.As(err, &refused) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":   "refused: " + strings.Join(refused.Reasons, "; "),
			"reasons": refused.Reasons,
		})
		return
	}
	if err != nil {
		s.LogError("tokens %s: %v", what, err)
		writeJSONError(w, http.StatusInternalServerError, "could not change the site's values")
		return
	}
	// One rebuild per call, however many values changed.
	if rerr := s.RenderSite(); rerr != nil {
		s.LogError("tokens %s: render: %v", what, rerr)
	}
	s.LogEvent("pub.polis.site.tokens_"+what, map[string]interface{}{})
	out := s.tokensState()
	out["previous"] = previous
	writeJSON(w, out)
}
