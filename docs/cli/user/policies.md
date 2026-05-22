# Policies

Policies are declarative rules that control how your polis site handles
incoming content and (in the future) outgoing event announcements. They
replace hardcoded logic with user-configurable statements.

**For the authoritative grammar spec — every verb, every layer, every
edge case — see [`docs/general/policy-grammar.md`](../../general/policy-grammar.md).**
This page is the user-facing guide. If anything here conflicts with the
spec, the spec wins.

## Files

Two policy files, each containing JSONL rules:

| File | Visibility | Priority | Purpose |
|------|------------|----------|---------|
| `.polis/policies/rules.jsonl` | Private | Higher | Per-instance overrides (never published) |
| `policies/rules.jsonl` | Public | Lower | Published moderation stance |

Private rules are evaluated first. The first matching rule wins across
both files.

## Grammar

```
<verb> <type> from <source> [at <domain>] [on <target>]
```

### Verbs

Six verbs, organized by **layer** — each layer uses the vocabulary that
matches the kind of decision it makes:

| Verb | Layer | Meaning |
|------|-------|---------|
| `allow` | 1 (tenant inbound) | Accept this content into storage |
| `deny` | 1 (tenant inbound) | Reject this content |
| `bless` | 1 (tenant inbound, comments) | Auto-display this comment alongside the post |
| `review` | 1 (tenant inbound, comments) | Queue this comment for human review |
| `emit` | 2 (tenant outbound, reserved) | Announce this event to the DS |
| `omit` | 2 (tenant outbound, reserved) | Suppress the DS announcement |

**Layer 2 status.** `emit`/`omit` parse and are retained, but no evaluator
currently reads them. This is a reserved surface for a future feature
(suppressing DS announcements for specific events). Do not rely on them
having any effect today.

### Types

- `all` — matches any event type
- `none` — matches nothing (useful for disabled rules)
- Dotted prefix — `pub.polis.comment` matches `pub.polis.comment`,
  `pub.polis.comment.published`, `pub.polis.comment.blessing.requested`,
  etc. (dot-boundary prefix matching)

### Sources

| Source | Meaning |
|--------|---------|
| `all` | Any actor |
| `none` | No actor |
| `following` | Actors in your following list |
| `followers` | Actors who follow you |
| `self` | Your own domain |
| `thread-blessed` | Actors with a prior blessing on the same thread (DS-resolved) |

### Optional clauses

- `at <domain>` — restrict to a specific actor domain
- `on <target>` — restrict to a specific target path

### Verb + type validity (tenant files)

Not every verb is valid with every type. The parser rejects invalid
combinations. Full matrix in the [grammar spec](../../general/policy-grammar.md#verb-validity-matrix).

| Rule form | Valid? | Why |
|---|---|---|
| `allow pub.polis.dm from <scope>` | ✅ | DM inbound acceptance |
| `deny pub.polis.dm from <scope>` | ✅ | DM inbound rejection |
| `bless pub.polis.comment from <scope>` | ✅ | Auto-display comment |
| `review pub.polis.comment from <scope>` | ✅ | Queue comment for review |
| `deny pub.polis.comment from <scope>` | ✅ | Reject comment |
| `allow pub.polis.comment from <scope>` | ❌ | Comments have no acceptance layer — use bless/review/deny |
| `allow pub.polis.post from <scope>` | ❌ | Post stream admission is a DS operator concern |
| `emit pub.polis.<type> from <scope>` | ✅ | Layer 2 outbound (reserved) |

## Default policies

Created by `polis init`:

### Public (`policies/rules.jsonl`)

```jsonl
{"version":2,"generator":"polis-cli-go/0.63.0"}
{"active":true,"policy":"allow pub.polis.dm from following"}
{"active":true,"policy":"deny pub.polis.dm from all"}
{"active":true,"policy":"bless pub.polis.comment from self"}
{"active":true,"policy":"bless pub.polis.comment from following"}
{"active":true,"policy":"bless pub.polis.comment from thread-blessed"}
{"active":true,"policy":"review pub.polis.comment from all"}
{"active":true,"policy":"deny all from all"}
```

What these mean:

- DMs accepted only from people you follow; rejected from everyone else.
- Your own comments on your posts are auto-blessed.
- Comments from people you follow are auto-blessed.
- Authors with a prior blessed comment in the same thread are auto-blessed.
- **All other commenters land in the review queue** — this is the
  explicit terminal rule that replaces the v1 silent-deny behavior.
- Default-deny catch-all for unrecognized content types.

### Private (`.polis/policies/rules.jsonl`)

```jsonl
{"version":2,"generator":"polis-cli-go/0.63.0"}
```

Empty by default. Add overrides here that you don't want advertised
publicly (e.g. silently blocking a specific domain).

## Evaluation

Rules are evaluated top-to-bottom (private file first), first match wins.
If no rule matches, the outcome is `allow` with `matched: false` — which
consumers interpret as "no explicit decision."

For comment blessing specifically, the decision flows through the
three-valued outcome space:

- `bless` match (or legacy `emit pub.polis.comment.blessing` match) → auto-grant
- `review` match → queued for human review (explicit pending)
- `deny` match → auto-deny
- No match → implicit pending (manual review)

The canonical default ruleset includes an explicit
`review pub.polis.comment from all` terminal so the pending outcome is
visible rather than implicit.

## Legacy grammar

Before v2, blessing rules were written as
`emit pub.polis.comment.blessing from <scope>`. The parser still accepts
this form (DS-signed historical attestations contain it, and rewriting
those strings would invalidate signatures). Legacy rules are translated
at parse time to their v2 equivalent — `bless pub.polis.comment from
<scope>` — and evaluated accordingly. Writers (new policy files, Medic
rewrites) never produce the legacy form.

If Patrol detects a v1 file (`version:1` header), Medic silently rewrites
it with the canonical v2 defaults on the next healing sweep. This is safe
because per-tenant policy customization does not yet exist; when it lands,
the rewrite logic will be replaced with a real translator.

## System behaviors

The following operate alongside the policy engine as system invariants.
They are hardcoded and not configurable via policy rules.

### Self-visibility

Authors always see their own unblessed comments and all comments on their
own posts, regardless of policy. Required for the blessing workflow.

### Fail-closed

If the DS cannot load or evaluate policies (e.g. storage error), events
are blocked rather than allowed. Prevents policy bypass during outages.

### Operational limits

Rate limits and size limits protect the system from abuse but do not
affect content decisions. Policies cannot override operational limits.

### SSRF protection

Domains matching localhost, IP literals, reserved TLDs (`.local`,
`.internal`, `.test`, `.example`, `.invalid`), and cloud metadata
endpoints are always rejected. Infrastructure security, not content
policy.

## Common recipes

### Block a domain

Add to `.polis/policies/rules.jsonl`:

```jsonl
{"active":true,"policy":"deny all from all at spammer.example"}
```

### Deny all comments outright

Add before the `review pub.polis.comment from all` line in the public file:

```jsonl
{"active":true,"policy":"deny pub.polis.comment from all"}
```

### Accept DMs from everyone

Replace the DM rules in the public file:

```jsonl
{"active":true,"policy":"allow pub.polis.dm from all"}
```

### Bless all thread-reply comments automatically

Already in the defaults as `bless pub.polis.comment from thread-blessed`.
To opt out, set `active: false` on that line.

### Disable a rule without deleting it

Set `active` to `false`:

```jsonl
{"active":false,"policy":"bless pub.polis.comment from thread-blessed"}
```

## Version header

The first line of each policy file is a version header:

```jsonl
{"version":2,"generator":"polis-cli-go/0.63.0"}
```

Version `2` is the current format (introduced with the decision-verb
refactor). Version `1` files are automatically upgraded by Patrol + Medic
on hosted deployments — no action required.

## DS operator policies

The discovery service maintains its own operator policies, stored in the
database and served at `GET /policies/rules.jsonl`. These are **Layer 3**
rules — they gate whether announcements enter the DS event stream and
provide fallback blessing policy when a tenant's rules.jsonl cannot be
fetched.

Operators manage these via the admin API:

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/v1/admin/policies` | GET | List all operator policy rules |
| `/v1/admin/policies` | POST | Add a new rule (validated via `parseRule(rule, 'operator')`) |
| `/v1/admin/policies/:id` | DELETE | Remove a rule by ID |
| `/v1/admin/policies/:id` | PATCH | Enable/disable a rule |
| `/v1/admin/block/domain` | POST | Convenience: add `deny all from all at <domain>` |
| `/v1/admin/block/domain` | DELETE | Convenience: remove matching domain deny rule |

**Operator-layer rules differ from tenant-layer rules.** Operator policies
accept `allow`/`deny` on `pub.polis.{post,comment,follow,site}` — which
tenant files reject. They also accept `bless`/`review` on
`pub.polis.comment` as fallback blessing policy. They do not accept
`emit`/`omit`. See the [grammar spec](../../general/policy-grammar.md#layer-3-ds-operator-ingestion)
for the full operator matrix.

When the DS uses its defaults instead of your policies for blessing
decisions, event metadata includes `policy_source: "ds-default"` and a
`fallback_reason` so you can see exactly which rules were applied.
