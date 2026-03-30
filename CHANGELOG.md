# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.60.0] - 2026-03-30

This release introduces the tag system, a registration marker architecture that eliminates network calls for checking registration state, five new Go packages for operational tooling, a Milkdown WYSIWYG editor in the webapp, shared base theme templates, and extensive test coverage improvements across the entire codebase.

### Added

- **[Bash CLI / Go CLI] `polis tag` command**: New content type `pub.polis.tag` lets authors tag their own and others' content across the network. Subcommands: `list`, `show`, `apply`, `remove`, `delete`. Tags are stored as signed JSON files with URI target lists and synced to the discovery service.
- **[Bash CLI / Go CLI] Registration marker system**: Local marker file at `.polis/ds/{ds-domain}/registered.json` serves as source of truth for "is this site registered?" All DS write operations (publish, comment, beseech, bless, deny, stream) are gated on this marker — no network call needed. Includes one-time backfill migration for existing sites.
- **[Bash CLI] Default avatar on init**: `polis init` now generates a default avatar with a random contrast-safe background color from a curated palette.
- **[Go CLI] `pkg/httppool`**: Shared HTTP transport with tuned connection pooling (200 max idle, 20 per host) for efficient TCP/TLS reuse across all outbound clients.
- **[Go CLI] `pkg/judge`**: Cross-boundary verification tool that validates the full trust chain — HTTP serving correctness, DS attestations, public key continuity, policy snapshots, index consistency, and cross-site comment signatures.
- **[Go CLI] `pkg/medic`**: Auto-remediation system that consumes patrol findings and applies safe, reversible fixes (permission corrections, quarantine of suspicious files).
- **[Go CLI] `pkg/tailor`**: Multi-version site diagnostic and auto-fixer that understands historical polis layouts and can upgrade from any version to current spec.
- **[Go CLI] `pkg/remote/cache`**: Thread-safe LRU cache with per-entry TTL expiry for caching remote content fetches and reducing redundant HTTP requests.
- **[Go CLI] `pkg/patrol` snapshot and volume checks**: New snapshot baseline system and volume-level integrity checks for detecting unauthorized modifications.
- **[Go CLI] Extensive new test coverage**: Added tests for blessing workflows, bundle loading, clone operations, discovery client, DS verification, DM encryption, feed handling, snippet resolution, and version history.
- **[Webapp] Milkdown WYSIWYG editor**: Rich text editing with live markdown preview, replacing the plain textarea for post and comment composition.
- **[Webapp] Navigation theme CSS**: New `nav-themes.css` for theme-aware navigation styling.
- **[Webapp] API handler and middleware tests**: New test suites for REST API dispatch layer and middleware (auth, CORS).
- **[Docs] Registration and privacy guide**: New `docs/general/registration-and-privacy.md` documenting the read-vs-write model, registration lifecycle, rate limits, and self-hosting options.

### Changed

- **[Go CLI] DS write operation gating**: Register, unregister, beseech, blessing-grant, blessing-deny, post, and stream commands all check the local registration marker before calling the discovery service.
- **[Go CLI] Key rotation conditional**: DS notification on key rotation only fires when the site is registered locally.
- **[Go CLI] Feed architecture overhaul**: Feed items now support posts, comments, and announcements with event type tracking and improved cache management.
- **[Go CLI] DM encryption improvements**: Enhanced send, receive, and store operations with improved error handling.
- **[Webapp] Unified sync handler**: Background sync uses a `SyncResult` aggregator tracking new notifications, feed items, follower changes, and file changes that trigger re-renders.
- **[Webapp] API middleware stack**: New `authMiddleware` for Bearer token validation on write routes and `corsMiddleware` enforcing same-origin policy.
- **Themes: shared `_base/` templates**: All page templates (index, post, posts, comment, comment-inline, tag, tag-index) and snippets now live in `themes/_base/`. CSS-only themes no longer need to duplicate HTML — they inherit from `_base/` automatically.
- **Docs**: Updated dispatch engine, command reference, JSON mode, templating, DS configuration, API reference, stream architecture, content system, security model, and webapp user manual.

### Fixed

- **[Bash CLI] Tag command path resolution**: Fixed undefined `$DATA_DIR` variable in tag functions — paths now use relative CWD-based resolution consistent with the rest of the bash CLI.

## [0.59.0] - 2026-03-17

This release adds security hardening across the Go CLI and webapp, enriched comment context cards (showing the title and excerpt of the post being replied to), a Milkdown WYSIWYG editor in the webapp, and theme improvements including dynamic widget versioning.

### Added

- **[Go CLI] HTML sanitization for markdown rendering**: Markdown output is now passed through bluemonday to strip dangerous elements (scripts, iframes, event handlers) while preserving structural HTML like `details`, `video`, `audio`, `figure`, and custom `class`/`id` attributes that themes depend on.
- **[Go CLI] `pkg/policycheck`**: Reusable remote policy evaluation package for checking whether a remote site accepts events (e.g., DMs) from a given domain. Two-phase evaluation: public policy rules first, mutual-follow fallback if no DM rules are defined.
- **[Go CLI] `FetchPolicies` and `FetchFollowingList` in `pkg/remote`**: Clients can now fetch a site's published `policies/rules.jsonl` and `following.json` for DM eligibility evaluation.
- **[Go CLI] `StreamQueryInvolved` and `StreamQueryBatch` in discovery client**: New unified DS stream endpoints for fetching events by involved domain or sending multiple filter sets in a single HTTP request.
- **[Go CLI] `Bundle.MergeDefaults`**: Adds content types from a default bundle to a site bundle without overwriting existing entries.
- **[Go CLI] `ValidatePublicKey` and `ZeroKey` in `pkg/signing`**: Public key format validation and secure key zeroing after signing operations to minimize the window where private key bytes exist in memory.
- **[Go CLI] Hook path containment and timeout**: Hooks are now verified to resolve within the site directory (preventing path traversal), and execute with a 30-second timeout. A new `HookTimeout` constant is exported.
- **[Webapp] DM recipients panel**: New `/api/dm/recipients` endpoint performs concurrent remote policy checks to show which followed authors accept DMs, with 5-minute result caching.
- **[Webapp] DM unread count**: The status endpoint now includes a `dm_unread` count derived from the DM store index.
- **Themes: `{{widget_version}}` template variable**: All theme `polis-widget.html` snippets now use `{{widget_version}}` instead of a hardcoded version string. The widget version is managed via a single `WidgetVersion` constant in `pkg/render/page.go`, so theme files never need manual version bumps.

### Changed

- **[Go CLI] Domain case-normalization (RFC 1123)**: DNS hostnames are now consistently lowercased throughout the DM, discovery, following, policy, and stream packages, preventing case-mismatch failures in domain comparisons and conversation IDs.
- **[Go CLI] Compile-time regex precompilation**: Frontmatter parsing regexes in `comment`, `publish`, and `render/page` packages are now package-level variables instead of being recompiled on every call.
- **[Go CLI] Default policy set includes `omit pub.polis.feed from self` and `deny all from all`**: Self-authored feed events are suppressed by policy rule rather than hardcoded logic. Unknown content types are denied by default.
- **[Go CLI] `ShouldSuspendSync` cooldown**: Sync suspension now lifts after a configurable cooldown period (default 5 minutes) to allow retry attempts after consecutive DS failures.
- **[Go CLI] DM store zeroes private key seed after derivation**: The seed is zeroed immediately after the storage key is derived.
- **[Go CLI] `.md` URL normalization for reply context**: Comment `in_reply_to_url` values ending in `.md` are converted to `.html` for the rendered link target.
- **[Webapp] Same-origin CORS policy**: CORS middleware now passes requests through without adding CORS headers, enforcing browser same-origin policy. Previously allowed all origins.
- **[Webapp] Unified DS stream query**: Background sync now uses a single DS `/v1/stream/batch` request (with `involvedDomain`) instead of three separate target/source/followed-authors queries, reducing DS load.
- **[Webapp] `StopSync()` method**: Gracefully stops the background sync goroutine; called automatically on `Close()`.
- **Themes: context card redesign**: Comment pages in all themes now display the parent post's title, domain, and excerpt (fetched from the remote source) in a structured context card, replacing the bare URL link.

### Fixed

- **[Go CLI] Patrol package: `CheckTenant` function**: Public version now matches simplified interface (key/signature/hash checks only).
- **[Webapp] Editor panel mode**: Editor right panel mode preference was removed; the editor now uses a fixed layout.
- **[Webapp] Sync goroutine leak**: Fixed goroutine leak on tenant cache eviction by properly tracking and stopping background sync goroutines.

## [0.58.0] - 2026-03-07

This release adds end-to-end encrypted direct messages, a major webapp UI redesign, DS signature verification with two-layer cryptographic trust, policy engine extensions, and a migration of all hardcoded blessing/notification logic to user-configurable declarative policies.

### Added

- **[Go CLI] Direct messages (`polis dm`)**: New `pub.polis.dm` content type with end-to-end encryption. Ed25519-to-X25519 key conversion, NaCl box transport, and secretbox storage. Subcommands: `polis dm list`, `read`, `send`, `retry`, `config`. Per-sender and global rate limiting via `POLIS_DM_RATE_*` / `POLIS_DM_MAX_SIZE` environment variables. Policy-based acceptance control.
- **[Go CLI] DS signature verification (`pkg/discovery/ds_verify.go`)**: Clients now verify Ed25519 envelope signatures on all discovery service responses. Cached with TTL, retries once on key rotation. Verification failures tracked per-domain with anomaly detection: sync suspended after 3 consecutive DS failures, author-level warning after 5 failures from the same domain within 24 hours. Configurable via `verification.json`.
- **[Go CLI] Author signature verification (`pkg/discovery/author_verify.go`)**: Stream events are now individually verified against the originating author's public key, closing the attack vector where a compromised DS could inject fake follows or fabricate blessing grants.
- **[Go CLI] `pkg/stream/verification_state.go`**: Tracks per-domain verification counters and anomaly state across sync sessions.
- **[Go CLI] `pkg/site/dirs.go`**: Shared directory path constants for site layout.
- **[Webapp] v2 UI redesign**: New topbar layout, dual-theme (dark/light), and redesigned feed, posts, comments, and blessings views. Button styles unified to outlined/ghost across all panels. Excerpts added to feed cards, post rows, and comment rows.
- **[Webapp] Avatar support**: User avatars displayed in the feed and settings. Avatar randomizer and display name configuration added to Settings. Custom avatars shown without initials overlay; reset restores initials.
- **[Webapp] Webapp middleware (`internal/server/middleware.go`)**: Request middleware extracted into dedicated file.

### Changed

- **[Bash CLI] Discovery API paths updated to `/v1/`**: All 14 endpoint paths in the bash CLI now match the DS `/v1/` REST redesign (e.g. `ds-content-register` → `/v1/content`).
- **[Go CLI] Policy engine: `emit`/`omit` verbs and `self`/`thread-blessed` sources**: Policy rules can now control event emission and filter by thread-blessed status. All hardcoded blessing, notification, and event-gating logic migrated to user-configurable policy rules.
- **[Go CLI] `.polis/` directory permissions**: Changed from default to `0700` for improved security.
- **[Webapp] Feed sort uses parsed timestamps**: Feed chronological ordering now parses timestamps instead of string-comparing, fixing sort order regressions.
- **[Webapp] Feed counts blessed comments as replies**: Fixed feed not including blessed comments in reply counts.
- **[Webapp] Remote site avatars**: Fetches and displays remote site avatars in feed items.

### Fixed

- **[Webapp] Conversations click handler**: Fixed SyntaxError in conversations click handler; restored Mark Unread / Unread From Here hover actions.
- **[Webapp] Mark Unread button**: Fixed button escaping and click bubbling issues.
- **[Webapp] Following empty state**: Restored heading on Following empty state CTA.
- **[Webapp] Blessed comments not rendering**: Fixed path mismatch (`/post/` vs `/posts/`) that prevented blessed comments from appearing.
- **[Webapp] Index page comment count**: Fixed comment count on index pages when using source content paths.
- **[Go CLI] DS key format mismatch**: Fixed conversion of raw base64 DS keys to `ssh-ed25519` format.

## [0.57.0] - 2026-03-02

This release introduces the bundle system, policy engine, content-type dispatch API, source/output separation for rendering, structured event logging, and a major webapp restructure. It also includes security hardening round 4 and a documentation reorganization into a component/audience hierarchy.

### Added

- **Bundle system (`pkg/bundle/`)**: Declarative content-type bundles that define how content is structured, validated, and rendered.
- **Path resolver (`pkg/resolve/`)**: Centralized path resolution for content, metadata, and configuration files.
- **Content-type dispatch engine (`pkg/ops/`)**: Type-aware operation routing that dispatches create/read/update/delete operations to the correct handler based on content type.
- **Policy engine (`pkg/policy/`)**: Declarative rule evaluation system for content policies (e.g., blessing requirements, publish constraints).
- **REST API (`webapp/internal/api/`)**: New `/v1/` content-type dispatch layer with router, handlers, middleware (auth, CORS, per-route body limits).
- **Structured event logging**: JSON-formatted event output to stdout with `pub.polis.*` event types for machine-readable log consumption.
- **SSE-gated background sync**: Webapp background sync (notifications, feed) only runs when at least one SSE client is connected, reducing idle resource usage.
- **Per-route body size limits**: HTTP request body limits configured per endpoint to prevent oversized payloads.
- **Content source-to-mount redirect**: 301 redirects from content source paths to their rendered mount paths.
- **New themes**: `especial`, `especial-light`, `vice` themes added.

### Changed

- **`polis.*` → `pub.polis.*` event types**: All event types, content types, and relationship types renamed with `pub.` prefix for namespace clarity.
- **Webapp flattened**: `webapp/localhost/` directory structure flattened to `webapp/` — the `localhost/` nesting level is removed.
- **Route extraction**: HTTP route registration extracted from `server.go` into dedicated `routes.go` file.
- **Source/output separation for rendering**: Render pipeline now distinguishes content source files from generated output files.
- **Documentation restructured**: Flat `docs/*.md` files reorganized into `docs/<component>/<audience>/` hierarchy (cli, webapp, api, ds, general).
- **Go version requirement**: Bumped from 1.22 to 1.24.
- **Makefile**: Updated all build paths for webapp restructure; added `linux/arm64` to release targets; fixed output path depth for webapp/bundled builds.

### Security

- **Round 4 hardening**: XSS fixes in template rendering, open redirect prevention, path traversal guards, CORS policy tightening for SSE, rate limiting on auth endpoints, IP spoofing mitigation, TOCTOU fix in magic link verification, sanitized error responses, security headers.

### Documentation

- **New docs**: `general/content-system.md` (bundles, content types, events), `api/user/reference.md` (REST API reference), `api/developer/dispatch-engine.md` (engine architecture).
- **Updated docs**: Security model, vision, glossary, CLI packages, webapp development guide, DS references.
- **Removed from public**: `ops/` (internal infrastructure), `archive/` (business planning).

## [0.56.0] - 2026-02-27

This release adds the `unpublish` command, introduces the `patrol` integrity-checking package, removes the `migrate` command and all migration code, hardens discovery service API authentication, simplifies key rotation with transition signatures, and ships the new studio13 theme.

### Added

- **`unpublish` command (Bash + Go CLI)**: Remove a published post, deleting local files and unregistering from the discovery service with a signed unregister request.
- **`patrol` package (Go)**: New integrity-checking package for verifying site content signatures and consistency.
- **`MarkdownToPlainText()` renderer**: Converts markdown to plain text with HTML stripping, entity unescaping, and word-boundary-aware truncation — used for generating excerpt text.
- **`{{excerpt}}` template variable**: New template variable available in post/comment templates for plain-text content summaries.
- **`MakeContentUnregisterCanonicalJSON()`**: New canonical JSON builder for signed content unregistration.
- **`MakeKeyRotationCanonicalJSON()` and `RotateKey()`**: New discovery client functions for cryptographic key rotation with transition signatures.
- **New `studio13/` theme**: Community-contributed theme by Nick Katsivelos (https://s13.nyc).
- **`comment-inline.html` snippet**: Added to all themes for inline comment rendering in post pages.

### Changed

- **Key rotation rewritten**: `rotate-key` now uses transition signatures — signs a rotation message with the old key before swapping, then notifies the discovery service. No longer backs up old keys to `.old` files.
- **Discovery client API auth hardening**: All HTTP methods now conditionally set the `Authorization` header only when an API key is present, preventing empty bearer tokens.
- **`SiteCheckResponse.RegisteredAt` → `CreatedAt`**: Field renamed to match updated discovery service schema.
- **Goldmark HTML renderer import alias**: Renamed from `html` to `goldhtml` to avoid conflict with stdlib `html` package.
- **Template engine**: Added `excerpt` to the set of recognized variables; section processing improvements.
- **Webapp handlers, routes, and server**: Expanded with new API endpoints, improved error handling, and UI updates.
- **Webapp web UI**: Updated `app.js`, `index.html`, and `style.css` with new features and styling.
- **Theme catalog**: Expanded `catalog.html` with studio13 and additional theme metadata.
- **All themes**: Added `comment-inline.html` snippet to post templates; minor CSS additions.
- **Docs**: Updated TEMPLATING.md, SECURITY-MODEL.md, USAGE.md, and WEBAPP-USER-MANUAL.md.
- **Completions**: Removed `migrate`/`migrations` from bash and zsh completions; added `unpublish`.

### Removed

- **`polis migrate` command**: Domain migration feature removed from both CLIs.
- **`polis migrations apply` command**: Migration application subcommand removed.
- **`pkg/migrate/` package**: Entire migration package deleted.

## [0.55.0] - 2026-02-24

This release adds an About page editor to the webapp, introduces theme switching from the Settings panel, ships new `also-reading` and `polis-widget` snippets for all six built-in themes, and delivers a range of webapp usability improvements.

### Added

- **[Go CLI] `theme.ExtractPalette()` and `theme.ListThemesWithPalettes()`**: New functions parse CSS `--color-*` variables from theme stylesheets to produce color swatches for UI previews.
- **`also-reading.html` snippet for all 6 themes**: New theme snippet enables an "Also Reading" sidebar section across especial, especial-light, sols, turbo, vice, and zane themes.
- **`polis-widget.html` snippet for all 6 themes**: New theme snippet provides a standardized Polis widget embed block for all built-in themes.
- **[Webapp] Theme switcher in Settings**: Users can now switch their site theme from the Settings panel. A grid of available themes with color palette swatches is displayed; selecting a theme updates the manifest, copies the CSS, and re-renders the site immediately.
- **[Webapp] About page editor**: New editor for the global `about.html` snippet, displayed in the Snippets sidebar across all site lifecycle stages. Mirrors the full-screen layout of the post editor.
- **[Webapp] `POST /api/settings/theme` endpoint**: Switches the active theme with validation, CSS copy, and full site re-render.
- **[Webapp] `POST /api/settings/sync` endpoint**: New sync endpoint with a DELETE handler for the widget API.

### Changed

- **[Webapp] Consolidated Conversations tab**: The Social sidebar now presents a single Conversations view (merging the previous Activity and Posts & Comments tabs). All feed type badges use a consistent green color.
- **[Webapp] My Comments tab order**: Tabs reordered to All, Drafts, Blessed, Pending, Denied.
- **[Webapp] Standardized Refresh buttons**: Action buttons across all views now uniformly labeled "Refresh".
- **[Webapp] Deep-link intent paths updated**: Follow intents now redirect to `/_/social/following?intent=follow&target=X`; comment intents route to `/_/comments/new`. Legacy root-path intents remain supported.
- **[Webapp] Snippets sidebar visibility**: Snippets (About) sidebar shown in all lifecycle stages including `first_post`.
- **"Space" terminology**: Post-init welcome message uses "space" instead of "site" in the onboarding message.

### Fixed

- **[Webapp] Widget follow/unfollow toggle**: Public site widget now includes a Follow/Unfollow toggle for logged-in users.
- **[Webapp] CORS headers**: Metadata files and the widget DELETE endpoint now include correct CORS headers.
- **[Webapp] `blessing.granted` stream event**: Fixed actor field to correctly identify the granter instead of the recipient.
- **[Webapp] Webapp version in metadata files**: Fixed webapp writing `"dev"` as the version string in metadata files.
- **[Webapp] Sidebar snippets section placement**: Fixed `sidebar-section-snippets` placement inside `sidebar-my-site`.
- **[Webapp] About editor textarea styling**: About editor textarea now matches the post editor styling.
- **[Webapp] Feed item display**: Fixed grouped feed item title fallback and URL display; fixed underline on grouped feed item links.

## [0.54.0] - 2026-02-17

This release shifts author identity from email to domain, simplifies the webapp with a streamlined sidebar and improved onboarding, and adds documentation for contributing, webapp usage, and security policy.

### Changed

- **Domain-based author identity**: Author identity now uses the domain (from `.well-known/polis` `domain` field or extracted from `POLIS_BASE_URL`) instead of email. Email is retained as a private legacy fallback. This affects comment frontmatter, discovery service registration, and blessing operations across both CLIs.
- **[Bash CLI] `fetch_author_identity_from_wellknown()`**: Renamed from `fetch_author_email_from_wellknown()` with domain-first resolution (domain field > URL extraction > email fallback). Old name kept as backward-compatible alias.
- **[Go CLI] Discovery registration uses domain**: `publish.RegisterPost()` and `comment.BeseechComment()` now pass domain identity to the discovery service instead of email.
- **[Go CLI] Comment frontmatter includes email**: Comment creation now includes the author's email in frontmatter metadata when available.
- **[Go CLI] Init flow simplified**: `site.Init()` accepts explicit Author and Email fields for headless provisioning scenarios.
- **[Go CLI] Package state refactored for reuse**: `publish`, `comment`, and `stream` packages now accept discovery config as function parameters instead of relying on package-level globals.
- **[Webapp] Simplified UI**: Removed browser split-pane mode (~2000 lines), replaced with progressive sidebar that adapts to site lifecycle stage. Merged blessing requests into a single view. Improved empty states and onboarding experience.
- **[Webapp] Welcome flow**: New users see an educational "What just happened?" disclosure after init, replacing the previous multi-banner approach.

### Fixed

- **[Go CLI] Verify command robustness**: Improved error handling for edge cases in signature verification.
- **[Go CLI] Template section rendering**: Additional test coverage for section rendering edge cases.
- **[Webapp] Editor button fix**: Fixed missing `updateEditorFmToggle()` that prevented "create new post" buttons from working.
- **[Webapp] Dashboard CTA clutter**: Reduced call-to-action buttons from 4 to 2 on My Site tab.

### Added

- **[Go CLI] New tests**: Added test suites for `comment`, `site/init`, `site/wellknown`, `render/page`, and `template/engine` packages.
- **[Go CLI] Render page helpers**: New page rendering utilities in `render/page.go` for index page generation.

### Documentation

- **New: CONTRIBUTING.md**: Covers all four components (Go CLI, webapp, bash CLI, discovery service) with build commands, prerequisites, and conventions.
- **New: WEBAPP-USER-MANUAL.md**: User guide for the localhost webapp with deployment instructions.
- **New: SECURITY.md**: Security policy with vulnerability reporting instructions.
- **New: docs/README.md**: Navigation index for the documentation directory.
- **Updated: USAGE.md**: Added Go CLI section, corrected installation paths.
- **Updated: SECURITY-MODEL.md**: Fixed key paths (`polis_key` → `id_ed25519`).
- **Updated: GLOSSARY.md**: Added TUI deprecation note.
- **Updated: DISCOVERY-STREAM-ARCHITECTURE.md**: Fixed cross-references.
- **Skills paths updated**: Cleaned up command references across skill files.

## [0.53.0] - 2026-02-13

This release improves homepage performance with limited post/comment sections and archive pages, adds quality of life improvements to the webapp (feed auto-refresh, following metadata display), and introduces authenticated discovery queries for privacy-sensitive operations.

### Breaking Changes

- **[Discovery] Authenticated queries for sensitive data**: Discovery service read endpoints now enforce access controls. Unauthenticated callers to `ds-content-query` only see blessed comments (pending/denied are filtered). Querying `ds-relationship-query` for `status=pending` or `status=denied` blessing records requires signed authentication headers (`X-Polis-Domain`, `X-Polis-Signature`, `X-Polis-Timestamp`). The `ds-stream` endpoint filters out `polis.blessing.denied` events for unauthenticated consumers. CLI commands that query pending/denied blessings (`polis blessing requests`, `polis follow` auto-bless, `polis notifications`) now use authenticated clients. Clients older than 0.53.0 will see reduced data from these endpoints.

### Added

- **Homepage post/comment limits**: New `{{#recent_posts}}` and `{{#recent_comments}}` template sections limit homepage display to the 10 most recent items. All 6 built-in themes (especial, especial-light, sols, turbo, vice, zane) updated to use these sections instead of unbounded `{{#posts}}`/`{{#comments}}` loops.
- **Archive page**: Running `polis render` now generates `posts/index.html` with a chronological list of all posts. Themes with a `posts.html` template automatically get this page. All built-in themes include the new archive template.
- **"View all posts" link**: Homepage displays a "View all N posts" link when you have more than 10 posts, linking to the archive page.
- **[Go CLI] Signed GET authentication**: Discovery client supports optional domain ownership proof on GET requests via authentication headers. New `discovery.NewAuthenticatedClient()` constructor for operations requiring proof of domain ownership.
- **Notification pruning**: `notification.Prune()` enforces configurable limits (`max_items` default 500, `max_age_days` default 90) to prevent unbounded JSONL growth. Configured via `config/notifications.json`.
- **Following metadata enrichment**: `following.json` now stores `site_title` and `author_name` from each followed author's `.well-known/polis`, captured during follow operations.
- **[Webapp] Feed auto-refresh**: Conversations tab polls for new items every 60 seconds, updating the sidebar badge and re-rendering the list when new posts or comments arrive.
- **[Webapp] Following metadata backfill**: The following list lazily enriches up to 3 entries per request by fetching `.well-known/polis` from remote sites, gradually populating missing metadata.
- **[Webapp] Following list improvements**: Following page now displays site titles and author names instead of bare domain names, and shows the `added_at` date when you started following each author.
- **[Webapp] Activity stream cap**: Activity stream now caps at 500 events, automatically trimming oldest entries to prevent unbounded memory growth.

### Changed

- **[Go CLI] Authenticated discovery queries**: Commands that query pending or denied blessings (`polis blessing requests`, `polis follow`, `polis notifications`, `polis comment sync`) now use authenticated clients with signed request headers.
- **[Webapp] Authenticated discovery queries**: Handlers that query pending or denied blessings (notification sync, comment sync, blessing requests, follow/unfollow) now use authenticated clients.

## [0.52.0] - 2026-02-12

### Security

- **Template injection fix**: User-supplied data containing `{{> partial}}` syntax in section loops could trigger unintended partial includes. Both `engine.go` and `sections.go` now sanitize interpolated values.

### Added

- **[Webapp] Following page onboarding**: New users see a guided card to discover authors via discover.polis.pub with one-click follow.
- **[Webapp] Unread toggle on Conversations**: "Unread Only" / "Show All" toggle on the Conversations page, persisted across reloads.
- **[Webapp] Local timestamps**: All feed items now display timestamps in the user's local timezone.
- **Auto-merge default notification rules**: New default rules (e.g., `new-post`) are automatically merged into existing user rule sets on upgrade.

### Fixed

- **[Webapp] Notification cursor comparison**: Fixed cursor stuck at single-digit positions by switching from lexicographic to numeric comparison.
- **[Webapp] Duplicate notifications**: Improved dedup key logic to prevent duplicate notifications from the same author.
- **[Webapp] Slow notification bell**: Async notification sync eliminates 2-3s delay on page load.
- **[Webapp] Toggle color theming**: Consistent toggle styling across light and dark themes.
- **[Webapp] Conversations auto-refresh**: Conversations page now refreshes automatically when opened.
- **[Webapp] "X new items" toast**: Shows actual new item count instead of total count.
- **[Webapp] Feed title truncation**: Long titles are properly truncated with CSS ellipsis.

### Changed

- **DS state filenames standardized**: `notifications.jsonl` → `polis.notification.jsonl`, `feed-cache.jsonl` → `polis.feed.jsonl`.
- **Usage message corrected**: `polis publish` → `polis post` in help output.

## [0.51.0] - 2026-02-11

This release replaces HTTP polling with stream-driven architecture for feed and notifications, adds three new themes, restructures per-discovery-service data directories, and removes notification CLI subcommands in favor of the webapp.

### Breaking Changes

- **Notification subcommands removed**: `polis notifications read`, `dismiss`, `sync`, and `config` are removed from both CLIs. Only `polis notifications [list]` remains. These operations are better handled by the webapp.
- **`NewManager()`/`NewCacheManager()` signatures changed**: Both now take `(dataDir, discoveryDomain)` instead of just `(dataDir)`. All callers must be updated.
- **DS directory restructure**: Per-discovery-service data moved from `.polis/projections/<domain>/` to `.polis/ds/<domain>/` with `config/` (user preferences) and `state/` (computed data) subdirectories. Old social paths (`.polis/social/feed-cache.jsonl`, `.polis/social/feed-manifest.json`) also relocated.
- **`feed.Aggregate()` removed**: Replaced by stream-driven `FeedHandler`. `AggregateOptions`, `AggregateResult`, `NotFollowingError`, and `extractDomain()` also removed. `last_checked` field removed from `following.json`.
- **`--register` flag removed from `polis init`**: Init is now local-only. Deploy first, then run `polis register` separately.

### Added

- **3 new themes**: Vice (GTA Vice City / Miami Vice aesthetic), Especial (gold on near-black, Modelo Especial inspired), and Especial Light (gold on warm fog, WCAG AA compliant). Theme catalog available at `themes/catalog.html`.
- **Stream-driven feed system**: Feed refresh now queries the discovery service stream instead of polling each author's site. Single query per sync using cursor-based pagination. Background sync in webapp every 60 seconds.
- **`FeedHandler`**: New stream event to `FeedItem` transformer. Handles post/comment published/republished events, skips self-authored content.
- **`ListFiltered()` with composable filters**: Feed items filterable by `type` and `status` (e.g., `?type=post&status=unread`).
- **Rule-driven notification system**: Notifications generated by matching stream events against configurable rules. Eight default rules covering: new-follower, lost-follower, blessing-requested, blessing-granted, blessing-denied, new-comment, updated-comment, new-post. Rules can be enabled/disabled per user.
- **`notification.DefaultRules()`**: Built-in rule set seeded on first sync. `ResolveTemplate()` for `{{var}}` substitution. `DedupeKey()` for preventing duplicate notifications across syncs.
- **Post discovery registration (Bash CLI)**: `_register_post_with_discovery()` registers posts with discovery service after publish/republish. Non-fatal if discovery not configured.
- **Follow/unfollow stream events (Bash CLI)**: `_emit_stream_event()` publishes `polis.follow.announced`/`polis.follow.removed` events, matching Go CLI behavior.
- **[Webapp] Setup wizard**: After initializing a new site, a 2-step wizard guides users through Deploy and Register. Dismissable and re-openable from dashboard banner.
- **[Webapp] Init panel overhaul**: Now collects Base URL, Discovery Service URL, and Discovery Service Key. DS fields in collapsible section with defaults. All values written to `.env` on init.
- **[Webapp] Feed sync after follow**: Following a new author triggers immediate background feed sync.

### Changed

- **"Feed" renamed to "Conversations"** in webapp sidebar and header.
- **Discovery config moved from `webapp-config.json` to `.env` only**: `DiscoveryURL` and `DiscoveryKey` removed from webapp config struct. Now loaded exclusively from environment variables, matching CLI behavior.
- **Go CLI now loads `.env` files on startup**: Search order `cwd/.env` then `~/.polis/.env`, matching bash CLI and webapp. Previously discovery-dependent commands silently failed when credentials were only in `.env`.
- **Business logic centralized in CLI packages**: Post registration (`publish.RegisterPost()`), comment beseech (`comment.BeseechComment()`), and stream event publishing (`stream.PublishEvent()`) moved from webapp handlers into `cli-go/pkg/`. Webapp is now a thin HTTP layer.
- **Single source of truth for author email**: Removed `AuthorEmail` from webapp config. Email read exclusively from `.well-known/polis` via `GetAuthorEmail()`.
- **`polis discover` uses stream-based discovery**: Queries DS stream instead of polling each author's site. `--since` flag removed (stream cursor replaces per-author timestamps).
- **Notification state format**: Notifications stored in `.polis/ds/<domain>/notifications/state.jsonl` with pre-rendered icon/message fields.
- **Improved `polis register` error messaging**: Helpful hint when discovery service can't reach `.well-known/polis`.

### Fixed

- **`site.Init()` default `view_mode`**: Fixed `webapp-config.json` to default to `"list"` instead of `"browser"`.
- **`GetSiteTitle()` fallback**: Now falls back to `POLIS_BASE_URL` env var when `.well-known/polis` has no `site_title`.
- **Re-registration after keypair regeneration**: `polis register` now works after `polis init` regenerates the keypair, without needing to unregister first.

## [0.50.0] - 2026-02-09

This release represents a major architectural refactoring of the discovery service infrastructure. The update unifies the content model, introduces extensible primitive types, establishes a new stream-based projection system, and consolidates business logic into the CLI packages where it belongs.

### Breaking Changes

- **[Discovery] Unified content model**: The separate `posts_metadata` and `comment_metadata` tables have been replaced by `ds_content_metadata` (type-keyed) and `ds_relationship_metadata`. This eliminates type-specific database tables and enables arbitrary content types through a pluggable validator registry. Schema version bumped to 0.8.0.
- **[Discovery] Table and endpoint namespacing**: All database tables now use `ds_` (user data) or `admin_` (operator data) prefixes. All edge function endpoints renamed with `ds-` or `admin-` prefix to match. Legacy endpoints removed: `posts-register`, `posts-unregister`, `posts-check`, `posts` (query), `comments-blessing-beseech`, `comments-blessing-grant`, `comments-blessing-deny`, `comments-blessing-requests`, `comments` (query), `polis-version`.
- **[Discovery] New unified endpoints**: `ds-content-register`, `ds-content-unregister`, `ds-content-check`, `ds-content-query`, `ds-relationship-update`, `ds-relationship-query`.
- **[Discovery] New canonical signing format**: All discovery operations now use a unified canonical JSON format: `{"type":"polis.post","url":"...","version":"...","author":"...","metadata":{...}}` for content registration, and `{"type":"polis.blessing","source_url":"...","target_url":"...","action":"...","timestamp":"..."}` for relationship operations.
- **[Discovery] Status terminology change**: Blessing status `blessed` renamed to `granted` throughout relationship records.
- **[Discovery] `polis_versions` table removed**: Version checking endpoint no longer exists.
- **[Go CLI] Discovery client rewrite**: Type-specific methods (`RegisterPost`, `BeseechBlessing`, `GrantBlessing`, `DenyBlessing`, etc.) replaced by generic methods: `RegisterContent()`, `UnregisterContent()`, `CheckContent()`, `QueryContent()`, `UpdateRelationship()`, `QueryRelationships()`.
- **[Go CLI] `blessing.Deny()` signature changed**: Now takes `(commentURL, targetURL, client, privateKey)` instead of `(commentVersion, client, privateKey)`.
- **[Webapp] Deny blessing API change**: `POST /api/blessing/deny` now requires `{"comment_url": "...", "in_reply_to": "..."}` instead of `{"comment_version": "..."}`.

### Added

- **[Go CLI] Stream projection framework** (`pkg/stream/`): Client-side projection system for consuming the Discovery Stream. The `Store` manages per-projection cursors and materialized state on disk, namespaced by discovery service domain (`.polis/projections/<domain>/`). Built-in handlers:
  - `FollowHandler`: Maintains follower set from `polis.follow.announced`/`removed` events
  - `BlessingHandler`: Processes blessing request/grant/deny events
  - `NotificationHandler`: Generates notifications from follow, blessing, and post events
- **[Go CLI] Canonical payload builders**: `MakeContentCanonicalJSON()` and `MakeStreamCanonicalJSON()` in the discovery client for standardized Ed25519 signing.
- **[Go CLI] Post discovery registration**: `publish.RegisterPost()` now automatically registers posts with `ds-content-register` after publishing/republishing (non-fatal if discovery not configured).
- **[Go CLI] Comment beseech extraction**: New `comment.BeseechComment()` function centralizes comment beseech logic, replacing ~130 lines of inline webapp code.
- **[Go CLI] Stream event publishing**: New `stream.PublishEvent()` for emitting stream events. Follow/unfollow now emit `polis.follow.announced`/`removed` via `following.FollowWithBlessing()` and `UnfollowWithDenial()`.
- **[Webapp] Activity stream**: `GET /api/activity` returns events from followed authors via the Discovery Stream with cursor-based pagination. New "Activity" view under Discover sidebar section shows event timeline with actor and action badges.
- **[Webapp] Followers view**: `GET /api/followers/count` uses the projection framework to maintain a materialized follower set. New "Followers" view in Stats section with refresh capability.
- **[Webapp] Notification system**: Three new endpoints (`GET /api/notifications`, `GET /api/notifications/count`, `POST /api/notifications/read`) with background polling (60s interval) for blessing requests. Notification bell icon in header with red dot badge and flyout panel.
- **[Webapp] Domain name in header**: Header now displays the domain name from `POLIS_BASE_URL`.
- **[Discovery] Pluggable validator registry**: New `ContentTypeValidator` and `RelationshipTypeValidator` interfaces enable adding content types without edge function changes.
- **[Discovery] Rate limiting**: Shared rate limiting on all write endpoints (50-100 requests/hour per domain depending on endpoint).
- **[Discovery] Input validation hardening**: URL length limits (2048 chars), email validation (RFC 5321), signature limits (2048 chars), metadata limits (4KB), total payload limits (64KB).
- **[Discovery] CORS hardening**: Explicit `Access-Control-Allow-Methods` and `Access-Control-Max-Age: 600` on all endpoints.
- **[Discovery] Author email verification**: `ds-content-register` now verifies the `author` email matches the `email` field in `.well-known/polis` (403 `AUTHOR_MISMATCH` on mismatch).
- **[Discovery] Blessing denial protection**: Re-registering a denied comment no longer resets blessing status to pending.

### Changed

- **[Go CLI] Business logic centralization**: Post registration, comment beseech, and stream event publishing moved from webapp handlers into CLI packages. The webapp is now a thin HTTP layer over the CLI.
- **[Go CLI] Single source for author email**: Removed `AuthorEmail` from webapp config struct. Email now read exclusively from `.well-known/polis` via `GetAuthorEmail()`.
- **[Go CLI] Generator string standardization**: Accepted patterns are `polis-cli-go/X.Y.Z` (primary), `polis-cli-bash/X.Y.Z`, and `polis-cli/X.Y.Z` (legacy). `polis-webapp` deprecated.
- **[Webapp] Discovery config consolidation**: `DiscoveryURL` and `DiscoveryKey` removed from `webapp-config.json`. Now loaded exclusively from `.env`/environment variables, matching CLI behavior.
- **[Webapp] "Feed" renamed to "Conversations"**: Social tab sidebar item and header updated for clarity.
- Field naming standardization: `owner_signature` → `signature` in register/unregister operations
- README rewritten as reference implementation guide
- Generator format version strings: Metadata files now write version as `polis-cli/$VERSION` instead of bare `$VERSION`

### Fixed

- **[Bash CLI] Registration bug**: `cmd_register` was reading `.author_name` instead of `.author` from `.well-known/polis`, causing empty `author_name` in `ds_registered_sites`.
- **[Webapp] Posts not appearing in discovery**: Posts published via webapp weren't registered because `AuthorEmail` was empty with no fallback to `.well-known/polis`.

### Deprecated

- **polis-tutorial**: Marked deprecated in documentation (script remains functional).

## [0.49.0] - 2026-02-08

### Added

- **[Go CLI] Feed aggregation package**: New `pkg/feed/` package provides importable feed aggregation logic. `feed.Aggregate()` fetches public indexes from followed authors, filters by `last_checked`, merges, sorts by published date, and updates timestamps.
- **[Go CLI] Social functions extracted**: `pkg/following/` package now includes `FollowWithBlessing()` and `UnfollowWithDenial()` for reusable social operations with side effects.
- **[Go CLI] Feed cache with read tracking**: Persistent JSONL cache (`.polis/social/feed-cache.jsonl`) with `CacheManager` supports merge (dedup by deterministic sha256 ID), mark read/unread, mark all read, mark unread from timestamp, and automatic pruning by age/count. Staleness detection via manifest (`.polis/social/feed-manifest.json`).
- **[Webapp] Social sidebar mode**: Two-mode sidebar (My Site / Social) brings social reading into the webapp. Social mode includes Feed (aggregated posts from followed authors) and Following (author management).
- **[Webapp] Follow/Unfollow UI**: Follow panel to add authors by HTTPS URL with automatic blessing of pending/denied comments. Unfollow with confirmation modal and warning about denying blessed comments.
- **[Webapp] Feed view**: Chronological feed of posts from followed authors with type badges (Post/Comment), refresh button, unreachable-author warnings, and empty states.
- **[Webapp] Remote post viewer**: Slide-out panel renders remote posts with dark theme styling, fetched via new `/api/remote/post` endpoint.
- **[Webapp] Feed cache with instant load**: `GET /api/feed` loads instantly from local cache. `POST /api/feed/refresh` runs network aggregation and merges into cache. Auto-refresh fires in background when cache is stale (default 15 minutes).
- **[Webapp] Feed read/unread tracking**: Unread items show bold title with teal dot indicator. Sidebar badge shows unread count. Opening an item marks it read (fire-and-forget). "Mark All Read" button in header.
- **[Webapp] Feed type filtering**: Filter tabs (All / Posts / Comments) above feed list.
- **[Webapp] Feed hover actions**: "Mark Unread" and "Unread From Here" buttons appear on hover for read feed items. "Unread From Here" marks the hovered item and all more recent items above it as unread.
- **[Webapp] Live markdown preview**: Editor now renders a live preview as you type (300ms debounce), replacing the manual "Render Preview" button. Ctrl+Enter triggers publish.
- **[Webapp] Frontmatter toggle in editor**: Added "Hide FM" / "Show FM" toggle to the editor. When active, displays frontmatter in a non-editable mini-pane above the textarea, preventing accidental edits to signatures and hashes.

### Fixed

- **[Webapp] Feed item click broken for apostrophes**: Inline onclick handlers used single-quoted JS strings, so titles with apostrophes caused silent syntax errors. Feed items now pass numeric indexes instead of raw strings.
- **[Webapp] "Open original" link pointed to `.md`**: Remote post viewer's "Open original" link now points to the `.html` version for browser viewing.
- **[Webapp] Remote post viewer styling**: Replaced light parchment styles with dark theme (surface background, lavender left-border, salmon headings, teal links).

### Changed

- **[Go CLI] `discover` refactored**: Now calls `feed.Aggregate()` instead of inline logic. Same CLI output format maintained.
- **[Webapp] Shared web assets package**: Moved `www/` from duplicated locations (`cmd/server/www/` and `cmd/polis-full/www/`) to single `internal/webui/www/` package. Both entry points now import `internal/webui.Assets`, eliminating file drift.
- **[Webapp] Browser mode toggle hidden**: Browser mode toggle buttons removed from header (code retained).
- **Bash CLI**: Version 0.47.0 → 0.49.0 (includes v0.48.0 security fixes already in Go CLI)

### Tests

- **[Go CLI] Feed tests**: 9 tests covering empty feeds, single author, since override, not-following errors, unreachable authors, last_checked updates, 10-item limit, special character titles.
- **[Go CLI] Feed cache tests**: 13 tests covering item ID determinism, empty cache, merge, merge dedup (preserves read state), mark operations, list by type, prune by count/age, staleness detection, manifest defaults, version propagation.
- **[Go CLI] Following tests**: 6 tests covering follow adds to list, already-followed, unfollow removes, not-following errors, unreachable sites.
- **[Webapp] Feed handler tests**: 17 tests for feed cache endpoints covering empty cache, unread count, type filter, method validation, mark operations, invalid ID errors, refresh with special characters.

## [0.48.0] - 2026-02-07

### Security

- **[Go CLI] [H3] `rotate-key` now updates `.well-known/polis`**: The rotate-key command previously read the config but never wrote back the new public key, leaving sites broken after rotation. Now properly parses the JSON config, replaces the `public_key` field, and writes it back while preserving all other fields.
- **[Go CLI] [M6] Private temp directory for diffs**: The `computeUnifiedDiff()` function now creates a private temp directory (0700 permissions) instead of using the system `/tmp` directly. Diff computation no longer creates world-readable temporary files.
- **[Webapp] [H1] Error detail redaction**: HTTP error responses no longer include `err.Error()` output that could leak file paths and OS error strings. All endpoints now return generic error messages to clients while logging full details server-side.
- **[Webapp] [M1] Draft ID whitelist sanitization**: Draft IDs are now sanitized using a whitelist regex (replacing `[^a-zA-Z0-9_-]` with `-`) instead of the previous blacklist that only stripped `/` and `\`.
- **[Webapp] [M2] Path traversal canonicalization**: The `validatePostPath()` and `validateContentPath()` functions now apply `filepath.Clean()` before checking for `..`, preventing encoded traversal sequences from bypassing validation.

### Changed

- **Version consolidation**: All three Go binaries (CLI, webapp, bundled) now share a single version from `cli-go/version.txt`. The webapp no longer maintains a separate version number.

### Tests

- **[Webapp] Error response redaction tests**: Added `TestErrorResponsesRedacted` to verify error responses don't leak OS error strings or file paths
- **[Webapp] Draft ID sanitization tests**: Added `TestDraftIDSanitization` to verify special characters, path traversal, null bytes, and unicode are properly stripped from draft IDs
- **[Webapp] Path canonicalization tests**: Added `TestValidatePostPath_Canonicalization` and `TestValidateContentPath_Canonicalization` to verify `filepath.Clean` edge cases are handled correctly

## [0.47.0] - 2026-02-06

### Added

- **[Go CLI] Shell completions for `serve` and `validate`**: Both bash and zsh completions now include the `serve` command (with `--data-dir`/`-d` flags) and `validate` command (with `--json` flag)
- **[Go CLI] Hooks auto-discovery**: Hook scripts placed at conventional paths (`.polis/hooks/{event}.sh`) are now automatically discovered and executed without needing to register them in `webapp-config.json`
- **[Go CLI] Template engine enhancements**: Added `{{target_author}}` and `{{preview}}` variables now available in comment loop templates for better metadata display
- **[Go CLI] Test coverage expansion**: Added 7 new test files covering comment operations, hooks, index rebuilding, metadata management, publishing, and site initialization

### Fixed

- **[Go CLI] File move behavior in `polis post`**: The CLI now moves (removes) the original file after publishing, matching the bash CLI's behavior. Non-fatal on failure (warns only).
- **[Go CLI] Comment count synchronization**: Blessing a comment now properly updates `manifest.json` comment count to stay in sync with actual blessed comments
- **[Go CLI] Version string consistency**: All generated metadata files (`.well-known/polis`, `manifest.json`, `following.json`, `blessed-comments.json`) and frontmatter generator fields now use the actual CLI version from `version.txt` instead of hardcoded values
- **[Go CLI] Deduplication in `public.jsonl`**: Index updates now check for existing entries by path and update in place instead of always appending

### Changed

- **[Go CLI] `polis init` creates `webapp-config.json`**: The init command now creates `.polis/webapp-config.json` with webapp-specific defaults
- **[Go CLI] Drafts directory migration**: Automatic migration from deprecated `.polis/drafts` to `.polis/posts/drafts` location

### [Webapp] Version Correction: 1.0.0 → 0.1.0

The webapp version has been corrected from the incorrect `1.0.0` to `0.1.0`. This reflects its alpha status and aligns with semantic versioning.

- **CLI version propagation**: Server now accepts `CLIVersion` via `RunOptions` and propagates it through publish, comment, and metadata packages
- **Hooks without config**: Publish, republish, and beseech handlers now fire hooks without explicit configuration, using auto-discovery
- **Styled confirmation dialogs**: Replaced browser `confirm()` calls with custom styled modal dialogs
- **Startup migrations**: Automatic drafts directory migration on webapp startup
- **Config cleanup**: Removed deprecated `Subdomain` field from webapp configuration

## [0.46.0] - 2026-02-05

### Changed

- **Directory restructure**: `bin/` renamed to `cli-bash/` for consistency with private repo
  - Bash scripts now at `cli-bash/polis`, `cli-bash/polis-tui`, etc.
  - Tests moved to `cli-bash/tests/`
  - Update PATH: `export PATH="$PATH:$(pwd)/polis-cli/cli-bash"`

### Deprecated

- **polis-tui**: Terminal UI is deprecated in favor of webapp (`polis-full serve`)
  - Code remains in place for existing users
  - No new features will be added
  - Use `polis-full serve` for interactive site management

## [0.45.0] - 2026-02-05

### Milestone: Go CLI Full Feature Parity

The Go CLI reaches complete feature parity with the Bash CLI and is now the
recommended implementation.

### Added

- **Go CLI v0.45.0** - All 27 commands from Bash CLI now implemented
  - New packages: clone, following, index, migrate, notification, remote, verify, version
  - JSON output mode (`--json` flag) for all commands
  - Data directory override (`--data-dir` flag)

- **Three distribution targets**
  - `polis` - CLI-only (~9 MB) - recommended for most users
  - `polis-server` - Web UI only (~11 MB)
  - `polis-full` - Bundled CLI + serve command (~12 MB)

- **Webapp** - Browser-based site management UI
  - Local HTTP server for site preview and management
  - Embedded web assets for zero-dependency deployment

### Changed

- Go CLI is now the **primary/recommended** implementation
- Bash CLI (`cli-bash/polis`) is now the reference implementation (feature frozen)
- SHA256 checksums for bash scripts in `cli-bash/` directory

### Notes

- The `serve` command requires `polis-full` binary
- Bash CLI remains fully functional for existing users
- Windows support now available via Go CLI

## [0.44.0] - 2026-02-04

### Added

- **[Go CLI] Initial release (v0.1.0)** - Native Go implementation of the polis CLI
  - Zero external dependencies - single binary for Linux, macOS, and Windows
  - Cross-platform support: linux/darwin/windows on amd64/arm64
  - Core commands implemented: `init`, `post`, `comment`, `render`, `register`, `validate`, `blessing` (requests/grant/deny), `version`
  - Same data formats and file structures as bash CLI for full compatibility
  - Available via GitHub Releases or build from source with `make build`
  - Install script: `curl -fsSL https://raw.githubusercontent.com/vdibart/polis-cli/main/scripts/install.sh | bash`

### Changed

- **`polis init` improvements** - Enhanced `.gitignore` and added `.env.example` template
  - `.gitignore` now excludes more files: `.env`, `logs/`, polis executables, `themes/`
  - New `.env.example` template created during init with documented configuration options
  - Clearer setup guidance for new users

- **`polis render --no-markers` flag** - Disable snippet markers in rendered HTML
  - By default, `polis render` injects hidden markers around snippet content for browser-based editing
  - Use `--no-markers` for production builds or clean HTML output
  - Useful for strict HTML validators or when markers are unnecessary

## [0.43.0] - 2026-02-01

### Fixed

- **URL extension normalization** - Standardized URL handling across CLI and discovery service to use `.md` extensions consistently. Both `.html` and `.md` URLs are now accepted as input and automatically normalized. This affects `polis comment`, `polis republish`, and discovery service endpoints.

- **`polis render` JSON validation** - Now validates `.well-known/polis` JSON syntax before rendering and reports specific parse errors from jq. Previously, malformed JSON was silently ignored due to error suppression.

- **Auto-blessing reliability** - Fixed two issues preventing comments from being auto-blessed:
  - Email fetch no longer blocks auto-blessing when post author's `.well-known/polis` lacks an `email` field (uses domain as fallback)
  - www subdomain normalization now works correctly (following `https://www.example.com` matches comments from `https://example.com` and vice versa)

- **Nested comments rendering** - Fixed comments on comments (replies to replies) not appearing in rendered output:
  - Render freshness check now includes `blessed-comments.json` changes
  - Comment templates now include blessed comments section to display replies
  - All three built-in themes updated (sols, turbo, zane)

### Documentation

- Added blessed comment caching behavior note to TEMPLATING.md explaining that `--force` is required to fetch updated remote blessed comments

## [0.42.0] - 2026-01-28

### Changed
- **Site title storage location** - Moved from `manifest.json` to `.well-known/polis` for semantic consistency
  - Identity metadata now lives with the identity file
  - Remote author attribution fetches `.well-known/polis` instead of `manifest.json`
  - Existing sites should add `site_title` to their `.well-known/polis` file
  - Documentation updated across USAGE.md, TEMPLATING.md, and GLOSSARY.md

- **`.env` file lookup order** - Simplified to two locations
  - Current working directory (`.env`) for per-site configuration
  - Home directory (`~/.polis/.env`) for shared configuration across sites
  - Per-site config takes precedence over shared config

### Fixed
- **`polis render` error message** - Now shows correct `.env` lookup paths in error output

## [0.41.0] - 2026-01-26

### Removed
- **`polis snippet` command** - Snippets no longer require signing
  - Snippets are now simple content files - just place `.md` or `.html` files in the `snippets/` directory
  - No frontmatter or metadata required
  - Existing signed snippets continue to work (frontmatter is automatically stripped during render)
  - The `snippets.jsonl` index is no longer created on init and can be safely deleted from existing sites
  - Rationale: Snippets are local template fragments that never travel between sites, so cryptographic signing adds friction without providing security benefits

### Changed
- **`.env` file location** - Configuration now stored in CLI directory instead of per-site
  - The `.env` file is now shared across all polis sites (contains global settings like `DISCOVERY_SERVICE_KEY`)
  - `polis init` checks the CLI directory for `.env` and copies from `.env.example` if missing
  - If neither exists, the command provides clear instructions with an example `cp` command
  - CLI directory resolution follows symlinks correctly when determining the location

### Fixed
- **`polis render` silent failure** - Now validates `POLIS_BASE_URL` before rendering
  - Previously failed silently with broken URLs if `.env` was missing or `POLIS_BASE_URL` was unset
  - Now shows a clear error message with the exact path to the `.env` file that needs configuration

- **Bash completion breaks file path completion** - Fixed `polis post <tab>` not completing file paths
  - Completion now only activates for flags when typing `--`, otherwise falls back to default file completion
  - Added `-o default -o bashdefault` for proper fallback behavior

- **JSON parsing fragility** - Replaced fragile grep/sed JSON parsing with jq
  - Multiple functions now use `jq` for safer JSON extraction (`extract_author_from_wellknown`, `_internal_beseech_from_file`, `rebuild_blessed_comments`)
  - Note: Canonical payload building for cryptographic signing intentionally uses printf (not jq) to ensure consistent key ordering and whitespace for signature verification

### Documentation
- **USAGE.md** - Added "File Content Integrity" guidance
  - Explains how `current-version` hash and `signature` are computed for posts and comments
  - Documents what breaks when manually editing published files without running `polis republish`
  - Clarifies which files are safe to edit (rendered HTML, themes) vs. which are not (signed `.md` files)
  - Explains operational implications of changing `POLIS_BASE_URL` without proper migration
  - Moved from SECURITY-MODEL.md for better discoverability

## [0.40.0] - 2026-01-26

### Documentation
- **MANIFESTO.md rewritten** - Complete restructure with clearer strategic vision
  - New title: "The Social Network That Isn't"
  - Refined thesis emphasizing web-as-platform vs building new platforms
  - Added sections: Discovery Service as metadata layer, Blessing Model, Business Model, Competitive Position
  - Tighter narrative flow from problem statement through solution to long-term vision
  - Clearer articulation of how Polis differs from decentralized alternatives (Mastodon, Nostr, Bluesky)

- **EXPERIENCE-PRINCIPLES.md enhanced** - Added "Who This Is For" section
  - Maps specific personas to experience levels (Substack authors → L5-6, Builders → L1-2, etc.)
  - Explicit audience definition with pain points and entry levels
  - Clear "not for" criteria to set appropriate expectations

- **README.md refinements** - Minor wording improvements
  - Clarified that comments are "hosted on" (not "never leave") the commenter's server
  - Streamlined identity explanation by removing redundant "DNS for identity" reference

## [0.39.0] - 2026-01-24

### Changed
- **Bold text styling in themes** - Bold/strong text in post content now uses theme accent colors for improved readability
  - sols theme: dimmed peach (`--color-peach-dim`)
  - turbo theme: purple accent (`--color-accent`)
  - zane theme: salmon (`--color-salmon`)
  - Provides better visual hierarchy and emphasis within post content

### Fixed
- **Spurious snippet warnings during render** - Removed literal `{{> theme:name}}` syntax from HTML template comments
  - Template comments were being incorrectly parsed as partial includes
  - Eliminates confusing warnings about missing snippet files during `polis render`
- **Recent posts URLs broken on post pages** - Fixed path doubling issue on nested post pages
  - Made `{{#recent_posts}}` URLs root-relative to prevent incorrect path construction
  - URLs now correctly resolve from any depth in the site hierarchy

### Documentation
- **README.md streamlined** - Condensed from 405 to 120 lines with tighter narrative focus
  - Clearer value proposition: "A decentralized social network where you own everything"
  - Simplified "The idea" section explaining the self-moderating conversation model
  - Quick start guide now focuses on getting users running quickly
  - Removed verbose sections that slowed initial comprehension

- **USAGE.md compressed** - Reduced from 1680 to 1301 lines (23% reduction)
  - Added "copy to content repo" installation option (recommended for production use)
  - Moved TUI documentation to dedicated `TUI.md` file
  - Moved upgrade documentation to dedicated `UPGRADING.md` file
  - Replaced inline JSON examples with references to `JSON-MODE.md`
  - Trimmed verbose CLI output examples for better readability

- **New documentation files created**
  - `TUI.md` - Dedicated guide for the Terminal UI (`polis-tui`)
  - `UPGRADING.md` - Standalone upgrade instructions and migration guide
  - Improves discoverability and navigation of documentation

## [0.38.0] - 2026-01-23

### Changed
- **Comment author now links to comment URL** - Author names in blessed comments are now clickable links
  - Links point to the comment on the commenter's own site
  - Added hover underline styling for better affordance
  - Applied across all built-in themes (sols, turbo, zane)

### Fixed
- **URL normalization issues** - Eliminated double slashes in comment and post URLs
  - Defensive URL construction with `${POLIS_BASE_URL%/}` to strip trailing slashes
  - Prevents malformed URLs like `https://example.com//comments/post.md`
  - Applied to beseech command and comment URL construction
  - Removed render-time normalization workaround (now unnecessary)

## [0.37.0] - 2026-01-22

### Added
- **Recent posts navigation on post pages** - "More from this site" section displays below comments
  - Shows up to 3 recent posts (automatically excludes current post)
  - Reuses existing `post-item` snippet for consistent formatting (date, title, comment count)
  - Hidden automatically when no other posts exist (using CSS `:has()` selector)
  - New `{{#recent_posts}}` template block available in post templates

### Changed
- **Comment count display repositioned** - Moved from far-right float to inline placement after post title
  - Now renders as "(N comments)" directly adjacent to the title in post lists
  - Applied consistently across all built-in themes (sols, turbo, zane)
  - Improves readability and semantic grouping of post metadata

- **Comment CTA redesigned** - Replaced expandable box with streamlined inline link
  - Appears as "Learn how..." link next to "COMMENTS (N)" header on the same line
  - Expands full-width below the header when clicked
  - Restored detailed multi-step setup instructions for clarity
  - Reduced visual clutter on post pages

## [0.36.0] - 2026-01-22

### Added
- **Standalone upgrade tool (`polis-upgrade`)** - New script for managing CLI/TUI upgrades with version migration support
  - Fetches latest versions from GitHub with SHA-256 checksum verification
  - Self-update capability (checks and updates itself before proceeding)
  - Supports `--to VERSION` for partial upgrades and `--check` for dry runs
  - Auto-detects site directory and polis CLI location
  - Downloads and executes version-specific migration scripts when needed
  - Recovery instructions displayed on migration failure

- **Version migration system** - Database-style migration scripts for managing breaking changes
  - Migration scripts stored in `migrations/cli/` and `migrations/tui/` directories
  - Each migration includes `manifest.json` with version requirements and checksums
  - Scripts are idempotent, non-destructive, and checksum-verified before execution
  - Only created for versions that introduce file/data layout changes
  - Provides clear upgrade path for users on older versions

- **Multi-component version tracking** - Discovery service now tracks CLI, TUI, and upgrade script independently
  - New `component` column in `polis_versions` table: `'cli'`, `'tui'`, or `'upgrade'`
  - Per-component `is_latest` flag and separate version histories
  - `/polis-version` endpoint accepts optional `?component=` parameter (defaults to `cli`)
  - Enables independent release cycles for each component

- **TUI version update notification** - About screen now shows TUI update availability
  - Queries discovery service with `component=tui` parameter
  - Displays yellow indicator when newer TUI version is available
  - Consistent with CLI version notification pattern

### Changed
- **Version check now specifies component** - CLI version checks explicitly pass `component=cli` parameter (backwards compatible with discovery service)

## [0.35.0] - 2026-01-22

### Added
- **Comment CTA on post pages** - Collapsible "Want to comment?" section on all default themes
  - Explains how Polis comments work (cryptographically signed, published from your own site)
  - Expandable setup instructions with code sample
  - Links to GitHub repo for full documentation
  - Uses native `<details>` element (no JavaScript required)

- **Enhanced snippet include syntax** - Full control over snippet resolution with tier prefixes and explicit extensions
  - **Explicit tier prefixes**: `{{> global:about}}` and `{{> theme:about}}` for fine-grained control
  - **Explicit extensions**: `{{> about.md}}` or `{{> about.html}}` skips fallback resolution
  - **Combined syntax**: `{{> theme:about.html}}` for complete specificity
  - Allows `about.html` (theme wrapper) to include `about.md` (author content) without naming collision

### Changed
- **Snippet lookup order reversed** - Global snippets (`./snippets/`) now take precedence over theme snippets
  - Previously: theme → global (theme won)
  - Now: global → theme (author wins)
  - Use `{{> theme:name}}` prefix to force theme-first behavior when needed

- **Theme templates updated** - All default themes now use explicit `{{> theme:...}}` prefix for snippets
  - Ensures themes work out of the box without relying on lookup precedence
  - Users can override by creating global snippets and changing templates to use `{{> name}}`

### Fixed
- **Manifest JSON validation** - `polis render` now validates manifest.json before processing
  - Shows clear error message with parse details if JSON is malformed
  - Previously failed silently when manifest had syntax errors

- **Snippet rendering error handling** - Pandoc failures in snippets now warn instead of crashing
  - Displays warning with snippet path and error details
  - Continues rendering with empty content instead of silent exit

- **Snippet debug output** - Fixed debug messages appearing in rendered HTML
  - Redirected `load_snippet` and `render_partials` debug output to stderr
  - Previously, info/warning messages were captured as part of snippet content

- **Missing link styling in about section** - Added `.about-content a` CSS rules to all themes
  - Links in the about snippet now use theme colors (teal/cyan) instead of browser defaults
  - Affects sols, turbo, and zane themes

## [0.34.0] - 2026-01-21

### Added
- **Notifications system (Phase 1)** - Local notification tracking with version checking
  - New `polis notifications` command with subcommands: `list`, `read`, `dismiss`, `sync`, `config`
  - Track notifications locally in `.polis/notifications.jsonl` (log) and `.polis/notifications-manifest.json` (preferences/state)
  - Support for notification types: `version_available`, `version_pending`, `new_follower`, `new_post`, `blessing_changed`
  - Filter notifications by type, show all (including read), mark as read, dismiss old notifications
  - Sync from discovery service with optional full reset
  - Configure notification preferences: polling intervals, enable/disable, mute/unmute specific types

- **Version checking in `polis about`** - Automatic upgrade availability detection
  - Shows "→ X.Y.Z available" when a new CLI version is released
  - Warns when metadata files need rebuild after upgrade
  - Displays unread notification count with prompt to view notifications

- **Discovery service version tracking** - Infrastructure for CLI version notifications
  - New `polis_versions` table tracks CLI releases with version, release notes, download URL, and checksum
  - `GET /polis-version` endpoint returns latest version with `upgrade_available` flag
  - Rate limited: 100 requests/hour (global, no authentication required)

- **Discovery service rate limiting** - Per-domain rate limiting infrastructure
  - New `rate_limits` table tracks requests per domain/endpoint/time window
  - Atomic rate limit checking and increment via `increment_rate_limit()` RPC
  - Automatic cleanup of old rate limit entries (older than 24 hours)

- **Documentation**
  - New `docs/NOTIFICATIONS.md` - Comprehensive notification system documentation
  - Updated `docs/SECURITY-MODEL.md` - Added notifications security section covering signed requests, attack prevention, and privacy considerations
  - Updated `docs/USAGE.md` - Added notifications commands and `--announce` flag documentation

### Changed
- **Consolidated `polis manifest` into `polis rebuild`** - Manifest generation now automatic after rebuild operations
  - Removed standalone `polis manifest` command
  - Renamed `--content` flag to `--posts` for clarity
  - Added `--notifications` flag to reset notification files
  - `--all` flag now includes posts, comments, and notifications

- **Claude Code skill updated** - Comprehensive update to `/polis` skill with full command coverage
  - Rewrote Feature 6 (Notifications) with all subcommands documented
  - Added Feature 7 (Site Registration) for `register`/`unregister` commands
  - Added Feature 8 (Clone Remote Site) for `polis clone` command
  - Added Feature 9 (About / Site Info) for `polis about` command
  - Updated `commands.md` reference with all missing commands and flags
  - Updated `json-responses.md` with new response schemas

- **Shell completions enhanced** - Full notifications support and improved JSON mode handling
  - Added `notifications` subcommands: `list`, `read`, `dismiss`, `sync`, `config`
  - Added completion options for all notification subcommands (`--type`, `--all`, `--older-than`, `--reset`, etc.)
  - `--json` flag now offered at any position and for all commands
  - Added `--announce` completion for `follow`/`unfollow` commands
  - Zsh: Added argument type hints for file paths, URLs, and durations

### Fixed
- **`polis rebuild --notifications` path error** - Fixed undefined `$POLIS_DIR` variable
  - Was causing "Permission denied" error when attempting to write to `/notifications-manifest.json`
  - Now correctly writes to `.polis/notifications-manifest.json`

## [0.33.0] - 2026-01-20

### Added
- **Post metadata tracking** - Discovery service now tracks and indexes posts from registered authors
  - Posts can be registered, updated, queried, and removed from the discovery index
  - Only posts from registered authors are accepted (same privacy model as comments)
  - Enables discovery of content across the polis network
  - Schema version bumped to 0.5.0 for discovery service compatibility

### Changed
- **Theme layout improvements** - Refined post page header layout across all built-in themes (turbo, zane, sols)
  - Date and signature information now displayed on the same line in post header
  - Date left-aligned, signature info ("Signed by ... | Version ...") right-aligned
  - Increased content width from 720px to 900px for better readability
  - Centered content container for improved visual balance
  - Signature information moved from footer to header for better visibility
  - Index page hero/title alignment now matches content edge
  - Simplified post header spacing and removed dividing rule

- **Theme visual enhancements** - Accessibility and visual polish for all default themes
  - Improved muted text contrast for better readability (WCAG compliance)
  - Added responsive breakpoints: tablet (768px) and small phone (480px)
  - Added keyboard focus states (`:focus-visible`) for accessibility
  - Turbo: Glow effects on navigation and post hover, increased border visibility, magenta accent for author names
  - Zane: Subtle shadows on content cards, gold accent for dates
  - Sols: Increased pink border visibility, added shadows for depth, sage accent for author names

### Fixed
- **Registration payload signatures** - Use compact JSON formatting to match server-side signature verification
  - Previously used pretty-printed JSON with newlines, causing signature mismatch errors
  - Affects `polis register` and `polis unregister` commands
  - Ensures cryptographic signatures verify correctly on discovery service

- **Attestation verification** - Fixed signature verification when checking registration status
  - Now uses proper allowed_signers file format for `ssh-keygen -Y verify`
  - Format requires `<principal> <key-type> <key>` instead of raw public key
  - Fixes "Attestation Verification: FAILED" error when running `polis register` on already-registered sites

## [0.32.0] - 2026-01-18

### Added
- **Shell completion support** - Tab completion for bash and zsh shells to improve command-line productivity
  - Complete all 24 polis commands (type `polis i<tab>` to expand to `init`)
  - Complete subcommands for `blessing` and `migrations` namespaces
  - Complete the global `--json` flag
  - Includes installation scripts: `completions/polis.bash` and `completions/polis.zsh`
  - Detailed setup instructions added to USAGE.md for both bash and zsh

## [0.31.0] - 2026-01-18

### Added
- **Auto-registration on init** - New `polis init --register` flag automatically registers your site with the discovery service after initialization
  - Requires `POLIS_BASE_URL` and discovery service credentials to be configured
  - Shows helpful warnings if prerequisites are missing (initialization still succeeds)
  - Streamlines the setup process for new sites joining the network

- **Improved help text** - `polis init --site-title` flag now documented in built-in help output

### Changed
- **Consolidated configuration display** - `polis config` command merged into `polis about` for unified system information
  - `polis about` now shows: site info (URL, title), version details (CLI, .well-known/polis, following.json, blessed-comments.json, manifest.json), configuration paths, key status and fingerprint, discovery service configuration, and project details
  - Supports both human-readable and `--json` output modes
  - Provides single command for complete system status overview

- **TUI about screen redesign** - TUI version bumped to 0.6.0 with enhanced about display
  - Now shows SITE, VERSIONS, KEYS, DISCOVERY, and PROJECT sections matching CLI output
  - Fixed "not configured" display bug that occurred after `polis config` command removal
  - Consistent user experience between CLI and TUI interfaces

### Removed
- **`polis config` command** - Deprecated in favor of `polis about` which provides all the same information with consistent `--json` support

## [0.30.0] - 2026-01-18

### Added
- **Glossary documentation** - New `docs/GLOSSARY.md` with definitions for 20 polis-specific terms including post, comment, blessing, snippet, theme, signature, beseech, discovery service, render, manifest, frontmatter, and more. Quick reference for understanding polis terminology.

### Fixed
- **Theme CSS specificity** - Fixed `.post-meta` styling conflicts across all three built-in themes
  - Scoped `.post-meta` styles to `.post-content` context to prevent nav bar conflicts
  - Added separate `.site-footer .post-meta` styling for footer placement
  - Affects turbo, zane, and sols themes

- **Turbo theme post layout** - Moved post date from navigation bar to content area
  - Date now appears in bottom right of post content (`.post-meta` section)
  - Simplified navigation bar to only show "← Home" link
  - Cleaned up unused flexbox styles from navigation

### Changed
- **Documentation reorganization** - Removed duplicate theme customization content from USAGE.md
  - Theme customization, template variables, and mustache syntax now consolidated in TEMPLATING.md
  - USAGE.md now links to TEMPLATING.md for detailed theming information
  - Reduces confusion from maintaining same content in multiple locations

- **Template documentation syntax** - Updated HTML comment examples in TEMPLATING.md
  - Removed mustache `{{> }}` syntax from comments (was being expanded by template engine)
  - Comments now use plain snippet names (e.g., `about` instead of `{{> about}}`)

- **Polis branding guidance** - Softened branding language in TEMPLATING.md
  - Changed from requirement to friendly suggestion for keeping cyan logo color
  - "Keep the cyan color..." → "We'd appreciate it if you keep the cyan color..."

## [0.29.0] - 2026-01-18

### Added
- **Site registration** - List your site in the public directory to make your content discoverable
  - `polis register` - Register your site in the discovery service directory (idempotent)
  - `polis unregister [--force]` - Remove your site from the directory (requires confirmation)
  - Registration status now displayed in `polis about` output
  - TUI: Register/Unregister options added to Admin menu for easy access

- **Attestation verification** - Client-side verification of discovery service signatures
  - When already registered, `polis register` verifies the server's attestation signature
  - Uses discovery service's public key to validate that registration was properly signed
  - Displays verification status: Valid, Failed, or Not checked
  - Ensures server-side attestations are cryptographically verifiable by clients

### Changed
- **BREAKING: `POLIS_ENDPOINT_BASE` renamed to `DISCOVERY_SERVICE_URL`**
  - Update your environment variables and `.env` files to use the new name
  - No backward compatibility - the old variable name is no longer recognized
  - Improves naming clarity and consistency across the codebase

- **BREAKING: Network participation now requires site registration**
  - Both comment author AND target site must be listed in the public directory
  - Unlisted sites cannot participate in cross-site conversations
  - Error codes: `AUTHOR_NOT_REGISTERED`, `TARGET_NOT_REGISTERED`
  - Register your site with `polis register` to participate in the network

- **Registration messaging** - Updated terminology to emphasize discoverability and community
  - "Enable blessing flow" → "Make your site publicly discoverable"
  - "Beseech blessings" → "Discover content and engage with posts"
  - Focus on joining the public directory for networking, not just technical feature enablement

### Discovery Service
- New Edge Functions for site registration:
  - `sites-register` - Register a site with Ed25519 signature verification
  - `sites-unregister` - Unregister a site (hard delete for privacy)
  - `sites-check` - Check if a domain is registered
  - `sites-public-key` - Get discovery service's public key for attestation verification
- New `registered_sites` database table with dual-signature scheme
- Added `isRegistered()` helper for registration checks in blessing flow

## [0.28.0] - 2026-01-18

### Changed
- **BREAKING: `polis publish` renamed to `polis post`**
  - Command syntax: `polis post <file>` (was `polis publish <file>`)
  - JSON mode: `polis --json post` or `polis post --json`
  - JSON responses now return `"command": "post"` instead of `"publish"`
  - Scripts and workflows using `polis publish` must be updated to use `polis post`
  - Test files renamed to reflect new command name

- **Post template layout improvements**
  - Post date moved to bottom right corner of content area
  - Navigation bar simplified to display only "← Home" link
  - Cleaner visual hierarchy and improved readability on post pages
  - Applied to sols and zane themes

### Fixed
- **`--json` flag positioning** - Now works as first OR last argument
  - `polis --json post file.md` (flag at start)
  - `polis post file.md --json` (flag at end)
  - Improves ergonomics for scripting and interactive use

## [0.27.0] - 2026-01-16

### Added
- **Theme system** - Self-contained styling packages with templates, CSS, and snippets
  - Ships with 3 built-in themes: turbo (retro computing), zane (neutral dark), sols (violet/peach)
  - Themes stored at `themes/` in the distribution, copied to `.polis/themes/` on init
  - Each theme contains: index.html, post.html, comment.html, comment-inline.html, theme CSS, snippets/
  - THEMES_DIR configurable via environment, .env, or .well-known/polis config

- **First-render theme selection** - Automatic theme setup on first render
  - Randomly selects from available themes if `active_theme` not set in manifest
  - Copies theme CSS to `styles.css` at site root
  - Theme selection stored in `manifest.json` for persistence

- **Snippets** - New signed content type for reusable template fragments
  - `polis snippet <file>` - Sign and publish snippets to `snippets/` directory
  - `polis snippet -` - Create snippets from stdin (supports `--filename`, `--title` flags)
  - Snippets tracked in `metadata/snippets.jsonl` (parallel to `public.jsonl`)
  - Both `.md` (pandoc processed) and `.html` (as-is) formats supported
  - Two-level lookup: theme snippets first, then global snippets directory

- **Mustache templating syntax** - Standard partial and loop syntax for composable templates
  - `{{> path/to/snippet}}` - Include snippets in templates (recursive up to 10 levels)
  - `{{#posts}}...{{/posts}}` - Loop over posts with item variables
  - `{{#comments}}...{{/comments}}` - Loop over outgoing comments
  - `{{#blessed_comments}}...{{/blessed_comments}}` - Loop over blessed comments on posts

- **Post navigation bar** - Home link and date display on post pages
  - `{{home_path}}` variable provides relative links back to homepage
  - Navigation bar styled with theme accent colors
  - Displays "← Home" link and post publication date

### Changed
- **`polis init`** - Now installs theme files alongside initialization
  - Looks for `themes/` directory alongside polis script (follows symlinks)
  - Copies themes to `.polis/themes/` (or custom location via `--themes-dir` flag)
  - Warns if themes directory not found but continues initialization
  - Adds `directories.themes` to `.well-known/polis` config

- **`polis render`** - Enhanced with theme system support
  - On first render, auto-selects theme and saves to manifest
  - Loads templates from active theme directory
  - Copies theme CSS to styles.css on each render

- **`polis republish`** - Now auto-detects content type by path
  - `posts/**` paths republish as posts
  - `comments/**` paths republish as comments
  - `snippets/**` paths republish as snippets
  - Falls back to frontmatter `type` field if path doesn't match

### Fixed
- **Theme persistence** - `active_theme` now preserved when manifest regenerates after render
- **Multiline template sections** - `{{#section}}...{{/section}}` blocks now handle multiline content correctly
- **Nested content CSS paths** - Posts/comments use relative `{{css_path}}` (e.g., `../../styles.css`) for correct styling
- **Post template duplicate h1** - Removed duplicate title from hero section; markdown `#` heading is now the only h1
- **Navigation bar layout** - Navigation items now grouped together instead of spread across full width

### Removed
- **`polis render --init-templates`** - Replaced by theme system
  - Templates now come from themes, not a separate init command
  - Custom templates should be created as a custom theme

- **`.polis/templates/` directory** - Replaced by `.polis/themes/`
  - Migration guide available in TEMPLATING.md for existing customizations

### Documentation
- **TEMPLATING.md** - Complete rewrite as comprehensive theme system guide
  - Theme structure and file organization
  - Snippet lookup order and override mechanism
  - Theme developer's guide with CSS conventions
  - Migration guide from old template system
- **USAGE.md** - Updated directory structure and `polis init` options
- **JSON-MODE.md** - Removed `--init-templates` response format documentation

## [0.26.0] - 2026-01-15

### Changed
- **Command rename** - `polis get-version` renamed to `polis extract` for clarity
  - More intuitive name for extracting specific versions from version history
  - Updated help text and usage messages to reflect new command name
- **Template title handling** - Removed `<h1>{{title}}</h1>` from default templates
  - Markdown's first `# Heading` now becomes the `<h1>` in rendered HTML
  - Frontmatter `title` field only used for `<title>` tag (browser tab/SEO)
  - Allows different display heading vs document title if desired
  - Reverses the "duplicate title fix" from v0.24.0

### Fixed
- **Version reconstruction reliability** - Improved `polis extract` command robustness
  - Now tries efficient backward reconstruction first, falls back to forward reconstruction
  - Handles edge cases where canonical file has been modified during republish
  - Better error messages when version chain is broken
- **Hash verification compatibility** - Fixed verification for content published with different hashing methods
  - Tries canonical hashing first (matches current publish behavior)
  - Falls back to non-canonical hashing for backwards compatibility
  - Ensures old content can still be verified correctly
- **Republish detection** - `polis republish` now detects when content is unchanged
  - Compares canonical hashes to determine if update is needed
  - Skips version history update when content hash is identical
  - Prevents unnecessary version entries for no-op edits
- **Blank line handling** - Fixed double blank lines between frontmatter and content
  - `extract_content_without_frontmatter` already includes separator blank line
  - Removed redundant blank line insertions in publish and republish
  - Ensures consistent spacing in generated files

## [0.25.0] - 2026-01-14

### Added
- **Site title support** - Optional branding with `--site-title` flag during initialization
  - Set during init: `polis init --site-title "My Blog"`
  - Stored in `manifest.json` and preserved across rebuilds
  - Used in rendered HTML templates via `{{site_title}}` variable
  - Displayed in `polis about`, `polis config`, and TUI About screen
  - Falls back to domain name if not configured
- **Comment author names from manifest** - Blessed comments now display author's site title
  - Fetches remote `manifest.json` to get commenter's site title
  - Falls back to domain name if title not available
  - Local comments use local manifest for author name

### Changed
- **`polis config`** - Added new "Site" section displaying URL and title
  - JSON output includes `data.site.url` and `data.site.title` fields
- **Documentation** - Updated USAGE.md and TEMPLATING.md with site title configuration details

### Fixed
- **TUI About screen** - Corrected JSON path to properly display CLI version
  - Changed from `data.cli.version` to `data.versions.cli`

## [0.24.0] - 2026-01-13

### Fixed
- **Blessed comments not re-rendering** - `polis render` without `--force` now detects when blessed comments have been updated
  - Checks if `blessed-comments.json` is newer than HTML files for posts with blessed comments
  - Re-renders posts when new comments are blessed, even if the post markdown hasn't changed
  - Ensures blessed comments appear promptly after granting blessings
- **Duplicate titles in rendered HTML** - Fixed title appearing twice in rendered output
  - Template displays `{{title}}` from frontmatter, but post body also included the `# Title` heading
  - Now strips leading blank lines and first heading from markdown body before pandoc conversion
  - Applies to both post content and inline blessed comments for consistent formatting
- **TUI stats display** - Fixed post and comment counts in TUI dashboard stats bar
  - Now properly counts posts and comments from public.jsonl instead of just posts
  - Corrected stats text to show "Blessing Requests" instead of "Blessings"
- **TUI menu display** - Fixed text parameter not being displayed before menu options in TUI

## [0.23.0] - 2026-01-13

### Added
- **`polis discover`** - Check followed authors for new content
  - Efficiently polls each author's `manifest.json` to detect updates
  - Fetches full content indexes only when new posts or comments are detected
  - Updates `last_checked` timestamp in `following.json` to track discovery state
  - Displays summary of new content since last check
- **`polis manifest`** - Generate manifest.json summary file
  - Contains `last_published`, `post_count`, `comment_count`, and CLI version
  - Auto-generated by `publish`, `comment`, and `render` commands
  - Enables efficient discovery without downloading full content indexes
- **TUI Admin menu** - New maintenance operations submenu
  - Regenerate manifest.json
  - Rebuild site (normal or force re-render)
- **TUI Discover submenu** - Enhanced content discovery interface
  - "Check for new content" - runs `polis discover` CLI command
  - "Browse followed authors" - view and manage following list

### Changed
- **`polis config`** - Now displays manifest version and groups version information
- **`polis render --force`** - Now regenerates `manifest.json` during force rebuild
- **`polis init`** - Creates initial `manifest.json` during site setup

### Fixed
- **TUI Enter key not working** - Fixed menu selection with Enter key
  - Root cause: Exit code was overwritten before second condition could check it
- **TUI submenu navigation** - Fixed q/ESC in Admin and Discover menus
  - Now properly returns to main menu instead of exiting application
- **Comment JSON output** - Fixed to output single JSON object with nested beseech data
- **TUI startup performance** - Removed duplicate stats fetch that caused ~4 second delay on startup

## [0.22.0] - 2026-01-12

### Added
- **`polis about`** - Branded info screen with tagline, versions, and project links
- **`polis config`** - Configuration viewer showing all settings, paths, and key fingerprint (supports `--json`)
- **TUI comment filename prompt** - Now prompts for filename when creating comments, matching publish workflow
- **TUI init flexibility** - Offers to initialize polis in directories with non-conflicting files
  - Previously required empty or git-only directories
  - Now only checks for actual conflicts (existing `.polis/keys` or `.well-known/polis`)
- **TUI "View in browser"** - Added option to open previewed URLs and blessing review URLs in browser

### Changed
- **BREAKING: Content canonicalization** - Signatures and hashes now use canonicalized content
  - Trims trailing whitespace from each line and ensures exactly one trailing newline
  - Eliminates verification failures caused by HTTP transfer or editor differences
  - **Existing signed content must be re-signed** with `polis rotate-key`
- **`--filename` flag** - Now works in all modes for `publish` and `comment` commands (previously stdin-only)
- **Color scheme** - Replaced BLUE with CYAN throughout the CLI for better terminal contrast
- **License correction** - Fixed to AGPL v3 in `polis about` output (was incorrectly showing MIT)

### Fixed
- **Signature verification** - Fixed false "invalid signature" errors caused by mawk incompatibility
  - Rewrote `extract_body_from_content` to use pure bash instead of awk
- **Hash verification** - Fixed false "hash mismatch" errors from trailing newline differences
- **`--json` flag positioning** - Now works as first or last argument (e.g., `polis --json config`)
- **`polis index --json`** - No longer conflicts with global `--json` flag
- **Template overwrite protection** - `polis render --init-templates` no longer overwrites existing templates
- **Index sorting** - `index.html` now lists posts in descending date order (newest first)
- **Preview URL flexibility** - `polis preview` now tries alternate extensions (.html ↔ .md) when content not found

## [0.21.0] - 2026-01-12

### Added
- **polis-tui** - New interactive terminal user interface for Polis
  - Menu-driven dashboard with keyboard navigation
  - Real-time stats display (posts, following, blessings, git status)
  - Integrated workflows: publish, comment, blessing management, discover, preview
  - Post-action options: rebuild, git commit/push, or continue
  - Built-in git integration with detailed commit messages
  - About screen with version info and configuration
- **Documentation** - Added "Implementation Security Audit" section to SECURITY-MODEL.md
  - Comprehensive verification that private keys are never printed or logged
  - Audit of file permissions, git exclusion, temp file handling
  - Complete security checklist for key management practices

### Fixed
- **Blessed comments not rendering** - Fixed `polis render` failing to find blessed comments when URLs had mismatched extensions
  - Normalized `update_blessed_comments_json` to always store post URLs with `.md` extension
  - Now checks both `.md` and `.html` extensions when looking up posts

### Changed
- **README restructure** - Reorganized for better new user experience
  - New "Try it now" section featuring polis-tui as recommended path
  - Updated tagline: "Your content, free from platform control"
  - Command-line mode section for users who prefer CLI workflows

## [0.20.0] - 2026-01-10

### Fixed
- **Embedded source cleanup** - Removed YAML `---` delimiters from embedded frontmatter in HTML comments
  - Eliminates `--` escaping that produced `&#45;&#45;` artifacts
  - Cleaner output with just the frontmatter fields

## [0.19.0] - 2026-01-09

### Changed
- **Embedded source optimization** - `polis render` now embeds only frontmatter in HTML comments instead of full content
  - Avoids duplication since body content is already rendered in HTML
  - Source reference uses canonical URL instead of local file path
- **README documentation** - Added "Render to a deployable website" section
  - Documents `polis render` workflow and output
  - Explains template customization with `--init-templates`
  - Highlights verifiable HTML with embedded signed source

## [0.18.0] - 2026-01-09

### Added
- **Embedded source in rendered HTML** - Each rendered HTML file now includes the original markdown source and frontmatter in an HTML comment at the end
  - Enables verification that HTML matches the signed source
  - Allows extraction of original markdown from rendered files
  - Useful for debugging template rendering issues

### Fixed
- **Template variable substitution bug** - Fixed variables containing special characters (like `&middot;` in `{{blessed_comments}}`) not being substituted correctly
  - Root cause: Bash pattern replacement treats `&` as a special character
  - Added `escape_for_replacement()` helper to properly escape `&` and `\` in template values
- **Render command exiting early** - Fixed `polis render` terminating before completion when processing posts with blessed comments
  - Root cause: Functions returning non-zero exit codes triggered `set -e` behavior
  - Changed post-increment `((var++))` to pre-increment `((++var))` throughout render code
- **Index generation failure** - Fixed `index.html` not being created due to early exit issues described above

### Changed
- Enhanced `polis render` diagnostics
  - Added working directory output for troubleshooting
  - Added error check for empty index templates
  - Improved progress messages
- Updated USAGE.md with comprehensive `polis render` documentation
  - Added pandoc to prerequisites section
  - Added template variables reference table
  - Added "Embedded Source" documentation
  - Updated directory structure examples

## [0.17.0] - 2026-01-08

### Added
- **Templating system** (`polis render`) - Render markdown posts and comments to HTML
  - Generates `.html` files alongside `.md` files for blog-like interfaces
  - Renders blessed comments inline on post pages
  - Generates `index.html` listing all posts and comments
  - Supports custom templates in `.polis/templates/`
  - Incremental rendering (skips unchanged files based on timestamps)
  - `--force` flag to re-render all files regardless of timestamps
  - `--init-templates` flag to export default templates for customization
  - Template variables: `{{title}}`, `{{content}}`, `{{published}}`, `{{blessed_comments}}`, etc.
  - Requires pandoc for markdown to HTML conversion

### Changed
- Updated dependencies documentation to include pandoc (optional, for render command)

## [0.16.0] - 2026-01-08

### Changed
- **Rebuild command refactor**: `polis rebuild` now requires explicit target flags for better control
  - `--content` - Rebuild public.jsonl from posts and comments
  - `--comments` - Full rebuild of blessed-comments.json from discovery service
  - `--all` - Rebuild all indexes
  - Flags are combinable (e.g., `--content --comments`)
  - Replaced `--diff` and `--full` flags (removed incremental sync feature)
  - JSON output now includes counts: `posts_indexed`, `comments_indexed`, `blessed_comments`
  - Updated help text and skills documentation

### Removed
- **Incremental blessed comments sync**: Removed `--diff` flag from `polis rebuild`
  - Incremental sync proved unreliable for maintaining consistency
  - Full rebuild (`--comments`) is now the only supported method

## [0.15.0] - 2026-01-08

### Added
- **Key rotation command** (`polis rotate-key`) - Generate new keypair and re-sign all content
  - Addresses critical security gap: previously no way to recover from key compromise without domain migration
  - Generates new Ed25519 keypair and re-signs all posts and comments
  - Updates `.well-known/polis` with new public key
  - Archives old key to `.polis/keys/id_ed25519.old` (or deletes with `--delete-old-key` flag)
  - Provides recovery mechanism if rotation is interrupted

### Security
- **Key rotation support** - Users can now rotate compromised keys without migrating domains
  - Immediate mitigation for suspected key exposure
  - Enables routine security hygiene practices
  - See SECURITY-MODEL.md for rotation procedures and best practices

## [0.14.0] - 2026-01-08

### Added
- **Automatic .env creation**: `polis init` now automatically copies `.env.example` to `.env` if it doesn't exist
  - Displays helpful warning reminding users to update configuration values
  - JSON mode includes `env_created` boolean flag in response data

### Changed
- **Environment variable naming**: Updated `.env.example` to use `DISCOVERY_SERVICE_KEY` instead of `SUPABASE_ANON_KEY` for consistency
- **Test framework refactor**: Tests now run in isolated temporary directories instead of tracked test-data/ directory for better isolation
- **Test fixtures**: Enhanced to support `POSTS_DIR` and `COMMENTS_DIR` environment variables

### Fixed
- **Blessing workflow tests**: Updated to use content hash-based blessing operations instead of database IDs

## [0.13.0] - 2026-01-08

### Breaking Changes
- **Blessing status**: Renamed `rejected` status to `denied` for consistency with CLI terminology
  - Database migration required: `ALTER TYPE public.blessing_status_enum RENAME VALUE 'rejected' TO 'denied';`
- **Blessing operations**: Now use content hash instead of database ID
  - `polis blessing grant <hash>` (was `<id>`)
  - `polis blessing deny <hash>` (was `<id>`)
  - Hash can be short form (e.g., `f4bac5-350fd2`) or full `sha256:...` prefix
  - CLI displays short hash in blessing requests table
  - Signed payload field changed from `comment_id` to `comment_version`

### Fixed
- **Nested comments**: Added missing `warn` and `warn_human` helper functions that caused silent failures when commenting on comments
- **Follow message**: Corrected misleading message "manually blessed" → "automatically blessed" when following an author with no pending comments
- **Comment URL argument**: `polis comment <file> <url>` now correctly uses the URL argument instead of always prompting interactively
- **Blessing preview**: Fixed "Redirecting" appearing in preview by following HTTP redirects when fetching comment content

## [0.12.0] - 2026-01-08

### Fixed
- **Sync bug**: `blessing_status` query parameter was ignored by discovery service, causing pending comments to be incorrectly added to `blessed-comments.json` during sync
- **URL validation**: `polis blessing grant` now validates comment URL exists before blessing (blocks on 404)
- `polis blessing deny` now warns if comment URL returns 404

### Changed
- `polis blessing grant` and `polis blessing deny` now fetch request details in all modes (not just human mode)

## [0.11.0] - 2026-01-07

### Added
- `polis rebuild --diff` - Sync missing blessed comments from discovery service (incremental)
- `polis rebuild --full` - Rebuild blessed-comments.json entirely from discovery service

### Changed
- Removed automatic `git add` staging from all commands (users manage git manually)

## [0.10.0] - 2026-01-07

### Removed
- `polis reset` command - Removed to simplify the CLI. See USAGE.md "Starting Fresh" section for manual reset instructions if needed.

## [0.9.0] - 2026-01-07

### Added
- **Domain migration tracking** - Migrations are now recorded in the discovery service for discoverability
  - Commenters can discover when authors they interact with have migrated
  - Enables updating local references when followed authors change domains
- **Notifications command** (`polis notifications`) - View pending actions requiring attention
  - Pending blessing requests for your posts
  - Domain migrations for authors you follow or interact with
- **Migrations apply command** (`polis migrations apply`) - Update local references to migrated domains
  - Interactive confirmation before modifying files
  - **Key continuity verification** - Ensures new domain is controlled by same owner
  - Updates following.json, blessed-comments.json, and comment frontmatter
  - Warns and skips if public key mismatch detected (hijacking protection)

### Changed
- **BREAKING**: Replaced `/comments-migrate` endpoint with RESTful `/migrations` endpoint
  - POST /migrations: Record a migration
  - GET /migrations: Query migration history for specified domains
- Updated Claude Code skill with notifications workflow

## [0.8.0] - 2026-01-07

### Security
- **BREAKING**: Grant/deny blessing endpoints now require Ed25519 signature verification
  - Previous implementation used self-reported email for authorization (spoofable)
  - Now uses same cryptographic verification pattern as beseech endpoint
  - CLI signs `{action, comment_id, timestamp}` payload with author's private key
  - Server verifies signature using public key from post author's `.well-known/polis`

### Changed
- `polis blessing grant` now signs requests with Ed25519
- `polis blessing deny` now signs requests with Ed25519

## [0.7.0] - 2026-01-07

### Added
- **Domain migration command** (`polis migrate <new-domain>`) - Migrate all content to a new domain
  - Auto-detects current domain from published files
  - Updates all local files (posts, comments, metadata)
  - Re-signs all content with new URLs (required for comments where URL is in signed payload)
  - Updates discovery service database (preserves blessing status)
  - New edge function `comments-migrate` for authenticated database updates
  - New SQL function `migrate_domain()` for bulk URL updates

### Changed
- **BREAKING**: Renamed `SUPABASE_ANON_KEY` environment variable to `DISCOVERY_SERVICE_KEY`

## [0.6.0] - 2026-01-07

### Fixed
- Documentation: Corrected `polis comment` argument order (`<file> <url>`, not `<url> [file]`)
- Documentation: Fixed directory format references (`YYYYMMDD`, not `YYYY/MM`)

## [0.5.0] - 2026-01-06

### Added
- **Claude Code skill** - AI-powered workflows for publishing, discovering, commenting, and managing blessings
- `cli/skills/polis/` - Skill definition with command reference and JSON response schemas
- `CLAUDE.md` - Project context file for Claude Code integration
- Skill installation instructions in CLI README

## [0.4.0] - 2026-01-06

### Fixed
- **macOS compatibility** - `extract_domain_from_url()` now uses portable bash parameter expansion instead of GNU sed-specific `\?` regex that fails on BSD sed
- **Signature verification for nested comments** - Discovery service now includes `root_post` in signed payload verification to match CLI behavior

## [0.3.0] - 2026-01-06

### Added
- **Nested comment threads** - Comments can now reply to other comments, not just posts
- **Thread-specific auto-blessing** - Authors who have been blessed once on a post are auto-blessed for future comments on that same post
- `root-post` frontmatter field - Tracks the original post for nested comments
- `is_comment_url()` helper for detecting comment vs post URLs
- `fetch_root_post_for_comment()` helper for querying root post from discovery service
- `isAuthorBlessedOnThread()` server-side function for thread trust queries
- Database migration for `root_post` column (`migrations/002_nested_comments.sql`)

### Changed
- **BREAKING**: `in_reply_to` now means "immediate parent" (can be post OR comment URL)
- **BREAKING**: New required field `root_post` in beseech payload (always the original post URL)
- **BREAKING**: Old CLI versions will fail validation (missing `root_post` field)
- Blessing requests query now uses `root_post` domain (not `in_reply_to`) to properly support nested threads
- Updated documentation for nested threads and auto-blessing

### Migration
See `discovery-service/migrations/002_nested_comments.sql` for database migration.
Existing comments are automatically backfilled with `root_post = in_reply_to`.

## [0.2.0] - 2026-01-05

### Added
- `polis init` - Initialize a new Polis site with Ed25519 keypair
- `polis publish` - Publish markdown posts with cryptographic signatures
- `polis comment` - Create signed comments on others' posts
- `polis republish` - Update existing posts with version history
- `polis preview` - Preview and verify remote content before blessing
- `polis blessing requests` - View pending blessing requests
- `polis blessing grant` - Approve a comment for amplification
- `polis blessing deny` - Reject a blessing request
- `polis follow` - Follow an author and auto-bless their comments
- `polis unfollow` - Stop following an author
- `polis index` - Generate public.jsonl index of all content
- `polis rebuild` - Rebuild metadata files from source
- `polis version` - Display CLI version
- Interactive tutorial (`polis-tutorial`)
- JSON mode (`--json`) for all commands
- SHA256 checksum verification for script integrity
- Comprehensive documentation (USAGE.md)

### Security
- Ed25519 signatures for all published content
- SHA256 content hashes for integrity verification
- SSH-based signature verification for remote content

## [0.1.0] - 2026-01-01

### Added
- Initial proof of concept
- Basic publish and comment functionality
