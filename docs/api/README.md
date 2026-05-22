# Content Type API

The Content Type API provides programmatic access to polis content operations via REST endpoints under `/v1/`.

## Scope

This API covers **content type operations only**: publishing posts, managing comments, handling blessings, querying feeds and following lists. Site settings, theme switching, dashboard aggregations, and setup wizards remain in the webapp/CLI.

## Authentication

- **Read operations** (GET on content and bundles) are public — no auth required
- **Write operations** require `Authorization: Bearer <api-key>`
- API keys are generated via `polis api-key create` and stored as SHA-256 hashes

## CORS

All API routes allow `*` origins. OPTIONS preflight returns 200.

## Body Limits

Request bodies are limited to 1MB.

## Quick Start

```bash
# Generate an API key
polis api-key create --name "my-script"

# List posts (public)
curl https://mysite.example.com/v1/content/post

# Publish a post (auth required)
curl -X POST https://mysite.example.com/v1/content/post \
  -H "Authorization: Bearer polis_abc123..." \
  -H "Content-Type: application/json" \
  -d '{"markdown": "# Hello\n\nFirst API post."}'
```

## Documentation

| Document | Audience | Description |
|----------|----------|-------------|
| [developer/reference.md](developer/reference.md) | Developers | Routes, request/response examples, error codes, implementation status |
| [developer/dispatch-engine.md](developer/dispatch-engine.md) | Developers | Engine architecture, handler types, adding operations |

## See Also

- [docs/general/content-system.md](../general/content-system.md) — Content types, bundles, and events
- [docs/ds/developer/api-reference.md](../ds/developer/api-reference.md) — Discovery service API (separate from the content API)
