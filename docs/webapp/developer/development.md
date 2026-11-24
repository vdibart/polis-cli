# Webapp Development Guide

*For* [Contributors](../../README.md#contributing-to-polis) — *Kind* [Guide](../../README.md#kinds-of-page) — *Component* [Webapp](../README.md) — *See also* [tour](../../handbook/README.md)

Development patterns, testing, and architecture for the polis webapp: the server, and the owner app it serves.
Every site's owner app is the v5 app (`webui/www/v5/`), which calls `/v1` only; every site action is one `/v1`
operation in the action layer (`cli-go/pkg/ops`), described in the [API developer reference](../../api/developer/reference.md).
The previous owner app (`app.js`, `owner-extras.js`, its stream screen) was deleted on 2026-11-22.

## Critical Rules

1. **Drift detection:** Whenever you modify webapp code, check the bash CLI (the `polis` script in `cli-bash/`) for the equivalent `cmd_*` function. Flag any behavioral differences.
2. **Tests required:** Add or update tests for every change. Run `go test ./...` after changes.
3. **Dependency rule:** The webapp imports from `cli-go/pkg/`. Never put shared logic in `internal/`.

## Directory Structure

```
webapp/
├── cmd/server/main.go          # Standalone entry point
├── cmd/polis-full/main.go      # Bundled CLI+server entry point
├── cmd/hosted/, cmd/patrol/,   # Hosted service and standalone actor binaries
│   cmd/medic/, cmd/tailor/
├── internal/server/
│   ├── server.go               # Server struct, config, logging, sync loop
│   ├── routes.go               # The routes outside /v1, each /api/ one with its reason
│   ├── site_api.go, owner_api.go # The site's own /v1 operations (settings, agents, messages)
│   ├── handlers.go             # Handlers those operations and routes reach
│   ├── handlers_stream.go      # The items behind every PQL view, counts, bodies
│   ├── handlers_pql.go         # GET /pql/<sentence> (and /v1/query, which reaches it)
│   ├── v5owner.go              # Serves the owner app at /_/ and below
│   ├── sync.go                 # Unified DS sync + its handlers
│   ├── middleware.go           # Request logging, correlation IDs
│   ├── handlers_test.go        # Handler tests
│   └── server_test.go          # Server/validation tests
├── internal/api/
│   ├── router.go               # /v1: content operations, auth, the live-grant check
│   ├── handlers.go             # Thin HTTP → Dispatch → JSON
│   ├── hostroutes.go           # The host's own /v1 operations (api.HostRoute)
│   ├── envelope.go             # Every /v1 error as one envelope
│   └── middleware.go           # Body limits, CORS hook
├── internal/agentauth/         # An agent signing in (polis.pub)
├── internal/serve/             # Shared public-content serving (hosted + reader/mirror modes)
├── internal/webui/
│   ├── assets.go               # Shared embedded FS (//go:embed www/*)
│   ├── *.test.js               # The owner app's tests (node --test)
│   └── www/
│       ├── v5/                 # The owner app (index.html, frame.js, api.js, the sections, card.js, sheet.js, rich.js)
│       └── dm*.js, vendor/     # The messages key's cryptography and the sheet's rich-text editor, served from the site root
└── internal/hosted/            # Multi-tenant service (polis.pub)
```

## Adding an operation

⛔ **Nothing new goes under `/api/`.** A site action has one implementation, in the action layer
(`cli-go/pkg/ops`), and every caller reaches it through `/v1`: the owner app, agents (MCP is generated from the
site's description), scripts and the CLI. What is left under `/api/` is a short list of things that are not API
operations, each with its reason ([the site API page](../../api/developer/site-api.md)).

1. **Declare the operation.** A content operation goes in `cli-go/pkg/ops/builtin_core_ops.go` (type, action,
   method, path, input schema, auth and scope), its handler in `builtin_core.go` and its success answer in
   `builtin_core_answers.go`. A setting or a host-only operation is an `api.HostRoute` beside its handler
   (`webapp/internal/server/site_api.go`, `owner_api.go`), with its answer in `api_answers.go`. Paths follow
   the [path rule](../../api/developer/reference.md#paths): a content type's operations under `/v1/content/{type}`, a
   site's settings under `/v1/{area}/…`.
2. **Declare what a write causes** in the shared after-write module (`cli-go/pkg/ops/afterwrite.go`): its
   event, its render, its hook. A test fails until it is there.
3. **Reach it from the owner app** (`webapp/internal/webui/www/v5/`) through `api.js`, which calls `/v1` only.
4. **Test it through `/v1`** with the helpers in `webapp/internal/server` (`queryRoutes`, `mint`, `apiCall`):
   the happy path, the refusals, what an agent may and may not do, and the effects.

## Test Helpers

| Helper | Purpose |
|--------|---------|
| `newTestServer(t)` | Temp dir with required subdirectories, no keys |
| `newConfiguredServer(t)` | Real Ed25519 keys, config, `.well-known/polis` |
| `jsonBody(t, v)` | Marshal to `*bytes.Buffer` for request bodies |
| `queryRoutes(t)` | A configured site and its full route set, for calling `/v1` |
| `mint(t, s, scopes...)` | A token on that site: the owner's own full-access one with no scopes, an agent's scoped one with them |
| `apiCall(t, h, method, path, token, body)` | One request; returns the status and the decoded answer |

The owner app's JavaScript has its own tests, `webapp/internal/webui/*.test.js`, run with
`node --test webapp/internal/webui/*.test.js` (part of `make test` when Node is installed). Each file's rules
are pure functions it exports, and the tests call those.

## Frontend Architecture

**The owner app is the v5 app, in `webapp/internal/webui/www/v5/`, at `/_/` and below on every instance**
(`server.ServeV5Owner`, `internal/server/v5owner.go`). It has no build step: `index.html` is its shell, whose
`{{asset:…}}` references the server fills with each file's address and content hash, and each script is a plain
file. The rules every page follows (cards, the frame, every label) are the [style guide](../designer/style-guide.md).

| File | What it does |
|---|---|
| `frame.js`, `frame.css` | The menu and the section box, the addresses, the dots that say something is new, hints and help. `frame.css` holds the app's own tokens: the owner app does not take the site's public theme |
| `api.js` | The one door to the site: `/v1`, same-origin, on the owner's own sign-in, with no token. Every answer is `{ok, status, data, error}`, the error read from the envelope's `error.message` |
| `gather.js` · `write.js` · `message.js` · `enhance.js` | The four sections: the network, what the owner wrote (and the comments waiting for a blessing), messages, settings in groups |
| `card.js` | Every item's card, built from the same parts in the same order as the public site's (a test holds the two together) |
| `sheet.js` | The one editor, opened in place over the section: a new post, an edit, a reply (in write, and in gather's focus mode). Unfinished writing is saved as a draft on the site. An edit can be republished only once it differs from what was opened, so a post opened and left alone keeps its bytes and its signature. Its `field` (rich text with the formatting buttons and the markdown switch, over a text area) is also the bio's editor in enhance, whose Save likewise stays off until the bio has changed |
| `rich.js` | The sheet's rich text: the body edited as formatted text and written back as markdown, over the vendored Milkdown kit (`www/vendor/`, fetched only when the sheet first needs it). The editor sees the body only, never the frontmatter; an untouched body is never re-serialised; an edited one is written with the post's own bullet and rule and bare links, so an edit changes only its own region. The writer can switch to markdown, and the sheet falls back to it if the editor cannot load |
| `msgkey.js`, `help.js` | The messages key (unlocking, sealing, the messages password), and each section's help |

**Data:** only `/v1`, through `api.js` (`ctx.api.get/post/put`). ⛔ Nothing in the owner app calls `/api/`.
Lists are PQL sentences, fetched through `/v1/query` (`api.queryPath`), which runs the site's own `/pql/`
handler, so a count and its list come from the same sentence.

### Addresses

`frame.js` owns them: `/_/<section>` (gather, write, message, enhance), a view as
`/_/<section>/pql/<sentence>`, an enhance group as `/_/enhance/<group>`; `/_/` opens the section the owner last
left (gather the first time). An address from the previous app is redirected to its v5 place by the server
(`V5OwnerPlace` in `internal/server/v5owner.go`). Every other route the server answers is registered in
`internal/server/routes.go` (outside `/v1`) and `internal/api/router.go` (`/v1`): read them before changing route
handling.

## Server Patterns

### Config Loading Order

1. `site.Validate()` — check site structure
2. `LoadConfig()` — read `.polis/webapp/config.json` (only if the site is valid)
3. `LoadKeys()` — read Ed25519 keypair from `.polis/keys/` (only if the site is valid)
4. `LoadEnv()` — search: `<data-dir>/.env` → `cwd/.env` → `~/.polis/.env`; its discovery settings override the config

### Logging

```go
s.LogInfo("message: %v", arg)    // Level 1
s.LogWarn("message: %v", arg)    // Level 1
s.LogError("message: %v", err)   // Level 1
s.LogDebug("message: %v", arg)   // Level 2
s.LogEvent("pub.polis.x.y", map[string]interface{}{...}) // structured event
```

Logs to `<data-dir>/.polis/logs/YYYY-MM-DD.log`. Thread-safe with mutex.

### Security

- Path traversal: `contentpath.Validate()` (`cli-go/pkg/contentpath`), the one rule for every surface that takes a path from a caller — no `..`, no null bytes
- Content paths restricted to: `content/pub.polis.core/post/`, `content/pub.polis.core/comment/`, `.polis/bundles/pub.polis.core/posts/drafts/`, the rendered `posts/` and `comments/` mounts, and root-level `.md`/`.html`
- Draft IDs made safe as file names (`draft.SanitizeID`: anything but letters, digits, `_` and `-` becomes `-`)

### Answers

A `/v1` success answer is the operation's own JSON object, with no common wrapper; each operation states its
shape in `/v1/openapi.json`. Every `/v1` error is one envelope, written by `/v1`'s outermost wrapper
(`internal/api/envelope.go`) whatever the handler wrote:

```json
{"status": "error", "error": {"code": "invalid_request", "message": "…"}}
```

See [Answers](../../api/developer/reference.md#answers) and [Error Responses](../../api/developer/reference.md#error-responses).

## Build Commands

```bash
# Build webapp
cd webapp && go build -o polis-server ./cmd/server

# Build bundled (CLI + server)
cd webapp && go build -o polis-full ./cmd/polis-full

# Quick dev cycle
cd webapp && go build -o polis-server ./cmd/server && ./polis-server

# If cli-go packages changed, rebuild both
cd cli-go && go build ./... && go test ./... && \
cd ../webapp && go build -o polis-server ./cmd/server && go test ./...
```
