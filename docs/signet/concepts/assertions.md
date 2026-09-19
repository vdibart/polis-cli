# Assertions

*What polis lets someone say, and what makes any of it worth believing*

*For* [Reviewers](../../README.md#reviewing-the-security-and-identity-design) · [Developers](../../README.md#building-on-polis) — *Kind* [Concept](../../README.md#kinds-of-page) — *See also* [spec](../spec/attestation.md) · [spec](../spec/license.md) · [guide](../guides/attest.md)

An assertion is a claim one identity makes, signed with its key. Polis has a small number of them, and
they are deliberately the same shape: **someone says something, and the saying is provable.**

| Assertion | The claim | Status |
|---|---|---|
| **Attribution** | *this is mine* | shipped |
| **Licence** | *here are my terms for it* | shipped — [spec](../spec/license.md) · [guide](../guides/set-your-terms.md) |
| **Attestation** | *I claim this about that* | shipped — [spec](../spec/attestation.md) · [guide](../guides/attest.md) |
| **Counter-signature** | *and I agree* | **shipped as a pair of attestations** for a claim both sides make (`same-as`) — see below. ⚠️ Assenting to a *different* claim someone made about you has **no predicate yet** ([the graph §3](graph.md#3-the-trust-triangle)) |

Three are written up here. Counter-signature turns out not to be a fourth thing.

---

## Attribution: *this is mine*

A polis work is markdown with frontmatter, signed with the author's Ed25519 key. The signature covers
the frontmatter and the body together, so the metadata is not a separate, forgeable layer around the
content — the title, the date, and the version hash are inside the same signature as the words.

The key belongs to a domain, and **the web PKI has already attested that binding.** Fetching
`https://maya.example/.well-known/polis` over TLS means a certificate authority vouched for control of
that domain. So a polis attribution is not purely self-asserted, which is unusual: most signed-content
schemes stop at *"signed by someone claiming to be Maya."*

## Licence: *here are my terms*

The licence is the same move applied to consent. Because frontmatter is already inside the signature,
terms cost nothing extra to sign — the interesting work is everywhere *else*: making them legible to
things that never read markdown.

### Two artifacts, because there are two questions

| Question | Answered by |
|---|---|
| *"What are her terms **now**?"* | the site's signed `license.json` |
| *"What may I do with **this** work?"* | the terms inside that work's signature |

A consumer never has to reconcile them, because they are never asked the same question. And when they
diverge — loosened in 2024, tightened in 2026 — that divergence is the honest state of the world
rather than an inconsistency.

**Terms are not retroactive.** This is the property that surprises people, and polis surfaces it rather
than burying it: a newer licence never reaches back into an older grant. What you granted then and what
you would grant now are both true, and both visible.

### Carry the standards, own the binding

Nothing in the vocabulary is polis's. `train-ai` and `search` are IETF AIPREF's keys with AIPREF's
meanings; `ai-input` and `attribution` are RSL's. **What polis adds is the binding** — the terms sit
inside a signature bound to the author's key and domain, so they travel with the work.

That is a deliberate strategic choice as well as a technical one. A licence's entire value is network
effect: GPL, MIT, CC and Apache won by being free and universal. Inventing a fourth vocabulary in 2026
would be inventing something nobody has a reason to read.

> **We name selections. We never author terms.** A profile like
> `pub.polis.license.reserved/1` is a *name for a combination* of other people's values, the way
> `CC BY-NC-SA` names a bundle whose legal text belongs to Creative Commons.

### Be exact about what kind of claim it is

Three things get called "a licence", and running them together is caught immediately by anyone who
works in this area:

- **AIPREF is a preference signal** and explicitly not a licence. It states a wish.
- **RSL is licensing** — a grant, on conditions.
- **Creative Commons ships legal code.**

Polis marks every field with the layer it belongs to. A file carrying mostly AIPREF values states
*preferences*, and calling that a licence overclaims.

### What it is not

**It is not enforcement.** A signature stops nobody, and if no crawler honours a term it is a signed
note in a drawer. Saying this first is not modesty — it is the difference between a claim that survives
scrutiny and one that does not. What the signature buys is that the terms are **legible, dated,
provable, and the author's own**, which is the precondition for a negotiation, a claim, or an audit —
and which nothing site-scoped and unsigned can offer.

## Attestation: *I claim this about that*

The first two assertions are about **your own** work. An attestation is the one that points outward:
*on this date, I asserted this predicate about this subject.* A correction about someone's article. A
vouch for a person. A receipt for a work you used.

**A claim event, and the word "event" is doing work.** It happened once, at a moment, and it is never
edited. A correction to a claim is a *new record about the old one*, not a rewrite — which is why one
attestation holds exactly one subject and carries exactly one date.

### One noun, three parts

⚠️ **`issuer`, `predicate` and `subject` are the parts of one noun, not three new concepts.** There
is no separate thing called a claim, no credential type, no subject registry. The whole vocabulary of
this layer is: *someone asserted something about something.*

- the **issuer** is a site, identified by its domain
- the **predicate** is what is being asserted — `same-as`, `correction`, `endorsement`
- the **subject** is what it is about — a work, or a party

Say the sentence out loud and it is complete: *vdibart.polis.pub asserts `same-as` about
vincent.example.com.*

### What it is not

This is the part that decides whether a reader trusts the design, so it is worth being blunt.

- **Not a rating, not a score.** There is no number anywhere in the format and no aggregate. Signed
  is portable and permanent; computed is per-viewer and disposable — so the evidence travels and the
  judgment is recomputed by whoever is asking. A 4.8 out of 5 is a platform taking the reader's
  judgment away from them.
- **Not a credential.** No authority issues one, and none can revoke one. A party signs a sentence
  with their own key.
- **Not moderation, and not negative reporting.** An attestation that a site's signed artifacts
  verify is a narrow, falsifiable, cryptographic claim. It is not a safety rating, and losing that
  distinction loses the point.
- **Not enforcement.** Same honesty as the licence: a signature stops nobody.

### Two properties that are hard to get any other way

**A claim can be pinned to exact bytes.** Every fact-check on the internet today attaches to a
*mutable* object: the publisher edits the article and the fact-check silently becomes wrong,
unfalsifiable, or both. A polis correction names a content hash, so it is permanently scoped to the
bytes the issuer actually saw. Republish the work and the old attestation still visibly points at the
old version — the correction and the edit are both in the record, and neither can quietly erase the
other.

**A verifier can be disagreed with.** Anyone may publish attestations about anyone; several may
publish contradictory ones; each accrues its own reputation, and **the consumer picks whose to
weigh.** If a verifier is wrong, you can prove it is wrong — which is the opposite of an opaque score
you cannot inspect, reproduce, or appeal.

## Counter-signature: *and I agree*

Not a fourth assertion. **A counter-signature is two attestations that reference each other**, each
signed by its own key — never one file with two signatures.

The clearest case is identity continuity. `did:web` has a known hole: your identity *is* your domain,
so losing the domain loses the identity, and the DID spec's `alsoKnownAs` is self-asserted only —
whoever hijacks the old domain can claim it just as loudly.

Two records close it. The old site asserts *same-as* about the new; the new site asserts *same-as*
back. Each verifies against its own site's published key, so **half a pair proves nothing** — and
that asymmetry is exactly what a self-assertion cannot offer. The result is a portable identity
migration with no registry, no ledger, and no authority to ask.

The same shape does more than identity: a withdrawal is a record about the record being withdrawn, a
dispute is a record about a claim, and a delegation is a grant the principal signs, which every act
done under it names ([delegation](../spec/delegation.md)). **No new primitives** — a record has a permanent URL, so a
record can be a subject.

## Absent means unstated

The rule underneath all of it: **an assertion nobody made is not a denial and not a grant.** A site with
no licence has said nothing. A work with no terms has said nothing.

This is AIPREF's own semantics — *"in the absence of a statement of preference, all usage categories
are assigned a preference value of `unknown`"* — and polis arrived at it independently before adopting
it, which is a good sign about both.

It has a practical consequence worth stating plainly: **polis will never state terms on a user's
behalf**, and neither will a host running polis for them. Not a default, not a backfill, not a
"sensible" placeholder. An unsigned default would contradict the signed layer's silence, and a
provisioned `license.json` would assert an intent its author never had.

## What a consumer decides

Whether something is signed, and whether the signature verifies, are **facts**. Whether that makes it
acceptable is a **judgment**, and it belongs to whoever is asking — never to polis, never to a
verifier, never to a version gate.

So polis reports: *this verified*, *this did not*, *this states no terms*. What to do about any of
those is not its call.

**The same rule governs what a reader does with a vocabulary it does not know.** An attestation whose
predicate you have never seen must still verify, still parse, and still render legibly — it is simply
ignored by anything computing over records. A reader that rejected unknown predicates would mean no
predicate could ever be added without breaking every deployed reader, and nobody outside polis could
build on the format. Rejecting the unrecognised is not caution; it is a closed system with extra
steps.
