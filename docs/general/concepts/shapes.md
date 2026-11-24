# Shapes

*For* [Developers](../../README.md#building-on-polis) — *About* [Content](../README.md#content) — *Kind* [Concept](../../README.md#kinds-of-page) — *See also* [reference](content-system.md) · no spec yet · no recipe yet

> Part of the foundation set: [bundles](bundles.md), [content types](content-types.md), **shapes** (this doc), [themes](themes.md). See [architecture.md](architecture.md) for the four-surface map. For the deep reference, see [content-system.md](content-system.md).

A **shape** is a *rendering approach* — the set of HTML templates (and optionally client-side scripts) that turns a polis site's signed content into a user-facing surface. Shapes are declared by [bundles](bundles.md); themes (CSS only) sit on top of shapes; together they decide what a polis site *looks and behaves like*.

The `pub.polis.core` bundle ships three shapes. **v5 is the current one: every new site starts on it**, and on polis.pub every site is on it. The blog and stream shapes are no longer supported but still render, unchanged, for a site that records one:

| Shape | Wire name | What it produces | Status |
|---|---|---|---|
| **v5** (cards) | `pub.polis.shapes.v5` | The site as cards under its own menu (posts, comments, about, following): each item's own page is the item open with its neighbours folded around it, the home page is the newest posts, and any other view is addressed by its PQL sentence (`/pql/<sentence>`) and drawn by `v5.js`. Themes are tokens (`v5.json`). | **current**, the default |
| **Blog** | `pub.polis.shapes.v3` | Classic per-post HTML pages, dated archive, tag pages, RSS-style index. | not supported; still renders |
| **Stream** (infinity stream) | `pub.polis.shapes.v4` | Single stream-screen: focus post inlined + adjacent siblings excerpted, PQL-driven filtering. | not supported; still renders |

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

The v5 shape is a frame and one template per kind of card, plus the controller:

```
.polis/bundles/pub.polis.core/shapes/v5/
├── frame.html              # every page: the top bar with the site's menu, the column, the footer
├── post.html               # a post's card (open on its own page, folded elsewhere)
├── comment.html            # one of the site's own comments, as a card
├── about.html              # the profile card
├── following.html          # the following card, and person.html its rows
├── v5.css                  # the shape's stylesheet; a theme's tokens set its variables
└── v5.js                   # the controller: opens and folds cards, redraws a view from its sentence
```

The unsupported stream shape (`v4`) adds the JavaScript that drives its live filter:

```
.polis/bundles/pub.polis.core/shapes/v4/
├── stream.html             # the single page
├── stream.css              # shape-level CSS (themes layer on top)
├── stream.js               # client-side stream controller (PQL routing, hydration)
├── stream-post.html        # a post, server-rendered as the focus entry or a sibling
└── snippets/
    ├── sentence-filter.html
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

This is the **theme-overrides-shape** pattern: a theme can choose to ship its own `post.html` (`studio13-nk` does this for the blog shape: it ships its own `index.html`, `post.html` and `posts.html`) when it needs structural changes that go beyond CSS. Most themes don't — they ship only CSS and inherit every template from the shape. Changing a template in the shape immediately updates every CSS-only theme.

For a v5 site, rendering writes each post's and comment's own page (`posts/<date>/<slug>.html`, `comments/<date>/<slug>.html`), the home page (`index.html`, the newest posts), the profile (`about.html`, served at `/about`), `styles.css` (the shape's stylesheet, then the theme's tokens, then the site's own CSS) and `/v5.js`. Every page reads without script; `v5.js` opens and folds cards and, for a view's address (`/pql/<sentence>`, which the server answers with `index.html` marked with its sentence), fetches the sentence's items from the site's own `/pql/` as JSON and draws each by taking its card from its own page.

For sites still on the *stream* shape (`v4`), rendering produces a single HTML page (`stream.html`) that loads `stream.js`. The script hydrates the stream-screen by:

1. Reading the URL — the site root, a post's permalink, or `/pql/<sentence>`. (The owner's app has addresses of its own, under `/_/`: [Addresses](../../webapp/developer/development.md#addresses).)
2. Resolving the sentence (or default landing PQL) into a filter.
3. Fetching matching entries as JSON from the site's own `/pql/` endpoint.
4. Rendering them into the stream's layout around the server-rendered focus post.

See [`pql.md`](../reference/pql.md) for how sentences compose, and [`webapp/developer/feed-architecture.md`](../../webapp/developer/feed-architecture.md) for how the stream fetches data.

---

## Why three shapes?

The blog and stream shapes embody different theories of how readers engage with content, and v5 keeps what each got right.

**Blog (`v3`):** A reader arrives at a *specific page* — a single post, an archive, a tag — and consumes content that's already been arranged for them. Navigation is between pages. Comments are inlined on the post they reply to. This is the model that ten thousand WordPress and Hugo sites have trained the web on.

**Stream (`v4`):** A reader arrives at a *single screen* and re-shapes it with queries. The default landing is "activity from my network by date"; one click filters to "my posts," "comments to bless," "profiles." Every navigation gesture is a re-filter rather than a page load. This was the model polis.pub used until v5, because the value isn't *individual content* — it's *flow through content the network has authored*. See [infinity-stream.md](infinity-stream.md) for the philosophy in full.

**v5:** every item has its own page again (blog), and that page is the stream at that item, loading newer and older around it as the reader scrolls (stream), so landing on a post is the same as scrolling the home page down to it. Views are PQL sentences, as in the stream, but they are drawn from the same cards every page carries, so a card is never drawn twice in two places.

⚠️ **The blog and stream shapes are no longer supported.** The default shape is v5, and a new site starts on it. A site that still records the blog or stream shape keeps rendering exactly as before; nothing removes either shape. A polis site's shape is set in `.polis/bundles/registry.json`; the choice is reversible and doesn't break signed content.

---

## Active shape (per tenant)

Like the active theme, the active shape is **private per-tenant config**:

```json
// .polis/bundles/registry.json
{
  "active_theme": "pub.polis.themes.vice",
  "active_shape": "pub.polis.shapes.v4",
  "installed_bundles": [
    { "name": "pub.polis.core", "path": ".polis/bundles/pub.polis.core", "shape_versions": { "...": "..." }, "theme_versions": { "...": "..." } }
  ]
}
```

Switching shapes is a single field change followed by a re-render. There's no migration: the same `content/pub.polis.core/post/...` files render through either shape, because the content is the source of truth and the shape only decides how to present it.

⚠️ **Who decides differs by where the site runs.** On polis.pub, and any custodied deployment, every site is on the default shape, v5: the maintenance cycle moves a site on the blog or stream shape to it. A site you run yourself keeps the shape you set, and [Tailor](actors.md#tailor) only reports a site on either of them, with how to switch. A site from before the stream existed (before v0.63) has no recorded shape; when its registry is first created it records the blog shape it was already rendering, and a new site starts on the default shape, v5. Every shipped theme that declares the blog shape is rendered through it in the test suite on every build.

---

## Custom shapes

⚠️ **Not implemented.** polis loads only the core bundle embedded in its binary; nothing reads a third-party bundle today. The rest of this section describes the intended design, not something you can do.

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
- [pql.md](../reference/pql.md) — The query language behind v5's views (and the unsupported stream shape's).
- [cli/user/templating.md](../../cli/user/templating.md) — Template syntax used inside shape templates.
- [content-system.md § SHAPE / BUNDLE / THEME](content-system.md#shape--bundle--theme) — Deep reference.
