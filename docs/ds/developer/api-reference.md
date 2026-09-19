# Discovery Service API Reference

*For* [Developers](../../README.md#building-on-polis) — *Kind* [Reference](../../README.md#kinds-of-page) — *Component* [Discovery service](../README.md)

All endpoints are available at `{DS_PUBLIC_URL}/{endpoint}`. Resource endpoints live under the `/v1/` prefix.

Auth conventions:
- **Write operations** require an Ed25519 SSH signature in the request body. Every write except `POST /v1/sites`, `POST /v1/content` and `POST /v1/stream` also carries a signed `timestamp`, which must be within **±5 minutes** of the service's clock (checked before the signature)
- **Read queries** on content, relationships, stream and PQL accept **optional** signed GET headers (`X-Polis-Domain`, `X-Polis-Timestamp`, `X-Polis-Signature`). With no headers the caller is unauthenticated and sees only public data; with all three the caller is authenticated as that domain and also sees what is scoped to it. Supplying only some of the three is an error (`401 AUTH_INCOMPLETE`). The signature covers `{"action":"query","domain":"<domain>","timestamp":"<timestamp>"}`, verified against the domain's `.well-known/polis` key, and the timestamp must be within ±5 minutes
- **Admin operations** require `Authorization: Bearer <operator-api-key>` header

Rate limiting:
- **Write endpoints** enforce two tiers: (1) IP-based pre-auth limit of 120 req/hr before any outbound fetch or signature verification, (2) per-domain limit after authentication (varies by endpoint)
- **Read endpoints** enforce IP-based limits (see individual endpoints for rates)
- IP-based limits (and `POST /v1/content`'s per-domain limit) return `429` with a `Retry-After` header and the `RATE_LIMIT_EXCEEDED` code. Other per-domain limits return `429` with the wait in the `error` message only
- **Content quota:** a domain may create at most **2,000 new content records per rolling 30 days** (see [Per-Domain Quotas](#per-domain-quotas)). Over quota returns `429` with `QUOTA_EXCEEDED`, a `Retry-After` header and a `retry_after_seconds` field. The write is refused, never silently dropped

SSRF protection:
- All write endpoints reject domains that name internal infrastructure (IP literals, `localhost`, the reserved TLDs `.local`/`.internal`/`.localhost`/`.test`/`.example`/`.invalid`, cloud metadata hosts) with `400` and the reason in `error`. The signed-GET headers get the same check (`401 AUTH_INVALID_DOMAIN`)

Response signing:
- Responses from `GET /v1/sites/check`, `GET /v1/sites/keys/history`, `GET /v1/content`, `GET /v1/relationships`, `GET /v1/relationships/followed`, the three stream queries and `GET /pql/…` include `ds_signature` and `ds_key_id` fields, whether or not the caller authenticated. The signature covers the response body as JSON with keys sorted alphabetically at every level. Other responses are unsigned

Size limits:
- URLs: 2KB
- Signatures: 2KB
- Metadata: 4KB
- Stream event payloads: 8KB
- POST bodies: 64KB

Query limits:
- `offset` parameter capped at 10,000
- `limit` parameter capped at 100 (content/relationships) or 1,000 (stream)

Stream events:
- **A write never fails because its stream event could not be emitted.** If the insert fails, or the operator's policy blocks the event, the write still succeeds with its usual `2xx` and the event is simply absent from the stream. Clients must not read a successful write as a promise that an event was recorded. The DS logs a failed insert as `db` / `event_insert_failed` at error level, with the actor, the event type and the cause, so its operator can alert on it — that log is the only signal, by design: the write has already been recorded, and failing the request afterwards would report failure for work that was done

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

The `email`, `author_name`, and `description` fields are optional. The signature covers `{"version":1,"action":"register","domain":"alice.com"}`.

Registering a domain that is already registered under the same key is a no-op: `201` with `"message": "Site already registered"` and no event. Under a different key it is refused with `409` — use `POST /v1/sites/keys/rotate`. Otherwise emits `pub.polis.site.registered` (or `pub.polis.site.reregistered` when the domain's row exists but has no current key on record) and returns `"Site registered"` / `"Site re-registered"`.

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

**Query parameters:** `key_id` (optional; if omitted returns the current active key, which is empty unless the operator set `DISCOVERY_SERVICE_PUBLIC_KEY`)

```json
// Response 200
{
  "public_key": "ssh-ed25519 AAAA...",
  "key_id": "ds-primary"
}
```

Returns `404` if a specific `key_id` is requested but not found.

### POST /v1/sites/unregister

Remove a site from the registry. Deletes the site's registration row; its key-history rows are deleted with it. Content records, relationship records and stream events for the domain are not removed.

⚠️ **Known limitation:** content records reference the key-history rows, so for a domain that has registered any content the delete is refused by the database and the request fails with `500` (`INTERNAL_ERROR`); nothing is removed. Only a domain with no content records unregisters successfully today.

Rate limit: per-domain 5/hr

```json
// Request
{
  "version": 1,
  "action": "unregister",
  "domain": "alice.com",
  "timestamp": "2026-01-18T12:00:00Z",
  "signature": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----"
}

// Response 200
{
  "success": true,
  "message": "Site unregistered",
  "domain": "alice.com"
}
```

The signature covers `{"version":1,"action":"unregister","domain":"alice.com","timestamp":"…"}`, verified against the domain's current key in the DS's key history. `404` if the domain is not registered.

### GET /v1/sites/list

Paginated, searchable directory of registered sites. Unauthenticated; the listing never includes a site's email. Used by profile-discovery and "all profiles" views in the webapp.

Rate limit: per-IP via `DS_RATE_IP_SITES_LIST` (default 300/hr).

**Query parameters:**

| Param | Type | Default | Notes |
|-------|------|---------|-------|
| `limit` | int | 50 | Clamped to 1–100; a non-numeric value means the default |
| `cursor` | string | — | The `next_cursor` from the previous page. Opaque; a malformed cursor starts from the beginning |
| `sort` | enum | `name` | `name` (author name, falling back to domain, A→Z) or `activity` (most recently active first). Anything else is `400` |
| `search` | string | — | Case-insensitive substring match on domain or author name (first 100 characters used) |

```json
// Response 200
{
  "rows": [
    {
      "id": "12",
      "domain": "alice.com",
      "registry_url": "https://ds.example.com/sites-check?domain=alice.com",
      "author_name": "Alice",
      "description": "Alice's site",
      "created_at": "2026-04-01T12:00:00Z",
      "updated_at": "2026-04-01T12:00:00Z",
      "last_active_at": "2026-09-01T08:00:00Z",
      "post_count": "14",
      "recent_post_url": "https://alice.com/posts/hello.md",
      "recent_post_title": "Hello World",
      "recent_post_published_at": "2026-09-01T08:00:00Z"
    }
  ],
  "next_cursor": "eyJuYW1lIjoiYWxpY2UiLCJkb21haW4iOiJhbGljZS5jb20ifQ"
}
```

`next_cursor` is `null` on the last page. With `sort=activity` the first request pins a snapshot time inside the cursor, so a site becoming active mid-pagination does not shift page boundaries. There is no offset and no total count.

⚠️ A `400` from this endpoint carries `error` as an object (`{"error":{"code":"INVALID_PAYLOAD","message":"…"}}`), unlike every other endpoint.

---

## Content

### POST /v1/content

Register or update content metadata. The DS extracts the actor domain from the URL. Accepted types: `pub.polis.post`, `pub.polis.comment`, `pub.polis.tag`, `pub.polis.attestation`.

The actor's domain must be registered (`403` otherwise), and for a comment so must the domain of `metadata.in_reply_to`. The signature is verified against the actor's `.well-known/polis` key (`401` on failure).

Rate limit: per-domain 50/hr (checked after the signature verifies)

Quota: per-domain 2,000 **new** records per rolling 30 days. Updating a record the DS already holds (same `type` + `url`) is not counted. Over quota:

```json
// Response 429 (headers include Retry-After: 86400)
{
  "error": "Content quota exceeded: alice.com has registered 2000 new items in the last 30 days (limit 2000). Updates to content already registered are not affected. Try again in 86400 seconds.",
  "code": "QUOTA_EXCEEDED",
  "retry_after_seconds": 86400
}
```

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

// Response 201 (new) or 200 (update: "message": "Content updated", "status": "updated")
{
  "success": true,
  "message": "Content registered",
  "type": "pub.polis.post",
  "url": "https://alice.com/posts/hello.md",
  "status": "created",
  "witness": {
    "action": "content-registration",
    "type": "pub.polis.post",
    "url": "https://alice.com/posts/hello.md",
    "version": "sha256:abc123...",
    "author": "alice",
    "actor": "alice.com",
    "public_key": "ssh-ed25519 AAAA...",
    "artifact_hash": "sha256:def456...",
    "ds": "https://ds.polis.pub",
    "ds_key_id": "ds-primary",
    "witnessed_at": "2026-01-15T12:00:00.123Z",
    "signature": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----"
  }
}
```

**`witness`** — the DS's countersignature over the registration it just verified, made **after** the
author's signature verifies. The client publishes it in the site's witness set. Absent when the DS is
not configured to witness; ⛔ **never required** by anything. Format, canonical bytes and verification:
[`docs/signet/spec/witness.md`](../../signet/spec/witness.md).

**`metadata.artifact_hash`** *(optional, any type)* — `sha256:` + 64 hex over the artifact's signing
base (frontmatter + body as its own signature covers them). When present it is lifted into the
witness, so the witness binds the whole signed artifact rather than only `version`, which for posts
and comments hashes the body. A present-but-malformed value is rejected with `400`. ⚠️ Post
`metadata.published_at` is the registration time, not the post's frontmatter `published:`.

The DS retains the current version's countersignature on the content row; a re-registration replaces
it. No endpoint returns a stored witness.

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

For `pub.polis.comment` content, the response includes a `relationship_status` field. A new comment is always `pending`: the DS records the blessing request and wakes the post author, whose own site decides it later. A comment whose signed `metadata.beseech` is `false` is registered as content only: no blessing request, no `relationship_status`, and only the `published`/`republished` event.

```json
// Comment response
{
  "success": true,
  "message": "Content registered",
  "type": "pub.polis.comment",
  "url": "https://alice.com/comments/reply.md",
  "status": "created",
  "relationship_status": "pending"
}
```

The DS enforces, but never decides a blessing for a user: it does not read the post author's rules. A re-registration of a comment that was already decided reports that earlier `granted` or `denied` status unchanged. Relationships recorded before this change may carry `auto_blessed: true`, `bless_reason`, `policy_rule` and `policy_source` from the DS's old auto-blessing; nothing writes them now.

Emits stream events: `pub.polis.post.published` / `pub.polis.post.republished` for posts, `pub.polis.comment.published` / `pub.polis.comment.republished` for comments, plus `pub.polis.comment.blessing.requested` for a comment's blessing request. An attestation emits `pub.polis.attestation.issued` — or `pub.polis.attestation.withdrawn` when its predicate is `pub.polis.attestation.withdrawal` — on every registration, new or repeated.

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

Query registered content. Only `active` records are returned (unpublished and removed ones are not). Signed GET headers are optional.

Rate limit: IP 600/hr

**Query parameters:** `type` (required), `actor` (optional domain filter), `limit` (default 100, max 100), `offset` (default 0, max 10000), `metadata.<key>` (optional exact-match filters; combine with AND)

Only these metadata keys can be filtered, per type. Any other `metadata.*` parameter, including any on a type not listed, is `400 INVALID_PAYLOAD` with an `error` naming the keys that type allows:

| `type` | Filterable keys |
|---|---|
| `pub.polis.comment` | `metadata.in_reply_to`, `metadata.root_post` |
| `pub.polis.tag` | `metadata.tag`, `metadata.target` |
| `pub.polis.attestation` | `metadata.predicate`, `metadata.subject`, `metadata.subject_type`, `metadata.subject_version` |

**Headers (optional):** `X-Polis-Domain`, `X-Polis-Timestamp`, `X-Polis-Signature`

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

For `pub.polis.comment` queries that list a post's thread (any query without `actor`, or with a `metadata.in_reply_to` / `metadata.root_post` filter): comments with a granted blessing are visible to all; others are visible only to an authenticated caller who wrote the comment or whose domain owns the post. A query by `actor` alone ("comments by this site") returns the site's comments whatever their blessing status. Results ordered by `updated_at` DESC, newest registration first among equal timestamps.

#### Attestations — `type=pub.polis.attestation`

Lists the attestation records sites have registered. **No signed headers are needed** (they are
optional on every query). The filters combine with AND:

| Parameter | Filters on |
|---|---|
| `actor` | the issuing site's domain, e.g. `actor=vdibart.polis.pub` |
| `metadata.predicate` | the fully-qualified predicate, e.g. `metadata.predicate=pub.polis.attestation.withdrawal` |
| `metadata.subject` | the subject's `id`, e.g. `metadata.subject=https://bob.example` |
| `metadata.subject_type` | `identity` or `uri` |
| `metadata.subject_version` | a pinned subject's `sha256:` hash |

```json
// GET /v1/content?type=pub.polis.attestation&metadata.predicate=pub.polis.attestation.withdrawal — abridged
{
  "count": 1,
  "records": [
    {
      "type": "pub.polis.attestation",
      "url": "https://vdibart.polis.pub/content/pub.polis.core/attestation/20260829T044519Z-be6a9fa4824a7bad.json",
      "version": "sha256:be6a9fa4…",
      "actor": "vdibart.polis.pub",
      "metadata": {
        "predicate": "pub.polis.attestation.withdrawal",
        "subject": "https://vdibart.polis.pub/content/pub.polis.core/attestation/20260829T044437Z-dfc92682e69f33ef.json",
        "subject_type": "uri",
        "subject_version": "sha256:dfc92682…"
      }
    }
  ]
}
```

`url` is the record on its issuer's site and `version` is its `current_version`.

⚠️ **A row is an index entry, not the claim.** Fetch the record from `url` and verify it against the
issuer's published key before relying on it — nothing here is signed by the issuer.

⚠️ **The discovery service holds only what was registered with it, so an empty result is not a
negative.** A site that is not registered, or a record written by a path that does not register —
an operator's `agent-disclosure` records from `polis actor register`, for one — never appears here.
**To ask what a site has attested, read the site's own `index.jsonl`**
([recipe 4](../../signet/recipes/04-list-attestations.md)).

### POST /v1/content/unregister

Remove tag content from the index (soft delete — status set to `removed`).

**Tags only.** Posts and comments must use `POST /v1/content/unpublish`. Requests for non-tag types return `400`. `404` if the record is not registered.

Rate limit: per-domain 20/hr

```json
// Request
{
  "type": "pub.polis.tag",
  "url": "https://alice.com/tags/reading/abc123",
  "timestamp": "2026-02-01T12:00:00Z",
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
  "timestamp": "2026-02-01T12:00:00Z",
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

**Comment unpublish:** Resets the comment's own blessing relationship to `pending`, so a republish is a fresh request for the post author's own site to decide.

**Clean break semantics:** Unpublish severs all ties to the published identity. If the content is republished later, it is treated as a completely fresh publication — orphaned blessings are NOT restored, and comment authors must re-beseech for blessing. See [Unpublish Lifecycle](unpublish-lifecycle.md) for full state transition rules.

For both unregister and unpublish the signature covers `{"type":"…","url":"…","timestamp":"…"}`. `orphaned_blessings` and `denied_blessings` appear only when a post is unpublished.

Emits `pub.polis.post.unpublished` or `pub.polis.comment.unpublished` stream event.

### POST /v1/content/comments/counts

Cross-tenant comment counts for a batch of post URLs. Used by the stream-screen to render comment-count badges on posts authored across multiple tenants without N+1 fetches. Unauthenticated.

Rate limit: IP 600/hr (shared with `GET /v1/content`)

```json
// Request
{
  "urls": [
    "https://alice.com/posts/2026/04/hello.md",
    "https://bob.com/posts/2026/04/welcome.md"
  ]
}

// Response 200
{
  "counts": {
    "https://alice.com/posts/2026/04/hello.md": 7
  }
}
```

Each count is the number of `active` registered comments whose `in_reply_to` is that URL, **whatever their blessing status**. URLs with no comments are omitted — treat a missing key as zero. Input URLs are normalized (`.html` → `.md`) before matching, and the keys are the normalized form. At most **50** URLs per request (`400 INVALID_PAYLOAD` otherwise). Counts reflect comments that have registered with the DS — not comments hosted privately.

### POST /v1/content/comments/latest

The single most-recent comment on each of a batch of post URLs, **whatever its blessing status**. Used by the stream-screen's read-focus mode, which shows one comment per post and leaves the full thread on the origin site. Unauthenticated.

Rate limit: IP 600/hr (shared with `GET /v1/content`)

```json
// Request
{ "urls": ["https://alice.com/posts/2026/04/hello.md"] }

// Response 200
{
  "latest": {
    "https://alice.com/posts/2026/04/hello.md": {
      "url": "https://carol.com/comments/reply.md",
      "author_domain": "carol.com",
      "published": "2026-04-02T09:00:00Z",
      "version": "sha256:abc123..."
    }
  }
}
```

"Most recent" orders by the comment's frontmatter timestamp (`published`, which may be `null` for older comments), then by registration time. Only `active` comments count. Posts with no comments are omitted. Same URL normalization and 50-URL cap as `/counts`.

---

## Relationships

### POST /v1/relationships

Grant or deny a blessing. Signature must be from the target domain (post author). This is the only way a blessing is decided: the post author's own site signs it, whether the author acted by hand or a user agent (Rosie) decided under the author's grant.

Rate limit: per-domain 50/hr

```json
// Request
{
  "type": "pub.polis.comment.blessing",
  "source_url": "https://alice.com/comments/reply.md",
  "target_url": "https://bob.com/posts/original.md",
  "action": "grant",
  "timestamp": "2026-09-15T12:00:00Z",
  "agent": "rosie",
  "grant": "https://bob.com/content/pub.polis.core/attestation/20260915T120000Z-0123456789abcdef.json",
  "signature": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----"
}

// Response 200
{
  "success": true,
  "message": "Relationship granted",
  "type": "pub.polis.comment.blessing",
  "source_url": "https://alice.com/comments/reply.md",
  "target_url": "https://bob.com/posts/original.md",
  "status": "granted",
  "grant_state": "live",
  "witness": { "action": "relationship-update", "...": "..." }
}
```

Actions: `grant` (maps to status `granted`), `deny` (maps to status `denied`).

The signature covers `{"type","source_url","target_url","action","timestamp"}` (plus the marker, below); `timestamp` must be within ±5 minutes. The relationship must already exist — it is created when the comment registers — or the request is refused with `404`. The per-domain limit is counted against the target domain and checked before the signature.

**The agent marker.** `agent` and `grant` are optional and travel together: a request with only one of them is refused (`400`). When present they are inside the signature — appended after `type, source_url, target_url, action, timestamp`, in that order ([delegation](../../signet/spec/delegation.md) §5.1). An unmarked request signs the five fields exactly, as before.

**`grant_state`** (marked requests only) is what the DS found about the cited grant in its own registration rows: `live`, `withdrawn` (a registered withdrawal names it), `not-found`, or `unknown` (the rows could not be read). It is a fact, never a verdict: **the relationship is updated whatever it says.** `withdrawn` is logged as `agent_grant_withdrawn` (`source: security`); `not-found` as `agent_grant_not_found` (`source: api`).

**`witness`** is the DS's countersignature over the decision it verified ([witness](../../signet/spec/witness.md) §4.1). Absent when the DS is not configured to witness; absence is never a failure.

Includes a TOCTOU guard: only updates if status is unchanged since read; a concurrent change returns `409`.

Emits `pub.polis.comment.blessing.granted` or `pub.polis.comment.blessing.denied` stream event. The actor in the event is the target domain (post author). A marked request's event also carries `agent`, `grant`, `grant_state`, and the `witness`. The comment author's domain is woken.

### GET /v1/relationships?type=pub.polis.comment.blessing

Query relationships. Signed GET headers are optional.

Rate limit: IP 600/hr

**Query parameters:** `type` (required), `actor` (optional domain), `status` (optional: `pending`|`granted`|`denied`|`orphaned`), `source_url`, `target_url`, `limit` (default 100, max 100), `offset` (default 0, max 10000)

**Headers (optional):** `X-Polis-Domain`, `X-Polis-Timestamp`, `X-Polis-Signature`

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

Access control: an unauthenticated caller sees only `granted` records — no `status` means `granted`, and asking for any other status is `401`. An authenticated caller sees every `granted` record, and a record with any other status only when its domain is the actor or the host of the source or target URL. That holds with or without a `status` parameter. The filter runs before `limit`/`offset`, so a page is short only at the end of the results, and `count` never includes records the caller cannot see. Results ordered by `updated_at` DESC, newest record first among equal timestamps.

### GET /v1/relationships/followed?actor=alice.com

The set of domains an actor currently follows, computed from its `pub.polis.follow.announced` and `pub.polis.follow.removed` events **including archived ones**, so a follow announced before the stream's retention window still counts. Unauthenticated — the follow graph is public.

Rate limit: IP 600/hr (shared with `GET /v1/relationships`)

```json
// Response 200
{
  "targets": ["bob.com", "carol.com"],
  "ds_signature": "...",
  "ds_key_id": "ds-primary"
}
```

A target counts when its announces outnumber its removals. `actor` is case-insensitive, and targets are lowercased. `400 INVALID_ACTOR` if `actor` is missing.

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
  "key_id": 42,
  "witness": {
    "action": "key-rotation",
    "domain": "alice.com",
    "old_key": "ssh-ed25519 AAAA...",
    "new_key": "ssh-ed25519 BBBB...",
    "timestamp": "2026-01-20T12:00:00Z",
    "transition_sig": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----",
    "ds": "https://ds.polis.pub",
    "ds_key_id": "ds-primary",
    "witnessed_at": "2026-01-20T12:00:00.417Z",
    "signature": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----"
  }
}
```

The `transition_sig` is the old key's Ed25519 signature over the canonical JSON (keys sorted alphabetically):
```json
{"action":"key-rotation","domain":"alice.com","new_key":"ssh-ed25519 BBBB...","old_key":"ssh-ed25519 AAAA...","timestamp":"2026-01-20T12:00:00Z"}
```

Atomically closes old key and inserts new one. Evicts cached `.well-known/polis`. Emits a `pub.polis.site.key_rotated` stream event.

**Error cases:**
- `400` — Missing required fields, new key same as old key, or **`timestamp` outside ±5 minutes**
- `401` — Transition signature verification failed
- `404` — Domain not registered, or no current key found
- `409` — `old_key` does not match the current active key
- `429` — Rate limited

**`witness`** — the DS's countersignature over the rotation it just recorded. `witnessed_at` is the
DS's own clock and is byte-identical to the new key's `created_at` in
`GET /v1/sites/keys/history`. The rotating site carries it inside the new `public_key_history` entry.
Absent when the DS is not configured to witness; never required. See
[`docs/signet/spec/witness.md`](../../signet/spec/witness.md).

⚠️ **Freshness is checked BEFORE the signature.** `timestamp` must be within ±5
minutes of DS time. This is not only replay defence: the same value becomes the
new key's `valid_from` in the site's published key history, and the whole
published chain is signed over it. A DS that recorded a backdated rotation
would be a witness that agrees with a backdated chain.

### GET /v1/sites/keys/history?domain=alice.com

The key history the DS has **witnessed** for a domain, oldest first.

Rate limit: per-IP 300/hr

```json
// Response 200
{
  "domain": "alice.com",
  "keys": [
    {
      "public_key": "ssh-ed25519 AAAA...",
      "valid_from": "2026-03-03T05:35:09.000Z",
      "valid_until": "2026-09-15T10:22:03.000Z",
      "transition_sig": null,
      "created_at": "2026-03-03T05:35:09.000Z"
    },
    {
      "public_key": "ssh-ed25519 BBBB...",
      "valid_from": "2026-09-15T10:22:03.000Z",
      "valid_until": null,
      "transition_sig": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----",
      "created_at": "2026-09-15T10:22:04.000Z"
    }
  ],
  "ds_signature": "...",
  "ds_key_id": "ds-primary"
}
```

⛔ **PUBLIC AND UNAUTHENTICATED, AND THAT IS THE DESIGN — NOT AN OVERSIGHT.**
Every entry is a key the site already published, and every `transition_sig` is
verifiable by anyone holding the key it succeeds. There is nothing here a reader
could not assemble by watching the domain over time.

The reason it must stay ungated is what the endpoint is *for*. A polis site
publishes its own key history in `.well-known/polis` (`public_key_history`), so
old signatures survive a rotation **without asking any service**. This endpoint
is the other half: it lets anyone check that the published chain matches the one
the DS saw — catching an entry quietly omitted, or a chain quietly backdated.
Requiring a credential to consult the witness would rebuild the middleman
dependency that publishing the chain exists to remove: a verifier who needs an
API key to check a site's history is a verifier who cannot check it.

⭐ **`created_at` is not a duplicate of `valid_from`.** `valid_from` comes from
the rotating client and sits inside the transition signature; `created_at` is
stamped by the DS and cannot be chosen by the site. The gap between them is the
evidence against a backdated chain.

**Notes:**
- An unregistered domain, or one with no recorded keys, returns `200` with an
  **empty `keys` array**. "I witnessed nothing" is an answer; a 404 would be an
  error the caller has to interpret, and the two are not the same claim.
- Genesis carries `transition_sig: null` — nothing signed for the first key,
  by definition.
- The current key is the entry with `valid_until: null`.
- Response is signed (`ds_signature` / `ds_key_id`) like `/v1/sites/check`, so a
  contradiction is attributable to this DS rather than to whoever proxied it.
  Signing is attribution, never a gate.

**Error cases:**
- `400 INVALID_PAYLOAD` — `domain` query parameter missing, or not a hostname: more than 253 characters, or a label that is empty, longer than 63 characters, or holds anything but letters, digits, `-` and `_`
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
- `created_after` (optional, ISO 8601 — only events created at or after this time)
- `limit` (default 1000, max 1000)

`source` matches the event's `actor`; `target` matches `payload.target_domain`. All filters combine with AND.

**Headers (optional):** `X-Polis-Domain`, `X-Polis-Timestamp`, `X-Polis-Signature`

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
      },
      "signed_by": "alice.com",
      "principal": "alice.com",
      "authority": "self"
    }
  ],
  "cursor": "42",
  "has_more": false,
  "ds_signature": "...",
  "ds_key_id": "ds-primary"
}
```

**Who signed, on whose behalf, under what authority.** Every event stored since
migration 020 carries three facts, recorded when the event was accepted:

| Field | Value |
|---|---|
| `signed_by` | the domain whose published key the event's `signature` was verified against |
| `principal` | the party the event is on behalf of — today always the same as `actor` |
| `authority` | `self` — the principal's own key signed it · a **custody grant URL** — someone else signed, and a registered grant from the principal names them · `none` — someone else signed and **no grant was found** · `withdrawn` — the grant found has been withdrawn · `unknown` — the service could not look |

- ⛔ **`none` is not "unauthorised"**, and `unknown` is not `none`. The service **records; it never
  rejects** an event for its authority.
- ⚠️ **A row is immutable.** It records what was true when the event was accepted; a later withdrawal
  does not rewrite it.
- ⚠️ **Absent means *not recorded*.** Events stored before migration 020 omit all three fields — never
  read an absent `authority` as `self`.
- ⚠️ **`self` is a fact about keys, not about people.** Under key custody an operator signing with a
  tenant's key produces `self`, indistinguishable from the tenant. Whether that matches what the
  operator declared is for the tenant to check — see
  [`custody.md` §12](../../signet/spec/custody.md#12-custody-of-a-tenants-key--the-tenant-half).
- **Grant lookup is from registration rows only** — no fetch, and no verification of a delegated
  event's chain.

Denial visibility: unauthenticated callers cannot see denial events. Authenticated callers see denials scoped to their domain.

The `involved` parameter generates a SQL OR:
```sql
WHERE (payload->>'target_domain' = $1 OR actor = $1)
```
Postgres uses `BitmapOr` across existing indexes (`idx_ds_events_actor`, `idx_ds_events_payload_target_domain`). No new indexes needed.

### POST /v1/stream/batch

Send multiple filter sets in a single request. Each sub-query runs independently — one after another, not in parallel — and returns its own events, cursor, and `has_more`. Auth, rate limiting, and response signing are applied once for the entire batch.

Rate limit: IP 1200/hr (shared with `GET /v1/stream`)

**Headers (optional):** `X-Polis-Domain`, `X-Polis-Timestamp`, `X-Polis-Signature`

Each sub-query accepts `since`, `limit`, `types`, `actors`, `targetDomain`, `sourceDomain` and `involvedDomain` (arrays and camelCase, unlike the GET parameters).

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

**Headers (optional):** `X-Polis-Domain`, `X-Polis-Timestamp`, `X-Polis-Signature`

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

Clients may publish only these `pub.polis.*` types: `pub.polis.follow.announced`, `pub.polis.follow.removed`, `pub.polis.actor.registered`, `pub.polis.actor.reregistered`, `pub.polis.actor.withdrawn`. Every other `pub.polis.*` type is emitted by the service itself and refused here (`400`); types outside `pub.polis.` are not restricted.

Verifies the actor is registered (`403`), then evaluates the event against the DS operator policies — a `deny` match is refused with `403` — then verifies the signature over `{"type","payload"}` against the actor's `.well-known/polis` key (`401`), then applies the per-domain limit. Event payloads limited to 8KB.

### GET /pql/:sentence

Query the stream with a [PQL](../../general/reference/pql.md) sentence in the path, e.g. `GET /pql/all+posts+from+alice.com`. The response is the shared PQL envelope; its `items` are stream events. Full contract, including the envelope and its error codes: [PQL JSON API](pql-json-api.md).

Rate limit: IP 1200/hr (shared with `GET /v1/stream`)

**Query parameters:** `since` or `cursor` (default `"0"`), `limit` (default 1000, max 1000)

**Headers (optional):** `X-Polis-Domain`, `X-Polis-Timestamp`, `X-Polis-Signature`

The service resolves only public scopes. A sentence with no scope means all of polis.

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

All admin endpoints require `Authorization: Bearer <operator-api-key>` header (set via `OPERATOR_API_KEYS` env var, comma-separated for rotation, or legacy `OPERATOR_API_KEY`). A missing or wrong key is `401`. Rate limit: IP 30/hr across all admin endpoints. Every admin action is logged and written, best-effort, to `admin_audit_log`, with `operator_key_index` (the authenticating key's position in `OPERATOR_API_KEYS`) among its params.

Content blocking is **policy-based**: every block is a row in the `ds_operator_policies` table, expressed in the Layer 3 vocabulary of the policy grammar (`allow|deny <type> from <source> [at <domain>]`) and evaluated in insertion order, first match winning. The legacy `admin_blocked_domains` / `admin_blocked_types` tables and the `/v1/admin/blocks*` and `/v1/admin/stream/mode` endpoints have been removed.

### GET /v1/admin/policies

List operator policy rules, oldest first.

**Query parameters:** `limit` (default 200, max 1000), `cursor` (the previous page's `cursor`)

```json
// Response 200
{
  "policies": [
    {
      "id": 42,
      "active": true,
      "policy": "deny all from all at spam.example.com",
      "reason": "spam",
      "created_at": "2026-04-01T12:00:00Z",
      "updated_at": "2026-04-01T12:00:00Z"
    }
  ],
  "cursor": "42",
  "has_more": false
}
```

### POST /v1/admin/policies

Add a policy rule. The rule string uses the same v2 grammar as site policies; see `docs/general/reference/policy-grammar.md`. Refused with `400` if `policy` is missing, longer than 500 characters, or does not parse.

```json
// Request
{
  "policy": "deny all from all at spam.example.com",
  "reason": "spam"
}

// Response 200
{ "success": true, "id": 42 }
```

### DELETE /v1/admin/policies/:id

Remove a policy rule by ID.

```json
// Response 200
{ "success": true, "message": "Policy 42 removed" }
```

### PATCH /v1/admin/policies/:id

Enable or disable a policy without removing it. `active` must be a boolean (`400` otherwise).

```json
// Request
{ "active": false }

// Response 200
{ "success": true, "message": "Policy 42 disabled" }
```

### POST /v1/admin/block/domain

Convenience wrapper that adds a `deny all from all at <domain>` policy (domain lowercased).

```json
// Request
{ "domain": "spam.example.com", "reason": "spam" }

// Response 200
{ "success": true, "id": 43 }
```

### DELETE /v1/admin/block/domain

Convenience wrapper that removes every `deny all from all at <domain>` rule, matched case-insensitively.

```json
// Request
{ "domain": "spam.example.com" }

// Response 200
{ "success": true, "message": "Domain spam.example.com unblocked", "removed": 1 }
```

### POST /v1/admin/stream/purge

Purge events matching filters (combined with AND). At least one filter is required (`400` otherwise).

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

Health check. External requests get minimal info; internal requests (identified by the platform's internal forwarding header) get diagnostics.

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
  "error": "Database is not responding",
  "code": "DB_UNAVAILABLE"
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

DS operator policies, read live from the database. Unauthenticated. Returns JSONL (`application/jsonl`, `Cache-Control: public, max-age=300`): a header line `{"version":1,"generator":"polis-ds/<version>"}`, then one `{"active":…,"policy":"…"}` line per rule.

---

## Stream Event Types

Every event type can be blocked by an operator `deny` rule; none is exempt.

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
| `pub.polis.comment.blessing.granted` | POST /v1/relationships |
| `pub.polis.comment.blessing.denied` | POST /v1/relationships |
| `pub.polis.attestation.issued` | POST /v1/content (attestation) |
| `pub.polis.attestation.withdrawn` | POST /v1/content (attestation whose predicate is `pub.polis.attestation.withdrawal`) |
| `pub.polis.tag.applied` | POST /v1/content (tag registration) |
| `pub.polis.tag.removed` | POST /v1/content/unregister (tag) |
| `pub.polis.follow.announced` | POST /v1/stream (client-published) |
| `pub.polis.follow.removed` | POST /v1/stream (client-published) |
| `pub.polis.actor.registered` | POST /v1/stream (client-published) |
| `pub.polis.actor.reregistered` | POST /v1/stream (client-published) |
| `pub.polis.actor.withdrawn` | POST /v1/stream (client-published) |

`pub.polis.comment.blessing.denied` is visible only to an authenticated caller whose domain is the event's source or target.

Events older than the retention window (90 days by default) move to an archive table and no longer appear in stream queries.

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

### Per-Domain Quotas

A rate limit bounds how fast a domain writes; a quota bounds how much it can add.

| Resource | Quota | Counts |
|----------|-------|--------|
| Content records (`POST /v1/content`) | **2,000 per rolling 30 days** | Records the domain **created** in the window, in every status (unpublished included). Updates to an existing record do not count |

- Over quota is **refused** (`429`, `QUOTA_EXCEEDED`), never accepted and dropped. `retry_after_seconds` (and `Retry-After`) is when enough of the domain's oldest records in the window age out for one more to fit.
- **Nothing already stored is deleted to make room.** Content records have no retention window.
- ⚠️ **The quota is a rate over a long window, not a lifetime cap.** Because records are kept forever, one domain's permanent records are bounded by rate × time — at most 24,000 a year — and there is no ceiling on the total.
- The number was chosen from the live network: in September 2026 the busiest site had created at most 671 records in any 30 days, and every other site 20 or fewer. If a legitimate site of yours needs more, the operator can raise `DS_QUOTA_DOMAIN_CONTENT_ROWS`.

### IP-Based Limits

| Endpoint | Limit |
|----------|-------|
| Write pre-auth (all writes) | 120/hr |
| Sites check | 300/hr |
| Sites list | 300/hr |
| Sites key history | 300/hr |
| Content check | 300/hr |
| Content query (incl. comment counts and latest) | 600/hr |
| Relationships query (incl. followed) | 600/hr |
| Stream query (incl. batch, unified, PQL) | 1200/hr |
| Stream health | 300/hr |
| DS public key | 30/hr |
| Admin (all) | 30/hr |

---

## Error Responses

Every error body carries `error` as a **string**, with an optional machine-readable `code`:

```json
{
  "error": "Rate limit exceeded. Retry after 1800s",
  "code": "RATE_LIMIT_EXCEEDED"
}
```

(The one exception is a `400` from `GET /v1/sites/list`; see that endpoint.)

⚠️ **Many errors carry no `code`.** Failures inside the write handlers — unregistered domain (`403`), signature failure (`401`), unknown record (`404`), key mismatch or concurrent change (`409`), a stale `timestamp` (`400`), a policy denial (`403`), and most per-domain rate limits (`429`) — return only `error`. Read the HTTP status first.

Codes the service emits:

| Code | HTTP | Meaning |
|------|------|---------|
| `INVALID_PAYLOAD` | 400 | Missing or malformed query parameters, batch bodies or URL lists; unparseable JSON |
| `INVALID_ACTOR` | 400 | `GET /v1/relationships/followed` without `actor` |
| `PQL_PARSE_ERROR`, `PQL_OWNER_LOCAL_TYPE`, `PQL_OWNER_RELATIVE_UNSUPPORTED`, `PQL_NETWORK_SCOPE_UNSUPPORTED`, `PQL_BAD_SCOPE` | 400 | `GET /pql/…` — see [PQL JSON API](pql-json-api.md) |
| `AUTH_INCOMPLETE` | 401 | Some but not all of the three signed-GET headers |
| `AUTH_INVALID_TIMESTAMP` | 401 | `X-Polis-Timestamp` is not ISO 8601 |
| `AUTH_TIMESTAMP_EXPIRED` | 401 | `X-Polis-Timestamp` more than 5 minutes from service time |
| `AUTH_INVALID_DOMAIN` | 401 | `X-Polis-Domain` fails the SSRF check |
| `AUTH_SIGNATURE_INVALID` | 401 | Signed-GET signature does not verify |
| `AUTH_VERIFICATION_FAILED` | 401 | The domain's `.well-known/polis` could not be fetched |
| `NOT_FOUND` | 404 | `GET /v1/sites/public-key?key_id=` names no key |
| `PAYLOAD_TOO_LARGE` | 413 | Request body exceeds 64KB |
| `INVALID_CONTENT_TYPE` | 415 | POST body is not `application/json` |
| `RATE_LIMIT_EXCEEDED` | 429 | An IP limit, or `POST /v1/content`'s per-domain limit (includes `Retry-After` header; `POST /v1/content` also `retry_after_seconds`) |
| `QUOTA_EXCEEDED` | 429 | Domain has created its quota of new content records in the window (includes `Retry-After` header and `retry_after_seconds`) |
| `INTERNAL_ERROR` | 500 | Unhandled server error |
| `DB_UNAVAILABLE` | 503 | Database not responding (readiness probe) |

---

## Wake Callbacks (Outbound)

After emitting events that affect a particular domain, the DS fires a **fire-and-forget** outbound callback to notify the domain that new events are available:

```
GET https://{domain}/v1/wake
```

This is a performance optimization — it triggers the domain's sync cycle so new events (blessing requests, blessing decisions, comment notifications) are picked up promptly rather than waiting for the next poll cycle.

### Behavior

- **No payload** — the call carries no event data. It merely signals "check your stream."
- **No auth headers** — the endpoint is public and exposes no data.
- **Fire-and-forget** — the DS does not inspect the response. Failures (404, connection refused, timeout) are logged and silently discarded.
- **Never to the event's own author** — no wake is sent when the affected domain is the one that caused the event.
- **Rate-limited** — at most one wake per domain per 30 seconds. Rapid events for the same domain are collapsed.
- **3-second timeout** — the DS does not block on slow domains.
- **Hardcoded path** — `/v1/wake` is not configurable. It is a well-known endpoint.

### When wakes are sent

| Trigger | Target domain |
|---------|---------------|
| Comment registered (`pub.polis.comment`) | Post author's domain (extracted from `in_reply_to`) |
| Blessing granted or denied (by hand or by the post author's agent) | Comment author's domain (extracted from `source_url`) |

A registered comment wakes the post author's domain so its own site picks up the blessing request and decides it. The DS never asks a site for a decision; the site pulls the request from the stream.

### For self-hosted sites

Self-hosted polis sites do not implement `/v1/wake`. The DS will receive a 404 or connection refused, which it silently ignores. No action is needed — the wake mechanism is entirely optional. Sites that do not support it continue to discover events through their regular polling cycle.

### Operator control

Set `DS_WAKE_ENABLED=false` to disable all outbound wake callbacks (requires DS restart).
