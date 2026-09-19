# Navigation System

*For* [Contributors](../../README.md#contributing-to-polis) — *Kind* [Reference](../../README.md#kinds-of-page) — *Component* [Webapp](../README.md) — *Code* [`webapp/internal/webui/www`](../../../webapp/internal/webui/www) — *See also* [tour](../../handbook/stream-overview.md)

## Overview

The nav is a 64px topbar (`--topbar-height`) that persists across all logged-in pages. It is the connective tissue of the polis experience — the one element that tells the user "you're still here, you're still logged in, your stuff is one gesture away."

The nav's avatar, icons and handle label are always themed to the **viewer's** theme, never the site owner's. When visiting someone else's site, the nav is not a separate bar: it mounts into that site's own topbar (see *On Someone Else's Site*).

## Nav Bar Anatomy

```
┌──────────────────────────────────────────────────────────────────────────┐
│  [V]  │  ⌒  ¶  💬  👥  ✉  +              [ all  posts  ... ]            │
│ avatar│ sep icon row (PQL presets)        sentence-filter (centered)     │
└──────────────────────────────────────────────────────────────────────────┘
  LEFT                                       CENTER → RIGHT (handle label)
```

### Left Section
- **Avatar** (28px circle) — user's initial on a gradient background, or a custom avatar pattern. Hover opens the avatar dropdown menu; a click returns to the default view (`/_/`). The avatar carries no notification dot.
- **Separator** (1px vertical line, 20px tall, `--nav-border` color)

### Icon Row (PQL presets)
The 6 icon buttons (36px tap targets, 20px SVG icons) each load a preset PQL sentence by routing to `/_/pql/<sentence>`. They are the user's primary navigation gestures.

- **Gateway** (arc + dots) — `nav-btn-gateway`. "Activity from my network." Carries dot `nav-dot-gateway` for unread activity.
- **Paragraph** (three horizontal lines) — `nav-btn-paragraph`. "My posts."
- **Comment** (speech bubble) — `nav-btn-comment`. "Comments to bless." Carries dot `nav-dot-comment` for pending blessings.
- **People** (silhouette + circle) — `nav-btn-people`. "Profiles."
- **Envelope** — `nav-btn-envelope`. "My messages." Carries dot `nav-dot-envelope` for unread DMs.
- **Edit** (circled plus) — `nav-btn-edit`. "New post." Opens the inline editor card on the stream. (Replaces the old "+ button.")

There is **no `@` icon in the current nav.** The `@` symbol is reserved for a future at-mentions feature; today the messages slot is the envelope icon.

### Center
- **Sentence-filter widget** — centered, role=group, slot-based PQL composer (qualifier / type / scope / modifier slots, plus a site-typeahead input). Builds a sentence and routes to `/_/pql/<sentence>`. Replaces the old "polis" wordmark — the logged-in nav has no wordmark. The wordmark only appears on the logged-out landing nav (see "Logged-Out Nav" section below).

### Right Section
- **Pinned dot** (aria-hidden) and **handle label** (`.polis-handle-label`) — right-aligned. The handle label shows **your** handle: on your own pages, and on a foreign site, where the widget turns the host's "sign in" link into it.

On narrow screens a hamburger button (`nav-hamburger`) opens a mobile drawer carrying the same icons and dots.

## Icon States

| State | Background | Color |
|-------|------------|-------|
| Default | transparent | `--nav-icon` (muted) |
| Hover | `--nav-bg-hover` | `--nav-icon-hover` |
| Active | `--nav-bg-active` | `--nav-icon-active` |

## Notification Indicator Dots

Small red/orange dots (7px, `--nav-dot` color) positioned at the top-right of icon buttons. In the webapp, a dot means **something new arrived since you last opened that view**; opening the view clears it.

| Icon | Element ID | Dot appears when |
|------|------------|-----------------|
| Gateway | `nav-dot-gateway` | New activity from your network since you last viewed it |
| Comment | `nav-dot-comment` | New items in your blessing inbox since you last viewed it |
| Envelope | `nav-dot-envelope` | New DMs since you last viewed them |
| Paragraph (Posts) | — | (typically no dot) |
| People | — | (typically no dot) |
| Edit (New post) | — | (no dot) |

The dot has a 1.5px border matching `--nav-bg` to create separation from the icon.

⚠️ The cross-site nav widget uses a different rule for two of the three dots: comment shows while any blessing request is pending, envelope while any DM is unread.

## Avatar Hover Menu

Pure CSS hover menu — appears on rollover, no click required. Positioned below the avatar, left-aligned.

**Contents (webapp):**
1. **Header** — full name + handle (clickable → your public site, new tab)
2. **Stats row** — followers, following, posts
3. **Copy follow link** (with share icon)
4. **Settings** link (with gear icon) → `/_/settings`

The cross-site widget's menu has the same header and stats, and a single **Settings** link back to your own dashboard.

**Styling:** themed card background (`--bg-card`), 210px min-width, 12px border-radius.

## Edit Icon Behavior

The edit icon (formerly a separate "+ button") opens the editor inline. On a foreign site it links back to your own dashboard instead. There is no longer a dropdown of creation options in the nav — the editor itself surfaces the relevant choices (new post, new comment, new message) based on context.

## Contextual Icon Behavior

Each icon button loads a PQL sentence. Scope words like `me`, `my network`, `my mutuals`, `all polis`, or a specific handle change what content the sentence resolves to.

### On Your Pages (your handle's webapp SPA)
| Icon | Tooltip | Loads (example PQL sentence) |
|------|---------|-------|
| Gateway | "Activity from my network" | `all activity from my network` |
| Paragraph | "My posts" | `all posts from me by date` |
| Comment | "Comments to bless" | `all comments from all polis to bless` |
| People | "Profiles" | `all profiles from my network by name` |
| Envelope | "My messages" | `all messages from my mutuals by date` |
| Edit | "New post" | (opens the editor) |

### On Someone Else's Site
When you are logged in to polis.pub and visit another tenant's site, the hosted server adds the nav widget (`nav.js`) to the page. It mounts **into the host site's own topbar** rather than adding a bar:

- Your avatar (with its menu) and the icon row appear on the left, in your theme. The host's wordmark is hidden.
- The host's sentence filter stays in the middle. It is recoloured to your theme, or to the host's own filter colours where the host's theme defines them.
- The host's "sign in" link becomes your handle label on the right.
- Every icon **links back to the same view on your own dashboard** (`<you>.polis.pub/_/pql/<sentence>`); the presets do not re-scope to the visited site. Tooltips are the same as on your pages.

The bar is always visible — there is no autohide — and it is part of the host's topbar, so it is never an overlay.

## Logged-Out Nav (System Topbar)

On the polis.pub landing page (not logged in), the nav is a different element:

- **Height:** 64px minimum (matches the logged-in topbar)
- **Background:** `rgba(33,28,53,0.92)` with `backdrop-filter: blur(12px)` — frosted glass over SOLS
- **Sticky** at top of viewport
- **Contents:** "polis" wordmark at the left edge, the pinned "yours, truly" tagline, and right-aligned **Nerd Vision** toggle | **Sign in** | **Join**
- **"Nerd Vision"** — muted; reveals the landing page's technical layer
- **"Sign in"** — dim color
- **"Join"** — peach accent color, weight 600
- No icons, no avatar, no + button
