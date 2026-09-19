# Discovery Service

The Discovery Service (DS) is the coordination layer for the polis decentralized network. Individual polis sites are fully independent — they own their content and serve it from their own domains. The DS enables interaction between sites.

## What It Does

1. **Sites register** with the DS, publishing their public key and domain
2. **Content is indexed** — posts and comments are registered with metadata
3. **Relationships are managed** — blessings (comment approval), following
4. **Events are streamed** — real-time notifications of network activity

Sites can operate without a DS. The DS adds discoverability and real-time coordination.

## Architecture

The codebase — not in the public repository — follows a hexagonal (ports and adapters) pattern:

```
discovery-service/
  core/               # Pure business logic (no platform deps)
    types.ts          # Shared type definitions
    storage.ts        # StorageAdapter interface (the "port")
    validation.ts     # Input validation, signature verification
    config.ts         # Tuning parameters: types, defaults, validation
    policy.ts         # Operator policy parsing and evaluation
    pql.ts            # PQL sentence parser (the DS side of `/pql/<sentence>`)
    stream.ts         # Event emission, operator policy gate
    witness.ts        # Witness countersignatures
    wake.ts           # Wake callbacks to affected domains
    handlers/         # Request handlers (pure functions)

  server/             # Adapter: Hono/Deno server for Fly.io (active)
    src/index.ts            # Routes and wiring
    src/env-config.ts       # The only reader of DS_* environment variables
    src/postgres-storage.ts # StorageAdapter impl using postgres.js
    src/middleware/         # CORS, security headers, logging, operator auth

  schema/postgres.sql # Schema, applied idempotently on every boot
  _archived/          # Retired adapters (Supabase Edge Functions)
```

The StorageAdapter interface is the boundary between business logic and persistence. Core handlers are testable without any database or HTTP framework.

### Data Model

| Table | Purpose |
|-------|---------|
| `ds_registered_sites` | Site registry (domain, registry URL, owner signature, service attestation) |
| `ds_key_history` | Per-domain Ed25519 key rotation history |
| `ds_content_metadata` | Content index (posts, comments with URL, version, witness countersignature) |
| `ds_relationship_metadata` | Relationships (blessings, following with status) |
| `ds_events` | Event stream (ordered, typed events with cursors) |
| `ds_events_archive` | Events moved out of `ds_events` after the retention window (90 days by default) |
| `ds_operator_policies` | Operator policy rules (allow/deny, replaces the legacy `admin_blocked_*` tables) |
| `admin_audit_log` | Audit trail of operator admin actions |
| `admin_ds_keys` | This service's own signing keys, current and past, for verifying old attestations |
| `admin_rate_limits` | Rate-limit counters, per domain and per client IP |

A view, `ds_sites_with_activity`, backs `GET /v1/sites/list`.

## Quick Start

The Discovery Service source is not in the public repository, so there is no quick start for running your own DS today. Sites use a
running DS, such as `ds.polis.pub`, through its [REST API](developer/api-reference.md). What a deployment consists of,
and how to verify one, is in [admin/deployment.md](admin/deployment.md).

## Documentation

| Document | Audience | Description |
|----------|----------|-------------|
| [admin/deployment.md](admin/deployment.md) | Operators | Overview: what a deployment consists of, and how to verify one |
| [admin/configuration.md](admin/configuration.md) | Operators | Overview: the admin API's operator-policy model, and what an operator can tune |
| [developer/api-reference.md](developer/api-reference.md) | Developers | Complete REST API reference (30+ endpoints) |
| [developer/pql-json-api.md](developer/pql-json-api.md) | Developers | `GET /pql/<sentence>` — the PQL query endpoint as JSON |
| [developer/stream-architecture.md](developer/stream-architecture.md) | Developers | Event stream design, projections, security layers |
| [developer/unpublish-lifecycle.md](developer/unpublish-lifecycle.md) | Developers | What unpublishing does to content and blessings |
| [developer/storage-adapter.md](developer/storage-adapter.md) | Developers | Retired — the page now says so |

## See Also

- [docs/general/concepts/content-system.md](../general/concepts/content-system.md) — Events and content types
- [docs/general/security/security-model.md](../general/security/security-model.md) — Auth model, signature verification
