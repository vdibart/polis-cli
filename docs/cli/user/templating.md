# Polis Theme System

The polis theme system renders markdown posts and comments to styled HTML pages. Themes are self-contained packages that include layout templates, stylesheets, and snippets.

## Purpose

Polis stores content as signed markdown files with YAML frontmatter. While this format is ideal for:
- Cryptographic verification
- AI/LLM consumption
- Programmatic access via `public.jsonl`

It's not ideal for human readers browsing your site. The theme system bridges this gap by generating HTML files that:
- Display beautifully in browsers
- Include blessed comments inline on post pages
- Provide an index page listing all content
- Preserve the original markdown files for verification

## Quick Start

```bash
# Initialize a polis site (themes are installed automatically)
polis init

# Render all posts and comments to HTML
polis render

# Force re-render everything (ignore timestamps)
polis render --force
```

## Built-in Themes

Polis ships with six themes:

| Theme | Description |
|-------|-------------|
| **turbo** | Retro computing aesthetic with deep blue foundation |
| **zane** | Neutral dark theme with teal and salmon accents |
| **sols** | Nine Sols inspired theme with violet and peach tones |
| **vice** | GTA Vice City / Miami Vice inspired with coral and sunset tones |
| **especial** | Modelo Especial inspired dark theme with gold and cream |
| **especial-light** | Light variant of especial with warm fog background |

On first render, polis randomly selects a theme from those available. You can change the active theme at any time.

## Changing Themes

### Via the Dashboard (recommended)

Open **Settings** in the webapp dashboard. The **Theme** section shows all available themes with color palette previews. Click any theme to switch immediately — the site is re-rendered automatically.

### Via the CLI

Edit `metadata/manifest.json` and set the `active_theme` field:

```json
{
  "version": "0.27.0",
  "active_theme": "zane",
  "post_count": 5,
  "comment_count": 3
}
```

Then re-render:

```bash
polis render --force
```

The theme's CSS will be copied to `styles.css` at your site root.

## Theme Structure

Each theme is a self-contained folder in `.polis/themes/`:

```
.polis/themes/turbo/
├── index.html              # Homepage template
├── post.html               # Individual post template
├── comment.html            # Comment page template
├── comment-inline.html     # Blessed comment (rendered inside posts)
├── posts.html              # Archive page template (optional)
├── turbo.css               # Theme stylesheet
└── snippets/               # Theme-specific snippets
    ├── about.html          # About section
    ├── post-item.html      # Post list item
    ├── comment-item.html   # Comment list item
    └── blessed-comment.html # Blessed comment block
```

## Snippets

Snippets are reusable template fragments included with `{{> name}}` syntax.

### Snippet Lookup Order

When resolving `{{> path}}`:

1. **Global snippets** - `./snippets/{path}.md` or `.html` (author overrides)
2. **Theme snippets** - `.polis/themes/{active_theme}/snippets/{path}.html` or `.md`

This allows you to override theme defaults by creating snippets in `./snippets/`.

### Explicit Tier Selection

Use prefixes to control which tier is checked first:

| Prefix | Behavior |
|--------|----------|
| `{{> about}}` | Default: global first, then theme |
| `{{> global:about}}` | Explicit global-first (same as default) |
| `{{> theme:about}}` | Theme-first, then global fallback |

Example: A theme's `about.html` provides default styling, but your `snippets/about.md` with personal content takes precedence. Use `{{> theme:about}}` if you need the theme version.

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

### Default Theme Snippets

Each theme includes these snippets:

| File | Purpose |
|------|---------|
| `about.html` | About section (displayed on homepage) |
| `post-item.html` | Post list item (used in `{{#posts}}` loops) |
| `comment-item.html` | Comment list item (used in `{{#comments}}` loops) |
| `blessed-comment.html` | Blessed comment (used in `{{#blessed_comments}}` loops) |

### Global Snippets

Create snippets in `./snippets/` to override theme defaults or add custom content:

```bash
# Override the about section (will take precedence over theme)
echo '# About Me' > snippets/about.md

# Create a custom snippet
mkdir -p snippets/widgets
echo '<div class="newsletter">Subscribe!</div>' > snippets/widgets/newsletter.html
```

Reference in templates:
```html
{{> about}}                  <!-- Uses your global override -->
{{> theme:about}}            <!-- Forces theme version -->
{{> widgets/newsletter}}     <!-- Uses your custom snippet -->
```

## How Rendering Works

### Rendering Process

1. **Select theme** - On first render, randomly selects from available themes
2. **Load templates** - Reads HTML templates from active theme
3. **Copy CSS** - Copies theme stylesheet to `styles.css`
4. **Scan content** - Finds all `.md` files in `posts/` and `comments/`
5. **Check timestamps** - Skips files where `.html` is newer than `.md` (unless `--force`)
6. **Extract frontmatter** - Reads title, published date, signature, etc.
7. **Convert to HTML** - Uses pandoc to render markdown body
8. **Apply template** - Substitutes variables and renders snippets
9. **Write output** - Creates `.html` file alongside `.md` file
10. **Generate index** - Creates `index.html` from `public.jsonl`

### File Relationships

```
posts/
└── 2026/
    └── 01/
        ├── my-post.md           # Source (signed markdown)
        └── my-post.html         # Generated (rendered HTML)

comments/
└── 2026/
    └── 01/
        ├── reply.md             # Source (signed markdown)
        └── reply.html           # Generated (rendered HTML)

index.html                       # Generated (listing page)
posts/
└── index.html                   # Generated (archive page, all posts)
styles.css                       # Copied from active theme
```

The `.md` files remain the source of truth. HTML files are regenerated from them.

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

## Creating Custom Themes

### Copy an Existing Theme

```bash
# Copy turbo as a starting point
cp -r .polis/themes/turbo .polis/themes/mytheme

# Rename the CSS file
mv .polis/themes/mytheme/turbo.css .polis/themes/mytheme/mytheme.css

# Edit the templates and CSS
vim .polis/themes/mytheme/index.html
vim .polis/themes/mytheme/mytheme.css

# Activate your theme
# Edit manifest.json: "active_theme": "mytheme"

# Render
polis render --force
```

### Theme Requirements

A valid theme must contain:

| File | Required | Purpose |
|------|----------|---------|
| `index.html` | Yes | Homepage template |
| `post.html` | Yes | Post page template |
| `comment.html` | Yes | Comment page template |
| `comment-inline.html` | Yes | Blessed comment template |
| `posts.html` | Optional | Archive page template (all posts) |
| `{themename}.css` | Yes | Theme stylesheet |
| `snippets/` | Optional | Theme-specific snippets |

### Template Documentation

Each theme template includes a comment header documenting which snippets it loads:

```html
<!--
    Polis Theme: Turbo - Homepage Template

    Snippets loaded by this template:
    - about          - About section (theme: snippets/about.html, global: snippets/about.md)
    - post-item      - Post list item (theme: snippets/post-item.html)
    - comment-item   - Comment list item (theme: snippets/comment-item.html)

    Snippet lookup order: theme snippets -> global snippets
-->
```

## Configuration

### THEMES_DIR

The themes directory can be configured:

| Method | Example |
|--------|---------|
| Environment | `THEMES_DIR=custom/themes polis render` |
| .env file | `THEMES_DIR=custom/themes` |
| .well-known/polis | `{"config": {"directories": {"themes": "custom/themes"}}}` |

Default: `.polis/themes`

### Site Information

The theme system reads site information from `metadata/manifest.json`:

```json
{
  "version": "0.42.0",
  "active_theme": "turbo",
  "last_published": "2026-01-14T00:00:00Z",
  "post_count": 5,
  "comment_count": 3
}
```

Note: `site_title` is stored in `.well-known/polis`, not in `manifest.json`.

Set the site title during initialization: `polis init --site-title "My Site"`

## Important Files and Paths

| Path | Purpose |
|------|---------|
| `.polis/themes/` | Installed themes |
| `snippets/` | Global snippets (override theme snippets) |
| `styles.css` | Active theme's stylesheet (copied on render) |
| `index.html` | Generated listing page |
| `posts/index.html` | Generated archive page (all posts) |
| `posts/**/*.html` | Generated post pages |
| `comments/**/*.html` | Generated comment pages |
| `metadata/manifest.json` | Site metadata including `active_theme` |
| `metadata/public.jsonl` | Content index (used for index.html) |
| `metadata/blessed-comments.json` | Blessed comments (rendered inline) |

## Troubleshooting

### "pandoc is required for rendering"

Install pandoc:

```bash
# Linux
sudo apt install pandoc

# macOS
brew install pandoc
```

### "Polis not initialized"

Run `polis init` first to create the required directory structure.

### "Theme 'xyz' not found"

Check that the theme exists in `.polis/themes/xyz/`. Available themes can be listed:

```bash
ls .polis/themes/
```

### HTML files not updating

The render command skips files where the `.html` is newer than the `.md`. Use `--force` to re-render:

```bash
polis render --force
```

### Template variables not substituting

Check that:
1. Variables use double curly braces: `{{variable}}`
2. Variable names are spelled correctly (case-sensitive)
3. Required data exists (e.g., `POLIS_BASE_URL` for `{{site_url}}`)

### Blessed comments not appearing

Verify that:
1. `metadata/blessed-comments.json` exists and contains entries
2. The post URL in the JSON matches the post being rendered
3. Comment files are accessible (local or via HTTP)

Run `polis blessing sync` to update blessed comments from the discovery service.

### Blessed comments showing stale content

Remote blessed comments are fetched when a post is rendered, but caching may show outdated content if:
- The comment author updated their comment after you last rendered
- Your post's HTML is already up-to-date based on local file timestamps

Use `polis render --force` to re-fetch all remote blessed comments.

### Styling not applied

1. Check that `styles.css` exists at your site root
2. Verify the theme's CSS file exists in `.polis/themes/{theme}/{theme}.css`
3. Re-render to copy the CSS: `polis render --force`

### Index page empty

The index is generated from `metadata/public.jsonl`. Ensure:

1. Posts are published (have frontmatter with `version` field)
2. Run `polis rebuild --content` to regenerate the index

## Dependencies

| Tool | Required | Purpose |
|------|----------|---------|
| pandoc | Yes | Markdown to HTML conversion |
| jq | Yes | JSON parsing |
| curl | For remote comments | Fetching blessed comments |

## Theme Developer's Guide

This section covers advanced topics for creating polished, consistent themes.

### CSS Variable Conventions

The built-in themes follow a consistent CSS variable naming pattern:

```css
:root {
    /* Background layers (dark to light) */
    --color-bg: #000818;         /* Page background */
    --color-bg-light: #001030;   /* Slightly lighter background */
    --color-surface: #001448;    /* Card/panel backgrounds */
    --color-panel: #001a58;      /* Elevated elements */

    /* Primary accent (Polis cyan - keep consistent for branding) */
    --color-cyan: #00d4ff;
    --color-cyan-soft: #80e4ff;
    --color-cyan-dim: #0090b0;
    --color-cyan-glow: rgba(0, 212, 255, 0.3);

    /* Text hierarchy */
    --color-text: #ffffff;       /* Primary text */
    --color-text-soft: #a0c0d8;  /* Secondary text */
    --color-text-muted: #607890; /* Tertiary/meta text */

    /* Borders */
    --color-border: rgba(0, 212, 255, 0.2);
    --color-border-light: rgba(0, 212, 255, 0.35);

    /* Typography */
    --font-mono: 'JetBrains Mono', 'Fira Code', 'SF Mono', Consolas, monospace;
    --font-display: 'Orbitron', var(--font-mono);

    /* Layout */
    --max-width: 600px;
}
```

### Required CSS Classes

Themes should style these key classes:

| Class | Used In | Purpose |
|-------|---------|---------|
| `.hero` | index, post, comment | Page header section |
| `.hero-title` | all | Main page title |
| `.hero-subtitle` | post, comment | Date/metadata line |
| `.about` | index | About section wrapper |
| `.about-content` | index | About section content box |
| `.recent-posts` | index | Posts listing section |
| `.recent-comments` | index | Comments listing section |
| `.section-title` | index | Section headers |
| `.post-list` | index | Post list container |
| `.post-item` | index (loop) | Individual post entry |
| `.post-date` | index | Post date in list |
| `.post-title` | index | Post title in list |
| `.post-comments` | index | Comment count in list |
| `.comment-list` | index | Comment list container |
| `.comment-item` | index (loop) | Individual comment entry |
| `.comment-meta` | index | Comment metadata row |
| `.comment-author` | all | Comment author name |
| `.comment-date` | all | Comment date |
| `.comment-preview` | index | Comment preview text |
| `.post-content` | post, comment | Main content wrapper |
| `.content-body` | post, comment | Content box with styling |
| `.reply-context` | comment | "In reply to" section |
| `.context-box` | comment | Reply context container |
| `.context-label` | comment | "In reply to:" label |
| `.context-link` | comment | Link to parent content |
| `.comments` | post | Blessed comments section |
| `.comments-title` | post | "Comments (N)" header |
| `.comments-list` | post | Comment list container |
| `.comment` | post (inline) | Individual blessed comment |
| `.comment-header` | post (inline) | Comment header row |
| `.comment-body` | post (inline) | Comment content |
| `.site-footer` | all | Page footer |
| `.footer-logo` | all | Polis branding link |
| `.footer-tagline` | all | Tagline text |
| `.view-all` | index | "View all N posts" link |
| `.post-meta` | post, comment | Signature/metadata block |
| `.meta-label` | post, comment | "Signed by" etc. |
| `.meta-value` | post, comment | Author name etc. |
| `.meta-sep` | post, comment | Separator between meta items |

### Responsive Design

Include mobile breakpoints. The built-in themes use 600px:

```css
@media (max-width: 600px) {
    .hero-title {
        font-size: 1.25rem;
    }

    .footer-logo {
        font-size: 1.4rem;
    }
}
```

### Template HTML Structure

Templates should follow this general structure:

```html
<!--
    Polis Theme: [Name] - [Template Type]

    Snippets loaded by this template:
    - snippet-name  - Description

    Snippet lookup order: theme snippets -> global snippets
-->
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{title}} - {{site_title}}</title>
    <meta name="description" content="...">
    <link rel="stylesheet" href="{{css_path}}">
    <!-- Optional: Google Fonts for display font -->
</head>
<body>
    <!-- Hero section -->
    <section class="hero">...</section>

    <!-- Main content -->
    <article class="post-content">...</article>

    <!-- Comments (for posts) -->
    <section class="comments">...</section>

    <!-- Footer -->
    <footer class="site-footer">
        <a href="https://polis.pub" class="footer-logo">POLIS
            <span class="footer-tagline">Your content, free from platform control</span>
        </a>
    </footer>

<!-- Hidden metadata comment -->
<!-- Source: {{url}} | Version: {{version}} -->
</body>
</html>
```

### Snippet Best Practices

1. **Keep snippets focused** - One purpose per snippet
2. **Use semantic HTML** - `<article>`, `<section>`, `<time>` where appropriate
3. **Support all loop variables** - Don't assume which variables will be present
4. **Test with empty data** - Ensure graceful handling of missing comments/posts

Example well-structured snippet:

```html
<!-- snippets/post-item.html -->
<a href="{{url}}" class="post-item">
    <span class="post-date">{{published_human}}</span>
    <span class="post-title">{{title}}</span>
    <span class="post-comments">{{comment_count}} comments</span>
</a>
```

### Testing Your Theme

1. Create test content with various lengths
2. Test with 0, 1, and many posts/comments
3. Test mobile and desktop viewports
4. Verify all links work (CSS, internal navigation)
5. Check that blessed comments render correctly

```bash
# Quick test workflow
echo "active_theme: mytheme" | ... # (edit manifest.json)
polis render --force
python -m http.server 8000  # Preview at localhost:8000
```

### Polis Branding

We'd appreciate it if you keep the cyan color (`#00d4ff`) for the footer POLIS logo across custom themes. This helps with consistent branding while you're free to use theme-specific accent colors elsewhere.

## Migration from Templates

If you have a custom `.polis/templates/` directory from a previous version:

1. Create a new theme: `mkdir -p .polis/themes/custom/snippets`
2. Move templates: `mv .polis/templates/*.html .polis/themes/custom/`
3. Create CSS: `touch .polis/themes/custom/custom.css`
4. Move relevant snippets to `snippets/` (global) or `.polis/themes/custom/snippets/` (theme-specific)
5. Set active theme in `manifest.json`: `"active_theme": "custom"`
6. Remove old directory: `rm -r .polis/templates`
7. Render: `polis render --force`
