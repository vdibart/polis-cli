# Page Architecture

## Overview

In the v4 architecture there are **three surfaces**, not five pages. The old "Dashboard" and "My Content" pages collapsed into a single stream-screen that re-filters via PQL sentences. The nav bar (and the editor, and settings) are constants; only the stream filter changes.

| # | Surface | URL | Nav | Content Theme |
|---|---------|-----|-----|---------------|
| 1 | Landing | polis.pub (logged out) | System topbar | SOLS |
| 2 | Webapp SPA (logged in) | `handle.polis.pub/_/`, `/_/pql/<sentence>`, `/_/settings` | User's icon nav | User's theme |
| 3 | Foreign site visit | alice.polis.pub | User's icon nav via widget (autohide) | Alice's theme |

Inside surface 2, what used to be separate "Dashboard" and "My Content" pages are now PQL-filtered views of the same stream-screen: each icon button loads a preset sentence (see `navigation.md`), and the sentence-filter widget composes ad-hoc queries. Static routes are limited to `/_/` (default landing sentence) and `/_/settings`; everything else is `/_/pql/<sentence>`.

## Routing Model

| Action | Destination |
|--------|-------------|
| Click avatar | Avatar dropdown opens (no navigation by default) |
| Avatar menu → Dashboard | Stream-screen with the "owner overview" PQL preset |
| Avatar menu → Settings | `/_/settings` (the only non-stream static route) |
| Avatar menu → Log out | Landing (surface 1) |
| Click an icon button | Stream-screen with that icon's PQL preset (gateway, paragraph, comment, people, envelope) |
| Click edit icon | Opens the inline editor |
| Compose in sentence-filter | `/_/pql/<sentence>` |
| Click author name in stream | Their site (surface 3) |
| Click "polis" wordmark | (no wordmark in logged-in nav; only present on the landing nav, where it scrolls to top) |

---

## Page 1: Landing (polis.pub — logged out)

**Purpose:** The front door. Prove the network is alive, explain what polis is, convert visitors.

**Layout:** Single scroll page with SOLS palette. No icon nav — uses system topbar.

**Sections (top to bottom):**
1. **System topbar** — frosted glass, centered "polis" wordmark, right-edge "Sign in | Join"
2. **Brand lockup** — "The space we share." (48px Newsreader italic) + 2-line subtitle (19px Newsreader, indented 40px from left)
3. **Gradient divider** — fades from transparent to violet-line to transparent
4. **Activity stream** — "Happening now" label with green pulsing dot, 5 stream items showing real network activity. This IS the pitch — it proves the system works before you read a word.
5. **Divider**
6. **Join CTA** — "Claim your space" + handle input (.polis.pub suffix) + email + "Create my space" button
7. **The Network** — section explaining server architecture with 3 site cards
8. **Blessing Model** — 3-step flow (publish → reply → bless) with card visuals
9. **Three Steps** — compact rows explaining onboarding
10. **No Lock-in** — portability message with domain migration visual
11. **Final CTA** — repeat signup form with "The space we share." tagline
12. **Footer** — wordmark, tagline, GitHub/Terms/Privacy links

**Design principle:** "Show me then tell me." The activity stream at the top is alive — real conversations, real people. The educational narrative unfolds below for those who scroll. The activity IS the pitch.

---

## Surface 2: Webapp SPA (logged in, your handle)

**Purpose:** Your home base. A single stream-screen that adapts to whatever PQL sentence is in effect — your network's activity, your posts, pending blessings, profiles, messages, or anything composed from the sentence-filter slots.

**Layout:** Icon nav (user's theme) + content column (640px, user's theme).

**Default landing (`/_/`)**: the gateway preset — `all activity from my network by date`. A blended chronological stream interleaving your posts, posts from people you follow, posts from the wider network, comments on your posts, and follow events.

**Stream item anatomy:** avatar (28px circle) + author name + action label + post title (Newsreader) + 2-line excerpt + timestamp + stats. Date separators: "Today", "Yesterday", "This week" (11px uppercase Inter). Your own posts are visually identical to others' — no special border or badge. The stream is egalitarian.

### Preset filter views (sourced from the icon row)

The icon buttons select sub-views of the same stream by loading preset PQL sentences. The presets below replace what used to be separate Dashboard / My Content / People pages.

- **Gateway** — Activity from your network (default landing).
- **Paragraph (My posts)** — Your own post list, reverse-chronological. Replaces the old "My Content" page.
- **Comment (Comments to bless)** — Pending blessing requests on your posts plus blessed-comment status. Replaces the old "Dashboard / Blessing requests" section.
- **People (Profiles)** — Profiles in your network, sortable by name or date.
- **Envelope (My messages)** — DM threads with your mutuals.

**Why one screen:** A new user shouldn't have to choose between feeds they don't understand. The blended stream is the default; preset icons and the sentence-filter widget let power users carve it without ever leaving the surface. Filtering is composable, not modal.

---

## Surface 3: Foreign site visit (alice.polis.pub)

**Purpose:** Reading someone else's site with your context preserved.

**Layout:** Icon nav (user's theme, autohide) + content column (640px, THEIR theme).

**Nav behavior:**
- Autohides after 2 seconds. Hover top edge to reveal.
- Paragraph icon (posts) is active by default — the visited author's posts are what's already on screen.
- Tooltips contextualize the icon-row PQL presets to the visited handle ("Alice's posts", "Profiles in Alice's network", "Message Alice").
- Edit icon disabled (greyed out — can't create on someone else's site).
- Handle label on the right reads `alice.polis.pub` to anchor the visiting context.

**Content:**
- Site header in THEIR theme (their avatar, name, handle, bio, stats)
- Their post list in THEIR theme, rendered by their active shape
- Clear visual boundary between your nav strip and their content

---

## Stream Views (loaded by icon presets)

Clicking an icon button doesn't open a separate panel — it re-filters the stream-screen with a new PQL sentence and replaces the column content. The same renderer handles every view.

### Comments-to-bless view (`comment` icon)
**Your handle:** pending blessing requests on your posts with Grant/Deny actions; blessed comments shown with status.
**Foreign site:** the visited author's recent comments on others' posts; any comments you've made on theirs and their blessing status.

### Profiles view (`people` icon)
**Your handle:** profiles in your network — avatar, name, handle, follow/unfollow action. Sortable by name or date.
**Foreign site:** profiles in the visited author's network (their followers + following). Useful for discovering mutual connections.

### Messages view (`envelope` icon)
**Your handle:** DM thread list. Unread threads visually distinguished. Click a thread to open the conversation.
**Foreign site:** your DM thread with the visited author, or an empty state with the option to start one.

### Settings (`/_/settings`)
The only non-stream static route. Accessed via the avatar dropdown. Site info (the visited author's policies, signing key, version, content stats) surfaces here when applicable, not via a separate nav icon.
