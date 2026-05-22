# Tour: URL-as-filter

> A guided tour of the URL-as-filter thread. Source-of-truth concept docs live in [`../general/`](../general/); this tour walks the source code with you. Map of all threads: [`../../AGENTS.md`](../../AGENTS.md).

## The observation

Visit polis.pub while logged in. Click the **gateway** icon (the leftmost in the topbar's icon row). The URL becomes:

```
https://<you>.polis.pub/_/pql/all+activity+from+my+network
```

Click the **comment** icon. The URL becomes:

```
https://<you>.polis.pub/_/pql/all+comments+from+all+polis+to+bless
```

Now scroll. The URL might change again as the focused post in the stream shifts. None of this looks like normal page navigation. There's no full reload, no obvious routing trick, and the URL contains a sentence you could almost read aloud.

What's going on?

## What's actually happening

**The URL string isn't tracking what you see. The URL string is what you see.**

That sentence — `all activity from my network` — is a **PQL** sentence (Polis Query Language). It's the active filter on the stream-screen. When you click an icon or change a filter, the polis code mutates the sentence, pushes the new URL through `history.pushState`, and the stream re-filters to match. The URL is the source of truth for "what view is on screen."

This is *not* a feature retrofitted onto a normal SPA router. It's the core design of the v4 stream-screen surface — there's only one screen, the filter is the URL, every navigation gesture is a filter mutation.

## Walking the source

Three files carry the thread, in this order: **producer** (intercepts URLs), **grammar** (parses sentences), **consumer** (renders results).

### 1. Producer: [`webapp/internal/webui/www/app.js`](github.com/vdibart/polis-cli/blob/main/webapp/internal/webui/www/app.js)

The owner SPA's navigation dispatch. When you click any link or icon in the topbar, `App.navigateTo(path)` runs. Inside it, the very first check is the PQL URL intercept:

```javascript
// PQL URL intercept (chunk B). Paths shaped /_/pql/<sentence>
// (or /pql/<sentence> relative) parse the sentence, push the
// canonical URL, activate the stream-screen, and apply the
// filter once owner-extras + the controller are ready.
if (path && (path.indexOf('/pql/') === 0 || path.indexOf('pql/') === 0)) {
    return this._navigateToPQL(path, opts);
}
```

A path that looks like `/pql/all+activity+from+my+network` gets handed to `_navigateToPQL`, which calls `window.PQL.parse(sentence)` to turn the URL string into a filter-state object, then either pushes or replaces history with the canonical form, then tells the stream controller to apply the filter.

If the parser hasn't loaded yet (early in page life), the dispatch punts gracefully to the default filter — "land somewhere working" beats "show a blank screen."

If the path *isn't* a PQL URL, the same `navigateTo` continues into a fallback that quietly `replaceState`s to `/_/` and renders the default filter. Old v3 routes (`/_/posts`, `/_/blessings`, `/_/messages`) all land here. Hard-cutover by design.

### 2. Grammar: [`webapp/internal/webui/www/pql.js`](github.com/vdibart/polis-cli/blob/main/webapp/internal/webui/www/pql.js)

The parser and composer. Two functions matter most:

- **`PQL.parse(sentence)`** — Takes a sentence string ("all activity from my network") or a URL-encoded equivalent ("all+activity+from+my+network") and returns a filter-state object: `{ qualifier: "all", type: "activity", scope: "my-network", modifier: null }`.
- **`PQL.compose(state)`** — The inverse. Takes a filter-state object and returns the canonical sentence string.

The grammar itself is documented in [`pql.md`](../general/pql.md) — five positional slots (qualifier, type, "from", scope, modifier), with type-conditional rules that the dispatcher enforces. The parser is permissive: anything well-formed parses; downstream layers decide what's valid for each type.

Two vocabularies live side by side in this file: the **PQL grammar tokens** (user-facing — what the URL contains: `messages`, `my network`, `by date`) and the **internal JS values** (what the controller speaks: `dms`, `my-network`, `by-date`). The parser maps between them at the URL boundary; nothing else has to know about either vocabulary.

This is why the URL is *readable*: the file went out of its way to keep the user-facing vocabulary in the URL itself, even though the internal code uses hyphenated machine tokens.

### 3. Consumer: [`cli-go/pkg/bundle/fixtures/pub.polis.core/shapes/v4/stream.js`](github.com/vdibart/polis-cli/blob/main/cli-go/pkg/bundle/fixtures/pub.polis.core/shapes/v4/stream.js)

The stream controller — installed per-tenant from the embedded bundle fixture, served at `/bundle-assets/pub.polis.core/shapes/v4/stream.js`. It owns the actual rendering: hydrate the DOM, fetch matching content, insert items, observe scroll.

`stream.js` exposes a public surface on `window.PolisStream`:

- `setFilterScope(value, opts)`, `setFilterType(value)`, etc. — programmatic filter mutation. When the icon row in the topbar wires up a preset, these are the functions it calls.
- `applyFilter()` — clears the dynamic entries and re-fetches matching content for the current filter state.
- `appendEntry`, `clearDynamicEntries`, `getEntries` — DOM-side primitives.
- `renderers.{post,comment,profile,mention,dm}` — type-specific renderers; `registerRenderer` lets `owner-extras.js` override for owner-only views (e.g., DM with decryption indicator).
- `afterRender(type, fn)` — extension hook fired after a renderer produces DOM; the place where owner-only chrome (bless/edit/deny rollovers) gets bolted on without touching the base renderer.

The scroll-driven URL update closes the loop the other way: an `IntersectionObserver` watches each entry, and when the focused entry changes, the controller calls `history.replaceState` to update the URL with the focused item's path. This is what you saw scrolling — the URL is the *current view*, even when the change came from your scroll wheel rather than a click.

## The full picture

```
   ┌───────────────────────────────────────────────────────────────────┐
   │                          URL changes                              │
   │              ↑                                  │                 │
   │              │                                  │                 │
   │              │  ┌── pushState / replaceState ───┘                 │
   │              │  │                                                 │
   │              │  ▼                                                 │
   │   app.js (navigateTo / _navigateToPQL)                            │
   │              │                                                    │
   │              ├──→ PQL.parse(sentence) ──→ filter state            │
   │              │       (pql.js)                                     │
   │              │                                                    │
   │              ▼                                                    │
   │   stream.js (PolisStream.applyFilter)                             │
   │              │                                                    │
   │              ├──→ fetch matching content (local API + DS stream)  │
   │              │                                                    │
   │              ├──→ render via renderers.{type}                     │
   │              │                                                    │
   │              ▼                                                    │
   │   <main class="layout"> updates                                   │
   │              │                                                    │
   │              └──→ scroll → IntersectionObserver → URL changes ────┘
   │                                                                   │
   └───────────────────────────────────────────────────────────────────┘
```

Click an icon: producer → grammar → consumer. Scroll: consumer → URL → (rests there until you do something). Either direction, the URL is always the truth.

## Why polis is shaped this way

Pull the thread further. Three concept docs answer different "why" questions:

### [`docs/general/pql.md`](../general/pql.md) — the grammar's spec and rationale

Why a *sentence* and not a query string? Because filter views are things you'd share with someone in a message-board recommendation: "look at *all comments from alice.polis.pub to bless* — it's wild." A sentence reads. A query string doesn't. PQL also intentionally aligns with [`policy-grammar.md`](../general/policy-grammar.md) — the same `qualifier type scope` shape that powers `allow|deny` rules — so a user fluent in one is fluent in both.

### [`docs/general/infinity-stream.md`](../general/infinity-stream.md) — why a single screen

Why does the URL go to all this trouble? Because the v4 stream surface is **one screen, many filters** — there is no "Feed page" or "Posts page" or "Messages page" anymore. The previous design had modal tabs; the URL didn't reflect them; switching meant clicking around. PQL turned the filter into a first-class URL-addressable object, which let polis collapse the modal tabs into a single re-filterable surface. Read this if you want to understand the *design pressure* that produced PQL in the first place.

### [`docs/general/shapes.md`](../general/shapes.md) — the infinity stream is a *shape*

`stream.js` ships inside the `pub.polis.shapes.v4` shape — the **infinity stream shape**. A polis site running the older blog shape (`pub.polis.shapes.v3`) doesn't have any of this URL-as-filter machinery — it has per-post HTML pages. Shapes are swappable, per the [snap-off architecture](../general/snap-off-architecture.md). The whole URL-as-filter thread is a property of v4 specifically, not of polis as a protocol.

## What you should now understand

If you followed the thread end-to-end:

- The URL on polis.pub is meaningful — it's a sentence in a grammar (PQL).
- The grammar is parseable both ways (sentence ↔ filter-state); the URL is the canonical form of "what view is on screen."
- Three files carry the thread end-to-end: `app.js` (intercept), `pql.js` (grammar), `stream.js` (apply).
- This isn't a routing trick — it's the core architecture of the v4 stream-screen, which is itself a *shape* polis sites can opt into.
- The same grammar that filters the stream is intentionally close to the grammar that writes policy rules. Polis is leaning into "sentence as primary abstraction" across multiple surfaces.

If you want to go deeper:

- The starter map of every thread: [`AGENTS.md`](../../AGENTS.md)
- The four-surface architecture: [`../general/architecture.md`](../general/architecture.md)
- Why every layer (including the stream shape) is replaceable: [`../general/snap-off-architecture.md`](../general/snap-off-architecture.md)
- What lives under `/bundle-assets/`: [`../general/bundles.md`](../general/bundles.md)
