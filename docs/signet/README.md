# Signet

**Signet is the identity layer of polis** — the set of specifications covering how a person proves
what they made, states the terms it may be used under, vouches for someone else, and lets something
act on their behalf.

It is not a product, a service, or an add-on. It is the name for the specs, so that implementers,
standards bodies, and anyone building against polis have something to reference. **A polis user never
"uses Signet"** — they set terms on a post, or see that a signature verified.

> **New here and already familiar with the identity field?** Start with
> [`overview.md`](overview.md) — how polis thinks about identity and trust, the standards it
> implements and declines, and what it has not solved, on one page.
>
> **Building on polis?** Start with [`concepts/graph.md`](concepts/graph.md), then the
> [recipe book](recipes/README.md). The specs are for writing a second implementation; you do not
> need them to issue, read or verify a claim.

## What it covers

| | |
|---|---|
| **Identity** | a domain and a keypair — also published as a conformant `did:web` document |
| **Attribution** | *this is mine*, provable and tamper-evident, and it survives leaving your site |
| **Consent & licence** | *here are my terms* — machine-readable, signed by the author, travelling with the work |
| **Attestation** | *I claim this about that* — one identity signing a statement about a subject, pinnable to exact bytes |
| **Delegation** | *this may act for me*, scoped and revocable. Today the agent signs with your key, marking each act as its own; delegating without handing over the key is a later mode, not yet built |
| **Verification** | whether a thing is signed and whether it verifies — stated as fact, never adjudicated |

## The two laws

Everything here obeys both. They are the shortest accurate description of the design.

**1 · Signed is portable and permanent. Computed is per-viewer and disposable.**
Never sign a computation; never compute what should be signed. This is why polis has attestations and
not scores: the evidence travels, the judgment is recomputed by whoever is asking.

**2 · The protocol states facts. Consumers hold policy.**
Whether something is signed, and whether the signature verifies, are facts. Whether that makes it
acceptable is a judgment belonging to whoever is asking — never to the verifier, and never to a
version gate. A producer may sign or not; a consumer may hold that against them.

## Status

Signet is being built and documented incrementally. **Each page appears when the capability it
describes ships**, so an empty slot below means unbuilt, not undocumented.

### Concepts — what it is and why
*for reviewers, and anyone deciding whether this is sound*

| Page | Status |
|---|---|
| [`concepts/identity.md`](concepts/identity.md) — domain + key, actors as citizens, did:web | **shipped** — domain + key, did:web, and *actors as citizens*: an actor that signs as itself instead of borrowing a tenant's name |
| [`concepts/assertions.md`](concepts/assertions.md) — attribution, licence, attestation, counter-signature | **shipped** — all four written; counter-signature turns out to be a pair of attestations rather than a fourth thing |
| [`concepts/trust.md`](concepts/trust.md) — why reputation is computed and never stored | **partial** — *why edges are signed* written; the computed-reputation argument arrives with the capability |
| [`concepts/verification.md`](concepts/verification.md) — facts vs judgments, and what a consumer decides | **shipped** — the line between fact and judgment, the integrity observation a publishing verifier emits, how it avoids being wrong, and what a withdrawal cannot reach |
| [`concepts/projection.md`](concepts/projection.md) — **the primitives recombine: why polis is a substrate rather than a conformance target** | **shipped** — the rule, the trust triangle and its bounded claim, and the test that keeps the claim honest (sufficiency, not support) |
| [`concepts/delegation.md`](concepts/delegation.md) — a user letting software decide some things for them: the grant, the marker, revocation, and what the marker does not prove | **shipped** — one user agent today |
| [`concepts/graph.md`](concepts/graph.md) — **the attestation and identity graph, end to end:** nodes, edges, the trust triangle, what a reader verifies and what stays policy | **shipped** — and the entry point for developers; it states where the model stops, including the two things three signatures do not span |

### Spec — how to implement it
*for implementers, including anyone writing a second implementation*

| Page | Status |
|---|---|
| [`spec/signing-base.md`](spec/signing-base.md) — the canonical signing base for every signed object | **shipped** — all eight signed shapes, both families, with worked examples checked against the implementation mechanically |
| [`spec/license.md`](spec/license.md) — the signed consent/licence format | **shipped** |
| [`spec/attestation.md`](spec/attestation.md) — subject × predicate, the wire shape | **shipped** — the attestation record, the signed follow file and the signed blessing list |
| [`spec/custody.md`](spec/custody.md) — the operator holds the key; nothing is revocable | **shipped** — the actor registry, its signing base, the countersignature and the stranger's check; and the tenant half: the operator's per-tenant custody declaration, the tenant's grant, and the tenant's own check |
| [`spec/delegation.md`](spec/delegation.md) — scoped, **revocable** authority a tenant grants | **shipped for one agent** — the grant, the in-signature marker and live-grant resolution, implemented for Rosie |
| [`spec/did-web.md`](spec/did-web.md) — the polis did:web profile | **shipped** |
| [`spec/key-history.md`](spec/key-history.md) — a site's own published key history, so old signatures survive a rotation | **shipped** — the format, the verifier walk, and the resolution rule that makes a rotated site's older artifacts verify; rotations now carry discovery-service witnesses |
| [`spec/witness.md`](spec/witness.md) — every signature a discovery service makes, and the witness a site carries | **shipped** — the shared envelope, the four DS signatures, the witness record and its golden bytes, how a verifier checks one, and what a witness does not protect against |

> [`spec/signing-base.md`](spec/signing-base.md) is the load-bearing document. **The test is that a
> second implementation could be built from it alone** — so its worked examples are compared against
> the implementation's own golden bytes on every test run rather than transcribed by hand.

### Guides — how to use it
*for people running a polis site*

| Page | Status |
|---|---|
| [`guides/set-your-terms.md`](guides/set-your-terms.md) | **shipped** |
| [`guides/attest.md`](guides/attest.md) — making a claim about someone else's work | **shipped** |
| [`guides/verify-content.md`](guides/verify-content.md) — checking a site, from anywhere, trusting nothing | **shipped** |
| [`guides/network-health.md`](guides/network-health.md) — what the operator's Judge could not verify, and how to tell a quiet Judge from a muted one | **shipped** |
| [`guides/who-blessed-this.md`](guides/who-blessed-this.md) — whether a blessing was decided by a person or by their agent, and under what grant | **shipped** |

### Recipes — build with it
*Issuing claims from an app, reading the graph, verifying a chain*

| Page | Status |
|---|---|
| [`recipes/README.md`](recipes/README.md) — the recipe book: task-first, each with the records it produces, the commands, and how to verify it from another machine | **shipped** — eleven recipes, ten of them run end to end on every test run (recipe 7 is parked and says so), and the list of what cannot be built yet |

> **Every runnable recipe is exercised by a test**, the same way the spec's worked examples are checked
> against the implementation. A recipe that cannot yet be written is listed as a gap, with the work
> that will unblock it — never papered over.

## The map

*The derivation map is not drawn yet.* It will be a single diagram of how everything in polis derives
from three owned primitives — identity, content, relationships — carrying three dimensions: what is
**shipped vs designed vs open**, what is **inherited from the web vs polis's own**, and what genuinely
**derives** from what versus what is merely adjacent.

It doubles as this project's status board, and it is kept honest structurally: a node turns from
*designed* to *shipped* only when the work that shipped it says so.

## See also

- [`../general/vision.md`](../general/vision.md) — why polis exists; Signet is steps one through three made concrete
- [`../general/security/security-model.md`](../general/security/security-model.md) — cryptographic foundations and threat model
- [`../general/reference/policy-grammar.md`](../general/reference/policy-grammar.md) — **inbound** policy (who may interact with you), distinct from a **licence** (what others may do with your work)
- [`../general/concepts/content-types.md`](../general/concepts/content-types.md) — the data model these specs extend
