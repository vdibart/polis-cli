# Polis Glossary

*For* [Writers](../../README.md#writing-on-polis) · [Developers](../../README.md#building-on-polis) · [Reviewers](../../README.md#reviewing-the-security-and-identity-design) — *Kind* [Reference](../../README.md#kinds-of-page)

Quick reference for polis-specific terminology.

---

### beseech

Request blessing from a post author. Publishing a comment registers it with the discovery service, which records a `pub.polis.comment.blessing.requested` event for the post author; `polis blessing beseech` re-sends a request. The discovery service records the request and decides nothing — the post author's own software does.

**Related**: blessing, comment, discovery service

---

### API key

A `polis_`-prefixed Bearer token for authenticating with the Content Type API (`/v1/`). Stored as a SHA-256 hash in `.polis/api-keys.json` (there is no generator command yet — keys are added by hand). Required for all write operations; read operations are public.

**Related**: Content Type API, bundle

---

### blessed.json

The **signed blessing list** (`content/pub.polis.core/comment/blessed.json`): the comments a site's author has admitted onto their pages, each pinned to the version that was blessed. Written by `polis blessing grant`, `polis follow`, `polis blessing sync`, or Rosie under the author's grant — never rebuilt from discovery-service state. Used during render to embed blessed comments in post HTML. Specified in [`signet/spec/attestation.md`](../../signet/spec/attestation.md#the-signed-blessing-list).

**Related**: blessing, following.json, render

---

### blessing

An author's approval of a comment, making it visible and amplified to their audience. The blessing model is polis's anti-spam mechanism: comments exist regardless, but only blessed ones are promoted. Statuses: pending, blessed, denied.

For the rationale behind the term and the model — why "bless" rather than "approve"/"moderate", and why curation lives with the author — see [In Defense of Bless](in-defense-of-bless.md).

**Related**: beseech, comment, follow

---

### canonical_url

The authoritative HTTPS URL where a post or comment is published (e.g., `https://alice.com/posts/20260101/hello.md`), built from `POLIS_BASE_URL` and the content's path. It is what a site registers with the discovery service and what a reader fetches to verify. It is not a frontmatter field.

**Related**: frontmatter, signature, version

---

### comment

A reply to a post or another comment, published on the commenter's own domain. Comments are signed, carry an `in-reply-to` block (`url`, `root-post`) pointing to the parent, and request blessing from the original author when registered.

**Related**: blessing, post, beseech

---

### bundle

A namespaced package that declares content types, events, storage layout, a handler, shapes and themes. Bundles are the unit of extensibility in polis. The core bundle `pub.polis.core` declares nine content types: post, comment, follow, DM, tag, attestation, actor registry, licence and theme. Bundle definitions live at `content/<bundle-name>/bundle.json`.

**Related**: content type, event, handler

---

### content type

A category of content managed by a bundle (e.g., `pub.polis.post`, `pub.polis.comment`). Each content type's directory, mount, storage pattern and emitted events are declared in its bundle's `bundle.json`; the actions it supports are its handler's. (Notification rules live in the private `.polis/bundles/registry.json`.)

**Related**: bundle, event, policy

---

### discovery service

A coordination layer (a Hono/Deno server) that enables interaction between polis sites. It verifies signatures on what sites register, indexes content metadata, records blessing requests and decisions, applies its operator's ingestion policy, and provides an event stream. It stores no content — just URLs, versions and signatures — and it decides no blessing for anyone.

**Related**: beseech, blessing, signature, event

---

### follow / unfollow

**Follow**: Add an author to your trust list. With the default rules, your site blesses their future comments on any of your posts (when Rosie is on). Stored in `following.json`.

**Unfollow**: Remove an author from your trust list. Their granted blessings are re-evaluated against your policy, and any it no longer admits are denied. Both operations re-sign `following.json`.

**Related**: blessing, following.json

---

### event

A named state change that flows through the discovery service stream (e.g., `pub.polis.post.published`, `pub.polis.comment.blessing.granted`). Events follow the pattern `<content_type>.<action>` and trigger notifications, policies, and handler logic.

**Related**: bundle, content type, discovery service

---

### feed

Aggregated content from authors you follow, cached locally as JSONL. The feed is populated by the webapp's background sync loop (there is no CLI feed command). Configured per-DS in `.polis/ds/<domain>/pub.polis.core/config/feed.json`.

**Related**: follow, discovery service

---

### following.json

The signed list (`content/pub.polis.core/follow/following.json`) of authors you follow, published as JSON. Under the default rules, your own site blesses comments from followed authors without manual review (when Rosie is on).

**Related**: follow, blessed.json

---

### frontmatter

YAML metadata section at the top of markdown posts and comments, enclosed by `---` markers. A post carries `title`, `published`, `generator`, `current-version`, `version-history` and `signature`, plus a `license` block when terms are stated. A comment adds `type: comment`, an `in-reply-to` block, and an `author` written after signing. Which fields a signature covers is specified in [`signet/spec/signing-base.md`](../../signet/spec/signing-base.md).

**Related**: canonical_url, signature, version

---

### handler

The mechanism by which a bundle processes actions. Three handler types: `builtin` (native Go code), `executable` (JSON stdin/stdout to external binary), `http` (JSON POST to URL). Declared in `bundle.json`.

**Related**: bundle, content type

---

### manifest

*(Deprecated — split between `.well-known/polis` and `.polis/bundles/registry.json`.)* Formerly `metadata/manifest.json`. The `active_theme` and `active_shape` fields now live in `.polis/bundles/registry.json` (per-tenant, private). `.well-known/polis` carries the public identity document. `post_count` and `comment_count` are derived from `content/pub.polis.core/index.jsonl` at runtime.

**Related**: theme, shape, bundle, .well-known/polis

---

### policy

An **inbound** rule: what a site accepts from others. Rules use the grammar `<verb> <type|all|none> from <all|none|following|followers|self|thread-blessed> [at <domain>] [on <target>]`, with the verb chosen by layer — `allow`/`deny`/`bless`/`review` on a site's inbound layer, `emit`/`omit` on its outbound layer (reserved), and `allow`/`deny` for a discovery-service operator. Private policies (`.polis/policies/rules.jsonl`) are evaluated before public ones (`policies/rules.jsonl`). First match wins; with no match, access is allowed and a blessing request goes to manual review. Specified in [`policy-grammar.md`](policy-grammar.md). Not to be confused with a **licence**, which is outbound.

**Related**: event, blessing, content type

---

### post

Original content published by an author to their own domain. Posts are markdown files with signed frontmatter, stored in `content/pub.polis.core/post/YYYYMMDD/`, indexed in `content/pub.polis.core/index.jsonl`, and rendered to HTML using themes.

**Related**: comment, signature, render

---

### public key

The Ed25519 public key used to verify signatures on your posts and comments. Published at `.well-known/polis` (and as `did.json`) so anyone, including the discovery service, can verify your content's authenticity. Every key the site has held is recorded in its **key history**.

**Related**: signature, .well-known/polis

---

### index.jsonl

Line-delimited JSON index (`content/pub.polis.core/index.jsonl`) listing a site's published posts, comments, tags and attestations — how a reader finds records whose names HTTP cannot list. Each line is a JSON object with type, source path, title, published date and version; the format is specified in [content-system.md](../concepts/content-system.md#the-content-index--indexjsonl). It is only as fresh as the site's last write or heal. Used to generate `index.html`.

**Related**: post, comment, bundle

---

### render

Convert markdown posts and comments to styled HTML using the active shape's templates (and any the active theme overrides). The `polis render` command processes all content, applies mustache templating, embeds blessed comments, and generates `index.html`.

**Related**: theme, snippet, template

---

### signature

An Ed25519 cryptographic signature — in frontmatter for posts and comments, as a `signature` member for signed JSON files — proving the content was signed by the site's key and hasn't been tampered with. It covers the **signing base**, not simply the file. The discovery service verifies it before registering content.

**Related**: public key, Ed25519, frontmatter

---

### snippet

A reusable template fragment (HTML or markdown) included in rendered pages via `{{> snippet-name}}` syntax. Lookup is global-first: `site/snippets/`, then the active theme's snippets, then the active shape's (`.polis/bundles/pub.polis.core/shapes/<shape>/snippets/`). `{{> theme:snippet-name}}` looks in the theme first.

**Related**: theme, template, render

---

### shape

A rendering approach declared by a bundle, comprising the templates and (optionally) client-side scripts that turn content into a user-facing surface. The core bundle ships two shapes: `pub.polis.shapes.v3` (the classic blog — per-post pages, comment threads inline) and `pub.polis.shapes.v4` (the *infinity stream* — single stream-screen, PQL-driven filtering, used on polis.pub and the webapp logged-in view). The active shape is set in `.polis/bundles/registry.json`. Themes are independent: a theme may target one or more shapes, and may override individual shape templates.

**Related**: bundle, theme, content type

---

### theme

A CSS-only presentation package scoped to a bundle and compatible with one or more shapes. Themes live at `.polis/bundles/pub.polis.core/themes/<name>/` per-tenant; the active theme is set via `active_theme` in `.polis/bundles/registry.json`. The core bundle ships: `especial`, `especial-light`, `vice`, `turbo`, `zane`, `studio13`, `studio13-nk` (user-selectable), plus `sols` (the logged-out landing theme) and `stardust`, which are reserved and cannot be chosen as a personal theme. See [themes.md](../concepts/themes.md). A theme may override individual shape templates (e.g. `post.html`) to customize markup.

**Related**: shape, snippet, bundle, render

---

### actor registry

An **operator's** signed list of the system actors it runs (`pub.polis.actor`), found through `.well-known/polis` → `actor_registry`. Each entry names what the operator **expects** an actor to do. It is not an allow-list and nothing enforces it: the operator holds its actors' keys, so what the list buys is that a deviation is observable. Managed with `polis actor`. Specified in [`signet/spec/custody.md`](../../signet/spec/custody.md).

**Related**: custody, attestation, Rosie

---

### attestation

A signed claim one party makes about someone or something else (`pub.polis.attestation`): *on this date, this issuer asserted this predicate about this subject.* One claim per file, optionally pinned to the exact version of its subject. It is never deleted; it is withdrawn by a second signed record with the predicate `pub.polis.attestation.withdrawal`. Managed with `polis attest`. Specified in [`signet/spec/attestation.md`](../../signet/spec/attestation.md).

**Related**: signature, withdrawal, tag

---

### custody

An operator holding a user's key — as polis.pub does for hosted tenants — declared openly in the operator's records. Custody is not revocable and has no principal who could refuse; that is what distinguishes it from a **grant**. Specified in [`signet/spec/custody.md`](../../signet/spec/custody.md).

**Related**: actor registry, grant

---

### did.json

A site's identity key published as a W3C `did:web` document at `.well-known/did.json`, alongside the same key in `.well-known/polis`. Retired keys stay listed for verification but can no longer sign. Specified in [`signet/spec/did-web.md`](../../signet/spec/did-web.md).

**Related**: public key, key history

---

### grant

A user's signed record (an attestation with predicate `pub.polis.attestation.grant`) of what a **user agent** may do for them. Revocable, and scoped to named behaviours. Every act the agent performs carries a marker naming the agent and the grant, inside the signature. Specified in [`signet/spec/delegation.md`](../../signet/spec/delegation.md).

**Related**: Rosie, custody, attestation

---

### key history

A site's own record of every identity key it has held (`public_key_history` in `.well-known/polis`). Each rotation is signed by the key it replaces, so anyone can walk the chain back to the first key and resolve an old signature to the key that made it, without asking a discovery service. Append-only. Specified in [`signet/spec/key-history.md`](../../signet/spec/key-history.md).

**Related**: public key, did.json, rotation

---

### licence

A site's signed, **outbound** statement of the terms its author's work may be used under (`pub.polis.license`), found through `.well-known/polis` → `license`. Each published work also carries the terms in force when it was published, inside its signature; a newer licence never changes an older work's terms. Set with `polis license`. Not to be confused with a **policy**, which is inbound. Specified in [`signet/spec/license.md`](../../signet/spec/license.md).

**Related**: policy, frontmatter

---

### Rosie

The user's agent. Under the user's own **grant**, and signing with the user's key, she decides blessing requests by the user's published policy and marks every decision as hers; on polis.pub she also keeps each tenant's content caches faithful. She has no key or site of her own. See [`signet/spec/delegation.md`](../../signet/spec/delegation.md).

**Related**: grant, blessing, policy

---

### rotation

Replacing a site's identity key (`polis rotate-key`). The old key signs the handover to the new one, and the change is appended to the **key history**; content signed before the rotation stays verifiable.

**Related**: key history, public key

---

### signing base

The exact bytes a signature covers — never simply "the file". For posts and comments, the frontmatter and body minus the fields a type leaves unsigned; for signed JSON files, a compact serialisation in a fixed field order. Specified in [`signet/spec/signing-base.md`](../../signet/spec/signing-base.md).

**Related**: signature, frontmatter

---

### witness

A discovery service's signature over what it verified — that particular bytes existed, signed by the domain's key, by a date the site did not choose. Evidence, never a requirement: an artifact with no witness verifies exactly as before. Specified in [`signet/spec/witness.md`](../../signet/spec/witness.md).

**Related**: discovery service, signature

---

### TUI

*(Deprecated — use the [webapp](../../webapp/user/user-manual.md) instead.)*

Terminal User Interface (`polis-tui`): a menu-driven, interactive dashboard for polis operations. Was replaced by the webapp in v0.46.0.

**Related**: CLI, webapp

---

### version

SHA-256 hash of content used for content-addressing and change detection, written as `current-version` (with prior values in `version-history`). Updated by `polis republish` when content changes. Previous versions are stored in `.versions/` directories alongside content.

**Related**: canonical_url, signature, frontmatter

---

### .well-known/polis

Public identity document at your domain root. Contains author details, the Ed25519 public key and its key history, site title, avatar, pointers to the bundles, licence and actor registry, and the messages key. Used by the discovery service and other readers to verify content signatures and discover site capabilities. (Active theme and active shape live in the *private* `.polis/bundles/registry.json`, not here.)

**Related**: public key, signature, bundle, canonical_url
