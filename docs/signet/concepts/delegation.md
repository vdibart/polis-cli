# Delegation

*For* [Reviewers](../../README.md#reviewing-the-security-and-identity-design) · [Developers](../../README.md#building-on-polis) — *About* [Identity](../../general/README.md#identity) — *Kind* [Concept](../../README.md#kinds-of-page) — *See also* [spec](../spec/delegation.md) · [guide](../guides/who-blessed-this.md)

**Delegation is a user letting software decide some things for them, under a record they can see and
withdraw.** The software is a **user agent**. It works for the user, signs with the user's key, and marks
every act it makes with its name and the record it acted under. polis has one today: Rosie, who decides
blessing requests on a user's posts by the user's own rules, public and private.

This page explains why delegation is shaped this way and what it can and cannot tell a reader. The formats
are in [the delegation spec](../spec/delegation.md); reading a real marked blessing from outside is
[the guide](../guides/who-blessed-this.md).

---

## 1. The problem: a signature names a key, not a decision-maker

A polis signature proves which key signed ([identity](identity.md)). It says nothing about **who decided**.
Once software can sign with your key, a blessing Rosie granted and a blessing you granted by hand are the
same bytes, and a reader cannot tell them apart.

Two things would answer that badly:

- **Give the agent its own key.** Then its approval is not yours, and a reader would have to trust a second
  identity to learn what you admitted onto your own page. The spec describes this as a second mode, for an
  agent that runs away from the user's key. It is not built.
- **Keep a log somewhere else.** A log beside a signature is strippable and proves nothing about the
  signed act.

So polis keeps the user's key as the signer and puts the answer **inside the signed bytes**.

## 2. The three pieces

| Piece | What it is | Why it is shaped this way |
|---|---|---|
| **The grant** | a signed record on the user's own site: which agent, who provides it, which versioned set of behaviours, and how the record came to exist | the authority lives with the user, where anyone can fetch it, and not in a provider's database |
| **The marker** | the agent's name and the grant's URL, inside the signature of every act the agent makes | a reader can answer *who decided* from the act alone, and nobody can remove the answer without breaking the signature |
| **The projection** | a generated summary of the site's grants, and a page for people | convenient to read, and never cited: acts point at the grant itself |

The grant is an ordinary [attestation](assertions.md) with its own predicate
([spec §3](../spec/delegation.md#3-the-grant-record)). Nothing new had to be invented to publish, sign,
register or withdraw it.

## 3. Revocation is the point

**Switching an agent off writes a withdrawal of every standing grant for it.** Nothing is deleted; the grant
and its withdrawal both stay on the site ([spec §3.1](../spec/delegation.md#31-withdrawal)).

Before every act the agent resolves a **live grant**: on the user's own site, naming it, covering the act,
verifying against the site's key, and named by no withdrawal
([spec §3.2](../spec/delegation.md#32-the-live-grant--what-agent-software-checks-before-every-act)). Three
choices make revocation hold:

- ⛔ **It fails closed.** Anything the agent cannot establish, including a withdrawal pointer it cannot
  follow, reads as *no grant*, so it does not act. The only question asked is *may the agent act?*, and the
  safe answer to doubt is no.
- ⛔ **Off stays off.** A hosting operator may issue a default grant, but never after the user has withdrawn
  one for that agent: not on restart, not in the next default pass, not when a new behaviour version ships
  ([spec §4](../spec/delegation.md#4-consent-defaults-and-versioning)).
- **A key rotation does not switch the agent off by accident.** A grant signed before a rotation still
  verifies through the site's [key history](../spec/key-history.md). Without that, rotating a key would
  silently disable the agent, and the user could not tell why.

⭐ **The test for what belongs to an agent is revocation itself.** An act is the agent's only if a user would
expect switching it off to stop that act. Site upkeep such as re-registration and cache repair would not
stop, so it is not the agent's and carries no marker.

## 4. Delegation is not custody

Both involve software signing with a user's key, and they describe different relationships.
[Custody](../spec/custody.md) is an operator holding the key: nothing is revocable, and what the operator
publishes are **expected actions**. Delegation is recorded on the user's own site, names what it covers, and the user can
withdraw it. On polis.pub, once Rosie is switched on for the service, both hold at once, and neither describes the other.

| | Custody | Delegation |
|---|---|---|
| Who holds the authority | the operator | the user, through the grant |
| Revocable by the user | ⛔ no — the remedy is to leave | ✅ yes — withdraw the grant |
| Marked on each act | ⛔ no | ✅ yes |
| Who may originate a statement in the user's name | nobody: an operator actor only restores or completes what the user did | the agent, only within granted behaviours, under a live grant, always marked |

⚠️ **Delegation does not reduce custody.** On a hosted site the operator still holds the user's key, for
publishing, not because of the agent.

## 5. What the marker proves, and what it does not

**A marked act proves** that it was signed with the site's key, that the signer declared it an act of the
named agent, and which grant it cited. A reader can fetch that grant and see its basis, its behaviours and
whether it has since been withdrawn.

**It does not prove:**

- ⛔ **That every agent act is marked.** The marker is self-declared. Whoever holds the key could sign without
  it. Marked acts are fully traceable; unmarked acts are no more visible than they were before.
- ⛔ **That the user consented.** A grant with `basis: hosting-terms` was issued by the hosting operator under
  its terms, and it says so. It is revocable and prospective, and it applies rules the user already
  published. It is neither consent nor terms, and nothing should cite it as either.
- ⛔ **That the agent was stopped from acting.** Enforcement is the agent's own code, checking the live grant
  before it signs. A discovery service records the cited grant's state beside the decision and flags a
  withdrawn one; it never refuses the act ([spec §7](../spec/delegation.md#7-what-a-discovery-service-does)).
- ⛔ **That the decision was right.** Delegation moves the labour, not the responsibility.
- ⛔ **That an unmarked act is suspicious.** No marker means no agent decided, or the software predates the
  marker.

The full list is [spec §8](../spec/delegation.md#8-what-this-design-does-not-claim).

## 6. Why no signature broke

The marker's two fields are **omitted entirely when absent** and appended after every existing field. An act
the user makes themselves, and every signature made before the marker existed, is byte-identical to what it
was. That is the [signing base](../spec/signing-base.md)'s one rule: no existing signature may stop
verifying.

## See also

- [The delegation spec](../spec/delegation.md) — the grant, the live-grant rule, defaults, the marker, the projection
- [Tell whether a blessing was decided by a person or by their agent](../guides/who-blessed-this.md) — the marker on a live site, with `curl`
- [Custody](../spec/custody.md) §12 — the operator holding a tenant's key, and what it declares
- [Identity](identity.md) — the key both custody and delegation sign with
