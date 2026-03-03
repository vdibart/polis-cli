# Content Type API Reference

The Content Type API provides programmatic access to polis content operations. It runs alongside the webapp under the `/v1/` route prefix.

## Use Cases

- **Site owner tools**: Publish posts, manage comments, handle blessings from scripts, mobile apps, or external tools.
- **3rd party integrations**: Any tool that speaks HTTP+JSON can manage content on a polis site.
- **Future**: Instance-to-instance operations (DMs, cross-site comments) without discovery service coupling.

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

API keys are generated via `ops.GenerateAPIKey()` and stored as SHA-256 hashes in `.polis/api-keys.json`. Keys are prefixed with `polis_`.

| Operation | Auth Required |
|-----------|---------------|
| `GET /v1/content/{type}` | No |
| `GET /v1/content/{type}/{id}` | No |
| `GET /v1/bundles` | No |
| `GET /v1/bundles/{name}` | No |
| Everything else | Yes |

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

Both forms work: `/v1/content/post` and `/v1/content/pub.polis.post` are equivalent.

## Content Types

### pub.polis.post

| Action | Method + Path | Status |
|--------|---------------|--------|
| List posts | `GET /v1/content/post` | Implemented |
| Get post | `GET /v1/content/post/{id}` | Stub (dispatches, handler not wired) |
| Create (publish) | `POST /v1/content/post` | Implemented |
| Update (republish) | `PUT /v1/content/post/{id}` | Stub |
| Delete (unpublish) | `DELETE /v1/content/post/{id}` | Stub |
| Render preview | `POST /v1/content/post/actions/render` | Stub |
| Draft CRUD | `*/v1/content/post/drafts[/{id}]` | Stub |

**Create post request:**
```json
{
  "markdown": "# Post Title\n\nPost content in markdown."
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
| List comments | `GET /v1/content/comment` | Stub |
| Get comment | `GET /v1/content/comment/{id}` | Stub |
| Create (beseech) | `POST /v1/content/comment` | Routed (needs pending comment in `.polis/`) |
| Bless | `POST /v1/content/comment/actions/bless` | Stub |
| Deny | `POST /v1/content/comment/actions/deny` | Stub |
| Revoke | `POST /v1/content/comment/actions/revoke` | Stub |
| Sync | `POST /v1/content/comment/actions/sync` | Stub |

**Create (beseech) request:**
```json
{
  "comment_id": "abc123"
}
```

### pub.polis.follow

| Action | Method + Path | Status |
|--------|---------------|--------|
| List following | `GET /v1/content/follow` | Implemented |
| Follow | `POST /v1/content/follow` | Stub |
| Unfollow | `DELETE /v1/content/follow/{url}` | Stub |

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
| List feed | `GET /v1/content/feed` | Stub |
| Refresh | `POST /v1/content/feed/actions/refresh` | Stub |

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
        }
      ]
    }
  ]
}
```

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
| 401 | `unauthorized` | Missing Authorization header |
| 403 | `forbidden` | Invalid API key |
| 404 | `not_found` | Unknown content type, content not found |
| 405 | `method_not_allowed` | Wrong HTTP method for this route |
| 503 | `not_configured` | Site not configured (e.g., no signing keys) |
| 500 | `internal_error` | Unexpected server error |

## CORS

All API routes include CORS headers:
- `Access-Control-Allow-Origin: *`
- `Access-Control-Allow-Methods: GET, POST, PUT, DELETE, OPTIONS`
- `Access-Control-Allow-Headers: Content-Type, Authorization`

OPTIONS preflight requests return 200 with these headers.

## Body Limits

Request bodies are limited to 1MB.

## Current Implementation Status

### Fully Implemented (4 operations)

These operations work end-to-end through the dispatch engine:

1. **`pub.polis.post/list`** — Reads index.jsonl, filters comments, returns newest-first
2. **`pub.polis.post/create`** — Signs content, writes post, updates index, registers with DS
3. **`pub.polis.comment/create`** — Sends beseech request for pending comment
4. **`pub.polis.follow/list`** — Reads following.json, returns entries with metadata

### Routed but Not Wired (~16 operations)

These have REST routes and dispatch correctly, but the handler returns "unsupported action". Each needs the corresponding cli-go package logic wired into `builtin_core.go`:

- Post: get, update, delete, render, draft.*
- Comment: list, get, bless, deny, revoke, sync
- Follow: create, delete
- Feed: list, refresh

### Known Gaps

1. **Payload validation** — No per-action schema validation. Bad inputs produce confusing errors deep in handler code.
2. **Pagination** — List operations return all results. Needs cursor/limit support.
3. **Draft auth** — GET on drafts is currently public (same as other GETs). Drafts should require auth.
4. **Feed population** — Feed list reads from cache, but the cache is populated by the webapp's background sync loop. `feed/refresh` needs sync extraction.
5. **Instance-to-instance auth** — Only local Bearer tokens. Cross-instance auth protocol undefined.
