# Polis Webapp User Manual

This guide covers the Polis webapp — a local web interface for managing your Polis site. If you use the command-line tool instead, see [USAGE.md](USAGE.md).

The webapp runs on your computer, modifies files on your disk, and opens in your browser. It is not a hosted web application — there are no accounts, no login, and no database. Your files are the source of truth.

---

## Table of Contents

1. [Getting Started](#getting-started)
2. [Layout & Navigation](#layout--navigation)
3. [Core Features](#core-features)
4. [Social Features](#social-features)
5. [How the Webapp Differs from a Hosted Web App](#how-the-webapp-differs-from-a-hosted-web-app)
6. [Deploying Your Site](#deploying-your-site)
7. [Security](#security)
8. [Settings](#settings)
9. [Hooks & Automations](#hooks--automations)
10. [Notifications](#notifications)
11. [File Structure](#file-structure)
12. [Troubleshooting](#troubleshooting)
13. [Manual Operations](#manual-operations)
14. [Updating the Webapp](#updating-the-webapp)

---

## Getting Started

### Starting the Webapp

There are two binaries that can run the webapp:

**Standalone binary** (`polis-server`):

```
polis-server                        # Uses current directory as your site
polis-server --data-dir /path       # Use a specific directory
polis-server -d /path               # Short form
```

**Bundled binary** (`polis-full`):

```
polis serve                         # Uses current directory as your site
polis serve --data-dir /path        # Use a specific directory
polis serve -d /path                # Short form
```

The bundled binary includes all CLI commands plus the `serve` command. The standalone binary only runs the server.

If you run `polis serve` with the CLI-only binary (not the bundled one), you'll see an error directing you to use the bundled binary instead.

### What Happens on Startup

When you start the webapp:

1. The data directory is created if it doesn't exist
2. Your site configuration is loaded (keys, `.env`, `.well-known/polis`)
3. Any old-format draft directories are automatically migrated to the new layout
4. Background sync starts — but only fires when browser tabs are connected via SSE
5. A port is automatically found on localhost
6. Your default browser opens after a brief delay
7. The server prints its URL and data directory to the terminal

There is no `--port` flag — the port is always dynamically allocated. The URL is printed to the terminal so you can find it.

The server binds to `localhost` only. It is never accessible from other machines on your network.

### First Launch

If the data directory has no site configured, the webapp shows a **Welcome screen** with two options:

**Initialize New Site** — Creates a fresh Polis site with:
- A new Ed25519 keypair for signing your content
- A `.well-known/polis` identity file
- The directory structure for posts, comments, and snippets
- A default `site/snippets/about.md` for your About section

You can optionally provide a site title, base URL, and discovery service URL. These can also be configured later.

**Link Existing Site** — Points the webapp at a directory that already contains a Polis site. The directory must have a `.well-known/polis` file and a keypair in `.polis/keys/`.

### The Setup Wizard

After creating or linking a site, the webapp shows a setup wizard if your site isn't registered with the discovery network yet. The wizard walks through:

1. **Deploy** — Make your site publicly accessible (e.g., push to GitHub Pages or Vercel). The wizard polls your domain every few seconds until it detects your `.well-known/polis` file is reachable.
2. **Register** — Register your domain with the discovery service so other authors can find you.

You can dismiss the wizard and complete these steps later. If dismissed before registering, a persistent banner reminds you to finish.

### Lifecycle Stages

The UI adapts to where you are in the setup process:

- **just_arrived** — No site configured yet. Only the welcome/init flow is shown.
- **first_post** — Site is initialized but no posts published yet. A welcome panel with onboarding steps is shown. Social features are available but sub-tabs may show empty states.
- **active** — At least one post published. Full navigation is visible.

---

## Layout & Navigation

The webapp uses a centered-column layout with a thin top navigation bar. The logged-in view is a **single stream-screen** rendered by the v4 infinity-stream shape — there is no Feed/Posts mode toggle. Filtering is driven by PQL (polis query language) sentences: clicking an icon loads a preset sentence; the sentence-filter widget in the topbar lets you compose your own.

### Top Nav Bar

The top bar contains (left to right):

- **Avatar button** — 28px circle with your initial on a gradient. Click opens a dropdown with your name/handle, follower/following/post stats, Dashboard link, Settings link, and Log out. Carries a notification dot when *any* unread item exists across the system.
- **Icon row** (each loads a PQL preset; carries its own notification dot where noted):
  - **Gateway** (arc + dots) — "Activity from my network." Dot = unread activity items.
  - **Paragraph** (three lines) — "My posts."
  - **Comment** (speech bubble) — "Comments to bless." Dot = pending blessing requests.
  - **People** (silhouette + circle) — "Profiles."
  - **Envelope** — "My messages." Dot = unread DMs.
  - **Edit** (cross) — "New post."
- **Sentence-filter widget** (centered) — composable PQL: qualifier slot, type slot, scope slot, modifier slot, plus a site-typeahead input. Builds sentences like `all activity from my network by date` and routes to `/_/pql/<sentence>`.

The bell and heart icons that used to sit in the topbar are gone — their indicators are now the badge dots on the gateway and comment buttons described above.

### Centered Content Column

All content is rendered in a 640px centered column below the top bar. The same column hosts the stream, settings, the editor, post detail, message threads — every view in the SPA.

**Toasts** appear in the bottom-right corner for success, error, warning, and info messages. They auto-dismiss after a few seconds. Some toasts include action buttons — for example, after blessing a comment you may see a suggestion to follow the commenter back.

**Confirmation modals** appear centered on screen for destructive or important actions (publishing, revoking blessings, unfollowing).

### Deep-Linking

The webapp supports bookmarkable URLs via the `/_/` path prefix. You can navigate directly to any view:

| URL | View |
|-----|------|
| `/_/posts` | Published posts |
| `/_/posts/drafts` | Draft posts |
| `/_/comments` | My Comments |
| `/_/comments/pending` | My Comments (pending tab) |
| `/_/blessings` | Blessing Requests |
| `/_/snippets` | About editor |
| `/_/social/pulse` | Community Pulse |
| `/_/social/conversations` | Conversations |
| `/_/social/following` | Following |
| `/_/social/followers` | Followers |
| `/_/settings` | Settings |

---

## Core Features

### Writing and Publishing Posts

1. Click **New Post** in the Posts section (or the "+" button)
2. The editor opens with a markdown area on the left and a live preview on the right
3. Enter a filename (auto-generated from your title, editable before first save)
4. Write your content in markdown
5. Click **Publish** — a confirmation modal appears
6. The post is signed with your Ed25519 key and saved to `content/pub.polis.core/post/YYYYMMDD/`

After publishing, the post appears in your Published list. A brief pulsing "broadcast" animation appears below the post to indicate it was just published.

### Editing and Republishing

1. Click any published post in the sidebar
2. Edit the markdown content
3. Click **Republish** — the version number increments and the post is re-signed
4. The version history in the post's frontmatter is updated automatically

### Unpublishing a Post

To remove a published post:

1. Click the post in the Published list
2. Click **Unpublish** in the post detail panel
3. Confirm the action

Unpublishing removes the post from the public index. The source file is deleted. This cannot be undone from the UI — if you still have the file in version control, you can recover it manually.

### Drafts

Click **Save Draft** at any time while writing. Drafts are stored in `.polis/bundles/pub.polis.core/posts/drafts/` with auto-numbered IDs. Open a draft from the Drafts sidebar view to continue editing, then publish when ready.

### Commenting on Other Authors' Posts

1. Click **New Comment** in the My Comments section
2. Enter the URL of the post you're replying to in the "Replying to" field
3. Write your comment in markdown
4. Click **Sign & Send for Blessing**

This signs your comment and sends a blessing request to the post's author. Possible outcomes:

- **Auto-blessed**: The author has auto-blessing enabled and your comment is immediately approved
- **Pending**: The comment is saved and awaits the author's manual approval
- **Signed but request failed**: The comment is signed locally, but the blessing request couldn't reach the discovery service (a warning toast explains what happened)

Your comment then appears in **My Comments > Pending** until the author blesses or denies it. Use the **Sync** button in the Pending tab to check for updates.

### Blessing Workflow

> For CLI blessing commands, see the [CLI Command Reference](USAGE.md). For terminology, see the [Glossary](GLOSSARY.md).

When other authors comment on your posts, their blessing requests appear in **Blessing Requests**.

**To review a request:**

1. Click **Blessing Requests** in the sidebar to open the consolidated view
2. Use the tabs to filter: All, Pending, or Blessed
3. Click any pending request to open a detail panel
4. The panel shows who commented, on which post, and when
5. Click **Bless** to approve or **Deny** to reject

After blessing a comment, a suggestion toast may appear offering to follow the commenter back. Click the Follow button in the toast to follow them without opening the full follow panel.

**Blessed comments** become part of your site's public content. They appear in the Blessed tab.

**To revoke a blessing:**

1. Go to **Blessing Requests > Blessed**
2. Click the blessed comment
3. Click **Revoke Blessing** in the detail panel
4. Confirm the action

Revoking removes the comment from your blessed index.

### About Page

The **Snippets** sidebar item opens the About editor — a full-screen markdown editor for your site's About section. This edits `site/snippets/about.md`, which is included by your theme's About template.

Changes are saved immediately when you click **Save**. The site is re-rendered automatically after saving.

If `site/snippets/about.md` does not exist yet (older sites), the editor pre-populates with default welcome text. Saving creates the file.

### Snippets in Posts

You can include reusable content in posts using `{{> snippet-name}}`.

Snippet resolution order: `.md` first, then `.html`, then exact name match. Global snippets live in `site/snippets/`. Theme snippets are part of your active theme.

---

## Social Features

### Following Authors

Go to **Social > Authors > Following** and click **Follow Author**. Enter the author's URL in any of these formats:
- Full URL: `https://example.com/`
- Bare domain: `example.com`
- Follow link: `polis.pub/f/handle`

When your following list is empty, the webapp suggests `discover.polis.pub` as a community hub to get started. You can follow it with one click.

Each followed author shows their domain, full URL, and when they were last checked. Click **Unfollow** to remove them (requires confirmation).

**Important side effects:**
- Following an author automatically blesses any of their pending blessing requests on your posts.
- Unfollowing an author automatically denies any of their pending blessing requests on your posts.

### Conversations

**Social > Discover > Conversations** shows a combined view of posts from authors you follow and activity from the discovery network. It has three subtabs:

- **All** — Merged view of Feed and Activity together
- **Feed** — Posts from authors you follow only
- **Activity** — Chronological stream of events from the discovery service (follows, comments, blessings, etc.)

The feed:
- Refreshes automatically in the background (every 30 seconds server-side when a browser tab is connected via SSE)
- Shows a badge with your unread count
- Supports manual refresh with the Refresh button
- Lets you mark items as read/unread individually or in bulk
- Has an **Unread From Here** button — marks the clicked item and everything above it as unread
- Has a **Mark All Read** button in the header
- Shows a staleness banner if the feed hasn't updated in over 24 hours

### Tagging Feed Items

You can tag feed items for personal organization. Hover over any item in the Feed to reveal a **Tag** button. Clicking it opens an autocomplete input pre-populated with your existing tags. Type to filter or create a new tag, then press Enter or click a suggestion to apply it. Tags appear as small labels on the feed item.

To remove a tag, click the "x" on the tag label. To view all items with a specific tag, click the tag name -- this filters the feed to show only items with that tag.

Tags are stored locally at `content/pub.polis.core/tag/` and optionally registered with the discovery service.

### Community Pulse

**Social > Discover > Pulse** shows a dashboard of activity across the Polis network. The Pulse view displays summary cards with aggregate activity data from the discovery service.

Pulse data is fetched from the discovery service and cached locally.

### Followers

**Social > Stats > Followers** shows how many people follow your site and their domains.

---

## How the Webapp Differs from a Hosted Web App

The Polis webapp is not like a typical web application:

- **It modifies local files.** Every action (publishing, commenting, blessing) writes files to your disk. The webapp is a graphical interface for managing a directory of files.
- **It binds to localhost only.** The server is never accessible from other machines.
- **There are no user accounts or login.** If you can reach `localhost`, you have full access. This is intentional — see [Security](#security).
- **There is no database.** Files and directories are the source of truth. JSON files store configuration, JSONL files store event history.
- **You still need to deploy separately.** Publishing a post writes files locally. To make them available on the internet, you need a deployment step (git push, rsync, etc.). The [Hooks](#hooks--automations) system can automate this.

---

## Deploying Your Site

Publishing a post in the webapp writes files to your local disk. To make them publicly accessible, you need a separate deployment step.

### Why Deployment Is Needed

Polis sites are static files. The webapp is a local authoring tool, not a web host. After you publish or update content, those changes exist only on your machine until you deploy them.

### Common Deployment Patterns

**Git push to a hosting provider** (most common):
Your site directory is a git repository connected to GitHub Pages, Vercel, or Netlify. After publishing, commit and push — the hosting provider builds and deploys automatically. The webapp's [Deployment Wizard](#configuring-hooks-via-the-webapp) can set this up for you.

**rsync to a server**:
Use `rsync` to copy your site directory to a web server. Useful if you manage your own hosting.

**Manual upload**:
Copy your site files to any static file host (S3, a shared hosting control panel, etc.). Simplest approach for infrequent publishers.

### Automating Deployment with Hooks

The recommended approach is to configure a [hook](#hooks--automations) that runs after every publish. The webapp includes a Deployment Wizard (in Settings) that generates hook scripts for Vercel, GitHub Pages, and git-only workflows. Once configured, publishing a post automatically commits and pushes — no manual steps needed.

---

## Security

The webapp binds to `localhost` only (hardcoded, no flag to change it) and has no login system — if you can reach the port, you have full access. This is by design: it runs locally for your use only.

Your Ed25519 private key is read by the server process for signing but is never transmitted — only signatures are sent. All file paths are validated to prevent directory traversal.

**Protect these files:**
- `.polis/keys/id_ed25519` — your private signing key
- `.env` — site URL and discovery service credentials
- Your data directory — treat it like any directory with private files

For the full cryptographic model, key management details, and threat analysis, see [SECURITY-MODEL.md](SECURITY-MODEL.md).

---

## Settings

Navigate to **My Site > Settings** (gear icon at the sidebar footer) to view and manage your site configuration.

### Site Section

| Field | Source | Description |
|-------|--------|-------------|
| Site Title | `.well-known/polis` | Your site's display name (editable inline — click to edit) |
| Avatar | `.well-known/polis` | Your site's avatar (circle with initial letter). A default is generated on init — use **Randomize** to pick a new color scheme, **Save** to apply, or **Reset** to remove |
| Public Key | `.polis/keys/id_ed25519.pub` | Your Ed25519 public key (truncated, with Copy and Rotate buttons) |
| Data Directory | Startup flag or cwd | Where your site files live |

**Rotating your key:**

Click **Rotate Key** to generate a new keypair. This:
1. Notifies the discovery service first (so your signature changes are attributed correctly)
2. Generates a new Ed25519 keypair
3. Writes the new keys to `.polis/keys/`
4. Updates your `.well-known/polis` identity file

Key rotation requires your site to be registered with the discovery service and your `POLIS_BASE_URL` to be set in `.env`.

### Webapp Appearance

The Webapp Appearance section lets you toggle between light and dark color modes:

- **Light**: Warm cream background with brown accents
- **Dark**: Deep violet background with warm gold accents

This controls only the webapp's color scheme — it does not affect your public site's theme. The preference is saved to `config.json` and synced via `localStorage` for instant loading.

### Site Theme

The Site Theme section has a dropdown selector showing all user-selectable themes:

| Theme | Description |
|-------|-------------|
| `especial` | Dark gold and navy, inspired by Modelo Especial |
| `especial-light` | Light variant of especial with warm fog tones |
| `studio13` | Stark black and burnt orange, late-night studio energy |
| `turbo` | Deep blue with bright cyan, retro computing aesthetic |
| `vice` | Warm coral and sunset hues, Miami Vice vibes |
| `zane` | Neutral dark with teal and salmon, based on a classic editor theme |

> `sols` ships with the core bundle but is *reserved* as the logged-out landing theme on polis.pub. It is filtered out of the user-selectable dropdown so that selecting a personal theme always produces a visible shift from the system chrome.

Select a theme from the dropdown to switch immediately — this updates `active_theme` in `.polis/bundles/registry.json`, applies the new theme CSS, and re-renders your site. (Active theme is private per-tenant configuration; it does not live in `.well-known/polis`.)

### Discovery Service Section

| Field | Source | Description |
|-------|--------|-------------|
| Status | Runtime | "Connected" (green) or "Not configured" (yellow) |
| URL | `.env` `DISCOVERY_SERVICE_URL` | Your discovery service endpoint |
| Registration | Discovery service API | Whether your domain is registered |

If your site is registered, you'll see the registration date. If not, a **Register** button lets you register directly. You can also **Unregister** to remove your site from the discovery network.

The discovery service uses sensible defaults — if you don't set `DISCOVERY_SERVICE_URL` in your `.env`, the public Polis discovery service (`ds.polis.pub`) is used automatically.

### View Preferences

| Setting | Storage | Default | Description |
|---------|---------|---------|-------------|
| View mode | `.polis/webapp/config.json` | `list` | List view or split-pane browser view |
| Show frontmatter | `.polis/webapp/config.json` | `true` | Toggle YAML frontmatter visibility in the editor |
| Hide read items | `.polis/webapp/config.json` | `false` | Hide read items in feed views |
| Webapp theme | `.polis/webapp/config.json` | `dark` | Light or dark color mode for the webapp UI |

### Troubleshooting Section

The Settings page includes a **Troubleshooting** section with:

- **Re-render all pages** — Force-rebuilds all your published HTML from source markdown. Useful after changing your theme, updating snippets, or recovering from a corrupted render.

### Export Section

- **Download site** — Downloads your entire site directory as a zip archive, including your keys. Use this for backups or migrating to a new machine.

### Where Settings Come From

> For the full configuration loading order (environment variables, `.env`, `.well-known/polis`, defaults), see [USAGE.md §Configuration](USAGE.md#configuration).

Settings are loaded from multiple places:

| Source | What It Stores |
|--------|---------------|
| `.well-known/polis` | Site identity (title, author, email, public key) |
| `.env` | Runtime config (`POLIS_BASE_URL`, `DISCOVERY_SERVICE_URL`, `DISCOVERY_SERVICE_KEY`, `LOG_LEVEL`, `LOG_RETENTION_DAYS`) |
| `.polis/webapp/config.json` | UI preferences (view mode, frontmatter toggle, hide read, setup wizard state, hooks) |

The `.env` file is searched in order: your data directory first, then the current working directory, then `~/.polis/`.

---

## Hooks & Automations

Hooks are shell scripts that run automatically after you publish, republish, or bless a comment. The most common use is **automated deployment** — pushing your site to a hosting provider after every publish.

### The Three Hook Events

| Event | When It Fires |
|-------|--------------:|
| `post-publish` | After a new post is published |
| `post-republish` | After an existing post is updated |
| `post-comment` | After a comment is auto-blessed |

### Configuring Hooks via the Webapp

In **Settings**, find the **Help Me...** section with two wizards:

**Deployment Wizard** — walks you through setting up automated deployment:
1. Choose a deployment method (Vercel, GitHub Pages, or Git-only)
2. Select which hook events to configure
3. Review the generated script
4. Confirm — scripts are created in `.polis/webapp/hooks/`

**Custom Script Wizard** — creates starter scripts for you to customize:
1. Review the three hook types and available environment variables
2. Select which hooks to create
3. Scripts are created with placeholder content

### Configuring Hooks on the Filesystem

Hook scripts live at `.polis/webapp/hooks/`:

```
.polis/webapp/hooks/
├── post-publish.sh
├── post-republish.sh
└── post-comment.sh
```

Each script must be executable (`chmod +x`). Hooks are resolved by checking the explicit paths in `.polis/webapp/config.json`, then auto-discovering scripts in `.polis/webapp/hooks/`.

### Built-In Templates

| Template | What It Does |
|----------|-------------|
| **Vercel** | `git add -A && git commit && git push` (triggers Vercel deployment) |
| **GitHub Pages** | `git add -A && git commit && git push` (triggers GitHub Pages build) |
| **Git Commit** | `git add -A && git commit` (commit only, no push) |
| **Custom** | Starter script with comments explaining available variables |

### Environment Variables Passed to Hooks

Every hook script receives these environment variables:

| Variable | Description | Example |
|----------|-------------|---------|
| `POLIS_EVENT` | The hook event type | `post-publish` |
| `POLIS_PATH` | Relative path to the file | `content/pub.polis.core/post/20260213/my-post.md` |
| `POLIS_TITLE` | Post title (or reply-to URL for comments) | `My First Post` |
| `POLIS_VERSION` | Version string | `1` |
| `POLIS_TIMESTAMP` | ISO 8601 timestamp | `2026-02-13T15:04:05Z` |
| `POLIS_SITE_DIR` | Absolute path to site directory | `/home/user/my-site` |
| `POLIS_CONFIG_DIR` | Absolute path to `.polis/` directory | `/home/user/my-site/.polis` |
| `POLIS_COMMIT_MESSAGE` | Suggested git commit message | `Publish: My First Post` |

### Hook Payload

In addition to environment variables, the same data is passed as JSON on **stdin**:

```json
{
  "event": "post-publish",
  "path": "content/pub.polis.core/post/20260213/my-post.md",
  "title": "My First Post",
  "version": "1",
  "timestamp": "2026-02-13T15:04:05Z",
  "commit_message": "Publish: My First Post"
}
```

### Using Hooks for Deployment

The main use case for hooks is deployment. A typical setup:

1. Your site files are in a git repository
2. You configure a `post-publish` hook that commits and pushes
3. Your hosting provider (Vercel, GitHub Pages, Netlify, etc.) builds from the repository
4. After you click Publish in the webapp, the hook runs and your site updates

The suggested commit messages follow this pattern:
- `Publish: <title>` for new posts
- `Update: <title>` for republished posts
- `Comment blessed: <title>` for blessed comments

### Active Automations Panel

The Settings page shows an **Active Automations** section listing all configured hooks. Each shows its name, description, and a Remove button. If no hooks are configured, the section shows "No automations configured yet."

### Hook Execution Details

- Hooks run in your site directory as the working directory
- Hook failures are logged but do not fail the publish operation — your post is still published even if the hook errors
- Combined stdout and stderr output is captured and logged

---

## Notifications

Notifications tell you when something happens on the discovery network that's relevant to you — someone follows you, comments on your post, or blesses your comment.

### How Notifications Work

1. The webapp syncs with the discovery service every **30 seconds** when at least one browser tab is connected via SSE
2. Events are matched against your **notification rules** to decide what's relevant
3. Matching events are written to a local state file as notification entries
4. The **gateway** icon in the topbar shows a dot when you have unread activity (the old "bell" icon was retired in v4 — see `Top Nav Bar` above)

An immediate sync runs when you first open a browser tab, then repeats every 30 seconds as long as a tab remains open. Sync pauses when all tabs are closed.

### The Notifications View

Click the **gateway** icon (the arc+dots icon, leftmost in the icon row) to load the activity stream — it filters the main stream-screen to your unread/recent network activity. The view shows notifications newest-first, with unread items highlighted.

Each notification shows an icon, a message, and a relative timestamp (e.g., "2 days ago").

When you open the panel, displayed notifications are automatically marked as read. You can toggle between **Show All** and **Unread Only** views.

Clicking a blessing-requested notification navigates you directly to the Blessing Requests view so you can take action.

The panel supports infinite scroll — older notifications load as you scroll down.

### The 9 Default Rules

| Rule | Event | Enabled | Filter | Message |
|------|-------|---------|--------|---------|
| `new-follower` | `pub.polis.follow.announced` | Yes | target_domain | `{{actor}} started following you` |
| `lost-follower` | `pub.polis.follow.removed` | Yes | target_domain | `{{actor}} unfollowed you` |
| `blessing-requested` | `pub.polis.comment.blessing.requested` | Yes | target_domain | `{{actor}} requested a blessing on {{post_name}}` |
| `blessing-granted` | `pub.polis.comment.blessing.granted` | Yes | source_domain | `{{actor}} blessed your comment` |
| `blessing-denied` | `pub.polis.comment.blessing.denied` | Yes | source_domain | `{{actor}} denied your comment` |
| `new-comment` | `pub.polis.comment.published` | Yes | target_domain | `{{actor}} commented on {{post_name}}` |
| `updated-comment` | `pub.polis.comment.republished` | No | target_domain | `{{actor}} updated their comment on {{post_name}}` |
| `new-post` | `pub.polis.post.published` | Yes | followed_author | `{{actor}} published a new post` |
| `updated-post` | `pub.polis.post.republished` | No | followed_author | `{{actor}} updated a post` |

Two rules are disabled by default (`updated-comment` and `updated-post`) to reduce noise from content updates.

### Relevance Filters

Each rule uses a filter to determine which events are relevant to you:

| Filter | Meaning | Example |
|--------|---------|---------|
| `target_domain` | Events targeting your domain | Someone follows you, comments on your post, requests a blessing |
| `source_domain` | Events where your domain is the source | Your comment is blessed or denied by another author |
| `followed_author` | Events from authors you follow | A followed author publishes a new post |

### Enabling and Disabling Rules

Rules are stored in `.polis/ds/<domain>/pub.polis.core/config/notifications.json`. You can edit this file to enable or disable specific rules by changing the `enabled` field:

```json
{
  "rules": [
    {
      "id": "new-follower",
      "event_type": "pub.polis.follow.announced",
      "enabled": true,
      ...
    }
  ]
}
```

### Template Variables

Notification messages support these variables:

| Variable | Description | Example |
|----------|-------------|---------|
| `{{actor}}` | The domain that triggered the event | `alice.com` |
| `{{post_name}}` | The post name (extracted from URL path) | `welcome` |
| `{{source_domain}}` | Domain extracted from source URL | `alice.com` |
| `{{target_domain}}` | Domain extracted from target URL | `bob.com` |
| `{{timestamp}}` | Event timestamp | `2026-02-13T10:30:00Z` |

You can customize message text by editing the `template.message` field in `notifications.json`.

### Muting Domains

To suppress all notifications from a specific domain, add it to the `muted_domains` array in `notifications.json`:

```json
{
  "rules": [...],
  "muted_domains": ["spam.example.com", "bot.example.com"]
}
```

Events from muted domains are silently skipped, regardless of which rules match.

### Notification Files

| File | Path | Purpose |
|------|------|---------|
| Config | `.polis/ds/<domain>/pub.polis.core/config/notifications.json` | Your rules and muted domains (user preferences) |
| State | `.polis/ds/<domain>/pub.polis.core/state/pub.polis.notification.jsonl` | Notification entries (computed, safely deletable) |

The config file survives resets and reflects your preferences. The state file is append-only JSONL — each line is one notification entry. Deleting the state file is safe; notifications will be rebuilt on the next sync.

### Auto-Merging New Rules

When a new Polis release adds new default notification rules, they are automatically merged into your config on the next sync. Your existing rules (including any you've disabled) are preserved. The notification cursor is reset so new rules can process past events.

---

## File Structure

Your Polis site is a directory of files. Understanding the structure helps with troubleshooting and manual operations.

### Overview

```
your-site/
├── .well-known/polis              # Site identity (JSON)
├── .env                           # Runtime config
├── .polis/
│   ├── keys/
│   │   ├── id_ed25519            # Private key (never share)
│   │   └── id_ed25519.pub        # Public key
│   ├── content/pub.polis.core/
│   │   ├── posts/drafts/         # Post drafts (JSON)
│   │   └── comments/
│   │       ├── drafts/           # Comment drafts
│   │       ├── pending/          # Awaiting blessing
│   │       └── denied/           # Rejected comments
│   ├── ds/<discovery-domain>/
│   │   └── pub.polis.core/
│   │       ├── config/           # User preferences (survives resets)
│   │       │   ├── notifications.json
│   │       │   └── feed.json
│   │       └── state/            # Computed data (safely deletable)
│   │           ├── cursors.json
│   │           ├── pub.polis.notification.jsonl
│   │           ├── pub.polis.feed.jsonl
│   │           ├── pub.polis.follow.json
│   │           └── pub.polis.comment.blessing.json
│   ├── logs/                      # Daily logs (YYYY-MM-DD.log)
│   └── webapp/
│       ├── config.json            # UI preferences
│       └── hooks/                 # Hook scripts
│           ├── post-publish.sh
│           ├── post-republish.sh
│           └── post-comment.sh
├── content/pub.polis.core/
│   ├── bundle.json               # Bundle definition
│   ├── index.jsonl               # Public content index
│   ├── post/YYYYMMDD/            # Source posts (markdown)
│   ├── comment/
│   │   ├── YYYYMMDD/             # Source comments (markdown)
│   │   └── blessed.json          # Blessed comments index
│   └── follow/following.json     # Following list
├── posts/                        # Rendered posts (HTML, served publicly)
├── comments/                     # Rendered comments (HTML, served publicly)
├── site/
│   └── snippets/
│       └── about.md              # About section content (editable in webapp)
└── styles.css                    # Active theme CSS (copied from theme)
```

### Config vs State

This distinction is important for troubleshooting:

**Config files** contain your preferences. They survive resets and should be preserved:
- `.polis/webapp/config.json` — UI preferences and hook paths
- `.polis/ds/<domain>/pub.polis.core/config/notifications.json` — notification rules and muted domains
- `.polis/ds/<domain>/pub.polis.core/config/feed.json` — feed display preferences
- `.env` — discovery service credentials and site URL

**State files** contain computed data derived from the discovery service. They can be safely deleted and will be rebuilt on the next sync:
- `.polis/ds/<domain>/pub.polis.core/state/cursors.json` — sync positions
- `.polis/ds/<domain>/pub.polis.core/state/pub.polis.notification.jsonl` — notification entries
- `.polis/ds/<domain>/pub.polis.core/state/pub.polis.feed.jsonl` — feed cache
- `.polis/ds/<domain>/pub.polis.core/state/pub.polis.follow.json` — followers list
- `.polis/ds/<domain>/pub.polis.core/state/pub.polis.comment.blessing.json` — blessing decisions

### Key Files Explained

#### `.well-known/polis`

Your site's identity file. Contains your author name, email, public key, site title, and the directory layout for your site. This file is publicly accessible when your site is deployed.

#### `.env`

Runtime configuration. Key variables:

```
POLIS_BASE_URL=https://your-domain.com
DISCOVERY_SERVICE_URL=https://...
DISCOVERY_SERVICE_KEY=eyJ...
LOG_LEVEL=1
LOG_RETENTION_DAYS=7
```

| Variable | Default | Description |
|----------|---------|-------------|
| `POLIS_BASE_URL` | — | Your site's public URL (required for registration and key rotation) |
| `DISCOVERY_SERVICE_URL` | `https://ds.polis.pub` | Discovery service endpoint |
| `DISCOVERY_SERVICE_KEY` | — | Discovery service API key (optional — public service works without one) |
| `LOG_LEVEL` | `1` | `0` = off, `1` = basic, `2` = verbose |
| `LOG_RETENTION_DAYS` | `7` | How many days of logs to keep |

The `.env` file is searched in order: your data directory, current working directory, then `~/.polis/`. The first one found is used.

#### `.polis/webapp/config.json`

Stores webapp UI preferences:

```json
{
  "view_mode": "list",
  "show_frontmatter": true,
  "hide_read": false,
  "webapp_theme": "dark",
  "setup_wizard_dismissed": true,
  "hooks": {
    "post-publish": ".polis/webapp/hooks/post-publish.sh"
  }
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `view_mode` | `"list"` | `"list"` or `"browser"` |
| `show_frontmatter` | `true` | Show YAML frontmatter in editor |
| `hide_read` | `false` | Hide read items in feed views |
| `webapp_theme` | `"dark"` | `"light"` or `"dark"` — webapp color mode |
| `setup_wizard_dismissed` | `false` | Whether the setup wizard has been dismissed |
| `hooks` | — | Hook script paths by event type |

Note: `log_level` is configured via `LOG_LEVEL` in `.env`, not in this file.

#### `cursors.json`

Tracks your sync position with the discovery service. Each "cursor" is a stream position — a number that says "I've processed all events up to here."

```json
{
  "cursors": {
    "pub.polis.notification": {
      "position": "12345",
      "last_updated": "2026-02-13T14:30:00Z"
    },
    "pub.polis.feed": {
      "position": "12340",
      "last_updated": "2026-02-13T14:25:00Z"
    }
  }
}
```

The cursor name matches the state filename it corresponds to (e.g., cursor `pub.polis.feed` corresponds to state file `pub.polis.feed.jsonl`).

#### `notifications.json` (Config)

Your notification preferences — rules and muted domains. See [Notifications](#notifications) for the full format.

#### `feed.json` (Config)

Controls feed behavior:

```json
{
  "staleness_minutes": 15,
  "max_items": 500,
  "max_age_days": 90
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `staleness_minutes` | `15` | How old the cache can be before a refresh is needed |
| `max_items` | `500` | Maximum items to keep in cache |
| `max_age_days` | `90` | Discard items older than this |

### Content Directories

**`content/pub.polis.core/post/YYYYMMDD/`** — Published post source files as markdown with YAML frontmatter. Frontmatter includes title, publish date, content hash, version history, and Ed25519 signature.

**`content/pub.polis.core/comment/YYYYMMDD/`** — Blessed (approved) comment source files. Same format as posts, with additional `in-reply-to` frontmatter linking to the original post.

**`site/snippets/`** — Global snippets, including `about.md` (your About page content).

**`content/pub.polis.core/`** — Index files:
- `index.jsonl` — index of all published posts and comments
- `comment/blessed.json` — index of blessed comments
- `follow/following.json` — list of authors you follow

---

## Troubleshooting

### "Not configured" errors

**Missing keys**: The webapp needs a keypair at `.polis/keys/id_ed25519` and `.polis/keys/id_ed25519.pub`. If they're missing, re-initialize your site through the welcome screen.

**Missing `.env`**: If your discovery service shows "Not configured", create a `.env` file in your data directory:

```
POLIS_BASE_URL=https://your-domain.com
```

Discovery service credentials are optional — defaults are provided for the public service.

### Discovery service unreachable

Check that your `.env` has a valid `DISCOVERY_SERVICE_URL`. The default public service (`ds.polis.pub`) should work without any configuration. If you're using a custom discovery service, verify the URL and API key.

### Notifications not updating

Notifications sync every 30 seconds, but only when a browser tab is connected via SSE. If they're stuck:

1. Ensure a browser tab is open and connected to the webapp
2. Check that `POLIS_BASE_URL` is set in `.env`
3. Check that your private key exists at `.polis/keys/id_ed25519`
4. Try deleting `.polis/ds/<domain>/pub.polis.core/state/pub.polis.notification.jsonl` and restarting — it will be rebuilt

### Feed shows stale content

The feed refreshes every 30 seconds while a browser tab is open. If it seems stuck:

1. Click the **Refresh** button in the Conversations view
2. Check that you're following at least one author (the feed only shows content from followed authors)
3. Try deleting `.polis/ds/<domain>/pub.polis.core/state/pub.polis.feed.jsonl` — it will be rebuilt

### Hooks not running

Common causes:

- **Not executable**: Run `chmod +x .polis/webapp/hooks/post-publish.sh`
- **Script not found**: Ensure scripts are in `.polis/webapp/hooks/` (not `.polis/hooks/`)
- **Script errors**: Check the terminal output — hook failures are logged but don't prevent publishing
- **Wrong shebang**: Ensure the first line is `#!/bin/bash` (or your preferred shell)
- **Missing tools**: If your hook uses `git`, ensure `git` is on the system PATH

### Posts not appearing after publish

Publishing writes files locally. If your site doesn't update publicly:

1. You need a deployment step (git push, file sync, etc.)
2. Set up a [hook](#hooks--automations) to automate deployment
3. Check your hosting provider's build status

### Rendered pages look wrong after changing theme

After switching themes or updating snippets, use **Settings > Re-render all pages** to force-rebuild all published HTML from source markdown. This is also useful after recovering from any rendering issue.

### "Site not registered" banner

This banner appears when your site isn't registered with the discovery service. Go to **Settings** and click **Register**, or complete the setup wizard.

Registration requires:
- `POLIS_BASE_URL` set in `.env`
- Your site deployed and publicly accessible at that URL
- A valid keypair in `.polis/keys/`

---

## Manual Operations

These are advanced operations for when you need to fix something or work at a lower level.

### Resetting a Cursor

Cursors track how far you've synced with the discovery service. Resetting one forces a full re-sync from the beginning.

**Why**: If notifications or feed items seem wrong or incomplete.

**How**: Edit `.polis/ds/<domain>/pub.polis.core/state/cursors.json` and set the cursor's `position` to `"0"`:

```json
{
  "cursors": {
    "pub.polis.notification": {
      "position": "0",
      "last_updated": "2026-02-13T14:30:00Z"
    }
  }
}
```

**Risk**: The next sync will reprocess all historical events for that cursor. For notifications, this means your entire notification history will be regenerated (existing entries are deduplicated, so you won't get duplicates).

### Deleting State Files

All files in `.polis/ds/<domain>/pub.polis.core/state/` can be safely deleted. They will be rebuilt on the next sync cycle (within 30 seconds of a browser tab being open).

To force a complete rebuild of all state:

```bash
rm -rf .polis/ds/*/pub.polis.core/state/
```

Restart the webapp or open a browser tab — the background sync will regenerate everything on its next cycle.

### Manually Editing Notification Rules

Edit `.polis/ds/<domain>/pub.polis.core/config/notifications.json` directly. You can:

- Disable a rule: set `"enabled": false`
- Change the message template: edit `"template.message"`
- Change the icon: edit `"template.icon"`
- Add muted domains: add to the `"muted_domains"` array

Changes take effect on the next sync cycle.

### Manually Editing Feed Config

Edit `.polis/ds/<domain>/pub.polis.core/config/feed.json` to change:

- `staleness_minutes` — how often the feed should refresh
- `max_items` — how many items to keep
- `max_age_days` — how old items can be before they're pruned

### Clearing Notification History

Delete the state file to remove all notification entries:

```bash
rm .polis/ds/<domain>/pub.polis.core/state/pub.polis.notification.jsonl
```

Optionally reset the cursor too if you want to regenerate from scratch:

```bash
# Edit cursors.json and set pub.polis.notification position to "0"
```

### Rebuilding the Blessed-Comments Index

If your `content/pub.polis.core/comment/blessed.json` is out of date, use the CLI to rebuild it:

```bash
polis index rebuild
```

This scans your content directory and regenerates the index from actual files.

### Downloading Your Site

In Settings, click **Download site** to get a zip archive of your entire site, including keys. Use this for:
- Full backups
- Migrating to a new machine
- Archiving a snapshot of your site

---

## Updating the Webapp

### Current Process

The webapp is distributed as a binary. To update:

1. Download the new binary (or build from source)
2. Replace the old binary with the new one
3. Restart the webapp

### What Happens on Restart

The webapp re-initializes on every startup:

- Configuration is reloaded from disk
- Old-format directories are automatically migrated to the new layout
- New default notification rules (if any) are merged into your config
- Background sync resumes from your last cursor position (when a tab connects)
- No data migration is needed — file formats are forward-compatible

### Checking Your Version

The version is printed when the webapp starts. With the bundled binary, you can also run:

```bash
polis version
```

Or check the startup messages in your terminal for the version number.
