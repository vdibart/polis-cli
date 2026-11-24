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
├── internal/webui/    # Embedded owner app (www/v5/: index.html, frame.js, api.js, the four sections) and the messages key's scripts
└── internal/hosted/   # Multi-tenant hosted service (polis.pub)
```

**Critical dependency rule:** The webapp imports core logic from `cli-go/pkg/`. It never shells out to any CLI binary, and shared logic never lives in `internal/`.

### The owner app

The owner app is the **v5 app** (`webui/www/v5/`), at `/_/` and below on every instance, whatever the site's shape. It reaches the site only through `/v1`, via `api.js`. `frame.js` owns the menu, the section box and the addresses; the four sections are `write.js`, `gather.js`, `message.js` and `enhance.js`, and one editor (`sheet.js`) opens in place over a section. Addresses are `/_/<section>`, a view as `/_/<section>/pql/<sentence>`, and an enhance group as `/_/enhance/<group>`; `/_/` opens the section the owner last left. The previous owner app was deleted on 2026-11-22. The rules every v5 page follows are the [style guide](designer/style-guide.md).

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
| [designer/style-guide.md](designer/style-guide.md) | Designers · Developers · agents | ⭐ The v5 style guide: the rules every v5 page follows (tokens, the frame, cards, alignment, words, small screens) |
| [designer/navigation.md](designer/navigation.md) | Designers | The topbar: avatar, icon presets, dots, the cross-site nav |
| [designer/pages.md](designer/pages.md) | Designers | The three surfaces: landing, webapp, foreign-site visit |

## See Also

- [webapp/README.md](../../webapp/README.md) — Full webapp architecture and API endpoints
- [Development guide](developer/development.md) — drift detection rules, handler patterns, build commands
- [docs/api/](../api/README.md) — v1 Content Type API documentation
