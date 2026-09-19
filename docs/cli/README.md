# CLI

The polis CLI is the primary tool for managing a polis site from the command line. It handles publishing, commenting, blessing, following, rendering, and discovery service operations.

## Architecture

Polis has two CLI implementations sharing the same **data formats**. They no longer share a
**feature set** — the Go CLI leads and the bash CLI is feature-frozen. See
[implementation-parity.md](implementation-parity.md) for the command matrix, the behavioural
divergences within shared commands, and the policy.

| Implementation | Language | Status | Version |
|---------------|----------|--------|---------|
| **Go CLI** (`cli-go/`) | Go | Active development | `cli-go/version.txt` |
| **Bash CLI** (`cli-bash/`) | Bash | Feature-frozen (release version tracks Go CLI) | Inline `VERSION=` in `bin/polis` |

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
| CLI-only | `polis` | CLI commands, no HTTP server | ~15 MB |
| Webapp-only | `polis-server` | HTTP server + web UI | ~17 MB |
| Bundled | `polis-full` | CLI + `serve` command | ~18 MB |

## Quick Start

```bash
# Build
cd cli-go && go build -o polis ./cmd/polis

# Run tests
cd cli-go && go test ./...

# Initialize a site
./polis init

# Publish a post
./polis post content.md

# See all commands
./polis --help
```

## Documentation

| Document | Audience | Description |
|----------|----------|-------------|
| [user/command-reference.md](user/command-reference.md) | Users | Complete CLI command reference |
| [user/json-mode.md](user/json-mode.md) | Users | Machine-readable `--json` output format |
| [user/policies.md](user/policies.md) | Users | Inbound policy — who may comment on your posts and what gets blessed |
| [user/templating.md](user/templating.md) | Users | Template syntax, variables and snippets |
| [implementation-parity.md](implementation-parity.md) | Users, Developers | Go and bash command matrix and behavioural divergences |
| [developer/packages.md](developer/packages.md) | Developers | Package structure, import rules, version propagation |

## See Also

- [cli-go/README.md](../../cli-go/README.md) — Go CLI build instructions and library usage
- [`cli-bash/polis`](../../cli-bash/polis) — Bash CLI (feature-frozen)
- [docs/general/concepts/content-system.md](../general/concepts/content-system.md) — Content types and filesystem layout
