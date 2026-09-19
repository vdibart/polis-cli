# Delegation — user agents, the grant and the marker

*For* [Reviewers](../../README.md#reviewing-the-security-and-identity-design) · [Developers](../../README.md#building-on-polis) — *About* [Identity](../../general/README.md#identity) · [Relationships](../../general/README.md#relationships) — *Kind* [Spec](../../README.md#kinds-of-page) — *Code* [`cli-go/pkg/agent`](../../../cli-go/pkg/agent) — *See also* [concept](../concepts/delegation.md) · [guide](../guides/who-blessed-this.md)

Implemented for one agent, Rosie. The grant, the writer rule and live-grant resolution are in
`cli-go/pkg/attestation/grant.go`; Rosie is `cli-go/pkg/agent/`; the marker is carried by
`cli-go/pkg/discovery/client.go` and `cli-go/pkg/metadata/blessed.go`.

> ⭐ **A user agent is software that works for a user. It acts under the user's own record of what it is
> to her, and marks every act — inside the signature — with its name and that record's URL.**

This page is for someone building a second implementation, or checking what one did. It specifies
**formats**, which any provider can reproduce. The code in polis is one agent's alone.

⛔ **This is not custody.** [`custody.md`](custody.md) describes an operator holding a user's key, where
nothing is revocable and nothing is enumerated. Delegation is revocable, names what it covers, and is
issued by the user. The two can hold at once — on polis.pub they do — and they never describe each other.

---

## 1 The framework: one set of artifacts, two signing modes

| Artifact | What it is | Who can produce it |
|---|---|---|
| **The grant** | the user's signed record of what an agent is to her, on her site (§3) | any user, for any agent, from any provider |
| **The marker** | `agent` + `grant` inside the signed bytes of every act (§5) | any agent implementation |
| **The projection** | `agents.json` and a page, generated from the grants (§6) | any site |

| | **Mode A — the agent runs where the user's key is** | **Mode B — the agent runs elsewhere** |
|---|---|---|
| Example | Rosie on polis.pub; Rosie on a user's own instance | an agent another operator runs; a shaper |
| Signs with | **the user's key**, marker inside the signed bytes | the agent's own key, naming the principal |
| What carries the authority | the user's signature; the grant is the agent software's gate and the public record | the grant, checked by anyone |
| Status | ✅ **built** (Rosie) | ⛔ **specified here, not built** — no consumer yet |

⭐ **The artifacts are the same in both modes; only the signing key differs.** Mode B adds the agent's
identity and key to its grant, and an act's `authority` becomes the grant URL rather than `self`.

## 2 Rosie

polis's first user agent. Mode A.

| | |
|---|---|
| `agent` | `rosie` |
| `provider` | `polis.pub` |
| behaviour set | `rosie/1` — **comment blessing: grant and deny, under the user's own rules** |
| Where she runs | inside the user's own server sync (the web app: localhost, `serve`, hosted). ⚠️ A CLI-only site has no Rosie, whatever its grants say |
| Key, site, registry entry | **none**. She is not an operator actor |

⚠️ **The revocation test decides what is an agent's.** An act belongs to Rosie only if a user would expect
switching her off to stop it. Site upkeep — re-registration, DM keyring provisioning, cache repair — is
**not** hers; it is operator custody and carries no marker.

## 3 The grant record

An ordinary attestation ([`attestation.md`](attestation.md)) with predicate
**`pub.polis.attestation.grant`**.

```json
{
  "type": "pub.polis.attestation",
  "issuer": "https://alice.polis.pub",
  "predicate": "pub.polis.attestation.grant",
  "subject": { "type": "identity", "id": "https://alice.polis.pub" },
  "payload": {
    "agent": "rosie",
    "basis": "hosting-terms",
    "behaviours": "rosie/1",
    "provider": "polis.pub"
  },
  "asserted": "2026-10-01T12:00:00Z",
  "generator": "polis-cli-go/0.67.0",
  "current_version": "sha256:…",
  "signature": "-----BEGIN SSH SIGNATURE-----\n…"
}
```

| Key | Required | Meaning |
|---|---|---|
| `agent` | ✅ | the user's name for the agent. Meaningful only with this record; nothing about it is global |
| `provider` | ✅ | who supplies the software. **A claim, never authority** |
| `behaviours` | ✅ | a versioned behaviour set, `<name>/<n>` (`n` ≥ 1). The provider documents each set |
| `basis` | ✅ | `hosting-terms` — the hosting operator issued it under its terms · `user-signed` — a present person switched the agent on. The values are [`custody.md` §12](custody.md)'s |
| `terms` | — | an https URL |

**Writer rule** (what an implementation must refuse to write; readers accept anything, per
[`attestation.md`](attestation.md)):
- subject type `identity`, and subject id **equal to the issuer** (trailing `/` ignored) — a user grants
  an agent for herself;
- `agent`, `provider`, `behaviours`, `basis` present — **no defaults**;
- `behaviours` matches `^[a-z0-9][a-z0-9-]*/[1-9][0-9]*$`;
- `basis` is one of the two values; `terms`, if present, is https.

**Signing and addressing** are the attestation family's: canonical bytes in
[`signing-base.md`](signing-base.md), the record's URL is its content path. **That URL is what every act
cites.**

**Registration.** A grant, and its withdrawal, are registered with the discovery service **at the moment
they are made**. Registration failing does not undo the record: the record on the site is the record.

### 3.1 Withdrawal

Switching an agent off is the existing **`pub.polis.attestation.withdrawal`** of each standing grant for
that agent, issued by the user, with the forward pointer `withdrawn_by` added to the withdrawn record
([`custody.md` §12.3](custody.md)). Nothing is deleted.

### 3.2 The live grant — what agent software checks before every act

A grant is **live** for an agent and an act when **all** of these hold:
1. it is a grant on the user's own site, subject = issuer, naming that `agent`;
2. its signature **verifies** against the site's published key — resolving a retired key through the site's
   [key history](key-history.md) for a grant signed before a rotation;
3. its behaviour set covers the act — for Rosie's blessing, set `rosie` at any version ≥ 1;
4. **no withdrawal on the site names it** — neither a withdrawal record whose subject is its URL, nor a
   `withdrawn_by` pointer (an unresolvable pointer still counts).

The **highest version** among live grants is the one an act cites. ⛔ **Fail closed:** anything that cannot
be established reads as "no live grant", because the only question asked is whether the agent may act.

## 4 Consent, defaults and versioning

| Flow | Record written | Basis |
|---|---|---|
| A hosting operator's default, for a new or existing user | a grant for the current version | `hosting-terms` |
| The user switches the agent on | a grant for the current version, unless a live one at that version stands | `user-signed` |
| The user switches the agent off | a withdrawal of **every** standing grant for the agent | — |
| A new behaviour version | a new `hosting-terms` grant, to users with a standing grant and **no withdrawal ever** | `hosting-terms` |

⛔ **The default rule.** At most **one** default grant per (user, agent, version), and **none after any
withdrawal** for the (user, agent) pair. The blocking fact is the records on the user's own site. A user
who switched the agent off stays off — through restarts, through the default pass, and through every new
version. A (user, agent) pair is independent of every other pair.

- **A new version** is a new behaviour, or one that materially changes what the agent decides. Bug fixes do
  not bump it. The older grant stays standing: no operator writes a withdrawal in the user's name.
- **Actor sites** (an operator's own site and the sites its registry lists) receive no default: an actor is
  not a user.
- ⚠️ **`hosting-terms` is not consent.** It is a record in the user's name that the operator issued,
  labelled as such. No demonstration, dashboard or document may cite one as consent.

### 4.1 polis.pub

- **On by default, for existing and new tenants**, behind the operator switch `POLIS_ROSIE` (off unless set).
  While off, no default is written and Rosie acts for nobody; a user's own choice is still saved.
- **Existing tenants are opted in once**, by the first boot with the switch on. Every later boot finds the
  grants standing.
- **No prompt at signup.** Settings → Rosie is where a person meets the choice.

### 4.2 Self-hosted

The same code, and **no default**. An instance with no grant makes **no** auto-decisions until its owner
switches Rosie on (`user-signed`). Interactive `polis init` asks, nothing pre-selected.

### 4.3 The catch-up pass

When an agent becomes live for a user — the operator switch turns on, or the user switches it on — it makes
**one** pass over requests still pending and decides them exactly as it would a new one. Rosie reads
"pending" from the discovery service, considers requests up to **30 days** old, and decides at most **200**
per pass; everything else, and anything the user's rules hold for review, stays waiting. The pass runs once
per grant.

## 5 The marker

Every act an agent performs carries two fields **inside its signed bytes**:

| Field | Value |
|---|---|
| `agent` | the grant's `agent` |
| `grant` | the live grant's **source URL** — never the projection (§6), never a page |

⛔ **Both are omitted entirely when absent** — never written as `""` — and appended after the existing
fields. So every act a user makes herself, and every signature made before the marker existed, is
byte-identical.

⛔ **Outside the signature, the marker is strippable and worth nothing.** It is inside, on every act type.

### 5.1 A blessing decision — `POST /v1/relationships`

The signed canonical payload is compact JSON, fields in this order:

```
{"type":"pub.polis.comment.blessing","source_url":"https://bob.example/c.md","target_url":"https://alice.example/p.md","action":"deny","timestamp":"2026-09-15T12:00:00Z","agent":"rosie","grant":"https://alice.example/content/pub.polis.core/attestation/x.json"}
```

The request body carries `agent` and `grant` beside the other fields. ⚠️ **A discovery service that predates
the marker rebuilds the payload from the five older fields and rejects a marked request (`401`)**; see §7.

### 5.2 A blessing-list entry — `blessed.json`

A grant decision also adds an entry to the user's signed blessing list; the entry carries the marker, in the
list's signed bytes. Canonical form and golden bytes: [`signing-base.md`](signing-base.md), *The blessing
list*. **A deny writes no entry**; its marker lives in the relationship request.

⚠️ **Every writer of the list must declare both fields.** A writer that rebuilds entries from three fields
drops the markers and then signs the list with the user's key, turning the agent's acts into the user's own.

⭐ **A live entry, and the contrast beside it.** `discover.polis.pub`'s blessing list carries both shapes:
entries decided before this design record `"version": ""` and no marker — neither *which bytes* were
approved nor *who decided* — while an entry decided by an agent carries a version pin **and** the marker.
[The guide](../guides/who-blessed-this.md) reads them side by side with `curl`, and covers what the
marker does **not** tell you.

### 5.3 Acts that are not the agent's carry no marker

| Act | Signer | Marker |
|---|---|---|
| Blessing grant / deny by the agent | the user's key | ✅ |
| The user's own grant / deny | the user's key | — |
| Operator upkeep (re-registration, repairs, DM keyring provisioning) | the user's key, as declared custody | — |
| Operator writes of the blessing list from a projection | none — **unsigned** | — |

⛔ **No operator actor writes a marker, and no agent act lacks one.**

## 6 What an agent is to this site — the projection

Discovery is **by pointer**: `.well-known/polis` → **`agents`**, a site-relative URL.

```json
{ "agents": "/attestations/agents.json" }
```

`agents.json` is generated from the site's grant records:

```json
{
  "type": "pub.polis.agents",
  "note": "Generated from this site's grant records. Cite a grant's own URL, never this file.",
  "agents": [
    {
      "agent": "rosie",
      "provider": "polis.pub",
      "behaviours": "rosie/1",
      "basis": "hosting-terms",
      "grant": "https://alice.polis.pub/content/pub.polis.core/attestation/20261001T120000Z-….json",
      "asserted": "2026-10-01T12:00:00Z",
      "state": "withdrawn",
      "withdrawn_at": "2026-10-09T08:30:00Z",
      "withdrawal": "https://alice.polis.pub/content/pub.polis.core/attestation/20261009T083000Z-….json"
    }
  ]
}
```

`state` is `live`, `withdrawn`, or `unverified` (the record's signature does not verify). A page for people
sits beside it at `<mount>/agents/`.

- ⛔ **A projection, never a source.** Regenerated on every grant or withdrawal write — **the failure branch
  included** — and never cited in a signature.
- The files live under the **attestation type's mount** from the site's bundle (`/attestations` by default),
  so the path is user-configurable and cannot collide with a page at the site root.
- **The pointer is written only when its value changes.** `.well-known/polis` is watched; rewriting an
  identical value would turn every refresh into a finding.
- **A site that never had a grant publishes nothing**: no pointer, no file. Absence is never a finding.

## 7 What a discovery service does

A discovery service **records requests, verifies signatures, and witnesses decisions. It does not decide.**
Registering a comment always yields a **pending** request and a `pub.polis.comment.blessing.requested`
event, and wakes the post author. The post author's own software — the agent, under the post author's
grant — evaluates the rules and signs the decision.

For a marked `POST /v1/relationships`, the discovery service:

- **Accepts** `agent` and `grant` in the signed payload, appended after the five fields only when both are
  present, and verifies the request against the post author's key. An unmarked request's bytes are the five
  fields exactly. A request carrying **one half** of the marker is malformed and refused (`400`).
- **Checks the cited grant** against its own registration rows — a `pub.polis.attestation.grant` at that URL
  issued by the post author, still active, and whether a registered withdrawal names it:
  `live` · `withdrawn` · `not-found` (often a grant still waiting to be registered) · `unknown` (could not
  look). ⛔ **A fact, never a verdict.**
- **Records** `agent`, `grant` and `grant_state` on the `blessing.granted` / `denied` event, and on the
  **witness record** it issues for the decision ([witness](witness.md) §4.1). `signed_by`, `principal` and
  `authority` are unchanged: a Mode A act is `authority: self`.
- ⛔ **Never rejects on `grant_state`.** The relationship updates whatever it says. A `withdrawn` citation is
  logged on `source: security` as `agent_grant_withdrawn`; a `not-found` one on `source: api` as
  `agent_grant_not_found`.
- ⛔ **No per-agent allowlist and no agent configuration** in the discovery service.

⭐ **Enforcement lives in the agent, evidence in the discovery service.** The agent's own code refuses to act
without a live grant (§3.2). The discovery service is the witness that shows, publicly and permanently, when
an act went out anyway.

⚠️ **History.** Before this, a discovery service evaluated the post author's published rules and, on a
match, granted the blessing itself, signing the decision with its own key (`auto_blessed`, `ds_attestation`
on the event). Those records stay on the stream and their attestation still verifies
([witness](witness.md) §3); nothing produces a new one.

⚠️ **Deploy order.** An implementation that sends marked requests must not do so until the discovery service
it talks to accepts the fields: an older one rebuilds the five fields and answers `401`. polis.pub keeps
`POLIS_ROSIE` off until then.

## 8 What this design does NOT claim

- ⛔ **The marker is self-declared.** In Mode A, whoever holds the user's key could sign without it. Marked
  acts are **fully traceable**; unmarked acts are **no more visible than before**.
- ⛔ **`hosting-terms` is not consent.**
- ⛔ **Enforcement is the agent's own code.** It checks the live grant before signing. The discovery service
  witnesses and flags; it does not stop an act.
- ⛔ **Custody is not reduced.** On hosting the operator still holds the user's key, for interactive
  publishing, not because of the agent. Only client-side signing reduces custody, and that is the one change
  that would move an agent to Mode B.

## 9 Conformance checklist

An implementation of a Mode A user agent:
- [ ] writes grants that pass §3's writer rule, and withdraws with the existing withdrawal;
- [ ] resolves the live grant by §3.2 **before every act**, failing closed;
- [ ] signs no act without a live grant, and has no path that signs the same act unmarked;
- [ ] puts `agent` + `grant` inside the signed bytes of every act, omitted when absent;
- [ ] declares the marker fields on every writer that rebuilds a list containing them;
- [ ] never issues a default after a withdrawal for the pair;
- [ ] regenerates the projection on every grant or withdrawal write, including after a failure, and never
      cites it.

## See also

[`attestation.md`](attestation.md) · [`custody.md`](custody.md) §12 · [`signing-base.md`](signing-base.md) ·
[`../concepts/identity.md`](../concepts/identity.md) · Settings → Rosie in the
[user manual](../../webapp/user/user-manual.md)
