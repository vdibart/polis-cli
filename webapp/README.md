# Polis Webapp

Go HTTP server with an embedded single-page application for managing a Polis site
through the browser.

---

## Overview

The webapp provides a browser-based interface for all Polis operations: publishing posts,
managing comments and blessings, editing snippets, following other sites, and browsing
your feed. It embeds its SPA as static files compiled into the binary — no separate
frontend build step.

Core business logic lives in `cli-go/pkg/` packages, which the webapp imports. The
webapp adds HTTP handlers and a web UI on top.

---

## Architecture

```
webapp/
├── cmd/
│   ├── server/main.go          # Webapp-only entry point
│   └── polis-full/main.go      # Bundled CLI+server entry point
├── internal/
│   ├── server/
│   │   ├── server.go           # Server struct, Config, Initialize(), Run()
│   │   ├── routes.go           # Route registration (~60 endpoints)
│   │   ├── handlers.go         # HTTP handlers (~40 handlers)
│   │   ├── handlers_test.go    # Handler tests (httptest pattern)
│   │   └── server_test.go      # Server/validation tests
│   └── webui/
│       ├── assets.go           # Embedded filesystem (//go:embed www/*)
│       └── www/
│           ├── index.html      # SPA shell
│           ├── app.js          # Client logic (App object, 70+ methods)
│           └── style.css       # Dark theme, teal accents
└── go.mod
```

### Critical Dependency Rule

```
cli-go/pkg/  →  webapp/internal/server/
(owner)          (consumer)
```

The webapp imports from `cli-go/pkg/`. It never shells out to any CLI binary. If you
need new shared logic, add it to a `cli-go/pkg/` package and import it.

---

## Three Binary Targets

| Target | Binary | Entry Point | Contents |
|--------|--------|-------------|----------|
| CLI-only | `polis` | `cli-go/cmd/polis/` | CLI commands, no HTTP server |
| Webapp-only | `polis-server` | `webapp/cmd/server/` | HTTP server + web UI |
| Bundled | `polis-full` | `webapp/cmd/polis-full/` | CLI + `serve` command |

All three share the version from `cli-go/version.txt`.

---

## Building

**Prerequisites:** Go 1.22+

### Using Makefile (from repo root)

```bash
make webapp     # Build polis-server → dist/polis-server
make bundled    # Build polis-full → dist/polis-full
make all        # Build all three targets
make test       # Run all tests
```

### Manual builds

```bash
# Webapp-only
cd webapp
VERSION=$(cat ../../cli-go/version.txt)
go build -ldflags "-X main.Version=$VERSION" -o polis-server ./cmd/server

# Bundled
cd webapp
VERSION=$(cat ../../cli-go/version.txt)
go build -ldflags "-X main.Version=$VERSION" -o polis-full ./cmd/polis-full
```

### Dev cycle

```bash
# Quick rebuild and run
cd webapp && go build -o polis-server ./cmd/server && ./polis-server

# If cli-go packages changed, rebuild and test both
cd cli-go && go build ./... && go test ./... && \
cd ../webapp && go build -o polis-server ./cmd/server && go test ./...
```

---

## Running

The server operates on a **data directory** containing a Polis site:

```bash
# Default: current working directory
cd /path/to/my-site && polis-server

# Override with flag
polis-server --data-dir /path/to/my-site

# Bundled binary
polis-full serve --data-dir /path/to/my-site
```

Default port: `3000`. Override with `--port`.

---

## Frontend Architecture

### SPA Shell (`index.html`)

Single HTML file with all screens, panels, sidebar, and dashboard markup. Screens are
toggled via the `.hidden` CSS class.

### App Object (`app.js`)

Single global `App` object with all state and methods (70+). Key state:

| Property | Purpose |
|----------|---------|
| `currentView` | Active sidebar section (e.g., `'posts-published'`) |
| `viewMode` | `'list'` or `'browser'` (split-pane preview) |
| `counts` | Cached badge counts for sidebar sections |
| `browserState` | Navigation history for browser mode |
| `snippetState` | Snippet editor state |

### Screen Flow

```
Welcome Screen (no site) → Init / Link Panel
                         → Dashboard Screen (site configured)
                             ├── Sidebar navigation
                             ├── Editor Screen (post editing)
                             ├── Comment Screen (comment editing)
                             └── Snippet Screen (snippet editing)
```

### CSS Design System (`style.css`)

Dark theme with teal accents. Key variables:

```
--bg-color: #262626          (dark background)
--accent-color: #5fafaf      (teal primary)
--salmon: #d97054            (warm titles)
--cyan: #00d4ff              (branding)
--preview-bg: #d8d2c8        (parchment preview pane)
```

Monospace fonts: JetBrains Mono, Fira Code for editor areas.

### UI Patterns

- **Screens**: Full-page views toggled via `.hidden` class
- **Panels**: Slide-in overlays from right
- **Toasts**: `showToast(message, type, duration)` — type: `success` | `error` | `info`
- **Confirm**: `showConfirmModal(title, message, callback)`
- **Badges**: Count badges on sidebar items, updated via `loadAllCounts()`

---

## API

The site's API is `/v1`, described by the site itself at `/v1/openapi.json` and written up in the
[API reference](../docs/api/developer/reference.md). The owner app, agents, scripts and the CLI all use it.
What is left under `/api/` (signing in, the capability descriptor, the hosted account, the embeddable
widget, and two routes the v4 shape's public pages call) is listed, each with its reason, in
[the site API page](../docs/api/developer/site-api.md). `webapp/internal/server/routes.go` registers them.

---

## Data Directory Structure

```
data/
├── .polis/
│   ├── keys/id_ed25519[.pub]     # Ed25519 keypair
│   ├── drafts/                   # Post drafts (JSON)
│   ├── comments/
│   │   ├── drafts/               # Comment drafts
│   │   ├── pending/              # Awaiting blessing
│   │   └── denied/               # Denied comments
│   ├── themes/                   # Theme snippet overrides
│   ├── hooks/                    # Auto-discovered hook scripts
│   └── webapp-config.json        # Webapp settings
├── .well-known/polis             # Site identity (JSON)
├── .env                          # Runtime config (KEY=VALUE)
├── posts/                        # Published posts (markdown)
├── comments/                     # Blessed comments
├── snippets/                     # Global snippets
├── metadata/                     # Blessed comments index
└── logs/                         # YYYY-MM-DD.log files
```

---

## Testing

```bash
# Run all tests
cd webapp && go test ./...

# Verbose
go test -v ./...

# Specific package
go test -v ./internal/server/...
```

### Test Helpers

| Helper | Purpose |
|--------|---------|
| `newTestServer(t)` | Temp dir with subdirectories, no keys |
| `newConfiguredServer(t)` | Real Ed25519 keys, config, .well-known/polis |
| `jsonBody(t, v)` | Marshal to `*bytes.Buffer` for request bodies |

### What to Test

- Happy path (valid input, expected output)
- Method validation (wrong HTTP method returns 405)
- Missing preconditions (no keys, no config)
- Invalid input (bad JSON, missing fields)
- Edge cases specific to the feature

---

## Development Guide

For development instructions (drift detection, handler patterns, parity
checklists), see the [webapp development guide](../docs/webapp/developer/development.md).

## Centralized Documentation

For comprehensive webapp documentation, see [`docs/webapp/`](../docs/webapp/README.md):

- [User Manual](../docs/webapp/user/user-manual.md) — How to use the local web interface
- [Development Guide](../docs/webapp/developer/development.md) — Handler patterns, testing, frontend architecture
- [API Reference](../docs/api/developer/reference.md) — Content Type REST API
