# Polis Policy Grammar

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

## The three layers, in detail

### Layer 1 — Tenant inbound

Runs on a tenant's site. Decides what to do about content that has arrived
(a DM delivered by a sender) or been announced about me (a comment on my
post, requesting blessing). These are decisions with terminal outcomes, so
the verbs are decision verbs.

| Content type | Valid verbs | What each means | Evaluator |
|---|---|---|---|
| `pub.polis.dm` | `allow`, `deny` | Accept this DM into storage, or reject it | `cli-go/pkg/dm/receive.go:441`, `cli-go/pkg/policycheck/check.go:168` |
| `pub.polis.comment` | `deny`, `bless`, `review` | Reject the comment; auto-display it alongside my post; queue it for human review | DS `handlers/content.ts:316-405`; webapp `sync.go:568`; CLI `following/social.go:140` |

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
| `pub.polis.post` | `allow`, `deny` | Admit post announcement to stream | `discovery-service/core/stream.ts:84` |
| `pub.polis.comment` | `allow`, `deny` | Admit comment announcement to stream | same |
| `pub.polis.follow` | `allow`, `deny` | Admit follow announcement to stream | same |
| `pub.polis.site` | `allow`, `deny` | Admit site-registration announcement to stream | same |
| `pub.polis.comment` | `bless`, `review` | Fallback blessing policy when tenant file cannot be fetched | `discovery-service/core/handlers/content.ts:320` |

**Who writes Layer 3 rules.** The DS operator, via `ds_operator_policies`
rows. These rules are not authored by tenants. They must not appear in
tenant `rules.jsonl` files — the tenant-mode parser rejects them.

**Why bless/review appear here.** When a comment announcement arrives, the
DS fetches the target tenant's public `rules.jsonl` to decide blessing. If
that fetch fails, the DS falls back to its seeded operator-level blessing
rules. These are the same bless/review verbs, serving as a default instead
of per-tenant policy.

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
- `thread-blessed` — actors with a prior blessing on the same thread
  (resolved by the DS; always false client-side)
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
| `allow pub.polis.dm from <scope>` | ✅ live (Layer 1) | ❌ |
| `deny pub.polis.dm from <scope>` | ✅ live (Layer 1) | ❌ |
| `bless pub.polis.comment from <scope>` | ✅ live (Layer 1) | ✅ live (Layer 3 fallback) |
| `review pub.polis.comment from <scope>` | ✅ live (Layer 1) | ✅ live (Layer 3 fallback) |
| `deny pub.polis.comment from <scope>` | ✅ live (Layer 1) | ❌ |
| `allow pub.polis.comment from <scope>` | ❌ parse error | ✅ live (Layer 3) |
| `allow pub.polis.post from <scope>` | ❌ parse error | ✅ live (Layer 3) |
| `allow pub.polis.follow from <scope>` | ❌ parse error | ✅ live (Layer 3) |
| `allow pub.polis.site from <scope>` | ❌ parse error | ✅ live (Layer 3) |
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
- Tenant public: `<site>/policies/rules.jsonl` (published, fetched by DS)
- Tenant private: `<site>/.polis/policies/rules.jsonl` (not published; higher priority than public at evaluation time)

## Detecting and upgrading v1 files

**Detection.** Patrol reads the header line of each tenant's policy files
and flags any with `version < 2`. Drift events are emitted as
`patrol.policy_format_drift` with `handle`, `path`, `current_version`,
`expected_version` fields.

**Upgrade.** Medic rewrites v1 files with the canonical v2 default content
from `policy.DefaultPublicPolicyContent()` / `DefaultPrivatePolicyContent()`.
Because per-tenant policy customization does not yet exist, overwrite is
safe — the rewrite is equivalent to re-running `polis init`. When
customization lands, this logic needs revisiting (likely a real translator
rather than a template overwrite).

**Observability.** Medic emits `medic.policy_upgrade` with `handle`, `path`,
and `rule_count` on every rewrite. Re-running after upgrade is a no-op.

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

Source: `discovery-service/schema/postgres.sql` (around line 236).

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
- `discovery-service/core/policy.ts` — TypeScript parser + evaluator
