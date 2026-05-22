# Theme System

## Overview

Polis has three theming contexts:

1. **System theme (SOLS)** — the logged-out experience on polis.pub
2. **User theme** — the logged-in user's chosen theme, applied to the nav bar and their own content pages
3. **Foreign theme** — the theme of someone else's site, seen when visiting them

The key design challenge: when you visit alice.polis.pub, YOUR nav (in your theme) sits on top of HER site (in her theme). Both must be legible. Neither should fight.

## SOLS as System Theme

SOLS is the brand palette. It is what polis *looks like* before you are anyone. It is reserved — users cannot select SOLS as their personal theme.

**Why SOLS is reserved:**
- If your personal theme matches the system theme, there is no visual shift when you log in. The nav looks identical to the system chrome. You lose the "you've arrived" signal.
- On other people's sites, a SOLS-themed nav could blur with SOLS-influenced content, dissolving the boundary between your layer and their layer.
- Making SOLS unavailable gives theme selection meaning — every other theme is a declaration away from the default.

## Theme Architecture

Themes use the active **shape's templates** as the canonical HTML. Shapes and themes both ship inside the `pub.polis.core` bundle and are installed per-tenant under `.polis/bundles/pub.polis.core/`:

- Shapes at `.polis/bundles/pub.polis.core/shapes/<version>/` (the core bundle ships `v3` — classic blog — and `v4` — the infinity stream used on polis.pub and the webapp logged-in view).
- Themes at `.polis/bundles/pub.polis.core/themes/<name>/`.

Individual themes provide only CSS and optional per-template overrides:

- **CSS-only themes** (turbo, sols, zane, especial, especial-light, vice): Contain only a `.css` file. All HTML comes from the active shape.
- **Override themes** (studio13, studio13-nk): Contain CSS plus specific template overrides for structural differences (e.g., different post-item layout with excerpts).

The render pipeline resolves each template: theme dir → shape dir → error. Changing a template in the shape immediately updates all CSS-only themes. The active shape and active theme are recorded in `.polis/bundles/registry.json` (private per-tenant config).

> Pre-bundle-refactor sites used a shared `themes/_base/` directory for the canonical HTML. Patrol/Medic migrate this into the bundle's shape automatically; see `docs/general/content-system.md` SHAPE/BUNDLE/THEME for the full model.

## Available Themes

The webapp settings dropdown filters out `sols` because it is reserved as the logged-out landing theme on polis.pub. The list of *user-selectable* themes is therefore everything below except `sols`.

| Theme | Type | Character | Overrides | Selectable? |
|-------|------|-----------|-----------|-------------|
| Turbo | Dark | Retro computing, deep blue-black, neon cyan accent | CSS only | ✓ |
| Zane | Dark | Editor/IDE palette, neutral gray, multi-color accents | CSS only | ✓ |
| Vice | Dark | 80s Miami, warm saturated blue, pink/teal accents | CSS only | ✓ |
| Especial | Dark | Premium, near-black, gold/navy accents | CSS only | ✓ |
| Especial-Light | Light | Warm fog, beige, dark gold accent | CSS only | ✓ |
| Studio13 | Dark | Bold minimal, pure black, orange accent | index, post, posts, post-item | ✓ |
| Studio13-nk | Dark | Studio13 variant (NK) | per-template overrides | ✓ |
| **SOLS** | Dark | Violet and peach, system brand palette | CSS only | **No — system only** |

## Theme Variable Contract

Every theme must define these CSS variables for the nav bar to function:

### Nav Variables (derived from the theme)
```css
--nav-bg          /* Nav strip background — slightly lighter/raised from theme bg */
--nav-bg-hover    /* Icon hover background */
--nav-bg-active   /* Active icon background */
--nav-border      /* Nav bottom border and separator lines */
--nav-icon        /* Default icon color (muted) */
--nav-icon-hover  /* Icon hover color */
--nav-icon-active /* Active icon color and wordmark color (brightest) */
--nav-accent      /* Primary accent — used for + button bg, wordmark hover */
--nav-dot         /* Notification dot color (warm alert, not theme accent) */
```

### Page Variables (for content areas)
```css
--page-bg          /* Content area background */
--page-text        /* Primary text */
--page-text-2      /* Secondary text */
--page-text-3      /* Tertiary/muted text */
--page-card        /* Card/surface background */
--page-card-shadow /* Card shadow */
--page-border      /* Borders and dividers */
--page-accent      /* Links, actions, highlighted elements */
```

## Example: Vice Theme

```css
/* Nav */
--nav-bg: #1a2c50;
--nav-bg-hover: #223860;
--nav-bg-active: #2a4470;
--nav-border: rgba(255,255,255,0.08);
--nav-icon: #8890a0;
--nav-icon-hover: #d0c4b8;
--nav-icon-active: #f0e8e0;
--nav-accent: #d06888;
--nav-dot: #e88050;

/* Page */
--page-bg: #122040;
--page-text: #f0e8e0;
--page-text-2: #d0c4b8;
--page-text-3: #8890a0;
--page-card: #1a2c50;
--page-card-shadow: 0 1px 3px rgba(0,0,0,0.3), 0 0 0 1px rgba(255,255,255,0.04);
--page-border: rgba(255,255,255,0.06);
--page-accent: #d06888;
```

## Cross-Theme Compatibility

When your nav sits on top of someone else's site, the themes will differ. The nav survives this because:

1. **The nav is always a solid strip** — it has its own background color, never transparent. The content below has a clear boundary.
2. **The nav autohides on foreign sites** — it appears for 2 seconds then slides away, minimizing visual conflict. Users hover the top edge to reveal it when needed.
3. **The nav height (50px) is consistent** — it pushes content down when visible, never overlays.
4. **Dark navs on light sites (and vice versa)** are the hardest combos. The 1px border and solid background provide enough separation. The autohide means these combos are momentary, not persistent.

### Compatibility Rules
- Nav `--nav-bg` should be a solid, opaque color. Never transparent or semi-transparent over foreign content.
- The notification dot (`--nav-dot`) should be a warm color (red, orange, sunset) that reads on the nav background. Not the theme's primary accent.
- The `--nav-border` should provide subtle but visible separation from the content below.
- The avatar gradient colors are fixed per-user (derived from their theme accent), not affected by the site they're visiting.

## Theme and Page Mapping

| Page | Nav theme | Content theme |
|------|-----------|---------------|
| polis.pub (logged out) | None (system topbar) | SOLS |
| polis.pub (logged in) | User's theme | User's theme |
| Dashboard | User's theme | User's theme |
| My content (handle.polis.pub) | User's theme | User's theme |
| Someone else's site | User's theme (autohide) | Their theme |
