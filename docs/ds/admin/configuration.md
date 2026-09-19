# Discovery Service Configuration

*For* [Operators](../../README.md#running-polis) — *Kind* [Reference](../../README.md#kinds-of-page) — *Component* [Discovery service](../README.md)

What an operator of a Discovery Service can tune, and why: the admin API and its operator-policy model, and the
classes of operational limit a deployment can change.

**Target audience:** Operators tuning an existing Discovery Service deployment.

⚠️ **The Discovery Service source is not in the public repository**, so the full tuning reference — every variable,
its default and its endpoint — is not published here. The sections below say what each class of setting is for. They
return in full when the DS source is published.

---

## Admin API

All admin endpoints are mounted under `/v1/admin/` and require an operator API
key as a Bearer token. Set via `OPERATOR_API_KEYS` (comma-separated for key
rotation) or legacy `OPERATOR_API_KEY` env var. Admin requests are rate-limited
to 30 per client IP.

### Operator Policies

Content blocking decisions are driven by the `ds_operator_policies` table.
Rules use the Layer 3 vocabulary of the
[policy grammar](../../general/reference/policy-grammar.md) (`allow`/`deny`),
are evaluated in insertion order (first match wins), and gate the event
stream: an event whose actor or type a `deny` rule matches is not recorded, and
a direct `POST /v1/stream` from it is rejected with 403. No matching rule means
allow. Changes made through this API take effect immediately.

```bash
# List operator policies (paginated: ?limit= default 200, max 1000;
# pass the returned "cursor" back while "has_more" is true)
curl -H "Authorization: Bearer $KEY" "$DS_URL/v1/admin/policies"

# Add a policy rule
curl -X POST -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"policy":"deny all from all at spam.example.com","reason":"spam"}' \
  "$DS_URL/v1/admin/policies"

# Remove a policy by ID
curl -X DELETE -H "Authorization: Bearer $KEY" \
  "$DS_URL/v1/admin/policies/42"

# Disable a policy without removing it
curl -X PATCH -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"active":false}' \
  "$DS_URL/v1/admin/policies/42"
```

### Convenience: Domain Blocking

```bash
# Block a domain (adds "deny all from all at <domain>" policy)
curl -X POST -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"domain":"spam.example.com","reason":"spam"}' \
  "$DS_URL/v1/admin/block/domain"

# Unblock a domain (removes every "deny all from all at <domain>" rule, case-insensitive)
curl -X DELETE -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"domain":"spam.example.com"}' \
  "$DS_URL/v1/admin/block/domain"
```

### Stream Purge

```bash
# Purge events (not a policy action — cleans event data).
# At least one filter is required: "actor", "type", or "before" (a timestamp).
curl -X POST -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"actor":"spam.example.com"}' \
  "$DS_URL/v1/admin/stream/purge"
```

### Key Rotation

To rotate operator API keys without downtime:

1. Set `OPERATOR_API_KEYS=oldkey,newkey` in env
2. Deploy — both keys now work
3. Update clients to use `newkey`
4. Set `OPERATOR_API_KEYS=newkey` (remove old)
5. Deploy — old key is rejected

Every admin action is logged to stdout and, best-effort, to the
`admin_audit_log` table. Each entry records which key authenticated the request
as `operator_key_index` in its params: the key's position in
`OPERATOR_API_KEYS`, counting from 0, never the key itself. Before step 4, check
that recent entries no longer show the old key's index.

---

## Tuning Reference

Everything in this section is optional; the defaults work for most deployments. The per-variable reference is not
published while the DS source is closed, and returns in full when it is.

### Overview

Every operational constant in the Discovery Service — rate limits, quotas, size limits, timeouts, query caps — can be
overridden by an environment variable at deploy time. There are no config files. Every value is optional, unset means
the default, and an invalid value stops the server at startup rather than running with a broken configuration.

### All Configurable Settings

The full table of settings is not published while the DS source is closed. The classes of setting are below.

### Domain Rate Limits

Per-domain, per-hour limits on write operations — site registration, content registration and unpublishing,
relationship updates, stream publishes — keyed on the acting domain rather than the IP. An operator tunes them for
high-volume publishers, or tightens them on a small private instance.

### Domain Quotas

A rate limit bounds how fast a domain writes; a quota bounds how much it can add. Content records are never pruned, so
a per-domain cap on new content records in a rolling window stops one domain accumulating an unbounded index inside
the hourly limit. Over quota is refused with a retry-after; nothing stored is deleted. ⚠️ A quota below what an honest
site needs is an outage, so it should be measured against real sites before it is lowered.

### IP Rate Limits

Per-IP, per-hour limits applied before any payload parsing or authentication, protecting the server from brute-force
and scraping. Read endpoints that clients poll, such as the stream, carry higher limits.

### Size Limits

Caps on URLs, signatures, metadata, stream payloads and the raw request body, so oversized requests are refused before
they cost anything.

### Timeouts

Timeouts on the DS's outbound fetches during signature verification, and the maximum age of a signed GET request's
timestamp — the replay window, where shorter is more secure but less tolerant of clock skew.

### Query Limits

Caps on pagination parameters, so no single query is unboundedly expensive.

### Config Examples

Not published while the DS source is closed.

### Validation and Failure Behavior

Values are validated at startup, and an invalid one — not an integer, zero, or negative — stops the server with an
error naming the variable. Unset or empty values use the defaults.

### Wake Callbacks

After emitting events that affect a domain, the DS fires a data-free `GET https://{domain}/v1/wake` telling that
domain to check its stream. It is fire-and-forget, rate-limited per domain, and ignores sites that do not implement
the endpoint. An operator can switch wake callbacks off.

### Architecture Note

Configuration follows the same split as the rest of the DS: the core defines the settings, their defaults and their
validation without reading the environment, and the server adapter is the only part that reads environment
variables.
