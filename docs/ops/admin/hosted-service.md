# Polis Hosted Service

*For* [Operators](../../README.md#running-polis) · [Reviewers](../../README.md#reviewing-the-security-and-identity-design) — *Kind* [Reference](../../README.md#kinds-of-page) — *Component* [Hosted service](../README.md) — *See also* [concept](../../general/concepts/actors.md)

Multi-tenant hosting platform for polis.pub. Runs as a single Go binary (`polis-hosted`) that serves the root domain landing page, handles passwordless authentication via magic links, provisions tenant sites on signup, and routes subdomain requests to per-tenant server instances.

This page describes how the hosted service is built, by design. Its source is not in the public repository, so its configuration, build, deployment and database administration are not documented here.

## Architecture

```
polis.pub (root domain)
  GET /                  Landing page (signup/login forms)
  GET /f/<handle>        Follow page — short link (signup + auto-follow)
  GET /follow?author=x   Follow page — canonical form
  POST /auth/signup      Create signup magic link
  POST /auth/login       Create login magic link
  GET /auth/verify       Consume magic link, provision site, set session
  POST /auth/logout      Clear session
  /recover, /legal/*, /for/*, /llms.txt, widget and nav scripts, /health

{handle}.polis.pub (tenant subdomain)
  /_/ , /_/*             Owner SPA (session must match handle; otherwise a login page or a
                         redirect to the public equivalent) with __POLIS_HOSTED=true injected
  /v1/wake               Wake the tenant's server (public, 204)
  /v1/*                  Tenant server's public protocol routes
  /api/*, /pql/*         Authenticated API (session cookie must match handle) — except the
                         public stream and PQL reads, which need no session
  /*                     The tenant's published site, served from its data directory
```

The `HostedService` struct implements `http.Handler`. Its `ServeHTTP` inspects the `Host` header to decide routing: root domain requests go to the landing/auth handlers, subdomain requests go to tenant handlers.

### Key Components

All hosted service code lives in `webapp/internal/hosted/`:

| File | Purpose |
|------|---------|
| `hosted.go` | `HostedService` struct, top-level HTTP routing, SPA serving, tenant handler loading |
| `handlers.go` | Auth handlers (signup, login, verify, logout), site provisioning logic |
| `store.go` | Dual SQLite/Postgres persistence (`hosted_tenants`, `hosted_sessions`, `hosted_magic_links`, `hosted_widget_tokens`, `hosted_pending_blessings`, `hosted_email_history`) |
| `email.go` | Resend API client for transactional email (magic links) |
| `tenant_cache.go` | Per-tenant handler cache with double-checked locking and LRU eviction (500 tenants); holds each tenant's wake callback |
| `landing.go` | Landing page HTML and JS, generated (the share image is in `landing_assets.go`) |
| `follow.go` | Embedded follow page HTML (`{{AUTHOR}}` placeholder replaced at serve time) |

Also here: the actors (`patrol.go`, `medic.go`, `judge.go`, `clerk.go`, `chaplain.go`, `reaper.go`; Medic's cache upkeep is `medic_cache.go`), actor-key projection (`actorkeys.go`), custody declarations (`custody.go`), default Rosie grants (`rosie_grants.go`) and the handle policy (`handle_policy.go`).

Entry point: `webapp/cmd/hosted/main.go`

### Dependency Flow

```
cli-go/pkg/site.Init()    <--  hosted/handlers.go (provisioning)
cli-go/pkg/...             <--  server.NewServer() (tenant API handlers)
internal/server/           <--  hosted/hosted.go (loadTenantHandler)
internal/webui/            <--  cmd/hosted/main.go (embedded SPA assets)
```

The hosted service imports the same `server.NewServer()` used by the standalone webapp. Each tenant gets its own `Server` instance with `dataDir` set to `{DATA_DIR}/tenants/{handle}`. The tenant server handles `/api/*`, `/pql/*` and `/v1/*`; the published site and the SPA assets are served by the hosted service directly (the published site from the tenant's data directory, the SPA from the shared embedded filesystem).

## Tenant Lifecycle

### Signup

1. User submits handle + email on the landing page (`POST /auth/signup`).
2. Handle is validated: 4–30 lowercase letters, digits or hyphens, not starting or ending with a hyphen (`handleRegex`, `handlers.go`); then the handle policy (`handle_policy.go`) — reserved names, every 1–3 character handle, the `polis` and `test` prefixes, and a profanity check; then not already taken. Email must be unique. The endpoint is rate-limited per IP (3 per hour, shared with the other auth endpoints).
3. A magic link token is created in the database (15-minute expiry).
4. Resend API sends the magic link email.
5. User clicks the link (`GET /auth/verify?token=...`).
6. `handleVerify` consumes the token, then provisions:
   - Creates the tenant record, then calls `site.Init(tenantsDir/handle)` to create the site directory, generate Ed25519 keys, and write `.well-known/polis`.
   - Writes `.env` with `POLIS_BASE_URL=https://{handle}.polis.pub` and the DS URL — no DS key; all DS auth uses the site's Ed25519 signature.
   - Renders the site, registers it with the DS, and — when switched on — issues the operator's custody declaration (`POLIS_CUSTODY_DECLARE`) and the tenant's default Rosie grant (`POLIS_ROSIE`).
   - Follows the invited author (follow signups) and `discover.polis.pub`.
7. The tenant is marked verified, a session is created (1-week expiry), a `polis_session` cookie is set on `.polis.pub`, and the user is redirected to their site.

⚠️ **Provisioning states no licence terms, and this is deliberate.** `site.Init` is called with no
`License`, so a new tenant publishes no `license.json`, no `robots.txt` and no `rsl.xml` — byte-identical
to a site created before terms shipped. Signup does not prompt for terms either: licence vocabulary
mid-registration is a bounce risk, and a signed statement of intent may only come from the author.
Tenants set terms afterwards in **Settings → Terms of use**. Do not "fix" this by applying a default.

### Login

1. User submits email (`POST /auth/login`).
2. If the email matches a tenant, a magic link is created and emailed. The response is identical whether the email exists or not (no enumeration).
3. User clicks the link, `handleVerify` creates a session and redirects.

### Follow

1. An author shares their follow link: `https://polis.pub/f/alice` (short form) or `https://polis.pub/follow?author=alice.polis.pub` (canonical).
2. The follow page renders with the author pre-filled.
3. The visitor submits handle + email. The signup flow runs with `purpose=follow`.
4. On provisioning, `addFollowing` appends the author to the new site's `following.json`.
5. If the visitor already has an account, the form auto-switches to login with the follow intent preserved.

### Tenant Request Routing

When a request arrives at `{handle}.polis.pub`:

- **Public paths** (`/`, `/posts/...`, etc.): The tenant's rendered site is served from its data directory, rate-limited per IP. A signed-in visitor from another tenant gets the nav widget injected.
- **Owner SPA** (`/_/`): served from the shared embedded filesystem when the session's handle matches the subdomain; the HTML gets a nonce-bearing `<script>` setting `window.__POLIS_HOSTED=true` and `window.__POLIS_BASE_DOMAIN`. Without a matching session, `/_/` shows the tenant login page and deeper SPA paths redirect.
- **API paths** (`/api/*`, and `/pql/*` owner queries): The `polis_session` cookie is validated -- the session must exist, be non-expired, and its handle must match the subdomain. If valid, the request is forwarded to the tenant's cached `server.Server` handler. If not, a 401 JSON error is returned. The public stream reads (`/api/v1/stream/focus-comment` and `/api/v1/stream/body`) and public PQL queries skip the session check. (`/api/v1/stream/items` was retired when the stream moved to PQL; its data path is `GET /pql/<sentence>`.)
- **Protocol paths** (`/v1/*`): forwarded to the tenant's server without a session.

Tenant handlers are lazily loaded and cached in `TenantCache` (LRU, 500 entries; an evicted tenant's sync goroutine is stopped). Use `Cache.Invalidate(handle)` to force reload after config changes.

## Security Notes

- Sessions use 32-byte cryptographically random tokens (hex-encoded), stored as rows in `hosted_sessions`. Nothing is signed with a server secret: a session is valid only while its row exists and is unexpired, so deleting rows revokes sessions.
- Magic links expire after 15 minutes and are single-use.
- The `polis_session` cookie is `HttpOnly`, `Secure`, `SameSite=Lax`, scoped to `.polis.pub`.
- API requests require the session handle to match the subdomain -- a user authenticated as `alice` cannot call the API on `bob.polis.pub`.
- Because the cookie is scoped to `.polis.pub`, every tenant is the same *site* as every other, and `SameSite=Lax` does not stop one tenant's page sending a request to another's API. So a state-changing `/api/*` request whose `Origin` is not the tenant's own, or whose `Sec-Fetch-Site` is anything but `same-origin` or `none`, is refused with `403` before the session is read. The widget routes (`/api/widget/*`, other than `connect` and `token`) are exempt: they authenticate with a widget token, never the cookie.
- Handle validation rejects reserved subdomains — every 1–3 character handle (`www`, `api`, `ds`, `app`, `ftp` …) plus named ones such as `admin`, `auth`, `static`, `assets`, `discover`, `validate`, `help`, `about`, `blog`, `mail`, `smtp` (`handle_policy.go`).
- A session is created only for an **active** tenant, in one statement, so a tenant reaped mid-login cannot get one (`store.go`, `CreateSession`). Confirming an email change deletes that tenant's sessions.
- Login responses do not reveal whether an email is registered. **Signup does**: an address that already has an account gets `409` "This email is already registered. Use login instead.", and a taken handle gets `409` too.

## Wake Endpoint

Each tenant exposes a public `GET /v1/wake` endpoint that the DS calls after emitting events affecting that tenant (new blessing requests for Rosie to decide, blessing decisions, comment notifications).

### Behavior

- Returns `204 No Content` — no data is exposed
- No authentication required
- Loads the tenant server into cache if not already loaded (starts sync goroutine)
- Triggers an immediate sync cycle via `TriggerSync()` (non-blocking, buffered channel of 1)
- Logs `wake.received`
- Multiple rapid wake calls collapse into a single sync cycle

### Load

Anyone can call it, as often as they like, for any tenant, and nothing limits a caller today:
- A call for a tenant that is not in the cache loads its server, which starts its sync goroutine and can evict another tenant from the cache (500 entries).
- Every call asks for a sync cycle. At most one waits per tenant (`TriggerSync`'s channel holds one), so a steady stream of calls keeps that tenant syncing back to back, and each cycle calls the discovery service.
- The discovery service's own limit, one wake per domain per 30 seconds, applies only to the wakes it sends.

A per-client limit on `/v1/wake` is planned.

### Self-hosted sites

Self-hosted polis sites do not implement `/v1/wake` (the standalone webapp registers no such route). The DS tolerates 404 and connection refused responses. No configuration is needed — sites that don't support the endpoint continue to discover events through their regular polling cycle (every 30 seconds while a browser tab is connected).

---

## Background Actors

The hosted service runs **6** background goroutines — Patrol (hourly integrity), Medic (hourly auto-remediation), Judge (hourly trust verification), Clerk (daily state parity, which invokes Chaplain at the end of each sweep), Reaper (daily account lifecycle), and Medic's daily content-cache upkeep (its own ticker, started with Medic). ⚠️ **All six start unconditionally in `webapp/cmd/hosted/main.go`; there is no switch to disable one.** Switches change what some of them *do*: `POLIS_JUDGE_PUBLISH` (whether Judge publishes signed findings) and the `POLIS_JUDGE_*` schedule.

Before any of them starts, actor keys are projected from `POLIS_ACTOR_KEYS` / `POLIS_ACTOR_KEY_<HANDLE>` — ⛔ an absent or stale key **refuses to boot**. Two one-off passes then run at every boot, each idempotent and each **off unless switched on**: the operator's custody declarations (`POLIS_CUSTODY_DECLARE=on`) and default Rosie grants (`POLIS_ROSIE=on`). ⚠️ Rosie's decisions on comments are not a hosted goroutine: each tenant's own server makes them under that tenant's grant, and `POLIS_ROSIE` off pauses them.

For what each actor does, and what it deliberately does not do, see [Actors](../../general/concepts/actors.md). For the custody and authority rules they operate under, see [the operator's manual](operator-guide.md).

## Log Format

When `LOG_FORMAT=json` is set, each HTTP request produces a JSON line on stdout:

```json
{"ts":"2026-02-25T15:30:45Z","source":"http","request_id":"3f0c…","method":"GET","path":"/","host":"alice.polis.pub","status":200,"duration_ms":12}
```

Health checks (`/health`) and `/.well-known/polis` fetches are excluded from logging to reduce noise
(`webapp/internal/hosted/hosted.go`, `ServeHTTP`).

**Every line shares a base schema:** `ts` (RFC 3339, UTC) and `source` (`http`, `landlord`, and on the
discovery service `api`, `admin`, `auth`, `system`, `perf`, `security`, `db`). Named events carry
their name in `action`. ⭐ **Nothing in the services knows where the logs go**, so any log aggregator works.

### Correlation IDs

An operation that crosses into the discovery service is joined across logs by `request_id`, sent as
the `X-Request-Id` header. The DS reads it, or generates a UUID when it is absent, and stamps it on
every line it logs for that request.

On `polis-hosted`, every request gets one at the edge (`server.WithRequestID`, called from `ServeHTTP`): an inbound `X-Request-Id` is kept when it is well formed, and otherwise a UUID is generated.

| Origin | `request_id` |
|---|---|
| **HTTP request lines** (`source: http`) | ✅ the request's id |
| **Tenant requests forwarded to a tenant server** | ✅ the same id, carried in the request context, so a DS call made while serving one sends it as `X-Request-Id` |
| **Chaplain repairs** | ✅ one per operation (`chaplain-<kind>-<handle>-<ms>`), on the DS request **and** on Chaplain's own events |
| **Signup provisioning** | ✅ `provision-<handle>-<ms>` on the DS registration call |

The standalone webapp (`polis-full serve`, `polis-server`) and the CLI also generate one per request or command
(`webapp/internal/server/middleware.go`, `requestLoggingMiddleware`).
