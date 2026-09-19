# Polis

[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](https://github.com/vdibart/polis-cli?tab=AGPL-3.0-1-ov-file)
[![Platform: Linux | macOS | Windows](https://img.shields.io/badge/Platform-Linux%20%7C%20macOS%20%7C%20Windows-lightgrey.svg)]()

**A decentralized social network that makes the open web fun again.**

Polis helps you publish, follow, and have conversations — without losing control over your content. Your posts are signed markdown files. Your identity is a keypair. Easily deploy to GitHub Pages, Netlify, or any static host and own everything you create.

---

## Why Polis

- **Your content can't be deplatformed.** Posts are markdown files signed with your Ed25519 key, hosted on your domain. No API to shut off, no account to suspend.

- **Comments without spam.** Anyone can comment on your posts (published on *their* domain). You bless the ones you want your audience to see. Unblessed comments still exist — just not on your site.

- **Move hosts anytime.** Everything is flat files. Switch from GitHub Pages to Netlify to a VPS — your content, keys, and followers come with you.

- **Your terms, signed; your history, provable.** State the terms your work may be used under, including whether AI may train on it, as a signed licence your site publishes where crawlers look. Your site keeps a signed history of its keys, so posts you signed years ago still verify after you rotate a key.

- **No algorithms, no fees, no lock-in.** No engagement metrics, no 10% platform cut, no terms of service that change under your feet.

---

## Two ways to get started

**Polis CLI** — A single binary you run on your machine. Publish from your own domain, sign everything locally, deploy to any static host. Full control, no dependencies on us.

**polis.pub** — A hosted service where you get `yourname.polis.pub` and start publishing immediately. Same features, managed infrastructure, no server to maintain.

**Why both?** Decentralized networks need a critical mass of participants before they become self-sustaining. polis.pub bootstraps that network — it gives people a way to join and start writing today, without setting up hosting first. The goal is a future where most authors self-host and polis.pub is just one node among many. Think of it as scaffolding: necessary now, designed to become optional.

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

Four binaries are available on [GitHub Releases](https://github.com/vdibart/polis-cli/releases):

| Binary | What you get | Size |
|--------|-------------|------|
| **`polis`** (recommended) | CLI | ~10 MB |
| `polis-full` | CLI + local web UI | ~13 MB |
| `polis-server` | Web UI only | ~12 MB |
| `tailor` | Site migration & health tool for self-hosters | ~9 MB |

### Build from source

```bash
git clone https://github.com/vdibart/polis-cli.git
cd polis-cli && make all
./dist/polis version
```

---

## Going deeper

- **Themes** — Seven built-in themes with Mustache-style templating. See [Templating](docs/cli/user/templating.md).
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
