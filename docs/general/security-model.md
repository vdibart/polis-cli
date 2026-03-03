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
| `active_theme` | string | omitted | Currently active theme name (e.g., `sols`, `turbo`, `zane`) |
| `bundles` | object | omitted | Bundle registry mapping bundle IDs to their configuration |

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

### Removed v1 Fields

The following fields from the v1 format are no longer used:

| Removed Field | Reason | Replacement |
|---------------|--------|-------------|
| `manifest_url` | Replaced by bundle system | `bundles` field in `.well-known/polis` |
| `posts_url` | Hardcoded content paths | Bundle `path` + content type directory |
| `comments_url` | Hardcoded content paths | Bundle `path` + content type directory |
| `following_url` | Hardcoded content paths | Bundle `path` + content type directory |
| `manifest_version` | Redundant with `version` | `version` field |

### Absorbed Fields from manifest.json

The v1 `manifest.json` file has been eliminated. Its fields are now covered by `.well-known/polis`:

| Former manifest.json field | Now in .well-known/polis |
|---------------------------|------------------------|
| `public_key` | `public_key` |
| `author` | `author` |
| `email` | `email` |
| `site_title` | `site_title` |
| `active_theme` | `active_theme` |
| `created` | `created` |

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
    Created     string                 `json:"created"`
    ActiveTheme string                 `json:"active_theme,omitempty"`
    Bundles     map[string]BundleEntry `json:"bundles,omitempty"`
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
published: 2026-01-15T14:00:00Z
in-reply-to:
  url: https://author.com/content/pub.polis.core/post/original-post.md
  version: sha256:def456...
current-version: sha256:789abc...
generator: polis-cli-go/0.57.0
signature: <SSH signature block>
---

Comment body content here...
```

#### Blessings

Blessing requests are signed by the commenter and include the comment content. The site owner's blessing (approval) or denial is a separate signed action communicated through the discovery service.

#### Domain Migrations

When an author migrates to a new domain, the migration announcement is signed with the original key, creating a chain of trust from the old domain to the new one:

```yaml
---
type: migration
from: olddomain.com
to: newdomain.com
migrated: 2026-03-01T00:00:00Z
signature: <signed with old domain's key>
---
```

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

The discovery service (DS) is a coordination layer, not a trust authority:

| DS Responsibility | Security Implication |
|------------------|---------------------|
| Storing follow announcements | DS could fabricate follows (mitigated by client-side verification) |
| Relaying blessing requests | DS could drop or fabricate requests (mitigated by signed content) |
| Providing stream events | DS could inject events (mitigated by client-side filtering) |
| Storing site registrations | DS knows which domains are using polis (metadata exposure) |

**Key principle**: Clients NEVER trust DS data for authentication. The DS is a convenience layer; all security-critical verification happens by fetching content directly from the author's domain and checking signatures.

The DS does verify signatures on certain operations (e.g., site registration, content announcements) to prevent spam, but this is defense-in-depth, not the primary trust model.

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

Each line in a `rules.jsonl` file is a JSON object with two fields:

```json
{"active": true, "policy": "allow pub.polis.comment from following"}
{"active": false, "policy": "deny all from all at spam.com"}
```

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
| `action` | `allow`, `deny` | What to do when the rule matches |
| `type-match` | `all`, `none`, dotted type prefix | Event type to match |
| `from` | literal keyword | Required separator |
| `source-match` | `all`, `none`, `following`, `followers` | Who the event is from |
| `at <domain>` | optional | Restrict match to a specific actor domain |
| `on <target>` | optional | Restrict match to a specific target path |

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

### Evaluation Order

1. **Private rules first**: Rules from `.polis/policies/rules.jsonl` are loaded before `policies/rules.jsonl`
2. **First match wins**: The first active rule that matches the event determines the outcome
3. **Default allow**: If no rule matches, the event is allowed (permissive by default)

This evaluation model is critical for the blessing workflow. `EvaluateExplicit` distinguishes between:

| Outcome | Decision | Explicit | Meaning |
|---------|----------|----------|---------|
| Explicit allow | `Allow` | `true` | A rule explicitly permits this; auto-grant blessing |
| Explicit deny | `Deny` | `true` | A rule explicitly forbids this; auto-deny blessing |
| No match | `Allow` | `false` | No rule matched; requires manual review |

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
{"active":true,"policy":"allow pub.polis.comment.blessing from following"}
```

When a blessing request arrives from a followed domain, `EvaluateExplicit` returns `(Allow, true)`, triggering automatic granting.

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

### Default Policy

New sites are initialized with a single default policy rule:

```jsonl
{"active":true,"policy":"allow pub.polis.comment.blessing from following"}
```

This default auto-grants blessing requests from followed authors while leaving all other events at the permissive default.

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
    Action string // "allow" or "deny"
    Type   string // event type prefix, "all", or "none"
    Source string // "all", "none", "following", "followers"
    Domain string // optional: actor domain filter (from "at <domain>")
    Target string // optional: target path filter (from "on <target>")
}

// EvalContext provides runtime context for source matching.
type EvalContext struct {
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

// Evaluate returns the decision for an event against a set of policies.
// First active matching rule wins. No match returns Allow (default permissive).
func Evaluate(policies []Policy, evt Event, ctx EvalContext) Decision

// EvaluateExplicit returns the decision and whether an explicit rule matched.
// Returns (Allow, false) when no rule matched (default permissive).
func EvaluateExplicit(policies []Policy, evt Event, ctx EvalContext) (Decision, bool)
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
| **Mitigation** | Clients verify all content by fetching from origin domains and checking signatures |
| **Residual risk** | DS can suppress events (denial of service) or leak metadata (who follows whom) |

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

### 8.4 Domain Migration

| Aspect | Security Property |
|--------|-------------------|
| **Announcement** | Migration signed with the old domain's key |
| **Chain of trust** | Old key vouches for the new domain |
| **Limitation** | No way to revoke a fraudulent migration announcement |
| **DS coordination** | DS can relay migration announcements to followers |

### 8.5 Following

| Aspect | Security Property |
|--------|-------------------|
| **Privacy** | Following list is public (`following.json` published on site) |
| **Announcement** | Follow actions announced to DS |
| **Unfollowing** | Removing from `following.json` and announcing to DS |
| **No mutual consent** | Following is unilateral; no approval required from the followed party |

### 8.6 Nested Comments

| Aspect | Security Property |
|--------|-------------------|
| **Reply chain** | Each comment's `in-reply-to` references the parent's URL and version |
| **Independent signing** | Each reply is independently signed by its author |
| **Site owner control** | Entire thread is subject to blessing on the site owner's domain |
| **Cross-site threads** | Thread may span multiple domains; each segment verified independently |

### 8.7 Webapp Security

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

### 8.8 Notifications

| Aspect | Security Property |
|--------|-------------------|
| **Source** | Delivered via DS stream; client-side policy filtering |
| **Storage** | Local JSONL files in `.polis/ds/<domain>/pub.polis.core/state/` |
| **Filtering** | Policy rules can suppress notifications from denied sources |
| **No execution** | Notifications are data-only; no code execution or link auto-following |

---

## 9. Known Limitations

| Limitation | Impact | Potential Mitigation |
|------------|--------|---------------------|
| **No key revocation** | Compromised key remains valid until domain key is changed | Key revocation list or certificate transparency log |
| **No key pinning** | Client fetches key on every verification; domain takeover = key replacement | TOFU (Trust On First Use) key pinning |
| **No forward secrecy** | Key compromise exposes all past signatures (though content is public anyway) | N/A for public content system |
| **Unencrypted private keys** | Keys stored without password protection | Optional passphrase encryption |
| **No trusted timestamps** | Self-reported timestamps could be backdated or future-dated | Timestamping authority or blockchain anchoring |
| **Public following lists** | No way to follow someone privately | Private following list option |
| **No rate limiting** | DS has limited rate limiting on incoming events | Per-domain rate limits at DS level |
| **Single key per site** | No key delegation or sub-keys for different operations | Hierarchical key model |
| **No content encryption** | All content is public; no private posts or DMs | Content encryption layer |
| **No multi-signature** | No support for content requiring multiple signers | Multi-sig threshold signatures |

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

### TOFU Key Pinning

Trust On First Use would allow clients to:
1. Record the public key on first encounter
2. Alert if the key changes unexpectedly
3. Require explicit user approval for key changes

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
| 2026-03-01 | Merged security model, .well-known/polis spec, and policies spec into unified document |

---

## 12. Feedback Welcome

If you find a security issue in polis, please report it responsibly. See the [security policy](security.md) for reporting instructions. Do not report security vulnerabilities through public GitHub issues.

For questions about the security model, architecture discussions, or suggestions for improvements, open a discussion or reach out to the maintainers.
