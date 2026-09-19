# Content System

*For* [Developers](../../README.md#building-on-polis) — *Kind* [Reference](../../README.md#kinds-of-page) — *Code* [`cli-go/pkg/bundle`](../../../cli-go/pkg/bundle) — *See also* [concept](bundles.md) · [concept](content-types.md)

> **This is the deep reference.** For the narrative introductions to each concept, start with:
> [bundles.md](bundles.md) → [content-types.md](content-types.md) → [shapes.md](shapes.md) → [themes.md](themes.md), and place them in the larger picture via [architecture.md](architecture.md). This file documents the full schemas, validation rules, filesystem layout, and event catalog that the narrative docs reference.

The polis content system organizes all site data around **bundles** -- namespaced packages that declare content types, events, storage layout, and handler dispatch. The initial bundle `pub.polis.core` declares nine content types: post, comment, follow, dm, tag, attestation, actor, license, and theme. (A further data shape, `pub.polis.feed`, is a *derived* local cache — not a declared bundle type; see the note in the Content Types section.) Third-party bundles extend the system with custom types without modifying the core protocol.

---

## Table of Contents

1. [Filesystem Layout](#filesystem-layout)
2. [Bundles](#bundles)
3. [Content Types](#content-types)
4. [Events](#events)
5. [Previous Event Names](#previous-event-names)

---

## Filesystem Layout

### Full Directory Tree

```
.well-known/
  polis                              # identity + bundle pointers + site metadata
  did.json                           # did:web document projected from the key history

site/
  snippets/                          # rendering primitives (about.md, etc.)

content/                             # SOURCE OF TRUTH (all content lives here)
  pub.polis.core/
    bundle.json                      # bundle definition (the contract)
    index.jsonl                      # bundle content registry
    post/                            # pub.polis.post source
      YYYYMMDD/slug.md
      YYYYMMDD/.versions/slug.md
    comment/                         # pub.polis.comment source
      YYYYMMDD/slug.md
      blessed.json                   # signed blessing list (public artifact)
    follow/                          # pub.polis.follow data
      following.json
    tag/<name>.json                  # pub.polis.tag
    attestation/<id>.json            # pub.polis.attestation
    license/license.json             # pub.polis.license (only once terms are stated)
    actor/registry.json              # pub.polis.actor (only on an operator's site)

policies/                            # public policies (one rule per line, JSONL)
  rules.jsonl

# RENDERED OUTPUT (generated from content/ via mount points)
posts/                               # mount: /posts (rendered HTML + copied .md)
  YYYYMMDD/slug.html
  YYYYMMDD/slug.md
comments/                            # mount: /comments
  YYYYMMDD/slug.html
  YYYYMMDD/slug.md
  blessed.json                       # copied from content/

.polis/                              # ALL PRIVATE STATE
  keys/
    id_ed25519
    id_ed25519.pub
  storage-salt                       # 32 random bytes (hex); retained as a tamper canary (Patrol) — no longer a DM key salt
  api-keys.json                      # API key hashes (SHA-256)
  logs/                              # structured event logs
  policies/                          # private policies (evaluated before public)
    rules.jsonl
  ds/<domain>/
    pub.polis.core/                  # bundle-scoped DS (handler-managed)
      state/                         # computed/derived data (safely deletable)
        cursors.json                 # per-projection cursor positions
        pub.polis.follow.json        # materialized follower set
        pub.polis.comment.blessing.json  # blessing state
        pub.polis.notification.jsonl # notification entries
        pub.polis.feed.jsonl         # feed items (plus scoped variants, e.g. pub.polis.feed.me.jsonl)
      config/                        # user preferences (survives resets)
        feed.json                    # staleness_minutes, max_items, max_age_days
  bundles/
    registry.json                    # active theme + shape, installed bundles, notification rules
    pub.polis.core/                  # installed payload + private bundle state
      shapes/                        # reference payload (synced from the binary)
      themes/
      posts/
        drafts/
      comments/
        drafts/
        pending/
        denied/
      dm/                            # pub.polis.dm (private, encrypted at rest)
        keyring.json                 # wrapped message keys, per epoch
        inbox.json                   # derived, rebuildable
        conversations/<peer>/
          messages.jsonl
          conversation.json
  webapp/
    config.json
    hooks/
```

### Directory Categories

| Category | Root | Purpose |
|----------|------|---------|
| **Public source** | `content/` | Authoritative content files. All posts, comments, and follow data live here. |
| **Rendered output** | `posts/`, `comments/` | Generated from `content/` via mount points. Regeneratable, but must be committed for self-hosting. |
| **Site resources** | `site/` | Snippets and other rendering resources. (Themes live in the installed bundle under `.polis/bundles/`.) |
| **Identity** | `.well-known/polis`, `.well-known/did.json` | Author identity, public key and its history, bundle pointers, site metadata. |
| **Private state** | `.polis/` | Keys, logs, DS state/config, drafts, pending comments, API keys, webapp config. |
| **Policies** | `policies/`, `.polis/policies/` | Public and private policy rules (JSONL). Private evaluated first. |

### Conventions

**Bundle naming.** Bundles use reverse-domain notation: `pub.polis.core`, `com.example.recipes`. The bundle name is the directory name under `content/`.

**Content type directories.** Each content type declares a `dir` field. The source directory is `content/<bundle>/<dir>/`. Example: `content/pub.polis.core/post/` for `pub.polis.post`.

**Date format.** Posts and comments use `YYYYMMDD` date directories (e.g., `content/pub.polis.core/post/20260301/hello.md`). The format is declared per content type in `storage.date_format`.

**Version storage.** When `storage.versions` is true, previous versions are stored in `.versions/` subdirectories alongside content. Example: `content/pub.polis.core/post/20260301/.versions/hello.md`.

**Private mirroring.** Private state mirrors the public content path under `.polis/bundles/`. If bundle content is at `content/pub.polis.core/`, the private root is `.polis/bundles/pub.polis.core/`. The private directory uses plural names for content types that have public mounts: `post` becomes `posts`, `comment` becomes `comments`. Private-only types like `dm` keep their original directory name. Example: `.polis/bundles/pub.polis.core/posts/drafts/`. (Older sites used `.polis/content/`; Medic migrates this path automatically.)

**Bundle install.** Each tenant has its `.polis/bundles/<bundle>/` populated with the bundle's reference payload — shape templates and per-theme CSS — by `polis init` (new sites) and resynced by Medic whenever the installed versions fall behind the binary's (existing tenants). The reference payload ships embedded in the CLI binary at build time. See SHAPE/BUNDLE/THEME below.

**DS state scoping.** Discovery service state is scoped by DS domain and bundle name: `.polis/ds/<domain>/<bundle>/`. Each bundle maintains its own state and config directories. State files are safely deletable (recomputed from the stream). Config files hold user preferences and survive resets. All state filenames match their cursor key in `cursors.json` (e.g., cursor key `pub.polis.feed` corresponds to file `pub.polis.feed.jsonl`).

---

## Bundles

### Location

Bundle declarations live at `content/<bundle-name>/bundle.json`. The `.well-known/polis` file points at each installed bundle's declaration (abridged — the identity fields are specified in [`signet/spec/key-history.md`](../../signet/spec/key-history.md) and beside the other pointers):

```json
{
  "version": "polis-cli-go/0.67.0",
  "public_key": "ssh-ed25519 ...",
  "author_name": "alice",
  "site_title": "Alice's Space",
  "created": "2026-01-01T00:00:00Z",
  "bundles": {
    "pub.polis.core": {
      "path": "content/pub.polis.core/bundle.json"
    }
  }
}
```

Listing a bundle is what activates it; an older `active` flag was always true and has been removed.

> **active_theme moved.** Pre-bundle-refactor `.well-known/polis` carried an
> `active_theme` field. That has moved to a private per-tenant file at
> `.polis/bundles/registry.json` (see SHAPE/BUNDLE/THEME below). Tenants
> with the legacy field are migrated transparently by Medic.

### Schema

```json
{
  "name": "<string, required>",
  "version": "<semver string, required>",
  "description": "<string, optional>",
  "handler": { "<handler declaration, required>" },
  "ds": { "<DS integration, optional>" },
  "types": { "<map of content type name → declaration, required>" },
  "shapes": { "<map of shape name → shape declaration, optional>" },
  "themes": { "<map of theme name → theme declaration, optional>" },
  "artifacts": ["<bundle-level output filenames, optional>"]
}
```

### Top-Level Fields

#### Required Fields

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Reverse-domain bundle identifier (e.g., `pub.polis.core`, `com.example.recipes`). |
| `version` | string | Semantic version of the bundle. |
| `handler` | object | Declares how the system invokes this bundle. See [Handler Declaration](#handler-declaration). |
| `types` | map | Content type declarations, keyed by fully-qualified type name. |

#### Optional Fields

| Field | Type | Description |
|-------|------|-------------|
| `description` | string | Human-readable description of the bundle. |
| `ds` | object | Discovery service integration settings. See [DS Integration](#ds-integration). |
| `shapes` | map | Shape declarations — `name`, `version`, `entry` (template map), `partials`, `default_css`. See [SHAPE / BUNDLE / THEME](#shape--bundle--theme). |
| `themes` | map | Theme declarations — `name`, `version`, `css`, `display_name`, `compatible_shapes`. |
| `artifacts` | string[] | Bundle-level output filenames (e.g., `["index.jsonl"]`). |

### Handler Declaration

The handler declares how the system dispatches operations for this bundle's content types.

```json
{
  "handler": {
    "type": "builtin | executable | http",
    "path": "<path to executable, required for executable type>",
    "url": "<endpoint URL, required for http type>"
  }
}
```

| Type | Description | Use Case |
|------|-------------|----------|
| `builtin` | Handled by CLI/webapp natively (Go code) | `pub.polis.core` types |
| `executable` | External binary invoked with JSON stdin/stdout | Third-party local plugins |
| `http` | Remote HTTP endpoint called with JSON POST | Remote/hosted plugins |

**Validation rules:**
- `type` is required and must be one of `builtin`, `executable`, or `http`.
- `executable` type requires `path`.
- `http` type requires `url`.
- `builtin` type requires no additional fields.

### DS Integration

The `ds` field controls how the bundle interacts with discovery service events.

```json
{
  "ds": {
    "subscribes_to": ["pub.polis.*"]
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `subscribes_to` | string[] | Event glob patterns for fan-out routing. Multiple bundles can match the same events (no ordering guarantee). |

Glob matching rules:
- `*` matches everything.
- `pub.polis.*` matches any event starting with `pub.polis.`.
- Exact strings match exactly.

### Content Type Declarations

Each entry in the `types` map declares a single content type. The key is the fully-qualified type name (e.g., `pub.polis.post`).

```json
{
  "pub.polis.post": {
    "dir": "post",
    "mount": "/posts",
    "renderer": "html",
    "storage": {
      "pattern": "dated",
      "date_format": "YYYYMMDD",
      "versions": true
    },
    "emits": [
      "pub.polis.post.published",
      "pub.polis.post.republished"
    ]
  }
}
```

> **Note — notification rules moved out of `bundle.json`.** Earlier versions
> declared a `notifications` array on each content type here. As of the
> step-06 owner-stream work they live in the **private** per-tenant registry
> (`.polis/bundles/registry.json`, `BundleRegistry.Notifications`) instead, so
> they're not part of the public content-type contract a third party reads.
> See [Notification Rules](#notification-rules) below. Patrol/Medic relocates
> any lingering per-type `notifications` from existing tenants' `bundle.json`
> on each healing cycle.

#### System Fields

These fields are read by the engine and resolver to route, render, and organize content.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `dir` | string | yes | Content type directory, relative to `content/<bundle>/`. Must be unique within the bundle. |
| `mount` | string | no | Public URL path for rendered output (e.g., `/posts`). Must be unique within the bundle. |
| `renderer` | string | no | Rendering engine. Currently only `html` is supported. |
| `emits` | string[] | no | Fully namespaced event names this type produces. Used for policy matching and DS validation. |
| `private` | boolean | no | Marks the type as owner-private (e.g. `pub.polis.follow`, `pub.polis.dm`). The v1 content API requires auth to read these; CI enforces parity with the `isPrivateContentType` allowlist (`webapp/internal/api/router.go`). |

#### User Customization

These fields are read by the handler, not the system. The handler uses them as guidance for storage decisions.

| Field | Type | Description |
|-------|------|-------------|
| `storage` | object | Storage layout configuration. |
| `storage.pattern` | string | `"dated"` (date-based directories) or `"flat"` (single directory). |
| `storage.date_format` | string | Date format for directory names (e.g., `"YYYYMMDD"`). |
| `storage.versions` | boolean | Whether to store previous versions in `.versions/` subdirectories. |

#### Notification Rules

Notification rules declare when and how to notify the owner about events. They
are **not** part of the public `bundle.json` content-type declaration — they
live in the **private** per-tenant registry at `.polis/bundles/registry.json`
(`BundleRegistry.Notifications`). The canonical default set is seeded by
`defaultNotificationRules()` in `cli-go/pkg/bundle/config.go`. Keeping them in
the private registry means a site's notification preferences aren't exposed in
its world-readable `bundle.json`, and the public content-type contract stays
purely about *what content exists*, not *how the owner is alerted*.

The rule schema itself is unchanged:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | yes | Unique identifier for this rule (across the registry). |
| `on` | string | yes | Event type that triggers this notification. |
| `relevance` | string | yes | Filter that determines who sees the notification. |
| `template` | string | yes | Mustache-like template for the notification message. |
| `icon` | string | no | Icon name (e.g., `pencil`, `comment`, `prayer`, `check`, `x`, `follow`, `unfollow`). |
| `enabled` | boolean | no | Whether the rule is active. Defaults to `true` if omitted. |
| `batch` | string | no | Batching window (e.g., `"24h"`). When set, notifications of this type are batched over the window. |

> Third-party bundles *may* still ship a per-type `notifications` array in
> their own `bundle.json` (the loader/validator still accepts it). The
> relocation to the private registry applies to the built-in `pub.polis.core`
> bundle; Patrol/Medic migrates legacy core-bundle entries automatically.

**Relevance values:**

| Value | Meaning |
|-------|---------|
| `target_domain` | Notify when `payload.target_domain` matches the local site's domain. Used for incoming actions (comments on your posts, follow events targeting you, blessing decisions on your posts). |
| `source_domain` | Notify when `payload.source_domain` matches the local site's domain. Used for outgoing action results (your comment was blessed/denied). |
| `followed_author` | Notify when the event actor is in the local site's following list. Used for content from authors you follow (new posts, updates). |

### Conventions Not Declared

The following are the handler's business and are NOT declared in `bundle.json`:

- Internal workflow states (drafts, pending, denied).
- DS state/config file names, formats, and structure.
- Per-type artifact lists.
- Action/command lists.
- The handler creates and manages its own DS files under `.polis/ds/<domain>/<bundle>/`.

### Full `pub.polis.core` Bundle

```json
{
  "name": "pub.polis.core",
  "version": "1.0.0",
  "description": "Core polis content types",
  "handler": {
    "type": "builtin"
  },
  "ds": {
    "subscribes_to": ["pub.polis.*"]
  },
  "types": {
    "pub.polis.post": {
      "dir": "post",
      "mount": "/posts",
      "renderer": "html",
      "storage": {"pattern": "dated", "date_format": "YYYYMMDD", "versions": true},
      "emits": ["pub.polis.post.published", "pub.polis.post.republished", "pub.polis.post.unpublished"]
    },
    "pub.polis.comment": {
      "dir": "comment",
      "mount": "/comments",
      "renderer": "html",
      "storage": {"pattern": "dated", "date_format": "YYYYMMDD", "versions": false},
      "emits": [
        "pub.polis.comment.published", "pub.polis.comment.republished", "pub.polis.comment.unpublished",
        "pub.polis.comment.blessing.requested", "pub.polis.comment.blessing.granted", "pub.polis.comment.blessing.denied"
      ]
    },
    "pub.polis.follow": {
      "dir": "follow",
      "mount": "/follow",
      "private": true,
      "emits": ["pub.polis.follow.announced", "pub.polis.follow.removed"]
    },
    "pub.polis.dm": {
      "dir": "dm",
      "private": true,
      "storage": {"pattern": "flat"}
    },
    "pub.polis.tag": {
      "dir": "tag",
      "mount": "/tags",
      "storage": {"pattern": "flat"},
      "emits": ["pub.polis.tag.applied", "pub.polis.tag.removed"]
    },
    "pub.polis.attestation": {
      "dir": "attestation",
      "mount": "/attestations",
      "storage": {"pattern": "flat"},
      "emits": ["pub.polis.attestation.issued", "pub.polis.attestation.withdrawn"]
    },
    "pub.polis.actor": {
      "dir": "actor",
      "mount": "/actors",
      "storage": {"pattern": "flat"},
      "emits": ["pub.polis.actor.registered", "pub.polis.actor.reregistered", "pub.polis.actor.withdrawn"]
    },
    "pub.polis.license": {
      "dir": "license",
      "mount": "/license"
    },
    "pub.polis.theme": {
      "dir": "theme",
      "storage": {"pattern": "flat"}
    }
  },
  "shapes": { "v3": { "...": "..." }, "v4": { "...": "..." } },
  "themes": { "vice": { "...": "..." }, "...": "..." },
  "artifacts": ["index.jsonl"]
}
```

> Notification rules are intentionally absent from this example — they live in
> the private registry (see [Notification Rules](#notification-rules)).
> `pub.polis.follow` and `pub.polis.dm` are `private` (auth-gated reads). The
> source of truth for this manifest is `DefaultCoreBundle()` in
> `cli-go/pkg/bundle/bundle.go`.

### Handler Invocation Contract

When an action is triggered, the system resolves the content type to its bundle and handler, then dispatches using a standard JSON contract. The contract is the same regardless of handler type (`builtin`, `executable`, or `http`).

**Request:**

```json
{
  "action": "create",
  "content_type": "com.example.recipes.recipe",
  "payload": {
    "file": "content/com.example.recipes/recipes/pasta.md",
    "site_dir": "/path/to/site",
    "author": "alice",
    "domain": "alice.example.com"
  }
}
```

**Response:**

```json
{
  "status": "success",
  "data": {
    "path": "content/com.example.recipes/recipes/pasta.md",
    "title": "Pasta Carbonara",
    "version": "sha256:abc123..."
  }
}
```

For `builtin` handlers, the dispatch is a Go function call within `BuiltinCoreHandler`. For `executable` handlers, the request is written to the binary's stdin and the response is read from stdout. For `http` handlers, the request is POSTed to the handler URL and the response is the HTTP body.

**Executable handler environment variables:**

| Variable | Value |
|----------|-------|
| `POLIS_SITE_DIR` | Absolute path to the site root |
| `POLIS_BASE_URL` | Site's base URL |
| `POLIS_DISCOVERY_URL` | Discovery service URL |

### Naming Convention

Bundle names use reverse-domain notation. The `pub.polis` prefix is reserved for official polis types. Third-party bundles use their own domain prefix (e.g., `com.example.recipes`, `org.writers.prompt`).

Content type names are prefixed with the publisher's namespace, not the bundle name. Example: `pub.polis.post` belongs to the `pub.polis.core` bundle, but its namespace prefix is `pub.polis`.

### Validation Rules

The following constraints are enforced when loading a bundle:

1. `name` is required and must be non-empty.
2. `version` is required and must be non-empty.
3. `handler.type` is required and must be `builtin`, `executable`, or `http`.
4. `executable` handlers must provide `path`; `http` handlers must provide `url`.
5. Every content type must have a non-empty `dir`.
6. No two content types within a bundle may share the same `dir`.
7. No two content types within a bundle may share the same `mount`.
8. Notification rule `id` values must be unique across the entire bundle.
9. Each notification rule must have `id`, `on`, `relevance`, and `template`.
10. A bundle name may not contain `.themes.` or `.shapes.`.
11. Each shape needs a `name` matching its map key, a `version` and a non-empty `entry`; each theme needs a matching `name`, a `version` and `css`.

---

## SHAPE / BUNDLE / THEME

A bundle ships not only **content types** (post, comment, …) but also
**shapes** and **themes** that drive rendering.

### Shape

A **shape** declares the rendering approach for a class of polis output —
the markup templates plus supporting partials. The `pub.polis.core` bundle
ships two shapes: the **blog** shape (post pages, index, archive, tag pages)
and the **stream** shape (infinity-stream rendering — focus post inlined +
adjacent sibling excerpts). Wire names remain `pub.polis.shapes.v3` and
`pub.polis.shapes.v4` for tenant-data compatibility; `v3`/`v4` are the
on-disk shorthand the blog/stream shapes were originally introduced under.

Shapes are declared in `bundle.json` under the `shapes` key:

```json
"shapes": {
  "v3": {
    "name": "v3",
    "version": "1.0.0",
    "entry": {
      "post": "post.html",
      "comment": "comment.html",
      "comment_inline": "comment-inline.html",
      "index": "index.html",
      "archive": "posts.html",
      "tag": "tag.html",
      "tag_index": "tag-index.html"
    },
    "partials": ["snippets/about.html", "snippets/post-item.html", ...],
    "default_css": "themes/_shared/base.css"
  }
}
```

### Theme

A **theme** is a CSS-only presentation compatible with one or more shapes.
Themes are declared without markup. Per-template markup overrides (the `studio13-nk`
pattern, for the blog shape) live in the *theme directory* at install time and beat the shape's
templates via the loader's local-then-shape lookup.

Themes are declared in `bundle.json` under the `themes` key:

```json
"themes": {
  "_shared": {
    "name": "_shared",
    "version": "1.2.0",
    "css": "base.css",
    "compatible_shapes": ["pub.polis.shapes.v3", "pub.polis.shapes.v4"]
  },
  "studio13-nk": {
    "name": "studio13-nk",
    "version": "1.0.1",
    "css": "studio13-nk.css",
    "display_name": "studio13",
    "compatible_shapes": ["pub.polis.shapes.v3"]
  }
}
```

`_shared` (leading underscore) is a system directory — the structural CSS the
themes share — not a selectable theme. `display_name` is the picker's label
when it differs from the name.

### Per-tenant install

Each tenant has the bundle's reference payload installed at
`.polis/bundles/<bundle>/`. For `pub.polis.core` this means:

```
.polis/bundles/pub.polis.core/
├── shapes/v3/                     # templates + snippets
│   ├── index.html  post.html  comment.html  ...
│   └── snippets/about.html  ...
├── shapes/v4/                     # stream.html  stream-post.html  stream.js  stream.css  snippets/
└── themes/                        # CSS (studio13-nk also ships three templates)
    ├── _shared/base.css
    ├── especial-light/especial-light.css
    ├── vice/vice.css
    └── ...
```

`polis init` writes this on new sites. On hosted tenants Patrol compares the
installed shape and theme versions with the binary's on every cycle, and Medic
resyncs when they fall behind — the binary's embedded reference payload is
always the source of truth, so a binary upgrade ships new shape templates
or theme CSS without a manual step. Self-hosters get the same migrations by
running the standalone `tailor --apply` binary against their site.

### Active selections

The site's active theme and shape live in a private per-tenant file:

```
.polis/bundles/registry.json
{
  "active_theme": "pub.polis.themes.vice",
  "active_shape": "pub.polis.shapes.v4",
  "installed_bundles": [
    { "name": "pub.polis.core", "path": ".polis/bundles/pub.polis.core",
      "shape_versions": { "v3": "1.0.0", "v4": "..." }, "theme_versions": { "vice": "1.1.0", "...": "..." } }
  ],
  "notifications": [ "..." ]
}
```

Theme/shape names are **fully qualified**: `<namespace>.<kind>.<name>`.
Reserved substrings `.themes.` and `.shapes.` cannot appear in bundle
names — the parser uses them as pivots.

Pre-refactor sites carried `active_theme` in `.well-known/polis`;
`site.MigrateActiveThemeToRegistry` relocates it (Medic and Tailor invoke
this automatically).

### Where the reference payload lives in the repo

Source-of-truth payload ships embedded in the CLI binary at
`cli-go/pkg/bundle/fixtures/<bundle>/`. For `pub.polis.core`:

```
cli-go/pkg/bundle/fixtures/pub.polis.core/
├── shapes/v3/...
└── themes/{_shared,especial-light,vice}/...
```

All shipped themes (`especial`, `especial-light`, `sols`, `stardust`, `studio13`,
`studio13-nk`, `turbo`, `vice`, `zane` — plus `_shared` for base CSS)
now live in the bundle fixture at
`cli-go/pkg/bundle/fixtures/pub.polis.core/themes/`. The repo-root
`themes/` directory is retained for `catalog.html` (a development-only
gallery) and no longer ships site CSS. `sols` (the logged-out landing theme)
and `stardust` are shipped but reserved — the webapp refuses either as a
personal theme.

---

## Content Types

The `pub.polis.core` bundle declares **nine** content types — post, comment,
follow, dm, tag, attestation, actor, license, and theme. Each has distinct
storage patterns, actions, and lifecycle behavior. `pub.polis.theme` declares
themes as content (see SHAPE/BUNDLE/THEME above). The sections below cover the
types with lifecycle detail specific to this page; the licence and the actor
registry have their own specs ([`signet/spec/license.md`](../../signet/spec/license.md),
[`signet/spec/custody.md`](../../signet/spec/custody.md)). A further section,
`pub.polis.feed`, documents the *derived* feed cache — which is **not** a
declared bundle type (see the note in that section).

### The content index — `index.jsonl`

⭐ **This is the format's specification.** It went unwritten for a long time, and
in that time four separate Go structs disagreed about it — which is how
`polis rebuild` came to publish an index the site's own integrity checker
rejected on every line.

**Path:** `content/<bundle>/index.jsonl`, discovered by following
`.well-known/polis` → `bundles` → each bundle's `bundle.json`. Never assume
`content/pub.polis.core/` — `dir` and `mount` are user-configurable.
**Mode:** `0644`. It is public content served over HTTP.
**Format:** one JSON object per line, newline-terminated, no wrapper array.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `type` | string | ✅ | `post` · `comment` · `tag` · `attestation` |
| `path` | string | ✅ | Site-relative, slash-separated path of the **signed source**, e.g. `content/pub.polis.core/post/20260101/hello.md` |
| `title` | string | — | Human label. The tag name for a tag; the predicate for an attestation |
| `published` | string | ✅ | RFC 3339. The `published` frontmatter field for a post or comment, `created` for a tag, `asserted` for an attestation |
| `current_version` | string | ✅ | `sha256:<64 hex>` — the content version, copied from the source's own field rather than recomputed |
| `in_reply_to` | object | — | Comments only: `{"url": …, "version": …}` |
| `license` | object | — | The work's materialised terms, read from its signed frontmatter. A **projection**, refreshed whenever the entry is rewritten |

**`type` uses the index's own short vocabulary** (`post`, not `pub.polis.post`).
The fully-qualified name is the bundle's; this file has said `post` and
`comment` since it existed.

⚠️ **The address is the source, never a projection.** `path` points at the
signed markdown or JSON, not at the rendered `/posts/…` mount. That is what
makes an entry verifiable: you fetch the bytes that were signed. A consumer
that wants the rendered page derives it from the mount declaration.

**Ordering is ASCENDING by `published`** — the order an append-built index
naturally has. Consumers that want newest-first reverse it.

⚠️ **A reader must tolerate an unknown `type` and unknown fields.** The list is
open: a bundle may declare content types this reader has never met, and dropping
a line it cannot classify is the destructive choice. Filter on the types you
handle; carry the rest through.

#### Who writes it, and the rule that governs them

⭐ **A projection may have many sources. Any regenerator that knows one source
must not own the whole file.**

Entries arrive by several routes — `publish` appends a post, blessing a comment
appends a comment, every tag and attestation write refreshes its own type's
lines, Medic restores missing tag and attestation lines on a hosted site, and
`polis rebuild` regenerates from the content on disk — and each knows only part
of the file.

⚠️ **An index is only as fresh as the site's last write or heal.** A type with
no entries means *nothing indexed*, never *nothing exists*: a site running an
older CLI, or a record copied in by hand, can hold records its index does not
list — and HTTP does not list directories, so a reader cannot tell. Tags and
attestations are written to the index at write time; on sites written by older
CLIs they reached the index only on `polis rebuild`. So a rebuild is composed from
**contributors**: one function per content type, each returning its own slice.
`polis rebuild --posts` replaces the post lines and re-emits every other line
**from its original bytes**, never re-marshalled through a struct that might not
model all of its fields.

The implementation is `cli-go/pkg/index` and there is exactly one of it: Tailor's
`index-rebuild` pass calls the same code rather than carrying its own.

> ⚠️ **The action tables below list what each type's handler *advertises*** (`Actions()` in
> `cli-go/pkg/ops/builtin_core.go`), which is what `/v1/bundles` reports. **The builtin handler
> implements fewer**: over the v1 API, posts support `list` and `create`; comments `create` and
> `update`; follows `list`; tags and DMs everything listed; themes `list` and `get`; the licence `get`.
> An advertised action it does not implement is refused as unsupported. The CLI and the webapp's own
> endpoints call the same packages directly and are not limited by this.

### `pub.polis.post`

Posts are the primary content unit in polis -- markdown files with signed frontmatter published to the author's domain.

**Source path:** `content/pub.polis.core/post/YYYYMMDD/slug.md`
**Mount path:** `posts/YYYYMMDD/slug.html` (rendered), `posts/YYYYMMDD/slug.md` (copied)
**Private path:** `.polis/bundles/pub.polis.core/posts/drafts/`
**Renderer:** `html`
**Storage:** Dated directories (`YYYYMMDD`), with version history in `.versions/`

**Actions:**

| Action | Description |
|--------|-------------|
| `list` | Read from `content/pub.polis.core/index.jsonl`, return post entries (newest first). |
| `get` | Retrieve a single post by path or ID. |
| `create` | Publish a new post: sign content, write to content dir, update index, register with DS. |
| `update` | Republish an existing post with updated content (new version). |
| `delete` | Remove a post (unpublish). |
| `render` | Render markdown to HTML using the active theme. |
| `draft.list` | List saved drafts. |
| `draft.get` | Retrieve a single draft. |
| `draft.save` | Save a draft to `.polis/bundles/pub.polis.core/posts/drafts/`. |
| `draft.delete` | Delete a draft. |

**Events declared:** `pub.polis.post.published`, `pub.polis.post.republished`, `pub.polis.post.unpublished`

**Lifecycle:** Create writes signed markdown to the dated content directory, stores a version hash in frontmatter, appends to `index.jsonl`, and registers with the discovery service (emitting `pub.polis.post.published`). Republish updates the content, creates a new version entry in `.versions/`, and emits `pub.polis.post.republished`. Unpublish removes the post, and the discovery service emits `pub.polis.post.unpublished` — declared behind a migration; see *Adding a declared event* below.

### `pub.polis.comment`

Comments are replies to posts or other comments, published on the commenter's own domain. They participate in the blessing workflow -- the original author must approve (bless) a comment before it is promoted.

**Source path:** `content/pub.polis.core/comment/YYYYMMDD/slug.md`
**Mount path:** `comments/YYYYMMDD/slug.html` (rendered), `comments/YYYYMMDD/slug.md` (copied)
**Private paths:**
- `.polis/bundles/pub.polis.core/comments/drafts/` -- comment drafts
- `.polis/bundles/pub.polis.core/comments/pending/` -- comments awaiting blessing
- `.polis/bundles/pub.polis.core/comments/denied/` -- denied comments
**Renderer:** `html`
**Storage:** Dated directories (`YYYYMMDD`), with version history in `.versions/` (mirrors posts)
**Public artifacts:** `content/pub.polis.core/comment/blessed.json` -- index of blessed comments

**Actions:**

| Action | Description |
|--------|-------------|
| `list` | List comments from the index. |
| `get` | Retrieve a single comment. |
| `create` | Publish a comment and register it with the DS, which records a blessing request for the post author. |
| `update` | Republish a comment with edited content (new signed version recorded in `.versions/`; blessing carries forward). |
| `bless` | Grant blessing to a pending comment (post author action). |
| `deny` | Deny blessing to a pending comment (post author action). |
| `revoke` | Revoke a previously granted blessing. |
| `sync` | Synchronize blessing state with the discovery service. |

**Events emitted:** `pub.polis.comment.published`, `pub.polis.comment.republished`, `pub.polis.comment.unpublished`, `pub.polis.comment.blessing.requested`, `pub.polis.comment.blessing.granted`, `pub.polis.comment.blessing.denied`

**Lifecycle:** Create writes a signed comment with `in_reply_to` metadata, then registers it with the DS, which records a blessing request (emitting `pub.polis.comment.blessing.requested`) and decides nothing. The post author can bless (emitting `pub.polis.comment.blessing.granted`) or deny (emitting `pub.polis.comment.blessing.denied`). Blessed comments are added to `blessed.json` and rendered alongside the original post. Blessing decisions are policy-driven and made by the post author's own site, never the DS: by default, with Rosie switched on, comments from self, followed authors, and thread-trusted authors are blessed via `bless` rules in `policies/rules.jsonl`. See `docs/cli/user/policies.md`.

Republish (`update`) produces a new signed version: it preserves the original `published` timestamp (keeping the comment URL stable), records an `updated` timestamp, appends the new version to the `.versions/` side-car, advances `current_version` in `index.jsonl`, and re-registers the content with the DS (emitting `pub.polis.comment.republished`). Blessing state carries forward as the DS records it — a granted blessing is preserved, a denial stays denied (republish cannot launder it back into review), and only a still-pending comment is put back to the post author's site to decide. `blessed.json` is intentionally left pinned to the blessed version, so divergence from the live `current_version` is the canonical "edited since blessing" signal (`comment.IsEditedSinceBlessing`). ⚠️ **Nothing may regenerate `blessed.json` from discovery-service state** — the DS stores *which* comment was blessed and never *which version*, so a rebuild from DS records erases every pin and the signal with it, unrecoverably. The file is authored, and it is also **signed**; see [`../../signet/spec/attestation.md`](../../signet/spec/attestation.md) *The signed blessing list*. As of this writing the capability is wired through the CLI, ops engine, and v1 REST API but not yet surfaced in the webapp UI.

### `pub.polis.follow`

Follow is the social graph primitive. It manages the list of authors a site trusts and the follower set maintained from stream events.

**Source path:** `content/pub.polis.core/follow/`
**Mount path:** `follow/`
**Public artifacts:**
- `content/pub.polis.core/follow/following.json` -- authors this site follows
**DS state:** `.polis/ds/<domain>/pub.polis.core/state/pub.polis.follow.json` -- materialized follower set (from stream projection)

**Actions:**

| Action | Description |
|--------|-------------|
| `list` | Return the following list with entry metadata (URL, added_at, site_title, author_name). |
| `create` | Follow a new author: add to `following.json` (re-signed), bless their comments waiting on your posts that your policy now admits, publish `pub.polis.follow.announced` event. |
| `delete` | Unfollow an author: remove from `following.json` (re-signed), re-evaluate their granted blessings against your policy and deny those it no longer admits, publish `pub.polis.follow.removed` event. |

**Events emitted:** `pub.polis.follow.announced`, `pub.polis.follow.removed`

**Lifecycle:** Follow events are published directly to the stream (explicit emission) -- the client signs a canonical payload and calls `POST /v1/stream`. The local follower set is maintained as a client-side projection: the `FollowHandler` processes `pub.polis.follow.announced` and `pub.polis.follow.removed` events, materializing a follower list in `.polis/ds/<domain>/pub.polis.core/state/pub.polis.follow.json`.

### `pub.polis.feed`

Feed is an aggregated view of content from followed authors. It combines stream events with direct site polling to provide a unified timeline.

> **Note — feed is *not* a declared `types` entry in the core bundle.** Unlike
> the nine declared types, `pub.polis.feed` has no entry in
> `DefaultCoreBundle()`. It's a derived/aggregated view implemented by the
> `cli-go/pkg/feed` package and materialized into the DS state cache below —
> there is no `content/pub.polis.core/feed/` source directory or public mount.
> The `pub.polis.feed` name survives as the cursor/state key, not as a bundle
> content type.

**DS state:** `.polis/ds/<domain>/pub.polis.core/state/pub.polis.feed.jsonl` (feed item cache)
**DS config:** `.polis/ds/<domain>/pub.polis.core/config/feed.json` (staleness_minutes, max_items, max_age_days)

It has no dispatch actions: the webapp and CLI read and refresh the cache through `cli-go/pkg/feed` directly.

**Lifecycle:** Feed items are collected from followed authors via stream events and direct site polling. The cache is stored as JSONL in the DS state directory. Staleness is tracked per-entry with configurable thresholds. The feed type has no events of its own -- it consumes events from other types.

### `pub.polis.dm`

Direct messages are private, end-to-end encrypted messages between polis instances. This is the first content type with **no mount point, no renderer, and no DS events** -- it validates that the bundle system handles private content types gracefully.

**Source path:** `.polis/bundles/pub.polis.core/dm/`
**Mount path:** None (private content, never rendered to public HTML)
**Renderer:** None
**Storage:** Flat — a `keyring.json`, a derived `inbox.json`, and `conversations/<peer>/` holding an append-only `messages.jsonl` and a `conversation.json`
**Encryption:** Each message is sealed with NaCl `box` (X25519 + XSalsa20-Poly1305) to the recipient's current messages-key epoch, and stored as that ciphertext. The epoch's key is wrapped by a key derived from the owner's password (Argon2id) or recovery phrase. The full model — including the bootstrap window before a password is set — is [`dm-encryption.md`](../security/dm-encryption.md); the byte format is `cli-go/pkg/dm/FORMAT.md`.

**Actions:**

| Action | Auth | Description |
|--------|------|-------------|
| `list` | Bearer token | List conversation summaries with unread counts. |
| `get` | Bearer token | Get a conversation's messages. |
| `send` | Bearer token | Deliver a sealed DM to a remote instance. |
| `deliver` | Signed request | Receive a sealed DM from a remote instance. |
| `protection_status` | Bearer token | Report whether messages are protected by a password yet. |
| `mark_read` | Bearer token | Mark messages in a conversation as read. |
| `delete` | Bearer token | Delete a conversation locally. |
| `retry` | Bearer token | Retry delivering unsent messages. |

The `deliver` action accepts **signed-request auth** (not a Bearer token) so remote instances can push messages without a pre-shared API key. See [`api/developer/reference.md` § Authentication](../../api/developer/reference.md#authentication).

**Events emitted:** None. DMs are private and do not register with the discovery service.

**Lifecycle:** The sender's instance POSTs the sealed envelope straight to the recipient's `/v1/content/dm/actions/deliver` with signed request headers, and keeps its own copy sealed to the sender's epoch key; a failed delivery is kept as unsent and retried via `retry`. The recipient's instance verifies the signed request and the sender's published messages key, checks its DM acceptance policy, refuses an envelope sealed to a superseded epoch so the sender refetches, and stores the ciphertext exactly as received — it does not decrypt or re-encrypt it.

### `pub.polis.tag`

Tags are lightweight labels applied to content (posts, feed items). A tag is a metadata association between a tag name and a target URL, stored as a flat JSON file on the author's own site.

**Source path:** `content/pub.polis.core/tag/`
**Mount path:** `/tags`
**Renderer:** None
**Storage:** Flat (single directory, no date-based subdirectories)

**Actions:**

| Action | Description |
|--------|-------------|
| `list` | List all tags, optionally filtered by tag name or target URL. |
| `apply` | Apply a tag to a target URL. Creates the tag if it does not exist. |
| `remove` | Remove a tag from a target URL. |
| `delete` | Delete a tag and all its associations. |

**Events emitted:** `pub.polis.tag.applied`, `pub.polis.tag.removed`

**Lifecycle:** Apply writes a tag association linking a tag name to a target URL, registers with the discovery service (emitting `pub.polis.tag.applied`). Remove deletes the association and emits `pub.polis.tag.removed`. Tags are local to the author's site -- each author maintains their own tag vocabulary.

### `pub.polis.attestation`

A signed claim one party makes about someone or something else: *on this date, this issuer asserted this predicate about this subject.*

**Source path:** `content/pub.polis.core/attestation/`
**Mount path:** `/attestations`
**Renderer:** None
**Storage:** Flat (single directory, no date-based subdirectories)

It shares `pub.polis.tag`'s serialisation -- canonical JSON, a `sha256:` content version, one signature -- and none of its concept. A tag is **self-directed**: structure an author puts on their own reading, where the file *is* the tag and holds many targets under one timestamp. An attestation is **other-directed** and happens at a moment, so it holds exactly **one subject** and carries **one date**. Same envelope, different noun.

**Actions:**

| Action | Description |
|--------|-------------|
| `issue` | Write, sign and register a new claim. |
| `list` | List claims this site has issued, ordered by assertion date. |
| `show` | Show one claim and its signature status. |
| `verify` | Check issued claims against the site's published key history. |
| `withdraw` | Issue a signed withdrawal record naming an earlier claim. |

These are `polis attest` subcommands, run by the key holder. ⚠️ **The type has no dispatch actions** — nothing issues a claim through the v1 API.

⚠️ **There is no delete action, deliberately.** Withdrawing a claim is a signed record saying so (predicate `pub.polis.attestation.withdrawal`), never a file deletion -- a `404` read as a retraction would make an outage, a moved site or a lapsed domain read as one too, and the evidence would be gone.

**Events emitted:** `pub.polis.attestation.issued`, `pub.polis.attestation.withdrawn` — the discovery service emits `.withdrawn` instead of `.issued` when the registered record's predicate is `pub.polis.attestation.withdrawal`

**Lifecycle:** Issue writes one signed JSON record per claim and registers it with the discovery service (emitting `pub.polis.attestation.issued`), with `version` set to the record's `current_version` so the DS can tell a changed row from an unchanged one. Records are never edited: a correction to a claim is a new record whose subject is the old one. Because a record has a permanent URL, an attestation can be the subject of another attestation -- which is how counter-signing, withdrawal and dispute all work without any new primitive.

**Format:** [`docs/signet/spec/attestation.md`](../../signet/spec/attestation.md) is the specification, including the byte-level signing base and what a reader must do with a predicate it does not recognise.

---

## Events

### Naming Pattern

Events follow the pattern `<content_type>.<action>`. The content type IS the namespace prefix.

```
pub.polis.post.published
^^^^^^^^^^^^^^^^ ^^^^^^^^
  content type    action
```

Core events use the `pub.polis.*` namespace. Third-party events use reverse-domain namespacing (e.g., `com.bookclub.recommendation`). No registration or permission is required for custom event types -- if the site is registered and the signature is valid, it can publish events with any non-`pub.polis.*` type.

### Complete Event List

#### Post Events

| Event | Emitted By | Description |
|-------|-----------|-------------|
| `pub.polis.post.published` | DS (side-effect of post registration) | A new post was published. |
| `pub.polis.post.republished` | DS (side-effect of post update) | An existing post was updated with new content. |
| `pub.polis.post.unpublished` | DS (side-effect of `POST /v1/content/unpublish`) | A post was unpublished. |

#### Comment Events

| Event | Emitted By | Description |
|-------|-----------|-------------|
| `pub.polis.comment.published` | DS (side-effect of comment registration) | A new comment was published. |
| `pub.polis.comment.republished` | DS (side-effect of comment update) | An existing comment was updated. |
| `pub.polis.comment.unpublished` | DS (side-effect of `POST /v1/content/unpublish`) | The commenter withdrew a comment. |
| `pub.polis.comment.blessing.requested` | DS (side-effect of comment registration) | A comment awaits the post author's decision. The DS records the request and decides nothing. |
| `pub.polis.comment.blessing.granted` | DS (side-effect of the post author's signed grant) | A comment was blessed by the post author. |
| `pub.polis.comment.blessing.denied` | DS (side-effect of deny) | A comment was denied blessing. |

#### Follow Events

| Event | Emitted By | Description |
|-------|-----------|-------------|
| `pub.polis.follow.announced` | Client (explicit via `POST /v1/stream`) | A site started following another site. |
| `pub.polis.follow.removed` | Client (explicit via `POST /v1/stream`) | A site unfollowed another site. |

#### Tag Events

| Event | Emitted By | Description |
|-------|-----------|-------------|
| `pub.polis.tag.applied` | DS (side-effect of tag registration) | A tag was applied to content. |
| `pub.polis.tag.removed` | DS (side-effect of tag removal) | A tag was removed from content. |

#### Attestation Events

| Event | Emitted By | Description |
|-------|-----------|-------------|
| `pub.polis.attestation.issued` | DS (side-effect of attestation registration) | A signed claim was published. Carries `predicate`, `subject`, `subject_type` and the record's `version`. |
| `pub.polis.attestation.withdrawn` | DS (side-effect of registering a record whose predicate is `pub.polis.attestation.withdrawal`) | A claim was withdrawn. The withdrawal is itself a signed record; nothing is deleted. |

#### Actor Registry Events

| Event | Emitted By | Description |
|-------|-----------|-------------|
| `pub.polis.actor.registered` · `.reregistered` · `.withdrawn` | Client (explicit via `POST /v1/stream`) | An operator added, changed or removed an actor in its registry. ⚠️ The event carries a pointer, not the facts; the signed registry file is authoritative. See [`signet/spec/custody.md`](../../signet/spec/custody.md). |

#### Adding a declared event

Every event the discovery service emits for a core content type is declared in that type's `emits` list. `pub.polis.post.unpublished` and `pub.polis.comment.unpublished` were the last two to be added, and how they were added is the rule for the next one.

⛔ **Adding an emit to an existing type is a fleet migration, not an edit.** Patrol and Tailor compare a tenant's `bundle.json` against the shipped defaults as a superset test (the tenant must declare at least what the defaults do), `MergeDefaults` only adds whole missing types, and both remediations *flag* per-field drift rather than repairing it — deliberately, so a tenant's own edits are never overwritten. A new name in the defaults would therefore put every existing tenant into drift that no maintenance cycle clears. So the two `unpublished` events went in behind a **named migration** in Medic and Tailor that adds exactly those two strings to exactly those two types, and nothing else — shipped in one deploy, confirmed on every tenant, and only then followed by the deploy that changed the defaults.

The reverse case is free: `pub.polis.post.removed` and the licence and theme events were declared and emitted by nothing, and were removed from the defaults (removal is safe — a tenant that still declares them is a superset). Stating terms or installing a theme writes files and a local log line; neither puts an event on the network.

#### Site Events

Site events use `pub.polis.site.*`. These are not content type events -- they describe site-level state changes.

| Event | Emitted By | Description |
|-------|-----------|-------------|
| `pub.polis.site.registered` | DS (side-effect of site registration) | A new site was registered with the DS. |
| `pub.polis.site.reregistered` | DS (side-effect of re-registration) | A site re-registered (e.g., after metadata update). |
| `pub.polis.site.key_rotated` | DS (side-effect of key rotation) | A site rotated its Ed25519 signing key. |

### Event Payload Structure

#### Common Envelope

Every event in the stream has the same outer structure:

```json
{
  "id": 4521,
  "type": "pub.polis.post.published",
  "created_at": "2026-02-08T14:30:00Z",
  "actor": "alice.com",
  "signature": "-----BEGIN SSH SIGNATURE-----\n...",
  "payload": { }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `id` | integer | Monotonically increasing event ID. Serves as the cursor -- clients request "give me everything after ID N." |
| `type` | string | Fully-qualified event type name. |
| `created_at` | string | ISO 8601 timestamp of when the event was recorded. |
| `actor` | string | Domain of the site that caused the event. |
| `signature` | string | Ed25519 SSH signature over the canonical payload. |
| `payload` | object | Type-specific data (see below). |

#### Common Payload Fields

These fields appear across multiple event types:

| Field | Used In | Description |
|-------|---------|-------------|
| `target_domain` | follow, comment, blessing | The domain the action is directed at. |
| `source_domain` | comment, blessing | The domain that originated the action (commenter's domain). |
| `url` | post, comment | The canonical URL of the published content. |
| `title` | post | The post title. |
| `version` | post, comment | SHA-256 content hash. |
| `in_reply_to` | comment, blessing | URL of the content being replied to. |
| `root_post` | comment, blessing | URL of the root post in the thread. |
| `comment_url` | blessing | URL of the comment being blessed/denied/requested. |
| `source_url` | blessing (manual grant/deny) | URL of the comment (manual blessing path). |
| `target_url` | blessing (manual grant/deny) | URL of the post being commented on (manual blessing path). |
| `post_name` | comment notification template | Name of the post being commented on (used in notification templates). |

#### Example Payloads

**Post published:**
```json
{
  "url": "https://alice.com/posts/20260208/on-gardens.md",
  "version": "sha256:abc123...",
  "title": "On Gardens"
}
```

**Follow announced:**
```json
{
  "target_domain": "bob.com"
}
```

**Blessing requested:**
```json
{
  "comment_url": "https://carol.com/comments/20260210/reply.md",
  "in_reply_to": "https://alice.com/posts/20260208/on-gardens.md",
  "root_post": "https://alice.com/posts/20260208/on-gardens.md",
  "target_domain": "alice.com",
  "source_domain": "carol.com"
}
```

**Third-party event:**
```json
{
  "type": "com.bookclub.recommendation",
  "actor": "carol.com",
  "payload": {
    "book": "Braiding Sweetgrass",
    "review_url": "https://carol.com/posts/20260205/sweetgrass.md"
  }
}
```

### Glob Patterns for Subscriptions

Bundles declare event interest using glob patterns in `ds.subscribes_to`. The stream store routes events to handlers based on these patterns.

| Pattern | Matches |
|---------|---------|
| `pub.polis.*` | All core polis events (post, comment, follow, tag, attestation, actor, site). |
| `pub.polis.post.*` | Only post events (published, republished, unpublished). |
| `pub.polis.comment.blessing.*` | Only blessing events (requested, granted, denied). |
| `com.bookclub.*` | All events from a hypothetical book club bundle. |
| `*` | All events (wildcard). |

Matching is prefix-based: `pub.polis.*` matches any event whose type starts with `pub.polis.`. Multiple bundles can subscribe to the same patterns, resulting in fan-out (each bundle processes the events independently with its own cursors).

### Event Flow

#### Side-Effect Emission (Post Publish)

```
Client                    Discovery Service              Events Table
  |                              |                           |
  |  POST /v1/content            |                           |
  |  {url, version, sig, ...}    |                           |
  |----------------------------->|                           |
  |                              |  UPSERT ds_content_metadata
  |                              |-------------------------->|
  |                              |                           |
  |                              |  INSERT event             |
  |                              |  type: pub.polis.post.published
  |                              |  (fire-and-forget)        |
  |                              |-------------------------->|
  |                              |                           |
  |  201 {success: true}         |                           |
  |<-----------------------------|                           |
```

#### Explicit Emission (Follow)

```
Client                    POST /v1/stream              Events Table
  |                              |                           |
  |  POST /v1/stream             |                           |
  |  {type, actor, payload, sig} |                           |
  |----------------------------->|                           |
  |                              |  Verify registration      |
  |                              |  Check operator policy    |
  |                              |  Verify signature         |
  |                              |  Check rate limit         |
  |                              |                           |
  |                              |  INSERT event             |
  |                              |  type: pub.polis.follow.announced
  |                              |-------------------------->|
  |                              |                           |
  |  201 {event_id: 4521}       |                           |
  |<-----------------------------|                           |
```

#### Client-Side Projection (Follower Count)

```
Client                    Stream (GET /v1/stream)      Local Disk
  |                              |                           |
  |  Load cursor                 |                           |
  |<---------------------------------------------------------|
  |  cursor = "4500"             |                           |
  |                              |                           |
  |  GET /v1/stream?since=4500   |                           |
  |  &type=pub.polis.follow.*    |                           |
  |----------------------------->|                           |
  |                              |                           |
  |  {events: [...], cursor: "4521"}                         |
  |<-----------------------------|                           |
  |                              |                           |
  |  Process events:             |                           |
  |  +alice.com, -carol.com      |                           |
  |                              |                           |
  |  Save state + cursor         |                           |
  |---------------------------------------------------------→|
  |  {followers: [alice, bob], count: 2}                     |
  |  cursor = "4521"             |                           |
```

### Operator Blocks Apply to Every Event

There is no protected "core" set. Every event — side-effect or client-published, `pub.polis.*` or third-party — is checked against the DS operator's Layer 3 policies before it is recorded (`isBlocked` in `discovery-service/core/stream.ts`, called from both `emitEvent` and `publishToStream`). A matching `deny` drops a side-effect event and refuses a client-published one; if the check itself fails, the event is blocked (fail closed). See [`policy-grammar.md`](../reference/policy-grammar.md) for Layer 3.

### Client-Publishable Events

Only five `pub.polis.*` event types support direct client publication via `POST /v1/stream`:

- `pub.polis.follow.announced` -- the client signs `{type, payload}` and publishes.
- `pub.polis.follow.removed` -- same pattern.
- `pub.polis.actor.registered`, `pub.polis.actor.reregistered`, `pub.polis.actor.withdrawn` -- same pattern, published by an operator.

All other `pub.polis.*` events are emitted server-side as side effects of DS mutations. Third-party events (non-`pub.polis.*` types) can be published directly by any registered actor.

### Canonical Signing

For explicit events (published via `POST /v1/stream`), the signed payload is:

```
JSON.stringify({ type: eventType, payload: payloadObject })
```

For side-effect events, the signature comes from the original DS mutation (e.g., the post registration signature, the blessing grant signature).

### Validation Rules

The following constraints are enforced by the DS when accepting events:

1. The actor must be registered with the discovery service.
2. The Ed25519 signature must be valid over the canonical payload.
3. `pub.polis.*` event types cannot be published by clients (except the five above).
4. The actor and type must not be denied by operator policy.
5. Rate limit: 100 events/hour/actor by default (`domainRateLimits.streamPublish`).
6. Payload must be a JSON object of at most 8KB serialized by default (`sizeLimits.streamPayloadMaxBytes`).
7. Events are immutable once inserted -- operators cannot modify content after insertion.

---

## Previous Event Names

The event naming scheme was updated from `polis.*` to `pub.polis.*` as part of the bundle-based content type architecture. This table maps old event names to their current equivalents.

| Old Name | New Name |
|----------|----------|
| `polis.post.published` | `pub.polis.post.published` |
| `polis.post.republished` | `pub.polis.post.republished` |
| `polis.post.removed` | *(none)* — unpublishing emits `pub.polis.post.unpublished` |
| `polis.comment.published` | `pub.polis.comment.published` |
| `polis.comment.republished` | `pub.polis.comment.republished` |
| `polis.blessing.requested` | `pub.polis.comment.blessing.requested` |
| `polis.blessing.granted` | `pub.polis.comment.blessing.granted` |
| `polis.blessing.denied` | `pub.polis.comment.blessing.denied` |
| `polis.follow.announced` | `pub.polis.follow.announced` |
| `polis.follow.removed` | `pub.polis.follow.removed` |
| `polis.site.registered` | `pub.polis.site.registered` |
| `polis.site.reregistered` | `pub.polis.site.reregistered` |
| `polis.site.key_rotated` | `pub.polis.site.key_rotated` |

Note that the blessing events moved from `polis.blessing.*` to `pub.polis.comment.blessing.*`, nesting them under the comment content type rather than being a top-level namespace. The stream store includes automatic cursor key migration from old `polis.*` keys to their `pub.polis.*` equivalents.
