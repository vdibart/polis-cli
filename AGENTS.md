# AGENTS.md — A handbook for humans and LLMs exploring polis

> If you arrived here from a "Pull the thread" comment in a polis source file, this is the map. If you're an LLM helping a human build on polis, **read this file first.** It points to everything else.

## Polis, in one paragraph

Polis is a decentralized social-networking protocol. Sites publish signed markdown on their own domains; the **Discovery Service (DS)** coordinates blessing, follow, and discovery; the **webapp** is a Go server that runs locally, on your own host, or as part of `polis.pub` for managed hosting, and serves the site's owner app (the v5 app, at `/_/`) and its one API, `/v1`; the **CLI** owns the canonical file format, and its packages (`cli-go/pkg/`) are the source of truth for business logic: every site action has one implementation, in `cli-go/pkg/ops`, which the owner app, agents, scripts and the CLI all reach. The protocol's *fixed* surface is small (Ed25519 over canonical content, `.well-known/polis`, the bundle namespace, the DS API, core content-type schemas); every other layer is replaceable. Read [`docs/general/concepts/architecture.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/concepts/architecture.md) for the four-surface map before going deeper.

## How this handbook works

The publicly-served source code of polis.pub is part of the documentation. When you view-source on `polis.pub` or `<handle>.polis.pub`, the JavaScript, HTML, CSS, and rendered markup you see carry **trail markers** — header comments naming what each file implements and pointing to relevant docs.

Three layers of instrumentation, calibrated per thread:

1. **Trail markers in source.** Header comments in files served to browsers. Cite related files and concept docs by GitHub path. Always present; minimum viable trail.
2. **Guided tours (when a thread spans many files).** Curated walk-through `.md` files under [`docs/handbook/`](https://github.com/vdibart/polis-cli/blob/main/docs/handbook/) that thread an observation → mechanism → architecture path through the codebase. Optional escalation when comments alone can't tell the story.
3. **Concept docs.** The philosophical destinations — what the threads lead *to*. Live in [`docs/general/`](https://github.com/vdibart/polis-cli/blob/main/docs/general/). Stable, citable, the answer-of-record for each named concept.

A reader who pulls a thread starts at code, hops through marker → related file → tour (sometimes) → concept doc. They exit understanding polis the philosophy without ever having intended to read docs.

> **Where to start today.** Every site's owner app is the v5 app (`/_/` on any site), and a new site's public pages are the v5 shape: cards under the site's own menu. Start at the owner app's page ([`webapp/internal/webui/www/v5/index.html`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/webui/www/v5/index.html)) or at view-source on any page of a v5 site, and at [`docs/general/concepts/shapes.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/concepts/shapes.md). The stream tours ([`stream-overview.md`](https://github.com/vdibart/polis-cli/blob/main/docs/handbook/stream-overview.md) and its spokes) are the record of the v4 stream screen and owner app, which v5 replaced on 2026-11-22; the mechanisms they follow below the page (PQL, the sync from the discovery service) are still the ones in use.

## The threads

The starter slate. Each entry: what observation starts the thread, which files carry it, and whether a guided tour exists.

| Thread | Starts at | Marker files | Tour |
|---|---|---|---|
| **Owner app** | `/_/` on any site: the four sections (gather, write, message, enhance), one editor, every list a PQL sentence, and only `/v1` behind it | [`v5/index.html`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/webui/www/v5/index.html), [`api.js`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/webui/www/v5/api.js), [`frame.js`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/webui/www/v5/frame.js), [`v5owner.go`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/server/v5owner.go), [`cli-go/pkg/ops/`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/ops/) (every site action's one implementation) | (no tour yet; the API it calls: [`docs/api/developer/reference.md`](https://github.com/vdibart/polis-cli/blob/main/docs/api/developer/reference.md)) |
| **Public site (v5 shape)** | View-source on any page of a v5 site: a kept `<!--!` comment at the top, then `/styles.css` and `/v5.js` | [`frame.html`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/bundle/fixtures/pub.polis.core/shapes/v5/frame.html), [`v5.js`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/bundle/fixtures/pub.polis.core/shapes/v5/v5.js), [`render/v5.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/render/v5.go) | (no tour yet; the concept: [`docs/general/concepts/shapes.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/concepts/shapes.md)) |
| **URL-as-filter** | The address changes as you open a view or scroll, and `/pql/<sentence>` is both a page and, asked as JSON, its items | [`v5.js`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/bundle/fixtures/pub.polis.core/shapes/v5/v5.js) (the public site), [`frame.js`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/webui/www/v5/frame.js) (the owner app's `/_/<section>/pql/<sentence>`), [`handlers_pql.go`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/server/handlers_pql.go); on a site still on the v4 shape, [`stream.js`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/bundle/fixtures/pub.polis.core/shapes/v4/stream.js) | [`docs/handbook/url-as-filter.md`](https://github.com/vdibart/polis-cli/blob/main/docs/handbook/url-as-filter.md) (the record of the v4 app) |
| **PQL (one grammar, two parsers)** | Same filter grammar in Go and TS; both assert one golden corpus so it can't drift. `/pql/<sentence>` is a data + public endpoint on the site and on the DS; `/v1/query` reaches the site's | [`cli-go/pkg/pql/pql.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/pql/pql.go), [`handlers_pql.go`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/server/handlers_pql.go), the DS's `core/pql.ts` (not in this repository) — canonical [`pql-vocabulary.json`](https://github.com/vdibart/polis-cli/blob/main/docs/general/reference/pql-vocabulary.json) + [`pql-golden.jsonl`](https://github.com/vdibart/polis-cli/blob/main/docs/general/reference/pql-golden.jsonl) | [`docs/general/reference/pql.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/reference/pql.md), [`docs/ds/developer/pql-json-api.md`](https://github.com/vdibart/polis-cli/blob/main/docs/ds/developer/pql-json-api.md) |
| **DS to stream** | New items from sites you follow appear in gather and your network's views; cross-tenant comment counts decorate them | [`sync.go`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/server/sync.go), [`feed/handler.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/feed/handler.go), [`stream/store.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/stream/store.go), [`handlers_stream.go`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/server/handlers_stream.go); the DS's `stream.ts`, `counts.ts` and `latest.ts` (not in this repository) | [`docs/handbook/ds-to-stream.md`](https://github.com/vdibart/polis-cli/blob/main/docs/handbook/ds-to-stream.md) (the record of the v4 stream) |
| **Identity anchor** | `.well-known/polis` on any site, or a signed post's frontmatter | `cli-go/pkg/site/` (`.well-known/polis`, key history, `did.json`) | (no marker or tour yet) |
| **Blessing flow** | A comment from `bob@elsewhere` on a post on `alice.polis.pub`, and whether Alice has blessed it | `cli-go/pkg/blessing/`, the comment card in the active shape (the DS side is not in this repository; its contract is [`docs/ds/developer/api-reference.md`](https://github.com/vdibart/polis-cli/blob/main/docs/ds/developer/api-reference.md)) | (no marker or tour yet) |
| **Shape assets** | `/styles.css` and `/v5.js` in a v5 page's source (`/stream.js` on a v4 site) | `cli-go/pkg/bundle/` (the embedded fixture under `cli-go/pkg/bundle/fixtures/pub.polis.core/`, installed in each site's `.polis/bundles/`), `cli-go/pkg/theme/loader.go` (`writeStyles`, `CopyV5Controller`: written to the site's root at render) | (no marker or tour yet) |
| **Foreign-site widget** | Signed in to polis.pub, your way home (your mark, lower left) on someone else's polis.pub site; the Follow and reply widget on every page | `nav_inject.go`, `nav.js`, `widget.js`, `widget_embed.go`, [`page.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/render/page.go) `WidgetVersion` | [`docs/handbook/foreign-site-widget.md`](https://github.com/vdibart/polis-cli/blob/main/docs/handbook/foreign-site-widget.md) |
| **DM encryption** | You set a password and your own server can still deliver and store your messages but can no longer read them; a message reaches the recipient with no central broker | [`keyring.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/dm/keyring.go), [`epoch.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/dm/epoch.go), [`send.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/dm/send.go), [`receive.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/dm/receive.go), [`mailbox.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/dm/mailbox.go), [`dm.js`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/webui/www/dm.js), [`router.go`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/api/router.go) | [`docs/handbook/dm-encryption.md`](https://github.com/vdibart/polis-cli/blob/main/docs/handbook/dm-encryption.md) |
| **Agent access** | A post an agent wrote carries `agent:` and `grant:` in its signed frontmatter; switching the connection off refuses the next request | [`mcpgen.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/mcpgen/mcpgen.go), [`router.go`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/api/router.go), [`grantctx.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/ops/grantctx.go), [`connection.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/connection/connection.go), [`publish.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/publish/publish.go), `agentauth` (polis.pub's sign-in server) | [`docs/handbook/agent-access.md`](https://github.com/vdibart/polis-cli/blob/main/docs/handbook/agent-access.md) |

Threads with "no tour yet" have header comments in the named files but no curated MD walkthrough; threads with "no marker or tour yet" have neither, and start at the files named. They become tours when a thread is requested often enough that the trail markers leave readers wanting more.

## Recipes — "if you want to build X"

Concrete file-by-file paths for common builds. Each recipe is a sequence of files (with concept docs as context) — follow it in order.

### Set up a polis site locally

1. Run `polis init` ([`cli-go/pkg/cmd/init.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/cmd/init.go))
2. Understand what got written: [`docs/general/concepts/content-system.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/concepts/content-system.md) (filesystem layout)
3. Pick a theme: [`docs/general/concepts/themes.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/concepts/themes.md)
4. Publish your first post: `polis post foo.md` ([`cli-go/pkg/cmd/publish.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/cmd/publish.go))
5. Register with the DS: `polis register` ([`cli-go/pkg/cmd/register.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/cmd/register.go))
6. Run the webapp: `polis serve` ([`webapp/cmd/polis-full/main.go`](https://github.com/vdibart/polis-cli/blob/main/webapp/cmd/polis-full/main.go))

### Build a custom content type

1. Understand content types: [`docs/general/concepts/content-types.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/concepts/content-types.md)
2. Understand bundles (the container): [`docs/general/concepts/bundles.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/concepts/bundles.md)
3. See how a core type is wired: [`cli-go/pkg/ops/builtin_core.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/ops/builtin_core.go) (the dispatch engine)
4. Read the dispatch engine architecture: [`docs/api/developer/dispatch-engine.md`](https://github.com/vdibart/polis-cli/blob/main/docs/api/developer/dispatch-engine.md)
5. Author a new bundle's `bundle.json` declaring your type + a handler (`builtin` / `executable` / `http`)
6. If your type produces public content, design its shape templates: [`docs/general/concepts/shapes.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/concepts/shapes.md)
7. If it emits events, declare them; readers can subscribe via the DS event stream

### Work with a Discovery Service

The Discovery Service's source is not in this repository (it is closed for now), so this is a recipe for building against one,
not for deploying one.

1. Understand what the DS does: [`docs/general/concepts/architecture.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/concepts/architecture.md) (Discovery Service section) + [`docs/ds/README.md`](https://github.com/vdibart/polis-cli/blob/main/docs/ds/README.md)
2. Read the API contract, the stable public surface: [`docs/ds/developer/api-reference.md`](https://github.com/vdibart/polis-cli/blob/main/docs/ds/developer/api-reference.md)
3. Understand what registering tells it, and what leaving removes: [`docs/general/security/registration-and-privacy.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/security/registration-and-privacy.md)
4. See what a deployment consists of, and what an operator can tune: [`docs/ds/admin/deployment.md`](https://github.com/vdibart/polis-cli/blob/main/docs/ds/admin/deployment.md) · [`docs/ds/admin/configuration.md`](https://github.com/vdibart/polis-cli/blob/main/docs/ds/admin/configuration.md)
5. Point your sites at a DS via `DISCOVERY_SERVICE_URL`

### Write a polis client in another language

1. Read the protocol's fixed surface: [`docs/general/concepts/snap-off-architecture.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/concepts/snap-off-architecture.md) (the "fixed protocol surface" section)
2. Understand signing: [`docs/general/security/security-model.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/security/security-model.md) (Ed25519 over canonical content)
3. Understand the file format: [`docs/general/concepts/content-system.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/concepts/content-system.md)
4. Reference the Go implementation: [`cli-go/pkg/signing/`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/signing/) (canonicalization), [`cli-go/pkg/publish/`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/publish/) (publish flow)
5. Reference the bash implementation for an alternate take: the `polis` script in [`cli-bash/`](https://github.com/vdibart/polis-cli/tree/main/cli-bash)
6. Stamp your client's generator string into every artifact you produce (e.g., `my-polis-client/0.1.0`)

### Snap off any layer

1. Read [`docs/general/concepts/snap-off-architecture.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/concepts/snap-off-architecture.md) (the layer-by-layer replaceability table)
2. Identify which layer you're replacing
3. The doc names the public contract for each layer — that's what you build against
4. Stay honest about your protocol version (`generator` field) and namespace (`pub.<yourname>.*`)

## How an LLM should use this file

You are most useful to a human exploring polis when you:

1. **Start them at observation.** If they ask "why does X happen on polis.pub," find the thread in the table above that matches X. Send them to the first marker file. Don't dump the whole architecture on them.
2. **Pull threads with them, not for them.** The trail markers are designed for incremental discovery. Resist the urge to summarize five files at once. Read one, surface the next pointer, let them follow.
3. **Cite by GitHub path.** Every doc and code file referenced in this handbook uses `github.com/vdibart/polis-cli/blob/main/...` paths. When you cite, match the form so the human can click.
4. **Reach for the concept docs when "why" comes up.** The trail markers explain *what* a file does; the concept docs in [`docs/general/`](https://github.com/vdibart/polis-cli/blob/main/docs/general/) explain *why* polis is shaped that way.
5. **Use recipes for "I want to build X."** Don't reconstruct paths from scratch. The recipes above are curated reading orders.
6. **Flag drift if you find it.** A trail marker that points at a file that's been moved or renamed is a bug. Surface it.

## Doc map (where everything lives)

The single index. Everything else points back here.

**[`docs/general/`](https://github.com/vdibart/polis-cli/blob/main/docs/general/) — concept docs (the philosophy)**
- [`architecture.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/concepts/architecture.md) — the four surfaces
- [`bundles.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/concepts/bundles.md), [`content-types.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/concepts/content-types.md), [`shapes.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/concepts/shapes.md), [`themes.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/concepts/themes.md) — foundation concepts
- [`infinity-stream.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/concepts/infinity-stream.md) — the v4 stream, polis.pub's model until v5 (kept as the record)
- [`snap-off-architecture.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/concepts/snap-off-architecture.md) — layer-by-layer replaceability
- [`pql.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/reference/pql.md) — the query language behind every view: the owner app's lists, the public site's views, `/v1/query`
- [`security-model.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/security/security-model.md) — crypto, identity, trust, threats
- [`policy-grammar.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/reference/policy-grammar.md) — v2 policy grammar (Layer 1/2/3)
- [`content-system.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/concepts/content-system.md) — deep reference (full schemas, event catalog)
- [`glossary.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/reference/glossary.md) — terminology

**[`docs/handbook/`](https://github.com/vdibart/polis-cli/blob/main/docs/handbook/) — guided tours (curated walk-throughs)**
- [`stream-overview.md`](https://github.com/vdibart/polis-cli/blob/main/docs/handbook/stream-overview.md) — the record of the v4 stream screen, which v5 replaced: the meta-tour that ties URL-as-filter + DS-to-stream together with the sentence-filter widget and the infinity stream design
- [`url-as-filter.md`](https://github.com/vdibart/polis-cli/blob/main/docs/handbook/url-as-filter.md) — pulling the URL-as-filter thread, through the v4 app (the record)
- [`ds-to-stream.md`](https://github.com/vdibart/polis-cli/blob/main/docs/handbook/ds-to-stream.md) — how DS events reach your site; continuous sync + cross-tenant aggregation (written for the v4 stream, kept as the record; the sync it follows is current)
- [`foreign-site-widget.md`](https://github.com/vdibart/polis-cli/blob/main/docs/handbook/foreign-site-widget.md) — the injected nav widget on logged-in-visitor pages; the comment/follow widget on every page
- [`agent-access.md`](https://github.com/vdibart/polis-cli/blob/main/docs/handbook/agent-access.md) — how an agent's write is marked inside its signature, and refused once its connection is switched off
- [`dm-encryption.md`](https://github.com/vdibart/polis-cli/blob/main/docs/handbook/dm-encryption.md) — how a DM is encrypted, delivered server-to-server with no broker, stored as ciphertext its own server can't read, and unlocked in the browser; the epoch keyring and the bootstrap window

**[`docs/cli/`](https://github.com/vdibart/polis-cli/blob/main/docs/cli/), [`docs/webapp/`](https://github.com/vdibart/polis-cli/blob/main/docs/webapp/), [`docs/api/`](https://github.com/vdibart/polis-cli/blob/main/docs/api/), [`docs/ds/`](https://github.com/vdibart/polis-cli/blob/main/docs/ds/) — per-component docs (user / developer / designer / admin)**

## See also

- [`README.md`](https://github.com/vdibart/polis-cli/blob/main/README.md) — the conventional repo entry (high-level intro for browsers landing on the GitHub page)
- [`docs/general/contributing.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/contributing.md) — build commands, repository layout and conventions; complementary to this file
- [`docs/webapp/developer/development.md`](https://github.com/vdibart/polis-cli/blob/main/docs/webapp/developer/development.md) — webapp-specific development guide
