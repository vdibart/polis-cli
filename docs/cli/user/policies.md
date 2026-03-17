# Policies

Policies are declarative rules that control how your polis site handles incoming events and outgoing side-effects. They replace hardcoded logic with user-configurable statements.

## Files

Two policy files, each containing JSONL rules:

| File | Visibility | Priority | Purpose |
|------|-----------|----------|---------|
| `.polis/policies/rules.jsonl` | Private | Higher | Instance preferences (never published) |
| `policies/rules.jsonl` | Public | Lower | Published moderation stance |

Private rules are evaluated first. The first matching rule wins.

## Grammar

```
<verb> <type> from <source> [at <domain>] [on <target>]
```

### Verbs

| Verb | Direction | Meaning |
|------|-----------|---------|
| `allow` | Incoming | Accept the event |
| `deny` | Incoming | Reject the event |
| `emit` | Outgoing | Trigger a side-effect (e.g., auto-bless a comment) |
| `omit` | Outgoing | Suppress a side-effect (e.g., skip a notification) |

`allow`/`deny` gate access. `emit`/`omit` control reactions.

### Types

- `all` -- matches any event type
- `none` -- matches nothing
- Dotted prefix -- `pub.polis.comment` matches `pub.polis.comment`, `pub.polis.comment.published`, `pub.polis.comment.blessing.requested`, etc. (dot-boundary prefix matching)

### Sources

| Source | Meaning | Resolved by |
|--------|---------|-------------|
| `all` | Any actor | Direct match |
| `none` | No actor | Direct match |
| `following` | Actor is in your following list | Client or DS |
| `followers` | Actor follows you | Client or DS |
| `self` | Actor domain matches your domain | Compare domains |
| `thread-blessed` | Actor has a prior granted blessing on the thread | DS (storage query) |

### Optional Clauses

- `at <domain>` -- restrict to a specific actor domain
- `on <target>` -- restrict to a specific target path

## Default Policies

Created by `polis init`:

### Public (`policies/rules.jsonl`)

```jsonl
{"version":1,"generator":"polis-cli-go/0.57.0"}
{"active":true,"policy":"emit pub.polis.comment.blessing from self"}
{"active":true,"policy":"emit pub.polis.comment.blessing from following"}
{"active":true,"policy":"emit pub.polis.comment.blessing from thread-blessed"}
```

These mean:
- Your own comments on your posts are auto-blessed
- Comments from people you follow are auto-blessed
- Authors who already have a blessed comment on a thread are auto-blessed on subsequent comments

### Private (`.polis/policies/rules.jsonl`)

```jsonl
{"version":1,"generator":"polis-cli-go/0.57.0"}
{"active":true,"policy":"allow pub.polis.post from all"}
{"active":true,"policy":"allow pub.polis.comment from all"}
{"active":true,"policy":"allow pub.polis.follow from all"}
{"active":true,"policy":"allow pub.polis.site from all"}
{"active":true,"policy":"allow pub.polis.dm from following"}
{"active":true,"policy":"deny pub.polis.dm from all"}
{"active":true,"policy":"omit pub.polis.notification from self"}
{"active":true,"policy":"omit pub.polis.feed from self"}
{"active":true,"policy":"deny all from all"}
```

These mean:
- All core content types are accepted from everyone
- DMs accepted only from people you follow
- Notifications for your own actions are suppressed
- Your own posts/comments are excluded from your feed
- **Default-deny catch-all**: unknown content types are denied (last rule)

The `deny all from all` catch-all ensures that unrecognized content types are
rejected by default. To allow a new content type, add an `allow` rule for it
above the catch-all.

**Existing sites:** The catch-all is only added for new sites created with
`polis init`. Existing sites are not affected. To opt in, add this line to
the end of `.polis/policies/rules.jsonl`:
```jsonl
{"active":true,"policy":"deny all from all"}
```

## Evaluation

Rules are evaluated top-to-bottom, first match wins. If no rule matches, the default is `allow` (permissive).

For blessing decisions specifically:
- `emit` match -> auto-grant
- `omit`/`deny` match -> auto-deny
- No match -> pending (manual review)

## System Behaviors

The following behaviors operate alongside the policy engine as system invariants.
They are intentionally hardcoded and not configurable via policy rules.

### Self-visibility

Authors always see their own unblessed comments and all comments on their own
posts, regardless of policy. This is required for the blessing workflow -- authors
must see pending comments to act on them. Enforced in the DS comment query handler.

### Blessing state machine

The blessing workflow uses policy evaluation to make automatic decisions:
- `emit` or `allow` (matched) -> auto-grant blessing
- `deny` (matched) -> auto-deny blessing
- No rule matches -> pending (manual review)
- Previously denied comments stay denied on author re-registration

### Fail-closed

If the DS cannot load or evaluate policies (e.g., database error), events are
blocked rather than allowed. This prevents policy bypass during outages.

### Operational limits

Rate limits and size limits exist alongside policies as operational constraints.
They protect the system from abuse but do not affect content decisions. Policies
cannot override operational limits.

### SSRF protection

Domains matching localhost, IP address literals, reserved TLDs (`.local`,
`.internal`, `.test`, `.example`, `.invalid`), and cloud metadata endpoints
are always rejected. This is infrastructure security, not content policy.

## Common Recipes

### Block a domain

Add to `.polis/policies/rules.jsonl`:
```jsonl
{"active":true,"policy":"deny all from all at spammer.example"}
```

### Disable comments entirely

Remove `allow pub.polis.comment from all` from private policies, or add:
```jsonl
{"active":true,"policy":"deny pub.polis.comment from all"}
```

### Accept DMs from everyone

Replace the DM rules in private policies:
```jsonl
{"active":true,"policy":"allow pub.polis.dm from all"}
```

### Show your own posts in your feed

Remove the feed omit rule from private policies:
```jsonl
{"active":false,"policy":"omit pub.polis.feed from self"}
```

### Opt out of thread-trust

Remove the thread-blessed rule from public policies. Only self and following will auto-bless.

### Custom content-type-only site

Remove all `allow pub.polis.*` rules from private policies. Only your custom content types (registered via bundles) will be processed.

### Disable a rule without deleting it

Set `active` to `false`:
```jsonl
{"active":false,"policy":"emit pub.polis.comment.blessing from thread-blessed"}
```

## Version Header

The first line of each policy file is a version header:
```jsonl
{"version":1,"generator":"polis-cli-go/0.57.0"}
```

This enables future upgrades to detect which generation of defaults a site has and apply only new rules without overwriting your customizations.

## DS Operator Policies

The discovery service maintains its own operator policies, stored in the database
and served at `GET /policies/rules.jsonl`. These define which content types the
DS accepts and what blocking rules are in effect.

Operators manage policies via the admin API:

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/v1/admin/policies` | GET | List all operator policy rules |
| `/v1/admin/policies` | POST | Add a new rule (validated via `parseRule()`) |
| `/v1/admin/policies/:id` | DELETE | Remove a rule by ID |
| `/v1/admin/policies/:id` | PATCH | Enable/disable a rule |
| `/v1/admin/block/domain` | POST | Convenience: add `deny all from all at <domain>` |
| `/v1/admin/block/domain` | DELETE | Convenience: remove matching domain deny rule |
| `/v1/admin/stream/purge` | POST | Purge events (not a policy action) |

When the DS uses its defaults instead of your policies for blessing decisions,
event metadata includes `policy_source: "ds-default"` and `fallback_reason` so
you can see exactly which rules were applied.
