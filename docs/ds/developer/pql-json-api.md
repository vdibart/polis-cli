# PQL JSON API

*For* [Developers](../../README.md#building-on-polis) — *Kind* [Reference](../../README.md#kinds-of-page) — *Component* [Discovery service](../README.md) · [Webapp](../../webapp/README.md) — *Code* [`webapp/internal/server/handlers_pql.go`](../../../webapp/internal/server/handlers_pql.go) — *See also* [spec](../../general/reference/pql.md)

`GET /pql/<sentence>` is the PQL-native query endpoint. The **same
grammar** drives the owner SPA, the public infinity stream, and this
developer-facing JSON API — see [`../../general/reference/pql.md`](../../general/reference/pql.md)
for the full grammar. This page is the contract for consuming it as JSON.

There are two hosts:

| Host | Endpoint | `items` are | Scope default (clause omitted) |
|---|---|---|---|
| A tenant | `https://<handle>.polis.pub/pql/<sentence>` | rendered content items | `from @<this-tenant>` |
| Discovery service | `https://<ds-domain>/pql/<sentence>` | raw stream events | `from all polis` |

## Content negotiation

The tenant endpoint content-negotiates on `Accept`:

- `Accept: application/json` → the JSON envelope (below).
- `?format=json` → forces JSON (handy for a browser/curl without headers).
- otherwise → the HTML infinity-stream page.

The DS endpoint is JSON-only.

```bash
curl -H 'Accept: application/json' \
  'https://alice.polis.pub/pql/all+posts+by+date'

curl 'https://alice.polis.pub/pql/all+comments+by+date?format=json'

curl 'https://ds.polis.pub/pql/all+posts+from+alice.polis.pub'
```

## The sentence on the path

The path tail is the PQL sentence with spaces replaced by `+`
(handles keep their dots). On a tenant the `from`/`about` clause is
optional and defaults to the tenant; on the DS a missing clause defaults
to `all polis`.

```
/pql/all+posts+by+date
/pql/all+comments+from+all+polis+to+bless
/pql/all+activity+about+alice.polis.pub
```

## The envelope (`pql.v1`)

```json
{
  "version": "pql.v1",
  "query": "all posts from alice.polis.pub by date",
  "url": "/pql/all+posts+by+date",
  "parsed": {
    "qualifier": "all",
    "type": "posts",
    "relation": "from",
    "scope": "@alice.polis.pub",
    "modifier": "by-date"
  },
  "tenant": "alice.polis.pub",
  "items": [ /* ... */ ],
  "pagination": { "next_cursor": "<opaque>", "has_more": true }
}
```

| Field | Notes |
|---|---|
| `version` | Envelope version. `pql.v1` today. New shapes bump this; integrators should branch on it. |
| `query` | The **resolved** sentence (explicit scope), for display/debugging. |
| `url` | Tenant endpoint only; the short, shareable canonical URL (tenant-relative clause omitted). |
| `parsed` | The structured filter the server resolved the sentence to. |
| `tenant` | Tenant endpoint only; the serving domain. Absent on the DS. |
| `ds_signature`, `ds_key_id` | DS endpoint only; the service's signature over the rest of the envelope, as on its other query responses. |
| `items` | Tenant: rendered content items. DS: raw stream events. |
| `pagination.next_cursor` | Opaque cursor. On a tenant it is empty when there are no more pages; on the DS it is always present (the last event's id, or the cursor you sent when the page is empty). |
| `pagination.has_more` | Whether another page exists. |

## Pagination

Pass `?cursor=<next_cursor>` to fetch the following page; stop when
`has_more` is false. `?limit=<n>` caps the page size (server-clamped; the
DS allows at most 1000). The DS also accepts `?since=` as a synonym for
`cursor`.

```bash
curl -H 'Accept: application/json' \
  'https://alice.polis.pub/pql/all+posts+by+date?cursor=<opaque>&limit=30'
```

## Scope-resolution boundary

PQL is one grammar, but **where a scope resolves** differs by host (full
rationale in [`../../general/reference/pql.md`](../../general/reference/pql.md#scope-resolution-boundary-who-resolves-what)):

- **Public scopes** — `all polis`, `<handle>`, `about <handle>` —
  resolve on the DS, and (`about` aside) on a tenant.
- **`<handle>'s network`** is public, but only a tenant serves it, and
  only for its own handle's profiles; the DS rejects it
  (`PQL_NETWORK_SCOPE_UNSUPPORTED`).
- **First-person scopes** — `me`, `my network`, `my mutuals` (and
  `about me`) — resolve **only** on the owner's own tenant, via the
  authenticated session. The DS rejects them; on a hosted tenant an
  anonymous request for one gets `401 {"error": "unauthorized"}`.
- **Owner-local types** — `messages` (DMs), `drafts` — owner only.
- The `about` relation and **fully-qualified event types**
  (`pub.polis.follow.announced`) are served by the **DS** endpoint, not
  the per-tenant surface.

## Errors

Errors are JSON with an HTTP status. **The DS** adds a stable `code`:

```json
{ "error": "owner-relative scopes ... use a public scope", "code": "PQL_OWNER_RELATIVE_UNSUPPORTED" }
```

| Code | Status | Meaning |
|---|---|---|
| `PQL_PARSE_ERROR` | 400 | The sentence is not valid PQL. |
| `PQL_OWNER_RELATIVE_UNSUPPORTED` | 400 | A first-person scope was sent to the DS, which cannot resolve it. |
| `PQL_OWNER_LOCAL_TYPE` | 400 | `messages`/`drafts` requested on the DS. |
| `PQL_NETWORK_SCOPE_UNSUPPORTED` | 400 | `<handle>'s network` on the DS (reserved). |
| `PQL_BAD_SCOPE` | 400 | A scope the DS does not recognise. |

A signed request whose headers fail verification gets the DS's `AUTH_*`
codes with `401` (see the [API reference](api-reference.md#error-responses)).

**The tenant endpoint** returns `{"error": "..."}` with no `code`: `400`
for an invalid sentence, and `400` pointing to the DS endpoint for the
`about` relation or a fully-qualified event type.

## Authentication

Public scopes need no auth. First-person scopes on a tenant require the
owner's same-origin session. The DS authenticates signed requests for
**denial visibility** only — it does not use auth to resolve scopes.
