# Polis DM wire protocol (`pub.polis.core` direct messages)

This is the **cross-implementation contract** for sending a direct message from one
polis site to another. Two implementations that both follow this document can DM each
other regardless of language or runtime. It is the companion to [`FORMAT.md`](./FORMAT.md)
(the *at-rest* format): FORMAT.md says how a message is stored and decrypted offline;
PROTOCOL.md says how it travels between sites.

The authoritative Go implementation is `send.go`, `receive.go`, and `messageskey.go` in
this package. Where this doc and the code disagree, the code wins — file a fix.

## Principles

- **Point-to-point.** A message goes from the sender's site directly to the recipient's
  site over HTTPS. **No discovery service and no central polis entity is in the message
  path or its metadata.** An operator hosting either endpoint is necessarily in the path,
  but cannot read message contents (see threat model in the plan).
- **End-to-end encrypted to a messages key, not the identity key.** Messages are sealed to
  the recipient's published *messages* X25519 key for an epoch. The identity (signing) key
  only ever *signs* the messages-key advertisement; it can never decrypt anything.
- **Fail-closed.** A recipient that has not published a messages key is treated as
  un-encryptable: the sender refuses rather than downgrading. A messages-key advertisement
  that does not verify against the recipient's identity key is a key-swap attempt and is
  also refused.

All binary fields are **base64 (standard encoding, with padding)**. Timestamps are
**RFC 3339 in UTC**. JSON object key order is irrelevant *except* where a canonical
signing form is specified — there, the bytes are exact.

## 1. Key discovery — `GET /.well-known/polis`

The recipient's `.well-known/polis` advertises both the identity key and the signed
messages-key block:

```json
{
  "public_key": "<OpenSSH ed25519 identity public key>",
  "public_key_messages": {
    "current": { "epoch": 1, "key": "<b64 x25519>", "sig": "<identity signature>" },
    "history": [
      { "epoch": 0, "key": "<b64 x25519>", "sig": "<identity signature>" }
    ]
  }
}
```

- **`public_key`** — the site's long-lived Ed25519 identity key (OpenSSH format), the same
  key the discovery service attests and that signs published content.
- **`public_key_messages.current`** — the messages key for the recipient's current epoch.
  Senders encrypt to this.
- **`public_key_messages.history`** — messages keys for older epochs, newest-epoch-first.
  Present so a message addressed to a now-rotated key can still be matched; senders never
  encrypt to history.

### Canonical signing form (exact bytes)

Each entry's `sig` is an identity-key signature (`pkg/signing`, the ssh-keygen `-Y`
compatible format) over the exact byte string:

```
pub.polis.public_key_messages\nepoch=<epoch>\nkey=<b64 key>
```

i.e. `fmt.Sprintf("pub.polis.public_key_messages\nepoch=%d\nkey=%s", epoch, keyB64)` with a
single `\n` (0x0A) between segments and no trailing newline. This form is **frozen** — do
not reorder or reformat it once shipped (`canonicalMessagesKey` in `messageskey.go`).

### Sender verification rule (MUST)

Before encrypting, a sender MUST:

1. Fetch the recipient's `.well-known/polis`.
2. Require `public_key_messages` to be present. **Absent → refuse** (recipient is a
   pre-DM-encryption site; surface as "can't send — recipient hasn't set up secure
   messages").
3. Verify `current.sig` against the advertised `public_key` over the canonical form.
   **Invalid → refuse** (key-swap attempt). Never fall back to the identity-derived key.
4. Encrypt to `current.key` (the verified messages X25519 key).

(`fetchRecipientKey` in `send.go`; `VerifyMessagesKeyEntry` in `messageskey.go`.)

## 2. The message box

The message body is a **NaCl box** (X25519 + XSalsa20-Poly1305): the sender seals the
inner payload from its own current-epoch messages *private* key to the recipient's
current-epoch messages *public* key.

### Inner payload (the sealed plaintext)

```json
{ "content": "<utf-8 message text>",
  "reply_to_id": "<message id?>",
  "sender_public_key": "<b64 x25519 — sender's messages pubkey for sender_epoch>" }
```

`sender_public_key` lets the recipient cross-check the box-opening key against the
sender's published block (defense-in-depth) and is stored at rest as `box_pub` for the
incoming message (see FORMAT.md).

### Envelope (the cleartext wrapper that travels on the wire)

```json
{ "version": 1,
  "sender_domain": "alice.example.com",
  "recipient_domain": "bob.example.com",
  "encrypted_content": "<b64 NaCl box ciphertext>",
  "nonce": "<b64 24 bytes>",
  "timestamp": "<rfc3339>",
  "box_pub": "<b64 x25519 — sender's messages pubkey for sender_epoch>",
  "sender_epoch": 1,
  "recipient_epoch": 1 }
```

- `version` is the envelope schema version (currently `1`).
- `box_pub` is the X25519 public key needed to **open** the box; it is the sender's
  messages public key for `sender_epoch`. It is not secret. Carrying it on the envelope
  keeps receipt self-contained (no circular dependency: you need it to open the box, and
  it would otherwise be inside the box).
- `sender_epoch` is the sender's messages epoch the box was sealed from. The recipient
  looks up the matching entry in the sender's published `public_key_messages` and
  **requires `box_pub` to equal that signed key (MUST)** — see §5. This binds the
  box-opening key to the DS-attested identity: without it, a party able to present a
  valid signed deliver header (e.g. by replaying one) could attach an attacker-generated
  `box_pub` + a box sealed under it, and the recipient would decrypt attacker-chosen
  plaintext attributed to the sender. (Was a "RECOMMENDED cross-check" pre-hardening; now
  enforced — `dm.VerifyBoxPub`.)
- `recipient_epoch` is **which of the recipient's epochs the sender sealed to** (the
  `current.epoch` it fetched from the recipient's block). The recipient **stamps this as
  the stored `key_epoch`** without opening the box — essential because a hosted recipient
  on a password epoch holds no DEK at receive time and *cannot* open it. The recipient
  validates the epoch exists in its keyring and rejects delivery if not (so the sender
  refetches a fresh block); it does **not** trial-decrypt to discover the epoch.

The envelope is **schema version 2**. `box_pub`, `sender_epoch`, and `recipient_epoch`
are required; the signed-request canonical (§3) binds the recipient domain and a digest
of the envelope body. (Version 1 — pre-hardening, signature bound only
`{action,domain,timestamp}` and `box_pub` was unverified at receipt — is retired; there
was no live cross-site traffic when v2 landed, so no dual-accept window is carried.)

## 3. Delivery — `POST /v1/content/dm/actions/deliver`

The sender POSTs the envelope (JSON body) to the recipient site:

```
POST https://<recipient>/v1/content/dm/actions/deliver
X-Polis-Domain:    <sender domain>
X-Polis-Timestamp: <rfc3339>
X-Polis-Signature: <compact identity signature>
Content-Type: application/json

<envelope JSON>
```

### Signed-request authentication

`X-Polis-Signature` is the sender's **identity-key** signature over the canonical auth
JSON:

```json
{"action":"deliver","body_sha256":"<b64 sha256(body)>","domain":"<sender domain>","recipient":"<recipient domain>","timestamp":"<rfc3339>"}
```

marshalled with that **exact field order** — `action`, `body_sha256`, `domain`,
`recipient`, `timestamp` (`MakeDeliverAuthCanonicalJSON` in `send.go`; the byte form is
frozen, do not reorder). `body_sha256` is the base64 SHA-256 of the exact request body
(for `protection_status`, the body is empty, so it's the digest of the empty string).
`recipient` is the recipient's domain. **Neither `recipient` nor the body is carried on the
wire** — the receiver re-derives `recipient` from its own configured domain and the digest
from the bytes it received, so neither is attacker-spoofable. Binding both means a captured
signature cannot be replayed against a different recipient or with a swapped body.

The header carries the signature in compact form (PEM header/footer and newlines stripped;
the receiver restores them before verifying). The recipient:

1. Reconstructs the canonical auth JSON — using its **own** domain as `recipient` and the
   SHA-256 of the **received body** as `body_sha256` — and verifies the signature against
   the sender domain's identity `public_key` (fetched from the sender's `.well-known/polis`).
   Timestamp skew is bounded to **±2 minutes** (`timestampWindow`).
2. **Policy gate (gate #2 of the three independent gates).** Evaluates the inbound
   `pub.polis.dm` policy (`checkPolicy` → `policy.Evaluate`, Layer-1 inbound
   `allow`/`deny`/`bless`/`review`). The default posture is `allow pub.polis.dm from
   following` — i.e. a one-way "I accept DMs from people I follow." A `deny` decision
   rejects delivery. This is independent of the send-time mutual-follow UI gate (#1) and
   of protection-status exposure (#3).
3. Authenticates `box_pub` against the sender's published `public_key_messages` (see §5)
   and stores the box at rest **without opening it**.

Responses: `201 Created` on accept; `4xx` on auth/policy rejection; `5xx` on transient
failure (the sender keeps the message `unsent` and may retry).

## 4. Protection status — `POST /v1/content/dm/actions/protection_status`

A caller may ask whether a site has secured its messages (set a password), to render the
"this person hasn't set a password — be careful what you send" warning. Exposure is
**minimal and gated** (gate #3):

- The request is signed (same signed-request scheme as `deliver`).
- The responder front-checks the caller against its own follow relationship before
  answering; callers outside that relationship get `403`/`404`, not a status.
- The answer is a signed `{ "protected": true|false }` (identity-signed so it can't be
  spoofed by an operator in the path). Unknown/unset → the caller treats it as
  unprotected and warns.

This endpoint never reveals keys, message counts, or contents — only the single protected
bit, and only to a follower. (Task **2.3**.)

## 5. Receipt & storage

On accepted delivery the recipient **does not open the box** — a hosted recipient on a
password epoch holds no DEK, and not decrypting is the point (the operator can't read it).
It:

1. Validates `recipient_epoch` against its keyring (rejects delivery if that epoch is
   unknown, so the sender refetches a fresh block).
2. **Authenticates `box_pub` (MUST).** Fetches the sender's `public_key_messages` and
   requires `box_pub` to equal the identity-signed key for `sender_epoch`
   (`VerifyBoxPub`). A mismatch is rejected — this is what stops a replayed-header attacker
   from attaching an attacker-generated `box_pub` and having the box decrypt to
   attacker-chosen plaintext attributed to the sender. Enforced **only here**, at receipt,
   on incoming boxes; never at read time (a stored `box_pub` may legitimately be an
   ephemeral forward-reseal key or the recipient's own key — see below).
3. **Deduplicates** on the message id (`sha256(nonce)`): a replayed envelope reuses the
   nonce, so a re-POST is idempotent — no second stored line, no unread bump.
4. Stores the **sender's wire box as-is** via the mailbox (`AppendReceived`, `dir:"in"`,
   `box_pub` = sender messages pubkey from the wire, `key_epoch` = `recipient_epoch`). The
   box is **not** opened and **not** re-sealed under any seed-derived key. See FORMAT.md
   §"Message storage".

The box is opened only later, **client-side**, by a reader holding the unwrapped
`key_epoch` DEK: `plaintext = box_open(ciphertext, nonce, box_pub, DEK[key_epoch])`. A
message whose `key_epoch` DEK is not loaded this session comes back **locked**; one whose
DEK *is* available but whose box fails to open (a corrupt tag) comes back **undecryptable**
and is skipped — a single bad box never fails the whole-conversation read. Both are
per-message flags, never an error that aborts the read.

The sender stores its own copy by sealing the same content to **its own** current-epoch
public key (`AppendSent`, `dir:"out"`), so it can re-read its side; an operator holding the
sent site's disk still cannot read it (unless that epoch is the server-held bootstrap
epoch). Sending therefore requires the sender's current-epoch DEK in hand: the server-held
bootstrap DEK (`server_dek`), or — for a password epoch — an unlocked DEK (the SPA unlock,
phase 4). (Tasks **2.5c/2.5d**.)

## 6. Key rotation & stale addressing

- **Epoch rotation (new password / recovery).** The owner gains a new epoch with a fresh
  messages key; `public_key_messages.current` advances and the prior key moves to
  `history`. In-flight messages sealed to the prior key still open via the history walk in
  §5.
- **Identity rotation (`rotate_key`).** The messages keys are unchanged, but every
  `public_key_messages` entry is **re-signed** under the new identity key, and the
  `public_key` is replaced. Verifiers that cached the old identity key re-fetch. Judge
  expects a one-time re-baseline of the signatures. (Task **2.6**.)

## 7. What is explicitly out of the wire path

- No discovery service, no relay, no central inbox. Delivery is site→site.
- No DM-related notifications routed through the DS.
- No plaintext, preview, subject, or unencrypted metadata beyond routing
  (`sender_domain`/`recipient_domain`/`timestamp`) ever crosses the wire.
