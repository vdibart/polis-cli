# Tour: DM encryption

> A guided tour of how a direct message gets encrypted, delivered from one polis instance to another, stored where its own server can't read it, and unlocked again in your browser. Source-of-truth concept docs live in [`../general/`](../general/) — chiefly [`dm-encryption.md`](../general/security/dm-encryption.md); this tour walks the source code with you. Map of all threads: [`../../AGENTS.md`](../../AGENTS.md).

## The observation

Open Messages on your polis.pub site and send someone a message. It arrives on their site — not on a central server, because there isn't one. Their *own* server received it, stored it, and will hand it back to them when they log in.

Now set a password on your messages (the "Set a password to secure your messages" bar). From that moment your server can still *receive* and *store* messages addressed to you, and still *deliver* the ones you send — but it can no longer *read* either side. Your password never went to the server when you set it. There was no key exchange round-trip, no "messaging server" handshake.

Three things are happening at once, and none of them are obvious:

1. Messages are end-to-end encrypted, yet you didn't generate or exchange any keys — and before you set a password you could message anyway.
2. A message travels directly from your server to the recipient's server with no broker in between, and the recipient's server trusts it came from you.
3. Your server stores your messages but, once you've set a password, can't read them — and neither can we.

How does each work?

## The shape of the answer: an epoch keyring

Every DM key decision in polis comes back to one structure — a per-user **keyring** that is an ordered list of **epochs**. An epoch is one generation of message-key material. Epoch 0 is the *bootstrap* epoch (the server holds the key, no password); epochs 1+ are *password* epochs (the key is wrapped under your password, and the server never sees it).

```
   keyring.json  (.polis/bundles/pub.polis.core/dm/)
   ┌───────────────────────────────────────────────────────────────┐
   │  epoch 0  kind=bootstrap   server_held=true                    │
   │     public_key_messages : X25519 pub  (senders seal to this)   │
   │     server_dek          : X25519 priv, PLAINTEXT               │
   │                           ← operator-readable BY DESIGN,        │
   │                             present only during the forwarding  │
   │                             window, then cleared                │
   │                                                                 │
   │  epoch 1  kind=password    server_held=false                   │
   │     public_key_messages : X25519 pub  (new key — senders seal  │
   │                           to THIS once you've upgraded)         │
   │     wrapped_dek          : secretbox(DEK) under password-KEK    │
   │     wrapped_dek_recovery : secretbox(DEK) under recovery-KEK    │
   │     kdf / recovery_kdf   : how each KEK is derived              │
   │  current: 1                                                     │
   └───────────────────────────────────────────────────────────────┘
```

The two-tier idea is the load-bearing one: messages are sealed to the epoch's **DEK** (data encryption key — an X25519 keypair); the DEK is itself wrapped under a **KEK** (key encryption key) derived from your password. Changing your password re-wraps the DEK (cheap); it never re-encrypts a single message.

The verbatim definitions are the best documentation — [`cli-go/pkg/dm/keyring.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/dm/keyring.go):

```go
// Epoch is one generation of DM key material. Epoch 0 is the bootstrap epoch
// (server-held, no password); epochs 1+ are password epochs whose DEK is stored
// twice — wrapped under the password KEK (wrapped_dek) and under the
// recovery-phrase KEK (wrapped_dek_recovery). Either unwraps the same DEK.
type Epoch struct {
    ID                 int        `json:"id"`
    Kind               string     `json:"kind"`
    PublicKeyMessages  string     `json:"public_key_messages"`            // base64 x25519 pub
    ServerHeld         bool       `json:"server_held"`                    // true only for the bootstrap epoch
    ServerDEK          string     `json:"server_dek,omitempty"`           // base64 X25519 private DEK, plaintext, bootstrap epoch only ...
    WrappedDEK         string     `json:"wrapped_dek,omitempty"`          // base64 secretbox(nonce||ct)
    WrappedDEKRecovery string     `json:"wrapped_dek_recovery,omitempty"` // base64 secretbox(nonce||ct)
    KDF                *KDFParams `json:"kdf,omitempty"`                  // password KEK params
    RecoveryKDF        *KDFParams `json:"recovery_kdf,omitempty"`         // recovery-phrase KEK params
    CreatedAt          string     `json:"created_at"`
}
```

We'll walk the thread from key material → sealing → delivery → at-rest → unlock.

---

## 1. The bootstrap window — messaging with no setup

### 1.1 Why epoch 0 exists

The first observation ("you could message before setting a password") is the bootstrap epoch. When your site is provisioned, the keyring starts with epoch 0: an X25519 keypair whose **private** key sits in `keyring.json` as plaintext (`server_dek`). Senders fetch your *public* messages key and seal to it; your server holds the matching private key, so it can open your inbound messages and show them to you with zero setup.

This is an honest trade, stated where the field is defined:

```go
ServerDEK string `json:"server_dek,omitempty"` // base64 X25519 private DEK, plaintext,
    // bootstrap epoch only — operator-readable by design; present during the forwarding
    // window, then cleared (see FORMAT.md)
```

The bootstrap epoch is convenience without a security claim: until you set a password, your server (and whoever operates it) *can* read your messages. The "Set a password to secure your messages" bar exists to move you off epoch 0.

### 1.2 The KEK derivation — [`cli-go/pkg/dm/keywrap.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/dm/keywrap.go)

When you do set a password, a KEK has to be derived from it. Two derivations exist, because there are two unlock paths — your password and your recovery phrase:

```go
// KDFParams records how a wrapping key (KEK) was derived, so an unwrap can
// reproduce it. Argon2id is used for the password KEK; HKDF-SHA256 for the
// high-entropy recovery-phrase KEK (no slow KDF needed there).
type KDFParams struct {
    Algo    string `json:"algo"` // "argon2id" | "hkdf-sha256"
    Salt    string `json:"salt"`
    Time    uint32 `json:"t,omitempty"`
    Memory  uint32 `json:"m,omitempty"`
    Threads uint8  `json:"p,omitempty"`
}
```

A human password is low-entropy, so it gets **Argon2id** (a deliberately slow, memory-hard KDF) at locked cost parameters. A recovery phrase is 128 bits of real entropy, so it gets cheap **HKDF-SHA256** — no brute-force surface to slow down.

---

## 2. Setting a password mints the epoch — in the browser

The crucial fact: setting a password does **not** send the password to the server. The browser does the whole upgrade and posts only the *results*.

### 2.1 Browser side: [`webapp/internal/webui/www/dm.js`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/webui/www/dm.js)

The trail marker at the top of `dm.js` states the invariant plainly:

```js
// dm.js — high-level browser DM operations, built on PolisDMCrypto (crypto primitives) +
// PolisDMSession (in-memory DEK cache). This is where the password-epoch flows live; the
// server never sees the password, KEK, DEK, or recovery phrase (see security-model.md).
```

The browser mirrors the Go Argon2id cost exactly so a keyring minted in the browser can be unwrapped by `polis dm decrypt` and vice-versa — the two implementations are one protocol:

```js
// Locked Argon2id cost — MUST match the Go defaults (cli-go/pkg/dm/keywrap.go +
// params_test.go: t=3, m=64MiB, p=4). Recorded per-epoch in keyring.json's kdf block.
```

`setPassword(password)` runs locally: generate a fresh X25519 DEK for the new epoch, derive the password-KEK (Argon2id) and recovery-KEK (HKDF over a freshly generated BIP39 phrase), wrap the DEK under both, and `POST /api/dm/password` with only `{public_key_messages, wrapped_dek, wrapped_dek_recovery, kdf, recovery_kdf}`. The plaintext password, both KEKs, the unwrapped DEK, and the recovery phrase never leave the tab. The phrase is shown to you exactly once.

### 2.2 Go side, same protocol: [`cli-go/pkg/dm/epoch.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/dm/epoch.go)

The Go package implements the identical operations so the CLI and a future second client interoperate: `SetPassword` mints epoch 1, `ChangePassword` re-wraps the DEK in O(1) (no message touched), and `UnlockEpochWithPassword` / `UnlockEpochWithPhrase` reproduce the KEK and unwrap. Because both `wrapped_dek` and `wrapped_dek_recovery` seal the *same* DEK, either secret recovers your whole history.

---

## 3. Sealing one message

### 3.1 The box — [`cli-go/pkg/dm/crypto.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/dm/crypto.go)

A message body is sealed with **NaCl box** (X25519 key agreement + XSalsa20-Poly1305 AEAD), from the sender's epoch DEK to the recipient's published epoch public key, under a random 24-byte nonce. Authenticated encryption means the recipient's instance can verify the ciphertext wasn't tampered with — but, lacking the recipient's private key, still can't read a password-epoch message.

### 3.2 The wire envelope — [`cli-go/pkg/dm/types.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/dm/types.go)

The sealed body is wrapped in a `MessageEnvelope` — cleartext routing metadata plus the encrypted payload:

```go
// MessageEnvelope is the wire format for DM delivery (cleartext metadata + encrypted payload).
type MessageEnvelope struct {
    Version          int    `json:"version"`
    SenderDomain     string `json:"sender_domain"`
    RecipientDomain  string `json:"recipient_domain"`
    EncryptedContent string `json:"encrypted_content"` // base64, NaCl box ciphertext
    Nonce            string `json:"nonce"`             // base64, 24 bytes
    Timestamp        string `json:"timestamp"`

    BoxPub         string `json:"box_pub,omitempty"`         // sender's messages pubkey for SenderEpoch — required to open the box
    SenderEpoch    int    `json:"sender_epoch,omitempty"`    // sender's epoch that BoxPub belongs to
    RecipientEpoch int    `json:"recipient_epoch,omitempty"` // recipient epoch the sender sealed to; the receiver stamps it as key_epoch (no server-side decrypt needed)
    ReplyTo        string `json:"reply_to,omitempty"`        // cleartext metadata, not in the box
}
```

Two fields earn their keep. `recipient_epoch` lets the receiver file the ciphertext under the right key generation *without ever opening it* — it just stamps the stored `key_epoch`. `box_pub` carries the sender's messages public key for the epoch they sealed from, which the receiver will **authenticate** (next section) rather than trust.

The version constant records that v2 is the hardened protocol:

```go
// v2 is the hardened protocol: box_pub is authenticated against the sender's published
// messages key at receipt (DM-1) and the signed-request canonical binds the recipient
// domain + body digest (DM-2).
const MessageEnvelopeVersion = 2
```

---

## 4. Point-to-point delivery — no broker

### 4.1 Sender: [`cli-go/pkg/dm/send.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/dm/send.go)

`Sender.SendMessage` fetches the recipient's published messages key from their `.well-known/polis` (verifying its signature), seals the body to it, builds the envelope, then **signs a canonical request** with the sender's *identity* key and POSTs it straight to the recipient's instance:

```
POST https://<recipient>/v1/content/dm/actions/deliver
X-Polis-Domain:    <sender>
X-Polis-Timestamp: <RFC3339>
X-Polis-Signature: <identity-key signature over {action, domain, recipient, timestamp, body_sha256}>

<envelope JSON>
```

There is no message broker in that line. The sender's server talks to the recipient's server. The sender also keeps a copy sealed to its *own* epoch key, so your "sent" messages decrypt the same way your received ones do — and your own server can't read them either.

### 4.2 Receiver: [`cli-go/pkg/dm/receive.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/dm/receive.go) + [`webapp/internal/api/router.go`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/api/router.go)

This answers "the recipient's server trusts it came from you." The `deliver` and `protection_status` actions are the only DM endpoints authenticated by **signed request** rather than an owner Bearer token — they're cross-tenant, so there's no shared session. The router recognizes them and verifies before dispatch:

```go
// DM signed-request routing: deliver + protection_status use
// X-Polis-Domain/Signature/Timestamp for cross-tenant authentication (not Bearer tokens).
```

`VerifySignedRequestForAction` rebuilds the canonical JSON using the receiver's **own** domain plus the body's SHA-256, fetches the *sender's* identity public key from the sender's `.well-known/polis`, verifies the signature, and rejects anything whose timestamp is outside a ±2-minute window. Only then does the handler:

1. Authenticate `box_pub` against the sender's published messages-key block (DM-1) — the sender can't claim a key they didn't publish.
2. Evaluate the inbound `pub.polis.dm` policy — does this recipient accept DMs from this sender?
3. Stamp `key_epoch = recipient_epoch` and append the **ciphertext** to the mailbox. For a password epoch there is no server-side decryption step, because there is no key to do it with.

---

## 5. At rest — stored, not readable

### 5.1 The mailbox — [`cli-go/pkg/dm/mailbox.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/dm/mailbox.go)

Messages live under the tenant DM dir as append-only JSONL, one conversation per peer:

```
.polis/bundles/pub.polis.core/dm/
  keyring.json                      # epochs (§ above)
  inbox.json                        # derived index (rebuildable)
  conversations/<peer>/
    messages.jsonl                  # append-only: one sealed message per line
    conversation.json               # thread metadata + read cursor
```

Each `messages.jsonl` line stores `ciphertext`, `nonce`, `box_pub`, and `key_epoch` — the wire box, not plaintext. The conversation ID is deterministic (`sha256` over the two sorted domains), so both sides compute the same thread identity without coordinating. For a password epoch, this file is exactly as opaque to your server as it is to an attacker who steals the disk.

---

## 6. Unlocking — back in the browser

### 6.1 The in-session DEK cache + decrypt — [`webapp/internal/webui/www/owner-extras.js`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/webui/www/owner-extras.js)

When you open Messages, the server hands the SPA the locked wire box for password-epoch messages — it cannot do otherwise. The trail marker says it directly:

```js
// Password-epoch messages arrive from the server as a locked placeholder
// plus the raw wire box (ciphertext / nonce / box_pub / key_epoch). The
// server can't read them — only the user's password can. When the epoch
// DEK is unlocked in-session (PolisDMSession), we open the box in the
// browser and swap in the plaintext; otherwise we surface an inline
// "enter your password" CTA over the messages view. The password, KEK,
// and DEK never leave the browser.
```

The unwrapped DEK is held only in memory (`PolisDMSession`) for the session; each message is opened with `PolisDMCrypto.openBox(ciphertext, nonce, box_pub, dek)`. Lock the tab, the DEK is gone, the messages are ciphertext again.

### 6.2 The protection-status gate — [`cli-go/pkg/dm/protection.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/dm/protection.go)

Before you message someone, the composer warns you if *they* haven't secured their messages (their inbound mail would land in a bootstrap epoch their operator can read). The browser can't make that query — it requires a mutuals-gated, signed cross-tenant call — so your **server** asks theirs (`/v1/content/dm/actions/protection_status`), gated so it leaks nothing to non-mutuals. The amber "be careful what you send" banner you see in the composer (see the [`dm-banner-doc` link in `owner-extras.js`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/webui/www/owner-extras.js)) is that result.

### 6.3 Offline read — `polis dm decrypt` ([`cli-go/pkg/cmd/dm.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/cmd/dm.go))

Because the at-rest format is the whole story, you can read an exported site with no server running:

```
decrypt [options]       Decrypt and print your messages from a local/exported site
  Bootstrap-epoch messages decrypt with no prompt (their key is in the export);
  Neither secret is ever stored or transmitted — decryption happens locally.
```

It loads `keyring.json`, prompts for your password (or `--phrase`), reproduces the KEK, unwraps the DEK, and opens each `messages.jsonl` line — the same crypto as the browser, proving the export is self-contained. (On a running localhost site, `polis serve` is read-only for DMs unless you pass `--dev`; the "Reading only — sending is disabled" banner is that distinction.)

---

## Where it all meets

```
   set password (browser)            send a message
   ──────────────────────            ──────────────────────────────
   derive KEK (Argon2id)             seal body: NaCl box (DEK → peer pub)
   wrap DEK → keyring.json           sign request (identity key)
   POST results only                 POST /v1/content/dm/actions/deliver
        │                                   │
        ▼                                   ▼  (recipient server)
   server stores wrapped DEK         verify signature + box_pub + policy
   (cannot unwrap)                   append CIPHERTEXT to messages.jsonl
                                            │
   read a message ◄─────────────────────────┘
   server hands you the locked box → browser unwraps DEK with your password → openBox → plaintext
```

The server is a **mailbox and a courier**, never a reader: it routes and stores sealed boxes, holds a DEK it can't unwrap, and is structurally unable to show plaintext for a password epoch — to you on someone else's behalf, or to anyone.

---

## Pull the thread

- **[`../general/security/dm-encryption.md`](../general/security/dm-encryption.md)** — the authoritative concept doc. Read [What this protects — and what it does not](../general/security/dm-encryption.md#what-this-protects) for the honest threat model (the bootstrap window, metadata, the served-JS trust assumption), [The bootstrap window](../general/security/dm-encryption.md#bootstrap-window), [Passwords and recovery](../general/security/dm-encryption.md#passwords-and-recovery), and [Browser cryptography (transparency disclosure)](../general/security/dm-encryption.md#browser-crypto).
- **[`cli-go/pkg/dm/FORMAT.md`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/dm/FORMAT.md)** — the exact at-rest format: keyring.json, messages.jsonl, and the manual decryption recipe.
- **[`cli-go/pkg/dm/PROTOCOL.md`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/dm/PROTOCOL.md)** — the wire protocol: key discovery, the message box, signed-request delivery, and the DM-numbered hardening requirements (DM-1 box_pub auth, DM-2 canonical binding, …).

## What you should now understand

If you followed the tour end-to-end:

- DM keys live in a **keyring of epochs**. Epoch 0 (bootstrap) is server-held convenience with no security claim; epochs 1+ (password) wrap a DEK under a password-derived KEK the server never sees.
- Setting a password is a **browser-only** operation — the server receives the wrapped DEK and public key, never the password, KEK, plaintext DEK, or recovery phrase. The Go and JS implementations are one protocol at locked Argon2id cost so an export round-trips.
- Delivery is **point-to-point** between two servers, authenticated by an **identity-key signed request** (not a session token), with `box_pub` authenticated against the sender's published key and an inbound policy gate.
- At rest, a password-epoch message is **stored ciphertext** — opaque to its own server. Reading happens in the browser (or `polis dm decrypt`) by unwrapping the DEK with your password or recovery phrase.
- The recipient-protection warning exists because the bootstrap window is real: your server asks theirs whether they've upgraded, mutuals-gated so it leaks nothing.

If you want to go deeper:

- The full thread map: [`../../AGENTS.md`](../../AGENTS.md)
- The at-rest format and offline decryption: [`cli-go/pkg/dm/FORMAT.md`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/dm/FORMAT.md)
- The wire protocol and hardening requirements: [`cli-go/pkg/dm/PROTOCOL.md`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/dm/PROTOCOL.md)
