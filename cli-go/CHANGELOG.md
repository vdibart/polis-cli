# Changelog

All notable changes to the Go CLI will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.59.0]

### Added

### Changed

### Fixed

## [0.56.0]

### Added

### Changed

- **Discovery client: RESTful API paths** — Updated all 14 endpoint paths in
  `pkg/discovery/client.go` to match the DS `/v1/` redesign. Since the webapp
  imports `cli-go/pkg/discovery`, both CLI and webapp binaries get the new paths
  automatically.

### Fixed

## [0.55.0]

### Added

### Changed

### Fixed

## [0.54.0]

### Added

- **[Webapp] Deep-linking for all SPA views**: The webapp now supports shareable, bookmarkable URLs via `/_/` paths. All dashboard views (`/_/posts`, `/_/social/feed`, `/_/settings`, etc.), editor screens (`/_/posts/new`, `/_/posts/drafts/:id`, `/_/posts/:path+`), comment screens (`/_/comments/new`, `/_/comments/drafts/:id`), and snippet screens (`/_/snippets/:source/:path+`) have unique URLs. Browser back/forward navigation works across views. Page refresh restores the correct state.
- **[Webapp] SPA fallback handler**: `spaHandler` in `server.go` serves `index.html` for any path that doesn't match a static asset, enabling deep-link URLs to load correctly on refresh.
- **[Webapp] Intent deep-link paths**: Follow intents now redirect to `/_/social/following?intent=follow&target=X` instead of the root path. Comment intents route to `/_/comments/new`. Legacy root-path intents (`/_/?intent=follow`) remain supported for backwards compatibility.
- **[Webapp] Theme switcher in dashboard Settings**: Users can now switch their site theme visually from the Settings panel. Shows a grid of all available themes with color palette swatches. Clicking a theme updates the manifest, copies the CSS, and re-renders the entire site immediately. Available to both localhost and hosted users.
- **Theme palette extraction**: New `theme.ExtractPalette()` and `theme.ListThemesWithPalettes()` functions parse CSS `--color-*` variables from theme stylesheets to produce 5 representative colors for UI previews.
- **`POST /api/settings/theme` endpoint**: Switches the active theme with validation, CSS copy, and full site re-render.

### Changed

- **[Hosted] Auth verify redirect uses deep-link path**: When a follow author is present in the magic link, the redirect now goes to `/_/social/following?intent=follow&target=...` instead of `/_/?intent=follow&target=...`.

### Fixed

## [0.53.0]

### Breaking Changes

- **[Discovery] Read endpoint access controls require signed auth for sensitive queries**:
  `ds-content-query` now only returns blessed comments to unauthenticated callers.
  `ds-relationship-query` requires signed auth headers (`X-Polis-Domain`,
  `X-Polis-Signature`, `X-Polis-Timestamp`) to query `status=pending` or
  `status=denied` blessing records. `ds-stream` filters out `polis.blessing.denied`
  events for unauthenticated consumers. Clients older than 0.53.0 will see reduced
  data from these endpoints — specifically, `polis blessing requests` and
  `polis follow` auto-bless will fail without the updated client.

### Added

- **Signed GET request authentication**: Discovery client supports optional domain
  ownership proof on GET requests via `X-Polis-Domain` / `X-Polis-Signature` /
  `X-Polis-Timestamp` headers. New `discovery.NewAuthenticatedClient()` constructor.
- **`authenticateGetRequest()` shared helper**: Edge functions can verify domain
  ownership on read endpoints using the same Ed25519 `.well-known/polis` mechanism
  as write endpoints.

### Changed

- **`discovery.Client` struct expanded**: Added optional `Domain` and
  `PrivateKeyPEM` fields for authenticated GET requests. `NewClient()` still works
  for unauthenticated queries (backward compatible).
- **All CLI commands that query pending/denied blessings now use authenticated client**:
  `blessing requests`, `follow`, `notifications`, `comment sync`.
- **[Webapp] All handlers that query pending/denied blessings now use authenticated client**:
  Notification sync, comment sync, blessing requests, follow/unfollow.

### Added

- **Notification pruning**: `notification.Prune()` enforces `MaxItems` (default 500)
  and `MaxAgeDays` (default 90) to prevent unbounded JSONL growth. Configurable via
  `max_items`/`max_age_days` in `config/notifications.json`.
- **Following author metadata**: `FollowingEntry` now stores `site_title` and
  `author_name` from `.well-known/polis`, captured during follow. New helpers:
  `UpdateMetadata()`, `EntriesMissingMetadata()`.
- **[Webapp] Feed auto-refresh**: Conversations tab polls `/api/feed/counts` every
  60s, updating sidebar badge and re-rendering the list when new items arrive.
- **Homepage post/comment limits**: `{{#recent_posts}}` and `{{#recent_comments}}`
  template sections now limit to 10 most recent items. All built-in themes updated
  to use these instead of the unbounded `{{#posts}}`/`{{#comments}}` loops.
- **Archive page**: `polis render` generates `posts/index.html` listing all posts.
  Themes with a `posts.html` template get this automatically. All 6 built-in themes
  include the new archive template. The "View all N posts" link on the homepage only
  appears when there are more than 10 posts.
- **[Webapp] Following metadata backfill**: GET `/api/following` lazily enriches
  up to 3 entries per request by fetching `.well-known/polis` from remote sites.
- **[Webapp] Following list display**: Shows site title and author name instead of
  bare domain; uses `added_at` date instead of stale `last_checked` field.
- **[Webapp] Activity stream cap**: Frontend caps activity events at 500, trimming
  oldest entries to prevent unbounded memory growth.

### Documentation

- **Root README rewritten**: Replaced stale MVP-era README with current project
  overview, repository structure, documentation index, and getting started guide.
- **CONTRIBUTING.md rewritten**: Now covers all four components (Go CLI, webapp,
  bash CLI, discovery service) with build commands, prerequisites, and conventions.
- **USAGE.md updated**: Added frozen-CLI banner, Go CLI section, deprecated TUI
  reference, and cross-references to webapp manual and security model.
- **WEBAPP-USER-MANUAL.md updated**: Added "Deploying Your Site" section, trimmed
  security section with cross-reference to SECURITY-MODEL.md, added cross-references
  for blessing workflow and configuration.
- **Fixed stale paths**: Updated key paths in SECURITY-MODEL.md (`polis_key` →
  `id_ed25519`), version table and scope in SECURITY.md, broken cross-references
  in DISCOVERY-STREAM-ARCHITECTURE.md and discovery-service/README.md.
- **Skills paths updated**: Replaced 98 occurrences of `./cli/bin/polis` with
  `polis` across SKILL.md and reference files.
- **cli-go/README.md**: Replaced stale 3-command list with full parity statement.
- **New docs/README.md**: Navigation index for the docs directory.
- **TUI.md archived**: Replaced with redirect to webapp manual.
- **GLOSSARY.md**: Added TUI deprecation note.

## [0.52.2]

### Fixed

- **Template injection via user data in section loops**: Post titles (or other
  loop variables) containing Mustache syntax like `{{> partial}}` were
  interpreted as partial includes, causing "partial not found" crashes. Now
  escapes `{{` in substituted user values during loop rendering and processes
  partials before variable substitution in all four section renderers
  (`renderPostsSection`, `renderCommentsSection`, `renderBlessedCommentsSection`,
  `renderRecentPostsSection`). This also closes a potential file-disclosure
  vector where crafted titles could read arbitrary files via `{{> ../../path}}`.
- **[Webapp] MY SITE tab missing timestamps**: Posts, drafts, and comments in
  the MY SITE tab only showed dates (e.g. "Feb 11") while the SOCIAL tab showed
  date + time. All three list renderers now use the same `item-date-group`
  wrapper with `formatTime()`.

### Changed

- **DS state filenames standardized to match cursor keys**: Renamed
  `notifications.jsonl` → `polis.notification.jsonl` and `feed-cache.jsonl` →
  `polis.feed.jsonl` so every state file matches its cursor key in
  `cursors.json`. Migration script: `scripts/migrate-state-filenames.sh`.
  The `index rebuild` command handles both old and new filenames.

## [0.52.1]

### Fixed

- **[Webapp] Notification cursor stuck at single-digit positions**: The cursor
  comparison in `syncNotifications` used string comparison (`"30" > "4"` is
  false lexicographically). Replaced with numeric comparison via `cursorGreater()`
  so the notification cursor properly advances past position 9.
- **[Webapp] All posts from same author collapsed into one notification**: Post
  events use `url` instead of `source_url` in their payload, but `DedupeKey`
  only checked `source_url` — falling back to `target_domain` and deduplicating
  all posts from the same domain. Now checks `url` as a fallback before
  `target_domain`.
- **[Webapp] Slow notification bell open (2-3s)**: The synchronous
  `syncNotifications()` call in `handleNotifications` made 3 HTTP requests
  before returning data. Changed to async — the panel now reads cached data
  immediately and triggers a background sync for the next open.
- **[Webapp] Toggle color theming inconsistent**: The "Show All" / "Unread Only"
  toggle in Conversations used the opposite active-class logic from the
  notification flyout. Now both use the same mapping: teal = "Unread Only",
  non-teal = "Show All".
- **[Webapp] Conversations not loading fresh content**: After the stale banner
  fix, the auto-refresh was gated on `stale === true` which no longer triggers
  when the cache is up to date. Now always performs a silent background sync
  when viewing the Conversations page, showing cached data immediately and
  re-rendering if new items arrive.
- **[Webapp] Feed title truncation**: Titles now use CSS ellipsis instead of
  hard-clipping mid-character. Also increased `deriveTitle()` limit from 60 to
  120 characters so discover.polis.pub quip titles show more of their text.
- **[Webapp] "X new items" toast showed total count**: The feed refresh toast
  was reporting `len(items)` (total items) instead of actual new items added.
  Now correctly counts before/after sync to show only genuinely new items.
- **[Webapp] Notifications not appearing for new posts**: The notification panel
  only synced via a 60-second background timer — opening the panel or refreshing
  the feed wouldn't trigger a sync. Now `handleFeedRefresh` fires a background
  notification sync so the bell dot updates after feed refresh.

### Added

- **[Webapp] Local timestamps on feed items**: Each Conversations and Activity
  item now shows the time (e.g., "8:30 PM") in the user's local timezone below
  the date.
- **[Webapp] "Unread Only" / "Show All" toggle**: Conversations tab has a toggle
  button (matching the notification flyout style) that filters out read items.
  Setting persists across reloads via `webapp-config.json`. Defaults to off.
- **[Webapp] Auto-merge new notification rules**: When new default notification
  rules are added in a release, they are automatically merged into existing
  configs on next sync. The notification cursor resets so the new rules can
  process past events.

### Fixed

- **[DS] ds-discover-publish content registration**: Replaced the internal HTTP
  call to `ds-content-register` with a direct `ds_content_metadata` upsert.
  The Supabase edge runtime doesn't properly pass JWTs for function-to-function
  calls, causing persistent 401 errors. The direct upsert uses the existing
  Supabase client (which handles auth automatically) and avoids the gateway
  entirely. `ds-content-register` retains JWT verification for external callers.
- **[DS] ds-discover-publish "remaining" count off by one**: The pending quip
  count was queried before marking the current quip as published, so the count
  was always 1 too high. Now queries after the status update.

### Changed

- **[DS] Deterministic random quip ordering**: The `ds_discover_queue` table now
  has a `display_order` integer column with pre-shuffled values. The publish
  function picks the lowest `display_order` where `status = 'pending'` instead
  of random selection from a batch. To replay all quips, simply reset them to
  `pending` — the shuffled order is preserved.

## [0.52.0]

### Added

- **[Webapp] Following onboarding**: New users with 0 follows see a rich
  onboarding card on the Following page recommending `discover.polis.pub` as
  their first follow, with a one-click Follow button. The Follow Author flyout
  also shows a suggestion to use `discover.polis.pub` when the user has no
  follows yet.
- **`polis.post.republished` notification rule**: New `updated-post` rule
  (disabled by default) generates notifications when followed authors update
  posts. Added `polis.post.republished` to `EventTypes()` list.

### Changed

- **`new-post` notification enabled by default**: The `polis.post.published`
  rule is now enabled out of the box. This is the most valuable notification for
  network building — users should know when authors they follow publish new
  content.
- **DS directory restructure (STATE vs CONFIG)**: The per-discovery-service
  directory `.polis/ds/<domain>/` is reorganized from a flat `projections/`
  layout into `config/` (user preferences, survives resets) and `state/`
  (computed/derived, safely deletable):
  ```
  .polis/ds/<domain>/
    config/
      notifications.json   ← rules, muted_domains
      feed.json             ← staleness, limits
    state/
      cursors.json          ← all stream cursors (consolidated)
      polis.follow.json     ← computed followers
      polis.blessing.json   ← computed blessings
      notifications.jsonl   ← notification entries
      feed-cache.jsonl      ← feed items
  ```
  Old layout: `projections/` (mixed config+state), `notifications/state.jsonl`,
  `feed/cache.jsonl`. Run `scripts/migrate-ds-layout.sh <site-dir>` to migrate
  existing sites. Nine default notification rules (was eight).
- **Consolidated cursor storage**: All stream cursors are now stored in a single
  `state/cursors.json` file instead of being embedded in each projection file.
  Each entry has `position` and `last_updated` fields.
- **[Webapp] Notification and feed sync updated**: `syncNotifications()` reads
  rules from `config/notifications.json` via `store.LoadConfig()`.
  `syncFeed()` uses `cm.GetCursor()`/`cm.SetCursor()` instead of
  `LoadProjectionState()`/`SaveProjectionState()`.

### Fixed

- **[Webapp] Persistent "Cache is stale" banner**: `syncFeed()` only called
  `SetCursor()` when the cursor position changed, so syncs with no new events
  never refreshed `LastUpdated`, leaving the cache permanently stale. Now
  `SetCursor()` is called after every successful sync regardless of whether the
  position moved.

## [0.51.0]

### Added

- **New themes: Vice and Especial**: Two new themes expanding the lineup to six.
  Vice is inspired by GTA Vice City / Miami Vice (warm saturated blues, coral
  pink, tropical teal). Especial is inspired by Modelo Especial with a dark
  variant (gold on near-black) and a light variant, Especial Light (gold on warm
  fog, WCAG AA compliant). Theme preview available at `themes/_preview.html`.

- **Stream-driven feed**: Feed refresh now queries the DS stream instead of
  polling each author's site directly. Single query per sync using
  `?type=...&actor=...&since=<cursor>`.
- **`ListFiltered()` with composable filters**: `?type=post&status=unread`
  returns only unread posts. Both `type` and `status` params are optional and
  composable.
- **Feed state scoped per discovery service**: Cache lives at
  `.polis/ds/<domain>/feed/cache.jsonl`, config + cursor in
  `.polis/ds/<domain>/projections/polis.feed.json`.
- **`FeedHandler`**: New stream event → `FeedItem` transformer
  (`cli-go/pkg/feed/handler.go`). Handles post/comment published/republished
  events, skips self-authored content.
- **[Webapp] Background feed sync**: `syncFeed()` runs alongside notification
  sync every 60 seconds, querying the DS stream for new content.
- **[Discovery] `source_domain` on comment/blessing events**: All comment and
  blessing stream events now include `source_domain` (the commenter's domain)
  alongside the existing `target_domain` (the post owner). Enables separate
  server-side filtering for "events about my posts" vs "events about my
  comments."
- **[Discovery] `source` query parameter on ds-stream**: Clients can now filter
  stream events by `source_domain` (e.g., `?source=mydomain.com`), mirroring
  the existing `?target=` filter. Used by the notification system to efficiently
  fetch blessing grant/deny events for the commenter.
- **[Discovery] Comment stream events**: New `polis.comment.published` and
  `polis.comment.republished` event types emitted when comments are registered.
  Previously comments only surfaced as blessing events, making it impossible to
  track "new comments on posts I follow."
- **[Discovery] `target_domain` on all stream events**: Every event in the
  discovery stream now includes `target_domain` in the payload, identifying who
  the event is about (e.g., the post owner for blessing requests). Enables
  server-side filtering via the `?target=` query parameter.
- **[Discovery] `target` query parameter on ds-stream**: Clients can now filter
  stream events by `target_domain` (e.g., `?target=mydomain.com`), indexed for
  performance. Reduces data transfer for clients that only need events relevant
  to their domain.
- **Rule-driven notification system**: Notifications are now generated by
  matching stream events against configurable rules in
  `.polis/ds/<domain>/projections/polis.notification.json`. Each rule specifies
  an event type, relevance filter (`target_domain`, `source_domain`, or
  `followed_author`), and a message template. Eight default rules cover all
  event types. Users can enable/disable rules and mute domains by editing the
  projection file.
- **`notification.DefaultRules()`**: Returns the built-in rule set seeded on
  first sync. Covers: new-follower, lost-follower, blessing-requested,
  blessing-granted, blessing-denied, new-comment, updated-comment, new-post.
- **`notification.ResolveTemplate()`**: Simple `{{var}}` substitution for
  notification message templates (actor, post_name, timestamp, etc.).
- **`notification.DedupeKey()`**: Deterministic dedupe key from rule ID +
  event content, preventing duplicate notifications across syncs.

### Changed

- **Removed `--register` flag from `polis init`**: Init is now local-only (no
  network calls). Users should deploy first, then run `polis register`. Updated
  "Next steps" output to guide this workflow.
- **Improved `polis register` error messaging**: When the discovery service can't
  reach `.well-known/polis` (WELLKNOWN_FETCH_FAILED), now prints a helpful hint
  asking if the site is deployed.
- **Improved discovery registration warnings in `polis post`**: Changed warning
  format from `[warning]` to `[!]` prefix and added `polis register` hint for
  newly deployed sites.
- **[Webapp] Setup wizard after init**: After initializing a new site, the webapp
  now opens a 2-step setup wizard guiding users through Deploy and Register. The
  wizard is dismissable ("Do this later") and re-openable from a dashboard
  banner. Includes deployment polling that detects when the site goes live.
- **[Webapp] Init panel collects all configuration**: The "Initialize New Site"
  panel now includes Base URL, Discovery Service URL, and Discovery Service Key
  inputs. DS fields are in a collapsible section, pre-populated with defaults.
  All values are written to `.env` on init so users get a working setup without
  manual configuration.
- **[Webapp] Feed sync after follow**: Following a new author now triggers a
  background feed sync immediately, so their content appears in Conversations
  without waiting for the next 60-second sync cycle.
- **Directory restructure**: `.polis/projections/<domain>/` moved to
  `.polis/ds/<domain>/projections/`. The new `.polis/ds/<domain>/` root serves
  as the boundary for all per-discovery-service data, with `projections/` and
  `notifications/` as siblings. No migration needed — just new paths.
- **Notification state format**: Notifications stored in
  `.polis/ds/<domain>/notifications/state.jsonl` with pre-rendered icon/message
  fields. Old `.polis/notifications.jsonl` and `.polis/notifications-manifest.json`
  are no longer read or written.
- **`notification.NewManager()` signature**: Now takes `(dataDir, discoveryDomain)`
  instead of just `(dataDir)`. All callers updated.
- **`NotificationHandler` decoupled from `ProjectionHandler` interface**: Uses
  a different processing model — `Process()` returns `[]notification.StateEntry`
  instead of mutating in-memory state. Removed from `BuiltinHandlers` registry.
- **`StreamQuery` signature**: Added optional `sourceFilter` variadic parameter
  (6th argument). Backwards compatible — existing callers unchanged.
- **[Discovery] Renamed `polis.post.updated` to `polis.post.republished`**:
  Aligns stream event naming with CLI command naming convention. Existing events
  in the database are migrated by `010_target_domain_index.sql`.
- **[Webapp] Projection-based notification sync**: Replaced the ad-hoc
  `syncNotifications()` (which polled relationship queries for pending blessings)
  with a projection-based sync that issues separate stream queries per relevance
  group (target_domain, source_domain, followed_author), applies rules, and
  appends to `state.jsonl`.
- **[Webapp] Generic notification rendering**: Frontend `renderNotification()`
  uses pre-rendered `icon` and `message` from the backend. No more type-based
  branching (`if type === 'blessing_request'`).
- **`NewCacheManager()` signature**: Now takes `(dataDir, discoveryDomain)`
  instead of just `(dataDir)`. All callers updated.
- **`handleFeed` supports `?status=read|unread` query parameter**: Composable
  with existing `?type=` filter.
- **`polis discover` uses stream-based discovery**: Queries the DS stream
  instead of polling each author's site. `--since` flag removed (stream cursor
  replaces per-author timestamps).

### Removed

- **`feed.Aggregate()` and direct HTTP polling**: Removed `Aggregate()`,
  `AggregateOptions`, `AggregateResult`, `NotFollowingError`, `extractDomain()`
  from `cli-go/pkg/feed/feed.go`. Stream-based `FeedHandler` replaces all
  polling logic.
- **`last_checked` field from `following.json`**: Stream cursor in
  `polis.feed.json` replaces per-author timestamps. `UpdateLastChecked()`
  removed from `following` package.
- **`.polis/social/feed-cache.jsonl` and `.polis/social/feed-manifest.json`**:
  Moved to DS-scoped paths (`.polis/ds/<domain>/feed/cache.jsonl` and
  `.polis/ds/<domain>/projections/polis.feed.json`).

### Fixed

- **[Discovery] `target_domain` consistency in blessing grant/deny**: Previously
  `ds-relationship-update` set `target_domain` to the commenter's domain (a
  notification-driven hack). Now consistently means the post owner's domain
  across all event types. Commenter's domain available via `source_domain`.
- **[Discovery] Blessing event payload inconsistency**: Blessing events emitted
  from `ds-content-register` (auto-bless and pending requests) now include
  `source_url` and `target_url` fields, matching the payload shape from
  `ds-relationship-update`. Previously the Go `BlessingHandler` and
  `NotificationHandler` silently dropped these events because the expected
  fields were missing.
- **[Discovery] Auto-blessed `target_domain` in `ds-content-register`**: Fixed
  auto-granted blessing events to set `target_domain` to the post owner (was
  incorrectly set to the commenter's domain).
- **`site.Init()` default `view_mode`**: Fixed `webapp-config.json` to default
  `view_mode` to `"list"` instead of `"browser"`.
- **[Webapp] "Site: Not configured" with `POLIS_BASE_URL` set**: `GetSiteTitle()`
  now falls back to the `POLIS_BASE_URL` env var when `.well-known/polis` has no
  `site_title`. Previously it only checked deprecated fields (`base_url`,
  `subdomain`) that are removed by upgrades.
- **[Discovery] Re-registration after keypair regeneration**: `ds-sites-register`
  now updates the stored public key when the domain's `.well-known/polis` key has
  changed (e.g. after `polis init` regenerates the keypair). Previously the old
  key stayed cached and required `polis unregister` with the destroyed key — now
  `polis register` just works.
- **CLI not loading `.env` file**: The Go CLI now loads `.env` files on startup
  (search order: `cwd/.env` → `~/.polis/.env`), matching the bash CLI and webapp
  behavior. Previously `DISCOVERY_SERVICE_URL`, `DISCOVERY_SERVICE_KEY`, and
  `POLIS_BASE_URL` were only read from environment variables, causing all
  discovery-dependent commands (`post`, `comment`, `register`, `follow`,
  `unfollow`, `blessing`, `discover`, `notifications`, `migrate`, `about`,
  `rebuild`) to silently skip or fail discovery operations when credentials
  were only in `.env`.

## [0.50.2]

### Removed

- **Notification subcommands `read`, `dismiss`, `sync`, `config`**: These are
  better handled by the webapp (`read`/`dismiss`) or configured non-existent
  infrastructure (`sync`/`config`). Only `polis notifications [list]` remains.
  Also fixes a bug in the Go CLI `sync` subcommand that used a hardcoded time
  format string instead of `time.Now()`.
- Dead notification package methods: `SetPollInterval`, `EnableType`,
  `DisableType`, `IsTypeEnabled`, `MuteDomain`, `UnmuteDomain`, `GetWatermark`,
  `Remove`, `RemoveAll`, `RemoveOlderThan`. Methods used by the webapp
  (`MarkRead`, `UpdateWatermark`, `Add`, etc.) are retained.

### Changed

- **Centralized discovery registration in CLI packages**: Post registration,
  comment beseech, and stream event publishing moved from webapp handlers into
  `cli-go/pkg/` packages. The webapp is now a thin HTTP layer — all business
  logic lives in the CLI.
  - `publish.RegisterPost()` — called automatically by `PublishPost()`/`RepublishPost()`
  - `comment.BeseechComment()` — replaces ~130 lines of inline webapp logic
  - `stream.PublishEvent()` — replaces webapp's `emitStreamEvent()`
  - Follow/unfollow stream events moved into `following.FollowWithBlessing()`
    and `UnfollowWithDenial()`, so both CLI and webapp emit them
- **Single source of truth for author email**: Removed `AuthorEmail` from webapp
  config struct. Email is now read exclusively from `.well-known/polis` via
  `GetAuthorEmail()`. Deleted duplicate `WellKnownPolis` struct from server.go
  in favor of `site.WellKnown`.
- **[Webapp] Discovery config moved from webapp-config.json to .env**: Removed
  `DiscoveryURL` and `DiscoveryKey` from `Config` struct (and `webapp-config.json`).
  These are now fields on the `Server` struct, loaded exclusively from `.env` /
  environment variables — matching the CLI's behavior. Eliminates duplication
  between `.env` and `webapp-config.json`.
- **Discovery config propagation**: `cmd/root.go` and webapp `Initialize()` both
  propagate `DiscoveryURL`, `DiscoveryKey`, and `BaseURL` to `publish`, `comment`,
  and `stream` packages at init time.

### Changed (UI)

- **[Webapp] Renamed "Feed" to "Conversations"**: The Social tab sidebar item
  and content header now read "Conversations" instead of "Feed" for clarity.

### Fixed

- **[Webapp] Posts not appearing in discovery**: Posts published via webapp were
  not registered with `ds-content-register` because `AuthorEmail` was empty in
  `webapp-config.json` with no fallback to `.well-known/polis`.

## [0.50.1]

### Fixed

- **[Discovery] Author email spoofing**: `ds-content-register` now verifies the
  `author` email in the payload matches the `email` field published in the actor's
  `.well-known/polis`. Returns 403 `AUTHOR_MISMATCH` on mismatch. Sites without
  an email field are unaffected (backwards compatible).
- **[Discovery] Blessing denial bypass**: Re-registering a comment that was explicitly
  denied no longer resets the blessing status to `pending`. The denial is preserved —
  only the post owner can re-grant via `ds-relationship-update`.

### Added

- **[Discovery] Rate limiting on all write endpoints**: Shared `checkRateLimit()`
  helper using the existing `increment_rate_limit` database function. Limits per
  hour per domain:
  - `ds-content-register`: 50
  - `ds-content-unregister`: 20
  - `ds-relationship-update`: 50
  - `ds-sites-register`: 5
  - `ds-sites-unregister`: 5
  - `ds-stream-publish`: 100 (refactored from inline to shared helper)
- **[Discovery] Input size limits**: URL (2048 chars), email (254 chars, RFC 5321),
  signature (2048 chars), metadata (4KB), total payload (64KB, returns 413).
- **[Discovery] CORS hardening**: All endpoints now declare explicit
  `Access-Control-Allow-Methods` (POST or GET per endpoint) and
  `Access-Control-Max-Age: 600`.

### Changed

- **[Discovery] Stricter email validation**: Regex now requires standard local-part
  characters, domain labels that don't start/end with hyphens, and TLD of 2+
  alphabetic characters. Rejects edge cases like `a@b.c`.
- **[Discovery] `fetchPublicKey()`** refactored as a thin wrapper around new
  `fetchWellKnownPolis()` which returns both `publicKey` and `email`.

## [0.50.0]

### Breaking Changes

- **[Discovery] Unified content model**: Separate `posts_metadata` and `comment_metadata`
  tables replaced by `ds_content_metadata` (type-keyed) and `ds_relationship_metadata`.
  Schema version bumped to 0.8.0.
- **[Discovery] Table namespacing**: All tables renamed — `ds_` prefix for user data
  (`ds_registered_sites`, `ds_events`, `ds_domain_migrations`), `admin_` prefix for
  operator data (`admin_blocked_domains`, `admin_blocked_types`, `admin_stream_config`,
  `admin_rate_limits`).
- **[Discovery] Edge function namespacing**: All edge function endpoints renamed with
  `ds-` or `admin-` prefix to match table naming. Discovery client updated for all
  14 user-facing endpoints (`ds-content-*`, `ds-relationship-*`, `ds-sites-*`,
  `ds-stream*`, `ds-migrations`).
- **[Discovery] Endpoints removed**: `posts-register`, `posts-unregister`, `posts-check`,
  `posts` (query), `comments-blessing-beseech`, `comments-blessing-grant`,
  `comments-blessing-deny`, `comments-blessing-requests`, `comments` (query),
  `polis-version`.
- **[Discovery] Endpoints added**: `ds-content-register`, `ds-content-unregister`,
  `ds-content-check`, `ds-content-query`, `ds-relationship-update`, `ds-relationship-query`.
- **[Discovery] Signing format**: New unified canonical JSON format —
  `{"type":"polis.post","url":"...","version":"...","author":"...","metadata":{...}}`
  replaces type-specific formats.
- **[Discovery] `polis_versions` table removed** from database.
- **[Discovery] Status terminology**: Blessing status `blessed` → `granted` in
  relationship records.
- **Go client rewrite** (`pkg/discovery/client.go`): Type-specific methods removed
  (`RegisterPost`, `BeseechBlessing`, `GrantBlessing`, `DenyBlessing`,
  `GetPendingRequests`, `GetBlessedComments`, `GetCommentsByAuthor`,
  `CheckBlessingStatus`, `CheckVersion`). Replaced by generic methods:
  `RegisterContent()`, `UnregisterContent()`, `CheckContent()`, `QueryContent()`,
  `UpdateRelationship()`, `QueryRelationships()`.
- **`blessing.Deny()` signature changed**: Now takes `(commentURL, targetURL, client, privateKey)` instead of `(commentVersion, client, privateKey)`.
- **[Webapp] Deny blessing API**: `POST /api/blessing/deny` now requires
  `{"comment_url": "...", "in_reply_to": "..."}` instead of `{"comment_version": "..."}`.
- **[Bash CLI] Unified content API**: All discovery service endpoints updated to match
  the unified content/relationship API. Old endpoints (`comments-blessing-beseech`,
  `comments-blessing-grant`, `comments-blessing-deny`, `comments-blessing-requests`,
  `comments`) replaced by `ds-content-register`, `ds-content-query`, `ds-content-check`,
  `ds-relationship-update`, `ds-relationship-query`.
- **[Bash CLI] New canonical JSON signing format**: `cmd_comment` beseech payload uses
  `{"type":"polis.comment","url":"...","version":"...","author":"...","metadata":{...}}`
  instead of flat fields. Grant/deny use
  `{"type":"polis.blessing","source_url":"...","target_url":"...","action":"...","timestamp":"..."}`.
- **[Bash CLI] Status terminology**: `blessed` → `granted` in response parsing.
- **[Bash CLI] Version check removed**: `check_version_update()` stubbed out —
  `polis-version` endpoint no longer exists.

### Added

- **`pkg/stream/` package**: Client-side projection framework for consuming the Discovery Stream
  - `store.go`: `Store` manages per-projection cursors and materialized state on disk, namespaced by discovery service domain (`.polis/projections/<domain>/`)
  - `handler.go`: `ProjectionHandler` interface with `TypePrefix()`, `EventTypes()`, `NewState()`, `Process()`. Built-in handler registry for extensibility.
  - `follow.go`: `FollowHandler` — built-in projection that maintains a follower set from `polis.follow.announced` / `polis.follow.removed` events, filtered by target domain
  - `blessing.go`: `BlessingHandler` — processes `polis.blessing.requested`, `polis.blessing.granted`, `polis.blessing.denied` events. Maintains `BlessingState` with entries list and granted/denied counts.
  - `notification.go`: `NotificationHandler` — processes follow, blessing, and post events. Generates notification entries with type, actor, message, timestamp, and read status.
- **Discovery client methods** (`pkg/discovery/client.go`):
  - `RegisterContent()`, `UnregisterContent()`, `CheckContent()`, `QueryContent()` — unified content API
  - `UpdateRelationship()`, `QueryRelationships()` — unified relationship API
  - `StreamQuery()`, `StreamPublish()`, `StreamHealth()` — stream API
  - `MakeContentCanonicalJSON()`, `MakeStreamCanonicalJSON()` — canonical payload builders for Ed25519 signing
- **[Discovery] Pluggable validator registry**: `ContentTypeValidator` and
  `RelationshipTypeValidator` interfaces in `_shared/validation.ts`. New content types
  only require implementing a validator — zero edge function changes.
- **[Discovery] Migration 009**: `009_unified_content.sql` — creates new tables, renames
  existing tables to namespaced names, drops old tables.
- **[Webapp] Activity stream**: `GET /api/activity` returns events from followed authors via the Discovery Stream. Loads following list, builds actor filter, queries stream with cursor pagination.
- **[Webapp] Follower count**: `GET /api/followers/count` uses the projection framework — `FollowHandler` processes follow/unfollow events, `Store` persists the materialized follower set. Supports `?refresh=true` to replay from cursor 0.
- **[Webapp] Stream event emission**: Fire-and-forget goroutines emit stream events after mutations:
  - Follow → `polis.follow.announced` via `ds-stream-publish`
  - Unfollow → `polis.follow.removed` via `ds-stream-publish`
  - Publish/Republish → `ds-content-register` (edge function emits `polis.post.published`/`updated` as side effect)
- **[Webapp] Activity view**: Sidebar navigation item under Discover section. Renders event timeline with actor, action type badges, and detail links. Cursor-based pagination with "Load more" button.
- **[Webapp] Followers view**: New Stats sidebar section with follower count badge. Lists followers with refresh capability.
- **[Webapp] Notification API**: Three new endpoints:
  - `GET /api/notifications` — paginated notification list (offset, limit, include_read)
  - `GET /api/notifications/count` — unread count for badge
  - `POST /api/notifications/read` — mark notifications as read by IDs or all
- **[Webapp] Background notification sync**: Server starts a goroutine that polls
  the discovery service every 60s for pending blessing requests and creates
  `blessing_request` notifications with deduplication.
- **[Webapp] Notification bell UI**: Header now shows domain name (from
  `POLIS_BASE_URL`) and a bell icon with red dot badge for unread notifications.
  Click opens a flyout panel with notification list, "Show All" toggle, infinite
  scroll pagination, and auto-mark-as-read on view.
- **`notification.Manager` new methods**: `MarkRead(ids, markAll)`,
  `CountUnread()`, `ListPaginated(offset, limit, includeRead)`.

### Changed

- Module path alignment: Go module paths changed from `polis-planning` to
  `polis-cli` to eliminate import path rewriting during release
- [Webapp] Post-comment hook description updated for accuracy
- **Generator string standardization**: Accepted generator patterns are now
  `polis-cli-go/X.Y.Z` (primary), `polis-cli-bash/X.Y.Z` (bash-lineage),
  and `polis-cli/X.Y.Z` (legacy fallback). `polis-webapp` is deprecated and
  no longer recognized.
- **[Validator] Version-aware root-post check**: Missing `in-reply-to.root-post`
  is now a warning (not a fail) for comments created with `polis-cli` < 0.44.0,
  which did not include the field.
- **[Discovery] `polis-version` endpoint**: Component names updated from
  `cli`/`tui`/`upgrade` to `cli-bash`/`cli-go`. Default component is now
  `cli-bash`.
- **`pkg/stream/store.go`**: Root directory `.polis/projections/<domain>/`. Self-contained
  projection files with embedded cursor (`{cursor, last_updated, state}`). File naming:
  `<projection>.json`.
- **`pkg/stream/handler.go`**: Built-in handler registry includes `FollowHandler`,
  `BlessingHandler`, and `NotificationHandler`.
- **`pkg/blessing/*`**: All 5 files rewritten to use unified relationship API methods.
- **`pkg/comment/sync.go`**: Uses `QueryRelationships` for blessing status checks.
- **`pkg/following/social.go`**: `FollowWithBlessing` and `UnfollowWithDenial` use generic
  `QueryRelationships` + `UpdateRelationship`.
- **`pkg/index/rebuild.go`**: `rebuildCommentsIndex` uses `QueryRelationships`.
- **`pkg/cmd/notifications.go`**: Version check notification removed.
- **`pkg/cmd/follow.go`**, **`pkg/cmd/unfollow.go`**: Use generic relationship API.
- **[Webapp] `registerPostWithDiscovery`**: Uses `RegisterContent()` with unified
  canonical JSON.
- **[Webapp] Comment beseech handler**: Uses `RegisterContent()` with
  `ContentRegisterRequest`.
- **[Discovery] All 12 existing edge functions**: Updated table name references to use
  `ds_`/`admin_` prefixed names.
- **[Discovery] `schema.sql`**: Rewritten to v0.8.0 canonical reference.
- **Generator-format version strings**: All metadata files now write version as
  `polis-cli-go/X.Y.Z` (Go) or `polis-cli/X.Y.Z` (bash) instead of bare `X.Y.Z`.
  Affected files: `.well-known/polis`, `manifest.json`, `blessed-comments.json`,
  `following.json`, `notifications-manifest.json`, `feed-manifest.json`.
  Added `GetGenerator()` to `site`, `metadata`, `following`, `notification`,
  `index`, and `feed` packages. `publish.DefaultVersion()` now returns generator
  format. `cmd/root.go` propagates `Version` to `site` package.

### Fixed

- `EventPostComment` doc comment expanded to describe all trigger conditions
  (grant, sync, auto-bless)
- Test fixtures in `pkg/comment` and `pkg/site` updated from `polis-webapp`
  to `polis-cli-go` to match actual generator output
- **[Webapp] `author_name` registration bug**: `handleSiteRegister` was reading
  `rawWKP["author_name"]` instead of `rawWKP["author"]` from `.well-known/polis`,
  causing `author_name` to always be empty in `ds_registered_sites`.
- **[Bash CLI] `author_name` registration bug**: `cmd_register` was reading
  `.author_name` instead of `.author` from `.well-known/polis`, same root cause.

### Tests

- **`pkg/stream/`**: Store tests (cursor read/write, state load/save, self-contained projection files), FollowHandler tests (announce adds follower, remove deletes follower, filters by target domain, deduplication)
- **[Webapp]**: 9 handler tests — activity stream, follower count, `extractDomainFromURL`
- **`pkg/notification/`**: `TestMarkRead`, `TestCountUnread`, `TestListPaginated`,
  updated `TestInitManifest_UsesGeneratorVersion`, `TestLoadManifest_DefaultUsesGeneratorVersion`
- **[Webapp]**: `TestHandleNotificationCount`, `TestHandleNotificationCount_MethodNotAllowed`,
  `TestHandleNotifications_EmptyList`, `TestHandleNotificationRead`,
  `TestHandleNotificationRead_MethodNotAllowed`
- Updated version assertion tests across `site`, `index`, `following`, `publish`,
  `feed` packages for generator-format version strings

### Docs

- [Webapp] README.md created (architecture, build/run, frontend, API reference)
- Bash CLI README expanded as reference implementation guide
- `polis-tutorial` deprecated across documentation

## [0.49.0]

### Added

- **`pkg/feed/` package**: New importable package that extracts feed aggregation logic from `cmd/discover.go`. `feed.Aggregate()` fetches public indexes from followed authors, filters by `last_checked`, merges, sorts by published date, and updates timestamps.
- **`pkg/following/` social functions**: `FollowWithBlessing()` and `UnfollowWithDenial()` extract the blessing/denial side-effects from `cmd/follow.go` and `cmd/unfollow.go` into importable functions.
- **[Webapp] Social features — Following + Feed**: Two-mode sidebar ("My Site" / "Social") brings social reading into the webapp. Social mode includes Feed (aggregated posts from followed authors) and Following (author management with follow/unfollow).
- **[Webapp] Follow/Unfollow**: Follow panel to add authors by HTTPS URL (auto-blesses pending/denied comments). Unfollow with confirmation modal (warns about denying blessed comments).
- **[Webapp] Feed view**: Chronological feed of posts from followed authors with type badges, refresh button, unreachable-author warnings, and empty states.
- **[Webapp] Remote post viewer**: Slide-out panel renders remote posts with parchment-styled content, fetched via new `/api/remote/post` endpoint.
- **[Webapp] API endpoints**: `GET/POST/DELETE /api/following`, `GET /api/feed`, `GET /api/remote/post?url=...`
- **`pkg/feed/cache.go` — Feed cache with read tracking**: Persistent JSONL cache (`.polis/social/feed-cache.jsonl`) with `CacheManager` that supports Merge (dedup by deterministic sha256 ID), MarkRead, MarkUnread, MarkAllRead, MarkUnreadFrom, Prune (by age and count), and staleness detection via manifest (`.polis/social/feed-manifest.json`).
- **[Webapp] Feed cache — instant load + background refresh**: `GET /api/feed` now loads instantly from local cache. `POST /api/feed/refresh` runs network aggregation and merges into cache. Auto-refresh fires in background when cache is stale (default 15 minutes).
- **[Webapp] Feed read/unread tracking**: Unread items show bold title + teal dot indicator. Sidebar badge shows unread count. Opening an item marks it read (fire-and-forget). "Mark All Read" button in header.
- **[Webapp] Feed type filtering**: Filter tabs (All / Posts / Comments) above feed list, passed as `?type=` query param.
- **[Webapp] Feed hover actions**: "Mark Unread" and "Unread From Here" buttons appear on hover for read feed items only (hidden on unread items). "Unread From Here" marks the hovered item and all more recent items above it as unread. Styled like the FM toggle buttons, replaces the date column on hover.
- **[Webapp] API endpoints**: `POST /api/feed/refresh`, `POST /api/feed/read`, `GET /api/feed/counts`

### Changed

- **`cmd/discover.go` refactored**: Now calls `feed.Aggregate()` instead of inline logic. Same CLI output format maintained.
- **[Webapp] `following.Version` propagation**: `server.go` `Initialize()` now propagates CLI version to the `following` package alongside `publish`, `comment`, and `metadata`.
- **[Webapp] Live markdown preview**: Editor now renders a live preview as you type (300ms debounce), replacing the manual "Render Preview" button. Ctrl+Enter now triggers publish instead of render.
- **[Webapp] Frontmatter toggle in editor**: Added "Hide FM" / "Show FM" toggle to the editor markdown pane header. When active, displays frontmatter in a non-editable mini-pane above the textarea. Frontmatter is never exposed in the editable textarea, preventing accidental edits to signatures and hashes. Shares the persisted setting with browser mode.
- **[Webapp] Hide browser mode toggle**: Browser mode toggle buttons hidden from the header (code retained, just not visible).
- **[Webapp] Shared web assets package**: Moved `www/` from duplicated locations (`cmd/server/www/` and `cmd/polis-full/www/`) to a single `internal/webui/www/` package. Both entry points now import `internal/webui.Assets`, eliminating file drift between builds.

### Fixed

- **[Webapp] Feed item click broken for titles with apostrophes**: Inline `onclick` handlers used single-quoted JS strings, so titles like "It's Not Beyond Our Reach" caused a silent syntax error. Feed items now pass a numeric index instead of raw strings.
- **[Webapp] "Open original" link pointed to `.md`**: The remote post viewer's "Open original" link now points to the `.html` version of the post for browser viewing.
- **[Webapp] Remote post viewer styling**: Replaced light parchment styles with zane-complementary dark theme (surface background, lavender left-border, salmon headings, teal links, monospace font).

### Tests

- **`pkg/feed/`**: 9 tests — empty feeds, single author, since override, not-following errors, unreachable authors, last_checked updates, no-new-content, 10-item limit, special character titles.
- **`pkg/feed/cache`**: 13 tests — ComputeItemID determinism, empty cache, merge, merge dedup (preserves read state), mark read, mark unread, mark all read, mark unread from, list by type, prune by count, prune by age, staleness detection, manifest defaults, version propagation, not-found errors, directory creation.
- **`pkg/following/`**: 6 tests — follow adds to list, already-followed, unfollow removes, unfollow when not following, unreachable sites.
- **[Webapp]**: 17 handler tests for feed cache endpoints — empty cache, unread count, type filter, method validation, mark read/unread/all/unread-from, invalid ID error, empty refresh, feed counts (empty/with items), feed refresh with special character titles.

## [0.48.0]

### Added

### Changed

### Fixed

- **Hardcoded version strings in 4 packages**: `following`, `index/rebuild`, `notification`, and `theme` now use the propagated CLI version instead of hardcoded `"0.45.0"` / `"0.1.0"`. Affects `following.json`, `manifest.json`, `blessed-comments.json`, `notifications-manifest.json`, and theme manifest defaults.

### Security

- **[H3] `rotate-key` now updates `.well-known/polis`**: Previously, `rotate-key` read `.well-known/polis` but never wrote back the new public key, leaving the site broken after rotation. Now the command parses the JSON, replaces `public_key`, and writes it back while preserving all other fields.
- **[M6] Temp files use private directory**: `computeUnifiedDiff()` now creates a private temp directory (`0700`) instead of using the system `/tmp` directly. Files created during diff computation are no longer world-readable.
- **[Webapp] [H1] Error detail redaction**: All HTTP error responses that previously included `err.Error()` (leaking file paths, OS error strings) now return generic messages. Internal error details are logged server-side via `s.LogError()`.
- **[Webapp] [M1] Draft ID whitelist sanitization**: Draft IDs are now sanitized with a whitelist regex (`[^a-zA-Z0-9_-]` replaced with `-`) instead of the previous blacklist approach that only stripped `/` and `\`.
- **[Webapp] [M2] Path traversal canonicalization**: `validatePostPath()` and `validateContentPath()` now apply `filepath.Clean()` before checking for `..`, preventing encoded traversal sequences from bypassing the check.

### Tests

- **[Webapp]** Added `TestErrorResponsesRedacted` to verify error responses don't contain OS error strings or file paths.
- **[Webapp]** Added `TestDraftIDSanitization` to verify special characters, path traversal, null bytes, and unicode are stripped from draft IDs.
- **[Webapp]** Added `TestValidatePostPath_Canonicalization` and `TestValidateContentPath_Canonicalization` to verify `filepath.Clean` inputs are handled correctly.

## [0.47.0]

### Added

- **Shell completions for `serve` and `validate`**: Both bash and zsh completions now include `serve` (with `--data-dir`/`-d` flags) and `validate` (with `--json` flag)
- **`polis serve --help`**: Serve command now prints flag documentation when invoked with `--help`/`-h`
- **`{{target_author}}` and `{{preview}}` in comment loops**: Template engine now wires `target_author` and `preview` variables through `{{#comments}}` sections and partial includes
- **`polis init` creates `webapp-config.json`**: Init now creates `.polis/webapp-config.json` with webapp-specific defaults (`setup_at`, `view_mode`, `show_frontmatter`). Discovery credentials are not included — they belong in `.env` and are loaded at runtime.
- **Hooks auto-discovery**: `RunHook()` and `GetHookPath()` now check `.polis/hooks/{event}.sh` when no explicit path is configured. Placing a script in the conventional location just works without registering it in `webapp-config.json`.
- **[Webapp] CLI version propagation**: Server accepts `CLIVersion` via `RunOptions` and propagates it to `publish`, `comment`, and `metadata` packages so all generated metadata uses the correct CLI version

### Fixed

- **`polis post` now moves the original file**: `polis post <file>` removes the original file after publishing into `posts/`, matching the bash CLI's move behavior. Non-fatal on failure (warns only). Does not apply to `republish`.

- **Comment count mismatch after blessing**: `MoveComment()` now calls `publish.UpdateManifest()` after blessing a comment, so `manifest.json` `comment_count` stays in sync with actual blessed comments
- **Hardcoded version strings in metadata files**: `.well-known/polis`, `manifest.json`, `following.json`, and `blessed-comments.json` now use the CLI version from `version.txt` instead of hardcoded `"0.1.0"`, `"1.0"`, or `"0.42.0"`
- **Hardcoded generator tags in frontmatter**: Post and comment generator fields now use `"polis-cli-go/<version>"` computed from the CLI version, replacing hardcoded `"polis-cli-go/0.1.0"` and `"polis-webapp/0.1.0"`

### Previously added

- **Filename collision prevention**: Posts and comments auto-append `-2`, `-3`, etc. when a filename already exists across posts, drafts, and all comment status directories
- **Random slug for untitled posts**: Publishing without a title now generates `untitled-<random hex>` instead of bare `untitled`, preventing silent overwrites
- **Blessed comments in rendered posts**: Renderer loads local comment content (strips frontmatter, renders markdown to HTML) for blessed comments instead of leaving content empty
- **Rebuild fetches blessed comments**: `rebuild --comments` now queries the discovery service for blessed comments and populates `blessed-comments.json` (falls back to empty file when discovery is not configured)
- **Webhook safety regression tests**: Tests verify hooks only fire after successful operations, never on error paths
- **`polis init` flag parity**: Added 10 missing flags (`--site-title`, `--register`, `--keys-dir`, `--posts-dir`, `--comments-dir`, `--snippets-dir`, `--themes-dir`, `--versions-dir`, `--public-index`, `--blessed-comments`, `--following-index`); renamed `--title` to `--site-title`; removed Go-only flags `--author`, `--email`, `--base-url` (author/email sourced from git config)

### Fixed

- **Random theme selection when no active theme set**: Replaced hardcoded `"turbo"` fallback with random selection from available themes, matching the bash CLI's `select_theme()` behavior; also fixes the empty `active_theme` bug where `GetActiveTheme()` returned `("", nil)` causing `theme.Load()` to fail with "theme name is required"

### Changed

- **public.jsonl deduplication**: `AppendToPublicIndex()` now checks for existing entries by path and updates in place instead of always appending; `publish.AppendToIndex()` delegates to `metadata.AppendPostToIndex()` for unified dedup
- **MoveComment populates blessed-comments.json**: Moving a comment to blessed status now adds it to both `public.jsonl` and `blessed-comments.json`
- **Flexible blessed comment path matching**: `GetBlessedCommentsForPost()` matches across `.md`/`.html` extensions and full URL vs relative path variants
- **Renderer skips .versions directories**: `RenderAll()` Walk callbacks now skip `.versions` directories, matching `index/rebuild.go` behavior
- **Drafts directory renamed**: `.polis/drafts` → `.polis/posts/drafts`; old path still accepted in content validation for backwards compatibility
- **[Webapp] Hooks fire without explicit config**: Publish, republish, and beseech handlers no longer guard hook execution behind `s.Config.Hooks != nil`. `RunHook()` now handles nil config gracefully with auto-discovery from `.polis/hooks/`.
- **[Webapp] Automations list shows auto-discovered hooks**: `getAutomations()` uses `GetHookPathWithDiscovery()` so the settings UI displays hooks found at conventional paths, not just those registered in `webapp-config.json`.
- **[Webapp] Native confirm() replaced**: All 5 browser `confirm()` calls replaced with styled `showConfirmModal()` dialogs with appropriate danger/default types
- **[Webapp] Subdomain removed from webapp-config.json**: `SaveConfig()` strips the deprecated `Subdomain` field; `LoadEnv()` no longer derives subdomain from `POLIS_BASE_URL`; all runtime usage goes through `GetSubdomain()` which derives from `BaseURL`
- **[Webapp] Beseech auto-bless renders site**: The auto-blessed branch of the beseech handler now calls `RenderSite()` before running hooks, ensuring HTML is generated before deployment
- **[Webapp] Drafts directory migration on startup**: Automatic migration from `.polis/drafts` to `.polis/posts/drafts` on webapp startup
- **[Webapp] Init handler compatibility**: Removed deleted `Author`/`Email`/`BaseURL` fields from `InitOptions` construction to match updated `cli-go/pkg/site` API

## [0.46.0] - 2026-02-05

### Deprecated

- **polis-tui**: Terminal UI deprecated in favor of webapp (`polis-full serve`)

## [0.45.0] - 2026-02-05

### Summary

The Go CLI reaches implementation parity with the Bash CLI. This version is
**untested in production** but implements all 27 commands with matching output
formats and error codes. The Go CLI will be the active implementation going
forward.

### Added

- Full command parity with Bash CLI (27 commands)
- Packages: remote, verify, version, following, notification, index, migrate, clone
- Commands: post, republish, comment, preview, extract, index, about
- Commands: follow, unfollow, discover, clone
- Commands: blessing (requests, grant, deny, beseech, sync)
- Commands: notifications (list, read, dismiss, sync, config)
- Commands: rebuild, migrate, migrations apply, rotate-key
- Commands: init, render, register, unregister, version, serve (stub)
- JSON output mode (`--json` flag)
- Data directory override (`--data-dir` flag)

### Notes

- The `serve` command is a stub in the CLI-only binary; requires the bundled
  binary (`polis-full`) for actual web server functionality
- This release has not been tested against production discovery services
- Report issues at: https://github.com/vdibart/polis-cli/issues
