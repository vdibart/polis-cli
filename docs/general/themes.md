# Themes

> Part of the foundation set: [bundles](bundles.md), [content types](content-types.md), [shapes](shapes.md), **themes** (this doc). See [architecture.md](architecture.md) for the four-surface map. For the deep reference, see [content-system.md](content-system.md). For CSS-variable contracts and runtime resolution, see [`webapp/designer/theme-system.md`](../webapp/designer/theme-system.md).

A **theme** is the *presentation layer* — the CSS (and optionally a small number of template overrides) that decides how a site **looks**. Themes sit on top of [shapes](shapes.md): the shape decides *what* gets rendered; the theme decides *how* it appears.

Themes are intentionally constrained. By default they're **CSS-only** — every template comes from the active shape, and themes just hand the renderer a stylesheet plus a few variables. This means a single template change in a shape instantly updates every CSS-only theme, and a theme can be written without knowing how the rest of the system works.

---

## What ships today

The `pub.polis.core` bundle ships these themes:

| Theme | Type | Vibe | Selectable? |
|---|---|---|---|
| `vice` | Dark | 80s Miami — warm saturated blue with pink/teal accents | ✓ |
| `studio13` | Dark | Bold minimal — pure black with burnt-orange accent | ✓ (ships template overrides) |
| `studio13-nk` | Dark | Studio13 variant | ✓ (ships template overrides) |
| `especial` | Dark | Premium near-black, gold/navy accents | ✓ |
| `especial-light` | Light | Warm fog, beige, dark gold accent | ✓ |
| `turbo` | Dark | Retro computing — deep blue-black, neon cyan | ✓ |
| `zane` | Dark | Editor/IDE palette — neutral gray, multi-color accents | ✓ |
| `sols` | Dark | Violet and peach — the polis brand palette | **No — system only** |

`sols` is the polis brand palette and is **reserved as the logged-out landing theme on `polis.pub`**. The webapp filters it out of the theme dropdown so that picking a personal theme always produces a visible shift from the system chrome.

---

## What a theme contains

```
.polis/bundles/pub.polis.core/themes/<name>/
├── <name>.css          # the stylesheet (required)
└── post.html           # optional: a per-template override
```

For a CSS-only theme, that's it — one file. Themes that need structural changes (different page anatomy, alternate post-item layout) ship template overrides alongside the CSS:

| Theme | Overrides |
|---|---|
| `vice`, `especial`, `especial-light`, `turbo`, `zane` | None — CSS only |
| `studio13`, `studio13-nk` | `index.html`, `post.html`, `posts.html`, `post-item.html` |

The renderer's lookup order is **theme dir → shape dir → error** (see [shapes.md § The render pipeline](shapes.md#the-render-pipeline)), so any file the theme provides beats the shape's version.

---

## CSS variable contract

Every theme defines a fixed set of CSS custom properties on `[data-theme="<name>"]` (or the root element). The shape's templates reference these variables exclusively — they never hardcode colors. This is the **contract between shape and theme**: shapes commit to using only the named variables; themes commit to providing values for all of them.

The contract splits into two groups:

```css
/* Nav variables — drive the topbar (logged-in icon nav + logged-out landing nav) */
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

For the full nav-system rationale (why nav colors are separate from page colors, why the dot is warm, why dark navs need to stay opaque), see [`webapp/designer/theme-system.md`](../webapp/designer/theme-system.md) and [`webapp/designer/navigation.md`](../webapp/designer/navigation.md).

---

## Light vs dark

The webapp tracks two orthogonal axes of "appearance":

1. **Webapp Appearance** — light vs dark for the webapp shell (set via the settings UI; persisted in localStorage and `webapp_theme` server config). Determines the `[data-theme]` value.
2. **Site Theme** — which named theme the *published site* uses (set via the Site Theme dropdown; persisted in `.polis/bundles/registry.json`).

These are separate so a reader can use a light-mode webapp while their site renders in a dark theme (or vice versa). Most themes are dark; `especial-light` is the canonical light option among user-selectable themes.

---

## Cross-theme compatibility

When you visit someone else's polis site, **your** nav appears on top of **their** content — your theme's nav variables compose with their theme's page variables on the same screen. Themes are designed so this works without coordination:

1. The nav is always a solid opaque strip — never transparent over foreign content.
2. The nav autohides on foreign sites (2-second linger, slides up unless mouse is over the trigger zone), minimizing visual conflict.
3. The notification dot color (`--nav-dot`) is always warm (red / orange / sunset) — never the theme's primary accent — so dots read on any nav background.
4. The avatar gradient is fixed per-user (derived from their theme accent), not affected by the site they're visiting.

Authors don't pick "themes for visiting." Every theme is expected to work both for the author's own site and as a viewer-nav over someone else's site.

---

## Active theme (per tenant)

The active theme is private per-tenant config:

```json
// .polis/bundles/registry.json
{
  "active_theme": "pub.polis.themes.vice",
  "active_shape": "pub.polis.shapes.v3"
}
```

(Pre-bundle-refactor sites carried `active_theme` in `.well-known/polis`; `polis init` and Patrol/Medic migrate it to the registry on first run. The public identity at `.well-known/polis` no longer carries it.)

Switching themes via the webapp:
1. Updates `active_theme` in `.polis/bundles/registry.json`,
2. Re-renders all pages (CSS-only themes don't need template re-generation, but the markup may reference `{{active_theme}}` in a `<link>` tag),
3. Persists the user's appearance preference in `webapp_theme` for the next browser load.

---

## Writing a custom theme

A custom theme can ship inside the core bundle (rare, requires PR to the polis repo) or inside a third-party bundle.

Minimal steps for the third-party path:

1. Add a `themes/<name>/<name>.css` file to your bundle's fixture directory.
2. Declare the theme in your bundle's `bundle.json`:
   ```json
   "themes": {
     "<name>": {
       "name": "<name>",
       "version": "1.0.0",
       "css": "<name>.css",
       "compatible_shapes": ["pub.polis.shapes.v3", "pub.polis.shapes.v4"],
       "type": "dark"
     }
   }
   ```
3. Provide values for every variable in the contract (above).
4. Optionally override `post.html`, `posts.html`, `post-item.html`, etc. by dropping them in the same theme directory — the renderer picks them up via theme → shape lookup.
5. Test against both the blog shape and the stream shape if you're claiming compatibility with both.

Custom themes participate in the same lifecycle as core themes: they ship in the bundle's embedded fixture, get installed to `.polis/bundles/<bundle>/themes/<name>/` on `polis init`, and get resynced by Patrol/Medic on every cycle for hosted tenants.

---

## See also

- [shapes.md](shapes.md) — What themes sit *on top of*.
- [bundles.md](bundles.md) — What themes ship *inside*.
- [webapp/designer/theme-system.md](../webapp/designer/theme-system.md) — Runtime resolution, the cross-theme compat rationale, theme/page mapping.
- [webapp/designer/navigation.md](../webapp/designer/navigation.md) — The nav anatomy themes color.
- [cli/user/templating.md](../cli/user/templating.md) — Template syntax for theme overrides.
- [content-system.md § SHAPE / BUNDLE / THEME](content-system.md#shape--bundle--theme) — Deep reference.
