# Polis CLI - Complete Usage Guide

> **Note:** This is the Bash CLI command reference. The Bash CLI is feature-frozen (release version tracks the Go CLI in `cli-go/version.txt`). The **Go CLI** has identical commands, ships as a single binary with no external dependencies, and is the recommended choice for new users. For the webapp (local web interface), see [../../webapp/user/user-manual.md](../../webapp/user/user-manual.md).

Command-line tool for managing decentralized social content with cryptographic signing and version control.

## Overview

The Polis CLI enables authors to:
- Create and sign posts and comments using Ed25519 cryptography
- Manage content with git-based version history
- Publish content as static files with frontmatter metadata
- Render markdown to static HTML with customizable templates
- Request blessings from discovery services for authenticated comment discovery
- Automate workflows with JSON mode for scripting and CI/CD integration

## Go CLI

The **Go CLI** is the recommended CLI for new users. It implements all commands listed below, ships as a single binary, and has **no external dependencies** (no jq, curl, OpenSSH, or pandoc required).

- Primary command is `post` (the `publish` alias also works)
- Same `--json` flag for machine-readable output
- Same directory structure and file formats
- Download pre-built binaries from the releases page, or build from source: `cd cli-go && go build -o polis ./cmd/polis`

The command reference below applies to both CLIs — the commands, flags, and behavior are identical.

## Installation (Bash CLI)

> The sections below are specific to the **Bash CLI**. If you're using the Go CLI, you only need the single `polis` binary.

### Prerequisites

- **OpenSSH 8.0+** (for Ed25519 signing with `-Y` flag)
- **jq** (JSON processor for index management and JSON mode)
- **curl** (for API communication with discovery service)
- **sha256sum** or **shasum** (for content hashing)
- **git** (optional, for version control integration)
- **pandoc** (optional, required only for `polis render` command)

### Install Dependencies

```bash
# Linux (Debian/Ubuntu)
sudo apt-get install openssh-client jq curl coreutils git
sudo apt-get install pandoc  # Optional: for polis render

# macOS
brew install openssh jq curl coreutils git
brew install pandoc  # Optional: for polis render
```

### Install Polis CLI

```bash
# Option 1: Add to PATH (quick start)
export PATH="/path/to/polis-cli/bin:$PATH"

# Option 2: Create symlink
sudo ln -s /path/to/polis-cli/bin/polis /usr/local/bin/polis

# Verify installation
polis --help
```

### Option 3: Copy to your content repository

For production use, copy the CLI tools directly into your content repository. This keeps your site self-contained—CLI versions are locked to your content, and everything deploys together.

```bash
# Clone polis-cli
git clone https://github.com/vdibart/polis-cli.git

# Create your content repository
mkdir my-site && cd my-site
git init

# Copy CLI tools and themes
cp ../polis-cli/bin/polis ./bin/
cp ../polis-cli/bin/polis-upgrade ./bin/
cp -r ../polis-cli/themes ./themes/

# Initialize your site
./bin/polis init

# Add bin/ to .gitignore if you don't want to version the CLI
# Or commit it to lock the CLI version with your content
```

**What to copy:**
- `bin/polis` — Main CLI
- `bin/polis-upgrade` — Upgrade script (optional)
- `themes/` — Theme templates (required for `polis init` and `polis render`)

**Why this approach:**
- Your site is fully self-contained
- CLI version is locked to your content (no surprise breakage from updates)
- Works offline after initial setup
- Easy to deploy—just push your repo

## Quick Start

For a quick introduction, see the [README](../README.md). This guide covers detailed usage.

## Directory Structure

> For a more detailed file structure reference including webapp-specific files, see [WEBAPP-USER-MANUAL.md §File Structure](WEBAPP-USER-MANUAL.md#file-structure).

After running `polis init`, your directory will contain:

```
.
├── .polis/
│   ├── keys/
│   │   ├── id_ed25519       # Private signing key (keep secret!)
│   │   └── id_ed25519.pub   # Public verification key
│   └── themes/              # Installed themes
│       ├── turbo/           # Retro computing theme
│       │   ├── index.html
│       │   ├── post.html
│       │   ├── comment.html
│       │   ├── comment-inline.html
│       │   ├── turbo.css
│       │   └── snippets/
│       ├── zane/            # Neutral dark theme
│       └── sols/            # Nine Sols inspired theme
├── metadata/                 # Metadata files
│   ├── public.jsonl         # Content index (JSONL format)
│   ├── blessed-comments.json # Index of approved comments
│   ├── manifest.json        # Site metadata (active_theme)
│   └── following.json       # Following list
├── posts/                    # Your posts
│   └── 20260106/            # Date-stamped directory (YYYYMMDD)
│       ├── .versions/       # Version history for posts in this directory
│       │   └── my-post.md
│       ├── my-post.md
│       └── my-post.html     # Generated by polis render
├── comments/                 # Your comments
│   └── 20260106/            # Date-stamped directory (YYYYMMDD)
│       ├── .versions/       # Version history for comments in this directory
│       │   └── reply.md
│       ├── reply.md
│       └── reply.html       # Generated by polis render
├── snippets/                 # Global snippets (override theme snippets)
│   └── about.md             # Custom about section
├── styles.css               # Active theme's stylesheet (copied on render)
├── index.html               # Site index (generated by polis render)
└── .well-known/
    └── polis                # Public metadata (author, public key, site_title)
```

## Commands

### `polis init`

Initialize a new Polis directory with keys and metadata.

```bash
polis init
polis init --site-title "My Awesome Blog"
polis init --site-title "My Blog" --register
```

**Options:**
- `--site-title <title>` - Set a custom site title for branding (optional)
- `--register` - Auto-register with discovery service after init (requires `POLIS_BASE_URL` and discovery service credentials)
- `--posts-dir <dir>` - Custom posts directory (default: `posts`)
- `--comments-dir <dir>` - Custom comments directory (default: `comments`)
- `--keys-dir <dir>` - Custom keys directory (default: `.polis/keys`)
- `--themes-dir <dir>` - Custom themes directory (default: `.polis/themes`)

**Creates:**
- `.polis/keys/` - Ed25519 keypair for signing
- `.polis/themes/` - Installed themes (turbo, zane, sols)
- `posts/`, `comments/` - Content directories
- `metadata/` - Metadata directory
- `.well-known/polis` - Public metadata file (includes a default avatar)
- `metadata/public.jsonl` - Content index (JSONL format)
- `metadata/blessed-comments.json` - Blessed comments index
- `metadata/following.json` - Following list
- `metadata/manifest.json` - Site metadata (includes `active_theme` if set)

**Default Avatar:**
A default avatar is automatically generated during init with a random background color and white foreground. The avatar displays as a circle with the first letter of your handle. You can customize or remove it later via the webapp settings page (Randomize/Save/Reset buttons).

### `polis post <file>`

Sign and publish a post or comment with frontmatter metadata.

```bash
polis post posts/my-post.md
polis post comments/my-comment.md
```

**What it does:**
1. Generates SHA-256 hash of content
2. Signs content with Ed25519 private key
3. Adds frontmatter with metadata (version, author, signature)
4. Appends entry to `public.jsonl` index
5. Creates `.versions` file for version history

**Example output:**
```
[i] Content hash: sha256:a3b5c7d9...
[i] Signing content with Ed25519 key...
[✓] Created canonical file: posts/20260106/my-post.md
```

### `polis republish <file>`

Republish an existing file with updated content (creates new version).

```bash
# Edit your published file
vim posts/20260106/my-post.md

# Republish with new version
polis republish posts/20260106/my-post.md
```

**What it does:**
1. Generates diff between old and new content
2. Appends diff to `.versions` file
3. Updates frontmatter with new version hash
4. Re-signs content with new signature
5. Rebuilds `public.jsonl` index (prevents duplicates)

**Version history:**
- Stored in `.versions/` subdirectory alongside content files
- Example: `posts/20260106/my-post.md` → `posts/20260106/.versions/my-post.md`
- Directory name configurable via `POLIS_VERSIONS_DIR_NAME` (default: `.versions`)
- Uses unified diff format (compatible with `diff`/`patch` tools)
- Enables version reconstruction

### Snippets

Snippets are reusable content fragments for templates. Unlike posts and comments, snippets don't require signing - just place plain `.md` or `.html` files in the `snippets/` directory.

```bash
# Create a snippet - just write a file
echo "Hi, I'm Vincent." > snippets/about.md

# Use nested directories for organization
mkdir -p snippets/homepage
echo "<footer>© 2026</footer>" > snippets/homepage/footer.html
```

**Snippets vs Themes:**
- **Snippets** are content fragments stored in `snippets/` (global) or theme directories
- **Themes** are complete styling packages stored in `.polis/themes/`
- Snippets can be included in templates using `{{> path/to/snippet}}`
- Snippet lookup order: **global snippets first, then theme snippets** (author overrides theme)

**Snippet syntax:**
- `{{> about}}` - Default lookup (global → theme), tries .md then .html
- `{{> theme:about}}` - Force theme-first lookup
- `{{> global:about}}` - Explicit global-first lookup
- `{{> about.md}}` or `{{> about.html}}` - Load specific file extension

**File formats:**
- `.md` files are processed through pandoc (markdown → HTML)
- `.html` files are used as-is

**Example snippet (`snippets/about.md`):**
```markdown
Hi, I'm Vincent. I build things with code and think deeply about
how technology shapes human connection.
```

**Note:** Snippets don't need frontmatter - just the content. If you have existing snippets with frontmatter from older versions, they'll continue to work (frontmatter is stripped during render).

### Publishing from stdin

You can pipe content directly to `polis post` and `polis comment` without creating temporary files:

```bash
# Basic usage
echo "# My Post" | polis post -

# Specify filename
echo "# My Post" | polis post - --filename my-post.md

# Specify title
echo "Content without heading" | polis post - --title "My Title"

# Both options
echo "Content" | polis post - --filename post.md --title "My Title"

# From file redirect
polis post - < draft.md

# From curl
curl -s https://example.com/draft.md | polis post -

# From heredoc
polis post - << 'EOF'
# My Post
Content here
EOF

# Piping with preprocessing
grep -v "^Draft:" draft.md | polis post - --filename final.md
```

**Options:**
- `--filename <name>` - Specify output filename (default: `stdin-TIMESTAMP.md`)
- `--title <title>` - Override title extraction

**For comments:**
```bash
# Comment from stdin
echo "# My reply" | polis comment - https://bob.com/posts/original.md

# With filename
echo "# Reply" | polis comment - https://bob.com/post.md --filename my-reply.md

# With title override
echo "Content" | polis comment - https://bob.com/post.md --title "My Reply"
```

**JSON mode:**
```bash
echo "# Test" | polis --json post - --filename test.md | jq
```

### `polis comment <url> [file]`

Create a comment in reply to a post or another comment (nested threads).

```bash
# Reply to a post
polis comment https://alice.example.com/posts/20260106/hello.md

# Reply to another comment (nested thread)
polis comment https://bob.example.com/comments/20260105/reply.md my-reply.md

# From file
polis comment https://bob.example.com/posts/intro.md my-reply.md
```

**What it does:**
1. Creates comment file in `comments/YYYYMMDD/`
2. Adds `in_reply_to` frontmatter (with `url` and `root-post` fields)
3. Signs and publishes comment
4. Appends entry to `public.jsonl` index
5. Automatically requests blessing from discovery service

**Nested threads:** When replying to a comment (instead of a post), the CLI automatically detects this and fetches the original post URL (`root-post`) from the discovery service.

**Frontmatter structure:**
```yaml
in-reply-to:
  url: https://bob.com/comments/reply.md  # Immediate parent (post or comment)
  root-post: https://alice.com/posts/intro.md  # Always the original post
```

### `polis preview <url>`

Preview content at a URL (posts or comments) with signature verification.

```bash
# Preview a post
polis preview https://alice.com/posts/20260105/hello.md

# Preview a comment before blessing it
polis preview https://bob.com/comments/20260105/reply.md

# JSON mode for scripting
polis --json preview https://alice.com/posts/hello.md
```

**What it shows:**
- Frontmatter metadata (displayed dimmed/greyed)
- Signature verification status (valid/invalid/missing)
- Content hash verification (valid/mismatch)
- For comments: the in-reply-to URL
- The content body

**Use cases:**
- Preview a comment before blessing it
- Preview content before responding to it
- Verify content integrity and authenticity

**JSON mode:** See [json-mode.md](json-mode.md) for response format.

### `polis rebuild`

Rebuild local indexes and reset state. Automatically regenerates `manifest.json` after any rebuild.

```bash
# Rebuild posts/comments index
polis rebuild --posts

# Rebuild blessed comments index
polis rebuild --comments

# Reset notification files
polis rebuild --notifications

# Rebuild everything
polis rebuild --all
```

**Flags (combinable):**
- `--posts` - Rebuild `public.jsonl` from posts and comments on disk
- `--comments` - Rebuild `blessed-comments.json` from discovery service
- `--notifications` - Reset notification files (`.polis/notifications.jsonl`, `.polis/notifications-manifest.json`)
- `--all` - All of the above

**Note:** `manifest.json` is automatically regenerated after any rebuild operation.

**Use when:**
- Index is corrupted or out of sync
- You manually edited published files
- You restored from backup
- Notification state is corrupted
- CLI version was upgraded (clears "metadata files need update" warning)

### `polis index [--json]`

View the content index in JSONL or JSON format (read-only, outputs to stdout).

```bash
# View as JSONL (default)
polis index

# View as JSON (grouped by type)
polis index --json

# Pipe to jq for pretty printing
polis index --json | jq

# Count posts
polis index | grep -c '"type":"post"'

# View recent 10 entries
polis index | tail -10 | jq

# Extract all post titles
polis index --json | jq -r '.posts[].title'
```

**What it does:**
- Reads `metadata/public.jsonl`
- Default: outputs raw JSONL (one entry per line)
- `--json` flag: converts to grouped JSON format for readability
- Read-only operation - never modifies the index file

**Use cases:**
- Debugging index contents
- Scripting and automation
- Inspecting published content metadata

### `polis extract <file> <version-hash>`

Reconstruct a specific version of a file from version history.

```bash
# Get specific version
polis extract posts/20260106/my-post.md sha256:abc123...

# Output to file
polis extract posts/20260106/my-post.md sha256:abc123... > old-version.md
```

### Starting Fresh

To completely reset your Polis installation and start over, move or remove the following files/directories, then run `polis init`:

- `.polis/` - Configuration and signing keys
- `.well-known/` - Public metadata
- `posts/` - Published posts
- `comments/` - Published comments
- `metadata/` - Index and blessing data

**Example:**
```bash
rm -rf .polis .well-known posts comments metadata
polis init
```

**Note:** This will generate new signing keys. If you want to keep your identity, back up `.polis/keys/` before removing.

### `polis migrate <new-domain>`

Migrate all content to a new domain. This command handles the complete migration process including re-signing files and updating the discovery service database.

```bash
polis migrate newdomain.com
```

**What it does:**
1. Auto-detects current domain from published files
2. Updates `canonical_url` in all posts and comments
3. Updates `in_reply_to` and `root_post` URLs (only for own content)
4. Re-signs all files with new URLs
5. Updates `metadata/blessed-comments.json`
6. Updates `.well-known/polis` endpoints
7. Rebuilds `metadata/public.jsonl` index
8. Updates discovery service database (preserves blessing status)
9. Stages all changes in git

**Example output:**
```
[✓] Migration complete!
    Old domain: olddomain.com → New domain: newdomain.com
    Posts: 3, Comments: 5, Database rows: 5
```

**JSON mode:** See [json-mode.md](json-mode.md) for response format.

**Important notes:**
- Domain should not include protocol (use `example.com`, not `https://example.com`)
- Comments you made on others' posts will have their `in_reply_to`/`root_post` preserved (pointing to the other author's domain)
- The discovery service database update requires `DISCOVERY_SERVICE_URL` and `DISCOVERY_SERVICE_KEY` to be configured
- If database update fails, local migration still succeeds - you can re-beseech comments later

### `polis version`

Print the CLI version number.

```bash
polis version
```

**Example output:**
```
polis 0.22.0
```

### `polis about`

Display complete system information including site details, versions, configuration, keys, discovery status, and project links.

```bash
polis about
polis --json about
```

**Example output:**
```
Polis - Decentralized Social Networking
────────────────────────────────────────
SITE        https://example.com (My Blog)
CLI         0.29.0
KEYS        initialized (SHA256:abc123...)
DISCOVERY   registered
```

Displays site details, versions, configuration paths, key status, and discovery registration.

**Registration status values:**
- **registered** - Site is registered with the discovery service
- **not registered** - Site is not publicly listed (run `polis register` to join the directory)
- **discovery not configured** - `DISCOVERY_SERVICE_URL` or `DISCOVERY_SERVICE_KEY` not set
- **check failed** - Could not reach the discovery service

**JSON mode:** Returns structured data with all sections. See [json-mode.md](json-mode.md) for the full JSON response format.

### `polis register`

List your site in the public directory. Registration makes your site discoverable to other authors and allows you to participate in conversations across the polis network.

```bash
polis register
```

**Features:**
- **Idempotent** - Running on an already-registered site shows current status
- **Attestation verification** - Verifies the discovery service's signature on your registration
- **Automatic metadata** - Pulls email and author name from `.well-known/polis` if available

**Example output (new registration):**
```
[✓] Site registered successfully!

Registration Details:
  Domain: example.com
  Registry: https://ds.polis.pub
  Registered: 2026-01-18T12:00:00Z

Your site is now listed in the public directory.
Other authors can now discover your content and engage with your posts.
```

**Example output (already registered):**
```
[✓] Site already registered.

Registration Details:
  Domain: example.com
  Registry: https://ds.polis.pub
  Registered: 2026-01-15T10:30:00Z

[✓] Attestation Verification: Valid
  (Server signature verified against locally reconstructed payload)
```

**Requirements:**
- `DISCOVERY_SERVICE_URL` and `DISCOVERY_SERVICE_KEY` must be set
- `POLIS_BASE_URL` must be set (domain is extracted from this)
- Your `.well-known/polis` must be accessible via HTTPS

**JSON mode:** Returns registration details including `service_attestation` for verification.

### `polis unregister [--force]`

Unregister your site from the discovery service. This performs a **hard delete** (privacy promise) - all registration data is permanently removed.

```bash
# Interactive confirmation required
polis unregister

# Skip confirmation (for scripting)
polis unregister --force
```

**Warning displayed:**
```
WARNING: Unregistering will remove your site from the public directory.

This means:
  - Your site will no longer be publicly listed or discoverable
  - Other authors cannot find or interact with your content through the network
  - You can re-register anytime to rejoin the community

Are you sure you want to unregister example.com? (type 'yes' to confirm)
```

**Effects of unregistering:**
- Your site is removed from the public directory
- Your content is no longer discoverable through the network
- Other authors cannot interact with your posts via the discovery service
- You can rejoin anytime with `polis register`

**JSON mode:** Skips interactive confirmation automatically.

### `polis render [--force] [--no-markers]`

Render markdown posts and comments to static HTML files using pandoc.

```bash
# Render all posts and comments (skips up-to-date files)
polis render

# Force re-render all files
polis render --force

# Render without snippet markers (for production/clean HTML)
polis render --no-markers
```

**What it does:**
1. On first render, automatically selects a theme from available themes
2. Converts all markdown files in `posts/` and `comments/` to HTML
3. Uses pandoc for markdown-to-HTML conversion
4. Applies theme templates with metadata substitution
5. Embeds blessed comments directly in post HTML files
6. Copies theme CSS to `styles.css` at site root
7. Generates an `index.html` listing all posts
8. Skips files where HTML is newer than markdown (unless `--force`)
9. **Note:** Remote blessed comments are cached. If a comment author updates their comment, use `--force` to fetch the latest content.

**Requires:** pandoc (install with `apt install pandoc` or `brew install pandoc`)

**Generated files:**
- `posts/YYYYMMDD/my-post.html` - Rendered post with embedded blessed comments
- `comments/YYYYMMDD/my-comment.html` - Rendered comment
- `index.html` - Site index listing all posts

**Example output:**
```
=== Render Configuration ===
[i] POLIS_BASE_URL: https://example.com
[i] Posts dir: posts
[i] Comments dir: comments
[i] Blessed comments: 2 post(s) with blessed comments

=== Rendering Posts ===
Processing: posts/20260106/hello.md
  -> Rendered: posts/20260106/hello.html

=== Rendering Comments ===
Processing: comments/20260106/reply.md
  -> Rendered: comments/20260106/reply.html

=== Generating Index ===
Generated: index.html

[✓] Rendering complete!
[i]   Posts rendered: 1 (skipped: 0)
[i]   Comments rendered: 1 (skipped: 0)
[i]   Index: index.html
```

#### Themes

Polis ships with six themes (turbo, zane, sols, vice, especial, especial-light). On first render, a theme is randomly selected. To change themes:

- **Dashboard**: Open **Settings > Theme** and click a theme card. The site re-renders automatically.
- **CLI**: Edit `metadata/manifest.json`, set `active_theme`, then run `polis render --force`.

For theme customization, creating custom themes, template variables, and mustache syntax, see [TEMPLATING.md](TEMPLATING.md).

#### Embedded Source

Each rendered HTML file includes the original markdown source and frontmatter in an HTML comment at the end of the file:

```html
<!--
=== POLIS SOURCE ===
Source: posts/20260106/hello.md
title: Hello World
published: 2026-01-06T12:00:00Z
current-version: abc123...
signature: AAAAB3NzaC1...
---
This is my first post!
=== END POLIS SOURCE ===
-->
```

This enables:
- Verification that the HTML matches the signed source
- Extraction of original markdown from rendered HTML
- Debugging template issues by comparing source to output

#### Snippet Markers

By default, `polis render` injects hidden markers around snippet content to enable in-browser snippet editing in the webapp. Each snippet inclusion (`{{> snippet-name}}`) is wrapped with:

```html
<!-- POLIS-SNIPPET-START: global:snippet-name path=snippets/snippet-name.html -->
<span class="polis-snippet-boundary" data-snippet="global:snippet-name"
      data-path="snippets/snippet-name.html" data-source="global" hidden></span>
<!-- actual snippet content -->
<!-- POLIS-SNIPPET-END: global:snippet-name -->
```

**Marker behavior:**
- The hidden `<span>` provides a DOM anchor for JavaScript without affecting layout
- `data-snippet` contains the snippet identifier (source:name format)
- `data-path` contains the file path for API calls
- `data-source` indicates "global" (from `snippets/`) or "theme" (from theme)
- Nested snippets each get their own markers, creating a hierarchy

**Disabling markers:**

For production deployments or when you want clean HTML without markers:

```bash
polis render --no-markers
```

This is useful for:
- Production builds where markers are unnecessary
- Debugging template output without extra HTML
- Compatibility with strict HTML validators

**JSON mode:** See [json-mode.md](json-mode.md) for response format.

### `polis blessing`

Parent command for blessing-related operations. Must be followed by a subcommand.

#### `polis blessing requests`

List pending blessing requests for your posts.

```bash
polis blessing requests
```

**Example output:**
```
ID    Author              Post                    Status
42    bob@example.com     /posts/hello.md         pending
73    carol@example.com   /posts/hello.md         pending
```

#### `polis blessing grant <hash>`

Approve a pending blessing request by content hash.

```bash
polis blessing grant abc123-def456
```

**What it does:**
1. Updates discovery service status to "blessed"
2. Adds entry to `metadata/blessed-comments.json`
3. Comment becomes visible to your audience

#### `polis blessing deny <hash>`

Reject a pending blessing request by content hash.

```bash
polis blessing deny abc123-def456
```

**What it does:**
1. Updates discovery service status to "denied"
2. Comment remains on author's site but won't be amplified

#### `polis blessing beseech <hash>`

Re-request blessing for a comment by content hash (retry after changes).

```bash
polis blessing beseech abc123-def456
```

**Use when:**
- Original request failed
- You updated the comment and want to re-request

#### `polis blessing sync`

Synchronize auto-blessed comments from the discovery service to your local `blessed-comments.json`.

```bash
polis blessing sync
```

**What it does:**
1. Fetches all blessed comments for your posts from discovery service
2. Compares with local `metadata/blessed-comments.json`
3. Adds any missing entries (e.g., comments auto-blessed while you were offline)

**When to use:**
- After being offline for a while
- To ensure local file matches discovery service
- Automatically called when running `polis blessing requests`

**Example output:**
```
[i] Syncing blessed comments from discovery service...
[✓] Synced 3 comment(s) to blessed-comments.json
```

### `polis follow <author-url>`

Follow an author to auto-bless their future comments on your posts.

```bash
polis follow https://alice.example.com
```

**What it does:**
1. Adds author to `metadata/following.json`
2. Auto-blesses any existing pending comments from this author
3. Future comments from this author are automatically blessed

**Example output:**
```
[OK] Following alice.example.com
[OK] 3 existing comments auto-blessed
```

### `polis unfollow <author-url>`

Stop following an author and hide all their comments.

```bash
polis unfollow https://alice.example.com
```

**What it does:**
1. Removes author from `metadata/following.json`
2. Removes all blessed comments from this author (nuclear option)

**Warning:** This is a destructive action - all previously blessed comments from this author will be hidden.

### `polis notifications`

View and manage notifications about activity on your site and from authors you follow.

```bash
# List unread notifications (default)
polis notifications

# List all notifications
polis notifications list

# Show specific types
polis notifications list --type new_follower,version_available

# JSON output
polis --json notifications
```

**Notification types:**
- `version_available` - A new CLI version is available
- `version_pending` - You upgraded but metadata files need update (run `polis rebuild`)
- `new_follower` - Someone you don't follow started following you
- `new_post` - An author you follow published a new post
- `blessing_changed` - Your comment was blessed or unblessed

#### `polis notifications read <id>`

Mark a notification as read (removes it from the list).

```bash
polis notifications read notif_1737388800_abc123

# Mark all as read
polis notifications read --all
```

#### `polis notifications dismiss <id>`

Dismiss a notification without marking as read.

```bash
polis notifications dismiss notif_1737388800_abc123

# Dismiss old notifications
polis notifications dismiss --older-than 30d
```

#### `polis notifications sync`

Sync notifications from the discovery service.

```bash
# Fetch new notifications
polis notifications sync

# Reset watermark and do full re-sync
polis notifications sync --reset
```

#### `polis notifications config`

Configure notification preferences.

```bash
# Show current config
polis notifications config

# Set poll interval
polis notifications config --poll-interval 30m

# Enable/disable notification types
polis notifications config --enable new_post
polis notifications config --disable version_available

# Mute notifications from specific domain
polis notifications config --mute spam.com
polis notifications config --unmute spam.com
```

**Local storage:**
- `.polis/notifications.jsonl` - Notification log (one per line)
- `.polis/notifications-manifest.json` - Preferences and sync state

### `polis follow --announce`

When following or unfollowing an author, you can optionally announce this to the discovery service:

```bash
# Follow and announce (others can discover you follow alice)
polis follow https://alice.com --announce

# Unfollow and announce
polis unfollow https://alice.com --announce
```

**Privacy note:** Without `--announce`, follow/unfollow is local-only. With `--announce`, your follow action is recorded in the discovery service (opt-in).

### Auto-Blessing

Comments can be automatically blessed (no manual approval required) in two scenarios:

**1. Global Trust (Following)**
When you follow an author, ALL their future comments on ANY of your posts are auto-blessed.

```bash
# Follow Alice - all her comments on your posts are now auto-blessed
polis follow https://alice.example.com
```

**2. Thread-Specific Trust**
When you manually bless a comment from an author, their future comments *on the same post* are auto-blessed. This allows trust to be scoped to specific conversations.

```
Example:
1. Bob comments on your "Intro to Polis" post
2. You bless Bob's comment (polis blessing grant 123)
3. Bob comments again on "Intro to Polis" → auto-blessed!
4. Bob comments on your "Advanced Polis" post → NOT auto-blessed (different post)
```

**Precedence:** Global trust (following) takes priority. If you follow someone, thread-specific trust is irrelevant - they're trusted everywhere.

### `polis dm`

> **Go CLI only.** DM commands are not available in the Bash CLI due to cryptographic requirements (NaCl box encryption, X25519 key conversion).

Manage end-to-end encrypted direct messages between polis instances.

```bash
polis dm <subcommand> [options]
```

**Subcommands:**

| Subcommand | Usage | Description |
|------------|-------|-------------|
| `list` | `polis dm list` | List DM conversations with unread counts |
| `read` | `polis dm read <conversation_id>` | Read messages in a conversation (marks as read) |
| `send` | `polis dm send <recipient_url> <message>` | Send a DM to a recipient |
| `retry` | `polis dm retry [conversation_id]` | Retry delivering unsent messages |
| `config` | `polis dm config` | Show DM acceptance policy from rules.jsonl |
| `decrypt` | `polis dm decrypt [--phrase] [--conversation <id>] [--json]` | Decrypt and print your messages from a local or exported site |

**Examples:**

```bash
# List conversations
polis dm list

# Read a conversation
polis dm read f8e7d6c5b4a3f2e1

# Send a DM
polis dm send https://bob.example.com "Hello Bob!"

# Retry failed deliveries
polis dm retry

# Show DM policy configuration
polis dm config

# Decrypt your messages from an unzipped export (prompts for your password)
polis dm decrypt
polis dm decrypt --phrase          # unlock with your recovery phrase instead
polis dm decrypt --conversation f8e7d6c5b4a3f2e1 --json
```

**Sending:** Fetches the recipient's public key from `.well-known/polis`, encrypts the message with NaCl box (X25519 + XSalsa20-Poly1305), and POSTs to the recipient's `/v1/content/dm/actions/deliver` endpoint. On delivery failure, the message is saved locally as "unsent" and retryable via `polis dm retry`.

**Receiving:** Requires running the webapp or headless API server. CLI-only users can send DMs but cannot receive them (no server to accept incoming deliveries).

**Decrypting an export (`polis dm decrypt`):** point it at an unzipped `.polis` export (or a local site) to print your message plaintext. Bootstrap-epoch messages (received before you set a message password) open with no prompt — the server-held key travels in the export. Password-epoch messages prompt for your **password** (or, with `--phrase`, your **recovery phrase**); the secret is read without echo, used only for the local unwrap, and never stored or transmitted. All decryption runs locally with the same crypto as the browser. The friendlier read path is to run `polis serve` in the export and read in the web UI — see [`polis serve`](#polis-serve-options) below. The on-disk format is documented in `cli-go/pkg/dm/FORMAT.md`.

**Policy:** DM acceptance is controlled via policy rules in `.polis/policies/rules.jsonl`. By default, `allow pub.polis.dm from following` + `deny pub.polis.dm from all` restricts DMs to followed domains. The policy system also supports `emit`/`omit` verbs for blessing and notification control, and `self`/`thread-blessed` sources. See `docs/cli/user/policies.md` for the full grammar and common recipes. Use `polis dm config` to view current rules.

All subcommands support `--json` for machine-readable output.

### `polis tag`

> **Go CLI only.** Tag commands are not available in the Bash CLI.

Manage tags on content. Tags are lightweight labels you apply to posts and feed items for personal organization.

```bash
polis tag <subcommand> [options]
```

**Subcommands:**

| Subcommand | Usage | Description |
|------------|-------|-------------|
| `list` | `polis tag list [--tag <name>] [--target <url>]` | List tags, optionally filtered by tag name or target URL |
| `show` | `polis tag show <name>` | Show all content tagged with a specific tag |
| `apply` | `polis tag apply <name> <target-url>` | Apply a tag to a target URL |
| `remove` | `polis tag remove <name> <target-url>` | Remove a tag from a target URL |
| `delete` | `polis tag delete <name>` | Delete a tag and all its associations |

**Examples:**

```bash
# List all tags
polis tag list

# List all content tagged "favorite"
polis tag show favorite

# Tag a post
polis tag apply favorite https://alice.com/posts/20260301/on-gardens.md

# Remove a tag from a post
polis tag remove favorite https://alice.com/posts/20260301/on-gardens.md

# Delete a tag entirely
polis tag delete old-topic
```

All subcommands support `--json` for machine-readable output.

### `polis clone <url> [target-dir]`

Clone a remote polis site to a local directory. Useful for archiving someone's content, reading offline, or operating on a snapshot of a site you don't own.

**Usage:**
```bash
polis clone https://alice.polis.pub               # target dir derived from domain
polis clone https://alice.polis.pub ./alice       # explicit target dir
polis clone https://alice.polis.pub --full        # re-download all content
polis clone https://alice.polis.pub --diff        # only download changes (default after first clone)
```

**Flags:**
- `--full` — re-download all content, ignore cached state
- `--diff` — only fetch new/changed content (default if the target was previously cloned)

If neither flag is given the clone package decides based on whether a clone-state file exists in the target.

### `polis rotate-key`

Rotate your site's Ed25519 signing key. Generates a fresh keypair, publishes the new public key, records the rotation in your key history at the DS, and updates `.well-known/polis`. Existing signed content remains verifiable via the key history.

**Usage:**
```bash
polis rotate-key                       # rotate; keep old private key archived
polis rotate-key --delete-old-key      # rotate and securely delete the old private key
```

**Flags:**
- `--delete-old-key` — securely delete the old private key after rotation (irreversible)

Must be run from a polis site directory.

### `polis validate`

Run the local validation suite over your site: signed content integrity, index consistency, policy file parseability, key/handle alignment, and bundle registry health. Mirrors the checks Patrol/Medic run server-side for hosted sites.

**Usage:**
```bash
polis validate
polis validate --json
```

### `polis discover`

Interact with the discovery service to find new content or sync state for the people you follow.

**Usage:**
```bash
polis discover                                       # check all followed authors for new activity
polis discover --author https://alice.polis.pub      # check just one author
polis discover --json
```

**Flags:**
- `--author <url>` — limit discovery to one specific author

Requires `DISCOVERY_SERVICE_URL` to be configured. Reads `following.json` to determine who to query.

### `polis unpublish <path>`

Unpublish a post or comment — a *clean break* operation that severs all ties between the published identity and the content. Differs from `unregister` (which removes the whole site) and from deleting the file (which leaves DS state behind).

**Usage:**
```bash
polis unpublish content/pub.polis.core/post/20260201/my-post.md
polis unpublish content/pub.polis.core/comment/20260201/comment-id.md
polis unpublish <path> -y                            # skip confirmation
```

**Flags:**
- `-y` — skip the confirmation prompt

**Semantics:** Post unpublish cascades blessing state in the DS (blessed → orphaned, pending → denied). Comment unpublish resets the comment's blessing to `pending`. Republishing later is treated as a brand-new publication — orphaned blessings are NOT restored. See [Unpublish Lifecycle](../../ds/developer/unpublish-lifecycle.md) for full state transition rules.

### `polis serve [options]`

Start the polis HTTP server (the webapp). Only available in the bundled binary (`polis-full`) — the CLI-only binary (`polis`) prints a pointer to the bundled binary if you try.

**Usage:**
```bash
polis serve                            # bind to default port
polis serve --port 8080                # custom port
polis serve --data-dir /path/to/site   # serve a site outside cwd
polis serve --dev                      # developer mode (enable localhost messaging UI)
polis serve --help                     # show all options
```

**Options:**

| Flag | Description |
|------|-------------|
| `-d, --data-dir <path>` | Site directory to serve (default: current directory) |
| `-p, --port <n>` | Port to bind on localhost (default: auto-pick) |
| `--dev` | **Developer mode** — enable the localhost messaging compose/send UI (read-only by default; see below) |
| `--mirror` | Serve a read-only clone (data tenancy); default is `--owner` |
| `--reader` | Reader surface; default is `--editor` for `--owner` |

**Reading an exported archive offline:** unzip your `.polis` export, run `polis serve` inside it, open the printed localhost URL, and enter your message password to read your DMs in the same web UI you use hosted — no network, no CLI crypto. (For a scriptable extract, see [`polis dm decrypt`](#polis-dm) above.)

**Localhost messages are read-only by default.** Direct messages are a networked feature (a message is sealed in your browser and delivered point-to-point to the recipient's running instance), and a served export is offline. So on plain localhost the messages view renders read-only: the conversation list and threads display (and password epochs unlock with your password or recovery phrase, exactly as hosted), but the composer, send, mark-read sync, and recipient-protection checks are disabled, with a *"Reading only — sending is disabled on localhost"* banner and a matching note in **Settings → Messages**. Setting or changing your message password still works offline. The `--dev` flag re-enables the compose/send UI for development and testing (delivery to a real peer still requires a reachable network). The signal is purely `window.__POLIS_HOSTED` (injected by the hosted service) plus the `--dev` override — there is no separate "archive" mode or per-site toggle.

See the [Webapp User Manual](../../webapp/user/user-manual.md) for full usage. Webapp-only and bundled-binary build instructions are in [docs/cli/README.md](../README.md).

## File Frontmatter

Published files include YAML frontmatter with metadata:

```yaml
---
canonical_url: https://alice.example.com/posts/20260106/hello.md
version: sha256:a3b5c7d9e1f2...
author: alice@example.com
published: 2026-01-15T12:00:00Z
signature: -----BEGIN SSH SIGNATURE-----
U1NIU0lHAAAAAQA...
-----END SSH SIGNATURE-----
in_reply_to: https://bob.example.com/posts/intro.md  # Comments only
in_reply_to_version: sha256:xyz789...                # Comments only
---

# Your Content Here

The actual post or comment content follows the frontmatter.
```

## Version History

Polis uses diff-based version storage. The `.versions` file format uses standard unified diff format, making it compatible with Unix `diff` and `patch` utilities for manual inspection or reconstruction.

**Example `.versions` file:**
```
== Version 1 ==
Version: sha256:abc123...
Date: 2026-01-15T12:00:00Z

Full content of version 1...

== Version 2 ==
Version: sha256:def456...
Date: 2026-01-20T15:30:00Z
Previous: sha256:abc123...

--- old
+++ new
@@ -5,7 +5,7 @@
-This is the old line
+This is the updated line
```

## Configuration

Polis CLI uses a layered configuration system with the following precedence (highest to lowest):

1. **Environment variables** - For CI/CD and temporary overrides
2. **`.env` file** - For developer/deployment settings
3. **`.well-known/polis`** - For user-specific directory customization
4. **Built-in defaults** - Always available as fallback

### Environment Variables

```bash
# Required for blessing commands
export POLIS_BASE_URL="https://yourdomain.com"

# Discovery service (optional - has default)
export DISCOVERY_SERVICE_URL="https://ds.polis.pub"

# API authentication (required for blessing operations)
export DISCOVERY_SERVICE_KEY="your-api-key"

# Optional directory overrides
export KEYS_DIR=".polis/keys"
export POSTS_DIR="posts"
export COMMENTS_DIR="comments"
export VERSIONS_DIR_NAME=".versions"
```

Add to `~/.bashrc` or `~/.zshrc` for persistence.

### Using a `.env` File

The CLI looks for `.env` in this order:
1. Current working directory (`.env`) - for per-site configuration
2. Home directory (`~/.polis/.env`) - for shared configuration across sites

Create a `.env` file in your site directory or in `~/.polis/`:

```bash
# Per-site config (in your polis site directory)
cp .env.example .env

# Or shared config (for DISCOVERY_SERVICE_KEY etc.)
mkdir -p ~/.polis
cp .env.example ~/.polis/.env
```

Example `.env`:
```bash
POLIS_BASE_URL=https://alice.example.com
DISCOVERY_SERVICE_KEY=eyJhbGciOiJI...
```

**Security Note:** Never commit `.env` files containing secrets. The `.env.example` file is safe to commit as a template.

### Site Title

Set a custom site title for branding in rendered HTML and comment attribution:

```bash
polis init --site-title "My Awesome Blog"
```

The site title is stored in `.well-known/polis` and used:
- In HTML page titles and headers (`{{site_title}}` template variable)
- When displaying your comments on other people's posts
- In `polis about` output

If not set, the domain from `POLIS_BASE_URL` is used as a fallback.

### Custom Directory Paths

You can customize directory paths during initialization:

```bash
polis init --posts-dir articles --comments-dir replies
polis init --site-title "My Blog" --posts-dir articles
```

Or edit the `config` section in `.well-known/polis` after initialization:

```json
{
  "version": "0.2.0",
  "config": {
    "directories": {
      "keys": ".polis/keys",
      "posts": "articles",
      "comments": "replies",
      "versions": ".versions"
    },
    "files": {
      "public_index": "metadata/public.jsonl",
      "blessed_comments": "metadata/blessed-comments.json",
      "following_index": "metadata/following.json"
    }
  }
}
```

## JSON Mode

All commands support `--json` for machine-readable output:

```bash
polis --json post my-post.md | jq -r '.data.content_hash'
```

See [json-mode.md](json-mode.md) for response schemas, error codes, and scripting examples.

## Publishing Workflow

### 1. Write Content
```bash
vim posts/my-thoughts.md
```

### 2. Publish Locally
```bash
polis post posts/my-thoughts.md
```

### 3. Commit to Git
```bash
git add .
git commit -m "Add: my-thoughts.md"
```

### 4. Push to Static Host
```bash
git push origin main
```

### 5. Request Blessing (if commenting)
```bash
# Note: polis comment and polis republish automatically request blessings
# You rarely need to run this manually - only for edge cases (see docs)

# If you do need to retry a blessing request:
polis blessing requests           # View pending requests
polis blessing beseech <hash>     # Retry request by hash
```

## Common Use Cases

### Creating a Blog Post
```bash
cat > posts/why-decentralization-matters.md << 'EOF'
# Why Decentralization Matters

Centralized platforms have too much control...
EOF

polis post posts/why-decentralization-matters.md
git add . && git commit -m "New post: decentralization"
git push
```

### Replying to Someone's Post
```bash
polis comment https://alice.com/posts/20260106/hot-take.md

# Interactive editor opens - write your reply

# After saving, blessing request is automatically sent
# Your comment will be pending until the post author blesses it
```

### Updating a Post
```bash
# Edit the canonical file directly
vim posts/20260106/my-post.md

# Republish with new version
polis republish posts/20260106/my-post.md

git add . && git commit -m "Update: my-post.md"
git push
```

### Viewing Version History
```bash
# List all versions in .versions file
cat posts/20260106/.versions/my-post.md

# Reconstruct specific version
polis extract posts/20260106/my-post.md sha256:abc123...
```

## Security Notes

### Private Key Protection
- **Never commit `.polis/keys/id_ed25519`** (private key)
- Add to `.gitignore`: `.polis/keys/id_ed25519`
- Public key (`.polis/keys/id_ed25519.pub`) is safe to share

### Signature Verification
Anyone can verify your content signatures:

```bash
# Extract public key from .well-known/polis
curl https://alice.com/.well-known/polis | jq -r .public_key > alice.pub

# Verify signature (manual process - automated tool coming)
ssh-keygen -Y verify -f alice.pub -I alice@example.com -n file \
  -s signature.sig < content.txt
```

### File Content Integrity

Each published file (`.md`) - **both posts and comments** - contains two integrity fields in its frontmatter:

**`current-version` (Content Hash)**

The SHA-256 hash of the **entire file content** (frontmatter + body), canonicalized:

```
current-version: sha256:a1b2c3d4e5f6...
```

**Canonicalization** ensures consistent hashing:
- Trailing whitespace removed from each line
- Trailing empty lines removed
- Exactly one newline at the end

**`signature` (Cryptographic Signature)**

The Ed25519 signature is computed over the **entire file content minus the signature field itself**, canonicalized:

```
signature: AAAAB3NzaC1lZDI1NTE5...
```

This means the signature covers:
- All frontmatter fields (title, published, type, current-version, in-reply-to, etc.)
- The entire body content

### What Happens If You Edit Without Republishing

If you manually edit a published post or comment and deploy without running `polis republish`:

| Change Made | Hash Valid? | Signature Valid? | Consequence |
|-------------|-------------|------------------|-------------|
| Edit body text | ❌ No | ❌ No | Verification fails |
| Edit title | ❌ No | ❌ No | Verification fails |
| Edit published date | ❌ No | ❌ No | Verification fails |
| Edit current-version | ❌ No | ❌ No | Verification fails |
| Edit any frontmatter | ❌ No | ❌ No | Verification fails |
| Trailing whitespace only | ✅ Maybe* | ✅ Maybe* | May pass due to canonicalization |

*Canonicalization normalizes trailing whitespace, so minor whitespace changes may not break verification.

**Practical consequences of editing without republishing:**

1. **`polis preview <url>`** shows: "Signature: FAILED"
2. **Other polis users** see the content as tampered/unverified
3. **New blessing requests** may fail verification
4. **Version history** becomes inconsistent (hash doesn't match content)

**Safe fields to change:** None. Any edit to a published `.md` file requires `polis republish` to:
- Recompute the content hash
- Re-sign with your private key
- Update the version history

**Files you CAN safely edit:**
- Rendered `.html` files (not signed, regenerated by `polis render`)
- Theme/template files
- Configuration files

### What Happens If You Change POLIS_BASE_URL Without Migrating

The canonical URL of your content is **not stored in the file itself** - it's derived from `POLIS_BASE_URL + file_path`. This means:

**What's in the frontmatter:**
```yaml
# Posts - NO URL field
title: My Post
published: 2026-01-26T12:00:00Z
current-version: sha256:...
signature: ...

# Comments - in-reply-to points to PARENT's URL, not yours
in-reply-to:
  url: https://other-person.com/posts/their-post.md
  root-post: https://other-person.com/posts/their-post.md
```

**If you change `POLIS_BASE_URL` without running `polis migrate`:**

| What | Status | Why |
|------|--------|-----|
| Your signatures | ✅ Valid | URL not in signed content |
| Your content hashes | ✅ Valid | URL not in file content |
| Discovery service records | ❌ Broken | Still indexed by old URLs |
| Others' links to your posts | ❌ Broken | Point to old domain |
| Others' `in-reply-to` fields | ❌ Broken | Their comments reference your old URLs |
| Your `blessed-comments.json` | ❌ Broken | Post URLs use old domain |

**Bottom line:** Cryptographically valid, but operationally broken.

**Why `polis migrate` exists:**
1. Updates discovery service URL indexes
2. Creates signed migration announcement for others to verify
3. Proves key continuity (same private key controls both domains)
4. Allows followers to update their local references

Always use `polis migrate <new-domain>` when changing domains - don't just edit `POLIS_BASE_URL`.

## Terminal User Interface (polis-tui)

> **Deprecated** — The TUI is deprecated as of v0.46.0. Use the [webapp](WEBAPP-USER-MANUAL.md) instead for an interactive interface.

## Upgrading

**Go CLI / self-hosters:** use **Tailor** to bring an existing site up to the current spec — `tailor --apply` runs in one pass for any historical layout, defaults to a dry-run diagnosis, and writes a timestamped backup before changing anything. See [actors.md](../../general/actors.md#tailor). (Tailor migrates *site data and layout*; it does not download binaries — the Go CLI ships as a single binary you replace directly.)

**Bash CLI:** `polis-upgrade` is the bash-specific tool for both site migrations and binary self-update (downloading updated `polis`/`polis-tui` binaries). Run `polis-upgrade --help` for the full set of options.

## Shell Completion

Polis includes tab completion scripts for bash and zsh. After setup, you can type `polis i<tab>` to complete to `init`.

### Bash

Add to your `~/.bashrc`:

```bash
source /path/to/polis/completions/polis.bash
```

Or for auto-loading, copy to the bash-completion directory:

```bash
cp completions/polis.bash ~/.local/share/bash-completion/completions/polis
```

### Zsh

Add the completions directory to your fpath in `~/.zshrc`:

```zsh
fpath=(/path/to/polis/completions $fpath)
autoload -Uz compinit && compinit
```

Or copy to your zsh completions directory:

```bash
mkdir -p ~/.zsh/completions
cp completions/polis.zsh ~/.zsh/completions/_polis
```

Then add to `~/.zshrc`:

```zsh
fpath=(~/.zsh/completions $fpath)
autoload -Uz compinit && compinit
```

### What's Completed

- All 24 polis commands (init, post, comment, etc.)
- Subcommands for `blessing` and `migrations`
- Global `--json` flag

## Troubleshooting

### "ssh-keygen: command not found"
Install OpenSSH client (see Installation section above).

### "jq: command not found"
Install jq JSON processor (see Installation section above).

### "pandoc is required for rendering"
Install pandoc to use `polis render`: `apt install pandoc` (Linux) or `brew install pandoc` (macOS). Pandoc is only required for the render command.

### "No such file: .polis/keys/id_ed25519"
Run `polis init` to create keys and directory structure.

### "Index file is corrupted or missing"
Run `polis rebuild` to regenerate `public.jsonl` from published files.

### Version history missing
`.versions` files are created on first `polis republish` - they don't exist for initial `polis post`.

## Next Steps

- Deploy your content to GitHub Pages, Netlify, or any static host
- Read [security-model.md](../../general/security-model.md) for the full cryptographic model and threat analysis
- Customize your site with [templating.md](templating.md)
- Try the [webapp](../../webapp/user/user-manual.md) for a visual interface

## Support

For issues, questions, or feature requests, please file an issue in the GitHub repository.

## License

AGPL-3.0 - See [LICENSE](../LICENSE)
