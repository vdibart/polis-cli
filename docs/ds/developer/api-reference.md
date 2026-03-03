# API Reference

All endpoints are available at `{DS_PUBLIC_URL}/{endpoint}`.

Auth conventions:
- **Write operations** require an Ed25519 signature in the request body
- **Read operations** on content/relationships require signed GET headers (`X-Polis-Domain`, `X-Polis-Timestamp`, `X-Polis-Signature`)
- **Admin operations** require `X-Operator-Api-Key` header

Rate limiting:
- **Write endpoints** enforce two tiers: (1) IP-based pre-auth limit of 120 req/hr before any outbound fetch or signature verification, (2) per-domain limit after authentication
- **Read endpoints** enforce IP-based limits (see individual endpoints for rates)
- Rate-limited responses return `429` with `Retry-After` header and `RATE_LIMIT_EXCEEDED` error code

SSRF protection:
- All write endpoints reject domains that resolve to internal infrastructure (IP literals, `localhost`, reserved TLDs, cloud metadata endpoints) with `400 INVALID_DOMAIN`

Query limits:
- `offset` parameter is capped at 10,000 on ds-content-query and ds-relationship-query

---

## Sites

### POST /ds-sites-register

Register a site with the discovery service.

```json
// Request
{
  "domain": "alice.com",
  "public_key": "ssh-ed25519 AAAA...",
  "email": "alice@alice.com",
  "timestamp": "2026-01-18T12:00:00Z",
  "signature": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----"
}

// Response 201
{
  "domain": "alice.com",
  "registered": true,
  "attestation": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----"
}
```

### GET /ds-sites-check?domain=alice.com

Check if a domain is registered.

```json
// Response 200
{
  "domain": "alice.com",
  "registered": true
}
```

### GET /ds-sites-public-key

Get the discovery service's public key for verifying attestations.

```json
// Response 200
{
  "public_key": "ssh-ed25519 AAAA...",
  "key_id": "polis-discovery-v1"
}
```

### POST /ds-sites-unregister

Remove a site from the registry (hard delete).

```json
// Request
{
  "domain": "alice.com",
  "timestamp": "2026-01-20T12:00:00Z",
  "signature": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----"
}

// Response 204 (no body)
```

---

## Content

### POST /ds-content-register

Register or update content metadata.

```json
// Request
{
  "type": "polis.post",
  "url": "https://alice.com/posts/hello.md",
  "version": "sha256:abc123...",
  "metadata": {
    "title": "Hello World",
    "published_at": "2026-01-15T12:00:00Z"
  },
  "timestamp": "2026-01-15T12:00:00Z",
  "signature": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----"
}

// Response 201
{
  "url": "https://alice.com/posts/hello.md",
  "version": "sha256:abc123...",
  "status": "created"
}
```

### GET /ds-content-check?type=polis.post&url=https://alice.com/posts/hello.md

Check if specific content is registered.

```json
// Response 200
{
  "registered": true,
  "url": "https://alice.com/posts/hello.md",
  "version": "sha256:abc123...",
  "type": "polis.post"
}

// Response 404
{
  "registered": false
}
```

### GET /ds-content-query?type=polis.post

Query registered content. Requires signed GET headers.

**Query parameters:** `type` (required), `domain`, `limit`, `offset`

**Headers:** `X-Polis-Domain`, `X-Polis-Timestamp`, `X-Polis-Signature`

```json
// Response 200
{
  "count": 2,
  "items": [
    {
      "url": "https://alice.com/posts/hello.md",
      "version": "sha256:abc123...",
      "type": "polis.post",
      "metadata": { "title": "Hello World" },
      "created_at": "2026-01-15T12:00:00Z"
    }
  ]
}
```

### POST /ds-content-unregister

Remove content from the index.

```json
// Request
{
  "type": "polis.post",
  "url": "https://alice.com/posts/hello.md",
  "timestamp": "2026-01-20T12:00:00Z",
  "signature": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----"
}

// Response 204 (no body)
```

---

## Relationships

### POST /ds-relationship-update

Create or update a relationship (blessing, following, etc.).

```json
// Request (blessing)
{
  "type": "polis.blessing",
  "action": "beseech",
  "source_url": "https://alice.com/comments/reply.md",
  "target_url": "https://bob.com/posts/original.md",
  "content_version": "sha256:abc123...",
  "timestamp": "2026-01-15T12:00:00Z",
  "signature": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----"
}

// Response 201
{
  "type": "polis.blessing",
  "source_url": "https://alice.com/comments/reply.md",
  "target_url": "https://bob.com/posts/original.md",
  "status": "pending"
}

// Request (follow)
{
  "type": "polis.follow",
  "action": "follow",
  "source_url": "https://bob.com",
  "target_url": "https://alice.com",
  "timestamp": "2026-01-15T12:00:00Z",
  "signature": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----"
}

// Response 201
{
  "type": "polis.follow",
  "source_url": "https://bob.com",
  "target_url": "https://alice.com",
  "status": "active"
}
```

### GET /ds-relationship-query?type=polis.blessing

Query relationships. Requires signed GET headers.

**Query parameters:** `type` (required), `source`, `target`, `status`, `limit`, `offset`

**Headers:** `X-Polis-Domain`, `X-Polis-Timestamp`, `X-Polis-Signature`

```json
// Response 200
{
  "count": 1,
  "items": [
    {
      "type": "polis.blessing",
      "source_url": "https://alice.com/comments/reply.md",
      "target_url": "https://bob.com/posts/original.md",
      "status": "pending",
      "created_at": "2026-01-15T12:00:00Z"
    }
  ]
}
```

---

## Keys

### POST /ds-key-rotate

Rotate a site's Ed25519 public key. The old key signs a canonical rotation message proving ownership of both keys. The DS closes the old key record and inserts the new key into the key history.

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

The `transition_sig` is the old key's Ed25519 signature over the canonical JSON:
```json
{"action":"key-rotation","domain":"alice.com","new_key":"ssh-ed25519 BBBB...","old_key":"ssh-ed25519 AAAA...","timestamp":"2026-01-20T12:00:00Z"}
```

Keys are sorted alphabetically. A `polis.site.key_rotated` stream event is emitted on success.

**Error cases:**
- `400` — Missing required fields, or new key same as old key
- `404` — Domain not registered, or no current key found
- `409` — `old_key` does not match the current active key
- `401` — Transition signature verification failed
- `429` — Rate limited

---

## Stream

### GET /ds-stream?after=cursor&type=polis.follow

Consume events from the discovery stream. Cursor-based pagination.

**Query parameters:** `after` (cursor, optional), `type` (filter, optional), `limit` (default 100)

```json
// Response 200
{
  "events": [
    {
      "id": "evt_42",
      "type": "polis.follow",
      "data": {
        "source": "https://bob.com",
        "target": "https://alice.com"
      },
      "namespace": "bob.com",
      "created_at": "2026-01-15T12:00:00Z"
    }
  ],
  "cursor": "42",
  "has_more": false
}
```

### POST /ds-stream-publish

Publish a signed event to the stream.

```json
// Request
{
  "type": "polis.notification.mention",
  "data": {
    "source": "https://alice.com/posts/hello.md",
    "target": "https://bob.com"
  },
  "namespace": "alice.com",
  "timestamp": "2026-01-15T12:00:00Z",
  "signature": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----"
}

// Response 201
{
  "id": "evt_43",
  "type": "polis.notification.mention",
  "cursor": "43"
}
```

### GET /ds-stream-health

Check stream status.

```json
// Response 200
{
  "status": "ok",
  "mode": "live",
  "event_count": 1247
}
```

---

## Admin

All admin endpoints require the `X-Operator-Api-Key` header.

### GET /admin/blocks

List all blocks and stream configuration.

```json
// Response 200
{
  "domains": ["spam.example.com"],
  "types": [],
  "config": {
    "mode": "live"
  }
}
```

### POST /admin/blocks/domains

Block or unblock a domain.

```json
// Request
{
  "action": "block",
  "domain": "spam.example.com"
}

// Response 200
{
  "action": "block",
  "domain": "spam.example.com"
}
```

### POST /admin/blocks/types

Block or unblock an event type.

```json
// Request
{
  "action": "block",
  "type": "polis.notification.mention"
}

// Response 200
{
  "action": "block",
  "type": "polis.notification.mention"
}
```

### POST /admin/stream-mode

Set the stream mode.

```json
// Request
{
  "mode": "maintenance"
}

// Response 200
{
  "mode": "maintenance"
}
```

### POST /admin/stream-purge

Purge events before a timestamp.

```json
// Request
{
  "before": "2026-01-01T00:00:00Z"
}

// Response 200
{
  "purged": 847
}
```

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
| `SIGNATURE_FAILED` | 403 | Ed25519 signature verification failed |
| `AUTHOR_NOT_REGISTERED` | 403 | Actor's domain is not registered |
| `TARGET_NOT_REGISTERED` | 403 | Target domain is not registered |
| `NOT_FOUND` | 404 | Resource does not exist |
| `DUPLICATE` | 409 | Resource already exists |
| `BLOCKED` | 403 | Domain or type is blocked by operator |
| `UNAUTHORIZED` | 401 | Missing or invalid operator API key |
| `RATE_LIMIT_EXCEEDED` | 429 | Too many requests (includes `Retry-After` header) |
| `INVALID_DOMAIN` | 400 | Domain fails SSRF safety check (IP literal, localhost, reserved TLD, metadata endpoint) |
| `PAYLOAD_TOO_LARGE` | 413 | Request body exceeds 64KB |
