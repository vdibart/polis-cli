# Discovery Service Configuration

Tuning reference and admin API for the Polis Discovery Service.

**Target audience:** Operators tuning an existing Discovery Service deployment.

---

## Admin API

All admin endpoints are mounted under `/admin/*` and require the `OPERATOR_API_KEY` as a Bearer token.

```bash
# View blocks and stream config
curl -H "Authorization: Bearer $KEY" "$DS_URL/admin/blocks"

# Block a domain
curl -X POST -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"domain":"spam.example.com","scope":"all","reason":"spam"}' \
  "$DS_URL/admin/blocks-domains"

# Unblock a domain
curl -X DELETE -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"domain":"spam.example.com"}' \
  "$DS_URL/admin/blocks-domains"

# Block an event type (cannot block core polis.* types)
curl -X POST -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"type":"custom.spam","reason":"abuse"}' \
  "$DS_URL/admin/blocks-types"

# Switch stream to allowlist mode
curl -X POST -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"mode":"allowlist","allowed_types":["custom.approved"]}' \
  "$DS_URL/admin/stream-mode"

# Purge events by actor
curl -X POST -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"actor":"spam.example.com"}' \
  "$DS_URL/admin/stream-purge"
```

---

## Tuning Reference

Everything below is optional. The defaults work for most deployments.

### Overview

Every operational constant in the Discovery Service — rate limits, size limits, timeouts, query caps — can be overridden via environment variables at deploy time. This fits the Fly.io/Docker deployment model: no config files, just env vars.

**Design principles:**

- **Env vars only.** No config files. Set them in your `fly.toml`, `docker-compose.yml`, or shell.
- **All optional.** Unset vars use the default. You only need to set what you want to change.
- **All integers.** Every configurable value is a positive integer.
- **Fail fast.** Invalid values (non-integers, zero, negative) crash the server at startup with a clear error message.
- **Rate limit windows are fixed at 3600 seconds (1 hour).** Making windows configurable would double the env var count for minimal benefit.

**Naming convention:** `DS_<CATEGORY>_<NAME>`, where category is one of `RATE`, `LIMIT`, `TIMEOUT`, or `QUERY`.

### All Configurable Settings

All 24 settings at a glance:

| Env Var | Default | Description |
|---------|---------|-------------|
| `DS_RATE_DOMAIN_SITES_REGISTER` | 5 | Per-domain site registrations per hour |
| `DS_RATE_DOMAIN_SITES_UNREGISTER` | 5 | Per-domain site unregistrations per hour |
| `DS_RATE_DOMAIN_CONTENT_REGISTER` | 50 | Per-domain content registrations per hour |
| `DS_RATE_DOMAIN_CONTENT_UNREGISTER` | 20 | Per-domain content unregistrations per hour |
| `DS_RATE_DOMAIN_RELATIONSHIP_UPDATE` | 50 | Per-domain relationship updates per hour |
| `DS_RATE_DOMAIN_STREAM_PUBLISH` | 100 | Per-domain stream publishes per hour |
| `DS_RATE_IP_WRITE_PREAUTH` | 120 | Per-IP write requests per hour (pre-auth) |
| `DS_RATE_IP_SITES_CHECK` | 300 | Per-IP site check queries per hour |
| `DS_RATE_IP_CONTENT_CHECK` | 300 | Per-IP content check queries per hour |
| `DS_RATE_IP_CONTENT_QUERY` | 600 | Per-IP content queries per hour |
| `DS_RATE_IP_RELATIONSHIP_QUERY` | 600 | Per-IP relationship queries per hour |
| `DS_RATE_IP_STREAM` | 1200 | Per-IP stream queries per hour |
| `DS_RATE_IP_STREAM_HEALTH` | 300 | Per-IP stream health checks per hour |
| `DS_RATE_IP_MIGRATIONS_QUERY` | 300 | Per-IP migration queries per hour |
| `DS_LIMIT_URL_MAX_LENGTH` | 2048 | Maximum URL length in characters |
| `DS_LIMIT_SIGNATURE_MAX_LENGTH` | 2048 | Maximum signature length in characters |
| `DS_LIMIT_METADATA_MAX_BYTES` | 4096 | Maximum metadata JSON size in bytes |
| `DS_LIMIT_STREAM_PAYLOAD_MAX_BYTES` | 8192 | Maximum stream event payload in bytes |
| `DS_LIMIT_POST_BODY_MAX_BYTES` | 65536 | Maximum POST request body in bytes |
| `DS_TIMEOUT_WELLKNOWN_FETCH_MS` | 10000 | Timeout for .well-known/polis fetches (ms) |
| `DS_TIMEOUT_GET_AUTH_MAX_AGE_MS` | 300000 | Max age for signed GET timestamps (ms) |
| `DS_QUERY_CONTENT_MAX_LIMIT` | 100 | Max `limit` for content/relationship queries |
| `DS_QUERY_STREAM_MAX_LIMIT` | 1000 | Max `limit` for stream queries |
| `DS_QUERY_MAX_OFFSET` | 10000 | Max pagination offset for queries |

### Domain Rate Limits

These limits are per-domain per hour. They restrict how many operations a single registered domain can perform. The domain is extracted from the request payload (the acting domain, not the IP).

| Env Var | Default | Endpoint |
|---------|---------|----------|
| `DS_RATE_DOMAIN_SITES_REGISTER` | 5 | `POST /ds-sites-register` |
| `DS_RATE_DOMAIN_SITES_UNREGISTER` | 5 | `POST /ds-sites-unregister` |
| `DS_RATE_DOMAIN_CONTENT_REGISTER` | 50 | `POST /ds-content-register` |
| `DS_RATE_DOMAIN_CONTENT_UNREGISTER` | 20 | `POST /ds-content-unregister` |
| `DS_RATE_DOMAIN_RELATIONSHIP_UPDATE` | 50 | `POST /ds-relationship-update` |
| `DS_RATE_DOMAIN_STREAM_PUBLISH` | 100 | `POST /ds-stream-publish` |

**When to tune:** If you expect high-volume publishers, increase `DS_RATE_DOMAIN_CONTENT_REGISTER`. If you're running a small private instance and want tighter limits to prevent abuse, lower the values.

### IP Rate Limits

These limits are per-IP per hour. They apply before any payload parsing or authentication, protecting the server from brute-force or scraping. IP is extracted from `X-Forwarded-For` or `X-Real-IP` headers (standard for reverse proxies like Fly.io).

| Env Var | Default | Endpoints |
|---------|---------|-----------|
| `DS_RATE_IP_WRITE_PREAUTH` | 120 | All `POST` endpoints |
| `DS_RATE_IP_SITES_CHECK` | 300 | `GET /ds-sites-check` |
| `DS_RATE_IP_CONTENT_CHECK` | 300 | `GET /ds-content-check` |
| `DS_RATE_IP_CONTENT_QUERY` | 600 | `GET /ds-content-query` |
| `DS_RATE_IP_RELATIONSHIP_QUERY` | 600 | `GET /ds-relationship-query` |
| `DS_RATE_IP_STREAM` | 1200 | `GET /ds-stream` |
| `DS_RATE_IP_STREAM_HEALTH` | 300 | `GET /ds-stream-health` |
| `DS_RATE_IP_MIGRATIONS_QUERY` | 300 | `GET /ds-migrations` |

**When to tune:** The stream endpoint has a higher default (1200/hr = 20/min) because clients poll it frequently for new events. If you're behind an additional rate limiter (e.g. Cloudflare), you may want to raise these.

### Size Limits

These limits cap the size of various payloads to prevent oversized requests.

| Env Var | Default | What it limits |
|---------|---------|----------------|
| `DS_LIMIT_URL_MAX_LENGTH` | 2048 | URLs in registration payloads |
| `DS_LIMIT_SIGNATURE_MAX_LENGTH` | 2048 | OpenSSH signature strings |
| `DS_LIMIT_METADATA_MAX_BYTES` | 4096 | JSON metadata on content records |
| `DS_LIMIT_STREAM_PAYLOAD_MAX_BYTES` | 8192 | Event payload in stream publish |
| `DS_LIMIT_POST_BODY_MAX_BYTES` | 65536 | Raw POST body (checked via Content-Length) |

**When to tune:** If you need to support longer metadata (e.g. rich post metadata), increase `DS_LIMIT_METADATA_MAX_BYTES`. The `DS_LIMIT_POST_BODY_MAX_BYTES` is a global cap checked before JSON parsing — it should always be larger than any individual field limit.

### Timeouts

These control network timeout behavior for outbound requests.

| Env Var | Default | What it controls |
|---------|---------|------------------|
| `DS_TIMEOUT_WELLKNOWN_FETCH_MS` | 10000 | Fetch timeout for `.well-known/polis`, `following.json`, and other outbound HTTP requests during signature verification |
| `DS_TIMEOUT_GET_AUTH_MAX_AGE_MS` | 300000 | Maximum age (in ms) for timestamps in signed GET requests. Default is 5 minutes. |

**When to tune:** If sites you serve have high-latency origins, increase `DS_TIMEOUT_WELLKNOWN_FETCH_MS`. The auth max age controls replay window — shorter is more secure but less tolerant of clock skew.

### Query Limits

These cap pagination parameters to prevent expensive queries.

| Env Var | Default | What it caps |
|---------|---------|--------------|
| `DS_QUERY_CONTENT_MAX_LIMIT` | 100 | Max `limit` param on `/ds-content-query` and `/ds-relationship-query` |
| `DS_QUERY_STREAM_MAX_LIMIT` | 1000 | Max `limit` param on `/ds-stream` |
| `DS_QUERY_MAX_OFFSET` | 10000 | Max `offset` param on all query endpoints |

**When to tune:** The stream has a higher limit because clients often need to catch up on many events. If you have very large datasets and need deeper pagination, increase `DS_QUERY_MAX_OFFSET`.

### Config Examples

Fly.io (`fly.toml`):

```toml
[env]
  DS_RATE_DOMAIN_CONTENT_REGISTER = "200"
  DS_LIMIT_POST_BODY_MAX_BYTES = "131072"
  DS_TIMEOUT_WELLKNOWN_FETCH_MS = "15000"
```

Docker Compose:

```yaml
environment:
  DS_RATE_DOMAIN_CONTENT_REGISTER: "200"
  DS_LIMIT_POST_BODY_MAX_BYTES: "131072"
  DS_TIMEOUT_WELLKNOWN_FETCH_MS: "15000"
```

Shell:

```bash
DS_RATE_DOMAIN_CONTENT_REGISTER=200 \
DS_LIMIT_POST_BODY_MAX_BYTES=131072 \
deno run --allow-net --allow-env --allow-read server/src/index.ts
```

### Validation and Failure Behavior

All config values are validated at startup:

- Every value must be a **positive integer** (> 0).
- Non-integer strings (e.g. `DS_RATE_IP_STREAM=abc`) cause an immediate crash with:
  ```
  Invalid environment variable DS_RATE_IP_STREAM: expected an integer, got "abc"
  ```
- Zero or negative values cause a crash with:
  ```
  Invalid config: ipRateLimits.stream must be a positive integer, got 0
  ```
- Empty or unset env vars are ignored (defaults apply).

This fail-fast behavior ensures you never run a production instance with silently broken configuration.

### Architecture Note

The configuration system follows the same core/server split as the rest of the DS:

- **`core/config.ts`** defines the types, defaults, merge logic, and validation. No env var reading — core stays platform-independent.
- **`server/src/env-config.ts`** is the only file that reads `Deno.env`. It maps env vars to the config structure and calls the core validation.
- **`core/validation.ts`** uses a module-level setter (`setValidationConfig()`) called once at startup.
- **Handler functions** accept an optional `rateLimits` parameter with defaults, so existing callers and tests work without changes.
