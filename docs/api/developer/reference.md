# Content Type API Reference

*For* [Developers](../../README.md#building-on-polis) — *Kind* [Reference](../../README.md#kinds-of-page) — *Component* [Webapp](../../webapp/README.md) — *Code* [`webapp/internal/api`](../../../webapp/internal/api)

The Content Type API provides programmatic access to polis content operations. It runs alongside the webapp under the `/v1/` route prefix.

## Use Cases

- **Site owner tools**: Publish posts, manage comments, handle blessings from scripts, mobile apps, or external tools.
- **3rd party integrations**: Any tool that speaks HTTP+JSON can manage content on a polis site.
- **Instance-to-instance operations**: DMs use signed-request auth for cross-site delivery without discovery service coupling.

This API covers **content type operations only**. Site settings, theme switching, dashboard aggregations, and setup wizards remain in the webapp/CLI.

## Quick Start

```bash
# Create an API key by hand (no generator command yet — see Authentication below)
KEY="polis_$(openssl rand -hex 16)"
printf '%s' "$KEY" | sha256sum   # record this hash in .polis/api-keys.json
echo "$KEY"                       # use this value as the Bearer token

# List posts (public, no auth needed)
curl https://mysite.example.com/v1/content/post

# Publish a post (auth required)
curl -X POST https://mysite.example.com/v1/content/post \
  -H "Authorization: Bearer polis_abc123..." \
  -H "Content-Type: application/json" \
  -d '{"markdown": "# Hello World\n\nMy first API post."}'

# List installed bundles
curl https://mysite.example.com/v1/bundles
```

## Authentication

Read operations (GET) on content and bundles are public. All write operations require a Bearer token.

```
Authorization: Bearer <api-key>
```

There is no key-generation command yet. An API key is any token — `polis_`-prefixed by convention; the server does not check the prefix (`ops.ValidateAPIKey`) — whose **SHA-256 hash** you record in `.polis/api-keys.json`; the server hashes the presented Bearer token and compares against the stored hashes. Create one by hand:

```bash
KEY="polis_$(openssl rand -hex 16)"
HASH=$(printf '%s' "$KEY" | sha256sum | cut -d' ' -f1)
```

Then add an entry to `.polis/api-keys.json` (create the file if it doesn't exist):

```json
{
  "keys": [
    { "id": "k1", "name": "my-script", "key_hash": "<HASH>", "created_at": "2026-06-11T00:00:00Z" }
  ]
}
```

Use the plaintext `$KEY` as the Bearer token — only its hash is stored, never the key itself.

| Operation | Auth Required |
|-----------|---------------|
| `GET /v1/content/{type}` | No (public types only) |
| `GET /v1/content/{type}/{id}` | No (public types only) |
| `GET /v1/bundles` | No |
| `GET /v1/bundles/{name}` | No |
| `GET /v1/content/{type}/drafts[/{id}]` | Yes |
| `GET` on private types (`dm`, `follow`) | Yes |
| Everything else | Yes |

Private content types (`pub.polis.dm`, `pub.polis.follow`) require Bearer token auth for all operations including reads. Every other type allows unauthenticated reads.

The `deliver` and `protection_status` actions on `pub.polis.dm` are site-to-site: they authenticate **only** with signed request headers, never a Bearer token (a request without `X-Polis-Domain` is refused with `403`). The remote instance signs a canonical JSON payload with its Ed25519 private key.

| Header | Purpose |
|--------|---------|
| `X-Polis-Domain` | Sender's domain |
| `X-Polis-Signature` | Ed25519 SSH signature over canonical JSON |
| `X-Polis-Timestamp` | UTC timestamp in the form `2006-01-02T15:04:05Z`, within **2 minutes** of the receiver's clock |

The signed canonical JSON is `{"action","body_sha256","domain","recipient","timestamp"}` in that key order: the action name, the base64 SHA-256 of the exact request body, the sender's domain, the receiving site's domain, and the timestamp. The receiver verifies it against the sender's `.well-known/polis` key.

### Error Responses

Missing auth returns `401 Unauthorized`. Invalid key, or a signed request that fails verification, returns `403 Forbidden`.

## Routes

### Content CRUD

```
GET    /v1/content/{type}                    List content
GET    /v1/content/{type}/{id}               Get content by ID
POST   /v1/content/{type}                    Create content
PUT    /v1/content/{type}/{id}               Update content
DELETE /v1/content/{type}/{id}               Delete content
```

### Type-Specific Actions

```
POST   /v1/content/{type}/actions/{action}   Dispatch action
```

### Drafts

```
GET    /v1/content/{type}/drafts             List drafts
GET    /v1/content/{type}/drafts/{id}        Get draft
POST   /v1/content/{type}/drafts             Save draft
DELETE /v1/content/{type}/drafts/{id}        Delete draft
```

### Bundle Introspection

```
GET    /v1/bundles                           List installed bundles
GET    /v1/bundles/{name}                    Get bundle details
```

## Type Names

The `{type}` parameter accepts short names or fully-qualified names:

| Short Name | Full Name |
|-----------|-----------|
| `post` | `pub.polis.post` |
| `comment` | `pub.polis.comment` |
| `follow` | `pub.polis.follow` |
| `dm` | `pub.polis.dm` |
| `tag` | `pub.polis.tag` |
| `theme` | `pub.polis.theme` |
| `license` | `pub.polis.license` |
| `attestation` | `pub.polis.attestation` |
| `actor` | `pub.polis.actor` |

Both forms work: `/v1/content/post` and `/v1/content/pub.polis.post` are equivalent.

Resolution: short names try `pub.polis.<name>` first, then search all bundles for a suffix match. Ambiguous matches fail. A type is available only if the site's bundle declares it (`content/pub.polis.core/bundle.json`, or the built-in default when that file is absent). An undeclared type — there is no `feed` type — returns `404`.

## Content Types

### pub.polis.post

| Action | Method + Path | Status |
|--------|---------------|--------|
| List posts | `GET /v1/content/post` | Implemented |
| Get post | `GET /v1/content/post/{id}` | Routed, not wired |
| Create (publish) | `POST /v1/content/post` | Implemented |
| Update (republish) | `PUT /v1/content/post/{id}` | Routed, not wired |
| Delete (unpublish) | `DELETE /v1/content/post/{id}` | Routed, not wired |
| Render preview | `POST /v1/content/post/actions/render` | Routed, not wired |
| Draft CRUD | `*/v1/content/post/drafts[/{id}]` | Routed, not wired |

A route marked *not wired* answers `400 unsupported_action`.

**Create post request:**
```json
{
  "markdown": "# Post Title\n\nPost content in markdown.",
  "filename": "optional-custom-filename.md"
}
```

**Create post response** (201):
```json
{
  "title": "Post Title",
  "path": "content/pub.polis.core/post/20260301/post-title.md",
  "version": "sha256:3f2a...",
  "signature": "...",
  "url": ""
}
```

`url` is present but currently always empty; build the public URL from the site's base URL and `path`.

**List posts response** (200):
```json
{
  "posts": [
    {
      "type": "post",
      "path": "content/pub.polis.core/post/20260302/second.md",
      "title": "Second Post",
      "published": "2026-03-02T00:00:00Z",
      "current_version": "sha256:9c1e..."
    },
    {
      "type": "post",
      "path": "content/pub.polis.core/post/20260301/first.md",
      "title": "First Post",
      "published": "2026-03-01T00:00:00Z",
      "current_version": "sha256:3f2a..."
    }
  ],
  "count": 2
}
```

Posts are returned newest-first (reverse index order). Only `type: "post"` entries are returned; comments, tags and attestations in the index are filtered out. An entry also carries a `license` block when the work states terms.

### pub.polis.comment

| Action | Method + Path | Status |
|--------|---------------|--------|
| List comments | `GET /v1/content/comment` | Routed, not wired |
| Get comment | `GET /v1/content/comment/{id}` | Routed, not wired |
| Create (beseech) | `POST /v1/content/comment` | Implemented |
| Update (republish) | `PUT /v1/content/comment/{id}` | Implemented |
| Bless | `POST /v1/content/comment/actions/bless` | Routed, not wired |
| Deny | `POST /v1/content/comment/actions/deny` | Routed, not wired |
| Revoke | `POST /v1/content/comment/actions/revoke` | Routed, not wired |
| Sync | `POST /v1/content/comment/actions/sync` | Routed, not wired |

**Create (beseech) request:**
```json
{
  "comment_id": "abc123"
}
```

**Create (beseech) response** (200):
```json
{
  "success": true,
  "status": "pending",
  "message": "Comment sent to discovery service",
  "auto_blessed": false
}
```

A new comment is always pending: the post author's own site decides it later, so the commenter learns the outcome asynchronously. `auto_blessed` is `true` only when the comment had already been blessed and was re-registered. `message` is the discovery service's message, or a local note when the site is not registered with one.

**Update (republish) request** — `{id}` in the path is not used; the comment is named by `path`:
```json
{
  "path": "content/pub.polis.core/comment/20260301/reply.md",
  "markdown": "The corrected reply."
}
```

**Update (republish) response** (200):
```json
{
  "comment_id": "...",
  "path": "content/pub.polis.core/comment/20260301/reply.md",
  "version": "sha256:7d0b...",
  "signature": "...",
  "blessing_state": "blessed",
  "rebeseeched": false,
  "deferred": false
}
```

Signs a new version of an already-published comment and re-registers it; a granted or denied blessing is preserved. `blessing_state` is `pending`, `blessed` or `denied`; `rebeseeched` is `true` when a pending comment was re-sent for blessing; `deferred` is `true` when the discovery service was skipped because the site is not registered.

### pub.polis.follow

| Action | Method + Path | Status |
|--------|---------------|--------|
| List following | `GET /v1/content/follow` | Implemented |
| Follow | `POST /v1/content/follow` | Routed, not wired |
| Unfollow | `DELETE /v1/content/follow/{url}` | Routed, not wired |

**List following response** (200):
```json
{
  "following": [
    {
      "url": "https://alice.example.com",
      "added_at": "2026-01-01T00:00:00Z",
      "site_title": "Alice's Blog",
      "author_name": "Alice"
    }
  ],
  "count": 1,
  "signature": {
    "status": "valid"
  }
}
```

`signature` reports the state of the follow file's own signature
(`pub.polis.follow` is signed — see
[the signed follow file](../../signet/spec/attestation.md)).

| `status` | Meaning |
|----------|---------|
| `valid` | signed, and the signature verifies against the site's published identity key |
| `unsigned` | no signature. **A fact, not a defect** — the normal state for a follow list nobody has re-authored since signing shipped |
| `invalid` | signed, and the signature does **not** verify. A `message` field carries the detail |
| `unknown` | could not be checked (no `.well-known/polis`, or no `public_key` in it). A `message` field carries the detail |

⚠️ **`signature` is reported, never enforced.** The follow list is returned in
full whatever the status says — including `invalid`. Whether a given status is
acceptable is the caller's judgment, not the API's. Do not collapse `unsigned`
and `invalid` into one "bad" state: they are different facts, and treating an
unsigned list as broken misreads the common case.

### pub.polis.dm

| Action | Method + Path | Status |
|--------|---------------|--------|
| List conversations | `GET /v1/content/dm` | Implemented |
| Get conversation | `GET /v1/content/dm/{conv_id}` | Implemented |
| Send DM | `POST /v1/content/dm` | Implemented |
| Delete conversation | `DELETE /v1/content/dm/{conv_id}` | Implemented |
| Deliver (receive) | `POST /v1/content/dm/actions/deliver` | Implemented (site-to-site, signed request) |
| Protection status | `POST /v1/content/dm/actions/protection_status` | Implemented (site-to-site, signed request) |
| Mark read | `POST /v1/content/dm/actions/mark_read` | Implemented |
| Retry unsent | `POST /v1/content/dm/actions/retry` | Implemented |

**List conversations response** (200):
```json
{
  "conversations": [
    {
      "id": "f8e7d6c5b4a3f2e1",
      "peer_domain": "bob.example.com",
      "peer_url": "https://bob.example.com",
      "last_message_at": "2026-03-07T10:00:00Z",
      "unread_count": 2,
      "last_preview": ""
    }
  ],
  "count": 1
}
```

`last_preview` is always empty: messages are end-to-end encrypted and the server cannot read them.

**Get conversation response** (200):
```json
{
  "conversation_id": "f8e7d6c5b4a3f2e1",
  "peer_domain": "bob.example.com",
  "peer_url": "https://bob.example.com",
  "messages": [
    {
      "id": "msg1",
      "from": "alice.example.com",
      "to": "bob.example.com",
      "content": "Hello Bob!",
      "timestamp": "2026-03-07T10:00:00Z",
      "status": "sent",
      "key_epoch": 1,
      "locked": false
    }
  ],
  "count": 1
}
```

A message whose key epoch is locked (its password has not been entered) has empty `content` and `locked: true`. A reply carries `reply_to_id`.

**Send DM request:**
```json
{
  "recipient_url": "https://bob.example.com",
  "content": "Hello Bob!",
  "reply_to_id": ""
}
```

**Send DM response** (201, delivered):
```json
{
  "message_id": "a1b2c3d4",
  "conversation_id": "f8e7d6c5b4a3f2e1",
  "status": "sent"
}
```

**Send DM response** (201, delivery failed):
```json
{
  "message_id": "a1b2c3d4",
  "conversation_id": "f8e7d6c5b4a3f2e1",
  "status": "unsent",
  "error": "recipient unreachable"
}
```

**Deliver (receive) request** — sent by remote instance with signed headers:
```json
{
  "version": 2,
  "sender_domain": "alice.example.com",
  "recipient_domain": "bob.example.com",
  "encrypted_content": "<base64>",
  "nonce": "<base64, 24 bytes>",
  "timestamp": "2026-03-07T10:00:00Z",
  "box_pub": "<base64 x25519>",
  "sender_epoch": 1,
  "recipient_epoch": 1,
  "reply_to": ""
}
```

Only envelope version `2` is accepted. `sender_domain` must match the signed `X-Polis-Domain`. The receiver's policy, rate limits and size limit are applied before the envelope is parsed. The envelope format is specified in [DM encryption](../../general/security/dm-encryption.md).

**Deliver response** (201):
```json
{
  "message_id": "msg1",
  "conversation_id": "f8e7d6c5b4a3f2e1",
  "status": "received"
}
```

**Protection status response** (200) — only to a caller this site follows; anyone else gets `403`:
```json
{
  "domain": "bob.example.com",
  "protected": true,
  "at": "2026-03-07T10:00:00Z",
  "sig": "..."
}
```

**Mark read request:**
```json
{
  "conversation_id": "f8e7d6c5b4a3f2e1"
}
```

**Mark read response** (200):
```json
{
  "conversation_id": "f8e7d6c5b4a3f2e1"
}
```

**Delete conversation response** (200):
```json
{
  "conversation_id": "f8e7d6c5b4a3f2e1",
  "deleted": true
}
```

**Retry response** (200):
```json
{
  "unsent_count": 2,
  "unsent": [
    { "conversation_id": "f8e7d6c5b4a3f2e1", "message_id": "a1b2c3d4", "to": "bob.example.com", "timestamp": "2026-03-07T10:00:00Z" }
  ]
}
```

With nothing unsent the response is `{"retried": 0, "message": "no unsent messages"}`.

**Note:** The `deliver` and `protection_status` actions use signed-request authentication, not Bearer tokens. All other DM actions require Bearer token auth. Despite its name, `retry` does not resend: it returns the list of unsent messages from the store.

### pub.polis.tag

| Action | Method + Path | Status |
|--------|---------------|--------|
| List tags | `GET /v1/content/tag` | Implemented |
| Apply tag | `POST /v1/content/tag/actions/apply` | Implemented |
| Remove target | `POST /v1/content/tag/actions/remove` | Implemented |
| Delete tag | `POST /v1/content/tag/actions/delete` | Implemented |

**List tags response** (200, all tags):
```json
{
  "tags": [
    {
      "tag": "favorite",
      "count": 3,
      "updated": "2026-03-07T10:00:00Z"
    }
  ],
  "count": 1
}
```

**List tags request** (filter by name) — `POST /v1/content/tag/actions/list` (Bearer auth), since a `GET` carries no body:
```json
{
  "tag": "favorite"
}
```

**List tags response** (200, single tag with targets):
```json
{
  "tag": "favorite",
  "targets": [
    {
      "uri": "https://bob.example.com/posts/20260215/hello.md",
      "added": "2026-03-07T10:00:00Z"
    }
  ],
  "count": 1
}
```

**Apply tag request:**
```json
{
  "tag": "favorite",
  "target_uri": "https://bob.example.com/posts/20260215/hello.md"
}
```

**Apply tag response** (200):
```json
{
  "tag": "favorite",
  "target_uri": "https://bob.example.com/posts/20260215/hello.md",
  "count": 3
}
```

On success, the tag is synced to the discovery service (non-fatal if DS is unavailable).

**Remove target request:**
```json
{
  "tag": "favorite",
  "target_uri": "https://bob.example.com/posts/20260215/hello.md"
}
```

**Remove target response** (200):
```json
{
  "tag": "favorite",
  "target_uri": "https://bob.example.com/posts/20260215/hello.md",
  "count": 2
}
```

Unregisters the target from the discovery service (non-fatal if DS is unavailable).

**Delete tag request:**
```json
{
  "tag": "favorite"
}
```

**Delete tag response** (200):
```json
{
  "tag": "favorite",
  "deleted": true
}
```

Deletes the tag file locally and unregisters all targets from the discovery service.

### `pub.polis.theme`

Read-only access to the themes declared by the site's bundle. The active theme is set via the webapp settings page (which writes `.polis/bundles/registry.json`) — there is no `set`/`update` action on this content type at the API layer today, and the API does not report which theme is active.

**Actions:** `list`, `get`

**List response** (`GET /v1/content/theme`):
```json
{
  "themes": [
    { "name": "_shared",  "version": "1.2.0", "css": "base.css",     "compatible_shapes": ["pub.polis.shapes.v3", "pub.polis.shapes.v4"] },
    { "name": "especial", "version": "1.1.0", "css": "especial.css", "compatible_shapes": ["pub.polis.shapes.v3", "pub.polis.shapes.v4"] }
  ],
  "count": 2
}
```

Every declared theme is listed, sorted by name — including `_shared` (shared CSS, not selectable) and the reserved themes the webapp hides from its picker. `compatible_shapes` is omitted when the declaration names none.

**Get request:** `GET /v1/content/theme/{name}` (e.g. `GET /v1/content/theme/vice`, no auth), or `POST /v1/content/theme/actions/get` with `{"name": "vice"}` (Bearer auth, since it is a POST). The response is one entry in the shape above. A name the bundle does not declare is `404 not_found`.

### `pub.polis.license`

The site's current terms — the machine endpoint for the site default. Read-only: stating terms is a signing act for the key holder (`polis license`), never an API caller.

**Actions:** `get`

**Get response** (`GET /v1/content/license/current` — the `{id}` segment is required by the route and ignored):
```json
{
  "stated": true,
  "terms": { "...": "the signed licence's terms" },
  "pointer": "..."
}
```

A site that has stated no terms returns `"stated": false` with `"terms": null` — a defined state, not an error. `GET /v1/content/license` (list) is `400 unsupported_action`. For the terms a particular work was published under, read the work (or its index entry), not this endpoint. See the [licence spec](../../signet/spec/license.md).

### `pub.polis.attestation` · `pub.polis.actor`

Declared by the core bundle but not served by this API: every action answers `400 unsupported_action`, and `/v1/bundles` lists their `actions` as `null`. Read attestations from the site's `index.jsonl` and actor registries by the `actor_registry` pointer in `.well-known/polis`.

## Custom Bundles

The dispatch engine supports external handlers for custom content types via `bundle.json`:

| Handler Type | How It Works |
|-------------|--------------|
| `builtin` (or empty) | Go code in `builtin_core.go` (pub.polis.core only) |
| `executable` | External binary — receives `ActionRequest` as JSON on stdin, returns `ActionResult` on stdout. Env vars: `POLIS_SITE_DIR`, `POLIS_BASE_URL`, `POLIS_DISCOVERY_URL` |
| `http` | HTTP endpoint — receives `ActionRequest` as JSON POST, returns `ActionResult`. 30s timeout. |

⚠️ The webapp builds its engine from **one** bundle: the site's `content/pub.polis.core/bundle.json` (or the built-in default). No other bundle is loaded, so a third-party bundle is not reachable through this API today; the handler types apply to whatever handler that one bundle declares.

## Bundle Introspection

**List bundles response** (200) — abridged; types appear in no fixed order:
```json
{
  "bundles": [
    {
      "name": "pub.polis.core",
      "version": "1.0.0",
      "description": "Core polis content types",
      "types": [
        { "name": "pub.polis.post", "actions": ["list", "create"] },
        { "name": "pub.polis.comment", "actions": ["create", "update"] },
        { "name": "pub.polis.follow", "actions": ["list"] },
        { "name": "pub.polis.dm",
          "actions": ["list", "get", "send", "deliver", "protection_status", "mark_read", "delete", "retry"] },
        { "name": "pub.polis.tag", "actions": ["list", "apply", "remove", "delete"] },
        { "name": "pub.polis.theme", "actions": ["list", "get"] },
        { "name": "pub.polis.license", "actions": ["get"] },
        { "name": "pub.polis.attestation", "actions": null },
        { "name": "pub.polis.actor", "actions": null }
      ]
    }
  ]
}
```

`actions` lists exactly the actions the handler accepts; every other action on the type answers `400 unsupported_action`. A route marked *Routed, not wired* in the tables above exists and is not in this list.

## Dispatch Engine Internals

All content routes dispatch through `Engine.Dispatch()`:

1. Resolve short type name → fully-qualified name (e.g. `"post"` → `"pub.polis.post"`)
2. Find the bundle that owns the type
3. Look up the handler registered for that bundle
4. Check if the action is a write — if yes and no private key is configured, return `503 not_configured`
5. Call `handler.Handle(ctx, request, env)`
6. On write success, trigger `OnContentChanged()` callback (re-renders the site)

**Write actions** (require private key): `create`, `update`, `delete`, `bless`, `deny`, `revoke`, `sync`, `draft.save`, `draft.delete`, `refresh`, `send`, `deliver`, `mark_read`, `retry`, `apply`, `remove`

## Error Responses

All errors follow a consistent format:

```json
{
  "status": "error",
  "error": {
    "code": "not_found",
    "message": "The requested resource was not found."
  }
}
```

`message` is a fixed sentence per code, never the underlying error (which may contain server paths). The underlying error is logged server-side as `pub.polis.api.dispatch_error` with the request's `X-Request-Id`.

| HTTP Status | Error Code | When |
|-------------|-----------|------|
| 400 | `invalid_request` | Bad or malformed JSON, missing required fields, invalid payload |
| 400 | `unsupported_action` | Action not available for this content type |
| 401 | `unauthorized` | Missing Authorization header, or not a `Bearer` token |
| 403 | `forbidden` | Invalid API key, signed-request verification failed, or a DM site-to-site action refused |
| 404 | `not_found` | Unknown content type, content not found |
| 405 | `method_not_allowed` | Wrong HTTP method for this route |
| 500 | `internal_error` | Unexpected server error |
| 503 | `not_configured` | Site not configured (e.g., no signing keys) |

## CORS

The API sends **no** CORS headers, so a browser enforces same-origin: a page on another origin cannot call it. Scripts, servers and other polis instances are unaffected. There is no OPTIONS preflight handling.

## Body Limits

Request bodies are limited to 1MB. Write actions time out after 30 seconds.

## Current Implementation Status

### Fully Implemented (20 operations)

These operations work end-to-end through the dispatch engine:

1. **`pub.polis.post/list`** — Reads index.jsonl, keeps post entries, returns newest-first
2. **`pub.polis.post/create`** — Signs content, writes post, updates index, registers with DS
3. **`pub.polis.comment/create`** — Sends beseech request for pending comment
4. **`pub.polis.comment/update`** — Republishes an already-published comment as a new signed version
5. **`pub.polis.follow/list`** — Reads following.json, returns entries with metadata and the file's signature status
6. **`pub.polis.dm/list`** — Lists conversation summaries with unread counts
7. **`pub.polis.dm/get`** — Returns conversation with decrypted messages
8. **`pub.polis.dm/send`** — Encrypts and delivers DM to remote instance
9. **`pub.polis.dm/deliver`** — Receives encrypted DM from remote instance (signed-request auth)
10. **`pub.polis.dm/protection_status`** — Tells a followed site whether this site's messages are protected (signed-request auth)
11. **`pub.polis.dm/mark_read`** — Marks conversation messages as read
12. **`pub.polis.dm/delete`** — Deletes a conversation locally
13. **`pub.polis.dm/retry`** — Returns list of unsent messages
14. **`pub.polis.tag/list`** — Lists all tags or a single tag's targets
15. **`pub.polis.tag/apply`** — Applies a tag to a target URL, syncs to DS
16. **`pub.polis.tag/remove`** — Removes a target from a tag, unregisters from DS
17. **`pub.polis.tag/delete`** — Deletes a tag and unregisters all targets from DS
18. **`pub.polis.theme/list`** — Lists themes declared by the bundle
19. **`pub.polis.theme/get`** — Returns a single theme's declaration (`GET /v1/content/theme/{name}` or `POST …/actions/get`)
20. **`pub.polis.license/get`** — Returns the site's current terms, or `stated: false`

### Routed but Not Wired (16 operations)

These have REST routes and dispatch correctly through the engine, but the handler returns "unsupported action". Each needs the corresponding cli-go package logic wired into `builtin_core.go`:

- Post: get, update, delete, render, draft.list, draft.get, draft.save, draft.delete
- Comment: list, get, bless, deny, revoke, sync
- Follow: create, delete

### Known Gaps

1. **Payload validation** — No per-action schema validation. Bad inputs produce confusing errors deep in handler code.
2. **Pagination** — List operations return all results. Needs cursor/limit support.
3. **No feed type** — the feed is populated by the webapp's background sync loop and is not a content type this API serves.
