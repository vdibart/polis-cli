# PQL v0 — Polis Query Language

*For* [Developers](../../README.md#building-on-polis) — *About* [Content](../README.md#content) — *Kind* [Spec](../../README.md#kinds-of-page) — *Code* [`cli-go/pkg/pql`](../../../cli-go/pkg/pql) — *See also* [concept](../concepts/infinity-stream.md) · [reference](../../ds/developer/pql-json-api.md)

> See also: [infinity-stream.md](../concepts/infinity-stream.md) (the surface PQL drives) and [policy-grammar.md](policy-grammar.md) (the sibling grammar PQL is intentionally aligned with).

PQL is the sentence grammar behind every list view in polis: each view
in the owner app's gather and write is a PQL sentence run on `/v1/query`,
and the same sentences are served to anyone at a site's `/pql/<sentence>`,
to agents through the `query_pql` tool, and across the network by the
discovery service. A view's sentence is also its address
(`/_/gather/pql/all+activity+from+my+network`). This doc is the
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
the view re-filters; change the view's filters, the URL updates.
There is no separate "filter state" hiding anywhere.

That property — *the filter is a first-class citizen, in the URL,
in human-readable form* — is what makes PQL more than a query
syntax. It's how polis avoids the modal-nav trap (see
[infinity-stream.md § Why a single screen](../concepts/infinity-stream.md#why-a-single-screen)).

## TL;DR

| Concept | Form |
|---|---|
| Sentence (UI) | `all activity from my network` |
| URL | `/pql/all+activity+from+my+network` (a site's); in the owner app `/_/gather/pql/all+activity+from+my+network` |
| Encoding | every token separated by `+` |
| Grammar | `qualifier type "from" scope [modifier]` |

The URL is isomorphic to the displayed sentence — spaces in the UI
become `+` in the URL, nothing else.

## Grammar

```
sentence    := qualifier [adjective] type relation scope [modifier]

qualifier   := "all" | "new"
               ; "new" is reserved but currently locked off in the UI
               ; (see "Reserved tokens" below).

adjective   := "blessed"
               ; only the comments the post's author blessed. It takes
               ; the modifier's place: a sentence has an adjective or a
               ; modifier, never both.

type        := "posts" | "comments" | "profiles"
             | "messages" | "drafts" | "activity"
             | <event-type>
               ; friendly aliases each expand to a SET of event types
               ; (e.g. "posts" = pub.polis.post.published +
               ; pub.polis.post.republished). A fully-qualified event
               ; type (pub.polis.follow.announced) is also accepted for
               ; operational precision — see "The type slot" below.

relation    := "from" | "about" | "on"
               ; "from"  = authored-by (the actor axis).
               ; "about" = concerns / is-directed-at (the involved axis;
               ;           maps to the DS involvedDomain filter).
               ; "on"    = the replies to one post; its scope is the
               ;           post's address. Served by a site, not the DS.
               ; Exactly one relation per sentence. On a tenant the
               ; relation+scope clause is OPTIONAL (see "Tenant-relative
               ; PQL" below).

scope       := "me"
             | "my" "network"
             | "my" "mutuals"
             | "all" "polis"
             | <handle>
             | <handle> "'s" "network"
             | <post-address>
               ; <handle> is a fully-qualified domain (e.g.
               ; alice.polis.pub, sub.alice.polis.pub).
               ; <post-address> follows "on" only: an https URL, kept
               ; as written (scheme and host lowercased), with no
               ; + ? # or spaces.

modifier    := "by" sort_key
             | "with" filter_key
             | "to" action_key

sort_key    := "date" | "name" | "activity"
filter_key  := "comments"
action_key  := "bless"
```

## The two relations: `from` and `about`

`from` selects by **who wrote it** (actor). `about` selects by **who
it concerns** (target/source — the event "involves" that handle):

| Sentence | Meaning |
|---|---|
| `all activity from my network` | events my network authored |
| `all activity about me` | events about me — someone blessed my comment, commented on my post, followed me (notifications/mentions) |
| `all activity about alice.polis.pub` | what's happening around Alice |

`about <handle>` maps onto the discovery service's existing
`involvedDomain` filter (target OR source = handle). Combining
relations (`from X about Y`) is reserved for a future grammar
extension; today a sentence carries exactly one.

## A post's comments: `on`

`all comments on <post address>` is **every comment made on that post**, oldest first, each naming its
author (`author_domain`, `author_url`) and marked `"blessed": true` or `false`. Blessing never filters it: whether a comment is blessed matters to the post's
author, and for anyone else it is information shown on each comment. `all blessed comments on <post address>`
asks for only the blessed ones.

⛔ **On a site's own posts, only blessed comments appear, to everyone but the site's owner** (who you ask decides
what you see: [content types](../concepts/content-types.md#what-blessing-does-and-why-the-answer-depends-on-who-you-ask)). Comments that
aren't blessed never appear next to an author's content on the author's own site: asked of `alice.polis.pub`
about one of Alice's posts, the sentence answers blessed comments only, through every surface the site serves
(its pages, `/pql/` and `/v1/query`). Alice herself, signed in or with a token carrying `read`, sees every
comment, marked. Asked about anyone else's post, it is every comment, marked.

| Sentence | Meaning |
|---|---|
| `all comments on https://alice.polis.pub/posts/20261113/hello.html` | the whole thread, each comment marked |
| `all blessed comments on https://alice.polis.pub/posts/20261113/hello.html` | only what Alice blessed |

The post's address keeps its case (paths are case-sensitive). `on` is for comments only, and a polis site
serves it from the discovery service's records; the discovery service's own `/pql/` refuses `on` and `blessed`
(`PQL_ON_UNSUPPORTED`, `PQL_BLESSED_UNSUPPORTED`).

## The type slot — aliases and fully-qualified event types

The friendly type tokens are **abbreviations**. `posts` is not one
event type — it expands to the *set* `{pub.polis.post.published,
pub.polis.post.republished}`, and carries a render shape. The slot also
accepts a **fully-qualified event type** (matching
`pub.polis.<seg>.<seg>…`) for operational precision — e.g.
`all pub.polis.follow.announced from alice.polis.pub` (used by the
parity/reconcile actors). FQ event types are a discovery-service / CLI
concern; the per-tenant webapp surface serves the alias (content-view)
types and rejects FQ types (use the DS `/pql/` endpoint for those).

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
- `to bless` is public, like every other modifier: blessing decides
  who is notified (the post author) and what the author's page
  shows, not who may ask which comments are waiting. Asked of the
  author's own site by anyone but the author, it is empty: see
  [what blessing does, and why the answer depends on who you ask](../concepts/content-types.md#what-blessing-does-and-why-the-answer-depends-on-who-you-ask).

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
  fast-switcher.

### `type=drafts`
- Personal-lock: forces `scope=me` (drafts are local to the owner)
- Allowed modifiers: `by date` (default), `by name`

### `type=activity`
- Allowed scopes: any
- No modifier slot (chronological is the only natural ordering)

## URL encoding

Path-form: `/pql/<sentence-with-+>`, and the same encoding wherever the owner's app puts a sentence in its own
addresses (`/_/<section>/pql/<sentence>`; the full set is [Addresses](../../webapp/developer/development.md#addresses)).

- Every sentence token is separated by `+`.
- The URL form is isomorphic to the displayed sentence — spaces in
  the UI become `+` in the URL, and a post address's slashes become
  `%2F` (`all+comments+on+https:%2F%2Falice.polis.pub%2Fposts%2F…`),
  nothing else. ⛔ The slashes must be encoded: HTTP routers clean a
  path holding `//` to one slash and redirect, which breaks the
  sentence.
- Handles (`alice.polis.pub`) keep their dots — domain separators
  are inside the token, not between tokens.
- `decodeURIComponent` + `+` → ` ` reproduces the displayed sentence.

Where `/_/` lands, and what happens to an address from the previous app, is the owner app's, stated in
[Addresses](../../webapp/developer/development.md#addresses).

## Worked examples

⚠️ The tables below are the previous (v4) owner app's icon presets and addresses, kept as worked examples of the
encoding; the v5 app puts the same sentences under a section (`/_/write/pql/all+comments+from+all+polis+to+bless`).

Every icon-preset in the topbar nav mapped to a canonical PQL URL:

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

⚠️ This widget was the previous (v4) owner app's, removed with that app in November 2026; this section is
kept as the record of how it composed sentences. The v5 app has no slot widget: gather offers its filters and
a search, each a sentence on `/v1/query`, and any other sentence can be given as an address
(`/_/<section>/pql/<sentence>`), to `/v1/query`, or to the `query_pql` tool.

The topbar's centered widget is a UI surface for composing PQL sentences without typing. It is the inverse of the URL parser: the user manipulates slots, the widget produces a sentence, the sentence becomes a URL.

```
   ┌──────────────────────────────────────────────────────────────────────┐
   │  [all]  [activity]  from  [my network]  [by date]                    │
   │  ^      ^                  ^             ^                            │
   │  │      │                  │             │                            │
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
4. Notify the stream controller (`window.PolisStream.setFilterType` / `setFilterScope` / `setFilterModifier` / `setFilterQualifier`), which re-runs the fetch + render cycle.

**Type-conditional slot visibility.** When `type=messages` is selected on a thread view, the modifier slot hides (no meaningful sort choice). When `type=drafts` is selected, the scope slot locks to `me`. When `type=activity` is selected, the modifier slot disappears. The widget enforces what the [type-conditional rules](#type-conditional-rules) document — every UI affordance maps directly to a vocabulary rule.

**Site-typeahead.** For `type=messages`, the scope dropdown offers a `site` entry that opens a handle typeahead: pick a polis handle (`alice.polis.pub`) and the scope becomes that handle, producing `… from alice.polis.pub …`. For every other type the entry is hidden, because a handle typed there is not yet wired to a server-side filter; a handle scope still works when typed into the URL.

**Icon-row presets vs widget composition.** Clicking an icon button (gateway, paragraph, comment, people, envelope) loads a *preset* PQL sentence — fast common views. Composing in the widget is the *general case* — anything the grammar allows. Both produce URLs that look the same; nothing about a preset path is privileged.

## Tenant-relative PQL (the `from` clause is optional on a tenant)

On a per-tenant surface (`<handle>.polis.pub/pql/…`) the relation+scope
clause is **optional**. When omitted, it defaults to `from
@<this-tenant>` — because "this tenant" is the obvious subject of a
query directed at the tenant's own domain:

| On `alice.polis.pub` | Resolves to |
|---|---|
| `/pql/all+posts+by+date` | `all posts from alice.polis.pub by date` |
| `/pql/all+comments+by+date` | `all comments from alice.polis.pub by date` |
| `/pql/all+profiles+by+name` | `all profiles from alice.polis.pub by name` |

The composer is symmetric: when the scope equals the tenant default
(and the relation is `from`), the clause is omitted from the URL — so
the bar reads `…/pql/all+posts+by+date`, not the verbose form. The
discovery service has no single tenant, so a missing clause there
defaults to `from all polis` (the cross-tenant index).

The owner SPA always uses explicit scopes (`from me`, `from my
network`) under its `/_/pql/…` shell route.

## Scope-resolution boundary (who resolves what)

PQL is one grammar, but **where a scope can be resolved** differs by
surface. The test is the first-person tokens **`me` / `my`**:

| Scope | Resolved by | On the DS? |
|---|---|---|
| `all polis`, `<handle>`, `about <handle>`, `<handle>'s network` | public — anyone | ✅ yes |
| `me`, `my network`, `my mutuals` (+ `about me`) | the **owner**, via the webapp/auth API | ❌ rejected (`PQL_OWNER_RELATIVE_UNSUPPORTED`) |
| `messages` (DMs), `drafts` (types) | the owner only | ❌ owner-local |

This is a **dispatch policy, not a grammar fork** — all three parsers
(JS/Go/TS) parse every scope identically; each *surface* then narrows
what it will resolve:

- **Discovery service** (`ds…/pql/…`) resolves only public scopes; it
  rejects first-person scopes with a 400 + a `warn` log. It still
  authenticates callers — but only for *visibility* (denial filtering),
  never to interpret a first-person scope.
- **Tenant webapp** (`<handle>.polis.pub/pql/…`) resolves first-person
  scopes only for the authenticated owner (same-origin session); it
  rejects them for anonymous visitors. It also rejects the `about`
  relation and FQ event types (DS-only).

Why the line: a first-person scope's *meaning* depends on whose follow
graph it's read against. The owner's site is the authority on "my
network"; the DS is a cross-tenant index and deliberately does not
interpret a caller's personal relative context. The line is an explicit
initial boundary, revisitable later.

## JSON API + the versioned envelope

`GET /pql/<sentence>` content-negotiates on `Accept`:

- `Accept: application/json` (or `?format=json`) → the JSON envelope.
- otherwise → the HTML infinity-stream page.

The envelope is **versioned** so the shape can evolve without breaking
integrators:

```json
{
  "version": "pql.v1",
  "query": "all posts from alice.polis.pub by date",
  "url": "/pql/all+posts+by+date",
  "parsed": { "qualifier": "all", "type": "posts", "relation": "from",
              "scope": "@alice.polis.pub", "modifier": "by-date" },
  "tenant": "alice.polis.pub",
  "items": [ /* … */ ],
  "pagination": { "next_cursor": "…", "has_more": true }
}
```

`query` echoes the *resolved* sentence (explicit scope); `url` is the
short shareable form. On the tenant webapp `items` are rendered content
items; on the DS `items` are raw stream events (and there is no
`tenant` field). Full developer reference:
[`../ds/developer/pql-json-api.md`](../../ds/developer/pql-json-api.md).

## Reserved tokens

These exist in the grammar but are currently not surfaced in the UI:

- **`new` qualifier** — reserved. The dropdown only ships `all`:
  read-state tracking proved unreliable enough that filtering on it
  produced confusing results, so the `new` option was withdrawn until
  that is revisited.

## Removed tokens

These were once valid PQL but are no longer:

- **`follows` type** — removed by the 06-profiles work. "Follows" was
  a verb pretending to be a noun. The replacement: follow events
  surface inside `type=activity` as terse single-line entries
  ("alice.polis.pub followed bob.polis.pub"); profile management
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

**Canonical machine-readable vocabulary.** The lookup tables above
(qualifiers, types, relations, scope atoms, modifiers, handle pattern)
are mirrored in [`pql-vocabulary.json`](pql-vocabulary.json), which every
parser embeds/loads — the JS parser (`pql.js`), the Go parser
(`cli-go/pkg/pql`), and the DS TypeScript parser
(`discovery-service/core/pql.ts`). All three assert against the shared
golden corpus [`pql-golden.jsonl`](pql-golden.jsonl) so the grammar can
never drift between languages. When you add a vocabulary term, edit the
JSON + add a golden line; the parsers pick it up and the cross-language
tests enforce agreement.

## Future direction

PQL is intentionally aligned with [`policy-grammar.md`](policy-grammar.md) (the inbound-rule grammar — same `qualifier type scope` shape). A user fluent in one is fluent in both. That alignment isn't decoration; it's a bet that *sentence-as-filter* and *sentence-as-rule* converge as polis matures.

### Already shipped

Three extensions this page once listed as future work are live:

- **Cross-network queries.** The discovery service serves `GET /pql/<sentence>` with the same vocabulary, returning raw stream events as JSON (public scopes only — see [Scope-resolution boundary](#scope-resolution-boundary-who-resolves-what)). A reader without a polis site of their own can query it.
- **PQL as a programmable surface.** A site answers a sentence as JSON at `/v1/query` (the owner's token for
  sentences only the owner may read), and an agent asks it through the `query_pql` tool: "all posts from my
  network by date" is something a script, a digest or an assistant can run.
- **Public-surface PQL.** Every tenant serves `/pql/<sentence>` to anonymous visitors, with the relation+scope clause defaulting to the tenant (see [Tenant-relative PQL](#tenant-relative-pql-the-from-clause-is-optional-on-a-tenant)).

### Near-term extensions

- **CLI integration.** A command that runs a PQL sentence against the local feed cache and prints results. The Go parser the CLI would use (`cli-go/pkg/pql`) already exists; no command does yet.
- **New vocabulary.** Adding `by popularity`, `to review`, `with images`, `with attachments`, `from my topic <tag>`, etc. is intentionally cheap — modifiers are keyword-led, so each new term is one entry in [`pql-vocabulary.json`](pql-vocabulary.json), a golden line, and one row in this doc.
- **Read-state revisited.** The `new` qualifier is currently locked off; future work re-enabling unread-tracking would surface it again. Same for "trending" / "popular" qualifiers that depend on cross-tenant signals only the DS can compute.

### Architectural ambitions

- **Policy ↔ PQL convergence.** A user-author who writes `deny pub.polis.comment from all polis at spam.example.com` in their policy file is already speaking a PQL-shaped sentence. A future tool could let an author *select content via PQL* and then *write a policy rule from the same selection*. The shared grammar makes this a one-step UI.
- **Saved sentences.** A user marking a sentence as a favorite — "all comments from my network to bless" — gets it as one of their own views in gather. The plumbing is already there; the UX is the gap.
- **Composed sentences.** Future grammar growth might allow `and` / `or` between scopes ("from my network or from alice.polis.pub"). The parser is positional today; intersecting/uniting scopes requires a small grammar extension.

This doc is the authoritative spec — the Go and TypeScript parsers, and any future one, agree on the vocabulary documented here. The grammar is the contract.

## Pointer

Parser implementations (all assert against the shared
[`pql-golden.jsonl`](pql-golden.jsonl) + [`pql-vocabulary.json`](pql-vocabulary.json)):

- **Go** — `cli-go/pkg/pql/` (a site's `/pql/` and `/v1/query`; `TestGoldenCorpus`, `TestVocabularyMatchesCanonical`)
- **TS** — `discovery-service/core/pql.ts` (DS `/pql/` endpoint; `core/pql.test.ts`)

The previous owner app's JS parser (`pql.js`) was removed with that app. The stream shape's page script
keeps only the mapping from its filters to sentence tokens, against `pql-vocabulary.json`; the server
parses every sentence.

Endpoints: tenant `GET /pql/<sentence>`
(`webapp/internal/server/handlers_pql.go`); discovery service
`GET /pql/:sentence` (`discovery-service/server/src/index.ts` →
`pqlToStreamQueryFilters` in `core/handlers/stream.ts`).

This doc is the one place the grammar is written in prose. It was
once duplicated in internal notes, and the copy drifted from the
implementation in five distinct ways; the machine-readable vocabulary
and the shared golden corpus exist so that cannot recur.
