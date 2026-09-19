# A site's published key history

*for implementers, including anyone writing a second implementation*

*For* [Developers](../../README.md#building-on-polis) · [Reviewers](../../README.md#reviewing-the-security-and-identity-design) — *About* [Identity](../../general/README.md#identity) — *Kind* [Spec](../../README.md#kinds-of-page) — *Code* [`cli-go/pkg/site/keyhistory.go`](../../../cli-go/pkg/site/keyhistory.go) — *See also* [concept](../concepts/identity.md) · [recipe](../recipes/08-survive-a-key-rotation.md)

**Lives in:** `.well-known/polis` → `public_key_history` · **Projected into:** `.well-known/did.json` → `verificationMethod`

---

## 1 The problem it solves

A polis site publishes **one** `public_key`. Rotate it and every artifact the site ever signed — posts,
comments, `following.json`, `blessed.json`, every attestation — stops verifying against what the site
publishes. The signature is still perfectly good; the key that made it is simply gone from the
document a verifier reads.

Before this, the evidence needed to check those signatures existed in exactly one place: the discovery
service's `ds_key_history` table. **So the permanence of an identity routed through a central
database** — which makes *signed → portable and permanent* false at the moment it matters most,
because a rotation is the security escape hatch and must not cost you your history.

⚠️ **Nobody notices until someone rotates**, which is precisely when they most need it to work.

A published key history closes that: **a signature a site made before a rotation stays verifiable
from the site alone, with no service to ask** — for every artifact that says when it was signed
(§4, *The resolution rule*, names the two that do not).

## 2 The shape

`public_key_history` sits inside `.well-known/polis`, beside `public_key`. **No new pointer and no
second fetch** — a verifier already opens this file first.

It mirrors `public_key_messages`, the block that already publishes the DM messages key this way:
`current` plus `history`, each entry carrying its own signature, no wrapper.

### A site that has never rotated

```json
"public_key_history": {
  "current": {
    "epoch": 0,
    "key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMoMX7YBY61iiWfd… polis-local",
    "valid_from": "2026-03-03T05:35:09Z",
    "transition_sig": null
  },
  "history": []
}
```

### After one rotation

```json
"public_key_history": {
  "current": {
    "epoch": 1,
    "key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAANEWKEY… polis-local",
    "valid_from": "2026-09-15T10:22:03Z",
    "transition_sig": "-----BEGIN SSH SIGNATURE-----\nU1NIU0lHAAAAAQ…\n-----END SSH SIGNATURE-----\n"
  },
  "history": [
    {
      "epoch": 0,
      "key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMoMX7YBY61iiWfd… polis-local",
      "valid_from": "2026-03-03T05:35:09Z",
      "valid_until": "2026-09-15T10:22:03Z",
      "transition_sig": null
    }
  ]
}
```

### Field by field

| Field | Rule |
|---|---|
| `epoch` | Orders the chain. Genesis is `0`; each rotation adds one. Explicit rather than inferred from array position, matching `public_key_messages`. |
| `key` | The **OpenSSH** public-key string, byte-identical to what `public_key` held while this key was current. Same representation as `public_key` so head-vs-published is a **string compare**, never a conversion. *(`public_key_messages` uses raw base64; that is a different key and not the model here.)* |
| `valid_from` | When this key became current — **and the timestamp inside its `transition_sig`.** See §4. |
| `valid_until` | When it stopped. **Absent on `current`: being current IS the absence of an end.** |
| `transition_sig` | The **predecessor** key's signature over the canonical rotation message that handed authority to this key. **`null` on genesis** — nothing preceded the first key, and that is the honest value, not a placeholder. |
| `witnesses` | **Optional.** Discovery-service countersignatures over the rotation that made this key current — the date the chain cannot supply for itself. Record format: [witness §4–5](witness.md). **Absent means unwitnessed, never invalid.** Not covered by `transition_sig`. |

**Three things the JSON encoding must get right:**

- ⛔ **`transition_sig` on genesis is an explicit `null`, never an absent field.** An absent field and
  a null are different claims. Implementations that drop empty values must special-case this.
- ⛔ **An empty `history` is `[]`, never absent.** *"This site has never rotated"* is a statement.
  *"This document does not model history"* is a different one.
- ⚠️ **`witnesses` is the opposite: omitted entirely when there are none.** A site that never rotated,
  or rotated before its discovery service witnessed rotations, publishes a byte-identical document.

**Order:** `history` is **oldest-first**, so `history[last]` is the key `current` succeeded.

> ⚠️ **This is a deliberate divergence from `public_key_messages`, which sorts newest-first, and it
> must not be "unified" away.** Two readers depend on it: the verifier walk in §4 is defined as
> *current verifies against `history[last]`*, and the discovery service serves its own record ordered
> by `valid_from ASC` — so a parity check zips the two directly instead of reversing one first.

⚠️ **One more deliberate difference from `public_key_messages`:** the messages history has `epoch`
and **no dates**. The identity history needs `valid_from` / `valid_until` — which is precisely what a
DID document cannot express, and the entire reason a native shape exists alongside `did.json`.

## 3 There is no document-level signature, on purpose

The block is not signed as a whole, and neither is `.well-known/polis`.

**Signing the history with the current key would be circular.** The chain exists to establish *which
key is authoritative*; a signature made by the key under question proves nothing about the question.
The per-entry `transition_sig` is the right proof, and both rotation paths already produce it: each
entry is signed by the key it succeeds, so the chain proves itself link by link.

⭐ **And `.well-known/polis` being unsigned is a feature here, not a gap.** It is the trust root. You
verify it by fetching it from the domain over TLS, which is the same act that establishes the domain
is the domain. Signing it would mean signing with a key the document exists to publish.

## 4 How a verifier walks it

For each entry after genesis, working forward from `history[0]`:

```
entry.transition_sig  must verify against  <the previous entry>.key

over the canonical JSON:

  {"action":"key-rotation",
   "domain":"<site host>",
   "new_key":"<entry.key>",
   "old_key":"<previous entry.key>",
   "timestamp":"<entry.valid_from>"}
```

The canonical JSON has **keys in alphabetical order** — `action`, `domain`, `new_key`, `old_key`,
`timestamp` — serialised with no whitespace. It is the same message the discovery service verifies at
`POST /v1/sites/keys/rotate`, so a chain and a DS record are attesting to identical bytes.

Signatures are SSH signatures (`ssh-keygen -Y sign -n file`), like everything else polis signs.

### ⛔ `valid_from` does double duty, and this is the sharpest gotcha in the design

`valid_from` is **the validity boundary AND the signed timestamp.** It must be carried as the
**byte-identical string** that went into the canonical message at rotation time.

Normalise the zone, drop the `Z`, add or round a fractional second, or round-trip it through a
date type that re-renders it — and **every transition signature in the chain stops verifying.**

Implementations should treat it as an opaque string: carry it, compare it, sign over it. Never parse
and re-emit it. (polis writes `2006-01-02T15:04:05Z` — RFC 3339, UTC, no fraction — which also makes
lexicographic comparison equal time comparison.)

### `domain`

The domain is **inside** the signature and is **not** recorded anywhere in `.well-known/polis`. A
verifier gets it from context:

| Verifier | Where the domain comes from |
|---|---|
| Fetching over HTTPS | the host it fetched from, with **the port stripped** — polis signs the port-less host |
| Reading a local directory | the site's own `.well-known/did.json` `id` (`did:web:<host>`) |
| A fleet operator | the tenant handle plus the operator's base domain |

⛔ **A verifier that cannot determine the domain must report that it could not check a chain with
rotations — never that the chain passed.** A genesis-only chain has nothing signed and verifies
without a domain.

### What else to check

Beyond the signatures, a well-formed chain satisfies:

- genesis is `epoch: 0` and carries `transition_sig: null`;
- epochs are contiguous — a gap is how a naively omitted entry shows;
- each entry's `valid_until` equals its successor's `valid_from` — a mismatch is a gap or an overlap,
  and an artifact signed inside a gap resolves to no key at all;
- no entry repeats its predecessor's key;
- `current` has no `valid_until`.

### Resolving a signature to a key

```
key_at(t) = the entry where  entry.valid_from <= t < entry.valid_until
            (current has no valid_until, so it covers everything from its valid_from onward)
```

⭐ **That one line is what this whole document is for.** An artifact signed before a rotation
resolves to the key that was authoritative when it was signed, from the site alone, with no service
in the loop.

#### The resolution rule

*(The chain and its resolver were published before any verifier called the resolver; until this rule
was applied, a rotation made every earlier post fail verification.)*

The rule is stated for a post or comment; the paragraph after the steps applies the same walk to the signed
JSON types. For a post or comment served by a site:

1. **Try the key the site publishes now** (`public_key`). If it verifies, stop: the artifact verified
   against the **current** key. ⛔ **This step comes first and is never skipped** — every artifact on
   the network was signed by a current key, and the common path must not change behaviour.
2. **Only if that fails**, and only if the site's chain is **usable**, resolve a retired key:
   - usable means the chain **verifies end to end** (§4 above, over the domain the verifier derived) and
     its **head is `public_key`**. An entry whose handover does not verify is an unsupported assertion,
     and resolving through it would launder the assertion into a pass. A chain that is not usable
     resolves nothing;
   - `t` is the artifact's own `published:` frontmatter field, which is **inside the signature** for
     both posts and comments. It must be in the chain's timestamp form (`2006-01-02T15:04:05Z`) — a
     claim that cannot be placed in the chain is not compared;
   - `key_at(t)` must land on a **retired** entry (one with a `valid_until`). Landing on `current` means
     no retired key applies, and a `t` outside every window resolves nothing — **so a retired key
     claiming a time after it was retired fails.** The window is enforced even though the claim inside
     it is not checkable;
   - verify against that entry's key.
3. **Report which key verified it.** A verifier must distinguish *verified against the current key*
   from *verified against a retired key (epoch N) at the artifact's claimed signing time* — they are
   different claims with different strength, and a consumer applying policy needs to tell them apart.
   polis reports `source` (`current` | `retired`), `epoch`, `claimed_signing_time`, and the retired
   key's `valid_from` / `valid_until`.

**The same walk, for the signed JSON types.** Only the claimed signing time `t` changes: an
attestation's `asserted`, a tag file's or a licence's `updated` — each inside its own signature.
⚠️ **The follow file and the blessing list carry no signing time**, so there is no `t` to resolve and
they verify against the **current key only**: one signed before a rotation reads `invalid` until its
author's next write re-signs it ([`attestation.md`](attestation.md), each file's *Verifying*).

⛔ **What a retired-key pass means, stated precisely:**

> **This artifact verifies against the key that was current at its CLAIMED signing time.**

**Not** *"this artifact is authentic."* `published:` is self-reported. Someone holding a retired key —
including a thief, if the key was retired **because** it was compromised — can set `published:` to any
moment inside that key's window, sign, and the artifact resolves and verifies. `valid_until` bounds the
window; **it does not bound the signer's choice inside it.** The consumer decides what a self-reported
timestamp is worth.

⭐ **This limitation is CURRENT, not inherent.** Nothing a site publishes about itself can date an
artifact independently — but a third party's countersignature carried **with** the artifact can:
the artifact cannot predate the witnessed time without the witness colluding — which is exactly the
**backdating** row of §5 below, and exactly what a witness is for.

⭐ **Content can now carry that countersignature: a discovery-service [witness](witness.md).** When an
artifact verifies only against a retired key and has a verified witness, a verifier reports the
witness's earliest `witnessed_at` — and if that is **after** the retired key's `valid_until`, the only
independent date contradicts the artifact's claimed signing time. That is what a signature backdated
with a compromised retired key looks like.

⚠️ **The word is shared with `did:webvh`, and the meaning is not.** A `did:webvh` witness is a
co-signer whose signature a log entry *requires*. A polis witness signs what it observed and is
**never required for validity** — it can contradict a chain, never withhold one
([witness §1](witness.md)).

⛔ **It changes nothing above.** A witness is evidence: never required, checked against the key the
discovery service publishes, and a missing or unverifiable witness leaves the resolution rule exactly
as stated. It bounds backdating only for artifacts that were registered with a witnessing discovery
service — which is a real gradient, stated in [witness §8](witness.md).

## 5 What the chain cannot prove, and who can

A published chain proves it is **internally consistent**. It does not prove it is **complete**.

| Attack | Why the chain misses it |
|---|---|
| **Omission** | You hold all your own old keys, so you can build *a* valid chain that quietly skips one you would rather disown — along with everything it signed. Every link verifies; the record is simply incomplete. |
| **Backdating** | The timestamp is inside the signature, but **you** generated it. Nothing in the chain proves *when*. |
| **Genesis** | `transition_sig` is `null` by definition. Nothing signs for the first key. |

All three are answered by a **witness**: a third party that is append-only and stamped its own record
of each rotation. For polis that is the discovery service, and it exposes what it saw at
[`GET /v1/sites/keys/history`](../../ds/developer/api-reference.md) — public, unauthenticated, and
**read-only**.

> ⛔ **The DS is a WITNESS, not a GATE, and the wording is not pedantic.**
>
> Affirmation would make the DS a gate: nothing counts until it blesses. Witness makes the chain
> **falsifiable**: everything counts, and the DS can prove you wrong.
>
> If a key history *required* the DS, the middleman would become permanent in the one place identity
> actually lives — which is the thing publishing the chain exists to end. A verifier who needs a
> credential to consult the witness is a verifier who cannot check anything.

**So a second implementation is complete without ever calling the DS.** Consulting it is an
additional, optional check that catches omission and backdating — and one that must be **reported as
not performed** rather than skipped in silence, because a clean report that omitted it would read as
a stronger claim than it is.

### The witness the site carries

A discovery service now also **signs** each rotation it records, and the rotating site carries that
countersignature inside the new entry (`witnesses`, §2; format in [witness](witness.md)). A verified
rotation witness answers **backdating** for that rotation **offline** — its `witnessed_at` is the DS's
own clock, and the DS refuses rotations stamped more than five minutes from it.

⚠️ **It does not answer omission.** A site that leaves a rotation out of its chain leaves that
rotation's witness out too, so the comparison against the DS's own record above is still the only
check for an omitted entry. And genesis stays unwitnessed: nothing observed a key being generated.

## 6 The `did:web` projection

The same facts appear a second time in `.well-known/did.json`, in the standard's own vocabulary.
Carry the standard, own the binding.

```json
{
  "id": "did:web:alice.polis.pub",
  "verificationMethod": [
    { "id": "did:web:alice.polis.pub#key-2", "…": "the CURRENT key" },
    { "id": "did:web:alice.polis.pub#key-1", "…": "a RETIRED key" }
  ],
  "authentication":  ["did:web:alice.polis.pub#key-2"],
  "assertionMethod": ["did:web:alice.polis.pub#key-2"]
}
```

**The whole design is which list a retired key appears in:**

| List | Contents | Says |
|---|---|---|
| `verificationMethod` | every key, current and retired | *these keys are mine, and some of them were mine* |
| `authentication` | the current key only | *this key can act as me now* |
| `assertionMethod` | the current key only | *this key speaks for me now* |

A retired key must stay **resolvable** (a signature it made two years ago is still checkable) and must
stop being **authoritative** (it can no longer act or assert). Those are different claims, and a DID
document already has two lists for exactly that.

**Fragments are `key-<epoch+1>`**, so genesis is `#key-1` — the fragment every polis DID document has
carried since the profile shipped. ⭐ A site that has never rotated therefore publishes a
**byte-identical** document, which is why adding history support changed nothing across the fleet.

Retired methods are listed **newest-first after the current one**, so reading down the list walks
backwards in time from now — the direction someone resolving a DID to check an old signature is
travelling. *(The native block is oldest-first because a verifier walks it forward from genesis.
Different readers, different order, one source.)*

⚠️ **`did.json` is a PROJECTION; `public_key_history` is the source.** The DID format cannot express
`valid_from` / `valid_until` or the transition signatures, which is the whole reason both exist.
Regenerate the document from the block; never author it.

## 7 Writing the chain

**A rotation appends. Nothing else ever writes an entry.**

`public_key` and the chain head must move **in one write**. A site whose published key its own history
never learned about is exactly the failure this document exists to prevent, and it is what happens
when two independent rotation implementations drift. polis routes all three of its rotation paths — the
Go CLI, the webapp handler, and the bash CLI — through a single append.

**Genesis** is written once, from facts the site already publishes:

```
epoch          0
key            the site's current `public_key`
valid_from     the site's own `created` field in .well-known/polis
valid_until    (absent)
transition_sig null
```

⭐ **Nothing comes from the discovery service.** The genesis entry says only what the identity
document already says, restated as a chain — which is what makes it *derived* rather than *authored*,
and safe for an actor to write on a site's behalf.

⛔ **But it assumes the current key is the FIRST key**, and that is not provable from the site alone:
a site that rotated before it kept a history has no local record of having done so. So an actor
provisioning genesis for an **existing** site must consult the witness first, and **write nothing** for
a domain the witness reports more than one key for. (A brand-new site needs no such check: a key
generated seconds ago has no history to contradict.)

⛔ **A chain is append-only and is never rebuilt.** Its later entries carry signatures made by private
keys that no longer exist, so there is no key on any machine that could reproduce them. "Repairing" a
broken chain could only mean deleting the entries that fail — which would erase real rotations and
turn a detected tamper into a clean-looking site. **A chain that is present and wrong is reported and
healed by nobody.**

### Every other writer must carry it through

The chain lives inside `.well-known/polis`, and most writes to that document are not about keys at all
— a display name, an avatar, a pointer. ⛔ **Every one of them must leave the chain byte-identical.**
A writer that rebuilds the document from a fixed field list and does not know this member erases it,
and an erased chain is exactly as unrecoverable as a broken one.

polis's writer keeps every member it does not model, and **refuses** a write that would drop or change
`public_key`, `public_key_history` or `public_key_messages` unless the write says it means to — which
only a rotation (and a messages-key republish) does.

### An erased chain is detectable from the site alone

Absence alone proves nothing: a site that never rotated legitimately publishes no chain. But the
`did:web` projection (§6) keeps retired keys in `verificationMethod`, so **a DID document naming two or
more keys beside an absent `public_key_history` is proof of an erasure** — no witness needed. A
validator reports it as a failure. ⚠️ An actor regenerating `did.json` must therefore **never** rebuild
it into a document naming fewer keys: that would overwrite the only local evidence within one sweep.

## 8 What this does not do

- **It is not a revocation list.** Publishing history is not a trust judgement: it says *this key was
  current then*, and consumers decide what that is worth.
- **It says nothing about other people's keys.** Resolving another site's history is that site's
  document, fetched the same way.
- **It does not change how anything is signed.** Content signing is untouched; this only makes the
  right key findable afterwards.
- **It cannot repair a site that already rotated without it.** Those rotations were never recorded
  locally, and no automatic rule can reconstruct them without asserting successions the author never
  attested.
