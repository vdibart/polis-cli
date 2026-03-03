# Polis Glossary

Quick reference for polis-specific terminology.

---

### beseech

Request blessing from a post author via the discovery service. When you publish a comment, it automatically beseeches the original author for their approval.

**Related**: blessing, comment, discovery service

---

### API key

A `polis_`-prefixed Bearer token for authenticating with the Content Type API (`/v1/`). Generated via `polis api-key create`, stored as SHA-256 hashes in `.polis/api-keys.json`. Required for all write operations; read operations are public.

**Related**: Content Type API, bundle

---

### blessed.json

Index file (`content/pub.polis.core/comment/blessed.json`) storing approved comments from other authors. Populated by `polis blessing grant`, `polis follow`, or `polis blessing sync`. Used during render to embed blessed comments in post HTML.

**Related**: blessing, following.json, render

---

### blessing

An author's approval of a comment, making it visible and amplified to their audience. The blessing model is polis's anti-spam mechanism: comments exist regardless, but only blessed ones are promoted. Statuses: pending, blessed, denied.

**Related**: beseech, comment, follow

---

### canonical_url

The authoritative HTTPS URL where a post or comment is published (e.g., `https://alice.com/posts/2026/01/hello.md`). Set automatically based on `POLIS_BASE_URL` and used for signature verification.

**Related**: frontmatter, signature, version

---

### comment

A reply to a post or another comment, published on the commenter's own domain. Comments are signed, include `in_reply_to` metadata pointing to the parent, and automatically request blessing from the original author.

**Related**: blessing, post, beseech

---

### bundle

A namespaced package that declares content types, actions, events, rendering, and storage layout. Bundles are the unit of extensibility in polis. The core bundle `pub.polis.core` provides posts, comments, following, and feed. Bundle definitions live at `content/<bundle-name>/bundle.json`.

**Related**: content type, event, handler

---

### content type

A category of content managed by a bundle (e.g., `pub.polis.post`, `pub.polis.comment`). Each content type has a storage pattern, actions, emitted events, and notification rules declared in its bundle's `bundle.json`.

**Related**: bundle, event, policy

---

### discovery service

A coordination layer (Hono/Deno server on Fly.io) that enables interaction between polis sites. It receives beseech requests, verifies signatures, stores blessing status, indexes content metadata, and provides an event stream. It stores no content — just URLs and signatures.

**Related**: beseech, blessing, signature, event

---

### follow / unfollow

**Follow**: Add an author to your trust list, auto-blessing all their future comments on any of your posts. Stored in `following.json`.

**Unfollow**: Remove an author from your trust list and remove all their previously blessed comments (destructive operation).

**Related**: blessing, following.json

---

### event

A named state change that flows through the discovery service stream (e.g., `pub.polis.post.published`, `pub.polis.comment.blessing.granted`). Events follow the pattern `<content_type>.<action>` and trigger notifications, policies, and handler logic.

**Related**: bundle, content type, discovery service

---

### feed

Aggregated content from authors you follow, cached locally as JSONL. The feed is populated by the webapp's background sync loop or `polis feed refresh`. Configured per-DS in `.polis/ds/<domain>/pub.polis.core/config/feed.json`.

**Related**: follow, discovery service

---

### following.json

Index file (`content/pub.polis.core/follow/following.json`) listing authors you trust. Comments from followed authors are automatically blessed without manual review.

**Related**: follow, blessed.json

---

### frontmatter

YAML metadata section at the top of markdown posts and comments, enclosed by `---` markers. Contains `canonical_url`, `version`, `author`, `published`, `signature`, and for comments, `in_reply_to`.

**Related**: canonical_url, signature, version

---

### handler

The mechanism by which a bundle processes actions. Three handler types: `builtin` (native Go code), `executable` (JSON stdin/stdout to external binary), `http` (JSON POST to URL). Declared in `bundle.json`.

**Related**: bundle, content type

---

### manifest

*(Deprecated — absorbed into `.well-known/polis`.)* Formerly `metadata/manifest.json`. The `active_theme` field now lives in `.well-known/polis`; `post_count` and `comment_count` are derived from `content/pub.polis.core/index.jsonl` at runtime.

**Related**: theme, .well-known/polis

---

### policy

A rule controlling access and content acceptance. Policies use the grammar `<allow|deny> <type> from <source> [at <domain>] [on <target>]`. Private policies (`.polis/policies/rules.jsonl`) are evaluated before public ones (`policies/rules.jsonl`). First match wins, default allow.

**Related**: event, blessing, content type

---

### post

Original content published by an author to their own domain. Posts are markdown files with signed frontmatter, stored in `content/pub.polis.core/post/YYYYMMDD/`, indexed in `content/pub.polis.core/index.jsonl`, and rendered to HTML using themes.

**Related**: comment, signature, render

---

### public key

The Ed25519 public key used to verify signatures on your posts and comments. Published at `.well-known/polis` so anyone (including the discovery service) can verify your content's authenticity.

**Related**: signature, .well-known/polis

---

### index.jsonl

Line-delimited JSON index (`content/pub.polis.core/index.jsonl`) containing metadata for all published posts and comments. Each line is a JSON object with URL, type, title, published date, version, and author. Used to generate `index.html`.

**Related**: post, comment, bundle

---

### render

Convert markdown posts and comments to styled HTML using the active theme's templates. The `polis render` command processes all content, applies mustache templating, embeds blessed comments, and generates `index.html`.

**Related**: theme, snippet, template

---

### signature

An Ed25519 cryptographic signature embedded in post/comment frontmatter, proving the content was authored by the claimed author and hasn't been tampered with. Verified by the discovery service before accepting blessing requests.

**Related**: public key, Ed25519, frontmatter

---

### snippet

A reusable template fragment (HTML or markdown) included in rendered pages via `{{> snippet-name}}` syntax. Theme snippets live in `site/themes/{theme}/snippets/`; global snippets in `site/snippets/` override theme defaults.

**Related**: theme, template, render

---

### theme

A complete styling package containing HTML templates (`index.html`, `post.html`, `comment.html`, `comment-inline.html`), CSS, and snippets. Polis ships with three themes: turbo, zane, and sols. Set via `active_theme` in `.well-known/polis`.

**Related**: snippet, render, manifest

---

### TUI

*(Deprecated — use the [webapp](../webapp/user/user-manual.md) instead.)*

Terminal User Interface (`polis-tui`): a menu-driven, interactive dashboard for polis operations. Was replaced by the webapp in v0.46.0.

**Related**: CLI, webapp

---

### version

SHA-256 hash of content used for content-addressing and change detection. Updated by `polis republish` when content changes. Version history stored in `.versions/` directories alongside content.

**Related**: canonical_url, signature, frontmatter

---

### .well-known/polis

Public identity document at your domain root. Contains author details, Ed25519 public key, site title, active theme, and bundle registry. Used by the discovery service and other readers to verify content signatures and discover site capabilities.

**Related**: public key, signature, bundle, canonical_url
