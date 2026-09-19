# Content Type API

The Content Type API provides programmatic access to polis content operations via REST endpoints under `/v1/`.

## Scope

This API covers **content type operations only**: publishing posts, publishing and republishing comments, direct messages, tags, and reading following lists, themes and licence terms. Site settings, theme switching, dashboard aggregations, and setup wizards remain in the webapp/CLI.

Those webapp-owned operations live under `/api/` on a **separate, owner-authenticated surface** with its own auth model — a different audience and a different way in, but documented here alongside the content API rather than somewhere else. See [developer/site-api.md](developer/site-api.md). It indexes every `/api/` route and writes up the site operations a script may rely on. The rest are marked internal: the web app's own plumbing, which may change without notice.

## Authentication, CORS, limits and a quick start

All in [developer/reference.md](developer/reference.md): reads are mostly public, writes take a Bearer API key you create by hand ([§ Authentication](developer/reference.md#authentication)), and the site-to-site DM actions take signed request headers instead. That page is the contract; this index does not restate it.

## Documentation

| Document | Audience | Description |
|----------|----------|-------------|
| [developer/reference.md](developer/reference.md) | Developers | Routes, request/response examples, error codes, implementation status |
| [developer/dispatch-engine.md](developer/dispatch-engine.md) | Developers | Engine architecture, handler types, adding operations |
| [developer/site-api.md](developer/site-api.md) | Developers | The webapp's `/api/` surface: an index of every route, and the site operations written up in full |

## See Also

- [docs/general/concepts/content-system.md](../general/concepts/content-system.md) — Content types, bundles, and events
- [docs/ds/developer/api-reference.md](../ds/developer/api-reference.md) — Discovery service API (separate from the content API)
