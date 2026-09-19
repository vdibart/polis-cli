# Identity

*For* [Reviewers](../../README.md#reviewing-the-security-and-identity-design) · [Developers](../../README.md#building-on-polis) — *About* [Identity](../../general/README.md#identity) — *Kind* [Concept](../../README.md#kinds-of-page) — *See also* [spec](../spec/key-history.md) · [spec](../spec/did-web.md) · [recipe](../recipes/08-survive-a-key-rotation.md) · [recipe](../recipes/06-prove-same-party.md)

**A polis identity is a domain and a keypair.** There is no account, no registry, no issuer, and
nothing to sign up for. You own a domain, you hold a private key, and you publish the matching public
key on that domain. Everything else in polis — attribution, licence, attestation, delegation — is built
on that one fact.

> **Status.** This page describes what is built, including *actors as citizens* — background
> components holding their own keys and identities, live since 2026-09-12 and described in §4.

---

## 1. The whole model

```
https://alice.com/.well-known/polis   →   { "public_key": "ssh-ed25519 AAAA…" }
```

That file is the identity document. Anyone who can fetch it can verify anything Alice signed.

**What makes it trustworthy is not the file — it is how you got it.** Fetching over HTTPS means DNS
resolved the name to Alice's server and the web PKI already attested that the server is the one the
domain names. The key↔domain binding is therefore *inherited from infrastructure that already exists
and is already adversarially tested*, not asserted by polis.

Which yields the properties that matter:

| Property | Because |
|---|---|
| **No issuer** | Nobody grants you an identity. You register a domain and generate a key. |
| **No revoker** | Nobody can take it away. There is no account to suspend. |
| **Portable** | The key is a file. You can move it, back it up, or walk away with it. |
| **Verifiable by anyone** | The public key is public. Verification needs no permission and no API. |

And the cost, stated plainly: **the identifier is the domain.** Lose the domain and you lose the
identifier — though not the key, and not the ability to prove that the same key signed the old work.

## 2. It was already `did:web`

Anyone from the decentralized-identity world who hears the description above says the same sentence:
*"you've built `did:web`."* They are right, and it was arrived at independently.

`did:web` is the W3C DID method that uses a web domain as the root of trust: a public key published at
a well-known location, anchored in DNS and TLS, with no ledger anywhere. That is exactly the model
above, in a different encoding.

So polis publishes the encoding too. Every site serves a conformant DID Document:

```
https://alice.com/.well-known/did.json   →   did:web:alice.com
```

**One key, two shapes.** The same 32 bytes, written once as an OpenSSH line for polis's own tooling and
once as a JWK for everyone else's. Nothing about polis changed to make this true — the file is a second
encoding of a key that was always there.

### What it buys

- A polis identity is a **first-class citizen of the DID/VC ecosystem**: it can be looked up, referred
  to, and **issued a Verifiable Credential** by anybody, today, with no cooperation from polis.
- The posture *"polis interoperates with the identity stack rather than reinventing it"* becomes
  **structurally true** — checkable by resolving the DID — instead of a claim in a document.

### What it does not buy

- **It does not make polis-signed content verifiable by VC tooling.** Polis signs content with the SSH
  signature format; a VC verifier expects a JWS or a Data Integrity proof. Being *resolvable* and being
  *verifiable inside credential tooling* are different capabilities, and only the first is built.
- **It does not add a login.** Polis is not an identity provider and is not trying to become one.
- **It does not make the identity durable against losing the domain.** `did:web` itself has no key
  history. polis's document does carry one — every retired key stays in `verificationMethod`, projected
  from the site's own signed chain ([`did-web.md` §3.5](../spec/did-web.md#35-key-history)), so a key
  can change and old signatures still resolve — but that chain lives on the domain, and it has no
  pre-rotation. `did:webs` (did:web plus a KERI key-event log with pre-rotation) is the standard answer,
  and it reuses `did:web`'s discovery — so what is built here is the front door that a durable identity
  would keep, not a step to be undone.

The full profile — the exact document, the hosted subdomain form, the `@context` and why it is that
one, and rotation semantics — is [`../spec/did-web.md`](../spec/did-web.md).

## 3. Derived, not authored — the line that governs who may write what

The DID Document is **derived**: it is fully determined by the host and the public key, and it says
nothing the site was not already saying. So polis generates it for every site, unasked, and rebuilds it
whenever it goes missing or is edited. There is no consent question, because there is no new claim.

A **licence** is the opposite. It is a signed statement of what an author intends, and creating one for
somebody who never made it would assert that they said something they did not — so no component ever
writes one on an author's behalf, not even to restore a deleted file.

> **One rule, two opposite answers.** Never author on someone's behalf; always rebuild what is derived.
> The rule turns up throughout polis, and it is worth reading whenever a component is about to write
> into a site it does not own.

## 4. Actors as citizens — software gets an identity, and that NARROWS what it can do

An operator runs software that acts on the network. In polis these are **actors**: Judge verifies
sites, Patrol and Medic sweep for damage, Chaplain repairs what drifted. (Rosie is not one of them — she
works for the user, below.)

**Three of them sign with tenants' keys.** Not metaphorically — they read a tenant's private key off
disk and sign with it. Chaplain re-registering a comment produces bytes **identical** to what that
tenant's own CLI would send. Not similar: identical, with no field anywhere saying software did it.

⚠️ **So "actors are getting keys" sounds like an expansion and is the opposite.**

| | **Signs as ITSELF** | **Signs as the TENANT** |
|---|---|---|
| Whose key | the actor's own | ⛔ **the tenant's, read off disk** |
| Authorised by the operator? | ✅ yes | ✅ **yes — equally** |
| What the network sees | *"Judge says X about Alice"* | ⛔ ***"Alice says X"*** |
| Can a reader tell? | ✅ yes | ⛔ **no** |

⭐ **An actor with its own key can speak as itself instead of borrowing an identity that can do
everything a tenant can do.** A reader can then disagree with Judge **without doubting Alice** —
which is not available at all when the two are indistinguishable.

⛔ **The honest word for the second column is IMPERSONATION**, and polis says so: the published value
is `as-tenant`, defined as *"the actor signed with the tenant's key. Full stop."* It is not
delegation — delegation shows a reader *"B acting for A"*, and this shows them nobody. It is not
*"on behalf of"* either, for the same reason.

⚠️ **Being honest about the worse behaviour is the design.** An operator who signs as a tenant **and
says so** is on record. If the honest value did not exist, that operator would simply not declare —
so widening the honest path matters more than making the good value the only value.

### An identity implies an accountable party

An actor's identity is only worth having if someone stands behind it. Otherwise anyone can register a
lookalike domain, sign an attestation, and be cryptographically indistinguishable from the real one —
the signature verifies against the key that domain publishes, which is exactly what a signature
proves.

So an operator publishes a signed **actor registry**: these are our actors, this is whose authority
each exercises, and these are the actions we **expect** each to perform. ⛔ **Expected, never
allowed** — the operator holds the keys, so no list can stop anything; what a list buys is that
deviation is **observable**, and the remedy is social rather than technical.

⭐ **And the consent mechanism is exit.** A self-hoster running their own actors is an operator and
publishes their own registry, with the same file and the same checks — which is what keeps *"you can
always leave"* a real answer rather than a slogan.

### When the operator holds YOUR key

Everything above is about an operator's own actors. On a hosted site the operator also holds **your**
key — that is what hosting is — and when your key is used, the result is indistinguishable from you.
**No protocol can detect that, and none ever will.**

So an honest operator **declares** it: a signed `custody` record about you, on the operator's own site,
signed with the operator's own key, saying it holds your key and whether it signs **as you**
(`as-tenant`) or **co-signs** what it does. ⭐ **That is a promise you can test**, because you are the
one person who knows what you did yourself — and a discovery service that records **who signed** each
of your events gives you something to test it against.

⚠️ **The honest limits, stated where they belong:**

- **It makes custody visible. It does not reduce it by one bit.** The operator can do exactly what it
  could do before. Only holding your own key reduces custody.
- **Your check is independent in its verification, not in its data.** Run from your own machine, it
  verifies each record against a key fetched from the site that signed it — so an operator can withhold
  a record but cannot forge one that passes.
- **Silence proves nothing.** A self-hoster has nothing to declare, and so does an operator that
  chooses not to — from outside they look the same.

Full mechanics: [`../spec/custody.md`](../spec/custody.md) — §1–§11 for actors, §12 for your key.

### When a helper acts for you

Some software works **for you** rather than for the operator: a **user agent**. polis's first is Rosie,
who approves or turns away comments using your own rules. She signs with your key — so, like custody, an
old reader shows her approval as yours — but two things make her acts **yours to trace**:

- **She acts only under your own record** of what she is to you — a *grant* on your site, naming her, who
  provides her, and which versioned behaviours. Switching her off withdraws it, and she stays off.
- **Every act carries a mark inside its signature**: her name and that record's URL. *Who did this?* has
  an answer for each approval — *you*, or *Rosie, under this record*.

⚠️ **The honest limits:**

- **The mark is self-declared.** Whoever holds your key could sign without it. Marked acts are fully
  traceable; unmarked ones are exactly as indistinguishable as custody says.
- **A record polis.pub made for you is not consent.** On polis.pub Rosie starts on, and the record says
  plainly that the host issued it (`basis: hosting-terms`). What it proves is *"polis.pub switched her on,
  and you have not switched her off"*.
- **Custody is not reduced by one bit.** Your host still holds your key, for publishing — not because of
  Rosie.

The whole idea, with its limits, is in [Delegation](delegation.md); the full format is [`../spec/delegation.md`](../spec/delegation.md).

## See also

- [`../spec/did-web.md`](../spec/did-web.md) — the profile, in full
- [`../spec/custody.md`](../spec/custody.md) — an operator's actors, the keys it holds for its tenants, and what it says about both
- [`assertions.md`](assertions.md) — what an identity can then say: attribution, licence, attestation
- [`../../general/security/security-model.md`](../../general/security/security-model.md) — key generation, storage, domain binding, threat model
