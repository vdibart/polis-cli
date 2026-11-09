# Stand up an identity, and read it two ways

*Recipe 17 · [the recipe book](README.md)*

*For* [Developers](../../README.md#building-on-polis) · [Reviewers](../../README.md#reviewing-the-security-and-identity-design) — *Kind* [Recipe](../../README.md#kinds-of-page) — *Component* [CLI](../../cli/README.md) — *See also* [concept](../concepts/identity.md) · [spec](../spec/did-web.md) · [spec](../spec/key-history.md)

Every command on this page is run by the test suite.

**The problem.** Before anything can be signed, something has to *be* the signer. On most
systems that means an account somewhere, issued by someone, revocable by them. Here it is
a domain and a keypair, and this page shows exactly what exists after you make one — read
twice, in two vocabularies, so you can see there is no third thing hiding behind them.

**What it runs.** `polis init`, and then two `curl`s. That is the whole of it, which is
the point.

## Make one

```bash
mkdir alice.example && cd alice.example
export POLIS_BASE_URL=https://alice.example
polis init --site-title "Alice" | grep '^\[i\] Public key:'
SITE_DIR=$PWD
```

```text output
[i] Public key: ssh-ed25519 AAAAC3NzaC1lZDI1NTE5…
```

⛔ **Nobody was asked.** No registration, no issuer, no account. `polis init` generated a
keypair and wrote two documents describing it. Anyone who can already resolve
`alice.example` can already check them, because the only thing binding the key to the name
is that the name serves it — over DNS and the web PKI the reader was relying on anyway.

## 1 · The polis-native reading

```bash
cd "$(mktemp -d)"
curl -s https://alice.example/.well-known/polis \
  | jq -c '{public_key: (.public_key | split(" ") | .[0]), history: .public_key_history}'
```

```json output
{"public_key": "ssh-ed25519", "history": {"current": {"epoch": 0, "key": "…", "transition_sig": null, "valid_from": "…"}, "history": []}}
```

Three things in that chain are worth naming.

**`epoch: 0` is genesis.** This key is the first this site has held. Nothing precedes it.

**`transition_sig: null` is a claim, not a gap.** Every later key carries a signature made
by its *predecessor*, handing over. A first key has no predecessor to sign for it, so the
field is present and explicitly `null`. ⛔ **An absent field and an explicit null are
different statements** — absent would mean "this document does not say", and null means
"there was nothing to sign".

**`history: []` is an empty list, not a missing one**, for the same reason. The site is
asserting that it has never rotated, rather than declining to answer.

⭐ **Together they are what makes [recipe 8](08-survive-a-key-rotation.md) legible.** After
a rotation this chain has entries, each signed by the key it replaced, and a reader can
walk back to this genesis entry and resolve any old signature to the key that made it —
**with no service to ask.**

## 2 · The `did:web` reading

The same key, expressed as a conformant DID document:

```bash
curl -s https://alice.example/.well-known/did.json \
  | jq -c '{id, vm: [.verificationMethod[].id], assertionMethod, authentication}'
```

```json output
{"id": "did:web:alice.example", "vm": ["did:web:alice.example#key-1"], "assertionMethod": ["did:web:alice.example#key-1"], "authentication": ["did:web:alice.example#key-1"]}
```

⚠️ **`#key-1`, from `epoch: 0`.** Fragments are the epoch plus one, so genesis is `key-1`
rather than `key-0`. A never-rotated site publishes a byte-identical document every time it
is generated.

## 3 · Same key, and you can check that by hand

The two documents look nothing alike — one is an OpenSSH public key, the other a JWK. They
are the same 32 bytes. An Ed25519 SSH key is a length-prefixed blob whose last 32 bytes are
the key itself, and a JWK carries those bytes base64url-encoded:

```bash
curl -s https://alice.example/.well-known/polis | jq -r .public_key \
  | awk '{print $2}' | base64 -d | tail -c 32 | base64 | tr '+/' '-_' | tr -d '=' > derived
curl -s https://alice.example/.well-known/did.json \
  | jq -r '.verificationMethod[0].publicKeyJwk.x' > published
diff derived published && echo "same key"
```

```text output
same key
```

⭐ **`did.json` is computed from the polis document, never authored beside it.** That is the
direction, and it matters: there is one source of truth, and the DID document is a
projection of it for readers who speak that vocabulary. Nothing can drift, because there is
nothing to keep in sync.

⛔ **A retired key does not disappear from `did.json`** — it stays in `verificationMethod`,
so old signatures still resolve, and drops out of `assertionMethod` and `authentication`,
so it can no longer be used to claim anything new. Also covered in
[recipe 8](08-survive-a-key-rotation.md).

## What you have — and what it costs

| | |
|---|---|
| ✅ An identity that works immediately | no issuer, no account, no approval, no waiting |
| ✅ Readable two ways from one source | polis-native and `did:web`, the second computed from the first |
| ✅ Checkable by anyone, with no service | two HTTPS fetches; the trust comes from DNS and the web PKI |
| ✅ Nobody can revoke it | there is no issuer, so there is nothing to revoke |
| ⛔ **The identifier IS the domain** | lose the domain and you lose the name. Renewal lapses, a registrar dispute, a seized name — the identity goes with it |
| ⛔ **Nobody can recover it for you** | the same property. There is no support queue, because there is no account |
| ⛔ **It says nothing about who you are** | it binds a key to a name, and nothing further. Reputation is [somebody else's claim about you](01-make-a-claim.md) |

⚠️ **That first cost is the real trade and it should not be soft-pedalled.** Every system
that can restore your identity when you lose the key can also take it away, and every
system that cannot do the first cannot do the second. polis chose the second. If the domain
matters, treat its renewal like the private key: back it up, and do not let it lapse.

## Next

- [Publish a post, and have a stranger prove it is yours](15-publish-and-be-proven.md) —
  what the key is for
- [Keep old posts verifying across a key rotation](08-survive-a-key-rotation.md) — what
  this chain does once there is more than one entry
- [Prove two domains are the same party](06-prove-same-party.md) — a different question:
  two identities, one author
