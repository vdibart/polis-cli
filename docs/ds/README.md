# Discovery Service

The Discovery Service (DS) is the coordination layer for the polis decentralized network. Individual polis sites are fully independent — they own their content and serve it from their own domains. The DS enables interaction between sites.

## What It Does

1. **Sites register** with the DS, publishing their public key and domain
2. **Content is indexed** — posts and comments are registered with metadata
3. **Relationships are managed** — blessings (comment approval), following
4. **Events are streamed** — real-time notifications of network activity

Sites can operate without a DS. The DS adds discoverability and real-time coordination.

## Architecture

The codebase follows a hexagonal (ports and adapters) pattern:

```
discovery-service/
  core/               # Pure business logic (no platform deps)
    types.ts          # Shared type definitions
    storage.ts        # StorageAdapter interface (the "port")
    validation.ts     # Input validation, signature verification
    handlers/         # Request handlers (pure functions)

  server/             # Adapter: Hono/Deno server for Fly.io (active)
    postgres-storage.ts   # StorageAdapter impl using postgres.js

  _archived/          # Retired adapters (Supabase Edge Functions)
```

The StorageAdapter interface is the boundary between business logic and persistence. Core handlers are testable without any database or HTTP framework.

### Data Model

| Table | Purpose |
|-------|---------|
| `ds_registered_sites` | Site registry (domain, public key, attestation) |
| `ds_content_metadata` | Content index (posts, comments with URL, version) |
| `ds_relationship_metadata` | Relationships (blessings, following with status) |
| `ds_events` | Event stream (ordered, typed events with cursors) |
| `ds_domain_migrations` | Domain migration records |
| `admin_blocked_domains` | Operator-blocked domains |
| `admin_blocked_types` | Operator-blocked event types |

## Quick Start

```bash
cd discovery-service/server

# Set environment
export DATABASE_URL="postgres://user:pass@host:5432/polis_ds"
export DS_PRIVATE_KEY="<base64-encoded-32-byte-key>"
export DS_PUBLIC_URL="https://ds.example.com"
export OPERATOR_API_KEY="$(openssl rand -hex 32)"

# Run
deno task start
```

## Documentation

| Document | Audience | Description |
|----------|----------|-------------|
| [admin/deployment.md](admin/deployment.md) | Operators | Deploy on Fly.io, Docker, or bare Deno |
| [admin/configuration.md](admin/configuration.md) | Operators | Admin API and 24 tuning parameters |
| [developer/api-reference.md](developer/api-reference.md) | Developers | Complete REST API reference (20+ endpoints) |
| [developer/stream-architecture.md](developer/stream-architecture.md) | Developers | Event stream design, projections, security layers |
| [developer/storage-adapter.md](developer/storage-adapter.md) | Developers | Custom storage adapter interface |

## See Also

- [discovery-service/README.md](../../discovery-service/README.md) — Source tree overview
- [docs/general/content-system.md](../general/content-system.md) — Events and content types
- [docs/general/security-model.md](../general/security-model.md) — Auth model, signature verification
