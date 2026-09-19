# Projection

*Why polis is designed to become other formats, instead of claiming standards*

*For* [Reviewers](../../README.md#reviewing-the-security-and-identity-design) — *Kind* [Concept](../../README.md#kinds-of-page) — *See also* [spec](../spec/did-web.md) · [recipe](../recipes/08-survive-a-key-rotation.md)

polis does not try to be every protocol. It tries to make its primitives the right **shape** — so
that a key, a signed record, and a witness can be projected into whatever shape a protocol demands,
by whoever wants that protocol, without the primitive having to change.

---

## 1 The rule

> **Design the primitives so they can be projected into whatever shape a protocol demands.**

Supporting every protocol is a miserable way to live: each one brings its own data model, its own
lifecycle and its own politics, and a system that adopts them all becomes the sum of their
compromises. polis takes a narrower bet. It keeps a small set of signed primitives — identity,
content, the claims people make about each other — and asks one question of each: *could this be
turned into that protocol's shape, faithfully, by a projection that knows about the primitive?*

⛔ **The direction matters.** A projection may know everything about the primitive. **The primitive
must never know about the projection.** The moment a protocol's assumptions leak into the thing being
projected, every other protocol inherits them.

## 2 The operative principle

> **A non-gating primitive projects into a gating protocol. A gating primitive projects into nothing.**

Optional projects into mandatory. Descriptive projects into prescriptive. Evidence projects into
proof. **Never the reverse.**

A protocol that *requires* something can consume a primitive that merely *offers* it. A protocol
that *forbids* it can ignore the offer. But a primitive that *requires* something can only project
into protocols that require the same thing — and is unreachable from everything else.

So the moment worth scrutinising in any design is the moment something becomes **required**: that is
when it quietly makes a set of other shapes unreachable.

The clearest case in polis is the discovery-service **witness**. A discovery service countersigns
what it saw registered, and the site carries that countersignature — **as evidence, never as a
requirement.** An artifact without one verifies exactly as before. That single choice is what lets
the same mechanism feed a protocol that demands witnesses and stay invisible to one that has none.
See [witness](../spec/witness.md).

## 3 The worked example: `did:web` is a pure function

A polis site's identity is a domain and a key, published in `.well-known/polis`, with the history of
every key it has held ([key history](../spec/key-history.md)).

Its `did:web` document is **computed from those, and nothing else**:

- the current key becomes the verification method that can authenticate and assert;
- each retired key stays resolvable, and drops out of authentication and assertion;
- a site that has never rotated produces a byte-identical document to the one it always had.

**Nothing about `did:web` leaked into the primitive.** The key history has no DID fields, no DID
vocabulary, and no dependency on the DID document existing. Delete `did.json` and it is rebuilt from
the source; change the DID profile and the source does not move. That is what projection looks like
when it works: the standard is carried, and the binding stays polis's own.

## 4 The trust triangle

Most trust structures people build are combinations of three signatures over one claim:

| Leg | Says | In polis |
|---|---|---|
| **Assertion** — the issuer | *"I assert X about B."* | an [attestation](assertions.md) — shipped |
| **Assent** — the subject | *"I saw this, and I do not disown it."* | **not yet — the missing leg.** The format can carry it, because a record can be the subject of another record, but no predicate is reserved and what assent should *mean* is unsettled ([the graph §3](graph.md#3-the-trust-triangle)) |
| **Witness** — a discovery service | *"I saw this, at time T."* | a DS [witness](../spec/witness.md) — shipped |

The table below is about **structure**: which legs a shape needs. Where it names assent, the shape is
reachable once an assent predicate exists — not today.

Laid against the structures commonly asked for:

| Structure | Spanned by |
|---|---|
| A credential issued about someone | assertion |
| Holder binding — the subject shows they control the key | assent |
| A subject presenting claims made about them | assertion + assent |
| Mutual or pairwise trust | assertion + assent, in both directions |
| A claim anchored to a point in time | witness |
| A witnessed identity log | witness |
| Delegation and accreditation chains | assertions composed edge by edge |
| Revocation | a withdrawal — itself an attestation about the claim it withdraws |

### ⛔ The bounded claim

> **Three signatures span the STRUCTURAL space of trust graphs.**

That is the whole claim, and it deliberately stops there. **Two things people reasonably expect are
not spanned:**

- **Selective disclosure** (BBS+, SD-JWT) — revealing some fields of a claim and not others.
- **Zero-knowledge predicate proofs** — proving *"over 18"* without revealing a birth date.

These are not missing graph shapes. They are a different axis — **what you reveal**, not **who
vouches** — and nothing about composing signatures reaches them. polis treats minimal-disclosure
retrofits as a non-goal rather than a gap to engineer around: its identities are relational and
public by construction, and a design that pretended otherwise would be claiming a property it does
not have.

## 5 What cannot be retrofitted: testimony

A protocol's **structure** can almost always be adopted later. A log format, a document shape, an
identifier scheme — if the primitive carries the facts, a projection can be written the day someone
needs it, and it can even begin its own history on that day.

What cannot be added afterwards is **a witness who was there.** A chain of signatures proves it is
internally consistent; it never proves *when* anything happened. A log created today can claim any
past date and still verify. The only thing that bounds that is a third party's signature made at the
time — and that signature has to have been collected at the time.

So polis collects witness testimony from the first registration a witnessing discovery service sees,
and carries it beside the thing witnessed. Every day that passes without it is a day of history that
can never be independently dated, whichever protocol later wants to read it.

## 6 How the claim is kept honest

A sentence like *"the primitives recombine into other shapes"* is exactly the kind of claim an
evaluator should distrust until something checks it. So one check exists.

The Go implementation carries a test (`cli-go/pkg/site/webvh_sufficiency_test.go`) that builds a site,
rotates its key through the same code a real rotation uses, has a discovery service witness the
rotation, and asserts that **every field a
`did:webvh` log entry requires has a populated source** in what polis already publishes — naming the
source for each:

| `did:webvh` log-entry field | polis source |
|---|---|
| `versionTime` | the key-history entry's `valid_from` |
| `versionId` | the entry's epoch, and a hash of the entry |
| `state` | the DID document projected at that epoch |
| `parameters.updateKeys` | the entry's `key` |
| `parameters.scid` | the genesis entry and genesis state, computable when a log is created |
| `parameters.portable` | a choice the projection makes in its first entry |
| `proof` | the entry's `transition_sig` for past rotations — a proof in that protocol's own format would be minted going forward |
| `witness` | the entry's discovery-service witnesses |

Someone who later reshapes the key history — drops `valid_from`, stops carrying witnesses — fails
that test with the field they just made unreachable named in the message.

⛔ **What the test proves, and what it does not.** It proves the primitives are **sufficient** to
project that shape. It does not make polis an implementation of that protocol: polis emits no such
log, resolves no such identifier, and the test is not evidence that it could interoperate with one.
Sufficiency is the claim this page makes. **Projection is the unit, not support.**

## See also

- [Identity](identity.md) — domain + key, and the `did:web` document derived from it
- [Assertions](assertions.md) — attestation, and why a counter-signature is a pair of them
- [Key history](../spec/key-history.md) — the chain a projection reads
- [Witness](../spec/witness.md) — the countersignature, evidence and never a gate
