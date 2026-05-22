# Brand Identity

## Logo / Wordmark

The polis logo is the word "polis" set in **Newsreader italic** at weight 500-600. It is not a graphic mark — the typography *is* the brand.

| Context | Size | Weight | Notes |
|---------|------|--------|-------|
| System topbar (logged out) | 20px | 500 | Centered in viewport, brightest text color |
| Icon nav bar (logged in) | 20px | 600 | Centered in viewport, `--nav-icon-active` color |
| Footer | 20px | 500 | Left-aligned, paired with tagline |

**Rules:**
- Always italic, always Newsreader
- Always centered horizontally in the full viewport (not the content column)
- Never paired with a graphic icon or symbol
- The wordmark is branding, not navigation. Clicking it scrolls to top of the current page. It does not navigate between pages.
- No notification indicator dot on the wordmark

**Tagline:** "the space we share" — used in the landing page hero and footer. Set in Newsreader italic, smaller size, muted color.

## Typography

Three font families, each with a distinct role:

### Newsreader (serif)
- **Role:** Content, headings, brand expression
- **Usage:** Post titles, excerpts, hero headlines, the "polis" wordmark, bio text, tagline
- **Character:** Literary, warm, editorial. Says "this is a place for writing."
- **Weights:** 400 (body), 500 (headings), 600 (wordmark)
- **Always italic** for the wordmark and hero headlines. Regular for content body.

### Inter (UI sans-serif)
- **Role:** Interface elements, navigation, labels, metadata
- **Usage:** Nav tooltips, section labels, timestamps, stats, buttons, form inputs, stream action lines ("marina published...")
- **Character:** Clean, functional, disappears. The UI should feel like it's not there.
- **Weights:** 400 (body), 500 (labels/links), 600 (buttons/counts), 700 (avatar initials)

### JetBrains Mono (monospace)
- **Role:** Handles, domains, technical identifiers
- **Usage:** `vdibart.polis.pub`, `.polis.pub` suffix in forms, step visuals, migration arrows
- **Character:** Technical credibility. Signals "this is real infrastructure."
- **Weight:** 400 only

### Sizing Scale
- Hero headline: 48px (Newsreader italic)
- Section headline: 32px (Newsreader)
- Post title (in stream): 16px (Newsreader, weight 500)
- Post title (on site page): 18px (Newsreader, weight 500)
- Body text (landing page): 17-19px (Newsreader)
- UI text: 14px (Inter)
- Labels/meta: 11-13px (Inter)
- Handles: 13px (JetBrains Mono)

## System Color Palette (SOLS)

SOLS is the system theme — what you see when logged out. It is the brand palette. It is **not** available as a personal theme (see [theme-system.md](theme-system.md) for why).

### Backgrounds
| Token | Value | Usage |
|-------|-------|-------|
| bg | `#211c35` | Page background (landing-hybrid uses this) |
| bg-alt | `#1a1525` | Slight variation |
| bg-raised | `#2a2440` | Form inputs, elevated surfaces |
| bg-card | `#2e2848` | Cards, panels, steps |

### Text
| Token | Value | Usage |
|-------|-------|-------|
| text | `#f0e8dc` | Primary text (warm cream) |
| text-dim | `#a89e90` | Secondary text, subtitles |
| text-muted | `#7a7068` | Tertiary text, timestamps, meta |

### Accents
| Token | Value | Usage |
|-------|-------|-------|
| peach | `#e8a060` | Primary accent — headlines, CTAs, links, site card handles |
| peach-hover | `#f0b070` | Hover state for peach elements |
| pink | `#d4829a` | Secondary accent — section labels, target authors, blessing step labels |
| violet-line | `#3d3558` | Borders, dividers, separator lines |
| green | `#6fcf7c` | Live activity indicator dot |

### Alert
| Token | Value | Usage |
|-------|-------|-------|
| dot-red | (per-theme) | Notification indicator dots on nav icons |

## Tone and Voice

Polis communication is **confident, direct, and literary**. Not corporate, not startup-y, not whimsical.

**Do:**
- Write in short declarative sentences
- Use the second person ("your words", "your domain")
- Let the product speak for itself — the activity stream IS the pitch
- Use serif for anything meant to be read, sans-serif for anything meant to be scanned

**Don't:**
- Use exclamation marks in UI copy
- Say "we" when you mean the platform (say "polis")
- Use buzzwords (decentralized, web3, protocol, blockchain)
- Explain what polis isn't — show what it is

**Examples:**
- Good: "The space we share."
- Good: "Your words, your domain, your key, owned by you."
- Bad: "The decentralized social platform that puts you in control!"
- Bad: "Unlike other platforms, we don't..."
