# Bundles

*For* [Developers](../../README.md#building-on-polis) — *About* [Content](../README.md#content) — *Kind* [Concept](../../README.md#kinds-of-page) — *See also* [reference](content-system.md) · no spec yet · no recipe yet

> Part of the foundation set: **bundles** (this doc), [content types](content-types.md), [shapes](shapes.md), [themes](themes.md). See [architecture.md](architecture.md) for the four-surface map these concepts live within. For the full reference (schemas, validation rules, event catalog), see [content-system.md](content-system.md).

A **bundle** is the package container that ships a coherent set of polis primitives — content types, shapes, themes — together as one versioned unit. It's the unit of extensibility: the difference between "what every polis site knows how to do" (the core bundle) and "what a community ships on top of polis" (third-party bundles) is one `bundle.json` declaration.

Today the network ships a single bundle: **`pub.polis.core`** — nine content types (post, comment, follow, DM, tag, attestation, actor registry, licence, theme), the blog and stream shapes, and the themes. Everything else listed in this doc is the architecture that *would* support more bundles, when they exist.

---

## What a bundle ships

A bundle is a namespaced unit that declares four kinds of things:

| Declared in `bundle.json` | What it is | Doc |
|---|---|---|
| **Content types** | The data model — what records the bundle understands (e.g. `pub.polis.post`, `pub.polis.dm`). Each declares its directory, public mount, storage layout, the events it emits, and whether it is private. | [content-types.md](content-types.md) |
| **Handler declaration** | How the bundle's actions get executed — one for the whole bundle: `builtin` (Go code in the CLI), `executable` (JSON stdin/stdout to a binary), or `http` (JSON POST to a URL). | [api/developer/dispatch-engine.md](../../api/developer/dispatch-engine.md) |
| **Shapes** | Rendering approaches — the templates that turn content into HTML. The core bundle ships `pub.polis.shapes.v3` (blog) and `pub.polis.shapes.v4` (infinity stream). | [shapes.md](shapes.md) |
| **Themes** | CSS-only presentation, scoped to the bundle and compatible with one or more of its shapes. | [themes.md](themes.md) |

The bundle is the *binding* between these — a theme is always scoped to a bundle; a shape is always installed under a bundle directory; a content type always belongs to a bundle's namespace.

---

## Naming

Bundles use **fully-qualified, namespaced names**: `<namespace>.<bundle>`.

| Example | What it is |
|---|---|
| `pub.polis.core` | The canonical core bundle shipped with polis. |
| `pub.alice.gardening` | A hypothetical community bundle from `alice.com` introducing a `pub.alice.gardening.plot` content type. |

Content types, shapes, and themes inside a bundle inherit the bundle's namespace and add a kind separator:

| Kind | Example |
|---|---|
| Content type | `pub.polis.post`, `pub.polis.dm`, `pub.polis.theme` |
| Shape | `pub.polis.shapes.v3`, `pub.polis.shapes.v4` |
| Theme | `pub.polis.themes.vice`, `pub.polis.themes.studio13` |

The substrings `.themes.` and `.shapes.` are **reserved pivots** — the parser uses them to distinguish a theme name from a content-type name. Bundle names cannot contain these substrings.

---

## Where bundles live

A bundle exists in three places, each with a different role:

### 1. Source-of-truth payload (embedded in the CLI binary)

```
cli-go/pkg/bundle/fixtures/pub.polis.core/
├── shapes/
│   ├── v3/                    # blog shape templates + partials
│   └── v4/                    # stream shape templates + partials
└── themes/
    ├── _shared/               # base CSS used by themes that don't ship their own
    ├── especial/
    ├── especial-light/
    ├── sols/                  # reserved: the logged-out landing theme
    ├── stardust/              # reserved: not user-selectable
    ├── studio13/
    ├── studio13-nk/
    ├── turbo/
    ├── vice/
    └── zane/
```

This is the **canonical payload**. Every CLI binary embeds the reference copy of every bundle it knows about. Upgrading the binary = upgrading the bundle.

### 2. Per-tenant install (`.polis/bundles/<bundle>/`)

When `polis init` creates a site, it installs the bundle's reference payload under `.polis/bundles/`:

```
.polis/bundles/pub.polis.core/
├── shapes/v3/        # synced from the binary's embedded fixture
├── shapes/v4/
└── themes/...
```

The same step writes the bundle's **declaration** to `content/pub.polis.core/bundle.json` — public, and pointed at from `.well-known/polis` → `bundles`.

This is what the renderer reads at runtime. **It is never edited by hand on hosted sites** — Patrol checks it on every cycle and Medic resyncs it whenever the installed shape or theme versions fall behind the binary's, so drift from the embedded source is corrected automatically (see [`actors.md`](actors.md)). Self-hosters get the same migrations from the standalone `tailor --apply` binary.

### 3. Per-tenant registry (`.polis/bundles/registry.json`)

The tenant's private configuration — which bundle is active, which theme, which shape:

```json
{
  "schema_version": 2,
  "active_theme": "pub.polis.themes.vice",
  "active_shape": "pub.polis.shapes.v4",
  "installed_bundles": [
    {
      "name": "pub.polis.core",
      "path": ".polis/bundles/pub.polis.core",
      "shape_versions": { "v3": "1.0.0", "v4": "1.8.65" },
      "theme_versions": { "vice": "1.1.0", "...": "..." }
    }
  ],
  "notifications": [ "..." ]
}
```

This file is **private state**, not part of the public identity at `.well-known/polis`. Listing a bundle in `installed_bundles` is what activates it; the version stamps are what Patrol compares to decide a resync. (Older sites carried `active_theme` in `.well-known/polis`; Medic, or `tailor --apply` for a self-hoster, moves it into the registry.)

---

## Anatomy of `bundle.json`

The bundle manifest declares everything the dispatch engine and renderer need to know:

```json
{
  "name": "pub.polis.core",
  "version": "1.0.0",
  "description": "Core polis content types",
  "handler": { "type": "builtin" },
  "ds": { "subscribes_to": ["pub.polis.*"] },
  "types": {
    "pub.polis.post":    { "dir": "post", "mount": "/posts", "renderer": "html",
                           "storage": { "pattern": "dated", "date_format": "YYYYMMDD", "versions": true },
                           "emits": ["pub.polis.post.published", "pub.polis.post.republished", "pub.polis.post.unpublished"] },
    "pub.polis.dm":      { "dir": "dm", "storage": { "pattern": "flat" }, "private": true },
    "...": "..."
  },
  "shapes": {
    "v3": { "name": "v3", "version": "1.0.0", "entry": { "post": "post.html", "index": "index.html", "...": "..." }, "partials": ["snippets/about.html", "..."], "default_css": "themes/_shared/base.css" },
    "v4": { "name": "v4", "version": "1.8.65", "entry": { "stream": "stream.html", "post": "stream-post.html" }, "partials": ["snippets/sentence-filter.html"], "default_css": "themes/_shared/base.css" }
  },
  "themes": {
    "vice": { "name": "vice", "version": "1.1.0", "css": "vice.css", "compatible_shapes": ["pub.polis.shapes.v3", "pub.polis.shapes.v4"] }
  },
  "artifacts": ["index.jsonl"]
}
```

Abridged from what `polis init` writes (the Go struct is `bundle.Bundle` in `cli-go/pkg/bundle/bundle.go`). ⚠️ **A content type declares no actions** — which actions a type supports is the handler's answer (`Handler.Actions`), not the manifest's.

See [`content-system.md` § Bundles](content-system.md#bundles) for the complete schema with every field, every validation rule, and full examples for handler types (executable + http).

---

## Three handler types

A bundle declares **one** handler type that the dispatch engine uses for all its content type actions:

| Handler | How actions run | Use case |
|---|---|---|
| `builtin` | Go code in `cli-go/pkg/ops/builtin_core.go`. Only `pub.polis.core` uses this. | Tightly integrated with cli-go packages. |
| `executable` | JSON stdin → external binary → JSON stdout. The engine spawns the binary per action. | Custom bundle implemented in any language as a local CLI. |
| `http` | JSON POST → HTTP endpoint → JSON response. | Custom bundle whose logic lives on a remote server. |

The contract is identical: every handler receives an `ActionRequest` (`{action, content_type, payload}`) and returns an `ActionResult` (`{status, data}`). See [`api/developer/dispatch-engine.md`](../../api/developer/dispatch-engine.md) for the full handler protocol.

---

## How a third-party bundle gets used

(Architectural — no third-party bundles ship today, but the dispatch engine is designed to support them.)

1. A community author publishes a bundle (e.g. `pub.alice.gardening`) — typically as a Git repo containing `bundle.json` plus the executable/HTTP handler.
2. A polis site operator installs it under `.polis/bundles/pub.alice.gardening/`.
3. The site's `.well-known/polis` points at each installed bundle's declaration under `bundles`. (The discovery service does not record bundles.)
4. Site visitors and other polis tools can introspect the bundle via `GET /v1/bundles` or `GET /v1/bundles/pub.alice.gardening`.
5. The dispatch engine routes any incoming action for `pub.alice.gardening.plot/<action>` to the bundle's declared handler.

Custom content types are governed by the same [policy grammar](../reference/policy-grammar.md) as core types — operators can `deny pub.alice.gardening.plot from all` at the DS, individual sites can `deny pub.alice.gardening.plot from <domain>`, and so on.

---

## See also

- [content-types.md](content-types.md) — What lives **inside** a bundle: the data model.
- [shapes.md](shapes.md) — How bundle content gets **rendered**.
- [themes.md](themes.md) — How bundle rendering gets **styled**.
- [content-system.md](content-system.md) — The deep reference: filesystem layout, full `bundle.json` schema, validation rules, complete event catalog.
- [architecture.md](architecture.md) — The four surfaces (CLI, webapp, polis.pub, DS) bundles live within.
- [api/developer/dispatch-engine.md](../../api/developer/dispatch-engine.md) — How the dispatch engine routes actions to bundle handlers.
- [actors.md](actors.md) — Patrol/Medic, the actors that keep tenant bundle installs in sync with the embedded source of truth.
