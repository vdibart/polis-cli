# The polis `did:web` profile

*For* [Reviewers](../../README.md#reviewing-the-security-and-identity-design) · [Developers](../../README.md#building-on-polis) — *About* [Identity](../../general/README.md#identity) — *Kind* [Spec](../../README.md#kinds-of-page) — *Code* [`cli-go/pkg/did`](../../../cli-go/pkg/did) — *See also* [concept](../concepts/projection.md) · [recipe](../recipes/08-survive-a-key-rotation.md)

**Method:** [`did:web`](https://w3c-ccg.github.io/did-method-web/) · **Document:** `https://<host>/.well-known/did.json`

Every polis site publishes its identity key twice: once as an OpenSSH public-key line in
`.well-known/polis`, and once as a W3C DID Document at `.well-known/did.json`. **One key, two
shapes.** This page is the profile — exactly what polis emits, and what a resolver can rely on.

---

## 1. Why there is anything to specify

Polis identity was, from the beginning: *a keypair bound to a domain, with the public key published at
a well-known location, and trust anchored in DNS plus TLS.* That is `did:web`, arrived at
independently and written in a different encoding.

So this is not an adoption of somebody else's model. It is the **same model, said in the standard
vocabulary**, which means the claim *"polis interoperates with the identity stack rather than
reinventing it"* is structurally true rather than rhetorical. The gap was encoding and one file.

| Concern | `did:web` | polis | after this profile |
|---|---|---|---|
| Identifier | the domain | the domain | identical |
| Key type | Ed25519 (among others) | Ed25519 | identical |
| Key publication | `did.json` at a well-known URL | `public_key` in `.well-known/polis` | **both** |
| Key encoding | JWK / multibase | OpenSSH one-line | **both** |
| Trust anchor | DNS + TLS/CA | DNS + TLS/CA | identical |

**Nothing about polis signing changed.** Content is still signed with the SSH signature format
(`ssh-keygen -Y`). Being *resolvable* as a DID and being *verifiable inside VC tooling* are separate
capabilities; this profile delivers the first only. See [§8](#8-what-this-does-not-do).

## 2. The DID string

```
did:web:<host>
```

`<host>` is the site's canonical host, lowercased, with no scheme, port, or path.

| Deployment | DID | Resolves at |
|---|---|---|
| Self-hosted at `alice.com` | `did:web:alice.com` | `https://alice.com/.well-known/did.json` |
| Hosted tenant | `did:web:alice.polis.pub` | `https://alice.polis.pub/.well-known/did.json` |

**Hosted tenants use the subdomain form, never `did:web:polis.pub:alice`.** The path form resolves to
`https://polis.pub/alice/did.json`, which is not where a polis tenant is served: each tenant is its own
host and its own TLS origin. The subdomain form matches the existing routing exactly, and it gives each
tenant a clean trust origin instead of putting the whole fleet under one apex.

## 3. The document

```json
{
  "@context": [
    "https://www.w3.org/ns/did/v1",
    "https://w3id.org/security/suites/jws-2020/v1"
  ],
  "id": "did:web:alice.polis.pub",
  "verificationMethod": [
    {
      "id": "did:web:alice.polis.pub#key-1",
      "type": "JsonWebKey2020",
      "controller": "did:web:alice.polis.pub",
      "publicKeyJwk": {
        "kty": "OKP",
        "crv": "Ed25519",
        "x": "11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"
      }
    }
  ],
  "authentication": ["did:web:alice.polis.pub#key-1"],
  "assertionMethod": ["did:web:alice.polis.pub#key-1"]
}
```

Serialised with two-space indentation and a trailing newline, in exactly the field order above. The
output is **deterministic**: the same key and host always produce the same bytes.

### 3.1 `@context` — and why this one

`JsonWebKey2020` is defined by **`https://w3id.org/security/suites/jws-2020/v1`**.

It is *not* defined by the neighbouring `https://w3id.org/security/jwk/v1`, which redirects to the 2025
VCDI JWK context and defines the newer, unversioned term `JsonWebKey`. Pairing that context with a
`JsonWebKey2020` verification method leaves the type **undefined** under JSON-LD expansion — dropped by
a lenient processor, rejected by a strict one. A strict-mode expansion of the wrong pairing fails; the
pairing above expands `type` to `https://w3id.org/security#JsonWebKey2020`.

> **The rule, not the pairing:** the context is chosen by *what defines the type being emitted*. If the
> key encoding ever changes, re-derive the context the same way rather than carrying this one forward.

### 3.2 Key encoding — JWK only

The `x` member is the **unpadded base64url encoding of the raw 32 public-key bytes**
([RFC 8037](https://www.rfc-editor.org/rfc/rfc8037) §2). It is the same key published as
`ssh-ed25519 AAAA…` in `.well-known/polis`, decoded out of the OpenSSH wire format.

A second `publicKeyMultibase` / `Ed25519VerificationKey2020` verification method is **deliberately not
emitted**. It would be a second representation of one key, to be kept in sync forever, for no known
consumer. If a real consumer appears it can be added additively.

### 3.3 `assertionMethod`

Present, and pointing at the same key as `authentication`. It is the verification relationship a VC
verifier checks when a DID is a credential **subject or issuer**, which is the entire point of being
resolvable — a document with only `authentication` resolves and is useless.

⛔ **On a site that has rotated, `assertionMethod` and `authentication` name the CURRENT key only.**
See §3.5.

### 3.4 The fragment

`#key-<epoch+1>`, so the genesis key is `#key-1`. A fingerprint-derived fragment was considered: it
changes on rotation anyway, so it buys no stability and costs readability.

Epochs count from 0 and fragments from 1, which is not an off-by-one to tidy up: it keeps `#key-1` the
fragment a never-rotated site has always published, so adding key-history support left every existing
document **byte-identical**.

### 3.5 Key history

The document carries every key the site has held, projecting
[`public_key_history`](key-history.md) into the standard's own vocabulary.

```json
"verificationMethod": [
  { "id": "did:web:alice.polis.pub#key-2", "…": "the CURRENT key" },
  { "id": "did:web:alice.polis.pub#key-1", "…": "a RETIRED key" }
],
"authentication":  ["did:web:alice.polis.pub#key-2"],
"assertionMethod": ["did:web:alice.polis.pub#key-2"]
```

⛔ **The whole design is which list a retired key appears in:**

| List | Contents | Says |
|---|---|---|
| `verificationMethod` | every key, current and retired | *these keys are mine, and some of them were mine* |
| `authentication` | the current key only | *this key can act as me now* |
| `assertionMethod` | the current key only | *this key speaks for me now* |

A retired key must stay **resolvable** — a signature it made two years ago is still checkable, which is
the entire reason to publish history — and must stop being **authoritative**: leaving it in
`assertionMethod` would let a retired key keep issuing claims in this identity's name, which would make
rotation meaningless. Those are different claims and the standard already has two lists for exactly
that. No extension is needed, which is the point.

Retired methods follow the current one **newest-first**, so reading down the list walks backwards in
time from now — the direction someone resolving a DID to check an old signature is travelling. *(The
native block is oldest-first, because a verifier walks it forward from genesis. Different readers,
different order, one source.)*

⚠️ **A consumer checking a signature must follow `assertionMethod`, not take
`verificationMethod[0]`.** On a document with history those are no longer the same entry, and a check
that conflated them would treat a retired key as authoritative on a document whose `assertionMethod`
had been tampered with.

⚠️ **The document is a PROJECTION.** `public_key_history` is the source and carries `valid_from` /
`valid_until` and the transition signatures that make the chain self-proving — none of which a DID
document can express. That is the whole reason both shapes exist. Regenerate this from the block;
never author it here.

### 3.6 No polis fields

The document carries **nothing outside the five members above** — no generator marker, no version
stamp, no polis-specific extension. Elsewhere polis stamps files it writes with
`polis-cli-go/<version>`; that convention stops at this file. A DID Document is read by other people's
tooling in a format polis does not own, and decorating it is the opposite of carrying a standard.

## 4. Discovery — no pointer

The document is at `/.well-known/did.json`, always, and **nothing in `.well-known/polis` points at
it**.

This is the opposite of how a polis licence is found, and deliberately so. A licence lives wherever the
site's own layout puts it, so `.well-known/polis` carries a `license` pointer. `did.json`'s location is
fixed by an external standard — did:web resolution *is defined as* `/.well-known/did.json` for the
no-path form. A pointer would be inert, because no resolver would read it, and it would create a second
source of truth for a path nobody can vary.

## 5. Serving requirements

| | |
|---|---|
| Status | `200` |
| `Content-Type` | `application/json` |
| `Access-Control-Allow-Origin` | `*` |
| `OPTIONS` | answered, with `Access-Control-Allow-Methods: GET, OPTIONS` |

TLS is not optional: the whole trust argument of `did:web` is that HTTPS proves the server is the one
the domain names.

## 6. Lifecycle

The document is **derived, never authored**. It is fully determined by (host, public key), so any
component may regenerate it at any time without asserting anything the site owner did not already say.
That is why polis writes it for existing sites without asking — the exact opposite of a licence, which
is a signed statement of intent nobody may make on the author's behalf.

| When | What happens |
|---|---|
| `polis init` | Published, if the canonical host is known. With no host, **nothing is written** — a DID naming the wrong host resolves to nothing, and guessing is worse than silence. |
| `polis rotate-key` | Republished with the new key. |
| Hosted maintenance sweep | Published for every tenant, and rebuilt whenever it is missing, stale, or edited. |
| `polis did --write` | Published on demand. |

Restoration needs no stored baseline: the correct bytes are recomputable from the site's own key and
host, so a deleted or rewritten document simply loses to the next sweep.

## 7. Rotation semantics

After `polis rotate-key`, the document states the new key in `authentication` and `assertionMethod`,
**and keeps the retired key in `verificationMethod`** (§3.5). So a resolver reading a current document
can still verify what an old key signed, while being told unambiguously which key speaks for the
identity now.

⚠️ **What `did:web` still does not give you is a signed succession.** The document says *these are my
keys*; it does not carry a pre-rotation commitment, a key-event log, or the transition signatures that
prove one key handed authority to the next. So a resolver reading a **stale** copy still cannot tell it
is stale, and a document is only as trustworthy as the TLS fetch that produced it.

That is a property of the method, not a gap in this profile — durable, self-proving key succession is
what `did:webs` (did:web + KERI) exists for. Polis carries the succession proof in its **own** shape
instead: [`public_key_history`](key-history.md) holds the validity dates and the per-entry
`transition_sig`, so the chain proves itself with no service and no resolver in the loop. *One key,
two shapes* becomes *its history, two shapes* — and the shape that can express the proof is the one
that carries it.

⛔ **A stale document is still worse than an absent one**, because it answers 200 with a retired key in
`assertionMethod` and a resolver cannot tell. Every rotation path therefore republishes it, or
**removes** it if republishing fails.

## 8. What this does not do

- **It does not make polis-signed content verifiable by VC tooling.** Polis signs with the SSH
  signature format; a VC verifier expects a JWS or a Data Integrity proof. That needs a second signing
  path and is not built.
- **It does not add a login or authentication flow.** Polis is not an identity provider.
- **It does not carry `alsoKnownAs`, `service`, or a `jwks.json`.** All are cheap; none has a consumer
  yet.
- **It does not survive losing the domain.** The identifier *is* the domain.
- **It does not carry the succession proof.** The keys are all there; the signed handovers between
  them are in [`key-history.md`](key-history.md). See §7.

## 9. Verifying an implementation

```bash
curl -sS https://alice.polis.pub/.well-known/did.json | jq .
curl -sSI https://alice.polis.pub/.well-known/did.json | grep -i 'content-type\|access-control'
```

Then resolve it — [`dev.uniresolver.io`](https://dev.uniresolver.io/), or the driver the universal
resolver uses:

```bash
npm i did-resolver web-did-resolver
node -e "import('did-resolver').then(async ({Resolver}) => {
  const { getResolver } = await import('web-did-resolver')
  console.log(JSON.stringify(await new Resolver(getResolver())
    .resolve('did:web:alice.polis.pub'), null, 2))
})"
```

A conformant result reports `contentType: application/did+ld+json` and no `error`.

To check the `@context` specifically, expand the document with a JSON-LD processor in **safe mode**
(`jsonld.expand(doc, { safe: true })`), which throws when a term is not defined by the context rather
than silently dropping it.

## See also

- [`../concepts/identity.md`](../concepts/identity.md) — what a polis identity is, and what the DID adds
- [`key-history.md`](key-history.md) — the native shape this document projects, and the only one that
  carries the succession proof
- [`license.md`](license.md) — the other half of *carry the standard, own the binding*
- [`../../general/security/security-model.md`](../../general/security/security-model.md) — keys, domain binding, and the threat model
