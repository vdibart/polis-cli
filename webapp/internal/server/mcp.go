package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vdibart/polis-cli/cli-go/pkg/navshortcut"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/cli-go/pkg/pql"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"github.com/vdibart/polis-cli/cli-go/pkg/theme"
)

// The instance's MCP endpoint, /mcp: how an owner's AI (v0 first) acts on the
// site. Streamable HTTP, stateless, authorised by an API key the owner minted.
//
// ⭐ EVERY TOOL IS A THIN WRAPPER OVER A HANDLER THE BROWSER APP ALREADY USES,
// invoked in-process. MCP adds no behaviour of its own; what it adds is a
// description an LLM can choose from, and a key in place of a session.
//
// ⛔ THE TOOL TABLE BELOW IS HAND-WRITTEN AND RETIRES. The considered design
// generates every MCP tool from the polis API's own description, so that an
// operation added to the API appears over MCP with no MCP code. When that
// lands, mcpTools goes and this file keeps only the endpoint, the key check
// and the call log.

// mcpCallsDir holds the log of every tools/call served, one object per call,
// named by its start time so the names sort oldest first.
//
// ⛔ NEVER ONE FILE APPENDED TO. An append is a read-modify-write, and on a
// remote instance two calls v0 makes in parallel run in separate invocations:
// the second write dropped the first call, the app never saw it, and a nav
// shortcut Kay had set did not appear. A key per call cannot collide.
const mcpCallsDir = ".polis/mcp/calls/"

// mcpCallsKey is the single-file log written before mcpCallsDir. It is read,
// never written, so calls logged by an earlier build still show.
const mcpCallsKey = ".polis/mcp/calls.jsonl"

// mcpCallNameTime is the time format at the head of a call's key: fixed
// width, so names sort as times do.
const mcpCallNameTime = "20060102T150405.000000000Z"

// mcpInstructions are sent to every client at initialize.
const mcpInstructions = "These tools act on one polis site: a personal website whose owner " +
	"issued the key you are using. Read the site with get_site before changing anything. " +
	"A change re-renders the site and publishes it; check get_publish_status to learn " +
	"when readers see it."

type mcpTool struct {
	name        string
	description string
	// scope is the API-key scope a call needs; "" means any valid key.
	scope  string
	input  map[string]any
	readOK bool
	run    func(ctx context.Context, s *Server, args json.RawMessage) (any, error)
}

var noInput = map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}

var mcpTools = []mcpTool{
	{
		name: "get_site",
		description: "Read the owner's polis site: its title, author and address, the active theme, " +
			"and the themes it can switch to (each with its five palette colours: background, " +
			"text and three accents). Call this before changing anything.",
		input:  noInput,
		readOK: true,
		run:    mcpGetSite,
	},
	{
		name: "list_posts",
		description: "List the site's published posts, newest first: title, date and the opening of " +
			"each. Use it to answer questions about what the owner has written.",
		input: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100,
					"description": "How many posts to return (default 20)."},
			},
			"additionalProperties": false,
		},
		readOK: true,
		run:    mcpListPosts,
	},
	{
		name: "set_theme",
		description: "Switch the site to another theme, by a name exactly as get_site lists it. " +
			"A theme changes how the whole public site looks. The site re-renders and " +
			"publishes itself; check get_publish_status to learn when readers see it.",
		scope: ops.ScopeRender,
		input: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{"theme": map[string]any{"type": "string", "description": "A theme name from get_site."}},
			"required":             []string{"theme"},
			"additionalProperties": false,
		},
		run: mcpSetTheme,
	},
	{
		name: "get_custom_css",
		description: "Read the CSS the owner's site layers after its theme on every render " +
			"(empty when it has none). Read it before replacing it, to keep what should stay.",
		input:  noInput,
		readOK: true,
		run:    mcpGetCustomCSS,
	},
	{
		name: "set_custom_css",
		description: "Replace the CSS the site layers after its theme, then re-render and publish. " +
			"This is how to change the site's look beyond picking a theme. Prefer overriding the " +
			"active theme's custom properties (get_site lists them, with their values) in a :root " +
			"block; use selectors such as a or h1, h2, h3 only for what a property does not cover. " +
			"Send the complete CSS: it replaces what is there. An empty string removes it. It may " +
			"not use @import or load anything from another site. Check get_publish_status to learn " +
			"when readers see it.",
		scope: ops.ScopeRender,
		input: map[string]any{
			"type": "object",
			"properties": map[string]any{"css": map[string]any{"type": "string", "maxLength": theme.MaxCustomCSSBytes,
				"description": "The complete CSS to layer after the theme."}},
			"required":             []string{"css"},
			"additionalProperties": false,
		},
		run: mcpSetCustomCSS,
	},
	{
		name: "get_stream_marks",
		description: "Read the rules that mark posts in the owner's own stream (each: the site that wrote " +
			"the post, and the keywords that pick it out). Read them before replacing them, to keep what " +
			"should stay.",
		input: noInput,
		// ⛔ Owner-private config: only a key that may change it may read it.
		scope:  ops.ScopeRender,
		readOK: true,
		run:    mcpGetStreamMarks,
	},
	{
		name: "set_stream_marks",
		description: "Replace the rules that mark posts in the owner's own stream. Each rule names one site " +
			"(author_domain, such as discover.polis.pub) and up to 10 keywords (any_of: plain words or short " +
			"phrases, matched case-insensitively as whole words). A post by that site that mentions any of " +
			"them is marked with a \"!\" in the stream's timeline dot. Only the owner sees marks. Send every " +
			"rule to keep: this replaces them all; an empty list removes them.",
		scope: ops.ScopeRender,
		input: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"rules": map[string]any{"type": "array", "maxItems": 20, "items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"author_domain": map[string]any{"type": "string", "description": "The site whose posts this rule picks out, e.g. discover.polis.pub."},
						"any_of":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 10, "description": "Plain keywords; any one marks the post."},
					},
					"required":             []string{"author_domain", "any_of"},
					"additionalProperties": false,
				}},
			},
			"required":             []string{"rules"},
			"additionalProperties": false,
		},
		run: mcpSetStreamMarks,
	},
	{
		name: "get_nav_shortcuts",
		description: "Read the shortcuts in the owner's nav bar (each: a label, an icon, and the PQL " +
			"sentence it opens), the icons a shortcut may use, and the words a PQL sentence may use.",
		input: noInput,
		// ⛔ Owner-private config: only a key that may change it may read it.
		scope:  ops.ScopeRender,
		readOK: true,
		run:    mcpGetNavShortcuts,
	},
	{
		name: "set_nav_shortcuts",
		description: "Replace the shortcuts in the owner's nav bar. Each opens one PQL sentence, the same " +
			"words the stream's filter uses, for example \"all posts from discover.polis.pub with comments\" " +
			"or \"all messages from my mutuals by date\". Each has a short label (shown on hover) and an icon " +
			"from the list get_nav_shortcuts returns. At most 5. Send every shortcut to keep: this replaces " +
			"them all; an empty list removes them. When the owner gives a sentence, use their words exactly; " +
			"never reword one that is valid, and if it is refused, say why rather than guess another. " +
			pql.Vocabulary(),
		scope: ops.ScopeRender,
		input: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"shortcuts": map[string]any{"type": "array", "maxItems": 5, "items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"label":    map[string]any{"type": "string", "maxLength": 32},
						"icon":     map[string]any{"type": "string", "enum": navshortcut.Icons},
						"sentence": map[string]any{"type": "string", "description": "A PQL sentence in the owner's own words, e.g. all profiles from all polis by activity."},
					},
					"required":             []string{"label", "sentence"},
					"additionalProperties": false,
				}},
			},
			"required":             []string{"shortcuts"},
			"additionalProperties": false,
		},
		run: mcpSetNavShortcuts,
	},
	{
		name: "get_publish_status",
		description: "Report whether the public site readers see matches the latest change: " +
			"'live' when it does, 'behind' while a change is still being published.",
		input:  noInput,
		readOK: true,
		run:    mcpPublishStatus,
	},
}

// handleMCP serves /mcp. ⛔ Not under /api/, so the owner gate does not see it:
// the API key is the whole of its authorisation, checked here on every request.
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) { s.serveMCP(w, r, nil) }

// serveMCP serves MCP with every tool, or, for point and ask, only one
// component's (kay_point.go). ⭐ A tool outside the component is not listed,
// and a call to it is refused, so the scope holds whatever the caller tries.
func (s *Server) serveMCP(w http.ResponseWriter, r *http.Request, comp *kayComponent) {
	// ⛔ An address that keeps failing is refused before its token is looked
	// at: only failures count, so v0's own steady calls are never limited.
	client := s.clientKey(r)
	if s.mcpFailures().exhausted(client) {
		w.Header().Set("Retry-After", "60")
		writeJSONError(w, http.StatusTooManyRequests, "too many requests with an invalid key; try again in a minute")
		return
	}
	key, ok := s.mcpKey(r)
	endpoint := "/mcp"
	if comp != nil {
		endpoint = "/mcp/c/" + comp.ID
	}
	// ⛔ A key bound to one endpoint works there and nowhere else, so a key
	// handed to a one-component chat cannot reach every tool at /mcp.
	if ok && !key.UsableAt(endpoint) {
		ok = false
	}
	if !ok {
		s.mcpFailures().allow(client)
		s.LogEvent("pub.polis.security.auth_failed", map[string]interface{}{
			"path": r.URL.Path, "method": r.Method, "reason": "mcp_key",
			"request_id": w.Header().Get("X-Request-Id"),
		})
		w.Header().Set("WWW-Authenticate", `Bearer realm="polis"`)
		writeJSONError(w, http.StatusUnauthorized, "a valid API key is required (Authorization: Bearer <key>)")
		return
	}
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return s.newMCPServer(key, comp)
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	h.ServeHTTP(w, r)
}

func (s *Server) mcpKey(r *http.Request) (ops.APIKey, bool) {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(auth) < 7 || subtle.ConstantTimeCompare([]byte(strings.ToLower(auth[:7])), []byte("bearer ")) != 1 {
		return ops.APIKey{}, false
	}
	return s.mcpKeys.lookup(s.Site(), strings.TrimSpace(auth[7:]))
}

// mcpKeyCache keeps the keys file in memory briefly, so /mcp, which anyone on
// the internet can POST to, does not cost a store read per request.
//
// ⭐ A key minted here is seen at once (a write bumps ops.APIKeysGeneration),
// and an unknown token re-reads at most once a second, so a key minted on
// another instance works within a second. ⚠️ A key revoked on ANOTHER instance
// can keep working here for up to mcpKeyCacheTTL; on this one it stops at once.
type mcpKeyCache struct {
	mu     sync.Mutex
	keys   []ops.APIKey
	loaded time.Time
	gen    uint64
	ok     bool
}

const (
	mcpKeyCacheTTL   = 10 * time.Second
	mcpKeyMissReread = time.Second
)

func (c *mcpKeyCache) lookup(site storage.Site, token string) (ops.APIKey, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if !c.ok || c.gen != ops.APIKeysGeneration() || now.Sub(c.loaded) > mcpKeyCacheTTL {
		c.reload(site, now)
	}
	if k, found := ops.MatchAPIKey(c.keys, token); found {
		return k, true
	}
	if now.Sub(c.loaded) > mcpKeyMissReread {
		c.reload(site, now)
		return ops.MatchAPIKey(c.keys, token)
	}
	return ops.APIKey{}, false
}

// mcpFailures is /mcp's limiter: a share of failures per address and ⛔ NO
// process-wide cap, because a cap that unauthenticated requests could fill
// would shut the owner's own AI out.
func (s *Server) mcpFailures() *attemptLimiter {
	s.mcpFailOnce.Do(func() {
		s.mcpFail = newAttemptLimiter()
		s.mcpFail.perProcess = int(^uint(0) >> 1)
	})
	return s.mcpFail
}

func (c *mcpKeyCache) reload(site storage.Site, now time.Time) {
	c.gen = ops.APIKeysGeneration()
	keys, err := ops.ListAPIKeys(site)
	// ⚠️ A failed read is "no valid key", as LookupAPIKey treats it.
	c.keys, c.loaded, c.ok = keys, now, err == nil
	if err != nil {
		c.keys = nil
	}
}

// newMCPServer builds the server for one request, bound to the key that made
// it. Building it is a handful of allocations, and binding the key in a closure
// keeps the scope check from depending on how the SDK passes context.
func (s *Server) newMCPServer(key ops.APIKey, comp *kayComponent) *mcp.Server {
	instructions := mcpInstructions
	var allowed map[string]bool
	if comp != nil {
		allowed = comp.toolSet()
		instructions += " This connection is limited to one part of the site, the " +
			strings.ToLower(comp.Label) + " (" + comp.Hint + "): only its tools are here. For anything " +
			"else, tell the owner to point at another part."
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "polis", Version: s.CLIVersion},
		&mcp.ServerOptions{Instructions: instructions})
	for _, t := range mcpTools {
		t := t
		if allowed != nil && !allowed[t.name] {
			continue
		}
		tool := &mcp.Tool{Name: t.name, Description: t.description, InputSchema: t.input}
		if t.readOK {
			tool.Annotations = &mcp.ToolAnnotations{ReadOnlyHint: true}
		}
		srv.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return s.runMCPTool(ctx, key, t, req.Params.Arguments), nil
		})
	}
	return srv
}

func (s *Server) runMCPTool(ctx context.Context, key ops.APIKey, t mcpTool, args json.RawMessage) *mcp.CallToolResult {
	started := time.Now()
	var out any
	var err error
	if t.scope != "" && !key.Allows(t.scope) {
		err = fmt.Errorf("this key is not allowed to %s: it needs the %q scope", t.name, t.scope)
	} else {
		out, err = t.run(ctx, s, args)
	}
	s.recordMCPCall(ctx, key, t.name, args, started, err)

	if err != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
	}
	text, merr := json.MarshalIndent(out, "", "  ")
	if merr != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "could not encode the result"}}}
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(text)}}}
}

// mcpCall is one line of the call log.
type mcpCall struct {
	TS         string `json:"ts"`
	KeyID      string `json:"key_id"`
	KeyName    string `json:"key_name"`
	Tool       string `json:"tool"`
	Args       string `json:"args,omitempty"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

// recordMCPCall appends to the call log and emits an event. A failure to
// record is logged and never fails the call: the change has already happened.
func (s *Server) recordMCPCall(ctx context.Context, key ops.APIKey, tool string, args json.RawMessage, started time.Time, err error) {
	c := mcpCall{
		TS: started.UTC().Format(time.RFC3339Nano), KeyID: key.ID, KeyName: key.Name,
		Tool: tool, OK: err == nil, DurationMS: time.Since(started).Milliseconds(),
	}
	if a := strings.TrimSpace(string(args)); a != "" && a != "{}" && a != "null" {
		if len(a) > 2000 {
			a = a[:2000] + "…"
		}
		c.Args = a
	}
	if err != nil {
		c.Error = err.Error()
	}
	line, _ := json.Marshal(c)
	var nonce [4]byte
	_, _ = rand.Read(nonce[:])
	name := mcpCallsDir + started.UTC().Format(mcpCallNameTime) + "-" + hex.EncodeToString(nonce[:]) + ".json"
	if aerr := s.Site().Store().Put(context.WithoutCancel(ctx), name, line,
		storage.WithMode(0o600), storage.IfAbsent()); aerr != nil {
		s.LogError("mcp: could not record a %s call: %v", tool, aerr)
	}
	s.LogEvent("pub.polis.mcp.tool_call", map[string]interface{}{
		"tool": tool, "key_id": key.ID, "ok": c.OK, "duration_ms": c.DurationMS,
	})
}

// handleMCPCalls serves GET /api/mcp/calls?since=<RFC 3339>: the calls served
// since then, oldest first, at most 200. Owner-only, like every /api/ route.
func (s *Server) handleMCPCalls(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
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
	writeJSON(w, map[string]interface{}{"calls": s.readMCPCalls(r.Context(), since, 200)})
}

// readMCPCalls reads the calls that started at or after since, oldest first,
// at most limit (the newest kept; 0 is no limit). A log that cannot be read is
// logged and read as empty: the log reports, it never gates.
func (s *Server) readMCPCalls(ctx context.Context, since time.Time, limit int) []mcpCall {
	store := s.Site().Store()
	calls := []mcpCall{}
	if data, err := store.Get(ctx, mcpCallsKey); err == nil {
		for _, line := range bytes.Split(data, []byte("\n")) {
			var c mcpCall
			if len(bytes.TrimSpace(line)) == 0 || json.Unmarshal(line, &c) != nil {
				continue
			}
			calls = append(calls, c)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		s.LogError("mcp: could not read the call log: %v", err)
	}
	entries, err := store.List(ctx, mcpCallsDir)
	if err != nil {
		s.LogError("mcp: could not list the call log: %v", err)
	}
	// Only the calls new enough are fetched: the name carries the start time.
	keys := []string{}
	for _, e := range entries {
		name := strings.TrimPrefix(e.Key, mcpCallsDir)
		if e.IsDir || len(name) < len(mcpCallNameTime) {
			continue
		}
		if t, perr := time.Parse(mcpCallNameTime, name[:len(mcpCallNameTime)]); perr != nil || t.Before(since) {
			continue
		}
		keys = append(keys, e.Key)
	}
	sort.Strings(keys)
	if limit > 0 && len(keys) > limit {
		keys = keys[len(keys)-limit:]
	}
	for _, k := range keys {
		data, gerr := store.Get(ctx, k)
		var c mcpCall
		if gerr != nil || json.Unmarshal(data, &c) != nil {
			continue
		}
		calls = append(calls, c)
	}
	out := calls[:0]
	for _, c := range calls {
		if t, perr := time.Parse(time.RFC3339Nano, c.TS); perr == nil && !t.Before(since) {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].TS < out[b].TS })
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// ── the tools ────────────────────────────────────────────────────────────

// invoke calls one of the browser app's handlers in-process and decodes its
// JSON answer. A non-2xx answer is an error carrying the handler's own text.
func (s *Server) invoke(ctx context.Context, h http.HandlerFunc, method, path string, body any) (map[string]any, error) {
	var rd *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	rec := &bufferedResponse{header: http.Header{}, status: http.StatusOK}
	h(rec, req)
	if rec.status < 200 || rec.status > 299 {
		// A raw body is cut short; a handler's own error message is kept
		// whole up to a larger cap, because it may name what would be
		// accepted (a refused PQL sentence carries the vocabulary).
		msg, limit := strings.TrimSpace(rec.body.String()), 300
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(rec.body.Bytes(), &e) == nil && e.Error != "" {
			msg, limit = e.Error, 1200
		}
		if len(msg) > limit {
			msg = msg[:limit]
		}
		return nil, fmt.Errorf("the site refused: %s", msg)
	}
	out := map[string]any{}
	if err := json.Unmarshal(rec.body.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("the site answered something that is not JSON")
	}
	return out, nil
}

type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
	wrote  bool
}

func (b *bufferedResponse) Header() http.Header { return b.header }
func (b *bufferedResponse) Write(p []byte) (int, error) {
	b.wrote = true
	return b.body.Write(p)
}
func (b *bufferedResponse) WriteHeader(code int) {
	if !b.wrote {
		b.status = code
		b.wrote = true
	}
}

func mcpGetSite(ctx context.Context, s *Server, _ json.RawMessage) (any, error) {
	st, err := s.invoke(ctx, s.handleSettings, http.MethodGet, "/api/settings", nil)
	if err != nil {
		return nil, err
	}
	site, _ := st["site"].(map[string]any)
	// ⭐ Only what can be chosen: set_theme refuses a reserved theme, so a
	// description that offered one would send the caller into a refusal.
	themes := []map[string]any{}
	if list, ok := st["themes"].([]any); ok {
		for _, t := range list {
			p, _ := t.(map[string]any)
			name, _ := p["name"].(string)
			if name == "" || theme.IsReserved(name) {
				continue
			}
			themes = append(themes, map[string]any{"name": name, "palette": p["colors"]})
		}
	}
	active, _ := st["active_theme"].(string)
	props := []theme.Property{}
	if active != "" {
		if p, perr := theme.ThemeProperties(s.Site(), active); perr == nil && p != nil {
			props = p
		}
	}
	return map[string]any{
		"active_theme_properties": props,
		"title":                   site["site_title"],
		"author":                  site["author_name"],
		"address":                 site["base_url"],
		"active_theme":            st["active_theme"],
		"themes":                  themes,
	}, nil
}

func mcpListPosts(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	var in struct {
		Limit int `json:"limit"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &in)
	}
	if in.Limit <= 0 || in.Limit > 100 {
		in.Limit = 20
	}
	res, err := s.invoke(ctx, s.handlePosts, http.MethodGet, "/api/posts", nil)
	if err != nil {
		return nil, err
	}
	list, _ := res["posts"].([]any)
	// ⚠️ No reader address: where a post is served depends on the type's
	// configurable mount, and a guessed address is worse than none.
	posts := make([]map[string]any, 0, len(list))
	for _, p := range list {
		e, _ := p.(map[string]any)
		posts = append(posts, map[string]any{
			"title": e["title"], "published": e["published"], "excerpt": e["excerpt"],
		})
	}
	sort.SliceStable(posts, func(i, j int) bool {
		a, _ := posts[i]["published"].(string)
		b, _ := posts[j]["published"].(string)
		return a > b
	})
	if len(posts) > in.Limit {
		posts = posts[:in.Limit]
	}
	return map[string]any{"count": len(list), "posts": posts}, nil
}

func mcpSetTheme(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	var in struct {
		Theme string `json:"theme"`
	}
	if err := json.Unmarshal(args, &in); err != nil || strings.TrimSpace(in.Theme) == "" {
		return nil, fmt.Errorf("give a theme name from get_site")
	}
	if _, err := s.invoke(ctx, s.handleThemeSwitch, http.MethodPost, "/api/settings/theme", map[string]string{"theme": in.Theme}); err != nil {
		return nil, err
	}
	return map[string]any{
		"theme": in.Theme, "accepted": true,
		"next": "the site has re-rendered; check get_publish_status to learn when readers see it",
	}, nil
}

func mcpPublishStatus(ctx context.Context, s *Server, _ json.RawMessage) (any, error) {
	st, err := s.invoke(ctx, s.handleDeployStatus, http.MethodGet, "/api/deploy-status", nil)
	if err != nil {
		return nil, err
	}
	state, _ := st["state"].(string)
	if state == "not-applicable" {
		// A site served straight from its own files is live the moment it renders.
		state = "live"
	}
	out := map[string]any{"state": state}
	if v, ok := st["behind_since"]; ok {
		out["behind_since"] = v
	}
	return out, nil
}

func mcpGetCustomCSS(ctx context.Context, s *Server, _ json.RawMessage) (any, error) {
	return s.invoke(ctx, s.handleCustomCSS, http.MethodGet, "/api/settings/custom-css", nil)
}

func mcpSetCustomCSS(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	var in struct {
		CSS *string `json:"css"`
	}
	if err := json.Unmarshal(args, &in); err != nil || in.CSS == nil {
		return nil, fmt.Errorf("give the complete CSS as css (an empty string removes it)")
	}
	if _, err := s.invoke(ctx, s.handleCustomCSS, http.MethodPost, "/api/settings/custom-css", map[string]string{"css": *in.CSS}); err != nil {
		return nil, err
	}
	return map[string]any{
		"accepted": true, "bytes": len(*in.CSS),
		"next": "the site has re-rendered; check get_publish_status to learn when readers see it",
	}, nil
}

func mcpGetStreamMarks(ctx context.Context, s *Server, _ json.RawMessage) (any, error) {
	return s.invoke(ctx, s.handleStreamMarks, http.MethodGet, "/api/stream-marks", nil)
}

func mcpSetStreamMarks(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	var in map[string]any
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("give rules as a list of {author_domain, any_of}")
	}
	return s.invoke(ctx, s.handleStreamMarks, http.MethodPost, "/api/stream-marks", in)
}

func mcpGetNavShortcuts(ctx context.Context, s *Server, _ json.RawMessage) (any, error) {
	return s.invoke(ctx, s.handleNavShortcuts, http.MethodGet, "/api/nav-shortcuts", nil)
}

func mcpSetNavShortcuts(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	var in map[string]any
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("give shortcuts as a list of {label, icon, sentence}")
	}
	return s.invoke(ctx, s.handleNavShortcuts, http.MethodPost, "/api/nav-shortcuts", in)
}
