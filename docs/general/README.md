# General Documentation

Cross-cutting documentation that applies to the polis project as a whole — protocol specifications, project governance, and reference material.

## Start here

| Document | Description |
|----------|-------------|
| [vision.md](vision.md) | Project manifesto and experience principles — why polis exists and how it meets users |
| [contributing.md](contributing.md) | Development setup, testing, and contribution guidelines |

## Concepts — how polis is built

| Document | Description |
|----------|-------------|
| [concepts/architecture.md](concepts/architecture.md) | System overview — the four surfaces (CLI, webapp, polis.pub, DS), how they relate, and where to go for X |
| [concepts/snap-off-architecture.md](concepts/snap-off-architecture.md) | The layer-by-layer replaceability table — every layer's default, why you'd replace, how, what you keep |
| [concepts/bundles.md](concepts/bundles.md) | The package container — what a bundle is, what it ships, how it installs per tenant |
| [concepts/content-types.md](concepts/content-types.md) | The data model — core content types, actions, public/private split, custom types |
| [concepts/content-system.md](concepts/content-system.md) | Deep reference: filesystem layout, full `bundle.json` schema, event catalog |
| [concepts/shapes.md](concepts/shapes.md) | The rendering approach — blog (v3) vs infinity stream (v4), the render pipeline |
| [concepts/themes.md](concepts/themes.md) | The presentation layer — CSS-only themes, the variable contract, cross-theme compatibility |
| [concepts/infinity-stream.md](concepts/infinity-stream.md) | The single-screen sentence-filtered view (`pub.polis.shapes.v4`) — what it means, why it represents polis.pub, three POVs |
| [concepts/actors.md](concepts/actors.md) | The background actors that keep a polis site healthy (Patrol, Medic, Judge, Clerk, Chaplain, Reaper, Rosie, Tailor) |

## Reference — specs & lookup

| Document | Description |
|----------|-------------|
| [reference/pql.md](reference/pql.md) | Polis Query Language — sentence-driven stream filtering |
| [reference/policy-grammar.md](reference/policy-grammar.md) | Authoritative spec for v2 policy grammar (Layer 1/2/3, verb-by-type matrix) |
| [reference/glossary.md](reference/glossary.md) | Polis-specific terminology reference |
| [reference/in-defense-of-bless.md](reference/in-defense-of-bless.md) | Why polis uses the word "bless" — its four senses, what it actually does here, and what it says about polis that no platform can claim |

## Security & privacy

| Document | Description |
|----------|-------------|
| [security/security-model.md](security/security-model.md) | Cryptographic foundations, identity, trust model, policies, attack vectors, and threat analysis |
| [security/dm-encryption.md](security/dm-encryption.md) | Direct-message encryption — what it protects, what it doesn't, and how it works |
| [security/registration-and-privacy.md](security/registration-and-privacy.md) | DS registration, hard delete, self-hosting your own DS |
| [security/SECURITY.md](security/SECURITY.md) | Vulnerability reporting policy (the deep threat model is in `security/security-model.md`) |

## See Also

- [docs/README.md](../README.md) — Documentation navigation index
- [Architecture overview](../cli/README.md) — CLI package structure and build targets
- [Discovery stream](../ds/developer/stream-architecture.md) — Event stream design
