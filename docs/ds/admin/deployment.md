# Discovery Service Deployment Guide

How to deploy your own Polis Discovery Service instance.

**Target audience:** Operators running their own Discovery Service.

---

## Prerequisites

- **PostgreSQL 14+** — any provider works (Fly Postgres, Supabase, RDS, self-hosted)
- **Deno 2.x** — the server runtime (bundled in the Docker image)
- **An Ed25519 keypair** — the DS signs service attestations with this key (see [Generate a DS Keypair](#generate-a-ds-keypair))

---

## Required Environment Variables

These must be set for the server to start. They are **not** part of the optional tuning config.

| Variable | Description | Example |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL connection string | `postgres://user:pass@host:5432/polis_ds` |
| `DS_PRIVATE_KEY` | Base64-encoded Ed25519 private key (32 bytes) for signing service attestations | *(see keypair section)* |
| `DS_PUBLIC_URL` | Public URL of this DS instance (used in registry URLs) | `https://ds.example.com` |
| `OPERATOR_API_KEY` | Bearer token for admin endpoints (`/admin/*`) | `$(openssl rand -hex 32)` |

Optional infrastructure variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | HTTP listen port |
| `DISCOVERY_SERVICE_PUBLIC_KEY` | *(empty)* | Public key served at `GET /v1/sites/public-key` |
| `DISCOVERY_SERVICE_KEY_ID` | `ds-primary` | Key ID served alongside the public key |

---

## Deploy on Fly.io

This is the recommended path. The repo includes a working `fly.toml` and `Dockerfile`.

```bash
cd discovery-service/server

# 1. Create app
fly apps create my-polis-ds

# 2. Create and attach Postgres
fly postgres create --name my-polis-ds-db
fly postgres attach my-polis-ds-db --app my-polis-ds

# 3. Set required secrets
fly secrets set \
  DS_PRIVATE_KEY="<base64-encoded-32-byte-key>" \
  OPERATOR_API_KEY="$(openssl rand -hex 32)" \
  --app my-polis-ds

# 4. Edit fly.toml: set your app name and DS_PUBLIC_URL
#    [env]
#      DS_PUBLIC_URL = "https://my-polis-ds.fly.dev"

# 5. Deploy
fly deploy

# 6. Verify
curl https://my-polis-ds.fly.dev/health
# → {"status":"ok","service":"polis-ds"}
```

The schema migration runs automatically on every boot (idempotent `CREATE TABLE IF NOT EXISTS`).

---

## Deploy with Docker

```bash
cd discovery-service

# Build
docker build -f server/Dockerfile -t polis-ds .

# Run (set required vars, add optional DS_* tuning vars as needed)
docker run -d \
  -p 8080:8080 \
  -e DATABASE_URL="postgres://user:pass@host:5432/polis_ds" \
  -e DS_PRIVATE_KEY="<base64-encoded-32-byte-key>" \
  -e DS_PUBLIC_URL="https://ds.example.com" \
  -e OPERATOR_API_KEY="$(openssl rand -hex 32)" \
  polis-ds
```

Or with Docker Compose:

```yaml
services:
  db:
    image: postgres:16
    environment:
      POSTGRES_DB: polis_ds
      POSTGRES_USER: polis
      POSTGRES_PASSWORD: secret
    volumes:
      - pgdata:/var/lib/postgresql/data
      - ./schema/postgres.sql:/docker-entrypoint-initdb.d/schema.sql

  ds:
    build:
      context: .
      dockerfile: server/Dockerfile
    ports:
      - "8080:8080"
    depends_on:
      - db
    environment:
      DATABASE_URL: postgres://polis:secret@db:5432/polis_ds
      DS_PRIVATE_KEY: "<base64-encoded-32-byte-key>"
      DS_PUBLIC_URL: "https://ds.example.com"
      OPERATOR_API_KEY: "change-me"
      # Optional tuning:
      # DS_RATE_DOMAIN_CONTENT_REGISTER: "200"

volumes:
  pgdata:
```

---

## Deploy on Bare Deno

For development or any host with Deno installed:

```bash
cd discovery-service

# 1. Run schema migration against your Postgres
psql "$DATABASE_URL" < schema/postgres.sql

# 2. Export required environment variables
export DATABASE_URL="postgres://user:pass@host:5432/polis_ds"
export DS_PRIVATE_KEY="<base64-encoded-32-byte-key>"
export DS_PUBLIC_URL="https://ds.example.com"
export OPERATOR_API_KEY="$(openssl rand -hex 32)"

# 3. Start the server
cd server
deno run --allow-net --allow-env --allow-read src/index.ts

# Or use the deno task:
deno task start

# For development with auto-reload:
deno task dev
```

---

## Verify Your Deployment

After deploying, run these checks:

```bash
DS_URL="https://your-ds.example.com"

# Health check
curl "$DS_URL/health"
# → {"status":"ok","service":"polis-ds"}

# Public key endpoint
curl "$DS_URL/v1/sites/public-key"
# → {"public_key":"ssh-ed25519 AAAA...","key_id":"ds-primary"}

# Stream health (confirms database connectivity)
curl "$DS_URL/v1/stream/health"
# → {"status":"ok","latest_cursor":"0","oldest_cursor":"0","event_count":0}

# Admin endpoint (requires OPERATOR_API_KEY)
curl -H "Authorization: Bearer $OPERATOR_API_KEY" "$DS_URL/v1/admin/blocks"
# → {"blocked_domains":[],"blocked_types":[],"stream_config":{"mode":"blocklist"}}
```

If any check returns an error, check the server logs (`fly logs` on Fly.io, `docker logs` for Docker).

---

## Generate a DS Keypair

The discovery service needs an Ed25519 private key to sign service attestations (proof that a site was registered through this DS). The key is a raw 32-byte Ed25519 seed, base64-encoded.

```bash
# Generate a 32-byte random key and base64-encode it
openssl rand 32 | base64
# → e.g. "K7gNU3sdo+OL0wNhqoVWhr3g6s1xYv72ol/pe/Unols="

# Store as DS_PRIVATE_KEY
fly secrets set DS_PRIVATE_KEY="K7gNU3sdo+OL0wNhqoVWhr3g6s1xYv72ol/pe/Unols="
```

To derive the corresponding public key (for `DISCOVERY_SERVICE_PUBLIC_KEY`), you can use the DS's own endpoint after deploying, or compute it offline with a script that calls `ed25519.getPublicKey()` on the raw bytes.

---

## Schema Migrations

The schema file is at `discovery-service/schema/postgres.sql`. It uses `CREATE TABLE IF NOT EXISTS` and `CREATE INDEX IF NOT EXISTS`, so it is safe to run on every boot — the server does this automatically at startup.

If you need to run it manually (e.g. before first deploy or when debugging):

```bash
psql "$DATABASE_URL" < schema/postgres.sql
```
