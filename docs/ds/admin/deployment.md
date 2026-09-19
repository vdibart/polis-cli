# Discovery Service Deployment Guide

*For* [Operators](../../README.md#running-polis) — *Kind* [Reference](../../README.md#kinds-of-page) — *Component* [Discovery service](../README.md)

What a Polis Discovery Service deployment consists of, and how to verify one.

**Target audience:** Operators running their own Discovery Service.

⚠️ **The Discovery Service source is not in the public repository**, so the step-by-step deployment procedures are
not published here. The sections below say what each part of a deployment is. They return in full when the DS source
is published.

---

## Prerequisites

- **PostgreSQL 14+** — any provider works (Fly Postgres, Supabase, RDS, self-hosted)
- **Deno 2.x** — the server runtime (bundled in the Docker image)
- **An Ed25519 keypair** — the DS signs service attestations with this key (see [Generate a DS Keypair](#generate-a-ds-keypair))

---

## Required Environment Variables

A deployment needs four things to start: a PostgreSQL connection string, the DS's Ed25519 private key, the public URL
the DS is served at, and one or more operator API keys for the admin endpoints under `/v1/admin/` (see
[Admin API](configuration.md#admin-api)). Optional variables set the listen port and the public key the DS serves.
Everything else is [tuning](configuration.md#tuning-reference).

---

## Deploy on Fly.io

polis.pub's discovery service runs on Fly.io. The procedure is not published while the DS source is closed.

---

## Deploy with Docker

The DS runs as a container alongside a PostgreSQL database. The procedure is not published while the DS source is
closed.

---

## Deploy on Bare Deno

The DS can run directly under Deno on any host with PostgreSQL available. The procedure is not published while the DS
source is closed.

---

## Verify Your Deployment

After deploying, run these checks:

```bash
DS_URL="https://your-ds.example.com"

# Health check (liveness only — does not touch the database)
curl "$DS_URL/health"
# → {"status":"ok","service":"polis-ds"}

# Readiness (pings the database; 503 with "not_ready" if it does not answer)
curl "$DS_URL/ready"
# → {"status":"ready"}

# Public key endpoint (public_key is "" unless DISCOVERY_SERVICE_PUBLIC_KEY is set)
curl "$DS_URL/v1/sites/public-key"
# → {"public_key":"<DISCOVERY_SERVICE_PUBLIC_KEY>","key_id":"ds-primary"}

# Stream health (on a fresh database)
curl "$DS_URL/v1/stream/health"
# → {"status":"ok","latest_cursor":"0","oldest_cursor":"0","event_count":0}

# Admin endpoint (requires OPERATOR_API_KEY)
curl -H "Authorization: Bearer $OPERATOR_API_KEY" "$DS_URL/v1/admin/policies"
# → {"policies":[ ...the seven rules the schema seeds on a fresh install... ],"cursor":"7","has_more":false}
```

If any check returns an error, check the server logs (`fly logs` on Fly.io, `docker logs` for Docker).

---

## Generate a DS Keypair

The discovery service signs service attestations — proof that a site was registered through this DS — with an Ed25519
key, supplied as a raw 32-byte seed, base64-encoded. The matching public key, when it is configured, is what the DS
serves at `GET /v1/sites/public-key`.

---

## Schema Migrations

The DS applies its schema idempotently at every startup, so a fresh database needs no manual migration step.
