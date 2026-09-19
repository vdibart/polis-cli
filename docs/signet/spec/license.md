# Signed consent & licence

*For* [Developers](../../README.md#building-on-polis) · [Writers](../../README.md#writing-on-polis) — *About* [Content](../../general/README.md#content) — *Kind* [Spec](../../README.md#kinds-of-page) — *Code* [`cli-go/pkg/license`](../../../cli-go/pkg/license) — *See also* [concept](../concepts/assertions.md) · [guide](../guides/set-your-terms.md)

**Schema:** `pub.polis.license.v1`

A polis licence is a statement of the terms under which an author's work may be used, **signed by the
author's key** and **carried inside the work itself**. It is built entirely from other people's
vocabularies. The only thing polis adds is the binding.

This document is the implementation spec. If you want the short version: [Set your
terms](../guides/set-your-terms.md).

---

## 1. What problem the signature solves

`robots.txt` is unsigned and site-scoped. Whoever controls the server controls the terms — so change
hosts, get mirrored, get quoted, get scraped into a dataset, and the terms don't travel. Nobody
downstream can tell whether the site owner or the author set them, or what they said last March.

A signed per-work licence is bound to the author's key, so it:

- **travels with the bytes** and survives the work leaving the author's site;
- is **unambiguous and dated** — nobody can claim they didn't know what the terms were in March;
- is **attributable to the author**, not to a host or a platform;
- is **evidence** — for a negotiation, a claim, or a third-party auditor.

**It is not enforcement.** A signature stops nobody. If no crawler honours it, it is a signed note in a
drawer. The value is that the terms are legible, provable, and the author's own — which is more than
anyone has today. Nothing in this spec should be described as *enforceable*.

**The binding is stronger than self-assertion.** Fetching `https://example.com/.well-known/polis` means
TLS and the web PKI have already attested domain control, so the key↔domain binding is inherited from
the CA system rather than claimed. Polis licence terms are not purely self-asserted — which is a claim
none of the site-scoped standards can make.

## 2. Which layer is doing the work

Three different things get called "a licence", and conflating them is caught immediately by anyone in
the standards or legal world. **This spec marks every field with the layer it belongs to.**

| Layer | Fields | Nature | Force |
|---|---|---|---|
| **IETF AIPREF** | `train-ai`, `search` | **preference** | a stated wish; honoured or not. Explicitly *not* a licence. |
| **RSL** | `ai-input`, `attribution` | **licence terms** | a grant, on conditions |
| A referenced standard instrument (e.g. Creative Commons) | via `terms` | **licence** | that instrument's legal code |

A licence file carrying mostly AIPREF values is **stating preferences, not granting rights.** Say so.

### We name selections; we never author terms

Polis does not write licence text. A **profile** is a *name for a selection* from other people's
vocabularies, the way `CC BY-NC-SA` labels a bundle whose legal text belongs to Creative Commons.

> **The checkable rule:** every value in a profile must come from a standard's vocabulary, with that
> standard's meaning. A profile that needs a value no standard defines means somebody has started
> drafting a licence — which is where to stop.

Creative Commons splits a licence three ways: **legal code · human-readable deed · machine-readable
metadata**. Polis writes the last two and points at other people's legal code.

### Nobody states terms for the author

A licence is a **signed statement of intent**, so the only thing that may produce one is the author
saying so. Not an actor, not a host, not a setup path, and not a default behind a prompt somebody
pressed past.

| Situation | What is stated |
|---|---|
| An existing site | nothing, until its author chooses |
| A new site created by a **hosted signup** | nothing, and the signup does not ask — licence vocabulary mid-registration is a usability cost with no matching gain, and the setting is reachable afterwards |
| An **interactive** setup command | asks, with **nothing pre-selected**; an empty or unrecognised answer states nothing, and the tool says how to state terms later |
| A **non-interactive** run with no explicit choice | nothing |

⭐ **The two failure directions do not cost the same, which is what settles every case above.** Terms
are not retroactive (§5.1), so a grant nobody meant to make **can never be walked back** for anything
already published, while a silence nobody meant to keep costs one command. **An implementation in
doubt states nothing.**

⚠️ **Silence is not a gap to be filled.** `unknown` is a defined outcome in AIPREF, so a site that has
said nothing is already in a valid state — there is no incomplete configuration here for a
well-meaning default to complete.

## 3. Vocabulary

### 3.1 AIPREF — the preference layer

From `draft-ietf-aipref-vocab-06`. The vocabulary has **exactly two categories**, and Table 1 of that
draft is the whole of it:

| Key | Category | Definition (§) |
|---|---|---|
| `train-ai` | AI Model Training | *"the act of using an asset in the production or refinement of an AI model that can generate content in one or more modalities"* (§4.1) |
| `search` | Search | *"use of an asset in an application where the primary purpose of the application is to select assets and direct users to the location of those assets"* (§4.2) |

Values are `y` (allow) and `n` (disallow). **An absent key means `unknown`** — the author has not said.
Not permitted, not denied (§5).

**Two clauses live inside the `search` definition** rather than being conditions attached to it, and
both matter:

> *"…includes a direct reference or link to the original location from which the asset was retrieved."*
> *"This category does not include the use of assets to generate summaries."*

So link-back is not a condition on `search`; it is part of what `search` *is*. An answer engine that
summarises without linking is not doing `search`.

⚠️ **`search=y` grants more than its name suggests, and any honest description has to say so.**
§4.2 closes by permitting processing internal to the search application, and *"that includes the
training of AI models using the assets"*, provided the resulting models and outputs are used
exclusively within a search meeting the conditions above. **This is not a loophole — it is what makes
`search` implementable.** An index is a model; refusing search-internal training is refusing search.

> ⚠️ **There is no `ai` key.** Some summaries of AIPREF show one — including
> `draft-ietf-aipref-attach-04` §3.4, which narrates its own example as `"ai=y"` where the example
> reads `train-ai=y`. That string appears nowhere in the vocabulary; it is editorial residue from the
> working group's rename of *Foundation Model Production* to *AI Model Training*. **An umbrella
> category can never be added either:** §4.3 permits extensions to define *subsets* of an existing
> category and forbids supersets. Emitting `ai=n` is a no-op — conforming consumers MUST ignore
> unknown labels (§6.4).

### 3.2 RSL — the licence-terms layer

From RSL 1.0 (`https://rslstandard.org/rsl`). Polis emits two of its terms:

| Key | RSL form | Why it is here and not in AIPREF |
|---|---|---|
| `ai-input` | `<permits\|prohibits type="usage">ai-input</...>` | The work fetched and **summarised into an answer at inference time**. AIPREF structurally cannot express it: `search` excludes summarisation by definition and `train-ai` is about model *production*, so this use falls under neither category and would resolve to `unknown`. |
| `attribution` | `<payment type="attribution">` | A free licence with *mandatory attribution* is a grant on a condition, and AIPREF's vocabulary is `y`/`n` with no conditions at all. |

`ai-input` is the axis that most affects publishers today — readers ask an answer engine, get the
thinking back in a paragraph, and never arrive. A licence that says "no training" and is silent on
retrieval misses the live wound.

### 3.3 Composition

**The preference rides AIPREF; the condition rides RSL.** Two rules keep them from contradicting each
other:

- **Never project a condition into a bare permission** — that over-grants.
- **Never project it into a denial** — that costs reach, which is usually the thing the author most
  wants to keep.

Where a vocabulary cannot express a term, it stays unexpressed *in that surface* and the licence link
carries it. `ai-input` therefore appears in `rsl.xml` and in the signed frontmatter, and **never** in
`Content-Usage` or the robots.txt rule.

## 4. Profiles

A profile is a **semantic tag**, following polis's existing namespacing (`pub.polis.post`,
`pub.polis.shapes.v4`). **It is never a URL.**

| Profile | `train-ai` | `search` | `ai-input` | `attribution` |
|---|---|---|---|---|
| `pub.polis.license.reserved/1` | `n` | `y` | `n` | `required` |
| `pub.polis.license.open/1` | `y` | `y` | `y` | — |

**A profile version never changes.** A work signed under `reserved/1` in 2026 means the same thing in
2036. A revised selection gets a new version number.

### Why the tag is not a URL

Putting a `polis.pub` URL into everyone's signed frontmatter would make polis.pub a **permanent,
unrevocable dependency of every author's terms**, baked into bytes they cannot edit without
republishing — and would make polis the authority on what other people's terms mean, forever.

**This is deliberately unlike Creative Commons.** CC's deed URL is load-bearing: you must fetch
`creativecommons.org/licenses/by/4.0/` to learn what BY means. Ours is not. A materialisation carries
**the profile *and* the expanded values**, so if every explanation page vanished tomorrow,
`train-ai: n · search: y · ai-input: n · attribution: required` still says everything operative. The
only URL in the payload points at the author's own domain.

## 5. Where terms live

### 5.1 Two artifacts, two questions

| Question | Answered by | Properties |
|---|---|---|
| *"What are her terms **now**?"* | `license.json` | current, editable, published, **signed** |
| *"What may I do with **this** work?"* | the work's `license:` frontmatter | one file, one signature, **travels with the bytes** |

A consumer never reconciles them, because they are never asked the same question. When the two diverge
— terms loosened in 2024, tightened in 2026 — that is not a bug, it is the honest state of the world.

> ⚠️ **Terms are not retroactive.** A licence is signed at time T. Republishing with new terms creates
> a new version; copies already made carry the old terms, provably. **A newer `license.json` never
> overrides an older frontmatter grant.**

### 5.2 Discovery is by pointer, never by convention

`.well-known/polis` carries a **`license`** field holding the URL of the licence document:

```json
{
  "version": "polis-cli-go/0.67.0",
  "public_key": "ssh-ed25519 AAAA…",
  "license": "/content/pub.polis.core/license/license.json"
}
```

**Consumers follow the pointer. They never assume a path.** A polis site's layout is user-configurable
— `dir` and `mount` are per-type declarations in `bundle.json` — so a hardcoded `/license` would be
wrong for any site that moved it.

The pointer is **unsigned**, like everything in `.well-known/polis`: TLS and the web PKI bind it, and
what is *signed* is what it points at. It is a **pointer only** — inlining the terms would make
`.well-known/polis` a second copy that can diverge, and a pointer can only go stale in location, never
in content. **Absent when no terms are stated.**

### 5.3 File layout

| Path | What | Authored or generated |
|---|---|---|
| `.well-known/polis` → `license` | the pointer | generated when terms are stated |
| `content/pub.polis.core/license/license.json` *(default; movable)* | **source of truth**, signed | **authored** |
| a work's frontmatter `license:` block | per-work materialisation | generated at publish |
| `/license` *(the type's mount)* | site terms page + profile explanations | generated |
| `robots.txt`, `rsl.xml` | standards projections | generated |

The licence is declared as a content type, `pub.polis.license` (`dir: license`, `mount: /license`).

**Every generated row tracks the source in BOTH directions.** Stating terms writes them; withdrawing
terms **removes** them. A projection that outlives its source is a published claim the author no
longer makes, and nothing else in the system would ever take it down.

⛔ **Withdrawal removes, it does not blank.** An empty `robots.txt` is a statement of its own; an
absent one is the pre-terms state, and that is what withdrawal returns a site to. Afterwards a
withdrawn site is indistinguishable from one that never stated terms — which is the correct
observable, since *absent means unstated*.

**Ordering.** Stating writes the document, then the pointer that advertises it. Withdrawing is the
mirror, outermost first: the generated surfaces, then the pointer, then the document. A crash midway
through a withdrawal therefore leaves a site that still states terms and is missing derived files —
which the next render regenerates — rather than published surfaces asserting withdrawn terms, which
nothing repairs.

⚠️ **The two generated surfaces that are healed are not the whole set.** An implementation that
regenerates `robots.txt` and `rsl.xml` on a schedule and leaves the terms page to the renderer alone
publishes two reliable files advertising one that may never have been written. If a surface is
advertised by another surface, whatever guarantees the advertiser must guarantee the target.

**It is deliberately not beside `policies/`.** Polis uses *policy* for the **inbound** grammar — who may
comment on me, what I will accept. A licence is **outbound**: what third parties may do with work I
made. Opposite direction, different audience, different verbs. See
[policy grammar](../../general/reference/policy-grammar.md).

## 6. The signed licence document

```json
{
  "type": "pub.polis.license",
  "terms": {
    "v": "pub.polis.license.v1",
    "profile": "pub.polis.license.reserved/1",
    "train-ai": "n",
    "search": "y",
    "ai-input": "n",
    "attribution": "required",
    "terms": "https://maya.example/license",
    "contact": "https://maya.example/license",
    "asserted": "2026-08-27T14:02:00Z"
  },
  "created": "2026-08-27T14:02:00Z",
  "updated": "2026-08-27T14:02:00Z",
  "generator": "polis-cli-go/0.67.0",
  "current_version": "sha256:…",
  "signature": "-----BEGIN SSH SIGNATURE-----…"
}
```

**Signing base:** the JSON serialisation of `{type, terms, created, updated, generator}` — the document
minus `signature` and minus `current_version`, which is the SHA-256 of those same bytes. Verify with the
`public_key` from `.well-known/polis`. This matches the `pub.polis.tag` convention.

> ⛔ **The exact bytes, the field order inside `terms`, and the escaping rules are in
> [`signing-base.md`](signing-base.md) §5.3**, with a worked example checked against the
> implementation mechanically. Field order is a **fixed sequence and is not alphabetical**; sort the
> keys and your signatures will not verify against ours.

**`contact` is what makes this more than a NO sign.** For anything to be asked for, there has to be
somewhere to ask.

## 7. Materialisation into a work

At publish, terms are resolved (**work > site**) and stamped into the work's frontmatter — which is
**already inside the post signature**, so no separate signing step exists.

*What the author writes* — usually nothing at all, inheriting the site default. When overriding:

```yaml
---
title: "The traffic contract is dead"
license: reserved
---
```

*What publish stamps:*

```yaml
---
title: "The traffic contract is dead"
published: 2026-08-27T14:02:00Z
current-version: sha256:…
license:
  v: pub.polis.license.v1
  profile: pub.polis.license.reserved/1
  train-ai: n
  search: y
  ai-input: n
  attribution: required
  terms: https://maya.example/license
  contact: https://maya.example/license
  asserted: 2026-08-27T14:02:00Z
signature: …
---
```

Three properties worth defending:

- **Profile *and* expanded values.** Profile alone would force a reader to resolve something; values
  alone would lose which preset was chosen. Both means the materialisation is readable without
  fetching anything.
- **AIPREF's exact key names**, so the vocabulary is the standard's rather than ours.
- **`asserted`** — the date is what makes *"what were the terms in March?"* answerable, which
  non-retroactivity requires.

`license: none` on a work is a genuine override: it publishes no terms even when the site states some.

**Comments carry the commenter's terms**, because a comment is the commenter's content on the
commenter's domain. That falls out of the architecture rather than being a special case.

### 7.1 Key-set constraint

**No key inside the `license:` block may be named `signature`, `current-version`, or `author`.**

⚠️ **The reason changed, and the constraint did not.** Until the frontmatter parsers matched keys by structural position, this was a live hazard:
polis's frontmatter parsers matched keys **by name after trimming**, without regard to nesting, so a
nested child with one of those names would have been read as a top-level field — and the markdown
signing base scanned the whole document for the `signature:` prefix rather than the frontmatter
block. Both are fixed: keys are now matched by **structural position**, an indented line is a child,
and the strip is anchored to the frontmatter block
([`signing-base.md`](signing-base.md) §4.2).

So the constraint is now **convention rather than a load-bearing safety property.** Keep it anyway —
a child that shadows a top-level field name is confusing to every reader, human and machine, and
nothing is gained by using one.

## 8. Wire surfaces

Every surface is **generated from the signed licence**, never authored in parallel. You cannot
contradict yourself if there is one source.

### 8.1 HTTP response headers

```http
HTTP/1.1 200 OK
Content-Type: text/html
Content-Usage: train-ai=n, search=y
Link: <https://maya.example/license>; rel="license"
```

`Content-Usage` is an RFC 8941 structured-field dictionary (`draft-ietf-aipref-attach-04` §2).

⚠️ **Per-resource, never per-site.** If a site defaults to `reserved` but a post is `open`, that post's
response carries *that post's* terms — otherwise the header contradicts the signed frontmatter of the
document it is attached to. Polis derives the header from the bytes being served rather than from the
site licence, so the two cannot disagree.

### 8.2 robots.txt

```
User-Agent: *
Allow: /
Content-Usage: train-ai=n, search=y
Content-Usage: /posts/20260827/take-this-one.html train-ai=y, search=y
License: https://maya.example/rsl.xml
```

The `content-usage` rule takes an **optional path pattern** (`attach-04` §3), which is what lets a
work that overrides the site default be expressed in a *file*. That matters: **a static self-hoster on
S3 or GitHub Pages cannot set response headers at all.**

| Works anywhere | Requires controlling the server |
|---|---|
| frontmatter (signed) · `license.json` · `robots.txt` · `rsl.xml` · HTML `<meta>`/`<link>` · JSON-LD | `Content-Usage` · `Link:` |

**Every term is expressed in at least one file-based surface. Headers are never the only home for a
term.**

`License:` is RSL's directive pointing at `rsl.xml`. Both it and the `Sitemap:` line are absolute, and
their origin is taken from the **`terms` URL inside the signed licence** rather than from server
configuration. A second implementation should do the same: it makes the whole file recomputable from
the site's own signed bytes, which is what lets a checker decide whether a served `robots.txt` still
agrees with the licence without holding a stored baseline.

⚠️ **This file has two writers** — the renderer emits it, and an operator's repair actor restores it
when it is missing or has drifted. They must generate it identically. Two writers with two ideas of
the content rewrite it at each other, and the per-path rules are what get lost in between.

### 8.3 rsl.xml

```xml
<?xml version="1.0" encoding="UTF-8"?>
<rsl xmlns="https://rslstandard.org/rsl">
  <content url="https://maya.example/">
    <license>
      <permits type="usage">search</permits>
      <prohibits type="usage">ai-train</prohibits>
      <prohibits type="usage">ai-input</prohibits>
      <payment type="attribution"></payment>
      <terms>https://maya.example/license</terms>
    </license>
  </content>
</rsl>
```

**Nothing is asserted that the terms do not state.** An absent value produces no rule — silence in,
silence out.

### 8.4 HTML and JSON

```html
<link rel="license" href="https://maya.example/license">
<meta name="content-usage" content="train-ai=n, search=y">
<meta name="polis-license-profile" content="pub.polis.license.reserved/1">
```

JSON-LD carries schema.org's `license` on the `BlogPosting`, omitted when unstated. The site's current
terms are readable over the content API at `GET /v1/content/license/current` (the `get` action; the id
segment is required by the route and ignored — see the [content API reference](../../api/developer/reference.md#pubpolislicense)),
which returns `stated: false` for a site
that has said nothing rather than an error or an empty grant. Index entries carry each work's
materialised terms.

## 9. Human-readable surfaces

Three, not one — because they answer different questions and change on different clocks.

| Surface | Answers | Changes |
|---|---|---|
| **Per-post notice**, on the work | *"What may I do with **this**?"* | **never** — frozen at publish |
| **Profile explanation**, at `/license/<name>-<version>.html` (e.g. `reserved-1.html`) | *"What does `reserved` **mean**?"* | **never**, per version |
| **Site terms page**, at `/license` | *"What are her terms **now**?"* | whenever the author edits |

⚠️ **A site terms page that does not state non-retroactivity is actively misleading.** A reader sees
current restrictive terms and assumes older posts are covered — or sees permissive current terms and
treats an older reserved post as fair game. The page must say: *these are my terms going forward; each
work carries its own.*

The profile explanation is a **deed**, not legal code, and it must describe what was actually granted —
including the `search` carve-out in §3.1. A deed saying only *"no AI training"* while the emitted signal
permits search-internal training misdescribes its own metadata. That is the trap Creative Commons had to
publish a primer about: a plain reading of CC BY permits training, and a great many people licensed
their archives in 2015 without knowing it.

## 10. Verifying a polis licence

1. `GET https://example.com/.well-known/polis` → read `license`. **Absent means the author has stated
   no terms** — not permitted, not denied.
2. Follow the pointer, fetch the document.
3. Reconstruct the signing base (§6) and verify against `public_key` from step 1. **The web PKI has
   already attested that this key belongs to this domain.** If that fails and the site has rotated, resolve
   the key through its published [key history](key-history.md#the-resolution-rule), using the document's
   `updated` as the claimed signing time.
4. For a specific work, read its own `license:` block. That is authoritative for that work, whatever
   the site says today.

`absent = unstated` is a **defined state**, and it is AIPREF's own semantics (§5): *"in the absence of
a statement of preference, all usage categories are assigned a preference value of `unknown`."* Never
infer a grant from an omission.

## 11. Out of scope

- **Enforcement.** See §1.
- **A negotiation endpoint.** Today, `contact` is a URL a human opens.
- **Per-paragraph or per-asset terms.** Per-work only.
- **Compliance attestation** — who honours terms. No standard here has an observation layer, and
  polis could plausibly provide one, but it is not built.

## References

- IETF AIPREF — [`draft-ietf-aipref-vocab-06`](https://www.ietf.org/archive/id/draft-ietf-aipref-vocab-06.txt),
  [`draft-ietf-aipref-attach-04`](https://www.ietf.org/archive/id/draft-ietf-aipref-attach-04.txt)
- RSL 1.0 — [rslstandard.org/rsl](https://rslstandard.org/rsl)
- RFC 8941, Structured Field Values for HTTP
- polis — [policy grammar](../../general/reference/policy-grammar.md) (inbound policy, not this)
