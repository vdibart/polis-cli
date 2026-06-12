# Shapes

> Part of the foundation set: [bundles](bundles.md), [content types](content-types.md), **shapes** (this doc), [themes](themes.md). See [architecture.md](architecture.md) for the four-surface map. For the deep reference, see [content-system.md](content-system.md).

A **shape** is a *rendering approach* — the set of HTML templates (and optionally client-side scripts) that turns a polis site's signed content into a user-facing surface. Shapes are declared by [bundles](bundles.md); themes (CSS only) sit on top of shapes; together they decide what a polis site *looks and behaves like*.

The `pub.polis.core` bundle ships two shapes. Picking one is a top-level architectural decision for a site:

| Shape | Wire name | What it produces | Where it shines |
|---|---|---|---|
| **Blog** | `pub.polis.shapes.v3` | Classic per-post HTML pages, dated archive, tag pages, RSS-style index. | Long-form writing, archival sites, anything where each post should be its own page. |
| **Stream** (infinity stream) | `pub.polis.shapes.v4` | Single stream-screen: focus post inlined + adjacent siblings excerpted, PQL-driven filtering. | Conversational sites, social feeds, anything where the experience is "what's happening on the network." |

The wire names (`v3` / `v4`) are kept for backward-compat with installed sites — they refer to the *blog* shape and *stream* shape respectively.

---

## What a shape ships

Every shape lives under a bundle's `shapes/<version>/` directory, declared in `bundle.json` with an **entry map** (which template handles which page) plus **partials** (reusable fragments) and an optional **default CSS** pointer (which theme to fall back to when none is set).

```
.polis/bundles/pub.polis.core/shapes/v3/
├── index.html              # site landing
├── post.html               # a single post page
├── posts.html              # archive view
├── tag.html                # tag-filtered view
├── tag-index.html          # list of all tags
├── comment.html            # a single comment (per-post stream)
├── comment-inline.html     # a comment inlined into a post
└── snippets/
    ├── about.html
    ├── post-item.html
    └── ...
```

Stream shape (`v4`) adds the JavaScript that drives the live filter:

```
.polis/bundles/pub.polis.core/shapes/v4/
├── stream.html             # the single page
├── stream.css              # shape-level CSS (themes layer on top)
├── stream.js               # client-side stream controller (PQL routing, hydration)
└── ...
```

Templates use the Mustache-like syntax documented in [`cli/user/templating.md`](../../cli/user/templating.md): `{{variable}}` substitution, `{{> snippet}}` partials, `{{#section}}…{{/section}}` blocks. The `default_css` field in the shape declaration tells the renderer which CSS file to load when the active theme doesn't ship its own.

---

## The render pipeline

When a site is rendered (locally via `polis render`, or on-the-fly via the webapp), the renderer composes output from three sources, in priority order:

```
   1. theme directory     ─►  if theme ships its own post.html, use that
   2. shape directory     ─►  else use the shape's post.html
   3. error               ─►  if neither has the template, error out
```

This is the **theme-overrides-shape** pattern: a theme can choose to ship its own `post.html` (the `studio13` theme does this) when it needs structural changes that go beyond CSS. Most themes don't — they ship only CSS and inherit every template from the shape. Changing a template in the shape immediately updates every CSS-only theme.

For sites running the *stream* shape (`v4`), rendering produces a single HTML page (`stream.html`) that loads `stream.js`. The script hydrates the stream-screen by:

1. Reading the URL — `/_/`, `/_/settings`, or `/_/pql/<sentence>`.
2. Resolving the sentence (or default landing PQL) into a filter.
3. Fetching matching content from the local API + the DS event stream.
4. Rendering items into the `<main class="layout">` element.

See [`pql.md`](../reference/pql.md) for how sentences compose, and [`webapp/developer/feed-architecture.md`](../../webapp/developer/feed-architecture.md) for how the stream fetches data.

---

## Why two shapes?

The blog and stream shapes embody different theories of how readers engage with content.

**Blog (`v3`):** A reader arrives at a *specific page* — a single post, an archive, a tag — and consumes content that's already been arranged for them. Navigation is between pages. Comments are inlined on the post they reply to. This is the model that ten thousand WordPress and Hugo sites have trained the web on.

**Stream (`v4`):** A reader arrives at a *single screen* and re-shapes it with queries. The default landing is "activity from my network by date"; one click filters to "my posts," "comments to bless," "profiles." Every navigation gesture is a re-filter rather than a page load. This is the model polis.pub uses, because the value isn't *individual content* — it's *flow through content the network has authored*. See [infinity-stream.md](infinity-stream.md) for the philosophy in full.

Both shapes are first-class. A polis site picks one in `.polis/bundles/registry.json`; the choice is reversible and doesn't break signed content.

---

## Active shape (per tenant)

Like the active theme, the active shape is **private per-tenant config**:

```json
// .polis/bundles/registry.json
{
  "active_theme": "pub.polis.themes.vice",
  "active_shape": "pub.polis.shapes.v3",
  "installed_bundles": [
    { "name": "pub.polis.core", "path": ".polis/bundles/pub.polis.core", "active": true }
  ]
}
```

Switching shapes is a single field change followed by a re-render. There's no migration: the same `content/pub.polis.core/post/...` files render through either shape, because the content is the source of truth and the shape only decides how to present it.

---

## Custom shapes

(Architectural — no third-party shapes ship today.)

A bundle can declare additional shapes by adding entries under `shapes` in its `bundle.json`. A custom shape:

- has a fully-qualified name (e.g. `pub.alice.shapes.gallery`),
- lives under `cli-go/pkg/bundle/fixtures/<bundle>/shapes/<short-name>/` and gets installed to `.polis/bundles/<bundle>/shapes/<short-name>/` per tenant,
- declares its entry map and partials in `bundle.json`,
- can opt in to compatibility with existing themes via the theme's `compatible_shapes` list.

Themes built for one shape can declare compatibility with another by listing both wire names in their `compatible_shapes` array — that's how a CSS-only theme like `vice` works seamlessly with both `v3` and `v4`.

---

## See also

- [bundles.md](bundles.md) — Shapes are declared by, and shipped with, a bundle.
- [themes.md](themes.md) — CSS-only presentation that sits on top of a shape.
- [content-types.md](content-types.md) — What gets rendered (posts, comments) — independent of *how*.
- [pql.md](../reference/pql.md) — The query language driving the v4 stream shape.
- [cli/user/templating.md](../../cli/user/templating.md) — Template syntax used inside shape templates.
- [webapp/designer/theme-system.md](../../webapp/designer/theme-system.md) — How shapes and themes resolve at runtime in the webapp.
- [content-system.md § SHAPE / BUNDLE / THEME](content-system.md#shape--bundle--theme) — Deep reference.
