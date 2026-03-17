# Content Type API Reference

The Content Type API provides programmatic access to polis content operations. It runs alongside the webapp under the `/v1/` route prefix.

## Use Cases

- **Site owner tools**: Publish posts, manage comments, handle blessings from scripts, mobile apps, or external tools.
- **3rd party integrations**: Any tool that speaks HTTP+JSON can manage content on a polis site.
- **Instance-to-instance operations**: DMs use signed-request auth for cross-site delivery without discovery service coupling.

This API covers **content type operations only**. Site settings, theme switching, dashboard aggregations, and setup wizards remain in the webapp/CLI.

## Quick Start

```bash
# Generate an API key
polis api-key create --name "my-script"
# => polis_abc123...

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

API keys are generated via `polis api-key create` and stored as SHA-256 hashes in `.polis/api-keys.json`. Keys are prefixed with `polis_`.

| Operation | Auth Required |
|-----------|---------------|
| `GET /v1/content/{type}` | No |
| `GET /v1/content/{type}/{id}` | No |
| `GET /v1/bundles` | No |
| `GET /v1/bundles/{name}` | No |
| `GET /v1/content/{type}/drafts[/{id}]` | No (gap: should require auth) |
| Everything else | Yes |

For the `deliver` action on `pub.polis.dm`, authentication uses **signed request headers** instead of Bearer tokens. Remote instances authenticate by signing a canonical JSON payload with their Ed25519 private key.

| Header | Purpose |
|--------|---------|
| `X-Polis-Domain` | Sender's domain |
| `X-Polis-Signature` | Ed25519 SSH signature over canonical JSON |
| `X-Polis-Timestamp` | ISO-8601 timestamp (must be within 5-minute window) |

### Error Responses

Missing auth returns `401 Unauthorized`. Invalid key returns `403 Forbidden`.

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
| `feed` | `pub.polis.feed` |
| `dm` | `pub.polis.dm` |

Both forms work: `/v1/content/post` and `/v1/content/pub.polis.post` are equivalent.

Resolution: short names try `pub.polis.<name>` first, then search all bundles for a suffix match. Ambiguous matches fail.

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
  "path": "posts/20260301/post-title.md",
  "version": "1",
  "signature": "...",
  "url": "https://mysite.example.com/posts/20260301/post-title.html"
}
```

**List posts response** (200):
```json
{
  "posts": [
    {
      "path": "posts/20260302/second.md",
      "title": "Second Post",
      "published": "2026-03-02T00:00:00Z",
      "current_version": "1"
    },
    {
      "path": "posts/20260301/first.md",
      "title": "First Post",
      "published": "2026-03-01T00:00:00Z",
      "current_version": "1"
    }
  ],
  "count": 2
}
```

Posts are returned newest-first. Comment entries are filtered from the index.

### pub.polis.comment

| Action | Method + Path | Status |
|--------|---------------|--------|
| List comments | `GET /v1/content/comment` | Routed, not wired |
| Get comment | `GET /v1/content/comment/{id}` | Routed, not wired |
| Create (beseech) | `POST /v1/content/comment` | Implemented |
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
  "count": 1
}
```

### pub.polis.feed

| Action | Method + Path | Status |
|--------|---------------|--------|
| List feed | `GET /v1/content/feed` | Routed, not wired |
| Refresh | `POST /v1/content/feed/actions/refresh` | Routed, not wired |

Feed list reads from cache, but the cache is populated by the webapp's background sync loop. The `refresh` action needs sync extraction before it can work.

### pub.polis.dm

| Action | Method + Path | Status |
|--------|---------------|--------|
| List conversations | `GET /v1/content/dm` | Implemented |
| Get conversation | `GET /v1/content/dm/{conv_id}` | Implemented |
| Send DM | `POST /v1/content/dm` | Implemented |
| Delete conversation | `DELETE /v1/content/dm/{conv_id}` | Implemented |
| Deliver (receive) | `POST /v1/content/dm/actions/deliver` | Implemented |
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
      "last_preview": "Thanks for reaching out..."
    }
  ],
  "count": 1
}
```

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
      "read_at": "2026-03-07T10:05:00Z"
    }
  ],
  "count": 1
}
```

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
  "version": 1,
  "sender_domain": "alice.example.com",
  "recipient_domain": "bob.example.com",
  "encrypted_content": "<base64>",
  "nonce": "<base64, 24 bytes>",
  "timestamp": "2026-03-07T10:00:00Z"
}
```

**Deliver response** (201):
```json
{
  "message_id": "msg1",
  "conversation_id": "f8e7d6c5b4a3f2e1",
  "status": "received"
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
  "unsent": [...]
}
```

**Note:** The `deliver` action uses signed-request authentication, not Bearer tokens. All other DM actions require Bearer token auth. The `retry` action returns the list of unsent messages from the store.

## Custom Bundles

The dispatch engine supports external handlers for custom content types via `bundle.json`:

| Handler Type | How It Works |
|-------------|--------------|
| `builtin` (or empty) | Go code in `builtin_core.go` (pub.polis.core only) |
| `executable` | External binary — receives `ActionRequest` as JSON on stdin, returns `ActionResult` on stdout. Env vars: `POLIS_SITE_DIR`, `POLIS_BASE_URL`, `POLIS_DISCOVERY_URL` |
| `http` | HTTP endpoint — receives `ActionRequest` as JSON POST, returns `ActionResult`. 30s timeout. |

## Bundle Introspection

**List bundles response** (200):
```json
{
  "bundles": [
    {
      "name": "pub.polis.core",
      "version": "1.0.0",
      "description": "Core polis content types",
      "types": [
        {
          "name": "pub.polis.post",
          "actions": ["list", "get", "create", "update", "delete", "render",
                      "draft.list", "draft.get", "draft.save", "draft.delete"]
        },
        {
          "name": "pub.polis.comment",
          "actions": ["list", "get", "create", "bless", "deny", "revoke", "sync"]
        },
        {
          "name": "pub.polis.follow",
          "actions": ["list", "create", "delete"]
        },
        {
          "name": "pub.polis.feed",
          "actions": ["list", "refresh"]
        },
        {
          "name": "pub.polis.dm",
          "actions": ["list", "get", "send", "deliver", "mark_read", "delete", "retry"]
        }
      ]
    }
  ]
}
```

## Dispatch Engine Internals

All content routes dispatch through `Engine.Dispatch()`:

1. Resolve short type name → fully-qualified name (e.g. `"post"` → `"pub.polis.post"`)
2. Find the bundle that owns the type
3. Look up the handler registered for that bundle
4. Check if the action is a write — if yes and no private key is configured, return `503 not_configured`
5. Call `handler.Handle(ctx, request, env)`
6. On write success, trigger `OnContentChanged()` callback (re-renders the site)

**Write actions** (require private key): `create`, `update`, `delete`, `bless`, `deny`, `revoke`, `sync`, `draft.save`, `draft.delete`, `refresh`, `send`, `deliver`, `mark_read`, `retry`

## Error Responses

All errors follow a consistent format:

```json
{
  "status": "error",
  "error": {
    "code": "not_found",
    "message": "unknown content type: pub.unknown"
  }
}
```

| HTTP Status | Error Code | When |
|-------------|-----------|------|
| 400 | `invalid_request` | Bad JSON, missing required fields, invalid payload |
| 400 | `unsupported_action` | Action not available for this content type |
| 400 | `bad_request` | Malformed JSON body |
| 401 | `unauthorized` | Missing Authorization header |
| 403 | `forbidden` | Invalid API key, or signed-request verification failed |
| 404 | `not_found` | Unknown content type, content not found |
| 405 | `method_not_allowed` | Wrong HTTP method for this route |
| 500 | `internal_error` | Unexpected server error |
| 503 | `not_configured` | Site not configured (e.g., no signing keys) |

## CORS

All API routes include CORS headers:
- `Access-Control-Allow-Origin: *`
- `Access-Control-Allow-Methods: GET, POST, PUT, DELETE, OPTIONS`
- `Access-Control-Allow-Headers: Content-Type, Authorization, X-Polis-Domain, X-Polis-Signature, X-Polis-Timestamp`

OPTIONS preflight requests return 200 with these headers.

## Body Limits

Request bodies are limited to 1MB.

## Current Implementation Status

### Fully Implemented (11 operations)

These operations work end-to-end through the dispatch engine:

1. **`pub.polis.post/list`** — Reads index.jsonl, filters comments, returns newest-first
2. **`pub.polis.post/create`** — Signs content, writes post, updates index, registers with DS
3. **`pub.polis.comment/create`** — Sends beseech request for pending comment
4. **`pub.polis.follow/list`** — Reads following.json, returns entries with metadata
5. **`pub.polis.dm/list`** — Lists conversation summaries with unread counts
6. **`pub.polis.dm/get`** — Returns conversation with decrypted messages
7. **`pub.polis.dm/send`** — Encrypts and delivers DM to remote instance
8. **`pub.polis.dm/deliver`** — Receives encrypted DM from remote instance (signed-request auth)
9. **`pub.polis.dm/mark_read`** — Marks conversation messages as read
10. **`pub.polis.dm/delete`** — Deletes a conversation locally
11. **`pub.polis.dm/retry`** — Returns list of unsent messages

### Routed but Not Wired (~16 operations)

These have REST routes and dispatch correctly through the engine, but the handler returns "unsupported action". Each needs the corresponding cli-go package logic wired into `builtin_core.go`:

- Post: get, update, delete, render, draft.*
- Comment: list, get, bless, deny, revoke, sync
- Follow: create, delete
- Feed: list, refresh

### Known Gaps

1. **Payload validation** — No per-action schema validation. Bad inputs produce confusing errors deep in handler code.
2. **Pagination** — List operations return all results. Needs cursor/limit support.
3. **Draft auth** — GET on drafts is currently public (same as other GETs). Drafts should require auth.
4. **Feed population** — Feed list reads from cache, but the cache is populated by the webapp's background sync loop. `feed/refresh` needs sync extraction.
