# Polis Architecture Overview

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
- [`webapp/designer/*.md`](../../webapp/designer/) — design system: theme variables, nav anatomy, page model.

### 3. polis.pub — the hosted platform

**Where it lives:** `webapp/` (same code as the local webapp) plus operational infrastructure. Production runs on a managed hosting platform, with Postgres for accounts and sessions and a persistent volume for tenant directories. The hosted runtime itself is not in this repo; only the polis pieces it composes (`cli-go/pkg/*`, `webapp/*`) are open-sourced here.

**What it owns:**
- Multi-tenant routing: every registered handle gets `<handle>.polis.pub` and a tenant directory on the hosted data volume.
- Account lifecycle: sign-up, handle claim, registration, key generation, unregistration (hard delete).
- The background actors that keep tenants healthy:
  - **Clerk + Chaplain** — registration intake and follow-up.
  - **Patrol + Medic** — detect and fix tenant drift.
  - **Judge** — validate signatures and watch key continuity (proto-TOFU).
  - **Reaper** — reclaim unused handles.
  - **Tailor** — replay Patrol/Medic changes for audit / rollback.

  Of these, `pkg/tailor` (multi-version site diagnostic and auto-fixer) is open-source and useful to self-hosters as well; the rest of the multi-tenant operational toolchain stays in the hosted runtime.
- The canonical DS deployment at `ds.polis.pub`.

**Who it serves:**
- Users who want polis without running infrastructure themselves.
- The default endpoint for the network's discovery and routing — though anyone can self-host an equivalent.

**Key docs:**
- [`actors.md`](actors.md) — the background actors that keep the hosted platform healthy.

### 4. Discovery Service (DS) — the coordination layer

**Where it lives:** The DS source is currently operated as part of the hosted runtime and is not yet included in this public repo. The canonical deployment runs at `ds.polis.pub`. The public API contract is documented in [`ds/developer/api-reference.md`](../../ds/developer/api-reference.md) and is the stable surface integrators and alternate implementations should target. An open-source release of the DS reference implementation is planned but not committed to a date.

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

**Authoring path:** Author runs `polis post` (CLI) or hits "Publish" in the webapp. The same `publish` package signs the content, writes it under `content/pub.polis.core/post/...`, updates `metadata/public.jsonl`, and registers the URL + version + signature with the DS. The DS verifies the signature against the site's public key and emits a `pub.polis.post.published` event into its stream.

**Reading path:** Visitors fetch a polis site's static HTML directly from the author's domain. The HTML loads the webapp's widget (`webapp/internal/hosted/widget/widget.js`), which queries the DS for blessed comments, blessing status, and (for logged-in viewers) injects the viewer's icon nav as an overlay that autohides over foreign content.

**Coordination path:** When Alice comments on Bob's post, her CLI/webapp registers the comment, then sends a beseech request to the DS, which routes a `pub.polis.comment.beseeched` event to Bob's site. Bob's webapp polls the stream, sees the event, surfaces it on his `comment` icon with a badge dot. He grants or denies; that decision goes back through the DS as a relationship update; the DS emits a blessing event; Alice's site sees it on its next poll.

**Multi-tenant path (polis.pub):** Each hosted tenant is a polis site running the same webapp code, isolated to its own per-tenant directory on the hosted volume. The hosted DS at `ds.polis.pub` is just a DS — there's nothing special about it from a polis site's perspective. A self-hosted site can point at `ds.polis.pub` or any other compatible DS.

---

## "Where do I go for X?"

| If you want to… | Start here |
|---|---|
| Publish a post from the terminal | [`cli/user/command-reference.md#polis-post`](../../cli/user/command-reference.md) |
| Use a browser instead | [`webapp/user/user-manual.md`](../../webapp/user/user-manual.md) |
| Understand bundles, content types, shapes, themes | [`content-system.md`](content-system.md) |
| Customize a theme | [`webapp/designer/theme-system.md`](../../webapp/designer/theme-system.md) + [`cli/user/templating.md`](../../cli/user/templating.md) |
| Write a policy rule | [`cli/user/policies.md`](../../cli/user/policies.md) + [`policy-grammar.md`](../reference/policy-grammar.md) |
| Filter the stream from the URL bar | [`pql.md`](../reference/pql.md) |
| Sign up on polis.pub | [`webapp/user/user-manual.md`](../../webapp/user/user-manual.md) |
| Self-host polis | [`webapp/developer/development.md`](../../webapp/developer/development.md) + [`ds/admin/deployment.md`](../../ds/admin/deployment.md) |
| Integrate via REST API | [`api/developer/reference.md`](../../api/developer/reference.md) |
| Operate a Discovery Service | [`ds/admin/configuration.md`](../../ds/admin/configuration.md) + [`ds/admin/deployment.md`](../../ds/admin/deployment.md) |
| Verify someone else's content | [`security-model.md`](../security/security-model.md) |
| Understand identity, keys, trust | [`security-model.md`](../security/security-model.md) |
| Build a tool on top of polis primitives | [`cli/developer/packages.md`](../../cli/developer/packages.md) + [`api/developer/reference.md`](../../api/developer/reference.md) |
| Run a custom content type | [`api/developer/dispatch-engine.md`](../../api/developer/dispatch-engine.md) §"Custom Bundles" |

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
