# Direct Message Encryption

This is the authoritative treatment of direct-message (DM) confidentiality in polis: what it
protects, what it does **not**, and exactly how it works. It is written to be checked — every
claim is scoped, and every limit is named. If a sentence here reads as a softer promise than
the mechanism can keep, it is a bug; file it.

The companion specs are `cli-go/pkg/dm/FORMAT.md` (the at-rest crypto format, independently
implementable) and `cli-go/pkg/dm/PROTOCOL.md` (the wire/delivery protocol). Where this doc
and the code disagree, the code wins. For a guided walk through the actual source — how a
message gets encrypted, delivered, stored, and unlocked — see the tour at
[`../../handbook/dm-encryption.md`](../../handbook/dm-encryption.md).

---

## In plain language

If you only read one section, read this one.

- **Your messages are sealed with a key made from a password we never receive.** When you set
  a message password, the key that unlocks your messages is derived from that password **in
  your browser**. The server never sees the password, the key, or your decrypted messages —
  so there is nothing on our side to read your message *content* with, and nothing to hand
  over if someone demands it.
- **There is no reset, by design.** Because we never hold your password, we cannot reset it.
  If you forget it, the **recovery phrase** (12 words, shown once when you set the password) is
  the only spare key. **Lose both the password and the recovery phrase and the messages are
  gone for good** — even to you. That is the honest cost of us not being able to read them.
- **Until you set a password, we can read your messages.** New accounts start with zero-setup
  messaging: messages you receive before you set a password are sealed with a key the server
  holds, so they are operator-readable. This is the **bootstrap window**. The guarantee is
  "private from the moment you set a password," not "private always." You can close the window
  to zero by setting a password proactively, before any message arrives.
- **We see *that* you message, not *what*.** "We can't read your DMs" is about **content**.
  The operators of your space and your correspondent's space still see who messages whom,
  when, and how often. That is metadata, and a federated network exposes peer domains anyway.
- **Keep your own copies.** Export your site (it includes your messages as ciphertext) and you
  can read them offline forever with your password — see [Reading your messages
  offline](#local-decryption). We never offer a plaintext download; decryption happens only on
  your machine.

The rest of this document is the precise version of the above. Terms in **bold** on first use
are defined in the [Glossary](#glossary).

---

## What this protects — and what it does not {#what-this-protects}

Claims are scoped honestly here so they survive review.

### Protected

Once you have set a password, for every message sealed after the [bootstrap
window](#bootstrap-window), confidentiality of **content** is reduced to **offline brute-force
of the wrapped key blob — i.e. as strong as your password plus the Argon2id cost**. That holds
against:

- A stolen volume snapshot, backup, or decommissioned disk.
- A remote-code-execution breach that grabs data at rest.
- A subpoena served on stored data.
- An honest operator who is later breached.
- **The operator's own ability to read your messages**, at rest or in transit, for any epoch
  past the bootstrap window.

In all of these the server never holds your password or the unwrapped key, so every party —
**including the operator** — is demoted from instant reader to offline brute-forcer. A strong
password plus Argon2id makes that infeasible.

### NOT protected

- **An actively malicious operator serving tampered code.** The server ships the JavaScript
  that handles your password; an operator who is *currently* evil can serve poisoned JS that
  captures the password as you type it. It cannot retroactively read messages sealed before it
  turned evil without you logging in again under the poisoned code, but this is the
  irreducible weakness of all browser-delivered end-to-end encryption. Mitigations: a strict
  Content-Security-Policy on the admin app, and the **offline read path** (export + local
  decryption — code you control, not the operator's served JS) as the high-assurance escape.
  We document this; we do not hide it.
- **Metadata.** The two endpoint operators — the hosts of the sender's and the recipient's
  spaces — still see who messages whom, when, frequency, and message sizes. "We can't read
  your DMs" means **content**. Because delivery is [point-to-point](#delivery), no discovery
  service or other central polis entity is in the path to learn even the metadata; the
  exposure is to the endpoint operators only.
- **The bootstrap window.** Messages received before you set a password are sealed to a
  server-held key and are operator-readable. See [The bootstrap window](#bootstrap-window).
- **Weak passwords.** The entire guarantee is "as strong as your password." A weak password,
  against an exfiltrated blob, falls to offline cracking. Minimum-strength prompting and the
  high-entropy recovery phrase are load-bearing, not decorative.
- **Intra-epoch forward secrecy.** All messages in one [epoch](#glossary) share a single key;
  compromising that key exposes the whole epoch. This is an accepted trade for a simple
  mailbox model. Rotating to a new epoch bounds the exposure going forward.

---

## How it works {#how-it-works}

### A separate key for messages

DMs do **not** reuse your Ed25519 identity (signing) key. Each space has an independent
X25519 **messages key** per [epoch](#glossary), published in `.well-known/polis` as a
`public_key_messages` block and **signed by the identity key** so a sender can verify they are
sealing to the real recipient and not an operator-substituted key. The identity key signs the
messages key; it never decrypts messages.

### Two-tier keys: DEK and KEK

- A **DEK** (Data Encryption Key) is the actual message key for one epoch — an X25519 private
  scalar. Messages are sealed to/from the DEK with NaCl `box`.
- A **KEK** (Key Encryption Key) is a 32-byte symmetric key derived **from your password**
  (Argon2id) or **from your recovery phrase** (HKDF-SHA256). The KEK never leaves your browser.
- The DEK is stored only **wrapped** — sealed under the KEK with NaCl `secretbox`. The server
  holds the wrapped blob; it is useless without your password or phrase.

To read messages, your browser derives the KEK from your password, unwraps the DEK, and opens
each message. The server only ever sees wrapped blobs and ciphertext.

### The epoch keyring

Your key material is an ordered list of **epochs** in `keyring.json`:

- **Epoch 0 — bootstrap.** Created at `polis init`. Its DEK (`server_dek`) is stored in
  plaintext in the keyring so the server can read bootstrap-era messages with no password —
  this is what makes zero-setup messaging work, and the honest cost of it. See below.
- **Epoch 1+ — password epochs.** Created when you set a password. The DEK is stored wrapped
  twice: once under the password KEK (`wrapped_dek`) and once under the recovery-phrase KEK
  (`wrapped_dek_recovery`). Either secret unwraps the same DEK.

Each message carries a `key_epoch` tag, so the reader knows which epoch's DEK it needs.

---

## The bootstrap window {#bootstrap-window}

New accounts can send and receive messages immediately, with no setup — because epoch 0's DEK
is **held by the server in readable form** (the `server_dek` entry in your `keyring.json`,
plaintext on the operator's disk). Anyone who holds that disk — the operator — can read
bootstrap-epoch messages.

This is a deliberate trade for zero-friction onboarding, and it is disclosed, not hidden:

- The guarantee is **"private from the moment you set a password,"** never "private always."
- **Setting a password closes the window.** From then on, new messages are sealed to a
  password epoch the server cannot read. The bootstrap `server_dek` is retained only through a
  short forwarding grace period (so in-flight messages still deliver), then cleared.
- **You can have a zero-exposure window** by setting a password *proactively*, before any
  message arrives (Settings → Messages).
- **Senders are warned** before messaging a recipient who has not set a password, so the
  exposure is informed on both sides rather than silent.

---

## Passwords and recovery {#passwords-and-recovery}

### Setting a password — the upgrade {#change-password}

Setting a password mints a new password epoch in your browser, wraps a fresh DEK under the
password KEK and the recovery KEK, and uploads only the wrapped blobs, the KDF parameters, and
the new epoch's public key. The server re-signs and republishes the `public_key_messages`
block. It never receives the password, the KEK, the DEK, or the recovery phrase.

**Changing your password** is an O(1) re-wrap: your browser unwraps the current DEK with the
old password and re-wraps it under the new one over a fresh salt. No new epoch, no message
re-encryption, no change to who can read what — instant regardless of how many messages you
have. The old password stops working immediately.

**Password validation is the AEAD tag.** Deriving the KEK from a wrong password and trying to
unwrap fails the authentication tag. "Did the unwrap succeed?" *is* "was the password
correct?" — so there is no server-side password oracle and no server-side rate-limit to build.
The only attack surface is offline brute-force against an exfiltrated blob.

We deliberately offer **no password hint** of any kind. A hint stored anywhere we can read it
would weaken the offline-attacker guarantee; a hint we cannot read is just a second secret.

### The recovery phrase {#recovery-phrase}

When you set a password you are shown a **12-word BIP39 recovery phrase, once**. It derives an
independent KEK (HKDF-SHA256 over the phrase's entropy) under which the **same DEK** is wrapped
a second time. So:

- Either the password **or** the recovery phrase unlocks your messages.
- The phrase is the **only** recovery path. We hold neither secret, so we cannot reset either.
- **Lose both and the messages are permanently lost** — to you and to us alike. Store the
  phrase offline, outside polis. Anyone who has it can read all your messages.

You can **regenerate** the recovery phrase (retiring the old one) from Settings → Messages;
this re-wraps the current DEK under a fresh phrase and leaves the password untouched.

---

## The lifecycle of one message {#message-lifecycle}

1. **You write it.** In your browser, the plaintext is sealed with NaCl `box` to the
   recipient's verified messages key, from your current epoch's DEK.
2. **It is delivered point-to-point.** Your space POSTs the sealed envelope straight to the
   recipient's space, authenticated instance-to-instance. No central entity relays it.
3. **It is stored as ciphertext** on the recipient's side — the wire `box`, exactly as
   received. The recipient's server has no key to open a password-epoch message.
4. **It sits locked** until the recipient opens it: their browser derives their KEK from their
   password, unwraps their epoch DEK, and opens the box. (A bootstrap-epoch message opens
   server-side, because the server holds that DEK.)
5. **The sender keeps a copy**, sealed to the sender's own epoch key, so you can re-read your
   own side later — and the operator cannot.

### Operations and who can read

| Operation | Where it happens | Server sees |
|-----------|------------------|-------------|
| Set / change password | Browser | Wrapped blobs + KDF params only |
| Derive key from password / phrase | Browser | Nothing |
| Seal a password-epoch message | Browser | Ciphertext only |
| Seal a bootstrap-epoch message | Server | Plaintext (bootstrap is server-readable) |
| Store a received message | Server | Ciphertext only (password epoch) |
| Open / read a password-epoch message | Browser | Nothing |
| Verify a recipient's messages key | Server | Public key only (signed, verified) |

---

## Point-to-point delivery {#delivery}

A DM travels **directly** from the sender's polis instance to the recipient's instance —
`POST /v1/content/dm/actions/deliver`, authenticated with a signed instance-to-instance
header. The discovery service is **not** in the message path. This is why the metadata
exposure is to the two endpoint operators only: no central polis entity ever sees that a DM
was sent, let alone its contents. Acceptance is gated by the recipient's Layer-1 policy
(`pub.polis.dm`), which by default restricts DMs to followed (mutual) domains.

---

## The wire→at-rest handoff {#wire-at-rest-handoff}

The most security-sensitive moment is the **handoff** from server-to-server transport to the
recipient's at-rest store. A delivered message must land sealed, never written in plaintext.
The two encryption layers are distinct:

- **Transport (outer):** TLS between the sender's and recipient's instances. It terminates at
  the recipient operator's edge — and the **payload underneath stays `box`-sealed** to the
  recipient's epoch messages key. TLS protects the hop; it is not what protects the content.
- **At-rest (inner):** the `box` ciphertext itself. The recipient's server takes the received
  ciphertext and stores it **as-is** — same bytes, tagged with the `recipient_epoch` the sender
  sealed to (the stored `key_epoch`), under 0700 permissions. It does **not** decrypt, does
  **not** re-encrypt, and never writes an intermediate plaintext to disk or logs message
  bodies. For a **password epoch** the server *cannot* open it (no DEK); for the **bootstrap
  epoch** it *can* (it holds `server_dek`) — that is the window, stated exactly.

Two server-side re-seal paths touch ciphertext + public keys only, never a password-epoch DEK:

- **Close-the-window re-seal (set-password).** When you set a password, the server — which
  still holds the bootstrap DEK at that moment — opens each prior bootstrap-epoch message and
  re-seals it forward to your new epoch's public key under a fresh ephemeral key, then **clears
  `server_dek`**. After this, those messages are readable only under your password, and the
  operator holds no readable DM key. (Implemented in `Mailbox.ReencryptBootstrapForward` +
  `ClearBootstrapServerDEK`.)
- **Stale-epoch rejection (delivery).** A delivery must seal to your **current** epoch. A
  message sealed to a superseded epoch (e.g. a sender with a stale cached key after you set a
  password) is **rejected for refetch + retry**, not stored as ciphertext nobody can open — so
  clearing `server_dek` never strands an in-flight message.

## What we see: content vs. metadata {#metadata}

"We cannot read your messages" is a claim about **content**, and only content. The endpoint
operators still observe:

- **Who** you message (peer domains — visible in any federated system).
- **When**, **how often**, and roughly **how large** the messages are.

We do not, and cannot, claim to hide that. Scoping the claim to content is the honest framing.

---

## Browser cryptography (transparency disclosure) {#browser-crypto}

For password epochs, **all key derivation and message decryption happen client-side in your
browser. The server never receives the password, the KEK, or a password-epoch DEK.** This is a
strength, and we disclose its mechanics rather than asking you to take it on faith:

- The browser ships a small, pinned crypto stack in the password path: **Argon2id**
  (`hash-wasm`, WASM), **NaCl box + secretbox** (`tweetnacl-js`), **HKDF-SHA256** (the
  browser's native WebCrypto), and **BIP39** (`@scure/bip39` + `@noble/hashes`). Exact pinned
  versions and provenance are recorded in `webapp/internal/webui/www/vendor/README.md`, and a
  conformance gate (`vendor/conformance.mjs`) proves these produce byte-identical output to the
  Go reference implementation — so a blob wrapped in one unwraps in the other.
- The Argon2id parameters match the Go side exactly (t=3, m=64 MiB, p=4), recorded per epoch in
  `keyring.json`.
- **One CSP relaxation:** the admin app's `script-src` includes `'wasm-unsafe-eval'`, required
  for WebAssembly *compilation* only. It does **not** permit JavaScript `eval()`/`new
  Function()` and does not widen the script-injection surface, which stays gated by
  `'self'` + per-response nonce.

This disclosure is technical and lives here and in `security-model.md`; it changes no
user-facing guarantee (unlike the bootstrap-window `server_dek` disclosure, which does and is
therefore also stated in plain language above and in the Terms of Service).

---

## Reading your messages offline {#local-decryption}

### No plaintext export — ever

Polis never offers a "download my cleartext messages" feature, not even one gated behind your
password. Every downloadable artifact is **ciphertext**. Decryption happens only on your own
machine, under your control. This is a deliberate posture, not a missing feature.

### How to read an export

Your site export (`.polis` zip) carries everything needed to read your messages with no running
hosted account: the keyring (both wraps + KDF params), the per-message `key_epoch` tags, and
the message ciphertext. Neither your password nor your recovery phrase is exported — you supply
one, exactly as hosted.

1. **In the web UI (recommended, non-technical).** Unzip the export, run `polis serve` inside
   it, open the localhost URL, and enter your password in **Messages** — the same in-browser
   unlock you use hosted, fully offline. Messages are read-only in this mode (see the `polis
   serve` section of `docs/cli/user/command-reference.md`).
2. **On the command line.** `polis dm decrypt` prints the plaintext (`--phrase` to use the
   recovery phrase instead). The secret is read without echo, used only for the local unwrap,
   and never stored or transmitted.
3. **Write your own.** `cli-go/pkg/dm/FORMAT.md` documents the format completely — the
   write-your-own durability backstop, so your messages outlive polis itself.

Messages from the bootstrap epoch need no secret at all: their DEK (`server_dek`) travels in the
keyring and opens them directly.

A future option is an [`age`](https://age-encryption.org)-compatible archive — decryptable with
the standard, multiply-implemented `age` tool — for the strongest tool-independent durability.
It is tracked but not yet shipped.

---

## What we can and cannot verify {#detection-limits}

Honesty about detection matters as much as honesty about the crypto:

- **Your own instance** can verify that *it* never stored a readable password-epoch key, and
  background actors check that the keyring and published messages-key signatures are intact.
- **At send time**, a sender's instance verifies the recipient's signed `public_key_messages`
  before sealing, and can query the recipient's signed `protection_status` (mutuals-gated) to
  warn you that a recipient has not secured their messages.
- **What cannot be remotely attested:** that a *different* operator's instance, running
  modified code, handles your correspondent's at-rest data honestly. A self-hoster running a
  modified polis is outside any guarantee we can enforce — federation means you trust each
  endpoint's operator for that endpoint's metadata and bootstrap-era content. The offline read
  path is the escape hatch for your *own* data; for a correspondent's instance, the protection
  is the protocol (you seal to their verified key) plus your judgment about whom you message.

---

## Glossary {#glossary}

- **DEK (Data Encryption Key)** — the per-epoch message key (an X25519 private scalar).
  Messages are sealed to/from it. Stored only wrapped.
- **KEK (Key Encryption Key)** — a 32-byte symmetric key derived from your password (Argon2id)
  or recovery phrase (HKDF). Wraps the DEK. Never leaves your browser.
- **Epoch** — one generation of message key material. Epoch 0 is the server-readable bootstrap
  epoch; epochs 1+ are password epochs.
- **Bootstrap window** — the period before you set a password, during which your messages are
  sealed to a server-held key and are operator-readable.
- **Wrapped blob** — a DEK sealed under a KEK (NaCl secretbox). Safe to store; useless without
  the password or phrase.
- **Recovery phrase** — a 12-word BIP39 phrase that derives an independent KEK wrapping the same
  DEK. The only recovery path; we cannot reset it.
- **Re-wrap** — changing the password (or regenerating the recovery phrase) by re-sealing the
  *same* DEK under a new KEK. O(1); no message re-encryption.
- **Rotate / rekey** — minting a *new* epoch with a fresh DEK and public key (a forward-secrecy
  boundary). Old messages stay sealed under the old epoch.
- **`server_dek`** — the bootstrap epoch's DEK, stored in plaintext in `keyring.json` so the
  server can read bootstrap-era messages. Cleared once you set a password (past the forwarding
  window).
- **`public_key_messages`** — your space's per-epoch X25519 messages public key, published in
  `.well-known/polis` and signed by your identity key.

See also the repository glossary at `docs/general/reference/glossary.md`.

---

## See also

- `docs/general/security/security-model.md` — the system-wide security model (DM overview links here).
- `cli-go/pkg/dm/FORMAT.md` — the at-rest crypto/keyring format (independently implementable).
- `cli-go/pkg/dm/PROTOCOL.md` — the wire/delivery protocol.
- `docs/cli/user/command-reference.md` — `polis dm decrypt`, `polis serve` (offline read).
- `docs/general/reference/policy-grammar.md` — the `pub.polis.dm` acceptance policy (Layer 1).
