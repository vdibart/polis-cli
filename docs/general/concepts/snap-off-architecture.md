# Snap Off Architecture

> The architectural thesis polis hangs on. Builds on: [architecture.md](architecture.md) (the four surfaces) and [bundles.md](bundles.md) / [content-types.md](content-types.md) / [shapes.md](shapes.md) / [themes.md](themes.md) (what each surface is made of). For the protocol primitives that *cannot* snap off, see [security-model.md](../security/security-model.md) and [policy-grammar.md](../reference/policy-grammar.md).

## The one-line version

**If you don't like a layer of polis, snap it off and replace it.**

That's not rhetoric. It's a structural property: polis is a protocol with a small set of fixed contracts (Ed25519 signatures, `.well-known/polis`, the bundle namespace, the DS API, core content-type schemas) and everything else — *every other layer of the stack* — is a default with a public interface you can swap.

The Bash CLI is a default. Don't like it? Use the Go CLI. Don't like the Go CLI? Build a new one on the same file format. Don't want to run locally? Sign up on polis.pub. Don't want polis.pub? Self-host. Don't trust the canonical DS? Run your own. Don't like the infinity stream? Use the blog shape. Don't like any shipped theme? Write one. **Every layer of the stack is something you can replace without losing the others.**

This doc is the map of those layers.

---

## Architectural inspiration: the net routes around damage

John Gilmore's 1993 line — *"The Net interprets censorship as damage and routes around it"* — is the architectural lineage polis claims.

The internet's resilience under hostile conditions has never come from any single layer being uncensorable; it has come from *every layer having an alternative*. When a route closes, traffic finds another. When a registrar goes hostile, names resolve through another. When a host disappears, content is mirrored elsewhere. The system survives because no single point holds the whole.

Polis aspires to the same property at a different layer of the stack — the social-networking layer. If polis.pub disappears, sites continue. If the canonical DS becomes hostile or unreachable, anyone can run their own. If a binary gets sabotaged, the open codebase can be rebuilt. If a content type or a theme gets challenged, it ships in a community bundle and any tenant can install it. If a policy regime turns oppressive on one DS, sites point at another. Every layer in the map below has a route around it — and the architecture is shaped to *permit* those routes rather than to wait until they're needed.

This is an aspiration, not a certification. Some routes are well-trodden today (alternative CLIs, custom themes, static-only hosting). Some are theoretically replaceable but not yet exercised at scale (a community-run DS competing with `ds.polis.pub`, third-party bundles with novel content types in production). The architecture is committed; the population of routes still has to be built. The table below is the current state of the map — what's a default, what's an alternative, and what stays the same regardless.

The point isn't that polis *will* be censored. The point is that polis is designed so that *if* any layer is censored, blocked, abandoned, or simply becomes unfit for purpose, the network routes around it. That property only exists if the layers are honest about their interfaces — which is what the rest of this doc enumerates.

---

## The replaceable-layers table

The thirteen layers below, top-to-bottom. Each row is a sentence: *the default*, *what you can replace it with*, and *why you keep what you keep when you swap*.

| Layer | Default | Why you'd replace it | How you replace it | What you keep |
|---|---|---|---|---|
| **CLI binary** | Go CLI (`cli-go/`), with Bash CLI (`cli-bash/`) as feature-frozen legacy. | Different language preference; need to embed polis in another tool; want a TUI/GUI/IDE plugin instead of a terminal. | Build any tool that reads/writes the on-disk format under `.polis/` and `content/`. The Go packages in `cli-go/pkg/` are importable; the Bash script is one file. | Same `.polis/` layout, same signing, same DS-registration semantics. Other tools (webapp, polis.pub, third-party clients) work unchanged. |
| **Authoring UI** | Webapp editor (`webapp/internal/webui/www/`) + CLI commands. | Want a VS Code extension, an iOS app, a Markdown-from-Notion importer, a batch publisher. | Produce signed markdown at the canonical path. Sign locally with the site's Ed25519 key (or via a hardware token or signing service). | DS registration, blessing flows, rendering, every downstream system. |
| **Local storage** | `.polis/` + `content/` directories on the operator's machine. | S3 backing for archival sites; IPFS/git-annex for content addressing; compressed snapshots for long-tail. | Keep the directory *convention*; back it with whatever substrate exposes the same path tree over HTTPS. | URL convention (`/posts/YYYYMMDD/slug.md`) is married to the directory convention. Change one, remap the other. |
| **Identity / keys** | Local Ed25519 keypair under `.polis/keys/`. | Hardware-backed signing (YubiKey, Nitrokey, cloud KMS); multi-party signing for team-run sites; key rotation regimes. | Move the private key anywhere a signing operation can run (HSM, KMS, SSH agent). Sign over the same canonical bytes. Public key still lives at `.well-known/polis`. | The signature *output* — Ed25519 over canonical content — is the protocol. The key's *home* is your choice. |
| **Web UI** | Webapp (`webapp/`) — Go server + SPA + widget. | Headless integrators; alt frontends (mobile native, browser extension, terminal-friendly client); read-only mirrors. | Build against the v1 REST API ([`/v1/content/{type}`](../../api/developer/reference.md)) or directly off the signed file format on disk. The widget is one optional path; static HTML is always available. | The signed-content contract + the v1 API contract. The webapp is one consumer of both. |
| **Rendering shape** | The bundle's shapes — `pub.polis.shapes.v3` (blog) and `pub.polis.shapes.v4` (infinity stream). | Want a gallery shape, a newsletter shape, a plain-HTML archive, a podcast feed shape, a custom-content-type shape. | Author a shape in your bundle: HTML templates + optional client JS + a `shapes.<name>` declaration in `bundle.json`. Themes declare which shapes they're compatible with. Tenants pick `active_shape` in `registry.json`. See [shapes.md](shapes.md). | Content is the source of truth; the shape only decides presentation. Switching shapes is a registry edit + re-render. |
| **Theme** | Shipped CSS themes (`vice`, `studio13`, `studio13-nk`, `especial`, `especial-light`, `turbo`, `zane`). `sols` is system-reserved. | You want a different palette; you want structural overrides (per-template HTML); you want to ship a community theme bundle. | Drop a `<name>.css` into your bundle's `themes/<name>/`. Provide every variable in the [theme contract](themes.md#css-variable-contract). Declare `compatible_shapes`. See [themes.md](themes.md). | The CSS variable contract is the only commitment. Themes are independent; shapes are independent; mixing is supported. |
| **Bundle** | `pub.polis.core` — the only bundle shipping today. | You want new content types, new shapes, new themes, new actions — anything beyond what the core bundle defines. | Ship a `bundle.json` under a new namespace (`pub.<yourname>.<bundle>`). Declare content types, handlers (builtin / executable / http), shapes, themes. See [bundles.md](bundles.md). | The dispatch engine is bundle-agnostic. New bundles get REST routes, event broadcasting, and policy governance for free. |
| **Hosting** | polis.pub (managed multi-tenant) for default users; localhost (the webapp on your machine) for development; nothing at all for static-only archival sites. | Want enterprise-managed; want bare-metal; want regional sovereignty; want air-gapped; want PWA / static-only. | Pick any combination: static-host the rendered HTML (GitHub Pages, Netlify, CDN); run `polis-server` on your own Fly/AWS/on-prem; sign up on polis.pub; or any hybrid (keys local, deploy remote). | Static content URLs over HTTPS + CORS — the only requirement. Subdomain routing, TLS, backups, scaling: all operator's choice. |
| **Discovery Service** | `ds.polis.pub` — managed Postgres + Hono. | Regional jurisdiction (EU-only DS); private/enterprise DS for closed networks; community-curated DS (only sites in your scene); a minimal reference DS for tests. | Implement the DS HTTP API ([`docs/ds/developer/api-reference.md`](../../ds/developer/api-reference.md)) over any storage backend ([`StorageAdapter`](../../ds/developer/storage-adapter.md) is the interface). Point sites at your DS via `DISCOVERY_SERVICE_URL`. | The DS HTTP contract is the federation layer. A site can register with multiple DSes; readers can query multiple DSes. |
| **Policy** | Default tenant policies (`cli-go/pkg/policy/policy.go`) + DS operator seed rules (`discovery-service/schema/postgres.sql`). | Want stricter inbound policies (auto-deny from non-followed); want a community moderation regime; want time-of-day rate limits; want an external policy engine plugged in. | Edit `policies/rules.jsonl` (public) or `.polis/policies/rules.jsonl` (private) with [v2 grammar](../reference/policy-grammar.md) rules. For DS-level policies, use `/v1/admin/policies`. | Same v2 grammar applies at every layer (Layer 1 inbound, Layer 2 outbound, Layer 3 DS operator). The evaluator is one piece of code. |
| **Background actors** | Patrol, Medic, Judge, Clerk, Chaplain, Reaper, Rosie (hosted), plus Tailor (self-hosted). | Self-hosters can simply not run them; enterprises might layer their own monitoring; alternate remediation policies (quarantine instead of auto-delete). | Don't run them, or import the packages (`cli-go/pkg/{patrol,medic,judge,rosie,...}`) into your own runtime. Tailor is a manual invocation. | The actors are *library code* with thin runtime wrappers. Their checks are importable; the "always run on a timer" is the polis.pub-specific choice. See [`actors.md`](actors.md). |
| **Reader / client** | Webapp SPA + widget for browser readers; CLI commands (`polis discover`, `polis notifications`) for terminal users. | TUI / mobile app / browser extension / agent client / archive reader / bot client. | Build any client that fetches signed markdown over HTTPS, verifies Ed25519, queries a DS, and presents content. | The signed-content + DS-API contracts. Every other UI choice is yours. |

A reader who absorbs this table understands polis architecturally.

---

## What can't snap off — the fixed protocol surface

Not everything is replaceable. A small core is **load-bearing for interop**. Replace anything in this list and you're no longer building polis — you might be building something good, but it won't federate with the rest of the network.

| Fixed element | What it is | Why it can't snap off |
|---|---|---|
| **Ed25519 signatures over canonical content** | Every published artifact carries an SSH-format Ed25519 signature over deterministically canonicalized bytes. | Swap the scheme → nobody else can verify your content → you've forked the trust graph. |
| **`.well-known/polis` as identity anchor** | A JSON file at a fixed path per site, declaring the site's public key + author metadata + bundle list. | All remote verification starts here. Change the path → other polis sites can't find you. |
| **Markdown + frontmatter as canonical artifact** | Posts are markdown files with a defined frontmatter shape (canonical_url, version, signature, etc.) at `content/pub.polis.core/post/YYYYMMDD/slug.md`. | The signature covers canonicalized-markdown-plus-frontmatter. Change the format → existing signatures stop verifying. |
| **Bundle namespace convention** | Bundles use `pub.<namespace>.<name>`; reserved substrings `.themes.` and `.shapes.` are parser pivots. See [bundles.md](bundles.md). | Naming determines how content flows between tenants and how the dispatch engine routes. |
| **Discovery Service HTTP API** | The endpoints documented in [`api-reference.md`](../../ds/developer/api-reference.md). Any DS must speak this contract to participate in the network. | A site pointing at a custom DS expects this API. Custom storage backend is fine; custom API is non-interop. |
| **Core content type minimums** | `pub.polis.post`, `pub.polis.comment`, `pub.polis.follow`, etc. each have minimum-required frontmatter fields (author, published, signature). Bundles may *extend* but not *remove* these. | Readers + DS decoders expect these fields. Remove them → content stops flowing across tenants. |

Everything else in the table above sits *on top of* this surface. The fixed surface is small on purpose — the more layers we can make replaceable, the more polis the protocol survives any individual implementation of polis the system.

---

## Why this design

Three structural reasons, all about durability:

### 1. Decentralization that survives the implementer

If polis.pub shut down tomorrow, the protocol doesn't die. Every author's markdown is on their own domain, signed with their own key, indexable by any DS. Readers who know a few author domains can find content directly. A new polis.pub-equivalent can be stood up by anyone.

The alternative — a protocol where polis.pub's database, signing infra, or webapp is *required* for content to be discoverable or verifiable — would be a centralized service wearing a federated costume. Polis is shaped so the canonical artifact at the author's own domain is always sufficient.

### 2. Experimentation without gatekeeping

Someone who wants a different reading experience — a power-user TUI, a mobile app, a browser extension that overlays polis comments on arbitrary pages — can build it against the same wire format. They don't need polis.pub's permission, don't negotiate an API key, don't reverse-engineer anything closed. As long as they fetch signed markdown over HTTPS and verify Ed25519 signatures, they're a polis client.

Same for alternate shapes, alternate bundles with new content types, alternate background actors. Anywhere there's a clean contract, that contract is open.

### 3. Lock-in avoidance for authors

An author who hosts on polis.pub today can migrate to self-hosting tomorrow — and vice versa — without changing identity, content, signatures, or audience. `.well-known/polis` moves domains, DNS updates, the DS re-indexes, nothing breaks. This matters practically (operator flexibility) and as a trust signal (authors aren't handing away ownership of their presence when they pick a host).

---

## How to fork well

The architecture *permits* forking. A few practices make it *durable*:

### Stay honest about your protocol version

Every polis artifact carries a `generator` field (`polis-cli-go/0.63.0`, `my-custom-cli/1.0.0`). Don't pretend to be polis.pub's CLI; be yourself. Version-bump your generator string as you evolve. Makes debugging across implementations humane.

### Replace one layer at a time

The cost of replacing layer N is proportional to how tightly it's coupled to N−1 and N+1. A first pass — "I want a different editor" or "I want a different theme" — stays close to the polis.pub defaults in all other layers. A more ambitious fork (custom DS + custom hosting + custom client) is a larger commitment. Sequence your work so each step is independently testable.

### Publish your namespace

If you're shipping a bundle, a theme, a shape, or a content type, claim a namespace (`pub.yourname.*`) and publish what you declare. Other polis sites that want to use your work can then reference it precisely. The bundle namespace is how the protocol stays open without collisions.

### Document your contract divergences

If you run a custom DS that federates to polis.pub but carries extensions your tenants rely on, write that down publicly. A good rule: an interop partner should be able to read your docs and decide in <15 minutes whether to trust your system.

### Don't silently change the fixed protocol surface

Any change to Ed25519-over-canonical-content, `.well-known/polis`, frontmatter shape, bundle namespace, DS API, or core content-type minimums forks the protocol. That's legitimate if you're building a distinct system, but call it out. Don't claim polis-compatibility when you aren't.

---

## Thought experiment: polis.pub goes dark

Worth tracing through, because the answer reveals the architecture.

**Day 1.** polis.pub's hosting goes offline. `handle.polis.pub` subdomains stop resolving. `ds.polis.pub` API returns 5xx.

**What still works.**
- Self-hosted polis sites continue publishing. Their `.well-known/polis` is reachable; their content is signed; their URLs resolve.
- Readers who have cached follower manifests can keep browsing what they have. Lazy body-fetch still works direct-to-origin; only DS-queried "what's new across the network" is broken.
- Archives of polis.pub tenants' published content remain signed + verifiable. Anyone with a `polis clone` snapshot can render + read it.

**What breaks.**
- New site registrations on polis.pub — nowhere to register, nothing's listening.
- Cross-tenant discovery (finding content you don't already follow) — the canonical DS is the index.
- polis.pub-hosted tenants (the `handle.polis.pub` subdomain tree) — DNS + webapp both offline.

**Day 7 recovery.**
- A community fork stands up `polisdns.net/ds` as a new DS. Sites register there; the site graph rebuilds. Initial bootstrapping is manual (known-site imports) until the graph saturates.
- polis.pub-hosted tenants bring data back online at new domains (migrations some had prepared; others reconstruct from local backups or `polis clone` snapshots kept by followers).
- The webapp + CLI binaries are reproduced from a fork of the open codebase. Alternate implementers step in where the monoculture retreats.
- The protocol itself didn't need a version bump — the fixed surface held, and the replaceable layers got swapped.

This isn't resilience fantasy; it's what the architecture is shaped for. The key insight: **polis.pub is not a database, not a certificate authority, not an identity provider. It's one implementation.** The durable state — content, keys, relationships — lives with the authors and their domains. The ephemeral state — indexing, coordination, presentation — is what snaps off.

---

## What this document isn't

- **A complete API reference.** Specific protocol contracts (DS API, bundle format, signing canonicalization) live in their own docs. This doc is the *architectural stance* that shapes those contracts.
- **A tutorial.** If you want to actually run a custom DS or ship a custom theme, the implementation guides (in [`ds/admin/`](../../ds/admin/), [`themes.md`](themes.md), [`bundles.md`](bundles.md)) cover the how.
- **A guarantee.** Snap-off is a design intent, not a certification. Replacing a layer means doing the work to meet the contract — this doc gives you the map, not the passport.

---

## See also

- [architecture.md](architecture.md) — The four surfaces (CLI, webapp, polis.pub, DS) snap-off applies to.
- [bundles.md](bundles.md), [content-types.md](content-types.md), [shapes.md](shapes.md), [themes.md](themes.md) — The concrete primitives that get swapped.
- [security-model.md](../security/security-model.md) — Why the *fixed* protocol surface is fixed (signing, trust chain).
- [policy-grammar.md](../reference/policy-grammar.md) — The grammar that survives layer swaps.
- [content-system.md](content-system.md) — Deep reference for the bundle/content-type model.
- [actors.md](actors.md) — The background actors you can choose to run, or not.
- [ds/developer/api-reference.md](../../ds/developer/api-reference.md) — The DS contract you'd implement to replace the canonical DS.
- [vision.md](../vision.md) — The "why polis exists" framing, in philosophical rather than architectural terms.
