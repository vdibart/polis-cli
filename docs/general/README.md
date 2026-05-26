# General Documentation

Cross-cutting documentation that applies to the polis project as a whole — protocol specifications, project governance, and reference material.

## Documents

| Document | Description |
|----------|-------------|
| [architecture.md](architecture.md) | System overview — the four surfaces (CLI, webapp, polis.pub, DS), how they relate, and where to go for X |
| [bundles.md](bundles.md) | The package container — what a bundle is, what it ships, how it installs per tenant |
| [content-types.md](content-types.md) | The data model — core content types, actions, public/private split, custom types |
| [shapes.md](shapes.md) | The rendering approach — blog (v3) vs infinity stream (v4), the render pipeline |
| [themes.md](themes.md) | The presentation layer — CSS-only themes, the variable contract, cross-theme compatibility |
| [infinity-stream.md](infinity-stream.md) | The single-screen sentence-filtered view (`pub.polis.shapes.v4`) — what it means, why it represents polis.pub, three POVs |
| [content-system.md](content-system.md) | Deep reference: filesystem layout, full `bundle.json` schema, event catalog |
| [snap-off-architecture.md](snap-off-architecture.md) | The layer-by-layer replaceability table — every layer's default, why you'd replace, how, what you keep |
| [security-model.md](security-model.md) | Cryptographic foundations, identity, trust model, policies, attack vectors, and threat analysis |
| [actors.md](actors.md) | The background actors — Patrol, Medic, Judge, Clerk, Chaplain, Reaper, and Tailor — that keep polis sites healthy: what each detects, heals, reconciles, or verifies |
| [policy-grammar.md](policy-grammar.md) | Authoritative spec for v2 policy grammar (Layer 1/2/3, verb-by-type matrix) |
| [pql.md](pql.md) | Polis Query Language — sentence-driven stream filtering |
| [registration-and-privacy.md](registration-and-privacy.md) | DS registration, hard delete, self-hosting your own DS |
| [vision.md](vision.md) | Project manifesto and experience principles — why polis exists and how it meets users |
| [in-defense-of-bless.md](in-defense-of-bless.md) | Why polis uses the word "bless" — its four senses, what it actually does here, and what it says about polis that no platform can claim |
| [glossary.md](glossary.md) | Polis-specific terminology reference |
| [contributing.md](contributing.md) | Development setup, testing, and contribution guidelines |
| [security.md](security.md) | Vulnerability reporting policy (threat model is in `security-model.md`) |

## See Also

- [docs/README.md](../README.md) — Documentation navigation index
- [Architecture overview](../cli/README.md) — CLI package structure and build targets
- [Discovery stream](../ds/developer/stream-architecture.md) — Event stream design
