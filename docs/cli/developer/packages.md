# CLI Package Structure

The Go CLI (`cli-go/`) provides importable packages used by both the CLI binary and the webapp.

## Package Hierarchy

All core packages live in `cli-go/pkg/`. Grouped by concern:

| Package | Purpose |
|---------|---------|
| **Entry points** | |
| `cmd/` | CLI command handlers, main dispatch logic |
| `signing/` | Ed25519 SSH signature format (compatible with `ssh-keygen -Y`) |
| **Content lifecycle** | |
| `publish/` | Post publishing (sign, write, index, register with DS) |
| `comment/` | Comment management (sign, beseech, pending/denied state) |
| `blessing/` | Blessing workflow (requests, grant, deny, beseech, sync) |
| `tag/` | Tag content type (apply, remove, sign, sync to DS) |
| `dm/` | Direct message encryption, storage, send/receive pipeline |
| `metadata/` | Public index management (`public.jsonl`) |
| `index/` | Index rebuilding |
| `version/` | Version history parsing/reconstruction |
| **Discovery / remote** | |
| `discovery/` | HTTP client for discovery service endpoints |
| `following/` | `following.json` management |
| `feed/` | Feed aggregation + cache (JSONL cache, read tracking, staleness) |
| `notification/` | Local notification CRUD |
| `stream/` | Store for DS state/config/cursors, notification/follow/blessing handlers |
| `remote/` | HTTP fetching for remote polis sites |
| `verify/` | Remote content signature/hash verification |
| `clone/` | Remote site cloning |
| `migrate/` | Domain migration |
| `httppool/` | Shared outbound HTTP client / pooling |
| `resolve/` | URL → canonical handle/site resolution |
| **Rendering / bundles** | |
| `render/` | Markdown to HTML + page rendering |
| `template/` | Mustache-like template engine |
| `theme/` | Theme template loading |
| `snippet/` | Snippet file management |
| `bundle/` | Bundle loading, registry, fixture install (`pub.polis.core`) |
| `sitemap/` | Sitemap generation |
| **Site state** | |
| `site/` | Site validation, initialization, `.well-known/polis` |
| `hooks/` | Post-action automation |
| `policy/` | Policy rule loading and evaluation |
| `policycheck/` | Policy evaluation engine (verb-by-type matrix, layered eval) |
| `url/` | URL normalization |
| `atomicfile/` | Atomic file writes (write-temp-then-rename) |
| **API / dispatch** | |
| `ops/` | Content-type dispatch engine (wraps packages for API) |
| **Background actors** (used by webapp's `polis-server` / `polis-full`) | |
| `patrol/` | Patrol — detect issues with tenant sites |
| `medic/` | Medic — fix issues Patrol surfaces |
| `judge/` | Judge — validate signatures, key continuity (proto-TOFU) |
| `clerk/` | Clerk — pre-registration intake |
| `chaplain/` | Chaplain — post-registration follow-up |
| `tailor/` | Tailor — replay Patrol/Medic changes |

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

The CLI entry point (`cmd/root.go`) propagates the version to the 7 packages that write generator strings into files:

```go
func Execute(version string) {
    publish.Version = version
    comment.Version = version
    metadata.Version = version
    following.Version = version
    index.Version = version
    site.Version = version
    tag.Version = version
}
```

Metadata files use the generator format (`polis-cli-go/X.Y.Z`) instead of the bare version. The bash CLI uses `polis-cli/$VERSION` format.

> Other packages (e.g. `tailor`) declare a `var Version = "dev"` but do not currently receive a propagated value from `Execute()` — they don't write generator strings to user-visible files today. If a new package starts emitting a generator marker, wire it into the list above.

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
