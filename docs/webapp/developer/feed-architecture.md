# Feed Architecture

The feed is a **local-first** system that caches content from the Discovery Service (DS) stream into JSONL files on disk. All filtering, read tracking, and rendering happens client-side against the local cache.

## Data Flow

```
DS Stream (live events)
  ↓ syncFeed() — filtered by event types + actor domains
FeedHandler.Process() — policy eval, event→item conversion
  ↓
CacheManager.MergeItems() — dedup, sort, per-type prune
  ↓
state/pub.polis.feed.jsonl (JSONL cache)
  ↓ /api/feed/grouped — read from cache, group, respond
Frontend — client-side time/type/read filtering
```

## Scopes

The feed supports four scopes. Two are **materialized** (separate JSONL + cursor) and two are **runtime-filtered** (predicates over the network cache):

| Scope | Type | Actors | File | Cursor Key |
|---|---|---|---|---|
| `network` (default) | materialized | Followed domains | `pub.polis.feed.jsonl` | `pub.polis.feed` |
| `global` | materialized | None (all of polis) | `pub.polis.feed.global.jsonl` | `pub.polis.feed.global` |
| `followers` | runtime filter | Follower domains | (network cache) | (network cursor) |
| `me` | runtime filter | Self | (network cache) | (network cursor) |

All materialized files live in `.polis/ds/<ds-domain>/pub.polis.core/state/`.

**Why two types?** The `global` scope contains posts from domains you don't follow — data outside the unified sync boundary that can't be derived from the network cache. `followers` and `me` are strict subsets of the network cache (every follower's or self-authored post is already fetched by the unified sync). Materializing subsets as separate files would duplicate data, drift cursors, and create a new file for every future filter type. Runtime filtering over ~500 cached items is sub-millisecond.

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

### Default Retention by Scope

| Scope | Posts | Comments | Announcements | Post/Comment MaxAge | Announcement MaxAge |
|---|---|---|---|---|---|
| network | 300 | 150 | 50 | 90 days | 14 days |
| global | 100 | 50 | 30 | 7 days | 2 days |

Runtime-filtered scopes (`followers`, `me`) inherit the network scope's retention since they read from the same cache.

### Why Per-Type Limits

Announcement events (follows, blessings, registrations) are more frequent than content events. Without separate caps, a single author following 50 people in one day would consume 50 cache slots that could hold posts. Per-type budgets guarantee content visibility regardless of announcement volume.

### Why Announcements Age Out Faster

A follow from 60 days ago isn't interesting; a post from 60 days ago might be. The 14-day announcement TTL keeps announcements fresh and prevents stale accumulation.

## Design Decisions

### Materialized vs. runtime-filtered scopes

A scope needs its own JSONL file only if it contains data **outside the unified sync boundary**. The global scope queries the DS with no actor filter, returning posts from domains the user doesn't follow — this data isn't in the network cache.

Scopes that are subsets of the network cache (followers, me, and any future filters like "mutual follows" or text search) are runtime filters applied at read time against the single network cache. This prevents file proliferation: new filter types require zero new state files, just a predicate in `FilterOptions`.

### "All of polis" restricted to "last hour" and "last day"

Unbounded global queries could fetch thousands of events on a busy DS. Bounding by time keeps volume predictable and sync fast. The DS `created_after` parameter provides a server-side timestamp floor to avoid paginating through old history.

Expand later based on user feedback.

### Activity stream consolidated into feed cache

Previously, the "All" feed tab merged two data sources: cached feed items (posts/comments from JSONL) and a live `/api/activity` endpoint (follows, blessings, registrations queried from DS on every page load). Activity events were ephemeral — the cursor reset on page refresh, had no read tracking, and followed a completely different data path.

This created real problems:
- Read state didn't work on activity events (no `read_at` field)
- Time filtering was inconsistent (cached vs live data)
- Scope changes required two separate mechanisms
- The staleness indicator only covered feed cache

Resolution: all event types now flow through the feed cache, giving uniform filtering, read tracking, and scope support. `/api/activity` was removed.

### Global scope syncs lazily

Background sync (30-second ticker) is reserved for the primary "my network" scope. The global scope syncs **on-demand only** — when the user switches to that scope and the cache is stale. This reduces DS query volume and avoids syncing data the user may never look at. Runtime-filtered scopes (followers, me) use the unified sync since their data is already in the network cache.

### Global scope uses tighter retention

Global content is exploratory, not the user's primary feed. Tighter bounds (100 posts, 50 comments, 30 announcements, 7-day/2-day age limits) keep disk usage and sync volume low.

## Key Files

| File | Purpose |
|---|---|
| `cli-go/pkg/feed/cache.go` | CacheManager, CachedFeedItem, FeedConfig, FilterOptions, per-type prune |
| `cli-go/pkg/feed/handler.go` | FeedHandler: DS events → FeedItems |
| `cli-go/pkg/feed/feed.go` | FeedItem struct |
| `webapp/internal/server/server.go` | syncFeed(), syncFeedScoped(), feedCacheForScope(), feedFilterForScope() |
| `webapp/internal/server/sync.go` | feedSyncHandler (unified sync) |
| `webapp/internal/server/handlers.go` | /api/feed, /api/feed/grouped, /api/feed/refresh |

## DS Index

The composite index `idx_ds_events_type_created_at ON ds_events(type, created_at)` supports global scope queries that combine type filtering with a timestamp floor (`type = ANY(...) AND created_at >= ...`).
