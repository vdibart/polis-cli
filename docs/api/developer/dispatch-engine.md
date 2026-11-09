# Dispatch Engine Architecture

*For* [Contributors](../../README.md#contributing-to-polis) — *Kind* [Reference](../../README.md#kinds-of-page) — *Component* [Webapp](../../webapp/README.md) — *Code* [`cli-go/pkg/ops`](../../../cli-go/pkg/ops)

The content type API is built on a two-layer architecture: a **dispatch engine** that routes operations to handlers, and a **REST API layer** that translates HTTP into engine calls.

> **Currently wired content types** (handled end-to-end by `BuiltinCoreHandler` for the `pub.polis.core` bundle): `pub.polis.post` (list, create), `pub.polis.comment` (create, update), `pub.polis.follow` (list), `pub.polis.dm` (list/get/send/deliver/protection_status/mark_read/delete/retry), `pub.polis.tag` (list/apply/remove/delete), `pub.polis.theme` (list, get), `pub.polis.license` (get). The core bundle also declares `pub.polis.attestation` and `pub.polis.actor`, which the handler does not serve at all. `Actions()` lists exactly these. Other standard actions have REST routes, and the handler returns "unsupported action" until they are wired. See [reference.md § Current Implementation Status](reference.md#current-implementation-status) for the full matrix.

## Dispatch Engine (`cli-go/pkg/ops/`)

The engine routes `ActionRequest` objects to the appropriate handler based on content type:

```
HTTP Request → Router → ActionRequest → Engine.Dispatch() → Handler → ActionResult → JSON Response
```

Key files:
- `engine.go` — Engine struct, Dispatch(), handler registry, type name resolution
- `handler.go` — Handler interface + ExecutableHandler + HTTPHandler
- `builtin_core.go` — BuiltinCoreHandler for pub.polis.core operations
- `auth.go` — API key validation (`ValidateAPIKey`)

There is no render helper in the package: the host passes an `OnContentChanged` callback (the webapp's re-render) when it builds the engine.

### Engine

The `Engine` struct holds:
- A map of bundle names to `Handler` implementations
- Type name resolution (short names like `post` → fully-qualified `pub.polis.post`)
- Bundle introspection (which types exist, which actions each supports)

`Engine.Dispatch(ctx, ActionRequest)` finds the bundle owning the requested content type, resolves to the appropriate handler, and calls `Handler.Handle()`.

### ActionRequest / ActionResult

```go
type ActionRequest struct {
    Action      string         // "create", "list", "bless", etc.
    ContentType string         // "pub.polis.post" (always fully-qualified after resolution)
    Payload     map[string]any // Action-specific input
}

type ActionResult struct {
    Status string         // "success" or "error"
    Data   map[string]any // Action-specific output
}
```

### Handler Interface

```go
type Handler interface {
    Handle(ctx context.Context, req ActionRequest, env HandlerEnv) (*ActionResult, error)
    Actions(contentType string) []string
}
```

`HandlerEnv` provides the handler with site context (data directory, signing key, base URL, discovery client).

## Handler Types

| Type | Description | Use Case |
|------|-------------|----------|
| `builtin` | Go code in BuiltinCoreHandler | pub.polis.core types |
| `executable` | JSON stdin/stdout to external binary | Custom bundle with local script |
| `http` | JSON POST to URL | Custom bundle with remote service |

Bundles declare their handler type in `bundle.json`. The engine instantiates the appropriate handler at startup.

### BuiltinCoreHandler

Handles all `pub.polis.core` content types by calling into `cli-go/pkg/` packages:

- `pub.polis.post/create` → `publish.PublishPost()`
- `pub.polis.post/list` → reads `index.jsonl`
- `pub.polis.comment/create` → `comment.BeseechComment()`
- `pub.polis.comment/update` → `comment.RepublishComment()`
- `pub.polis.follow/list` → `following.Load()`
- `pub.polis.dm/{list,get,send,deliver,protection_status,mark_read,delete,retry}` → `dm.*`
- `pub.polis.tag/list` → lists tags with optional filters
- `pub.polis.tag/apply` → applies a tag to a target URL
- `pub.polis.tag/remove` → removes a tag from a target URL
- `pub.polis.tag/delete` → deletes a tag and all associations
- `pub.polis.theme/list` → returns themes declared by the active bundle (with `active` field)
- `pub.polis.theme/get` → returns a single theme's manifest entry
- `pub.polis.license/get` → `site.SiteTerms()`

Each operation is a method on `BuiltinCoreHandler`. New operations are wired by adding a case to the action dispatch switch.

### ExecutableHandler

For `executable` bundles, the handler:
1. Serializes the `ActionRequest` as JSON
2. Writes it to the executable's stdin
3. Reads JSON from stdout
4. Deserializes into `ActionResult`

The executable path comes from `bundle.json`.

### HTTPHandler

For `http` bundles, the handler:
1. Serializes the `ActionRequest` as JSON
2. POSTs it to the handler URL from `bundle.json`
3. Reads the JSON response
4. Deserializes into `ActionResult`

## REST API Layer (`webapp/internal/api/`)

Thin HTTP layer that translates REST conventions into `ActionRequest` objects:

- `router.go` — Route registration, path parsing, auth enforcement (Bearer keys and the DM signed-request actions)
- `handlers.go` — HTTP → Dispatch → JSON adapters, error mapping
- `middleware.go` — body size limit, and a CORS hook that is currently a no-op (no CORS headers are sent)

### Route → Action Mapping

| HTTP | Route | Action |
|------|-------|--------|
| GET | `/v1/content/{type}` | `list` |
| GET | `/v1/content/{type}/{id}` | `get` |
| POST | `/v1/content/{type}` | `create` |
| PUT | `/v1/content/{type}/{id}` | `update` |
| DELETE | `/v1/content/{type}/{id}` | `delete` |
| POST | `/v1/content/{type}/actions/{action}` | `{action}` |
| GET | `/v1/content/{type}/drafts` | `draft.list` |
| GET | `/v1/content/{type}/drafts/{id}` | `draft.get` |
| POST | `/v1/content/{type}/drafts` | `draft.save` |
| DELETE | `/v1/content/{type}/drafts/{id}` | `draft.delete` |

### Auth

`routeContent` in `router.go` extracts the Bearer token from the `Authorization` header, hashes it with SHA-256, and checks against stored hashes in `.polis/api-keys.json` with `ops.LookupAPIKey`. A matching key that is scoped (and so every bound key) is refused with `403` and the code `insufficient_scope`: content is an authored source, and no scope covers one. GET requests on content and bundle routes bypass auth, except drafts and any type the core bundle does not declare public: the private types (`dm`, `follow`) and any type it does not know. Unauthenticated, an unknown type is therefore `401`, not `404`.

### Error Mapping

Engine errors are mapped to HTTP status codes:

| Engine Error | HTTP Status |
|-------------|-------------|
| Unknown content type | 404 (to an authenticated caller; `401` without auth) |
| Unsupported action | 400 |
| Validation failure | 400 |
| Auth failure | 401/403 (`403 insufficient_scope` for a scoped key) |
| Internal error | 500 |

## API Key Management

Keys live in `.polis/api-keys.json` (mode `600`), in `cli-go/pkg/ops/auth.go` (see [reference.md § Authentication](reference.md#authentication)):

- `ops.MintAPIKey` returns the plaintext once and stores only its SHA-256 hash; `ops.RevokeAPIKey` deletes an entry; `ops.ListAPIKeys` lists them. The owner reaches them through the webapp's `/api/keys` routes
- `ops.LookupAPIKey` returns the matching key with its `scopes`, compared in constant time; `ops.ValidateAPIKey` accepts only a **full-access** (unscoped) key, because its callers grant everything
- Each entry carries an `id`, a `name`, a `created_at` timestamp and, optionally, `scopes` and `bound`
- `bound` is the one path a key works at (`/mcp`, `/mcp/c/<component>`); `APIKey.UsableAt` checks it. `ops.MintBoundAPIKey` refuses to bind an unscoped key, so a bound key is always scoped. The webapp mints bound keys only for Kay; `/api/keys` mints unbound ones
- A rewrite carries every member it does not model through unchanged, since the file is unsigned

## Adding a New Operation

To wire a new operation for an existing content type:

1. Implement the operation in the appropriate `cli-go/pkg/` package
2. Add a case to `BuiltinCoreHandler.Handle()` in `builtin_core.go`
3. Add the action name to `BuiltinCoreHandler.Actions()` for the content type, in the same change — `TestActionsAreExactlyWhatTheHandlerAccepts` fails when the two disagree in either direction
4. The REST routes already handle all standard CRUD + custom actions — no route changes needed
5. Add tests in `cli-go/pkg/ops/` beside the existing ones (e.g. `comment_update_test.go`)

A third-party bundle cannot be added this way today. The engine accepts any number of bundles, but the webapp builds it from exactly one — the site's `content/pub.polis.core/bundle.json`, or the built-in default — and nothing discovers others.
