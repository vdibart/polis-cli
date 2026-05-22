# PQL v0 — Polis Query Language

> See also: [infinity-stream.md](infinity-stream.md) (the surface PQL drives) and [policy-grammar.md](policy-grammar.md) (the sibling grammar PQL is intentionally aligned with).

PQL is the sentence-filter grammar that drives every filter view in
the v4 stream surface. The same sentence you see at the top of the
stream ("all activity from my network") is also the bookmarkable URL
form (`/_/pql/all+activity+from+my+network`). This doc is the
canonical spec — what tokens are valid, how they compose, and how
they map to the URL.

## Why PQL exists

A normal feed has hidden filter state — a "tab" or a "mode" the user
has clicked into, which the URL doesn't reflect. Switching means
clicking around. Sharing a filter means screenshots. A teammate
asking "how did you see that?" gets a description, not a link.

PQL turns the filter into a **sentence the user can read and a URL
the user can share.** Every view in the stream surface is one PQL
sentence. The sentence is the source of truth: change the URL,
the stream re-filters; change the slots in the topbar widget, the
URL updates. There is no separate "filter state" hiding anywhere.

That property — *the filter is a first-class citizen, in the URL,
in human-readable form* — is what makes PQL more than a query
syntax. It's how polis avoids the modal-nav trap (see
[infinity-stream.md § Why a single screen](infinity-stream.md#why-a-single-screen)).

## TL;DR

| Concept | Form |
|---|---|
| Sentence (UI) | `all activity from my network` |
| URL | `/_/pql/all+activity+from+my+network` |
| Encoding | every token separated by `+` |
| Grammar | `qualifier type "from" scope [modifier]` |

The URL is isomorphic to the displayed sentence — spaces in the UI
become `+` in the URL, nothing else.

## Grammar

```
sentence    := qualifier type "from" scope [modifier]

qualifier   := "all" | "new"
               ; "new" is reserved but currently locked off in the UI
               ; (see "Reserved tokens" below).

type        := "posts" | "comments" | "profiles"
             | "messages" | "drafts" | "activity"

scope       := "me"
             | "my" "network"
             | "my" "mutuals"
             | "all" "polis"
             | <handle>
               ; <handle> is a fully-qualified domain (e.g.
               ; alice.polis.pub, sub.alice.polis.pub).

modifier    := "by" sort_key
             | "with" filter_key
             | "to" action_key

sort_key    := "date" | "name" | "activity"
filter_key  := "comments"
action_key  := "bless"
```

The parser tokenizes on `+` (in the URL) or whitespace (in the
displayed sentence), then walks the token stream positionally.
Multi-token atoms (`my network`, `all polis`, `by date`) are matched
by position; keyword-led clauses (`by <key>`, `with <key>`,
`to <key>`) take the next token as their value.

**Why keyword-led modifiers?** Future growth. Adding `by popularity`,
`to review`, `with images` is a one-line vocabulary update — not a
grammar restructure. No `by-popularity-cruft` ever enters the
language as a fused atom.

## Type-conditional rules

The grammar is permissive — any well-formed sentence parses — but
the dispatcher (server + UI) narrows valid combinations per type:

### `type=posts`
- Allowed scopes: `me`, `my network`, `my mutuals`, `all polis`, `<handle>`
- Allowed modifiers: `by date` (default), `with comments`
- Personal-lock: none

### `type=comments`
- Allowed scopes: `me`, `my network`, `my mutuals`, `all polis`, `<handle>`
- Allowed modifiers: `by date` (default), `to bless`
- Type-conditional: `scope=me` drops `to bless` (your own comments
  don't need blessing).

### `type=profiles`
- Allowed scopes: `my network`, `all polis` (the only meaningful
  vocabulary for a directory view)
- Allowed modifiers: `by name` (default), `by activity`

### `type=messages`
- Allowed scopes: `my mutuals` (inbox view), `<handle>.polis.pub`
  (conversation view with that mutual)
- Rejected scopes: `me`, `my network`, `all polis` (DMs are 1:1
  between mutuals; these don't apply)
- Allowed modifiers (inbox): `by date` (default), `by name`
- Allowed modifiers (thread): `by date` only — modifier slot hidden
  in conversation view since there's no meaningful sort choice.
- Type-conditional scope dropdown: pinned `↩ my mutuals` (returns
  to inbox) + the 10 most-recently-active conversations as a
  fast-switcher (see `docs/design/v4/mockups/07-messages.html`).

### `type=drafts`
- Personal-lock: forces `scope=me` (drafts are local to the owner)
- Allowed modifiers: `by date` (default), `by name`

### `type=activity`
- Allowed scopes: any
- No modifier slot (chronological is the only natural ordering)

## URL encoding

Path-form: `/_/pql/<sentence-with-+>`.

- Every sentence token is separated by `+`.
- The URL form is isomorphic to the displayed sentence — spaces in
  the UI become `+` in the URL, nothing else.
- Handles (`alice.polis.pub`) keep their dots — domain separators
  are inside the token, not between tokens.
- `decodeURIComponent` + `+` → ` ` reproduces the displayed sentence.

The bare path `/_/` is the canonical default landing — it lands on
the user's default filter (`all activity from my network`) without
the `/pql/` prefix. Any unknown `/_/*` path falls back to this
default with `replaceState`, so retired bookmarks land somewhere
working with a clean URL bar.

## Worked examples

Every icon-preset in the topbar nav maps to a canonical PQL URL:

| Icon | Sentence | URL |
|---|---|---|
| **gateway** | all activity from my network | `/_/pql/all+activity+from+my+network` |
| **paragraph** | all posts from me by date | `/_/pql/all+posts+from+me+by+date` |
| **comment** | all comments from all polis to bless | `/_/pql/all+comments+from+all+polis+to+bless` |
| **people** | all profiles from my network by name | `/_/pql/all+profiles+from+my+network+by+name` |
| **envelope** | all messages from my mutuals by date | `/_/pql/all+messages+from+my+mutuals+by+date` |

Selecting an entry in a list view often changes the scope to a
specific handle, producing URLs like:

| Sentence | URL |
|---|---|
| all messages from alice.polis.pub by date | `/_/pql/all+messages+from+alice.polis.pub+by+date` |
| all posts from alice.polis.pub by date | `/_/pql/all+posts+from+alice.polis.pub+by+date` |

## The sentence-filter widget

The topbar's centered widget is a UI surface for composing PQL sentences without typing. It is the inverse of the URL parser: the user manipulates slots, the widget produces a sentence, the sentence becomes a URL.

```
   ┌──────────────────────────────────────────────────────────────────────┐
   │  [all]  [activity]  from  [my network]  [by date]   [site search…]   │
   │  ^      ^                  ^             ^           ^                │
   │  │      │                  │             │           └ handle search  │
   │  │      │                  │             └ modifier slot              │
   │  │      │                  └ scope slot                               │
   │  │      └ type slot                                                   │
   │  └ qualifier slot (currently locked to "all"; "new" reserved)         │
   └──────────────────────────────────────────────────────────────────────┘
```

**Slot behavior.** Each slot is a dropdown wired to the vocabulary documented above. Slot changes immediately:

1. Update the local widget state.
2. Compose the new sentence (`qualifier type "from" scope [modifier]`).
3. `replaceState` the new URL (`/_/pql/<sentence>`).
4. Notify the stream controller (`window.PolisStream.setFilter(state)`), which re-runs the fetch + render cycle.

**Type-conditional slot visibility.** When `type=messages` is selected on a thread view, the modifier slot hides (no meaningful sort choice). When `type=drafts` is selected, the scope slot locks to `me`. When `type=activity` is selected, the modifier slot disappears. The widget enforces what the [type-conditional rules](#type-conditional-rules) document — every UI affordance maps directly to a vocabulary rule.

**Site-typeahead.** The last slot is free-form: type a polis handle (`alice.polis.pub`) and the widget swaps scope to that handle, producing `… from alice.polis.pub …`. Typeahead suggestions come from the user's follow list, recent visits, and (future) DS search.

**Icon-row presets vs widget composition.** Clicking an icon button (gateway, paragraph, comment, people, envelope) loads a *preset* PQL sentence — fast common views. Composing in the widget is the *general case* — anything the grammar allows. Both produce URLs that look the same; nothing about a preset path is privileged.

## Reserved tokens

These exist in the grammar but are currently not surfaced in the UI:

- **`new` qualifier** — reserved. The dropdown only ships `all`
  pending a revisit of R22 #12 (read-state tracking proved unreliable
  enough that filtering on it produced confusing results, so the
  `new` option was withdrawn).

## Removed tokens

These were once valid PQL but are no longer:

- **`follows` type** — removed by the 06-profiles work. "Follows" was
  a verb pretending to be a noun. The replacement: follow events
  surface inside `type=activity` as terse single-line entries
  ("vdibart.polis.pub followed cyrus.polis.pub"); profile management
  uses `type=profiles`. Old URLs like `/_/pql/all+follows+from+me+by+name`
  parse to "unknown type" and fall back to the default filter.

## Tier model

PQL captures the **high-order filter view**. It deliberately does
not capture sub-states:

| State | URL behavior |
|---|---|
| Filter view | `/_/pql/<sentence>` (URL-addressable) |
| Focus mode on a post | URL stays the same (modal, dismissed by ESC) |
| Inline editor open | URL stays the same (modal, dismissed by close) |
| Comment thread expanded | URL stays the same (modal) |
| DM composer | URL stays the same (modal) |

This is a deliberate choice — sub-states have natural dismissal
gestures (ESC, click-outside, explicit close) and don't need history
fidelity. The URL captures the answer to "how do I see X?" — the
filter view a user would share in a message-board recommendation —
not every UI state. Browser back exits the filter, not the sub-state.

## Internal value mapping

The parser maps between PQL grammar tokens (user-facing, with
spaces) and internal JS values (existing dispatch code, with
hyphens):

| PQL grammar | Internal value |
|---|---|
| `messages` | `dms` |
| `my network` | `my-network` |
| `my mutuals` | `my-mutuals` |
| `all polis` | `all-polis` |
| `by date` | `by-date` |
| `by name` | `by-name` |
| `by activity` | `by-activity` |
| `with comments` | `with-comments` |
| `to bless` | `to-bless` |
| `<handle>` | `@<handle>` |

The mapping is only at the URL boundary — server routes, code
identifiers, and config keys all use the internal form. Adding a
new vocabulary term: add a row to the parser's lookup tables.

## Future direction

PQL is intentionally aligned with [`policy-grammar.md`](policy-grammar.md) (the inbound-rule grammar — same `qualifier type scope` shape). A user fluent in one is fluent in both. That alignment isn't decoration; it's a bet that *sentence-as-filter* and *sentence-as-rule* converge as polis matures.

### Near-term extensions

- **Cross-network queries.** `ds.polis.pub/?pql=…` accepting the same vocabulary for cross-tenant queries. The grammar is host-agnostic; only the dispatcher differs. A reader without a polis site of their own could compose a PQL sentence against a DS and get JSON back.
- **Public-surface PQL.** Per-tenant pages (`https://alice.polis.pub/`) could surface a PQL filter widget with a constrained vocabulary — no owner-only types like `drafts`, scope locked to the tenant. A reader visiting Alice's site sees "all activity from alice.polis.pub" by default and can narrow to "all comments from alice.polis.pub by date" with one slot change.
- **CLI integration.** `polis stream "<sentence>"` runs a PQL sentence against the local feed cache and prints results. The CLI parser would share the vocabulary listed here.
- **New vocabulary.** Adding `by popularity`, `to review`, `with images`, `with attachments`, `from my topic <tag>`, etc. is intentionally cheap — modifiers are keyword-led, so each new term is one row in `pql.js`'s lookup tables and one row in this doc.
- **Read-state revisited.** The `new` qualifier is currently locked off; future work re-enabling unread-tracking would surface it again. Same for "trending" / "popular" qualifiers that depend on cross-tenant signals only the DS can compute.

### Architectural ambitions

- **PQL as a programmable surface.** A future polis tool might accept a PQL sentence as its input — "give me all posts from my network with comments, by date" — and return JSON. A bot, a digest emailer, a translation pipeline, an RSS bridge.
- **Policy ↔ PQL convergence.** A user-author who writes `deny pub.polis.comment from all polis at spam.example.com` in their policy file is already speaking a PQL-shaped sentence. A future tool could let an author *select content via PQL* and then *write a policy rule from the same selection*. The shared grammar makes this a one-step UI.
- **Saved sentences.** A user marking a sentence as a favorite — "all comments from my network to bless" — gets a personalized icon-row preset. The plumbing is already there; the UX is the gap.
- **Composed sentences.** Future grammar growth might allow `and` / `or` between scopes ("from my network or from alice.polis.pub"). The parser is positional today; intersecting/uniting scopes requires a small grammar extension.

When PQL grows beyond the owner SPA, this doc is the authoritative spec — the JS parser, a future Go parser, a future bash CLI parser should all agree on the vocabulary documented here. The grammar is the contract.

## Pointer

Parser implementation: `webapp/internal/webui/www/pql.js`.

`plans/todo.md`'s GRAMMAR section is a one-line pointer to this
doc — that's the canonical place to look. Prior to chunk B (v3
SPA route cleanup), the grammar was duplicated in `plans/todo.md`
and drifted from the implementation in five distinct ways. This
doc is the resolution.
