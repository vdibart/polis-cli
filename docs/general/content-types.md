# Content Types

> Part of the foundation set: [bundles](bundles.md), **content types** (this doc), [shapes](shapes.md), [themes](themes.md). See [architecture.md](architecture.md) for the four-surface map. For the deep reference (every type, every action, every event), see [content-system.md](content-system.md).

A **content type** is polis's unit of data. Every polis record — a post, a comment, a follow, a blessing, a tag, a DM — belongs to exactly one content type, which determines:

- where it's stored on disk,
- which actions it supports (list, get, create, bless, deliver, …),
- which events it emits when it changes,
- which policies govern it,
- and how it gets rendered (or whether it's rendered at all).

The dispatch engine routes every API request and CLI action through the responsible content type, which is bound to a [bundle](bundles.md) by its namespace.

---

## The core content types

The `pub.polis.core` bundle declares seven content types: **public** ones published on the author's domain (posts and comments are signed and rendered to HTML; the follow list is published as plain JSON), **private** per-tenant state that is never published, and **derived** local caches.

| Type | Public? | Storage | Purpose |
|---|---|---|---|
| `pub.polis.post` | Public | `content/pub.polis.core/post/YYYYMMDD/slug.md` | Original content the author publishes. Signed, versioned. |
| `pub.polis.comment` | Public | `content/pub.polis.core/comment/YYYYMMDD/<id>.md` | A reply on another post (or comment). Signed; lives on the commenter's own domain. |
| `pub.polis.tag` | Public | `content/pub.polis.core/tag/<name>.json` | A tag applied to a target URL. Signed; lets the network categorize content cross-tenant. |
| `pub.polis.theme` | Public (read-only API) | Declared in the active bundle's `bundle.json` | Theme metadata exposed via the API so the webapp can render a theme picker. |
| `pub.polis.follow` | Public | `content/pub.polis.core/follow/following.json` | Who *you* follow. Published as plain JSON (not signed like posts/comments); the DS only sees aggregate follow events. |
| `pub.polis.dm` | Private | `.polis/.../dm/<thread>/...` | Direct messages — NaCl-encrypted, never registered with the DS. |
| `pub.polis.feed` | Derived | `.polis/.../feed/*.jsonl` | The aggregated stream of activity from your network. Cached, not authored. |

**Blessings** are not a separate content type — they're *relationships* between a comment and a post author, recorded by the DS and surfaced on each side. See [content-system.md § Events](content-system.md#events) for the blessing lifecycle.

---

## Anatomy of a content-type declaration

Every content type is declared in its bundle's `bundle.json`:

```json
"pub.polis.post": {
  "dir": "post",                  // where it lives under content/<bundle>/
  "mount": "posts",               // where it renders under the public site root
  "renderer": "html",             // "html" | "json" | "none"
  "private": false,               // if true, never written to public output
  "actions": [
    "list", "get", "create", "update", "delete", "render",
    "draft.list", "draft.get", "draft.save", "draft.delete"
  ],
  "events": [
    "pub.polis.post.published",
    "pub.polis.post.unpublished",
    "pub.polis.post.updated"
  ],
  "notifications": [
    { "id": "post.published.followers", "on": "pub.polis.post.published", "relevance": "followers_of_actor", "template": "{actor} published {object}" }
  ]
}
```

The full schema, validation rules, and conventions are in [`content-system.md` § Bundles](content-system.md#bundles).

---

## Actions — the verbs

Each content type declares the **actions** it supports. The dispatch engine routes both REST API calls and CLI commands through the same action set, so the underlying behavior is shared:

```
                ┌─────────────────────────────────────────────┐
                │  same package, same behavior, same result   │
                ▼                                             ▼
   POST /v1/content/post                              polis post publish foo.md
   ─────────────────────                              ────────────────────────
   Action: create                                     Action: create
   ContentType: pub.polis.post                        ContentType: pub.polis.post
                │                                             │
                └──────────────► Engine.Dispatch() ◄──────────┘
                                       │
                                       ▼
                              publish.Publish(...)
```

A few action conventions worth knowing:

| Action | What it does | Examples |
|---|---|---|
| `list` | Return all records of this type. Always paginated by the underlying storage. | `pub.polis.post/list`, `pub.polis.tag/list` |
| `get` | Return a single record. | `pub.polis.post/get`, `pub.polis.theme/get` |
| `create` | Make a new record. For public types, this signs + registers with the DS as part of the same action. | `pub.polis.post/create`, `pub.polis.tag/apply` |
| `delete` / `unpublish` | Remove the record. `unpublish` (posts/comments) is a *clean break* operation that severs DS state too. | `pub.polis.tag/delete`, [`polis unpublish`](../cli/user/command-reference.md#polis-unpublish-path) |
| Type-specific verbs | Each type adds its own: `bless`, `deny`, `revoke`, `send`, `deliver`, `mark_read`, `refresh`, `sync`, … | See `Actions()` in `cli-go/pkg/ops/builtin_core.go` |

The currently-wired actions for the core bundle are listed in [`api/developer/dispatch-engine.md`](../api/developer/dispatch-engine.md). Routes that are declared but not yet wired return `unsupported_action` — they have HTTP routes but no handler logic yet.

---

## Public vs private types

The `private: true` flag in a content type's bundle declaration means **the type is never written to public output and may not be enumerated without auth**:

- **Public types** (`post`, `comment`, `tag`, `theme`): `GET /v1/content/<type>` is unauthenticated; the records are signed and exposed at canonical URLs on the author's domain.
- **Private types** (`follow`, `dm`, `feed`): every API operation requires `Authorization: Bearer <api-key>`. The records never leave the tenant directory — DMs are encrypted client-side; follow lists and feed caches are local-only.

For DMs specifically, the `deliver` action uses **signed-request authentication** (Ed25519 signature over a canonical payload, plus `X-Polis-Domain` / `X-Polis-Timestamp` headers) so a remote site can drop an encrypted DM into your inbox without holding one of your API keys. See [`api/developer/reference.md` § Authentication](../api/developer/reference.md#authentication).

---

## Lifecycle, signatures, and events

For public content types, every state change follows the same shape:

1. **Sign** — The author's CLI / webapp signs the content (frontmatter SHA-256 → Ed25519 signature) using the local private key.
2. **Write** — The signed file lands in `content/<bundle>/<dir>/...`. The `metadata/public.jsonl` index gets an entry.
3. **Register** — The action calls the DS to record the canonical URL, version hash, and signature. The DS verifies the signature against the site's public key at `.well-known/polis`.
4. **Emit** — The DS broadcasts the corresponding event into its stream (`pub.polis.post.published`, `pub.polis.comment.beseeched`, `pub.polis.tag.applied`, …).
5. **Subscribe** — Every site that polls the DS sees the event on its next sync and updates local caches, badge dots, or notification panels.

Private content types skip the DS roundtrip entirely. The complete event catalog (every event, every payload, every subscription glob) is in [`content-system.md` § Events](content-system.md#events).

---

## Custom content types

Custom types live in third-party bundles. Once a bundle declares a new type — say `pub.alice.gardening.plot` — the polis dispatch engine routes its actions through the bundle's handler (executable or HTTP). The new type:

- gets the same REST routes as core types (`/v1/content/pub.alice.gardening.plot`, `/v1/content/pub.alice.gardening.plot/actions/<action>`),
- can declare its own events and notification rules in `bundle.json`,
- is governed by the same [policy grammar](policy-grammar.md) — operators and individual sites can allow/deny custom types just like core ones,
- can ship its own shape templates (rendering) and theme overrides if it wants per-bundle UI.

What polis *doesn't* try to enforce: the custom type's data shape, validation, or business logic. That's the bundle author's responsibility; the dispatch engine is content-agnostic.

See [`api/developer/dispatch-engine.md` § Custom Bundles](../api/developer/dispatch-engine.md#custom-bundles).

---

## See also

- [bundles.md](bundles.md) — The container that *holds* content type declarations.
- [shapes.md](shapes.md) — How public content types get rendered.
- [policy-grammar.md](policy-grammar.md) — The grammar for governing content types at the site and DS layers.
- [content-system.md](content-system.md) — Deep reference: every core type's full spec, the complete event catalog, payload structures.
- [api/developer/reference.md](../api/developer/reference.md) — REST endpoints for every wired action.
- [api/developer/dispatch-engine.md](../api/developer/dispatch-engine.md) — How actions become handler invocations.
- [security-model.md](security-model.md) — Signing, key continuity, threat analysis for content types.
