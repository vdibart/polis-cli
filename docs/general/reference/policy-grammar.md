# Polis Policy Grammar

*For* [Developers](../../README.md#building-on-polis) · [Reviewers](../../README.md#reviewing-the-security-and-identity-design) — *About* [Relationships](../README.md#relationships) — *Kind* [Spec](../../README.md#kinds-of-page) — *Code* [`cli-go/pkg/policy`](../../../cli-go/pkg/policy) — *See also* [concept](in-defense-of-bless.md) · [guide](../../cli/user/policies.md)

Authoritative specification for polis policy rules (v2, current).

This document exists so that the layered model and verb-by-layer matrix do
not have to be re-derived from code. If something in the implementation
disagrees with this document, the implementation is the source of truth for
current behavior — but update this document to match, or change the code
to match this document, before doing more work. The whole point is to have
a single place that answers "which verbs apply where, and why."

## TL;DR

Polis has **three policy layers**. Each layer uses the vocabulary that
matches the kind of decision it makes:

| Layer | Purpose | Verbs | Lives in |
|---|---|---|---|
| **1. Tenant inbound** | Decisions about content that concerns me | `allow`, `deny`, `bless`, `review` | Tenant's `rules.jsonl` files |
| **2. Tenant outbound** | Whether to announce my events to the DS | `emit`, `omit` | Tenant's `rules.jsonl` files |
| **3. DS operator** | Whether to admit an announcement to the DS stream | `allow`, `deny` | DS `ds_operator_policies` table |

Every policy rule anywhere in the system belongs to exactly one layer. The
parser validates that the verb + type combination is legal for the target
layer — invalid combinations fail to parse (tenant files) or are skipped
(operator rows).

> ### ⚠️ Policy is not a licence
>
> These two share a noun and nothing else, and building them as one thing would
> be a mess. **Policy is inbound; a licence is outbound.**
>
> | | Policy (this document) | [Licence](../../signet/spec/license.md) |
> |---|---|---|
> | Direction | others → me | me → the world |
> | Audience | polis peers | any consumer, mostly non-polis |
> | Governs | interaction | use of content |
> | Verbs | `allow` / `deny` / `bless` / `review` | AIPREF `train-ai`/`search`, RSL `ai-input`/`attribution` |
> | Lives in | `policies/rules.jsonl` | `content/pub.polis.core/license/license.json`, signed |
>
> Layer 2's reserved `emit`/`omit` verbs are also **not** a licence: they govern
> whether *this site announces its own events to the DS*, which is a routing
> decision about polis peers. A licence says what a third party may do with the
> work once they have it.

## The three layers, in detail

### Layer 1 — Tenant inbound

Runs on a tenant's site. Decides what to do about content that has arrived
(a DM delivered by a sender) or been announced about me (a comment on my
post, requesting blessing). These are decisions with terminal outcomes, so
the verbs are decision verbs.

| Content type | Valid verbs | What each means | Evaluator |
|---|---|---|---|
| `pub.polis.dm` | `allow`, `deny` | Accept this DM into storage, or reject it | recipient: `Receiver.checkPolicy` in `cli-go/pkg/dm/receive.go`; sender pre-flight: `CheckDMEligibilityURL` in `cli-go/pkg/policycheck/check.go` |
| `pub.polis.comment` | `deny`, `bless`, `review` | Reject the comment; auto-display it alongside my post; queue it for human review | the post author's own site: webapp `internal/server/rosie.go` (`rosieEvaluate`, Rosie); CLI `following/social.go` (`FollowWithBlessing`). ⛔ **Never the discovery service** |

**Why no `allow` for `pub.polis.comment`.** Comments live on the commenter's
site; there is no acceptance/storage step on the recipient's side. The only
decision available is whether to bless (display), review (queue), or deny.
Writing `allow pub.polis.comment from X` in a tenant file is a parse error.

### Layer 2 — Tenant outbound

Runs on a tenant's site when publishing or otherwise producing an event.
Decides whether the event is announced to the DS at all. These are
event-plumbing decisions ("do I tell the DS?"), so the verbs are
event-plumbing verbs.

| Content type | Valid verbs | What each means | Evaluator |
|---|---|---|---|
| Any `pub.polis.*` event the tenant produces | `emit`, `omit` | Announce this event to the DS, or suppress the announcement | **No evaluator wired yet — reserved** |

**Reserved status.** The parser accepts `emit`/`omit` rules in tenant files
and retains them in loaded policy sets, but no production code path
currently consults them. This is a known surface for a future feature
(suppressing DS announcements for privacy-sensitive posts). Canonical
default policy files do not seed blanket `emit`/`omit` rules — defaults
should reflect live behavior. When the evaluator lands, defaults will be
added via the same Medic-upgrade mechanism used for v1 → v2.

### Layer 3 — DS operator ingestion

Runs on the DS. Decides whether an announcement received from a tenant is
admitted into the DS's event stream for indexing and fanout. This is
administrative firewall vocabulary.

| Content type | Valid verbs | What each means | Evaluator |
|---|---|---|---|
| `pub.polis.post` | `allow`, `deny` | Admit post announcement to stream | `isBlocked` in `discovery-service/core/stream.ts` |
| `pub.polis.comment` | `allow`, `deny` | Admit comment announcement to stream | same |
| `pub.polis.follow` | `allow`, `deny` | Admit follow announcement to stream | same |
| `pub.polis.site` | `allow`, `deny` | Admit site-registration announcement to stream | same |
| any other `pub.polis.*` type (tag, attestation, actor, …) | `allow`, `deny` | Admit that announcement to stream | same |
| `pub.polis.comment` | `bless`, `review` | ⚠️ **Parsed and stored, never applied** — see *Why bless/review appear here* | no evaluator |

**Who writes Layer 3 rules.** The DS operator, via `ds_operator_policies`
rows. These rules are not authored by tenants. They must not appear in
tenant `rules.jsonl` files — the tenant-mode parser rejects them.

**Why bless/review appear here, and what they do now.** They are history the
grammar still parses. A discovery service used to fetch the target tenant's
public `rules.jsonl` when a comment announcement arrived, decide the blessing
itself, and fall back to its own seeded blessing rows when that fetch failed.
**It no longer decides a blessing for anyone:** registering a comment records a
pending request and wakes the post author, whose own site applies these verbs
(Layer 1). The seeded `bless`/`review` rows still ship — in the
`ds_operator_policies` seed and in `DS_DEFAULT_POLICIES_CONTENT` /
`core/default-policies.jsonl` — and the operator parser still accepts them, but
**nothing evaluates them**, and no blessing decision carries a
`policy_source` or a `fallback_reason` any more. ⚠️ Layer 3's `allow`/`deny`
ingestion is untouched and live.

## Writable grammar

```
<action> <type> from <source> [at <domain>] [on <target>]
```

**Actions:** `allow`, `deny`, `bless`, `review`, `emit`, `omit` (six total)

**Types:**
- `all` — matches any event type (catch-all)
- `none` — matches nothing (no-op; used to disable rules without deletion)
- `pub.polis.*` — a dotted type prefix. Match is literal with dot-boundary
  semantics: `pub.polis.comment` matches `pub.polis.comment.published` but
  `pub.polis.com` does not match `pub.polis.comment`.

**Sources:**
- `all` — any actor
- `none` — no actor (never matches)
- `self` — the tenant's own domain
- `following` — domains in the tenant's following list
- `followers` — domains in the tenant's followers list
- `thread-blessed` — actors with a comment already blessed on the same thread
  (resolved on the post author's own instance, from its blessing list for the
  thread's root post; the discovery service does not resolve it)
- `<specific-domain>` — exact actor domain (via the `at` clause)

**Optional clauses:**
- `at <domain>` — restrict match to this specific actor domain
- `on <target>` — restrict match to this specific target path (e.g. a post)

## Verb validity matrix

For each rule, the parser checks that its verb + type combination is legal
for the target layer. Invalid combinations fail to parse in tenant files
(returning a specific error) and are skipped in operator policies.

| Rule form | Tenant files | Operator policies |
|---|---|---|
| `allow pub.polis.dm from <scope>` | ✅ live (Layer 1) | ⚠️ parses (Layer 3 accepts `allow`/`deny` for any type); inert — DMs never pass through the DS |
| `deny pub.polis.dm from <scope>` | ✅ live (Layer 1) | ⚠️ parses; inert, as above |
| `bless pub.polis.comment from <scope>` | ✅ live (Layer 1) | ⚠️ parses; **not applied** — the DS decides no blessing for a user |
| `review pub.polis.comment from <scope>` | ✅ live (Layer 1) | ⚠️ parses; **not applied** — the DS decides no blessing for a user |
| `deny pub.polis.comment from <scope>` | ✅ live (Layer 1) | ✅ live (Layer 3) |
| `allow pub.polis.comment from <scope>` | ❌ parse error | ✅ live (Layer 3) |
| `allow pub.polis.post from <scope>` | ❌ parse error | ✅ live (Layer 3) |
| `allow pub.polis.follow from <scope>` | ❌ parse error | ✅ live (Layer 3) |
| `allow pub.polis.site from <scope>` | ❌ parse error | ✅ live (Layer 3) |
| `deny pub.polis.<other type> from <scope>` (post, follow, tag, …) | ✅ parses (defensive deny) | ✅ live (Layer 3) |
| `emit pub.polis.<type> from <scope>` | ✅ reserved (Layer 2, no evaluator) | ❌ |
| `omit pub.polis.<type> from <scope>` | ✅ reserved (Layer 2, no evaluator) | ❌ |
| `bless` / `review` on any non-comment type | ❌ parse error | ❌ parse error |
| `allow all from all`, `deny all from all` | ✅ catch-all (any mode) | ✅ catch-all (any mode) |

## Evaluation semantics

- **First-match-wins.** Rules are evaluated in file order. The first rule
  whose type + source + optional qualifiers match returns its decision.
- **No-match default.** If no rule matches, the decision is `allow` with
  `matched: false`. Consumers distinguish this from an explicit allow via
  the `matched` flag — e.g. the blessing flow treats "no match" as
  implicit pending (manual review required).
- **Catch-all idiom.** A `deny all from all` terminal line makes the
  default posture explicit and defensive for unknown event types.
- **Inactive rules.** Rules with `"active": false` are skipped entirely.

## Legacy grammar

Prior to v2, blessing rules were written as:

```
emit pub.polis.comment.blessing from <scope>
```

This form treated blessing as an event emission. The v2 refactor replaced
these with decision verbs:

```
bless pub.polis.comment from <scope>
```

**Why the parser still accepts the legacy form.** DS-signed blessing
attestations embed the matched `policy_rule` string verbatim in their
canonical JSON. Rewriting those strings would invalidate the signatures.
To avoid re-signing history, the parser accepts the legacy form at read
time and translates it to its v2 equivalent for evaluation purposes. The
raw legacy string is preserved in `EvalResult.rule` so signature
verification continues to succeed byte-exact.

The legacy form is **read-only**. Writers (new policy files, Medic-rewritten
tenant files, seeded operator policies) never emit the legacy form. Only
historical data carries it.

## File format

Tenant policies and DS operator policies both use JSONL with a header line:

```
{"version":2,"generator":"polis-cli-go/0.63.0"}
{"active":true,"policy":"allow pub.polis.dm from following"}
{"active":true,"policy":"deny pub.polis.dm from all"}
{"active":true,"policy":"bless pub.polis.comment from self"}
{"active":true,"policy":"bless pub.polis.comment from following"}
{"active":true,"policy":"bless pub.polis.comment from thread-blessed"}
{"active":true,"policy":"review pub.polis.comment from all"}
{"active":true,"policy":"deny all from all"}
```

**Header.** First line is a JSON object with `version` (integer) and
`generator` (string). Lines without a `policy` field are treated as
metadata and skipped by the evaluator. Current format is `version: 2`.

**One rule per line.** Lines with `"policy"` are parsed as rules. Malformed
JSON lines are silently skipped (operationally this makes partial-write
recovery safer). `"active": false` rules are loaded but not evaluated.

**Paths.**
- Tenant public: `<site>/policies/rules.jsonl` (published; a sender's software fetches it for the DM pre-flight check — the DS does not fetch it)
- Tenant private: `<site>/.polis/policies/rules.jsonl` (not published; higher priority than public at evaluation time)

## Detecting and upgrading v1 files

**Detection.** Patrol reads the header line of each tenant's policy files
and flags any with `version < 2`. Drift events are emitted as
`patrol.policy_format_drift` with `handle`, `path`, `current_version`,
`expected_version` fields.

**Upgrade.** Medic rewrites v1 files with the canonical v2 default content
from `policy.DefaultPublicPolicyContent()` / `DefaultPrivatePolicyContent()`.
⚠️ **The rewrite does not keep a site's own rules.** A site's owner can edit
either rules file, and any file that differs from the default is replaced by
it: by Medic on a hosted tenant, whatever the file's version, and by
`tailor --apply` on a self-hosted site. Back up a customised policy file
before running `tailor --apply`. A translator that carries a site's own rules
across an upgrade is planned.

**Observability.** Medic emits `medic.policy_upgrade` with `handle`, `path`,
and `detail` on every rewrite (alongside the general `medic.provision`). Re-running after upgrade is a no-op.

## Canonical default files

### Tenant public (`policies/rules.jsonl`)

```
{"version":2,"generator":"polis-cli-go/X.Y.Z"}
{"active":true,"policy":"allow pub.polis.dm from following"}
{"active":true,"policy":"deny pub.polis.dm from all"}
{"active":true,"policy":"bless pub.polis.comment from self"}
{"active":true,"policy":"bless pub.polis.comment from following"}
{"active":true,"policy":"bless pub.polis.comment from thread-blessed"}
{"active":true,"policy":"review pub.polis.comment from all"}
{"active":true,"policy":"deny all from all"}
```

Source: `cli-go/pkg/policy/policy.go` `DefaultPublicPolicyContent()`.

### Tenant private (`.polis/policies/rules.jsonl`)

```
{"version":2,"generator":"polis-cli-go/X.Y.Z"}
```

Empty by default (no rules). Private policies are user-specific overrides
that should not be advertised publicly (e.g. silently blocking a domain
via `deny all from all at stalker.polis.pub`). Source:
`cli-go/pkg/policy/policy.go` `DefaultPrivatePolicyContent()`.

### DS operator policies (postgres `ds_operator_policies`)

Seeded on fresh DS install:

```sql
'allow pub.polis.post from all'
'allow pub.polis.comment from all'
'allow pub.polis.follow from all'
'allow pub.polis.site from all'
'bless pub.polis.comment from self'
'bless pub.polis.comment from following'
'bless pub.polis.comment from thread-blessed'
```

Source: the seed `INSERT INTO ds_operator_policies` in `discovery-service/schema/postgres.sql`, applied only when the table is empty.

## FAQ

**Q: Why doesn't `allow pub.polis.comment from X` parse in tenant files?**
Because comments don't have a tenant-side acceptance layer — they live on
the commenter's site. The meaningful decisions are bless (display),
review (queue), or deny. `allow` at Layer 1 would be a no-op. At Layer 3
(operator), `allow pub.polis.comment` is valid because it gates stream
ingestion, which is a real decision.

**Q: Where do I put `allow pub.polis.post from all`?**
In DS operator policies (the `ds_operator_policies` table), not in tenant
files. It controls whether post announcements enter the DS stream — an
operator concern.

**Q: What happens to old DS-signed attestations after the v2 upgrade?**
They continue to verify byte-exact. The parser accepts the legacy
`emit pub.polis.comment.blessing from X` form at read time and translates
it to `bless pub.polis.comment from X` for evaluation, preserving the raw
string in `EvalResult.rule` for signature use.

**Q: Why is `review pub.polis.comment from all` explicit in the default
file when the engine defaults to pending on no-match anyway?**
Transparency. The pending-queue outcome should be visible in the policy
file rather than emerging from the absence of a rule. Readers should be
able to see the full decision surface.

**Q: Why are `emit`/`omit` listed as writable if no evaluator consumes them?**
They belong to the Layer 2 outbound surface (deciding whether to announce
an event to the DS). The verbs fit that surface semantically and were
originally defined for it. Wiring the evaluator is a future feature;
preserving the verbs in the grammar avoids a future migration if someone
writes outbound rules now.

**Q: Why is the grammar layered at all instead of flat?**
Because the decisions being expressed are categorically different.
Deciding whether to accept a DM ("allow/deny") is not the same as deciding
whether to bless a comment ("bless/review"). Flattening the verbs led to
the blessing-as-emission shape that motivated this refactor, and to
several type+verb combinations that looked valid but had no evaluator.

## See also

- `docs/cli/user/policies.md` — user-facing policy reference
- `docs/general/security/security-model.md` — overall security model
- `cli-go/pkg/policy/` — Go parser + evaluator
- The discovery service's TypeScript parser and evaluator (its source is not public)
- [`docs/signet/spec/license.md`](../../signet/spec/license.md) — the **outbound** licence
  (`pub.polis.license`). Not policy; see the callout in the TL;DR.
