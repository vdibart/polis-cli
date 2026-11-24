package server

import (
	"bytes"
	"encoding/json"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"io"
	"net/http"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/apidesc"
	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
	"github.com/vdibart/polis-cli/webapp/internal/api"
)

// The site's settings and facts over the API (enhance's rows that aren't
// owner-only, and the public site's "about").
//
// ⭐ EACH IS THE BROWSER APP'S OWN HANDLER, called in-process after the API
// has checked the token's scope, the way /mcp's tools call them: one
// implementation of each action. Only site.get is new, because no handler
// served the site's public facts.

const sitePath = "/v1/site"

// forward serves a request with one of the app's handlers, at the method and
// path that handler answers, passing the body through.
func forward(h http.HandlerFunc, method, path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.Method = method
		r2.URL.Path = path
		h(w, r2)
	}
}

// forwardWith is forward with a fixed body.
func forwardWith(h http.HandlerFunc, method, path, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.Method = method
		r2.URL.Path = path
		r2.Body = io.NopCloser(bytes.NewReader([]byte(body)))
		r2.ContentLength = int64(len(body))
		h(w, r2)
	}
}

// siteRoutes are the site's settings and facts as API operations.
func (s *Server) siteRoutes() []api.HostRoute {
	str := apidesc.String
	return []api.HostRoute{
		{Op: apidesc.Operation{
			Type: "pub.polis.site", Action: "get", Title: "Read your site's details", Method: "GET", Path: sitePath,
			Summary: "Read the site's public facts: who writes it and how it looks",
			Description: "The site's address, the author's name and avatar, the bio (as markdown and HTML), the " +
				"identity key and its did:web name, when the site began (since), how many sites follow it, the " +
				"active theme and where its terms of use are. Everything here is already public; nothing " +
				"private is included. Public.",
			Auth: apidesc.AuthPublic,
		}, Serve: s.handlePublicSite},
		{Op: apidesc.Operation{
			Type: "pub.polis.site", Action: "set_name", Title: "Change your name", Method: "POST", Path: sitePath + "/name",
			Summary:     "Change the name shown on the owner's posts",
			Description: "Sets the author name the site shows on posts and in its identity document. Not signed.",
			Input:       apidesc.Object(map[string]any{"author_name": str("The name to show.")}, "author_name"),
			Auth:        apidesc.AuthOwner, Scope: apidesc.ScopeProfile, Write: true,
		}, Serve: forward(s.handleUpdateAuthorName, http.MethodPost, "/api/settings/author-name")},
		{Op: apidesc.Operation{
			Type: "pub.polis.site", Action: "set_avatar", Title: "Change your avatar", Method: "POST", Path: sitePath + "/avatar",
			Summary: "Change the owner's avatar",
			Description: "Sets the avatar's style (the same object the site's .well-known/polis carries as avatar), " +
				"or null to go back to the default. The favicon follows.",
			Input: map[string]any{"type": "object", "properties": map[string]any{
				"avatar": map[string]any{"type": []string{"object", "null"}, "description": "The avatar config, or null for the default."},
			}, "required": []string{"avatar"}},
			Auth: apidesc.AuthOwner, Scope: apidesc.ScopeProfile, Write: true,
		}, Serve: forward(s.handleUpdateAvatar, http.MethodPost, "/api/settings/avatar")},
		{Op: apidesc.Operation{
			Type: "pub.polis.site", Action: "set_about", Title: "Change your bio", Method: "POST", Path: sitePath + "/about",
			Summary:     "Change the owner's bio (the About card)",
			Description: "Replaces the bio with new markdown and rebuilds the site so readers see it. Not signed.",
			Input:       apidesc.Object(map[string]any{"content": str("The bio, in markdown.")}, "content"),
			Auth:        apidesc.AuthOwner, Scope: apidesc.ScopeProfile, Write: true,
		}, Serve: forward(s.handleAbout, http.MethodPost, "/api/about")},
		{Op: apidesc.Operation{
			Type: "pub.polis.site", Action: "set_theme", Title: "Switch your theme", Method: "POST", Path: sitePath + "/theme",
			Summary: "Switch the site to another of its themes",
			Description: "Makes one of the site's installed themes (theme.list) the active one and rebuilds the " +
				"site. Reserved system themes are refused.",
			Input: apidesc.Object(map[string]any{"theme": str("The theme's name, e.g. vice.")}, "theme"),
			Auth:  apidesc.AuthOwner, Scope: apidesc.ScopeAppearance, Write: true,
		}, Serve: forward(s.handleThemeSwitch, http.MethodPost, "/api/settings/theme")},
		{Op: apidesc.Operation{
			Type: "pub.polis.theme", Action: "tokens", Title: "Read your site's design", Method: "GET", Path: sitePath + "/tokens",
			Summary: "Read the design's tokens: what can be set, and the site's values",
			Description: "The v5 design's token contract: every token (its name, kind, the values it allows, its " +
				"default and what it changes), the font families a font token may name, the contract's version, " +
				"and the site's values: its own (site_values, layered over the active theme) and what each token " +
				"draws as now (values). Read this before site.set_tokens. Reads only.",
			Auth: apidesc.AuthOwner, Scope: apidesc.ScopeAppearance,
		}, Serve: s.handleThemeTokens},
		{Op: apidesc.Operation{
			Type: "pub.polis.site", Action: "set_tokens", Title: "Restyle your site", Method: "POST", Path: sitePath + "/tokens",
			Summary: "Restyle the site: set some of its design tokens",
			Description: "Sets the site's own values for any tokens theme.tokens lists (colours, fonts, and the " +
				"choices such as texture, rollover, card edge, headings, links and motion), layered over the " +
				"active theme, then rebuilds the site once. Every value is checked, and the result must pass the " +
				"contrast check; a refused change says why and changes nothing. Site-wide only. Answers with the " +
				"values from before (previous): set them with replace true to undo.",
			Input: map[string]any{"type": "object", "properties": map[string]any{
				"tokens":  map[string]any{"type": "object", "description": "Token names to values, e.g. {\"site\": \"#b5562a\", \"font-heading\": \"Lora\"}. theme.tokens lists the names and what each allows.", "additionalProperties": map[string]any{"type": "string"}},
				"replace": map[string]any{"type": "boolean", "description": "true: these become all of the site's own values (the rest go back to the theme's). Default false: only these change."},
			}, "required": []string{"tokens"}},
			Auth: apidesc.AuthOwner, Scope: apidesc.ScopeAppearance, Write: true,
		}, Serve: s.handleSetTokens},
		{Op: apidesc.Operation{
			Type: "pub.polis.site", Action: "reset_tokens", Title: "Undo restyling", Method: "POST", Path: sitePath + "/tokens/reset",
			Summary: "Put some or all of the site's design tokens back to the theme's",
			Description: "Removes the site's own values for the named tokens (or all of them, with all true), so " +
				"they draw as the active theme has them, then rebuilds the site once. Answers with the values " +
				"from before (previous).",
			Input: map[string]any{"type": "object", "properties": map[string]any{
				"tokens": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "The token names to put back."},
				"all":    map[string]any{"type": "boolean", "description": "true: put every token back to the theme's."},
			}},
			Auth: apidesc.AuthOwner, Scope: apidesc.ScopeAppearance, Write: true,
		}, Serve: s.handleResetTokens},
		{Op: apidesc.Operation{
			Type: "pub.polis.site", Action: "rebuild", Title: "Rebuild your site", Method: "POST", Path: sitePath + "/rebuild",
			Summary: "Rebuild every page of the site",
			Description: "Renders every page again from the site's sources, for when something looks stale. " +
				"Changes no source and signs nothing.",
			Auth: apidesc.AuthOwner, Scope: apidesc.ScopeAppearance, Write: true,
		}, Serve: forwardWith(s.handleRenderPage, http.MethodPost, "/api/render-page", `{"path":"/"}`)},
		{Op: apidesc.Operation{
			Type: "pub.polis.site", Action: "status", Title: "Check how your site is running", Method: "GET", Path: sitePath + "/status",
			Summary: "Read how the site is running",
			Description: "Whether the site is listed with the discovery service, and the state of its last " +
				"publish (live, publishing, or failed and why). Reads only.",
			Auth: apidesc.AuthOwner, Scope: apidesc.ScopeStatus,
		}, Serve: s.handleSiteStatus},
		{Op: apidesc.Operation{
			Type: "pub.polis.site", Action: "validate", Title: "Check your site's files", Method: "GET", Path: sitePath + "/validate",
			Summary: "Check that the site's directory is a valid polis site",
			Description: "Whether the site's own files are a valid polis site (valid, not_found, incomplete or " +
				"invalid), with each problem found and how to fix it. Reads only.",
			Auth: apidesc.AuthOwner, Scope: apidesc.ScopeStatus,
		}, Serve: forward(s.handleValidate, http.MethodGet, "/api/validate")},
		{Op: apidesc.Operation{
			Type: "pub.polis.site", Action: "register", Title: "List your site with the discovery service", Method: "POST", Path: sitePath + "/register",
			Summary: "Register the site with the discovery service, so others can find and follow it",
			Description: "Sends the site's signed registration to its discovery service and re-announces its " +
				"follows, each signed. Owner only. A site already registered is registered again.",
			Auth: apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true, Write: true, Signs: true,
		}, Serve: forward(s.handleSiteRegister, http.MethodPost, "/api/site/register")},
		{Op: apidesc.Operation{
			Type: "pub.polis.site", Action: "unregister", Title: "Remove your site from the discovery service", Method: "POST", Path: sitePath + "/unregister",
			Summary: "Remove the site from the discovery service",
			Description: "Asks the discovery service, signed, to stop listing the site. Owner only. A site hosted " +
				"on polis.pub cannot unregister.",
			Auth: apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true, Write: true, Signs: true, Destructive: true,
		}, Serve: forward(s.handleSiteUnregister, http.MethodPost, "/api/site/unregister")},
		{Op: apidesc.Operation{
			Type: "pub.polis.site", Action: "policy", Title: "Read who may comment and message", Method: "GET", Path: sitePath + "/policy",
			Summary: "Read the owner's rules for who may comment, message and be blessed",
			Description: "The owner's inbound rules, private then public, each in order: the private rules are " +
				"evaluated first, then the public, and the first rule that matches decides. Each rule is a " +
				"sentence such as \"allow pub.polis.dm from following\" (verbs: allow, deny, bless, review). " +
				"Reads only.",
			Auth: apidesc.AuthOwner, Scope: apidesc.ScopePolicyRead,
		}, Serve: s.handleSitePolicy},
		{Op: apidesc.Operation{
			Type: "pub.polis.site", Action: "policy_tighten", Title: "Refuse someone or something", Method: "POST", Path: sitePath + "/policy/tighten",
			Summary: "Add a rule that refuses something, ahead of the owner's rules",
			Description: "Adds one deny rule at the top of the owner's private rules, so it decides before " +
				"anything else: e.g. \"deny pub.polis.comment from all at spam.example\". It can only " +
				"tighten: allow, bless and review rules are refused (review is weaker than deny, so it could " +
				"undo an owner's deny), and the owner's own rules are never changed. Undo it with policy_release.",
			Input: apidesc.Object(map[string]any{"rule": str("One deny rule.")}, "rule"),
			Auth:  apidesc.AuthOwner, Scope: apidesc.ScopePolicyWrite, Write: true,
		}, Serve: s.handlePolicyTighten},
		{Op: apidesc.Operation{
			Type: "pub.polis.site", Action: "policy_release", Title: "Remove a rule you added", Method: "POST", Path: sitePath + "/policy/release",
			Summary: "Remove a rule that was added with policy_tighten",
			Description: "Removes one rule that policy_tighten added, given exactly as it was added. It cannot " +
				"remove the owner's own rules: removing a rule can loosen what the owner decided, so only the " +
				"owner does that.",
			Input: apidesc.Object(map[string]any{"rule": str("The rule, exactly as it was added.")}, "rule"),
			Auth:  apidesc.AuthOwner, Scope: apidesc.ScopePolicyWrite, Write: true,
		}, Serve: s.handlePolicyRelease},
	}
}

// messageRoutes are what the owner's own app needs to start a conversation
// and send a message it sealed itself. ⛔ All need the owner's own token: no
// agent scope reaches messages in this version (reading is the owner's alone,
// sending comes later).
func (s *Server) messageRoutes() []api.HostRoute {
	const base = "/v1/messages"
	return []api.HostRoute{
		{Op: apidesc.Operation{
			Type: "pub.polis.dm", Action: "recipients", Method: "GET", Path: base + "/recipients",
			Summary: "List who the owner can message, and why not where they can't",
			Description: "Every site the owner follows, each with whether a message can be sent to it now " +
				"(open), or why not (no-dm: it accepts no messages from the owner; no-follow: it doesn't " +
				"follow the owner back; unknown). Needs the owner's own token.",
			Auth: apidesc.AuthOwner, Scope: apidesc.ScopeFull,
		}, Serve: forward(s.handleDMRecipients, http.MethodGet, "/api/dm/recipients")},
		{Op: apidesc.Operation{
			Type: "pub.polis.dm", Action: "recipient_key", Method: "GET", Path: base + "/recipient-key",
			Summary: "Fetch a recipient's published messages key, to seal a message to it",
			Description: "The recipient site's current messages key and epoch, read from its own identity " +
				"document, given recipient_url in the query. The owner's app seals the message itself. Needs " +
				"the owner's own token.",
			QueryParams: map[string]string{"recipient_url": "The recipient site's address, e.g. https://alice.polis.pub."}, RequiredQuery: []string{"recipient_url"},
			Auth: apidesc.AuthOwner, Scope: apidesc.ScopeFull,
		}, Serve: forward(s.handleDMRecipientKey, http.MethodGet, "/api/dm/recipient-key")},
		{Op: apidesc.Operation{
			Type: "pub.polis.dm", Action: "recipient_protection", Method: "GET", Path: base + "/protection-status",
			Summary: "Ask whether a recipient's messages are password-protected",
			Description: "Whether the recipient (recipient_url in the query) keeps its messages under a " +
				"password, so the owner's app can say so before sending. Needs the owner's own token.",
			QueryParams: map[string]string{"recipient_url": "The recipient site's address, e.g. https://alice.polis.pub."}, RequiredQuery: []string{"recipient_url"},
			Auth: apidesc.AuthOwner, Scope: apidesc.ScopeFull,
		}, Serve: forward(s.handleDMProtectionStatus, http.MethodGet, "/api/dm/protection-status")},
		{Op: apidesc.Operation{
			Type: "pub.polis.dm", Action: "send_sealed", Method: "POST", Path: base + "/send-sealed",
			Summary: "Send a message the owner's app has already sealed",
			Description: "Delivers a message sealed in the owner's browser to the recipient's messages key " +
				"(the site never sees its text), signed as this site. Only to a site that follows the owner " +
				"back. Needs the owner's own token. Which send: this one is for a client that seals the message " +
				"itself; dm.send seals it on the site from the text. A message saved but not delivered answers 202 " +
				"with a warning, as dm.send does.",
			Input: map[string]any{"type": "object", "description": "The sealed envelope, as the owner's app builds it: " +
				"recipient_url, sender_epoch, recipient_epoch, box_pub, and the sealed boxes (ciphertext, nonce)."},
			Auth: apidesc.AuthOwner, Scope: apidesc.ScopeFull, Write: true, Signs: true,
		}, Serve: forward(s.handleDMSendSealed, http.MethodPost, "/api/dm/send-sealed")},
	}
}

// policyAddedBy marks the rules added over the API: the grant's URL for an
// agent's rule (so it is marked, and only that grant can release it), "api"
// for the owner's own full token.
func policyAddedBy(r *http.Request) string {
	if live := ops.GrantFrom(r.Context()); live != nil {
		return live.URL()
	}
	return "api"
}

func (s *Server) handlePolicyTighten(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Rule string `json:"rule"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Rule) == "" {
		writeJSONError(w, http.StatusBadRequest, `body must be JSON: {"rule": "deny … "}`)
		return
	}
	if err := policy.TightenPrivate(s.Site(), req.Rule, policyAddedBy(r)); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.LogEvent("pub.polis.policy.tightened", map[string]interface{}{"rule": strings.TrimSpace(req.Rule)})
	s.handleSitePolicy(w, r)
}

func (s *Server) handlePolicyRelease(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Rule string `json:"rule"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Rule) == "" {
		writeJSONError(w, http.StatusBadRequest, `body must be JSON: {"rule": "…"}`)
		return
	}
	removed, err := policy.RemoveAdded(s.Site(), req.Rule, policyAddedBy(r))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not change the rules")
		return
	}
	if !removed {
		writeJSONError(w, http.StatusNotFound, "no rule like that was added over the API; the owner's own rules can only be changed by the owner")
		return
	}
	s.LogEvent("pub.polis.policy.released", map[string]interface{}{"rule": strings.TrimSpace(req.Rule)})
	s.handleSitePolicy(w, r)
}

// handleSitePolicy serves site.policy: the owner's rules, private then public.
func (s *Server) handleSitePolicy(w http.ResponseWriter, r *http.Request) {
	private, public, err := policy.LoadPolicyFiles(s.Site())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not read the rules")
		return
	}
	// The file's header line loads as an empty rule; it isn't one.
	rules := func(in []policy.Policy) []policy.Policy {
		out := []policy.Policy{}
		for _, p := range in {
			if strings.TrimSpace(p.Rule) != "" {
				out = append(out, p)
			}
		}
		return out
	}
	private, public = rules(private), rules(public)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"order":   "private first, then public; the first rule that matches decides",
		"private": private,
		"public":  public,
	})
}

// handlePublicSite serves site.get: the site's public facts, each one already
// published by the site itself.
func (s *Server) handlePublicSite(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"base_url": s.GetBaseURL()}
	if wk, err := site.LoadWellKnown(s.Site()); err == nil && wk != nil {
		out["author_name"] = wk.AuthorName
		out["avatar"] = wk.Avatar
		out["public_key"] = strings.TrimSpace(wk.PublicKey)
		if wk.Created != "" {
			out["since"] = wk.Created
		}
	}
	// The number of followers, from the site's own synced follower state.
	// ⭐ The number only: who follows is public elsewhere, but the About
	// card asks for a count.
	var followers stream.FollowerState
	if err := stream.NewStore(s.Site(), s.GetDiscoveryDomain(), "pub.polis.core").LoadState("pub.polis.follow", &followers); err == nil {
		out["followers"] = followers.Count
	}
	if domain := discovery.ExtractDomainFromURL(s.GetBaseURL()); domain != "" {
		out["did"] = "did:web:" + domain
	}
	if name, _ := bundle.GetActiveThemeName(s.Site()); name != "" {
		out["theme"] = name
	}
	if p := site.LicensePointer(s.Site()); p != "" {
		out["terms"] = p
	}
	if about, err := s.invoke(r.Context(), s.handleAbout, http.MethodGet, "/api/about", nil); err == nil {
		out["about"] = about["content"]
		out["about_html"] = about["content_html"]
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// handleSiteStatus serves site.status: the discovery listing and the publish
// state, from the app's own two handlers.
func (s *Server) handleSiteStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{}
	if reg, err := s.invoke(r.Context(), s.handleSiteRegistrationStatus, http.MethodGet, "/api/site/registration-status", nil); err == nil {
		out["directory"] = reg
	} else {
		out["directory"] = map[string]any{"error": err.Error()}
	}
	if dep, err := s.invoke(r.Context(), s.handleDeployStatus, http.MethodGet, "/api/deploy-status", nil); err == nil {
		out["publish"] = dep
	} else {
		out["publish"] = map[string]any{"error": err.Error()}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
