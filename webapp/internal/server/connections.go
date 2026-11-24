package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/connection"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
)

// The owner's connections: every agent holding a grant to act through the
// site's API, by personal token or by signing in, and the switch that ends
// one.
//
// ⭐ The list reads the grants themselves (the signed records), so it shows a
// connection whichever way it was made; the act log adds when it last acted.
// ⛔ Switch off withdraws the grant first, so the connection is ended on the
// record; then any key presenting it is revoked and the host revokes its
// sign-in tokens (ConnectionOff).

// connectionView is one connection as the owner sees it.
type connectionView struct {
	GrantID  string   `json:"grant_id"`
	Grant    string   `json:"grant"`
	Name     string   `json:"name"`
	Client   string   `json:"client"`
	Scopes   []string `json:"scopes"`
	Created  string   `json:"created"`
	LastUsed string   `json:"last_used,omitempty"`
	Live     bool     `json:"live"`
	Off      string   `json:"off,omitempty"`
	// Via is "token" for a personal token, "sign-in" otherwise.
	Via string `json:"via"`
}

// connectionsSettings lists the site's connections, newest first.
func (s *Server) connectionsSettings() []connectionView {
	grants, err := attestation.Grants(s.Site(), "")
	if err != nil {
		return []connectionView{}
	}
	keys, _ := ops.ListAPIKeys(s.Site())
	byGrant := map[string]bool{}
	for _, k := range keys {
		if k.Grant != "" {
			byGrant[k.Grant] = true
		}
	}
	acts, _ := connection.Acts(s.Site(), "")
	last := map[string]string{}
	for _, a := range acts {
		last[a.Grant] = a.Time
	}
	out := []connectionView{}
	for i := len(grants) - 1; i >= 0; i-- {
		g := grants[i]
		if g.Set != connection.Set {
			continue
		}
		v := connectionView{
			GrantID: g.ID, Grant: g.URL, Name: g.Agent,
			Client:  g.Record.Payload[attestation.GrantKeyProvider],
			Scopes:  strings.Fields(g.Record.Payload[attestation.GrantKeyScopes]),
			Created: g.Record.Asserted, LastUsed: last[g.URL],
			Live: !g.Withdrawn && g.Signature == attestation.StatusValid,
			Off:  g.WithdrawnAt, Via: "sign-in",
		}
		if byGrant[g.URL] {
			v.Via = "token"
		}
		out = append(out, v)
	}
	return out
}

// handleConnectionOff serves POST /api/connections/off {"grant_id": "..."}.
func (s *Server) handleConnectionOff(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		GrantID string `json:"grant_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.GrantID) == "" {
		writeJSONError(w, http.StatusBadRequest, `body must be JSON: {"grant_id": "..."}`)
		return
	}
	if s.PrivateKey == nil {
		writeJSONError(w, http.StatusConflict, "this site has no key, so it cannot sign the withdrawal")
		return
	}
	known := false
	for _, c := range s.connectionsSettings() {
		if c.GrantID == req.GrantID {
			known = true
		}
	}
	if !known {
		writeJSONError(w, http.StatusNotFound, "no connection has that grant")
		return
	}
	if err := connection.Withdraw(s.connectionConfig(), req.GrantID); err != nil {
		s.LogError("Failed to switch off connection %s: %v", req.GrantID, err)
		writeJSONError(w, http.StatusInternalServerError, "could not withdraw the connection's grant")
		return
	}
	if keys, err := ops.ListAPIKeys(s.Site()); err == nil {
		for _, k := range keys {
			if k.GrantID == req.GrantID {
				_ = ops.RevokeAPIKey(s.Site(), k.ID)
			}
		}
	}
	if s.ConnectionOff != nil {
		s.ConnectionOff(r.Context(), req.GrantID)
	}
	s.LogEvent("pub.polis.connection.switched_off", map[string]interface{}{"grant_id": req.GrantID})
	writeJSON(w, map[string]interface{}{"connections": s.connectionsSettings()})
}
