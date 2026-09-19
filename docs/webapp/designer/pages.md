# Page Architecture

*For* [Contributors](../../README.md#contributing-to-polis) — *Kind* [Reference](../../README.md#kinds-of-page) — *Component* [Webapp](../README.md)

## Overview

In the v4 architecture there are **three surfaces**, not five pages. The old "Dashboard" and "My Content" pages collapsed into a single stream-screen that re-filters via PQL sentences. The nav bar (and the editor, and settings) are constants; only the stream filter changes.

| # | Surface | URL | Nav | Content Theme |
|---|---------|-----|-----|---------------|
| 1 | Landing | polis.pub (logged out) | System topbar | SOLS |
| 2 | Webapp SPA (logged in) | `handle.polis.pub/_/`, `/_/pql/<sentence>`, `/_/settings` | User's icon nav | User's theme |
| 3 | Foreign site visit | alice.polis.pub | User's icon nav, mounted into Alice's topbar by the widget | Alice's theme |

Inside surface 2, what used to be separate "Dashboard" and "My Content" pages are now PQL-filtered views of the same stream-screen: each icon button loads a preset sentence (see `navigation.md`), and the sentence-filter widget composes ad-hoc queries. Static routes are limited to `/_/` (default landing sentence) and `/_/settings`; everything else is `/_/pql/<sentence>`.

## Routing Model

| Action | Destination |
|--------|-------------|
| Hover avatar | Avatar dropdown opens |
| Click avatar | `/_/` (the default gateway view) |
| Avatar menu → Copy follow link | Copies your follow link; no navigation |
| Avatar menu → Settings | `/_/settings` (the only non-stream static route) |
| Click an icon button | Stream-screen with that icon's PQL preset (gateway, paragraph, comment, people, envelope) |
| Click edit icon | Opens the inline editor |
| Compose in sentence-filter | `/_/pql/<sentence>` |
| Click author name in stream | Their site (surface 3) |
| Click "polis" wordmark | (no wordmark in logged-in nav; on the landing nav it links to the top of the page) |

---

## Page 1: Landing (polis.pub — logged out)

**Purpose:** The front door. Prove the network is alive, explain what polis is, convert visitors.

**Layout:** Single scroll page with the SOLS palette. No icon nav — uses the system topbar. Source: `webapp/internal/hosted/landing.go`.

**Sections (top to bottom):**
1. **System topbar** — frosted glass; "polis" wordmark at the left, the pinned "yours, truly" tagline, right-edge "Nerd Vision | Sign in | Join"
2. **Hero** — "Your story. Your community. *Yours, truly.*", the subtitle "A social network for the open web.", a "Create your site" button, and "free to join · no password · no lock-in"
3. **Why you left** — "You didn't stop being social. You stopped taking the bait."
4. **Yours.** — ownership
5. **Truly.** — verifiability
6. **See it live.** — a framed preview linking to discover.polis.pub
7. **Wondering how any of this holds up?** — the door to the technical docs
8. **Create your site / Welcome back** — one form that switches between sign-up and sign-in
9. **Things people ask.** — FAQ
10. **Footer** — a pointer to why.polis.pub, and source · terms · privacy links

**Nerd Vision:** a topbar toggle that reveals technical asides (`nerd-vision` sections) between the sections above — how "yours" is cryptographic, how "truly" is a signature check, why discovery is an index and not a host.

---

## Surface 2: Webapp SPA (logged in, your handle)

**Purpose:** Your home base. A single stream-screen that adapts to whatever PQL sentence is in effect — your network's activity, your posts, pending blessings, profiles, messages, or anything composed from the sentence-filter slots.

**Layout:** Icon nav (user's theme) + content column (640px, user's theme).

**Default landing (`/_/`)**: the gateway preset — `all activity from my network`. A blended chronological stream of activity from the people you follow: posts, comments, blessings and follow events.

**Stream item anatomy (post):** a meta line — date and time on the left, the author's byline and comment count on the right — then the post title (Newsreader) and excerpt. There are no date-group separators. Your own posts are visually identical to others' — no special border or badge. The stream is egalitarian.

### Preset filter views (sourced from the icon row)

The icon buttons select sub-views of the same stream by loading preset PQL sentences. The presets below replace what used to be separate Dashboard / My Content / People pages.

- **Gateway** — Activity from your network (default landing).
- **Paragraph (My posts)** — Your own post list, reverse-chronological. Replaces the old "My Content" page.
- **Comment (Comments to bless)** — `all comments from all polis to bless`: comments awaiting your blessing. Replaces the old "Dashboard / Blessing requests" section.
- **People (Profiles)** — Profiles in your network, sortable by name or by activity.
- **Envelope (My messages)** — DM threads with your mutuals.

**Why one screen:** A new user shouldn't have to choose between feeds they don't understand. The blended stream is the default; preset icons and the sentence-filter widget let power users carve it without ever leaving the surface. Filtering is composable, not modal.

---

## Surface 3: Foreign site visit (alice.polis.pub)

**Purpose:** Reading someone else's site with your context preserved.

**Layout:** Alice's topbar, with your avatar and icon row mounted into it (your theme), + content column (640px, THEIR theme).

**Nav behavior:**
- Always visible; no autohide.
- Every icon, including edit, links back to the matching view on **your own** dashboard. Tooltips are the same as at home ("Activity from my network", "My posts", …).
- Alice's sentence filter stays in the middle of the bar, recoloured to your theme or to her theme's filter colours.
- Her "sign in" link becomes **your** handle label on the right.

**Content:**
- Site header in THEIR theme (their avatar, name, handle, bio, stats)
- Their post list in THEIR theme, rendered by their active shape
- Clear visual boundary between your nav strip and their content

---

## Stream Views (loaded by icon presets)

Clicking an icon button doesn't open a separate panel — it re-filters the stream-screen with a new PQL sentence and replaces the column content. The same renderer handles every view. These views exist on your own dashboard only; on a foreign site the icons link back here.

### Comments-to-bless view (`comment` icon)
Comments awaiting your blessing, with bless / deny actions on rollover.

### Profiles view (`people` icon)
Profiles in your network. Sortable by name or by activity.

### Messages view (`envelope` icon)
`all messages from my mutuals by date` — your DM conversations. DMs are between mutuals, so the scope is locked to `my mutuals`.

### Settings (`/_/settings`)
The only non-stream static route. Accessed via the avatar dropdown.
