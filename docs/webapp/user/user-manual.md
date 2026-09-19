# Polis Webapp User Manual

*For* [Writers](../../README.md#writing-on-polis) — *Kind* [Guide](../../README.md#kinds-of-page) — *Component* [Webapp](../README.md) — *See also* [concept](../../general/concepts/infinity-stream.md) · [guide](../../signet/guides/set-your-terms.md)

This guide covers the Polis webapp — a local web interface for managing your Polis site. If you use the command-line tool instead, see [CLI Command Reference](../../cli/user/command-reference.md).

There are two ways to run Polis:

- **Self-hosted (this guide's main focus).** The webapp runs on *your* computer, modifies files on your disk, and opens in your browser. There are no accounts, no passwords, and no database — your files are the source of truth.
- **Hosted (e.g. `you.polis.pub`).** A managed Polis space you sign into from any browser. Here there *is* an account, tied to your email and accessed with magic links (no passwords). If you arrived from a "Sign in to polis" or "Welcome to polis" email, see [Hosted Accounts & Signing In](#hosted-accounts--signing-in).

**Core Features** and **Social Features** work the same in both modes. **Settings** differs a little: a hosted space has an **Account** section and no hooks or discovery-service configuration, because the host runs those for you.

---

## Table of Contents

1. [Getting Started](#getting-started)
2. [Hosted Accounts & Signing In](#hosted-accounts--signing-in)
3. [Layout & Navigation](#layout--navigation)
4. [Core Features](#core-features)
5. [Social Features](#social-features)
6. [How the Webapp Differs from a Hosted Web App](#how-the-webapp-differs-from-a-hosted-web-app)
7. [Deploying Your Site](#deploying-your-site)
8. [Security](#security)
9. [Settings](#settings)
10. [Hooks & Automations](#hooks--automations)
11. [Notifications](#notifications)
12. [File Structure](#file-structure)
13. [Troubleshooting](#troubleshooting)
14. [Manual Operations](#manual-operations)
15. [Updating the Webapp](#updating-the-webapp)

---

## Getting Started

### Starting the Webapp

There are two binaries that can run the webapp:

**Standalone binary** (`polis-server`):

```
polis-server                        # Uses current directory as your site
polis-server --data-dir /path       # Use a specific directory
polis-server -d /path               # Short form
polis-server --port 8080            # Bind a fixed port instead of a free one
```

**Bundled binary** (`polis-full`):

```
polis serve                         # Uses current directory as your site
polis serve --data-dir /path        # Use a specific directory
polis serve -d /path                # Short form
polis serve -p 8080                 # Bind a fixed port instead of a free one
```

The bundled binary includes all CLI commands plus the `serve` command. The standalone binary only runs the server. Run either with `--help` for the remaining flags.

If you run `polis serve` with the CLI-only binary (not the bundled one), you'll see an error directing you to use the bundled binary instead.

### What Happens on Startup

When you start the webapp:

1. The data directory is created if it doesn't exist
2. Your site configuration is loaded (keys, `.env`, `.well-known/polis`)
3. Any old-format draft directories are automatically migrated to the new layout
4. Background sync with the discovery service starts: one sync runs straight away, then one every 30 seconds while at least one browser tab is connected
5. The server picks a free port on `localhost` (or uses `--port`)
6. The server prints its URL and data directory to the terminal
7. Your default browser opens after a brief delay

The server binds to `localhost` only. It is never accessible from other machines on your network.

### First Launch

If the data directory has no site configured, the webapp shows a **Welcome screen** with two options:

**Initialize New Site** — Creates a fresh Polis site with:
- A new Ed25519 keypair for signing your content
- A `.well-known/polis` identity file
- The directory structure for posts, comments, and snippets
- A default `site/snippets/about.md` for your About section

You can optionally provide a site title, base URL, and discovery service URL. These can also be configured later.

**Link Existing Site** — Points the webapp at a directory that already contains a valid Polis site (the directory is validated before it is linked).

### The Setup Wizard

After creating a site, and on later launches until your site is registered, the webapp opens a setup wizard with two steps:

1. **Deploy** — Make your site publicly accessible (e.g., push to GitHub Pages or Vercel). The wizard checks your domain every 5 seconds until your `.well-known/polis` file is reachable.
2. **Register** — Register your domain with the discovery service so other authors can find you.

Click **Do this later** to dismiss it; it stays dismissed. You can register at any time from **Settings → Discovery Service**.

---

## Hosted Accounts & Signing In

This section applies to the **hosted** Polis service (a space at an address like `you.polis.pub`). If you run the webapp on your own computer, skip it — self-hosting has no accounts or sign-in.

Polis uses **magic links**, not passwords. You prove who you are by clicking a one-time link sent to your email. Every link **expires 15 minutes** after it's sent and works **once**.

### Creating an account

1. Go to the hosted service and enter your email address.
2. You'll receive a **"Welcome to polis"** email. Click **Create my space**.
3. That verifies your email and provisions your space. Your new space automatically follows `discover.polis.pub` so your stream has something in it from day one.

Until you click the link your space is *unverified*. After **7 days** unverified you get a reminder email; after **14 days** the space is archived — your data is kept safe and can be recovered later (see Troubleshooting below).

### Signing in

1. On the sign-in page, enter the email tied to your space.
2. You'll receive a **"Sign in to polis"** email. Click **Sign in**.
3. You're taken straight into your space — no password to remember.

If you didn't request a sign-in link, you can safely ignore the email; your space stays locked.

> **Sign-in is separate from your DM password.** Signing in gets you *into* your space. Reading and sending direct messages may additionally ask for a **message password**, which encrypts your DMs at rest — it's a different secret from your email sign-in. See [DM encryption](../../general/security/dm-encryption.md) if you're prompted for it.

### Troubleshooting access

**The email never arrived.**
- Check your spam/junk folder.
- Give it a minute — delivery is usually quick but not instant.
- Request a fresh link from the sign-in page.

**"This link is invalid or has expired."**
- Links expire after 15 minutes and each works only once. Request a new one and use that.
- Don't open the link on a different device/browser than you intend to stay signed in on — sign in where you want to end up.

**I need to change my email address.**
- Sign in, then go to **Settings → Account → Change**. A confirmation link is sent to the *new* address; click it to complete the switch. Your old address stops working once confirmed.

**My space was archived (I didn't verify in time).**
- Your data isn't lost. On the recover page (`/recover` on the hosted service), enter the same email. The **Recover your polis space** email lets you download an archive of everything or reinstate the live site.

**Still stuck.**
- Use the **Help** link in any Polis email (it points here).

---

## Layout & Navigation

The webapp uses a centered-column layout with a thin top navigation bar. The logged-in view is a **single stream-screen** rendered by the v4 infinity-stream shape — there is no Feed/Posts mode toggle. Filtering is driven by PQL (polis query language) sentences: clicking an icon loads a preset sentence; the sentence-filter widget in the topbar lets you compose your own.

### Top Nav Bar

The top bar contains (left to right):

- **Avatar button** — your avatar. Clicking it returns you to the default stream; hovering opens a menu with your name and handle (click the handle to open your public site), follower/following/post counts, **Copy follow link**, and **Settings**.
- **Icon row** (each loads a PQL preset; three carry a notification dot):
  - **Gateway** (arc + dots) — "Activity from my network" (`all activity from my network`). Dot = new items in your network since you last looked.
  - **Paragraph** (three lines) — "My posts" (`all posts from me by date`).
  - **Comment** (speech bubble) — "Comments to bless" (`all comments from all polis to bless`). Dot = new blessing requests waiting for you.
  - **People** (silhouette + circle) — "Profiles" (`all profiles from my network by name`).
  - **Envelope** — "My messages" (`all messages from my mutuals by date`). Dot = new unread direct messages.
  - **New post** (circled plus) — opens the post editor at the top of the stream.
- **Sentence filter** — the current sentence, with a clickable slot for the type (activity, posts, comments, profiles, messages, drafts), the scope (me, my network, my mutuals, all polis — which scopes are offered depends on the type) and, for types that have one, the order or modifier (e.g. *by date*, *to bless*, *by name*). Changing a slot routes to `/_/pql/<sentence>`.
- **Your handle** at the right edge, linking to your public site.

A dot clears when you open its view.

On a narrow screen the icon row and avatar collapse into a **menu** button that opens a drawer with the same items plus **Copy follow link** and **Settings**.

### Centered Content Column

The stream, message threads and the editors are rendered in a centered column below the top bar. Settings uses the same page width. On a wide screen a card beside the stream shows your name, handle and About text (with an **edit** link), and your post and draft counts. Click either count to filter the stream to them.

**Toasts** appear in the bottom-right corner for success, error, warning, and info messages. They auto-dismiss after a few seconds.

**Confirmations** are asked for actions that are hard to undo, such as switching Rosie off or withdrawing your terms.

### Deep-Linking

The webapp is a single stream-screen, and the URL **is** the filter. There are only three kinds of route:

| URL | View |
|-----|------|
| `/_/` | The default stream (activity from your network, by date) |
| `/_/pql/<sentence>` | The stream filtered by a PQL sentence — every icon button and the sentence-filter widget just load one of these |
| `/_/settings` | Settings (the only non-stream surface) |

Every view you used to reach by a dedicated page — posts, comments, blessings, following, messages — is now a PQL filter. For example:

- `/_/pql/all+posts+from+me+by+date` — your posts
- `/_/pql/all+comments+from+all+polis+to+bless` — comments awaiting your blessing
- `/_/pql/all+profiles+from+my+network+by+name` — people you follow

Any other `/_/…` path falls through to the default stream. (The old v3 page routes — `/_/posts`, `/_/blessings`, `/_/social/*`, etc. — were retired.)

---

## Core Features

### Writing and Publishing Posts

1. Click the **New post** icon in the top bar, or the *"... yours, truly"* line at the top of the stream
2. An editor card opens at the top of the stream
3. Write your post in markdown. A first line of `# Your title` becomes the title; without one the post is untitled
4. Click **Publish**
5. The post is signed with your Ed25519 key and saved to `content/pub.polis.core/post/YYYYMMDD/`

A toast confirms the publish and the post appears in your stream. **Cancel** (or Esc) closes the editor.

### Editing and Republishing

1. Hover one of your published posts in the stream and click **Edit**
2. The editor opens with the post's content
3. Click **Republish** — the post is re-signed with a new version hash
4. The post's `version-history` frontmatter gains the new hash, and every version's full text is kept in the `.versions/` directory beside the post

### Unpublishing a Post

To remove a published post:

1. Hover the post in the stream
2. Click **Unpublish**

Unpublishing removes the post from your site and from the discovery service, deletes its version history, and moves the text back to your drafts (without its signature). If you publish it again it is treated as a new post. Your own comments carry the same **Unpublish** button.

### Drafts

Click **Save as draft** in the editor at any time. Drafts are markdown files stored in `.polis/bundles/pub.polis.core/posts/drafts/`, named after the post's title. To find them, click the **drafts** count beside the stream or choose the *drafts* type in the sentence filter. Click a draft to reopen it in the editor, or hover it and click **Discard** to delete it.

### Commenting on Other Authors' Posts

1. Find the post in your stream
2. Click its comment count on the left edge of the post. The post opens, with a comment editor below it
3. Write your comment in markdown
4. Click **Comment** (or **Save as draft** to finish later; the draft reopens the next time you comment on that post)

This signs your comment and sends a blessing request to the post's author. Possible outcomes:

- **Sent**: "Comment sent. It appears once the author approves it." The comment waits for the author's site to decide. If the author has Rosie on and your comment matches their rules, it may be approved within moments; otherwise the author approves it themselves. Either way you find out afterwards, never at the moment you send
- **Saved but not sent**: the comment is signed and saved locally, but the request couldn't reach the discovery service. A toast explains why. If your site isn't registered yet, the toast asks you to register first

### Blessing Workflow

> For CLI blessing commands, see the [CLI Command Reference](../../cli/user/command-reference.md). For terminology, see the [Glossary](../../general/reference/glossary.md).

When other authors comment on your posts, their blessing requests wait for you (unless Rosie decides them first — see [Rosie](#rosie)).

**To review a request:**

1. Click the **Comment** icon in the top bar. The stream shows comments waiting for your blessing
2. Hover a comment
3. Click **Bless** to approve it or **Deny** to reject it

From the command line the same decisions are `polis blessing requests`, `polis blessing grant <comment-version>` and `polis blessing deny <comment-version>`.

**Blessed comments** become part of your site's public content and appear beside the post they reply to.

**Your blessing list is signed.** The public file listing which comments you have blessed
(`blessed.json`, served from your site) carries your signature, so anyone can check that the comments
shown beside your posts are the ones *you* admitted — and that the version pinned to each is the
version you saw. You do not have to do anything: blessing or revoking signs the list as a side effect
of the action.

Two things worth knowing:

- **An older list is unsigned, and that is normal.** Your list is signed the next time you bless or
  revoke something. Until then it is simply unsigned, which is a fact about the file and not a
  problem with your site.
- **Nobody can sign it for you.** Background maintenance that touches the list — repairing the index,
  removing a comment you denied — writes it *unsigned* rather than re-signing it, because a signature
  under your key has to mean you. If you see your list go from signed to unsigned, that is what
  happened, and your next bless restores it.

### About Page

Your About text is shown in the card beside the stream. Click **edit** under it to change it, then **Save**. This edits `site/snippets/about.md`, which is included by your theme's About template, and re-renders your site.

If `site/snippets/about.md` does not exist yet (older sites), you see default welcome text. Saving creates the file.

### Snippets in Posts

You can include reusable content in posts using `{{> snippet-name}}`.

Snippet resolution order: `.md` first, then `.html`, then exact name match. Global snippets live in `site/snippets/`. Theme snippets are part of your active theme.

---

## Social Features

### Following Authors

Click the **People** icon, or pick the *profiles* type in the sentence filter. Choose *all polis* as the scope to see authors you don't follow yet. Each profile has a **+ Follow** or **Unfollow** button and says whether you follow each other (*mutual*, *follows you*, *following*, *not following*).

Anyone can follow you with your follow link: choose **Copy follow link** from the avatar menu to share it. New hosted spaces follow `discover.polis.pub` from the start, so the stream has content from day one.

**Important side effects:**
- Following an author blesses any of their pending blessing requests on your posts — **your own site does it**, as part of the follow, signing with your key. The discovery service is told the result; it never decides one.
- Unfollowing an author denies those pending requests the same way, from your own site.

### Activity

The default stream, and the **Gateway** icon, show **activity from your network**: posts, comments and follows from the authors you follow, newest first. Each item has a one-line summary (*published a new post*, *commented on …*). Click the author's handle in that line to see all their posts.

The stream is kept fresh by the background sync (every 30 seconds while a tab is open). Unread items are highlighted; opening one marks it, and the newer items above it, as read. New items do not push into the view you are reading; the Gateway dot tells you they have arrived.

### Messages

The **Envelope** icon shows your direct messages. Messages are exchanged between **mutuals** (authors who follow each other). Messages are encrypted: see [DM encryption](../../general/security/dm-encryption.md) for what that guarantees, and **Settings → Messages** to set or change your message password.

When you run the webapp on your own computer, messages are read-only unless you start it with `--dev`.

### Followers

The avatar menu shows your follower and following counts. In the profiles view, each profile says whether that author *follows you*.

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
Your site directory is a git repository connected to GitHub Pages, Vercel, or Netlify. After publishing, commit and push — the hosting provider builds and deploys automatically. The webapp's [deployment wizard](#configuring-hooks-via-the-webapp) can set this up for you.

**rsync to a server**:
Use `rsync` to copy your site directory to a web server. Useful if you manage your own hosting.

**Manual upload**:
Copy your site files to any static file host (S3, a shared hosting control panel, etc.). Simplest approach for infrequent publishers.

### Automating Deployment with Hooks

The recommended approach is to configure a [hook](#hooks--automations) that runs after every publish. The webapp includes a deployment wizard (**Settings → Help me... → Deploy my content using git**) that generates hook scripts for Vercel, GitHub Pages, and git-only workflows. Once configured, publishing a post automatically commits and pushes — no manual steps needed.

---

## Security

The webapp binds to `localhost` only (hardcoded — `--port` changes the port, and no flag changes the address) and has no login system — if you can reach the port, you have full access. This is by design: it runs locally for your use only.

Your Ed25519 private key is read by the server process for signing but is never transmitted — only signatures are sent. All file paths are validated to prevent directory traversal.

**Protect these files:**
- `.polis/keys/id_ed25519` — your private signing key
- `.env` — site URL and discovery service credentials
- Your data directory — treat it like any directory with private files

For the full cryptographic model, key management details, and threat analysis, see [Security Model](../../general/security/security-model.md).

---

## Settings

Open **Settings** from the avatar menu (or the menu drawer on a narrow screen). It lives at `/_/settings`. Sections, top to bottom:

| Section | Where | What it holds |
|---------|-------|---------------|
| **Messages** | sites with messaging set up | your message password and recovery phrase — see [DM encryption](../../general/security/dm-encryption.md) |
| **Your Site** | both | site title, display name, avatar, public key (and, on hosted, discovery registration) |
| **Site Theme** | both | the theme of your public site |
| **Rosie** | both | your helper, on or off |
| **Terms of use** | both | the signed terms your posts carry |
| **Discovery Service** | self-hosted | connection and registration |
| **Help me...** | self-hosted | the deployment and custom-script wizards |
| **Active Automations** | self-hosted | the hooks you have configured |
| **Troubleshooting** | both | re-render your site |
| **Account** | hosted | your sign-in email |
| **Your Data** | both | download (self-hosted) or export (hosted) a zip of your site |

### Your Site Section

| Field | Source | Description |
|-------|--------|-------------|
| Site | `.well-known/polis` | Your site's display name |
| Display Name | `.well-known/polis` | The author name shown with your posts — click **Edit** to change it |
| Avatar | `.well-known/polis` | Your site's avatar. Use **Randomize** to pick a new color scheme, **Save** to apply, or **Reset** to remove |
| Public Key | `.polis/keys/id_ed25519.pub` | Your Ed25519 public key (truncated, with **Copy** and **Rotate** buttons) |

**Rotating your key:**

Click **Rotate** to generate a new keypair. This:
1. Notifies the discovery service first (so your signature changes are attributed correctly)
2. Generates a new Ed25519 keypair
3. Writes the new keys to `.polis/keys/`
4. Updates your `.well-known/polis` identity file — the new key **and a signed record of the handover**
5. Republishes `.well-known/did.json` so your `did:web` document states the new key, and keeps the old
   one listed so a resolver can still verify what it signed

Key rotation requires your site to be registered with the discovery service and your `POLIS_BASE_URL` to be set in `.env`.

⭐ **Rotating does not cost you your past.** Your site publishes a **key history**: every key you have
held, when each was current, and a signature made by the *old* key handing authority to the new one.
So a post, comment or approval you signed years ago can still be verified by anyone — they read your
site, find the key that was current when you signed it, and check. No service to ask, and nothing to
lose if one goes away.

You do not have to do anything to get this. It starts the moment your site is created and grows by one
entry each time you rotate.

⚠️ **Your old private key is not kept anywhere.** Earlier versions tucked it away in a file; that has
been removed, because it was never what made old signatures checkable — the *public* key in your
history is — and a spare copy of a private key is only something to lose. If you have an old
`.polis/keys/id_ed25519.old` from before, it is left where it is and nothing new is written.

### Terms of Use

The **Terms of use** card states how others may use what you publish. It is the only practical way to
set them if your site is hosted, and it is the same signed licence the CLI's `polis license` writes.

**What terms are.** A short, machine-readable statement — *may this be used to train an AI model? may
a search engine index it? is attribution required?* — **signed with your key** and **written into each
post as you publish it**. That last part is what makes it different from a `robots.txt` file: a
`robots.txt` stays on your server, so terms are lost the moment your work is quoted, mirrored, or
scraped. Terms signed into the post travel with it.

**Setting them.** Two buttons, and a third once you have chosen:

| Choice | What it says |
|--------|--------------|
| **Reserved** *(recommended)* | Read and quote freely with a link back. Search engines may index your work and send people to it. AI training and answer-engine summaries require asking. |
| **Open** | Anyone may use your work for anything, including AI training. |
| **Publish no terms** | Withdraw. Readers fall back to their own assumptions. |

Once terms are stated, the card shows the plain-language summary, the profile name, and the machine
values so you can see exactly what is being published.

> ⚠️ **Terms are not retroactive.** Posts you have already published keep the terms they were signed
> with. Changing this applies from here on. Nothing rewrites your archive — and nothing should:
> re-signing old posts would claim you said something at a time when you did not.

**Stating nothing is a real choice**, and it is where every site starts. A site with no terms has
*not said* — which is different from permitting and different from denying. Signup does not ask and
does not choose for you: a new polis.pub site publishes no terms until you set some here. The card
invites you to choose, once, and does not nag.

**What gets published.** Changing your terms immediately regenerates your public `robots.txt`,
`rsl.xml`, and terms page from the signed licence. Those are always generated — never edited by hand,
or they would drift from what you actually signed.

**Publish no terms takes them all down.** Withdrawing removes the signed licence *and* the three
public surfaces it generated, so your site goes back to looking exactly like one that never stated
anything. It has to work that way: leaving a terms page up would keep asserting terms you just
withdrew.

**It is evidence, not a fence.** A signature stops nobody. What it buys is that your terms are
legible, dated, provably yours, and travel with your work — which is more than a `robots.txt` can say.
If a crawler ignores them, they are still the record of what you asked for.

For the full format, see [the licence spec](../../signet/spec/license.md); for a walkthrough,
[Set your terms](../../signet/guides/set-your-terms.md).

### Rosie

**Rosie is your helper.** She approves or turns away comments on your posts, using the rules you have
already set. That is all she does today.

**If Rosie is off, comments wait for you to approve them yourself.** Nothing else changes: your site
stays connected and your messages keep working, because that upkeep is not Rosie's job.

**You can switch her off, or back on, at any time** — one switch, in **Settings → Rosie**. When you switch
her on, she also looks at comments already waiting for you, and decides them by the same rules.
Comments your rules hold for your review stay waiting either way.

What the section shows:

| | |
|---|---|
| **On / Off** | whether Rosie is working for you, and since when |
| **Who switched her on** | *polis.pub switched her on for you* (on polis.pub she starts on) or *you switched her on* |
| **History** | every time she was switched on or off, each with a link to the record kept on your site |
| **Has not started yet** | on polis.pub, before Rosie is started for everyone: your choice is saved, and she follows it once she starts |

**Everything Rosie does is marked as hers.** Each approval she makes carries her name and a note of which
switch-on she was working under, and that record is kept on your own site — so you can always see exactly
what she did, and when. Approvals you make yourself carry no such note. If you switch her off, that choice
is saved on your site and she stays off, even when she learns new things.

**Where she runs.** Rosie works inside this web app — on polis.pub, and on your own computer while
`polis serve` is running. A site you only manage from the command line has no Rosie. If you host your own
site, she starts off; switch her on here, or when `polis init` asks.

For the full format, see [delegation](../../signet/spec/delegation.md).

### Who Can Act as You

This is not a card you click. It answers three questions about your own site, and the answers are
deliberately **not** shown inside this app — see why below.

**Who did this?** Everything announced for your site to the discovery network records **who signed
it** and **under what authority**. *"Signed with your key"* is the ordinary answer. Anything signed by
someone else says so, and says whether you allowed it (`none` means no permission was found — a fact,
not an accusation). **Rosie's approvals are signed with your key too, and marked as hers** — see
[Rosie](#rosie) above.

**What does this helper do?** If your site is hosted, your host holds your signing key — that is what
hosting is. An honest host **publishes a signed statement saying so**, on its own site and signed with
its own key, including whether it signs **as you** or adds its own signature to what it does. polis.pub
signs as you: when you publish from this app, and when its maintenance software repairs something for
you. That maintenance carries no mark. **Rosie is different**: she works for you, only while you have her
switched on, and marks everything she does.

**What did I allow?** Any custody permission published on your site, and whether it was withdrawn.
polis.pub does **not** publish one on your behalf. Rosie's switch-on records are separate, and
**Settings → Rosie** lists them — including the one polis.pub made when it switched her on for you.

**How to check — from your own computer, not from here.** A page inside your host's app showing your
host's behaviour is only as honest as your host. So the check runs on your machine, using the polis
command-line tool:

```bash
polis actor verify --custody yourname.polis.pub --operator polis.polis.pub
```

It fetches your host's statement and verifies it against your host's published key, and lists your
recent network activity with who signed each item. **You** are the one person who knows which of those
you did yourself.

> ⚠️ **What this does and does not do.** It makes custody visible; it does not reduce it — your host
> can still do everything it could before. Your host could hide records from this check, but cannot
> forge one that passes. The activity list is the discovery service's own record and is not
> independently re-checked. If that trade is not for you, you can hold your own key by self-hosting.

For the full format, see [custody, the tenant half](../../signet/spec/custody.md#12-custody-of-a-tenants-key--the-tenant-half).

### Site Theme

The Site Theme section has a dropdown of the themes you can choose. The themes, and the two reserved ones that are not offered, are listed in [Themes → What ships today](../../general/concepts/themes.md#what-ships-today).

Pick a theme and click **Change Theme**. This updates `active_theme` in `.polis/bundles/registry.json`, applies the new theme CSS, and re-renders your site. (The active theme is private per-site configuration; it does not live in `.well-known/polis`.) The web app itself takes its colors from your site theme.

### Discovery Service Section

*Self-hosted only — on a hosted space the host manages this, and **Your Site** shows the registration status.*

| Field | Source | Description |
|-------|--------|-------------|
| Status | Runtime | "Connected" (green) or "Not configured" (yellow) |
| URL | `.env` `DISCOVERY_SERVICE_URL` | Your discovery service endpoint |
| Registration | Discovery service API | Whether your domain is registered, and since when |

If your site is not registered, a **Register with discovery service** link registers it. If it is, **Unregister from discovery service** removes it from the discovery network.

The discovery service uses sensible defaults — if you don't set `DISCOVERY_SERVICE_URL` in your `.env`, the public Polis discovery service (`ds.polis.pub`) is used automatically.

### Troubleshooting Section

- **Re-render** — Rebuilds all your published HTML from source markdown. Useful after updating snippets, or recovering from a corrupted render.

### Your Data Section

- **Download** (self-hosted) — Downloads your entire site directory as a zip archive, including your keys (logs are left out). Use this for backups or migrating to a new machine.
- **Export** (hosted) — Emails you a download link for the same archive.

### Where Settings Come From

> For the full configuration loading order (environment variables, `.env`, `.well-known/polis`, defaults), see [CLI Command Reference § Configuration](../../cli/user/command-reference.md#configuration).

Settings are loaded from multiple places:

| Source | What It Stores |
|--------|---------------|
| `.well-known/polis` | Site identity (title, author name, avatar, public key) |
| `.env` | Runtime config (`POLIS_BASE_URL`, `DISCOVERY_SERVICE_URL`, `DISCOVERY_SERVICE_KEY`, `LOG_LEVEL`, `LOG_RETENTION_DAYS`) |
| `.polis/bundles/registry.json` | Active theme and shape, and the activity-summary rules |
| `.polis/webapp/config.json` | Web app state (setup wizard dismissed, hook paths, a few display preferences) |

The `.env` file is searched in order: your data directory first, then the current working directory, then `~/.polis/`.

---

## Hooks & Automations

Hooks are shell scripts that run automatically after you publish, republish, or a comment is blessed. They run only when you run the webapp yourself; a hosted space does not run hooks. The most common use is **automated deployment** — pushing your site to a hosting provider after every publish.

### The Three Hook Events

| Event | When It Fires |
|-------|--------------:|
| `post-publish` | After a new post is published |
| `post-republish` | After an existing post is updated |
| `post-comment` | After a comment becomes blessed — one you blessed, one Rosie blessed, or one of yours that another author blessed |

### Configuring Hooks via the Webapp

In **Settings**, the **Help me...** section has two wizards:

**Deploy my content using git** — walks you through setting up automated deployment:
1. Choose a deployment method (Vercel, GitHub Pages, or Git repository only)
2. Select which hook events to configure
3. Review the generated script
4. Confirm — scripts are created in `.polis/webapp/hooks/`

**Run a custom script when I post or comment** — creates starter scripts for you to customize:
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
| `POLIS_VERSION` | Version hash of the published file | `sha256:282b4e19…` |
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
  "version": "sha256:282b4e19…",
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

The Settings page shows an **Active Automations** section listing all configured hooks. Each shows its name, description, and a **Remove** button. If no hooks are configured, the section shows "No automations configured yet."

### Hook Execution Details

- Hooks run in your site directory as the working directory
- A hook is stopped after 30 seconds
- Hook failures are logged but do not fail the publish operation — your post is still published even if the hook errors
- Combined stdout and stderr output is captured and logged

---

## Notifications

Polis tells you something new has happened with the **dots** on the icons in the top bar, not with a separate notifications list.

### The Three Dots

| Dot | Lights when | Clears when |
|-----|-------------|-------------|
| **Gateway** | a sync has brought new items from your network since you last opened the activity view | you open the activity view |
| **Comment** | a sync has run since you last looked **and** at least one blessing request is waiting | you open *comments to bless* |
| **Envelope** | a conversation has unread messages newer than your last visit | you open your messages |

The dots are computed from files on your own disk and pushed to open tabs after each sync, which runs every **30 seconds** while at least one tab is open. Your own posts never light the Gateway dot.

### Activity Summaries

In the activity view each post, comment and follow carries a one-line summary with an icon — *published a new post*, *commented on …*, *started following you*. The wording and icon come from the rules in the `notifications` list of `.polis/bundles/registry.json`:

```json
{
  "notifications": [
    {
      "id": "new-post",
      "on": "pub.polis.post.published",
      "relevance": "followed_author",
      "template": "{{actor}} published a new post",
      "icon": "pencil"
    }
  ]
}
```

A new site gets eight rules: `new-post`, `updated-post`, `new-comment`, `blessing-requested`, `blessing-granted`, `blessing-denied`, `new-follower` and `lost-follower`. The summaries use the `template` and `icon` of the rules for `pub.polis.post.published`, `pub.polis.comment.published` and `pub.polis.follow.announced`. You can edit those two fields; the change shows the next time the web app loads.

---

## File Structure

Your Polis site is a directory of files. Understanding the structure helps with troubleshooting and manual operations.

### Overview

```
your-site/
├── .well-known/
│   ├── polis                     # Site identity (JSON)
│   └── did.json                  # did:web document
├── .env                           # Runtime config
├── .polis/
│   ├── keys/
│   │   ├── id_ed25519            # Private key (never share)
│   │   └── id_ed25519.pub        # Public key
│   ├── bundles/
│   │   ├── registry.json         # Active theme + shape, activity-summary rules
│   │   └── pub.polis.core/
│   │       ├── posts/drafts/     # Post drafts (markdown)
│   │       ├── comments/
│   │       │   ├── drafts/       # Comment drafts
│   │       │   ├── pending/      # Awaiting blessing
│   │       │   └── denied/       # Rejected comments
│   │       ├── dm/               # Direct messages
│   │       ├── shapes/           # Installed shape templates
│   │       └── themes/           # Installed theme CSS
│   ├── ds/<discovery-domain>/
│   │   └── pub.polis.core/
│   │       ├── config/           # User preferences (survives resets)
│   │       │   └── feed.json
│   │       └── state/            # Computed data (safely deletable)
│   │           ├── cursors.json
│   │           ├── pub.polis.feed.jsonl
│   │           ├── pub.polis.follow.json
│   │           └── pub.polis.comment.blessing.json
│   ├── logs/                      # Daily logs (YYYY-MM-DD.log)
│   └── webapp/
│       ├── config.json            # Web app state
│       └── hooks/                 # Hook scripts
├── content/pub.polis.core/
│   ├── bundle.json               # Bundle definition
│   ├── index.jsonl               # Public content index
│   ├── post/YYYYMMDD/            # Source posts (markdown)
│   ├── comment/
│   │   ├── YYYYMMDD/             # Your published comments (markdown)
│   │   └── blessed.json          # Comments you have blessed
│   └── follow/following.json     # Following list
├── posts/                        # Rendered posts (HTML, served publicly)
├── site/
│   └── snippets/
│       └── about.md              # About section content (editable in webapp)
├── index.html                    # Rendered home page
└── styles.css                    # Active theme CSS (copied from theme)
```

`.polis/ds/` and `.polis/logs/` fill in once the web app has run; `.env` is yours to create.

### Config vs State

This distinction is important for troubleshooting:

**Config files** contain your preferences. They survive resets and should be preserved:
- `.polis/webapp/config.json` — web app state and hook paths
- `.polis/bundles/registry.json` — active theme and the activity-summary rules
- `.polis/ds/<domain>/pub.polis.core/config/feed.json` — feed cache limits
- `.env` — discovery service settings and site URL

**State files** contain computed data derived from the discovery service. They can be safely deleted and will be rebuilt on the next sync:
- `.polis/ds/<domain>/pub.polis.core/state/cursors.json` — sync positions, and when you last opened each dotted view
- `.polis/ds/<domain>/pub.polis.core/state/pub.polis.feed.jsonl` — feed cache
- `.polis/ds/<domain>/pub.polis.core/state/pub.polis.follow.json` — followers list
- `.polis/ds/<domain>/pub.polis.core/state/pub.polis.comment.blessing.json` — blessing decisions

### Key Files Explained

#### `.well-known/polis`

Your site's public identity file. Contains your author name, avatar, public key and its [key history](../../signet/spec/key-history.md), site title, and a pointer to your content bundle. This file is publicly accessible when your site is deployed.

#### `.env`

Runtime configuration. Key variables:

```
POLIS_BASE_URL=https://your-domain.com
DISCOVERY_SERVICE_URL=https://...
LOG_LEVEL=1
LOG_RETENTION_DAYS=7
```

| Variable | Default | Description |
|----------|---------|-------------|
| `POLIS_BASE_URL` | — | Your site's public URL (required for registration and key rotation) |
| `DISCOVERY_SERVICE_URL` | `https://ds.polis.pub` | Discovery service endpoint |
| `DISCOVERY_SERVICE_KEY` | — | Discovery service API key (optional — requests are authenticated with your signature) |
| `LOG_LEVEL` | `1` | `1` = basic, `2` = verbose |
| `LOG_RETENTION_DAYS` | `7` | How many days of logs to keep |

When the web app starts it writes the log settings it is using back into the `.env` in your data directory, so they are visible.

The `.env` file is searched in order: your data directory, current working directory, then `~/.polis/`. The first one found is used.

#### `.polis/webapp/config.json`

Web app state, written by the app:

```json
{
  "setup_wizard_dismissed": true,
  "hooks": {
    "post-publish": ".polis/webapp/hooks/post-publish.sh"
  }
}
```

| Field | Description |
|-------|-------------|
| `setup_wizard_dismissed` | Whether the setup wizard has been dismissed |
| `hooks` | Hook script paths by event type |
| `show_frontmatter`, `hide_read`, `webapp_theme`, `editor_panel_mode` | Display preferences kept from earlier versions of the app |

Note: `log_level` is configured via `LOG_LEVEL` in `.env`, not in this file.

#### `cursors.json`

Tracks your sync position with the discovery service. A cursor is a stream position — a number that says "I've processed all events up to here." The main one is `pub.polis.sync`; others record when you last opened the dotted views.

```json
{
  "cursors": {
    "pub.polis.sync": {
      "position": "12345",
      "last_updated": "2026-02-13T14:30:00Z"
    }
  }
}
```

#### `feed.json` (Config)

Controls how much the feed cache keeps. Every field is optional:

```json
{
  "staleness_minutes": 5,
  "max_age_days": 90,
  "max_posts": 300,
  "max_comments": 150
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `staleness_minutes` | `5` | How old the cache can be before a refresh is needed |
| `max_age_days` | `90` | Discard posts and comments older than this |
| `max_posts` | `300` | Maximum posts to keep |
| `max_comments` | `150` | Maximum comments to keep |
| `max_announcements` | `50` | Maximum other announcements (follows and the like) to keep |
| `max_announcement_days` | `14` | Discard announcements older than this |
| `max_items` | `500` | Older single cap, used for posts and comments only when their own limits are unset |

### Content Directories

**`content/pub.polis.core/post/YYYYMMDD/`** — Published post source files as markdown with YAML frontmatter. Frontmatter includes title, publish date, current version, version history, and Ed25519 signature. Each directory has a `.versions/` subdirectory holding every version's full text.

**`content/pub.polis.core/comment/YYYYMMDD/`** — Comments you have written and sent. Same format as posts, with an `in-reply-to` block linking to the post.

**`site/snippets/`** — Global snippets, including `about.md` (your About page content).

**`content/pub.polis.core/`** — Index files:
- `index.jsonl` — index of all published posts and comments
- `comment/blessed.json` — the signed list of comments you have blessed
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

Check that your `.env` has a valid `DISCOVERY_SERVICE_URL`. The default public service (`ds.polis.pub`) should work without any configuration. If you're using a custom discovery service, verify the URL.

### Dots or activity not updating

Sync runs every 30 seconds, but only while a browser tab is connected. If nothing new arrives:

1. Ensure a browser tab is open and connected to the webapp
2. Check that `POLIS_BASE_URL` is set in `.env` — sync does nothing without it
3. Check that your private key exists at `.polis/keys/id_ed25519`
4. Check that you're following at least one author (the activity view shows your network)
5. Try a full rebuild of the sync state — see [Deleting State Files](#deleting-state-files)

### Hooks not running

Common causes:

- **Hosted space**: hooks only run when you run the webapp yourself
- **Not executable**: Run `chmod +x .polis/webapp/hooks/post-publish.sh`
- **Script not found**: Ensure scripts are in `.polis/webapp/hooks/` (not `.polis/hooks/`)
- **Script errors**: Check the terminal output — hook failures are logged but don't prevent publishing
- **Too slow**: a hook is stopped after 30 seconds
- **Wrong shebang**: Ensure the first line is `#!/bin/bash` (or your preferred shell)
- **Missing tools**: If your hook uses `git`, ensure `git` is on the system PATH

### Posts not appearing after publish

Publishing writes files locally. If your site doesn't update publicly:

1. You need a deployment step (git push, file sync, etc.)
2. Set up a [hook](#hooks--automations) to automate deployment
3. Check your hosting provider's build status

### Rendered pages look wrong

After updating snippets or recovering from a rendering problem, use **Settings → Troubleshooting → Re-render** to rebuild all published HTML from source markdown. (Changing the theme re-renders your site by itself.)

### Site not registered

Open **Settings → Discovery Service** and click **Register with discovery service**.

Registration requires:
- `POLIS_BASE_URL` set in `.env`
- Your site deployed and publicly accessible at that URL
- A valid keypair in `.polis/keys/`

---

## Manual Operations

These are advanced operations for when you need to fix something or work at a lower level.

### Resetting the Sync Cursor

The sync cursor records how far you've synced with the discovery service. Resetting it forces a full re-sync from the beginning.

**Why**: If activity, followers or blessing state seem wrong or incomplete.

**How**: Edit `.polis/ds/<domain>/pub.polis.core/state/cursors.json` and set `pub.polis.sync`'s `position` to `"0"`:

```json
{
  "cursors": {
    "pub.polis.sync": {
      "position": "0",
      "last_updated": "2026-02-13T14:30:00Z"
    }
  }
}
```

**Risk**: The next sync reprocesses every event the discovery service still holds.

### Deleting State Files

All files in `.polis/ds/<domain>/pub.polis.core/state/` can be safely deleted. They will be rebuilt on the next sync cycle (within 30 seconds of a browser tab being open).

To force a complete rebuild of all state:

```bash
rm -rf .polis/ds/*/pub.polis.core/state/
```

Restart the webapp or open a browser tab — the background sync will regenerate everything on its next cycle.

### Manually Editing Activity Summaries

Edit the `notifications` list in `.polis/bundles/registry.json`. For the post, comment and follow rules you can change:

- The summary text: edit `"template"`
- The icon: edit `"icon"`

Reload the web app to see the change.

### Manually Editing Feed Config

Edit `.polis/ds/<domain>/pub.polis.core/config/feed.json` to change the cache limits described under [`feed.json`](#feedjson-config).

### Rebuilding the Blessed-Comments Index

If your `content/pub.polis.core/comment/blessed.json` is out of date, use the CLI to rebuild it:

```bash
polis rebuild --comments
```

This rebuilds the comment entries in the content index from the files on disk and reconciles `blessed.json`.

### Downloading Your Site

In **Settings → Your Data**, click **Download** (or **Export** on a hosted space) to get a zip archive of your entire site, including keys. Use this for:
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
- Old-format draft directories are automatically migrated to the new layout
- Background sync resumes from your last cursor position

### Checking Your Version

With the bundled binary, run:

```bash
polis version
```

The standalone `polis-server` does not print its version; the bundled `polis` of the same release has the same one.
