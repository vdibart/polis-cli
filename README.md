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

- **No algorithms, no fees, no lock-in.** No engagement metrics, no 10% platform cut, no terms of service that change under your feet.

---

## Two ways to get started

**Polis CLI** — A single binary you run on your machine. Publish from your own domain, sign everything locally, deploy to any static host. Full control, no dependencies on us.

**polis.pub** — A hosted service where you get `yourname.polis.pub` and start publishing immediately. Same features, managed infrastructure, no server to maintain.

**Why both?** Decentralized networks need a critical mass of participants before they become self-sustaining. polis.pub bootstraps that network — it gives people a way to join and start writing today, without setting up hosting first. The goal is a future where most authors self-host and polis.pub is just one node among many. Think of it as scaffolding: necessary now, designed to become optional.

---

## See it

```bash
$ polis init
[✓] Generated Ed25519 keypair
[✓] Created .well-known/polis
[✓] Ready to publish

$ polis post essay.md
[i] Content hash: sha256:a3b5c7d9...
[i] Signing with Ed25519 key...
[✓] Published: posts/2026/01/essay.md

$ polis follow https://alice.dev
[✓] Following alice.dev
[i] 12 posts, 3 with comments
```

---

## Get started

```bash
curl -fsSL https://raw.githubusercontent.com/vdibart/polis-cli/main/scripts/install.sh | bash

mkdir my-site && cd my-site
polis init
export POLIS_BASE_URL="https://yourdomain.com"

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

Three binaries are available on [GitHub Releases](https://github.com/vdibart/polis-cli/releases):

| Binary | What you get | Size |
|--------|-------------|------|
| **`polis`** (recommended) | CLI | ~9 MB |
| `polis-full` | CLI + local web UI | ~12 MB |
| `polis-server` | Web UI only | ~11 MB |

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

The bootstrap bash implementation (`cli-bash/polis`) is a single ~8500-line file that implements the complete Polis protocol with minimal dependencies (bash, jq, curl, ssh). It serves as a readable, executable specification — purpose-built for developers and LLMs to reference when porting Polis to other languages. Not deprecated, not legacy: a spec you can run.

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

---

## Documentation

### For Users

| Document | Description |
|----------|-------------|
| [Command Reference](docs/cli/user/command-reference.md) | Complete command reference |
| [Templating](docs/cli/user/templating.md) | Theme customization and template syntax |
| [JSON Mode](docs/cli/user/json-mode.md) | JSON output for scripting |
| [API Reference](docs/api/user/reference.md) | Content Type REST API |
| [Glossary](docs/general/glossary.md) | Polis-specific terminology |

### For Developers

| Document | Description |
|----------|-------------|
| [Content System](docs/general/content-system.md) | Bundles, content types, events, filesystem layout |
| [Security Model](docs/general/security-model.md) | Cryptographic foundations, threat model, policies |
| [CLI Packages](docs/cli/developer/packages.md) | Package structure, import rules, version propagation |
| [Webapp Development](docs/webapp/developer/development.md) | Handler patterns, testing, frontend architecture |
| [Dispatch Engine](docs/api/developer/dispatch-engine.md) | API engine architecture and handler types |
| [Contributing](docs/general/contributing.md) | Development setup and contribution guidelines |

### General

| Document | Description |
|----------|-------------|
| [Vision](docs/general/vision.md) | Why Polis exists — manifesto and experience principles |
| [Security Policy](docs/general/security.md) | Reporting vulnerabilities |

---

## Contributing

We welcome contributions! See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## Support

Questions or issues? [Open a GitHub issue](https://github.com/vdibart/polis-cli/issues)

## License

**AGPL-3.0** — See [LICENSE](https://github.com/vdibart/polis-cli?tab=AGPL-3.0-1-ov-file)

---

*Your content, your domain, your rules.*
