# Site API (webapp)

*For* [Developers](../../README.md#building-on-polis) — *Kind* [Reference](../../README.md#kinds-of-page) — *Component* [Webapp](../../webapp/README.md) — *Code* [`webapp/internal/server/routes.go`](../../../webapp/internal/server/routes.go)

**Build on `/v1`.** A site has one API: the operations under `/v1/`, described by the site itself at
`/v1/openapi.json` and written up in [the reference](reference.md). The owner's own app, agents (over MCP,
generated from that description), scripts and the `polis` CLI all run the same operations. This page is
what sits **outside** it: a short list, each with its reason.

> | Prefix | What | Documented |
> |---|---|---|
> | `/v1/` | **The site's API**: content, settings, agents, messages, queries | [`reference.md`](reference.md) |
> | `/api/` | Getting signed in, the capability descriptor, the hosted account, the embeddable widget, the signed-in visitor's nav on polis.pub, and two routes the v4 shape's public pages still call | this page |
> | `/mcp` | An instance's own MCP server, for an AI app that needs an address. Not on the hosted service | [below](#mcp-outside-api) |
> | `/pql/<sentence>` | Every view's data path: the owner app's lists and the public site's views; `/v1/query` is the same reads through the API | [the PQL reference](../../general/reference/pql.md) |

⛔ **There is no other `/api/` route.** The old browser app and its routes are gone, and this page lists every
`/api/` route that is left, each with its reason. Everything the old app did that a script may do is a `/v1`
operation (the table below). Nothing new is added under `/api/`.

## Authentication

| Mode | How the caller is authorised |
|---|---|
| **owner** | **Localhost:** nothing per request. The webapp binds to `localhost` and has no login, so if you can reach the port you are the owner, by design (see [the security model](../../general/security/security-model.md)). **Hosted:** a session cookie whose handle matches the tenant. **Remote** (your own instance on a host such as Vercel, made with `polis branch`): a sign-in session, set up through the [sign-in routes](#signing-in-to-a-remote-instance) |
| **public** | No authentication; rate-limited per IP |
| **session** | Hosted only: the tenant's session cookie |
| **widget token** | `Authorization: Bearer <token>`, issued to the embeddable widget. Cross-origin |
| **API key** | `Authorization: Bearer <key>`, a token made with `/v1/agents/tokens` ([keys and scopes](reference.md#authentication)). Used by [`/mcp`](#mcp-outside-api) |

## Moved to `/v1`

The site operations this page used to write up, and the operation that does each one now.

| Was | Now | Notes |
|---|---|---|
| `GET /api/status` | `GET /v1/site/status` | the discovery service listing and publish state |
| `GET /api/settings` | `GET /v1/site`, `GET /v1/site/tokens`, `GET /v1/agents/rosie` | the site's facts, its design tokens, its agent |
| `POST /api/settings/avatar` · `/author-name` · `/theme` | `POST /v1/site/avatar` · `/name` · `/theme` | |
| `GET, POST /api/settings/rosie` | `GET, POST /v1/agents/rosie` | |
| `GET, POST /api/keys`, `DELETE /api/keys/{id}` | `GET, POST /v1/agents/tokens`, `POST /v1/agents/tokens/revoke` | making a token is sign-in only: no token can make one |
| `GET, POST /api/about` | `GET /v1/site` (its `about`), `POST /v1/site/about` | |
| `POST /api/rotate-key` | `POST /v1/site/key/rotate` | sign-in only |
| `GET /api/download-site` | `GET /v1/site/download` | sign-in only; not offered on the hosted service |
| `GET /api/site/registration-status` · `/deploy-check` | `GET /v1/site/status` | `directory` and `publish` |
| `POST /api/site/license` | `POST /v1/site/license` | |
| `GET, POST, DELETE /api/following` | `GET, POST /v1/content/follow`, `POST /v1/content/follow/actions/delete` | |
| `GET /api/validate` | `GET /v1/site/validate` | whether the site's files are a valid polis site, and each problem |
| `POST /api/dm/password` | `POST /v1/messages/password` | sign-in only; the browser does the cryptography |
| `POST /api/dm/password/rewrap` | `POST /v1/messages/password/change` | sign-in only |
| `GET /api/nav/state` (on an instance) | none | the old app's avatar menu; polis.pub's visitor nav keeps its own (below) |
| `POST /api/site/register` · `/unregister` | `POST /v1/site/register` · `/v1/site/unregister` | owner only; both sign with the site's key; a site on polis.pub cannot unregister |

## Outside `/v1` by design

These are not API operations, and each says why it is not one.

### Signing in to a remote instance

**Why outside:** they are how a caller gets a credential at all, so they cannot require one. A laptop needs
no sign-in and the hosted service has its own, so these do their work only on a remote instance. Elsewhere
the recovery routes answer `404` (no recovery key is configured) and sign-in has no provider to offer.
Errors are JSON.

| Route | Method | Auth |
|---|---|---|
| `/api/recover/challenge` | GET, POST | public, rate-limited |
| `/api/recover/verify` | POST | public: a recovery-key signature over that challenge |
| `/api/enrol/{provider}/begin` · `/complete` | POST | the enrolment session `/api/recover/verify` gives, or the owner signed in recently |
| `/api/providers` | GET, DELETE | owner |
| `/api/signin/begin` | GET, POST | public, rate-limited |
| `/api/signin/complete` | POST | public: an assertion over that challenge |
| `/api/signout` | POST | owner |

### Hosted account

**Why outside:** they act on the polis.pub account, the operator's, not on the site. Hosted only.

| Route | Method | Auth |
|---|---|---|
| `/api/account/info` | GET | session |
| `/api/account/change-email` | POST | session |
| `/api/export/request` | POST | session |
| `/api/export/download` | GET | the one-time link that `/api/export/request` emails |

### The embeddable widget

**Why outside:** the widget is polis.pub's own service, not part of a site's API. Its script loads from
polis.pub, and the widget token, the cross-origin access to these routes and the signups that make an account
all exist only on the hosted service. A self-hosted or remote instance does not offer it: its
`/api/widget/connect` answers `501`, so a visitor whose own site is elsewhere cannot connect. The page the
widget sits on can be anywhere. Its writes run the same code the API's do.

| Route | Method | Auth |
|---|---|---|
| `/api/widget/connect` | GET | owner; on hosted, a session, answered by the hosted service |
| `/api/widget/token` | GET | session (hosted) |
| `/api/widget/comment` | POST | widget token |
| `/api/widget/follow` | POST, DELETE | widget token |
| `/api/widget/signup-and-publish` · `/signup-and-follow` | POST | none: creates an account from an email address (hosted, apex domain) |

### The capability descriptor and operator calls

**Why outside:** `GET /api/capabilities` is read before a caller knows what the site's API offers. `/v1/wake`
(the discovery service asking a site to sync) and a message delivered by another site (`dm.deliver`, a signed
request) are site-to-site calls, not the owner's.

### The signed-in visitor's nav (polis.pub)

**Why outside:** it is polis.pub's own service, not a site's, and part of the widget's exception (the same cross-origin visitor service, the same token). When someone signed in to polis.pub reads another
tenant's page, the nav on that page shows their own site's name, avatar and badges, read from their site by the
hosted service. A self-hosted or remote instance has no such route.

| Route | Method | Auth |
|---|---|---|
| `/api/nav/state` | GET | session, or the widget token |

### Public pages of the v4 shape

**A named exception for as long as the v4 shape exists, and not a contract:** these two are kept only because the public pages of the v4 stream shape (`pub.polis.shapes.v4`)
call them from every visitor's browser. That shape is no longer offered to new sites but still renders sites that
use it, exactly as before; when it goes, so do these. A client reads the same things through `/v1` (`post.body`; the latest
comment through the query operation), never through these.

| Route | Method | Auth |
|---|---|---|
| `/api/v1/stream/focus-comment?url=` | GET | public, rate-limited: the latest comment on one post |
| `/api/v1/stream/body?url=` | GET | public, rate-limited: one post's body, from this site or a site it follows |

### `/mcp` (outside `/api/`)

| Route | Method | Auth | Where |
|---|---|---|---|
| `/mcp` | MCP over HTTP | API key | a laptop or a remote instance (the hosted service does not route it; polis.pub's own MCP is below) |

`/mcp` is the site's API over MCP, served by the instance itself, so an AI app that needs an address (not a
local process) can reach it. **Its tools are generated from the site's own description** (`/v1/openapi.json`,
as `/v1` shows it to the key presenting the request), by the same generator as `polis mcp` and polis.pub's
hosted MCP, and **every call is made to the site's `/v1` with that same key**. So it offers exactly what the
key's grant allows, `/v1` checks each call as it checks any request (a write outside the grant is refused, and
the tool says so), and an operation added to the API appears here with no change. It is not under `/api/`, so
the owner's sign-in does not apply: **the API key is its whole authorisation**, and a request without a valid
one gets `401`. A key **bound** to `/mcp` (one minted for Kay, the built-in agent whose page went with the previous owner app) works only there: `/v1`
refuses it except on a call `/mcp` makes for it. An address whose requests keep failing to authenticate is
refused for a while ([its limit, and the key cache](reference.md#personal-tokens-and-grants)). From a
laptop, `polis mcp` serves the same tools over stdio. How the three MCP servers fit together:
[MCP and agent access](mcp.md).

## See also

- [The site's API](reference.md) — `/v1/`, the one to build on
- [The licence spec](../../signet/spec/license.md) — format, vocabulary, and every wire surface
- [Key history](../../signet/spec/key-history.md) — what a key rotation appends to
- [`polis license`](../../cli/user/command-reference.md#polis-license) — the CLI equivalent of `/v1/site/license`
- [User manual § Terms of Use](../../webapp/user/user-manual.md#terms-of-use) — the same operation in the UI
