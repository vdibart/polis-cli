# Webapp Development Guide

Development patterns, testing, and architecture for the polis webapp.

## Critical Rules

1. **Drift detection:** Whenever you modify webapp code, check the bash CLI (`cli-bash/bin/polis`) for the equivalent `cmd_*` function. Flag any behavioral differences.
2. **Tests required:** Add or update tests for every change. Run `go test ./...` after changes.
3. **Dependency rule:** The webapp imports from `cli-go/pkg/`. Never put shared logic in `internal/`.

## Directory Structure

```
webapp/
├── cmd/server/main.go          # Standalone entry point
├── cmd/polis-full/main.go      # Bundled CLI+server entry point
├── internal/server/
│   ├── server.go               # Server struct, config, logging
│   ├── routes.go               # Route registration
│   ├── handlers.go             # All HTTP handlers (~40 endpoints)
│   ├── handlers_test.go        # Handler tests
│   └── server_test.go          # Server/validation tests
├── internal/api/
│   ├── router.go               # v1 content API routes
│   ├── handlers.go             # Thin HTTP → Dispatch → JSON
│   └── middleware.go           # Auth, CORS, body limits
├── internal/webui/
│   ├── assets.go               # Shared embedded FS (//go:embed www/*)
│   └── www/                    # SPA source (index.html, app.js, style.css)
└── internal/hosted/            # Multi-tenant service (polis.pub)
```

## Adding an API Endpoint

### 1. Register the route (`routes.go`)

```go
mux.HandleFunc("/api/your-endpoint", s.handleYourEndpoint)
```

### 2. Add the handler (`handlers.go`)

Follow the existing pattern:
1. Method check
2. Precondition checks (keys, config)
3. Parse request body
4. Business logic (use `cli-go/pkg/` packages)
5. JSON response

### 3. Add the frontend (`app.js`)

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

Single global `App` object in `app.js` with all state and methods:
- `currentView` — active sidebar section
- `viewMode` — `'list'` or `'browser'` (split-pane preview)
- `counts` — cached badge counts

### Screen Flow

```
Welcome Screen (no site) → Init/Link Panel
                         → Dashboard Screen (site configured)
                             ├── Sidebar navigation
                             ├── Editor Screen (full-screen editing)
                             └── Snippet Screen
```

### UI Patterns

- **Screens:** Full-page views toggled via `.hidden` class
- **Panels:** Slide-in from right (settings, comment detail)
- **Toasts:** `this.showToast(message, type, duration)`
- **Confirm:** `this.showConfirmModal(title, message, callback)`

### URL Routing (Deep-Linking)

SPA deep-links use `/_/` path prefix:

| URL | View |
|-----|------|
| `/_/posts` | Published posts |
| `/_/posts/drafts` | Post drafts |
| `/_/blessings` | Blessing requests |
| `/_/social/feed` | Activity feed |
| `/_/social/following` | Following list |
| `/_/settings` | Settings |

### CSS Design System

```css
--bg-color: #262626          /* dark background */
--accent-color: #5fafaf      /* teal primary */
--salmon: #d97054            /* warm titles */
--preview-bg: #d8d2c8        /* parchment preview */
```

Monospace fonts: JetBrains Mono, Fira Code.

## Server Patterns

### Config Loading Order

1. `site.Validate()` — check site structure
2. `LoadConfig()` — read `.polis/webapp-config.json`
3. `LoadKeys()` — read Ed25519 keypair
4. `LoadEnv()` — search: `data/.env` → `cwd/.env` → `~/.polis/.env`

### Logging

```go
s.LogInfo("message: %v", arg)    // Level 1
s.LogError("message: %v", err)   // Level 1
s.LogDebug("message: %v", arg)   // Level 2
```

Logs to `data/logs/YYYY-MM-DD.log`. Thread-safe with mutex.

### Security

- Path traversal: `validatePostPath()`, `validateContentPath()` — no `..`, no null bytes
- Content paths restricted to: `posts/`, `comments/`, `.polis/drafts/`, root `.md`/`.html`
- Draft IDs sanitized with whitelist regex

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
