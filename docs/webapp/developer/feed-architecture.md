# Feed Architecture

*For* [Contributors](../../README.md#contributing-to-polis) — *Kind* [Reference](../../README.md#kinds-of-page) — *Component* [Webapp](../README.md) — *Code* [`cli-go/pkg/feed`](../../../cli-go/pkg/feed) — *See also* [tour](../../handbook/ds-to-stream.md)

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
  ↓ GET /pql/<sentence> → stream-items pipeline (handlers_stream.go) — scope, type, time and read filters, pagination
v4 stream controller (stream.js) — renders the page
```

`FeedHandler` does not evaluate policy: policy governs network engagement (DM acceptance, blessing decisions), not what the feed displays.

The pre-v4 endpoints `/api/feed` and `/api/feed/grouped` are still registered, but the SPA no longer calls them.

## Scopes

The feed cache has four scope keys. Two are **materialized** (separate JSONL + cursor) and two are **runtime-filtered** (predicates over the network cache). The stream's PQL scopes map onto them: `my-network` → `network`, `all-polis` → `global` unioned with `network`, `my-mutuals` / `@handle` → author filters over `network`. `my` (and `@<your own handle>`) does not read the cache at all: the unified sync skips self-authored events, so the stream loads your own content from disk instead.

| Scope | Type | Actors | File | Cursor Key |
|---|---|---|---|---|
| `network` (default) | materialized | Followed domains | `pub.polis.feed.jsonl` | `pub.polis.feed` |
| `global` | materialized | None (all of polis) | `pub.polis.feed.global.jsonl` | `pub.polis.feed.global` |
| `followers` | runtime filter | Follower domains | (network cache) | (network cursor) |
| `me` | runtime filter | Self | (network cache) | (network cursor) |

All materialized files live in `.polis/ds/<ds-domain>/pub.polis.core/state/`.

**Why two types?** The `global` scope contains posts from domains you don't follow — data outside the unified sync boundary that can't be derived from the network cache. `followers` is a strict subset of the network cache. ⚠️ `me` is kept as a runtime filter for the pre-v4 `/api/feed`, but the unified sync skips self-authored events, so over the cache it returns nothing — the v4 stream reads your own content from disk instead. Materializing subsets as separate files would duplicate data, drift cursors, and create a new file for every future filter type. Runtime filtering over ~500 cached items is sub-millisecond.

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

The limits come from `DefaultFeedConfig()`, overridable in `config/feed.json`. ⚠️ Both materialized caches read the **same** config file, so `global` has the same limits as `network` — there is no tighter global retention. Its volume is bounded instead by the 24-hour query window below. Runtime-filtered scopes (`followers`, `me`) read the network cache, so its retention applies.

### Why Per-Type Limits

Announcement events (follows, blessings, registrations) are more frequent than content events. Without separate caps, a single author following 50 people in one day would consume 50 cache slots that could hold posts. Per-type budgets guarantee content visibility regardless of announcement volume.

### Why Announcements Age Out Faster

A follow from 60 days ago isn't interesting; a post from 60 days ago might be. The 14-day announcement TTL keeps announcements fresh and prevents stale accumulation.

## Design Decisions

### Materialized vs. runtime-filtered scopes

A scope needs its own JSONL file only if it contains data **outside the unified sync boundary**. The global scope queries the DS with no actor filter, returning posts from domains the user doesn't follow — this data isn't in the network cache.

Scopes that are subsets of the network cache (followers, me, and any future filters like "mutual follows" or text search) are runtime filters applied at read time against the single network cache. This prevents file proliferation: new filter types require zero new state files, just a predicate in `FilterOptions`.

### "All of polis" restricted to the last 24 hours

Unbounded global queries could fetch thousands of events on a busy DS. Bounding by time keeps volume predictable and sync fast. The global sync sets the DS `created_after` parameter to 24 hours ago, a server-side timestamp floor that avoids paginating through old history.

### Activity stream consolidated into feed cache

Previously, the "All" feed tab merged two data sources: cached feed items (posts/comments from JSONL) and a live `/api/activity` endpoint (follows, blessings, registrations queried from DS on every page load). Activity events were ephemeral — the cursor reset on page refresh, had no read tracking, and followed a completely different data path.

This created real problems:
- Read state didn't work on activity events (no `read_at` field)
- Time filtering was inconsistent (cached vs live data)
- Scope changes required two separate mechanisms
- The staleness indicator only covered feed cache

Resolution: all event types now flow through the feed cache, giving uniform filtering, read tracking, and scope support. `/api/activity` was removed.

### Global scope syncs on demand

Background sync (30-second ticker, only while a browser tab is connected) is reserved for the network cache, through the unified sync. The global scope syncs **on demand only** — through `syncFeedScoped("global")`, whose one caller is `POST /api/feed/refresh?scope=global`. This reduces DS query volume and avoids syncing data the user may never look at. Runtime-filtered scopes (followers, me) use the unified sync since their data is already in the network cache.

⚠️ The v4 SPA does not call `/api/feed/refresh`, so nothing in the current UI triggers a global sync; `all polis` serves whatever the global cache already holds, unioned with the network cache.

## Key Files

| File | Purpose |
|---|---|
| `cli-go/pkg/feed/cache.go` | CacheManager, CachedFeedItem, FeedConfig, FilterOptions, per-type prune |
| `cli-go/pkg/feed/handler.go` | FeedHandler: DS events → FeedItems |
| `cli-go/pkg/feed/feed.go` | FeedItem struct |
| `webapp/internal/server/server.go` | syncFeed(), syncFeedScoped(), feedCacheForScope(), feedFilterForScope() |
| `webapp/internal/server/sync.go` | runUnifiedSync(), feedSyncHandler |
| `webapp/internal/server/handlers_stream.go` | stream-items pipeline behind `/pql/`, streamCacheForScope() |
| `webapp/internal/server/handlers.go` | /api/feed, /api/feed/grouped (pre-v4), /api/feed/refresh, /api/feed/read |

## DS Index

The composite index `idx_ds_events_type_created_at ON ds_events(type, created_at)` supports global scope queries that combine type filtering with a timestamp floor (`type = ANY(...) AND created_at >= ...`).
