package server

import (
	"encoding/json"
	"errors"
	"github.com/vdibart/polis-cli/cli-go/pkg/apidesc"
	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/connection"
	"net/http"
	"strings"
	"sync"

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
	// Grant is the URL of the signed grant a personal token presents ("" for
	// the owner's own full key).
	Grant string `json:"grant,omitempty"`
}

func viewOf(k ops.APIKey) apiKeyView {
	scopes := k.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	return apiKeyView{ID: k.ID, Name: k.Name, Scopes: scopes, CreatedAt: k.CreatedAt, Grant: k.Grant}
}

// connectionConfig is the grant-writing configuration for this site, as
// Rosie's is.
func (s *Server) connectionConfig() connection.Config {
	cfg := connection.Config{Site: s.Site(), BaseURL: s.GetBaseURL(), PrivateKey: s.PrivateKey}
	if s.DiscoveryURL != "" {
		cfg.Discovery = &attestation.DiscoveryConfig{
			DiscoveryURL: s.DiscoveryURL,
			BaseURL:      s.GetBaseURL(),
			Site:         s.Site(),
			HTTPClient:   s.SharedHTTPClient,
		}
	}
	return cfg
}

var renewMu sync.Mutex

// renewalAllowed: the request is the site's own (Sec-Fetch-Site same-origin
// or none, as typing the address is) or carries no such header (not sent by
// a browser at all).
func renewalAllowed(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
		return true
	}
	return false
}

// renewKeys gives every scoped key made before keys carried grants a grant of
// its own, now that the owner is here looking at them.
//
// ⭐ The owner opening the keys list is the present person the grant's basis
// (user-signed) says made it. ⛔ Never done for a key nobody is looking at,
// and never for a full key (the owner's own) or one bound to a path (Kay's,
// whose connection the owner made in the Kay page). A scope an agent may not
// hold (tag) is dropped from the renewed key; a key left with none is left as
// it was and still only reads.
//
// ⛔ follow is dropped too, though an agent may now hold it: these keys were
// minted before follow could be granted, so renewing one with follow would
// write a grant for an act the owner never agreed an agent could make marked
// in her name. She grants follow by making a new connection.
func (s *Server) renewKeys(keys []ops.APIKey) []ops.APIKey {
	if s.PrivateKey == nil || s.GetBaseURL() == "" {
		return keys
	}
	// Two lists at once must not both issue a grant for the same key.
	renewMu.Lock()
	defer renewMu.Unlock()
	if fresh, err := ops.ListAPIKeys(s.Site()); err == nil {
		keys = fresh
	}
	cfg := s.connectionConfig()
	for i, k := range keys {
		if k.Unscoped() || k.PersonalToken() || k.Bound != "" {
			continue
		}
		var keep []string
		for _, sc := range k.Scopes {
			if apidesc.AgentGrantable(sc) && sc != apidesc.ScopeFollow {
				keep = append(keep, sc)
			}
		}
		scopes, err := connection.Scopes(keep)
		if err != nil {
			continue
		}
		name := k.Name
		if !connection.ValidName(name) {
			name = "API key " + strings.TrimPrefix(k.ID, "key_")
		}
		issued, err := connection.Issue(cfg, name, name, scopes)
		if err != nil {
			s.LogError("Failed to renew API key %s with a grant: %v", k.ID, err)
			continue
		}
		if err := ops.SetAPIKeyGrant(s.Site(), k.ID, scopes, issued.URL, issued.ID); err != nil {
			s.LogError("Failed to record the grant on API key %s: %v", k.ID, err)
			_ = connection.Withdraw(cfg, issued.ID)
			continue
		}
		keys[i].Scopes, keys[i].Grant, keys[i].GrantID = scopes, issued.URL, issued.ID
		s.LogEvent("pub.polis.api_key.renewed", map[string]interface{}{"key_id": k.ID, "grant": issued.URL, "scopes": scopes})
	}
	return keys
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
		// ⛔ Renewal signs and publishes grants, so a GET that another page can
		// cause (an <img> on a sibling tenant, any page against localhost) must
		// not do it: only a request the browser marks as the site's own, or
		// one no browser sent. Found by the security review, 2026-11-15.
		if renewalAllowed(r) {
			keys = s.renewKeys(keys)
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
		var token string
		var key ops.APIKey
		var err error
		if scopes == nil {
			// The owner's own full key: the owner, unmarked, no grant.
			token, key, err = ops.MintAPIKey(s.Site(), req.Name, nil)
		} else {
			// ⭐ A PERSONAL TOKEN: first the owner's signed grant naming the
			// connection and its scopes, then the key that presents it. ⛔ A
			// scope an agent may not hold (follow, tag) is refused, not dropped.
			if s.PrivateKey == nil || s.GetBaseURL() == "" {
				writeJSONError(w, http.StatusConflict, "this site has no key or address yet, so it cannot sign the grant a scoped key needs")
				return
			}
			clean, serr := connection.Scopes(scopes)
			if serr != nil {
				writeJSONError(w, http.StatusBadRequest, serr.Error()+`; an agent's key can hold any of the vocabulary except follow and tag, or ask for {"scopes": ["full"]} for your own full key`)
				return
			}
			// ⭐ No name: a neutral default, since the name is public.
			if strings.TrimSpace(req.Name) == "" {
				req.Name = connection.DefaultName(s.Site())
			}
			if !connection.ValidName(req.Name) {
				writeJSONError(w, http.StatusBadRequest, "a personal token's name is 1 to 64 letters, digits, spaces, dots, dashes or underscores, starting with a letter or digit (it names the connection on your site)")
				return
			}
			cfg := s.connectionConfig()
			issued, ierr := connection.Issue(cfg, req.Name, req.Name, clean)
			if ierr != nil {
				s.LogError("Failed to issue a grant for a new key: %v", ierr)
				writeJSONError(w, http.StatusInternalServerError, "could not sign the grant for this key")
				return
			}
			token, key, err = ops.MintPersonalToken(s.Site(), req.Name, clean, issued.URL, issued.ID)
			if err != nil {
				_ = connection.Withdraw(cfg, issued.ID)
			}
		}
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.LogEvent("pub.polis.api_key.minted", map[string]interface{}{
			"key_id": key.ID, "key_name": key.Name, "scopes": key.Scopes, "grant": key.Grant,
		})
		// A token made for an agent is a new connection: the owner hears of
		// it outside the app too. Never the owner's own full token.
		if key.Grant != "" {
			s.notifyAgentConnected(AgentConnectedNotice{Name: key.Name, How: HowToken, Scopes: key.Scopes})
		}
		// ⛔ The plaintext exists only in this response; the file holds its hash.
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusCreated)
		resp := map[string]interface{}{"key": viewOf(key), "token": token}
		if key.PersonalToken() {
			resp["warning"] = connection.PublicNameWarning
		}
		_ = json.NewEncoder(w).Encode(resp)
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
	// ⭐ Revoking a personal token withdraws its grant first, so the
	// connection is switched off on the record, not only by deleting a hash.
	if keys, lerr := ops.ListAPIKeys(s.Site()); lerr == nil {
		for _, k := range keys {
			if k.ID == id && k.GrantID != "" && s.PrivateKey != nil {
				if werr := connection.Withdraw(s.connectionConfig(), k.GrantID); werr != nil {
					s.LogError("Failed to withdraw the grant of API key %s: %v", id, werr)
					writeJSONError(w, http.StatusInternalServerError, "could not withdraw this key's grant, so it was not revoked")
					return
				}
			}
		}
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
