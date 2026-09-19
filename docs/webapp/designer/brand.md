# Brand Identity

*For* [Contributors](../../README.md#contributing-to-polis) — *Kind* [Reference](../../README.md#kinds-of-page) — *Component* [Webapp](../README.md)

## Logo / Wordmark

The polis logo is the word "polis" set in **Newsreader italic** at weight 550. It is not a graphic mark — the typography *is* the brand.

| Context | Size | Weight | Notes |
|---------|------|--------|-------|
| polis.pub landing topbar (logged out) | 24px | 550 | Left edge of the topbar, main text color at 90% opacity |
| Public site topbar (a tenant's stream shape) | 24px | 550 | Left edge; hidden once a logged-in visitor's nav widget fills the bar |
| Owner webapp (logged in) | — | — | No wordmark: the avatar and icon row take its place |

**Rules:**
- Always italic, always Newsreader
- Never paired with a graphic icon or symbol
- The wordmark is branding, not navigation. On the landing page it links to the top of the page.
- No notification indicator dot on the wordmark

**Tagline:** "yours, truly" — pinned in the landing topbar (Newsreader italic, 23px, pink), and closing the landing hero headline: *"Your story. Your community. Yours, truly."*

## Typography

Three font families, each with a distinct role:

### Newsreader (serif)
- **Role:** Content, headings, brand expression
- **Usage:** Post titles, excerpts, the "polis" wordmark, bio text, tagline. (The landing page's headlines are set in an italic Georgia serif stack.)
- **Character:** Literary, warm, editorial. Says "this is a place for writing."
- **Weights:** 400 (body), 500 (headings), 600 (wordmark)
- **Always italic** for the wordmark and hero headlines. Regular for content body.

### Inter (UI sans-serif)
- **Role:** Interface elements, navigation, labels, metadata
- **Usage:** Nav tooltips, section labels, timestamps, stats, buttons, form inputs, stream action lines ("marina published...") — in the webapp and stream. The landing page uses the system sans-serif stack instead.
- **Character:** Clean, functional, disappears. The UI should feel like it's not there.
- **Weights:** 400 (body), 500 (labels/links), 600 (buttons/counts), 700 (avatar initials)

### JetBrains Mono (monospace)
- **Role:** Handles, domains, technical identifiers
- **Usage:** `vdibart.polis.pub`, `.polis.pub` suffix in forms, step visuals, migration arrows
- **Character:** Technical credibility. Signals "this is real infrastructure."
- **Weight:** 400 only

### Sizing Scale
- Landing hero headline: fluid, 2.7rem–4.6rem (italic serif)
- Landing section headline: fluid, 1.9rem–2.7rem (italic serif)
- Post title (stream entry): 18px, 16px under 600px wide (Newsreader, weight 500)
- Body text (landing page): 17-19px (Newsreader)
- UI text: 14px (Inter)
- Labels/meta: 11-13px (Inter)
- Handles: 13px (JetBrains Mono)

## System Color Palette (SOLS)

SOLS is the system theme — what you see when logged out. It is the brand palette. It is **not** available as a personal theme (see [Themes](../../general/concepts/themes.md#what-ships-today) for why).

The values below are the landing page's own tokens (`:root` in `webapp/internal/hosted/landing.go`). ⚠️ The `sols` theme file in the core bundle (`themes/sols/sols.css`) shares the peach, pink and cream family but not every value — its page background, for instance, is `#1a1525`.

### Backgrounds
| Token | Value | Usage |
|-------|-------|-------|
| bg | `#211c35` | Page background |
| bg-raised | `#2a2340` | Form inputs, elevated surfaces |
| bg-subtle | `#332c4a` | Cards, subtle panels |

### Text
| Token | Value | Usage |
|-------|-------|-------|
| text | `#f2ebe0` | Primary text (warm cream) |
| text-dim | `#c4b4a2` | Secondary text, subtitles |
| text-muted | `#a49684` | Tertiary text, timestamps, meta |

### Accents
| Token | Value | Usage |
|-------|-------|-------|
| peach | `#e8a060` | Primary accent — CTAs, links, "Join" |
| peach-soft | `#f0c090` | Softer peach |
| pink | `#d4829a` | Secondary accent — the topbar tagline, labels |
| border | `rgba(212,130,154,0.15)` | Borders, dividers |
| green | `#a0d0a0` | Success messages, the "free" indicator dot |

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
- Good: "Your story. Your community. Yours, truly."
- Good: "Your words, your domain, your key, owned by you."
- Bad: "The decentralized social platform that puts you in control!"
- Bad: "Unlike other platforms, we don't..."
