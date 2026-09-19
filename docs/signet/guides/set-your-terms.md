# Set your terms

*For* [Writers](../../README.md#writing-on-polis) — *Kind* [Guide](../../README.md#kinds-of-page) — *See also* [concept](../concepts/assertions.md) · [concept](../../general/concepts/policy-and-licence.md) · [spec](../spec/license.md)

Your posts can carry a statement of how other people may use them — signed by your key, and travelling
inside each post rather than sitting in a file on your server that anyone with access could change.

This is the practical guide. The format is specified in [spec/license.md](../spec/license.md).

---

## What you get

When you state terms, every post you publish from then on carries them **inside its signature**. Quote
the post, mirror it, scrape it into a dataset — the terms come along, dated and attributable to you.

That is the whole idea, and it is worth being clear about its limit: **this is evidence, not a fence.**
A signature stops nobody. What it does is make your terms legible, provable, and unambiguously yours,
which is more than the web offers today.

## Choosing at setup

`polis init` asks. **Nothing is pre-selected** — pressing enter states no terms, and so does any
answer that isn't one of the three:

```
Your posts can carry terms describing how others may use them.

  1. Reserved  (recommended)
     Read and quote freely with a link back. Search engines may index
     your work and send people to it. AI training and answer-engine
     summaries require asking.
     → train-ai=n  search=y  ai-input=n  attribution=required

  2. Open
     Anyone may use your work for anything, including AI training.
     → train-ai=y  search=y  ai-input=y

  3. Unstated
     Publish no terms. Readers fall back to their own assumptions.

You can change this at any time with 'polis license'. Note that terms are
not retroactive — posts already published keep the terms they were signed
with.

Press enter to state nothing for now.

Choice:
```

Choose *Unstated* (or just press enter) and `polis init` tells you how to state terms later. **Nothing
here is final** — a declined choice is not a closed door.

For scripts and CI:

```bash
polis init --license reserved     # or: open, none
```

**A non-interactive run with no `--license` states nothing.** No default is applied for you.

## If your site is hosted on polis.pub

**Signup asks nothing and states nothing.** You get a site that publishes no terms, exactly like a
site created before terms existed — and the reason is not that terms don't matter, it's that a
registration form is the wrong place to make someone adjudicate AI-preference vocabulary.

Set them whenever you like, in **Settings → Terms of use**. Everything on this page applies; the
buttons there write the same signed licence `polis license` does.

## Changing your terms later

```bash
polis license              # what do I currently state?
polis license reserved     # state (or restate) terms
polis license open
polis license none         # withdraw — go back to stating nothing
polis render               # regenerate robots.txt, rsl.xml, and your terms page
```

## What "reserved" actually says

It is worth reading this once rather than trusting the label.

> **No AI training on this work, except the models a search engine needs to index it and link readers
> back. Nobody may summarise it into an answer that replaces the visit. Credit is required wherever it
> is used.**

The exception in the first sentence is real, and it is not a loophole. Allowing search means allowing
a search engine to build an index — and an index *is* a model. The standard says so explicitly. What
you are denying is training a **general** model, and summarising your work into an answer that means
nobody visits.

If that trade is wrong for you, `open` grants everything and `none` states nothing at all.

## The one thing that surprises people

**Terms are not retroactive.**

Stating terms today applies to posts you publish from today. It does not reach back into your archive:
those posts were signed with whatever was in force when they went out, and their signatures still
prove *that*. Changing your mind cannot rewrite what you already said — which is exactly the property
that makes the signature worth anything.

The same is true in the other direction. If you loosen your terms, an old post stays reserved.

**This is why the decision is worth a minute at setup.** An over-granting default could never be walked
back for anything already published; an over-restrictive one costs a conversation and is fixed by
running one command.

## Per-post terms

Override the site default on a single post by putting it in that post's frontmatter before publishing:

```yaml
---
license: open
---

# Take this one

I don't mind what you do with this one.
```

Or from the command line:

```bash
polis post my-post.md --license open
```

`license: none` publishes that one post with no terms even when your site states some.

## Where your terms show up

Once stated, everything below is generated from your signed licence. None of it is hand-maintained,
and editing any of it by hand does nothing — the next render overwrites it from the signed source.

| Where | What a reader or crawler sees |
|---|---|
| Each post's markdown | the terms, inside the signature |
| Each post's HTML | `<link rel="license">`, an AIPREF `<meta>`, and a JSON-LD `license` |
| Each post's page | a short line under the body saying what the terms are |
| `/license` | your terms page, in plain language |
| `/robots.txt` | the AIPREF `content-usage` rule + a `License:` pointer to your RSL file |
| `/rsl.xml` | the terms in RSL, the format publishers and licensing tools read |
| Response headers | `Content-Usage` and `Link: rel="license"` — hosted or self-served |
| `.well-known/polis` | a `license` pointer, so anyone can find the signed source |

**If you host static files** (S3, GitHub Pages, Netlify), you cannot set response headers — and nothing
is lost. Every term is also in `robots.txt`, `rsl.xml`, and each post's own frontmatter. The headers
are a convenience, never the only place a term lives.

## Checking it worked

```bash
polis license                      # what you state now
cat robots.txt rsl.xml             # the machine-readable projections
grep -A9 '^license:' content/pub.polis.core/post/*/*.md | head -12
```

And after deploying, from anywhere:

```bash
curl -s https://yourdomain.com/.well-known/polis | jq .license
curl -sI https://yourdomain.com/posts/…/your-post.html | grep -i 'content-usage\|link'
```

## If you state nothing

That is a real option, not a gap. Under the AIPREF standard, an absent statement resolves to
**`unknown`** — the author has not said. Not permitted, not denied.

Nothing about your site changes and nothing is published in your name. polis will not invent terms
for you, and neither will your host.
