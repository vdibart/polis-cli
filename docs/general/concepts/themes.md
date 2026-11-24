# Themes

*For* [Developers](../../README.md#building-on-polis) · [Writers](../../README.md#writing-on-polis) · [Contributors](../../README.md#contributing-to-polis) — *About* [Content](../README.md#content) — *Kind* [Concept](../../README.md#kinds-of-page) — *Component* [Webapp](../../webapp/README.md) — *See also* [reference](../../cli/user/templating.md) · no spec yet · [guide](../guides/themes.md)

> Part of the foundation set: [bundles](bundles.md), [content types](content-types.md), [shapes](shapes.md), **themes** (this doc). See [architecture.md](architecture.md) for the four-surface map. For the deep reference, see [content-system.md](content-system.md).

A **theme** is the *presentation layer* — the CSS (and optionally a small number of template overrides) that decides how a site **looks**. Themes sit on top of [shapes](shapes.md): the shape decides *what* gets rendered; the theme decides *how* it appears.

Themes are intentionally constrained. By default they're **CSS-only** — every template comes from the active shape, and themes just hand the renderer a stylesheet that sets the palette the shape reads. This means a single template change in a shape instantly updates every CSS-only theme, and a theme can be written without knowing how the rest of the system works.

---

## What ships today

The `pub.polis.core` bundle ships these themes (declared in `DefaultCoreBundle`, `cli-go/pkg/bundle/bundle.go`):

| Theme | Type | Vibe | Shapes | Selectable? |
|---|---|---|---|---|
| `vice` | Dark | 80s Miami — warm saturated blue with pink/teal accents | v5 · blog · stream | ✓ |
| `especial` | Dark | Near-black with gold accents | v5 · blog · stream | ✓ |
| `especial-light` | Light | Warm fog with a dark gold accent | v5 · blog · stream | ✓ |
| `turbo` | Dark | Retro computing — deep blue-black, neon cyan | v5 · blog · stream | ✓ |
| `zane` | Dark | Editor/IDE palette — neutral gray, multi-color accents | v5 · blog · stream | ✓ |
| `studio13` | Dark | Bold minimal — black with an orange accent, CSS-only | v5 · stream | ✓ |
| `studio13-nk` | Dark | The same brand for the blog shape — **ships template overrides** | v5 · blog | ✓ (labelled *studio13* in the picker) |
| `sols` | Dark | Violet and peach — the polis brand palette | v5 · blog · stream | **No — reserved** |
| `daryl` | — | defined by its `v5.json` alone | v5 | ✓ |
| `elwood-signal` · `elwood-noise` | — | a pair, defined by their `v5.json` alone | v5 | **No — reserved until they join v5** |
| `stardust` | Light | Aladdin-Sane palette — CSS-only | v5 · stream | **No — reserved** |

Two more rules shape that list:

- **The picker is shape-aware.** A theme only appears for a site whose active shape is in its `compatible_shapes`, which is why `studio13` and `studio13-nk` can share one label: a given site only ever sees one of them.
- **`sols` and `stardust` are reserved.** `sols` is the logged-out landing theme on `polis.pub`; `stardust` is reserved for one system site. The webapp refuses to set either as a personal theme (`reservedThemes`, `webapp/internal/server/handlers.go`), so picking a personal theme always produces a visible shift from the system chrome. An operator can still put a site on one by editing its registry.

`_shared` is not a theme anyone picks: it holds the structural CSS common to the themes, and each theme's file overrides only what is distinctive.

---

## What a theme contains

```
.polis/bundles/pub.polis.core/themes/<name>/
├── <name>.css          # the stylesheet (required)
└── post.html           # optional: a per-template override (blog shape)
```

For a CSS-only theme, that's it — one file. Themes that need structural changes (different page anatomy, alternate post-item layout) ship template overrides alongside the CSS:

| Theme | Overrides |
|---|---|
| every theme except `studio13-nk` | None — CSS only |
| `studio13-nk` | `index.html`, `post.html`, `posts.html` |

The renderer's lookup order is **theme dir → shape dir → error** (see [shapes.md § The render pipeline](shapes.md#the-render-pipeline)), so any file the theme provides beats the shape's version.

---

## CSS variable contract

⭐ **On the v5 shape (the default) a theme is TOKENS, not CSS:** its one definition is `themes/<name>/v5.json`, checked
against the v5 token contract (`cli-go/pkg/theme/v5tokens.go`: colours, fonts named by family, motion, and so on),
and polis writes the shape's variables from it into `styles.css` after the shape's own `v5.css`. A site can set its
own tokens over its theme's (`site.set_tokens`). The two sets of variables below are the blog and stream shapes'
(no longer supported, still rendering).

**The v5 shape tokens** are the geometry a theme may choose, each from a fixed scale and never a number, so the
[style guide](../../webapp/designer/style-guide.md#4-alignment)'s measured alignment holds at every value:

| Token | Values (default first) | What it changes |
|---|---|---|
| `corner-radius` | medium (a card's corners 14px) · square (0) · small (7px) · large (21px) | every corner, in proportion; the mark stays a circle |
| `spacing` | default · compact · roomy | a card's padding (24/34/22px; compact 18/26/16; roomy 30/42/28), the mark's distance from the card's edge (22, 16, 28px), the gap between cards (10, 6, 16px) and the footer's rule (22 and 16px; 16 and 12; 28 and 20) |
| `stroke-weight` | thin (1px) · medium (2px) · thick (3px) | the outline of cards, the section box and the pinned row; rules inside a card stay hairlines |

They never change the space between a card's first row and the words under its mark, the mark's distance from
those words, or the frame (the top bar, the menu's box height, the column).

There are **two** sets of variables, and they belong to different layers.

**1. The palette a theme sets, which the shapes read.** Every theme defines `--color-*` custom properties in its `:root` block — `--color-bg`, `--color-bg-light`, `--color-surface`, `--color-panel`, `--color-text`, `--color-text-soft`, `--color-text-muted`, `--color-border`, `--color-accent` and the rest — and the shape CSS and templates reference those names (`shapes/v4/stream.css`, `themes/_shared/base.css`). ⚠️ **Some names are vestigial**: a theme keeps `--color-pink` or `--color-sunset` even when its palette has neither, so shared rules stay byte-identical across themes. The practical checklist for a new theme is an existing theme's `:root` block.

Themes that support the stream shape also set two **filter buckets**, `--filter-dark-*` and `--filter-light-*`, which colour the sentence filter for a dark or a light top bar (see *Cross-theme compatibility* below).

**2. The chrome variables the webapp derives.** The webapp's own nav and page chrome use a second contract, set per site theme on `html[data-site-theme="<name>"]` in `webapp/internal/webui/www/nav-themes.css`:

```css
/* Nav variables — drive the topbar */
--nav-bg
--nav-bg-hover
--nav-bg-active
--nav-border
--nav-icon
--nav-icon-hover
--nav-icon-active
--nav-accent
--nav-dot               /* notification dot — warm alert color, NOT the theme accent */

/* Page variables — drive the content area */
--page-bg
--page-text
--page-text-2           /* secondary */
--page-text-3           /* tertiary / muted */
--page-card
--page-card-shadow
--page-border
--page-accent           /* links, actions, highlighted elements */
```

Each is derived from the theme's palette (the formula is in that file's header — `--nav-bg` from `--color-bg-light`, `--page-bg` from `--color-bg`, and so on). A theme author does not set them; a new site theme needs a block there for the webapp chrome to match it.

For the nav anatomy these colour, see [`webapp/designer/navigation.md`](../../webapp/designer/navigation.md).

---

## Light vs dark

Two things have an appearance, and they are set apart:

1. **The owner app** (the v5 app, at `/_/`) has its own look, defined by its own tokens in `frame.css`. It does not take the site's theme, and it has no light or dark setting of its own. (The v4 app's light/dark switch, kept in `localStorage` and the `webapp_theme` setting, went with that app.)
2. **The site theme** — which named theme the *published site* uses (chosen in enhance › site › Theme; persisted in `.polis/bundles/registry.json`).

Changing the site's theme never changes the owner app. Most themes are dark; `especial-light` is the canonical light option among user-selectable themes.

---

## Cross-theme compatibility

When a signed-in visitor opens someone else's polis site, **their** nav is drawn over **the author's** page (`webapp/internal/hosted/nav/nav.js`). Two themes share one screen, and the split is fixed:

1. **The visitor keeps** the nav band, its icons, the avatar and the handle label. The avatar's colours come from the visitor's own `.well-known/polis` → `avatar`, not from any theme, so they never change with the site being visited.
2. **The author's theme supplies** the sentence filter and its dot, using whichever of the author's `--filter-dark-*` / `--filter-light-*` buckets matches the brightness of the visitor's band, so it stays legible.
3. The notification dot (`--nav-dot`) is a warm alert colour, never the theme's primary accent, so it reads on any nav background.

Point 2 puts three requirements on a theme's filter buckets. The full contract is the banner comment in the `vice` theme's stylesheet (`cli-go/pkg/bundle/fixtures/pub.polis.core/themes/vice/vice.css`).

- **Define both buckets, six tokens each** (`text`, `text-hover`, `underline`, `underline-hover`, `identity`, `dot`), whatever the theme's own look. A visitor's band can be dark or light. One bucket is the theme's native filter; the other is its inverted variant.
- **Hardcode the bucket colours** as literal hex, or `color-mix` over literal hex. Do not reference `var(--color-accent)`, `var(--color-text-soft)` or `var(--color-text)`: the nav overrides those on the top bar with the visitor's palette, which would drag the bucket along with it.
- **A theme whose top bar and body have opposite brightness** (only `stardust` today) also sets the base `--filter-text`, `--filter-underline`, `--filter-identity` and `--filter-dot` at `:root`, because the shape's default is derived from the body colours.

If the author's theme does not define the bucket the nav needs, the nav falls back to the visitor's own filter colours rather than drawing an undefined colour.

Authors don't pick "themes for visiting." Every theme is expected to work both for the author's own site and under a visitor's nav.

---

## Active theme (per tenant)

The active theme is private per-tenant config:

```json
// .polis/bundles/registry.json
{
  "active_theme": "pub.polis.themes.vice",
  "active_shape": "pub.polis.shapes.v4"
}
```

(Older sites carried `active_theme` in `.well-known/polis`; Medic, or `tailor --apply` for a self-hoster, moves it into the registry. The public identity at `.well-known/polis` no longer carries it.)

Switching themes via the webapp:
1. Refuses a reserved theme, and any name that is not installed,
2. Updates `active_theme` in `.polis/bundles/registry.json`,
3. Copies the theme's stylesheet to the site's `styles.css`,
4. Re-renders the whole site.

The webapp's own light/dark appearance is a separate setting (`webapp_theme`) and is not touched.

---

## Writing a custom theme

Writing, activating and testing a theme of your own, and where a theme polis did not ship can run today, is a task: see
[the themes guide](../guides/themes.md).

---

## See also

- [shapes.md](shapes.md) — What themes sit *on top of*.
- [bundles.md](bundles.md) — What themes ship *inside*.
- [webapp/designer/navigation.md](../../webapp/designer/navigation.md) — The nav anatomy themes color.
- [cli/user/templating.md](../../cli/user/templating.md) — Template syntax for theme overrides.
- [content-system.md § SHAPE / BUNDLE / THEME](content-system.md#shape--bundle--theme) — Deep reference.
