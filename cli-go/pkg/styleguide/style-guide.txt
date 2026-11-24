# polis v5 style guide

*For* [Developers](../../README.md#building-on-polis) · [Contributors](../../README.md#contributing-to-polis) — *Kind* [Reference](../../README.md#kinds-of-page) — *Component* [Webapp](../README.md) — *Code* [`webapp/internal/webui/www/v5`](../../../webapp/internal/webui/www/v5)

The rules every v5 page follows: the owner app on every instance (`/_/`) and every site on the v5
shape. They were drawn from the v5 mockups and are held to them. When a page and this guide
disagree, one of them is a bug.

**Its place.** This guide holds the visual rules of the v5 design; the behaviour (how each section
works, what it shows and asks for, the names and addresses of every place) is the v5 spec's, which
points here for anything visual. The two never repeat each other.

**For an agent changing a site:** keep to these rules. A theme supplies tokens, never layout; the
token contract is the [themes](../../general/concepts/themes.md) page's. The same text is given to
agents over MCP (the `polis://style-guide` resource) and served beside polis.pub's agent files, at
`/for/agents/style-guide`, and on a self-hosted instance at the same path.

**How to read it.** Numbers are CSS pixels at 1x. "The mark" is the 30px circle at a card's left: an
avatar, or an icon for a settings group. Where a rule says *measured*, it was checked from pixels at
1x and 2x, not set by eye (see [Alignment](#4-alignment)).

---

## 1. Tokens

Every v5 page declares the same `:root` tokens. Use the token, never its value.

### Colour

| Token | Value | Use |
|---|---|---|
| `--bg-light` | `#e0deda` | page background, top bar |
| `--bg` | `#d8d6d2` | footer |
| `--surface` | `#eceae6` | cards, the section box, active filter |
| `--panel` | `#d4d2ce` | hints, quiet chips |
| `--text` | `#1c1c2a` | titles, handles, the words that lead |
| `--text-soft` | `#3a3a48` | body text, event verbs |
| `--text-muted` | `#605c56` | dates, times, labels, secondary links |
| `--ink` | `#f8f5ee` | text on a filled button |
| `--line` | `rgba(138,108,4,.22)` | rules inside cards, hairlines |
| `--peach` / `--peach-soft` / `--peach-dim` | `#8a6c04` / `#6e5600` / `#b39a50` | polis gold: primary actions, write, enhance |
| `--teal` / `--teal-soft` | `#0090b8` / `#00728f` | links, gather |
| `--pink-soft` | `#7a4e20` | copper: message |
| new / unread | `#c2412d` | the 6px dot only |
| wordmark | `#005a72` | the "polis" wordmark only |

**Section accents.** Each signed-in section has one accent, used for the box stroke, the card stroke
(`--sec-line`, at .28 alpha) and the active filter:

| Section | Accent | `--sec-line` |
|---|---|---|
| write | gold `--peach-soft` | `rgba(110,86,0,.28)` |
| gather | teal `--teal-soft` | `rgba(0,114,143,.28)` |
| message | copper `--pink-soft` | `rgba(122,78,32,.28)` |
| enhance | gold `--peach-soft` | `rgba(110,86,0,.28)` |

**The public site** is the author's page, not polis's: its accent is the theme's `--site`
(`#397f84` by default) and its strokes `--site-line`. Anything coloured on the site derives from
`--site` (`color-mix(in srgb, var(--site) N%, transparent)`), so a theme change carries through.
polis gold never appears on a public page.

### Type

Three families, each with one job:

- **Inter** (`--font-ui`): interface. Handles, dates, buttons, menus, filters, labels.
- **Newsreader** (`--font-content`): what people wrote. Titles, bodies, comments, event sentences.
- **JetBrains Mono** (`--font-mono`): small uppercase labels, counts in filters, the footer, code.

| Role | Spec |
|---|---|
| Title (card) | Newsreader 500 22px / 1.3, `--text` |
| Section title (enhance group) | Newsreader 500 21px / 1.3, `--text` |
| Body (collapsed post) | Newsreader 400 16.5px / 1.75, `--text-soft` (untitled: `--text`) |
| Second line (event link, bio, joined) | Newsreader 400 17px / 1.5, `--text-soft` |
| Comment (in its panel) | Newsreader 400 16.5px / 1.7, `--text` |
| "replied to …" line | Newsreader 400 15px / 1.5, `--text-muted`, quoted title `--text-soft` |
| Handle (gather first row) | Inter 500 13.5px, `--text`; the verb after it 13.5px `--text-soft` |
| Date / time | Inter 400 12.5px, `--text-muted` |
| Footer row of a card | Inter 13px, `--text-muted`; counts in JetBrains Mono 500 13px |
| Button (`.slim`) | Inter 500 13px, letter-spacing .02em; in a card, 12.5px |
| Text link (`.link`) | Inter 500 13px, `--text-muted`, underlined at 3px offset |
| Label | JetBrains Mono 500 11px, letter-spacing .04em, `--text-muted` |
| Page footer | JetBrains Mono 400 12px, `--text-muted` |
| Wordmark | Newsreader italic 700 30px, letter-spacing .06em, `#005a72` |

### Motion

One curve, `--ease: cubic-bezier(.22,.8,.24,1)`. Section changes .48–.55s; hover colour .2–.25s;
a collapsed body opens over .5s. Everything respects `prefers-reduced-motion`.

---

## 2. The page frame

Identical on every page; only the section accent changes.

- **Top bar** (`.topbar`): fixed, 64px (`--bar`), `--bg-light`, 20px side padding. Left: the
  wordmark (signed out) or your mark (signed in). Right of the column: the section menu.
- **The section box** (`.box`): 44px tall, 10px from the top, radius 12, `--surface`, 1px stroke in
  the section accent. It holds the section's own controls (search, filters, the + button) at the
  left and the current section's name at the right; the other sections sit outside it.
- **Menu items** (`.way`): Inter 500 13px, 8px 12px padding, radius 8, `--text-muted`; the current
  one in its section colour. Order is always **gather, write, message, enhance** (gather leads, 2026-11-17), the current one
  moving into the box.
- **Menu spacing**: items sit 2px apart. The current one sits 5px in from the box's right edge, and
  the next one only 4px after it, just clear of the box's stroke, whether the others trail outside
  the box or tuck inside it. ⛔ No extra air at the box's edge: a wider gap there leaves the
  second item looking detached from the current one (2026-11-18). The same in the owner's app and on
  the public site (v5.css `.menu .trail`).
- **Filters** (`.views button`): Inter 500 12.5px, 5px 11px, radius 7, inside a 3px-padded tray;
  the active one `--surface` with a hairline and the section accent. A count precedes its noun
  ("6 comments"); a waiting or new count carries the red dot.
- **Column** (`.col`): max 680px, centred, 24px side padding, content starting 18px under the bar,
  140px clear at the bottom.
- **Footer** (`.foot`): fixed, 52px (`--foot`), `--bg`, JetBrains Mono 12px.
- **Hints** (`.hint`): `--panel`, 12.5px / 1.45, 9px 12px, radius 8, max 300px, from the top right,
  gone after about three seconds. A hint explains; it never asks.

---

## 3. Cards

Everything in a list is a card, and every card is built the same way.

### Geometry

| | |
|---|---|
| Padding | **24px top, 34px right, 22px bottom, 66px left** (on the public site, a theme's `spacing` moves these; see [Alignment](#4-alignment)) |
| Radius | 14px (on the public site, times a theme's `corner-radius` factor) |
| Background | `--surface`; hover `#efede9` when the card opens |
| Stroke | 1px, the section's `--sec-line` (public site: `--site-line`) |
| Gap between cards | 10px |
| The mark | 30px circle, **left 22px, top 24px** (it hangs in the 66px gutter) |
| New / unread dot | 6px, `#c2412d`, 9px from the card's left edge, on the mark's centre line |

### The first row

The first row is **30px tall and shares the mark's centre line**. Everything on it (handle, verb,
date, time, edit) is centred on that line, measured to within about 0.75px of the centre of the
mark's letter.

There are two kinds of first row, and which one a card gets depends on whose page it is:

- **Your own things** (write, the public site, enhance): **the mark, then the date**; `edit` at
  the right of the same row, for the owner only. No handle: on your own page it goes without saying.
- **Other people's things** (gather, onboarding's gather): **the mark, the handle, then what they
  did** ("followed you", "commented on", "blessed your comment on", "joined polis", "followed by
  …"), **and the time at the right**. A post has no verb: its first row is just the handle.

### The second line

Whatever it is (a title, a link to a post, a bio, an untitled post's opening words, a "replied to …"
line), **the second line starts 15px below the bottom of the mark**, measured from the mark to the
top of the text's ink. The margins that produce 15px differ with the font:

| Second line | Margin after the first row |
|---|---|
| Title (22px / 1.3) | 12px |
| Untitled body (16.5px / 1.75) | 9px |
| Link, bio or sentence (17px / 1.5) | 11px |
| "replied to …" (15px / 1.5) | 12px |

### The body

A collapsed post shows **three full lines, and the fourth fades** (`max-height: 7em`, mask from
`calc(100% - 1.75em)`). A piece that fits **does not fade at all** (`.fits`, set by measuring). A
click on the card opens the whole piece; another folds it.

### Comments, set apart

A comment someone wrote, shown as the subject of a card (yours on write and the site; another
person's on gather), sits in a **quiet panel**: 10px 16px 11px 15px padding, a 2px rule at its left
edge, radius 0 10px 10px 0, and a faint tint. Gold on write and gather
(`rgba(179,154,80,.55)` rule, `rgba(138,108,4,.045)` tint); the theme's `--site` on the public site
(45% rule, 5% tint). Full-colour type. It marks the comment without competing with titles.

### Actions

- **Under the words, on their margin**, nudged in 10px: Follow, Follow back. Never in a corner.
- **On the first row, at the right**: `edit` (owner only). Nothing else.
- **In the footer**: everything about the piece (link out, counts, reply).

### The footer

A 1px `--line` rule 22px under the body, the row 16px under the rule, items 22px apart, Inter 13px.
Order, left to right:

1. **At the left, counts**: comments, then waiting (a count also closes the thread it opened)
2. **At the right**: copy the link, the link-out icon (inside the app only), then reply; on the
   site, the terms chip, then copy

"show less" sits above the rule, at the right, in quiet italics, while a card is open. The thread
opens under the footer with no second rule. The footer itself is not a click target.

A card with nothing for its footer has no rule.

### Links out

**Inside the app** (write, gather, onboarding) a published piece has a link-out icon, first in its
footer: the app is not the published page, and this is the way there. **On the public site** there
is none: the reader is already on the published page. A post's **title** is the link to its own
page (its date, when it is untitled); a comment's **quoted post title** links to that post.

### Card types

| Card | First row | Second line | Then | Footer |
|---|---|---|---|---|
| Post, yours (write) | mark · date · *edit* | title | body | link out · comments · waiting |
| Post, yours (site) | mark · date · *edit* | title (links to its page) | body | comments · terms |
| Post, theirs (gather) | mark · handle · time | title | body · Follow (Discover) | link out · comments · reply |
| Untitled post | as above | opening words (`--text`) | | as above |
| Comment, yours (write) | mark · date · *edit* | replied to "…" on site | comment panel | link out |
| Comment, yours (site) | mark · date · *edit* | replied to "…" (links) on site | comment panel | none |
| Comment, theirs (gather) | mark · handle · commented on · time | post title (link) | comment panel | reply |
| Event (gather) | mark · handle · verb · time | link or sentence, if any | Follow / Follow back | none |
| Person (Discover) | mark · handle · followed by … | bio | Follow | none |
| Conversation (message) | mark · latest message · *time · handle at right* | | thread when open | none |
| Settings group (enhance) | icon mark · group title | summary | rows when open | none |

---

## 4. Alignment

Two rules, both measured:

1. **The first row is on the mark's centre line.** The x-height middle of every piece of text on the
   first row sits within about 0.75px of the middle of the mark's letter, at 1x and 2x.
2. **Words below the mark start 15px under it**, measured to the top of the ink.

Rule 1 is optical: it is the letters that line up, not their boxes. On the public site both rules come
from the fonts' own metrics, so they hold whatever font a theme chooses: the mark's letter is centred
in its circle by its cap height, each piece of text on the first row by its x-height, and a title's
first line is trimmed to its cap height under a 15px margin (CSS `text-box`; a browser without it
centres whole lines, a little lower). Elsewhere, small offsets (`top: -.5px`, `padding-top: 3px`) in the
v5 stylesheets are the measured corrections that achieve this for a given font and size, each
commented *measured*. Change a font, size or line height there and re-measure: render at 1x and 2x,
find the mark's circle and the text's x-height band from pixels, and compare. At 1x the letter and the
text each land on whole pixels on their own, so they can part by one device pixel.

### The shape tokens keep both rules

On the public site a theme may choose three pieces of geometry, each from a short fixed scale and never
as a number: `corner-radius`, `spacing` and `stroke-weight` (the values are the
[themes](../../general/concepts/themes.md) page's). Each is built so the two rules hold by construction,
whatever the value:

- **The mark and the first row share one box** (`.hd`): the mark hangs at its top, and the row is
  centred in its 30px. Spacing moves that box, with the card's padding, and nothing else, so it can never
  part the mark from its row.
- **The space between the first row and the second line is a fixed margin** that no token touches
  (the table in *The second line* above). Spacing changes a card's padding, the gap between cards and
  the footer's rule; never that margin.
- **The mark keeps its distance from the words** (14px; 10px on a phone): spacing moves the mark with
  the card's left edge, so the card's left padding is the mark's distance from the edge, the mark, and
  the 14px, derived and never set on its own.
- **A heavier stroke grows outward** on a card (it is drawn as an outline, not a border) and inward on
  the section box, so it moves nothing inside either. The pinned row's sides and corners are derived
  from the card's padding, radius and stroke, so it still lies exactly over the card.
- **Corners scale; the mark does not.** Every corner is its default times one factor; the mark stays a
  circle, clear of a card's rounded corner at every value.
- **Every length a token sets is a whole pixel** (the corner factor aside), so edges fall on pixels at
  1x and 2x.

When a shape token is added or a value changes, measure every value (each with the others at their
defaults, and the extremes together) at 1x and 2x, and check each measures exactly as the default
does.

---

## 5. Words

- **Times.** Streams of what other people did (gather, message) use relative time: "just now", "2h",
  "yesterday", then a date without the year ("10 Nov"). Archives of your own work (write, the site)
  use the full date ("28 Aug 2026").
- **Handles** appear only where whose it is is a question: on gather and message, yes; on write and
  your own site, no.
- **Counts** lead their noun and agree with it: "1 comment", "2 comments".
- **Verbs** are lower case and past tense on gather: "followed you", "commented on".
- **Hints** say what something does, in a sentence, and never ask the reader to decide.

---

## 6. Small screens

At 560px and below:

- The menu drops its labels to icons; the page footer drops its links.
- A first row keeps the mark, the handle and the time on one line; the verb wraps after them.
- A conversation's time · handle becomes its own first row beside the mark, and the latest message
  moves below it, 15px under the mark.
- Nothing scrolls sideways.

---

## 7. Open questions

- ~~**Message's first row.**~~ **Decided 2026-11-16 (the v5 spec, 1.5):** conversations stay compact,
  leading with the latest message, time · handle in the corner. Matching gather's first row would make
  every conversation card taller.
- **The public site's comment cards** keep "replied to …" as the second line with no handle, by the
  "your own page" rule. If the site is ever read as a stream of other people's replies, it would need
  gather's first row instead.
