# The graph

*The attestation and identity graph, end to end*

*For* [Developers](../../README.md#building-on-polis) · [Reviewers](../../README.md#reviewing-the-security-and-identity-design) — *About* [Relationships](../../general/README.md#relationships) · [Identity](../../general/README.md#identity) — *Kind* [Concept](../../README.md#kinds-of-page) — *See also* [spec](../spec/attestation.md) · [recipe](../recipes/04-list-attestations.md) · [recipe](../recipes/13-verify-a-sites-attestations.md)

polis has no central graph. Every site publishes signed statements about itself and about others, at
URLs anyone can fetch; **the graph is what you get by following them.** This page says what the nodes
and edges are, how you walk from one to the next, which parts of any answer are facts, and where the
model stops.

To build something with it, go to [the recipe book](../recipes/README.md). The formats themselves are
in the [specs](../README.md#spec--how-to-implement-it).

---

## 1 Nodes

There are two kinds, and every edge connects them.

| Node | Is | Named by | Pinned by | Checked against |
|---|---|---|---|---|
| **Identity** | a domain and the key it publishes | its `https` base URL, e.g. `https://alice.example` | — | its `.well-known/polis`: the `public_key`, and the `public_key_history` of every key it has held |
| **Record** | something signed and published at a URL — a post, a comment, an attestation | its URL | a **content hash**: `current_version` for an attestation, `current-version` for a post or comment | the key of the identity that signed it |

**The binding between a domain and its key is the web's**, not polis's: fetching
`https://alice.example/.well-known/polis` over TLS is how you learn the key, so DNS and the web PKI
are what say that key speaks for that domain. polis adds no registry, issuer or authority of its own
([identity](identity.md)).

**A key can change and the identity survives.** Each rotation is signed by the key it replaces, so a
post, comment, attestation, tag file or licence signed before a rotation still resolves to the key that
made it. A follow file and a blessing list carry no signing time, so they verify against the current key
only ([key history](../spec/key-history.md), [recipe 8](../recipes/08-survive-a-key-rotation.md)).

## 2 Edges

An edge is a **signed statement** by one node about another. Every one is published by the party
making it, at a place you find through that party's identity document.

| Edge | From → to | Artifact | Signed by | Found through |
|---|---|---|---|---|
| **authorship** | identity → post or comment | the post's own frontmatter | the author's key | the site's `index.jsonl` |
| **attestation** | identity → identity, or → record (optionally pinned) | `pub.polis.attestation`, one record per claim | the issuer | the issuer's `index.jsonl` |
| **withdrawal** | identity → its **own** earlier attestation, pinned | an attestation with predicate `…withdrawal` | the same issuer | the issuer's `index.jsonl` |
| **same-as** | identity ↔ identity | **two** attestations, one from each side | each side's own key | each side's index |
| **follow** | identity → identities | `following.json` | the follower | the file itself |
| **blessing** | site → comment on its post, pinned | `blessed.json` | the blessing site | the file itself |
| **key handover** | key → the key after it | `public_key_history` in `.well-known/polis` | the retiring key | the identity document |
| **actor listing** | operator → actor domain, with expected actions (the record and event types the operator expects it to produce) | the actor registry | the operator, and optionally the actor, which **countersigns** its entry | `.well-known/polis` → `actor_registry` |
| **witness** | discovery service → record, pinned to its hash | the site's witness set | the discovery service | `.well-known/polis` → `witnesses` |

Three properties hold for all of them.

- **Every edge is signed by the party whose claim it is.** Nothing in the graph is a row in a service
  that says what someone else did. A discovery service holds copies to index, and an event stream
  that says what happened recently (§2a). **Both sit outside the graph**, which ends at what sites
  publish. The site holds the record.
- **A record can be an edge's subject.** It has a URL and a hash, so a correction, a withdrawal or a
  dispute is just an attestation whose subject is another record. This is how the graph grows new
  shapes without new machinery ([attestation §6](../spec/attestation.md#6-a-subject-may-be-another-attestation)).
- **A pin fixes an edge to exact bytes.** An attestation whose subject carries a `version` is about
  what hashed to it, and an edit of the subject cannot move it
  ([recipe 2](../recipes/02-pin-a-correction.md)).

## 2a Events: notices, not records

A discovery service also carries a **stream of events**. An event is a short, typed notification,
such as `pub.polis.comment.blessing.requested` or `pub.polis.actor.registered`. It names the site whose
signed request caused it (its `actor`) and carries that signature, which the service checked before
accepting the event. Events are how a site learns that something happened without watching every other
site.

**An event is not a record.**

| | Record | Event |
|---|---|---|
| Lives | at a URL on the site that signed it | in one discovery service's stream |
| Pinned by | a content hash | its position in that stream |
| Kept | for as long as the site publishes it | recent events only; the reference service keeps about 90 days |
| Found by | following pointers from an identity document | asking that discovery service |
| Is | the statement | a notice that a statement was made, or that a request arrived |

- **Events are not edges.** Nothing in §2 is an event, and walking the graph never needs one. An event
  can point at a record, but the edge is the record, published by its issuer.
- **Events are delivery. Records are the truth.** When an event and a record disagree, the record wins,
  and a record with no event is just as true. An event that announces an actor registration carries a
  pointer to the registry, not the facts in it ([custody](../spec/custody.md)).
- ⚠️ **A few facts travel only on an event**: a post author's blessing decision carries its
  discovery-service witness on the event ([witness](../spec/witness.md)). Such a fact lasts as long as
  the stream keeps it. That is a limit on the fact, not a second source of truth.

## 3 The trust triangle

Most structures people build out of claims combine three signatures over one claim:

| Leg | Says | In polis today |
|---|---|---|
| **Assertion** — the issuer | *"I assert X about B."* | ✅ an attestation — [recipe 1](../recipes/01-make-a-claim.md) |
| **Witness** — a discovery service | *"I saw these bytes at time T."* | ✅ a witness, carried beside the record — [recipe 9](../recipes/09-prove-when.md) |
| **Assent** — the subject | *"I saw this, and I do not disown it."* | ⛔ **not yet.** The format can carry it — B issues a record whose subject is A's attestation, pinned — but **no predicate is reserved**, and what assent should mean is unsettled |

⚠️ **Assent is the missing leg, and the reason it is missing is the definition, not the format.**
*"I agree this is true"* would turn every assent into an argument about truth. The honest minimal
reading is a fact — the subject's key signed over these bytes — with the consumer deciding what that is
worth. And it could never be required: an unassented claim still stands on its issuer's signature.

**`same-as` is not assent.** It is two assertions of the same kind, one in each direction. It works
because both parties are making the same claim; it does not let B respond to a *different* claim A made
about B.

### ⛔ The bounded claim

> **Three signatures span the STRUCTURAL space of trust graphs** — issuance, holder binding,
> presentation, mutual trust, timestamping, witnessed logs, delegation chains composed edge by edge,
> and revocation as a record about a record.

**Two things are not spanned, and this page will not pretend otherwise:**

- **Selective disclosure** (BBS+, SD-JWT) — showing some fields of a claim and not others.
- **Zero-knowledge predicate proofs** — proving *"over 18"* without revealing a birth date.

They are not missing graph shapes. They are a different axis — **what you reveal**, not **who
vouches** — and no composition of signatures reaches it. polis identities are public and relational by
construction ([projection §4](projection.md#4-the-trust-triangle)).

## 4 Walking it

**Outward from a site, everything is reachable and all of it is by pointer.** Start from an identity
document and follow what it names — never an assumed path, because a site's layout is configurable.

```text
https://alice.example/.well-known/polis
  ├─ public_key, public_key_history   → the keys that sign everything below
  ├─ bundles["pub.polis.core"].path   → bundle manifest; index.jsonl sits beside it
  │     └─ index.jsonl                → every post, comment, tag and attestation the site indexed
  │           └─ each attestation     → its subject: an identity URL, or a record URL (+ pin)
  ├─ license                          → the site's signed terms
  ├─ witnesses                        → discovery-service witnesses for its records
  ├─ actor_registry                   → (an operator) the actors it runs
  └─ operator                         → (an actor) the operator that lists it — a CLAIM, checked there
```

**Inward — *"who has said something about Bob?"* — is not answerable from the sites.** Every edge is
published by its issuer, so the only way to find edges pointing *at* a node is to already know every
issuer, or to ask something that has indexed many of them. A discovery service indexes registered
attestations and can be asked for the ones naming a subject
(`GET /v1/content?type=pub.polis.attestation&metadata.subject=…`), but it is a cache — it holds only what
was registered with it — so its answer is never *everyone*. Its event stream carries an attestation's
subject only in the payload, with no `target_domain`, so nobody is notified when they are named. **From
the sites you can issue, verify and walk outward; inward, you can only ask an index.** That is the first
entry on the [gap list](../recipes/README.md#what-you-cannot-build-yet).

| You want | Today | Recipe |
|---|---|---|
| everything a site has claimed | its index | [4](../recipes/04-list-attestations.md) |
| whether a record is genuine | the record + its issuer's published key | [1](../recipes/01-make-a-claim.md) |
| whether a claim was withdrawn | the issuer's other records | [3](../recipes/03-withdraw-a-claim.md) |
| whether two domains are one party | both halves of a `same-as` pair | [6](../recipes/06-prove-same-party.md) |
| whether a signer is an operator's actor | the operator's registry | [7](../recipes/07-check-an-actor.md) |
| an independent date for a record | its witness | [9](../recipes/09-prove-when.md) |
| everyone who vouched for someone | ⚠️ only what a discovery service indexed | gap list |

## 5 What a reader verifies, and what is policy

**The protocol states facts. The consumer holds policy.** Everything polis reports about the graph is
one of the facts below; everything on the right is a decision it leaves to you, and no polis tool makes
it for you.

| Facts — any reader gets the same answer | Policy — yours |
|---|---|
| a signature is `valid`, `unsigned`, `invalid`, or `unknown` (could not check) | whether an unsigned record is acceptable |
| which key verified it — the current key, or a retired one at the record's **claimed** signing time (posts, comments, attestations, tag files and licences; a follow file or blessing list carries no signing time and verifies against the current key only) | whether a retired-key pass is good enough |
| a pin matches the subject's hash, or does not | whether a claim about an old version still matters |
| a withdrawal from the same issuer exists, or does not | how long you keep acting on a claim — there is no expiry unless you set one |
| a witness verifies, is absent, or is forged | whether you require one — **polis never does** |
| an operator's registry lists a signer, and whether what it produced (a record it signed, or an event its request caused) is of an expected type | whether a deviation matters |
| issuer X endorsed Y | **whether X's endorsement means anything to you** |

⚠️ **There is no score, and there will not be one.** An endorsement is evidence that one party vouched
for another. How many endorsements, from whom, weighted how — that is a computation over the evidence,
done by whoever is asking and recomputed for them. A number stored in the graph would be someone else
making that judgment for every reader at once ([trust](trust.md)).

**A reader must tolerate what it does not recognise.** An attestation with a predicate or subject type
you do not know still verifies, still parses, and still renders as *issuer, subject, date*; it is simply
ignored by anything computing over records ([attestation §7](../spec/attestation.md#7--tolerance-what-a-reader-must-do-with-something-it-does-not-recognise)).
That rule is what lets anyone add a predicate — `com.yourdomain.reviewed` — without asking.

## 6 What the graph does not do

- **It does not say who is behind a key.** A signature proves the key holder signed. On a hosted
  deployment that key is held by the operator, and the graph cannot tell you whether the author and the
  key holder are the same person ([custody](../spec/custody.md)).
- **It does not date anything by itself.** Every date a site signs is the site's own claim. Only a
  witness is someone else's — and a witness says *no later than*, never *written at*.
- **It does not enforce anything.** A licence, a registry, an endorsement: each is evidence a party
  published, not a control that stops anyone.
- **It has no global view.** There is no list of all identities or all edges, and inward traversal is
  only as complete as whatever index you ask (§4).
- **It does not reveal selectively or prove predicates in zero knowledge** (§3).
- **It does not forget.** A withdrawal adds a record; nothing is deleted, and a missing record is never
  read as a retraction.

## See also

- [The recipe book](../recipes/README.md) — every recipe runs on every test run
- [Assertions](assertions.md) — what polis lets someone say
- [Identity](identity.md) — domain + key, and the documents derived from it
- [Projection](projection.md) — why the primitives are shaped to become other formats
- [Attestation spec](../spec/attestation.md) · [Custody spec](../spec/custody.md) · [Witness spec](../spec/witness.md) · [Key history spec](../spec/key-history.md)
