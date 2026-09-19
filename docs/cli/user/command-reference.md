# Polis CLI - Complete Usage Guide

*For* [Writers](../../README.md#writing-on-polis) · [Developers](../../README.md#building-on-polis) — *Kind* [Reference](../../README.md#kinds-of-page) — *Component* [CLI](../README.md) — *Code* [`cli-go/pkg/cmd`](../../../cli-go/pkg/cmd)

> **Note:** This reference covers both CLIs. The **Go CLI** is the recommended choice: it ships as a single binary and has every command below. The **Bash CLI** is feature-frozen (its release version tracks the Go CLI's), has a subset of the commands, and differs in a few shared ones — each section says where. The full record of divergence is [implementation-parity.md](../implementation-parity.md). For the webapp (local web interface), see [../../webapp/user/user-manual.md](../../webapp/user/user-manual.md).

Command-line tool for managing decentralized social content with cryptographic signing and version control.

## Overview

The Polis CLI enables authors to:
- Create and sign posts and comments using Ed25519 cryptography
- Keep a version history of every published file (`.versions/`), independent of git
- Publish content as static files with frontmatter metadata
- Render markdown to static HTML with customizable templates
- Request blessings from discovery services for authenticated comment discovery
- Automate workflows with JSON mode for scripting and CI/CD integration

## Go CLI

The **Go CLI** is the recommended CLI for new users. It implements all commands listed below, ships as a single binary, and needs no jq, curl, OpenSSH or pandoc. (Version history still calls the system
`diff` and `patch`, `cli-go/pkg/version/history.go`; `polis serve` is in the bundled `polis-full` binary only.)

- Same `--json` flag for machine-readable output (the JSON shapes are not always the same as bash's — see [json-mode.md](json-mode.md))
- Same directory structure and file formats — a site written by one CLI is readable and writable by the other
- Download pre-built binaries from the releases page, or build from source: `cd cli-go && go build -o polis ./cmd/polis`

⚠️ **The two CLIs do not have the same commands.** `actor`, `attest`, `did`, `dm`, `license`, `serve`, `site` and `validate` are Go only. Among the shared commands, `comment`, `init`, `post` (reading from stdin), `index --json`, `notifications`, `render`, `unregister` and `discover --since` differ; each section below says how.

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
# Option 1: Add the directory holding the polis script to PATH (quick start)
export PATH="/path/to/dir-containing-polis:$PATH"

# Option 2: Create symlink
sudo ln -s /path/to/polis /usr/local/bin/polis

# Verify installation
polis --help
```

### Option 3: Copy to your content repository

For production use, copy the CLI tools directly into your content repository. This keeps your site self-contained—CLI versions are locked to your content, and everything deploys together.

```bash
# Create your content repository
mkdir my-site && cd my-site
git init

# Copy the CLI script (and, optionally, the upgrade script) into it
mkdir -p bin
cp /path/to/polis ./bin/
cp /path/to/polis-upgrade ./bin/

# Initialize your site
./bin/polis init

# Add bin/ to .gitignore if you don't want to version the CLI
# Or commit it to lock the CLI version with your content
```

**What to copy:**
- `polis` — Main CLI
- `polis-upgrade` — Upgrade script (optional)
- a `themes/` directory **beside the `polis` script**, if you want `init` to install themes from it — bash `init` copies themes from `<script dir>/themes` into `.polis/bundles/pub.polis.core/themes/` and warns if there is none

**Why this approach:**
- Your site is fully self-contained
- CLI version is locked to your content (no surprise breakage from updates)
- Works offline after initial setup
- Easy to deploy—just push your repo

## Quick Start

For a quick introduction, see the [README](../README.md). This guide covers detailed usage.

## Directory Structure

> For a more detailed file structure reference including webapp-specific files, see [Webapp User Manual § File Structure](../../webapp/user/user-manual.md#file-structure).

After running `polis init`, your directory will contain:

```
.
├── .well-known/
│   └── polis                    # Public identity: public key, key history, site title, bundle pointer
├── content/
│   └── pub.polis.core/          # The core bundle's signed source
│       ├── bundle.json          # Declares each content type's directory and public mount
│       ├── index.jsonl          # Content index (one JSON entry per line)
│       ├── post/
│       │   └── 20260106/        # Date-stamped directory (YYYYMMDD)
│       │       ├── .versions/   # Version history for posts in this directory
│       │       │   └── my-post.md
│       │       └── my-post.md   # The signed post
│       ├── comment/
│       │   ├── blessed.json     # Comments you have blessed on your posts
│       │   └── 20260106/reply.md
│       ├── follow/
│       │   └── following.json   # Authors you follow
│       ├── tag/                 # Tag files
│       └── license/             # Your signed terms, once you state some
├── posts/                       # Rendered HTML (the public mount), generated by polis render
│   └── 20260106/my-post.html
├── site/
│   └── snippets/                # Global snippets (override theme snippets)
│       └── about.md
├── policies/rules.jsonl         # Public inbound policy (DMs, comments)
├── index.html                   # Site index (generated by polis render)
├── styles.css                   # Active theme's stylesheet (written on render)
└── .polis/                      # Private — never published (init adds it to .gitignore)
    ├── keys/
    │   ├── id_ed25519           # Private signing key (keep secret!)
    │   └── id_ed25519.pub       # Public verification key
    ├── policies/rules.jsonl     # Private inbound policy (empty by default)
    └── bundles/
        ├── registry.json        # Active theme and shape
        └── pub.polis.core/
            ├── shapes/          # Page templates (v3, v4)
            ├── themes/          # Installed themes
            ├── posts/drafts/
            └── comments/        # drafts/, pending/, denied/
```

Content directories and public mounts come from `content/pub.polis.core/bundle.json`, not from fixed paths. (Output of `polis init` plus one `polis post` and `polis render`, Go CLI.)

## Commands

### `polis init`

Initialize a new Polis directory with keys and metadata.

```bash
polis init
polis init --site-title "My Awesome Blog"
polis init --site-title "My Blog" --license reserved     # Go CLI
```

Registering with the discovery service is a separate step, once the site is deployed: [`polis register`](#polis-register).

**Options (Go CLI):**
- `--site-title <title>` - Set a custom site title for branding (optional)
- `--author <name>` - Author name
- `--email <address>` - Email address (optional)
- `--theme <name>` - Initial theme (default: one is picked at random)
- `--license reserved|open|none` - State terms without being asked (see [`polis license`](#polis-license))

**Options (Bash CLI):** `--site-title`, plus path overrides for the files it creates — `--keys-dir`, `--posts-dir`,
`--comments-dir`, `--snippets-dir`, `--themes-dir`, `--versions-dir`, `--public-index`, `--blessed-comments`,
`--following-index`. The Go CLI accepts none of the path overrides.

**Creates** (Go CLI; bash creates the same layout):
- `.polis/keys/` - Ed25519 keypair for signing
- `.polis/bundles/` - the bundle registry (active theme and shape) and the installed shapes and themes
- `content/pub.polis.core/` - the bundle declaration, `index.jsonl`, `comment/blessed.json`, `follow/following.json`
- `.well-known/polis` - Public identity document (includes a default avatar and the genesis entry of your key history)
- `policies/rules.jsonl` - Default inbound policy
- `site/snippets/about.md` - An example snippet
- `.gitignore` - keeps `.polis/` and `.env*` out of git

**Terms (Go CLI, interactive only):** `polis init` also asks which terms your posts are offered under.
Nothing is pre-selected: pressing enter states no terms and prints how to state some later. A
non-interactive run with no `--license` states nothing. See [`polis license`](#polis-license).

**Rosie (Go CLI, interactive only):**
In an interactive terminal, `polis init` describes Rosie — the helper who approves or turns away comments
using your own rules — and asks whether to switch her on. **Nothing is pre-selected**: only `yes` (or `y`)
switches her on; pressing enter leaves her off. Switched on, `init` writes your own record of her
(`basis: user-signed`) on your site, which needs `POLIS_BASE_URL` set; without it, `init` says to switch her
on later in the web app. Rosie works only while the web app runs (`polis serve`); a site you only manage
from the command line has no Rosie. A non-interactive or `--json` run asks nothing and states nothing, so
`--json` output is unchanged. There is no command to switch her afterwards: use **Settings → Rosie**.
The bash CLI does not ask ([parity](../implementation-parity.md)).

**Default Avatar:**
A default avatar is automatically generated during init with a random background color and white foreground. The avatar displays as a circle with the first letter of your handle. You can customize or remove it later via the webapp settings page (Randomize/Save/Reset buttons).

### `polis post <file>`

Sign and publish a new post from a markdown file.

```bash
polis post my-post.md
polis post --filename hello my-post.md        # Go CLI: choose the published filename
polis post --license open my-post.md          # Go CLI: terms for this post only
```

⚠️ **Go CLI: flags go before the file.** The Go CLI stops reading flags at the first argument that is not
one, so `polis post my-post.md --filename hello` publishes under the default name and ignores the flag.

**What it does:**
1. Computes the SHA-256 hash of the canonicalized body (`current-version`)
2. Writes the frontmatter (title, published, generator, version history, and your terms if you state any) and signs it with your Ed25519 key
3. Writes the signed file to `content/pub.polis.core/post/YYYYMMDD/` and removes the original file
4. Starts its version history in the directory's `.versions/`
5. Appends an entry to `content/pub.polis.core/index.jsonl`
6. Registers the post with the discovery service, when one is configured

**Example output (Go CLI):**
```
[✓] Moved original file into posts/
Published: content/pub.polis.core/post/20260106/hello.md
Title: Hello
Version: sha256:282b4e19...
```

Comments are not published with `post` — see [`polis comment`](#polis-comment).

### `polis republish <file>`

Republish an existing file with updated content (creates new version).

```bash
# Edit your published file
vim content/pub.polis.core/post/20260106/my-post.md

# Republish with new version
polis republish content/pub.polis.core/post/20260106/my-post.md
polis republish posts/20260106/my-post.md      # Go CLI also accepts the public mount form
```

**What it does:**
1. Generates a diff between the old and new body
2. Appends it to the `.versions` file
3. Updates the frontmatter with the new version hash and version history
4. Re-signs the file
5. Updates the file's entry in `index.jsonl` (no duplicate entry)

**Version history:**
- Stored in a `.versions/` subdirectory beside the content file
- Example: `content/pub.polis.core/post/20260106/my-post.md` → `content/pub.polis.core/post/20260106/.versions/my-post.md`
- Bash reads the directory name from `VERSIONS_DIR_NAME` (default: `.versions`); the Go CLI always uses `.versions`
- Diffs are unified diff format (compatible with `diff`/`patch` tools); the Go CLI runs the system `diff` to make them

### Snippets

Snippets are reusable content fragments for templates. Unlike posts and comments, snippets don't require signing - just place plain `.md` or `.html` files in the `site/snippets/` directory.

```bash
# Create a snippet - just write a file
echo "Hi, I'm Alice." > site/snippets/about.md

# Use nested directories for organization
mkdir -p site/snippets/homepage
echo "<footer>© 2026</footer>" > site/snippets/homepage/footer.html
```

**Snippets vs Themes:**
- **Snippets** are content fragments stored in `site/snippets/` (global) or theme directories
- **Themes** are complete styling packages installed in `.polis/bundles/pub.polis.core/themes/`
- Snippets can be included in templates using `{{> path/to/snippet}}`
- Snippet lookup order: **global snippets first, then theme snippets** (author overrides theme)

**Snippet syntax:**
- `{{> about}}` - Default lookup (global → theme), tries .md then .html
- `{{> theme:about}}` - Force theme-first lookup
- `{{> global:about}}` - Explicit global-first lookup
- `{{> about.md}}` or `{{> about.html}}` - Load specific file extension

**File formats:**
- `.md` files are converted to HTML (by pandoc in the Bash CLI; built in to the Go CLI)
- `.html` files are used as-is

**Example snippet (`site/snippets/about.md`):**
```markdown
Hi, I'm Alice. I build things with code and think deeply about
how technology shapes human connection.
```

**Note:** Snippets don't need frontmatter - just the content. If you have existing snippets with frontmatter from older versions, they'll continue to work (frontmatter is stripped during render).

### Publishing from stdin

> **Bash CLI only.** The Go CLI reads a file and has no `-` form and no `--title`.

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

### `polis comment`

Create a comment in reply to a post or another comment (nested threads). ⚠️ **The two CLIs have different
`comment` commands.**

**Bash CLI — `polis comment <file> [reply-to-url]`:**

```bash
# Reply to a post (the file is your reply, without frontmatter)
polis comment my-reply.md https://alice.example.com/posts/20260106/hello.md

# Reply to another comment (nested thread)
polis comment my-reply.md https://bob.example.com/comments/20260105/reply.md

# The URL can also be given with --reply-to; with neither, you are asked for it
polis comment my-reply.md --reply-to https://alice.example.com/posts/20260106/hello.md
```

What it does:
1. Adds `in-reply-to` frontmatter (with `url` and `root-post` fields)
2. Signs the comment and writes it to `content/pub.polis.core/comment/YYYYMMDD/`
3. Registers it with the discovery service and requests a blessing from the post's author

**Nested threads:** When replying to a comment (instead of a post), bash detects this and fetches the original post URL (`root-post`) from the discovery service.

**Go CLI — `polis comment draft | sign | list | sync`:**

```bash
polis comment draft https://alice.example.com/posts/20260106/hello.md
# Created draft: <id>  — write your reply in .polis/bundles/pub.polis.core/comments/drafts/<id>.md
polis comment sign <id>          # sign the draft; it moves to comments/pending/
polis comment list [drafts|pending|blessed|denied]
polis comment sync               # check the blessing status of pending comments and file them accordingly
```

⚠️ **`comment sync` checks status; it does not register a comment with the discovery service or request a
blessing**, and no other Go CLI command does either (`polis blessing beseech` points back at `comment sync`).
A comment drafted and signed with the Go CLI reaches the post's author only through the web app, which
publishes, registers and requests the blessing in one step.

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
- **Which key verified it** — if the site has rotated its key, a post signed before the rotation is
  checked against the key the site's published key history says was current at the post's
  `published:` time, and the output says so:
  ```
  [✓] Signature verified — against a RETIRED key, not the site's current key
      verified against a RETIRED key (epoch 0, current … → …), … CLAIMED signing time … — not against
      the key the site publishes now. The signing time is the artifact's own claim and nothing here checks it.
  ```
  The site's current key is always tried first. See [key-history §4](../../signet/spec/key-history.md).
- Content hash verification (valid/mismatch)
- **Whether a discovery service witnessed it** — read from the site's published witness set, with the
  witness's signature checked against the key its discovery service publishes:
  ```
  [✓] witnessed: a discovery service countersigned these bytes at 2026-09-13T12:00:01.500Z, so they existed by then …
  [i] unwitnessed: no discovery service countersignature covers these bytes. The artifact still verifies …
  [?] a witness is published for these bytes but could not be checked — …
  ```
  ⛔ **None of these is a failure.** An unwitnessed post is a weaker claim, not a broken one, and a
  discovery service that cannot be reached leaves the witness unchecked. **One thing is:** a witness
  the site publishes for these bytes that does **not** verify prints
  `[x] INVALID WITNESS: …` — the site is claiming testimony it does not have, and `polis validate`
  fails `content.witnesses` for it. If a post verified only
  against a **retired** key and its earliest witness is later than that key's retirement, a `[!]` line
  says the witness and the claimed signing time disagree. See
  [witness](../../signet/spec/witness.md).
- For comments: the in-reply-to URL
- The content body

**Use cases:**
- Preview a comment before blessing it
- Preview content before responding to it
- Verify content integrity and authenticity

**JSON mode:** See [json-mode.md](json-mode.md) for response format.

### `polis rebuild`

Regenerate the **content index** (`content/pub.polis.core/index.jsonl`) from the
signed content on disk.

```bash
# Rebuild the post entries
polis rebuild --posts

# Rebuild the comment entries and reconcile the blessing list
polis rebuild --comments

# Rebuild the tag / attestation entries
polis rebuild --tags
polis rebuild --attestations

# Rebuild every content type
polis rebuild --all
```

**Flags (combinable):**

- `--posts` — rebuild the `post` entries
- `--comments` — **two jobs under one flag:** rebuild the `comment` entries of `index.jsonl` from your
  comment files, **and** reconcile `blessed.json` (preserved when readable, recovered only when missing —
  see below). They are different files with different rules; they share the flag because both are "comments"
- `--tags` — rebuild the `tag` entries
- `--attestations` — rebuild the `attestation` entries
- `--all` — every content type above

⭐ **A partial flag touches only its own type's lines.** `index.jsonl` carries
entries for every content type your site publishes, and they arrive by different
routes — a post is appended when you publish it, a comment when you bless it, a tag
or attestation whenever you write one. So
`--posts` replaces the post lines and leaves every other line **byte-identical**,
including types this CLI does not recognise. The result says so:

```
[✓] Rebuild complete!
  Content index: 31 entries
    rebuilt: 8 post
    preserved: 23 comment
```

⚠️ **`--comments` will not rebuild `blessed.json` from the discovery service.**
That file is authored by you, not derived: the DS records *which* comment you
blessed and never *which version*, so rebuilding from it would silently erase the
version pin and with it the "edited since blessing" signal. If the file exists it
is preserved untouched — signature and all. The DS is consulted **only to recover
a file that is missing entirely**, and entries recovered that way carry no version
pin because there was none to fetch. A file that exists but cannot be parsed is
refused rather than overwritten; move it aside if you really want a fresh one.

**What a rebuild will and will not touch:**

| Regenerated | Preserved |
|---|---|
| the entries of the types you named | every other line of `index.jsonl`, byte for byte |
| | `blessed.json` whenever it is readable |
| | your content — rebuild never writes a post, comment, tag or attestation |

A file the rebuild walks but cannot index — a post with no `published`, a tag
with no `current_version` — is skipped and **counted**, not dropped in silence:

```
    skipped: 1 post
```

#### `--notifications` (deprecated)

`polis rebuild --notifications` still works and prints a pointer. It never
rebuilt anything: it *deletes* your local notification state, and nothing
anywhere can put it back. The verb is now
[`polis notifications clear`](#polis-notifications-clear).

⚠️ It is **no longer part of `--all`**, for the same reason.

**Use when:**
- The index is out of sync with what is on disk
- You manually edited or restored published files
- Tailor (the self-hosted repair tool, see [Upgrading](#upgrading)) reports `index-rebuild`

### `polis index [--json]`

View the content index in JSONL or JSON format (read-only, outputs to stdout).

```bash
# View as JSONL (default)
polis index

# View as JSON
polis index --json

# Count posts
polis index | grep -c '"type":"post"'

# View recent 10 entries
polis index | tail -10 | jq

# Extract all post titles (Go CLI)
polis index --json | jq -r '.data.entries[] | select(.type == "post") | .title'

# Extract all post titles (Bash CLI)
polis index --json | jq -r '.posts[].title'
```

**What it does:**
- Reads `content/pub.polis.core/index.jsonl` (the Go CLI finds it through the bundle declaration)
- Default: outputs JSONL (one entry per line)
- `--json`: the Go CLI wraps every entry in `{"status", "command", "data": {"entries", "count", "skipped", "skipped_lines"}}`; the Bash CLI groups entries into `{"version", "posts", "comments"}` and drops entries of any other type
- A line that is not a JSON object is left out and **reported** by the Go CLI (`skipped` and `skipped_lines`, or a warning on stderr), never dropped in silence. The Bash CLI prints the file as-is without `--json`, and with `--json` fails on the whole file (a `jq` parse error, non-zero exit)
- Read-only operation - never modifies the index file

**Use cases:**
- Debugging index contents
- Scripting and automation
- Inspecting published content metadata

### `polis extract <file> <version-hash>`

Reconstruct a specific version of a file from version history.

```bash
# Get specific version
polis extract content/pub.polis.core/post/20260106/my-post.md sha256:abc123...

# Output to file
polis extract content/pub.polis.core/post/20260106/my-post.md sha256:abc123... > old-version.md
```

⚠️ **Known defect (Go CLI):** when a file is edited in place and then republished, the Go CLI records an
empty diff for the new version, so extracting an *intermediate* version returns later text. The first
version and the current version extract correctly.

### Starting Fresh

To completely reset your Polis installation and start over, move or remove the following files/directories, then run `polis init`:

- `.polis/` - Configuration and signing keys
- `.well-known/` - Public identity
- `content/` - Signed posts, comments, index, follow file and blessing list
- `posts/`, `comments/` - Rendered HTML
- `policies/`, `site/` - Policy and snippets

**Example:**
```bash
rm -rf .polis .well-known content posts comments policies site
polis init
```

**Note:** This will generate new signing keys. If you want to keep your identity, back up `.polis/keys/` before removing.

### `polis version`

Print the CLI version number.

```bash
polis version
```

**Example output:**
```
polis <version>
```

### `polis about`

Display site details, the CLI version, the public key, discovery registration status, and directory paths.

```bash
polis about
polis --json about
```

**Example output (Go CLI):**
```
[i] Polis CLI version: <version>

=== Site Information ===
  Author: alice
  Created: 2026-01-06T12:00:00Z
  Site Title: My Blog
  Base URL: https://alice.example.com
  Theme: zane
  Posts: 3
  Comments: 1
  Following: 2

=== Public Key ===
  ssh-ed25519 AAAAC3Nza...

=== Discovery Service ===
  URL: https://ds.polis.pub
  Status: registered
  Registered: 2026-01-06T12:05:00Z

=== Directories ===
  ...
```

**Registration status (Go CLI):** `registered`, or `not registered` — which is also what a failed check
reports. With `POLIS_BASE_URL` unset, no check is made and the status is blank. The Bash CLI lays the
output out differently (SITE, VERSIONS, NOTIFICATIONS, CONFIGURATION sections) and includes metadata file
versions.

**JSON mode:** Returns structured data with all sections. See [json-mode.md](json-mode.md) for the full JSON response format.

### `polis register`

List your site in the public directory. Registration makes your site discoverable to other authors and allows you to participate in conversations across the polis network.

```bash
polis register
```

**Features:**
- **Idempotent** (Bash CLI) - Running on an already-registered site shows current status
- **Attestation verification** (Bash CLI) - Verifies the discovery service's signature on your registration
- **Automatic metadata** - Sends the author name from `.well-known/polis` if set; your email is never sent

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

The example output above is the Bash CLI's. The Go CLI prints `Site registered: <domain>` and `Registry URL: …`,
and also reconciles your follows to the discovery service.

**Requirements:**
- `POLIS_BASE_URL` must be set (domain is extracted from this); `DISCOVERY_SERVICE_URL` defaults to `https://ds.polis.pub`
- Your `.well-known/polis` must be accessible via HTTPS

**JSON mode:** Returns registration details including `service_attestation` for verification.

### `polis unregister [--force]`

Unregister your site from the discovery service. The service removes your site's registration record, and
with it the key history it holds for your domain. ⚠️ **Records of your content, your relationships and your
stream events are not deleted** by unregistering.

⚠️ **Today this fails for a site that has published content:** the service cannot remove the key history while
content records refer to it, and returns a server error. Nothing is removed and the site stays registered (the
local marker is kept, because the CLI removes it only after the service confirms).

```bash
# Bash CLI: interactive confirmation required
polis unregister

# Bash CLI: skip confirmation (for scripting)
polis unregister --force
```

⚠️ **The Go CLI asks for no confirmation** and has no `--force` flag: `polis unregister` unregisters immediately.

**Warning displayed (Bash CLI):**
```
WARNING: Unregistering will remove your site from the public directory.

This means:
  - Your site will no longer be publicly listed or discoverable
  - Other authors cannot find or interact with your content through the network
  - You can re-register anytime to rejoin the community

Are you sure you want to unregister example.com? (type 'yes' to confirm)
```

**Effects of unregistering:**
- Your site's registration record (and the key history the service holds for it) is deleted — ⚠️ currently only for a site that has published no content
- Write operations (publish, comment, bless, follow announcements) are blocked until you register again
- The service's records of your content, relationships and stream events remain
- You can rejoin anytime with `polis register`

**JSON mode:** Skips interactive confirmation automatically.

### `polis render [--force]`

Render your signed posts and comments to static HTML using the active shape and theme.

```bash
# Render all posts and comments (skips up-to-date files)
polis render

# Force re-render all files
polis render --force

# Bash CLI: render without snippet markers
polis render --no-markers
```

**What it does:**
1. On first render, if no theme is active, selects one at random from the installed themes
2. Converts each post and comment to HTML — with pandoc in the Bash CLI, built in to the Go CLI (which also refreshes the installed shapes and themes first)
3. Applies the active shape's templates and the active theme's CSS
4. Writes each page under the type's public mount (`posts/YYYYMMDD/my-post.html`, `comments/YYYYMMDD/reply.html`)
5. Writes `styles.css` and `index.html` at the site root (the v4 stream shape also writes its scripts and a sitemap)
6. Skips files whose HTML is newer than the source (unless `--force`)

**Requires (Bash CLI only):** pandoc (install with `apt install pandoc` or `brew install pandoc`)

**Example output (Go CLI):**
```
Rendered 1 posts, 0 comments
Generated index.html
```

#### Themes

Polis installs its themes into `.polis/bundles/pub.polis.core/themes/`; the list, including the two reserved for
polis's own pages, is in [Themes → What ships today](../../general/concepts/themes.md#what-ships-today). To change themes:

- **Webapp**: Open **Settings**, choose a theme from the **Site Theme** dropdown and click **Change Theme**. The site re-renders.
- **CLI**: set `active_theme` in `.polis/bundles/registry.json` to the fully-qualified name (for example `pub.polis.themes.vice`), then run `polis render --force`. The Go CLI's `polis init --theme <name>` sets it at creation.

⚠️ A theme picked at random by the CLI may be one of the reserved two.

For template syntax, variables and snippets, see [templating.md](templating.md).

#### Embedded Source (Bash CLI)

Each HTML file the Bash CLI renders ends with the source's frontmatter in an HTML comment:

```html
<!--
=== POLIS SOURCE ===
Source: https://alice.example.com/posts/20260106/hello.html
title: Hello World
published: 2026-01-06T12:00:00Z
current-version: sha256:abc123...
signature: U1NIU0lH...
=== END POLIS SOURCE ===
-->
```

Only the frontmatter is included, not the body. To verify a post, fetch its signed source under
`content/` — see [Signature Verification](#signature-verification).

#### Snippet Markers (Bash CLI)

By default, bash `polis render` injects hidden markers around snippet content to enable in-browser snippet editing in the webapp. Each snippet inclusion (`{{> snippet-name}}`) is wrapped with:

```html
<!-- POLIS-SNIPPET-START: global:snippet-name path=snippet-name -->
<span class="polis-snippet-boundary" data-snippet="global:snippet-name"
      data-path="snippet-name" data-source="global" hidden></span>
<!-- actual snippet content -->
<!-- POLIS-SNIPPET-END: global:snippet-name -->
```

**Marker behavior:**
- The hidden `<span>` provides a DOM anchor for JavaScript without affecting layout
- `data-snippet` contains the snippet identifier (source:name format)
- `data-path` contains the snippet path as written in the template, without its `global:`/`theme:` prefix
- `data-source` indicates "global" (from `site/snippets/`) or "theme" (from theme)
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

List pending blessing requests for your posts. Each request is identified by the comment's version hash
(`sha256:…`), which is the argument `grant` and `deny` take.

```bash
polis blessing requests
```

**Example output (Go CLI):**
```
Pending blessing requests (1):

  Version: sha256:9f2a...
  Author: bob.example.com
  Reply to: https://alice.example.com/posts/20260106/hello.md
  Comment URL: https://bob.example.com/comments/20260106/reply.md
```

The Bash CLI prints the same requests as a table, and runs `blessing sync` first.

#### `polis blessing grant <hash>`

Approve a pending blessing request by the comment's version hash.

```bash
polis blessing grant sha256:9f2a...
```

The Go CLI also accepts the comment's URL in place of the hash (the Bash CLI accepts a short hash).

**What it does:**
1. Finds the pending request, which carries the comment URL and the post it replies to, then updates the discovery service's record of it to granted
2. Adds the comment to `content/pub.polis.core/comment/blessed.json`, signed with your key
3. Comment becomes visible to your audience

#### `polis blessing deny <hash>`

Reject a pending blessing request by the comment's version hash.

```bash
polis blessing deny sha256:9f2a...
```

**What it does:**
1. Updates the discovery service's record of the request to denied
2. Comment remains on author's site but won't be amplified

#### `polis blessing beseech <hash>`

Re-request blessing for a comment you wrote, by its version hash (retry after changes).

```bash
polis blessing beseech sha256:9f2a...
```

**Use when:**
- Original request failed
- You updated the comment and want to re-request

⚠️ **Bash CLI only in practice.** The Go CLI checks that the discovery service knows the comment and then
prints a pointer to `polis comment sync`, which does not re-request a blessing (see [`polis comment`](#polis-comment)).

#### `polis blessing sync`

Copy blessings recorded at the discovery service for your posts into your local `blessed.json`.

```bash
polis blessing sync
```

**What it does:**
1. Fetches the blessed comments on your posts from the discovery service
2. Compares them with `content/pub.polis.core/comment/blessed.json`
3. Adds any missing entries (e.g., comments blessed from another device while you were offline)

**When to use:**
- After being offline for a while
- To ensure the local file matches the discovery service

**Example output:**
```
[✓] Synced 3 comment(s) to blessed-comments.json
```

### `polis follow <author-url>`

Follow an author. Their comments waiting on your posts are blessed now, and the default rules bless their future ones.

```bash
polis follow https://alice.example.com
```

**What it does:**
1. Blesses this author's comments on your posts that are pending or were denied (your own act, signed with your key)
2. Adds the author to `content/pub.polis.core/follow/following.json`, signed with your key
3. Future comments from this author match `bless pub.polis.comment from following` — decided by your own site when Rosie runs there (the web app); a CLI-only site decides nothing automatically

The Bash CLI also announces the follow to the discovery service (`pub.polis.follow.announced`), so the author
can see it; the Go CLI's `follow` does not announce. Following from the web app does.

**Example output:**
```
[✓] Successfully followed https://alice.example.com
  - Added to following.json
  - Blessed 3 comment(s)
```

### `polis unfollow <author-url>`

Stop following an author and hide all their comments.

```bash
polis unfollow https://alice.example.com
```

**What it does:**
1. Denies every comment from this author that you had blessed, at the discovery service (nuclear option)
2. Removes the author from `content/pub.polis.core/follow/following.json`

**Warning:** This is a destructive action - all previously blessed comments from this author will be hidden.

### `polis notifications`

List notifications about activity on your site and from authors you follow, along with blessing requests
waiting on your posts.

```bash
# List unread notifications (default)
polis notifications

# Include notifications already read
polis notifications list --all

# Bash CLI: show only some types
polis notifications list --type version_pending

# JSON output
polis --json notifications
```

The two CLIs keep notifications in different places. The Go CLI (and the web app) reads the state it
syncs from the discovery service, `.polis/ds/<discovery-domain>/pub.polis.core/state/pub.polis.notification.jsonl`;
which events become notifications is set by the `notifications` rules in `.polis/bundles/registry.json`
(new posts from authors you follow, comments and blessing requests on your posts, blessings of your
comments, new and lost followers). The Bash CLI reads `.polis/notifications.jsonl`, where the only
notification it records itself is `version_pending` (the CLI was upgraded and metadata files need `polis rebuild`). Marking notifications
read happens in the web app; neither CLI has a command for it.

#### `polis notifications clear`

> **Go CLI only.**

Delete this site's local notification state.

```bash
polis notifications clear
```

⚠️ **This is a delete and nothing can undo it.** No source anywhere holds a copy
of your read/unread state, so there is nothing to restore it from. It used to
live under `polis rebuild --notifications`, which is why that flag still works
and points here.

### When a comment is blessed without you

Registering a comment never blesses it: the discovery service records the request as pending. Comments are blessed automatically only by **your own site**, applying your rules, when Rosie is switched on there (the web app — localhost, `serve`, or polis.pub). A CLI-only site has no Rosie, so every request waits for `polis blessing grant`. With the default rules, three scenarios bless automatically — your own comments on your own posts (`bless pub.polis.comment from self`), and these two:

**1. Global Trust (Following)**
When you follow an author, ALL their future comments on ANY of your posts are blessed.

```bash
# Follow Alice - her comments on your posts are now blessed by your site
polis follow https://alice.example.com
```

**2. Thread-Specific Trust**
When you have blessed a comment from an author, their future comments *on the same post* are blessed. Your site answers this from its own `blessed.json`, keyed by the thread's root post. This allows trust to be scoped to specific conversations.

```
Example:
1. Bob comments on your "Intro to Polis" post
2. You bless Bob's comment (polis blessing grant sha256:…)
3. Bob comments again on "Intro to Polis" → blessed by your site
4. Bob comments on your "Advanced Polis" post → NOT blessed (different post)
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
| `config` | `polis dm config` | Show the DM rules in your public and private policy files |
| `decrypt` | `polis dm decrypt [--phrase] [--conversation <id>] [--json]` | Decrypt and print your messages from a local or exported site |
| `publish-key` | `polis dm publish-key` | Re-publish your DM messages key into `.well-known/polis` (repair) |

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

**Policy:** DM acceptance is controlled by policy rules — the public `policies/rules.jsonl` and the private `.polis/policies/rules.jsonl`. By default, `allow pub.polis.dm from following` + `deny pub.polis.dm from all` (in the public file) restricts DMs to followed domains. See [policies.md](policies.md) for common recipes and the [policy grammar](../../general/reference/policy-grammar.md) for every verb and source. Use `polis dm config` to view current rules.

All subcommands support `--json` for machine-readable output.

### `polis tag`

Manage tags on content. Tags are lightweight labels you apply to posts and feed items for personal organization.

```bash
polis tag <subcommand> [options]
```

**Subcommands:**

| Subcommand | Usage | Description |
|------------|-------|-------------|
| `list` | `polis tag list` | List your tags, with how many targets each has |
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

`apply`, `remove` and `delete` also bring the tag's line in `index.jsonl` up to date, so anyone
reading your index can find the tag without you running `polis rebuild`. ⚠️ **The Bash CLI's tag commands
do not** — a tag changed with bash keeps a stale index line until a Go CLI write or `polis rebuild --tags`.

### `polis clone <url> [target-dir]`

Copy someone else's live polis site to a local folder, to **read and analyse it offline** — for example
with [`polis validate ./alice`](#polis-validate). ⚠️ **A clone is not a way to serve their content or
settings as your own.** It carries no keys and no policies, and it is not your site.

**Usage:**
```bash
polis clone https://alice.polis.pub               # target dir derived from domain
polis clone https://alice.polis.pub ./alice       # explicit target dir
polis clone --full https://alice.polis.pub        # re-download all content
polis clone --diff https://alice.polis.pub        # only download changes (default after first clone)
```

⚠️ **Go CLI: flags go before the URL.** After the URL, `--full` is read as the target directory.

**Flags:**
- `--full` — re-download all content, ignore cached state
- `--diff` — only fetch new/changed content (default if the target was previously cloned)

If neither flag is given the clone package decides based on whether a clone-state file exists in the target.

**What a clone contains.** The site's identity document, stored as the exact bytes it published;
every bundle declaration it names, at the path it names; posts and comments from its index; the
follow file and the blessing list; the signed licence document if the site points at one; and
`.well-known/did.json` if it publishes one. **Content directories come from the site's own bundle
declaration**, so a site that moved a directory clones correctly rather than appearing empty.

It fetches the **canonical** path (`content/…/post/…/slug.md`), never the rendered mount (`/posts/…`).
The canonical path holds the signed source; the mount serves a projection of it, and what you verify
is the source.

**What a clone cannot contain, and says so.** `pub.polis.tag` and `pub.polis.attestation` records are
flat files, found only through the source's `index.jsonl` — HTTP does not list directories. A clone
collects every one the index lists. When the index lists none of a type, the clone reports that type
under *"Not collected"* rather than omitting it silently: an index is only as fresh as the site's last
write or heal, so **no entries means nothing indexed, never nothing exists**, and a later
[`polis validate`](#polis-validate) must not look like it found nothing wrong with artifacts nobody
fetched. To check one you know the address of, use `polis validate <url>`.

**A clone never carries `.polis/`** — no private key, no salt, no keyring. So a clone is a real site
directory with genuinely reduced visibility, and `polis validate ./alice` reports the owner-only
checks as NOT CHECKED rather than passed.

**A clone never writes outside its folder.** Every path it writes comes from the source site — index
entries, bundle declarations, the licence pointer — so each is checked first. An absolute path, a path
that climbs out (`../`), or one that would leave through a symlink already inside the folder is skipped
and reported (*"Refused"*, or `rejected_paths` in JSON mode); the rest of the clone continues.

### `polis rotate-key`

Rotate your site's Ed25519 signing key. Generates a fresh keypair, signs a handover with the **old**
key, tells the discovery service, publishes the new key in `.well-known/polis` — and **appends the
handover to your site's own key history**, so what you signed under the old key stays verifiable from
your site alone (with two exceptions, below).

**Usage:**
```bash
polis rotate-key
```

⭐ **This is why rotating is safe.** Your site publishes `public_key_history` beside `public_key`:
every key you have held, when it was current, and the signature the previous key made handing
authority to the next. Anyone can walk that chain from your current key back to your first one, resolve
a post you signed two years ago to the key that was current when you signed it, and verify it — **with
no service to ask.** The same walk resolves an attestation by its `asserted` time and a tag file or
licence by its `updated` time. ⚠️ **The exceptions are your follow file and your blessing list:** they
carry no signing time, so they verify against the current key only, and read as not verifying until your
next follow/unfollow or blessing decision re-signs them.

Nothing to do to enable it. `polis init` writes the first entry; each rotation appends one.

**No old key is kept, and there is no `--delete-old-key`.**

Earlier versions moved the retired private key to `id_ed25519.old` and offered a flag to skip that.
Both are gone. The single fixed filename was a poor substitute for a chain — rotate twice and the
second rotation overwrote the first backup, so your earliest key was simply gone. The published
history replaces it, and it keeps the **public** halves, which is what a verifier actually needs. A
spare copy of a retired private key was never what made old signatures checkable; it was only
something to lose. ⚠️ An `.old` file already on disk is left alone; nothing deletes it for you, and
nothing new is written.

**Effect on your DID document:** `.well-known/did.json` now carries every key you have held — retired
keys stay in `verificationMethod` so a resolver can still verify what they signed, and drop out of
`assertionMethod`, which names only the key that speaks for you now. The Go CLI and the webapp
republish it automatically. The bash CLI **removes** it and tells you to run `polis did --write`,
because it has never written that file. Either way the rotation itself always succeeds: rotating a
compromised key is a security operation and is never blocked by a derived file. A **stale** document is
worse than an absent one — it answers `200` with a key you no longer hold, and a resolver cannot tell.

**Check it afterwards:**
```bash
polis validate                              # verifies the chain from your directory
polis validate https://yoursite.example     # verifies it the way a stranger would
```

**Your old posts keep verifying.** After a rotation, `polis validate` and `polis preview` check a
post first against your new key and then, if that fails, against the key your published history
says was current at the post's `published:` time — and they say when that is what happened
(*"1 of them against a RETIRED key, at a claimed signing time"*). ⚠️ From a **directory**, this needs
your `.well-known/did.json`, because the handover signatures are made over your domain and nothing
else in the directory states it; without one, `polis validate` reports the old posts as not verifying
and says why. `polis validate https://yoursite.example` has no such gap.

**The rotation is witnessed.** When your site is registered, the discovery service countersigns the
rotation and `rotate-key` records that countersignature inside your new key-history entry. `polis
validate` then verifies it (`identity.key_history_witness`), which dates the rotation independently of
anything your site says about itself. No witness — an unregistered site, or a discovery service that
issued none — is a weaker claim, never an error.

⚠️ A witness cannot show a rotation that was **left out** of the chain, so `polis validate` still says
it did not compare your chain against the discovery service's own record. That comparison runs in the
hosted fleet sweep, and there is no self-hosted equivalent. Saying so is deliberate: a clean report
that silently skipped it would claim more than it checked.

Must be run from a polis site directory. Full format:
[`docs/signet/spec/key-history.md`](../../signet/spec/key-history.md).

### `polis license`

State, change, or withdraw the terms under which your posts may be used. Terms are **signed with your
key** and **materialised into each post's frontmatter at publish time**, so they travel with the work
when it is quoted, mirrored, or scraped — unlike `robots.txt`, which stays behind on the server.

With no argument it reports the current terms without changing anything.

**Usage:**
```bash
polis license                          # show the terms this site currently states
polis license reserved                 # the recommendation (see below)
polis license open                     # anyone may use the work for anything
polis license none                     # withdraw: publish no terms from here on
```

**Profiles:**

| Profile | Machine values | Means |
|---------|----------------|-------|
| `reserved` | `train-ai=n search=y ai-input=n attribution=required` | Read and quote freely with a link back. Search engines may index your work and send people to it. AI training and answer-engine summaries require asking. |
| `open` | `train-ai=y search=y ai-input=y` | Anyone may use your work for anything, including AI training. |
| `none` (`unstated`) | — | Publish no terms. Readers fall back to their own assumptions. |

A profile is a **name for a selection** from other people's vocabularies — IETF AIPREF for the
preference layer (`train-ai`, `search`), RSL for the licence-terms layer (`ai-input`, `attribution`).
Polis writes no licence text of its own.

⚠️ **Terms are not retroactive.** Posts already published keep the terms they were signed with —
stating or withdrawing terms applies from here on. Nothing rewrites your archive, and nothing should:
re-signing old work would assert you said something at a time you did not.

**Absent is a defined state, not a gap.** A site that states nothing has *not said* — which is
different from permitting or denying. Stating nothing is a legitimate choice, and it is where every
site starts: `polis init` **asks** but pre-selects nothing, so pressing enter states no terms and
tells you how to state some later. A non-interactive `polis init` with no `--license` states nothing,
and a site created by hosted signup on polis.pub states nothing until its author chooses.

After stating terms, run `polis render` to regenerate `robots.txt`, `rsl.xml`, and your public terms
page from the signed source. Those surfaces are always **generated** from `license.json` and never
authored beside it.

**`polis license none` cleans up after itself.** Withdrawing removes the signed licence, the pointer
to it, *and* the surfaces generated from it — `robots.txt`, `rsl.xml`, and the terms page. Afterwards
your site is indistinguishable from one that never stated terms, which is the point: an empty
`robots.txt` would be a statement of its own, and a terms page left standing would go on asserting
terms you no longer state. No re-render is needed. Anything of your own in the terms directory is left
alone.

**JSON mode:** `polis --json license` and `polis --json license <profile>` both emit the resulting
terms — see [JSON Mode](json-mode.md#polis-license).

Must be run from a polis site directory. Stating terms needs your private key; `POLIS_BASE_URL`
supplies the `terms` and `contact` URLs written into the licence.

**Related:** [Set your terms](../../signet/guides/set-your-terms.md) (guide) ·
[the licence spec](../../signet/spec/license.md) (format and wire surfaces)

### `polis attest`

Make a signed claim about **someone else's** work or identity. Everything else polis signs is about
your own content; this is the one that points outward — *on this date, I asserted this about that.*

One claim, one file, one signature, published at a permanent URL that anyone can fetch and check
against your published key. Issuing or withdrawing also adds the record to your `index.jsonl`, which
is how someone who does not already know a record's URL can ask your site what it has attested.

**Usage:**
```bash
polis attest <subcommand> [options]
```

**Subcommands:**

| Subcommand | Syntax | Description |
|------------|--------|-------------|
| `issue` | `polis attest issue --predicate <p> --subject <id> [options]` | Issue a new attestation |
| `list` | `polis attest list` | List attestations this site has issued, oldest first |
| `show` | `polis attest show <id>` | Show one attestation and its signature status |
| `verify` | `polis attest verify [id]` | Verify issued attestations against the site's published key |
| `withdraw` | `polis attest withdraw <id>` | Retract a claim you issued. **Writes a new record and deletes nothing** — the withdrawn claim stays published and still verifies |
| `register` | `polis attest register <id>` | Announce a record this site **already issued** to the discovery service — one written without it (`polis actor register`'s disclosures), or whose registration at issue time was skipped. **Refuses, and says why**, when no discovery service is configured, the site is not registered with it, the record's issuer is another site, or its signature does not verify. Safe to repeat |

**`issue` options:**

| Flag | Description |
|------|-------------|
| `--predicate <p>` | What is being asserted. **Fully qualified**, always — `pub.polis.attestation.same-as`, not `same-as` |
| `--subject <id>` | What the claim is about — an `https` URL |
| `--subject-type <t>` | `uri` (a work) or `identity` (a party). Default: `uri` |
| `--subject-version <h>` | Pin a `uri` subject to exact bytes: `sha256:` + 64 hex |
| `--payload k=v` | Predicate-specific detail. Repeatable; values are strings; only the first `=` separates |
| `--asserted <ts>` | RFC 3339 with a `Z`. Default: now |

**Reserved predicates:**

| Predicate | Subject | What it says |
|-----------|---------|--------------|
| `pub.polis.attestation.same-as` | identity | this identity and that one are the same party |
| `pub.polis.attestation.integrity` | identity | an integrity **observation** — ⛔ the finding is in the payload: `result` (`verified` \| `not-verified`), `vantage` and `observed` are **required**, and `issue` refuses the record without them. A reader that cannot read the payload must not interpret it. See [the spec, §4.1](../../signet/spec/attestation.md#41-integrity-is-an-observation-and-its-result-lives-in-the-payload) |
| `pub.polis.attestation.correction` | uri + pin | this specific version of this work is corrected |
| `pub.polis.attestation.used-under-terms` | uri | I used this work, under these terms, on this date |
| `pub.polis.attestation.agent-disclosure` | identity | this identity is an automated agent, operated by X, scoped to Y |
| `pub.polis.attestation.endorsement` | identity | I vouch for this party |
| `pub.polis.attestation.withdrawal` | uri + pin | the record at this URL is retracted by its issuer — ⛔ **issued only by `polis attest withdraw`**; `issue` refuses it |
| `pub.polis.attestation.custody` | identity | an **operator** declares it holds this site's identity key — payload `holds=identity-key` and `attribution=as-tenant\|co-signed` required. See [custody §12](../../signet/spec/custody.md) |
| `pub.polis.attestation.custody-grant` | identity | a site grants custody of its key to an operator — payload `scope=custodial` and `basis=hosting-terms\|user-signed` required (example under [`polis actor`](#polis-actor)) |
| `pub.polis.attestation.grant` | identity (your own site) | you grant a user agent the behaviours it names — payload `agent`, `provider`, `behaviours` (e.g. `rosie/1`) and `basis` required; `issue` refuses a grant about any other site. See [delegation](../../signet/spec/delegation.md) |

The list is **reserved, not a registry.** A predicate of your own — `com.yourdomain.reviewed` — is
issued, published and verified exactly like the reserved ones, and readers that do not recognise it
must still verify and display it. The one exception on the write side is `withdrawal`: `issue`
refuses it, because only `withdraw` checks that the claim is yours and not already withdrawn.

**Examples:**
```bash
# vouch for someone
polis attest issue --predicate pub.polis.attestation.endorsement \
  --subject https://maya.example --subject-type identity

# a correction pinned to exact bytes, so an edit cannot move it
polis attest issue --predicate pub.polis.attestation.correction \
  --subject https://site.example/posts/20260901-claim.md \
  --subject-version sha256:9f2a... \
  --payload "note=the figure cited was revised by the source"

polis attest list
polis attest show 20260828T235009Z-f0be117c4e29310d
polis attest verify
polis attest withdraw 20260828T140200Z-3f2a9c1d4e5b6a70
```

⚠️ **Pin anything you claim about a mutable work.** Without `--subject-version` your claim is about a
URL, and the URL's contents can change under it. With one, the claim is permanently scoped to the
bytes you actually saw — and the pin is inside your signature, so it cannot be repointed.

**`verify` reports four states**, and they are different facts: `valid`, `unsigned` (there is no
signature — a fact, not a problem), `invalid` (there is one and it does not verify — worth chasing),
and `unknown` (it could not be checked). The command exits non-zero **only** for `invalid`.
A record signed before a key rotation is checked against the key your site's published history says
was current at its `asserted` time; when a retired key verified it, `verify` and `show` say so (`retired`
and `signature_key` in `--json`).

⚠️ **There is no `delete`.** Withdrawing a claim is a signed record saying so, never a file deletion —
a `404` read as a retraction would make every outage, moved site and lapsed domain read as one too,
and the evidence would be gone.

**JSON mode:** every subcommand supports `--json` — see [JSON Mode](json-mode.md#polis-attest-issue).

Must be run from a polis site directory. Needs your private key, and `POLIS_BASE_URL`, which supplies
the `issuer` — a claim has to say who made it.

**Related:** [Make a claim about someone else's work](../../signet/guides/attest.md) (guide) ·
[the attestation spec](../../signet/spec/attestation.md) (wire format)

### `polis actor`

**For an operator that runs system actors.** Most sites never need this.

Software that acts on the network — a verifier, a cache custodian, a repair process — needs an
identity of its own, or it borrows a tenant's and the network cannot tell the difference. `polis
actor` publishes the operator's signed list of the actors it runs: which domains are ours, whose
authority each exercises, and what we **expect** each to do.

⛔ **EXPECTED, NEVER ALLOWED.** This is not an allow-list and nothing enforces it — the operator holds
its actors' keys, so no published list can stop one doing anything. What it buys is that **anyone can
notice** when an actor does something outside it. The remedy is social.

⭐ **A self-hoster running their own actors is an operator**, and everything here works the same for
them — ⚠️ **except becoming one.** `polis actor register` only updates a site that is already an
operator; it does not turn a site into one. Setting up a new operator is not available from this
command for now.

**Usage:**
```bash
polis actor <subcommand> [options]
```

**Subcommands** — run from the **operator's** site:

| Subcommand | Syntax | Description |
|------------|--------|-------------|
| `register` | `polis actor register <domain> --authority <a>` | Add or update an actor **on a site that is already an operator**. Writes the registry, records a permanent disclosure, and announces it. ⚠️ **On any other site it does nothing, and says why** |
| `withdraw` | `polis actor withdraw <domain>` | Remove an actor. **Issues a withdrawal record** — a removal without one reads as drift forever |
| `list` | `polis actor list` | Show the registry |
| `verify` | `polis actor verify [--tenants-dir <d>]` | Check the registry's signature, and each entry's countersignature |
| `announce` | `polis actor announce <domain>` | Publish `pub.polis.actor.registered` for an actor **already** in the registry, and nothing else. Refuses unless the registry verifies against this site's key and the pointer is published |

Run from an **actor's** site:

| Subcommand | Syntax | Description |
|------------|--------|-------------|
| `declare-operator` | `polis actor declare-operator <domain>` | Name the operator accountable for this actor |

Run from **anywhere** — no site, no key, nothing but HTTPS:

| Subcommand | Syntax | Description |
|------------|--------|-------------|
| `verify` | `polis actor verify <action-url-or-file> [--operator <domain>]` | **The stranger's check** ([`custody.md` §7](../../signet/spec/custody.md#7--how-a-stranger-checks--with-nothing-but-curl-and-a-signature-verifier)). Given one signed attestation: does it verify against its signer's key, does the operator list the signer, did the actor countersign its entry, and is the action among its expected actions |

⛔ **Three different findings, never one verdict.** A **lookalike** — the operator does not list the
signer; conclusive. An invalid **countersignature** — the operator changed the entry after the actor
agreed to it. A **deviation** — a listed actor did something off its list; ⚠️ **information, not a
blocked operation**, because nothing enforces the registry. An absent countersignature is a weaker
claim and is not a finding, and neither is a fetch that failed.

`--operator <domain>` asks that operator instead of the one the signer claims — which is how a
lookalike that simply claims nobody is caught. A signer that publishes its own registry is checked
against it. The action may be a URL **or a file**: a record that was never published is still signed.
Only `pub.polis.attestation` records are accepted today. Exits `1` when there is any finding.

| Subcommand | Syntax | Description |
|------------|--------|-------------|
| `verify --custody` | `polis actor verify --custody <domain> [--operator <domain>] [--ds <url>]` | **A site's own check of custody** ([`custody.md` §12.5](../../signet/spec/custody.md#125--how-the-tenant-checks--and-what-is-honestly-independent)) — run it from your own machine. *What does this helper do?* — the operator's signed declarations about the site, verified against the operator's key. *What did I allow?* — the site's custody grants, verified against its own key, withdrawals followed. *Who did this?* — the site's events at a discovery service, with who signed each and under what authority, as the service recorded them; those signed with the site's key split into **by you** and **by an agent under a grant** (the agent marker, each act listed with the grant it cites) |

`--operator` names the operator whose declarations to read; without it, the operator the site's newest
standing grant names is used, and with neither, no declaration is read. `--ds` defaults to
`DISCOVERY_SERVICE_URL`, else `https://ds.polis.pub`. ⚠️ **It verifies records, not events:** the event
list is the discovery service's own recording, and the service holds only recent events. The only
findings are declarations or grants whose signatures do not verify — a missing declaration, a withdrawn
grant or an unreachable service are notes. Exits `1` when there is any finding.

⛔ **It makes custody visible; it does not reduce it.** An operator can withhold records from this
check but cannot forge one that passes.

To **grant** custody of your own key, or withdraw a grant, use `polis attest`:

```bash
polis attest issue --predicate pub.polis.attestation.custody-grant \
  --subject https://polis.polis.pub --subject-type identity \
  --payload scope=custodial --payload basis=user-signed
polis attest withdraw <id>
```

Withdrawing adds a signed withdrawal and writes a `withdrawn_by` pointer onto the grant; it deletes
nothing.

**`register` options:**

| Flag | Description |
|------|-------------|
| `--authority <a>` | **Required.** `operator` — the actor acts on the operator's own authority. `user` — it exercises a tenant's, and may do so only under that tenant's grant |
| `--expects <type>` | A fully-qualified action type expected of this actor. Repeatable. **Descriptive, never permissive** |
| `--countersign-with <dir>` | The actor's own site directory. Its key countersigns the entry, and its `operator` pointer is written |
| `--no-attest` | Skip the permanent disclosure record |
| `--no-announce` | Skip the network announcement |

**One command, three artifacts** — the same fact at three lifetimes:

| Artifact | Lifetime | Job |
|---|---|---|
| The registry **file** | current state | *what is true now* — one fetch |
| An **`agent-disclosure` attestation** per actor | **permanent** | *what was claimed, and when* |
| A **`pub.polis.actor.registered`** stream event | ~90 days | **delivery** — it tells people who were not looking |

⛔ **The event is not authoritative.** It carries a pointer, not the facts. It says *"go look"*; the
file says what is true.

**The countersignature is the interesting part.** An entry the actor has countersigned says *"and the
actor agrees to this scope"*, and ⭐ **the operator cannot then widen it unilaterally** — widening
breaks the countersignature until the actor re-signs. Narrowing and removal need no consent, because
you must always be able to disown a broken actor.

**Examples:**
```bash
# From the operator's site
polis actor register judge.polis.pub --authority operator   --expects pub.polis.attestation.integrity   --countersign-with /data/tenants/judge

polis actor list
polis actor verify --tenants-dir /data/tenants
polis actor withdraw judge.polis.pub

# Announcement LAST: register without it, verify the file live, then announce.
# Re-running register instead would write a second permanent disclosure record
# and announce "reregistered" where the first announcement means "registered".
polis actor register judge.polis.pub --authority operator --no-announce
polis actor announce judge.polis.pub

# From the actor's own site, when it lives elsewhere
polis actor declare-operator polis.polis.pub
```

⚠️ **An entry names a DOMAIN, not a key.** Domains survive rotation; keys do not. The actor's own
`.well-known/polis` carries its current key.

⚠️ **`declare-operator` writes a CLAIM, not a fact.** A site can name any operator it likes; what
makes it mean something is that operator's registry listing the domain back.

**JSON mode:** every subcommand supports `--json`.

Must be run from a polis site directory. Needs the site's private key and `POLIS_BASE_URL`, which
supplies the `operator` origin a verifier fetches.

**Related:** [an operator's actors, and what it says about them](../../signet/spec/custody.md)
(spec) · [the actor roster](../../general/concepts/actors.md)

### `polis did`

Show the site's `did:web` identifier and DID Document. Polis publishes the site's public key twice —
as an OpenSSH line in `.well-known/polis`, and as a W3C DID Document at `.well-known/did.json` — so a
polis site is also a resolvable `did:web` identity that anyone can look up or issue a credential to.

The document is written automatically by `polis init` and refreshed by `polis rotate-key` — which
also adds any retired keys to `verificationMethod` while leaving `assertionMethod` naming only the
current one; this
command exists to read it, and to repair it.

**Usage:**
```bash
polis did                              # print the DID, its URL, and the document
polis did --write                      # (re)generate .well-known/did.json
polis did --host alice.example         # override the host (default: from POLIS_BASE_URL)
```

**Flags:**
- `--write` — write the document to `.well-known/did.json`
- `--host <host>` — canonical host to build the DID from; defaults to the host in `POLIS_BASE_URL`

The `On disk` line reports whether the published document still matches the site's current key. A
stale document is the one failure a resolver cannot see — it answers `200` with a key the site has
retired — so if it says *missing or stale*, run `polis did --write`.

Requires a canonical host: set `POLIS_BASE_URL` or pass `--host`. Must be run from a polis site
directory.

### `polis validate`

⚠️ **Go CLI only.** The bash CLI has no `validate` command.

Check a polis site — yours or anyone's — across five families: **signed content integrity**, **index
consistency**, **policy file parseability**, **key/handle alignment** and **bundle registry health**.
Every check is the same code the hosted actors (Patrol, Medic, Judge) run on the fleet, not a
reimplementation that agrees with them.

**Two forms, and neither one writes anything:**

```bash
polis validate                                  # this directory
polis validate ./alice                          # any directory — including a clone
polis validate https://alice.example            # a whole site, over the network
polis validate https://alice.example/content/pub.polis.core/post/20260101/hello.md
polis validate https://alice.example/content/pub.polis.core/attestation/20260115T100000Z-3f2a9c1d4e5b6a70.json
polis validate --json https://alice.example
```

The shape of the argument selects the form: a path (or nothing) checks a directory, a URL fetches.
A URL that addresses one artifact checks just that artifact — two fetches, the artifact and the
site's `.well-known/polis` for its key, with the key cached per domain so many artifacts on one site
still cost one key fetch.

The artifact may be either signing family. A **post or comment** is checked against the key of the
site that served it. A **JSON record** — an attestation, tag file, follow file, blessing list or
licence — is checked by its own package's predicate against the key of the site that **signed** it:
an attestation's `issuer`, otherwise the host that served it. An actor registry is not one of them;
check it with [`polis actor verify`](#polis-actor). Where the record carries a `current_version` that this command can recompute (attestations
and tag files), `content.hash` checks it.

**A record signed before a key rotation still verifies.** Like a post, a JSON record is checked
against the signing site's current key first, then against the key the site's published history says
was current at the record's **claimed** signing time — an attestation's `asserted`, a tag file's or a
licence's `updated` — and the output says when a retired key did it. ⚠️ A **follow file** and a
**blessing list** carry no signing time, so no retired key can be selected for them: they verify
against the current key only, and for a site that has rotated, one signed before the rotation reads
as not verifying and the output says why. (Rewriting either file re-signs it with the current key.)

**It never clones.** Cloning is [`polis clone`](#polis-clone-url-target-dir)'s job, and the two
compose:

```bash
polis clone https://alice.example ./alice
polis validate ./alice
```

**Every check reports one of four outcomes — and NOT CHECKED is the point.**

| | |
|---|---|
| **passed** ✓ | the check ran and found nothing wrong |
| **failed** ✗ | the check ran and found something wrong |
| **warning** ! | the check ran and found something about the site's **surroundings** — see below |
| **NOT CHECKED** – | the check did not run, and the output says why |

A clean result means *"I checked and it was fine"* — never *"I did not check."* What can be checked
depends on **what is there**, not on which form you used: your own site has `.polis/`, so key
permissions and private policies can be examined; a clone or a remote site does not, so those come
back NOT CHECKED with the reason spelled out. Validating a clone can never look identical to
validating your own site.

**Unsigned is not a failure.** Most artifacts on most sites carry no signature, and absence of terms
means terms were never stated. Only *present-and-failing* is a finding — but a passing check still
tells you how many artifacts were unsigned, because *"12 verified"* and *"12 verified, 3 unsigned"*
are different facts.

**Index consistency covers every entry type the index carries** — posts, comments, tags and
attestations, each hashed by its own rule. A clean result names what it checked, and an entry of a
type with no rule is reported as not checked rather than skipped. **A line of `index.jsonl` that does
not parse is a failure naming its line number**, in both forms — never skipped, so a half-garbage index
cannot read as clean over the half that parses. Likewise a tag or attestation file that does not parse
fails `content.tags` / `content.attestations`, by name.

**The two forms mean the same thing by the same check name.** A directory and a URL run the same
checks over the same site, and a check both can run gives the same verdict — `go test` compares
them on every build. What only one form can see is said in the other:

| Only a directory | Only a URL |
|---|---|
| **phantoms** — a file on disk the index does not list (HTTP does not list directories) | **what the edge serves** — `content.license_robots`, `content.license_rsl` |
| key files, key permissions, private policies, the bundle registry | |

Those come back NOT CHECKED in the form that cannot see them, with the reason — never passed.

A **site-wide** URL run verifies the **attestations and tag files the site's index lists**, each
against the key of the site that signed it, and says how many it could not check (a listed record
that does not serve, or whose issuer's key could not be read). It can only find what the index lists:
an index is only as fresh as the site's last write or heal, so a count of zero means *nothing
indexed*, never *nothing exists*.

A file that is **not there** is NOT CHECKED, not passed: a site with no `following.json` or
`blessed.json` has nothing wrong with it, and nothing was verified either.

#### Does the public surface still say what you said?

A URL run additionally fetches your **public** `robots.txt` and `rsl.xml` and compares them to your
**signed** licence. This is the one question nothing else can answer: the hosted Judge fetches over
loopback, upstream of any CDN, so an intermediary that rewrites `robots.txt` on the public wire is
invisible to it. `polis validate <url>` is the only form that sees what the world sees.

It reports three things, and the middle one is not what most people expect:

1. **Presence** — your own generated section is in the served file, intact.
2. **Precedence** — ⚠️ *not* whether your section is present, but whether it is **consulted**. Under
   RFC 9309 a crawler obeys the group whose `User-agent` match is most specific. A third party adding
   `User-agent: GPTBot` / `Disallow: /` does not sit alongside your `User-agent: *` group; it
   **replaces** it for that agent. Your directives can be in the file and never read.
3. **Direction** — which way a third party moved your terms, judged against what you **signed**, not
   against the file's own text.

⭐ **A note about a restrictive intermediary is NOT a problem to fix.** Many hosts (Cloudflare among
them) inject a managed block that blocks AI crawlers. If you reserved your terms, that block moves
them the same way you did: it is reported in full, as a **passed** check whose detail says *"Aligned,
and NOT a problem to fix."* You do not need to do anything, and there is nothing to escalate.

The same block against a site whose terms are **open** is a **warning** — the edge is refusing what
you granted. So is any third-party directive that **grants what your licence refuses**, which is the
serious direction: somebody answering a licence question on your behalf.

⛔ **Warnings never fail the run and polis repairs nothing here.** A managed `robots.txt` usually
belongs to your host, not to you, and polis will not tell you what your terms should be. This is a
report.

**Exit status:** `0` when nothing checked was found wrong, `1` when something failed or the run could
not start. Neither NOT CHECKED nor a warning affects the exit status — one is an honest gap, the other
is a fact about your surroundings. Only **failed** is a defect in the site.

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
- `--since <date>` — **Bash CLI only**: show items since a date instead of since the last check

Uses `DISCOVERY_SERVICE_URL` (default `https://ds.polis.pub`). Reads `following.json` to determine who to query.

### `polis unpublish <path>`

Unpublish a post or comment — a *clean break* operation that severs all ties between the published identity and the content. Differs from `unregister` (which removes the site's registration) and from deleting the file (which leaves DS state behind).

**Usage:**
```bash
polis unpublish content/pub.polis.core/post/20260201/my-post.md
polis unpublish content/pub.polis.core/comment/20260201/comment-id.md
polis unpublish -y <path>                            # Go CLI: skip confirmation
polis unpublish --url <discovery-service-url>        # Go CLI: remove one discovery-service registration only
```

**Flags (Go CLI; flags go before the path):**
- `-y` — skip the confirmation prompt
- `--url <url>` — unpublish that exact URL at the discovery service and touch no local file — for a stale or duplicate registration
- `--type pub.polis.post|pub.polis.comment` — the content type for `--url`, when it cannot be inferred from the URL

**Semantics:** Post unpublish cascades blessing state in the DS (blessed → orphaned, pending → denied). Comment unpublish resets the comment's blessing to `pending`. Republishing later is treated as a brand-new publication — orphaned blessings are NOT restored. See [Unpublish Lifecycle](../../ds/developer/unpublish-lifecycle.md) for full state transition rules.

### `polis site`

> **Go CLI only.**

Edit your site's identity document, and recover a signed file a newer polis wrote.

```bash
polis site set author-name "Alice Example"
polis site set avatar --bg '#8766aa' --fg '#ffffff' --pattern rings
polis site set avatar --clear
polis site rewrite-unsigned content/pub.polis.core/follow/following.json
```

- `site set author-name` and `site set avatar` change `.well-known/polis` and keep every other member of it
  (avatar flags: `--bg`, `--fg`, `--border`, `--border-w 0-3`, `--pattern none|rings|cross|grid|dots|stripes|diamond|halves`,
  `--pattern-color`, `--clear`).
- `site rewrite-unsigned <path>` is the escape hatch for a signed file carrying a field this version of polis
  does not recognise. Every command that would rewrite such a file refuses rather than drop the field and re-sign.
  This one rewrites it, drops what it cannot read, and leaves the file **unsigned** — it never signs. See
  [signing base §6.2](../../signet/spec/signing-base.md).

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

Published files carry YAML frontmatter. A post, as the Go CLI writes it (captured from `polis post` and
`polis republish` in a fresh site):

```yaml
---
title: Hello
published: 2026-09-16T17:25:59Z
updated: 2026-09-16T17:26:05Z            # only after a republish
generator: polis-cli-go/0.67.0
current-version: sha256:82acffe7...
version-history:
  - sha256:37386c0a... (2026-09-16T17:25:59Z)
  - sha256:82acffe7... (2026-09-16T17:26:05Z)
signature: U1NIU0lHAAAAAQAAADMAAAALc3No...
---

# Hello

The post body follows the frontmatter.
```

A `license:` block sits before `signature:` when the site states terms (see [`polis license`](#polis-license)).
A comment adds `type: comment`, a nested `in-reply-to` block and, written after signing, `author`:

```yaml
in-reply-to:
  url: https://bob.example.com/content/pub.polis.core/post/20260105/intro.md   # immediate parent
  root-post: https://bob.example.com/content/pub.polis.core/post/20260105/intro.md
```

- **There is no URL field on a post.** Its canonical URL is `POLIS_BASE_URL` + its path.
- `signature` is stored **unarmored** (bare base64 of an SSHSIG); see [Signature Verification](#signature-verification).
- Which of these fields the signature covers is defined, per type, by the
  [signing-base spec](../../signet/spec/signing-base.md) — not by this page.

## Version History

Every published file has a sibling history file at `.versions/<filename>` in the same directory, created
by the **first** `polis post` and appended to by each `polis republish`. The first version is stored in
full; each later one as a unified diff of the body against its parent. `polis extract` reconstructs any
version from it.

**Example** (captured from a post published once and republished once):
```
# VERSION_FILE_FORMAT=1.0
# CANONICAL_FILE=content/pub.polis.core/post/20260916/hello.md
# CURRENT_HASH=sha256:82acffe7...

[VERSION sha256:37386c0a...]
TIMESTAMP=2026-09-16T17:25:59Z
PARENT=none
FULL_CONTENT_START
# Hello

First body.
FULL_CONTENT_END

[VERSION sha256:82acffe7...]
TIMESTAMP=2026-09-16T17:26:05Z
PARENT=sha256:37386c0a...
DIFF_START
…unified diff…
DIFF_END
```

⚠️ **Known defect (Go CLI):** when the published file is edited in place and then republished, the Go CLI
writes an **empty** diff (the example above came out with nothing between `DIFF_START` and `DIFF_END`), so
an intermediate version cannot be reconstructed. See [`polis extract`](#polis-extract-file-version-hash).

The bash CLI names the history directory from `VERSIONS_DIR_NAME` (default `.versions`); the Go CLI always
uses `.versions`.

## Configuration

Both CLIs read settings from **environment variables**, filled in from a **`.env` file** for any variable
not already set, then fall back to **built-in defaults**. Directory layout is fixed by the bundle convention
(`content/pub.polis.core/…`); `.well-known/polis` no longer carries a `config` section, and nothing reads one.

### Environment Variables

```bash
# Your site's URL — needed by publishing, blessing, following, rendering and anything that names your domain
export POLIS_BASE_URL="https://yourdomain.com"

# Discovery service (optional — defaults to https://ds.polis.pub)
export DISCOVERY_SERVICE_URL="https://ds.polis.pub"

# Optional bearer token sent to the discovery service when set; requests are authenticated by your signature
export DISCOVERY_SERVICE_KEY="your-api-key"
```

**Bash CLI only** — directory overrides, read at startup by the bash `polis` script; the Go CLI ignores them:

```bash
export KEYS_DIR=".polis/keys"
export POSTS_DIR="content/pub.polis.core/post"
export COMMENTS_DIR="content/pub.polis.core/comment"
export VERSIONS_DIR_NAME=".versions"
```

Add to `~/.bashrc` or `~/.zshrc` for persistence.

### Using a `.env` File

Both CLIs load **one** `.env` file, never overriding a variable already set in the environment:
1. `.env` in the current working directory, if present — per-site configuration
2. otherwise `~/.polis/.env` — shared across sites

```bash
# Per-site config (in your polis site directory)
echo 'POLIS_BASE_URL=https://alice.example.com' > .env

# Or shared config
mkdir -p ~/.polis
echo 'DISCOVERY_SERVICE_URL=https://ds.polis.pub' > ~/.polis/.env
```

The bash CLI's `init` also writes a `.env.example` template; the Go CLI does not.

Example `.env`:
```bash
POLIS_BASE_URL=https://alice.example.com
DISCOVERY_SERVICE_KEY=eyJhbGciOiJI...
```

**Security Note:** Never commit `.env` files containing secrets. The `.gitignore` that `polis init` writes
ignores `.env*` — including the bash CLI's `.env.example` — and all of `.polis/`.

### Site Title

Set a custom site title for branding in rendered HTML and comment attribution:

```bash
polis init --site-title "My Awesome Blog"
```

The site title is stored as `site_title` in `.well-known/polis` and used:
- In HTML page titles and headers (`{{site_title}}` template variable)
- When displaying your comments on other people's posts
- In `polis about` output

If `--site-title` is omitted, the Go CLI's `init` uses the author name (`cli-go/pkg/site/init.go`); the bash
CLI writes no `site_title`.

### Directory Paths

Directory paths are **fixed** by the bundle convention: content lives under `content/pub.polis.core/`
(`post/`, `comment/`, `follow/`, `index.jsonl`), keys under `.polis/keys/`. `.well-known/polis` has no
`config` section any more, and neither CLI reads one. ⚠️ The bash CLI's `init --posts-dir` / `--comments-dir`
/ `--keys-dir` / `--versions-dir` flags and its directory environment variables still change where it
writes, but nothing else — the Go CLI, the web app, the hosted service, validation — looks anywhere but the
fixed paths, so a site initialised with them is not portable.

## JSON Mode

Commands support `--json` for machine-readable output. The response shapes differ between the CLIs:

```bash
polis --json post my-post.md | jq -r '.version'             # Go CLI
polis --json post my-post.md | jq -r '.data.content_hash'   # Bash CLI
```

See [json-mode.md](json-mode.md) for response schemas, errors, and scripting examples.

## Publishing Workflow

### 1. Write Content
```bash
vim my-thoughts.md
```

### 2. Publish Locally
```bash
polis post my-thoughts.md
# signs it and moves it to content/pub.polis.core/post/<YYYYMMDD>/my-thoughts.md
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

### 5. Blessings (if commenting)

With the bash CLI, `polis comment` sends the blessing request. With the Go CLI it does not — see
[`polis comment`](#polis-comment). To look after requests on **your own** posts:

```bash
polis blessing requests           # pending requests on your posts
polis blessing grant <version>    # bless one (the comment's sha256:… version hash)
polis blessing deny <version>
```

## Common Use Cases

### Creating a Blog Post

Write `why-decentralization-matters.md` in your editor, then:

```bash
polis post why-decentralization-matters.md
```

Commit and deploy the site as in the workflow above.

### Replying to Someone's Post

**Bash CLI** — write the reply to a file, then sign it and send the blessing request in one step:

```bash
polis comment my-reply.md https://alice.example.com/content/pub.polis.core/post/20260106/hot-take.md
```

**Go CLI** — draft, write, sign:

```bash
polis comment draft https://alice.example.com/content/pub.polis.core/post/20260106/hot-take.md
# edit the draft file it names, then:
polis comment sign <id>
```

Either way no editor is opened for you, and the comment stays pending until the post's author blesses it.

### Updating a Post
```bash
# Edit the published file directly
vim content/pub.polis.core/post/20260106/my-post.md

# Republish as a new version
polis republish content/pub.polis.core/post/20260106/my-post.md
```

### Viewing Version History
```bash
# Every version of the file
cat content/pub.polis.core/post/20260106/.versions/my-post.md

# Reconstruct a specific version
polis extract content/pub.polis.core/post/20260106/my-post.md sha256:abc123...
```

## Security Notes

### Private Key Protection
- **Never commit `.polis/keys/id_ed25519`** (private key)
- The `.gitignore` that `polis init` writes already ignores all of `.polis/`; keep it that way
- Public key (`.polis/keys/id_ed25519.pub`) is safe to share — it is published in `.well-known/polis`

### Signature Verification

Anyone can verify a published post with stock OpenSSH — no polis software required.
**The commands below were run end-to-end against a live post on 2026-09-16.**

> ⛔ **`ssh-keygen -Y verify -f` takes an `allowed_signers` FILE, not a bare public
> key.** Passing the key directly fails with a bare `Could not verify signature.`,
> which reads like a bad signature and is not. This page carried that broken form
> until 2026-09-08; so did `polis.pub/llms.txt`, where an external reviewer hit it.
>
> ⛔ **The `signature:` in frontmatter is stored UNARMORED.** `ssh-keygen` will not
> parse it until the PEM header and footer are put back — otherwise it reports
> `Couldn't parse signature: missing header`.

```bash
POST=https://vdibart.polis.pub/content/pub.polis.core/post/20260828/this-post-carries-its-own-terms.md
SITE=https://vdibart.polis.pub
curl -sS "$POST" -o post.md

# 1. allowed_signers — principal, key type, key. NOT the raw .public_key line.
curl -sS "$SITE/.well-known/polis" \
  | jq -r '"polis " + .public_key' | cut -d' ' -f1-3 > allowed_signers

# 2. re-armor the bare base64 signature into an SSHSIG PEM
sed -n 's/^signature: //p' post.md | fold -w 70 \
  | sed '1i -----BEGIN SSH SIGNATURE-----' \
  | sed '$a -----END SSH SIGNATURE-----' > sig.pem

# 3. the signing base: drop the TOP-LEVEL `signature:` line from the frontmatter
#    block only, then canonicalize. A `signature:` line in the BODY is signed.
awk 'NR==1&&$0=="---"{fm=1;print;next} fm&&$0=="---"{fm=0;print;next}
     fm&&/^signature:/{next} {print}' post.md | sed 's/[ \t]*$//' > raw.txt
printf '%s\n' "$(cat raw.txt)" > base.txt

ssh-keygen -Y verify -f allowed_signers -I polis -n file -s sig.pem < base.txt
# Good "file" signature for polis with ED25519 key SHA256:HpxqLj3Mq0Fh/hstf7L0+375HsrEQuo0Xx3UpzAb6uQ
```

**Isolate a failure before blaming the signature.** `current-version` is the SHA-256
of the canonicalized *body*, and checking it needs no cryptography at all:

```bash
sed '1,/^---$/d' post.md | sed 's/[ \t]*$//' | sed '/./,$!d' > body.raw
printf '%s\n' "$(cat body.raw)" | sha256sum
grep '^current-version:' post.md
```

If those disagree, your canonicalization is wrong and step 3 would have failed for
that reason rather than because the signature is bad.

### File Content Integrity

Each published file (`.md`) — **both posts and comments** — carries two integrity fields in its frontmatter:

- **`current-version`** is `sha256:` + the SHA-256 of the canonicalized **body alone** — not the frontmatter.
  (Checked on a fresh post: hashing the body after the closing `---` reproduces it exactly; the recipe is under
  [Signature Verification](#signature-verification).)
- **`signature`** is an Ed25519 SSH signature over the canonicalized frontmatter **and** body, minus the
  fields written after signing: `signature` itself for a post, and `signature` and `author` for a comment.
  So `title`, `published`, `current-version`, `license`, `in-reply-to` and the body are all covered.

⛔ The exact bytes — canonicalization, which lines are stripped and how — are specified in the
[signing-base spec](../../signet/spec/signing-base.md) §4, and nowhere else. Build a verifier from that
page, not from this summary.

### What Happens If You Edit Without Republishing

If you manually edit a published post or comment and deploy without running `polis republish`:

| Change Made | Hash Valid? | Signature Valid? | Consequence |
|-------------|-------------|------------------|-------------|
| Edit body text | ❌ No | ❌ No | Verification fails |
| Edit `title`, `published` or any other signed frontmatter | ✅ Yes (the hash covers the body only) | ❌ No | Verification fails |
| Edit `current-version` | ❌ No | ❌ No | Verification fails |
| Trailing whitespace on a line, or trailing blank lines | ✅ Yes | ✅ Yes | Canonicalization removes them before hashing and signing |

(Checked on a fresh post: editing only `title:` leaves the content hash intact and makes `polis validate`
report `content.posts … signature does not verify`.)

**Practical consequences of editing without republishing:**

1. **`polis preview <url>`** prints `[x] Signature INVALID - content may have been tampered with`, and for a
   body edit also `[x] Content hash MISMATCH`
2. **Other polis users** see the content as unverified
3. **New blessing requests** may fail verification
4. **Version history** becomes inconsistent (the current hash no longer matches the content)

**Safe fields to change:** None. Any edit to a published `.md` file requires `polis republish` to:
- Recompute the content hash
- Re-sign with your private key
- Update the version history

**Files you CAN safely edit:**
- Rendered `.html` files (not signed, regenerated by `polis render`)
- Theme/template files
- Configuration files

### What Happens If You Change POLIS_BASE_URL

⚠️ **Neither CLI has a command for moving a site to a new domain.** There is no `polis migrate`.

A post's canonical URL is **not stored in the file** — it is `POLIS_BASE_URL` + the file's path, and
`index.jsonl` and `blessed.json` record paths, not URLs. So after a change:

| What | Status | Why |
|------|--------|-----|
| Signatures and content hashes on your posts | ✅ Still valid | no URL of your own is inside them |
| Rendered HTML, `sitemap.xml`, `.well-known/did.json` | ⚠️ Stale | they name the old host — run `polis render` and `polis did --write` |
| Your key history, if you have ever rotated | ❌ Does not verify under the new domain | each handover signature is made over the domain (`site.VerifyChain`) |
| Discovery service records | ❌ Stale | registered under the old domain |
| Other people's links, and their comments' `in-reply-to` | ❌ Broken | they point at the old URLs, inside *their* signatures |

**Bottom line:** your content stays cryptographically valid, but the network still knows you by the old
domain, and nothing in polis moves it for you.

## Terminal User Interface (polis-tui)

> **Deprecated** — The TUI is deprecated as of v0.46.0. Use the [webapp](../../webapp/user/user-manual.md) instead for an interactive interface.

## Upgrading

**Go CLI / self-hosters:** use **Tailor** to bring an existing site up to the current spec — `tailor --apply` runs in one pass for any historical layout, defaults to a dry-run diagnosis, and writes a timestamped backup before changing anything. See [actors.md](../../general/concepts/actors.md#tailor). (Tailor migrates *site data and layout*; it does not download binaries — the Go CLI ships as a single binary you replace directly.)

**Bash CLI:** `polis-upgrade` is the bash-specific tool for both site migrations and binary self-update (downloading updated `polis`/`polis-tui` binaries). Run `polis-upgrade --help` for usage.

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

- 25 top-level commands (`about` … `version`)
- Subcommands for `blessing`, `dm`, `notifications` and `tag`
- Per-command flags, and the global `--json` flag

⚠️ **The completion scripts lag the CLI** (`completions/polis.bash`, `completions/polis.zsh`). They do not
offer `actor`, `attest`, `did`, `license` or `site`, nor `dm decrypt` / `dm publish-key`; and they still
offer flags neither CLI accepts — `follow`/`unfollow --announce`, `rotate-key --delete-old-key`,
`init --register` — plus bash-only flags (`init --posts-dir` and friends, `discover --since`,
`unregister --force`) that the Go CLI rejects or ignores.

## Troubleshooting

### "ssh-keygen: command not found"
Install OpenSSH client (see Installation section above).

### "jq: command not found"
Install jq JSON processor (see Installation section above).

### "pandoc is required for rendering"
Bash CLI only. Install pandoc to use `polis render`: `apt install pandoc` (Linux) or `brew install pandoc` (macOS). The Go CLI renders without it.

### "No such file: .polis/keys/id_ed25519"
Run `polis init` to create keys and directory structure.

### "Index file is corrupted or missing"
Run `polis rebuild --all` to regenerate `index.jsonl` from the published files on disk.

### Version history missing
A `.versions/<filename>` history is created by the first `polis post` and extended by each `polis republish`.
If one is missing, the file was not published through polis, or its history was deleted.

## Next Steps

- Deploy your content to GitHub Pages, Netlify, or any static host
- Read [security-model.md](../../general/security/security-model.md) for the full cryptographic model and threat analysis
- Template syntax for themes: [templating.md](templating.md)
- Try the [webapp](../../webapp/user/user-manual.md) for a visual interface

## Support

For issues, questions, or feature requests, please file an issue in the GitHub repository.

## License

AGPL-3.0 — see [LICENSE](../../../LICENSE) at the repository root.
