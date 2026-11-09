package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
)

// The owner's API keys: list, mint and revoke. Owner-only, as every /api/
// route is unless the owner gate names it. ⭐ On a remote instance the site's
// store is not on the owner's disk, so this is the only way a key gets made.

// apiKeyView is a key as the owner sees it: never its hash.
type apiKeyView struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	CreatedAt string   `json:"created_at"`
}

func viewOf(k ops.APIKey) apiKeyView {
	scopes := k.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	return apiKeyView{ID: k.ID, Name: k.Name, Scopes: scopes, CreatedAt: k.CreatedAt}
}

// handleAPIKeys serves GET /api/keys (list) and POST /api/keys (mint).
func (s *Server) handleAPIKeys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		keys, err := ops.ListAPIKeys(s.Site())
		if err != nil {
			s.LogError("Failed to list API keys: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "could not read the API keys")
			return
		}
		views := make([]apiKeyView, 0, len(keys))
		for _, k := range keys {
			views = append(views, viewOf(k))
		}
		writeJSON(w, map[string]interface{}{"keys": views})
	case http.MethodPost:
		var req struct {
			Name   string   `json:"name"`
			Scopes []string `json:"scopes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, `invalid request body: expected {"name": "...", "scopes": ["render"]}`)
			return
		}
		// ⛔ No default to full access: a key with no scopes can write and
		// delete signed content and read private types. Ask for it by name.
		if len(req.Scopes) == 0 {
			writeJSONError(w, http.StatusBadRequest, `name the key's scopes: {"scopes": ["render"]}, or {"scopes": ["full"]} for a key that can do everything a key can`)
			return
		}
		scopes := req.Scopes
		if len(scopes) == 1 && scopes[0] == "full" {
			scopes = nil // the stored form of an unscoped (full) key
		}
		token, key, err := ops.MintAPIKey(s.Site(), req.Name, scopes)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.LogEvent("pub.polis.api_key.minted", map[string]interface{}{
			"key_id": key.ID, "key_name": key.Name, "scopes": key.Scopes,
		})
		// ⛔ The plaintext exists only in this response; the file holds its hash.
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"key": viewOf(key), "token": token})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleAPIKey serves DELETE /api/keys/{id} (revoke).
func (s *Server) handleAPIKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/keys/")
	if id == "" || strings.Contains(id, "/") {
		writeJSONError(w, http.StatusBadRequest, "expected /api/keys/{id}")
		return
	}
	switch err := ops.RevokeAPIKey(s.Site(), id); {
	case errors.Is(err, ops.ErrAPIKeyNotFound):
		writeJSONError(w, http.StatusNotFound, "no API key has that id")
	case err != nil:
		s.LogError("Failed to revoke API key %s: %v", id, err)
		writeJSONError(w, http.StatusInternalServerError, "could not revoke the API key")
	default:
		s.LogEvent("pub.polis.api_key.revoked", map[string]interface{}{"key_id": id})
		w.WriteHeader(http.StatusNoContent)
	}
}
