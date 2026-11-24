package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"io"
	"net/http"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/apidesc"
	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/webapp/internal/api"
)

// The owner-only actions as /v1 operations (v5 spec 1.2, part 5): rotating
// the key, downloading the site, changing the terms, the agents (tokens,
// connections, Rosie), and the messages password.
//
// ⭐ EACH IS THE BROWSER APP'S OWN HANDLER, called after the API has checked
// who is asking, as the site's settings are (site_api.go): one implementation
// of each action, and the private /api/* routes retire at cutover.
//
// ⛔ Every one is OwnerOnly (scope full): no agent's token reaches it, and the
// MCP tools leave it out. ⛔ Some are SignInOnly as well, refused to every
// token, the owner's full-access one included: the download (the archive
// holds the private key), making a token (it would outlive the revocation of
// the token that made it) and rotating the key (Vincent, 2026-11-17); and
// every change to the messages key (setting or changing the password, a
// fresh key, re-wrapping an earlier one), since a leaked token could
// otherwise overwrite the wraps and lock the owner out of their messages for
// good, or swap in a key it knows and read what arrives after. The private
// routes behind them never took a token either.
//
// polis.pub's account actions (the sign-in email, signing out everywhere, and
// there the verified export that stands for the download) are the operator's,
// not the site's, and stay on polis.pub's account routes.

const (
	agentsPath   = "/v1/agents"
	messagesPath = "/v1/messages"
)

// ownerRoutes are the owner-only actions as API operations.
func (s *Server) ownerRoutes() []api.HostRoute {
	str := apidesc.String
	routes := []api.HostRoute{
		{Op: apidesc.Operation{
			Type: "pub.polis.site", Action: "rotate_key", Method: "POST", Path: sitePath + "/key/rotate",
			Summary: "Replace the site's signing key",
			Description: "Makes a new identity key, signs the handover with the old one, records it in the site's " +
				"key history and tells the discovery service. What the old key signed still verifies through the " +
				"history. Owner only, on the owner's own sign-in: no token can do it.",
			Auth: apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true, SignInOnly: true,
			Write: true, Signs: true,
		}, Serve: forward(s.handleRotateKey, http.MethodPost, "/api/rotate-key")},
		{Op: apidesc.Operation{
			Type: "pub.polis.site", Action: "set_policy", Method: "POST", Path: sitePath + "/policy",
			Summary: "Replace the owner's rules for who may comment, message and be blessed",
			Description: "Replaces the public or the private rules with the list given, in order, each a rule " +
				"sentence such as \"review pub.polis.comment from all\". Every rule is checked first: if any " +
				"does not parse, nothing changes and the answer names each, by its place, with why. The " +
				"defaults come back with site.reset_policy. Owner only: an agent only tightens.",
			Input: map[string]any{"type": "object", "properties": map[string]any{
				"which": map[string]any{"type": "string", "enum": []string{"public", "private"}, "description": "public (published at policies/rules.jsonl) or private."},
				"rules": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "The rules, in the order they apply."},
			}, "required": []string{"which", "rules"}},
			Auth: apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true,
			Write: true,
		}, Serve: s.handleSetPolicy},
		{Op: apidesc.Operation{
			Type: "pub.polis.site", Action: "reset_policy", Method: "POST", Path: sitePath + "/policy/reset",
			Summary:     "Put the owner's rules back to the defaults",
			Description: "Restores both rule files, public and private, to the defaults, removing every rule the owner or an agent added. Owner only.",
			Auth:        apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true,
			Write: true, Destructive: true,
		}, Serve: s.handleResetPolicy},
		{Op: apidesc.Operation{
			Type: "pub.polis.license", Action: "set", Method: "POST", Path: sitePath + "/license",
			Summary: "State, change or withdraw the terms others may use the owner's work under",
			Description: "Signs the site's terms of use as one of the profiles (reserved, open), or withdraws " +
				"them (none). Not retroactive: works already published keep the terms they were signed with. " +
				"Owner only: nobody states terms for the owner.",
			Input: apidesc.Object(map[string]any{"profile": str("reserved, open, or none to withdraw the terms.")}, "profile"),
			Auth:  apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true,
			Write: true, Signs: true,
		}, Serve: forward(s.handleSiteLicense, http.MethodPost, "/api/site/license")},

		// ── agents: tokens, connections, Rosie ──
		{Op: apidesc.Operation{
			Type: "pub.polis.agent", Action: "list_tokens", Method: "GET", Path: agentsPath + "/tokens",
			Summary:     "List the site's tokens",
			Description: "Every token made for this site: its id, name, what it may do, when it was made, and for an agent's token the grant it presents. Never the token itself. Owner only.",
			Auth:        apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true,
		}, Serve: forward(s.handleAPIKeys, http.MethodGet, "/api/keys")},
		{Op: apidesc.Operation{
			Type: "pub.polis.agent", Action: "make_token", Method: "POST", Path: agentsPath + "/tokens",
			Summary: "Make a token for a script or an agent",
			Description: "Makes a token with the scopes named (full only by name; there is no default), shown " +
				"once in the answer. Owner only, on the owner's own sign-in: no token can make another.",
			Input: map[string]any{"type": "object", "properties": map[string]any{
				"name":   str("What the token is for."),
				"scopes": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "What it may do, e.g. [\"post\", \"read\"], or [\"full\"]."},
			}, "required": []string{"name", "scopes"}},
			Auth: apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true, SignInOnly: true,
			Write: true, Signs: true,
		}, Serve: forward(s.handleAPIKeys, http.MethodPost, "/api/keys")},
		{Op: apidesc.Operation{
			Type: "pub.polis.agent", Action: "revoke_token", Method: "POST", Path: agentsPath + "/tokens/revoke",
			Summary:     "Revoke a token",
			Description: "Ends a token for good; a token belonging to a connection withdraws that connection's grant first. Owner only.",
			Input:       apidesc.Object(map[string]any{"id": str("The token's id, from agent.list_tokens.")}, "id"),
			Auth:        apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true,
			Write: true, Destructive: true, Signs: true,
		}, Serve: s.handleRevokeToken},
		{Op: apidesc.Operation{
			Type: "pub.polis.agent", Action: "list_connections", Method: "GET", Path: agentsPath + "/connections",
			Summary:     "List the agents connected to the site",
			Description: "Each agent connection, newest first: its name and app, what it may do, when it was made and last used, whether it is live or when it was switched off, whether it uses a token or signs in, and its grant. Owner only.",
			Auth:        apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true,
		}, Serve: s.handleListConnections},
		{Op: apidesc.Operation{
			Type: "pub.polis.agent", Action: "disconnect", Method: "POST", Path: agentsPath + "/connections/disconnect",
			Summary: "Disconnect an agent, for good",
			Description: "Withdraws the connection's grant, signed, and ends its tokens. Permanent: using the agent " +
				"again means connecting it again, which makes a new connection. Owner only.",
			Input: apidesc.Object(map[string]any{"grant_id": str("The connection's grant, from agent.list_connections.")}, "grant_id"),
			Auth:  apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true,
			Write: true, Destructive: true, Signs: true,
		}, Serve: forward(s.handleConnectionOff, http.MethodPost, "/api/connections/off")},
		{Op: apidesc.Operation{
			Type: "pub.polis.agent", Action: "rosie", Method: "GET", Path: agentsPath + "/rosie",
			Summary:     "Read whether Rosie, the site's own agent, is on",
			Description: "Whether Rosie is on, whether she has never been switched on, since when she is off, and the history of her grant. Owner only.",
			Auth:        apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true,
		}, Serve: forward(s.handleRosieSettings, http.MethodGet, "/api/settings/rosie")},
		{Op: apidesc.Operation{
			Type: "pub.polis.agent", Action: "set_rosie", Method: "POST", Path: agentsPath + "/rosie",
			Summary:     "Switch Rosie on or off",
			Description: "The owner's one switch for Rosie: switching her on issues her grant, switching her off withdraws it, signed either way. Owner only: an agent never widens its own permissions.",
			Input:       map[string]any{"type": "object", "properties": map[string]any{"on": map[string]any{"type": "boolean", "description": "true to switch her on, false to switch her off."}}, "required": []string{"on"}},
			Auth:        apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true,
			Write: true, Signs: true,
		}, Serve: forward(s.handleRosieSettings, http.MethodPost, "/api/settings/rosie")},

		// ── the messages password: the browser does the cryptography, and
		// the site never receives the password ──
		{Op: apidesc.Operation{
			Type: "pub.polis.dm", Action: "keyring", Method: "GET", Path: messagesPath + "/keyring",
			Summary: "Read the messages keyring the owner's browser unlocks",
			Description: "The messages keys' public halves and their wrapped private halves, with what the " +
				"browser needs to unwrap them with the owner's password. Never the site's own bootstrap key. " +
				"Owner only.",
			Auth: apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true,
		}, Serve: forward(s.handleDMKeyring, http.MethodGet, "/api/dm/keyring")},
		{Op: apidesc.Operation{
			Type: "pub.polis.dm", Action: "set_password", Method: "POST", Path: messagesPath + "/password",
			Summary: "Put the messages under a password, the first time",
			Description: "Takes the new messages key and its private half wrapped twice (by the password and by " +
				"the recovery phrase), all made in the owner's browser, and publishes the new key, signed. The " +
				"site never sees the password or the phrase. Owner only.",
			Input: map[string]any{"type": "object", "description": "As the owner's app builds it: public_key_messages, the wrapped key and its KDF parameters, the recovery wrap and its KDF parameters, and the keyring's revision."},
			Auth:  apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true, SignInOnly: true,
			Write: true, Signs: true,
		}, Serve: forward(s.handleDMSetPassword, http.MethodPost, "/api/dm/password")},
		{Op: apidesc.Operation{
			Type: "pub.polis.dm", Action: "change_password", Method: "POST", Path: messagesPath + "/password/change",
			Summary: "Change the messages password, or make a new recovery phrase",
			Description: "Takes the same key wrapped again in the owner's browser, under the new password or " +
				"phrase. The key itself is unchanged, so nothing is published. Owner only.",
			Input: map[string]any{"type": "object", "description": "As the owner's app builds it: wrapped_dek and kdf, and/or wrapped_dek_recovery and recovery_kdf."},
			Auth:  apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true, SignInOnly: true,
			Write: true,
		}, Serve: forward(s.handleDMRewrapPassword, http.MethodPost, "/api/dm/password/rewrap")},
		{Op: apidesc.Operation{
			Type: "pub.polis.dm", Action: "new_key", Method: "POST", Path: messagesPath + "/key/new",
			Summary: "Start a fresh messages key, when the password and the recovery phrase are both lost",
			Description: "Takes a new messages key and its private half wrapped twice (by a new password and a new " +
				"recovery phrase), all made in the owner's browser, makes it current and publishes it, signed. " +
				"Earlier messages stay, sealed under their own keys, readable only with the old password or " +
				"phrase; nothing is deleted. Owner only, on the owner's own sign-in: no token can do it.",
			Input: map[string]any{"type": "object", "description": "As the owner's app builds it: public_key_messages, wrapped_dek and kdf, wrapped_dek_recovery and recovery_kdf."},
			Auth:  apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true, SignInOnly: true,
			Write: true, Signs: true,
		}, Serve: s.handleDMNewKey},
		{Op: apidesc.Operation{
			Type: "pub.polis.dm", Action: "rewrap_earlier", Method: "POST", Path: messagesPath + "/key/earlier",
			Summary: "Keep an earlier messages key unlocked under the current password",
			Description: "Takes an earlier key wrapped again in the owner's browser (in practice under the current " +
				"password), so its messages open without the old password. Only that key's password wrap " +
				"changes; its recovery phrase still opens it. Owner only, on the owner's own sign-in.",
			Input: map[string]any{"type": "object", "description": "epoch (the earlier key's id, from dm.keyring), wrapped_dek and kdf."},
			Auth:  apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true, SignInOnly: true,
			Write: true,
		}, Serve: s.handleDMRewrapEarlier},
	}
	// ⛔ On polis.pub the download is the verified export, an account action
	// (requested, then confirmed from the email), so the instance never hands
	// the key over itself: there is no such operation there.
	if s.effectiveMode() != mode.Custodied {
		routes = append(routes, api.HostRoute{Op: apidesc.Operation{
			Type: "pub.polis.site", Action: "download", Method: "GET", Path: sitePath + "/download",
			Summary: "Download the whole site, its key included",
			Description: "A zip of every file of the site, its private key included, so it can run anywhere. " +
				"Whoever holds the archive can sign as the site. Owner only, on the owner's own sign-in: no " +
				"token can do it. Once every ten minutes.",
			Auth: apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true, SignInOnly: true,
		}, Serve: forward(s.handleDownloadSite, http.MethodGet, "/api/download-site")})
	}
	return routes
}

// handleRevokeToken serves agent.revoke_token: the app's own revoke, by the
// id in the body rather than in the path.
func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	body, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(body, &req); err != nil || strings.TrimSpace(req.ID) == "" || strings.Contains(req.ID, "/") {
		writeJSONError(w, http.StatusBadRequest, `body must be JSON: {"id": "..."}`)
		return
	}
	r2 := r.Clone(r.Context())
	r2.Method = http.MethodDelete
	r2.URL.Path = "/api/keys/" + strings.TrimSpace(req.ID)
	r2.Body = io.NopCloser(bytes.NewReader(nil))
	s.handleAPIKey(w, r2)
}

// handleListConnections serves agent.list_connections.
func (s *Server) handleListConnections(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]interface{}{"connections": s.connectionsSettings()})
}

// handleSetPolicy serves site.set_policy, answering with the rules as they
// now stand (site.policy's answer).
func (s *Server) handleSetPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Which string   `json:"which"`
		Rules []string `json:"rules"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || (req.Which != "public" && req.Which != "private") || req.Rules == nil {
		writeJSONError(w, http.StatusBadRequest, `body must be JSON: {"which": "public" or "private", "rules": ["…", …]}`)
		return
	}
	f := policy.PublicFile
	if req.Which == "private" {
		f = policy.PrivateFile
	}
	if err := policy.SetRules(s.Site(), f, req.Rules); err != nil {
		var bad *policy.InvalidRulesError
		if errors.As(err, &bad) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "invalid": bad.Problems})
			return
		}
		s.LogError("set policy: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "could not save the rules just now")
		return
	}
	s.LogEvent("pub.polis.policy.set", map[string]interface{}{"which": req.Which, "rules": len(req.Rules)})
	s.handleSitePolicy(w, r)
}

// handleResetPolicy serves site.reset_policy.
func (s *Server) handleResetPolicy(w http.ResponseWriter, r *http.Request) {
	if err := policy.ResetDefaults(s.Site()); err != nil {
		s.LogError("reset policy: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "could not restore the rules just now")
		return
	}
	s.LogEvent("pub.polis.policy.reset", nil)
	s.handleSitePolicy(w, r)
}
