# CLI Package Structure

The Go CLI (`cli-go/`) provides importable packages used by both the CLI binary and the webapp.

## Package Hierarchy

All core packages live in `cli-go/pkg/`:

| Package | Purpose |
|---------|---------|
| `cmd/` | CLI command handlers, main dispatch logic |
| `signing/` | Ed25519 SSH signature format (compatible with `ssh-keygen -Y`) |
| `publish/` | Post publishing (sign, write, index, register with DS) |
| `comment/` | Comment management (sign, beseech, pending/denied state) |
| `blessing/` | Blessing workflow (requests, grant, deny, beseech, sync) |
| `discovery/` | HTTP client for discovery service endpoints |
| `following/` | `following.json` management |
| `feed/` | Feed aggregation + cache (JSONL cache, read tracking, staleness) |
| `notification/` | Local notification CRUD |
| `stream/` | Store for DS state/config/cursors, notification/follow/blessing handlers |
| `render/` | Markdown to HTML + page rendering |
| `template/` | Mustache-like template engine |
| `theme/` | Theme template loading |
| `snippet/` | Snippet file management |
| `metadata/` | Public index management (`index.jsonl`) |
| `site/` | Site validation, initialization, `.well-known/polis` |
| `hooks/` | Post-action automation |
| `remote/` | HTTP fetching for remote polis sites |
| `verify/` | Remote content signature/hash verification |
| `clone/` | Remote site cloning |
| `version/` | Version history parsing/reconstruction |
| `index/` | Index rebuilding |
| `migrate/` | Domain migration |
| `dm/` | Direct message encryption, storage, send/receive pipeline |
| `ops/` | Content-type dispatch engine (wraps packages for API) |
| `url/` | URL normalization |
| `policy/` | Policy rule loading and evaluation |

## Import Rules

1. **CLI packages are the source of truth.** The webapp imports from `cli-go/pkg/`, never the reverse.
2. **No circular dependencies.** Packages in `cli-go/pkg/` do not import from `webapp/`.
3. **No shared logic in webapp.** If both CLI and webapp need something, it goes in `cli-go/pkg/`.

```
cli-go/pkg/  →  webapp/internal/server/
(owner)          (consumer)
```

## Version Propagation

All packages that write version strings into files follow this pattern:

```go
// At package level
var Version = "dev"

func GetGenerator() string { return "polis-cli-go/" + Version }
```

The CLI entry point (`cmd/root.go`) propagates the version to all packages:

```go
func Execute(version string) {
    publish.Version = version
    comment.Version = version
    metadata.Version = version
    following.Version = version
    // ... 10 packages total
}
```

Metadata files use the generator format (`polis-cli-go/X.Y.Z`) instead of the bare version. The bash CLI uses `polis-cli/$VERSION` format.

### Adding a New Package That Writes Version

1. Add `var Version = "dev"` after imports
2. Add `func GetGenerator() string { return "polis-cli-go/" + Version }`
3. Add `<pkg>.Version = Version` in `cmd/root.go` Execute()
4. Use `GetGenerator()` (not bare `Version`) when writing to metadata files
5. Add test verifying written version matches `GetGenerator()`

## Using as a Library

The Go CLI packages can be imported by external programs:

```go
import (
    "github.com/vdibart/polis-cli/cli-go/pkg/publish"
    "github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// Publish a post programmatically
result, err := publish.Publish(publish.Options{
    DataDir:  "/path/to/site",
    File:     "content.md",
    Key:      privateKey,
    BaseURL:  "https://mysite.com",
})
```

## Building

```bash
cd cli-go

# Check compilation
go build ./...

# Run tests
go test ./...

# Build CLI binary
VERSION=$(cat version.txt)
go build -ldflags "-X main.Version=$VERSION" -o polis ./cmd/polis
```

## Output Format

The CLI uses consistent output prefixes:

| Prefix | Meaning |
|--------|---------|
| `[✓]` | Success |
| `[i]` | Information |
| `[x]` | Error |
| `[!]` | Warning |

With `--json`, all output follows:
```json
{"status": "success", "command": "<name>", "data": {...}}
{"status": "error", "command": "<name>", "error": {"code": "...", "message": "..."}}
```
