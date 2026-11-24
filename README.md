# Polis

[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](https://github.com/vdibart/polis-cli?tab=AGPL-3.0-1-ov-file)
[![Platform: Linux | macOS | Windows](https://img.shields.io/badge/Platform-Linux%20%7C%20macOS%20%7C%20Windows-lightgrey.svg)]()

**Your identity is your domain and a key only you hold. Everything you publish is signed with it, so it stays yours wherever it lives.**

Polis is a social network where you publish, follow and have conversations from your own site. There is no account to suspend and no platform in the middle deciding who sees what.

---

## Who you are, and why anyone should believe it

- **Identity: a domain plus a private key.** Your address says where to find you; your key proves it's you. Nobody issues it, and nobody can take it back.

- **Trust you can check, not trust you're asked for.** Every post, comment, follow and licence is signed with your key. A reader doesn't have to trust your host, or us, that you said it.

- **Transparency: anyone can audit it.** A stranger can fetch a post, fetch your key, walk your site's key history back to its first key, and check every signature, without asking any central service. The background services that keep polis.pub healthy are listed publicly with what each is expected to do, so what they do can be compared with what they say.

## What you control

- **Consent: you decide what happens to you and your work.** Anyone can reply to your posts, on their own site; you choose which replies your readers see, and you can let a helper decide for you within rules you set. You state, in a signed licence, the terms your work may be used under, including whether AI may train on it, and polis publishes those terms where crawlers look.

## Where it lives, and how it behaves

- **Portability: no host is privileged.** Run polis on your laptop, on a host you choose, or on polis.pub. A site can move to a new host and a new address and keep its history: every post you signed still verifies, no post is re-signed, and anyone can check the handover from your old key to your new one.

- **Durability: hard to break, hard to take away, hard to make extinct.** A post is a text file with a signature, served over HTTP. There is no database to corrupt, no account to suspend, and no format that stops being readable.

## How others find it

- **Discoverability.** A discovery service helps people find conversations across sites. It records and relays, and it never decides anything on your behalf.

---

## Three ways to run it

**On your own machine** (`local`): the polis CLI, a single binary. Sign everything locally and deploy to any static host: GitHub Pages, Netlify, a VPS. [Get started](#get-started) in a few commands.

**On polis.pub** (`custodied`): sign up, get `yourname.polis.pub` and start writing. Same signed artifacts, managed for you.

**On a host you run** (`remote`): your own polis instance on a platform such as Vercel, signing for you while you're away. ⚠️ Experimental.

**Why more than one?** A decentralized network needs people in it before it sustains itself. polis.pub is how people join and start writing today without setting up hosting first. The goal is a network where polis.pub is one node among many, and leaving it costs you nothing you signed.

---

## Get started

```bash
curl -fsSL https://raw.githubusercontent.com/vdibart/polis-cli/main/scripts/install.sh | bash

mkdir my-site && cd my-site
export POLIS_BASE_URL="https://yourdomain.com"
polis init

echo "# Hello World" > hello.md
polis post hello.md
polis render                    # Generate HTML

# Deploy
git init && git add . && git commit -m "First post"
git push                        # To GitHub Pages, Netlify, etc.
```

`polis post`, `polis follow`, `polis discover`, `polis comment`, and more. All support `--json` for scripting and automation. See the [full command reference](docs/cli/user/command-reference.md).

---

## See it

```bash
$ export POLIS_BASE_URL="https://yourdomain.com"
$ polis init
Your posts can carry terms describing how others may use them.

  1. Reserved  (recommended)
  2. Open
  3. Unstated
  …
Press enter to state nothing for now.

Choice: 1

Rosie, your helper
  …
Switch Rosie on? Type yes or no (press enter to leave her off): no
[✓] Initialized polis site at: /home/you/my-site
[i] Public key: ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA...
[i] DID: did:web:yourdomain.com
[✓] Licence: pub.polis.license.reserved/1
[i] Rosie: off. Comments will wait for you to approve them yourself.
    Switch her on whenever you like in the web app (`polis-full serve` or `polis-server`): Settings → Rosie.

$ polis post essay.md
[✓] Moved original file into posts/
Published: content/pub.polis.core/post/20260920/essay.md
Title: Essay
Version: sha256:b2a1809836f3...

$ polis follow https://alice.dev

[✓] Successfully followed https://alice.dev
  - Added to following.json
```

---

## The blessing model

Polis replaces top-down moderation with author-controlled curation.

1. Someone comments on your post (the comment lives on *their* domain)
2. They request your blessing via the discovery service
3. You review and grant or deny — blessed comments appear on your rendered post
4. Unblessed comments still exist on the commenter's domain, just not amplified to your audience

Curated conversation without censorship.

---

## Installation

### Pre-built binary (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/vdibart/polis-cli/main/scripts/install.sh | bash
```

The installer checks the release's signature with the polis release key, then the archive against the signed checksums, and installs nothing if either fails. It needs `ssh-keygen` (OpenSSH 8.0+); [SECURITY.md](SECURITY.md#security-best-practices-for-users) shows how to verify a download by hand.

Four binaries are available on [GitHub Releases](https://github.com/vdibart/polis-cli/releases):

| Binary | What you get | Size |
|--------|-------------|------|
| **`polis`** (recommended) | CLI | ~12 MB |
| `polis-full` | CLI + local web UI | ~19 MB |
| `polis-server` | Web UI only | ~18 MB |
| `tailor` | Site migration & health tool for self-hosters | ~9 MB |

### Build from source

```bash
git clone https://github.com/vdibart/polis-cli.git
cd polis-cli && make all
./dist/polis version
```

---

## Going deeper

- **Themes** — Eight built-in themes to choose from, with Mustache-style templating. See [Templating](docs/cli/user/templating.md).
- **JSON mode** — Every command supports `--json` for scripting and automation. See [JSON Mode](docs/cli/user/json-mode.md).
- **AI integration** — Polis includes a [Claude Code](https://claude.ai/code) skill for natural language workflows: "publish my draft", "check my blessing requests", "comment on Alice's post".

---

## The bash CLI as specification

The bash CLI (`cli-bash/polis`) is the feature-frozen original implementation (~9,500 lines of bash); see [implementation parity](docs/cli/implementation-parity.md) for what it lacks. It needs only bash, jq, curl and ssh. It serves as a readable, executable specification — purpose-built for developers and LLMs to reference when porting Polis to other languages. Not deprecated, not legacy: a spec you can run.

---

## Architecture

```
┌─────────────┐     imports     ┌─────────────┐
│   CLI (Go)  │ ──────────────▶ │   Webapp    │
│  cli-go/    │    cli-go/pkg/  │  webapp/    │
└──────┬──────┘                 └──────┬──────┘
       │                               │
       │ register/query                │ /v1/ API
       ▼                               ▼
┌──────────────────────────────────────────────┐
│          Discovery Service (TypeScript)      │
└──────────────────────────────────────────────┘
```

The discovery service's source is not in this repository. Its public contract is the [DS API reference](docs/ds/developer/api-reference.md). polis.pub's discovery service accepts up to 2,000 new content records per domain in any rolling 30 days; updates to records it already holds do not count.

---

## Documentation

**Start at [docs/README.md](docs/README.md)**: it routes you by what you are here to do. Also:

- [Trust, provenance and terms](docs/signet/README.md): signed terms of use, key history, attestations and how to verify a site
- [Reading paths](docs/paths/): guided routes through the docs, for security review and for building on polis
- [How polis.pub is run](docs/ops/README.md): the design of the hosted service and its background actors

### For Users

| Document | Description |
|----------|-------------|
| [Command Reference](docs/cli/user/command-reference.md) | Complete command reference |
| [Templating](docs/cli/user/templating.md) | Theme customization and template syntax |
| [JSON Mode](docs/cli/user/json-mode.md) | JSON output for scripting |
| [API Reference](docs/api/developer/reference.md) | Content Type REST API |
| [Glossary](docs/general/reference/glossary.md) | Polis-specific terminology |

### For Developers

| Document | Description |
|----------|-------------|
| [Content System](docs/general/concepts/content-system.md) | Bundles, content types, events, filesystem layout |
| [Security Model](docs/general/security/security-model.md) | Cryptographic foundations, threat model, policies |
| [CLI Packages](docs/cli/developer/packages.md) | Package structure, import rules, version propagation |
| [Webapp Development](docs/webapp/developer/development.md) | Handler patterns, testing, frontend architecture |
| [Dispatch Engine](docs/api/developer/dispatch-engine.md) | API engine architecture and handler types |
| [Contributing](docs/general/contributing.md) | Development setup and contribution guidelines |

### General

| Document | Description |
|----------|-------------|
| [Vision](docs/general/vision.md) | Why Polis exists — manifesto and experience principles |
| [Security Policy](docs/general/security/SECURITY.md) | Reporting vulnerabilities |

---

## Contributing

We welcome contributions! See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## Support

Questions or issues? [Open a GitHub issue](https://github.com/vdibart/polis-cli/issues)

## License

**AGPL-3.0** — See [LICENSE](https://github.com/vdibart/polis-cli?tab=AGPL-3.0-1-ov-file)

---

*Your content, your domain, your rules.*
