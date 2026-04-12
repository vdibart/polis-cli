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

The feed supports three scopes, each with its own JSONL file and cursor:

| Scope | Actors | File | Cursor Key |
|---|---|---|---|
| `network` (default) | Followed domains | `pub.polis.feed.jsonl` | `pub.polis.feed` |
| `followers` | Follower domains | `pub.polis.feed.followers.jsonl` | `pub.polis.feed.followers` |
| `global` | None (all of polis) | `pub.polis.feed.global.jsonl` | `pub.polis.feed.global` |

All files live in `.polis/ds/<ds-domain>/pub.polis.core/state/`.

Scopes are isolated: read state, cursors, and retention are independent. Marking items read in one scope does not affect others.

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
| followers | 200 | 100 | 30 | 90 days | 14 days |
| global | 100 | 50 | 30 | 7 days | 2 days |

### Why Per-Type Limits

Announcement events (follows, blessings, registrations) are more frequent than content events. Without separate caps, a single author following 50 people in one day would consume 50 cache slots that could hold posts. Per-type budgets guarantee content visibility regardless of announcement volume.

### Why Announcements Age Out Faster

A follow from 60 days ago isn't interesting; a post from 60 days ago might be. The 14-day announcement TTL keeps announcements fresh and prevents stale accumulation.

## Design Decisions

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

### Follower and global scopes sync lazily

Background sync (30-second ticker) is reserved for the primary "my network" scope. Secondary scopes sync **on-demand only** — when the user switches to that scope and the cache is stale. This reduces DS query volume and avoids syncing data the user may never look at.

### Global scope uses tighter retention

Global content is exploratory, not the user's primary feed. Tighter bounds (100 posts, 50 comments, 30 announcements, 7-day/2-day age limits) keep disk usage and sync volume low.

## Key Files

| File | Purpose |
|---|---|
| `cli-go/pkg/feed/cache.go` | CacheManager, CachedFeedItem, FeedConfig, per-type prune |
| `cli-go/pkg/feed/handler.go` | FeedHandler: DS events → FeedItems |
| `cli-go/pkg/feed/feed.go` | FeedItem struct |
| `webapp/internal/server/server.go` | syncFeed(), feedCacheForScope() |
| `webapp/internal/server/sync.go` | feedSyncHandler (unified sync) |
| `webapp/internal/server/handlers.go` | /api/feed, /api/feed/grouped, /api/feed/refresh |

## DS Index

The composite index `idx_ds_events_type_created_at ON ds_events(type, created_at)` supports global scope queries that combine type filtering with a timestamp floor (`type = ANY(...) AND created_at >= ...`).
