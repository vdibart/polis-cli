# Polis Architecture Overview

*For* [Developers](../../README.md#building-on-polis) · [Reviewers](../../README.md#reviewing-the-security-and-identity-design) · [Writers](../../README.md#writing-on-polis) — *Kind* [Concept](../../README.md#kinds-of-page) — *See also* [reference](content-system.md) · [reference](../../ds/developer/api-reference.md) · [guide](../../webapp/user/user-manual.md)

Polis is a decentralized social system with **four primary surfaces**. Each surface is independently deployable, has its own public contract, and serves a different audience. This doc maps them — what each is, what it owns, how data flows between them, and where to look for deeper docs on each.

> **Read first if** you're new to polis, or you know one surface well and need to place it in the larger picture. Once you have this map, every other doc in the tree has a clearer home.

---

## The four surfaces

```
   ┌──────────────────────────────────────────────────────────────────────┐
   │                                                                      │
   │   CLI ────────► WEBAPP ────────► polis.pub ◄─────► DISCOVERY (DS)    │
   │   bash + Go     local Go SPA    hosted platform   coordination layer │
   │                                                                      │
   │   "the truth"   "the UI"        "managed hosting"  "the network"     │
   │                                                                      │
   └──────────────────────────────────────────────────────────────────────┘
```

### 1. CLI — the source of truth

**Where it lives:** `cli-bash/` (the original Bash implementation) and `cli-go/` (the canonical Go implementation; bash is feature-frozen and tracks the Go version).

**What it owns:**
- The on-disk file format under `.polis/` and `content/` — signed posts, comments, blessings, follows, tags, DMs, themes, bundles, registries, policies.
- The Ed25519 signing model and the canonical-URL → version-hash → signature chain.
- All business logic: publishing, commenting, blessing, following, rendering, policy evaluation, dispatch.
- The set of canonical Go packages at `cli-go/pkg/` — every other surface imports from these.

**Who it serves:**
- Authors who want a terminal-first workflow.
- Scripts, CI/CD pipelines, integrations that automate publishing.
- Developers building anything on top of polis primitives — the CLI is the source of truth for "how it works."

**Key docs:**
- [`cli/user/command-reference.md`](../../cli/user/command-reference.md) — every command, every flag.
- [`cli/developer/packages.md`](../../cli/developer/packages.md) — the importable Go packages.
- [`cli/user/json-mode.md`](../../cli/user/json-mode.md) — machine-readable output.

### 2. Webapp — the local UI

**Where it lives:** `webapp/`. Built as either `polis-server` (HTTP server + web UI, no CLI) or `polis-full` (CLI bundled with a `serve` command).

**What it owns:**
- The single-tenant web UI: topbar + 640px centered column, icon row driving PQL preset routes, centered sentence-filter widget, editor, settings.
- The v1 REST [Content Type API](../../api/developer/reference.md) at `/v1/`.
- The widget that gets embedded in rendered HTML — when you visit someone else's polis site, their static HTML loads the polis widget, which renders the viewer's nav over the foreign content.
- Per-tenant request logging (`X-Request-Id` correlation, structured events to an observability backend).

**Who it serves:**
- Humans who want a browser-first editing experience rather than a terminal.
- Self-hosters running their own polis instance.
- Visitors reading published content (the widget injects there).

**Imports from:** `cli-go/pkg/*`. The webapp never duplicates business logic — it wraps the CLI packages in HTTP and HTML.

**Key docs:**
- [`webapp/user/user-manual.md`](../../webapp/user/user-manual.md) — what each page does for an end user.
- [`webapp/developer/development.md`](../../webapp/developer/development.md) — handler patterns, build commands, drift rules.
- [`webapp/designer/`](../../webapp/README.md) — design system: brand, nav anatomy, page model.

### 3. polis.pub — the hosted platform

**Where it lives:** `webapp/` (same code as the local webapp) plus operational infrastructure documented under [`ops/`](../../ops/README.md). Production runs on a managed hosting platform, with a SQL database for accounts and sessions (Postgres when `DATABASE_URL` is set, otherwise a SQLite file on the data volume) and a persistent volume for tenant directories.

**What it owns:**
- Multi-tenant routing: every registered handle gets `<handle>.polis.pub` and a tenant directory on the hosted data volume.
- Account lifecycle: sign-up, handle claim, registration, key generation, unregistration from the discovery service.
- The background actors that keep tenants healthy:
  - **Patrol + Medic** — Patrol detects drift from the on-disk site format; Medic heals it.
  - **Clerk + Chaplain** — Clerk measures parity between what a tenant holds and what the discovery service was told, read-only; Chaplain reconciles what Clerk finds.
  - **Judge** — independently verifies signatures, attestations and key-history continuity, and reports; it never heals.
  - **Reaper** — reminds, then archives and deletes, accounts that never verified.
- The canonical DS deployment at `ds.polis.pub`.

Two more names appear beside the actors and are neither hosted-only nor operator actors. **Tailor** is a standalone `tailor` binary that diagnoses any site against the current spec and, with `--apply`, fixes it — self-hosters run it. **Rosie** is the **user's agent**: under the user's own grant, signing with the user's key and marking every act as hers, she decides blessing requests by the user's published policy. That behaviour runs wherever the user's server runs — localhost, `serve`, or a hosted tenant. (The daily sweep that keeps each tenant's content caches faithful on polis.pub is Medic's, not hers.)

**Who it serves:**
- Users who want polis without running infrastructure themselves.
- The default endpoint for the network's discovery and routing — though anyone can self-host an equivalent.

**Key docs:**
- [`actors.md`](actors.md) — the background actors that keep the hosted platform healthy.

### 4. Discovery Service (DS) — the coordination layer

**Where it lives:** The Discovery Service source is not in the public repository. The canonical deployment runs at `ds.polis.pub`, and its public contract, the stable surface for integrators and alternate implementations, is [`ds/developer/api-reference.md`](../../ds/developer/api-reference.md). The code is pure business logic plus a `StorageAdapter` interface, with a Hono server as the active adapter.

**What it owns:**
- Site registry (who's on the network, what their public key is, what bundles they ship).
- Content metadata (signed URLs, versions, types) for posts, comments, follows, blessings, tags — content lives on the originating site; the DS only stores metadata + URL.
- Signature verification on registration and relationship updates.
- The event stream that lets every site know about activity affecting it.
- Operator policies (`ds_operator_policies`) for network-level content moderation.
- Cross-tenant aggregations (e.g. comment counts per post URL for the infinity stream).

**Who it serves:**
- Polis sites (CLI + webapp instances) coordinating blessing requests, follower notifications, and discovery.
- Integrators reading the public API: site listings, content queries, the event stream.
- Network operators tuning rate limits, blocking abusive domains, or running their own DS.

**Key docs:**
- [`ds/developer/api-reference.md`](../../ds/developer/api-reference.md) — every endpoint, every payload.
- [`ds/developer/stream-architecture.md`](../../ds/developer/stream-architecture.md) — the event stream protocol.
- [`ds/admin/deployment.md`](../../ds/admin/deployment.md) — running your own DS.
- [`ds/admin/configuration.md`](../../ds/admin/configuration.md) — env-var tuning reference.

---

## How they relate

The four surfaces compose into one network. The CLI is the trust root; everything else is a different interface to the same signed-content model.

```
                          POLIS SITE (one author)
                          ───────────────────────

    [author writes]            [HTTP/JSON]              [HTTPS]
    ──────────────►   CLI / Webapp   ────────►   Discovery Service
                       (cli-go/pkg)               (postgres + hono)
                          │                           │
                          │ signs locally             │ verifies signatures
                          │ stores under .polis/      │ stores URL + metadata
                          │ renders to public HTML    │ broadcasts events
                          ▼                           ▼

                   STATIC HTML on the                EVENT STREAM
                   author's domain                   (cursor-paginated,
                   (readers fetch via widget)         polled by every site)
```

**Authoring path:** Author runs `polis post` (CLI) or hits "Publish" in the webapp. The same `publish` package signs the content, writes it under `content/pub.polis.core/post/...`, updates the site index `content/pub.polis.core/index.jsonl`, and registers the URL + version + signature with the DS. The DS verifies the signature against the site's public key and emits a `pub.polis.post.published` event into its stream.

**Reading path:** Visitors fetch a polis site's static HTML directly from the author's domain. The HTML loads the widget script served by the hosted service (not in the public repository), which queries the DS for blessed comments, blessing status, and (for logged-in viewers) injects the viewer's icon nav as an overlay that autohides over foreign content.

**Coordination path:** When Alice comments on Bob's post, her CLI/webapp registers the comment with the DS, which records a `pub.polis.comment.blessing.requested` event. The DS decides nothing. Bob's own software reads the event from the stream: if his policy settles it and he has granted Rosie, she decides it for him; otherwise it surfaces on his `comment` icon with a badge dot and he grants or denies. The decision goes back through the DS as a signed relationship update, the DS emits `pub.polis.comment.blessing.granted` or `.denied`, and Alice's site sees it on its next poll.

**Multi-tenant path (polis.pub):** Each hosted tenant is a polis site running the same webapp code, isolated to its own per-tenant directory on the hosted volume. The hosted DS at `ds.polis.pub` is just a DS — there's nothing special about it from a polis site's perspective. A self-hosted site can point at `ds.polis.pub` or any other compatible DS.

---

## "Where do I go for X?"

| If you want to… | Start here |
|---|---|
| Publish a post from the terminal | [`cli/user/command-reference.md#polis-post`](../../cli/user/command-reference.md) |
| Use a browser instead | [`webapp/user/user-manual.md`](../../webapp/user/user-manual.md) |
| Understand bundles, content types, shapes, themes | [`content-system.md`](content-system.md) |
| Customize a theme | [`themes.md`](themes.md) + [`cli/user/templating.md`](../../cli/user/templating.md) |
| Write a policy rule | [`cli/user/policies.md`](../../cli/user/policies.md) + [`policy-grammar.md`](../reference/policy-grammar.md) |
| Filter the stream from the URL bar | [`pql.md`](../reference/pql.md) |
| Sign up on polis.pub | [`webapp/user/user-manual.md`](../../webapp/user/user-manual.md) |
| Self-host polis | [`webapp/developer/development.md`](../../webapp/developer/development.md) + [`ds/admin/deployment.md`](../../ds/admin/deployment.md) |
| Integrate via REST API | [`api/developer/reference.md`](../../api/developer/reference.md) |
| Operate a Discovery Service | [`ds/admin/configuration.md`](../../ds/admin/configuration.md) + [`ds/admin/deployment.md`](../../ds/admin/deployment.md) |
| Verify someone else's content | [`signet/guides/verify-content.md`](../../signet/guides/verify-content.md) |
| Understand identity, keys, trust | [`security-model.md`](../security/security-model.md) |
| Build a tool on top of polis primitives | [`cli/developer/packages.md`](../../cli/developer/packages.md) + [`api/developer/reference.md`](../../api/developer/reference.md) |
| Run a custom content type | [`api/developer/dispatch-engine.md`](../../api/developer/dispatch-engine.md) §"Handler Types" |

---

## Surface contracts (snap-off preview)

Every surface has a **public contract** — a stable interface that lets you swap one surface without losing the others. Examples:

| Surface | Public contract |
|---|---|
| CLI ↔ webapp | The `cli-go/pkg/*` package interfaces + the file format under `.polis/` |
| Webapp ↔ readers | Static signed HTML + the widget JS embedded in pages |
| Webapp ↔ external tools | The v1 REST API (`/v1/content/{type}`, `/v1/bundles`) |
| Site ↔ DS | The DS HTTP API (`/v1/sites`, `/v1/content`, `/v1/relationships`, `/v1/stream*`) |
| DS ↔ storage backend | The `StorageAdapter` TypeScript interface |
| Bundle ↔ dispatch engine | `bundle.json` + the `Handler` interface (builtin / executable / http) |

Because every interface is stable and signed content is portable, every layer of the stack is independently replaceable: don't like the Bash CLI? Use the Go CLI. Don't like the Go CLI? Build your own on the file format. Want managed hosting? Use polis.pub. Don't want polis.pub? Self-host. Don't trust the canonical DS? Run your own. **This is what "snap off" means** — see [`snap-off-architecture.md`](snap-off-architecture.md) for the full layer-by-layer replaceability map.

---

## See also

- [`vision.md`](../vision.md) — Why polis exists and how it meets users.
- [`content-system.md`](content-system.md) — What polis sites are *made of* (bundles, content types, shapes, themes, events).
- [`snap-off-architecture.md`](snap-off-architecture.md) — Why every layer is replaceable.
- [`security-model.md`](../security/security-model.md) — Cryptographic foundations, threat model, attack vectors.
- [`glossary.md`](../reference/glossary.md) — Quick lookup for polis-specific terms.
