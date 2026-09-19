# Content Types

*For* [Developers](../../README.md#building-on-polis) — *About* [Content](../README.md#content) — *Kind* [Concept](../../README.md#kinds-of-page) — *See also* [reference](content-system.md) · no spec yet · no recipe yet

> Part of the foundation set: [bundles](bundles.md), **content types** (this doc), [shapes](shapes.md), [themes](themes.md). See [architecture.md](architecture.md) for the four-surface map. For the deep reference (every type, every action, every event), see [content-system.md](content-system.md).

A **content type** is polis's unit of data. Every polis record — a post, a comment, a follow, a blessing, a tag, a DM — belongs to exactly one content type, which determines:

- where it's stored on disk,
- which actions it supports through the dispatch engine (list, get, create, bless, deliver, …), if any,
- which events it emits when it changes,
- which policies govern it,
- and how it gets rendered (or whether it's rendered at all).

The dispatch engine routes API requests through the responsible content type, which is bound to a [bundle](bundles.md) by its namespace.

---

## The core content types

The `pub.polis.core` bundle declares **nine** content types (`DefaultCoreBundle`, `cli-go/pkg/bundle/bundle.go`). Most are published on the author's domain as signed files; `pub.polis.dm` is never published. The table adds one more row, `pub.polis.feed`, which is **not** declared: it is a local cache derived from network activity.

| Type | Published? | Storage | Purpose |
|---|---|---|---|
| `pub.polis.post` | Yes | `content/pub.polis.core/post/YYYYMMDD/slug.md` | Original content the author publishes. Signed, versioned. |
| `pub.polis.comment` | Yes | `content/pub.polis.core/comment/YYYYMMDD/<id>.md` | A reply on another post (or comment). Signed; lives on the commenter's own domain. |
| `pub.polis.tag` | Yes | `content/pub.polis.core/tag/<name>.json` | A tag applied to target URLs. Signed; lets the network categorize content cross-tenant. |
| `pub.polis.attestation` | Yes | `content/pub.polis.core/attestation/<id>.json` | A signed claim about **someone else's** work or identity (see below). One claim, one file, one signature. |
| `pub.polis.follow` | Yes — but its API reads need auth | `content/pub.polis.core/follow/following.json` | Who *you* follow. Published as JSON and **signed** (see below). |
| `pub.polis.license` | Yes | `content/pub.polis.core/license/license.json` | The site's signed outbound terms. See [`signet/spec/license.md`](../../signet/spec/license.md). |
| `pub.polis.actor` | Yes, on an operator's site | `content/pub.polis.core/actor/registry.json` | An operator's signed list of the system actors it runs. See [`signet/spec/custody.md`](../../signet/spec/custody.md). |
| `pub.polis.theme` | Read-only API | Declared in the bundle's `bundle.json`; CSS under `.polis/bundles/pub.polis.core/themes/` | Theme metadata exposed via the API so the webapp can render a theme picker. |
| `pub.polis.dm` | **Never** | `.polis/bundles/pub.polis.core/dm/` | Direct messages — encrypted, never registered with the DS. |
| `pub.polis.feed` | Derived, not declared | `.polis/ds/<ds-domain>/pub.polis.core/state/pub.polis.feed.jsonl` | The aggregated stream of activity from your network. Cached, not authored. |

⚠️ Licence and registry paths are defaults: their `dir` and `mount` are configurable, so a reader finds them through the `license` and `actor_registry` pointers in `.well-known/polis`, never by path.

### `pub.polis.attestation` points outward

Every other signed type is about the author's **own** content — *this is mine, here are my terms.* An
attestation is the one that points at someone else: *on this date, this issuer asserted this predicate
about this subject.*

```json
{
  "type": "pub.polis.attestation",
  "issuer": "https://vdibart.polis.pub",
  "predicate": "pub.polis.attestation.correction",
  "subject": { "type": "uri", "id": "https://site.example/posts/20260901-claim.md",
               "version": "sha256:9f2a…" },
  "asserted": "2026-08-28T23:50:09Z",
  "generator": "polis-cli-go/0.67.0",
  "current_version": "sha256:…",
  "signature": "…"
}
```

⚠️ **It shares `pub.polis.tag`'s envelope and none of its concept.** A tag is *self-directed* — the
file **is** the tag, and it holds many targets under one timestamp. An attestation is *other-directed*
and happens at a moment, so it holds **one subject** and **one date**. Bundling several subjects into
one file would give one timestamp to claims made at different times and make retracting any of them
file surgery.

Four things worth knowing:

- **A claim can be pinned to exact bytes.** `subject.version` scopes it to the content the issuer
  actually saw, and the pin is *inside* the signature — so an edit to the subject cannot move the
  claim, and nobody can repoint it.
- **A record can be the subject of another record.** Records have permanent URLs, so counter-signing,
  withdrawal and disputes all fall out with no new primitive. A `same-as` counter-signature is **two
  independent records referencing each other**, never one file with two signatures.
- **Unknown predicates must still verify and render.** A reader that rejects a predicate it does not
  recognise makes the format unextendable by anyone but polis. Third-party predicates
  (`com.example.reviewed`) are ordinary.
- **Nothing deletes one.** Withdrawal is a signed record, because a `404` read as a retraction would
  make an outage or a lapsed domain read as one too.

Full shape and the exact signing base:
[`docs/signet/spec/attestation.md`](../../signet/spec/attestation.md#the-attestation-record).

### `pub.polis.follow` is signed

The follow list is **authored, not derived** — your action puts an entry in it, it is the source of
truth the discovery service mirrors, and nothing writes it from DS data. So it carries a `signature`
over its own contents, exactly as posts and comments do, and for the same reason: the follow graph is
what every trust computation weights by, and an unsigned roster is editable by anything that can write
the file.

```json
{
  "version": "polis-cli-go/0.67.0",
  "following": [
    { "url": "https://alice.example", "added_at": "2026-08-28T00:00:00Z", "site_title": "Alice" }
  ],
  "signature": "…"
}
```

Three things worth knowing:

- **The write path signs.** Every follow and unfollow re-signs the file, so a roster you have touched
  is signed because *you* acted. Nothing signs it on your behalf.
- **Unsigned is a fact, not a defect.** A follow list nobody has re-authored since signing shipped has
  no signature, and that is a normal, reportable state — never an error. Treating absent as invalid
  is the single most common way to misread this file.
- **A failing signature is evidence, not a verdict.** A follow file whose signature does not verify is
  still loaded and still used. Report it; do not drop follows over it. Refusing to load someone's
  roster on a signature failure would disconnect them from their network as a "security response",
  and the likeliest cause is a bug.

⚠️ `version` here holds the **generator string** (`polis-cli-go/0.67.0`), not a document version —
a historical misnomer kept because the field is published and widely read. It is part of the signed
content.

Full shape and the exact signing base:
[`docs/signet/spec/attestation.md`](../../signet/spec/attestation.md#the-signed-follow-file).

**Blessings** are not a separate content type. A blessing is the post author admitting a comment onto their own pages: the author's software records it in a **signed blessing list**, `content/pub.polis.core/comment/blessed.json`, and the discovery service records the relationship and announces it. See [`signet/spec/attestation.md` § The signed blessing list](../../signet/spec/attestation.md#the-signed-blessing-list) for the file and [content-system.md § Events](content-system.md#events) for the lifecycle.

---

## Anatomy of a content-type declaration

Every content type is declared in its bundle's `bundle.json`:

```json
"pub.polis.post": {
  "dir": "post",                  // where it lives under content/<bundle>/
  "mount": "/posts",              // where it is served under the public site root
  "renderer": "html",             // omitted for types that are not rendered to pages
  "storage": { "pattern": "dated", "date_format": "YYYYMMDD", "versions": true },
  "emits": [
    "pub.polis.post.published",
    "pub.polis.post.republished",
    "pub.polis.post.unpublished"
  ]
}
```

A private type adds `"private": true` (today `pub.polis.dm` and `pub.polis.follow`). ⚠️ **A declaration lists no actions** — the actions a type supports are the handler's answer, below. Notification rules used to sit here too; they now live in the tenant's private `.polis/bundles/registry.json`.

The full schema, validation rules, and conventions are in [`content-system.md` § Bundles](content-system.md#bundles).

---

## Actions — the verbs

Each content type's **handler** reports the actions it supports (`Actions()` in `cli-go/pkg/ops/builtin_core.go` for the core bundle). The REST API dispatches through that engine; the CLI calls the same packages directly, so the underlying behavior is shared:

```
                ┌─────────────────────────────────────────────┐
                │  same package, same behavior, same result   │
                ▼                                             ▼
   POST /v1/content/pub.polis.post                    polis post foo.md
   ───────────────────────────────                    ─────────────────
   Action: create                                             │
   ContentType: pub.polis.post                                │
                │                                             │
         Engine.Dispatch()                                    │
                │                                             │
                └──────────► publish.PublishPost(...) ◄───────┘
```

A few action conventions worth knowing:

| Action | What it does | Examples |
|---|---|---|
| `list` | Return all records of this type. Always paginated by the underlying storage. | `pub.polis.post/list`, `pub.polis.tag/list` |
| `get` | Return a single record. | `pub.polis.post/get`, `pub.polis.theme/get` |
| `create` | Make a new record. For public types, this signs + registers with the DS as part of the same action. | `pub.polis.post/create`, `pub.polis.tag/apply` |
| `delete` / `unpublish` | Remove the record. `unpublish` (posts/comments) is a *clean break* operation that severs DS state too. ⚠️ **Not every type has one** — attestations are withdrawn by a signed record, never deleted, because a deleted claim teaches the network to read a `404` as a retraction. | `pub.polis.tag/delete`, [`polis unpublish`](../../cli/user/command-reference.md#polis-unpublish-path) |
| Type-specific verbs | Each type adds its own: `bless`, `deny`, `revoke`, `send`, `deliver`, `mark_read`, `sync`, … | See `Actions()` in `cli-go/pkg/ops/builtin_core.go` |

⚠️ **Not every declared type is routable.** The core handler wires actions for post, comment, follow, DM, tag, theme and licence (read-only). Attestations and the actor registry have none: they are written by the key holder's `polis attest` and `polis actor`, never through the API. The currently-wired actions are listed in [`api/developer/dispatch-engine.md`](../../api/developer/dispatch-engine.md).

---

## Public vs private types

The `private: true` flag in a content type's bundle declaration means **the type's records may not be read over the v1 API without auth** (`isPrivateContentType`, `webapp/internal/api/router.go`, kept in parity with the flag by a test):

- **Private types** (`dm`, `follow`): every v1 API operation requires `Authorization: Bearer <api-key>`. DMs never leave the tenant directory and are encrypted. ⚠️ **The follow list is private only on the API**: the file itself is published at `content/pub.polis.core/follow/following.json`, signed, because the follow graph is what trust computations weight by. The flag keeps an API caller from enumerating it; it does not hide the file.
- **Every other routable type** (`post`, `comment`, `tag`, `theme`, `license`): `GET /v1/content/<type>` is unauthenticated, and the records are signed and exposed at canonical URLs on the author's domain.
- **The feed cache** is not a declared type and is not on the API; it is local state.

For DMs specifically, the `deliver` action uses **signed-request authentication** (Ed25519 signature over a canonical payload, plus `X-Polis-Domain` / `X-Polis-Timestamp` headers) so a remote site can drop an encrypted DM into your inbox without holding one of your API keys. See [`api/developer/reference.md` § Authentication](../../api/developer/reference.md#authentication).

---

## Lifecycle, signatures, and events

For public content types, every state change follows the same shape:

1. **Sign** — The author's CLI / webapp signs the content's [signing base](../../signet/spec/signing-base.md) with Ed25519, using the local private key.
2. **Write** — The signed file lands in `content/<bundle>/<dir>/...`. The site index `content/pub.polis.core/index.jsonl` gets an entry.
3. **Register** — The action calls the DS to record the canonical URL, version hash, and signature. The DS verifies the signature against the site's public key at `.well-known/polis`.
4. **Emit** — The DS broadcasts the corresponding event into its stream (`pub.polis.post.published`, `pub.polis.comment.blessing.requested`, `pub.polis.tag.applied`, `pub.polis.attestation.issued`, …).
5. **Subscribe** — Every site that polls the DS sees the event on its next sync and updates local caches, badge dots, or notification panels.

DMs skip the DS roundtrip entirely. The complete event catalog (every event, every payload, every subscription glob) is in [`content-system.md` § Events](content-system.md#events).

---

## Custom content types

Custom types live in third-party bundles. Once a bundle declares a new type — say `pub.alice.gardening.plot` — the polis dispatch engine routes its actions through the bundle's handler (executable or HTTP). The new type:

- gets the same REST routes as core types (`/v1/content/pub.alice.gardening.plot`, `/v1/content/pub.alice.gardening.plot/actions/<action>`),
- can declare the events it emits in `bundle.json`,
- is governed by the same [policy grammar](../reference/policy-grammar.md) — operators and individual sites can allow/deny custom types just like core ones,
- can ship its own shape templates (rendering) and theme overrides if it wants per-bundle UI.

What polis *doesn't* try to enforce: the custom type's data shape, validation, or business logic. That's the bundle author's responsibility; the dispatch engine is content-agnostic.

See [`api/developer/dispatch-engine.md` § Handler Types](../../api/developer/dispatch-engine.md#handler-types).

---

## See also

- [bundles.md](bundles.md) — The container that *holds* content type declarations.
- [shapes.md](shapes.md) — How public content types get rendered.
- [policy-grammar.md](../reference/policy-grammar.md) — The grammar for governing content types at the site and DS layers.
- [content-system.md](content-system.md) — Deep reference: every core type's full spec, the complete event catalog, payload structures.
- [api/developer/reference.md](../../api/developer/reference.md) — REST endpoints for every wired action.
- [api/developer/dispatch-engine.md](../../api/developer/dispatch-engine.md) — How actions become handler invocations.
- [security-model.md](../security/security-model.md) — Signing, key continuity, threat analysis for content types.
