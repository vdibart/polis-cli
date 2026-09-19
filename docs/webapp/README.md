# Webapp

The polis webapp is a Go HTTP server with an embedded single-page application that manages a polis site through a browser. It provides the same capabilities as the CLI with a visual interface.

## Architecture

```
webapp/
├── cmd/server/        # Standalone entry point
├── cmd/polis-full/    # Bundled CLI + server entry point
├── cmd/hosted/        # Hosted service entry point (polis.pub)
├── cmd/patrol/, cmd/medic/, cmd/tailor/  # Standalone actor binaries
├── internal/server/   # HTTP handlers, routing, server logic, DS sync
├── internal/api/      # v1 REST API (content type operations)
├── internal/serve/    # Shared public-content serving
├── internal/webui/    # Embedded SPA (index.html, app.js, owner-extras.js, pql.js, style.css)
└── internal/hosted/   # Multi-tenant hosted service (polis.pub)
```

**Critical dependency rule:** The webapp imports core logic from `cli-go/pkg/`. It never shells out to any CLI binary, and shared logic never lives in `internal/`.

### SPA Architecture

The frontend is a single `App` object in `app.js`, with the logged-in stream rendered by the v4 shape's controller (`stream.js`) and owner-only chrome in `owner-extras.js`. Screens are toggled via CSS classes. Deep-linking uses the `/_/` path prefix: `/_/`, `/_/pql/<sentence>` and `/_/settings`.

## Quick Start

```bash
# Build and run
cd webapp && go build -o polis-server ./cmd/server && ./polis-server

# Run tests
cd webapp && go test ./...

# Build bundled binary (CLI + serve)
cd webapp && go build -o polis-full ./cmd/polis-full
```

## Documentation

| Document | Audience | Description |
|----------|----------|-------------|
| [user/user-manual.md](user/user-manual.md) | Users | How to use the local web interface |
| [developer/development.md](developer/development.md) | Developers | Handler patterns, testing, frontend architecture |
| [developer/feed-architecture.md](developer/feed-architecture.md) | Developers | The local feed cache: scopes, retention, sync |
| [designer/brand.md](designer/brand.md) | Designers | Wordmark, typography, the SOLS palette, voice |
| [designer/navigation.md](designer/navigation.md) | Designers | The topbar: avatar, icon presets, dots, the cross-site nav |
| [designer/pages.md](designer/pages.md) | Designers | The three surfaces: landing, webapp, foreign-site visit |

## See Also

- [webapp/README.md](../../webapp/README.md) — Full webapp architecture and API endpoints
- [Development guide](developer/development.md) — drift detection rules, handler patterns, build commands
- [docs/api/](../api/README.md) — v1 Content Type API documentation
