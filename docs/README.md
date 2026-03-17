# Polis Documentation

Documentation is organized by component and audience. Each component directory has a README with an overview, quick start, and links to its documents.

## By Component

| Component | Description | README |
|-----------|-------------|--------|
| [general/](general/) | Protocol specs, project governance, reference | [general/README.md](general/README.md) |
| [cli/](cli/) | CLI tool (Go + Bash implementations) | [cli/README.md](cli/README.md) |
| [webapp/](webapp/) | Local web interface (Go SPA) | [webapp/README.md](webapp/README.md) |
| [api/](api/) | Content Type REST API (`/v1/`) | [api/README.md](api/README.md) |
| [ds/](ds/) | Discovery Service (coordination layer) | [ds/README.md](ds/README.md) |
| [ops/](ops/) | Platform operations (polis.pub) | [ops/README.md](ops/README.md) |

## By Audience

### For Users

| Document | Component | Description |
|----------|-----------|-------------|
| [Webapp User Manual](webapp/user/user-manual.md) | webapp | How to use the local web interface |
| [CLI Command Reference](cli/user/command-reference.md) | cli | Complete command reference |
| [Templating](cli/user/templating.md) | cli | Theme customization and template syntax |
| [JSON Mode](cli/user/json-mode.md) | cli | Machine-readable `--json` output format |
| [API Reference](api/user/reference.md) | api | REST API routes, examples, error codes |
| [Glossary](general/glossary.md) | general | Polis-specific terminology |

### For Developers

| Document | Component | Description |
|----------|-----------|-------------|
| [Content System](general/content-system.md) | general | Bundles, content types, events, filesystem layout |
| [Security Model](general/security-model.md) | general | Crypto, identity, trust model, policies, threats |
| [CLI Packages](cli/developer/packages.md) | cli | Package structure, import rules, version propagation |
| [Webapp Development](webapp/developer/development.md) | webapp | Handler patterns, testing, frontend architecture |
| [Dispatch Engine](api/developer/dispatch-engine.md) | api | Engine architecture, handler types |
| [DS API Reference](ds/developer/api-reference.md) | ds | Discovery service REST API (20+ endpoints) |
| [Stream Architecture](ds/developer/stream-architecture.md) | ds | Event stream design and protocol |
| [Storage Adapter](ds/developer/storage-adapter.md) | ds | Custom storage adapter interface |
| [Contributing](general/contributing.md) | general | Development setup and contribution guidelines |

### For Operators

| Document | Component | Description |
|----------|-----------|-------------|
| [DS Deployment](ds/admin/deployment.md) | ds | Deploy on Fly.io, Docker, or bare Deno |
| [DS Configuration](ds/admin/configuration.md) | ds | Admin API and 24 tuning parameters |
| [Operations Runbook](ops/admin/runbook.md) | ops | Monitoring, backup, recovery, incident response |
| [Structured Logging](ops/admin/logging.md) | ops | Log format, event catalog, Loki queries |
| [Hosted Service](ops/admin/hosted-service.md) | ops | Multi-tenant hosted platform (polis.pub) |

### For Everyone

| Document | Component | Description |
|----------|-----------|-------------|
| [Vision](general/vision.md) | general | Why Polis exists — manifesto and experience principles |
| [Security Policy](general/security.md) | general | Vulnerability reporting |

## Archived

Historical planning documents are in [archive/](archive/).
