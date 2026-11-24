package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vdibart/polis-cli/cli-go/pkg/apidesc"
	"github.com/vdibart/polis-cli/cli-go/pkg/mcpgen"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"github.com/vdibart/polis-cli/webapp/internal/api"
)

// The instance's MCP endpoint, /mcp: how an owner's AI (v0 first) acts on the
// site. Streamable HTTP, stateless, authorised by an API key the owner minted.
//
// ⭐⭐ EVERY TOOL IS GENERATED FROM THE SITE'S OWN API DESCRIPTION (mcpgen), the
// same generator `polis mcp` and polis.pub's hosted MCP use, and every call is
// made to this instance's own /v1, in-process, with the caller's own key. So
// /v1's auth, grant, scope and marker checks apply unchanged, an operation added
// to the API appears here with no MCP code, and nothing in this file knows an
// operation. What this file adds is the endpoint: the key check (with its
// binding), the failure limit and an event for every call.

// handleMCP serves /mcp. ⛔ Not under /api/, so the owner gate does not see it:
// the API key is the whole of its authorisation, checked here on every request
// and again by /v1 on every call a tool makes.
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if s.switchedOff(api.SwitchMCP) {
		api.WriteSwitchedOff(w)
		return
	}
	// ⛔ An address that keeps failing is refused before its token is looked
	// at: only failures count, so v0's own steady calls are never limited.
	client := s.clientKey(r)
	if s.mcpFailures().exhausted(client) {
		w.Header().Set("Retry-After", "60")
		writeJSONError(w, http.StatusTooManyRequests, "too many requests with an invalid key; try again in a minute")
		return
	}
	key, token, ok := s.mcpKey(r)
	// ⛔ A key bound to another endpoint works there and nowhere else.
	if ok && !key.UsableAt(mcpEndpoint) {
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
	srv, err := s.newMCPServer(r.Context(), key, token)
	if err != nil {
		s.LogError("mcp: %v", err)
		writeJSONError(w, http.StatusBadGateway, "the site's API could not be described")
		return
	}
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	h.ServeHTTP(w, r)
}

// mcpEndpoint is the one address this server answers at; a key bound to it
// works here and, through this server, at /v1.
const mcpEndpoint = "/mcp"

func (s *Server) mcpKey(r *http.Request) (ops.APIKey, string, bool) {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(auth) < 7 || subtle.ConstantTimeCompare([]byte(strings.ToLower(auth[:7])), []byte("bearer ")) != 1 {
		return ops.APIKey{}, "", false
	}
	token := strings.TrimSpace(auth[7:])
	key, ok := s.mcpKeys.lookup(s.Site(), token)
	return key, token, ok
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

// newMCPServer builds the server for one request: the tools this key may use,
// generated from the site's own description as /v1 shows it to this key, each
// call made to /v1 in-process with the same key.
//
// ⭐ Nothing here decides what a key may do. /v1 does, on every call: a write
// outside the key's grant is refused there, and the tool reports the refusal.
func (s *Server) newMCPServer(ctx context.Context, key ops.APIKey, token string) (*mcp.Server, error) {
	client := &http.Client{Transport: v1InProcess{s: s}, Timeout: 60 * time.Second}
	base := strings.TrimRight(s.GetBaseURL(), "/")
	if base == "" {
		base = "http://instance"
	}
	ts := s.mcpToolsets.get(key)
	if ts == nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+apidesc.DescriptionPath, nil) //nolint:gosec // G704: this instance's own /v1, through an in-process transport; no network request is made
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req) //nolint:gosec // G704: this instance's own /v1, through an in-process transport; no network request is made
		if err != nil {
			return nil, fmt.Errorf("could not describe the site's API: %w", err)
		}
		desc, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		_ = resp.Body.Close() // read side; the body is already read
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("the site's API description answered %d", resp.StatusCode)
		}
		if ts, err = mcpgen.FromDescription(desc); err != nil {
			return nil, err
		}
		s.mcpToolsets.put(key, ts)
	}
	srv := mcpgen.NewServer(ts, &mcpgen.Executor{Base: base, Token: token, Client: client}, s.CLIVersion)
	// The owner's log of what their AI did: every tools/call, as it ended.
	mcpgen.OnToolCall(srv, func(tool string, started time.Time, err error) {
		s.recordMCPCall(key, tool, started, err)
	})
	return srv, nil
}

// v1InProcess answers a request by serving it through this instance's own
// /v1, without leaving the process.
//
// ⭐ The request is marked as made through /mcp (api.Via), which is the only
// way a key bound to /mcp is accepted at /v1: a client presenting that key to
// /v1 itself is refused.
type v1InProcess struct{ s *Server }

func (t v1InProcess) RoundTrip(req *http.Request) (*http.Response, error) {
	h := t.s.v1Handler()
	if h == nil {
		return nil, errors.New("this instance's API is not available")
	}
	in := req.Clone(api.Via(req.Context(), mcpEndpoint))
	if in.Body == nil {
		in.Body = http.NoBody
	}
	rec := &bufferedResponse{header: http.Header{}, status: http.StatusOK}
	// ⭐ The call carries the /mcp request's id, so the API's events for the
	// act join the request that made it.
	if id := RequestIDFromContext(req.Context()); id != "" {
		in.Header.Set("X-Request-Id", id)
	}
	in, _ = WithRequestID(rec, in)
	h.ServeHTTP(rec, in)
	return &http.Response{
		StatusCode: rec.status, Status: http.StatusText(rec.status), Header: rec.header,
		Body: io.NopCloser(bytes.NewReader(rec.body.Bytes())), Request: req,
		ProtoMajor: 1, ProtoMinor: 1,
	}, nil
}

// mcpToolsetCache keeps each key's generated tools for a minute, so /mcp does
// not describe the site and rebuild them on every request.
//
// ⭐ The tools depend only on what the site offers and what the key's grant
// allows. The key is still checked on every request and every call, so a
// revoked key or a withdrawn grant is refused at once, whatever is cached; a
// change to what the site offers shows within the minute.
type mcpToolsetCache struct {
	mu      sync.Mutex
	entries map[string]mcpToolsetEntry
}

type mcpToolsetEntry struct {
	ts *mcpgen.Toolset
	at time.Time
}

const (
	mcpToolsetTTL = time.Minute
	mcpToolsetMax = 256
)

func mcpToolsetKey(k ops.APIKey) string {
	return fmt.Sprintf("%s|%d", k.ID, ops.APIKeysGeneration())
}

func (c *mcpToolsetCache) get(k ops.APIKey) *mcpgen.Toolset {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[mcpToolsetKey(k)]
	if !ok || time.Since(e.at) > mcpToolsetTTL {
		return nil
	}
	return e.ts
}

func (c *mcpToolsetCache) put(k ops.APIKey, ts *mcpgen.Toolset) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil || len(c.entries) >= mcpToolsetMax {
		c.entries = map[string]mcpToolsetEntry{}
	}
	c.entries[mcpToolsetKey(k)] = mcpToolsetEntry{ts: ts, at: time.Now()}
}

// recordMCPCall logs one tools/call as an event: which tool, under which key,
// whether it worked and how long it took. ⭐ The event is the record; there is
// no call log on disk (its only reader, the v4 app's page, is gone).
func (s *Server) recordMCPCall(key ops.APIKey, tool string, started time.Time, err error) {
	s.LogEvent("pub.polis.mcp.tool_call", map[string]interface{}{
		"tool": tool, "key_id": key.ID, "ok": err == nil, "duration_ms": time.Since(started).Milliseconds(),
	})
}
