# Polis CLI

[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](https://github.com/vdibart/polis-cli?tab=AGPL-3.0-1-ov-file)
[![Platform: Linux | macOS | Windows](https://img.shields.io/badge/Platform-Linux%20%7C%20macOS%20%7C%20Windows-lightgrey.svg)]()

**A decentralized social network for the open web.**

Your posts live on your domain. Your followers are yours. Your content persist even if the network disappears. No oversight, no lock-in, no algorithm. 

---

## The idea

Social networks captured something valuable—the connections between people—then held it hostage. Polis gives that back.

With Polis, your content publishes to your server. You decide which comments to amplify or hide, but the comment is hosted on the commenter's server.  No algorithms, no oversight, no spam - just self-moderating conversation.

Built on standards you already trust: HTTPS for delivery, Ed25519 for signatures. Your content is markdown files. Your identity is a keypair. Move hosts anytime—everything comes with you.

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

Works with GitHub Pages, Vercel, Netlify, or any static host.

---

## Quick start

### Prerequisites

```bash
# macOS
brew install openssh jq curl pandoc git

# Ubuntu/Debian
sudo apt-get install openssh-client jq curl pandoc git

# Windows (Git Bash)
# See docs/WINDOWS.md for detailed setup instructions
# Quick setup: .\scripts\setup-windows.ps1
```

### Install

```bash
git clone https://github.com/vdibart/polis-cli.git
export PATH="$PATH:$(pwd)/polis-cli/bin"
```

### Initialize and publish

```bash
mkdir my-site && cd my-site
polis init
export POLIS_BASE_URL="https://yourdomain.com"

echo "# Hello World" > hello.md
polis post hello.md
```

### Deploy

```bash
polis render                    # Generate HTML
git init && git add . && git commit -m "First post"
git push                        # To GitHub Pages, Netlify, etc.
```

---

## Going deeper

**Interactive mode** — `polis-tui` provides a menu-driven dashboard with keyboard navigation, git integration, and your $EDITOR for writing posts.

**Scripting** — All commands support `--json` for machine-readable output. Pipe content directly, automate blessing workflows, integrate with other tools.

**Tutorial** — New to Polis? Run `polis-tutorial` for an interactive walkthrough with simulated commands.

**AI integration** — Polis includes a [Claude Code](https://claude.ai/code) skill for natural language workflows: "publish my draft", "check my blessing requests", "comment on Alice's post".

---

## Documentation

- **[USAGE.md](docs/USAGE.md)** — Complete command reference
- **[WINDOWS.md](docs/WINDOWS.md)** — Windows setup and usage guide
- **[JSON-MODE.md](docs/JSON-MODE.md)** — JSON output for scripting
- **[TEMPLATING.md](docs/TEMPLATING.md)** — Customize your site's HTML
- **[TUI.md](docs/TUI.md)** — Terminal user interface
- **[UPGRADING.md](docs/UPGRADING.md)** — Version migrations
- **[SECURITY-MODEL.md](docs/SECURITY-MODEL.md)** — Cryptographic details
- **[MANIFESTO.md](docs/MANIFESTO.md)** — Vision and philosophy

---

## Contributing

We welcome contributions! See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## Support

Questions or issues? [Open a GitHub issue](https://github.com/vdibart/polis-cli/issues)

## License

**AGPL-3.0** — See [LICENSE](https://github.com/vdibart/polis-cli?tab=AGPL-3.0-1-ov-file)

---

*Polis: Your content, your network, your rules.*
