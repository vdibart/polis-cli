# Changelog

All notable changes to the Go CLI will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.58.0] - 2026-03-07

This release adds end-to-end encrypted direct messages, DS signature verification with two-layer cryptographic trust, policy engine extensions, and a migration of all hardcoded blessing/notification logic to user-configurable declarative policies. The webapp gets a major UI redesign with a new topbar layout, dual-theme support, excerpt cards, and avatar support.

### Added

- **`polis dm` command**: End-to-end encrypted direct messages as a new `pub.polis.dm` content type. Ed25519-to-X25519 key conversion, NaCl box transport, secretbox storage. Subcommands: `list`, `read`, `send`, `retry`, `config`. Per-sender and global rate limiting; policy-based acceptance control.
- **`pkg/dm/`**: Full DM implementation package — crypto, store, rate limiting, send, receive, types. 49 tests.
- **`pkg/discovery/ds_verify.go`**: Discovery service response verification via Ed25519 envelope signatures. Cached with TTL, retries once on key rotation. Anomaly tracking: sync suspended after 3 consecutive DS failures.
- **`pkg/discovery/author_verify.go`**: Per-event author signature verification on stream events to prevent DS impersonation attacks.
- **`pkg/stream/verification_state.go`**: Per-domain verification counters and anomaly state.
- **`pkg/site/dirs.go`**: Shared directory path constants.
- **[Webapp] v2 UI redesign**: Topbar layout, dual-theme (dark/light), redesigned feed/posts/comments/blessings views. Unified outlined/ghost button styles. Excerpts on feed cards, post rows, and comment rows.
- **[Webapp] Avatar support**: Remote site avatars in feed items; avatar randomizer and display name in Settings. Custom avatars suppress initials overlay.
- **[Webapp] `internal/server/middleware.go`**: Request middleware extracted into dedicated file.

### Changed

- **Discovery API paths**: All endpoint paths updated to match DS `/v1/` REST redesign (14 paths).
- **Policy engine: `emit`/`omit` verbs and `self`/`thread-blessed` sources**: Policy rules now control event emission and can filter by thread-blessed status. All hardcoded blessing, notification, and event-gating logic migrated to configurable rules.
- **`.polis/` directory permissions**: Changed to `0700`.
- **[Webapp] Feed sort**: Timestamp parsing replaces string comparison for correct chronological ordering.
- **[Webapp] Feed reply counts**: Blessed comments now counted as replies.
- **[Webapp] Remote avatars**: Fetched and displayed in feed items.

### Fixed

- **[Webapp] Conversations**: Fixed SyntaxError in click handler; restored Mark Unread / Unread From Here hover actions.
- **[Webapp] Mark Unread button**: Fixed button escaping and click bubbling.
- **[Webapp] Following empty state**: Restored heading on empty state CTA.
- **[Webapp] Blessed comments not rendering**: Fixed `/post/` vs `/posts/` path mismatch.
- **[Webapp] Index page comment count**: Fixed comment count for source content paths.
- **DS key format**: Fixed raw base64 DS key conversion to `ssh-ed25519` format.

## [0.55.0] - 2026-02-24

This release adds an About page editor to the webapp, introduces theme switching from the Settings panel, ships new `also-reading` and `polis-widget` snippets for all six built-in themes, and delivers a range of webapp usability improvements.

### Added

- **`theme.ExtractPalette()` and `theme.ListThemesWithPalettes()`**: New functions parse CSS `--color-*` variables from theme stylesheets to produce color swatches for UI previews.
- **`also-reading.html` and `polis-widget.html` snippets for all 6 themes**: New theme snippets shipped for especial, especial-light, sols, turbo, vice, and zane.
- **[Webapp] Theme switcher in Settings**: Users can switch their site theme from the Settings panel. A grid of themes with color palette swatches allows one-click theme changes that immediately re-render the site.
- **[Webapp] About page editor**: New full-screen editor for the global `about.html` snippet, accessible from the Snippets sidebar in all lifecycle stages.
- **[Webapp] `POST /api/settings/theme` endpoint**: Switches the active theme with validation, CSS copy, and full site re-render.
- **[Webapp] `POST /api/settings/sync` and widget DELETE endpoint**: New sync and widget management API endpoints.

### Changed

- **[Webapp] Consolidated Conversations tab**: Social sidebar merges Activity and Posts & Comments into a single Conversations view with consistent green badge colors.
- **[Webapp] My Comments tab order**: Reordered to All, Drafts, Blessed, Pending, Denied.
- **[Webapp] Deep-link intent paths**: Follow intents redirect to `/_/social/following?intent=follow&target=X`; comment intents route to `/_/comments/new`. Legacy `/_/?intent=...` paths remain supported.
- **[Webapp] Snippets sidebar**: Now visible in all lifecycle stages including `first_post`.
- **"Space" terminology**: Post-init welcome message updated from "site" to "space".

### Fixed

- **[Webapp] Widget follow/unfollow toggle**: Public site widget now includes a Follow/Unfollow toggle.
- **[Webapp] CORS headers**: Metadata files and the widget DELETE endpoint include correct CORS headers.
- **[Webapp] `blessing.granted` stream event actor**: Fixed to correctly identify the granter.
- **[Webapp] Version in metadata files**: Fixed webapp writing `"dev"` instead of the actual version string.
- **[Webapp] Feed item display**: Fixed grouped feed item title fallback, URL display, and link underlines.
- **[Webapp] About editor and sidebar placement**: Fixed textarea styling and sidebar-section-snippets placement.

## [0.54.0] - 2026-02-17

This release shifts author identity from email to domain, simplifies the webapp with a streamlined sidebar and improved onboarding, and adds documentation for contributing, webapp usage, and security policy.

### Changed

- **Domain-based author identity**: Author identity now uses the domain from `.well-known/polis` instead of email. Affects comment frontmatter, discovery service registration, and blessing operations.
- **Init flow simplified**: `site.Init()` accepts explicit Author and Email fields for headless provisioning.
- **Package state refactored**: `publish`, `comment`, and `stream` packages accept discovery config as function parameters instead of package-level globals.
- **[Webapp] Simplified UI**: Removed browser split-pane mode, replaced with a progressive sidebar adapting to site lifecycle stage. Merged blessing requests into a single view.
- **[Webapp] Welcome flow**: New users see a "What just happened?" educational disclosure after init.

### Added

- **New tests**: Added test suites for `comment`, `site/init`, `site/wellknown`, `render/page`, and `template/engine` packages.
- **Render page helpers**: New page rendering utilities in `render/page.go` for index page generation.

### Fixed

- **Verify command robustness**: Improved error handling for edge cases in signature verification.
- **Template section rendering**: Additional test coverage for section rendering edge cases.

## [0.53.0] - 2026-02-13

This release improves homepage performance with limited post/comment sections and archive pages, adds quality of life improvements to the webapp (feed auto-refresh, following metadata display), and introduces authenticated discovery queries for privacy-sensitive operations.

### Breaking Changes

- **[Discovery] Authenticated queries for sensitive data**: Discovery service read endpoints now enforce access controls. Unauthenticated callers to `ds-content-query` only see blessed comments (pending/denied are filtered). Querying `ds-relationship-query` for `status=pending` or `status=denied` blessing records requires signed authentication headers (`X-Polis-Domain`, `X-Polis-Signature`, `X-Polis-Timestamp`). The `ds-stream` endpoint filters out `polis.blessing.denied` events for unauthenticated consumers. CLI commands that query pending/denied blessings (`polis blessing requests`, `polis follow` auto-bless, `polis notifications`) now use authenticated clients. Clients older than 0.53.0 will see reduced data from these endpoints.

### Added

- **Homepage post/comment limits**: New `{{#recent_posts}}` and `{{#recent_comments}}` template sections limit homepage display to the 10 most recent items. All 6 built-in themes updated to use these sections.
- **Archive page**: Running `polis render` now generates `posts/index.html` with a chronological list of all posts. Themes with a `posts.html` template automatically get this page.
- **"View all posts" link**: Homepage displays a "View all N posts" link when you have more than 10 posts.
- **Signed GET authentication**: Discovery client supports optional domain ownership proof on GET requests via authentication headers. New `discovery.NewAuthenticatedClient()` constructor.
- **Notification pruning**: `notification.Prune()` enforces configurable limits (default 500 items, 90 days) to prevent unbounded JSONL growth.
- **Following metadata enrichment**: `following.json` now stores `site_title` and `author_name` from each followed author's `.well-known/polis`.
- **[Webapp] Feed auto-refresh**: Conversations tab polls for new items every 60 seconds.
- **[Webapp] Following metadata backfill**: Following list lazily enriches entries by fetching `.well-known/polis` from remote sites.
- **[Webapp] Following list improvements**: Displays site titles and author names instead of bare domains.
- **[Webapp] Activity stream cap**: Activity stream caps at 500 events, trimming oldest entries.

### Changed

- **Authenticated discovery queries**: Commands that query pending or denied blessings now use authenticated clients with signed request headers.
- **[Webapp] Authenticated discovery queries**: Handlers that query pending or denied blessings now use authenticated clients.

## [0.50.0]

### Breaking Changes

- **[Discovery] Unified content model**: Separate `posts_metadata` and `comment_metadata` tables replaced by `ds_content_metadata` (type-keyed) and `ds_relationship_metadata`. Schema version 0.8.0.
- **[Discovery] Table namespacing**: All tables renamed with `ds_` (user data) or `admin_` (operator data) prefixes.
- **[Discovery] Edge function namespacing**: All endpoints renamed with `ds-` or `admin-` prefix. Legacy endpoints removed: `posts-register`, `posts-unregister`, `posts-check`, `posts` (query), `comments-blessing-beseech`, `comments-blessing-grant`, `comments-blessing-deny`, `comments-blessing-requests`, `comments` (query), `polis-version`.
- **[Discovery] New endpoints**: `ds-content-register`, `ds-content-unregister`, `ds-content-check`, `ds-content-query`, `ds-relationship-update`, `ds-relationship-query`.
- **[Discovery] Canonical signing format**: Unified format `{"type":"polis.post","url":"...","version":"...","author":"...","metadata":{...}}` for content; `{"type":"polis.blessing","source_url":"...","target_url":"...","action":"...","timestamp":"..."}` for relationships.
- **[Discovery] Status terminology**: `blessed` → `granted` in relationship records.
- **Discovery client rewrite** (`pkg/discovery/client.go`): Type-specific methods removed. Replaced by generic methods: `RegisterContent()`, `UnregisterContent()`, `CheckContent()`, `QueryContent()`, `UpdateRelationship()`, `QueryRelationships()`.
- **`blessing.Deny()` signature changed**: Now takes `(commentURL, targetURL, client, privateKey)` instead of `(commentVersion, client, privateKey)`.
- **[Webapp] Deny blessing API**: `POST /api/blessing/deny` now requires `{"comment_url": "...", "in_reply_to": "..."}` instead of `{"comment_version": "..."}`.

### Added

- **`pkg/stream/` package**: Client-side projection framework for consuming the Discovery Stream
  - `store.go`: `Store` manages per-projection cursors and materialized state (`.polis/projections/<domain>/`)
  - `handler.go`: `ProjectionHandler` interface with built-in handler registry
  - `follow.go`: `FollowHandler` maintains follower set from `polis.follow.announced`/`removed` events
  - `blessing.go`: `BlessingHandler` processes blessing request/grant/deny events
  - `notification.go`: `NotificationHandler` generates notifications from events
- **Discovery client methods**: `RegisterContent()`, `UnregisterContent()`, `CheckContent()`, `QueryContent()`, `UpdateRelationship()`, `QueryRelationships()`, `StreamQuery()`, `StreamPublish()`, `StreamHealth()`, `MakeContentCanonicalJSON()`, `MakeStreamCanonicalJSON()`
- **Post discovery registration**: `publish.RegisterPost()` automatically registers posts with `ds-content-register`
- **Comment beseech extraction**: `comment.BeseechComment()` centralizes beseech logic (replaces ~130 lines of inline webapp code)
- **Stream event publishing**: `stream.PublishEvent()` for emitting events; follow/unfollow emit `polis.follow.announced`/`removed`
- **[Webapp] Activity stream**: `GET /api/activity` returns events from followed authors. New "Activity" view with timeline.
- **[Webapp] Followers view**: `GET /api/followers/count` uses projection framework. New "Followers" view with refresh.
- **[Webapp] Notification API**: `GET /api/notifications`, `GET /api/notifications/count`, `POST /api/notifications/read`. Background polling (60s) for blessing requests. Bell icon in header with badge and flyout.
- **[Webapp] Domain name in header**: Displays domain from `POLIS_BASE_URL`.
- **[Discovery] Pluggable validator registry**: `ContentTypeValidator` and `RelationshipTypeValidator` interfaces
- **[Discovery] Rate limiting**: Shared rate limiting on write endpoints (50-100 req/hour per domain)
- **[Discovery] Input validation**: URL (2048), email (RFC 5321), signature (2048), metadata (4KB), payload (64KB) limits
- **[Discovery] CORS hardening**: Explicit `Access-Control-Allow-Methods` and `Access-Control-Max-Age: 600`
- **[Discovery] Email verification**: `ds-content-register` verifies author email matches `.well-known/polis`
- **[Discovery] Denial protection**: Re-registering denied comments preserves denial status

### Changed

- **Business logic centralization**: Post registration, comment beseech, stream events moved from webapp to CLI packages
  - `publish.RegisterPost()` called by `PublishPost()`/`RepublishPost()`
  - `comment.BeseechComment()` replaces inline webapp logic
  - `stream.PublishEvent()` replaces webapp's `emitStreamEvent()`
  - Follow/unfollow events in `following.FollowWithBlessing()`/`UnfollowWithDenial()`
- **Single source for author email**: Removed `AuthorEmail` from webapp config. Read from `.well-known/polis` only.
- **[Webapp] Discovery config consolidation**: Removed `DiscoveryURL`/`DiscoveryKey` from `webapp-config.json`. Load from `.env` only.
- **[Webapp] "Feed" renamed to "Conversations"**: Sidebar and header updated.
- **Generator string standardization**: Accepts `polis-cli-go/X.Y.Z`, `polis-cli-bash/X.Y.Z`, `polis-cli/X.Y.Z`. Deprecated `polis-webapp`.
- **Discovery config propagation**: `cmd/root.go` and webapp `Initialize()` propagate config to `publish`, `comment`, `stream` packages
- Module path alignment: Changed from `polis-planning` to `polis-cli`

### Fixed

- **[Webapp] Posts not in discovery**: Posts weren't registered because `AuthorEmail` was empty
- **[Discovery] Email spoofing**: Verifies author email matches `.well-known/polis`
- **[Discovery] Denial bypass**: Re-registering denied comments preserves denial

## [0.49.0]

### Added

- **`pkg/feed/` package**: New importable package that extracts feed aggregation logic from `cmd/discover.go`. `feed.Aggregate()` fetches public indexes from followed authors, filters by `last_checked`, merges, sorts by published date, and updates timestamps.
- **`pkg/following/` social functions**: `FollowWithBlessing()` and `UnfollowWithDenial()` extract the blessing/denial side-effects from `cmd/follow.go` and `cmd/unfollow.go` into importable functions.
- **`pkg/feed/cache.go` — Feed cache with read tracking**: Persistent JSONL cache (`.polis/social/feed-cache.jsonl`) with `CacheManager` that supports Merge (dedup by deterministic sha256 ID), MarkRead, MarkUnread, MarkAllRead, MarkUnreadFrom, Prune (by age and count), and staleness detection via manifest (`.polis/social/feed-manifest.json`).
- **[Webapp] Social features — Following + Feed**: Two-mode sidebar ("My Site" / "Social") brings social reading into the webapp. Social mode includes Feed (aggregated posts from followed authors) and Following (author management with follow/unfollow).
- **[Webapp] Follow/Unfollow**: Follow panel to add authors by HTTPS URL (auto-blesses pending/denied comments). Unfollow with confirmation modal (warns about denying blessed comments).
- **[Webapp] Feed view**: Chronological feed of posts from followed authors with type badges, refresh button, unreachable-author warnings, and empty states.
- **[Webapp] Remote post viewer**: Slide-out panel renders remote posts with dark theme styling, fetched via new `/api/remote/post` endpoint.
- **[Webapp] API endpoints**: `GET/POST/DELETE /api/following`, `GET /api/feed`, `GET /api/remote/post?url=...`, `POST /api/feed/refresh`, `POST /api/feed/read`, `GET /api/feed/counts`
- **[Webapp] Feed cache — instant load + background refresh**: `GET /api/feed` now loads instantly from local cache. `POST /api/feed/refresh` runs network aggregation and merges into cache. Auto-refresh fires in background when cache is stale (default 15 minutes).
- **[Webapp] Feed read/unread tracking**: Unread items show bold title + teal dot indicator. Sidebar badge shows unread count. Opening an item marks it read (fire-and-forget). "Mark All Read" button in header.
- **[Webapp] Feed type filtering**: Filter tabs (All / Posts / Comments) above feed list, passed as `?type=` query param.
- **[Webapp] Feed hover actions**: "Mark Unread" and "Unread From Here" buttons appear on hover for read feed items only (hidden on unread items). "Unread From Here" marks the hovered item and all more recent items above it as unread. Styled like the FM toggle buttons, replaces the date column on hover.
- **[Webapp] Live markdown preview**: Editor now renders a live preview as you type (300ms debounce), replacing the manual "Render Preview" button. Ctrl+Enter now triggers publish instead of render.
- **[Webapp] Frontmatter toggle in editor**: Added "Hide FM" / "Show FM" toggle to the editor markdown pane header. When active, displays frontmatter in a non-editable mini-pane above the textarea. Frontmatter is never exposed in the editable textarea, preventing accidental edits to signatures and hashes. Shares the persisted setting with browser mode.

### Changed

- **`cmd/discover.go` refactored**: Now calls `feed.Aggregate()` instead of inline logic. Same CLI output format maintained.
- **[Webapp] `following.Version` propagation**: `server.go` `Initialize()` now propagates CLI version to the `following` package alongside `publish`, `comment`, and `metadata`.
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
