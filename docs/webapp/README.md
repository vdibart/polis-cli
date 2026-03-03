# Webapp

The polis webapp is a Go HTTP server with an embedded single-page application that manages a polis site through a browser. It provides the same capabilities as the CLI with a visual interface.

## Architecture

```
webapp/
├── cmd/server/        # Standalone entry point
├── cmd/polis-full/    # Bundled CLI + server entry point
├── internal/server/   # HTTP handlers, routing, server logic
├── internal/api/      # v1 REST API (content type operations)
├── internal/webui/    # Embedded SPA (index.html, app.js, style.css)
└── testdata/          # Test fixtures
```

**Critical dependency rule:** The webapp imports core logic from `cli-go/pkg/`. It never shells out to any CLI binary, and shared logic never lives in `internal/`.

### SPA Architecture

The frontend is a single `App` object in `app.js` with all state and methods. Screens are toggled via CSS classes. Deep-linking uses `/_/` path prefix with a route table.

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

## See Also

- [webapp/README.md](../../webapp/README.md) — Full webapp architecture and API endpoints
- [webapp/CLAUDE.md](../../webapp/CLAUDE.md) — Development guide with drift detection rules
- [docs/api/](../api/README.md) — v1 Content Type API documentation
