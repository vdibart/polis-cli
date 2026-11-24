# The site's API

**A site has one API: the operations under `/v1/`.** Each site describes its own at `/v1/openapi.json`
(OpenAPI 3.1, generated from the operations themselves, never written by hand), and every caller runs the
same operations: the owner's own app, an agent over MCP (whose tools are generated from that description),
a script with a token, and the `polis` CLI, which runs them in process on a local site with no server.

## Scope

Content (posts, comments, follows, tags, direct messages, drafts), the site's settings and facts (its name,
avatar, about, theme and design tokens, policy, terms), its agents (tokens, connections, Rosie), its
messages key, what the owner has seen, and reads of anything through the query operation (PQL).

**What is deliberately outside `/v1`**, each for a reason: getting signed in (a caller cannot need a
credential to obtain one), polis.pub's account and export (the operator's, not the site's), the capability
descriptor (read before a caller knows the API), site-to-site calls (the discovery service waking a site, a
message delivered by another site), and the embeddable widget (polis.pub's own service). All of these are listed in
[developer/site-api.md](developer/site-api.md). The old browser app and its `/api/` routes are gone; do not
build on what is left of `/api/`.

⚠️ `/v1` is a **preview**: an operation's shape may still change, and what that promises is stated once, in
[The API describes itself](developer/reference.md#the-api-describes-itself).

## Authentication, CORS, limits and a quick start

All in [developer/reference.md](developer/reference.md): reads are mostly public, writes take a Bearer API key the site's owner mints ([§ Authentication](developer/reference.md#authentication)), and the site-to-site DM actions take signed request headers instead. That page is the contract; this index does not restate it.

## Documentation

| Document | Audience | Description |
|----------|----------|-------------|
| [developer/reference.md](developer/reference.md) | Developers | Authentication, paths, errors and answers, every operation |
| [developer/mcp.md](developer/mcp.md) | Developers | MCP and agent access: tools generated from the description, the three servers, the grant on every call |
| [developer/dispatch-engine.md](developer/dispatch-engine.md) | Developers | Engine architecture, handler types, adding operations |
| [developer/site-api.md](developer/site-api.md) | Developers | What is outside `/v1`, and why; where the old `/api/` site operations moved |

## See Also

- [docs/general/concepts/content-system.md](../general/concepts/content-system.md) — Content types, bundles, and events
- [docs/ds/developer/api-reference.md](../ds/developer/api-reference.md) — Discovery service API (separate from the content API)
