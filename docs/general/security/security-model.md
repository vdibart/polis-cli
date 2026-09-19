# Polis Security Model

*For* [Reviewers](../../README.md#reviewing-the-security-and-identity-design) — *Kind* [Concept](../../README.md#kinds-of-page) — *See also* [spec](../../signet/spec/signing-base.md) · [spec](../../signet/spec/key-history.md) · [recipe](../../signet/recipes/05-verify-a-site.md)

Polis takes a fundamentally different approach to security than traditional social platforms. Rather than relying on a central authority to authenticate users and protect content, polis uses cryptographic signatures to bind content to a site and to prove it has not been altered. Every post, comment, and interaction is signed with the site's Ed25519 private key, and anyone can verify it by checking the signature against the public key the site publishes.

**A valid signature proves that the site's key signed. It cannot show who was holding the key.** The protocol does not specify who holds a site's private key, and nothing in a signature records it. Where the author is the key's only holder, a signature is the author's. Where anyone else also holds the key — a hosting service, a shared machine, a team, a backup, a thief — that party's signatures are valid and indistinguishable from the author's own. See [Key Custody](#key-custody) and [What a Signature Does Not Tell You](#what-a-signature-does-not-tell-you).

This document describes the polis protocol and its reference implementation — the cryptographic foundations, identity model, signature scheme, trust model, access control policies, known attack vectors, and feature-level security analysis. It does not describe how any particular deployment holds keys. A service that hosts polis sites for other people holds their keys under its own arrangements, and disclosing those arrangements is that service's responsibility.

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

API keys for the v1 REST API are stored only as SHA-256 hashes, in `.polis/api-keys.json`; the server hashes the presented Bearer token and compares it against the stored hashes. polis has no key generator: the key holder creates the token and records its hash by hand (see the [API reference](../../api/developer/reference.md#authentication)), so the plaintext is never written by polis. Validation uses constant-time comparison (`crypto/subtle.ConstantTimeCompare`) to prevent timing attacks.

### Direct Message Encryption

Direct messages are end-to-end encrypted to a per-epoch messages key that is separate from the Ed25519 identity key, with
message keys wrapped under a key derived from a password the server never receives. What that protects and what it does not
(the bootstrap window, passwords and recovery, what an operator still sees, and the limits of cryptography delivered by a
browser) is in [Direct-message encryption](dm-encryption.md); the byte formats are in the [at-rest format](../../../cli-go/pkg/dm/FORMAT.md)
and the [delivery protocol](../../../cli-go/pkg/dm/PROTOCOL.md).

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

### Key Custody

A site has one private key, and **whoever can read it can sign as the site.** The protocol says nothing about who that is. The layout above is where the reference CLIs put the key when they generate it; it is not a property the key keeps.

A key has more than one holder whenever a copy exists outside the author's sole control:

- a service that generated the key for the author, or stores it;
- a shared or managed machine, and whoever administers it;
- a backup, snapshot, or sync service that copies `.polis/`;
- people the key was given to — a team, a collaborator;
- anyone who has stolen a copy (§7.2).

Every holder signs with the same key, so every holder produces the same signatures. **Nothing a verifier can check tells them apart** — not the signature, not `.well-known/polis`, not the key history. Software that signs with a site's key while no person is present, such as a scheduled job or a maintenance process, is a holder in exactly the same sense.

The only arrangement in which the key holder and the author are known to coincide is one where the author generated the key and has never let a copy out. The author can know that. Nobody else can verify it.

**When someone else has held the key.** Rotating (`polis rotate-key`) moves future signing to a new key. It does not strip the other copy of all its power: a retired key still verifies content whose self-reported `published:` time falls inside that key's validity window, so whoever holds the old key can still sign content that *claims* to predate the rotation (see the resolution rule in [key history](../../signet/spec/key-history.md)). A discovery-service witness can contradict such a claim for content that was registered with that service; nothing requires one. And whoever rotates first controls the key the discovery service treats as current (§7.2).

**Holding the only copy has a cost.** The author becomes the only party who can back the key up (§7.12), and a key lost with no copy cannot be recovered by anyone.

### Key Distribution

The public key is distributed through the `.well-known/polis` identity document (see Section 3). Anyone who can fetch `https://example.com/.well-known/polis` can obtain the public key needed to verify signatures.

### Domain Binding

Keys are bound to domains through the `.well-known/polis` file:

1. Author publishes their public key at `https://domain.com/.well-known/polis`
2. Verifier fetches the public key from the author's domain
3. Verifier checks the signature using the fetched key
4. Trust is established through DNS ownership (controlling a domain implies controlling the `.well-known/polis` file)

There is no certificate authority or key registry. Domain ownership is the root of trust.

### Second Encoding: the `did:web` Document

The same public key is published a second time, at `<site>/.well-known/did.json`, as a W3C DID
Document — so a polis site is also a conformant `did:web` identity (`did:web:alice.polis.pub`).

**Nothing about the key or the signing path changes.** The DID Document is a *projection*: the same
32 Ed25519 public-key bytes that appear in `.well-known/polis` as an OpenSSH line, re-encoded as an
RFC 8037 JWK (`{"kty":"OKP","crv":"Ed25519","x":<base64url>}`). Content is still signed with the SSH
signature format; there is no second key, no second signing path, and no JOSE dependency.

The security properties are therefore **identical to the ones above** — `did:web` anchors trust in DNS
plus TLS/CA exactly as `.well-known/polis` does, which is why polis matched the method before adopting
it. Two consequences follow:

- **The projection adds no attack surface of its own.** An attacker who can rewrite `did.json` can
  already rewrite `.well-known/polis`; the DID Document is not a second root of trust, it is a second
  *reading* of the same one. On a hosted service it is regenerated from the site's own key on every
  maintenance sweep, so a rewritten copy is replaced rather than merely detected — with one exception: a
  document that names *more* keys than a rebuild would is left in place and reported, because it may be
  the only local evidence of an erased key history.
- **The key read for the projection comes from `.well-known/polis`, not from `.polis/keys/`.** The
  published document can therefore never state a key different from the one the identity document
  states. If the two on-disk copies ever diverge, that divergence stays visible where Patrol reports it
  rather than being silently resolved.

After a rotation, retired keys stay in the DID Document's `verificationMethod` and drop out of
`assertionMethod` and `authentication`, so a resolver can still find the key an older artifact was
signed with. Signed key succession itself lives on the site, in `.well-known/polis` →
`public_key_history` ([key history](../../signet/spec/key-history.md)), where each handover is signed
by the key it retires; the discovery service keeps an independent record that can contradict an
edited chain.

Full profile: [`../../signet/spec/did-web.md`](../../signet/spec/did-web.md).

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

These audit tables describe what the reference CLIs do when they **create** a key. "Never published" and "never transmitted" are properties of that code path, not guarantees about where copies of a key go afterwards — see [Key Custody](#key-custody).

---

## 3. Identity Document (.well-known/polis)

`.well-known/polis` is a site's identity document: the public key every signature from the site is checked against, the history of every key the site has held, and pointers to the site's other signed documents. It is the root of trust for verification. Its identity fields are specified in the [key history spec](../../signet/spec/key-history.md#2-the-shape), and the same keys are projected as a W3C DID document by the [`did:web` spec](../../signet/spec/did-web.md#3-the-document).

### Location

The document is served over HTTPS at `https://<domain>/.well-known/polis`, as JSON, and lives at `.well-known/polis` in the site root. Its integrity rests on TLS and control of the domain; see [What Polis Trusts](#what-polis-trusts).

### JSON Schema

Specified in the [key history spec](../../signet/spec/key-history.md#field-by-field) for the identity fields; the bundle pointers are described in [the content system](../concepts/content-system.md).

### Required Fields

`public_key` is the field verification depends on. See the [key history spec](../../signet/spec/key-history.md#field-by-field).

### Optional Fields

`public_key_history` records every identity key the site has held, each handover signed by the key it retired ([key history](../../signet/spec/key-history.md)); `public_key_messages` carries the DM messages key, signed by the identity key ([DM encryption](dm-encryption.md)). The remaining members are site metadata and pointers, which a reader must treat as self-reported.

### Writing the document

The document is unsigned, so a writer preserves every member it does not model, and refuses to drop or alter `public_key`, `public_key_history` or `public_key_messages` unless the change is a key rotation or a messages-key republish. An erased key history cannot be restored, because its entries are signed by keys that no longer exist. See the [key history spec](../../signet/spec/key-history.md#every-other-writer-must-carry-it-through); signed artifacts take the opposite rule, described in [signing base §6.2](../../signet/spec/signing-base.md#62-rewriting--tolerant-reading-is-not-tolerant-writing).

#### Avatar Config

Avatar styling is display metadata with no security role.

### Bundle Registry

The `bundles` member points at each installed bundle's declaration. It carries no security property of its own; see [the content system](../concepts/content-system.md).

### Validation Rules

`polis validate` checks the document, including key-history chain validity. The rules a verifier applies to the key chain are in the [key history spec](../../signet/spec/key-history.md#4-how-a-verifier-walks-it).

### Go Struct Definition

The reference implementation is `cli-go/pkg/site`.

### Example: Minimal Identity Document

See [A site that has never rotated](../../signet/spec/key-history.md#a-site-that-has-never-rotated).

### Example: Full Identity Document

See [After one rotation](../../signet/spec/key-history.md#after-one-rotation).

### Example: Multi-Bundle Identity Document

See [the content system](../concepts/content-system.md).

---

## 4. Signature Model

Every post, comment and signed JSON artifact carries an Ed25519 signature, made with the site's key, over a defined **signing base** — the exact bytes the signature covers, which is never simply "the file". A verifier fetches the artifact, fetches the site's public key from `.well-known/polis`, rebuilds the signing base, and checks the signature. The signing base for every type, the four verification outcomes, and the rule that no existing signature may ever stop verifying are specified in the [signing base spec](../../signet/spec/signing-base.md).

### What Gets Signed

Two families: markdown content (posts and comments — frontmatter and body, minus the type's unsigned fields) and canonical JSON (the licence, follow file, tag, blessing list, attestations and actor registry). The per-type rules are in the [signing base spec](../../signet/spec/signing-base.md#1-what-a-signing-base-is-and-why-this-page-exists).

#### Posts

A post's signature covers its frontmatter, including `author:`, and its body. See [signing base §4](../../signet/spec/signing-base.md#44-worked-example--a-post).

#### Comments

A comment's signature covers its frontmatter and body; its `author:` field is written after signing and is not covered. See [signing base §4](../../signet/spec/signing-base.md#45-worked-example--a-comment).

#### Blessings

A post author's blessing decisions are recorded in a signed blessing list on the author's own site. See [the blessing list](../../signet/spec/signing-base.md#the-blessing-list-blessedjson) and the [attestation spec](../../signet/spec/attestation.md).

### Verification Flow

A signature that verifies proves the site's key signed those bytes; a body hash that matches proves the body was not changed since. The outcomes a verifier reports — including `unknown` for a file carrying fields the verifier does not recognise — are in [signing base §6](../../signet/spec/signing-base.md#6-verifying-end-to-end). A signature made under a retired key resolves through the site's [key history](../../signet/spec/key-history.md#resolving-a-signature-to-a-key).

"Valid" is a statement about the key. Whether the site's author made the signature depends on who held the key when it was made, which verification cannot see — see [What a Signature Does Not Tell You](#what-a-signature-does-not-tell-you).

### Instance-to-Instance Signed Requests

One site authenticates to another (for DM delivery) with signed request headers, verified against the sender's `.well-known/polis` key and bounded by a timestamp window, so no API keys are shared between sites. The headers and the signed canonical JSON are specified in the [API reference](../../api/developer/reference.md#authentication).

---

## 5. Trust Model

Polis trusts domains, the keys those domains publish, and signatures checked against those keys — nothing else. The discovery service is a coordination layer whose output is checked, not an authority.

### What Polis Trusts

Control of a domain (bound by DNS and TLS), the public key that domain publishes at `.well-known/polis`, and signatures that verify against that key or an earlier key in its [key history](../../signet/spec/key-history.md). A signature shows that **a holder of the key** signed — not necessarily the author.

### What a Signature Does Not Tell You

**The protocol proves that a signature was made by a holder of the site's key. It cannot tell you whether that holder is the author.** Any arrangement in which someone else holds the key — a hosting service, a shared machine, a team, a stolen laptop — produces signatures that are **valid and indistinguishable** from the author's own. Nothing in the signature, the identity document, or the key history records who held the key when it signed.

This is a limit of what a signature can say, not a flaw in how polis checks one, and no verifier can work around it. A reader who needs to know that a *person* stands behind content, and not merely a key, has to learn how that site's key is held from outside the protocol — from the author, or from whoever hosts the site. An operator's custody declaration, which `polis actor verify --custody <domain>` shows, is what the operator *said* about how it holds the key, not proof of it. See [Key Custody](#key-custody) and the [custody spec](../../signet/spec/custody.md).

### What Polis Does NOT Trust

Self-reported fields (email, display names, timestamps), who holds a key, the discovery service, and other sites' content, which is always fetched and verified independently.

### Discovery Service Trust

The discovery service signs its responses, and the events it relays carry the original author's signature, so a client can detect a tampered response or a fabricated event. It decides no blessing: blessing decisions are signed by the post author and the service only witnesses them. A compromised service can still suppress or delay events and see who follows whom. What the service signs and keeps is specified in the [witness spec](../../signet/spec/witness.md#8-the-discovery-services-side-stated-honestly).

### following.json Trust

A site's follow list is published and signed with the site's key. It is a statement of whom the site owner follows, not a claim that those sites are trustworthy, and it feeds the `following` source in policies. Its signed form is in [signing base §5.3](../../signet/spec/signing-base.md#pubpolisfollow--the-follow-file-followingjson).

---

## 6. Policies

A site decides what it accepts from others with declarative policy rules, evaluated by the site's own software — never by the discovery service on the site's behalf. The grammar, the three policy layers, the verb validity matrix, the evaluation order and the default files are specified in the [policy grammar](../reference/policy-grammar.md); the user guide is [Policies](../../cli/user/policies.md).

### File Locations

A private file (`.polis/policies/rules.jsonl`, never published) is evaluated before the public one (`policies/rules.jsonl`). See [file format](../reference/policy-grammar.md#file-format).

### Format

See [file format](../reference/policy-grammar.md#file-format).

### Grammar

See [writable grammar](../reference/policy-grammar.md#writable-grammar) and the [verb validity matrix](../reference/policy-grammar.md#verb-validity-matrix).

### Type Matching

Type prefixes match on a dot boundary. See [writable grammar](../reference/policy-grammar.md#writable-grammar).

### Source Matching

See [writable grammar](../reference/policy-grammar.md#writable-grammar).

### Evaluation Order

Private rules before public, first match wins. See [evaluation semantics](../reference/policy-grammar.md#evaluation-semantics).

### Examples

See [common recipes](../../cli/user/policies.md#common-recipes).

### Default Policies

See [canonical default files](../reference/policy-grammar.md#canonical-default-files).

### Legacy grammar (v1)

Older blessing rules are still parsed, because historical signed attestations embed the rule string byte for byte. See [legacy grammar](../reference/policy-grammar.md#legacy-grammar).

### DM Acceptance Policy

Which sites may send a site direct messages is decided by Layer 1 `allow` / `deny` rules on `pub.polis.dm`, evaluated before any cryptographic work. See [Layer 1 in the policy grammar](../reference/policy-grammar.md#layer-1--tenant-inbound).

### Integration Points

Policies gate inbound content, notifications, DM acceptance and the blessing decision. See [the three layers](../reference/policy-grammar.md#the-three-layers-in-detail).

### Go Interface

The reference implementation is `cli-go/pkg/policy`.

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
| **Attack** | Someone other than the author obtains a copy of the site's private key — from `.polis/keys/`, from a backup or snapshot, or from any party that generated or stores the key |
| **Mitigation** | The reference CLIs write the key with mode `0600` and exclude `.polis/` from git. These protect one copy on one machine; they do nothing about copies held anywhere else ([Key Custody](#key-custody)) |
| **Residual risk** | No key *revocation*. Until a rotation, the copy signs freely, and **indistinguishably** — its signatures are exactly as valid as the author's. **The author may not get to rotate at all.** A rotation needs only the current private key: the discovery service checks that the old key is the one it holds as current and that the transition signature verifies, and nothing else. So a holder of a copy can rotate to a key of their own choosing, the discovery service accepts it and records their key as current, and anyone who can also write to the site can publish it in the site's key history. Author and thief hold the same key, **whoever rotates first wins**, and the protocol gives the other no way to undo it. Even after the author rotates first, the retired key can still sign content that claims to predate the rotation ([Key Custody](#key-custody)). Pre-rotation — committing to the next key in advance — would close the first-rotation race; it is not implemented |

### 7.3 Domain Takeover

| Aspect | Detail |
|--------|--------|
| **Attack** | An attacker who controls the *served* `.well-known/polis` — full domain takeover, but also a rogue CDN edge, a static-host operator with filesystem write (GitHub Pages, Netlify, S3), or a domain reclaimed after expiry — replaces the published `public_key` with their own |
| **Mitigation** | The `public_key` is the root of trust, so substitution does not retroactively forge history: previously signed content fails verification against the new key. For DS-registered sites the DS keeps a versioned key history and only accepts a rotation carrying a valid transition signature from the *old* key (`discovery-service/core/handlers/keys.ts`), so an attacker lacking the old private key cannot make the DS adopt their key. JUDGE alerts on unexpected key changes for hosted tenants (Section 8.10). |
| **Residual risk** | A client verifying content fetched directly from the origin trusts the freshly-fetched key — there is no general client-side TOFU pinning yet. Self-signing the identity file would *not* close this gap: the embedded proof would verify against the same (substituted) key the file declares. The mitigation direction is key pinning / trust-on-first-use, not file self-signing. |

### 7.4 Discovery Service Compromise

| Aspect | Detail |
|--------|--------|
| **Attack** | Attacker compromises the DS and injects false follow/content/blessing events |
| **Mitigation** | Two-layer verification: (1) DS envelope signature on all query responses verified against DS public key at `/.well-known/polis`, (2) author signature on each stream event verified against author's public key. Clients reject responses with invalid DS signatures and skip events with invalid author signatures. Key rotation recovery: on DS verification failure, client re-fetches the DS key and retries once. |
| **Detection** | Verification failures logged with actor domain, event type, and failure reason. Per-domain failure counters persisted across sync cycles. 3+ consecutive DS envelope failures suspend sync with warning. 5+ author failures from same domain in 24h trigger blocking recommendation. |
| **Residual risk** | DS can suppress events (censorship/denial of service) or leak metadata (who follows whom). A compromised DS serving unsigned events from the pre-signing era could bypass author verification if `require_author_signatures` is false (transitional). The DS no longer decides blessings, so a compromised DS cannot grant one: a blessing needs the post author's signature. It could still withhold or delay a blessing request. |

### 7.5 Replay Attacks

| Aspect | Detail |
|--------|--------|
| **Attack** | Attacker re-submits a previously valid signed message |
| **Mitigation** | Signed requests carry a timestamp that is accepted only inside a freshness window — ±5 minutes at the discovery service, ±2 minutes for instance-to-instance DM delivery. The DS keeps one registration per type and URL, so a replayed registration overwrites the same row rather than adding one. A replayed DM is dropped by its message ID, which is derived from the transport nonce |
| **Residual risk** | A request replayed inside its freshness window is accepted again; for a registration that re-applies the same state. There are no sequence numbers |

### 7.6 Impersonation (Author Spoofing)

| Aspect | Detail |
|--------|--------|
| **Attack** | Attacker creates a site claiming to be a different author |
| **Mitigation** | Signature verification requires fetching public key from the claimed domain |
| **Residual risk** | Display names are not verified; similar domain names could mislead users. **Verification establishes the key, not the person**: anyone who holds a copy of a site's key ([Key Custody](#key-custody)) can publish as that site, and the result is indistinguishable from the author's own. This attack needs no spoofing at all, and nothing in this row detects it |

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
| **Residual risk** | Follower list metadata is public. The DS rate-limits follow announcements per domain (stream-publish 100/hr), so volume-based inflation is bounded |

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
| **Residual risk** | Remote content fetches in the verification flow are read through a 10 MB cap per fetch, but a verification run that fetches many resources is not capped in total |

### 7.12 Private Key Backup Failure

| Aspect | Detail |
|--------|--------|
| **Attack** | The only copy of the private key is lost — disk failure, no backup |
| **Mitigation** | None in the protocol. Backing up the key is the key holder's job. Any backup is itself another copy of the key, so whoever holds the backup is a key holder too ([Key Custody](#key-custody)) — which is why backup and exclusive control pull in opposite directions |
| **Residual risk** | All previous content remains verifiable, but nobody can sign new content or updates as the site, and no key rotation is possible (rotation needs the old key) |

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
| **Mitigation** | On `polis.pub`, an **edge budget first**: the hosting layer charges the delivery to a per-(recipient, claimed sender) and a per-recipient hourly budget before the recipient's server is even loaded, answering `429` when either is exhausted. Then the recipient's own seven checks, in the order they run: timestamp freshness → sender key fetch (cached) and signature verify → acceptance policy → global rate limit → per-sender rate limit → size check → validate the recipient epoch and store the box unopened |
| **Residual risk** | The recipient's own timestamp and signature checks, including a cached fetch of the sender's `.well-known/polis`, run before its policy and rate-limit checks, so a flood of well-formed requests costs a verification each before any of its budgets is consumed. An attacker controlling many verified domains could distribute load across senders; the global rate limit is the backstop. ⚠️ **The hosted edge budget is charged before verification, so it is charged to the sender the request CLAIMS to be** — see the trade-off below |

⚠️ **The hosted edge budget cuts both ways, and this is a deliberate trade.** Verification is the
expensive work being protected, so the budget has to be spent before the claimed sender has been
proved. Anyone can therefore set `X-Polis-Domain` to a domain they do not control and spend that
sender's hourly budget to one recipient, or spend a recipient's whole hourly inbound budget with
rotating claimed senders. **The cost is bounded: up to an hour of refused inbound DMs for that
recipient, and nothing is accepted, altered or read** — a forged header cannot deliver a message,
because the recipient still verifies every one. Without the edge budget the same flood costs unbounded
outbound fetches, signature verifications and disk writes instead. It is visible as
`dm.deliver.global_rate_limited` with many distinct `sender_domain` values. A per-IP layer in front of
it is planned.

### 7.15 DM Content Interception (Transport)

| Aspect | Detail |
|--------|--------|
| **Attack** | Network attacker intercepts DM in transit between instances |
| **Mitigation** | NaCl box encryption (X25519 + XSalsa20-Poly1305) provides confidentiality + integrity; HTTPS provides transport layer security |
| **Residual risk** | No forward secrecy within an epoch: a compromised epoch key (DEK) exposes every message sealed to or from it, with every peer. Other epochs are unaffected ([`dm-encryption.md`](dm-encryption.md)) |

### 7.16 DM Storage Compromise (At Rest)

| Aspect | Detail |
|--------|--------|
| **Attack** | Attacker gains filesystem access to `.polis/bundles/pub.polis.core/dm/` |
| **Mitigation** | Password-epoch messages are stored as the wire `box` ciphertext; their DEK is held only **wrapped** under a password/recovery KEK (`keyring.json`), so disk access yields ciphertext + wrapped blobs whose recovery is reduced to **offline brute-force of the password** (Argon2id). File permissions 0600. Only routing metadata (from, to, timestamp, `key_epoch`) is cleartext. See [`dm-encryption.md`](dm-encryption.md). |
| **Residual risk** | **Bootstrap-epoch** messages (before the user set a password) are readable — their key is held in plaintext by design (the bootstrap window). A weak password falls to offline cracking. Metadata is visible without decryption. The private *identity* key does **not** derive the message key. |

### 7.17 DM Sender Impersonation

| Aspect | Detail |
|--------|--------|
| **Attack** | Attacker forges DM delivery pretending to be another domain |
| **Mitigation** | Signed request headers verified against the sender's `.well-known/polis` public key, with the signature bound to the recipient domain and a digest of the body; the envelope's `box_pub` is then checked against the sender's published, identity-signed messages keys, so a box sealed from an unpublished key is rejected |
| **Residual risk** | Domain takeover allows impersonation (same as 7.3); no key pinning for DM peers |

### 7.18 DM Key Rotation Disruption

| Aspect | Detail |
|--------|--------|
| **Attack** | Sender rotates key; previously sent DMs become unverifiable if receiver cached old key |
| **Mitigation** | Each message carries the sender's epoch + `box_pub`, and a `key_epoch` tag, so a stored message is decryptable regardless of later sender or receiver rotation; the receiver's stored copy is sealed under its own per-epoch DEK, unaffected by sender rotation |
| **Residual risk** | DMs in flight during sender key rotation may fail signature verification; sender must retry after rotation completes |

### 7.19 DM Metadata Exposure

| Aspect | Detail |
|--------|--------|
| **Attack** | Observer analyzes cleartext metadata to learn communication patterns |
| **Mitigation** | Metadata is local to each instance (not centralized); no intermediary server sees both sides |
| **Residual risk** | Filesystem access reveals routing metadata only — who you message, when, frequency, message sizes — not message content, which is stored as the wire ciphertext. No preview of a message is stored. Conversation IDs are deterministic (derivable from both domains). |

### 7.20 Malicious DM Content

| Aspect | Detail |
|--------|--------|
| **Attack** | Sender delivers encrypted message containing malicious content (exploit payloads, phishing links, HTML injection) |
| **Mitigation** | The box is authenticated (Poly1305), so a message that fails to open is marked undecryptable rather than shown. Opened text is inserted into the page as text (`textContent`), never as HTML, so markup is inert and links are not made clickable. The envelope is capped at 64 KB before it is stored |
| **Residual risk** | There is no validation of the plaintext beyond decoding it as UTF-8. Server operator stores encrypted blobs they cannot inspect (inherent to E2E encryption). Social engineering content cannot be prevented by technical controls. |

---

## 8. Feature Security Analysis

### 8.1 Posts

| Aspect | Security Property |
|--------|-------------------|
| **Authorship** | Ed25519 signature by the site's key (which proves the key, not who held it — Section 5) |
| **Integrity** | SHA-256 hash in `current-version` frontmatter |
| **Confidentiality** | None; posts are public by design |
| **Versioning** | Hash changes with content; signature covers hash |
| **Canonicalization** | Content canonicalized before hashing (strip leading empty lines, trim trailing whitespace, single trailing newline) |

### 8.2 Comments

| Aspect | Security Property |
|--------|-------------------|
| **Authorship** | Signed with the commenter's site key, not the host site's |
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
| **Who decides** | The post author's own software evaluates the rules and signs the decision (by hand, or a user agent under the author's grant, marked as such). The DS applies only its operator's Layer 3 rules, and records and witnesses what the author's software decided; it never acts for the author. Historical DS autobless attestations still verify |

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
| **Independent signing** | Each reply is independently signed with its own site's key |
| **Site owner control** | Entire thread is subject to blessing on the site owner's domain |
| **Cross-site threads** | Thread may span multiple domains; each segment verified independently |

### 8.6 Webapp Security

| Aspect | Security Property |
|--------|-------------------|
| **Local binding** | Webapp binds to `127.0.0.1` by default (localhost only) |
| **Authentication** | No auth for local webapp (trusts local user); API keys for v1 REST API |
| **API key storage** | Hashed with SHA-256 in `.polis/api-keys.json`. polis reads this file and never writes it — there is no key generator, so the key's format and the file's permissions are whatever the key holder chose (the [API reference](../../api/developer/reference.md#authentication) suggests a `polis_`-prefixed random token) |
| **CORS** | The v1 API sends no CORS headers, so browsers enforce same-origin. Public content endpoints allow any origin for `GET` |
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

The security properties of direct messages (confidentiality in transit and at rest, sender authentication, integrity, what
metadata stays visible, and the absence of forward secrecy within an epoch) are stated in
[Direct-message encryption](dm-encryption.md); sender authentication and integrity are in 7.15, 7.17 and 7.20 above, and the byte-level
detail is in [`FORMAT.md`](../../../cli-go/pkg/dm/FORMAT.md) and [`PROTOCOL.md`](../../../cli-go/pkg/dm/PROTOCOL.md). The threats they answer are 7.14–7.20 above; which senders a site accepts is a
Layer 1 rule on `pub.polis.dm` in the [policy grammar](../reference/policy-grammar.md#layer-1--tenant-inbound).

### 8.9 Hooks

| Aspect | Security Property |
|--------|-------------------|
| **Availability** | Self-hosting only. Disabled on polis.pub (`EnableHooks = false`; all hook endpoints return 403) |
| **Execution model** | Shell scripts invoked via `exec.CommandContext` with 30-second timeout |
| **Path containment** | Hook scripts must resolve within the site directory. Symlinks within the site directory are followed, allowing controlled escape for system-installed scripts |
| **Environment** | Inherits parent process environment plus `POLIS_*` variables (event, path, title, version, timestamp, site dir, config dir, commit message) |
| **Input** | JSON payload on stdin containing event metadata |
| **Output** | Combined stdout/stderr captured and returned to the caller. A hook script uploaded through the webapp is capped at 32KB (`MaxHookBodySize`) |
| **Discovery** | Explicit config (`.polis/webapp/config.json` hooks section) or convention (`.polis/webapp/hooks/{event}.sh`) |
| **No sandboxing** | Scripts run with the same user/permissions as the polis process. No seccomp, no chroot, no capabilities restriction |
| **No signature verification** | Hook scripts are not signed or verified. Any executable file at the configured/conventional path will be run |
| **Trigger events** | `post-publish`, `post-republish`, `post-comment` (blessed) |

### 8.10 JUDGE (Automated Cross-Boundary Verification)

| Aspect | Security Property |
|--------|-------------------|
| **Schedule** | Hourly background job on hosted service (`judgeTick = 1 * time.Hour`) |
| **HTTP content verification** | Fetches posts via loopback HTTP and verifies Ed25519 signatures + SHA-256 content hashes, testing the full serving pipeline end-to-end |
| **DS attestation audit** | Verifies each registration's DS service attestation against the key of the DS that signed it; an attestation that cannot be checked (no DS URL, DS unreachable) is reported as not verified, not as failed |
| **Key continuity monitoring** | Tracks each tenant's public key fingerprint over time; alerts on unexpected key changes (proto-TOFU). Baselines are kept per tenant in the Judge state directory |
| **Policy snapshot verification** | Snapshots policy file hashes and alerts once on any change; it compares hashes and knows nothing about blessings. Snapshots are kept per tenant in the Judge state directory |
| **Index consistency** | Verifies `index.jsonl` entries match real signed files on disk; detects orphaned entries (indexed but missing), phantom files (on disk but not indexed), and hash mismatches |
| **Cross-site comment verification** | Verifies the comments in the tenant's own comment directory (normally the tenant's own comments) against the key of the author each names, resolved through the author's published key history (fetched from the author's `.well-known/polis` via HTTPS); an unreachable author is reported, not counted as checked |
| **Signed-artifact checks** | Also verifies the identity-document snapshot, the messages-key signature, the follow file and blessing list signatures, served artifacts against disk, licence origin, key-history chain validity and head agreement, and reports signed fields the verifier does not recognise. Judge reports and never heals |
| **State persistence** | Judge state survives restarts via a persistent volume |
| **Observability** | All findings emitted as structured JSON events (`judge.sweep`, `judge.fail.*`, `judge.alert.*`, `judge.info.*`) to an observability backend for monitoring and alerting |
| **Graceful degradation** | External HTTP failures (item 6) are skipped gracefully with 5-second timeouts; unreachable domains are cached to avoid retry storms |

### 8.11 Rate Limiting and Abuse Prevention

Rate limiting is enforced at every network boundary, returning `429` with a `Retry-After` header when a bucket is exhausted. Client IPs are resolved from the platform's trusted client-IP header rather than the spoofable left-most `X-Forwarded-For`, so limits cannot be evaded by forging forwarding headers.

| Boundary | Scope | Representative limits |
|----------|-------|-----------------------|
| **DS — per IP** | All query + write endpoints | content-query 600/hr, stream 1200/hr, relationship-query 600/hr, sites-check 300/hr, write-preauth 120/hr |
| **DS — per domain** | Authenticated writes | site-register 5/hr, content-register 50/hr, content-unregister 20/hr, relationship-update 50/hr, stream-publish 100/hr, key-rotate 5/hr; plus a quota of 2,000 new content rows per 30 days |
| **Webapp — per IP** | Public content + structured-query endpoints | 1000/hr |
| **Hosted — per IP** | Public site pages | 3000/hr |
| **Hosted — per IP** | Signup / login / recover | 3/hr, plus per-email caps (recover 1/hr/email; export & email-change 1/10min) |
| **DM — per sender + global** | Inbound DM delivery, per recipient site, checked by the recipient after verification | 10/sender/hr, 100/hr global (configurable via `POLIS_DM_RATE_*`) |
| **Hosted — per claimed sender + per tenant** | Inbound DM delivery at the hosting edge, **before** the recipient verifies anything | the same 10/sender/hr and 100/hr budgets (`POLIS_DM_RATE_*`) |

Every DS write first passes the per-IP `write-preauth` budget, before its handler runs. The per-domain limits for site registration and unregistration, key rotation and relationship updates are then checked before any signature verification or outbound `.well-known/polis` fetch; content registration and stream publish check theirs after the signature verifies. A recipient's own DM rate checks run after the timestamp and signature checks (Section 7.14), so they bound what a verified sender may deliver rather than protecting against unverified floods. On `polis.pub` the hosting edge adds a budget in front of them, charged to the sender the request claims to be, with the trade-off stated in Section 7.14; a claimed sender that is not a well-formed domain name is refused there outright, before it can key anything. Their counters are in memory, per recipient site and per process, and start again after a restart. Memory is bounded first by the limiters themselves: each one drops expired counters as it goes, at a cost that stays flat however many distinct callers it has seen, and Reaper's periodic sweep of the hosted per-IP and per-email maps is the backstop behind that.

---

## 9. Known Limitations

| Limitation | Impact | Potential Mitigation |
|------------|--------|---------------------|
| **A key is not bound to a person** | A valid signature proves the site's key signed. It cannot show that the author did, or tell the author apart from anyone else who holds the key (Section 5, [Key Custody](#key-custody)) | None within a signature. The author generating the key and never letting a copy out is the only arrangement in which key holder and author coincide — and it cannot be proven to anyone else |
| **No key revocation** | Key rotation (`polis rotate-key`) lets a key holder *replace* a key, but there is no mechanism to actively *revoke* one. A compromised key stays valid until someone rotates — and a thief holding the same key can rotate first (Section 7.2) | Revocation list or certificate-transparency-style log; pre-rotation to close the first-rotation race |
| **No key pinning** | Clients fetch the key fresh on every verification, so a substituted `public_key` is trusted on first contact | TOFU (Trust On First Use) key pinning, possibly anchored on the DS's first-seen key history. JUDGE's key-continuity monitoring (Section 8.10) is a partial mitigation today, alerting on unexpected key changes for hosted tenants. Self-signing the identity file does *not* help — its proof verifies against the same key the file declares |
| **No forward secrecy** | Key compromise exposes all past signatures (though content is public anyway) | N/A for public content system |
| **Unencrypted private keys** | Keys stored without password protection, so anything that can read the file — including every backup of it — can sign as the site | Optional passphrase encryption |
| **No trusted timestamps** | Self-reported timestamps could be backdated or future-dated. A discovery service's witness countersignature records when that service saw a registration or key rotation ([witness spec](../../signet/spec/witness.md)), but only for what was registered with it, and nothing requires one | Timestamping authority or blockchain anchoring |
| **Single key per site** | No key delegation or sub-keys. A person or program acting for a site signs with the site's one key, so a signature cannot show which of them acted. An operator can publish a signed registry of the separate actor sites it runs ([custody spec](../../signet/spec/custody.md)); that registry records expected actions and enforces nothing, and does not change what a signature made with a site's own key shows. A key a site grants to someone else, and can revoke, is not implemented | Delegated, revocable sub-keys; hierarchical key model |
| **No content encryption for public types** | Public content (posts, comments) is unencrypted by design. Direct messages are end-to-end encrypted (NaCl box) and stored at rest as that same ciphertext. | Per-reader encryption for public content |
| **No multi-signature** | No support for content requiring multiple signers | Multi-sig threshold signatures |
| **No DM forward secrecy** | A compromised epoch key exposes every message in that epoch, with all peers; other epochs are unaffected. The Double Ratchet (Signal protocol) would address this but requires synchronized state incompatible with async delivery. | Double Ratchet protocol |
| **DM metadata cleartext** | Sender/recipient domains, timestamps, and message sizes stored in cleartext for indexing. | Encrypt entire conversation files |
| **Agent acts are self-declared** | A user agent signs with the user's key and marks its acts; the mark cannot prove an unmarked act was the user's own. The DS records and flags an act citing a withdrawn grant, but does not reject it | Agent's own live-grant check before signing; the DS's public flag (`grant_state`) as evidence |

---

## 10. Future Considerations

### Key Rotation Protocol ✅ (Implemented)

`polis rotate-key` implements key rotation end to end:
1. Generates a new Ed25519 keypair
2. Signs a canonical rotation message with the **old** key, proving control of both keys
3. Submits the rotation to the discovery service, which verifies the old-key transition signature, confirms the old key matches the site's current active key, and atomically records the new key in its versioned key history (emitting a signed `pub.polis.site.key_rotated` event)
4. Publishes the new public key at `.well-known/polis` and appends the handover to the site's `public_key_history`. No `.old` backup of the retired private key is written

Existing content is *not* re-signed. It stays verifiable because a verifier that fails against the current key resolves the retired key from the site's published key history ([key history](../../signet/spec/key-history.md)) — rotation moves identity forward without re-anchoring history. The retired key keeps verifying content that claims a time inside its window, whoever signs it (Section 7.2).

Remaining future work:
- **Key revocation** (distinct from rotation): a way to actively invalidate a key so prior-trusting parties reject it
- **Client-side rotation-chain consumption**: general clients (not just the DS) could follow the old-key-signed rotation chain to auto-accept announced rotations and reject *unannounced* key changes — the enforcement half of TOFU

### Key Revocation

A revocation mechanism could use:
- Revocation certificates (signed by the key being revoked)
- DS-mediated revocation announcements
- Time-bounded key validity (expiration dates)

### DS Signature Verification ✅ (Implemented)

The discovery service now signs all query responses with its Ed25519 private key, and clients verify both the DS envelope signature and author signatures on stream events. See Section 5 (Discovery Service Trust) and Section 7.4 for details.

Remaining future work:
- **Certificate transparency for DS keys**: publish DS key rotations to a verifiable log
- **Event-level DS co-signatures**: Partially implemented — the DS witnesses content registrations, key rotations and blessing decisions. Remaining: co-signatures on all individual stream events for offline verification of event batches
- **Full DS attestation audit**: JUDGE currently counts blessed comments. Historical autobless attestations could still be verified against the DS public key via `discovery.VerifyAutoblessAttestation()`; no new ones are produced

### TOFU Key Pinning

Trust On First Use would allow clients to:
1. Record the public key on first encounter
2. Alert if the key changes unexpectedly
3. Require explicit user approval for key changes

**Partial implementation**: JUDGE's key continuity monitoring (Section 8.10) implements steps 1-2 for hosted tenants. It records a key baseline on first encounter and alerts via `judge.alert.key_change` events when the key changes in a way the site's own published key history does not prove. A rotation that history proves (verified back to genesis, its head the published key, the baselined key among its retired entries) moves the baseline instead. Full TOFU would extend this to client-side verification for all domains.

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
| 2026-09-15 | The DS no longer decides blessings: comment registration is always pending, and the post author's own software (or a user agent under the author's grant) decides and signs. `thread-blessed` is resolved by the post author's site. Updated Sections 5, 7, 8.3, 9, 10 |
| 2026-09-13 | Key-holder accuracy pass: the intro no longer says content is signed with "the author's" key or that the document covers "the entire polis system" — it documents the protocol and its reference implementation, and states that a signature proves the site's key, not who held it. Added **Key Custody** (Section 2) and **What a Signature Does Not Tell You** (Section 5); reworded the Section 4 verification outcomes and Section 8 authorship rows to name the key rather than the author. Section 7.2 now states that anyone holding a copy of a key can rotate first and that the discovery service accepts the first valid rotation. Split Section 7.12's "user responsibility" into what the protocol does and what a backup implies. Added the "a key is not bound to a person" limitation and reframed "single key per site" (Section 9). Corrected three statements made stale by later work: `did:web` and key history (Section 3), `following.json` signing (Section 5), and `.old` key backups (Section 10) |
| 2026-05-24 | Accuracy pass: removed the stale "no rate limiting" limitation and added Section 8.11 documenting the actual per-IP / per-domain / per-sender rate-limiting posture (DS, webapp, hosted, DM); marked the Key Rotation Protocol implemented (Section 10) and reflected `polis rotate-key` + DS-enforced transition signatures across attack vectors 7.2/7.3/7.8 and the key-revocation/pinning limitations; clarified that self-signing `.well-known/polis` does not mitigate key substitution (pinning / TOFU is the direction); removed the "public following lists" limitation (a by-design privacy property already documented in §8.4, not a security gap); credited DM index-preview encryption at rest across §7.16/§7.19/§8.8 (only structural routing metadata remains cleartext) |
| 2026-03-27 | Added JUDGE automated verification system (Section 8.10): hourly cross-boundary verification covering HTTP content verification, DS attestation audit, key continuity monitoring, policy snapshots, index consistency, and cross-site comment verification. Updated Sections 9 (Known Limitations) and 10 (Future Considerations) to reference JUDGE capabilities |
| 2026-03-23 | DS attestation signatures for autoblessed comments: DS signs autobless policy evaluation decisions, stored in blessing relationship signature field and stream event payload. Updated Sections 5, 7.4, 8.3, 9, 10 |
| 2026-03-08 | Accuracy fixes: added `author_name` and `avatar` fields to well-known/polis schema; removed legacy manifest.json migration tables; replaced incorrect single default policy with actual 10-rule defaults across public/private files; added version header documentation for rules.jsonl; updated EvaluateExplicit outcome table with all four decisions (allow/deny/emit/omit) and EvalResult struct; fixed auto-bless example to use `emit` verb; expanded verb semantics documentation; added `version-history` to post frontmatter and `author` to comment frontmatter examples; simplified DM policy provenance text; added EvaluateWithLog to Go interface |
| 2026-03-07 | DS signature verification: DS envelope signing on all query responses, client-side DS + author signature verification, verification failure tracking and anomaly detection, updated Section 5 (Discovery Service Trust), Section 7.4 (DS Compromise), Section 10 (Future Considerations) |
| 2026-03-07 | Added DM security model: Ed25519→X25519 key conversion, NaCl box/secretbox encryption, instance-to-instance signed request auth, DM acceptance policies, attack vectors 7.14-7.20, feature analysis 8.8, updated known limitations |
| 2026-03-03 | Removed deprecated domain migration section (feature removed); fixed comment `in-reply-to` structure (`root-post` not `version`); renumbered Feature Security Analysis subsections |
| 2026-03-01 | Merged security model, .well-known/polis spec, and policies spec into unified document |

---

## 12. Feedback Welcome

If you find a security issue in polis, please report it responsibly. See the [security policy](SECURITY.md) for reporting instructions. Do not report security vulnerabilities through public GitHub issues.

For questions about the security model, architecture discussions, or suggestions for improvements, open a discussion or reach out to the maintainers.
