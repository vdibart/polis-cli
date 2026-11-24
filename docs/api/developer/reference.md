# Content Type API Reference

*For* [Developers](../../README.md#building-on-polis) — *Kind* [Reference](../../README.md#kinds-of-page) — *Component* [Webapp](../../webapp/README.md) — *Code* [`webapp/internal/api`](../../../webapp/internal/api)

The site's API: every operation under `/v1/`, its content, settings, agents and messages. It is the one API a
site has. The owner's own app, an agent over MCP, a script and the `polis` CLI all run these operations, the
CLI in process on a local site with no server. What is outside it, and why, is in
[the site API page](site-api.md).

## The API describes itself

⚠️ **Preview.** The API is a preview until every site action has one implementation, and the description
says so (`info.x-polis-preview` is `true`). Until then an operation and its shape may still change, but
every change is listed in the changelog and nothing changes silently. When that work is finished, `/v1` is
declared stable: it then changes only by addition, a breaking change goes to `/v2`, served beside `/v1`, and
`/v1` is still served three months after `/v2` is announced. `x-polis-preview` becomes `false` on that day.

`GET /v1/openapi.json` is this site's API as an **OpenAPI 3.1** document. It is public and generated from the
operations the site serves, never written by hand, so it cannot list an operation the site does not have. It is
**per site**: it lists only what this site can do. A site whose instance never holds the owner's key leaves out
every operation that signs. A site with no public API leaves out the operations other sites call. A site with
no operator has no account operations.

Each operation carries:

| Member | Meaning |
|---|---|
| `x-polis-auth` | `public` (no credential), `owner` (a token the owner issued, with the scope in `x-polis-scope`), or `site-signed` (another polis site's signed request) |
| `x-polis-title` | its name for a person, short and plain ("Restyle your site"), what an agent app shows for the tool; every operation an agent can be offered has one |
| `x-polis-write` | whether it changes anything; a client can ask the owner before calling one |
| `x-polis-destructive` | whether it removes something no other call restores |
| `x-polis-signs` | whether it signs with the site's key |
| `x-polis-owner-only` | whether it is on the owner-only list: served to the owner's own full-access token and never to an agent's scope |
| `x-polis-sign-in-only` | present and `true` on the owner-only operations served on the owner's own sign-in alone, and to no token, the owner's full-access one included: downloading the site (`site.download`), making a token (`agent.make_token`), rotating the key (`site.rotate_key`), and every change to the messages key (`dm.set_password`, `dm.change_password`, `dm.new_key`, `dm.rewrap_earlier`). A token gets `403` `sign_in_only` |

`x-polis-prompts` lists ready-made requests an agent app can show the owner as suggestions ("What's new in my
network?", "Restyle my site"), each with the operations it needs. The MCP server offers a connection only the prompts
whose every operation it may call, as MCP prompts beside the tools.

`x-polis-manual` lists what only the owner can do, by hand, in their own app: rotating the key, downloading the
site, changing the terms of use, switching agents on or off, reading and protecting messages, managing sign-in.
Each entry says why it is the owner's alone and gives the steps and a link, so an agent asked to do one can tell
the owner how. The steps name the owner's app by its labels and link to its places (`steps_provisional` is `false`; while it was `true`, they named an older app).

Most of those actions are also owner-only operations, which the owner's own interface calls: `site.rotate_key`,
`site.download`, `license.set`, `site.set_policy` and `site.reset_policy` (the owner's own rules, any that are valid, and the defaults back: a token with `policy:write` only tightens), the agents' (`agent.list_tokens`, `agent.make_token`, `agent.revoke_token`,
`agent.list_connections`, `agent.disconnect`, `agent.rosie`, `agent.set_rosie`) and the messages password's
(`dm.keyring`, `dm.set_password`, `dm.change_password`, `dm.new_key` for a fresh key when the password and phrase are both lost, `dm.rewrap_earlier` to keep an earlier key open under the current password). `dm.get` also answers a message the site cannot open (under the owner's password) with its box (`ciphertext`, `nonce`, `box_pub`, `key_epoch`), for the owner's browser to open. On `polis.pub` there is no `site.download`: downloading
a site there is `polis.pub`'s verified export, requested in the owner's account and confirmed from the email, and
the sign-in email is `polis.pub`'s too.

**Finding it.** A site that publishes its API names it in `.well-known/polis` under `api`:

```json
"api": {
  "description": "https://alice.example.com/v1/openapi.json",
  "version": "v1",
  "mcp": "https://alice.example.com/mcp"
}
```

`mcp` is present only where the site has an MCP endpoint an outside agent can reach. A site with no API an
outsider can reach publishes no `api` member. Read the pointer rather than assuming the path. The pointer is
published only where the instance is started with `POLIS_API_POINTER=1`; the description is served either way.

## Agents: MCP tools generated from this description

An agent reaches the site through MCP tools **generated from this description** (`cli-go/pkg/mcpgen`): one tool per
operation an agent may use, `how_do_i` for what only the owner can do (from `x-polis-manual`), and the prompts above,
each call made to this same `/v1` with the agent's own token. There are three servers: `polis mcp --site <address>`
over stdio with the token in `POLIS_TOKEN`, a laptop's or remote instance's own [`/mcp`](site-api.md#mcp-outside-api),
and on polis.pub each site's `https://<handle>.polis.pub/mcp`, where an agent signs in instead of being handed a
token. Owner-only operations and every message operation are never tools. The architecture (what the description
becomes, the three servers, the grant check and the marker on every call) is [MCP and agent access](mcp.md).

The owner sees every connection, however it was made, in the owner app under **enhance › Agents**, and can
disconnect any of them there: that withdraws the grant first and then ends the connection's tokens.

⭐ **Give an agent a personal token, never your full key.** A scoped key is a personal token: minting it writes
your signed grant for the connection (see *Scopes*), and everything the agent writes with it is marked as that
connection's. Your full key acts as you, unmarked.

## Reading content: the query operation

`GET /v1/query?pql=<sentence>` reads content with one sentence in polis's query language, PQL
([reference](../../general/reference/pql.md)), and answers the same JSON envelope as `/pql/<sentence>`, from the
same handler. Any other query parameter passes through as one of PQL's transport parameters.

```bash
# Public content: no credential
curl "https://mysite.example.com/v1/query?pql=all+posts+by+date"

# The owner's own private content (first-person scopes, drafts, messages): the owner's full-access token
curl -H "Authorization: Bearer polis_..." "https://mysite.example.com/v1/query?pql=all+posts+from+my+network"
```

An owner-private sentence answers `401` with no token and `403 insufficient_scope` with a scoped key. Settings
and site facts are not PQL. `/pql/` itself is unchanged and keeps its session-based check.

`search=<words>` keeps the items whose title or excerpt holds the words, for posts and comments as well as
profiles. It searches what the site has: its own content and the feeds it has cached from the network and polis;
nothing yet searches all of polis. `all profiles from <site>'s network` is that site's Following list, only the
sites it follows; `all profiles from my network` lists the owner's followers too, each marked.

`all activity about me` is the owner's own activity, newest first, from the site's notifications: who followed or
unfollowed them, who commented on their posts or asked for a blessing, and who blessed or turned away their
comments. A followed author publishing is in `all posts from my network` instead. It needs the `notify` scope.
Any other `about` is served by the discovery service's own `/pql/`.

When a request needs several scopes (a counts list, `new_for`), the token must carry every one of them.

`time=<window>` (`1h`, `24h`, `2d`, `7d`, `30d` or `all`) is how far back the network's posts and comments go. A
request that names none, and `all`, get the widest window the site serves, 30 days; counts read the same window as
the list. The owner's own content, and comments waiting for a blessing, are never cut by it.

`new_for=<section>` (`gather`, `write` or `message`) marks each item `"new": true` or `false` against the time the
owner has seen that section up to ([What the owner has seen](#what-the-owner-has-seen)); in a section never marked,
everything is new. `only_new=1` keeps only the new items. Both need the `read` scope, and neither clears anything.

`all comments from all polis to bless` lists every comment waiting for the owner's blessing, from the discovery
service's pending list, however old and whoever wrote it. Anyone may ask: blessing decides who is notified (the post
author) and what the author's page shows, not who may ask which comments are waiting. Asked of the author's own site by anyone but the
author, it is empty, because the author's site strips unblessed comments about its own posts; the discovery service
answers in full. See [who you ask decides what you see](../../general/concepts/content-types.md#what-blessing-does-and-why-the-answer-depends-on-who-you-ask).

## The site: its facts and settings

`GET /v1/site` is the site's public facts: its address, the author's name and avatar, the bio (markdown and HTML),
the identity key and its `did:web` name, when the site began (`since`), how many sites follow it (`followers`, a
count), the active theme, and where its terms of use are. Everything in it is
already public on the site. No credential.

The settings an owner changes in their app are operations under `/v1/site`, each served by the app's own code:

| Operation | Body | Scope |
|---|---|---|
| `POST /v1/site/name` | `{"author_name"}` | `profile` |
| `POST /v1/site/avatar` | `{"avatar": {…} or null}` | `profile` |
| `POST /v1/site/about` | `{"content"}` (markdown) | `profile` |
| `POST /v1/site/theme` | `{"theme"}` (one of `theme.list`; reserved themes refused) | `appearance` |
| `POST /v1/site/rebuild` | none: renders every page again | `appearance` |
| `GET /v1/site/status` | none: the discovery listing and the last publish's state | `status` |
| `GET /v1/site/policy` | none: the owner's rules, private then public, in evaluation order (first match decides) | `policy:read` |
| `POST /v1/site/policy/tighten` | `{"rule"}`: one `deny` rule, added at the top of the private rules | `policy:write` |
| `POST /v1/site/policy/release` | `{"rule"}`: removes a rule added with tighten, and nothing else | `policy:write` |

## Messages

Direct messages are the owner's alone: **every message operation needs the owner's own full-access token**, and
no agent scope reaches them, for reading or sending. Beside the `pub.polis.dm` content operations, the owner's
app uses these to start a conversation and send a message it sealed itself:

| Operation | What |
|---|---|
| `GET /v1/messages/recipients` | every site the owner follows, each with whether a message can go to it now (`open`) or why not (`no-dm`, `no-follow`, `unknown`) |
| `GET /v1/messages/recipient-key?recipient_url=…` | the recipient's published messages key and epoch, to seal to |
| `GET /v1/messages/protection-status?recipient_url=…` | whether the recipient keeps its messages under a password |
| `POST /v1/messages/send-sealed` | deliver a message sealed in the owner's browser; the site never sees its text |

`all profiles from my mutuals` lists the sites the owner and they both follow: the ones a message can go to.

## What the owner has seen

`GET /v1/seen` returns, for each section of the owner's app (`gather`, `write`, `message`), the time the owner has
seen it up to. An item is new when it is later than its section's time, and a section with no time has everything
new. `POST /v1/seen` with `{"section": "gather", "up_to": "<RFC 3339 time>"}` moves a section's time forward: the
owner's app sends the time it arrived when the owner leaves the section, so everything that was new on arrival
is seen. The time never moves backwards, a future time is taken as now, and the times belong to the account, so
every device agrees. Both need the `notify` scope.

Nothing else clears anything: reading content through `/v1/query` or `/v1/content` leaves the seen times as
they were. An agent marks a section seen only when the owner asks it to.

## Use Cases

- **Site owner tools**: Publish posts, manage comments, handle blessings from scripts, mobile apps, or external tools.
- **3rd party integrations**: Any tool that speaks HTTP+JSON can manage content on a polis site.
- **Instance-to-instance operations**: DMs use signed-request auth for cross-site delivery without discovery service coupling.

This is the site's one API: everything the owner's own app does to the site is an operation here, its settings
included (`/v1/site`, `/v1/agents`, `/v1/messages`). What stays outside `/v1` (getting signed in, polis.pub's
own account, the embeddable widget) is listed with its reason in
[the site API page](site-api.md). Setting up a new site is the CLI's (`polis init`) or the hosted signup's.

## Quick Start

```bash
# Make a token on your own sign-in: POST /v1/agents/tokens {"name": "my-script", "scopes": ["full"]}
# (sign-in only: no token can make one), or by hand (see Authentication below). The owner app's
# enhance → Agents → Tokens → Make a token makes a scoped one.
KEY="polis_..."                   # the token, shown once

# List posts (public, no auth needed)
curl https://mysite.example.com/v1/content/post

# Publish a post (auth required)
curl -X POST https://mysite.example.com/v1/content/post \
  -H "Authorization: Bearer polis_abc123..." \
  -H "Content-Type: application/json" \
  -d '{"markdown": "# Hello World\n\nMy first API post."}'

# List installed bundles
curl https://mysite.example.com/v1/bundles
```

## Authentication

Read operations (GET) on content and bundles are public. All write operations require a Bearer token.

```
Authorization: Bearer <api-key>
```

An API key is a token whose **SHA-256 hash** is recorded in `.polis/api-keys.json`; the server hashes the presented Bearer token and compares it against the stored hashes. Only the hash is ever stored.

**The owner's own sign-in.** The site's own interface calls `/v1` with no token, on the owner's sign-in: on `polis.pub` the owner's session, on a laptop the loopback listener, on a remote instance a signed-in session. Such a request is the owner, exactly as the owner's full-access key is: every scope, the owner-only operations included, and no grant or marker. ⛔ It holds only for a request from the site's own page: one whose `Origin` is another site's (`null` included), or whose `Sec-Fetch-Site` is anything but `same-origin` or `none`, is not the owner whatever it carries, reads included. ⭐ A request that presents a token is judged by its token alone, even while the owner is signed in, so an agent running in the owner's browser is still the agent.

**Minting a key.** The site's owner mints and revokes keys on their own sign-in, with `agent.make_token` (`POST /v1/agents/tokens`, sign-in only: no token can make another), `agent.list_tokens` and `agent.revoke_token`, which work the same on a laptop and on a remote instance whose files are not on the owner's disk (the `/api/keys` routes they replaced are gone):

| Operation | Does |
|---|---|
| `GET /v1/agents/tokens` | lists the keys: `id`, `name`, `scopes`, `created_at`, and `grant` for a personal token (never a hash). A scoped key made before keys carried grants is renewed here, with a grant of its own |
| `POST /v1/agents/tokens` `{"name": "...", "scopes": [...]}` | mints a key and returns it once, as `token`, with `Cache-Control: no-store`. Sign-in only. ⛔ `scopes` is required: name the scopes the key needs, or `["full"]` for a key with full access. An empty or missing list is refused rather than defaulting to full. A scoped key is a personal token: its grant is written first (see *Personal tokens and grants*) |
| `POST /v1/agents/tokens/revoke` `{"id": "..."}` | revokes it; a personal token's grant is withdrawn first |

A key can also be made by hand: `KEY="polis_$(openssl rand -hex 32)"`, then add `{"id": "k1", "name": "my-script", "key_hash": "<sha256 of $KEY>", "created_at": "..."}` to the `keys` list in `.polis/api-keys.json`, mode `600`.

### Scopes

A key with no `scopes` has **full access**: it is the owner's own token, as every key was before scopes existed.
A scoped key may act only within its scopes. Each operation's scope is in the API's description
(`x-polis-scope`, see *The API describes itself*). The vocabulary, version 1:

| Scope | Covers |
|---|---|
| `read` | the owner's own private content except messages: drafts, the network's feed, follows |
| `publish` | publishing and republishing posts |
| `comment` | writing, editing and sending comments |
| `bless` | blessing, turning away and unblessing comments on the owner's posts |
| `follow` | following and unfollowing |
| `tag` | tagging posts and comments |
| `appearance` | the site's theme and styles, never an authored or signed source (`render` is its earlier name and still works) |
| `profile` | the name, avatar and bio |
| `policy:read` | the owner's rules for who may comment and message |
| `policy:write` | tightening those rules: adding `deny` rules at the top of the private rules, and removing only rules the same grant added, never anything that could loosen a decision |
| `notify` | that something happened (a message from someone, a new follower, a comment waiting), never what it said |
| `status` | how the site is running: when it last synced, whether it published, its version |

The description publishes this vocabulary as `x-polis-scopes`: for each scope its `name`, the `sentence` a consent
screen shows for it, `agent` (whether an agent's grant may carry it) and, for `policy:write`, the `warning` said
beside it. Every place an owner grants scopes (polis.pub's consent page, the owner app's *Make a token*) renders
from that one list.

No scope reaches messages: they need the owner's full-access token. A token outside its scopes is refused with
`403` and the error code `insufficient_scope`, saying which scope was needed.

⚠️ A scoped key is only as narrow as the build reading it: a build older than scopes ignores the field and treats every key as full access.

### Personal tokens and grants

A scoped key is a **personal token**: it presents one signed grant on the owner's site, and the grant, not the
key, says what it may do. The owner makes one in the owner app, **enhance → Agents → Tokens → Make a token**:
they say what it is for and tick what it may do, and the token is shown once. That control calls
`POST /v1/agents/tokens` (`agent.make_token`), which is sign-in only: a script cannot make a token with a token.

- **Minting one writes the grant first.** It is a `pub.polis.attestation.grant` with behaviour set `agent/1`,
  basis `user-signed`, the key's name as `agent`, and the granted scopes in `scopes` ([delegation](../../signet/spec/delegation.md)).
  The key stores only the grant's URL. ⛔ An agent's grant can hold any scope above except `tag` (tagging
  stays with the owner, in the CLI or the app); `full` is never a grant. `follow` became grantable once the
  follow list's entries could carry the marker: a grant names its scopes, so no grant made before then holds it.
- **Every request checks the grant is live**: on the site, verifying, never withdrawn, and holding the scope the
  operation needs. If it isn't, the request is refused with `403` `grant_withdrawn`, reads included.
- **Every write is marked.** A post or comment carries `agent:` and `grant:` in its frontmatter, inside the
  signature. A blessing decision carries the marker in its signed request, a follow's entry in the signed
  follow list carries it (an unfollow removes an entry, so it is recorded instead), and a policy rule records
  the grant in `added_by`. Every write, signed or not, is recorded in the site's private act log
  (`.polis/connections/acts.jsonl`): the agent, the grant, the operation, the status and the request's
  `X-Request-Id`. The owner's own writes are recorded there too, unmarked, so the log answers "what changed
  my site?"; each kind keeps its own newest entries, so the owner's frequent writes never push an agent's
  record out.
- **Revoking the key withdraws its grant**, so the connection is switched off on the record.
- **The description a token is served** (`/v1/openapi.json` with its `Authorization`) leaves out what its grant
  cannot do, so generated tools never offer an operation that would be refused.
- ⚠️ **A scoped key made before keys carried grants** can read but not write, until the owner lists the
  tokens (`GET /v1/agents/tokens`), which renews it with a grant of its own (dropping `tag`, and `follow` too: the key was minted before an
  agent could be granted it, so the owner grants it by making a new connection).
- ⚠️ **The grant is public.** Like every attestation, it is published on the site and registered with the
  discovery service, and the site's agents page lists it. A connection's name is visible to anyone, so name it
  accordingly. Minting a personal token answers with a `warning` saying so, and a token minted with no `name`
  is called `Agent <n>`, which names no software.

A key may also be **bound** to one path (`"bound": "/mcp"` in its entry): it works there and nowhere else, and `/v1` accepts it only
on a call the instance's `/mcp` makes for it. Bound keys were minted for Kay (the instance's built-in agent, whose page went with
the previous owner app on 2026-11-22), each a personal token with its own grant like any agent's; one made then still works at `/mcp`,
and this build mints no new ones. A bound key is always scoped. `POST /v1/agents/tokens` makes unbound keys, and `GET /v1/agents/tokens`
does not show `bound`. ⚠️ Like scopes, a build older than binding ignores it.

**Rate limits.** Past a limit the answer is `429` with the error code `rate_limited` and a `Retry-After` header:
- each token other than the owner's full-access one may make **200 writes an hour**, of which at most **50 deletes**
  (reads are not counted), so an agent caught in a loop stops there;
- a call with no token is limited to **600 an hour per address**.

The counters are kept in memory by each server process.

**`/mcp` limits.** [`/mcp`](site-api.md#mcp-outside-api) is where the owner's AI acts on the site. An address whose requests keep failing to authenticate is refused with `429` for the rest of the minute; successful
calls are never counted. The instance keeps its list of keys in memory for a few seconds, so a key revoked on one instance of a
multi-instance deployment can keep working on another for up to ten seconds.

| Operation | Auth Required |
|-----------|---------------|
| `GET /v1/content/{type}` | No (public types only) |
| `GET /v1/content/{type}/{id}` | No (public types only) |
| `GET /v1/bundles` | No |
| `GET /v1/bundles/{name}` | No |
| `GET` on private types (`dm`, `follow`) and on any type the core bundle does not declare | Yes |
| Everything else | Yes |

On `/v1/query`, a public sentence needs nothing, the owner's own private content needs `read`, and messages need
the full-access token. Asking for `counts` needs whatever the most private counted type needs.

Private content types (`pub.polis.dm`, `pub.polis.follow`) require Bearer token auth for all operations including reads. Every other type allows unauthenticated reads.

The `deliver` and `protection_status` actions on `pub.polis.dm` are site-to-site: they authenticate **only** with signed request headers, never a Bearer token (a request without `X-Polis-Domain` is refused with `403`). The remote instance signs a canonical JSON payload with its Ed25519 private key.

| Header | Purpose |
|--------|---------|
| `X-Polis-Domain` | Sender's domain |
| `X-Polis-Signature` | Ed25519 SSH signature over canonical JSON |
| `X-Polis-Timestamp` | UTC timestamp in the form `2006-01-02T15:04:05Z`, within **2 minutes** of the receiver's clock |

The signed canonical JSON is `{"action","body_sha256","domain","recipient","timestamp"}` in that key order: the action name, the base64 SHA-256 of the exact request body, the sender's domain, the receiving site's domain, and the timestamp. The receiver verifies it against the sender's `.well-known/polis` key.

### Error Responses

Missing auth returns `401 Unauthorized`. Invalid key, or a signed request that fails verification, returns `403 Forbidden`. A valid key without the access an operation needs returns `403` with the code `insufficient_scope`.

## Paths

Every operation's path follows one rule, so a client can tell from a path what kind of operation it is. The
description at `/v1/openapi.json` is the full list; a build test checks every operation in it against this rule.

**A content type's operations**, under `/v1/content/{type}` (`post`, `comment`, `follow`, `tag`, `dm`, `theme`,
`license`):

```
GET    /v1/content/{type}                    list
POST   /v1/content/{type}                    create
GET    /v1/content/{type}/{id}               get one
PUT    /v1/content/{type}/{id}               update one
POST   /v1/content/{type}/actions/{action}   any other action; {action} is the operation's own name
                                             (post.save_draft is /v1/content/post/actions/save_draft)
GET    /v1/content/{type}/{view}             a named read of the type, its subject in the query string:
                                             /v1/content/post/source?path=…, /v1/content/post/body?url=…,
                                             /v1/content/license/current
```

**A site's settings, agents and messages**, under `/v1/{area}`, where `{area}` is `site`, `agents`, `messages`,
`query` or `seen`: `GET` reads and `POST` changes, and nested nouns are kebab-case
(`/v1/site/policy/tighten`, `/v1/messages/recipient-key`).

Drafts are actions on the post type (`save_draft`, `discard_draft`), read through the query operation
(`all drafts from me`). There is no `DELETE`: removing something is an action.

### Bundle Introspection

```
GET    /v1/bundles                           List installed bundles
GET    /v1/bundles/{name}                    Get bundle details
```

## Type Names

The `{type}` parameter accepts short names or fully-qualified names:

| Short Name | Full Name |
|-----------|-----------|
| `post` | `pub.polis.post` |
| `comment` | `pub.polis.comment` |
| `follow` | `pub.polis.follow` |
| `dm` | `pub.polis.dm` |
| `tag` | `pub.polis.tag` |
| `theme` | `pub.polis.theme` |
| `license` | `pub.polis.license` |
| `attestation` | `pub.polis.attestation` |
| `actor` | `pub.polis.actor` |

Both forms work: `/v1/content/post` and `/v1/content/pub.polis.post` are equivalent.

Resolution: short names try `pub.polis.<name>` first, then search all bundles for a suffix match. Ambiguous matches fail. A type is available only if the site's bundle declares it (`content/pub.polis.core/bundle.json`, or the built-in default when that file is absent). An undeclared type — there is no `feed` type — returns `404`.

## Content Types

### pub.polis.post

| Action | Method + Path | Status |
|--------|---------------|--------|
| List posts | `GET /v1/content/post` | Implemented |
| Get one of your posts (its markdown) | `GET /v1/content/post/source?path=…` | Implemented |
| Read any post in full | `GET /v1/content/post/body?url=…` | Implemented |
| Create (publish) | `POST /v1/content/post` | Implemented |
| Update (republish) | `PUT /v1/content/post/{id}` (the post named by `path` in the body) | Implemented |
| Unpublish | `POST /v1/content/post/actions/unpublish` (`{"path": …}`), owner only | Implemented |
| Save a draft | `POST /v1/content/post/actions/save_draft`, owner only | Implemented |
| Discard a draft | `POST /v1/content/post/actions/discard_draft`, owner only | Implemented |
| Read drafts | the query operation, `all drafts from me` | Implemented |

Not offered: `GET /v1/content/post/{id}` (read a post's markdown through `…/post/source`), `DELETE
/v1/content/post/{id}` (taking a post down is `unpublish`), and a render preview (`POST …/post/actions/render`):
a post is rendered when it is published, and the whole site by `POST /v1/site/rebuild`. Each answers
`400 unsupported_action`, and none is in the description. Unpublishing takes the post off the
site and the discovery service, removes its page, its earlier versions and its index entry, and puts its
text back in the drafts, without its signature. It cannot be undone: publishing the draft again is a new
post. `/v1/openapi.json` is the authoritative list.

**Create post request:**
```json
{
  "markdown": "# Post Title\n\nPost content in markdown.",
  "filename": "optional-custom-filename.md"
}
```

**Create post response** (201):
```json
{
  "title": "Post Title",
  "path": "content/pub.polis.core/post/20260301/post-title.md",
  "version": "sha256:3f2a...",
  "signature": "...",
  "url": ""
}
```

`url` is present but currently always empty; build the public URL from the site's base URL and `path`.

**List posts response** (200):
```json
{
  "posts": [
    {
      "type": "post",
      "path": "content/pub.polis.core/post/20260302/second.md",
      "title": "Second Post",
      "published": "2026-03-02T00:00:00Z",
      "current_version": "sha256:9c1e..."
    },
    {
      "type": "post",
      "path": "content/pub.polis.core/post/20260301/first.md",
      "title": "First Post",
      "published": "2026-03-01T00:00:00Z",
      "current_version": "sha256:3f2a..."
    }
  ],
  "count": 2
}
```

Posts are returned newest-first (reverse index order). Only `type: "post"` entries are returned; comments, tags and attestations in the index are filtered out. An entry also carries a `license` block when the work states terms.

### pub.polis.comment

| Action | Method + Path | Status |
|--------|---------------|--------|
| List comments | the query operation (`all comments from me`; waiting ones, `all comments from all polis to bless`) | Implemented |
| Get one of your comments (its markdown) | `GET /v1/content/comment/source?path=…` | Implemented |
| Create (beseech) | `POST /v1/content/comment` | Implemented |
| Update (republish) | `PUT /v1/content/comment/{id}` | Implemented |
| Bless | `POST /v1/content/comment/actions/bless` | Implemented |
| Deny | `POST /v1/content/comment/actions/deny` | Implemented |
| Unbless | `POST /v1/content/comment/actions/unbless` | Implemented |

Not offered: `GET /v1/content/comment` and `GET /v1/content/comment/{id}` (list and read comments through the
query operation and `…/comment/source`), `revoke` (taking a blessing back is `unbless`) and `sync` (the site
syncs its blessings itself, in the background). Each answers `400 unsupported_action`, and none is in the
description.

**Create (beseech) request:**
```json
{
  "comment_id": "abc123"
}
```

**Create (beseech) response** (200):
```json
{
  "success": true,
  "status": "pending",
  "message": "Comment sent to discovery service",
  "auto_blessed": false
}
```

A new comment is always pending: the post author's own site decides it later, so the commenter learns the outcome asynchronously. `auto_blessed` is `true` only when the comment had already been blessed and was re-registered. `message` is the discovery service's message, or a local note when the site is not registered with one.

**Update (republish) request** — `{id}` in the path is not used; the comment is named by `path`:
```json
{
  "path": "content/pub.polis.core/comment/20260301/reply.md",
  "markdown": "The corrected reply."
}
```

**Update (republish) response** (200):
```json
{
  "comment_id": "...",
  "path": "content/pub.polis.core/comment/20260301/reply.md",
  "version": "sha256:7d0b...",
  "signature": "...",
  "blessing_state": "blessed",
  "rebeseeched": false,
  "deferred": false
}
```

Signs a new version of an already-published comment and re-registers it; a granted or denied blessing is preserved. `blessing_state` is `pending`, `blessed` or `denied`; `rebeseeched` is `true` when a pending comment was re-sent for blessing; `deferred` is `true` when the discovery service was skipped because the site is not registered.

### pub.polis.follow

| Action | Method + Path | Status |
|--------|---------------|--------|
| List following | `GET /v1/content/follow` | Implemented |
| Follow | `POST /v1/content/follow` (`{"url": …}`) | Implemented |
| Unfollow | `POST /v1/content/follow/actions/delete` (`{"url": …}`) | Implemented |

**List following response** (200):
```json
{
  "following": [
    {
      "url": "https://alice.example.com",
      "added_at": "2026-01-01T00:00:00Z",
      "site_title": "Alice's Blog",
      "author_name": "Alice"
    }
  ],
  "count": 1,
  "signature": {
    "status": "valid"
  }
}
```

`signature` reports the state of the follow file's own signature
(`pub.polis.follow` is signed — see
[the signed follow file](../../signet/spec/attestation.md)).

| `status` | Meaning |
|----------|---------|
| `valid` | signed, and the signature verifies against the site's published identity key |
| `unsigned` | no signature. **A fact, not a defect** — the normal state for a follow list nobody has re-authored since signing shipped |
| `invalid` | signed, and the signature does **not** verify. A `message` field carries the detail |
| `unknown` | could not be checked (no `.well-known/polis`, or no `public_key` in it). A `message` field carries the detail |

⚠️ **`signature` is reported, never enforced.** The follow list is returned in
full whatever the status says — including `invalid`. Whether a given status is
acceptable is the caller's judgment, not the API's. Do not collapse `unsigned`
and `invalid` into one "bad" state: they are different facts, and treating an
unsigned list as broken misreads the common case.

### pub.polis.dm

| Action | Method + Path | Status |
|--------|---------------|--------|
| List conversations | `GET /v1/content/dm` | Implemented |
| Get conversation | `GET /v1/content/dm/{conv_id}` | Implemented |
| Send DM | `POST /v1/content/dm` | Implemented |
| Delete conversation | `DELETE /v1/content/dm/{conv_id}` | Implemented |
| Deliver (receive) | `POST /v1/content/dm/actions/deliver` | Implemented (site-to-site, signed request) |
| Protection status | `POST /v1/content/dm/actions/protection_status` | Implemented (site-to-site, signed request) |
| Mark read | `POST /v1/content/dm/actions/mark_read` | Implemented |
| Retry unsent | `POST /v1/content/dm/actions/retry` | Implemented |

**List conversations response** (200):
```json
{
  "conversations": [
    {
      "id": "f8e7d6c5b4a3f2e1",
      "peer_domain": "bob.example.com",
      "peer_url": "https://bob.example.com",
      "last_message_at": "2026-03-07T10:00:00Z",
      "unread_count": 2,
      "last_preview": ""
    }
  ],
  "count": 1
}
```

`last_preview` is always empty: messages are end-to-end encrypted and the server cannot read them.

**Get conversation response** (200):
```json
{
  "conversation_id": "f8e7d6c5b4a3f2e1",
  "peer_domain": "bob.example.com",
  "peer_url": "https://bob.example.com",
  "messages": [
    {
      "id": "msg1",
      "from": "alice.example.com",
      "to": "bob.example.com",
      "content": "Hello Bob!",
      "timestamp": "2026-03-07T10:00:00Z",
      "status": "sent",
      "key_epoch": 1,
      "locked": false
    }
  ],
  "count": 1
}
```

A message whose key epoch is locked (its password has not been entered) has empty `content` and `locked: true`. A reply carries `reply_to_id`.

**Send DM request:**
```json
{
  "recipient_url": "https://bob.example.com",
  "content": "Hello Bob!",
  "reply_to_id": ""
}
```

**Send DM response** (201, delivered):
```json
{
  "message_id": "a1b2c3d4",
  "conversation_id": "f8e7d6c5b4a3f2e1",
  "status": "sent"
}
```

**Send DM response** (201, delivery failed):
```json
{
  "message_id": "a1b2c3d4",
  "conversation_id": "f8e7d6c5b4a3f2e1",
  "status": "unsent",
  "error": "recipient unreachable"
}
```

**Deliver (receive) request** — sent by remote instance with signed headers:
```json
{
  "version": 2,
  "sender_domain": "alice.example.com",
  "recipient_domain": "bob.example.com",
  "encrypted_content": "<base64>",
  "nonce": "<base64, 24 bytes>",
  "timestamp": "2026-03-07T10:00:00Z",
  "box_pub": "<base64 x25519>",
  "sender_epoch": 1,
  "recipient_epoch": 1,
  "reply_to": ""
}
```

Only envelope version `2` is accepted. `sender_domain` must match the signed `X-Polis-Domain`. The receiver's policy, rate limits and size limit are applied before the envelope is parsed. The envelope format is specified in [DM encryption](../../general/security/dm-encryption.md).

**Deliver response** (201):
```json
{
  "message_id": "msg1",
  "conversation_id": "f8e7d6c5b4a3f2e1",
  "status": "received"
}
```

**Protection status response** (200) — only to a caller this site follows; anyone else gets `403`:
```json
{
  "domain": "bob.example.com",
  "protected": true,
  "at": "2026-03-07T10:00:00Z",
  "sig": "..."
}
```

**Mark read request:**
```json
{
  "conversation_id": "f8e7d6c5b4a3f2e1"
}
```

**Mark read response** (200):
```json
{
  "conversation_id": "f8e7d6c5b4a3f2e1"
}
```

**Delete conversation response** (200):
```json
{
  "conversation_id": "f8e7d6c5b4a3f2e1",
  "deleted": true
}
```

**Retry response** (200):
```json
{
  "unsent_count": 2,
  "unsent": [
    { "conversation_id": "f8e7d6c5b4a3f2e1", "message_id": "a1b2c3d4", "to": "bob.example.com", "timestamp": "2026-03-07T10:00:00Z" }
  ]
}
```

With nothing unsent the response is `{"retried": 0, "message": "no unsent messages"}`.

**Note:** The `deliver` and `protection_status` actions use signed-request authentication, not Bearer tokens. All other DM actions require Bearer token auth. Despite its name, `retry` does not resend: it returns the list of unsent messages from the store.

### pub.polis.tag

| Action | Method + Path | Status |
|--------|---------------|--------|
| List tags | `GET /v1/content/tag` | Implemented |
| Apply tag | `POST /v1/content/tag/actions/apply` | Implemented |
| Remove target | `POST /v1/content/tag/actions/remove` | Implemented |
| Delete tag | `POST /v1/content/tag/actions/delete` (body `{"tag": ...}`) or `DELETE /v1/content/tag/{name}` | Implemented |

**List tags response** (200, all tags):
```json
{
  "tags": [
    {
      "tag": "favorite",
      "count": 3,
      "updated": "2026-03-07T10:00:00Z"
    }
  ],
  "count": 1
}
```

**List tags request** (filter by name) — `POST /v1/content/tag/actions/list` (Bearer auth), since a `GET` carries no body:
```json
{
  "tag": "favorite"
}
```

**List tags response** (200, single tag with targets):
```json
{
  "tag": "favorite",
  "targets": [
    {
      "uri": "https://bob.example.com/posts/20260215/hello.md",
      "added": "2026-03-07T10:00:00Z"
    }
  ],
  "count": 1
}
```

**Apply tag request:**
```json
{
  "tag": "favorite",
  "target_uri": "https://bob.example.com/posts/20260215/hello.md"
}
```

**Apply tag response** (200):
```json
{
  "tag": "favorite",
  "target_uri": "https://bob.example.com/posts/20260215/hello.md",
  "count": 3
}
```

On success, the tag is synced to the discovery service (non-fatal if DS is unavailable).

**Remove target request:**
```json
{
  "tag": "favorite",
  "target_uri": "https://bob.example.com/posts/20260215/hello.md"
}
```

**Remove target response** (200):
```json
{
  "tag": "favorite",
  "target_uri": "https://bob.example.com/posts/20260215/hello.md",
  "count": 2
}
```

Unregisters the target from the discovery service (non-fatal if DS is unavailable).

**Delete tag request:**
```json
{
  "tag": "favorite"
}
```

**Delete tag response** (200):
```json
{
  "tag": "favorite",
  "deleted": true
}
```

Deletes the tag file locally and unregisters all targets from the discovery service.

### `pub.polis.theme`

Read-only access to the themes declared by the site's bundle. The active theme is a site setting, not an action on this type: `GET /v1/site` reports it and `POST /v1/site/theme` sets it (see *The site: its facts and settings*), writing `.polis/bundles/registry.json`.

**Actions:** `list`, `get`

**List response** (`GET /v1/content/theme`):
```json
{
  "themes": [
    { "name": "_shared",  "version": "1.2.0", "css": "base.css",     "compatible_shapes": ["pub.polis.shapes.v3", "pub.polis.shapes.v4"] },
    { "name": "especial", "version": "1.1.0", "css": "especial.css", "compatible_shapes": ["pub.polis.shapes.v3", "pub.polis.shapes.v4"] }
  ],
  "count": 2
}
```

Every declared theme is listed, sorted by name — including `_shared` (shared CSS, not selectable) and the reserved themes the webapp hides from its picker. `compatible_shapes` is omitted when the declaration names none.

**Get request:** `GET /v1/content/theme/{name}` (e.g. `GET /v1/content/theme/vice`, no auth), or `POST /v1/content/theme/actions/get` with `{"name": "vice"}` (Bearer auth, since it is a POST). The response is one entry in the shape above. A name the bundle does not declare is `404 not_found`.

### `pub.polis.license`

The site's current terms — the machine endpoint for the site default. Read-only: stating terms is a signing act for the key holder (`polis license`), never an API caller.

**Actions:** `get`

**Get response** (`GET /v1/content/license/current` — the `{id}` segment is required by the route and ignored):
```json
{
  "stated": true,
  "terms": { "...": "the signed licence's terms" },
  "pointer": "..."
}
```

A site that has stated no terms returns `"stated": false` with `"terms": null` — a defined state, not an error. `GET /v1/content/license` (list) is `400 unsupported_action`. For the terms a particular work was published under, read the work (or its index entry), not this endpoint. See the [licence spec](../../signet/spec/license.md).

### `pub.polis.attestation` · `pub.polis.actor`

Declared by the core bundle but not served by this API: every action answers `400 unsupported_action`, and `/v1/bundles` lists their `actions` as `null`. Read attestations from the site's `index.jsonl` and actor registries by the `actor_registry` pointer in `.well-known/polis`.

## Custom Bundles

The dispatch engine supports external handlers for custom content types via `bundle.json`:

| Handler Type | How It Works |
|-------------|--------------|
| `builtin` (or empty) | Go code in `builtin_core.go` (pub.polis.core only) |
| `executable` | External binary — receives `ActionRequest` as JSON on stdin, returns `ActionResult` on stdout. Env vars: `POLIS_SITE_DIR`, `POLIS_BASE_URL`, `POLIS_DISCOVERY_URL` |
| `http` | HTTP endpoint — receives `ActionRequest` as JSON POST, returns `ActionResult`. 30s timeout. |

⚠️ The webapp builds its engine from **one** bundle: the site's `content/pub.polis.core/bundle.json` (or the built-in default). No other bundle is loaded, so a third-party bundle is not reachable through this API today; the handler types apply to whatever handler that one bundle declares.

## Bundle Introspection

**List bundles response** (200) — abridged; types appear in no fixed order:
```json
{
  "bundles": [
    {
      "name": "pub.polis.core",
      "version": "1.0.0",
      "description": "Core polis content types",
      "types": [
        { "name": "pub.polis.post", "actions": ["list", "create"] },
        { "name": "pub.polis.comment", "actions": ["create", "update"] },
        { "name": "pub.polis.follow", "actions": ["list"] },
        { "name": "pub.polis.dm",
          "actions": ["list", "get", "send", "deliver", "protection_status", "mark_read", "delete", "retry"] },
        { "name": "pub.polis.tag", "actions": ["list", "apply", "remove", "delete"] },
        { "name": "pub.polis.theme", "actions": ["list", "get"] },
        { "name": "pub.polis.license", "actions": ["get"] },
        { "name": "pub.polis.attestation", "actions": null },
        { "name": "pub.polis.actor", "actions": null }
      ]
    }
  ]
}
```

`actions` lists exactly the actions the handler accepts; every other action on the type answers `400 unsupported_action`. A route named *Not offered* in the tables above answers that way and is in neither this list nor the description.

## Dispatch Engine Internals

All content routes dispatch through `Engine.Dispatch()`:

1. Resolve short type name → fully-qualified name (e.g. `"post"` → `"pub.polis.post"`)
2. Find the bundle that owns the type
3. Look up the handler registered for that bundle
4. Check if the action is a write — if yes and no private key is configured, return `503 not_configured`
5. Call `handler.Handle(ctx, request, env)`
6. On write success, trigger `OnContentChanged()` callback (re-renders the site)

**Write actions** (require private key): `create`, `update`, `delete`, `bless`, `deny`, `revoke`, `sync`, `draft.save`, `draft.delete`, `refresh`, `send`, `deliver`, `mark_read`, `retry`, `apply`, `remove`

## Answers

**A success answer is the operation's own JSON object**, not wrapped in a common envelope, and each operation
states its shape in `/v1/openapi.json`: the schema of its `2xx` response. A shape is open: an operation may
add a field, so read the fields you know and ignore the rest. The code is `201` for a create, `200`
otherwise, unless the operation says differently: making a token is `201`, revoking one `204` with no body,
following a site `200`, and the site download is a zip, not JSON.

⚠️ **A `2xx` can still say something did not fully happen**, and the operation's answer says so: a comment
signed but not sent is `201` with `"sent": false` and `send_error`; a message saved but not delivered is `202`
with a `warning` (both message sends). Each can be sent again.

## Error Responses

Every `/v1` error, whichever operation answers it, is one JSON envelope:

```json
{
  "status": "error",
  "error": {
    "code": "not_found",
    "message": "The requested resource was not found."
  }
}
```

Branch on `code`, which is machine-readable and stable; `message` is for a person. When an error says more
than its message (the reasons a change was refused, which rule in a list failed), the extra fields are inside
`error` beside `code` and `message`, for example `"reasons": [...]`. Every answer, error or not, carries the
request's `X-Request-Id` header; quote it when reporting a problem. For a content operation `message` is a fixed
sentence per code, never the underlying error (which may contain server paths); the underlying error is logged
server-side with the request id.

| HTTP Status | Error Code | When |
|-------------|-----------|------|
| 400 | `invalid_request` | Bad or malformed JSON, missing required fields, invalid payload |
| 400 | `unsupported_action` | Action not available for this content type |
| 401 | `unauthorized` | Missing Authorization header, or not a `Bearer` token |
| 403 | `forbidden` | Invalid API key, signed-request verification failed, or a DM site-to-site action refused |
| 403 | `insufficient_scope` | A valid token whose scopes do not include the operation's |
| 403 | `sign_in_only` | An operation only the owner, signed in, may do; no token can |
| 404 | `not_found` | Unknown content type, content not found |
| 405 | `method_not_allowed` | Wrong HTTP method for this route |
| 409 | `conflict` | The thing changed meanwhile (a keyring revision, for one); read it again and retry |
| 413 | `too_large` | The body is over the limit |
| 429 | `rate_limited` | A limit was reached; wait for `Retry-After` |
| 500 | `internal_error` | Unexpected server error |
| 503 | `not_configured` / `unavailable` | Site not configured (e.g., no signing keys), or a dependency is down |

## CORS

The API sends **no** CORS headers, so a browser enforces same-origin: a page on another origin cannot call it. Scripts, servers and other polis instances are unaffected. There is no OPTIONS preflight handling.

## Body Limits

Request bodies are limited to 1MB. Write actions time out after 30 seconds.

## Current Implementation Status

### Fully Implemented (32 content operations)

These content operations work end to end (the site's settings, agents and messages are in their own sections
above, and `/v1/openapi.json` is the full list):

1. **`pub.polis.post/list`** — Reads index.jsonl, keeps post entries, returns newest-first
2. **`pub.polis.post/create`** — Signs content, writes post, updates index, registers with DS
3. **`pub.polis.comment/create`** — Sends beseech request for pending comment
4. **`pub.polis.comment/update`** — Republishes an already-published comment as a new signed version
5. **`pub.polis.follow/list`** — Reads following.json, returns entries with metadata and the file's signature status
6. **`pub.polis.dm/list`** — Lists conversation summaries with unread counts
7. **`pub.polis.dm/get`** — Returns conversation with decrypted messages
8. **`pub.polis.dm/send`** — Encrypts and delivers DM to remote instance
9. **`pub.polis.dm/deliver`** — Receives encrypted DM from remote instance (signed-request auth)
10. **`pub.polis.dm/protection_status`** — Tells a followed site whether this site's messages are protected (signed-request auth)
11. **`pub.polis.dm/mark_read`** — Marks conversation messages as read
12. **`pub.polis.dm/delete`** — Deletes a conversation locally
13. **`pub.polis.dm/retry`** — Returns list of unsent messages
14. **`pub.polis.tag/list`** — Lists all tags or a single tag's targets
15. **`pub.polis.tag/apply`** — Applies a tag to a target URL, syncs to DS
16. **`pub.polis.tag/remove`** — Removes a target from a tag, unregisters from DS
17. **`pub.polis.tag/delete`** — Deletes a tag and unregisters all targets from DS
18. **`pub.polis.theme/list`** — Lists themes declared by the bundle
19. **`pub.polis.theme/get`** — Returns a single theme's declaration (`GET /v1/content/theme/{name}` or `POST …/actions/get`)
20. **`pub.polis.license/get`** — Returns the site's current terms, or `stated: false`
21. **`pub.polis.post/update`** — Signs a new version of one of the site's posts at the same path (`PUT /v1/content/post/{id}` with `{"path", "markdown"}`; scope `publish`)
22. **`pub.polis.comment/bless`** — Blesses a comment waiting on one of the owner's posts (`POST …/comment/actions/bless` with `{"comment_url", "in_reply_to"}`; scope `bless`; needs a discovery service the site is registered with)
23. **`pub.polis.comment/deny`** — Turns a waiting comment away ("not here"; scope `bless`; same requirement)
24. **`pub.polis.comment/unbless`** — Takes a blessed comment off a post; it leaves other sites at their next sync (scope `bless`)
25. **`pub.polis.follow/create`** — Follows a site (`POST /v1/content/follow` with `{"url"}`; scope `follow`)
26. **`pub.polis.follow/delete`** — Unfollows a site (`POST /v1/content/follow/actions/delete` with `{"url"}`; scope `follow`)
27. **`pub.polis.post/unpublish`** — Takes one of the owner's posts down, back to a draft (`POST /v1/content/post/actions/unpublish` with `{"path"}`; owner only)
28. **`pub.polis.post/save_draft`** — Saves the owner's unfinished post (`POST /v1/content/post/actions/save_draft`; owner only); drafts are read through the query operation (`all drafts from me`)
29. **`pub.polis.post/discard_draft`** — Discards one of the owner's drafts (`POST /v1/content/post/actions/discard_draft`; owner only)
30. **`pub.polis.post/get`** — The markdown of one of the site's posts, for editing (`GET /v1/content/post/source?path=…`; scope `read`)
31. **`pub.polis.comment/get`** — The markdown of one of the site's comments (`GET /v1/content/comment/source?path=…`; scope `read`)
32. **`pub.polis.post/body`** — Any site's post in full, rendered to HTML (`GET /v1/content/post/body?url=…`; scope `read`)

Bless, deny, unbless, follow and unfollow are declared by the host (`webapp/internal/api/blessing.go`, `follow.go`), as are the three reads by path (`webapp/internal/server/content_source.go`), and served by the host's own functions, the one implementation of each, which the owner app reaches through these same operations.

### Not Offered

The generic routes answer these, with `400 unsupported_action`; none is in `/v1/openapi.json`, and a test fails
if a described operation ever answers that way:

- Post: get by id (read it through `…/post/source`), delete (taking a post down is `unpublish`), render (the
  site is rendered by `POST /v1/site/rebuild`)
- Comment: list and get by id (the query operation and `…/comment/source`), revoke (taking a blessing back is
  `unbless`), sync (the site syncs its blessings itself)

### Known Gaps

1. **Payload validation** — No per-action schema validation. Bad inputs produce confusing errors deep in handler code.
2. **Pagination** — List operations return all results. Needs cursor/limit support.
3. **No feed type** — the feed is populated by the webapp's background sync loop and is not a content type this API serves.
