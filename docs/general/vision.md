# Polis Vision

This document combines the project manifesto (why polis exists) with the experience principles (how it meets users where they are).

---

## Part 1: A Social Layer for the Open Web

### The Problem

Social media platforms are walled gardens. Your content, your audience, your identity — all held at the pleasure of a company that can change the rules, alter the algorithm, or disappear entirely.

- **Substack** takes 10% and owns the subscriber list — you're a tenant, not an owner
- **Medium** shows competitor content below your articles — your readers are their product
- **Twitter/X** can vaporize your account and audience with no recourse
- **LinkedIn** charges you to reach followers you already have

We need a better meeting place.

### Where Previous Attempts Went Wrong

Most decentralization efforts started from the question: "How do we build Twitter/Facebook, but decentralized?"

This framing begs for complex architectures based on federation protocols and instance selection.

The result: systems that are difficult to explain, challenging to configure and deploy, confusing to join, and not useful until you've already convinced your friends to switch. It's just a different kind of lock-in.

Polis asks a different question.

### A Different Starting Question

What if social didn't require a platform?

Not "how do I claim my social space" but **"how do we gather without a third party dictating the terms?"**

This framing changes the possibilities:

- A private group of five people sharing links and commentary? That works.
- A solo writer publishing essays and fielding responses? Same primitives.
- A community that wants shared ownership, no single authority? Also possible.
- A commons that nobody owns? The architecture supports it.

The key shift: Polis' primitives don't have an agenda. They can be assembled into any shape. The system is agnostic to governance — it just provides the building blocks for gathering and conversation.

### The Architecture That Follows

**Self-hostable by default.** No instance to choose, no federation to configure. Content that exists as files on your domain cannot be deplatformed, rate-limited, or algorithmically suppressed.

**Composable primitives.** Posts, comments, identity, discovery — each is a separable concern. Use what you need.

**Plain text and well-known structures.** Markdown files, predictable paths, signed content. No proprietary formats, no API required to read your own data.

| Primitive | Implementation | Why It Matters |
|-----------|----------------|----------------|
| **Content** | Markdown + cryptographic signatures | Files, not database records. Impossible to lock in. |
| **Identity** | Your domain + private key | `alice.com`, not `npub1x7fq...` or `@alice@mastodon.social` |
| **Hosting** | Static files | GitHub Pages, Vercel, your own nginx. Already free. |
| **Discovery** | Coordination service + AI | A thin layer, not a platform |

### How the Social Part Works

Polis' **Discovery Service** is a layer of metadata management. It doesn't store content — it stores metadata about content on the network to enable connection and discovery. Most users will rely on the canonical Discovery Service. Some communities will run their own. Some users may use none at all. Nothing breaks.

With Polis' **blessing model** your comments publish to your server. The original post's author can amplify them (or not) but the comment lives on. Conversations aren't censored. They're curated.

### Where This Goes

Today, Polis looks like a publishing tool. But the primitives point somewhere:

- **Publishing** becomes signing files to your domain. No platform needed.
- **Following** becomes a list of URLs. No algorithm decides what you see.
- **Conversation** becomes linked posts across domains. No one owns the thread.
- **Reputation** becomes your cryptographic history. No platform can grant or revoke it.

That's the long-term vision. Not a better social network, but the end of social networks as a category.

### Business Model: Open Core

The line is clear: everything below the ownership bar is free and open. Above that bar, paid services provide convenience.

**Always free:** access to your content, baseline tools (CLI / webapp), protocol spec, data format, ability to crawl content and switch providers.

**Potentially paid:** managed hosting, domain provisioning, real-time data firehose, advanced analytics, specialized discovery services.

**The test:** Can a technical user do this for free? If yes, the paid version is convenience, not lock-in.

---

## Part 2: Experience Principles

### The Core Promise

Users own their content. This isn't a feature — it's the foundation everything is built on.

A casual user who signs up through a hosting provider with a username and password owns their content in exactly the same way a developer running their own infrastructure does. The underlying format is identical: markdown files, cryptographic signatures, exportable keys.

Moving "up" toward convenience or "down" toward control requires no data migration, no format conversion, no special tools. Export produces the same output at every level.

### The Seven Levels

| Level | Archetype | Key Location | Content Location | What Polis Provides |
|-------|-----------|--------------|------------------|---------------------|
| 1 | **Builder** | Local, self-managed | Local, self-hosted | Spec + CLI + reference impl |
| 2 | **Developer** | Local | Local + deploy templates | CLI + templates + discovery |
| 3 | **Power User** | Local (guided) | Local (guided) | Installer + guided setup |
| 4 | **Technical Writer** | Local (in app) | Local (visible folder) | Desktop app wrapping CLI |
| 5 | **Blogger** | Browser/local | Managed (markdown files) | Web app + storage backend |
| 6 | **Burned Before** | Custodied (encrypted) | Managed | Full social + visible safety net |
| 7 | **Casual** | Custodied (encrypted) | Managed | Turnkey, indistinguishable from platforms |

### Level Details

**Level 1 (Builder):** Raw CLI, manual everything. Developers who read RFCs for fun.

**Level 2 (Developer):** CLI for authoring, provided deploy templates. Full visibility, trusts shared infrastructure.

**Level 3 (Power User):** Guided installer, step-by-step CLI with prompts. The tooling explains what it's doing, building intuition.

**Level 4 (Technical Writer):** Desktop app with markdown editor. Files live in a normal, browsable folder — the "aha" moment that makes moving down feel safe.

**Level 5 (Blogger):** Web-based editor, feels like a blogging platform. Managed storage that's literally just a folder of markdown files per user. Signing could still happen client-side.

**Level 6 (Burned Before):** Mobile-first social with a visible safety net. The export capability isn't a feature they'll use — it's a feature they need to *see*. A "Your Data" dashboard with a big green "Download Everything" button.

**Level 7 (Casual):** Username, password, post. Indistinguishable from centralized platforms. But the promise still applies — their data is still markdown files, their key is still their key.

### Architectural Invariants

These must be true at *every* level:

1. **Content is always markdown files** — never a proprietary format or database schema
2. **Signatures are always the same format** — generated the same way, verifiable the same way
3. **Keys are always exportable** — even custodied keys can be downloaded or content resigned
4. **Export produces identical output** — a Level 7 export is byte-for-byte usable at Level 1
5. **CLI is always the foundation** — every layer above is calling it (or reimplementing its logic faithfully)

### Transition Points

| Transition | Work Required |
|------------|---------------|
| **L3 → L4** | Desktop app that wraps CLI (Tauri?) |
| **L4 → L5** | Web app + managed storage that preserves file-based model |
| **L5 → L6** | Client-side key encryption, custody model, "Your Data" dashboard |
| **L6 → L7** | Frictionless onboarding flow that hides complexity without breaking promises |
