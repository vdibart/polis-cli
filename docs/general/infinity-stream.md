# The Infinity Stream

> Builds on: [architecture.md](architecture.md), [shapes.md](shapes.md), [pql.md](pql.md). Implementation lives in the `pub.polis.shapes.v4` shape; see also [`webapp/designer/pages.md`](../webapp/designer/pages.md) for the page model and [`webapp/designer/navigation.md`](../webapp/designer/navigation.md) for the icon-row nav that drives it.

The **infinity stream** is the single-screen, sentence-filtered view of a polis site. It's the shape `pub.polis.shapes.v4` and the experience that polis.pub leads with. Where the [blog shape](shapes.md) gives you per-post pages and per-tag archives, the infinity stream gives you one screen that becomes everything: your network's activity, your own posts, comments awaiting blessing, profiles you might follow, message threads, anything composable from [PQL](pql.md).

This doc is mostly about the *why* — why a single stream-screen is the right primary surface for polis, what "infinity" means in this context, and how the same shape simultaneously serves three different viewpoints (owner, network member, public visitor).

---

## What "infinity stream" means

"Infinity" is doing two jobs at once.

**Infinity as no-floor / no-ceiling.** A traditional blog has discrete pages: page 1 of posts, page 2, an archive page per year, a tag page per tag. The reader navigates *between* views. The stream collapses this — there are no pages, only a sequence. You scroll forward and content keeps appearing. You compose a sentence and the stream re-filters. There's no "back to the index" because the index *is* the stream.

**Infinity as composable scope.** The sentence-filter lets a viewer dial scope between extremes without changing surfaces. "All posts from me by date" gives one site's archive. "All posts from my network by date" gives a personal feed. "All posts from everyone by date" gives a network firehose. None of these are separate pages. They're all the same stream-screen, re-aimed.

The shape's wire name (`pub.polis.shapes.v4`) is what's recorded in `.polis/bundles/registry.json`; the user-facing name "infinity stream" is what we mean when we describe the experience.

---

## Why a single screen

The infinity stream replaced what was, in the v3 design, a modal nav: separate Feed / Posts / Messages tabs, separate Dashboard and My Content pages, separate routes per concern. That design read well as a sketch but failed in practice.

**The problem with modal nav.** Tabs make sense when each tab is a different *kind* of thing. But in polis, "your posts," "your network's posts," "your blessings," and "your messages" aren't fundamentally different — they're the same underlying records (posts, comments, follows) filtered by *whose* and *when*. Splitting them into modes forced the user to model the system the way the implementation modeled it. A new user clicking "logged in" had to choose between feeds they didn't understand. A more experienced user kept context-switching to assemble a complete picture.

**The single-screen alternative.** One stream, many filters. The icon row at the top of the topbar offers preset sentences for the common views; the sentence-filter widget at the center lets you compose any combination. The whole experience is one surface that adapts. There's nothing to "switch to" — the screen *becomes* what the sentence asks for.

**What we kept.** A small number of static routes still exist (`/_/settings` for the settings panel; `/_/` for the default landing sentence). Everything else routes through `/_/pql/<sentence>`. The editor is also separate — it opens *over* the stream-screen rather than living as a sibling mode. See [pages.md](../webapp/designer/pages.md) for the full v4 routing model.

---

## Three POVs on the same shape

Polis content lives in *three* viewing contexts simultaneously. The infinity stream serves all three without forking.

### POV 1 — Owner (you, on your own SPA)

The owner POV is the full webapp SPA at `<handle>.polis.pub/_/`. The icon-row presets are wired to PQL sentences that pull from your local site and the DS event stream. The sentence-filter widget is fully composable. Notifications surface as badge dots on the icon-row buttons. The editor is one click away (the `edit` icon).

**What runs:** `index.html` + `app.js` + the v4 shape (`stream.html`, `stream.js`, `stream.css`) + `owner-extras.js` (icon-row preset wiring, dropdown menu, future inline editor and stream-item decorators). The script `owner-extras.js` is loaded *only* by the owner SPA — public pages never include it.

### POV 2 — Network member (you, visiting alice.polis.pub)

When you visit a polis site you don't own, you're still authenticated as yourself, and you see *their* content rendered through *their* active shape — but your nav rides along on top, autohiding to give their content visual primacy. The widget (`webapp/internal/hosted/widget/widget.js`) injects your icon nav as an overlay; the sentence-filter still works, scoped to that handle.

**Why this matters.** A polis site isn't a destination you visit and leave. The infinity stream's scope vocabulary (`from <handle>`, `from my network`, `from everyone`) means the same icon presets pivot meaning by who you're looking at. The gateway icon on your own SPA says "Activity from my network"; on alice.polis.pub it says "Activity in Alice's network." Same gesture, different sentence.

### POV 3 — Public visitor (not logged in)

A reader who isn't logged in arrives at a polis site as static HTML. The stream-screen still renders — the v4 shape's templates produce HTML at publish time — but the owner-extras layer is absent. Filters that can be expressed in URL slugs still work (you can deep-link to `/_/pql/all+posts+from+@alice+by+date` and the static rendering will reflect that filter at publish time). Filters that require live data (unread counts, badge dots, the avatar dropdown) simply don't appear.

This is the layered model owner-extras.js documents at its head: **CSS converges, code diverges along file boundaries**. Every viewer gets the same stream layout and theme; the *behaviors* that require ownership context (editing, blessing, filtering with live counts) gate themselves on whether `owner-extras.js` loaded.

---

## How the v4 shape implements it

The shape is a small set of files that ship in `pub.polis.core` and install per-tenant under `.polis/bundles/pub.polis.core/shapes/v4/`:

| File | Role |
|---|---|
| `stream.html` | The single page. Renders the topbar, the centered column, and the empty stream container. |
| `stream.css` | Shape-level CSS — layout, item anatomy, transitions. Themes layer their colors on top of this. |
| `stream.js` | The stream controller. Reads the URL (sentence or default), fetches matching content, hydrates the column, exposes `window.PolisStream` for owner-extras. |
| `snippets/` | Reusable partials (item chrome, date separators, empty states). |
| `stream-post.html` | Per-type template for a post item in the stream. |
| `stream-comment.html` | Per-type template for a comment. |
| `stream-profile.html` | Per-type template for a profile row. |
| `stream-dm.html` | Per-type template for a DM thread row. |
| `stream-mention.html` | Per-type template for a future at-mentions item. |

The webapp adds `app.js` (route handling, settings, editor wiring) and `owner-extras.js` (owner-only behaviors). The widget bundles a comparable subset for foreign-site visits.

### The hydration flow

When the page loads:

1. **Boot** — `index.html` loads `stream.css`, `stream.js`, `app.js`, and (on owner SPA) `owner-extras.js`. `theme-boot.js` reads `localStorage` and sets `[data-theme]` before anything paints, avoiding flash-of-wrong-theme.
2. **Parse** — `stream.js` reads `location.pathname`. If `/_/pql/<sentence>`, it parses the sentence; otherwise it loads the default landing sentence.
3. **Fetch** — `stream.js` calls the local v1 API (`/v1/content/<type>`) and the DS event stream as needed, scoped by the sentence.
4. **Render** — Each result is matched to its `stream-<type>.html` partial and inserted into `<main class="layout">`.
5. **Decorate** — On the owner SPA, `owner-extras.js` runs `afterRender` hooks per type: wiring bless/deny buttons on comments, edit affordances on owner posts, count refresh on icon-row dots, etc. On public pages this layer is absent — the content renders, but no owner-only affordances appear.
6. **Listen** — `stream.js` subscribes to URL changes (so icon clicks and sentence-filter changes don't full-reload) and re-runs the fetch+render cycle.

See [`webapp/developer/feed-architecture.md`](../webapp/developer/feed-architecture.md) for the cache and sync model behind the fetch step, and [`pql.md`](pql.md) for the sentence vocabulary the controller parses.

---

## Why this represents polis.pub

The infinity stream is the right shape for polis.pub specifically because polis.pub's value isn't *individual posts* — it's *flow through content the network has authored*. Polis is a decentralized network; the things that make it interesting aren't isolated, they're contextualized by your network's activity.

A blog shape would optimize for *what Alice wrote*. The infinity stream optimizes for *what's happening, with Alice as one input among many*. Both are real ways to use polis — and the bundle ships both shapes precisely because both are valid. But the network's leading edge — discovery, blessing, threading, following — only makes sense in a view where activity *streams in*. That's the shape polis.pub leads with, and that's why the v4 work was the architectural pivot rather than a feature add.

The single-screen model also makes a subtle promise: **you never leave context.** Every navigation gesture re-shapes the screen rather than replacing it. The handle label in the topbar always tells you whose site you're on. The icon row always offers the same gestures, with sentences that adapt to the current scope. The result is a network you can walk through without ever feeling like you've left.

---

## See also

- [shapes.md](shapes.md) — Blog vs stream shapes as a binary architectural choice.
- [pql.md](pql.md) — The sentence grammar the stream filter is built on.
- [architecture.md](architecture.md) — Where the stream sits within the four-surface model.
- [webapp/designer/pages.md](../webapp/designer/pages.md) — The v4 routing model and surfaces.
- [webapp/designer/navigation.md](../webapp/designer/navigation.md) — The icon-row anatomy and badge-dot system.
- [webapp/designer/theme-system.md](../webapp/designer/theme-system.md) — How themes layer over the stream shape.
- [webapp/developer/feed-architecture.md](../webapp/developer/feed-architecture.md) — Cache + sync architecture behind the stream.
- [snap-off-architecture.md](snap-off-architecture.md) — Why the stream is a *shape* (replaceable layer), not a baked-in feature.
