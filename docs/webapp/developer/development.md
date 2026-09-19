# Webapp Development Guide

*For* [Contributors](../../README.md#contributing-to-polis) — *Kind* [Guide](../../README.md#kinds-of-page) — *Component* [Webapp](../README.md) — *See also* [tour](../../handbook/README.md)

Development patterns, testing, and architecture for the polis webapp.

## Critical Rules

1. **Drift detection:** Whenever you modify webapp code, check the bash CLI (the `polis` script in `cli-bash/`) for the equivalent `cmd_*` function. Flag any behavioral differences.
2. **Tests required:** Add or update tests for every change. Run `go test ./...` after changes.
3. **Dependency rule:** The webapp imports from `cli-go/pkg/`. Never put shared logic in `internal/`.

## Directory Structure

```
webapp/
├── cmd/server/main.go          # Standalone entry point
├── cmd/polis-full/main.go      # Bundled CLI+server entry point
├── cmd/hosted/, cmd/patrol/,   # Hosted service and standalone actor binaries
│   cmd/medic/, cmd/tailor/
├── internal/server/
│   ├── server.go               # Server struct, config, logging, sync loop
│   ├── routes.go               # Route registration (~100 paths)
│   ├── handlers.go             # Most /api/* handlers
│   ├── handlers_stream.go      # Stream items, counts, bodies
│   ├── handlers_pql.go         # GET /pql/<sentence>
│   ├── sync.go                 # Unified DS sync + its handlers
│   ├── middleware.go           # Request logging, correlation IDs
│   ├── handlers_test.go        # Handler tests
│   └── server_test.go          # Server/validation tests
├── internal/api/
│   ├── router.go               # v1 content API routes
│   ├── handlers.go             # Thin HTTP → Dispatch → JSON
│   └── middleware.go           # Auth, CORS, body limits
├── internal/serve/             # Shared public-content serving (hosted + reader/mirror modes)
├── internal/webui/
│   ├── assets.go               # Shared embedded FS (//go:embed www/*)
│   └── www/                    # SPA source (index.html, app.js, owner-extras.js, pql.js, dm*.js, style.css)
└── internal/hosted/            # Multi-tenant service (polis.pub)
```

## Adding an API Endpoint

### 1. Register the route (`routes.go`)

Handlers that accept a body are wrapped in `limitBody`, as the neighbouring routes are:

```go
mux.HandleFunc("/api/your-endpoint", limitBody(s.handleYourEndpoint, MaxDefaultBodySize))
```

### 2. Add the handler (`handlers.go`, or the `handlers_*.go` file for its area)

Follow the existing pattern:
1. Method check
2. Precondition checks (keys, config)
3. Parse request body
4. Business logic (use `cli-go/pkg/` packages)
5. JSON response

### 3. Add the frontend (`app.js` or `owner-extras.js`)

`app.js` holds the `App` object; `owner-extras.js` holds the owner-only stream chrome (icon presets, inline editor, bless/edit rollovers). Owner-only behaviour on the stream belongs in `owner-extras.js`.

```javascript
async yourFeature() {
    const response = await fetch('/api/your-endpoint', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ field: value })
    });
    // ...
}
```

### 4. Add tests (`handlers_test.go`)

```go
func TestHandleYourEndpoint(t *testing.T) {
    s := newConfiguredServer(t)
    body := jsonBody(t, map[string]string{"field": "value"})
    req := httptest.NewRequest(http.MethodPost, "/api/your-endpoint", body)
    w := httptest.NewRecorder()
    s.handleYourEndpoint(w, req)
    if w.Code != http.StatusOK {
        t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
    }
}
```

Always test: happy path, wrong HTTP method, missing preconditions, invalid input.

## Test Helpers

| Helper | Purpose |
|--------|---------|
| `newTestServer(t)` | Temp dir with required subdirectories, no keys |
| `newConfiguredServer(t)` | Real Ed25519 keys, config, `.well-known/polis` |
| `jsonBody(t, v)` | Marshal to `*bytes.Buffer` for request bodies |

## Frontend Architecture

### App Object

Single global `App` object in `app.js` with SPA state and methods — among them `screens` (the screen elements), `counts` (cached badge counts: drafts, pending comments, feed unread, DM unread, …), `siteInfo`, `webappTheme`, and the `isHosted` / `isDev` flags. Some fields (`currentView`, `sidebarMode`) are left over from the retired sidebar UI.

The stream itself is not driven by `App`: it is the v4 shape's controller, `window.PolisStream` (`stream.js`), with owner-only chrome layered on by `owner-extras.js`. See the [URL-as-filter tour](../../handbook/url-as-filter.md).

### Screen Flow

```
Welcome Screen (no site) → Init / Link panel
Error Screen (invalid site)
Stream Screen (site configured) — every /_/ and /_/pql/<sentence> view
  └── inline editor card (new post, edit, comment)
Dashboard Screen — hosts /_/settings only
```

The v3 sidebar, dashboard views and full-screen editor are gone.

### UI Patterns

- **Screens:** Full-page views toggled via `.hidden` class (`showScreen(name)`)
- **Panels:** Wizard and detail panels (init, link, registration, follow, key rotation, notifications)
- **Toasts:** `this.showToast(message, type, duration)`
- **Confirm:** `await this.showConfirmModal(title, message, confirmText, cancelText, type)` — returns a Promise

### URL Routing (Deep-Linking)

The SPA has only three route shapes: `/_/` (default stream), `/_/pql/<sentence>` (any
PQL-filtered view — what every icon button and the sentence-filter widget load), and
`/_/settings`. Any other `/_/…` path falls through to the default stream. The old v3 page
routes (`/_/posts`, `/_/blessings`, `/_/social/*`, …) were retired.

Routes are registered in `internal/server/routes.go` — read it before changing route handling.

### CSS Design System

The webapp uses a **dual-theme** (light/dark) system of semantic CSS custom properties
defined on `[data-theme]`, not a fixed palette. Fonts: Inter (UI), Newsreader (serif
content), JetBrains Mono (editor). The full variable contract lives in
[Themes](../../general/concepts/themes.md#css-variable-contract) — treat it as authoritative rather than duplicating values here.

## Server Patterns

### Config Loading Order

1. `site.Validate()` — check site structure
2. `LoadConfig()` — read `.polis/webapp/config.json` (only if the site is valid)
3. `LoadKeys()` — read Ed25519 keypair from `.polis/keys/` (only if the site is valid)
4. `LoadEnv()` — search: `<data-dir>/.env` → `cwd/.env` → `~/.polis/.env`; its discovery settings override the config

### Logging

```go
s.LogInfo("message: %v", arg)    // Level 1
s.LogWarn("message: %v", arg)    // Level 1
s.LogError("message: %v", err)   // Level 1
s.LogDebug("message: %v", arg)   // Level 2
s.LogEvent("pub.polis.x.y", map[string]interface{}{...}) // structured event
```

Logs to `<data-dir>/.polis/logs/YYYY-MM-DD.log`. Thread-safe with mutex.

### Security

- Path traversal: `validatePostPath()`, `validateContentPath()` — no `..`, no null bytes
- Post paths restricted to `content/pub.polis.core/post/`
- Content paths restricted to: `content/pub.polis.core/post/`, `content/pub.polis.core/comment/`, `.polis/bundles/pub.polis.core/posts/drafts/`, the rendered `posts/` and `comments/` mounts, and root-level `.md`/`.html`
- Draft IDs sanitized with an allow-list regex (`draftIDSanitizer`)

### JSON Response Convention

```json
{"success": true, "data": {...}}
```

Or domain-specific shapes: `{"posts": [...], "count": 5}`.

## Build Commands

```bash
# Build webapp
cd webapp && go build -o polis-server ./cmd/server

# Build bundled (CLI + server)
cd webapp && go build -o polis-full ./cmd/polis-full

# Quick dev cycle
cd webapp && go build -o polis-server ./cmd/server && ./polis-server

# If cli-go packages changed, rebuild both
cd cli-go && go build ./... && go test ./... && \
cd ../webapp && go build -o polis-server ./cmd/server && go test ./...
```
