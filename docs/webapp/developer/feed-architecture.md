# Feed Architecture

*For* [Contributors](../../README.md#contributing-to-polis) — *Kind* [Reference](../../README.md#kinds-of-page) — *Component* [Webapp](../README.md) — *Code* [`cli-go/pkg/feed`](../../../cli-go/pkg/feed) — *See also* [tour](../../handbook/ds-to-stream.md)

⚠️ **The owner app this cache was built for is replaced.** Since 2026-11-22 every site's owner app is the **v5 app** (at `/_/`, on `/v1` only), and every site on polis.pub is on the **v5 shape**; the previous app (its logged-in stream) is deleted, and the stream shape is no longer supported though a site that records it still renders as before. The cache and the `/pql/` pipeline below are current: the v5 app's lists are PQL sentences fetched through `/v1/query`, which runs the site's own `/pql/` handler. For the current model see [shapes](../../general/concepts/shapes.md) and [architecture](../../general/concepts/architecture.md).

The feed is a **local-first** system that caches content from the Discovery Service (DS) stream into JSONL files on disk. Filtering and read tracking run in the local webapp against that cache — no DS round trip at read time — and the browser renders the result.

## Data Flow

```
DS Stream (live events)
  ↓ unified sync (runUnifiedSync → feedSyncHandler) — event types + followed/involved domains
FeedHandler.Process() — event→item conversion, self-event filter
  ↓
CacheManager.MergeItems() — dedup, sort, per-type prune
  ↓
state/pub.polis.feed.jsonl (JSONL cache)
  ↓ GET /pql/<sentence> (or /v1/query?pql=…) → stream-items pipeline (handlers_stream.go) — scope, type, time and read filters, pagination
the owner app's sections (gather.js, write.js), which fetch the sentence through /v1/query (api.js) and render it
```

`FeedHandler` does not evaluate policy: policy governs network engagement (DM acceptance, blessing decisions), not what the feed displays.

The old app's feed routes (`/api/feed`, `/api/feed/grouped`, `/api/feed/refresh`, `/api/feed/read`) went with it.

## Scopes

The feed cache has four scope keys. Two are **materialized** (separate JSONL + cursor) and two are **runtime-filtered** (predicates over the network cache). The stream's PQL scopes map onto them: `my-network` → `network`, `all-polis` → `global` unioned with `network`, `my-mutuals` / `@handle` → author filters over `network`. `my` (and `@<your own handle>`) does not read the cache at all: the unified sync skips self-authored events, so the stream loads your own content from disk instead.

| Scope | Type | Actors | File | Cursor Key |
|---|---|---|---|---|
| `network` (default) | materialized | Followed domains | `pub.polis.feed.jsonl` | `pub.polis.feed` |
| `global` | materialized | None (all of polis) | `pub.polis.feed.global.jsonl` | `pub.polis.feed.global` |
| `followers` | runtime filter | Follower domains | (network cache) | (network cursor) |
| `me` | runtime filter | Self | (network cache) | (network cursor) |

All materialized files live in `.polis/ds/<ds-domain>/pub.polis.core/state/`.

**Why two types?** The `global` scope contains posts from domains you don't follow — data outside the unified sync boundary that can't be derived from the network cache. `followers` is a strict subset of the network cache. ⚠️ `me` is still a runtime filter (`feedFilterForScope`), but the unified sync skips self-authored events, so over the cache it returns nothing; your own content is read from disk instead. Materializing subsets as separate files would duplicate data, drift cursors, and create a new file for every future filter type. Runtime filtering over ~500 cached items is sub-millisecond.

Runtime-filtered scopes use `FilterOptions.AuthorDomains` in `ListFiltered()` to restrict results by author domain at read time. The `feedFilterForScope()` helper populates this from the follower list or own domain.

## Content Types

Feed items have a `type` field and an `event_type` field:

| type | event_type | Description |
|---|---|---|
| `post` | `pub.polis.post.published` | Published post |
| `post` | `pub.polis.post.republished` | Updated post |
| `comment` | `pub.polis.comment.published` | Published comment |
| `comment` | `pub.polis.comment.republished` | Updated comment |
| `announcement` | `pub.polis.comment.blessing.granted` | Comment blessed |
| `announcement` | `pub.polis.comment.blessing.requested` | Blessing requested |
| `announcement` | `pub.polis.follow.announced` | New follow |
| `announcement` | `pub.polis.site.registered` | New site registration |

The `event_type` preserves the original DS event type for rendering. The `type` is used for filtering and retention bucketing.

## Per-Type Retention Limits

Each content type has its own count cap and age limit, preventing announcement storms from displacing posts/comments.

### Default Retention

| Posts | Comments | Announcements | Post/Comment MaxAge | Announcement MaxAge |
|---|---|---|---|---|
| 300 | 150 | 50 | 90 days | 14 days |

The limits come from `DefaultFeedConfig()`, overridable in `config/feed.json`. ⚠️ Both materialized caches read the **same** config file, so `global` has the same limits as `network` — there is no tighter global retention. Runtime-filtered scopes (`followers`, `me`) read the network cache, so its retention applies.

### Why Per-Type Limits

Announcement events (follows, blessings, registrations) are more frequent than content events. Without separate caps, a single author following 50 people in one day would consume 50 cache slots that could hold posts. Per-type budgets guarantee content visibility regardless of announcement volume.

### Every Followed Site Keeps Its Newest Posts

The posts cap is shared by every site the owner follows, so one site that posts every two hours would fill 300 slots
on its own within a month and evict everyone else. When the cap is cut, each followed site keeps its newest posts
(`PostsKeptPerSite`, 10) and the rest of the cap goes to the newest posts overall. The floors together never exceed
the cap: with more sites than the cap holds at 10 each, every site's floor shrinks to its share, so the cache is
never larger than `max_posts`. A sync whose items the prune drops still advances its cursor.

### Why Announcements Age Out Faster

A follow from 60 days ago isn't interesting; a post from 60 days ago might be. The 14-day announcement TTL keeps announcements fresh and prevents stale accumulation.

## Design Decisions

### Materialized vs. runtime-filtered scopes

A scope needs its own JSONL file only if it contains data **outside the unified sync boundary**. The global scope queries the DS with no actor filter, returning posts from domains the user doesn't follow — this data isn't in the network cache.

Scopes that are subsets of the network cache (followers, me, and any future filters like "mutual follows" or text search) are runtime filters applied at read time against the single network cache. This prevents file proliferation: new filter types require zero new state files, just a predicate in `FilterOptions`.

### "All of polis" was restricted to the last 24 hours

Unbounded global queries could fetch thousands of events on a busy DS, so the global sync set the DS `created_after` parameter to 24 hours ago, a server-side timestamp floor that avoids paginating through old history. That sync went with the old app (below); the DS client still takes the parameter (`CreatedAfter`).

### Activity stream consolidated into feed cache

Previously, the "All" feed tab merged two data sources: cached feed items (posts/comments from JSONL) and a live `/api/activity` endpoint (follows, blessings, registrations queried from DS on every page load). Activity events were ephemeral — the cursor reset on page refresh, had no read tracking, and followed a completely different data path.

This created real problems:
- Read state didn't work on activity events (no `read_at` field)
- Time filtering was inconsistent (cached vs live data)
- Scope changes required two separate mechanisms
- The staleness indicator only covered feed cache

Resolution: all event types now flow through the feed cache, giving uniform filtering, read tracking, and scope support. `/api/activity` was removed.

### Only the network cache is synced

Background sync (30-second ticker, only while a browser tab is connected) is reserved for the network cache, through the unified sync. Runtime-filtered scopes (followers, me) use it too, since their data is already in the network cache. The global scope was synced on demand only, from the old app's refresh route; that route and its sync are gone, so ⚠️ **nothing now writes the global cache**: `all polis` serves whatever the global cache already holds, unioned with the network cache.

## Key Files

| File | Purpose |
|---|---|
| `cli-go/pkg/feed/cache.go` | CacheManager, CachedFeedItem, FeedConfig, FilterOptions, per-type prune |
| `cli-go/pkg/feed/handler.go` | FeedHandler: DS events → FeedItems |
| `cli-go/pkg/feed/feed.go` | FeedItem struct |
| `webapp/internal/server/server.go` | syncFeed(), feedCacheForScope(), feedFilterForScope() |
| `webapp/internal/server/sync.go` | runUnifiedSync(), feedSyncHandler |
| `webapp/internal/server/handlers_stream.go` | stream-items pipeline behind `/pql/`, streamCacheForScope() |

## DS Index

The composite index `idx_ds_events_type_created_at ON ds_events(type, created_at)` supports global scope queries that combine type filtering with a timestamp floor (`type = ANY(...) AND created_at >= ...`).
