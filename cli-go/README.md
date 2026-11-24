# Polis Go CLI

Go implementation of the Polis CLI, providing the same features as the bash CLI
with the added benefit of importable packages for the webapp.

## Building

**Prerequisites:** Go 1.21+

```bash
# Development build
go build ./cmd/polis

# Run
./polis render --force
./polis version
```

## Distribution Builds

```bash
# Linux (amd64)
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o polis-linux-amd64 ./cmd/polis

# macOS (Apple Silicon)
GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o polis-darwin-arm64 ./cmd/polis

# macOS (Intel)
GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o polis-darwin-amd64 ./cmd/polis

# Windows
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o polis-windows-amd64.exe ./cmd/polis
```

## Package Structure

Core packages in `pkg/` are designed to be imported by other Go applications:

| Package | Description |
|---------|-------------|
| `pkg/render` | Markdown to HTML conversion and page rendering |
| `pkg/template` | Mustache-like template engine |
| `pkg/theme` | Theme template loading |
| `pkg/signing` | Ed25519 cryptographic signing |
| `pkg/publish` | Post publishing logic |
| `pkg/comment` | Comment management |
| `pkg/blessing` | Blessing workflow |
| `pkg/discovery` | Discovery service HTTP client |
| `pkg/snippet` | Snippet file management |
| `pkg/metadata` | Public index (JSONL) management |
| `pkg/site` | Site validation and initialization |
| `pkg/hooks` | Post-action automation |
| `pkg/url` | URL normalization utilities |

## `pkg/` is an implementation detail, not a supported library surface

These packages exist so the webapp can import them instead of shelling out to
the CLI, which is what they were extracted from `webapp/internal/` to do. They
are shared between this module's binaries and the webapp, and nothing more:
**there is no API stability promise, no semantic versioning of the package
surface, and signatures change whenever the CLI needs them to.**

You are welcome to import them, and the examples below are accurate as of this
version — but pin a commit, because a minor release may change a signature
without saying so in the changelog. If you depend on behaviour here, the stable
contracts are the **data formats** (documented under `docs/`) and the **CLI
commands**, not the Go functions.

```go
import (
    "github.com/vdibart/polis-cli/cli-go/pkg/render"
    "github.com/vdibart/polis-cli/cli-go/pkg/storage"
    "github.com/vdibart/polis-cli/cli-go/pkg/publish"
)

// A site handle: where the site is, and what it is stored on.
site := storage.At("/path/to/site")

// Render all pages
renderer, _ := render.NewPageRenderer(render.PageConfig{
    Site:          site,
    CLIThemesDir:  "/path/to/themes",
    BaseURL:       "https://example.com",
    RenderMarkers: false,
})
stats, _ := renderer.RenderAll(true)

// Publish a post. This calls the package directly, so it does not render,
// run hooks or log the write the way `polis post` and /v1 do (those go
// through the action layer, cli-go/pkg/ops).
result, _ := publish.PublishPost(site, content, filename, privateKey)
```

## Commands

The Go CLI implements all Polis commands (`init`, `post`, `comment`, `render`, `blessing`, `follow`, `clone`, `migrate`, etc.). See the [CLI Command Reference](../docs/cli/user/command-reference.md) for the full list.

## Template System

The Go CLI implements the same Mustache-like template syntax as the bash CLI:

- `{{variable}}` - Variable substitution
- `{{> snippet}}` - Partial includes (global-first by default)
- `{{> theme:snippet}}` - Theme-first lookup
- `{{#section}}...{{/section}}` - Loop blocks

Resolution order for snippets without explicit extension: `.md` → `.html` → exact

## Testing

```bash
# Run all tests
go test ./...

# Run tests with verbose output
go test -v ./...

# Run specific package tests
go test -v ./pkg/render/...
```

## Centralized Documentation

For comprehensive CLI documentation, see [`docs/cli/`](../docs/cli/README.md):

- [Command Reference](../docs/cli/user/command-reference.md) — Complete CLI usage guide
- [JSON Mode](../docs/cli/user/json-mode.md) — Machine-readable output format
- [Templating](../docs/cli/user/templating.md) — Template syntax, variables and snippets
- [Package Structure](../docs/cli/developer/packages.md) — Import rules and version propagation
