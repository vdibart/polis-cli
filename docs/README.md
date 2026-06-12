# Polis Documentation

Documentation is organized by component and audience. Each component directory has a README with an overview, quick start, and links to its documents.

> **New here?** Start with [general/concepts/architecture.md](general/concepts/architecture.md) — the four-surface map that situates every other doc in this tree. For an LLM helping a human, or anyone arriving from a "Pull the thread" trail marker in source code, the canonical entry is [AGENTS.md](../AGENTS.md) at the repo root.

## By Component

| Component | Description | README |
|-----------|-------------|--------|
| [general/](general/) | Protocol specs, project governance, reference | [general/README.md](general/README.md) |
| [handbook/](handbook/) | Museum tours — curated walk-throughs of polis source code | [handbook/README.md](handbook/README.md) |
| [cli/](cli/) | CLI tool (Go + Bash implementations) | [cli/README.md](cli/README.md) |
| [webapp/](webapp/) | Local web interface (Go SPA) | [webapp/README.md](webapp/README.md) |
| [api/](api/) | Content Type REST API (`/v1/`) | [api/README.md](api/README.md) |
| [ds/](ds/) | Discovery Service (coordination layer) | [ds/README.md](ds/README.md) |

## By Audience

### For Users

| Document | Component | Description |
|----------|-----------|-------------|
| [Webapp User Manual](webapp/user/user-manual.md) | webapp | How to use the local web interface |
| [CLI Command Reference](cli/user/command-reference.md) | cli | Complete command reference |
| [Templating](cli/user/templating.md) | cli | Theme customization and template syntax |
| [JSON Mode](cli/user/json-mode.md) | cli | Machine-readable `--json` output format |
| [Glossary](general/reference/glossary.md) | general | Polis-specific terminology |

### For Developers

| Document | Component | Description |
|----------|-----------|-------------|
| [Architecture Overview](general/concepts/architecture.md) | general | The four surfaces (CLI, webapp, polis.pub, DS) and how they fit together |
| [Bundles](general/concepts/bundles.md) | general | The package container — namespaces, manifests, per-tenant install |
| [Content Types](general/concepts/content-types.md) | general | Core types (post/comment/follow/blessing/tag/dm/theme), actions, lifecycle |
| [Shapes](general/concepts/shapes.md) | general | Blog (v3) vs infinity stream (v4), the render pipeline |
| [Themes](general/concepts/themes.md) | general | CSS-only presentation, variable contract, cross-theme compat |
| [Infinity Stream](general/concepts/infinity-stream.md) | general | The single-screen `pub.polis.shapes.v4` experience — philosophy, three POVs, hydration flow |
| [Content System](general/concepts/content-system.md) | general | Deep reference — filesystem layout, full `bundle.json` schema, event catalog |
| [Security Model](general/security/security-model.md) | general | Crypto, identity, trust model, policies, threats |
| [CLI Packages](cli/developer/packages.md) | cli | Package structure, import rules, version propagation |
| [Webapp Development](webapp/developer/development.md) | webapp | Handler patterns, testing, frontend architecture |
| [API Reference](api/developer/reference.md) | api | REST API routes, examples, error codes |
| [Dispatch Engine](api/developer/dispatch-engine.md) | api | Engine architecture, handler types |
| [DS API Reference](ds/developer/api-reference.md) | ds | Discovery service REST API (20+ endpoints) |
| [Stream Architecture](ds/developer/stream-architecture.md) | ds | Event stream design and protocol |
| [Storage Adapter](ds/developer/storage-adapter.md) | ds | Custom storage adapter interface |
| [Contributing](general/contributing.md) | general | Development setup and contribution guidelines |

### For Operators

| Document | Component | Description |
|----------|-----------|-------------|
| [DS Deployment](ds/admin/deployment.md) | ds | Deploy on Fly.io, Docker, or bare Deno |
| [DS Configuration](ds/admin/configuration.md) | ds | Admin API (policy-based) and ~28 tuning parameters |

### For Everyone

| Document | Component | Description |
|----------|-----------|-------------|
| [Vision](general/vision.md) | general | Why Polis exists — manifesto and experience principles |
| [Security Policy](general/security/SECURITY.md) | general | Vulnerability reporting |

