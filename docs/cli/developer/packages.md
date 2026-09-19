# CLI Package Structure

*For* [Contributors](../../README.md#contributing-to-polis) · [Developers](../../README.md#building-on-polis) — *Kind* [Reference](../../README.md#kinds-of-page) — *Component* [CLI](../README.md) — *Code* [`cli-go/pkg`](../../../cli-go/pkg)

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
| `attestation/` | Signed claims, `pub.polis.attestation` — issue, withdraw, verify, DS registration. Using them rather than changing them: [the recipe book](../../signet/recipes/README.md) |
| `license/` | The signed outbound licence, `pub.polis.license`, and the surfaces generated from it |
| `did/` | Projects the site's identity key (and its retired keys) into a `did:web` document |
| `actor/` | An operator's actor registry — sign, countersign, the actors' local `Guard`, and the stranger's check `CheckAction` behind `polis actor verify <action>` |
| `dm/` | Direct message encryption, storage, send/receive pipeline |
| `metadata/` | Content index entry type + append/update/remove (`index.jsonl`) |
| `index/` | Rebuilding the content index — **the one implementation**; `polis rebuild` and Tailor's `index-rebuild` both call it |
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
| `cache/` | Isolated, non-canonical local mirrors of foreign content (the verified copy of a blessed comment) |
| `httppool/` | Shared outbound HTTP client / pooling |
| `resolve/` | URL → canonical handle/site resolution |
| **Rendering / bundles** | |
| `render/` | Markdown to HTML + page rendering |
| `template/` | Mustache-like template engine |
| `theme/` | Theme template loading |
| `snippet/` | Snippet file management |
| `bundle/` | Bundle loading, registry, fixture install (`pub.polis.core`) |
| `pql/` | PQL sentence parsing and composition (the Go sibling of the JS parser) |
| `sitemap/` | Sitemap generation |
| **Site state** | |
| `site/` | Site initialization, `.well-known/polis`, key history |
| `sitecheck/` | The integrity predicates behind `polis validate` — the same code Patrol and Judge run |
| `hooks/` | Post-action automation |
| `policy/` | Policy rule loading and evaluation |
| `policycheck/` | Remote policy evaluation — composes `policy`, `remote` and `following` to check another site's policy |
| `url/` | URL normalization |
| `atomicfile/` | Atomic file writes (write-temp-then-rename) |
| **API / dispatch** | |
| `ops/` | Content-type dispatch engine (wraps packages for API) |
| **Background actors** (used by webapp's `polis-server` / `polis-full`) | |
| `patrol/` | Patrol — local integrity checks on tenant sites (keys, permissions, signatures, hashes) |
| `medic/` | Medic — safe, reversible fixes for what Patrol finds |
| `judge/` | Judge — cross-boundary verification (what a site publishes vs. what the discovery service witnessed) |
| `clerk/` | Clerk — state-parity measurement between a tenant's files and the discovery service's event log |
| `chaplain/` | Chaplain — reconciliation repairs for what Clerk measures |
| `agent/` | Rosie as the user's agent — grants, the in-signature marker, and her decisions |
| `cache/upkeep/` | The cache-upkeep sweep Medic runs daily on hosted — reconcile, re-verify and garbage-collect local caches (was `rosie/`) |
| `tailor/` | Tailor — diagnoses and auto-fixes a self-hosted site to bring it up to the current spec |

### `index/` — the contributor seam

`index.jsonl` is a projection with **many sources**: `publish` appends a post,
blessing a comment appends a comment, and a rebuild regenerates from the content
on disk. So the rebuild does not own the file — it composes it.

```go
// contributors.go
func Contributors() []Contributor {
    return []Contributor{
        {EntryTypePost, "pub.polis.post", "--posts", postEntries},
        {EntryTypeComment, "pub.polis.comment", "--comments", commentEntries},
        {EntryTypeTag, "pub.polis.tag", "--tags", tagEntries},
        {EntryTypeAttestation, attestation.TypeName, "--attestations", attestationEntries},
    }
}
```

`PlanContentIndex(siteDir, only)` computes the composed file without writing;
`RebuildContentIndex(siteDir, only)` writes it. `only` names the entry types to
regenerate — everything else is re-emitted **from its original bytes**, so no
field is lost to a struct that does not model it.

**Adding a content type to the index** is one struct literal plus a function
returning `[]metadata.IndexEntry`. ⚠️ It is deliberately a **list of functions**,
not a registry: no registration side effects, no lookup table, no interface with
one implementation. If it starts to look like a plugin framework, it has gone a
level too high.

⛔ **Never write `index.jsonl` from anywhere else.** The rule the package exists
to keep: *a projection may have many sources, and any regenerator that knows one
source must not own the whole file.* Two implementations of this file is exactly
how `polis rebuild` once published an index the site's own integrity checker
rejected. The entry schema is specified in
[`docs/general/concepts/content-system.md`](../../general/concepts/content-system.md).

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

The CLI entry point (`cmd/root.go`) propagates the version to the 11 packages that write generator strings into files:

```go
func Execute(version string) {
    publish.Version = version
    comment.Version = version
    metadata.Version = version
    following.Version = version
    index.Version = version
    site.Version = version
    tag.Version = version
    attestation.Version = version
    dm.Version = version
    license.Version = version
    actor.Version = version
}
```

Metadata files use the generator format (`polis-cli-go/X.Y.Z`) instead of the bare version. The bash CLI uses `polis-cli/$VERSION` format.

⚠️ **Wiring is only half of it.** A package whose `GetGenerator()` is never read writes nothing, and a
package that is read but never assigned writes `polis-cli-go/dev` — inside the signature, for a signed
type. Check both ends when adding one.

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
    "os"

    "github.com/vdibart/polis-cli/cli-go/pkg/publish"
)

// Publish a post programmatically. privateKey is the PEM bytes of
// .polis/keys/id_ed25519; the optional *publish.DiscoveryConfig registers
// the post with a discovery service.
privateKey, _ := os.ReadFile("/path/to/site/.polis/keys/id_ed25519")
result, err := publish.PublishPost("/path/to/site", markdown, "", privateKey)
// result.Path, result.Version, result.Signature
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

With `--json`, most commands wrap their result as `{"status": "success", "command": "<name>", "data": {...}}`,
but not all: `post`, `republish`, `comment`, `render`, `register`, `unregister`, `blessing grant` and
`blessing deny` print a flat object, `validate` prints its report, and every failure prints
`{"success": false, "error": "<message>"}` (from `exitError` in `cmd/root.go`). See
[`json-mode.md`](../user/json-mode.md#response-shapes).
