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
| `vice` | Dark | 80s Miami — warm saturated blue with pink/teal accents | blog · stream | ✓ |
| `especial` | Dark | Near-black with gold accents | blog · stream | ✓ |
| `especial-light` | Light | Warm fog with a dark gold accent | blog · stream | ✓ |
| `turbo` | Dark | Retro computing — deep blue-black, neon cyan | blog · stream | ✓ |
| `zane` | Dark | Editor/IDE palette — neutral gray, multi-color accents | blog · stream | ✓ |
| `studio13` | Dark | Bold minimal — black with an orange accent, CSS-only | stream | ✓ |
| `studio13-nk` | Dark | The same brand for the blog shape — **ships template overrides** | blog | ✓ (labelled *studio13* in the picker) |
| `sols` | Dark | Violet and peach — the polis brand palette | blog · stream | **No — reserved** |
| `stardust` | Light | Aladdin-Sane palette — CSS-only | stream | **No — reserved** |

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

The webapp tracks two orthogonal axes of "appearance":

1. **Webapp Appearance** — light vs dark for the webapp shell (set via the settings UI; persisted in localStorage and `webapp_theme` server config). Determines the `[data-theme]` value.
2. **Site Theme** — which named theme the *published site* uses (set via the Site Theme dropdown; persisted in `.polis/bundles/registry.json`).

These are separate so a reader can use a light-mode webapp while their site renders in a dark theme (or vice versa). Most themes are dark; `especial-light` is the canonical light option among user-selectable themes.

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
