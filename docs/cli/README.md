# CLI

The polis CLI is the primary tool for managing a polis site from the command line. It handles publishing, commenting, blessing, following, rendering, and discovery service operations.

## Architecture

Polis has two CLI implementations sharing the same feature set and data formats:

| Implementation | Language | Status | Version |
|---------------|----------|--------|---------|
| **Go CLI** (`cli-go/`) | Go | Active development | `cli-go/version.txt` |
| **Bash CLI** (`cli-bash/`) | Bash | Feature-frozen at v0.45.0 | Inline in `bin/polis` |

The Go CLI owns all core packages in `cli-go/pkg/`. The webapp imports from these packages — the CLI is the source of truth for business logic.

```
cli-go/pkg/           (core logic — owns all packages)
    ↑
    imports from
    |
webapp/               (web UI — consumer, never the reverse)
```

## Three Build Targets

| Target | Binary | Contents | Size |
|--------|--------|----------|------|
| CLI-only | `polis` | CLI commands, no HTTP server | ~8-9 MB |
| Webapp-only | `polis-server` | HTTP server + web UI | ~11 MB |
| Bundled | `polis-full` | CLI + `serve` command | ~11-12 MB |

## Quick Start

```bash
# Build
cd cli-go && go build -o polis ./cmd/polis

# Run tests
cd cli-go && go test ./...

# Initialize a site
./polis init

# Publish a post
./polis post publish content.md

# See all commands
./polis --help
```

## Documentation

| Document | Audience | Description |
|----------|----------|-------------|
| [user/command-reference.md](user/command-reference.md) | Users | Complete CLI command reference |
| [user/json-mode.md](user/json-mode.md) | Users | Machine-readable `--json` output format |
| [user/templating.md](user/templating.md) | Users | Theme customization and template syntax |
| [developer/packages.md](developer/packages.md) | Developers | Package structure, import rules, version propagation |

## See Also

- [cli-go/README.md](../../cli-go/README.md) — Go CLI build instructions and library usage
- [cli-bash/README.md](../../cli-bash/README.md) — Bash CLI architecture (feature-frozen)
- [docs/general/content-system.md](../general/content-system.md) — Content types and filesystem layout
