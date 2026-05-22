# Tour: How the infinity stream actually works

> A higher-order tour. Knits together what's happening on the polis.pub stream-screen — the filter widget, the URL behavior, the live data appearing from across the network, the design choices behind it all. Points down into the two deep tours ([url-as-filter](url-as-filter.md), [ds-to-stream](ds-to-stream.md)) for the file-level walks, and out to the [PQL spec](../general/pql.md), the [infinity stream concept doc](../general/infinity-stream.md), and the [DS architecture](../ds/developer/stream-architecture.md) for the philosophy and protocol layers.
>
> **Start here if** you've poked around polis.pub and want one explanation that ties together "the filter," "the stream," "the URL," and "the network." Then follow the spokes that interest you.

## What you see, in one paragraph

You open `<you>.polis.pub`. A topbar runs across the top: avatar on the left, then six little icons (gateway, paragraph, comment, people, envelope, edit), then a centered widget with several dropdowns spelling out a sentence like **all activity from my network by date**, then your handle on the right. Below the topbar, a 640px-wide column shows a stream of items — your posts mixed with posts and comments from people you follow, dated, scrollable, no pagination. Click the **paragraph** icon: the sentence becomes **all posts from me by date** and the column re-fills with just your posts. Click into the sentence and change "my network" to "all polis": the column re-fills with the wider network. Scroll: the URL changes as different posts become the focused one. Wait 30 seconds: new items appear from people who just published. Each item from another tenant carries a comment-count badge that reflects what's on *their* site, not yours.

Every one of those behaviors is a piece of the same composed surface. This tour walks the composition.

## The composition — four layers

```
   ┌─────────────────────────────────────────────────────────────────┐
   │                                                                 │
   │   SHAPE         pub.polis.shapes.v4 — the stream-screen         │
   │                 templates + scripts that produce the page       │
   │                                                                 │
   │   FILTER        URL  ↔  PQL sentence  ↔  filter state           │
   │                 (the URL is the filter)                         │
   │                                                                 │
   │   DATA          local JSONL cache  ←  webapp sync loop  ←  DS   │
   │                 plus on-demand DS aggregation queries           │
   │                                                                 │
   │   CHROME        topbar, icon row, sentence-filter widget,       │
   │                 handle label, badge dots, avatar dropdown       │
   │                                                                 │
   └─────────────────────────────────────────────────────────────────┘

   The four compose into one experience: chrome lets you steer the
   filter; the filter is the URL; the data is staged in the cache by
   the sync loop; the shape renders it all into the column.
```

The four layers are independent in the codebase — they live in different files, packages, even repos — and they meet at the rendered stream-screen.

---

## The sentence-filter widget — chrome meets filter

The centered widget in the topbar is the single piece of UI that ties chrome and filter together. From [`index.html`](github.com/vdibart/polis-cli/blob/main/webapp/internal/webui/www/index.html) (line ~305):

```html
<div class="polis-topbar-filter" id="polis-topbar-filter">
    <div class="sentence-filter" role="group" aria-label="Stream filter">
        <!-- qualifier slot · type slot · "from" · scope slot · modifier slot · site-typeahead -->
    </div>
</div>
```

It's a `role="group"` of dropdowns plus a typeahead — five interactive slots that together compose a [PQL sentence](../general/pql.md). The user clicks slot 2 ("activity") and picks "posts"; the widget updates the local sentence state, composes the new URL (`/_/pql/all+posts+from+my+network+by+date`), `replaceState`s history, and tells the stream controller `setFilter(state)` — which clears the visible items and re-fetches matching ones.

The icon row to the left is a *shortcut layer over the same widget*. Each icon is a **preset PQL sentence** that the widget would otherwise compose by hand. From [`owner-extras.js`](github.com/vdibart/polis-cli/blob/main/webapp/internal/webui/www/owner-extras.js):

```javascript
// step-06/6.e: icon-row preset definitions.
// the topbar (avatar | gateway | paragraph | comment | people | envelope | edit)
{
  gateway:   { filter: { type: 'activity',  scope: 'my-network', modifier: null      } },
  paragraph: { filter: { type: 'posts',     scope: 'me',         modifier: 'by-date' } },
  comment:   { filter: { type: 'comments',  scope: 'all-polis',  modifier: 'to-bless'} },
  // ...
}
```

Clicking an icon is **shorthand for "set the sentence to this and re-fire."** Both gestures — icon click and slot edit — produce the same PQL sentence, push the same URL, run the same fetch+render. The widget is the canonical compose surface; the icon row is a presets layer.

This is why the URL stays a meaningful sentence even when the user uses icons: the user isn't bypassing the language, they're picking phrases from it.

For the file-by-file walk of how the URL pushes through the parser into the controller, **dive into the [URL-as-filter tour](url-as-filter.md)**. It walks [`app.js`](github.com/vdibart/polis-cli/blob/main/webapp/internal/webui/www/app.js) (the URL intercept), [`pql.js`](github.com/vdibart/polis-cli/blob/main/webapp/internal/webui/www/pql.js) (the parser/composer), and [`stream.js`](github.com/vdibart/polis-cli/blob/main/cli-go/pkg/bundle/fixtures/pub.polis.core/shapes/v4/stream.js) (the consumer).

---

## The data — where the stream gets filled

A filter is only useful if there's content to filter against. The stream-screen's items don't come from a render-time fetch against the DS; they come from a **local JSONL cache** populated continuously by the webapp's background sync loop.

```
   DS (Postgres)
      │
      │  cursor-paginated events
      ▼
   webapp sync loop (every ~30s while a tab is open)
      │
      │  fan-out: feed / follow / blessing / notification handlers
      ▼
   .polis/ds/<discovery-domain>/pub.polis.core/state/pub.polis.feed.jsonl
      │
      │  read at fetch time, filtered by the active PQL sentence
      ▼
   stream-screen renders the column
```

When the user changes the filter, the controller re-fetches *from the local cache* — no DS roundtrip — because the relevant events are already there. The DS roundtrip is the continuous sync; the filter is a query over what's already been pulled.

A second, complementary data path runs alongside: **cross-tenant aggregation queries**. When a post from another tenant scrolls into view, the webapp's stream handler asks the DS "how many comments does this post have across the network?" via `POST /v1/content/comments/counts`. The first 10 items in any rendered page get blocking 500ms fetches; later items get fire-and-forget background fetches that warm a local cache for the next render. The badge numbers on cross-tenant items come from these aggregation queries — they don't fit the cursor-paginated event model because they're aggregations, not events.

For the file-by-file walk of both data paths, **dive into the [DS-to-stream tour](ds-to-stream.md)**. It walks the webapp sync loop ([`sync.go`](github.com/vdibart/polis-cli/blob/main/webapp/internal/server/sync.go)), the DS stream endpoint ([`stream.ts`](github.com/vdibart/polis-cli/blob/main/discovery-service/core/handlers/stream.ts)), the feed transformer ([`feed/handler.go`](github.com/vdibart/polis-cli/blob/main/cli-go/pkg/feed/handler.go)), the local cache ([`stream/store.go`](github.com/vdibart/polis-cli/blob/main/cli-go/pkg/stream/store.go)), the DS counts endpoint ([`counts.ts`](github.com/vdibart/polis-cli/blob/main/discovery-service/core/handlers/counts.ts)), and the webapp's stream HTTP handlers ([`handlers_stream.go`](github.com/vdibart/polis-cli/blob/main/webapp/internal/server/handlers_stream.go)).

---

## Where the two threads meet

The stream-screen renders **a single mixed list**:

- The items come from the **local cache** (filled by Path A of the DS-to-stream thread).
- The cross-tenant decorations (comment counts, blessing counts) come from **on-demand DS queries** (Path B of the DS-to-stream thread).
- The filter that decides *which* items appear comes from the **URL** (URL-as-filter thread).
- The chrome that lets the user change the filter — the icon row and the sentence-filter widget — sits in the topbar (also URL-as-filter thread, on its UI side).

```
     URL  ──►  PQL sentence  ──►  filter state  ──►  fetch from cache  ──►  render
                                                        │
                                  local JSONL  ◄────────┤
                                                        │
                                                        │ (decorate)
                                                        ▼
                                  DS comment-counts  ◄──┘
                                  (visible-horizon
                                   strategy)
```

The two threads converge on `stream.js` — the consumer of the URL and the renderer of the cached + decorated items. Read both tours and the convergence is what makes the third pass (the high-leverage move you can build from this code) feel natural.

---

## The design — why polis.pub is shaped this way

The stream isn't just the v4 shape, the filter widget, and the sync loop wired together. It's a **design statement** about what a social-network surface should be.

A few of the load-bearing claims:

- **The filter is a first-class citizen, in the URL, in human-readable form.** Tabs and modes hide their state; a sentence in the URL doesn't. Sharing a view is sharing a link. Discovering what someone else is looking at is reading their address bar.
- **One screen, many views.** The single stream-screen replaces what would otherwise be a Feed page, a Posts page, a Comments page, a Messages page, a People page, a Pending-Blessings page. Modal navigation was rejected because the underlying data is the same — the only thing that changes is *whose* and *which type* and *when*.
- **Continuous data, not push.** Polis avoids websockets and SSE-pushes-content. The webapp polls the DS every ~30 seconds and stages events in a local cache. The "live" feeling comes from the polling rhythm matching human attention rhythm — fast enough to surprise you with new content, slow enough to be cheap to operate.
- **Decentralized data, centralized aggregation.** Every site holds its own content; the DS knows about URLs + signatures + events; cross-tenant aggregations like comment counts are computed at query time off an indexed column, not maintained as a counter. The DS is *coordinator*, not *host*.
- **The shape is replaceable.** Sites that prefer per-post pages can stay on the blog shape (`pub.polis.shapes.v3`); the stream is one shape among possible others. The infinity stream is a *choice*, not a baked-in identity.

For the full statement of these principles, **read the concept doc**: [`docs/general/infinity-stream.md`](../general/infinity-stream.md). It elaborates the *why* in a way this tour deliberately doesn't, because the tour is about how the code expresses the design — the concept doc is about the design itself.

---

## Pulling the threads further

This overview points at three layers of deeper reading. Pick what matches your question:

### Source-of-truth references (the "what")

- [`../general/pql.md`](../general/pql.md) — Polis Query Language. The grammar (qualifier · type · "from" · scope · modifier), the URL encoding, the type-conditional rules, the sentence-filter widget's slot model. Authoritative spec.
- [`../general/infinity-stream.md`](../general/infinity-stream.md) — The shape's design philosophy. What "infinity" means, why one screen, the three POVs the same shape serves (owner / network member / public visitor).
- [`../general/shapes.md`](../general/shapes.md) — Blog vs stream as a binary architectural choice. What a shape ships. The theme-overrides-shape render lookup.
- [`../general/content-types.md`](../general/content-types.md) — What the events in the stream actually *contain*. Each event maps to a content-type action; the same content-type model that powers publishing powers the feed.
- [`../ds/developer/stream-architecture.md`](../ds/developer/stream-architecture.md) — DS event stream design: schemas, cursor semantics, event categories, the unified-endpoint filter composition.
- [`../ds/developer/api-reference.md`](../ds/developer/api-reference.md) — Exact wire format for `/v1/stream`, `/v1/stream/unified`, `/v1/content/comments/counts`.

### Deep tours (the "how")

- [`url-as-filter.md`](url-as-filter.md) — The URL ↔ sentence ↔ filter-state path through `app.js` → `pql.js` → `stream.js`.
- [`ds-to-stream.md`](ds-to-stream.md) — The two data paths: continuous sync (events → cache → render) and on-demand aggregation (visible-horizon counts stamping).

### Architectural context (the "where")

- [`../general/architecture.md`](../general/architecture.md) — The four-surface map. Where the stream-screen sits inside the polis CLI / webapp / polis.pub / DS picture.
- [`../general/snap-off-architecture.md`](../general/snap-off-architecture.md) — Why every layer of the stream is replaceable. The shape can swap. The DS can swap. The webapp can swap. The contracts hold.

---

## A recipe that uses the composition

If you want to do something concrete that touches both threads, the canonical exercise is **adding a new icon-row preset**:

1. Decide the sentence — say, **all comments from my mutuals by date** (a "what my close circle is saying" view).
2. Pick an icon (or design one).
3. Add the preset to `owner-extras.js` ICON_ROW_PRESETS (the gateway/paragraph/comment/etc. table). Its `filter` field is a PQL filter-state object; the widget's `setFilter` API consumes it directly.
4. Add the icon to `index.html`'s icon-row (with an `id="nav-btn-<name>"` and an SVG icon).
5. If the new sentence requires a vocabulary token that doesn't exist yet (e.g., a new `scope` value), add it to `pql.js`'s lookup tables.
6. If the new sentence needs server-side filter support (a new comparison the local cache or the DS doesn't know about), extend the feed handler or the DS query.

That recipe touches the URL-as-filter thread (the PQL vocabulary + icon-as-preset wiring) and, depending on the sentence, the DS-to-stream thread (if the data fetch needs to extend). Both tours' files become natural reading.

---

## What you should now understand

If you read this tour and the two child tours:

- The stream-screen is **four layers composed**: shape, filter, data, chrome. They're loosely coupled — you can change one without rewriting the others.
- The **filter is the URL**, and the URL is human-readable PQL. The icon-row and the sentence-filter widget are two compose surfaces over the same grammar.
- The **data is staged locally** by a continuous sync loop; the filter is a query over that cache, not over the DS directly. The DS gets a roundtrip only for the periodic sync and for cross-tenant aggregations the cache can't answer.
- The **design is a statement**: one screen, sentence-as-URL, continuous-not-pushed, decentralized-with-centralized-aggregation, shape-is-replaceable.
- Both [`url-as-filter.md`](url-as-filter.md) and [`ds-to-stream.md`](ds-to-stream.md) are *deep dives* into specific pieces of this composition. Read them when you want the file-by-file walk; come back here for the synthesis.

If you want to go deeper:

- The PQL spec: [`../general/pql.md`](../general/pql.md)
- The infinity stream design: [`../general/infinity-stream.md`](../general/infinity-stream.md)
- The DS event stream architecture: [`../ds/developer/stream-architecture.md`](../ds/developer/stream-architecture.md)
- The starter map of every thread: [`../../AGENTS.md`](../../AGENTS.md)
