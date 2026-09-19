# Templating

*For* [Writers](../../README.md#writing-on-polis) · [Developers](../../README.md#building-on-polis) — *Kind* [Reference](../../README.md#kinds-of-page) — *Component* [CLI](../README.md) — *Code* [`cli-go/pkg/template`](../../../cli-go/pkg/template) — *See also* [concept](../../general/concepts/themes.md)

Themes and shapes now ship inside bundles: a [shape](../../general/concepts/shapes.md) provides a site's templates and a [theme](../../general/concepts/themes.md) provides its CSS. Those two pages describe how they are installed, chosen and customised. This page covers the template language itself: the Mustache-like syntax, the variables templates can use, and how snippets are resolved.

The earlier version of this page also described the theme system as it worked before bundles; that material has been retired. The last full version is at [v0.66.0](https://github.com/vdibart/polis-cli/blob/v0.66.0/docs/cli/user/templating.md).

## Snippets

Snippets are reusable template fragments included with `{{> name}}` syntax.

### Snippet Lookup Order

When resolving `{{> path}}`:

1. **Global snippets** - `site/snippets/{path}` (author overrides)
2. **Theme snippets** - `site/themes/{active_theme}/snippets/{path}`, if that directory exists
3. **Shape snippets** - `.polis/bundles/pub.polis.core/shapes/{active_shape}/snippets/{path}`
4. **Shape root** - `.polis/bundles/pub.polis.core/shapes/{active_shape}/{path}` (entry templates such as `stream-post.html`)

Each tier tries `{path}.md`, then `{path}.html`, then `{path}` exactly. This allows you to override
the defaults by creating snippets in `site/snippets/`.

⚠️ The installed bundle themes under `.polis/bundles/pub.polis.core/themes/` are CSS-only and carry no
snippets, so tier 2 applies only to a theme you add under `site/themes/`.

> **Migration note:** Pre-bundle-refactor sites used `.polis/themes/<name>/snippets/`
> and `.polis/themes/_base/snippets/`. Patrol/Medic (hosted) and Tailor
> (self-hosted) migrate these locations into the bundle layout above.

### Explicit Tier Selection

Use prefixes to control which tier is checked first:

| Prefix | Behavior |
|--------|----------|
| `{{> about}}` | Default: global, then theme, then shape |
| `{{> global:about}}` | Explicit global-first (same as default) |
| `{{> theme:about}}` | Theme, then shape, then global last |

Example: A theme's `about.html` provides default styling, but your `site/snippets/about.md` with personal content takes precedence. Use `{{> theme:about}}` if you need the theme version.

### Explicit File Extensions

By default, extension resolution tries `.md` → `.html` → exact match. Use explicit extensions to load a specific file:

| Syntax | Behavior |
|--------|----------|
| `{{> about}}` | Tries about.md, about.html, about |
| `{{> about.md}}` | Loads about.md only, no fallback |
| `{{> about.html}}` | Loads about.html only, no fallback |

### Combined Syntax

Prefixes and extensions can be combined:

```html
{{> theme:about.html}}   <!-- Theme's HTML version specifically -->
{{> global:about.md}}    <!-- Global's markdown version specifically -->
```

### Default Shape Snippets

The blog shape (`pub.polis.shapes.v3`) ships these snippets; the stream shape (`pub.polis.shapes.v4`)
ships `blessed-comment.html`, `sentence-filter.html` and `stream-filter.html`:

| File | Purpose |
|------|---------|
| `about.html` | About section (displayed on homepage) |
| `post-item.html` | Post list item (used in `{{#posts}}` loops) |
| `comment-item.html` | Comment list item (used in `{{#comments}}` loops) |
| `blessed-comment.html` | Blessed comment (used in `{{#blessed_comments}}` loops) |
| `also-reading.html` | "Also reading" — the sites this author follows (a `{{#following}}` loop) |
| `polis-widget.html` | The comment/follow widget mount |

### Global Snippets

Create snippets in `site/snippets/` to override the defaults or add custom content:

```bash
# Override the about section (will take precedence over theme)
echo '# About Me' > site/snippets/about.md

# Create a custom snippet
mkdir -p site/snippets/widgets
echo '<div class="newsletter">Subscribe!</div>' > site/snippets/widgets/newsletter.html
```

Reference in templates:
```html
{{> about}}                  <!-- Uses your global override -->
{{> theme:about}}            <!-- Forces theme version -->
{{> widgets/newsletter}}     <!-- Uses your custom snippet -->
```

## Mustache Syntax

Polis uses a Mustache-inspired templating syntax.

### Variable Substitution

```html
{{title}}           <!-- Simple variable -->
{{site_url}}        <!-- Site-level variable -->
{{published_human}} <!-- Formatted date -->
```

### Snippet Includes

Use `{{> path}}` to include snippets:

```html
{{> about}}              <!-- Includes about section -->
{{> post-item}}          <!-- Includes post list item -->
{{> widgets/newsletter}} <!-- Includes custom snippet -->
```

Snippets can include other snippets (up to 10 levels deep).

### Loops

Use `{{#section}}...{{/section}}` for loops:

```html
<!-- Loop over all posts (unlimited, use in archive pages) -->
{{#posts}}
    {{> post-item}}
{{/posts}}

<!-- Loop over recent posts (limited to 10, use on homepage) -->
{{#recent_posts}}
    {{> post-item}}
{{/recent_posts}}

<!-- Loop over all comments (unlimited) -->
{{#comments}}
    {{> comment-item}}
{{/comments}}

<!-- Loop over recent comments (limited to 10, use on homepage) -->
{{#recent_comments}}
    {{> comment-item}}
{{/recent_comments}}

<!-- Loop over blessed comments (on post pages) -->
{{#blessed_comments}}
    {{> blessed-comment}}
{{/blessed_comments}}
```

The `{{#recent_posts}}` and `{{#recent_comments}}` sections display the 10 most recent items. Use `{{#posts}}` and `{{#comments}}` (unlimited) in archive templates.

### Loop Variables

**Inside `{{#posts}}` and `{{#recent_posts}}` loops:**

| Variable | Description |
|----------|-------------|
| `{{url}}` | Link to HTML file |
| `{{title}}` | Post title |
| `{{excerpt}}` | First ~200 chars of post body (plain text) |
| `{{published}}` | ISO date |
| `{{published_human}}` | Human-readable date |
| `{{comment_count}}` | Number of blessed comments |
| `{{comment_count_display}}` | The same count, or empty when it is zero |

**Inside `{{#comments}}` and `{{#recent_comments}}` loops:**

| Variable | Description |
|----------|-------------|
| `{{url}}` | Link to HTML file |
| `{{target_author}}` | Domain of post being replied to |
| `{{published}}` | ISO date |
| `{{published_human}}` | Human-readable date |
| `{{preview}}` | First ~100 chars of body |

**Inside `{{#blessed_comments}}` loops:**

| Variable | Description |
|----------|-------------|
| `{{url}}` | Comment URL |
| `{{author_name}}` | Comment author |
| `{{author_domain}}` | Comment author's domain |
| `{{author_avatar_html}}` | Pre-rendered avatar markup |
| `{{published}}` | ISO date |
| `{{published_human}}` | Human-readable date |
| `{{content}}` | Comment body |

## Template Variables

### Available in All Templates

| Variable | Description | Example |
|----------|-------------|---------|
| `{{site_url}}` | Base URL from `POLIS_BASE_URL` | `https://example.com` |
| `{{site_title}}` | From `.well-known/polis` or domain fallback | `My Polis Site` |
| `{{year}}` | Current year (for copyright) | `2026` |

### Post and Comment Templates

| Variable | Description | Example |
|----------|-------------|---------|
| `{{title}}` | Post/comment title | `Why I Left Substack` |
| `{{content}}` | HTML-rendered markdown body | `<p>The story begins...</p>` |
| `{{published}}` | Publication date (ISO 8601) | `2026-01-08T12:00:00Z` |
| `{{published_human}}` | Human-readable date | `January 8, 2026` |
| `{{url}}` | Canonical URL | `https://example.com/posts/2026/01/post.md` |
| `{{version}}` | Content hash | `sha256:abc123...` |
| `{{author_name}}` | From `.well-known/polis` | `Alice Smith` |
| `{{author_url}}` | Site base URL | `https://example.com` |
| `{{signature_short}}` | Truncated signature (16 chars) | `AAAAC3NzaC1lZD...` |
| `{{css_path}}` | Relative path to styles.css | `../../styles.css` |

### Post-Specific Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `{{blessed_count}}` | Number of blessed comments | `3` |

### Comment-Specific Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `{{in_reply_to_url}}` | Parent post/comment URL | `https://bob.com/posts/original.md` |

### Index Template Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `{{post_count}}` | Number of posts | `12` |
| `{{comment_count}}` | Number of comments | `5` |
