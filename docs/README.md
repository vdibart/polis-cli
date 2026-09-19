# Polis Documentation

Polis is a way to publish on your own domain, sign what you write, and follow, comment and vouch across sites, with nothing in
the middle that can quietly rewrite who said what. These pages explain how it works, how to use it, how to build on it, and where
its design stops.

**Pick the door that describes you.** Each one is an ordered reading list: start at the top.

> **New here?** [The architecture overview](general/concepts/architecture.md) is the four-surface map that situates every other
> page, and [the vision](general/vision.md) says why polis exists. An LLM helping a human, or anyone arriving from a
> *"Pull the thread"* trail marker in source code, can start at [AGENTS.md](../AGENTS.md) at the repository root.

## Writing on polis

*You publish, in a browser or at the command line.*

| Page | What it is for |
|---|---|
| [User manual](webapp/user/user-manual.md) | **start here in a browser**: everything the web interface does |
| [Themes](general/guides/themes.md) | switch your site's theme, or write your own CSS-only one |
| [Set your terms](signet/guides/set-your-terms.md) | say how others may use your work |
| [Policy and licence](general/concepts/policy-and-licence.md) | who may reach you, against what may be done with your work: two different things |
| [Who holds your key](general/security/who-holds-your-key.md) | on polis.pub or on your own machine: what each means, what you can check, and how to take your key back |
| [Known issues](general/security/known-issues.md) | what is imperfect, in plain language |
| [Direct-message encryption](general/security/dm-encryption.md) | what it protects, and what it does not |
| [Registration and privacy](general/security/registration-and-privacy.md) | what the discovery service learns about you, and what leaving removes |
| [Command reference](cli/user/command-reference.md) | **start here at the command line**: every CLI command |
| [Policies](cli/user/policies.md) | who may comment on you, and what you accept |
| [Check a site](signet/guides/verify-content.md) · [Make a claim](signet/guides/attest.md) · [Who blessed this?](signet/guides/who-blessed-this.md) | the signed layer, used by hand |
| [Implementation parity](cli/implementation-parity.md) | which CLI you need: where the Go and bash CLIs differ |
| [Glossary](general/reference/glossary.md) | polis's words |

## Building on polis

*You are writing software that reads, writes or verifies polis, or a second implementation of it.*

| Page | What it is for |
|---|---|
| [The graph](signet/concepts/graph.md) | **start here**: identities, records, and the signed edges between them |
| [Recipe book](signet/recipes/README.md) | tasks, each run end to end by the test suite |
| [Content API](api/developer/reference.md) · [Site API](api/developer/site-api.md) | the webapp's REST surfaces |
| [Discovery service API](ds/developer/api-reference.md) · [PQL](general/reference/pql.md) · [PQL over HTTP](ds/developer/pql-json-api.md) | querying the network |
| [Stream architecture](ds/developer/stream-architecture.md) · [Unpublish lifecycle](ds/developer/unpublish-lifecycle.md) | how events and retractions travel |
| [JSON mode](cli/user/json-mode.md) | scripting the CLI |
| [Content types](general/concepts/content-types.md) · [Bundles](general/concepts/bundles.md) · [Shapes](general/concepts/shapes.md) · [Themes](general/concepts/themes.md) · [Templating](cli/user/templating.md) | the content model and its presentation; [writing a theme](general/guides/themes.md) |
| [Direct-message format](../cli-go/pkg/dm/FORMAT.md) · [delivery protocol](../cli-go/pkg/dm/PROTOCOL.md) | the bytes of an encrypted direct message, at rest and between sites |
| [Infinity stream](general/concepts/infinity-stream.md) | the single-screen view polis.pub is built around |
| [Snap-off architecture](general/concepts/snap-off-architecture.md) | replacing any layer |
| **A second implementation:** [Signet specifications](signet/README.md) · [Signing base](signet/spec/signing-base.md) · [Policy grammar](general/reference/policy-grammar.md) · [Content system](general/concepts/content-system.md) | precise enough to implement |

## Reviewing the security and identity design

*You assess designs like this for a living, and want the threat model, the trust assumptions and the limits.*

| Page | What it is for |
|---|---|
| [How polis thinks about identity and trust](signet/overview.md) | **start here**: the design on one page, including what it has not solved |
| [Security model](general/security/security-model.md) | the threat model and attack analysis |
| [Known issues](general/security/known-issues.md) | including what a signature does not prove |
| [Identity](signet/concepts/identity.md) · [Verification](signet/concepts/verification.md) · [Projection](signet/concepts/projection.md) · [Assertions](signet/concepts/assertions.md) · [Trust](signet/concepts/trust.md) · [Delegation](signet/concepts/delegation.md) | the concepts |
| [Policy and licence](general/concepts/policy-and-licence.md) | inbound rules against outbound terms, and who applies each |
| [Key history](signet/spec/key-history.md) · [Custody](signet/spec/custody.md) · [Delegation](signet/spec/delegation.md) · [Witnesses](signet/spec/witness.md) · [did:web](signet/spec/did-web.md) · [Signing base](signet/spec/signing-base.md) | each states what it does **not** claim |
| [The actors](general/concepts/actors.md) | what runs on a hosted service besides you, and what each may do |
| [Who holds your key](general/security/who-holds-your-key.md) | custody, as the person whose key it is sees it |
| [Direct-message encryption](general/security/dm-encryption.md), with its [format](../cli-go/pkg/dm/FORMAT.md) and [protocol](../cli-go/pkg/dm/PROTOCOL.md) · [Registration and privacy](general/security/registration-and-privacy.md) | confidentiality, and what the discovery service learns |
| [Check it yourself](signet/recipes/05-verify-a-site.md) · [Verify a follow file by hand](signet/recipes/14-verify-signed-edges.md) | a site you do not own, trusting nothing |
| [Report a vulnerability](general/security/SECURITY.md) | |

## Running polis

*You run polis for other people, or want to know how polis.pub is run. This is how the service is designed. The hosted service's
and the discovery service's source is not public, so operational procedure is not documented here.*

| Page | What it is for |
|---|---|
| [How polis.pub is run](ops/README.md) | **start here** |
| [The operator's manual](ops/admin/operator-guide.md) | custody, the authority rule, and what an operator takes on |
| [The actors](general/concepts/actors.md) | the background jobs, by design |
| [The hosted service](ops/admin/hosted-service.md) | its architecture and a tenant's lifecycle |
| [Discovery service deployment](ds/admin/deployment.md) · [configuration](ds/admin/configuration.md) | what a deployment consists of, and what an operator can tune |
| [What Judge could not verify](signet/guides/network-health.md) | reading a hosted fleet's health from outside |

## Contributing to polis

*You are changing polis itself.*

| Page | What it is for |
|---|---|
| [Contributing](general/contributing.md) | **start here**: setup and conventions |
| [Handbook](handbook/README.md) | guided tours of the source, reached from trail markers in the code |
| [CLI packages](cli/developer/packages.md) · [Webapp development](webapp/developer/development.md) · [Feed architecture](webapp/developer/feed-architecture.md) · [Dispatch engine](api/developer/dispatch-engine.md) | the codebase |
| [Brand](webapp/designer/brand.md) · [Navigation](webapp/designer/navigation.md) · [Pages](webapp/designer/pages.md) | the design system |

## Reading paths

Some questions cut across the whole system. These pages string the relevant documents together, in order.

- [Security](paths/security.md): from the threat model to checking a site yourself
- [Trust, provenance and terms](paths/trust-provenance-and-terms.md): what is signed, what is computed, and why the difference matters
- [Building on polis](paths/building-on-polis.md): from the architecture to a second implementation

## Kinds of page

| Kind | Answers | Where |
|---|---|---|
| **Concept** | why it is this way | `signet/concepts/`, `general/concepts/`, indexed [by what it is about](general/README.md) |
| **Spec** | exactly what, precisely enough to implement | `signet/spec/`, `general/reference/` |
| **Guide** | how to do something, in prose | `signet/guides/`, `general/guides/`, each component's `user/` |
| **Recipe** | how to do something, as commands a test runs | `signet/recipes/` |
| **Reference** | look it up | each component: `cli/`, `webapp/`, `api/`, `ds/`, `ops/` |
| **Tour** | how the source does it | `handbook/` |

Every page opens with one line under its title saying who it is for, what kind of page it is, and what to read next. A
**See also** entry reading *no spec yet* or *no recipe yet* marks a page that does not exist yet.

## How these docs are kept true

Documentation is checked against the code before each release. Two kinds of page are checked on **every** test run: every
**recipe**'s commands are executed, and every **specification**'s worked examples are compared byte for byte with the
implementation. Every link between these pages, and every page path cited from the code, is checked on every test run too.
If a page is wrong, that is a bug: open an issue, or [report it privately](general/security/SECURITY.md) if it is a security
matter.

## By Component

| Component | Description | README |
|-----------|-------------|--------|
| [general/](general/) | Concepts and reference that span every component, indexed by what they are about | [general/README.md](general/README.md) |
| [signet/](signet/) | **Signet**: the identity-layer specification suite | [signet/README.md](signet/README.md) |
| [handbook/](handbook/) | Guided tours of polis source code | [handbook/README.md](handbook/README.md) |
| [cli/](cli/) | CLI tool (Go and bash implementations) | [cli/README.md](cli/README.md) |
| [webapp/](webapp/) | Local web interface (Go SPA) | [webapp/README.md](webapp/README.md) |
| [api/](api/) | Content Type REST API (`/v1/`) | [api/README.md](api/README.md) |
| [ds/](ds/) | Discovery Service (coordination layer) | [ds/README.md](ds/README.md) |
| [ops/](ops/) | How polis.pub is run | [ops/README.md](ops/README.md) |
