# Dispatch Engine Architecture

The content type API is built on a two-layer architecture: a **dispatch engine** that routes operations to handlers, and a **REST API layer** that translates HTTP into engine calls.

## Dispatch Engine (`cli-go/pkg/ops/`)

The engine routes `ActionRequest` objects to the appropriate handler based on content type:

```
HTTP Request → Router → ActionRequest → Engine.Dispatch() → Handler → ActionResult → JSON Response
```

Key files:
- `engine.go` — Engine struct, Dispatch(), handler registry, type name resolution
- `handler.go` — Handler interface + ExecutableHandler + HTTPHandler
- `builtin_core.go` — BuiltinCoreHandler for pub.polis.core operations
- `auth.go` — API key generation, validation, revocation
- `render.go` — Content-type-aware site render helper

### Engine

The `Engine` struct holds:
- A map of bundle names to `Handler` implementations
- Type name resolution (short names like `post` → fully-qualified `pub.polis.post`)
- Bundle introspection (which types exist, which actions each supports)

`Engine.Dispatch(ctx, ActionRequest)` finds the bundle owning the requested content type, resolves to the appropriate handler, and calls `Handler.Handle()`.

### ActionRequest / ActionResult

```go
type ActionRequest struct {
    Action      string          // "create", "list", "bless", etc.
    ContentType string          // "pub.polis.post" (always fully-qualified after resolution)
    Payload     json.RawMessage // Action-specific input
}

type ActionResult struct {
    Status string      // "success" or "error"
    Data   interface{} // Action-specific output
}
```

### Handler Interface

```go
type Handler interface {
    Handle(ctx context.Context, req ActionRequest, env HandlerEnv) (ActionResult, error)
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

- `pub.polis.post/create` → `publish.Publish()`
- `pub.polis.post/list` → reads `index.jsonl`
- `pub.polis.comment/create` → `blessing.Beseech()`
- `pub.polis.follow/list` → `following.Load()`

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

- `router.go` — Route registration, path parsing, auth enforcement
- `handlers.go` — HTTP → Dispatch → JSON adapters, error mapping
- `middleware.go` — CORS, body size limits, auth middleware

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

### Auth Middleware

The auth middleware extracts the Bearer token from the `Authorization` header, hashes it with SHA-256, and checks against stored hashes in `.polis/api-keys.json`. GET requests on content and bundle routes bypass auth.

### Error Mapping

Engine errors are mapped to HTTP status codes:

| Engine Error | HTTP Status |
|-------------|-------------|
| Unknown content type | 404 |
| Unsupported action | 400 |
| Validation failure | 400 |
| Auth failure | 401/403 |
| Internal error | 500 |

## API Key Management

API keys are managed via `ops.GenerateAPIKey()`, `ops.ValidateAPIKey()`, and `ops.RevokeAPIKey()`:

- Keys are generated as random hex strings prefixed with `polis_`
- Only SHA-256 hashes are stored (in `.polis/api-keys.json`)
- The plaintext key is returned exactly once at creation time
- Keys include a name and creation timestamp for management

## Adding a New Operation

To wire a new operation for an existing content type:

1. Implement the operation in the appropriate `cli-go/pkg/` package
2. Add a case to `BuiltinCoreHandler.Handle()` in `builtin_core.go`
3. Add the action name to `BuiltinCoreHandler.Actions()` for the content type
4. The REST routes already handle all standard CRUD + custom actions — no route changes needed
5. Add tests in `builtin_core_test.go`

To add a new content type via a third-party bundle:

1. Create `content/<bundle>/bundle.json` declaring the type, handler, and actions
2. Implement the handler (executable script or HTTP endpoint)
3. Register the bundle in `.well-known/polis`
4. The engine discovers it at startup and routes actions automatically
