# Navigation System

## Overview

The nav is a 50px icon bar that persists across all logged-in pages. It is the connective tissue of the polis experience — the one element that tells the user "you're still here, you're still logged in, your stuff is one gesture away."

The nav is always themed to the **viewer's** theme, never the site owner's theme. When visiting someone else's site, the nav autohides to respect their content.

## Nav Bar Anatomy

```
┌──────────────────────────────────────────────────────────────────────────┐
│  [V]  │  ⌒  ¶  💬  👥  ✉  +              [ all  posts  ... ]            │
│ avatar│ sep icon row (PQL presets)        sentence-filter (centered)     │
└──────────────────────────────────────────────────────────────────────────┘
  LEFT                                       CENTER → RIGHT (handle label)
```

### Left Section
- **Avatar** (28px circle) — user's initial on a gradient background. Hover opens the avatar dropdown menu. Notification dot at 2 o'clock fires when *any* unread item exists across the system.
- **Separator** (1px vertical line, 20px tall, `--nav-border` color)

### Icon Row (PQL presets)
The 6 icon buttons (36px tap targets, 20px SVG icons) each load a preset PQL sentence by routing to `/_/pql/<sentence>`. They are the user's primary navigation gestures.

- **Gateway** (arc + dots) — `nav-btn-gateway`. "Activity from my network." Carries dot `nav-dot-gateway` for unread activity.
- **Paragraph** (three horizontal lines) — `nav-btn-paragraph`. "My posts."
- **Comment** (speech bubble) — `nav-btn-comment`. "Comments to bless." Carries dot `nav-dot-comment` for pending blessings.
- **People** (silhouette + circle) — `nav-btn-people`. "Profiles."
- **Envelope** — `nav-btn-envelope`. "My messages." Carries dot `nav-dot-envelope` for unread DMs.
- **Edit** (cross / plus) — `nav-btn-edit`. "New post." Opens the editor. (Replaces the old "+ button.")

There is **no `@` icon in the current nav.** The `@` symbol is reserved for a future at-mentions feature; today the messages slot is the envelope icon.

### Center
- **Sentence-filter widget** — centered, role=group, slot-based PQL composer (qualifier / type / scope / modifier slots, plus a site-typeahead input). Builds a sentence and routes to `/_/pql/<sentence>`. Replaces the old "polis" wordmark — the logged-in nav has no wordmark. The wordmark only appears on the logged-out landing nav (see "Logged-Out Nav" section below).

### Right Section
- **Pinned dot** (aria-hidden) and **handle label** (`.polis-handle-label`) — right-aligned. The handle label shows the current site context (your handle on your own pages, the visited author's handle on a foreign site).

## Icon States

| State | Background | Color |
|-------|------------|-------|
| Default | transparent | `--nav-icon` (muted) |
| Hover | `--nav-bg-hover` | `--nav-icon-hover` |
| Active | `--nav-bg-active` | `--nav-icon-active` |
| Disabled | transparent | `--nav-icon` at 30% opacity |

## Notification Indicator Dots

Small red/orange dots (7px, `--nav-dot` color) positioned at the top-right of icon buttons. Indicate unread/pending items.

| Icon | Element ID | Dot appears when |
|------|------------|-----------------|
| Avatar | `nav-dot-avatar` | Any unread items across the system |
| Gateway | `nav-dot-gateway` | Unread activity from your network |
| Comment | `nav-dot-comment` | Pending blessing requests |
| Envelope | `nav-dot-envelope` | Unread DMs |
| Paragraph (Posts) | — | (typically no dot) |
| People | — | (typically no dot) |
| Edit (New post) | — | (no dot) |

The dot has a 1.5px border matching `--nav-bg` to create separation from the icon.

## Avatar Hover Menu

Pure CSS hover menu — appears on rollover, no click required. Positioned below the avatar, left-aligned.

**Contents:**
1. **Header** — full name (Inter 13px, weight 600) + handle (JetBrains Mono 11px, clickable → my content page)
2. **Stats row** — followers, following, posts (Inter 12px, tabular)
3. **Dashboard** link (with grid icon)
4. **Settings** link (with gear icon)
5. **Log out** link (with arrow icon, muted color, red on hover)

**Styling:** White/light background (`#fff`), 200px min-width, 8px border-radius, subtle shadow. Text uses dark page colors for readability regardless of the nav theme.

## Edit Icon Behavior

The edit icon (formerly a separate "+ button") opens the editor inline. There is no longer a dropdown of creation options in the nav — the editor itself surfaces the relevant choices (new post, new comment, new message) based on context.

## Contextual Icon Behavior

Each icon button loads a PQL sentence. The sentence vocabulary lets the *same* icon adapt to whose site you're viewing: scope words like `my network`, `my followers`, `everyone`, or a specific handle change what content the sentence resolves to. Tooltips reflect the resolved sentence on the current site.

### On Your Pages (your handle's webapp SPA)
| Icon | Tooltip | Loads (example PQL sentence) |
|------|---------|-------|
| Gateway | "Activity from my network" | `all activity from my network by date` |
| Paragraph | "My posts" | `all posts from me by date` |
| Comment | "Comments to bless" | `all comments to bless from my network by date` |
| People | "Profiles" | `all profiles from my network by name` |
| Envelope | "My messages" | `all messages from my mutuals by date` |
| Edit | "New post" | (opens the editor) |

### On Someone Else's Site
On a foreign site, the icon nav is overlaid via the polis widget (see "Autohide Behavior" below) and the scope vocabulary of each preset shifts to the visited site's handle. Site-specific info (policies, metadata) is surfaced via the avatar dropdown or a dedicated info action, not a separate gear icon.

## Autohide Behavior (Foreign Sites)

When visiting someone else's site, the nav autohides to give their content full respect.

### Timing
1. On arrival, nav is visible for **2 seconds**
2. After 2 seconds, if mouse is not over the nav, it slides up to `height: 0` over **250ms** (cubic-bezier ease)
3. A **40px-wide, 3px-tall accent-colored pill** appears centered at the top of the viewport as a hint

### Reveal
1. An invisible **6px trigger zone** spans the full width at the top of the viewport
2. Mouse entering the trigger zone immediately reveals the nav (slides down)
3. Mouse leaving the nav triggers a **400ms delay** before hiding again
4. Mouse re-entering the nav during the delay cancels the hide

### Architecture
The nav **pushes content down** when visible — it is never an overlay. This is a deliberate architectural choice. Alice's site renders at full fidelity in every state. The nav is a container, not a floating panel.

## Logged-Out Nav (System Topbar)

On the polis.pub landing page (not logged in), the nav is a different element:

- **Height:** 50px (matches logged-in nav)
- **Background:** `rgba(33,28,53,0.92)` with `backdrop-filter: blur(12px)` — frosted glass over SOLS
- **Sticky** at top of viewport
- **Contents:** centered "polis" wordmark + right-aligned "Sign in | Join"
- **"Sign in"** — muted color, hover brightens
- **"Join"** — peach accent color, weight 600, subtle hover background
- No icons, no avatar, no + button
