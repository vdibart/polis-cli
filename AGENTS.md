# AGENTS.md — A handbook for humans and LLMs exploring polis

> If you arrived here from a "Pull the thread" comment in a polis source file, this is the map. If you're an LLM helping a human build on polis, **read this file first.** It points to everything else.

## Polis, in one paragraph

Polis is a decentralized social-networking protocol. Sites publish signed markdown on their own domains; the **Discovery Service (DS)** coordinates blessing, follow, and discovery; the **webapp** is a Go SPA that runs locally or as part of `polis.pub` for managed hosting; the **CLI** owns the canonical file format and is the source of truth for business logic. The protocol's *fixed* surface is small (Ed25519 over canonical content, `.well-known/polis`, the bundle namespace, the DS API, core content-type schemas); every other layer is replaceable. Read [`docs/general/architecture.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/architecture.md) for the four-surface map before going deeper.

> **Scope of this repo.** The CLI, webapp, themes, bundles, and shared docs are open-source here. The Discovery Service source code is not yet included; integrators should treat [`docs/ds/developer/api-reference.md`](https://github.com/vdibart/polis-cli/blob/main/docs/ds/developer/api-reference.md) as the canonical DS surface. The hosted operational toolchain (judge, patrol, medic, etc.) is also out of scope; `tailor` is the only operational binary shipped publicly.

## How this handbook works

The publicly-served source code of polis.pub is part of the documentation. When you view-source on `polis.pub` or `<handle>.polis.pub`, the JavaScript, HTML, CSS, and rendered markup you see carry **trail markers** — header comments naming what each file implements and pointing to relevant docs.

Three layers of instrumentation, calibrated per thread:

1. **Trail markers in source.** Header comments in files served to browsers. Cite related files and concept docs by GitHub path. Always present; minimum viable trail.
2. **Guided tours (when a thread spans many files).** Curated walk-through `.md` files under [`docs/handbook/`](https://github.com/vdibart/polis-cli/blob/main/docs/handbook/) that thread an observation → mechanism → architecture path through the codebase. Optional escalation when comments alone can't tell the story.
3. **Concept docs.** The philosophical destinations — what the threads lead *to*. Live in [`docs/general/`](https://github.com/vdibart/polis-cli/blob/main/docs/general/). Stable, citable, the answer-of-record for each named concept.

A reader who pulls a thread starts at code, hops through marker → related file → tour (sometimes) → concept doc. They exit understanding polis the philosophy without ever having intended to read docs.

> **Want the synthesis view of how the polis.pub stream-screen actually works?** Start at the overview tour: [`docs/handbook/stream-overview.md`](https://github.com/vdibart/polis-cli/blob/main/docs/handbook/stream-overview.md). It ties URL-as-filter and DS-to-stream together, explains the sentence-filter widget, summarizes the infinity stream's design, and points down into the deep tours and out to the PQL + DS docs. The deep tours are spokes; the overview is the hub.

## The threads

The starter slate. Each entry: what observation starts the thread, which files carry it, and whether a guided tour exists.

| Thread | Starts at | Marker files | Tour |
|---|---|---|---|
| **URL-as-filter** | URL changes when you click an icon or scroll | [`app.js`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/webui/www/app.js), [`pql.js`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/webui/www/pql.js), [`stream.js`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/bundle/fixtures/pub.polis.core/shapes/v4/stream.js) | [`docs/handbook/url-as-filter.md`](https://github.com/vdibart/polis-cli/blob/main/docs/handbook/url-as-filter.md) |
| **DS to stream** | New items from sites you don't host appear in your stream; cross-tenant comment counts decorate them | [`sync.go`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/server/sync.go), [`feed/handler.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/feed/handler.go), [`stream/store.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/stream/store.go), [`handlers_stream.go`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/server/handlers_stream.go) (DS-side handlers are not in this repo — see [`docs/ds/developer/api-reference.md`](https://github.com/vdibart/polis-cli/blob/main/docs/ds/developer/api-reference.md) for the API contract) | [`docs/handbook/ds-to-stream.md`](https://github.com/vdibart/polis-cli/blob/main/docs/handbook/ds-to-stream.md) |
| **Identity anchor** | `.well-known/polis` on any site, or a signed post's frontmatter | `cli-go/pkg/site/`, well-known writers in `webapp/internal/server/` | (marker only) |
| **Blessing flow** | A green check next to a comment from `bob@elsewhere` on `alice.polis.pub` | `cli-go/pkg/blessing/`, the comment template in the active shape (DS-side relationship handlers are not in this repo — see [`docs/ds/developer/api-reference.md`](https://github.com/vdibart/polis-cli/blob/main/docs/ds/developer/api-reference.md)) | (marker only) |
| **Bundle assets** | `/bundle-assets/pub.polis.core/shapes/v4/stream.css` in any page source | `cli-go/pkg/bundle/`, `webapp/internal/server/` bundle handler, the embedded fixture under `cli-go/pkg/bundle/fixtures/` | (marker only) |
| **Foreign-site widget** | The icon nav that appears (and autohides) on `alice.polis.pub` when you're logged in; the comment/follow widget that sits below posts | [`nav_inject.go`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/hosted/nav_inject.go), [`nav.js`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/hosted/nav/nav.js), [`widget.js`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/hosted/widget/widget.js), [`widget_embed.go`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/hosted/widget_embed.go), [`page.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/render/page.go) `WidgetVersion` | [`docs/handbook/foreign-site-widget.md`](https://github.com/vdibart/polis-cli/blob/main/docs/handbook/foreign-site-widget.md) |

Threads marked "marker only" today have header comments in the named files but no curated MD walkthrough. They become tours when a thread is requested often enough that the trail markers leave readers wanting more.

## Recipes — "if you want to build X"

Concrete file-by-file paths for common builds. Each recipe is a sequence of files (with concept docs as context) — follow it in order.

### Set up a polis site locally

1. Run `polis init` ([`cli-go/pkg/cmd/init.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/cmd/init.go))
2. Understand what got written: [`docs/general/content-system.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/content-system.md) (filesystem layout)
3. Pick a theme: [`docs/general/themes.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/themes.md)
4. Publish your first post: `polis post foo.md` ([`cli-go/pkg/cmd/publish.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/cmd/publish.go))
5. Register with the DS: `polis register` ([`cli-go/pkg/cmd/register.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/cmd/register.go))
6. Run the webapp: `polis serve` ([`webapp/cmd/polis-full/main.go`](https://github.com/vdibart/polis-cli/blob/main/webapp/cmd/polis-full/main.go))

### Build a custom content type

1. Understand content types: [`docs/general/content-types.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/content-types.md)
2. Understand bundles (the container): [`docs/general/bundles.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/bundles.md)
3. See how a core type is wired: [`cli-go/pkg/ops/builtin_core.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/ops/builtin_core.go) (the dispatch engine)
4. Read the dispatch engine architecture: [`docs/api/developer/dispatch-engine.md`](https://github.com/vdibart/polis-cli/blob/main/docs/api/developer/dispatch-engine.md)
5. Author a new bundle's `bundle.json` declaring your type + a handler (`builtin` / `executable` / `http`)
6. If your type produces public content, design its shape templates: [`docs/general/shapes.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/shapes.md)
7. If it emits events, declare them; readers can subscribe via the DS event stream

### Self-host a Discovery Service

1. Understand what the DS does: [`docs/general/architecture.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/architecture.md) (Discovery Service section) + [`docs/ds/README.md`](https://github.com/vdibart/polis-cli/blob/main/docs/ds/README.md)
2. Read the API contract: [`docs/ds/developer/api-reference.md`](https://github.com/vdibart/polis-cli/blob/main/docs/ds/developer/api-reference.md)
3. Understand the storage interface: [`docs/ds/developer/storage-adapter.md`](https://github.com/vdibart/polis-cli/blob/main/docs/ds/developer/storage-adapter.md)
4. Deploy the reference adapter: [`docs/ds/admin/deployment.md`](https://github.com/vdibart/polis-cli/blob/main/docs/ds/admin/deployment.md)
5. Tune it for your scale: [`docs/ds/admin/configuration.md`](https://github.com/vdibart/polis-cli/blob/main/docs/ds/admin/configuration.md)
6. Point your sites at it via `DISCOVERY_SERVICE_URL`

### Write a polis client in another language

1. Read the protocol's fixed surface: [`docs/general/snap-off-architecture.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/snap-off-architecture.md) (the "fixed protocol surface" section)
2. Understand signing: [`docs/general/security-model.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/security-model.md) (Ed25519 over canonical content)
3. Understand the file format: [`docs/general/content-system.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/content-system.md)
4. Reference the Go implementation: [`cli-go/pkg/signing/`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/signing/) (canonicalization), [`cli-go/pkg/publish/`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/publish/) (publish flow)
5. Reference the bash implementation for an alternate take: [`cli-bash/bin/polis`](https://github.com/vdibart/polis-cli/blob/main/cli-bash/bin/polis)
6. Stamp your client's generator string into every artifact you produce (e.g., `my-polis-client/0.1.0`)

### Snap off any layer

1. Read [`docs/general/snap-off-architecture.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/snap-off-architecture.md) (the layer-by-layer replaceability table)
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
- [`architecture.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/architecture.md) — the four surfaces
- [`bundles.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/bundles.md), [`content-types.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/content-types.md), [`shapes.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/shapes.md), [`themes.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/themes.md) — foundation concepts
- [`infinity-stream.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/infinity-stream.md) — the polis.pub experience
- [`snap-off-architecture.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/snap-off-architecture.md) — layer-by-layer replaceability
- [`pql.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/pql.md) — the query language driving the stream filter
- [`security-model.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/security-model.md) — crypto, identity, trust, threats
- [`policy-grammar.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/policy-grammar.md) — v2 policy grammar (Layer 1/2/3)
- [`content-system.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/content-system.md) — deep reference (full schemas, event catalog)
- [`glossary.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/glossary.md) — terminology

**[`docs/handbook/`](https://github.com/vdibart/polis-cli/blob/main/docs/handbook/) — guided tours (curated walk-throughs)**
- [`stream-overview.md`](https://github.com/vdibart/polis-cli/blob/main/docs/handbook/stream-overview.md) — **start here for the stream-screen.** Meta-tour that ties URL-as-filter + DS-to-stream together with the sentence-filter widget and the infinity stream design; spokes lead into the deep tours and out to PQL + DS docs
- [`url-as-filter.md`](https://github.com/vdibart/polis-cli/blob/main/docs/handbook/url-as-filter.md) — pulling the URL-as-filter thread
- [`ds-to-stream.md`](https://github.com/vdibart/polis-cli/blob/main/docs/handbook/ds-to-stream.md) — how DS events become the stream you see; continuous sync + cross-tenant aggregation
- [`foreign-site-widget.md`](https://github.com/vdibart/polis-cli/blob/main/docs/handbook/foreign-site-widget.md) — the injected nav widget on logged-in-visitor pages; the comment/follow widget on every page

**[`docs/cli/`](https://github.com/vdibart/polis-cli/blob/main/docs/cli/), [`docs/webapp/`](https://github.com/vdibart/polis-cli/blob/main/docs/webapp/), [`docs/api/`](https://github.com/vdibart/polis-cli/blob/main/docs/api/), [`docs/ds/`](https://github.com/vdibart/polis-cli/blob/main/docs/ds/) — per-component docs (user / developer / designer / admin)**

## See also

- [`README.md`](https://github.com/vdibart/polis-cli/blob/main/README.md) — the conventional repo entry (high-level intro for browsers landing on the GitHub page)
- [`CLAUDE.md`](https://github.com/vdibart/polis-cli/blob/main/CLAUDE.md) — Claude Code project config (build commands, env vars, codebase rules); complementary to this file
- [`webapp/CLAUDE.md`](https://github.com/vdibart/polis-cli/blob/main/webapp/CLAUDE.md) — webapp-specific development guide
