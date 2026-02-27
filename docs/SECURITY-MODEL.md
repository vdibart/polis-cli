# Polis Security Model

This document describes the security architecture of Polis, including cryptographic foundations, threat model, attack vector analysis, and feature-by-feature security considerations.

**Target audience:** Developers with security interest, security professionals, CIOs, and technical analysts evaluating the platform.

**Goal:** Build trust through transparency about our security approach, invite feedback, and actively identify gaps.

---

## Table of Contents

1. [Cryptographic Foundations](#cryptographic-foundations)
2. [Key Management](#key-management)
3. [Signature Model](#signature-model)
4. [Trust Model](#trust-model)
5. [Attack Vectors and Mitigations](#attack-vectors-and-mitigations)
6. [Feature Security Analysis](#feature-security-analysis)
7. [Known Limitations](#known-limitations)
8. [Future Considerations](#future-considerations)

---

## Cryptographic Foundations

### Algorithm Choice: Ed25519

Polis uses **Ed25519** for all digital signatures. This is a modern elliptic curve signature scheme with the following properties:

| Property | Value |
|----------|-------|
| Key size | 256-bit private key, 256-bit public key |
| Signature size | 512 bits (64 bytes) |
| Security level | ~128-bit equivalent |
| Performance | Very fast signing and verification |

**Why Ed25519?**

1. **Security:** No known practical attacks. Resistant to timing attacks by design.
2. **Performance:** One of the fastest signature schemes available.
3. **Simplicity:** Fixed parameters eliminate configuration errors (unlike ECDSA curve choices).
4. **Adoption:** Used by SSH, Signal, Tor, and many modern systems.
5. **Small signatures:** 64 bytes vs 256+ for RSA.

**Implementation:**
- **Discovery service (TypeScript):** Uses the `@noble/ed25519` library, a well-audited JavaScript implementation with no dependencies.
- **Go CLI and Webapp:** Uses Go's standard library `crypto/ed25519`, a constant-time implementation maintained by the Go team.

### Content Integrity: SHA-256

All content is hashed using **SHA-256** before signing. This provides:

- **Integrity verification:** Any modification to content changes the hash.
- **Efficient signing:** Sign the hash, not the entire content.
- **Version tracking:** Each content version has a unique hash identifier.

Hash format: `sha256:<hex-encoded-hash>`

---

## Key Management

### Key Generation

During `polis init`, a new Ed25519 keypair is generated:

```
.polis/
└── keys/
    ├── id_ed25519       # Private key (SSH format, never leaves device)
    └── id_ed25519.pub   # Public key (SSH format, published to web)
```

The private key is generated using cryptographically secure random number generation (CSPRNG) provided by the operating system.

### Private Key Storage

**Location:** `.polis/keys/id_ed25519`

**Format:** OpenSSH private key format (PEM-encoded)

**Protection:**
- File permissions: User-only read/write (0600)
- Never transmitted over network
- Never stored on discovery service
- User responsible for backup and protection

**What we don't do:**
- No password/passphrase protection (user's responsibility)
- No hardware security module (HSM) integration
- No key escrow or recovery service

### Public Key Distribution

The public key is published at a well-known URL:

```
https://<domain>/.well-known/polis
```

This file contains:
- Public key (base64-encoded)
- Author email address

**Why `.well-known`?**
- Standard location for site metadata (RFC 8615)
- Proves domain ownership (you control what's published there)
- Discoverable by any client without prior coordination

### Key-Domain Binding

The security model assumes: **whoever controls the domain controls the identity.**

This means:
- Your public key at `.well-known/polis` is your identity proof
- If you lose domain control, you lose identity control
- Domain migration requires cryptographic proof of continuity

### Implementation Security Audit

The following audit was performed on the CLI implementation to verify private key handling follows security best practices.

#### Private Key Never Printed or Logged

**Verified:** The private key contents are never:
- Printed to stdout or stderr
- Included in JSON output (only the *path* is returned, not contents)
- Written to log files
- Stored in environment variables

**How signing works:**
```bash
# The private key is only passed as a file path to ssh-keygen
ssh-keygen -Y sign -f "$keyfile" -n file "$temp_file" > /dev/null 2>&1
```

The key file path is passed to `ssh-keygen`, which reads the key internally. The CLI never reads or handles the key material directly.

#### No Shell Tracing of Key Operations

**Verified:** The `polis` script uses `set -e` (exit on error) but never enables `set -x` (trace mode), which could leak sensitive operations to stderr.

**polis-tui consideration:** The TUI has an optional `--log 3` mode that enables bash tracing, but this only traces polis-tui's own operations (the external `polis` commands it invokes), not the internals of the polis script itself.

#### File Permissions

**Verified:** Private keys are created with restricted permissions:
- `ssh-keygen` automatically sets `600` (owner read/write only) on private keys
- Public keys get `644` (world-readable) which is appropriate

#### Git Exclusion

**Verified:** The `polis init` command creates a `.gitignore` that excludes:
- The private key file (`.polis/keys/id_ed25519`)
- Environment files (`.env*`)

This prevents accidental commits of sensitive material.

#### Public Key Only in Output

**Verified:** The only key material that appears in output or logs is the public key:
- `init` command outputs the public key to `.well-known/polis`
- JSON mode returns key *paths*, not contents
- Human-readable output mentions paths, not key contents

#### Temporary Files

**Verified:** Temporary files created during signing contain only:
- Content being signed (posts, comments, payloads)
- Never the private key

Temp files are cleaned up immediately after use:
```bash
rm -f "$temp_file" "$temp_file.sig"
```

#### Error Messages

**Verified:** Error messages reference keys by path only:
- "Private key not found. Run 'polis init' first."
- "Failed to sign payload. Check your private key."

No error condition causes key material to be printed.

#### Audit Summary

| Check | Status |
|-------|--------|
| Private key never echoed/printed | ✓ Pass |
| Private key not in JSON output | ✓ Pass |
| Private key not in logs | ✓ Pass |
| No shell tracing of key operations | ✓ Pass |
| Proper file permissions (600) | ✓ Pass |
| Git exclusion configured | ✓ Pass |
| Temp files don't contain keys | ✓ Pass |
| Error messages don't leak keys | ✓ Pass |

### Go CLI Implementation Audit

The Go CLI (`cli-go/`) uses native `crypto/ed25519` instead of shelling out to `ssh-keygen`. This changes the trust surface:

**Signing approach:**
```go
// Private key read from PEM file into memory, used via crypto/ed25519
privKey, _ := os.ReadFile(keyPath)     // PEM bytes
key := signing.ParsePrivateKey(privKey) // ed25519.PrivateKey
signature := signing.SignContent(data, key)
```

The key is read into process memory and used directly by Go's `crypto/ed25519`. There is no external process or shell command involved.

#### Private Key Never Transmitted or Logged

**Verified:** The Go CLI:
- Reads PEM from file into `[]byte`, parses to `ed25519.PrivateKey`, signs, returns
- Key bytes are never printed, logged, or transmitted over the network
- JSON output includes file *paths* (e.g., `old_key_path`), never key contents
- `rotate-key` command outputs the new *public* key only

#### No Shell Tracing Risk

Unlike the bash CLI, Go does not have a `set -x` equivalent. There is no mechanism that could accidentally trace key material to stderr.

#### Memory Considerations

- Private key bytes remain in Go process memory until garbage collected
- Go's GC does not zero memory before freeing (standard limitation of GC-managed languages)
- The webapp keeps `PrivateKey []byte` in the server struct for the process lifetime
- This is a defense-in-depth consideration, not an active vulnerability

#### Go CLI Audit Summary

| Check | Status |
|-------|--------|
| Private key never printed/logged | ✓ Pass |
| Private key not in JSON output | ✓ Pass |
| Private key not in HTTP responses | ✓ Pass |
| No shell tracing risk | ✓ Pass (N/A for Go) |
| File permissions (600) on write | ✓ Pass |
| Git exclusion configured | ✓ Pass |
| Temp files don't contain keys | ✓ Pass |
| Error messages don't leak keys | ✓ Pass |

---

## Signature Model

### What Gets Signed

Every piece of published content includes a cryptographic signature over a defined payload:

#### Posts

Signed payload:
```json
{
  "url": "https://author.com/posts/20260107/my-post.md",
  "content_hash": "sha256:abc123...",
  "timestamp": "2026-01-07T12:00:00Z"
}
```

#### Comments

Signed payload:
```json
{
  "url": "https://commenter.com/comments/20260107/reply.md",
  "content_hash": "sha256:def456...",
  "in_reply_to": "https://author.com/posts/20260107/my-post.md",
  "root_post": "https://author.com/posts/20260107/my-post.md",
  "timestamp": "2026-01-07T12:00:00Z"
}
```

Note: `in_reply_to` is the immediate parent (post or comment), `root_post` is always the original post (for nested thread support).

#### Blessing Actions

Signed payload:
```json
{
  "action": "grant",  // or "deny"
  "comment_version": "sha256:f4bac5d0efaa759...",
  "timestamp": "2026-01-07T12:00:00Z"
}
```

#### Domain Migrations

Signed payload:
```json
{
  "old_domain": "old.example.com",
  "new_domain": "new.example.com"
}
```

### Signature Verification

All signature verification follows the same pattern:

1. **Fetch public key** from author's `.well-known/polis`
2. **Reconstruct signed payload** from provided data
3. **Verify signature** using Ed25519
4. **Reject if invalid** - no fallback, no retry

This happens:
- On the discovery service when receiving beseech requests
- On the discovery service when processing blessing grant/deny
- On the discovery service when processing domain migrations
- Locally when applying migrations (key continuity check)

---

## Trust Model

### What Polis Trusts

| Component | Trust Assumption |
|-----------|------------------|
| User's device | Private key is secure |
| User's domain | DNS is not compromised |
| HTTPS/TLS | Certificate authorities are trustworthy |
| Discovery service | Honest but curious (see below) |

### Discovery Service Trust

The discovery service is designed as **"honest but curious"**:

**What it can see:**
- All beseech requests (comment metadata)
- All blessing decisions
- Public keys (fetched during verification)

**What it cannot do:**
- Forge signatures (doesn't have private keys)
- Modify content (would invalidate signatures)
- Impersonate users (can't sign on their behalf)

**What it could do (if malicious):**
- Refuse to serve comments (denial of service)
- Serve stale data
- Log access patterns

**Mitigation:** The discovery service is designed to be replaceable. Users could run their own or switch to alternatives. The protocol doesn't depend on any single discovery service.

### `following.json` Trust Assumption

The `following.json` file (listing who you follow) is fetched over HTTPS without additional signature verification. This is an accepted part of the HTTPS/TLS trust boundary:

- An attacker who can MITM `following.json` can also MITM the `.well-known/polis` public key fetch, which is a strictly worse attack
- Injecting a false entry into `following.json` could trigger auto-blessing of that author's comments, but only on the victim's own posts
- This is the same trust model as fetching any other site metadata over HTTPS

**Long-term consideration:** Signed `following.json` would provide an additional layer of integrity verification.

### What Users Must Protect

1. **Private key:** Loss = impersonation possible until key rotation
2. **Domain control:** Loss = identity hijacking possible
3. **Local files:** Tampering would require re-signing (which requires private key)

---

## Attack Vectors and Mitigations

### 1. Impersonation Attack

**Threat:** Attacker publishes content claiming to be someone else.

**Mitigation:** All content must be signed with the author's private key. Verifiers fetch the public key from the claimed author's `.well-known/polis` and verify. Without the private key, signature verification fails.

**Residual risk:** If attacker compromises private key or domain, impersonation is possible.

---

### 2. Content Tampering

**Threat:** Attacker modifies published content (post or comment).

**Mitigation:** Content hash is included in signed payload. Any modification changes the hash, invalidating the signature.

**Residual risk:** None for signed content. Unsigned content (if any existed) would be vulnerable.

---

### 3. Replay Attack

**Threat:** Attacker replays a valid signed message in a different context.

**Mitigation:**
- Timestamps included in signed payloads
- Discovery service tracks unique `(comment_url, comment_version)` pairs
- Duplicate submissions rejected

**Residual risk:** Blessing grant/deny timestamps are validated with a 1-hour freshness window. Content publishing timestamps are not strictly validated.

---

### 4. Domain Hijacking

**Threat:** Attacker gains control of victim's domain and publishes new `.well-known/polis` with attacker's key.

**Mitigation:**
- Historical content remains verifiable (signatures were valid at publication time)
- Domain migrations require signature from old key (can't migrate without it)
- Key continuity checks when applying migrations locally

**Residual risk:** New content from hijacked domain would appear legitimate. Followers would need out-of-band notification.

---

### 5. Man-in-the-Middle (MITM)

**Threat:** Attacker intercepts communication between client and server.

**Mitigation:**
- All URLs are HTTPS (TLS encryption)
- Public keys fetched over HTTPS
- Signatures verified client-side

**Residual risk:** Depends on TLS/CA trust model. A compromised CA could issue fraudulent certificates.

---

### 6. Discovery Service Compromise

**Threat:** Attacker gains control of discovery service.

**Mitigation:**
- Service cannot forge signatures
- Service cannot modify content (would invalidate signatures)
- Comments are stored on author's own domain (discovery just indexes)
- Protocol designed for discovery service to be replaceable

**Residual risk:** Denial of service, privacy leakage (access patterns), serving stale data.

---

### 7. Unauthorized Blessing

**Threat:** Attacker blesses or denies comments on posts they don't own.

**Mitigation:**
- Blessing grant/deny requires signature verification
- Signed payload includes action, comment_version, timestamp
- Public key fetched from post author's domain (not requester's claim)

**Residual risk:** None if implementation is correct. Vulnerability existed in earlier version (v0.7.0 used email-based auth, fixed in v0.8.0).

---

### 8. Sybil Attack

**Threat:** Attacker creates many fake identities to spam comments or manipulate social graph.

**Mitigation:**
- Each identity requires a domain (cost barrier)
- Blessing model gives post authors control over what appears
- No automated amplification (no algorithmic feed)

**Residual risk:** Attacker with many domains could still spam. Blessing model limits visibility.

---

### 9. Private Key Theft

**Threat:** Attacker steals private key from user's device.

**Mitigation:**
- Private key never leaves device
- File permissions restrict access
- User responsible for device security
- **Key rotation available:** Run `polis rotate-key` to generate new key and re-sign all content

**Residual risk:** If device is compromised, attacker can sign content until key is rotated. Rotate immediately upon detecting compromise.

---

### 10. Timing Attacks

**Threat:** Attacker infers private key through timing variations in cryptographic operations.

**Mitigation:**
- Ed25519 is designed to be constant-time
- `@noble/ed25519` library follows constant-time practices

**Residual risk:** Low. Would require local access to observe timing.

---

### 11. Server-Side Request Forgery (SSRF)

**Threat:** Attacker registers a domain like `169.254.169.254` or `localhost` to make the discovery service issue outbound HTTP requests to internal infrastructure (cloud metadata endpoints, internal services).

**Mitigation:**
- `validateDomainSafety()` rejects all domains before outbound fetch
- Blocked: IPv4/IPv6 address literals, `localhost`, reserved TLDs (`.local`, `.internal`, `.localhost`, `.test`, `.example`, `.invalid`), cloud metadata endpoints
- Returns 400 before any network request is made

**Residual risk:** None for known metadata endpoints. DNS rebinding attacks (domain resolves to internal IP) are not covered — would require DNS resolution validation.

---

### 12. Resource Exhaustion via Write Spam

**Threat:** Attacker sends many requests with garbage signatures to force the discovery service to perform expensive outbound HTTP fetches and Ed25519 signature verifications.

**Mitigation:**
- Two-tier rate limiting: cheap IP-based check (120 req/hr) runs before any outbound fetch or crypto, per-domain check runs after authentication
- SSRF validation rejects requests early for internal domains
- 10s fetch timeout prevents slow-server resource tying
- 64KB payload size limit on all endpoints

**Residual risk:** Distributed attack from many IPs could bypass IP rate limits. Per-domain limits still protect against individual actor spam.

---

### 13. Read Endpoint Abuse

**Threat:** Attacker scrapes or DoS-es read endpoints with high request volume.

**Mitigation:**
- Per-IP rate limits on all read endpoints (300-1200 req/hr depending on endpoint)
- Offset cap of 10,000 on query endpoints prevents deep pagination abuse
- Hourly cleanup of rate limit rows via pg_cron

**Residual risk:** Distributed scraping from many IPs. Standard CDN/WAF protections recommended for production deployments.

---

## Feature Security Analysis

### Publishing Posts

**Question:** How do we ensure only the legitimate author can publish under their domain?

**Answer:** Posts are signed with the author's private key. The signature covers the URL (which includes domain), content hash, and timestamp. Verification fetches the public key from the claimed domain's `.well-known/polis`. Only the private key holder can produce valid signatures.

---

### Publishing Comments

**Question:** How do we prevent User A from publishing a comment that appears to come from User B?

**Answer:** Same as posts. Comment signatures include the comment URL (containing User A's domain). If User A tried to claim User B's domain in the URL, signature verification would fetch User B's public key and fail (User A doesn't have User B's private key).

---

### Blessing Comments

**Question:** How do we prevent User A from blessing comments on User B's posts?

**Answer:** Blessing requests must be signed. The discovery service:
1. Looks up the post author from the comment's `root_post` field
2. Fetches the public key from that author's `.well-known/polis`
3. Verifies the signature

Only the post author (holder of that private key) can sign valid blessing requests.

**Historical note:** Version 0.7.0 used self-reported email for authorization. This was spoofable. Fixed in v0.8.0 to require cryptographic signatures.

---

### Denying Blessing Requests

**Question:** How do we prevent User A from denying blessing requests on User B's posts?

**Answer:** Same as blessing grant - requires signature from post author's private key.

---

### Viewing Comments

**Question:** How do we prevent unauthorized viewing of comments?

**Answer:** Comments are public by default, but users have multiple mechanisms to control visibility:

1. **Don't beseech:** If the commenter never requests a blessing, the comment only exists in their local `public.jsonl`. The discovery service has no record of it.

2. **Application server/gateway:** An application layer can restrict access to specific URLs based on authentication, authorization, or other rules.

3. **Web server configuration:** Standard mechanisms like `.htaccess` (Apache), `nginx.conf` rules, or hosting platform settings can restrict access to comment files.

**Default behavior:** If a user beseeches a comment and takes no protective action, the comment is:
- Stored on commenter's domain (accessible if URL is known)
- Indexed in discovery service (discoverable via query API)
- Stored on the author's domain if blessed (accessible if URL is known)

**Rationale:** Polis defaults to public to enable open discourse, but users retain control over their content at the file and server level. The blessing model controls amplification (what gets promoted on the post author's site), not existence.

---

### Nested Comments

**Question:** How do we maintain security for comment threads?

**Answer:**
- `in_reply_to` field identifies immediate parent (post or comment)
- `root_post` field always references original post
- Both fields are included in signed payload
- Auto-blessing queries use `root_post` to determine thread context

This prevents someone from claiming their comment is part of a thread it was never added to.

---

### Domain Migration

**Question:** How do we prevent User A from hijacking User B's identity by claiming they "migrated"?

**Answer:** Domain migration requires:
1. Signature from old domain's private key
2. Discovery service fetches public key from old domain
3. Verification ensures migrator controls old domain

Migration also records the public key used for verification. When followers apply migrations locally, they verify the new domain has the same public key (key continuity check). If keys don't match, migration is rejected with warning.

---

### Following/Unfollowing

**Question:** Is following an author secure?

**Answer:** Following is a local operation:
- Adds author URL to local `following.json`
- No network request, no signature required
- Only affects local blessed-comments behavior

**Security consideration:** None - this is a local preference, not a network-visible action.

---

### Auto-Blessing (Following)

**Question:** Could someone exploit auto-blessing?

**Answer:** Auto-blessing applies when:
1. You follow an author
2. They comment on your post
3. Their comment is automatically blessed

**Risk:** If you follow a malicious author, their comments appear on your posts automatically.

**Mitigation:**
- Following is explicit opt-in
- You can unfollow and revoke blessing
- Blessing doesn't grant any special permissions, just visibility

---

### Thread-Specific Auto-Blessing

**Question:** How does thread-specific auto-blessing work securely?

**Answer:** Once you bless an author on a specific post, their future comments on that post's thread are auto-blessed. This is tracked by:
- `root_post` field in comments
- Query: "Has this author been blessed on this thread before?"

**Security:** This only affects your posts. No one else's blessing decisions are impacted.

---

### Webapp Security

The webapp (`webapp/localhost/`) is a Go HTTP server that manages a Polis site locally.

#### Network Binding

The webapp binds to `127.0.0.1` (localhost only), not `0.0.0.0`. It is not accessible from the network.

#### Authentication

No authentication is required. The webapp is a local tool (like a text editor), not a network service. The user who can reach `127.0.0.1` is assumed to be the site owner.

#### Private Key Handling

The private key is loaded into the `Server.PrivateKey []byte` field at startup. It is:
- Used only via `signing.SignContent()` for signing operations
- Never serialized to JSON or returned in any HTTP handler response
- Never included in log output

#### Path Traversal Protection

Two validation functions guard all file operations:

- `validatePostPath()` — requires `posts/` prefix, rejects `..` and null bytes, applies `filepath.Clean()` before checks
- `validateContentPath()` — allows specific prefixes (`posts/`, `comments/`, `.polis/posts/drafts/`), root `.md`/`.html` files, applies `filepath.Clean()` before checks

Draft IDs are sanitized with a whitelist regex (`[^a-zA-Z0-9_-]` replaced with `-`).

#### Error Message Redaction

All HTTP error responses return generic messages. Internal error details (file paths, OS error strings) are logged server-side via `s.LogError()` but never exposed to HTTP clients.

---

### Notifications

**Question:** How does the notification system protect user privacy and prevent abuse?

**Answer:** The notification system is designed with privacy and security as core principles:

#### Signed Requests

Notification queries to the discovery service require Ed25519 signatures:

```
GET /notifications?domain=mysite.com&since=2026-01-20T10:00:00Z

Headers:
  X-Polis-Signature: <Ed25519 signature>
  X-Polis-Timestamp: 2026-01-20T12:05:00Z
```

The canonical payload that gets signed:
```
GET /notifications domain=mysite.com since=2026-01-20T10:00:00Z timestamp=2026-01-20T12:05:00Z
```

This ensures only you can query your own notifications.

#### Attack Prevention

| Attack | Mitigation |
|--------|------------|
| Query someone else's notifications | Signature verification fails (requires their private key) |
| Replay captured request | Timestamp expiry (5-minute window) |
| Brute force polling | Rate limiting (60 requests/hour per domain) |
| Enumerate registered domains | Must provide valid signature for each domain |
| Spam follow announcements | Rate limiting (100/day) + deduplication |

#### Privacy Considerations

**What the discovery service learns:**

| Data | Visibility | Notes |
|------|------------|-------|
| Your domain | Full | Required for authentication |
| When you poll | Full | Request timestamps |
| Who you follow | Temporary | Fetched on-demand, not stored |
| Your comments | Partial | Only metadata, not content |

**What remains private:**

- Notification preferences (stored locally in `.polis/`)
- Which notifications you've read (stored locally)
- Your full social graph (discovery fetches `following.json` on-demand but doesn't store it)

#### Opt-in Behaviors

- **Follow announcements:** Require explicit `--announce` flag
- **Notification sync:** User initiates with `polis notifications sync`

---

## Known Limitations

### Key Rotation

Key rotation is supported via the `polis rotate-key` command. This generates a new keypair and re-signs all your content (posts and comments) with the new key.

**What happens during rotation:**
1. New Ed25519 keypair generated
2. All posts re-signed with new key
3. All comments re-signed with new key
4. `.well-known/polis` updated with new public key
5. Old keypair archived (can be deleted with `--delete-old-key`)

**What doesn't change:**
- Blessed comments from others (signed with their keys)
- Following relationships (local data)
- Discovery service records (verifies against current `.well-known/polis`)

**When to rotate:**
- Key compromise or suspected exposure
- Routine security hygiene
- Before transferring device access

**Recovery:** If rotation is interrupted, the old key is preserved at `.polis/keys/id_ed25519.old` and can be restored manually.

---

### No Key Recovery

Lost private key = lost identity. No backup, no escrow, no recovery.

**Rationale:** Self-sovereign means self-responsible. Recovery mechanisms introduce trust parties.

**User guidance:** Back up `.polis/keys/id_ed25519` securely.

---

### Timestamp Validation

Timestamps are included in signatures and validated on the discovery service for blessing grant/deny operations (1-hour freshness window, rejecting both stale and future-dated requests). Content publishing timestamps are not strictly validated.

**Future consideration:** Configurable timestamp windows for content publishing.

---

### Privacy

- Comments are public by default (but visibility can be controlled - see "Viewing Comments" above)
- Access patterns visible to discovery service
- No built-in encryption of content

**Rationale:** Polis defaults to public to enable open discourse. Users who need privacy can:
- Not beseech (keeps comment out of discovery service)
- Restrict access via web server configuration
- Use application-layer access controls

Private messaging and end-to-end encryption are out of scope for the current protocol.

---

### Single Discovery Service

Currently one discovery service (Supabase-hosted). If compromised or offline:
- New comments can't be blessed
- Existing content remains available (on author domains)
- No automatic failover

**Future consideration:** Federated discovery services.

---

### Discovery Service CORS

The discovery service uses `Access-Control-Allow-Origin: *` on all endpoints. This is intentional: the discovery service is a public API that any Polis client (CLI, webapp, browser extension) should be able to reach. There is no session or cookie-based authentication to protect.

---

### Webapp Request Body Size

The webapp does not enforce request body size limits. Since it binds to localhost only, this is mitigated by the local-only access model. A malicious local process could send arbitrarily large requests, but a malicious local process already has full access to the filesystem.

**Future consideration:** Add request body size limits as defense-in-depth.

---

## Future Considerations

### Key Rotation Protocol

Allow users to rotate keys without changing domains. Would require:
- Signed key rotation announcement
- Grace period for transition
- Revocation list or certificate chain

### Federated Discovery

Multiple discovery services that sync. Would provide:
- Redundancy (no single point of failure)
- Censorship resistance
- Geographic distribution

### Hardware Key Support

Integration with hardware security keys (YubiKey, Ledger). Would provide:
- Private key never exportable
- Physical authentication requirement
- Protection against software compromise

### Content Encryption

Optional encryption for private content. Would require:
- Key exchange protocol
- Recipient management
- Significant complexity increase

### Signed `following.json`

Add cryptographic signatures to `following.json` to prevent MITM injection of false following entries. Currently trusted via HTTPS.

### Request Body Size Limits

Add configurable request body size limits to the webapp as defense-in-depth, even though it binds to localhost only.

### CORS Origin Restrictions

If discovery service usage grows beyond the Polis ecosystem, consider restricting CORS origins to known Polis clients.

---

## Document History

- 2026-02-07: Security review of Go CLI, webapp, and discovery service (Go CLI audit, webapp security section, error redaction, timestamp freshness, path traversal hardening, following.json trust documentation)
- 2026-01-26: Moved File Content Integrity sections to USAGE.md (better discoverability for operational guidance)
- 2026-01-21: Added Notifications section (signed requests, attack prevention, privacy)
- 2026-01-12: Added Implementation Security Audit section (private key handling verification)
- 2026-01-07: Initial version

---

## Feedback Welcome

This security model is evolving. If you identify gaps, have questions, or want to contribute improvements:

- Review the source: https://github.com/vdibart/polis-cli
- Open an issue: https://github.com/vdibart/polis-cli/issues

We take security seriously and will address reported issues promptly.
