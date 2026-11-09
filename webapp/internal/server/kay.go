package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"github.com/vdibart/polis-cli/cli-go/pkg/v0"
)

// Kay: the owner's AI, reached from the Kay page. The instance holds the
// owner's v0 key, registers its own /mcp with v0 under a render-scoped API key
// it mints, and carries the conversation between the page and v0.
//
// ⭐ v0 ACTS ON THE SITE ONLY THROUGH /mcp, under that key's scope. These
// routes carry messages; they grant nothing.
//
// ⛔ THE /api/kay ROUTES RETIRE. They are private routes shaped for one page
// and one provider. What stays: the stored provider key, the self-registration
// and the system prompt, the first draft of Kay's personality. Where an
// owner's conversations with their AI live in the API is still to be decided.

const (
	// kayV0KeyKey holds the owner's v0 API key. ⛔ A secret: mode 600, never
	// rendered back, logged or put in an error.
	kayV0KeyKey = ".polis/kay/v0-key"
	// kayStateKey records the instance's registration with v0. No secret.
	kayStateKey = ".polis/kay/state.json"
	// kayKeyName names the API key Kay's MCP registration holds.
	kayKeyName = "Kay (v0)"
)

// kaySystemPrompt is Kay's personality, first draft.
const kaySystemPrompt = "You are Kay, the agent for one polis site, acting for its owner. Act on the " +
	"site only through your tools. Do not generate an app, files or a preview. Before you change " +
	"anything, read the site with get_site. For a visual change, write CSS that uses the active " +
	"theme's custom properties; if you change the background, change the text colours with it so " +
	"everything stays readable. Say what you changed in one or two sentences. After a write, " +
	"check get_publish_status and say when it's live."

type kayState struct {
	MCPServerID  string `json:"mcp_server_id,omitempty"`
	MCPURL       string `json:"mcp_url,omitempty"`
	APIKeyID     string `json:"api_key_id,omitempty"`
	RegisteredAt string `json:"registered_at,omitempty"`
	// Components are point and ask's registrations, one per component's own
	// address (kay_point.go), made with the same key as MCPServerID.
	Components map[string]kayRegistration `json:"components,omitempty"`
}

var kayMu sync.Mutex

func (s *Server) kayClient(ctx context.Context) (*v0.Client, error) {
	raw, err := s.Site().Store().Get(ctx, kayV0KeyKey)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errKayNotConnected
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the v0 key")
	}
	key := strings.TrimSpace(string(raw))
	if key == "" {
		return nil, errKayNotConnected
	}
	return v0.New(key, s.kayV0Base), nil
}

var errKayNotConnected = errors.New("Kay is not connected to v0 yet")

func (s *Server) kayState(ctx context.Context) kayState {
	var st kayState
	if raw, err := s.Site().Store().Get(ctx, kayStateKey); err == nil {
		_ = json.Unmarshal(raw, &st)
	}
	return st
}

func (s *Server) saveKayState(ctx context.Context, st kayState) error {
	b, _ := json.MarshalIndent(st, "", "  ")
	return s.Site().Store().Put(ctx, kayStateKey, append(b, '\n'), storage.WithMode(0o600))
}

func (s *Server) kayMCPURL() string {
	return strings.TrimRight(s.GetBaseURL(), "/") + "/mcp"
}

// ensureKayRegistered returns this instance's registrations with v0,
// registering when there are none, when Kay's key was revoked, or when the
// instance's address changed. ⭐ A registration that is still good is reused,
// never made again per ask.
//
// ⭐ Where point and ask is on, each component's own address is registered
// too, in the same step: the token exists only now (the site keeps its hash),
// so a registration made later would need a second key. An instance
// registered before point and ask registers again, once.
func (s *Server) ensureKayRegistered(ctx context.Context, c *v0.Client) (kayState, error) {
	kayMu.Lock()
	defer kayMu.Unlock()
	if st, ok := s.kayRegistrationCurrent(ctx); ok {
		return st, nil
	}
	// ⛔ Registering mints keys and creates registrations with v0, then
	// records them; two instances doing it at once each record their own and
	// orphan the other's live key. kayMu covers one process only, so the
	// registration is also taken under a lock in the store, and the state is
	// read again once it is held: another instance may have just registered.
	var out kayState
	err := ops.WithStoreLock(ctx, s.Site(), kayLockKey, 2*time.Minute, 90*time.Second, func() error {
		var err error
		out, err = s.registerKayLocked(ctx, c)
		return err
	})
	return out, err
}

// kayLockKey is the store lock around Kay's registration.
const kayLockKey = ".polis/kay/register.lock"

// kayRegistrationCurrent is the recorded registration, when it is still good.
func (s *Server) kayRegistrationCurrent(ctx context.Context) (kayState, bool) {
	st := s.kayState(ctx)
	url := s.kayMCPURL()
	return st, st.MCPServerID != "" && st.MCPURL == url && s.kayKeyCurrent(st.APIKeyID, "/mcp") &&
		(!s.pointAndAsk() || s.kayRegisteredComponents(st))
}

// registerKayLocked registers, unless the registration became good meanwhile.
// ⛔ Only under kayMu and kayLockKey.
func (s *Server) registerKayLocked(ctx context.Context, c *v0.Client) (kayState, error) {
	st := s.kayState(ctx)
	url := s.kayMCPURL()
	if st.MCPServerID != "" && st.MCPURL == url && s.kayKeyCurrent(st.APIKeyID, "/mcp") &&
		(!s.pointAndAsk() || s.kayRegisteredComponents(st)) {
		return st, nil
	}
	if url == "/mcp" || strings.Contains(url, "://localhost") || strings.Contains(url, "://127.0.0.1") {
		return kayState{}, fmt.Errorf("v0 cannot reach this instance at %q: Kay needs the site's public address", url)
	}
	// Replace what is stale: the old registrations, then the old keys.
	s.removeKayRegistrations(ctx, c, st)
	s.revokeKayKeys(st)
	// ⭐ ONE KEY PER ENDPOINT, each bound to it: the whole-site connection's
	// key works only at /mcp, and each component's only at its own address,
	// so a chat limited to one part of the site (whose prompt carries text
	// from other people's posts) cannot replay its key against every tool.
	token, key, err := ops.MintBoundAPIKey(s.Site(), kayKeyName, []string{ops.ScopeRender}, "/mcp")
	if err != nil {
		return kayState{}, fmt.Errorf("could not mint Kay's key: %w", err)
	}
	// ⭐ The token goes to v0 and nowhere else: the site keeps only its hash.
	srv, err := c.CreateMCPServer(ctx, "polis", url,
		"The owner's polis site. Read it, change its theme and its CSS.", token)
	if err != nil {
		_ = ops.RevokeAPIKey(s.Site(), key.ID)
		return kayState{}, fmt.Errorf("could not register this site with v0: %w", err)
	}
	st = kayState{MCPServerID: srv.ID, MCPURL: url, APIKeyID: key.ID, RegisteredAt: time.Now().UTC().Format(time.RFC3339)}
	if s.pointAndAsk() {
		st.Components = map[string]kayRegistration{}
		for _, comp := range kayComponents {
			if !comp.Enabled {
				continue
			}
			curl := url + "/c/" + comp.ID
			ctoken, ckey, kerr := ops.MintBoundAPIKey(s.Site(), kayKeyName+": "+comp.Label, []string{ops.ScopeRender}, "/mcp/c/"+comp.ID)
			if kerr != nil {
				s.removeKayRegistrations(ctx, c, st)
				s.revokeKayKeys(st)
				return kayState{}, fmt.Errorf("could not mint the key for %s: %w", comp.ID, kerr)
			}
			cs, cerr := c.CreateMCPServer(ctx, "polis-"+comp.ID, curl,
				"The owner's polis site, limited to one part of it: "+strings.ToLower(comp.Label)+".", ctoken)
			if cerr != nil {
				_ = ops.RevokeAPIKey(s.Site(), ckey.ID)
				s.removeKayRegistrations(ctx, c, st)
				s.revokeKayKeys(st)
				return kayState{}, fmt.Errorf("could not register %s with v0: %w", comp.ID, cerr)
			}
			st.Components[comp.ID] = kayRegistration{MCPServerID: cs.ID, MCPURL: curl, APIKeyID: ckey.ID}
		}
	}
	if err := s.saveKayState(ctx, st); err != nil {
		return kayState{}, fmt.Errorf("could not record the v0 registration: %w", err)
	}
	s.LogEvent("pub.polis.kay.registered", map[string]interface{}{"mcp_server_id": srv.ID, "key_id": key.ID, "components": len(st.Components)})
	return st, nil
}

// removeKayRegistrations deletes every registration st records. A failure is
// logged: a stale registration only points at a key that no longer works.
func (s *Server) removeKayRegistrations(ctx context.Context, c *v0.Client, st kayState) {
	ids := []string{}
	if st.MCPServerID != "" {
		ids = append(ids, st.MCPServerID)
	}
	for _, r := range st.Components {
		if r.MCPServerID != "" {
			ids = append(ids, r.MCPServerID)
		}
	}
	for _, id := range ids {
		if err := c.DeleteMCPServer(ctx, id); err != nil {
			s.LogWarn("kay: could not remove the old v0 registration %s: %v", id, err)
		}
	}
}

// kayKeyIDs are every key st records: the whole-site one and each component's.
func kayKeyIDs(st kayState) []string {
	ids := []string{}
	if st.APIKeyID != "" {
		ids = append(ids, st.APIKeyID)
	}
	for _, r := range st.Components {
		if r.APIKeyID != "" {
			ids = append(ids, r.APIKeyID)
		}
	}
	return ids
}

// revokeKayKeys revokes every key st records. A failure is logged.
func (s *Server) revokeKayKeys(st kayState) {
	for _, id := range kayKeyIDs(st) {
		if err := ops.RevokeAPIKey(s.Site(), id); err != nil && !errors.Is(err, ops.ErrAPIKeyNotFound) {
			s.LogWarn("kay: could not revoke key %s: %v", id, err)
		}
	}
}

// kayKeyCurrent reports whether key id exists and is bound to bound. A key
// from before binding is not current, so the instance registers again once.
func (s *Server) kayKeyCurrent(id, bound string) bool {
	if id == "" {
		return false
	}
	keys, err := ops.ListAPIKeys(s.Site())
	if err != nil {
		return false
	}
	for _, k := range keys {
		if k.ID == id {
			return k.Bound == bound
		}
	}
	return false
}

func (s *Server) apiKeyExists(id string) bool {
	if id == "" {
		return false
	}
	keys, err := ops.ListAPIKeys(s.Site())
	if err != nil {
		return false
	}
	for _, k := range keys {
		if k.ID == id {
			return true
		}
	}
	return false
}

func kayError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errKayNotConnected):
		writeJSONError(w, http.StatusConflict, err.Error())
	case v0.Unauthorized(err):
		writeJSONError(w, http.StatusBadGateway, "v0 refused the key: connect Kay again with a current v0 key")
	default:
		writeJSONError(w, http.StatusBadGateway, err.Error())
	}
}

// handleKay serves GET /api/kay: whether Kay is connected, and where v0 reaches
// the site. Never the key.
func (s *Server) handleKay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx := r.Context()
	_, err := s.kayClient(ctx)
	st := s.kayState(ctx)
	writeJSON(w, map[string]interface{}{
		"connected":  err == nil,
		"registered": st.MCPServerID != "" && s.apiKeyExists(st.APIKeyID),
		"mcp_url":    s.kayMCPURL(),
	})
}

// kayOnly answers 404 for the Kay API on polis.pub (custodied). Kay works by
// v0 calling the instance's /mcp, which hosted never routes, so on polis.pub
// every Kay call would store a v0 key and mint an API key for a connection
// that can never work. The SPA hides the Kay page there too.
func (s *Server) kayOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.effectiveMode() == mode.Custodied {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}
}

// handleKayConnect serves POST /api/kay/connect {"v0_key": "..."} (checks the
// key with v0, then stores it) and DELETE (removes the registration, Kay's
// API key and the stored key).
func (s *Server) handleKayConnect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	switch r.Method {
	case http.MethodPost:
		var req struct {
			V0Key string `json:"v0_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.V0Key) == "" {
			writeJSONError(w, http.StatusBadRequest, `invalid request body: expected {"v0_key": "..."}`)
			return
		}
		key := strings.TrimSpace(req.V0Key)
		user, err := v0.New(key, s.kayV0Base).Me(ctx)
		if err != nil {
			if v0.Unauthorized(err) {
				writeJSONError(w, http.StatusBadRequest, "v0 did not accept that key")
				return
			}
			kayError(w, err)
			return
		}
		if err := s.Site().Store().Put(ctx, kayV0KeyKey, []byte(key), storage.WithMode(0o600)); err != nil {
			s.LogError("kay: could not store the v0 key: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "could not store the v0 key")
			return
		}
		s.LogEvent("pub.polis.kay.connected", map[string]interface{}{"v0_user": user.ID})
		writeJSON(w, map[string]interface{}{"connected": true, "v0_user": user.Name})
	case http.MethodDelete:
		kayMu.Lock()
		defer kayMu.Unlock()
		st := s.kayState(ctx)
		if c, err := s.kayClient(ctx); err == nil {
			s.removeKayRegistrations(ctx, c, st)
		}
		s.revokeKayKeys(st)
		for _, k := range []string{kayV0KeyKey, kayStateKey} {
			if err := s.Site().Store().Delete(ctx, k); err != nil && !errors.Is(err, fs.ErrNotExist) {
				s.LogError("kay: could not delete %s: %v", k, err)
				writeJSONError(w, http.StatusInternalServerError, "could not disconnect Kay")
				return
			}
		}
		s.LogEvent("pub.polis.kay.disconnected", nil)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleKayAsk serves POST /api/kay/ask {"message": "...", "chat_id": "..."}:
// starts a v0 chat (or continues one) limited to this site's MCP server, and
// returns at once. v0 answers in the background; the page polls the chat.
func (s *Server) handleKayAsk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Message string `json:"message"`
		ChatID  string `json:"chat_id"`
		// Point is what the owner pointed at (point and ask); absent from
		// the Kay page, whose asks reach every tool.
		Point *kayPointContext `json:"point"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Message) == "" {
		writeJSONError(w, http.StatusBadRequest, `invalid request body: expected {"message": "...", "chat_id": "..."}`)
		return
	}
	if len(req.Message) > 4000 {
		writeJSONError(w, http.StatusBadRequest, "a message is at most 4,000 characters")
		return
	}
	message, system := req.Message, kaySystemPrompt
	var comp kayComponent
	if req.Point != nil {
		var ok bool
		comp, ok = findKayComponent(req.Point.Component)
		switch {
		case !s.pointAndAsk():
			writeJSONError(w, http.StatusNotFound, "point and ask is not available on this site")
			return
		case !ok:
			writeJSONError(w, http.StatusBadRequest, "point at one of the components /api/kay/components lists")
			return
		case !comp.Enabled:
			writeJSONError(w, http.StatusBadRequest, "Kay can't change the "+strings.ToLower(comp.Label)+" yet")
			return
		}
		framed, ferr := kayPointMessage(comp, *req.Point, req.Message)
		if ferr != nil {
			writeJSONError(w, http.StatusBadRequest, ferr.Error())
			return
		}
		message, system = framed, kayScopedPrompt(comp)
	}
	ctx := r.Context()
	c, err := s.kayClient(ctx)
	if err != nil {
		kayError(w, err)
		return
	}
	st, err := s.ensureKayRegistered(ctx, c)
	if err != nil {
		kayError(w, err)
		return
	}
	serverID := st.MCPServerID
	if req.Point != nil {
		reg, ok := st.Components[comp.ID]
		if !ok || reg.MCPServerID == "" {
			writeJSONError(w, http.StatusBadGateway, "the "+strings.ToLower(comp.Label)+" is not registered with v0")
			return
		}
		serverID = reg.MCPServerID
	}
	asked := time.Now().UTC()
	ask := v0.Ask{Message: message, System: system, MCPServerIDs: []string{serverID}}
	var ch v0.Chat
	if req.ChatID != "" {
		ch, err = c.Continue(ctx, req.ChatID, ask)
	} else {
		ch, err = c.StartChat(ctx, ask)
	}
	if err != nil {
		kayError(w, err)
		return
	}
	id := ch.Key()
	if id == "" {
		id = req.ChatID
	}
	s.LogEvent("pub.polis.kay.ask", map[string]interface{}{"chat_id": id, "new_chat": req.ChatID == "", "chars": len(req.Message), "component": comp.ID})
	writeJSON(w, map[string]interface{}{"chat_id": id, "web_url": ch.WebURL, "asked_at": asked.Format(time.RFC3339Nano)})
}

// kayMessage is a message as the page shows it.
type kayMessage struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Done      bool      `json:"done"`
	Steps     []v0.Step `json:"steps"`
	CreatedAt string    `json:"created_at,omitempty"`
}

// handleKayChat serves GET /api/kay/chats/{id}?since=<RFC 3339>: the chat as
// it stands (mid-turn included, with v0's steps), and the tool calls this
// site's MCP served for Kay's key since then, which carry each call's outcome.
func (s *Server) handleKayChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/kay/chats/")
	if id == "" || strings.ContainsAny(id, "/?#") {
		writeJSONError(w, http.StatusBadRequest, "expected /api/kay/chats/{id}")
		return
	}
	var since time.Time
	if v := r.URL.Query().Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "since must be an RFC 3339 time")
			return
		}
		since = t
	}
	ctx := r.Context()
	c, err := s.kayClient(ctx)
	if err != nil {
		kayError(w, err)
		return
	}
	ch, err := c.GetChat(ctx, id)
	if err != nil {
		if v0.NotFound(err) {
			writeJSONError(w, http.StatusNotFound, "v0 has no such chat")
			return
		}
		kayError(w, err)
		return
	}
	msgs := make([]kayMessage, 0, len(ch.Messages))
	for _, m := range ch.Messages {
		steps := m.Steps()
		if steps == nil {
			steps = []v0.Step{}
		}
		content := m.Content
		if m.Role == "assistant" {
			content = m.Text()
		}
		msgs = append(msgs, kayMessage{Role: m.Role, Content: content, Done: m.Role != "assistant" || m.Done(), Steps: steps, CreatedAt: m.CreatedAt})
	}
	done := len(msgs) > 0 && msgs[len(msgs)-1].Role == "assistant" && msgs[len(msgs)-1].Done
	writeJSON(w, map[string]interface{}{
		"chat_id": ch.Key(), "web_url": ch.WebURL, "done": done,
		"messages": msgs, "calls": s.kayCalls(ctx, since),
	})
}

// kayCalls are the MCP calls served for Kay's key at or after since.
func (s *Server) kayCalls(ctx context.Context, since time.Time) []mcpCall {
	out := []mcpCall{}
	ids := map[string]bool{}
	for _, id := range kayKeyIDs(s.kayState(ctx)) {
		ids[id] = true
	}
	if len(ids) == 0 {
		return out
	}
	for _, c := range s.readMCPCalls(ctx, since, 0) {
		if ids[c.KeyID] {
			out = append(out, c)
		}
	}
	return out
}
