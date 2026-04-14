# Discovery Service API Reference

All endpoints are available at `{DS_PUBLIC_URL}/{endpoint}`. Resource endpoints live under the `/v1/` prefix.

Auth conventions:
- **Write operations** require an Ed25519 SSH signature in the request body
- **Read queries** on content/relationships/stream require signed GET headers (`X-Polis-Domain`, `X-Polis-Timestamp`, `X-Polis-Signature`)
- **Admin operations** require `Authorization: Bearer <operator-api-key>` header

Rate limiting:
- **Write endpoints** enforce two tiers: (1) IP-based pre-auth limit of 120 req/hr before any outbound fetch or signature verification, (2) per-domain limit after authentication (varies by endpoint)
- **Read endpoints** enforce IP-based limits (see individual endpoints for rates)
- Rate-limited responses return `429` with `Retry-After` header and `RATE_LIMIT_EXCEEDED` error code

SSRF protection:
- All write endpoints reject domains that resolve to internal infrastructure (IP literals, `localhost`, reserved TLDs like `.local`/`.localhost`/`.example`, cloud metadata endpoints) with `400 INVALID_DOMAIN`

Response signing:
- All authenticated query responses include `ds_signature` and `ds_key_id` fields, allowing clients to verify the DS signed the response

Size limits:
- URLs: 2KB
- Signatures: 2KB
- Metadata: 4KB
- Stream event payloads: 8KB
- POST bodies: 64KB

Query limits:
- `offset` parameter capped at 10,000
- `limit` parameter capped at 100 (content/relationships) or 1,000 (stream)

---

## Sites

### POST /v1/sites

Register a site with the discovery service. The DS fetches the site's `.well-known/polis` to obtain the public key (it is not sent in the request).

Rate limit: per-domain 5/hr

```json
// Request
{
  "version": 1,
  "action": "register",
  "domain": "alice.com",
  "signature": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----",
  "email": "alice@alice.com",
  "author_name": "Alice",
  "description": "Alice's site"
}

// Response 201
{
  "success": true,
  "message": "Site registered",
  "domain": "alice.com",
  "registry_url": "https://ds.example.com/sites-check?domain=alice.com",
  "created_at": "2026-01-18T12:00:00Z",
  "service_attestation": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----"
}
```

The `email`, `author_name`, and `description` fields are optional. Emits a `pub.polis.site.registered` (or `pub.polis.site.reregistered`) stream event on success.

### GET /v1/sites/check?domain=alice.com

Check if a domain is registered.

Rate limit: IP 300/hr

```json
// Response 200 (registered)
{
  "is_registered": true,
  "domain": "alice.com",
  "registry_url": "https://ds.example.com/sites-check?domain=alice.com",
  "created_at": "2026-01-18T12:00:00Z",
  "registration_version": 1,
  "service_attestation": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----",
  "attestation_key_id": "ds-primary",
  "ds_signature": "...",
  "ds_key_id": "ds-primary"
}

// Response 200 (not registered)
{
  "is_registered": false,
  "domain": "alice.com"
}
```

### GET /v1/sites/public-key?key_id=ds-primary

Get the discovery service's public key for verifying attestations and response signatures.

Rate limit: IP 30/hr

**Query parameters:** `key_id` (optional; if omitted returns the current active key)

```json
// Response 200
{
  "public_key": "ssh-ed25519 AAAA...",
  "key_id": "ds-primary"
}
```

Returns `404` if a specific `key_id` is requested but not found.

### POST /v1/sites/unregister

Remove a site from the registry (hard delete, cascades to key history).

Rate limit: per-domain 5/hr

```json
// Request
{
  "version": 1,
  "action": "unregister",
  "domain": "alice.com",
  "signature": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----"
}

// Response 200
{
  "success": true,
  "message": "Site unregistered",
  "domain": "alice.com"
}
```

---

## Content

### POST /v1/content

Register or update content metadata. The DS extracts the actor domain from the URL.

Rate limit: per-domain 50/hr

```json
// Request
{
  "type": "pub.polis.post",
  "url": "https://alice.com/posts/hello.md",
  "version": "sha256:abc123...",
  "author": "alice",
  "metadata": {
    "title": "Hello World",
    "published_at": "2026-01-15T12:00:00Z",
    "in_reply_to": "",
    "root_post": ""
  },
  "signature": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----"
}

// Response 201 (new) or 200 (update)
{
  "success": true,
  "message": "Content registered",
  "type": "pub.polis.post",
  "url": "https://alice.com/posts/hello.md",
  "status": "created"
}
```

For `pub.polis.tag` content, the metadata must include `tag` (the tag name) and `target` (the URL being tagged):

```json
// Tag registration request
{
  "type": "pub.polis.tag",
  "url": "https://alice.com/tags/favorite",
  "version": "sha256:abc123...",
  "author": "alice",
  "metadata": {
    "tag": "favorite",
    "target": "https://bob.com/posts/20260215/hello.md"
  },
  "signature": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----"
}
```

Emits `pub.polis.tag.applied` on registration. Query by tag name with `metadata.tag=favorite` or by target URL with `metadata.target=https://...`.

For `pub.polis.comment` content, the response includes a `relationship_status` field indicating the auto-blessing decision:

```json
// Comment response
{
  "success": true,
  "message": "Content registered",
  "type": "pub.polis.comment",
  "url": "https://alice.com/comments/reply.md",
  "status": "created",
  "relationship_status": "granted"
}
```

Comments trigger policy-based auto-blessing: the DS evaluates the post author's blessing policy (or DS defaults if the author's policy is unreachable). If auto-blessed, metadata includes `auto_blessed: true`, `bless_reason: "policy-match"`, `policy_rule`, and `policy_source`.

Emits stream events: `pub.polis.post.published` / `pub.polis.post.republished` for posts, `pub.polis.comment.published` / `pub.polis.comment.republished` for comments, plus `pub.polis.comment.blessing.granted` or `pub.polis.comment.blessing.requested` for comment blessings.

URLs ending in `.html` are normalized to `.md`.

### GET /v1/content/check?type=pub.polis.post&url=https://alice.com/posts/hello.md

Check if specific content is registered.

Rate limit: IP 300/hr

```json
// Response 200 (exists)
{
  "exists": true,
  "type": "pub.polis.post",
  "url": "https://alice.com/posts/hello.md",
  "version": "sha256:abc123...",
  "actor": "alice.com"
}

// Response 200 (not found)
{
  "exists": false,
  "type": "pub.polis.post",
  "url": "https://alice.com/posts/hello.md"
}
```

### GET /v1/content?type=pub.polis.post

Query registered content. Requires signed GET headers.

Rate limit: IP 600/hr

**Query parameters:** `type` (required), `actor` (optional domain filter), `limit` (default 100, max 100), `offset` (default 0, max 10000), `metadata.*` (optional key-value filters)

**Headers:** `X-Polis-Domain`, `X-Polis-Timestamp`, `X-Polis-Signature`

```json
// Response 200
{
  "count": 2,
  "records": [
    {
      "id": 1,
      "type": "pub.polis.post",
      "url": "https://alice.com/posts/hello.md",
      "version": "sha256:abc123...",
      "actor": "alice.com",
      "author": "alice",
      "signature": "...",
      "signature_verified": true,
      "key_id": 5,
      "metadata": { "title": "Hello World" },
      "status": "active",
      "removed_at": null,
      "created_at": "2026-01-15T12:00:00Z",
      "updated_at": "2026-01-15T12:00:00Z"
    }
  ],
  "ds_signature": "...",
  "ds_key_id": "ds-primary"
}
```

For `pub.polis.comment` queries: granted blessings are visible to all; pending and denied blessings are scoped to the authenticated caller's domain. Results ordered by `updated_at` DESC.

### POST /v1/content/unregister

Remove tag content from the index (soft delete — status set to `removed`).

**Tags only.** Posts and comments must use `POST /v1/content/unpublish`. Requests for non-tag types return `400`.

Rate limit: per-domain 20/hr

```json
// Request
{
  "type": "pub.polis.tag",
  "url": "https://alice.com/tags/reading/abc123",
  "signature": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----"
}

// Response 200
{
  "success": true,
  "message": "Content unregistered"
}
```

Emits a `pub.polis.tag.removed` stream event.

### POST /v1/content/unpublish

Unpublish a post or comment (clean break retraction). Sets content status to `unpublished` and performs blessing cascades for posts.

**Posts and comments only.** Tags must use `POST /v1/content/unregister`. Requests for tag types return `400`. Content must be in `active` status; already-unpublished or removed content returns `409`.

Rate limit: per-domain 20/hr

```json
// Request
{
  "type": "pub.polis.post",
  "url": "https://alice.com/content/pub.polis.core/post/20260201/my-post.md",
  "signature": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----"
}

// Response 200
{
  "success": true,
  "message": "Content unpublished",
  "type": "pub.polis.post",
  "url": "https://alice.com/content/pub.polis.core/post/20260201/my-post.md",
  "orphaned_blessings": 2,
  "denied_blessings": 1
}
```

**Blessing cascade (posts only):**
- Blessed (`granted`) comments → `orphaned` (permanent — not restored on republish)
- Pending comments → `denied` (permanent)
- Already denied → unchanged

**Comment unpublish:** Resets the comment's own blessing relationship to `pending` so republish triggers fresh policy evaluation.

**Clean break semantics:** Unpublish severs all ties to the published identity. If the content is republished later, it is treated as a completely fresh publication — orphaned blessings are NOT restored, and comment authors must re-beseech for blessing. See [Unpublish Lifecycle](unpublish-lifecycle.md) for full state transition rules.

Emits `pub.polis.post.unpublished` or `pub.polis.comment.unpublished` stream event.

---

## Relationships

### POST /v1/relationships

Grant or deny a blessing. Signature must be from the target domain (post author).

Rate limit: per-domain 50/hr

```json
// Request
{
  "type": "pub.polis.comment.blessing",
  "source_url": "https://alice.com/comments/reply.md",
  "target_url": "https://bob.com/posts/original.md",
  "action": "grant",
  "signature": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----"
}

// Response 200
{
  "success": true,
  "message": "Relationship granted",
  "type": "pub.polis.comment.blessing",
  "source_url": "https://alice.com/comments/reply.md",
  "target_url": "https://bob.com/posts/original.md",
  "status": "granted"
}
```

Actions: `grant` (maps to status `granted`), `deny` (maps to status `denied`).

Includes a TOCTOU guard: only updates if status is unchanged since read.

Emits `pub.polis.comment.blessing.granted` or `pub.polis.comment.blessing.denied` stream event. The actor in the event is the target domain (post author).

### GET /v1/relationships?type=pub.polis.comment.blessing

Query relationships. Requires signed GET headers.

Rate limit: IP 600/hr

**Query parameters:** `type` (required), `actor` (optional domain), `status` (optional: `pending`|`granted`|`denied`), `source_url`, `target_url`, `limit` (default 100, max 100), `offset` (default 0, max 10000)

**Headers:** `X-Polis-Domain`, `X-Polis-Timestamp`, `X-Polis-Signature`

```json
// Response 200
{
  "count": 1,
  "records": [
    {
      "id": 1,
      "type": "pub.polis.comment.blessing",
      "source_url": "https://alice.com/comments/reply.md",
      "target_url": "https://bob.com/posts/original.md",
      "actor": "alice.com",
      "status": "granted",
      "signature": "...",
      "metadata": {},
      "created_at": "2026-01-15T12:00:00Z",
      "updated_at": "2026-01-15T12:00:00Z"
    }
  ],
  "ds_signature": "...",
  "ds_key_id": "ds-primary"
}
```

Access control: unauthenticated callers can only see `granted` status. Authenticated callers can see `pending` and `denied` when their domain matches source/target/actor. Results ordered by `updated_at` DESC.

---

## Keys

### POST /v1/sites/keys/rotate

Rotate a site's Ed25519 public key. The old key signs a canonical rotation message proving ownership of both keys. The DS closes the old key record and inserts the new key into the key history.

Rate limit: per-domain 5/hr

```json
// Request
{
  "domain": "alice.com",
  "old_key": "ssh-ed25519 AAAA... (current key)",
  "new_key": "ssh-ed25519 BBBB... (replacement key)",
  "timestamp": "2026-01-20T12:00:00Z",
  "transition_sig": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----"
}

// Response 200
{
  "success": true,
  "message": "Key rotated successfully",
  "key_id": 42
}
```

The `transition_sig` is the old key's Ed25519 signature over the canonical JSON (keys sorted alphabetically):
```json
{"action":"key-rotation","domain":"alice.com","new_key":"ssh-ed25519 BBBB...","old_key":"ssh-ed25519 AAAA...","timestamp":"2026-01-20T12:00:00Z"}
```

Atomically closes old key and inserts new one. Evicts cached `.well-known/polis`. Emits a `pub.polis.site.key_rotated` stream event.

**Error cases:**
- `400` — Missing required fields, or new key same as old key
- `401` — Transition signature verification failed
- `404` — Domain not registered, or no current key found
- `409` — `old_key` does not match the current active key
- `429` — Rate limited

---

## Stream

### GET /v1/stream?since=0&type=pub.polis.post.published&actor=alice.com&limit=100

Consume events from the discovery stream. Cursor-based pagination.

Rate limit: IP 1200/hr

**Query parameters:**
- `since` (cursor, default `"0"`)
- `type` (optional, comma-separated event types to include)
- `actor` (optional, comma-separated actor domains)
- `target` (optional, filter by target domain)
- `source` (optional, filter by source domain)
- `involved` (optional, filter by domain as either target OR source — mutually exclusive with `target` and `source`, returns 400 if combined)
- `limit` (default 1000, max 1000)

**Headers:** `X-Polis-Domain`, `X-Polis-Timestamp`, `X-Polis-Signature`

```json
// Response 200
{
  "events": [
    {
      "id": 42,
      "type": "pub.polis.post.published",
      "created_at": "2026-01-15T12:00:00Z",
      "actor": "alice.com",
      "signature": "...",
      "payload": {
        "url": "https://alice.com/posts/hello.md",
        "version": "sha256:abc123...",
        "title": "Hello World"
      }
    }
  ],
  "cursor": "42",
  "has_more": false,
  "ds_signature": "...",
  "ds_key_id": "ds-primary"
}
```

Denial visibility: unauthenticated callers cannot see denial events. Authenticated callers see denials scoped to their domain.

The `involved` parameter generates a SQL OR:
```sql
WHERE (payload->>'target_domain' = $1 OR actor = $1)
```
Postgres uses `BitmapOr` across existing indexes (`idx_ds_events_actor`, `idx_ds_events_payload_target_domain`). No new indexes needed.

### POST /v1/stream/batch

Send multiple filter sets in a single request. Each sub-query runs independently (via `Promise.all`) and returns its own events, cursor, and `has_more`. Auth, rate limiting, and response signing are applied once for the entire batch.

Rate limit: IP 1200/hr (shared with `GET /v1/stream`)

**Headers:** `X-Polis-Domain`, `X-Polis-Timestamp`, `X-Polis-Signature`

```json
// Request
{
  "queries": [
    { "since": "0", "limit": 1000, "involvedDomain": "alice.com" },
    { "since": "0", "limit": 1000, "actors": ["bob.com", "charlie.com"] }
  ]
}

// Response 200
{
  "results": [
    {
      "events": [...],
      "cursor": "42",
      "has_more": false
    },
    {
      "events": [...],
      "cursor": "55",
      "has_more": true
    }
  ],
  "ds_signature": "...",
  "ds_key_id": "ds-primary"
}
```

Constraints:
- `queries` must be a non-empty array (returns 400 if empty)
- Maximum 5 queries per batch (returns 400 if exceeded)
- Each sub-query's `limit` is capped at `streamMaxLimit` (default 1000)
- Each sub-query inherits the caller's `visibleDenialsDomain` from auth

### GET /v1/stream/unified?since=0&involved=alice.com&actor=bob.com,charlie.com&limit=3000

Unified OR query — combines `involved` domain and followed actors into a single DB query. Returns events where:

```sql
WHERE id > $cursor AND (
  payload->>'target_domain' = $involvedDomain
  OR actor = $involvedDomain
  OR actor = ANY($actors)
)
```

This is the most efficient query mode: one HTTP request, one DB query, one response signature. The `actor` and `involved` parameters are combined with OR (unlike `GET /v1/stream` where all filters use AND).

Rate limit: IP 1200/hr (shared with `GET /v1/stream`)

**Query parameters:**
- `since` (cursor, default `"0"`)
- `involved` (optional, domain as target OR source)
- `actor` (optional, comma-separated actor domains — OR'd with `involved`)
- `type` (optional, comma-separated event types — applied as AND filter)
- `limit` (default 1000, max 1000)

**Headers:** `X-Polis-Domain`, `X-Polis-Timestamp`, `X-Polis-Signature`

Response format is identical to `GET /v1/stream`.

**Pagination note:** Because all event types share a single `LIMIT`, a burst of events from one category (e.g., feed posts) can push out events from another (e.g., blessings) within a single page. The `has_more` flag triggers an immediate re-sync to recover. Clients should use a higher limit (e.g., 3000) to match the combined capacity of separate queries.

### POST /v1/stream

Publish a signed event to the stream.

Rate limit: per-domain 100/hr

```json
// Request
{
  "type": "pub.polis.follow.announced",
  "actor": "alice.com",
  "payload": {
    "source_domain": "alice.com",
    "target_domain": "bob.com"
  },
  "signature": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----"
}

// Response 201
{
  "success": true,
  "message": "Event published",
  "event_id": 42
}
```

Verifies the actor is registered. Checks operator blocks (domain and type). Evaluates against DS operator policies. Enforces stream mode (allowlist vs blocklist). Event payloads limited to 8KB.

### GET /v1/stream/health

Check stream status.

Rate limit: IP 300/hr

```json
// Response 200
{
  "status": "ok",
  "latest_cursor": "1047",
  "oldest_cursor": "1",
  "event_count": 1047
}
```

---

## Admin

All admin endpoints require `Authorization: Bearer <operator-api-key>` header. Rate limit: 30/hr per IP.

### GET /v1/admin/blocks

List all blocks and stream configuration.

```json
// Response 200
{
  "blocked_domains": [
    {
      "domain": "spam.example.com",
      "scope": "all",
      "reason": "spam source"
    }
  ],
  "blocked_types": [
    {
      "type": "pub.polis.custom.spam",
      "reason": "spam type"
    }
  ],
  "stream_config": {
    "mode": "blocklist",
    "allowed_types": []
  }
}
```

### POST /v1/admin/blocks/domains

Block a domain.

```json
// Request
{
  "domain": "spam.example.com",
  "scope": "all",
  "reason": "spam source"
}

// Response 200
{
  "success": true,
  "message": "Domain spam.example.com blocked"
}
```

### DELETE /v1/admin/blocks/domains

Unblock a domain.

```json
// Request
{
  "domain": "spam.example.com"
}

// Response 200
{
  "success": true,
  "message": "Domain spam.example.com unblocked"
}
```

### POST /v1/admin/blocks/types

Block an event type. Core types (`pub.polis.site.*`, `pub.polis.post.*`, `pub.polis.comment.*`, `pub.polis.follow.*`) cannot be blocked.

```json
// Request
{
  "type": "pub.polis.custom.spam",
  "reason": "spam type"
}

// Response 200
{
  "success": true,
  "message": "Type pub.polis.custom.spam blocked"
}
```

### DELETE /v1/admin/blocks/types

Unblock an event type.

```json
// Request
{
  "type": "pub.polis.custom.spam"
}

// Response 200
{
  "success": true,
  "message": "Type pub.polis.custom.spam unblocked"
}
```

### POST /v1/admin/stream/mode

Set the stream mode (`blocklist` or `allowlist`).

```json
// Request
{
  "mode": "blocklist",
  "allowed_types": []
}

// Response 200
{
  "success": true,
  "message": "Stream mode set to blocklist"
}
```

### POST /v1/admin/stream/purge

Purge events matching filters. At least one filter is required.

```json
// Request
{
  "actor": "spam.example.com",
  "type": "pub.polis.custom.spam",
  "before": "2026-01-01T00:00:00Z"
}

// Response 200
{
  "success": true,
  "message": "Purged 42 events",
  "purged_count": 42
}
```

---

## Infrastructure

### GET /health

Health check. External requests get minimal info; internal requests (via Fly.io `Fly-Forwarded-Port` header) get diagnostics.

```json
// External response 200
{
  "status": "ok",
  "service": "polis-ds"
}

// Internal response 200
{
  "status": "ok",
  "service": "polis-ds",
  "uptime_seconds": 12345,
  "registered_sites": 42,
  "stream": {
    "event_count": 1047,
    "latest_cursor": "1047"
  },
  "db_latency_ms": 5
}
```

### GET /ready

Readiness probe.

```json
// Response 200
{ "status": "ready" }

// Response 503 (DB unavailable)
{
  "status": "not_ready",
  "error": {
    "code": "DB_UNAVAILABLE",
    "message": "Database is not responding"
  }
}
```

### GET /.well-known/polis

DS identity endpoint.

```json
// Response 200
{
  "public_key": "ssh-ed25519 AAAA...",
  "key_id": "ds-primary",
  "signing_algorithm": "ed25519-ssh"
}
```

### GET /policies/rules.jsonl

DS operator policies. Returns JSONL stream. Cached for 5 minutes.

---

## Stream Event Types

Core event types (cannot be blocked by operators):

| Event Type | Emitted By |
|------------|------------|
| `pub.polis.site.registered` | POST /v1/sites |
| `pub.polis.site.reregistered` | POST /v1/sites (re-registration) |
| `pub.polis.site.key_rotated` | POST /v1/sites/keys/rotate |
| `pub.polis.post.published` | POST /v1/content (new post) |
| `pub.polis.post.republished` | POST /v1/content (updated post) |
| `pub.polis.post.unpublished` | POST /v1/content/unpublish (post) |
| `pub.polis.comment.unpublished` | POST /v1/content/unpublish (comment) |
| `pub.polis.comment.published` | POST /v1/content (new comment) |
| `pub.polis.comment.republished` | POST /v1/content (updated comment) |
| `pub.polis.comment.blessing.requested` | POST /v1/content (comment, pending) |
| `pub.polis.comment.blessing.granted` | POST /v1/content (auto-bless) or POST /v1/relationships |
| `pub.polis.comment.blessing.denied` | POST /v1/relationships |
| `pub.polis.follow.announced` | POST /v1/stream (client-published) |
| `pub.polis.follow.removed` | POST /v1/stream (client-published) |
| `pub.polis.tag.applied` | POST /v1/content (tag registration) |
| `pub.polis.tag.removed` | POST /v1/content/unregister (tag) |

---

## Rate Limits

### Per-Domain Limits (after authentication)

| Endpoint | Limit |
|----------|-------|
| Site register/unregister | 5/hr |
| Key rotation | 5/hr |
| Content register | 50/hr |
| Content unregister (tags) | 20/hr |
| Content unpublish | 20/hr |
| Relationship update | 50/hr |
| Stream publish | 100/hr |

### IP-Based Limits

| Endpoint | Limit |
|----------|-------|
| Write pre-auth (all writes) | 120/hr |
| Sites check | 300/hr |
| Content check | 300/hr |
| Content query | 600/hr |
| Relationships query | 600/hr |
| Stream query | 1200/hr |
| Stream health | 300/hr |
| DS public key | 30/hr |
| Admin (all) | 30/hr |

---

## Error Responses

All endpoints return errors in a consistent format:

```json
{
  "error": {
    "code": "AUTHOR_NOT_REGISTERED",
    "message": "Comment author's site is not registered. Run `polis register` first."
  }
}
```

Common error codes:

| Code | HTTP | Meaning |
|------|------|---------|
| `INVALID_PAYLOAD` | 400 | Missing or malformed request fields |
| `INVALID_DOMAIN` | 400 | Domain fails SSRF safety check |
| `SIGNATURE_FAILED` | 401 | Ed25519 signature verification failed |
| `UNAUTHORIZED` | 401 | Missing or invalid operator API key |
| `AUTHOR_NOT_REGISTERED` | 403 | Actor's domain is not registered |
| `TARGET_NOT_REGISTERED` | 403 | Target domain is not registered |
| `BLOCKED` | 403 | Domain or type is blocked by operator |
| `NOT_FOUND` | 404 | Resource does not exist |
| `DUPLICATE` | 409 | Resource already exists |
| `KEY_MISMATCH` | 409 | Old key does not match current active key |
| `PAYLOAD_TOO_LARGE` | 413 | Request body exceeds 64KB |
| `RATE_LIMIT_EXCEEDED` | 429 | Too many requests (includes `Retry-After` header) |
| `DB_UNAVAILABLE` | 503 | Database not responding (readiness probe) |

---

## Wake Callbacks (Outbound)

After emitting events that affect a particular domain, the DS fires a **fire-and-forget** outbound callback to notify the domain that new events are available:

```
GET https://{domain}/v1/wake
```

This is a performance optimization — it triggers the domain's sync cycle so new events (auto-blessings, blessing decisions, comment notifications) are picked up promptly rather than waiting for the next poll cycle.

### Behavior

- **No payload** — the call carries no event data. It merely signals "check your stream."
- **No auth headers** — the endpoint is public and exposes no data.
- **Fire-and-forget** — the DS does not inspect the response. Failures (404, connection refused, timeout) are logged and silently discarded.
- **Rate-limited** — at most one wake per domain per 30 seconds. Rapid events for the same domain are collapsed.
- **3-second timeout** — the DS does not block on slow domains.
- **Hardcoded path** — `/v1/wake` is not configurable. It is a well-known endpoint.

### When wakes are sent

| Trigger | Target domain |
|---------|---------------|
| Comment registered (`pub.polis.comment`) | Post author's domain (extracted from `in_reply_to`) |
| Blessing granted or denied (manual) | Comment author's domain (extracted from `source_url`) |

Auto-blessed comments trigger a wake because the `registerContent` handler emits the blessing event and then wakes the post author's domain.

### For self-hosted sites

Self-hosted polis sites do not implement `/v1/wake`. The DS will receive a 404 or connection refused, which it silently ignores. No action is needed — the wake mechanism is entirely optional. Sites that do not support it continue to discover events through their regular polling cycle.

### Operator control

Set `DS_WAKE_ENABLED=false` to disable all outbound wake callbacks (requires DS restart).
