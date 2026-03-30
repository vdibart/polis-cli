# Polis Security Model

Polis takes a fundamentally different approach to security than traditional social platforms. Rather than relying on a central authority to authenticate users and protect content, polis uses cryptographic signatures to prove authorship and content integrity. Every post, comment, and interaction is signed with the author's Ed25519 private key, and anyone can verify authenticity by checking the signature against the author's published public key. This document describes the cryptographic foundations, identity model, signature scheme, trust model, access control policies, known attack vectors, and feature-level security analysis for the entire polis system.

## Table of Contents

1. [Cryptographic Foundations](#1-cryptographic-foundations)
2. [Key Management](#2-key-management)
3. [Identity Document (.well-known/polis)](#3-identity-document-well-knownpolis)
4. [Signature Model](#4-signature-model)
5. [Trust Model](#5-trust-model)
6. [Policies](#6-policies)
7. [Attack Vectors and Mitigations](#7-attack-vectors-and-mitigations)
8. [Feature Security Analysis](#8-feature-security-analysis)
9. [Known Limitations](#9-known-limitations)
10. [Future Considerations](#10-future-considerations)
11. [Document History](#11-document-history)
12. [Feedback Welcome](#12-feedback-welcome)

---

## 1. Cryptographic Foundations

### Ed25519 Digital Signatures

Polis uses **Ed25519** (Edwards-curve Digital Signature Algorithm) for all cryptographic signing operations. Ed25519 was chosen for:

| Property | Detail |
|----------|--------|
| **Algorithm** | EdDSA over Curve25519 |
| **Key size** | 256-bit (32-byte) keys |
| **Signature size** | 64 bytes |
| **Security level** | ~128-bit equivalent |
| **Performance** | Fast signing and verification |
| **Deterministic** | Same input always produces the same signature (no random nonce) |
| **Compatibility** | Used by OpenSSH, widely supported |

### SSH Signature Format

Polis uses the **SSH signature format** (`ssh-keygen -Y sign/verify`) rather than raw Ed25519 signatures. This provides:

- **Standard format**: PEM-encoded signature blocks that are text-safe
- **Namespace isolation**: Signatures include a "file" namespace preventing cross-protocol attacks
- **Hash-then-sign**: Content is SHA-512 hashed before signing (via the SSH signing blob)
- **Tool compatibility**: Signatures can be independently verified using `ssh-keygen -Y verify`

The signing process internally constructs an SSH signing blob:

```
SSHSIG || length(namespace) || namespace || length(reserved) || reserved
       || length(hash_algorithm) || hash_algorithm || length(H(content)) || H(content)
```

Where `H` is SHA-512. The Ed25519 signature is computed over this blob, not the raw content.

### SHA-256 Content Hashing

Content integrity uses **SHA-256** hashes stored in the `current-version` frontmatter field:

| Property | Detail |
|----------|--------|
| **Algorithm** | SHA-256 |
| **Hash size** | 256 bits (32 bytes, 64 hex characters) |
| **Purpose** | Content integrity verification, version tracking |
| **Format** | `sha256:<hex-digest>` in frontmatter |

The hash is computed over the canonicalized body content (leading empty lines stripped, trailing whitespace trimmed per line, single trailing newline). This ensures identical content produces identical hashes regardless of trivial whitespace differences.

### SHA-256 API Key Hashing

API keys for the v1 REST API are hashed with SHA-256 before storage. Only the hash is persisted in `.polis/api-keys.json`; the plaintext key is shown once at generation time and never stored. Validation uses constant-time comparison (`crypto/subtle.ConstantTimeCompare`) to prevent timing attacks.

### Ed25519 to X25519 Key Conversion

Polis reuses existing Ed25519 signing keys for DM encryption by converting them to X25519 at runtime. This is a well-established pattern used by Signal and libsodium (`crypto_sign_ed25519_pk_to_curve25519`).

| Conversion | Method |
|------------|--------|
| **Private key** | SHA-512 of Ed25519 seed + scalar clamping → X25519 scalar |
| **Public key** | Edwards→Montgomery point conversion → X25519 point |
| **Library** | `filippo.io/edwards25519` for point conversion |
| **Caching** | Conversion is deterministic; results cached in memory |
| **Storage** | No new key material stored on disk |

### NaCl Box (Transport Encryption)

Direct messages use NaCl box for transport encryption between sender and recipient:

| Property | Detail |
|----------|--------|
| **Algorithm** | X25519 Diffie-Hellman key agreement + XSalsa20-Poly1305 authenticated encryption |
| **Nonce** | Random 24 bytes per message (from `crypto/rand`) |
| **Integrity** | Poly1305 MAC provides integrity and authenticity |
| **Library** | `golang.org/x/crypto/nacl/box` |

### NaCl Secretbox (Storage Encryption)

After receiving a DM, content is re-encrypted with a local symmetric key for at-rest storage:

| Property | Detail |
|----------|--------|
| **Algorithm** | XSalsa20-Poly1305 symmetric authenticated encryption |
| **Key** | Derived via HKDF-SHA256 from private key seed + site-wide salt |
| **Nonce** | Random 24 bytes per message |
| **Library** | `golang.org/x/crypto/nacl/secretbox` |

### HKDF Key Derivation

Storage keys for encrypted content types are derived using HKDF:

| Property | Detail |
|----------|--------|
| **Algorithm** | HKDF-SHA256 |
| **Secret** | Ed25519 private key seed (32 bytes) |
| **Salt** | `.polis/storage-salt` (32 random bytes, generated once per site) |
| **Info** | Purpose string (e.g., `"polis-dm-storage-v1"`) |
| **Output** | 32-byte symmetric key |

The salt is not secret — its purpose is domain separation. Different content types derive different keys using different `info` strings from the same salt.

---

## 2. Key Management

### Key Generation

Both CLI implementations generate Ed25519 keypairs during `polis init`:

| Implementation | Method | Storage |
|----------------|--------|---------|
| Bash CLI | `ssh-keygen -t ed25519 -f <path> -N "" -C "polis@$(hostname)"` | OpenSSH PEM format |
| Go CLI | `crypto/ed25519.GenerateKey(crypto/rand.Reader)` with custom OpenSSH encoding | OpenSSH PEM format |

The Go CLI implements its own OpenSSH private key encoder to avoid depending on external tools, while producing byte-compatible output with `ssh-keygen`.

### Key Storage

Keys are stored in the site's private directory:

```
.polis/keys/
  id_ed25519      # Private key (PEM, mode 0600)
  id_ed25519.pub  # Public key (OpenSSH single-line format, mode 0644)
```

| File | Permissions | Published | Purpose |
|------|-------------|-----------|---------|
| `id_ed25519` | `0600` | Never | Signing content |
| `id_ed25519.pub` | `0644` | Via `.well-known/polis` | Signature verification |

The `.polis/` directory is listed in `.gitignore` by default, so private keys are never committed to version control. The `.gitignore` created during `polis init` contains:

```gitignore
# Polis internals and secrets
.polis/
.env*
```

### Key Distribution

The public key is distributed through the `.well-known/polis` identity document (see Section 3). Anyone who can fetch `https://example.com/.well-known/polis` can obtain the public key needed to verify signatures.

### Domain Binding

Keys are bound to domains through the `.well-known/polis` file:

1. Author publishes their public key at `https://domain.com/.well-known/polis`
2. Verifier fetches the public key from the author's domain
3. Verifier checks the signature using the fetched key
4. Trust is established through DNS ownership (controlling a domain implies controlling the `.well-known/polis` file)

There is no certificate authority or key registry. Domain ownership is the root of trust.

### Safety: Init Refuses to Overwrite

Both CLI implementations check for existing keys before generation and refuse to overwrite them. The Go CLI checks for the existence of `id_ed25519`, `id_ed25519.pub`, and `.well-known/polis` before proceeding, returning an error if any already exist. This prevents accidental key loss.

### Key Audit Summary (Bash CLI)

| Check | Status |
|-------|--------|
| Private key never published | Enforced by `.gitignore` and directory convention |
| Private key permissions | `0600` set at creation |
| Key generated with strong RNG | `ssh-keygen` uses system CSPRNG |
| No key escrow or backup | User responsibility |
| Key never transmitted | Only public key published |
| No password protection | Keys are unencrypted (cipher: `none`) |

### Key Audit Summary (Go CLI)

| Check | Status |
|-------|--------|
| Private key never published | Enforced by `.gitignore` and directory convention |
| Private key permissions | `0600` set at `os.WriteFile` |
| Key generated with strong RNG | `crypto/rand.Reader` (system CSPRNG) |
| No key escrow or backup | User responsibility |
| Key never transmitted | Only public key published |
| No password protection | Keys are unencrypted (cipher: `none`) |
| OpenSSH format compatibility | Custom encoder produces ssh-keygen-compatible output |

---

## 3. Identity Document (.well-known/polis)

The `.well-known/polis` file is the identity document and bundle registry for a polis site. It serves as the root of trust for signature verification and site configuration discovery.

### Location

```
https://example.com/.well-known/polis
```

The file is served as JSON. On disk it lives at `.well-known/polis` relative to the site root (no file extension).

### JSON Schema

```json
{
  "version": "<generator-string>",
  "public_key": "<ssh-ed25519 public key in OpenSSH format>",
  "author": "<display name>",
  "created": "<ISO-8601 timestamp>",
  "email": "<optional contact email>",
  "site_title": "<optional site title>",
  "author_name": "<optional full display name>",
  "avatar": "<optional avatar styling config>",
  "active_theme": "<optional theme name>",
  "bundles": {
    "<bundle-id>": {
      "active": true,
      "path": "<relative path to bundle.json>"
    }
  }
}
```

### Required Fields

| Field | Type | Description |
|-------|------|-------------|
| `version` | string | Generator string (e.g., `polis-cli-go/0.57.0` or `polis-cli/0.45.0`) |
| `public_key` | string | Full OpenSSH public key line (e.g., `ssh-ed25519 AAAA... polis-local`) |
| `author` | string | Display name for the site author |
| `created` | string | ISO-8601 timestamp of site creation (e.g., `2026-01-15T12:00:00Z`) |

### Optional Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `email` | string | omitted | Contact email. Private by default; only written if explicitly provided during `polis init`. |
| `site_title` | string | omitted | Human-readable site title |
| `author_name` | string | omitted | Full display name (distinct from `author` which is typically the domain handle) |
| `avatar` | object | omitted | Custom avatar styling config (see Avatar Config below) |
| `active_theme` | string | omitted | Currently active theme name (e.g., `sols`, `turbo`, `zane`) |
| `bundles` | object | omitted | Bundle registry mapping bundle IDs to their configuration |

#### Avatar Config

The `avatar` object controls the visual representation of the site author:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `bg` | string | yes | Background color |
| `fg` | string | yes | Foreground/text color |
| `border` | string | no | Border color |
| `border_w` | int | no | Border width in pixels |
| `pattern` | string | no | Pattern name for avatar decoration |
| `pattern_color` | string | no | Color for the pattern overlay |

### Bundle Registry

The `bundles` field maps bundle identifiers to their registration entries:

```json
{
  "bundles": {
    "pub.polis.core": {
      "active": true,
      "path": "content/pub.polis.core/bundle.json"
    },
    "pub.polis.photos": {
      "active": true,
      "path": "content/pub.polis.photos/bundle.json"
    }
  }
}
```

Each bundle entry has:

| Field | Type | Description |
|-------|------|-------------|
| `active` | boolean | Whether this bundle is currently enabled |
| `path` | string | Relative path from site root to the bundle's `bundle.json` |

The bundle ID follows reverse-domain notation (e.g., `pub.polis.core`, `com.example.photos`). The `pub.polis.core` bundle is always created during `polis init` and contains the standard content types (post, comment, follow, feed).

### Validation Rules

1. `version` must be a non-empty string
2. `public_key` must start with `ssh-ed25519 ` and contain a valid base64-encoded key blob
3. `author` must be a non-empty string
4. `created` must be a valid ISO-8601 timestamp
5. If `bundles` is present, each entry must have a `path` string and `active` boolean
6. The `pub.polis.core` bundle should always be present for standard polis sites

### Go Struct Definition

```go
// BundleEntry represents a bundle registration in .well-known/polis.
type BundleEntry struct {
    Active bool   `json:"active"`
    Path   string `json:"path"`
}

// WellKnown represents the .well-known/polis v2 file structure.
// This is the identity document and bundle registry for a polis site.
type WellKnown struct {
    Version     string                 `json:"version"`
    PublicKey   string                 `json:"public_key"`
    Author      string                 `json:"author"`
    Email       string                 `json:"email,omitempty"`
    SiteTitle   string                 `json:"site_title,omitempty"`
    AuthorName  string                 `json:"author_name,omitempty"`
    Avatar      *AvatarConfig          `json:"avatar,omitempty"`
    Created     string                 `json:"created"`
    ActiveTheme string                 `json:"active_theme,omitempty"`
    Bundles     map[string]BundleEntry `json:"bundles,omitempty"`
}

// AvatarConfig controls the visual representation of the site author.
type AvatarConfig struct {
    BG           string `json:"bg"`
    FG           string `json:"fg"`
    Border       string `json:"border,omitempty"`
    BorderW      int    `json:"border_w,omitempty"`
    Pattern      string `json:"pattern,omitempty"`
    PatternColor string `json:"pattern_color,omitempty"`
}
```

### Example: Minimal Identity Document

```json
{
  "version": "polis-cli-go/0.57.0",
  "public_key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyHere polis-local",
  "author": "Alice",
  "created": "2026-01-15T12:00:00Z"
}
```

### Example: Full Identity Document

```json
{
  "version": "polis-cli-go/0.57.0",
  "public_key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyHere polis-local",
  "author": "Alice",
  "email": "alice@example.com",
  "site_title": "Alice's Thoughts",
  "author_name": "Alice Johnson",
  "avatar": {
    "bg": "#1a1a2e",
    "fg": "#e0e0e0",
    "border": "#16213e",
    "border_w": 2,
    "pattern": "dots",
    "pattern_color": "#0f3460"
  },
  "created": "2026-01-15T12:00:00Z",
  "active_theme": "sols",
  "bundles": {
    "pub.polis.core": {
      "active": true,
      "path": "content/pub.polis.core/bundle.json"
    }
  }
}
```

### Example: Multi-Bundle Identity Document

```json
{
  "version": "polis-cli-go/0.57.0",
  "public_key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyHere polis-local",
  "author": "Bob",
  "site_title": "Bob's Space",
  "created": "2026-02-01T08:30:00Z",
  "active_theme": "turbo",
  "bundles": {
    "pub.polis.core": {
      "active": true,
      "path": "content/pub.polis.core/bundle.json"
    },
    "pub.polis.photos": {
      "active": true,
      "path": "content/pub.polis.photos/bundle.json"
    },
    "com.example.recipes": {
      "active": false,
      "path": "content/com.example.recipes/bundle.json"
    }
  }
}
```

---

## 4. Signature Model

### What Gets Signed

Every piece of content in polis carries a cryptographic signature. The signature covers the content from the beginning of the file up to (but not including) the `signature:` frontmatter line.

#### Posts

Posts are signed during `polis post` (or `polis publish`). The signed content includes:

- All frontmatter fields (title, published date, current-version hash, generator)
- The full markdown body

```yaml
---
title: My Post Title
published: 2026-01-15T12:00:00Z
current-version: sha256:abc123...
version-history:
  - sha256:abc123... (2026-01-15T12:00:00Z)
generator: polis-cli-go/0.57.0
signature: <SSH signature block>
---

Post body content here...
```

The signature is computed over everything from `---` through the body, excluding the `signature:` line itself. This means the signature covers the content hash (`current-version`), creating a binding between the signature and the content integrity check.

#### Comments

Comments follow the same signing pattern but include additional `in-reply-to` metadata:

```yaml
---
title: Re: Original Post Title
type: comment
author: alice.com
published: 2026-01-15T14:00:00Z
in-reply-to:
  url: https://author.com/content/pub.polis.core/post/original-post.md
  root-post: https://author.com/content/pub.polis.core/post/original-post.md
current-version: sha256:789abc...
version-history:
  - sha256:789abc... (2026-01-15T14:00:00Z)
generator: polis-cli-go/0.57.0
signature: <SSH signature block>
---

Comment body content here...
```

#### Blessings

Blessing requests are signed by the commenter and include the comment content. The site owner's blessing (approval) or denial is a separate signed action communicated through the discovery service.

### Verification Flow

Content verification follows these steps:

1. **Fetch content** from the author's URL
2. **Parse frontmatter** to extract `signature` and `current-version` fields
3. **Fetch public key** from `https://<author-domain>/.well-known/polis`
4. **Verify signature** against the content (everything before `signature:` line)
5. **Verify content hash** by computing SHA-256 of the canonicalized body and comparing to `current-version`

```
Verifier                     Author's Site
   |                              |
   |-- GET /content/post/x.md -->|
   |<-- content + signature ------|
   |                              |
   |-- GET /.well-known/polis --->|
   |<-- { public_key: "..." } ---|
   |                              |
   | Verify signature(content, pubkey)
   | Verify hash(body) == current-version
```

Both checks must pass for content to be considered authentic and unmodified:

| Result | Signature | Hash | Interpretation |
|--------|-----------|------|----------------|
| Authentic | valid | valid | Content is from the claimed author and unmodified |
| Tampered | valid | mismatch | Content was modified after signing (should not happen with correct implementation) |
| Forged | invalid | any | Content was not signed by the claimed author |
| Unsigned | missing | any | Content has no cryptographic guarantee of authorship |

### Instance-to-Instance Signed Requests

For DM delivery, the sender's instance authenticates to the recipient's instance using signed request headers. This eliminates the need for pre-shared API keys between instances.

**Headers:**

| Header | Content |
|--------|---------|
| `X-Polis-Domain` | Sender's domain (e.g., `alice.example.com`) |
| `X-Polis-Signature` | Ed25519 SSH signature over canonical JSON |
| `X-Polis-Timestamp` | ISO-8601 timestamp of the request |

**Canonical JSON:**
```json
{"action":"deliver","domain":"alice.example.com","timestamp":"2026-03-07T10:00:00Z"}
```

**Verification flow:**

1. Receiver extracts `X-Polis-Domain` header
2. Receiver checks `X-Polis-Timestamp` is within 5-minute window (replay protection)
3. Receiver fetches sender's `.well-known/polis` to obtain public key (cached for 1 hour)
4. Receiver reconstructs canonical JSON from headers
5. Receiver verifies `X-Polis-Signature` against sender's public key

This is the same pattern used by the discovery service client (`addAuthHeaders` in `discovery/client.go`), extended to instance-to-instance communication.

**Dual auth model:** The v1 API accepts either auth method depending on the action:

| Action | Auth Method | Who Uses It |
|--------|------------|-------------|
| `list`, `get`, `send`, `mark_read`, `delete`, `retry` | Bearer token | Site owner (local operations) |
| `deliver` | Signed request headers | Remote instances (incoming DMs) |

---

## 5. Trust Model

### What Polis Trusts

Polis has a deliberately minimal trust model:

| Trust Anchor | What It Implies | How It's Verified |
|-------------|-----------------|-------------------|
| Domain ownership | Author controls the content at that domain | DNS + TLS (HTTPS) |
| Public key at `.well-known/polis` | Key belongs to domain owner | Fetched over HTTPS |
| Signature on content | Content was signed by key holder | Ed25519 verification |
| Content hash in frontmatter | Body has not been modified since signing | SHA-256 comparison |

### What Polis Does NOT Trust

| Not Trusted | Why |
|------------|-----|
| Email addresses | Not verified; optional field in `.well-known/polis` |
| Display names | Self-reported; no verification |
| Timestamps | Self-reported; no trusted timestamping |
| The discovery service | Used for coordination, not authentication (see below) |
| Other sites' content | Always fetched and verified independently |

### Discovery Service Trust

The discovery service (DS) is a coordination layer, not a trust authority. It provides two layers of cryptographic verification:

**Layer 1: DS Envelope Signing** — The DS signs all query responses (`GET /v1/stream`, `/v1/content`, `/v1/relationships`, `/v1/sites/check`) with its Ed25519 private key. Clients verify the DS signature against the DS public key published at `https://ds.polis.pub/.well-known/polis`. This prevents response tampering by intermediaries (CDN compromise, DNS hijack) and detects a compromised DS instance serving altered data.

**Layer 2: Author Signature Passthrough** — Stream events carry the original author's signature from publish time. Clients verify each event's author signature against the author's public key at `https://<actor>/.well-known/polis`. This prevents a compromised DS from fabricating events.

| DS Responsibility | Security Implication | Mitigation |
|------------------|---------------------|------------|
| Storing follow announcements | DS could fabricate follows | Author signature verification on stream events |
| Relaying blessing requests | DS could fabricate requests | Author signature verification on stream events |
| Providing stream events | DS could inject events | DS envelope signature + author signature verification |
| Storing site registrations | Metadata exposure | DS envelope signature prevents falsifying registration status |
| Auto-blessing comments based on policy evaluation | DS could fabricate autobless decisions | DS attestation signature on autobless decisions; clients verify against DS public key |
| Signing query responses | DS key compromise allows forged envelopes | Key rotation recovery (re-fetch on verification failure) |

**Key principle**: Clients verify both the DS envelope signature (response authenticity) and the author signature on each event (content authenticity). The DS is a convenience layer; all security-critical verification happens through cryptographic signatures.

The DS also verifies author signatures on ingest (site registration, content announcements, stream publishes) as defense-in-depth against spam.

**Residual risk**: Even with both layers, a compromised DS can still suppress or delay events (censorship) and leak metadata about who follows whom. These are inherent to any centralized coordination service.

### following.json Trust

The `following.json` file lists domains the site owner follows:

```json
{
  "version": "polis-cli-go/0.57.0",
  "following": [
    {
      "domain": "alice.com",
      "added": "2026-01-15T12:00:00Z"
    }
  ]
}
```

This file is:
- **Published** at `content/pub.polis.core/follow/following.json` (publicly visible)
- **Authored by the site owner** (not signed individually, but part of the site's content)
- **Used by policies** for `from following` source matching

The following list is trusted as a statement of intent by the site owner. It does not imply that the followed domains are trustworthy, only that the site owner has chosen to follow them.

---

## 6. Policies

Polis provides a declarative rule system for controlling which content and events are accepted by a site. Policies are stored as JSONL files and evaluated client-side (CLI and webapp).

### File Locations

| File | Visibility | Priority | Purpose |
|------|-----------|----------|---------|
| `.polis/policies/rules.jsonl` | Private (not published) | Highest | Personal overrides, blocklists |
| `policies/rules.jsonl` | Public (published on site) | Lower | Site-wide defaults visible to visitors |

Private policies are loaded first and take precedence over public policies due to first-match-wins evaluation order.

### Format

Policy files start with a version/generator header line, followed by one JSON object per rule:

```json
{"version":1,"generator":"polis-cli-go/0.57.0"}
{"active": true, "policy": "allow pub.polis.comment from following"}
{"active": false, "policy": "deny all from all at spam.com"}
```

The header line is not a rule — it records the file format version and the CLI version that created it. Parsers skip any line containing a `"version"` key.

| Field | Type | Description |
|-------|------|-------------|
| `active` | boolean | Whether this rule is currently enabled |
| `policy` | string | The policy rule string (see Grammar below) |

Inactive rules (`"active": false`) are skipped during evaluation. This allows disabling rules without deleting them.

### Grammar

```
<action> <type-match> from <source-match> [at <domain>] [on <target>]
```

| Component | Values | Description |
|-----------|--------|-------------|
| `action` | `allow`, `deny`, `emit`, `omit` | What to do when the rule matches |
| `type-match` | `all`, `none`, dotted type prefix | Event type to match |
| `from` | literal keyword | Required separator |
| `source-match` | `all`, `none`, `following`, `followers`, `self`, `thread-blessed` | Who the event is from |
| `at <domain>` | optional | Restrict match to a specific actor domain |
| `on <target>` | optional | Restrict match to a specific target path |

**Verb semantics:**

| Verb pair | Category | Purpose | Example |
|-----------|----------|---------|---------|
| `allow` / `deny` | Access control | Gate **incoming** events — determine whether content from a given source is accepted or rejected | `allow pub.polis.comment from following` accepts comments from followed authors |
| `emit` / `omit` | Side-effect control | Trigger or suppress **outgoing** side-effects in response to events | `emit pub.polis.comment.blessing from following` auto-blesses comments from followed authors; `omit pub.polis.notification from self` suppresses self-notifications |

Access control verbs (`allow`/`deny`) answer "should this event be accepted?" Side-effect verbs (`emit`/`omit`) answer "should this event trigger an automatic action?" Both use the same grammar and evaluation model. See `docs/cli/user/policies.md` for full usage guide.

### Type Matching

| Pattern | Matches | Does Not Match |
|---------|---------|----------------|
| `all` | Everything | (nothing excluded) |
| `none` | Nothing | (everything excluded) |
| `pub.polis.comment` | `pub.polis.comment`, `pub.polis.comment.published`, `pub.polis.comment.blessing.requested` | `pub.polis.com` (no dot boundary) |
| `pub.polis.comment.published` | `pub.polis.comment.published` only | `pub.polis.comment` (shorter), `pub.polis.comment.blessing` (different suffix) |

Type matching uses **prefix matching with dot boundary**: `pub.polis.comment` matches `pub.polis.comment.published` because the event type starts with the rule type followed by a dot. However, `pub.polis.com` does NOT match `pub.polis.comment.published` because `com` is not followed by a dot at the right boundary.

### Source Matching

| Source | Matches |
|--------|---------|
| `all` | Any actor domain |
| `none` | No actor domain (effectively disables the rule) |
| `following` | Actor domain is in the site's following list |
| `followers` | Actor domain is in the site's followers list |
| `self` | Actor domain matches the site's own domain |
| `thread-blessed` | Actor has a prior granted blessing on the thread (DS-resolved) |

### Evaluation Order

1. **Private rules first**: Rules from `.polis/policies/rules.jsonl` are loaded before `policies/rules.jsonl`
2. **First match wins**: The first active rule that matches the event determines the outcome
3. **Default allow**: If no rule matches, the event is allowed (permissive by default)

This evaluation model is critical for the blessing workflow. `EvaluateExplicit` returns an `EvalResult` that distinguishes between all four decisions:

| Outcome | Decision | Matched | Meaning |
|---------|----------|---------|---------|
| Explicit allow | `Allow` | `true` | A rule explicitly permits this; auto-grant blessing |
| Explicit deny | `Deny` | `true` | A rule explicitly forbids this; auto-deny blessing |
| Explicit emit | `Emit` | `true` | A rule triggers an outgoing side-effect (e.g., auto-bless) |
| Explicit omit | `Omit` | `true` | A rule suppresses an outgoing side-effect (e.g., skip notification) |
| No match | `Allow` | `false` | No rule matched; requires manual review |

The `EvalResult` also includes `Rule` (the raw policy string that matched) and `RuleIdx` (index in the policy list, -1 if no match), useful for logging and debugging.

### Examples

**Follow-only comments** (only people you follow can comment):

```jsonl
{"active":true,"policy":"allow pub.polis.comment from following"}
{"active":true,"policy":"deny pub.polis.comment from all"}
```

Non-comment events (follows, etc.) fall through both rules and are allowed by default.

**Domain block** (block all events from a specific domain):

```jsonl
{"active":true,"policy":"deny all from all at evil.corp"}
```

**Auto-bless comments from followed authors**:

```jsonl
{"active":true,"policy":"emit pub.polis.comment.blessing from following"}
```

When a blessing request arrives from a followed domain, `EvaluateExplicit` returns `(Emit, true)`, triggering automatic granting.

**Post-specific deny** (disable comments on a specific post):

```jsonl
{"active":true,"policy":"deny pub.polis.comment from all on posts/2026/01/draft.md"}
```

**Private blocklist + public allowlist** (combined private and public policies):

Private `.polis/policies/rules.jsonl`:
```jsonl
{"active":true,"policy":"deny all from all at spam.com"}
{"active":true,"policy":"deny all from all at troll.net"}
```

Public `policies/rules.jsonl`:
```jsonl
{"active":true,"policy":"allow pub.polis.comment from following"}
{"active":true,"policy":"deny pub.polis.comment from all"}
```

Private blocklist rules fire first (blocking spam.com and troll.net for all event types), then public rules control comment access.

### Default Policies

New sites are initialized with two policy files containing 10 rules total.

**Public** (`policies/rules.jsonl`) — visible to visitors, controls side-effects:

```jsonl
{"version":1,"generator":"polis-cli-go/0.57.0"}
{"active":true,"policy":"emit pub.polis.comment.blessing from self"}
{"active":true,"policy":"emit pub.polis.comment.blessing from following"}
{"active":true,"policy":"emit pub.polis.comment.blessing from thread-blessed"}
```

These auto-bless comments from yourself, followed authors, and anyone already blessed in the thread.

**Private** (`.polis/policies/rules.jsonl`) — not published, controls access and notifications:

```jsonl
{"version":1,"generator":"polis-cli-go/0.57.0"}
{"active":true,"policy":"allow pub.polis.post from all"}
{"active":true,"policy":"allow pub.polis.comment from all"}
{"active":true,"policy":"allow pub.polis.follow from all"}
{"active":true,"policy":"allow pub.polis.site from all"}
{"active":true,"policy":"allow pub.polis.dm from following"}
{"active":true,"policy":"deny pub.polis.dm from all"}
{"active":true,"policy":"omit pub.polis.notification from self"}
```

The private defaults allow all standard content types from everyone, restrict DMs to followed domains (allow-then-deny ordering), and suppress self-notifications.

### DM Acceptance Policy

DM acceptance is controlled through the same policy rule system. The `pub.polis.dm` type prefix is used for DM-specific rules:

```jsonl
{"active":true,"policy":"allow pub.polis.dm from following"}
{"active":true,"policy":"deny pub.polis.dm from all"}
```

These DM rules are part of the default private policies created during `polis init`. They restrict incoming DMs to followed domains by default.

| Goal | Policy Rules |
|------|-------------|
| DMs from followed only (default) | `allow pub.polis.dm from following` + `deny pub.polis.dm from all` |
| Accept from anyone | `allow pub.polis.dm from all` |
| Block specific domain | `deny pub.polis.dm from all at spam.example.com` (before allow rules) |
| Whitelist specific domain | `allow pub.polis.dm from all at trusted-friend.com` (before deny rules) |
| Disable DMs entirely | `deny pub.polis.dm from all` |

Policy evaluation happens before any cryptographic work in the DM receive pipeline, making it the cheapest rejection path.

### Integration Points

Policies are evaluated at three main integration points:

| Integration Point | Event Types | Effect |
|-------------------|-------------|--------|
| Content registration | `pub.polis.comment.published`, `pub.polis.follow.announced` | Block or allow incoming content |
| Notification filtering | All notification event types | Suppress notifications from denied sources |
| Blessing workflow | `pub.polis.comment.blessing.requested` | Auto-grant, auto-deny, or require manual review |

### Go Interface

```go
// Policy is a single line from a rules.jsonl file.
type Policy struct {
    Active bool   `json:"active"`
    Rule   string `json:"policy"`
}

// ParsedRule is the structured representation of a policy rule string.
type ParsedRule struct {
    Action string // "allow", "deny", "emit", or "omit"
    Type   string // event type prefix, "all", or "none"
    Source string // "all", "none", "following", "followers", "self", "thread-blessed"
    Domain string // optional: actor domain filter (from "at <domain>")
    Target string // optional: target path filter (from "on <target>")
}

// EvalContext provides runtime context for source matching.
type EvalContext struct {
    MyDomain         string          // the local site's domain
    FollowingDomains map[string]bool
    FollowerDomains  map[string]bool
}

// Event describes an incoming event to evaluate against policies.
type Event struct {
    Type         string
    ActorDomain  string
    TargetDomain string
    TargetPath   string
}

// EvalResult is the structured outcome of policy evaluation.
type EvalResult struct {
    Decision Decision // Allow, Deny, Emit, or Omit
    Matched  bool     // true if an explicit rule matched
    Rule     string   // the raw policy string that matched, empty if no match
    RuleIdx  int      // index in the policy list (-1 if no match)
}

// Evaluate returns the decision for an event against a set of policies.
// First active matching rule wins. No match returns Allow (default permissive).
func Evaluate(policies []Policy, evt Event, ctx EvalContext) Decision

// EvaluateExplicit returns the decision and whether an explicit rule matched.
// Returns (Allow, false) when no rule matched (default permissive).
func EvaluateExplicit(policies []Policy, evt Event, ctx EvalContext) (Decision, bool)

// EvaluateWithLog returns the full EvalResult including matched rule details.
func EvaluateWithLog(policies []Policy, evt Event, ctx EvalContext) EvalResult
```

---

## 7. Attack Vectors and Mitigations

### 7.1 Content Tampering (Man-in-the-Middle)

| Aspect | Detail |
|--------|--------|
| **Attack** | Attacker modifies content in transit between author's server and reader |
| **Mitigation** | Ed25519 signatures over full content; SHA-256 content hash in frontmatter |
| **Residual risk** | Requires HTTPS to prevent downgrade; signature stripped = unsigned, not forged |

### 7.2 Key Compromise

| Aspect | Detail |
|--------|--------|
| **Attack** | Attacker obtains author's private key from `.polis/keys/` |
| **Mitigation** | File permissions (0600), `.gitignore` exclusion, no network transmission |
| **Residual risk** | No key revocation mechanism yet; compromised key allows forging until detected |

### 7.3 Domain Takeover

| Aspect | Detail |
|--------|--------|
| **Attack** | Attacker gains control of author's domain and publishes new `.well-known/polis` with their key |
| **Mitigation** | Previously signed content becomes unverifiable (mismatch with new key) |
| **Residual risk** | Attacker can publish new content as the domain owner; no key pinning |

### 7.4 Discovery Service Compromise

| Aspect | Detail |
|--------|--------|
| **Attack** | Attacker compromises the DS and injects false follow/content/blessing events |
| **Mitigation** | Two-layer verification: (1) DS envelope signature on all query responses verified against DS public key at `/.well-known/polis`, (2) author signature on each stream event verified against author's public key. Clients reject responses with invalid DS signatures and skip events with invalid author signatures. Key rotation recovery: on DS verification failure, client re-fetches the DS key and retries once. |
| **Detection** | Verification failures logged with actor domain, event type, and failure reason. Per-domain failure counters persisted across sync cycles. 3+ consecutive DS envelope failures suspend sync with warning. 5+ author failures from same domain in 24h trigger blocking recommendation. |
| **Residual risk** | DS can suppress events (censorship/denial of service) or leak metadata (who follows whom). A compromised DS serving unsigned events from the pre-signing era could bypass author verification if `require_author_signatures` is false (transitional). For auto-blessed comments, the DS attestation signature creates an auditable trail tied to the DS key, but a compromised DS could still evaluate policies dishonestly — the attestation proves *which* DS signed, not that the evaluation was correct. |

### 7.5 Replay Attacks

| Aspect | Detail |
|--------|--------|
| **Attack** | Attacker re-submits a previously valid signed message |
| **Mitigation** | Content hashes include timestamps; DS deduplicates by content hash |
| **Residual risk** | No sequence numbers or nonces; same content at same timestamp would have same hash |

### 7.6 Impersonation (Author Spoofing)

| Aspect | Detail |
|--------|--------|
| **Attack** | Attacker creates a site claiming to be a different author |
| **Mitigation** | Signature verification requires fetching public key from the claimed domain |
| **Residual risk** | Display names are not verified; similar domain names could mislead users |

### 7.7 Comment Spam

| Aspect | Detail |
|--------|--------|
| **Attack** | Attacker floods a site with unwanted comments via blessing requests |
| **Mitigation** | Blessing workflow requires site owner approval; policy rules can auto-deny |
| **Residual risk** | Large volume of blessing requests could overwhelm the review queue |

### 7.8 Following Spam

| Aspect | Detail |
|--------|--------|
| **Attack** | Attacker creates many sites that all follow a target to inflate follower counts |
| **Mitigation** | Follower counts are informational only; no algorithmic amplification |
| **Residual risk** | Follower list metadata is public; no rate limiting on follow announcements |

### 7.9 Content Hash Collision

| Aspect | Detail |
|--------|--------|
| **Attack** | Attacker creates different content with the same SHA-256 hash |
| **Mitigation** | SHA-256 is collision-resistant; no known practical collision attacks |
| **Residual risk** | Theoretical concern only; SHA-256 provides 128-bit collision resistance |

### 7.10 Signature Stripping

| Aspect | Detail |
|--------|--------|
| **Attack** | Attacker removes the signature from content and re-publishes |
| **Mitigation** | Verifiers check for signature presence; unsigned content is flagged |
| **Residual risk** | Content without signature is still readable; users must check verification status |

### 7.11 Denial of Service via Content Size

| Aspect | Detail |
|--------|--------|
| **Attack** | Attacker publishes extremely large content to consume resources during verification |
| **Mitigation** | Webapp enforces body size limits for API requests; CLI processes local files |
| **Residual risk** | Remote content fetch has no explicit size limit in the verification flow |

### 7.12 Private Key Backup Failure

| Aspect | Detail |
|--------|--------|
| **Attack** | User loses private key due to disk failure, no backup |
| **Mitigation** | None (user responsibility) |
| **Residual risk** | All previous content remains verifiable, but user cannot sign new content or prove authorship for updates |

### 7.13 Timing Attacks on Key Validation

| Aspect | Detail |
|--------|--------|
| **Attack** | Attacker measures response time to brute-force API keys |
| **Mitigation** | API key validation uses `crypto/subtle.ConstantTimeCompare` for hash comparison |
| **Residual risk** | Standard constant-time comparison; no additional rate limiting on API auth endpoints |

### 7.14 DM Spam / Flooding

| Aspect | Detail |
|--------|--------|
| **Attack** | Attacker sends high volume of DMs to a target instance |
| **Mitigation** | 7-layer defense: policy check → timestamp → global rate limit → signature verify → per-sender rate limit → size check → process |
| **Residual risk** | Attacker controlling many verified domains could distribute load across senders; global rate limit is the backstop |

### 7.15 DM Content Interception (Transport)

| Aspect | Detail |
|--------|--------|
| **Attack** | Network attacker intercepts DM in transit between instances |
| **Mitigation** | NaCl box encryption (X25519 + XSalsa20-Poly1305) provides confidentiality + integrity; HTTPS provides transport layer security |
| **Residual risk** | Without forward secrecy, compromised long-term key exposes all past/future DMs with that peer |

### 7.16 DM Storage Compromise (At Rest)

| Aspect | Detail |
|--------|--------|
| **Attack** | Attacker gains filesystem access to `.polis/content/pub.polis.core/dm/` |
| **Mitigation** | Message content encrypted with secretbox using HKDF-derived key; file permissions 0600; metadata (from, to, timestamp) remains cleartext |
| **Residual risk** | Attacker with private key access can derive storage key and decrypt all stored DMs. Metadata (who you message, when, how often) is visible without decryption. |

### 7.17 DM Sender Impersonation

| Aspect | Detail |
|--------|--------|
| **Attack** | Attacker forges DM delivery pretending to be another domain |
| **Mitigation** | Signed request headers verified against sender's `.well-known/polis` public key; sender public key inside encrypted payload provides second verification layer |
| **Residual risk** | Domain takeover allows impersonation (same as 7.3); no key pinning for DM peers |

### 7.18 DM Key Rotation Disruption

| Aspect | Detail |
|--------|--------|
| **Attack** | Sender rotates key; previously sent DMs become unverifiable if receiver cached old key |
| **Mitigation** | Transport encryption is per-delivery (receiver decrypts immediately); storage uses local key derived from receiver's own private key, unaffected by sender rotation |
| **Residual risk** | DMs in flight during sender key rotation may fail signature verification; sender must retry after rotation completes |

### 7.19 DM Metadata Exposure

| Aspect | Detail |
|--------|--------|
| **Attack** | Observer analyzes cleartext metadata to learn communication patterns |
| **Mitigation** | Metadata is local to each instance (not centralized); no intermediary server sees both sides |
| **Residual risk** | Filesystem access reveals: who you message, when, frequency, message sizes. Conversation IDs are deterministic (derivable from both domains). |

### 7.20 Malicious DM Content

| Aspect | Detail |
|--------|--------|
| **Attack** | Sender delivers encrypted message containing malicious content (exploit payloads, phishing links, HTML injection) |
| **Mitigation** | Post-decryption validation: strict UTF-8, HTML stripping, schema validation, field whitelist. Display-time: content always escaped, links not auto-linkified |
| **Residual risk** | Server operator stores encrypted blobs they cannot inspect (inherent to E2E encryption). Social engineering content that passes validation cannot be prevented by technical controls. |

---

## 8. Feature Security Analysis

### 8.1 Posts

| Aspect | Security Property |
|--------|-------------------|
| **Authorship** | Ed25519 signature by site owner |
| **Integrity** | SHA-256 hash in `current-version` frontmatter |
| **Confidentiality** | None; posts are public by design |
| **Versioning** | Hash changes with content; signature covers hash |
| **Canonicalization** | Content canonicalized before hashing (strip leading empty lines, trim trailing whitespace, single trailing newline) |

### 8.2 Comments

| Aspect | Security Property |
|--------|-------------------|
| **Authorship** | Signed by commenter, not by site owner |
| **Integrity** | SHA-256 hash + signature verification |
| **Authorization** | Blessing workflow; site owner must approve (grant) comments |
| **Policy control** | Policies can auto-grant, auto-deny, or require manual review |
| **In-reply-to binding** | Comment references specific post URL and version hash |

### 8.3 Blessings

| Aspect | Security Property |
|--------|-------------------|
| **Request** | Commenter signs blessing request with their key |
| **Grant** | Site owner approves; blessed comment published on their site |
| **Deny** | Site owner rejects; comment not published |
| **Beseech** | Commenter submits comment to DS for site owner review |
| **Policy integration** | `EvaluateExplicit` determines auto-grant/auto-deny/manual |
| **Trust boundary** | Site owner has full control over what appears on their site |
| **Auto-bless attestation** | DS signs autobless decisions with its own Ed25519 key; clients verify the attestation to confirm the autobless was a legitimate policy evaluation, not a fabrication |

### 8.4 Following

| Aspect | Security Property |
|--------|-------------------|
| **Privacy** | Following list is public (`following.json` published on site) |
| **Announcement** | Follow actions announced to DS |
| **Unfollowing** | Removing from `following.json` and announcing to DS |
| **No mutual consent** | Following is unilateral; no approval required from the followed party |

### 8.5 Nested Comments

| Aspect | Security Property |
|--------|-------------------|
| **Reply chain** | Each comment's `in-reply-to` references the parent's URL and version |
| **Independent signing** | Each reply is independently signed by its author |
| **Site owner control** | Entire thread is subject to blessing on the site owner's domain |
| **Cross-site threads** | Thread may span multiple domains; each segment verified independently |

### 8.6 Webapp Security

| Aspect | Security Property |
|--------|-------------------|
| **Local binding** | Webapp binds to `127.0.0.1` by default (localhost only) |
| **Authentication** | No auth for local webapp (trusts local user); API keys for v1 REST API |
| **API key storage** | Hashed with SHA-256 in `.polis/api-keys.json` (file mode 0600) |
| **API key format** | `polis_` prefix + 64 hex chars (32 random bytes) |
| **CORS** | Configurable in API middleware |
| **Body limits** | API enforces request body size limits |
| **Static assets** | Embedded in binary via Go `embed` (no external file serving) |
| **SPA routing** | `/_/` deep-linking prefix for client-side routes |

### 8.7 Notifications

| Aspect | Security Property |
|--------|-------------------|
| **Source** | Delivered via DS stream; client-side policy filtering |
| **Storage** | Local JSONL files in `.polis/ds/<domain>/pub.polis.core/state/` |
| **Filtering** | Policy rules can suppress notifications from denied sources |
| **No execution** | Notifications are data-only; no code execution or link auto-following |

### 8.8 Direct Messages

| Aspect | Security Property |
|--------|-------------------|
| **Confidentiality (transport)** | NaCl box: X25519 DH + XSalsa20-Poly1305 authenticated encryption |
| **Confidentiality (storage)** | NaCl secretbox: HKDF-derived symmetric key + XSalsa20-Poly1305 |
| **Sender authentication** | Ed25519 signed request headers + sender public key inside encrypted payload |
| **Integrity** | Poly1305 MAC on both transport and storage encryption |
| **Access control** | Configurable acceptance policy via `rules.jsonl` (followed/anyone/blocked) |
| **Rate limiting** | Per-sender and global rate limits, checked before expensive crypto operations |
| **Metadata protection** | No intermediary; metadata local to each instance. Cleartext metadata: sender, recipient, timestamp, size |
| **Forward secrecy** | None in v1 (acknowledged limitation) |
| **Key rotation** | Storage survives peer key rotation; local key rotation requires re-encryption migration |
| **Spam resistance** | 7-layer defense: policy → timestamp → global rate → signature verify → per-sender rate → size → process |

### 8.9 Hooks

| Aspect | Security Property |
|--------|-------------------|
| **Availability** | Self-hosting only. Disabled on polis.pub (`EnableHooks = false`; all hook endpoints return 403) |
| **Execution model** | Shell scripts invoked via `exec.CommandContext` with 30-second timeout |
| **Path containment** | Hook scripts must resolve within the site directory. Symlinks within the site directory are followed, allowing controlled escape for system-installed scripts |
| **Environment** | Inherits parent process environment plus `POLIS_*` variables (event, path, title, version, timestamp, site dir, config dir, commit message) |
| **Input** | JSON payload on stdin containing event metadata |
| **Output** | Combined stdout/stderr captured, capped at 32KB (`MaxHookBodySize`) |
| **Discovery** | Explicit config (`.polis/webapp/config.json` hooks section) or convention (`.polis/webapp/hooks/{event}.sh`) |
| **No sandboxing** | Scripts run with the same user/permissions as the polis process. No seccomp, no chroot, no capabilities restriction |
| **No signature verification** | Hook scripts are not signed or verified. Any executable file at the configured/conventional path will be run |
| **Trigger events** | `post-publish`, `post-republish`, `post-comment` (blessed) |

### 8.10 JUDGE (Automated Cross-Boundary Verification)

| Aspect | Security Property |
|--------|-------------------|
| **Schedule** | Hourly background job on hosted service (`judgeTick = 1 * time.Hour`) |
| **HTTP content verification** | Fetches posts via loopback HTTP and verifies Ed25519 signatures + SHA-256 content hashes, testing the full serving pipeline end-to-end |
| **DS attestation audit** | Verifies structural integrity of blessed comments index; full DS attestation signature verification against DS public key planned for future iteration |
| **Key continuity monitoring** | Tracks each tenant's public key fingerprint over time; alerts on unexpected key changes (proto-TOFU). Baselines stored at `/data/judge/{handle}/key_baseline.json` |
| **Policy snapshot verification** | Snapshots policy file hashes; detects retroactive policy changes after blessings were granted. Snapshots stored at `/data/judge/{handle}/policy_snapshot.json` |
| **Index consistency** | Verifies `index.jsonl` entries match real signed files on disk; detects orphaned entries (indexed but missing), phantom files (on disk but not indexed), and hash mismatches |
| **Cross-site comment verification** | Verifies blessed comment signatures against the claimed author's current public key (fetched from the author's `.well-known/polis` via HTTPS) |
| **State persistence** | Judge state survives restarts via `/data/judge/` directory on the Fly.io persistent volume |
| **Observability** | All findings emitted as structured JSON events (`judge.sweep`, `judge.fail.*`, `judge.alert.*`, `judge.info.*`) to Axiom for monitoring and alerting |
| **Graceful degradation** | External HTTP failures (item 6) are skipped gracefully with 5-second timeouts; unreachable domains are cached to avoid retry storms |

---

## 9. Known Limitations

| Limitation | Impact | Potential Mitigation |
|------------|--------|---------------------|
| **No key revocation** | Compromised key remains valid until domain key is changed | Key revocation list or certificate transparency log |
| **No key pinning** | Client fetches key on every verification; domain takeover = key replacement | TOFU (Trust On First Use) key pinning. JUDGE's key continuity monitoring (Section 8.10) provides partial mitigation by alerting on unexpected key changes for hosted tenants |
| **No forward secrecy** | Key compromise exposes all past signatures (though content is public anyway) | N/A for public content system |
| **Unencrypted private keys** | Keys stored without password protection | Optional passphrase encryption |
| **No trusted timestamps** | Self-reported timestamps could be backdated or future-dated | Timestamping authority or blockchain anchoring |
| **Public following lists** | No way to follow someone privately | Private following list option |
| **No rate limiting** | DS has limited rate limiting on incoming events | Per-domain rate limits at DS level |
| **Single key per site** | No key delegation or sub-keys for different operations | Hierarchical key model |
| **No content encryption for public types** | Public content (posts, comments) is unencrypted by design. Direct messages are end-to-end encrypted (NaCl box) and encrypted at rest (NaCl secretbox). | Per-reader encryption for public content |
| **No multi-signature** | No support for content requiring multiple signers | Multi-sig threshold signatures |
| **No DM forward secrecy** | Compromised private key exposes all DM history with all peers. The Double Ratchet (Signal protocol) would address this but requires synchronized state incompatible with async delivery. | Double Ratchet protocol |
| **DM metadata cleartext** | Sender/recipient domains, timestamps, and message sizes stored in cleartext for indexing. | Encrypt entire conversation files |
| **Autobless trusts DS policy evaluation** | DS attestation proves which DS signed the autobless decision, but a compromised DS could evaluate policies dishonestly | Policy transparency logs; multi-DS cross-verification. JUDGE's policy snapshot verification (Section 8.10) detects retroactive policy changes after blessings are granted |

---

## 10. Future Considerations

### Key Rotation Protocol

A formal key rotation protocol would allow:
1. Generate new keypair
2. Sign rotation announcement with old key (linking old key to new key)
3. Publish new public key at `.well-known/polis`
4. Re-sign existing content with new key (optional)
5. Old key can verify rotation announcement; new key used for all future content

### Key Revocation

A revocation mechanism could use:
- Revocation certificates (signed by the key being revoked)
- DS-mediated revocation announcements
- Time-bounded key validity (expiration dates)

### DS Signature Verification ✅ (Implemented)

The discovery service now signs all query responses with its Ed25519 private key, and clients verify both the DS envelope signature and author signatures on stream events. See Section 5 (Discovery Service Trust) and Section 7.4 for details.

Remaining future work:
- **Certificate transparency for DS keys**: publish DS key rotations to a verifiable log
- **Event-level DS co-signatures**: Partially implemented — the DS now signs autobless decisions with attestation signatures on blessing relationships and stream events. Remaining: co-signatures on all individual stream events for offline verification of event batches
- **Full DS attestation audit**: JUDGE currently verifies blessed.json structural integrity. Future iteration will query DS relationship records and verify each attestation signature against the DS public key via `discovery.VerifyAutoblessAttestation()`

### TOFU Key Pinning

Trust On First Use would allow clients to:
1. Record the public key on first encounter
2. Alert if the key changes unexpectedly
3. Require explicit user approval for key changes

**Partial implementation**: JUDGE's key continuity monitoring (Section 8.10) implements steps 1-2 for hosted tenants. It records a key baseline on first encounter and alerts via `judge.alert.key_change` events when the key changes. Full TOFU would extend this to client-side verification for all domains.

### Content Encryption

For private content, polis could add:
- Per-reader content encryption using recipient's public key
- Group key management for audience-restricted posts
- Ephemeral keys for forward secrecy

### Hardware Key Support

Integration with hardware security modules (HSMs) or hardware keys:
- FIDO2/WebAuthn for webapp authentication
- Hardware-backed Ed25519 signing (e.g., YubiKey)
- Air-gapped signing workflows

---

## 11. Document History

| Date | Change |
|------|--------|
| 2026-03-27 | Added JUDGE automated verification system (Section 8.10): hourly cross-boundary verification covering HTTP content verification, DS attestation audit, key continuity monitoring, policy snapshots, index consistency, and cross-site comment verification. Updated Sections 9 (Known Limitations) and 10 (Future Considerations) to reference JUDGE capabilities |
| 2026-03-23 | DS attestation signatures for autoblessed comments: DS signs autobless policy evaluation decisions, stored in blessing relationship signature field and stream event payload. Updated Sections 5, 7.4, 8.3, 9, 10 |
| 2026-03-08 | Accuracy fixes: added `author_name` and `avatar` fields to well-known/polis schema; removed legacy manifest.json migration tables; replaced incorrect single default policy with actual 10-rule defaults across public/private files; added version header documentation for rules.jsonl; updated EvaluateExplicit outcome table with all four decisions (allow/deny/emit/omit) and EvalResult struct; fixed auto-bless example to use `emit` verb; expanded verb semantics documentation; added `version-history` to post frontmatter and `author` to comment frontmatter examples; simplified DM policy provenance text; added EvaluateWithLog to Go interface |
| 2026-03-07 | DS signature verification: DS envelope signing on all query responses, client-side DS + author signature verification, verification failure tracking and anomaly detection, updated Section 5 (Discovery Service Trust), Section 7.4 (DS Compromise), Section 10 (Future Considerations) |
| 2026-03-07 | Added DM security model: Ed25519→X25519 key conversion, NaCl box/secretbox encryption, instance-to-instance signed request auth, DM acceptance policies, attack vectors 7.14-7.20, feature analysis 8.8, updated known limitations |
| 2026-03-03 | Removed deprecated domain migration section (feature removed); fixed comment `in-reply-to` structure (`root-post` not `version`); renumbered Feature Security Analysis subsections |
| 2026-03-01 | Merged security model, .well-known/polis spec, and policies spec into unified document |

---

## 12. Feedback Welcome

If you find a security issue in polis, please report it responsibly. See the [security policy](security.md) for reporting instructions. Do not report security vulnerabilities through public GitHub issues.

For questions about the security model, architecture discussions, or suggestions for improvements, open a discussion or reach out to the maintainers.
