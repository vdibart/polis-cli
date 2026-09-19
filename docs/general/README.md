# General Documentation

Concepts and reference that span every component, **grouped by what they are about**: the three things polis is built from —
**identity** (who said it), **content** (what was said) and **relationships** (who vouches for whom, and who may reach whom).
A page about how those combine sits under *Across all three*.

Pages in [`signet/`](../signet/README.md), the identity-layer specification suite, are listed here too. Signet's own index
orders them by kind: concepts, specs, guides and recipes.

## Across all three

| Page | What it is about |
|---|---|
| [concepts/architecture.md](concepts/architecture.md) | the four surfaces (CLI, webapp, hosted service, discovery service), how they relate, and where to go for what |
| [concepts/snap-off-architecture.md](concepts/snap-off-architecture.md) | every layer's default, why you would replace it, how, and what you keep |
| [How polis thinks about identity and trust](../signet/overview.md) | the identity layer on one page, including what it has not solved |
| [Assertions](../signet/concepts/assertions.md) · [Projection](../signet/concepts/projection.md) · [Verification](../signet/concepts/verification.md) | what a person can say, what is signed versus computed, and what a verifier may conclude |
| [concepts/actors.md](concepts/actors.md) | the background actors on a hosted service, and what each may and may not do |
| [security/security-model.md](security/security-model.md) | cryptographic foundations, key management, attack vectors and threat analysis |
| [security/known-issues.md](security/known-issues.md) | known integrity issues, in plain language |
| [reference/glossary.md](reference/glossary.md) | polis's words |
| [vision.md](vision.md) | why polis exists |
| [contributing.md](contributing.md) · [security/SECURITY.md](security/SECURITY.md) | changing polis, and reporting a vulnerability |

## Identity

| Page | What it is about |
|---|---|
| [Identity](../signet/concepts/identity.md) | a polis identity is a domain and a keypair |
| [Key history](../signet/spec/key-history.md) · [did:web](../signet/spec/did-web.md) | surviving a key rotation, and the same identity as a DID document |
| [Delegation](../signet/concepts/delegation.md) | why a user agent signs with the user's key, marks every act, and can be switched off |
| [Custody](../signet/spec/custody.md) · [Delegation](../signet/spec/delegation.md) | what an operator holds, and what a user grants an agent |
| [Witnesses](../signet/spec/witness.md) | a discovery service's signed observation that something existed |
| [security/who-holds-your-key.md](security/who-holds-your-key.md) | who holds your key on polis.pub and on your own machine, for the person whose key it is |
| [security/security-model.md § Key Custody](security/security-model.md#key-custody) | where a private key lives, and who holds it on a hosted service |
| [security/registration-and-privacy.md](security/registration-and-privacy.md) | registering with a discovery service, what it learns, and what leaving removes |

## Content

| Page | What it is about |
|---|---|
| [concepts/content-types.md](concepts/content-types.md) | the data model: core content types, actions, public and private |
| [concepts/bundles.md](concepts/bundles.md) | the package container: what a bundle ships and how it installs |
| [concepts/shapes.md](concepts/shapes.md) · [concepts/themes.md](concepts/themes.md) | rendering approaches, and CSS-only presentation |
| [guides/themes.md](guides/themes.md) | switching a theme, and writing your own |
| [concepts/infinity-stream.md](concepts/infinity-stream.md) | the single-screen, sentence-filtered view |
| [concepts/content-system.md](concepts/content-system.md) | the deep reference: filesystem layout, `bundle.json`, event catalog |
| [reference/pql.md](reference/pql.md) | the Polis Query Language |
| [Signing base](../signet/spec/signing-base.md) | the exact bytes every signature covers |
| [Licence](../signet/spec/license.md) | an author's signed, outbound terms |

## Relationships

| Page | What it is about |
|---|---|
| [Trust](../signet/concepts/trust.md) · [The graph](../signet/concepts/graph.md) | why follows and blessings are signed, and walking the graph they make |
| [Attestation](../signet/spec/attestation.md) | one party's signed claim about another's work |
| [concepts/policy-and-licence.md](concepts/policy-and-licence.md) | inbound policy against outbound licence: two directions that share a noun |
| [reference/policy-grammar.md](reference/policy-grammar.md) | inbound policy: who may reach you, and what you accept |
| [reference/in-defense-of-bless.md](reference/in-defense-of-bless.md) | why polis says "bless", and what a blessing does |
| [security/dm-encryption.md](security/dm-encryption.md) | direct messages: what encryption protects, and what it does not; the bytes are in the [format](../../cli-go/pkg/dm/FORMAT.md) and [protocol](../../cli-go/pkg/dm/PROTOCOL.md) |

## See Also

- [docs/README.md](../README.md): the documentation's doors, reading paths and kinds of page
- [Discovery stream](../ds/developer/stream-architecture.md): the event stream design
