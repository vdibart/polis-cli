# Dispatch Engine Architecture

*For* [Contributors](../../README.md#contributing-to-polis) — *Kind* [Reference](../../README.md#kinds-of-page) — *Component* [Webapp](../../webapp/README.md) — *Code* [`cli-go/pkg/ops`](../../../cli-go/pkg/ops)

The content type API is built on a two-layer architecture: a **dispatch engine** that routes operations to handlers, and a **REST API layer** that translates HTTP into engine calls.

This engine is the action layer's content half: every caller of a content operation (the owner app, agents, scripts, MCP, and the CLI in-process through `cli-go/pkg/cmd/engine.go`) reaches the same handler through it.

> **Currently wired content types** (handled end-to-end by `BuiltinCoreHandler` for the `pub.polis.core` bundle): `pub.polis.post` (list, create, update, unpublish, save_draft, discard_draft), `pub.polis.comment` (create, update), `pub.polis.follow` (list), `pub.polis.dm` (list/get/send/deliver/protection_status/mark_read/delete/retry), `pub.polis.tag` (list/apply/remove/delete), `pub.polis.theme` (list, get), `pub.polis.license` (get). The core bundle also declares `pub.polis.attestation` and `pub.polis.actor`, which the handler does not serve at all. `Actions()` is derived from the one declaration of these operations, `builtin_core_ops.go`, so it lists exactly these. Blessing decisions and following are `/v1` operations the host declares and serves (`webapp/internal/api/blessing.go`, `follow.go`). Any other action on these routes is not offered: the handler answers `400` `unsupported_action`, and the description does not list it. See [reference.md § Current Implementation Status](reference.md#current-implementation-status) for the full matrix.

## Dispatch Engine (`cli-go/pkg/ops/`)

The engine routes `ActionRequest` objects to the appropriate handler based on content type:

```
HTTP Request → Router → ActionRequest → Engine.Dispatch() → Handler → ActionResult → JSON Response
```

Key files:
- `engine.go` — Engine struct, Dispatch(), handler registry, type name resolution
- `handler.go` — Handler interface + ExecutableHandler + HTTPHandler
- `builtin_core_ops.go` — the one declaration of the core operations (type, action, method, path, input, auth, scope), from which `Actions()` and `/v1/openapi.json` are generated
- `builtin_core.go` — BuiltinCoreHandler for pub.polis.core operations
- `builtin_core_answers.go` — each operation's success answer (an operation without one panics at start)
- `auth.go` — API key validation (`ValidateAPIKey`)
- `afterwrite.go` — what a write causes, for every caller: the render, the owner's hook, the draft it came
  from, and the write's event
- `comment_write.go`, `dm_send.go`, `follow.go` — the one implementation of writing a comment, sending a
  message and following, which the engine, the web app, the widget and the CLI all call

⭐ **What a write causes is the after-write module's, not the host's.** The engine reports each successful
write as a typed `Written` value (the operation, the path, title and version, the draft it came from, the
agent's marker, the answer) to the host's `AfterWrite`, which is the shared module's `Apply`. Each operation
declares its effects there: its event, its render (none, the one post, or the whole site; a post renders
incrementally where the shape can, and in full if that fails), its hook, and whether it removes a draft. A
host supplies only what differs: where events go, the renderer, whether hooks may run (the CLI never runs
them), and extras of its own.

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

- `pub.polis.post/create` → `publish.PublishPostAs()` (with the agent's marker, if any)
- `pub.polis.post/update` → republishes the post as a new signed version
- `pub.polis.post/unpublish`, `save_draft`, `discard_draft` → take a post down to a draft; keep or discard the owner's drafts
- `pub.polis.post/list` → reads `index.jsonl`
- `pub.polis.comment/create` → `ops.WriteComment()` (`comment_write.go`: sign, then send with `comment.BeseechComment()`)
- `pub.polis.comment/update` → `comment.RepublishCommentAs()`
- `pub.polis.follow/list` → `following.Load()`
- `pub.polis.dm/{list,get,send,deliver,protection_status,mark_read,delete,retry}` → `dm.*`
- `pub.polis.tag/list` → lists tags with optional filters
- `pub.polis.tag/apply` → applies a tag to a target URL
- `pub.polis.tag/remove` → removes a tag from a target URL
- `pub.polis.tag/delete` → deletes a tag and all associations
- `pub.polis.theme/list` → returns themes declared by the active bundle (with `active` field)
- `pub.polis.theme/get` → returns a single theme's manifest entry
- `pub.polis.license/get` → `site.SiteTerms()`

Each operation is a method on `BuiltinCoreHandler`, reached from the action dispatch switch, and declared once in `builtin_core_ops.go`.

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

Drafts have no routes of their own: they are saved and discarded by the post type's owner-only actions
(`save_draft`, `discard_draft`) and read through the query operation (`all drafts from me`).

### Auth

`routeContent` in `router.go` calls `requireScope` with the scope the operation declares (`scopeFor`; `full` for anything undeclared). The owner's own sign-in passes every scope. Otherwise the Bearer token from the `Authorization` header is hashed with SHA-256 and looked up in `.polis/api-keys.json`; a scoped token whose grant lacks the scope is refused with `403` and the code `insufficient_scope`, naming the scope in `WWW-Authenticate`. A scoped token's grant is then resolved on every request ([MCP and agent access](mcp.md#grants-scopes-and-the-marker)). GET requests on content and bundle routes bypass auth, except on any type the core bundle does not declare public: the private types (`dm`, `follow`) and any type it does not know. Unauthenticated, an unknown type is therefore `401`, not `404`.

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

- `ops.MintAPIKey` returns the plaintext once and stores only its SHA-256 hash; `ops.RevokeAPIKey` deletes an entry; `ops.ListAPIKeys` lists them. The owner reaches them through the `/v1/agents/tokens` operations (making one is sign-in only)
- `ops.LookupAPIKey` returns the matching key with its `scopes`, compared in constant time; `ops.ValidateAPIKey` accepts only a **full-access** (unscoped) key, because its callers grant everything
- Each entry carries an `id`, a `name`, a `created_at` timestamp and, optionally, `scopes` and `bound`
- `bound` is the one path a key works at (`/mcp`); `APIKey.UsableAt` checks it, at `/mcp` and at `/v1`, which accepts a bound key only on a call the instance's `/mcp` makes for it in-process. `ops.MintBoundAPIKey` refuses to bind an unscoped key, so a bound key is always scoped. Bound keys were minted for Kay, whose page went with the previous owner app (each a personal token with its own grant, `ops.MintBoundPersonalToken`); nothing in this build calls that now, and `/v1/agents/tokens` mints unbound ones
- A rewrite carries every member it does not model through unchanged, since the file is unsigned

## Adding a New Operation

To wire a new operation for an existing content type:

1. Implement the operation in the appropriate `cli-go/pkg/` package
2. Declare it in `builtin_core_ops.go` (type, action, method, a path under the [path rule](reference.md#paths), input schema, auth and scope, whether it writes or signs), and its success answer in `builtin_core_answers.go`. `Actions()` and the API's description follow from the declaration
3. Add its case to the handler's dispatch in `builtin_core.go` — `TestActionsAreExactlyWhatTheHandlerAccepts` fails when the declaration and the handler disagree in either direction
4. If it writes, declare what the write causes (its event, its render, its hook, whether it removes a draft) in `afterwrite.go`'s `writeEffects` — `TestEveryWriteOperationDeclaresItsEffects` fails until it is there
5. The REST routes already handle all standard CRUD + custom actions — no route changes needed
6. Add tests in `cli-go/pkg/ops/` beside the existing ones (e.g. `comment_update_test.go`)

A third-party bundle cannot be added this way today. The engine accepts any number of bundles, but the webapp builds it from exactly one — the site's `content/pub.polis.core/bundle.json`, or the built-in default — and nothing discovers others.
