# Polis DM at-rest format (`pub.polis.core` direct messages)

This documents the on-disk format and crypto primitives for encrypted direct
messages, so the data can be decrypted by an independent tool — the polis CLI
(`polis dm decrypt`), the browser WASM, a reference decryptor, or code you write
against this spec. It is the durability/time-capsule contract: anything that can
read this spec + holds the password (or recovery phrase) can recover the messages,
with no running polis instance.

The authoritative Go implementation lives alongside this file in
`cli-go/pkg/dm/` (`keyring.go`, `keywrap.go`, `recovery.go`, `epoch.go`,
`rekey.go`, `mailbox.go`). Where this doc and the code disagree, the code wins —
file a fix.

All binary fields are **base64 (standard encoding, with padding)** unless noted.
Timestamps are **RFC 3339 in UTC**.

## Reading your messages from an export

You do not need to understand this spec to read your own messages. The export (`.polis`
site zip) carries everything required: the keyring (both wraps + KDF params), the per-message
`key_epoch` tags, and the message ciphertext.

1. **In the web UI (recommended).** Unzip the export, run `polis serve` inside it, open the
   printed localhost URL, and enter your password in **Messages** — the same in-browser unlock
   you use hosted, fully offline. Messages are read-only here (see the `polis serve` section of
   `docs/cli/user/command-reference.md`).
2. **On the command line.** `polis dm decrypt` (point it at the unzipped export) prints the
   plaintext; `--phrase` unlocks with the recovery phrase instead of the password.
3. **Write your own.** Everything below is enough to decrypt independently with the password or
   the recovery phrase, no running polis instance and no polis code — the durability backstop.

Messages sealed under the **bootstrap epoch** (received before a password was set) need no
secret at all: the bootstrap DEK travels in the keyring (`server_dek`) and opens them directly.

## Primitives

| Purpose | Primitive |
|---------|-----------|
| Password KEK derivation | **Argon2id** (`golang.org/x/crypto/argon2` `IDKey`), 32-byte output |
| Recovery-phrase KEK derivation | **HKDF-SHA256** (`golang.org/x/crypto/hkdf`), info `"polis-dm-recovery-kek-v1"`, 32-byte output |
| DEK wrap (key-wrapping) | **NaCl secretbox** (XSalsa20-Poly1305), 24-byte random nonce |
| Message encryption | **NaCl box** (X25519 + XSalsa20-Poly1305), 24-byte random nonce |
| Public key from a DEK | **X25519** (`curve25519.X25519(dek, basepoint)`) |
| Recovery phrase | **BIP39**, 12 words / 128-bit entropy (upgradeable to 24 / 256-bit) |

A **DEK** (Data Encryption Key) is an X25519 private scalar (32 bytes) — the actual
message key for one epoch. A **KEK** (Key Encryption Key) is a 32-byte symmetric key
derived from the password or recovery phrase; it only ever wraps the DEK.

## `keyring.json`

At `.../dm/keyring.json`. Holds the per-user key material as an ordered list of
**epochs**. The password and recovery phrase themselves are never stored.

```json
{
  "schema_version": 1,
  "revision": 7,
  "generator": "polis-cli-go/X.Y.Z",
  "epochs": [
    { "id": 0, "kind": "bootstrap", "public_key_messages": "<b64 x25519 pub>",
      "server_held": true, "server_dek": "<b64 x25519 priv>", "created_at": "<rfc3339>" },
    { "id": 1, "kind": "password", "public_key_messages": "<b64 x25519 pub>",
      "server_held": false, "created_at": "<rfc3339>",
      "wrapped_dek":          "<b64 secretbox blob>",
      "wrapped_dek_recovery": "<b64 secretbox blob>",
      "kdf":          { "algo": "argon2id", "salt": "<b64>", "t": 3, "m": 65536, "p": 4 },
      "recovery_kdf": { "algo": "hkdf-sha256", "salt": "<b64>" } }
  ],
  "current": 1
}
```

- **`revision`** — monotonic; every mutation bumps it. Writers use compare-and-swap
  (write only if the on-disk revision is unchanged) so concurrent multi-device writes
  never silently drop a wrap.
- **`kdf.m`** is Argon2id memory in **KiB**; `t` iterations; `p` parallelism. These
  are recorded per-epoch; a decryptor MUST use the recorded params, not its own
  defaults, or the KEK won't match.
- **Bootstrap epoch (id 0):** server-held key, no password — `wrapped_dek` /
  `wrapped_dek_recovery` / `kdf` / `recovery_kdf` are absent. Its private DEK is stored
  **plaintext** in `server_dek` (base64 X25519 private scalar). **This is operator-readable
  by design** — it is the honest cost of the frictionless default: DMs work from message #1
  with no password, and bootstrap-era messages are readable by whoever holds the disk. The
  server needs it to seal its own sent copy and to forward stale-addressed mail. Setting a
  password mints a password epoch and (past a short forwarding window) `server_dek` is
  cleared, after which no operator-readable key remains. An export includes `server_dek`
  while present, so a bootstrap-only account is still recoverable offline.
- **Password epoch (id ≥ 1):** the DEK is stored **twice** — wrapped under the
  password KEK (`wrapped_dek` + `kdf`) and under the recovery KEK
  (`wrapped_dek_recovery` + `recovery_kdf`). Either unwraps the same DEK.

### Wrapped-blob format & unwrapping a DEK

A `wrapped_dek` / `wrapped_dek_recovery` is `base64( nonce[24] ‖ secretbox_ciphertext )`.

To recover a DEK:

1. Derive the KEK:
   - password: `KEK = Argon2id(password, b64decode(kdf.salt), kdf.t, kdf.m, kdf.p, 32)`
   - recovery: `KEK = HKDF-SHA256(entropy, b64decode(recovery_kdf.salt), "polis-dm-recovery-kek-v1")[:32]`,
     where `entropy = BIP39_decode(recovery_phrase)` (the BIP39 checksum validates the phrase first).
2. `raw = b64decode(wrapped)`, `nonce = raw[:24]`, `ct = raw[24:]`.
3. `DEK = secretbox_open(ct, nonce, KEK)`. **A failed Poly1305 tag means the wrong
   password/phrase** — there is no separate verifier.

## Message storage

```
dm/
  inbox.json                         (derived index; rebuildable — not a source of truth)
  conversations/<peer-handle>/       (peer handle, lowercased)
    messages.jsonl                   (append-only; one JSON object per line)
    conversation.json                (metadata + read cursor)
```

### `messages.jsonl` line

```json
{ "id": "<hex16>", "key_epoch": 1, "dir": "in|out",
  "from": "<domain|me>", "to": "<domain|me>", "at": "<rfc3339>",
  "nonce": "<b64 24 bytes>", "ciphertext": "<b64 NaCl box>",
  "box_pub": "<b64 x25519>", "reply_to": "<id?>", "status": "<sent|received>" }
```

- No plaintext, no preview is ever stored.
- **`box_pub`** is the X25519 *counterparty* public key needed to open the box with
  this epoch's DEK:
  - `dir: "in"` (received) — the **sender's** messages public key; the ciphertext is the
    sender's wire box, stored as received.
  - `dir: "out"` (sent) — **your own** epoch public key; the message is sealed to
    yourself so you can re-read your side (the operator cannot).
- **Decrypt a message:** `plaintext = box_open(b64decode(ciphertext),
  b64decode(nonce), b64decode(box_pub), DEK_for(key_epoch))`. `id = hex(sha256(nonce))[:16]`.

### `conversation.json`

```json
{ "peer": "<handle>", "conversation_id": "<hex>", "created_at": "<rfc3339>",
  "last_message_at": "<rfc3339>", "read_at": "<rfc3339>", "message_count": 14 }
```

`conversation_id = hex(sha256(sorted(lower(domainA), lower(domainB)) joined by "\n"))`.
Unread = incoming messages with `at > read_at`.

## Decryption recipe (independent decryptor)

```
load keyring.json
password  := prompt()            # or recovery phrase
for each password epoch e:
    KEK  := Argon2id(password, e.kdf.salt, e.kdf.{t,m,p}, 32)     # or HKDF(BIP39(phrase), e.recovery_kdf.salt)
    DEK  := secretbox_open(unb64(e.wrapped_dek)[24:], unb64(e.wrapped_dek)[:24], KEK)   # wrong pw => stop
    dek_by_epoch[e.id] := DEK
# epoch 0 (bootstrap): DEK is the server-held key shipped in the export
for each conversations/<peer>/messages.jsonl line msg:
    DEK  := dek_by_epoch[msg.key_epoch]      # absent => "locked", skip
    text := box_open(unb64(msg.ciphertext), unb64(msg.nonce), unb64(msg.box_pub), DEK)
    # box_open fail with the DEK present => "undecryptable" (corrupt tag); skip that one
    # line and continue — a single bad box must not abort the whole conversation read.
```

Ubiquitous deps: a libsodium/NaCl binding (PyNaCl, libsodium-wrappers, sodiumoxide),
an Argon2 lib, and a BIP39 lib — all have multiple independent implementations.
