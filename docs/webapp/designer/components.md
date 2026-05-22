# Component Patterns

## Stream Item

The primary content unit in the feed. Shows a single activity event.

```
┌─────────────────────────────────────────────────────┐
│ [AV]  author  action-label                    time  │
│       Post Title in Newsreader                      │
│       Two-line excerpt in serif, muted color,       │
│       clamped with overflow...                      │
│       3 comments · 2 blessed                        │
└─────────────────────────────────────────────────────┘
```

| Element | Font | Size | Color |
|---------|------|------|-------|
| Avatar | — | 28px circle | Gradient per-author |
| Author | Inter | 13px, weight 600 | `--page-text` |
| Action | Inter | 12px | `--page-text-3` |
| Time | Inter | 11px, tabular-nums | `--page-text-3` |
| Title | Newsreader | 16px, weight 500 | `--page-text` |
| Excerpt | Newsreader | 13.5px | `--page-text-2`, 2-line clamp |
| Stats | Inter | 11px | `--page-text-3` |

**Spacing:** 14px padding, 12px gap between avatar and body, 2px gap between items.
**Interaction:** Hover shows subtle background change. Click navigates to the author's site.

## Blessing Row

Compact row for pending blessing requests on the dashboard.

```
┌──╎ alice  On the Ethics of Content Moderation  12m  [Grant] [View] │
```

| Element | Font | Size | Color |
|---------|------|------|-------|
| Left border | — | 2px | `--page-accent` |
| Author | Inter | 13px, weight 500 | `--page-text` |
| Post title | Newsreader | 13px | `--page-text-2`, truncated |
| Time | Inter | 11px | `--page-text-3` |
| Grant button | Inter | 11px, weight 600 | Green on green-soft bg |
| View button | Inter | 11px, weight 600 | Muted on subtle bg |

**Spacing:** 10px vertical padding, 16px horizontal, 12px gap between elements.
**Interaction:** Click row to expand and show comment preview. Click Grant/View for direct action.

## Post Row

Compact post listing for dashboard and content pages.

```
┌─────────────────────────────────────────────────────┐
│ Post Title in Newsreader                            │
│ 2 days ago · 8 comments · 2 blessed                │
└─────────────────────────────────────────────────────┘
```

| Element | Font | Size | Color |
|---------|------|------|-------|
| Title | Newsreader | 16px, weight 500 | `--page-text` |
| Meta | Inter | 12px | `--page-text-3` |
| Draft badge | Inter | 10px, weight 600 | `--page-accent` on accent-soft bg |

**Spacing:** 12px padding, 2px gap between rows.

## Site Post

Post listing on a user's public site page. More spacious than post rows.

```
March 19, 2026
Post Title in Larger Newsreader
Excerpt text that can run longer, in serif, showing the
opening of the post...
```

| Element | Font | Size | Color |
|---------|------|------|-------|
| Date | Inter | 12px | `--page-text-3` (or theme equivalent) |
| Title | Newsreader | 18px, weight 500 | `--page-text` |
| Excerpt | Newsreader | 14.5px | `--page-text-2` |

**Spacing:** 18px vertical padding. No card background — posts sit directly on the page.

## Site Header

Identity block at the top of a user's content page.

```
[AVATAR]  Full Name
          handle.polis.pub

    Bio text indented to align with the name,
    max-width 480px.

    12 posts · 18 followers · 24 following
    ─────────────────────────────────────────
```

| Element | Font | Size | Color |
|---------|------|------|-------|
| Avatar | — | 48px circle | Theme-derived gradient |
| Name | Newsreader | 22px, weight 500 | `--page-text` |
| Handle | JetBrains Mono | 13px | `--page-text-3` |
| Bio | Newsreader | 15px | `--page-text-2` |
| Stats | Inter | 12px | `--page-text-3`, bold counts |

**Layout:** Avatar and name/handle in a flex row. Bio and stats indented 62px (avatar width + gap) to align with the name.

## Section Header

Used on the dashboard to label content groups.

```
BLESSING REQUESTS                              View all 3
```

| Element | Font | Size | Color |
|---------|------|------|-------|
| Label | Inter | 11px, weight 500, uppercase | `--page-text-3` |
| Action | Inter | 12px, weight 500 | `--page-accent` |

**Spacing:** 20px top padding, 10px bottom padding. First section header: 16px top.

## Date Separator

Used in the feed stream to group items chronologically.

```
TODAY
```

| Element | Font | Size | Color |
|---------|------|------|-------|
| Label | Inter | 11px, weight 500, uppercase, letterspaced | `--page-text-3` |

**Spacing:** 16px top padding, 6px bottom.

## Buttons

### Primary (+ button, CTA)
- Background: `--nav-accent` (nav) or `--peach` (landing)
- Text: white or dark bg color
- Border-radius: 100px (pill) for nav, 8px for forms
- Font: Inter, 15px, weight 600

### Secondary (Grant, View)
- Background: subtle tinted bg (green-soft, neutral)
- Text: corresponding color
- Border-radius: 100px
- Font: Inter, 11px, weight 600

### Ghost (Sign in, nav links)
- Background: transparent
- Text: muted, brightens on hover
- No border

## Form Inputs

Used in the landing page signup form.

| Property | Value |
|----------|-------|
| Background | `--bg-raised` (dark surfaces) |
| Border | 1px solid `--violet-line` |
| Border-radius | 8px |
| Text color | `--text` |
| Placeholder | `--text-muted` |
| Focus | Border color → accent |
| Font | Inter, 15px |
| Padding | 12px 16px |

The handle input has a `.polis.pub` suffix (Inter 14px, muted, absolutely positioned right).

## Notification Dot

Small indicator for unread/pending items.

| Property | Value |
|----------|-------|
| Size | 7px diameter |
| Color | `--nav-dot` |
| Border | 1.5px solid `--nav-bg` |
| Position | Absolute, top-right of parent icon button |
| Avatar special | Positioned at 2 o'clock, outside circle boundary |

## Tooltips

Appear on hover for nav icon buttons.

| Property | Value |
|----------|-------|
| Background | `#2a2520` |
| Text | `#eae5dc` |
| Font | Inter, 11px, weight 500 |
| Padding | 3px 8px |
| Border-radius | 4px |
| Position | Below icon, centered horizontally |
| Timing | Appears on hover with 150ms fade |

## Dropdown Menus

Used for the avatar menu and + button menu.

| Property | Value |
|----------|-------|
| Background | `#fff` |
| Border | 1px solid `--nav-border` |
| Border-radius | 8px |
| Shadow | `0 8px 24px rgba(0,0,0,0.12)` |
| Min-width | 190-200px |
| Padding | 4px |
| Item padding | 8px 12px |
| Item hover | `rgba(0,0,0,0.04)` bg |
| Item font | Inter, 13px |
| Item icon | 15-16px SVG, 50% opacity |
| Separator | 1px line, `--page-border` color, 4px vertical margin, 8px horizontal inset |
| Animation | 120ms fade + 4px slide up |

Dropdown text always uses dark colors for readability, regardless of the nav theme.
