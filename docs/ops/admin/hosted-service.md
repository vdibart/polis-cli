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
  /_/ , /_/*             The v5 owner app (session must match handle; otherwise polis.pub's
                         sign-in, or a redirect to the public equivalent)
  /v1/wake               Wake the tenant's server (public, 204)
  /v1/*                  The site's API, forwarded to the tenant server, which authenticates
                         each request itself (session, token, or a signed DM delivery)
  /api/*, /pql/*         What remains under /api/ (sign-in, enrolment, recovery, the widget's
                         routes, polis.pub's account and export routes) and owner-private PQL:
                         session must match handle — except the stream shape's two public reads,
                         public PQL and /api/capabilities, which need no session
  /*                     The tenant's published site, served from its data directory
```

The `HostedService` struct implements `http.Handler`. Its `ServeHTTP` inspects the `Host` header to decide routing: root domain requests go to the landing/auth handlers, subdomain requests go to tenant handlers.

### Key Components

All hosted service code lives in `webapp/internal/hosted/`:

| File | Purpose |
|------|---------|
| `hosted.go` | `HostedService` struct, top-level HTTP routing, SPA serving, tenant handler loading |
| `handlers.go` | Auth handlers (signup, login, verify, logout), site provisioning logic |
| `store.go` | Postgres persistence; the service refuses to start without a Postgres `DATABASE_URL` (`hosted_tenants`, `hosted_sessions`, `hosted_magic_links`, `hosted_widget_tokens`, `hosted_pending_blessings`, `hosted_email_history`) |
| `email.go` | Resend API client for transactional email: magic links, sign-in codes, and the notices an owner is sent (an agent connected, a passkey added or removed, the old address told when the sign-in email changes), each with a plain-text part |
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

The hosted service imports the same `server.NewServer()` used by the standalone webapp. Each tenant gets its own `Server` instance with `dataDir` set to `{DATA_DIR}/tenants/{handle}`. The tenant server handles `/v1/*`, `/pql/*` and what remains of `/api/*`; the published site and the owner app's assets are served by the hosted service directly (the published site from the tenant's data directory, the owner app from the shared embedded filesystem).

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
Tenants set terms afterwards in the owner app, under **enhance → Terms of use**. Do not "fix" this by applying a default.

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
- **Owner app** (`/_/`): every tenant's owner app is the v5 app, whatever its shape, served from the shared embedded filesystem (`server.ServeV5Owner`, the same code every instance uses) when the session's handle matches the subdomain. Without a matching session, `/_/` redirects to polis.pub's own sign-in (there is no per-site login page), a path that names a published post or comment goes to that public page, and anything deeper goes to the site's root. A signed-in visitor at another tenant's `/_/` is sent to the same place on their own site.
- **`/api/*` and owner-private `/pql/*`**: the `polis_session` cookie is validated -- the session must exist, be non-expired, and its handle must match the subdomain. If valid, the request is forwarded to the tenant's cached `server.Server` handler. If not, a 401 JSON error is returned. What a tenant's server still serves under `/api/` is sign-in, sign-out, enrolment and recovery, the capability descriptor and the widget's token-authorised routes; anything else answers `404`. polis.pub's account and export routes (`/api/account/*`, `/api/export/*`) are answered by the hosted service itself. Two reads the stream shape's public pages call (`/api/v1/stream/focus-comment` and `/api/v1/stream/body`) and public PQL queries skip the session check. So does the capability descriptor, `/api/capabilities`: a third party reads it to learn how to talk to the site, and a `401` there would read as "this site has no API".
- **The site's API** (`/v1/*`): forwarded to the tenant's server without a session check here; the API layer authenticates each request itself (the owner's session, a token, or a signed DM delivery).

Tenant handlers are lazily loaded and cached in `TenantCache` (LRU, 500 entries; an evicted tenant's sync goroutine is stopped). Use `Cache.Invalidate(handle)` to force reload after config changes.

## Security Notes

- Sessions use 32-byte cryptographically random tokens (hex-encoded), stored as rows in `hosted_sessions`. Nothing is signed with a server secret: a session is valid only while its row exists and is unexpired, so deleting rows revokes sessions.
- Magic links expire after 15 minutes and are single-use.
- The `polis_session` cookie is `HttpOnly`, `Secure`, `SameSite=Lax`, scoped to `.polis.pub`.
- A request carrying more than one `polis_session` cookie is treated as signed out, and logged as
  `pub.polis.security.cookie_tossing`. A second cookie of that name can only have been set by another page under
  `.polis.pub`, so neither is trusted.
- No tenant can serve script from its subdomain. Every tenant is the same site as every other and shares the
  `.polis.pub` cookies, so a tenant's own `.js` or `.svg` could reach its visitors' sessions and widget tokens. The
  tenant pages' Content-Security-Policy allows script only from the tenant's own origin and `polis.pub`, and no
  write a tenant can make produces a `.js` or `.svg` file in its directory; a test drives every write path to check.
- The listener (hosted and self-hosted alike) drops a client that takes more than 10 seconds to send its request headers, and closes idle
  keep-alive connections after 120 seconds, so a slow client cannot hold connections open.
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
- Limited per client address, before anything is loaded: over the limit it returns `429` with `Retry-After`, loads nothing and triggers no sync
- A handle with no tenant is redirected to the root domain before the wake route runs, so it loads nothing and does not count against the limit
- Loads the tenant server into cache if not already loaded (starts sync goroutine)
- Triggers an immediate sync cycle via `TriggerSync()` (non-blocking, buffered channel of 1), unless the tenant is inside its wake cooldown: then it still returns `204`, and one sync is scheduled for the end of the cooldown instead
- Logs `wake.received`, with `sync` saying what the call did: `triggered`, `deferred` (a sync is now scheduled for the end of the cooldown), or `already_deferred` (one already was)

### Load

Anyone can call it, for any tenant. What a call can cost, and the two defences that bound it:
- **A cold load: the per-client limit.** A call for a tenant that is not in the cache loads its server, which starts its sync goroutine and can evict another tenant from the cache (500 entries). This is the expensive part, and the **per-client limit** bounds it: 60 wakes per minute per client address, across all tenants, checked before the cache is touched (`wake_limit.go`). A refusal logs `rate_limit.hit` with `scope: wake`.
- **A sync cycle: the per-tenant cooldown.** Every accepted call asks for one, and each cycle calls the discovery service. `TriggerSync` merges only wakes that arrive while a sync is still queued, and an empty sync takes about 42 ms, so on its own it merges almost nothing: on 2026-09-27, 70 wakes from one address ran 56 empty sync cycles in eight seconds. So a tenant has a **wake cooldown of 30 seconds** (`wakeSyncCooldown`, `wake_limit.go`): a wake triggers a sync only if no wake has triggered one for that tenant in the last 30 seconds. A wake inside the cooldown is not dropped; one sync runs when the cooldown ends, and every wake before then joins it. The cooldown belongs to the tenant, not to an address, so it holds however many addresses call: at most two wake syncs a minute per tenant.

The cooldown starts only from syncs that wakes triggered, not from the other syncs a tenant runs (at start, after a write, and when the owner reads their network and the last sync is more than two minutes old), which would otherwise swallow wakes for a tenant in use. A wake is deferred rather than dropped because no timer syncs a site: for an idle tenant, a dropped wake would wait for the next wake or the owner's next read.

Neither defence holds back the DS in practice. Its own limit, one wake per domain per 30 seconds, already spaces its wakes a cooldown apart, and a wake deferred by the cooldown arrives at most 30 seconds late. That DS limit is not itself a defence here: it constrains only the wakes the DS sends, not anyone calling `/v1/wake` directly.

The per-client limit is set from the observed rate of legitimate wakes, which all come from the DS and so from one address. Over 2026-08-26 to 2026-09-27 Axiom shows 2 `wake.received` events in total (peak 2 in one minute, 1 per tenant), and every DS registration that could trigger a wake peaked at 34 in one minute, so 60 leaves headroom without refusing the DS. `POLIS_WAKE_RATE_PER_CLIENT` overrides it if the DS's wake volume grows; watch for `rate_limit.hit` with `scope: wake` from the DS before raising it.

The client address is the one every hosted limiter uses (`clientIP`): the visitor's own address, with a Cloudflare peer replaced by the client named in `CF-Connecting-IP`.

### Self-hosted sites

Self-hosted polis sites do not implement `/v1/wake` (the standalone webapp registers no such route). The DS tolerates 404 and connection refused responses. No configuration is needed — sites that don't support the endpoint discover events when they sync: at start, after a write, and when the owner reads their network (the owner app, an agent over `/v1/query` or MCP) and the last sync is more than two minutes old.

---

## Background Actors

The hosted service runs **6** background goroutines — Patrol (hourly integrity), Medic (hourly auto-remediation), Judge (hourly trust verification), Clerk (daily state parity, which invokes Chaplain at the end of each sweep), Reaper (daily account lifecycle), and Medic's daily content-cache upkeep (its own ticker, started with Medic). ⚠️ **All six start unconditionally in `webapp/cmd/hosted/main.go`; there is no switch to disable one.** Switches change what some of them *do*: `POLIS_JUDGE_PUBLISH` (whether Judge publishes signed findings) and the `POLIS_JUDGE_*` schedule.

Before any of them starts, actor keys are projected from `POLIS_ACTOR_KEYS` / `POLIS_ACTOR_KEY_<HANDLE>` — ⛔ an absent or stale key **refuses to boot**. Two one-off passes then run at every boot, each idempotent and each **off unless switched on**: the operator's custody declarations (`POLIS_CUSTODY_DECLARE=on`) and default Rosie grants (`POLIS_ROSIE=on`). ⚠️ Rosie's decisions on comments are not a hosted goroutine: each tenant's own server makes them under that tenant's grant, and `POLIS_ROSIE` off pauses them.

For what each actor does, and what it deliberately does not do, see [Actors](../../general/concepts/actors.md). For the custody and authority rules they operate under, see [the operator's manual](operator-guide.md).

## Log Format

When `LOG_FORMAT=json` is set, each HTTP request produces a JSON line on stdout:

```json
{"ts":"2026-02-25T15:30:45Z","source":"http","request_id":"3f0c…","method":"GET","path":"/","host":"alice.polis.pub","status":200,"duration_ms":12,"client":"9b1e…","agent":"Mozilla/5.0 …"}
```

⛔ **A request line never carries the client's address.** `client` is an HMAC of the address under a
salt generated once per process and never stored (`client_identity.go`): it can tell one client from
ten thousand, and cannot say where a person reads, because this line is written for every page anyone
reads. Correlation lasts as long as the process, and each machine salts differently, so counting
distinct clients across machines over-counts. `agent` is the `User-Agent`, plain, cut at 256 bytes: it
names software, not a person. Both are omitted when empty. Security events (`source: security`) are
rare and specific, and may record the raw address. A request from a listed AI training crawler also
carries `crawler_gate` and `crawler_gate_reason`.

Health checks (`/health`) and `/.well-known/polis` fetches are excluded from logging to reduce noise
(`webapp/internal/hosted/hosted.go`, `ServeHTTP`).

**Every line shares a base schema:** `ts` (RFC 3339, UTC) and `source` (`http`, `landlord`, `security`, and on the
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
