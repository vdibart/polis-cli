# How polis thinks about identity and trust

*For* [Reviewers](../README.md#reviewing-the-security-and-identity-design) · [Developers](../README.md#building-on-polis) — *Kind* [Concept](../README.md#kinds-of-page) — *See also* [spec](spec/key-history.md) · [spec](spec/custody.md) · [recipe](recipes/05-verify-a-site.md)

polis is an open system for publishing from your own domain. Underneath it sits a question the
identity field has worked on for a long time: how can anyone check who said something, what they
said about someone else, and when — without asking a platform?

This page is a short map of polis's answers: what it built, what it chose not to build, and what it
has not solved. Each section names the detailed page, and most show a live example you can fetch
yourself. Where there is nothing live to point at, a short excerpt stands in and says where it comes
from.

> **About the examples.** Excerpts were fetched from live sites on 2026-09-16 and are abridged:
> signatures and long keys are cut short with `…`. The linked files are the record. If an excerpt
> and its link disagree, the link is right and this page is out of date.

---

## We don't ask anyone to adopt anything

New identity projects usually turn out to be DIDs reinvented, or a request to adopt a new stack.
polis is trying to be neither.

polis keeps a small set of signed primitives: a key, a signed record, a witness. The effort goes into
getting their **shape** right, so each can be projected into whatever a protocol needs without the
primitive changing. One principle decides most design questions:

> **A non-gating primitive projects into a gating protocol. A gating primitive projects into nothing.**

So in polis, the moment something becomes mandatory is the moment to ask what it just made
unreachable.
→ [Projection](concepts/projection.md)

## An identity is a domain and a key

A site publishes an Ed25519 public key at `/.well-known/polis`. The link between key and domain
comes from DNS and the web PKI. polis adds no trust root of its own, and there is no issuer, account
or revoker.

Every site also serves `did.json`, the same key as a `did:web` document. That document is computed
from the site's published key history, in which every rotation is signed by the key it replaced.
Retired keys stay resolvable, but they no longer speak for the site.

**Live:** [`vdibart.polis.pub/.well-known/polis`](https://vdibart.polis.pub/.well-known/polis). This
site has never rotated its key, so its chain holds only the genesis entry.

```json
{
  "public_key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMoMX7YB…",
  "public_key_history": {
    "current": {
      "epoch": 0,
      "key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMoMX7YB…",
      "valid_from": "2026-03-03T05:35:09Z",
      "transition_sig": null
    },
    "history": []
  },
  "license": "/content/pub.polis.core/license/license.json"
}
```

**Live:** [`vdibart.polis.pub/.well-known/did.json`](https://vdibart.polis.pub/.well-known/did.json)
publishes the same key.

```json
{
  "id": "did:web:vdibart.polis.pub",
  "verificationMethod": [{
    "id": "did:web:vdibart.polis.pub#key-1",
    "type": "JsonWebKey2020",
    "publicKeyJwk": { "kty": "OKP", "crv": "Ed25519",
                      "x": "ygxftgFjrWKJZ92RT1xkOJ3Vf5IAQAk3d31XL_wEYQA" }
  }],
  "authentication":  ["did:web:vdibart.polis.pub#key-1"],
  "assertionMethod": ["did:web:vdibart.polis.pub#key-1"]
}
```

**From the spec, after one rotation.** No site on the network has rotated yet, so this excerpt is
illustrative. In `did.json`, the retired key stays in `verificationMethod` and leaves
`authentication` and `assertionMethod`.

```jsonc
"public_key_history": {
  "current": {
    "epoch": 1,
    "key": "ssh-ed25519 AAAA…NEWKEY",
    "valid_from": "2026-09-15T10:22:03Z",
    "transition_sig": "-----BEGIN SSH SIGNATURE-----\n…",   // signed by the OLD key
    "witnesses": [ { "action": "key-rotation", "ds": "https://ds.polis.pub", … } ]
  },
  "history": [ { "epoch": 0, "key": "ssh-ed25519 AAAA…OLDKEY", "valid_until": "2026-09-15T10:22:03Z", … } ]
}
```

The honest cost is that the identifier *is* the domain.
→ [Identity](concepts/identity.md) · [Key history](spec/key-history.md) · [did:web profile](spec/did-web.md)

## Signed things travel; judgments don't

1. **Signed things are portable and permanent. Computed things are per-viewer and disposable.**
   polis stores signed evidence and never a score. Whoever is asking does the computing.
2. **The protocol states facts. Consumers hold policy.** Whether a signature verifies is a fact.
   Whether that is good enough is up to the reader. An unsigned artifact is *unsigned*, never
   *invalid*.

**Live:** [a post that carries its own terms](https://vdibart.polis.pub/content/pub.polis.core/post/20260828/this-post-carries-its-own-terms.md).
The signature covers the frontmatter and the body together.

```yaml
---
title: This post carries its own terms
published: 2026-08-28T16:20:07Z
current-version: sha256:e1d970e9f42e…   # hash of the body
license:                               # inside the signature
  v: pub.polis.license.v1
  profile: pub.polis.license.reserved/1
  train-ai: n                          # AIPREF
  search: y                            # AIPREF
  ai-input: n                          # RSL
  attribution: required                # RSL
  asserted: 2026-08-28T23:21:22Z
signature: U1NIU0lHAAAAAQAAADMAAAALc3NoLWVkMjU1MTk…
---
```

→ [Assertions](concepts/assertions.md) · [Trust](concepts/trust.md)

## The trust graph is three signatures

- **Assertion.** An issuer signs an attestation about a subject, for example *same-as*,
  *correction* or *endorsement*. A claim can be pinned to the exact bytes it is about.
- **Assent.** The subject answers. The format can already carry this, because a record can be the
  subject of another record. What assent should *mean* is unsettled: polis has no predicate for it
  yet, and *"I agree this is true"* is the definition it wants to avoid.
- **Witness.** A discovery service countersigns what it saw registered, and when. It is **evidence,
  never a requirement**: an unwitnessed artifact verifies exactly as before.

**Live:** [a withdrawal](https://vdibart.polis.pub/content/pub.polis.core/attestation/20260829T044519Z-be6a9fa4824a7bad.json)
whose subject is another record,
[a `same-as` claim](https://vdibart.polis.pub/content/pub.polis.core/attestation/20260829T044437Z-dfc92682e69f33ef.json).
That claim stays published and still verifies. A withdrawal adds a record and deletes nothing.

```jsonc
{
  "type": "pub.polis.attestation",
  "issuer": "https://vdibart.polis.pub",
  "predicate": "pub.polis.attestation.withdrawal",
  "subject": {
    "type": "uri",
    "id": "https://vdibart.polis.pub/content/pub.polis.core/attestation/20260829T044437Z-dfc92682e69f33ef.json",
    "version": "sha256:dfc92682e69f33ef…"      // pinned to the exact bytes withdrawn
  },
  "asserted": "2026-08-29T04:45:19Z",
  "signature": "-----BEGIN SSH SIGNATURE-----\n…"
}
```

**Live:** one record from [`discover.polis.pub`'s witness set](https://discover.polis.pub/content/witness/witnesses.json),
found through the `witnesses` pointer in its `.well-known/polis`. The discovery service's signing key
is published at [`ds.polis.pub/.well-known/polis`](https://ds.polis.pub/.well-known/polis).

```jsonc
{
  "action": "content-registration",
  "type": "pub.polis.post",
  "url": "https://discover.polis.pub/content/pub.polis.core/post/20260913/nine-notification-rules-….md",
  "version": "sha256:bd63585297690580…",
  "artifact_hash": "sha256:9ff4ecf32ed9915c…",   // binds the whole signed artifact
  "ds": "https://ds.polis.pub",
  "ds_key_id": "ds-2026-02",                     // verify against the key the DS publishes
  "witnessed_at": "2026-09-13T10:00:01.435Z",    // the DS's clock, not the author's
  "signature": "-----BEGIN SSH SIGNATURE-----\n…"
}
```

These three cover the *structure* of trust graphs. They do not cover selective disclosure or
zero-knowledge proofs, which are about what you reveal rather than who vouches.

A witness is the one leg that can't be added later. A chain made today can claim any past date and
still verify. That is why witnessing has already started.
→ [Attestation](spec/attestation.md) · [Witness](spec/witness.md)

## Anyone can check, from anywhere

`polis validate https://any-site` checks a site's signatures over public HTTP, from a machine with no
relationship to it. An operator also publishes a signed registry of the actors it runs and the
actions it *expects* them to take. That registry is not an allow-list: whoever holds the keys can
ignore it. What it buys is that a deviation leaves evidence.

**Real output** of `polis validate https://vdibart.polis.pub`, run 2026-09-16 and trimmed. What the
check could not see is reported as not checked, so a clean result is never implied.

```text
signed-content-integrity
  ✓ content.attestations     3 attestations examined: 3 signature(s) verified, 0 unsigned
  ✓ content.blessed          blessed.json: unsigned — nothing to verify, which is a legal state
  ✓ content.comments         24 comments examined: 24 signature(s) verified, 0 unsigned
  ✓ content.following        following.json: signature verifies
  ✓ content.license          the site's stated terms are intact and signed by its own key
  ✓ content.license_rsl      public rsl.xml is exactly what this site's signed licence projects
  ✓ content.posts            8 posts examined: 8 signature(s) verified, 0 unsigned
  ✓ content.witnesses        37 artifacts examined (…): 2 witnessed, 35 unwitnessed, 0 with a witness that could not be checked, 0 with a witness that does not verify. …

key-handle-alignment
  ✓ identity.did_document    did.json publishes the same key as .well-known/polis
  ✓ identity.key_history     genesis only — the site has never rotated, so there is no handover to verify
  – identity.key_perms       NOT CHECKED: a site's private state is never served over HTTP — …

24 checks: 19 passed, 0 failed, 0 warning, 5 not applicable
[!] 5 check(s) did not run. This result covers only what was checked; see NOT CHECKED above.
[✓] Nothing checked was found wrong.
```

**Live:** [`polis.polis.pub`'s actor registry](https://polis.polis.pub/content/pub.polis.core/actor/registry.json).

```jsonc
{
  "v": "pub.polis.actor-registry.v1",
  "operator": "polis.polis.pub",
  "actors": [
    { "domain": "judge.polis.pub",
      "authority": "operator",
      "expected_actions": ["pub.polis.attestation.integrity"],    // expected, not allowed
      "countersignature": "-----BEGIN SSH SIGNATURE-----\n…" },   // the actor's own key agrees
    …
  ],
  "asserted": "2026-09-13T02:07:18Z",
  "signature": "-----BEGIN SSH SIGNATURE-----\n…"
}
```

The model is attribution, not prevention. The community that would do the watching mostly doesn't
exist yet.
→ [Check a site](guides/verify-content.md) · [Custody](spec/custody.md)

## Standards, in three registers

| | Standard | What is true | See it |
|---|---|---|---|
| **Implemented** | `did:web` | sites serve a DID document derived from their key history | [did.json](https://vdibart.polis.pub/.well-known/did.json) |
| | SSH signatures (Ed25519) | every polis signature; `ssh-keygen -Y verify` checks them | [excerpt](#check-a-signature-with-stock-openssh) |
| | IETF AIPREF · RSL | their vocabulary, unchanged, inside a licence the author signs | [license.json](https://vdibart.polis.pub/content/pub.polis.core/license/license.json) · [rsl.xml](https://vdibart.polis.pub/rsl.xml) |
| | RFC 9309 | `polis validate` applies its precedence rules when comparing a served `robots.txt` with the signed licence | [robots.txt](https://vdibart.polis.pub/robots.txt) |
| **Projectable** | `did:webvh` | key history and witnesses carry every field a log entry needs, and a test asserts it. polis emits no such log | [excerpt](#didwebvh-sufficiency) |
| **Declined** | required witnesses | same mechanism as `did:webvh`, opposite policy | [witnesses](https://discover.polis.pub/content/witness/witnesses.json) |
| | `did:webvh` as the identifier | nobody has asked for it, and it trades away the domain being the identity | |
| | Verifiable Credentials as the core | the attestation stays the source; a credential could be exported from one | |
| | RFC 8785 canonical JSON | sorting keys would change the signed bytes of artifacts that can never be re-signed | [excerpt](#why-not-rfc-8785) |

#### Check a signature with stock OpenSSH

Namespace `file`, hash `sha512`, Ed25519. The same envelope covers posts, comments, licences,
attestations and discovery-service witnesses. The signed bytes are the canonical signing base, not
the file as served.

```bash
ssh-keygen -Y verify -f allowed_signers -I vdibart.polis.pub -n file \
  -s artifact.sig < signing-base.bin
```

#### Why not RFC 8785

The spec's example signed bytes, checked against the implementation by a test on every run. Fields
are in a fixed order, not sorted. Sorting would change the bytes every existing signature covers.

```text
{"type":"pub.polis.attestation","issuer":"https://vdibart.polis.pub","predicate":"pub.polis.attestation.same-as","subject":{"type":"identity","id":"https://vincent.example.com"},"asserted":"2026-08-28T00:00:00Z","generator":"polis-cli-go/test"}
```

#### did:webvh sufficiency

From `cli-go/pkg/site/webvh_sufficiency_test.go`. The test fails, naming the field, if key history is
reshaped so that any row below loses its source. It shows the data is sufficient, not that polis
produces such a log.

```text
did:webvh log-entry field   polis source
versionTime                 key-history entry's valid_from
versionId                   entry epoch + a hash of the entry
state                       the DID document projected at that epoch
parameters.updateKeys       the entry's key
parameters.scid             genesis entry + genesis state, computable when a log is created
proof                       the entry's transition_sig (past rotations)
witness                     the entry's discovery-service witnesses
```

→ [Licence](spec/license.md) · [Signing base](spec/signing-base.md)

## What we haven't solved

**Custody.** On a hosted deployment, the operator holds the key that signs your posts. A signature
proves that *the key holder* signed. It can't tell you whether that was the author. polis makes
custody visible rather than impossible: the actor registry, checks from outside, and the option to
leave and self-host. The operator's per-tenant custody declaration is built but ships switched off, and
polis.pub's discovery service holds none yet.

**A stolen key.** A rotation needs only the old key's signature, and there is no pre-rotation
commitment. So whoever holds the current key can hand the identity to a key of their choosing, and a
witness would date that handover, not dispute it. Key history could gain a commitment later. Whether
it should, and at what cost to someone who just wants a domain and a key, is open.

**Identity after losing the domain.** Two sites asserting `same-as` about each other form a pair, and
each half verifies only against its own site's key. The shape is specified and tested, but no live
pair on two independently keyed domains exists yet. The closest thing on the network is
[one forward half](https://vdibart.polis.pub/content/pub.polis.core/attestation/20260829T044437Z-dfc92682e69f33ef.json),
since withdrawn, pointing at a domain that
[publishes no key](https://vdibart.com/.well-known/polis) to answer with. It is also unsettled what
the forward half is worth once the old domain has changed hands.
