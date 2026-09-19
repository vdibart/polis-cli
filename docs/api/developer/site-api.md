# Site API (webapp)

*For* [Developers](../../README.md#building-on-polis) — *Kind* [Reference](../../README.md#kinds-of-page) — *Component* [Webapp](../../webapp/README.md) — *Code* [`webapp/internal/server/routes.go`](../../../webapp/internal/server/routes.go)

The webapp's own endpoints under `/api/`. The web app calls them for everything it shows its owner, and
a few of them are **site operations**: settings, registration, identity, terms and following, which a
script may call too.

> **The webapp serves three HTTP surfaces, and this page is one of them.**
>
> | Prefix | What | Documented |
> |---|---|---|
> | `/v1/` | The **Content Type API**: posts, comments, follows, DMs, tags. Public reads, bearer-token writes | [`reference.md`](reference.md) |
> | `/api/` | **The web app's own endpoints**: site operations, and the plumbing the web app runs on | this page |
> | `/pql/<sentence>` | The stream's data path | [the PQL reference](../../general/reference/pql.md) |
>
> ⚠️ `/api/` has its own DM routes (`/api/dm/*`) and its own tag route (`/api/tags`). Those are the web
> app's, and they are internal. **From a script, use `pub.polis.dm` and `pub.polis.tag` on `/v1/`.**

⛔ **A route documented in an API reference is a contract, so only the site operations are written up
here.** Every other route is in the [index](#route-index) with its method and auth mode, and is marked
⚠️ **internal**: *the web app uses it itself, and it may change without notice.* Do not build on an
internal route.

## Authentication

| Mode | How the caller is authorised |
|---|---|
| **owner** | **Localhost:** nothing per request. The webapp binds to `localhost` and has no login, so if you can reach the port you are the owner, by design (see [the security model](../../general/security/security-model.md)). **Hosted:** a session cookie whose handle matches the tenant. Anything else gets `401` with the body `{"error": "unauthorized"}` |
| **public** | No authentication; rate-limited per IP |
| **session** | Hosted only: the tenant's session cookie |
| **widget token** | `Authorization: Bearer <token>`, issued to the embeddable widget. Cross-origin |

## Conventions

- **Bodies are limited to 1 MiB** unless a route says otherwise. An oversized body fails to decode and
  returns that route's `400`.
- **Error bodies are plain text** (`Content-Type: text/plain`) unless a route says otherwise. That
  includes hosted's `401` and `403`, whose text happens to be JSON.
- **A method a route does not accept gets `405` with `Method not allowed`**, as plain text.
- **Success bodies are JSON.** Most carry `"success": true`; `/api/site/license` and
  `/api/settings/rosie` carry `"status": "success"` instead.
- **Several routes sign with the site's key.** Each write-up says so. Nothing else on this page signs.

---

## Route index

Every `/api/` route the webapp serves: **104** in all, 97 registered in
[`routes.go`](../../../webapp/internal/server/routes.go) and 7 that only the hosted service answers.

**Columns.** *Auth* uses the modes above. *Where* is `both`, `local` (localhost webapp only) or
`hosted` (the hosted service only). *Audience* is **site op**, a site operation written up below that
a script may rely on, or ⚠️ **internal**, which the web app uses itself and **may change without
notice**.

### Site and identity

| Route | Method | Auth | Where | Audience |
|---|---|---|---|---|
| `/api/status` | GET | owner | both | **[site op](#get-apistatus)** |
| `/api/validate` | GET | owner | both | **[site op](#get-apivalidate)** |
| `/api/settings` | GET | owner | both | **[site op](#get-apisettings)** |
| `/api/settings/avatar` | POST | owner | both | **[site op](#post-apisettingsavatar)** |
| `/api/settings/author-name` | POST | owner | both | **[site op](#post-apisettingsauthor-name)** |
| `/api/settings/theme` | POST | owner | both | **[site op](#post-apisettingstheme)** |
| `/api/settings/rosie` | GET, POST | owner | both | **[site op](#get-post-apisettingsrosie)** |
| `/api/settings/show-frontmatter` | POST | owner | both | ⚠️ internal |
| `/api/settings/hide-read` | POST | owner | both | ⚠️ internal |
| `/api/settings/webapp-theme` | POST | owner | both | ⚠️ internal |
| `/api/settings/editor-panel-mode` | POST | owner | both | ⚠️ internal |
| `/api/about` | GET, POST | owner | both | **[site op](#get-post-apiabout)** |
| `/api/rotate-key` | POST | owner | both | **[site op](#post-apirotate-key)** |
| `/api/download-site` | GET | owner | both (hosted: `403`) | **[site op](#get-apidownload-site)** |
| `/api/site/registration-status` | GET | owner | both | **[site op](#get-apisiteregistration-status)** |
| `/api/site/register` | POST | owner | both | **[site op](#post-apisiteregister)** |
| `/api/site/unregister` | POST | owner | both | **[site op](#post-apisiteunregister)** |
| `/api/site/deploy-check` | GET | owner | both | **[site op](#get-apisitedeploy-check)** |
| `/api/site/license` | POST | owner | both | **[site op](#post-apisitelicense)** |
| `/api/site/setup-wizard-dismiss` | POST | owner | both | ⚠️ internal |
| `/api/init` | POST | owner | both | ⚠️ internal |
| `/api/link` | POST | owner | both | ⚠️ internal |

### Content

To publish from a script, use the [Content Type API](reference.md).

| Route | Method | Auth | Where | Audience |
|---|---|---|---|---|
| `/api/posts` | GET | owner | both | ⚠️ internal |
| `/api/posts/` | GET | owner | both | ⚠️ internal |
| `/api/render` | POST | owner | both | ⚠️ internal |
| `/api/publish` | POST | owner | both | ⚠️ internal |
| `/api/republish` | POST | owner | both | ⚠️ internal |
| `/api/unpublish` | POST | owner | both | ⚠️ internal |
| `/api/drafts` | GET, POST | owner | both | ⚠️ internal |
| `/api/drafts/` | GET, DELETE | owner | both | ⚠️ internal |
| `/api/content/` | GET | owner | both | ⚠️ internal |
| `/api/templates` | GET | owner | both | ⚠️ internal |
| `/api/snippets` | GET, POST | owner | both | ⚠️ internal |
| `/api/snippets/` | GET, PUT, DELETE | owner | both | ⚠️ internal |
| `/api/render-page` | POST | owner | both | ⚠️ internal |
| `/api/tags` | GET, POST, DELETE | owner | both | ⚠️ internal |

### Comments and blessings

| Route | Method | Auth | Where | Audience |
|---|---|---|---|---|
| `/api/comments/drafts` | GET, POST | owner | both | ⚠️ internal |
| `/api/comments/drafts/` | GET, DELETE | owner | both | ⚠️ internal |
| `/api/comments/sign` | POST | owner | both | ⚠️ internal |
| `/api/comments/beseech` | POST | owner | both | ⚠️ internal |
| `/api/comments/pending` · `/api/comments/pending/` | GET | owner | both | ⚠️ internal |
| `/api/comments/blessed` · `/api/comments/blessed/` | GET | owner | both | ⚠️ internal |
| `/api/comments/denied` · `/api/comments/denied/` | GET | owner | both | ⚠️ internal |
| `/api/comments/sync` | POST | owner | both | ⚠️ internal |
| `/api/blessing/requests` | GET | owner | both | ⚠️ internal |
| `/api/blessing/grant` | POST | owner | both | ⚠️ internal |
| `/api/blessing/deny` | POST | owner | both | ⚠️ internal |
| `/api/blessing/revoke` | POST | owner | both | ⚠️ internal |
| `/api/blessed-comments` | GET | owner | both | ⚠️ internal |
| `/api/comment/blessing/viewed` | POST | owner | both | ⚠️ internal |

### Network, feed and notifications

| Route | Method | Auth | Where | Audience |
|---|---|---|---|---|
| `/api/following` | GET, POST, DELETE | owner | both | **[site op](#get-post-delete-apifollowing)** |
| `/api/profiles` | GET | owner | both | ⚠️ internal |
| `/api/followers/count` | GET | owner | both | ⚠️ internal |
| `/api/feed` | GET | owner | both | ⚠️ internal |
| `/api/feed/refresh` | POST | owner | both | ⚠️ internal |
| `/api/feed/read` | POST | owner | both | ⚠️ internal |
| `/api/feed/viewed` | POST | owner | both | ⚠️ internal |
| `/api/feed/counts` | GET | owner | both | ⚠️ internal |
| `/api/feed/grouped` | GET | owner | both | ⚠️ internal |
| `/api/remote/avatar` | GET | owner | both | ⚠️ internal |
| `/api/remote/post` | GET | owner | both | ⚠️ internal |
| `/api/pulse` | GET | owner | both | ⚠️ internal |
| `/api/conversations` | GET | owner | both | ⚠️ internal |
| `/api/notifications` | GET | owner | both | ⚠️ internal |
| `/api/notifications/count` | GET | owner | both | ⚠️ internal |
| `/api/notifications/read` | POST | owner | both | ⚠️ internal |

### Direct messages

For DMs from a script, use `pub.polis.dm` on the [Content Type API](reference.md#pubpolisdm).

| Route | Method | Auth | Where | Audience |
|---|---|---|---|---|
| `/api/dm/keyring` | GET | owner | both | ⚠️ internal |
| `/api/dm/password` | POST | owner | both | ⚠️ internal |
| `/api/dm/password/rewrap` | POST | owner | both | ⚠️ internal |
| `/api/dm/conversations` | GET | owner | both | ⚠️ internal |
| `/api/dm/conversations/` | GET, DELETE | owner | both | ⚠️ internal |
| `/api/dm/send` | POST | owner | both | ⚠️ internal |
| `/api/dm/send-sealed` | POST | owner | both | ⚠️ internal |
| `/api/dm/recipient-key` | GET | owner | both | ⚠️ internal |
| `/api/dm/protection-status` | GET | owner | both | ⚠️ internal |
| `/api/dm/mark-read` | POST | owner | both | ⚠️ internal |
| `/api/dm/retry` | POST | owner | both | ⚠️ internal |
| `/api/dm/recipients` | GET | owner | both | ⚠️ internal |
| `/api/dm/viewed` | POST | owner | both | ⚠️ internal |

### The stream, and the web app's own plumbing

| Route | Method | Auth | Where | Audience |
|---|---|---|---|---|
| `/api/v1/stream/focus-comment` | GET | public | both | ⚠️ internal |
| `/api/v1/stream/body` | GET | public | both | ⚠️ internal |
| `/api/v1/event` | POST | owner | both | ⚠️ internal |
| `/api/v1/bundle/notification-rules` | GET | owner | both | ⚠️ internal |
| `/api/sse` | GET | owner | both | ⚠️ internal |
| `/api/counts` | GET | owner | both | ⚠️ internal |
| `/api/nav/state` | GET | owner; on hosted, a widget token or a session, answered by the hosted service | both | ⚠️ internal |

### Automations (localhost only)

Hooks run scripts on the server, so the hosted service does not register these routes.

| Route | Method | Auth | Where | Audience |
|---|---|---|---|---|
| `/api/automations` | GET, POST | owner | local | ⚠️ internal |
| `/api/automations/quick` | POST | owner | local | ⚠️ internal |
| `/api/automations/` | DELETE | owner | local | ⚠️ internal |
| `/api/hooks/generate` | POST | owner | local | ⚠️ internal |

### The embeddable widget

| Route | Method | Auth | Where | Audience |
|---|---|---|---|---|
| `/api/widget/connect` | GET | owner; on hosted, a session, answered by the hosted service | both | ⚠️ internal |
| `/api/widget/token` | GET | session | hosted | ⚠️ internal |
| `/api/widget/publish` | POST | widget token | both | ⚠️ internal |
| `/api/widget/comment` | POST | widget token | both | ⚠️ internal |
| `/api/widget/follow` | POST, DELETE | widget token | both | ⚠️ internal |
| `/api/widget/signup-and-publish` | POST | none: creates an account from an email address | hosted, apex domain | ⚠️ internal |
| `/api/widget/signup-and-follow` | POST | none: creates an account from an email address | hosted, apex domain | ⚠️ internal |

### Hosted account

| Route | Method | Auth | Where | Audience |
|---|---|---|---|---|
| `/api/account/info` | GET | session | hosted | ⚠️ internal |
| `/api/account/change-email` | POST | session | hosted | ⚠️ internal |
| `/api/export/request` | POST | session | hosted | ⚠️ internal |
| `/api/export/download` | GET | the one-time link that `/api/export/request` emails | hosted | ⚠️ internal |

---

## Site operations

### `GET /api/status`

Whether the site is set up, and the basics about it. No side effects.

**Response** — `200`

| Field | Type | |
|---|---|---|
| `configured` | bool | `true` when `validation.status` is `valid` |
| `site_title` · `base_url` | string | |
| `validation` | object | `{status, errors}`, as [`/api/validate`](#get-apivalidate) returns |
| `site_info` | object | present when the site could be read |
| `show_frontmatter` | bool | |
| `avatar` · `author_name` · `active_theme` | | present only when set |

### `GET /api/validate`

Checks that the site directory is a valid polis site. No side effects.

**Response** — `200`, always, whatever the result:

```json
{
  "status": "valid",
  "errors": [{ "code": "…", "message": "…", "path": "…", "suggestion": "…" }],
  "site_info": { "site_title": "…", "public_key": "…", "version": "…", "created": "…", "active_theme": "…" }
}
```

`status` is `valid`, `not_found`, `incomplete` or `invalid`. `errors` is absent when empty. Within each
error, `path` and `suggestion` are optional, and every `site_info` field is optional.

### `GET /api/settings`

Everything the Settings screen shows. No side effects.

**Response** — `200`

| Field | Type | |
|---|---|---|
| `site` | object | `subdomain`, `site_title`, `public_key` (`""` when there is no key), `discovery_url`, `discovery_configured`, `show_frontmatter`, `base_url`, `avatar` (object or `null`), `author_name` |
| `license` | object | always has `stated`; when `true`, also `profile`, `content_usage`, `summary`, `terms_url`, `asserted` (as in [`/api/site/license`](#post-apisitelicense)) |
| `rosie` | object | as in [`/api/settings/rosie`](#get-post-apisettingsrosie) |
| `active_theme` | string | |
| `themes` | array | the themes a user may select for the active shape |
| `automations` · `existing_hooks` | array or `null` | ⚠️ always `null` on hosted, where hooks do not run |
| `setup_wizard_dismissed` · `hide_read` · `webapp_theme` · `editor_panel_mode` | | web app display preferences |

### `POST /api/settings/avatar`

Set or remove the generated avatar. Writes `.well-known/polis` and regenerates `favicon.svg`. **Signs
nothing.**

**Request**

```json
{ "avatar": { "bg": "#1f2937", "fg": "#f9fafb", "border": "#f59e0b", "border_w": 2, "pattern": "dots", "pattern_color": "#374151" } }
```

`"avatar": null` removes it. `bg` and `fg` are required `#RRGGBB` colours. `border` and `pattern_color`
are optional `#RRGGBB`. `border_w` is 0 to 3. `pattern` must be one of the patterns the web app offers.
Members not listed here are dropped.

**Response** — `200` `{"success": true, "avatar": {…} | null}`

| Status | When |
|---|---|
| `400` | Body is not JSON, or `Invalid avatar: <reason>` |
| `500` | The identity document could not be written |

⚠️ **Any change to `.well-known/polis` is visible to monitoring.** On hosted, expect one
`judge.alert.wellknown_change` for the tenant.

### `POST /api/settings/author-name`

Set the display name. Writes `.well-known/polis`. **Signs nothing.**

**Request** `{"author_name": "Alice Example"}`. It is trimmed, and must be at most **50 bytes** (not
characters, so a name with accented or non-Latin letters reaches the limit sooner). An empty string
clears it.

**Response** — `200` `{"success": true, "author_name": "Alice Example"}`

| Status | When |
|---|---|
| `400` | Body is not JSON, or the name is too long |
| `500` | The identity document could not be written |

### `POST /api/settings/theme`

Switch the site's theme, then re-render the whole site.

**Request** `{"theme": "pub.polis.themes.vice"}`, required.

**Response** — `200` `{"success": true, "theme": "pub.polis.themes.vice"}`

| Status | When |
|---|---|
| `400` | Body is not JSON; `theme` is missing; the theme is reserved (`sols`, `stardust`); or the theme is unknown |
| `500` | The theme could not be listed, set or copied |

⚠️ **The unknown-theme check is against every installed theme**, while `GET /api/settings` lists only
those for the active shape. A theme can be accepted here that the settings list does not offer. A
render failure after the switch is logged, not returned.

### `GET, POST /api/settings/rosie`

Read or switch Rosie, the site's own agent, which decides blessing requests under the owner's rules
([delegation](../../signet/spec/delegation.md)). **POST signs with the site's key.**

**`GET` response** — `200` `{"rosie": {…}}`

| Field | |
|---|---|
| `text` | the wording the web app shows about Rosie |
| `on` · `never` · `not_started` | bools. `not_started` is `true` when the operator has not started Rosie on this deployment; the owner's choice is still saved |
| `history` | the site's Rosie grants; `[]` when there are none |
| `live` | present only while a grant is in force |
| `off_since` | present only when Rosie has been switched off |
| `error` | present only when Rosie's records could not be read; `on` is then `false` |

**`POST` request** `{"on": true}` or `{"on": false}`. `on` is required.

**`POST` response** — `200` `{"status": "success", "rosie": {…}}`, the same object as `GET`.

| Status | When |
|---|---|
| `400` | `on` is missing or not a bool |
| `503` | The site has no key to sign with |
| `500` | The grant or withdrawal could not be issued. Switching on also needs `POLIS_BASE_URL` |

**Side effects.** `on: true` issues a signed `pub.polis.attestation.grant`, unless a valid one already
stands, and registers it with the discovery service when the site is registered. `on: false` signs a
withdrawal of every standing grant and registers each one. Both regenerate `agents.json`.

### `GET, POST /api/about`

The site's About text. **POST** writes `site/snippets/about.md` and re-renders the site. **Body
limit: 64 KiB of content** (the route accepts 69,632 bytes of request).

**`GET` response** — `200` `{"content": "…", "content_html": "…", "has_custom": true}`. With no custom
text, `content` is the default and `has_custom` is `false`.

**`POST` request** `{"content": "Markdown…"}`

**`POST` response** — `200` `{"success": true}`

| Status | When |
|---|---|
| `400` | Body is not JSON |
| `413` | The request or the content is over the limit |
| `500` | The file could not be written |

### `POST /api/rotate-key`

Replace the site's identity key. **Signs with both keys**, and is not reversible. No request body.

**Response** — `200` `{"success": true, "public_key": "ssh-ed25519 …", "key_history_epoch": 2}`

**What it does, in order:**

1. Generates a new key pair.
2. Signs the handover with the **old** key.
3. Asks the discovery service to record the rotation. **If the service refuses, nothing local has
   changed** (`502`).
4. Writes the new keys. No copy of the old private key is kept.
5. Updates `public_key` and appends to `public_key_history` in `.well-known/polis`
   ([key history](../../signet/spec/key-history.md)).
6. Re-signs the DM messages-key block with the new key, if the site has a DM keyring.
7. Republishes `did.json`, and re-renders the site.

| Status | When |
|---|---|
| `400` | There are no keys, or `POLIS_BASE_URL` is unset or unusable |
| `502` | The discovery service rejected the rotation. Nothing was changed |
| `500` | A key could not be generated or signed, or a file could not be written. ⚠️ **A `500` after step 3 can leave the service and the site disagreeing.** Before retrying, compare the service's record of the site's keys (`GET /v1/sites/keys/history?domain=<domain>` on the discovery service) with `public_key_history` in `.well-known/polis` |

⚠️ **Everything signed before the rotation keeps verifying** through `public_key_history`. A client
that rotates any other way must append to that history, or the site's earlier signatures stop resolving.

### `GET /api/download-site`

The whole site directory as a zip, **private key included**. Only `.polis/logs` is left out.

**Response** — `200`, `Content-Type: application/zip`, `Content-Disposition: attachment;
filename="polis-site.zip"`, streamed.

| Status | When |
|---|---|
| `429` | Another download started less than 10 minutes ago (the limit is per server process) |
| `403` | **Hosted, always.** The body is `{"error": "use verified export"}`. Hosted sites export through an emailed, one-time link instead (`/api/export/request`) |

⚠️ **Files over 50 MiB are skipped, and the archive stops at 500 MiB.** The response has already
started by then, so a very large site gets a **truncated zip with a `200`**. Check the archive.

### `GET /api/site/registration-status`

Whether the discovery service knows this site. Makes one call to the service. **Always `200`**: every
outcome is in the body.

| Body | Means |
|---|---|
| `{"configured": false, "error": …}` | No discovery service is configured |
| `{"configured": true, "error": …}` | `POLIS_BASE_URL` is unset or has no domain |
| `{"configured": true, "domain": …, "error": …}` | The service could not be asked, or its answer did not verify |
| `{"configured": true, "domain": …, "is_registered": bool, "created_at": …, "registry_url": …}` | The answer |

### `POST /api/site/register`

Register the site with the discovery service. **Signs with the site's key.** No request body.

**Response** — `200` `{"success": bool, "domain": …, "created_at": …, "registry_url": …, "reconciled_follows": 3}`

**Side effects.** Sends a signed registration and writes the service's attestation to
`.polis/ds/<service>/registration.json`. Then re-announces the site's follows, each signed. A failure to
write the marker or re-announce a follow is logged and does not fail the call.

| Status | When |
|---|---|
| `400` | No discovery service is configured, there is no private key, or `POLIS_BASE_URL` is unset or has no domain |
| `500` | The discovery service refused or could not be reached |

### `POST /api/site/unregister`

Remove the site from the discovery service. **Signs with the site's key.** No request body.

**Response** — `200` `{"success": bool, "domain": …, "message": …}`. Removes the local registration
marker.

| Status | When |
|---|---|
| `400` | As for `register` |
| `403` | The domain ends in `.polis.pub`: sites hosted there cannot unregister |
| `500` | The discovery service refused or could not be reached |

### `GET /api/site/deploy-check`

Whether the site is live at its own address: fetches `https://<domain>/.well-known/polis`. No writes.
**Always `200`.**

**Response** `{"deployed": bool, "domain": "alice.example"}`. `deployed` is `true` only when that fetch
answered `200`. When `POLIS_BASE_URL` is unset or has no domain, the body also carries `error`.

### `POST /api/site/license`

State or withdraw the terms under which the site's work may be used. **Signs with the site's key.**
*(Code: [`license.go`](../../../webapp/internal/server/license.go).)*

**Request**

```json
{ "profile": "reserved" }
```

| `profile` | Effect |
|---|---|
| `reserved` | `train-ai=n search=y ai-input=n attribution=required` |
| `open` | `train-ai=y search=y ai-input=y` |
| `none` (or `unstated`) | Withdraw — the site states nothing from here on |

Names are matched case-insensitively. **Withdrawal must be named:** an empty or missing `profile` is refused with `400` and changes nothing.

**Response** — `200`

```json
{
  "status": "success",
  "license": {
    "stated": true,
    "profile": "pub.polis.license.reserved/1",
    "content_usage": "train-ai=n, search=y",
    "summary": "Read and quote freely with a link back…",
    "terms_url": "https://alice.example/license",
    "asserted": "2026-08-28T09:15:00Z"
  }
}
```

After a withdrawal the `license` object is `{"stated": false}`. **`stated` is always present**:
*"this site says nothing"* and *"the field is missing"* have to stay distinguishable — the first is a
legitimate state deserving a prompt, the second is a bug.

**Errors**

| Status | When |
|---|---|
| `400` | Body is not JSON, `profile` is empty or missing, or `profile` is not a known profile name |
| `401` | Hosted, and the session does not own this tenant |
| `405` | Method is not `POST` |
| `500` | Signing or writing the licence failed |

#### Two properties a caller must not get wrong

**The choice always comes from the user.** The server applies the profile it is handed and never
picks one. An operator-chosen licence would be the operator speaking for the author, in her name,
under her key — so there is no default here and no server-side fallback.

**Stating terms is not retroactive, and this endpoint does not pretend otherwise.** Already-published
works keep the terms they were signed with; only works published after the call carry the new ones. A
client that describes this as "updating your licence" is describing it wrongly.

#### Side effects

The call regenerates the derived public surfaces — `robots.txt`, `rsl.xml`, and the terms page — from
the newly signed licence, so they cannot disagree with the source the moment it changes. Those files
are always generated and never authored beside the signed licence.

#### Events

These are the web app's own log events (they reach the operator's log pipeline). Stating or withdrawing terms puts nothing on the network, so the `pub.polis.license` content type declares no `emits`.

| Event | When |
|---|---|
| `pub.polis.license.stated` | Terms stated, with `profile` (the full identifier, e.g. `pub.polis.license.reserved/1`) |
| `pub.polis.license.withdrawn` | Terms withdrawn |
| `pub.polis.license.materialised` | A work published, recording the terms it actually went out with — read back from the written file rather than from the intent, and fired with `stated: false` too, so *"the author said nothing"* stays distinguishable from *"the event never fired"* |

### `GET, POST, DELETE /api/following`

The site's follow list. **POST and DELETE sign with the site's key, and so can GET.**

**`GET` response** — `200`

```json
{
  "following": [{ "url": "https://bob.example", "added_at": "…", "site_title": "…", "author_name": "…" }],
  "count": 1,
  "signature": { "status": "valid" }
}
```

`signature.status` is `unsigned`, `valid`, `invalid` or `unknown`. `signature.message` appears only when
verification returned an error. `site_title` and `author_name` are optional. ⚠️ **GET can write:** it
fills in missing titles and names for up to three entries, and if anything changed it re-signs and saves
the follow file.

**`POST` request** `{"url": "https://bob.example"}` — follow. It must be `https://`, is lowercased, and
cannot be the site's own address.

**`POST` response** — `200`
`{"success": true, "data": {"author_url", "author_email", "comments_found", "comments_blessed", "comments_failed", "already_followed"}}`

**`DELETE` request** `{"url": "https://bob.example"}` — unfollow. It must be `https://`.

**`DELETE` response** — `200`
`{"success": true, "data": {"author_url", "comments_denied", "comments_failed", "comments_found", "was_following"}}`

| Status | When |
|---|---|
| `400` | There is no private key, the body is not JSON, the URL is not `https://`, or (POST) it is the site's own address |
| `500` | Anything else, with the error as text |

**Side effects of POST and DELETE.** The follow file is re-signed. Comments by that author waiting on
this site's posts are blessed (POST) or denied (DELETE) under the site's rules. A signed
`pub.polis.follow.announced` or `pub.polis.follow.removed` event goes to the discovery service when the
site is registered. POST also starts filling the feed with the author's posts.

## See also

- [The Content Type API](reference.md) — `/v1/`, for content from scripts
- [The licence spec](../../signet/spec/license.md) — format, vocabulary, and every wire surface
- [Key history](../../signet/spec/key-history.md) — what `/api/rotate-key` appends to
- [`polis license`](../../cli/user/command-reference.md#polis-license) — the CLI equivalent of `/api/site/license`
- [User manual § Terms of Use](../../webapp/user/user-manual.md#terms-of-use) — the same operation in the UI
