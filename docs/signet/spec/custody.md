# Custody — an operator's actors, and what it says about them

*for implementers, including anyone writing a second implementation*

*For* [Reviewers](../../README.md#reviewing-the-security-and-identity-design) · [Developers](../../README.md#building-on-polis) — *About* [Identity](../../general/README.md#identity) — *Kind* [Spec](../../README.md#kinds-of-page) — *Component* [Hosted service](../../ops/README.md) — *Code* [`cli-go/pkg/actor`](../../../cli-go/pkg/actor) — *See also* [concept](../../general/concepts/actors.md) · [recipe](../recipes/07-check-an-actor.md)

**Lives in:** `content/pub.polis.core/actor/registry.json` · **Discovered by:** `.well-known/polis` → `actor_registry` · **Content type:** `pub.polis.actor`

*§1–§11 are the **actor** half — an operator's own actors. §12 is the **tenant** half — an operator
holding a tenant's key. Same page on purpose: both are custody, and neither is revocable.*

---

⛔ **This page is about CUSTODY, not delegation. They are opposites and share a shape.**

| | **Custody** — this page | **Delegation** |
|---|---|---|
| Who holds the key | ⛔ **the operator, by definition** | the principal grants; the agent acts |
| Is there a principal who could refuse? | ⛔ **no** | ✅ **yes — that is the point** |
| Revocable | ⛔ **no** | ✅ **yes** |
| Enforceable | ⛔ **no — the custodian holds the key** | ✅ **yes** |
| Right noun | **expected actions** | a grant, a leash |

A page whose title asserts revocability would be describing the other half. **Nothing here is
revocable, and nothing here is enforced.**

---

## 1 The problem it solves

An operator runs software that acts on the network. Polis calls these **actors**: Judge verifies
sites, Patrol and Medic sweep for damage and Medic maintains caches. Some of them sign things.

**Without a published record, anyone can stand up a lookalike.** Register `judge.example`, sign an
attestation, and to a reader it is indistinguishable from the real one — the signature verifies
against the key that domain publishes, which is exactly what a signature is supposed to prove. The
missing fact is not cryptographic. It is *"who says this domain is one of ours?"*

An **actor registry** is that fact, signed by the party accountable for it.

## 2 ⛔ Expected, never allowed

**The registry is not an allow-list and nothing enforces it.**

The operator holds its actors' private keys — that is what makes them its actors — so no published
list can stop one doing anything. What the list buys is that **deviation is OBSERVABLE**:

| Published claim | Can the community disprove it? |
|---|---|
| *"These are our actors"* | ✅ **Yes.** An action signed by a key belonging to no listed domain is caught by one record |
| *"This actor's expected actions are X and Y"* | ⚠️ **Observably deviated from, not prevented.** An action outside the list is INFORMATION, not a violation the protocol blocks |

The remedy for the second row is **social** — remediation, apology, reputation — and saying so
plainly is the design, not a caveat on it. ⛔ **An allow-list would imply an enforcement that does not
exist, which is worse than nothing: it looks like a control.**

⚠️ **Two honest limits.** A canary nobody watches is indistinguishable from one that cannot trip, so
trip it once on purpose. And at the time of writing the community that would watch is prospective
rather than actual — the mechanism is real; the watchers are not yet.

⭐ **The consent mechanism is exit.** If that tradeoff is unacceptable to someone, they can
self-host — which is why **a self-hoster running their own actors is an operator** and publishes
their own registry, with the same file, the same command and the same checks. There is no
hosted-only path anywhere in this design.

## 3 The shape

```json
{
  "v": "pub.polis.actor-registry.v1",
  "operator": "polis.polis.pub",
  "actors": [
    {
      "domain": "judge.polis.pub",
      "authority": "operator",
      "expected_actions": ["pub.polis.attestation.integrity"],
      "countersignature": "-----BEGIN SSH SIGNATURE-----\n…\n-----END SSH SIGNATURE-----\n"
    }
  ],
  "asserted": "2026-09-05T14:02:00Z",
  "generator": "polis-cli-go/0.67.0",
  "signature": "-----BEGIN SSH SIGNATURE-----\n…\n-----END SSH SIGNATURE-----\n"
}
```

### `operator`

⛔ **The SITE ORIGIN, not the party name.** A verifier *fetches* it:
`https://<operator>/.well-known/polis` is where the key that signed this file is published. Naming a
parent brand there points a verifier at a document that does not carry the signing key.

⚠️ The operator's **site** and the operator as a **party** are different things and are easy to
conflate in prose. On the reference deployment the party is *polis.pub* and the site is
`polis.polis.pub`.

### `actors[].domain`

⭐ **A DOMAIN, not a public key.** Domains survive rotation; keys do not — put a key in an entry and
rotating that actor's key staleds the entry until the registry is rewritten *and re-countersigned*.
The actor's own `.well-known/polis` carries its current key, at the cost of one extra fetch.

### `actors[].authority` — `operator` | `user`

**Whose authority the actor exercises**, and these two values are not new vocabulary. They name the
already-published authority rule:

> An actor may perform a **user-authority** action only under a tenant's grant. **Operator-authority**
> actions need none.

⚠️ There is no third value. In particular there is no `handle`: a third word for a concept that
already has two published ones is drift.

### `actors[].expected_actions`

Fully-qualified action types the operator expects this actor to perform. ⛔ **Descriptive, never
permissive.** An empty list is `[]` — *"we expect this actor to do nothing"* is a statable claim and
needs one byte sequence.

### ⚠️ There is deliberately no `actor_type`

A type would be a summary of `expected_actions`, and summaries drift from what they summarise.
`authority` is the axis that carries information a summary cannot.

## 4 The countersignature — and the asymmetry is the point

An entry MAY carry a signature made by **that actor's own key**. The two states make visibly
different claims:

| Entry state | What it proves |
|---|---|
| operator-signed only | *"we say this is ours"* — enough to catch a lookalike |
| ⭐ **+ countersigned** | *"and the actor agrees it is listed, and agrees to this scope"* — the actor cannot later disown it |

⭐ **So an operator CANNOT unilaterally widen what it claims an actor does.** Change an actor's
`expected_actions` and its countersignature stops verifying until the actor re-signs. That protects
the reader from the one thing they most need protecting from.

| Operation | Countersignature required? |
|---|---|
| **Add** an actor · **widen** its expected actions | ✅ **yes** — the actor must consent to what is claimed of it |
| **Remove** an actor · **narrow** it | ⛔ **NO.** A decommissioned or compromised actor cannot sign, and you must always be able to disown one |

The exact bytes a countersignature covers are in
[`signing-base.md` §5.3](signing-base.md). Two properties of that definition are load-bearing:

- ⛔ **It binds the OPERATOR.** Without `operator` inside the signed bytes the countersignature is
  **replayable**: a hostile operator lists the same actor in *its* registry, pastes the signature
  across, and it verifies — so the file says the actor agreed to be theirs. *"The actor agrees it is
  listed"* is meaningless without *"listed by whom"*.
- ⚠️ **It excludes `asserted` and `generator`.** An actor consents to what is claimed **about it**,
  not to when the operator last restated the list. Including either would break every
  countersignature on every unrelated re-statement.

## 5 Three artifacts, one fact, three lifetimes

Registering an actor produces three things, and they are not redundant:

| Artifact | Lifetime | Job |
|---|---|---|
| **The file** on the operator's site | current state | ⭐ *what is true NOW* — **one `curl`** |
| **`pub.polis.attestation.agent-disclosure`**, one per actor | ⭐ **permanent** | *what was claimed, and when* — survives the discovery service's 90-day prune |
| **`pub.polis.actor.registered`** in the event stream | ⚠️ **90 days** | ⭐ **DELIVERY** — it tells people who were not looking |

⭐ **This is `current` + `history` again**, the `public_key` / `public_key_history` shape applied to a
different fact. And it is Law 1: signed → portable and permanent; a delivery mechanism → disposable.

**Two rules that keep the third artifact honest:**

1. ⛔ **THE EVENT IS NOT AUTHORITATIVE.** Its payload carries `target_domain` and a `registry_url`
   **pointer**, never the entry's contents. A consumer that trusts the announcement instead of
   fetching the file has made the discovery service a **gate**; it is a **witness**. The event says
   *"go look"*. The file says what is true.
2. ⛔ **A REMOVAL MUST ISSUE A WITHDRAWAL.** Deleting an entry without a
   `pub.polis.attestation.withdrawal` leaves a live disclosure for an actor the file no longer
   lists — so every removal would read as drift forever, and a checker could not tell a retirement
   from a tampered file.

**Event types.** `pub.polis.actor.registered` / `.reregistered` / `.withdrawn`, mirroring
`site.registered` / `site.reregistered`.

⚠️ **The stream event's `actor` column is ALWAYS the OPERATOR**, never the actor being registered,
because the claim is the operator's. The subject is `payload.target_domain`. ⛔ **The obvious later
"fix" of putting the registered actor in the `actor` column is a correctness bug**: the discovery
service verifies the signature against the named domain's published key, so such an event is rejected.

⚠️ **`actor`, not `agent`.** There will be two kinds of agent registration — an operator registering
its system actor (this page) and a tenant granting authority to their AI agent (delegation) — and
`agent.registered` is the obvious name for both. `actor` is already polis's word for system actors
specifically. *(The `agent-disclosure` predicate keeps its generic name correctly: as a predicate
meaning "this identity is automated", generic is right.)*

### The disclosure record

`polis actor register` issues one `pub.polis.attestation.agent-disclosure` per actor, from the operator's
site, with the same `asserted` time as the registry it accompanies:

```json
{
  "type": "pub.polis.attestation",
  "issuer": "https://polis.polis.pub",
  "predicate": "pub.polis.attestation.agent-disclosure",
  "subject": { "type": "identity", "id": "https://judge.polis.pub" },
  "payload": {
    "authority": "operator",
    "expected_actions": "pub.polis.attestation.integrity",
    "operator": "polis.polis.pub"
  },
  "asserted": "2026-09-13T02:07:18Z",
  "generator": "polis-cli-go/0.67.0",
  "current_version": "sha256:b185374e…",
  "signature": "-----BEGIN SSH SIGNATURE-----\n…"
}
```

| Payload key | Present | Value |
|---|---|---|
| `authority` | always | the entry's `authority` — `operator` or `user` |
| `operator` | always | the operator's domain, the same value as the registry's `operator` |
| `expected_actions` | **only when the entry lists at least one** | the entry's `expected_actions` **joined with `,`, in registry order, no spaces**. ⛔ **Omitted entirely when the list is empty — never `""`** |

**Why a joined string, not an array.** Attestation payload values are **strings only**
([`attestation.md` §3](attestation.md)) — the restriction removes number and structure canonicalisation
from the signing base. A list therefore has to travel as one string. Expected actions are
fully-qualified dotted event and predicate names, which contain no `,`, so **a reader splits on `,`.**

- **`issuer`** is the operator's base URL; **`subject`** is `identity`, `https://<actor domain>`.
- ⚠️ **The record is history, the file is current state.** A later `register` issues a **new**
  disclosure rather than editing one. The newest non-withdrawn disclosure for an actor should match its
  current entry; a mismatch is drift (rule 2 above).
- **Removing an actor withdraws its disclosure** — `polis actor withdraw` issues the withdrawal, which
  is what keeps a retirement distinguishable from a tampered file.

## 6 Discovery — by pointer, never by convention

`.well-known/polis` gains two optional fields:

```json
{
  "actor_registry": "/content/pub.polis.core/actor/registry.json",
  "operator": "polis.polis.pub"
}
```

- **`actor_registry`** — on an **operator's** site: where its registry is. ⭐ It points at the
  **signed source**, not the rendered mount: a permanent address points at the bytes that were
  signed, and a mount can be re-rendered or moved. `dir` and `mount` are user-configurable per-type
  declarations, so a hardcoded path would be wrong.
- **`operator`** — on an **actor's** site: the domain of the party accountable for it. ⛔ **A CLAIM,
  not a fact.** A site can name any operator; what makes it mean something is that operator's
  registry listing the domain back. **The check runs in that direction only**, because the operator
  is the one with something to lose.

⭐ **Together they are a two-hop path, and it is why the operator's site does not need to live at a
guessable hostname.** Nobody finds a registry by guessing. They arrive from an actor: a signature
names a domain → that domain's `.well-known/polis` names its operator → the operator's names the
registry.

Both fields are **absent** on an ordinary site, which is nearly all of them. Absent means *runs no
actors*, and it is a defined state rather than a gap.

## 7 ⭐ How a stranger checks — with nothing but `curl` and a signature verifier

**This is the whole point of §2, so it has to be exact.** Nothing below needs the operator's logs,
the discovery service, or any polis software beyond an Ed25519/SSHSIG verifier.

Given an action signed by `judge.example` that you want to evaluate:

1. **`GET https://judge.example/.well-known/polis`** → its `public_key` and its `operator` claim.
   Verify the action's signature against that key. *If it does not verify, stop: the artifact is not
   from this domain, whatever it says.*
2. **`GET https://<operator>/.well-known/polis`** → the operator's `public_key` and its
   `actor_registry` pointer. *If the site publishes no pointer, the claimed operator publishes no
   registry — the actor's claim is uncorroborated.*
3. **`GET https://<operator><actor_registry>`** → the registry. Rebuild its signing base
   ([`signing-base.md` §5.3](signing-base.md)) and **verify it against step 2's key** — or, if the
   operator has rotated, against the key its published `public_key_history` says was current at the
   registry's `asserted` time ([`key-history.md`](key-history.md)); say that it was a retired key.
   *If it does not verify, the file is not the operator's statement and nothing in it counts.*
4. **Find the entry whose `domain` is `judge.example`.**
   ⛔ **No entry → a LOOKALIKE.** The operator does not claim this domain. **This is the case one
   record closes, and it is closed completely.**
5. **If the entry carries a `countersignature`**, verify it against step 1's key over the entry base.
   *Valid → the actor agreed to this scope. Absent → a weaker claim, not a broken one. Invalid → the
   operator has widened the entry since the actor consented, and that is worth saying out loud.*
   ⚠️ **If the actor has rotated,** a countersignature made under its old key verifies against a
   **retired** key in the actor's published history. The entry base carries no signing time, so
   nothing places it inside that key's window: treat it as **weaker**, exactly like an absent
   countersignature — genuine consent under a replaced key, never a finding and never a full pass.
6. **Compare the action's type against `expected_actions`.**
   ⚠️ **Outside the list → a DEVIATION.** Report it, ask about it, publish it. **It is not a
   violation the protocol blocked, and nothing here should be described as though it were.**

⭐ **Steps 4 and 6 are different instruments and an evaluator should not confuse them.** Step 4 is
binary and conclusive. Step 6 is evidence.

**With the polis CLI** the six steps are one command, run from any machine with no site and no key:
`polis actor verify <action-url-or-file> [--operator <domain>]`. It reports each step and names the
**lookalike**, **countersignature** and **deviation** findings separately
([JSON contract](../../cli/user/json-mode.md#polis-actor-verify-action)). A step verified by a **retired**
key (steps 1, 3 and 5 above) carries a machine-readable `key_used` beside its sentence, and a step verified
by a current key carries none — so the two `weaker` outcomes at step 5 (no countersignature, or one under a
key the actor has replaced) are distinguishable without reading prose. Two cases the steps above
leave implicit, and how it handles them:

- **The signer names no operator.** Nothing in its own document points anywhere, so there is no
  registry to consult — unless you name one. `--operator <domain>` runs steps 2–6 against that
  operator, which is how a lookalike that simply **omits** the claim is caught.
- **The signer publishes its own `actor_registry`.** It is an operator, and an operator that signs
  things lists itself. Its site carries no `operator` field (§6 defines that field for actor sites),
  so the check consults the signer's own registry.

## 8 Which actors publish, and which do not

⚠️ **A signed claim is only worth making if someone can check it.** Applying that test:

| Claim | Disprovable by a **stranger** | Disprovable by the **subject** | Instrument |
|---|---|---|---|
| **Judge** — *this site's signed artifacts verify* | ✅ | ✅ | ⭐ **a canary** |
| **Patrol** — *no unauthorized changes* | ⛔ | ⚠️ partly — the tenant knows what she did | a commitment |
| **Medic** — *0 quarantined files* | ⛔ | ⛔ quarantine is invisible to the tenant | ⛔ a bare assertion |

⭐ **This is a TEST, not a ban.** A signed claim by an identified party creates accountability even
when unfalsifiable: if an operator signs *"no unauthorized changes"* and evidence later contradicts
it, the operator is on record. That is real stake. But a design proposing a new published claim should
**say which instrument it is building**, because a bare assertion presented as a canary is the
*"looks like assurance while providing none"* failure.

⚠️ On the reference deployment only the **operator** and **Judge** have sites. Patrol, Medic, Clerk,
Chaplain and Reaper report to operators, and an operator log is not a site. **Do not build one for an
actor with nothing to say.**

## 9 What actors do with the registry

Three passes on the reference deployment read it, each **before it acts on a site**: Medic's cache
upkeep (it skips actor sites), the pass that issues default Rosie grants (it issues none on an actor
site), and the custody declaration pass (it finds the one operator whose registry it trusts, and
refuses when there are none or several). Patrol, Judge, Clerk and Chaplain do not read it: they check
an actor site like any other, and Judge skips only itself, by handle. Where it is read, this is
defence in depth rather than the primary control: the operator holds every key, so it stops
mistakes, not an operator who has decided to misbehave.

⛔ **Those passes read it from LOCAL DISK, never over HTTP.** Three reasons, each disqualifying:

1. A network dependency inside sweeps that today run entirely off local state.
2. An unreachable operator would force a fail-open/fail-closed choice and **both answers are bad** —
   fail-open operates on actor sites, fail-closed stops the fleet.
3. ⛔ **It is circular.** The operator's site is itself swept, so judging it would need the registry
   *from* it.

⭐ **The artifact the world fetches over HTTPS and the one an actor reads locally are ONE FILE.** Two
artifacts kept in step is a drift problem you then have to build detection for; with one, drift is
impossible rather than detectable.

⭐ **The defence in depth is the SIGNATURE CHECK, not the file's location.** The registry sits in a
directory the operator's own maintenance software writes to, so an actor must not trust the bytes
merely because they are on its disk. It verifies against the operator site's published key — also
local, so verification needs no network either.

**Three states, all handled explicitly:**

| State | Behaviour |
|---|---|
| **Bootstrap** — no registry | ⚠️ **fail-open but LOUD.** Nothing is excluded, which is what happens with no registry anyway — ⛔ **but an actor that silently proceeds because the registry is missing is the same failure as one that never checked** |
| **Deletion** — pointer without a file | reported explicitly. Otherwise the checks quietly stop and look exactly like a fleet with no actors |
| **Tampering** | ⭐ caught by the signature check — which is why that step is not optional |

⛔ **An untrusted registry excludes NOTHING, and the direction matters.** Entries make actors
invisible to some actors' sweeps, so trusting a bad signature would let anyone who can write one file
**hide a real tenant**.

## 10 Conformance checklist

An implementation handles custody correctly when it can:

- [ ] Fetch and verify a registry from `.well-known/polis` → `actor_registry` alone.
- [ ] Reproduce the registry and entry signing bases in [`signing-base.md` §5.3](signing-base.md),
      byte for byte.
- [ ] Reject a countersignature lifted from another operator's registry.
- [ ] Treat a missing entry (a lookalike) and an out-of-list action (a deviation) as **different**
      findings.
- [ ] Report an absent countersignature as a weaker claim, never as a failure.
- [ ] Refuse to trust a registry whose signature does not verify — and exclude nothing on the
      strength of it.
- [ ] Publish a withdrawal when removing an actor.
- [ ] Read an `agent-disclosure` payload per §5 — `authority`, `operator`, and `expected_actions` as a
      comma-joined list that is **omitted when empty**.
- [ ] Verify a `custody` declaration against the **operator's** key and a `custody-grant` against the
      **tenant's** — with key history, so a record signed before a rotation still verifies (§12.2).
- [ ] Accept `attribution: as-tenant` as a legal value, never only `co-signed` (§12.1).
- [ ] Follow `withdrawn_by` as discovery, verify the withdrawal it names, and discard a pointer that does
      not verify — and still look for a withdrawal when the pointer is absent (§12.3).
- [ ] Record, never reject, on `authority`; keep `unknown` distinct from `none`; never spell `none`
      as *unauthorised* (§12.4).
- [ ] Never treat a missing declaration or grant as a finding (§12.6).

## 11 Reference implementation

| Piece | Where |
|---|---|
| Registry type, signing, countersigning | `cli-go/pkg/actor/registry.go` |
| The runtime check actors use | `cli-go/pkg/actor/guard.go` |
| The stranger's check (§7), over HTTPS | `cli-go/pkg/actor/stranger.go` (`polis actor verify <action>`) |
| Key projection from a secret store | `cli-go/pkg/actor/projection.go` |
| Commands | `cli-go/pkg/cmd/actor.go` (`polis actor`) |
| Discovery pointers | `cli-go/pkg/site/wellknown.go` |
| Custody predicates, their writer rules, `withdrawn_by` | `cli-go/pkg/attestation/attestation.go` |
| The tenant's check (§12.5) | `cli-go/pkg/actor/custody.go` (`polis actor verify --custody <domain>`) |
| Recording `signed_by` / `principal` / `authority` | `discovery-service/core/stream.ts` (`eventProvenance`) |
| Issuing the operator's declarations on a hosted deployment | `webapp/internal/hosted/custody.go` |

## 12 Custody of a tenant's key — the tenant half

§1–§11 cover the keys an operator holds **for its own actors**. On a hosted deployment it also holds
**its tenants'** keys — that is what hosting is, and every place a tenant's key is used today signs
**as the tenant**: a post published from the browser, and the repairs the operator's software makes.
A signature made that way is byte-identical to one the tenant made herself. **Nothing on the wire can
tell them apart, and nothing ever will.**

⭐ **One exception, and it is a mark rather than a key: user agents.** Rosie, polis's user agent, also signs
with the tenant's key, so her acts are `as-tenant` too — but she acts only under the tenant's own
**grant**, and every act carries `agent` + `grant` **inside the signed bytes**. Her acts are therefore
distinguishable and traceable; the operator's own upkeep, which carries no mark, is exactly as
indistinguishable as this section says. Delegation is revocable and custody is not, so they live on
different pages: [`delegation.md`](delegation.md).

⛔ **What this section buys is legibility, never less power.** An operator that follows it can do
exactly what it could do before. What changes is that it has **said** it holds the key, in a record it
cannot disclaim, and the one person who knows what she did herself — the tenant — has something to
check it against.

### 12.1 Two records, and they are not redundant

| Record | Predicate | Issuer → subject | Signed with | Can the operator make it for someone else? |
|---|---|---|---|---|
| **declaration** | `pub.polis.attestation.custody` | operator → tenant | ⭐ **the operator's own key** | ⛔ **no** |
| **grant** | `pub.polis.attestation.custody-grant` | tenant → operator | the tenant's key *(which the operator holds)* | ⚠️ **yes** |

A declaration alone is a unilateral claim — anyone could declare custody of anyone. A grant alone is
manufacturable, so the operator has committed to nothing. ⭐ **The declaration is the half that
matters: it is the only one the operator cannot later disclaim.**

Both are ordinary [attestations](attestation.md): one subject, one file, one signature, string-valued
payloads, withdrawn by a `withdrawal` record. ⛔ **Custody is ONE concept in two directions**, not two
concepts — and it is not delegation (the table at the top of this page).

**The declaration's payload:**

| Key | Required | Value |
|---|---|---|
| `holds` | ✅ | `identity-key` |
| `attribution` | ✅ | `as-tenant` — the operator signs **as** the tenant · `co-signed` — the operator's own key co-signs what it does |
| `terms` | — | an `https` URL for the terms custody is held under |

⚠️ **`as-tenant` must stay legal.** An operator that signs as the tenant **and says so** is being
honest about the worse behaviour. If `co-signed` were the only value, that operator would simply not
declare. **Widening the honest path matters more than making the good value the only value.**

⚠️ **An operator whose actors have identities may still be `as-tenant`.** On the reference deployment
Judge signs its observations with its **own** key — but Judge never uses a tenant's key, so that says
nothing about this record. `attribution` is about what happens **when the tenant's key is used**.

```json
{ "type": "pub.polis.attestation",
  "issuer": "https://polis.polis.pub",
  "predicate": "pub.polis.attestation.custody",
  "subject": { "type": "identity", "id": "https://alice.polis.pub" },
  "payload": { "attribution": "as-tenant", "holds": "identity-key" },
  "asserted": "2026-09-14T12:00:00Z",
  "generator": "polis-cli-go/0.67.0",
  "current_version": "sha256:…",
  "signature": "-----BEGIN SSH SIGNATURE-----\n…" }
```

⚠️ **The issuer is the operator's SITE** (`polis.polis.pub`), whose `.well-known/polis` publishes the
signing key — never a brand apex that has no key (§3, `operator`).

**The grant's payload:**

| Key | Required | Value |
|---|---|---|
| `scope` | ✅ | `custodial` — the only value |
| `basis` | ✅ | `user-signed` — a present person signed it · `hosting-terms` — the operator issued it under its hosting terms |
| `terms` | — | an `https` URL |

- **`scope: custodial`, and no list.** Custody authorises everything the key can do, and the record says
  that rather than implying a set of tasks it does not have. Enumerating tasks would be a fiction of
  per-action consent — and granular revocation, its only benefit, is decorative while the operator
  holds the key anyway.
- ⭐ **`basis` is provenance.** Without it a grant the operator issued is indistinguishable from one a
  person signed, and silently claims a consent it does not carry. An operator **can** lie here — but
  then it has made a **false signed claim**, which is a different and more serious thing than
  exercising authority it holds.

⛔ **No field has a default, on either record.** A polis writer refuses a declaration without
`attribution` or a grant without `basis`. Like every writer rule these constrain what is **written**:
a reader still verifies and renders a record that breaks them ([`attestation.md` §7](attestation.md#7--tolerance-what-a-reader-must-do-with-something-it-does-not-recognise)).

### 12.2 Where they live and how they are found

Each is published at its issuer's attestation content path and listed in the issuer's `index.jsonl`,
found **by pointer**: the index sits beside the bundle manifest `.well-known/polis` names
([recipe 4](../recipes/04-list-attestations.md)). To find the declaration about a tenant, read the
**operator's** index; to find a tenant's grants, read the **tenant's**.

Verify each against its **issuer's** published key **with key history**
([`key-history.md`](key-history.md)). ⛔ **Without history, every grant and declaration silently stops
verifying at the issuer's next key rotation.**

### 12.3 Withdrawal, and the forward reference

Either record is withdrawn like any attestation: the issuer publishes a `withdrawal` naming it, and the
record itself stays published and valid. **A withdrawal is prospective** — what was declared or granted
stays declared or granted for the time before it.

⭐ **A withdrawn record also gains `withdrawn_by`** — an unsigned pointer to its withdrawal
([`attestation.md` §6](attestation.md#6-a-subject-may-be-another-attestation)). It exists because a
reader holding one grant has no database to search. **It is discovery, not proof:** follow it, verify
the withdrawal against the issuer's key, and check it names this record's URL and `current_version`.
⚠️ **Its absence proves nothing** — whoever can write the file can remove it, and that is the party
holding the key — so a reader that needs to know still looks through the issuer's withdrawals.

### 12.4 What a discovery service records

A discovery service that follows this section records **three facts** on every event it accepts:

```
signed_by:  the domain whose published key the event's signature verified against
principal:  the party the event is on behalf of
authority:  self | <custody grant URL> | none | withdrawn | unknown
```

| `authority` | Means |
|---|---|
| `self` | the principal's own key signed it. ⚠️ **Under custody this includes whatever the operator signed as the tenant** |
| a grant URL | someone else signed, and a registered, non-withdrawn grant from the principal names them |
| `none` | someone else signed, and **no grant was found** |
| `withdrawn` | the grant found has been withdrawn |
| `unknown` | the service **could not look** |

- ⛔ **ACCEPT AND RECORD. NEVER REJECT.** A missing grant is a fact, never a verdict — rejecting would
  make the service an enforcer where it is a witness. ⛔ **`none` is never spelled *unauthorised*.**
- ⚠️ **`unknown` is not `none`.** Having failed to look is not having looked and found nothing.
- ⛔ **The row is immutable.** It records what was true when the event was accepted; a later
  withdrawal never rewrites it.
- **The grant is looked up from the service's own registration rows** — a grant's signature was checked
  when it was registered, and a withdrawal is itself a registered row. ⚠️ A grant URL here means only
  *"a registered grant names this signer"*; **verifying a delegated event's full chain is a separate,
  later step** and is not part of this section.
- An event stored before a service recorded these fields carries none of them: **absent means *not
  recorded***, never `self`.
- ⚠️ **Auto-blessing — historical only.** The reference service once auto-blessed comments under the
  post author's published policy, signing the decision with its own key (`auto_blessed: true` and a
  `ds_attestation` in the `pub.polis.comment.blessing.granted` payload). It no longer does: a
  discovery service records the request and **never decides a blessing for a user**, and every
  `blessing.granted` event is now signed by the post author — `authority: self`, including the ones a
  user agent decides ([delegation](delegation.md) §7). Older auto-bless events carry no provenance
  fields at all (*not recorded*), and the service never wrote an `authority: none` row for one.

A service emits a `source: security` log event when it records `withdrawn`, and not for `none`, which
is the ordinary case for a long time.

### 12.5 ⭐ How the tenant checks — and what is honestly independent

**The tenant is the detector.** No third party knows which of her records she made herself; she does.
So the check hands her the operator's declaration and her own records side by side, and stops.

With the polis CLI, from **her own machine**, needing no site and no key:

```
polis actor verify --custody alice.polis.pub [--operator polis.polis.pub] [--ds https://ds.polis.pub]
```

It answers three plain questions:

| Question | From | Verified against |
|---|---|---|
| **What does this helper do?** | the operator's `custody` declarations about her | ⭐ **the operator's key** |
| **What did I allow?** | her own `custody-grant` records, withdrawals followed | ⭐ **her key** |
| **Who did this?** | her events at a discovery service, with `signed_by`, `authority` and a user agent's marker (`agent`, `grant`), so an act her agent signed with her key is not counted as hers | ⚠️ **nothing — the service's own recording** |

If the operator declares `as-tenant`, anything signed with her key may be hers or the operator's, and
only she knows which. If it declares `co-signed`, **something signed only with her key that she did not
do herself contradicts the declaration** — and that contradiction is the whole point of having one.

⛔ **State the independence exactly, never more.** On a hosted deployment the operator runs the site
**and** may run the discovery service, so every byte this reads could be **withheld**. What it cannot
do is **forge** a declaration or grant that passes, because each is checked against a key fetched from
the site that signed it. **The verification is independent; the data source is not.** And the event
list is not re-verified at all.

⚠️ **A discovery service's stream holds recent events** (about 90 days on the reference service), so
*"Who did this?"* is a recent window, not a full history.

The check compares nothing automatically: its only **findings** are records whose signatures do not
verify. **Absence, a withdrawn grant and an unreachable service are notes, never findings.**

### 12.6 ⛔ What this section must never be used to say

- **Never treat a missing declaration as a finding.** A self-hoster holds her own key and has nothing
  to declare, and from outside that is indistinguishable from an operator that declares nothing.
  ⚠️ **That also makes silence camouflage** for an operator avoiding the obligation — which is why the
  answer stays *uninformative*, not *suspicious*.
- **Never infer custody from hosting topology.** A shared IP, a certificate or a CNAME is inference, not
  proof, and building on it rewards hiding behind a CDN.
- **Never describe a grant as consent.** Under custody the operator holds the key that signs it; `basis`
  says how it was obtained, and a declaration is evidence of what someone **said**, never of what is
  true.
- **Never describe any of it as constraining an operator.** It makes custody visible. It does not
  reduce it. The only thing that reduces custody is the tenant holding her own key.

## See also

- [`signing-base.md`](signing-base.md) — the exact bytes, §5.3
- [`attestation.md`](attestation.md) — `agent-disclosure` and `withdrawal`
- [`../concepts/identity.md`](../concepts/identity.md) — why an actor has an identity at all
- [`../../general/concepts/actors.md`](../../general/concepts/actors.md) — the actors, and what each does
