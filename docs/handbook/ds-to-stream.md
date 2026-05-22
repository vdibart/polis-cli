# Tour: DS to stream

> A guided tour of how content flows from the Discovery Service into the stream-screen you see in your browser. Source-of-truth concept docs live in [`../general/`](../general/); this tour walks the source code with you. Map of all threads: [`../../AGENTS.md`](../../AGENTS.md).
>
> **Scope note.** The DS-side files referenced in this tour (e.g. `discovery-service/core/handlers/stream.ts`, `counts.ts`) are not part of this public repo. The DS reference implementation is planned for open-source release; until then, the [DS API reference](../ds/developer/api-reference.md) and [stream architecture doc](../ds/developer/stream-architecture.md) describe the same behavior as a stable public contract. Webapp- and CLI-side files in this tour are all in this repo and clickable.

## The observation

Open the stream-screen on your polis.pub site. Wait a minute. New items appear — a post from someone you follow, a blessing on a comment you made, a new follower notification. You didn't refresh. You didn't poll anything yourself. The DS didn't push anything to your browser. Yet the items show up.

Now scroll. As posts from other people's sites scroll into view, each one has a comment-count badge that reflects the comments that *site* has on that post — not what's in your local data. Your site has no idea how many comments `bob.polis.pub` has on his own posts, but the count is there.

How does either of these work?

## Two paths of data

The infinity stream draws content from the DS through **two distinct paths** that meet in the rendered view:

```
   ┌──────────────────────────────────────────────────────────────────────┐
   │                                                                      │
   │   Path A — continuous sync (background, push-ish)                    │
   │   ────────────────────────────────────────────                       │
   │                                                                      │
   │   webapp sync loop (every ~30s while a tab is open)                  │
   │       │                                                              │
   │       │ HTTP GET /v1/stream/unified?since=<cursor>&...                │
   │       ▼                                                              │
   │   DS stream endpoint  ──────►  Postgres                              │
   │       │                                                              │
   │       │ events[] (typed, cursor-paginated)                           │
   │       ▼                                                              │
   │   handler fan-out (feed / follow / blessing / notification)          │
   │       │                                                              │
   │       │ writes local state                                           │
   │       ▼                                                              │
   │   .polis/ds/<domain>/pub.polis.core/state/*.jsonl + cursors.json     │
   │       │                                                              │
   │       │ SPA reads via local /api/feed etc.                           │
   │       ▼                                                              │
   │   stream.js renders new items                                        │
   │                                                                      │
   │   ────────────────────────────────────────────                       │
   │                                                                      │
   │   Path B — on-demand aggregation (pull-ish, render-driven)           │
   │   ────────────────────────────────────────────────                   │
   │                                                                      │
   │   stream item enters viewport (or is in the visible horizon)         │
   │       │                                                              │
   │       │ webapp batches the URLs                                      │
   │       ▼                                                              │
   │   webapp stream handler  ──HTTP POST──►  DS /v1/content/comments/counts │
   │       │                                                              │
   │       │ counts keyed by URL                                          │
   │       ▼                                                              │
   │   stream items decorate with comment-count badges                    │
   │                                                                      │
   └──────────────────────────────────────────────────────────────────────┘
```

Path A is *push-ish* — your webapp polls the DS regularly and grows local state. Path B is *pull-ish* — your webapp asks the DS specific aggregation questions on demand as the user scrolls. Both originate at the DS, both end at the rendered view.

We'll walk each path through the source.

---

## Path A — continuous sync

### A.1 Producer: [`webapp/internal/server/sync.go`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/server/sync.go)

The webapp runs a background sync goroutine that ticks every ~30 seconds whenever at least one browser tab has an SSE connection open. The core entry point is `runUnifiedSync()`:

```go
// runUnifiedSync performs a single unified sync cycle: queries the DS stream
// with a single cursor, fans out events to all registered handlers, then
// advances the cursor and renders if needed.
func (s *Server) runUnifiedSync() SyncResult { ... }
```

It uses a *single cursor* (`pub.polis.sync`) to fetch every relevant event from the DS in one HTTP call — events involving this domain (target or source) plus events from followed authors. The previous design used three separate queries; the current unified endpoint replaces them. Cursor lives at `.polis/ds/<discovery-domain>/pub.polis.core/state/cursors.json`.

Each cycle emits two structured log events for observability:
- `pub.polis.sync.start` — `{sync_id, cursor, handler_count}`
- `pub.polis.sync.complete` — `{sync_id, cursor_before, cursor_after, events_processed, new_notifications, duration_ms}`

The `sync_id` correlates all DS HTTP calls within a single cycle via the `X-Request-Id` header.

### A.2 DS endpoint: [`discovery-service/core/handlers/stream.ts`](https://github.com/vdibart/polis-cli/blob/main/discovery-service/core/handlers/stream.ts)

The webapp's sync hits the DS at `/v1/stream/unified?since=<cursor>&involved=<my-domain>&actor=<followed-domains>`. Server-side, this lands in `queryStream` (and `queryStreamUnified` for the multi-filter variant):

```typescript
export async function queryStream(
  storage: StorageAdapter,
  filters: StreamQueryFilters,
  auth: AuthContext,
): Promise<StreamQueryResult> {
  const adjustedFilters = {
    ...filters,
    limit: filters.limit + 1,
    visibleDenialsDomain: auth.authenticated ? auth.domain : null,
  };
  const events = await storage.queryEvents(adjustedFilters);
  // ...
  return { events: page, cursor, has_more: hasMore };
}
```

Two things to notice. First, the +1 limit trick: the handler fetches one extra row to detect `has_more` without a separate count query. Second, `visibleDenialsDomain` — when the caller is authenticated, the storage adapter knows to surface the caller's own denial events (you can see *your* denied comments; nobody else can see them). Authorization is pushed into the SQL, not enforced after the fact.

Cursor pagination is the contract: the response carries the cursor of the last returned event; the next call passes that cursor as `since`.

### A.3 Handlers: [`cli-go/pkg/feed/handler.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/feed/handler.go) (and siblings)

Back on the webapp side, the events returned by the DS go through a fan-out. `runUnifiedSync` knows about four handlers, each one a small interface implementation registered in `sync.go`:

- `feedSyncHandler` — wraps `cli-go/pkg/feed`'s `FeedHandler` to convert post/comment/follow events into `FeedItem` rows for the cache.
- `followSyncHandler` — wraps `cli-go/pkg/following` to reconcile your local follower set with DS follow events.
- `blessingSyncHandler` — wraps `cli-go/pkg/blessing` to reconcile blessing state.
- `notificationSyncHandler` — wraps `cli-go/pkg/notification` to add notification entries.

Each handler declares `EventTypes()` (which event names it consumes) and `Process(events)` (what to do with them). The fan-out is type-routed: each event is sent only to handlers that asked for it.

The feed handler is representative:

```go
// FeedHandler transforms discovery stream events into FeedItems.
// It filters self-authored events (unless IncludeSelf is set) and maps
// event payloads to the common FeedItem structure.
type FeedHandler struct {
    MyDomain        string
    FollowedDomains map[string]bool
    IncludeSelf     bool
}

func (h *FeedHandler) Process(events []discovery.StreamEvent) []FeedItem { ... }
```

The output `FeedItem`s are what eventually land in the local cache as JSONL.

### A.4 Storage: [`cli-go/pkg/stream/store.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/stream/store.go)

The local state and cursor file are managed by `stream.Store`, scoped per discovery service:

```go
// Root: .polis/ds/<discovery-service-domain>/<bundle-name>/
// Directories:
//   config/    — user preferences (notification rules, feed settings); survives resets
//   state/     — computed/derived data (followers, blessings, cursors, notifications, feed cache)
```

The split is deliberate. `config/` carries things you set ("notify me when X happens"); `state/` is computed from DS events. You can delete the entire `state/` directory and your next sync rebuilds it from scratch — the DS still has the events; cursors restart from `0`.

The materialized files match cursor keys: cursor `pub.polis.feed` → file `pub.polis.feed.jsonl`. The exception is feed *scopes* — `network` and `global` are separate cached files (the global scope has its own cursor because it's a different DS query), but `followers` and `me` are computed at render time by filtering the network cache via `FilterOptions.AuthorDomains`.

### A.5 Consumer: [`webapp/internal/webui/www/app.js`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/webui/www/app.js) → [`stream.js`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/bundle/fixtures/pub.polis.core/shapes/v4/stream.js)

When the SPA renders the stream-screen, it fetches from the webapp's local API (`/api/feed?...`), which reads from the JSONL cache populated by the sync loop. The PQL sentence in the URL becomes the filter applied at fetch time. See the [URL-as-filter tour](url-as-filter.md) for that side of the story.

New events arriving during a session are surfaced as the next render cycle picks them up. The SSE channel that keeps the sync loop alive also nudges the SPA to re-query when new data is known to be ready.

---

## Path B — on-demand aggregation

### B.1 The need

A post in your stream from `bob.polis.pub` shows a comment-count badge: "12 comments." Your site doesn't have Bob's comments locally. Your local feed cache only knows about events your site has registered for. Where does the 12 come from?

It comes from a cross-tenant aggregation query — Path B.

### B.2 Webapp side: [`webapp/internal/server/handlers_stream.go`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/server/handlers_stream.go)

The webapp stream handler stamps comment counts onto items at render time. It uses a **visible-horizon strategy**:

```go
// items[0:visibleHorizon] in the paginated page are treated as "likely visible
// above the fold" and get a sync DS fetch with a strict timeout; items past
// the horizon get a fire-and-forget background fetch that populates the cache
// for the next render.
const (
    visibleHorizon              = 10
    visibleCountFetchTimeout    = 500 * time.Millisecond
    backgroundCountFetchTimeout = 5 * time.Second
)
```

The first 10 items get blocking 500ms fetches — they're about to render and need their counts now or they ship as "0." Items past that horizon get fire-and-forget background fetches that populate a local short-lived cache so the *next* time the user scrolls them into view, the badge already has its number.

### B.3 DS endpoint: [`discovery-service/core/handlers/counts.ts`](https://github.com/vdibart/polis-cli/blob/main/discovery-service/core/handlers/counts.ts)

The webapp batches up to 50 URLs into a single `POST /v1/content/comments/counts` request:

```json
// Request
{
  "urls": [
    "https://alice.com/posts/2026/04/hello.md",
    "https://bob.com/posts/2026/04/welcome.md"
  ]
}

// Response
{
  "counts": {
    "https://alice.com/posts/2026/04/hello.md": { "total": 7, "blessed": 3 },
    "https://bob.com/posts/2026/04/welcome.md": { "total": 2, "blessed": 2 }
  }
}
```

The handler's design intent is documented at the top of `counts.ts`:

> Aggregates total comment counts per target post URL. Used by the webapp's stream view to populate the cross-tenant comment-count badge on each post entry. The badge would otherwise always show "0" for posts authored by other tenants, since each tenant only knows its own local blessed-comment state.
>
> Design: keep it stateless and query-time aggregated. No materialized counter table to maintain. The partial expression index `idx_ds_content_metadata_in_reply_to_active` makes the aggregation cheap (single index scan per batch).

Two takeaways. First, this endpoint exists specifically because polis content is decentralized — without the DS as aggregator, no single site can compute network-wide counts. Second, the design is intentionally *not* a materialized counter — counts are recalculated at query time off an indexed column, which keeps the DS schema simple and avoids the consistency bugs that come with maintained counters.

### B.4 Tying back to the stream

The webapp's stream handler merges the counts into the page response. The SPA's renderer reads `meta.comment_count` and `meta.blessed_count` from each item and draws the badge. From the SPA's perspective, it never knows whether the count came from local cache or a live DS query — it just gets numbers.

---

## Where the two paths meet

The stream-screen renders **a single mixed list**:

- The items themselves come from the local feed cache populated by Path A (continuous sync).
- The cross-tenant decorations (comment counts, blessing counts) come from Path B (aggregation).

The PQL sentence in the URL filters the items (see the [URL-as-filter tour](url-as-filter.md)). When you change the filter, the items re-fetch from local cache (no DS roundtrip — the cache already has them); Path B fires again to top up counts for items that slid into the new view.

This is why polis.pub feels live without feeling expensive. The continuous data is pre-staged locally; the decorative-but-network-wide bits are batched and tied to viewport visibility.

---

## Pull the thread

Five concept docs deepen what you just walked:

- **[`../general/architecture.md`](../general/architecture.md)** — Where the DS sits in the four-surface model, and what its contract is with sites. The DS is a *coordinator*, not a host; understanding why is the foundational piece.
- **[`../general/infinity-stream.md`](../general/infinity-stream.md)** — The shape that this whole data flow exists to feed. The stream-screen is what's rendered; this tour is how it stays fresh.
- **[`../general/content-types.md`](../general/content-types.md)** — What's actually *in* the stream events. Each event corresponds to a content-type action; the same content-type model that powers publishing on one side powers the feed on the other.
- **[`../ds/developer/stream-architecture.md`](../ds/developer/stream-architecture.md)** — The DS event stream's design in depth: schemas, cursor semantics, event categories, how the unified endpoint composes filters.
- **[`../ds/developer/api-reference.md`](../ds/developer/api-reference.md)** — The exact wire format for every endpoint touched in this tour (`/v1/stream/unified`, `/v1/content/comments/counts`).

And one architecture-of-concern doc:

- **[`../general/snap-off-architecture.md`](../general/snap-off-architecture.md)** — The DS is a *replaceable layer*. The contract this tour describes — event stream + cursor pagination + counts aggregation — is the contract any DS implementation must speak. A self-hosted DS, a community DS, a regional DS all participate in the same flow you just walked.

## What you should now understand

If you followed the tour end-to-end:

- The DS doesn't push to your browser. The webapp pulls. The continuous "your stream stays current" feeling is a 30-second polling loop, not a websocket.
- The webapp's local JSONL cache is the source of truth for what your stream shows. The cache is rebuilt from DS events, so it's deletable / regeneratable without data loss.
- Cross-tenant data (comment counts, blessing counts) that no individual site can know is exposed via batched aggregation queries on the DS, with a viewport-horizon strategy that keeps visible items fast and off-screen items eventually-consistent.
- Cursor-paginated events + on-demand aggregation queries together compose the "live network feed" experience without any actual real-time push infrastructure.
- The PQL sentence-as-URL filter (see [URL-as-filter](url-as-filter.md)) decides *which* items render; this tour is about how the items *get there*.

If you want to go deeper:

- The starter map of every thread: [`../../AGENTS.md`](../../AGENTS.md)
- The stream's DOM/scroll/URL behaviors: [`url-as-filter.md`](url-as-filter.md)
- The DS as a replaceable layer: [`../general/snap-off-architecture.md`](../general/snap-off-architecture.md)
- The full DS REST surface: [`../ds/developer/api-reference.md`](../ds/developer/api-reference.md)
