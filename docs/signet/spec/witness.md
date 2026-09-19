# Discovery-service signatures and witnesses

*for implementers, including anyone writing a second implementation*

*For* [Reviewers](../../README.md#reviewing-the-security-and-identity-design) · [Developers](../../README.md#building-on-polis) — *About* [Identity](../../general/README.md#identity) — *Kind* [Spec](../../README.md#kinds-of-page) — *Component* [Discovery service](../../ds/README.md) — *Code* [`cli-go/pkg/discovery/witness.go`](../../../cli-go/pkg/discovery/witness.go) — *See also* [recipe](../recipes/09-prove-when.md)

**Lives in:** discovery-service responses · `.well-known/polis` → `witnesses` (content) · `public_key_history[].witnesses` (rotations)

---

## 1 What a witness is

A discovery service (DS) already verifies what it is shown: a site's registration, a post's
signature, a key rotation's handover. A **witness** is the DS signing what it saw, so the observation
can travel with the thing observed instead of living in the DS's database:

> *"At `witnessed_at`, a party who could sign as this domain presented me these exact values, and the
> signature verified against the key the domain published then."*

That sentence carries three facts, and the first is the reason witnesses exist:

1. **The bytes existed by `witnessed_at`** — a date the site did not choose.
2. The presenter controlled the domain's key.
3. The DS is willing to be quoted on it.

Nothing a site publishes about itself can supply fact 1. A post's `published:` and a key's
`valid_from` are both the author's own claims — inside their signatures, and chosen by the signer.

### ⛔ Evidence, never a gate

**Nothing may require a witness.** An artifact without one verifies exactly as it did before, on its
own signature; a witness adds a claim and never withholds one. A verifier that treats a missing
witness as a failure is not implementing this spec.

This is the one decision that separates a polis witness from a `did:webvh` witness, whose log entries
*must* be witnessed. Same mechanism, opposite policy.

### What a witness does not say

- **Not that the content is true, good, or authorised.** It says a DS saw these bytes at a time.
- **Not that the witnessed version is the current one.** A witness of v1 stays true after v2 exists.
- **Not that nothing was omitted.** A witness dates a rotation a chain *records*; it cannot reveal a
  rotation the chain leaves out, because the site leaves that witness out too.
- **Not protection against the operator that issued it.** See §8.

## 2 The envelope every DS signature shares

Every signature a polis DS makes — old and new — is the same envelope:

| | |
|---|---|
| Algorithm | Ed25519 |
| Encoding | OpenSSH SSHSIG (`-----BEGIN SSH SIGNATURE-----`), namespace **`file`**, hash **`sha512`** |
| Signed bytes | a **canonical JSON** string, defined per signature below |
| Signer | the DS's own key — never a site's |

The same envelope polis sites use for everything they sign, so any SSHSIG verifier works
(`ssh-keygen -Y verify -n file`).

### Where a DS publishes its keys

| Request | Returns |
|---|---|
| `GET <ds>/.well-known/polis` | `{"public_key", "key_id", "signing_algorithm": "ed25519-ssh"}` — the **current** key |
| `GET <ds>/v1/sites/public-key?key_id=<id>` | `{"public_key", "key_id"}` — **any** key the DS has held under that id, retired ones included |

`public_key` may be an `ssh-ed25519 …` string or the raw 32-byte key in base64; accept both.

⛔ **A witness names its key (`ds_key_id`) and a verifier must fetch THAT key**, not the DS's current
one. That is what keeps a witness verifiable after the DS rotates.

⛔ **Never take the key from the record.** A key sitting inside a record anyone could have written is
no evidence that it is the DS's key. The key comes from the DS's own domain over TLS — the same trust
root as a site's own `.well-known/polis`.

⚠️ **What a DS does not publish: the history of its own keys.** You can fetch any key a DS has held *by its
id*, but there is no list of those keys and no signed handover from one to the next. A site publishes
exactly that about itself ([key history](key-history.md)); a DS does not yet. So a retired DS key is trusted
on the DS's word over TLS alone, the same as its current key, and nobody can audit the succession — for
example, confirm that a key id was not added after the fact. A witness still verifies; what is missing is
the evidence that the key that made it was the DS's key at the time.

## 3 The four things a DS signs

| Signature | Over | Canonical form | Survives as |
|---|---|---|---|
| **Site registration** (`service_attestation`) | the registration payload the site signed | `JSON.stringify({version, action, domain})` — **insertion order, not sorted** | the site's `.polis/ds/<ds>/registration.json` and the DS's `ds_registered_sites` row |
| **Autobless decision** (`ds_attestation`) — ⚠️ **historical** | the DS's own blessing decision, from when a DS granted blessings itself | sorted: `{action:"autobless", comment_url, ds_key_id, policy_rule, policy_source, target_url, type:"pub.polis.comment.blessing"}` | `pub.polis.comment.blessing.granted` stream events written before the change |
| **Signed response** (`ds_signature`) | a query **response**, minus `ds_signature` / `ds_key_id` | sorted keys at every level | nothing — a new one is minted per request |
| ⭐ **Witness** | an **observation**: a content registration, a key rotation, or a post author's blessing decision | §4 | carried by the site beside the thing witnessed (§5); a decision's, on its stream event (§4.1) |

⚠️ **The autobless decision is no longer made.** A discovery service records blessing requests and decides
nothing for a user: the post author's own software decides and signs ([delegation](delegation.md) §7).
**The canonical form above stays specified, and verifiers keep checking it,** because every event that
carries one is still on the stream and must keep verifying. Nothing produces a new one.

⚠️ **The first row's canonical form is the odd one out and must not be "fixed".** It is exactly the
bytes the site signs to register — `{"version":1,"action":"register","domain":"alice.polis.pub"}` in
that order — and the DS countersigns the same bytes. Sorting them would change what every existing
`service_attestation` covers. It also carries no `ds_key_id`; the key is recorded beside it
(`attestation_key_id`).

⚠️ **A signed response is not a witness.** It signs *an answer to a question at fetch time*, is not
carried with anything, and is re-minted on every request. `GET /v1/sites/keys/history` returns one —
which proves the DS said something, not that the DS saw a rotation happen.

## 4 The witness record

One flat JSON object, returned by the DS and carried by the site unchanged.

| Field | `content-registration` | `key-rotation` |
|---|---|---|
| `action` | `"content-registration"` | `"key-rotation"` |
| `type` · `url` · `version` · `author` | the registration's, as the DS recorded them | — |
| `actor` | the domain the DS derived from `url` | — |
| `public_key` | the key the author's signature verified against | — |
| `artifact_hash` | **optional** — see §6 | — |
| `domain` · `old_key` · `new_key` · `timestamp` · `transition_sig` | — | the rotation request, verbatim; `timestamp` is the new key's `valid_from` |
| `ds` | the DS's public base URL — where its keys are published | same |
| `ds_key_id` | the DS key that signed | same |
| `witnessed_at` | **the DS's own clock** when it recorded the observation (`toISOString()`, millisecond precision) | same — byte-identical to that key's `created_at` in `GET /v1/sites/keys/history` |
| `signature` | SSHSIG over the canonical form | same |

### Canonical form

Take the action's field list from the table above — **only those fields**, excluding `signature`.
Drop any whose value is an empty string. Serialise as JSON with **keys sorted**, **no whitespace**, and
**no HTML escaping** (`<`, `>`, `&` stay literal; strings are escaped exactly as ECMAScript
`JSON.stringify` does). Unknown fields in a record are ignored for signing.

⚠️ **The field lists are a wire contract.** Adding a field to an action changes the signed bytes of
every record written afterwards; removing one breaks every record already carried by a site. An
unknown `action` has no canonical form — a verifier must not guess one.

Golden bytes — both implementations (the DS signer and the Go verifier) test against these exact
strings, read from one shared fixture
(`discovery-service/core/contract-fixtures/witness-canonical.json`):

**Content registration:**

```
{"action":"content-registration","actor":"alice.polis.pub","artifact_hash":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","author":"alice.polis.pub","ds":"https://ds.example","ds_key_id":"ds-primary","public_key":"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample","type":"pub.polis.post","url":"https://alice.polis.pub/content/pub.polis.core/post/20260913/hello.md","version":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","witnessed_at":"2026-09-13T12:00:00.123Z"}
```

**Key rotation** (the `\n` sequences are the two-character JSON escapes inside `transition_sig`):

```
{"action":"key-rotation","domain":"alice.polis.pub","ds":"https://ds.example","ds_key_id":"ds-primary","new_key":"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINew","old_key":"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOld","timestamp":"2026-09-13T12:00:00Z","transition_sig":"-----BEGIN SSH SIGNATURE-----\nU1NIU0lH\n-----END SSH SIGNATURE-----","witnessed_at":"2026-09-13T12:00:00.456Z"}
```

### 4.1 A blessing decision — `relationship-update`

A post author's grant or deny, as `POST /v1/relationships` presented it and the DS verified it.

| Field | Value |
|---|---|
| `action` | `"relationship-update"` |
| `type` · `source_url` · `target_url` · `timestamp` | the request's, verbatim |
| `decision` | the request's `action` (`grant` / `deny`), renamed because `action` names the witness action |
| `actor` | the post author's domain — the key the request verified against is theirs |
| `public_key` | that key |
| `agent` · `grant` | **only for a user agent's act**: the request's marker ([delegation](delegation.md) §5.1) |
| `grant_state` | **only for a user agent's act**: what the DS found about the cited grant — `live` · `withdrawn` · `not-found` · `unknown`. A fact, never a verdict |
| `ds` · `ds_key_id` · `witnessed_at` · `signature` | as above |

The witness names every field of the signed request except its signature, so anyone holding the request's
signature (it is on the stream event) can rebuild the request's bytes from the witness and check both.
The record is **returned in the response and carried on the `blessing.granted` / `denied` stream event**.

**Relationship update, by an agent:**

```
{"action":"relationship-update","actor":"alice.polis.pub","agent":"rosie","decision":"grant","ds":"https://ds.example","ds_key_id":"ds-primary","grant":"https://alice.polis.pub/content/pub.polis.core/attestation/20260915T120000Z-0123456789abcdef.json","grant_state":"live","public_key":"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample","source_url":"https://bob.polis.pub/content/pub.polis.core/comment/20260915/re.md","target_url":"https://alice.polis.pub/content/pub.polis.core/post/20260915/hello.md","timestamp":"2026-09-15T12:00:00Z","type":"pub.polis.comment.blessing","witnessed_at":"2026-09-15T12:00:00.789Z"}
```

## 5 Where a site carries witnesses

⭐ **The witness lives beside the thing witnessed.**

### A rotation — inside the key-history entry

`public_key_history` entries gain an optional `witnesses` list
([key-history §2](key-history.md)). The witness rides in the entry for the key the rotation made
current:

```json
"current": {
  "epoch": 1,
  "key": "ssh-ed25519 AAAA…NEWKEY",
  "valid_from": "2026-09-15T10:22:03Z",
  "transition_sig": "-----BEGIN SSH SIGNATURE-----\n…",
  "witnesses": [
    { "action": "key-rotation", "domain": "alice.polis.pub", "old_key": "…", "new_key": "…",
      "timestamp": "2026-09-15T10:22:03Z", "transition_sig": "…",
      "ds": "https://ds.polis.pub", "ds_key_id": "ds-primary",
      "witnessed_at": "2026-09-15T10:22:03.417Z", "signature": "-----BEGIN SSH SIGNATURE-----\n…" }
  ]
}
```

- **Absent means unwitnessed**, never invalid. A chain with no witnesses publishes no `witnesses` key
  at all, so every site that never rotated — or rotated before witnessing — is byte-identical.
- `transition_sig`'s signed bytes (the fixed five-field rotation message) are unchanged. Carrying a
  witness never alters what the chain itself signs.
- Genesis has no witness. Nothing observed a key being generated.

### Content — the site's witness file, found by pointer

Content is unbounded and `.well-known/polis` is fetched on every verification, so content witnesses
live in their own file:

```json
// .well-known/polis
"witnesses": "/content/witness/witnesses.json"
```

```json
// the file the pointer names
{
  "witnesses": {
    "https://alice.polis.pub/content/pub.polis.core/post/20260913/hello.md": [
      { "action": "content-registration", "type": "pub.polis.post", "url": "…", "version": "sha256:…",
        "author": "…", "actor": "alice.polis.pub", "public_key": "ssh-ed25519 …",
        "artifact_hash": "sha256:…", "ds": "https://ds.polis.pub", "ds_key_id": "ds-primary",
        "witnessed_at": "2026-09-13T12:00:01.500Z", "signature": "-----BEGIN SSH SIGNATURE-----\n…" }
    ]
  }
}
```

- ⛔ **Discovery is by pointer.** `/content/witness/witnesses.json` is polis's default location, not a
  convention a reader may assume.
- **Keyed by the registered URL, a list per artifact.** A verifier matches on the URL's **path**, so a
  site that moves hosts, or a local copy of one, still finds its witnesses.
- **Not a content type and not signed.** A witness is somebody else's testimony, not the site's
  speech; each record carries its own DS signature, and a site signature over the collection would
  claim authorship of it.

### ⛔ Writing the set: merge, never replace

A writer **adds** records and **never removes one**. Two records of the same testimony — the same DS
observing the same values (same action; same URL, `version` and `artifact_hash`, or same rotation) —
collapse to the **earliest** `witnessed_at`; a later witness of the same bytes adds nothing.

The reason is §7: a DS retains only the current version's witness, so **for every superseded version,
the site's published copy is the only copy in existence.** A writer that replaced the set with what a
DS returns today would delete that testimony. An unreadable witness file is never overwritten, for
the same reason. Nor is anything a writer does not model dropped: a member of the file, or of any
record, that the writer does not recognise is written back unchanged. The file is unsigned, so
carrying a member through asserts nothing, and a record's DS signature covers only its §4 fields.

## 6 `artifact_hash` — binding the whole artifact

For `pub.polis.post` and `pub.polis.comment`, the registration's `version` is the sha256 of the
**canonicalized body only**. A witness over `version` alone therefore does not bind the frontmatter —
`title`, the `license:` block, or `published:`, the very claim a backdated signature would rewrite.

So a registration may carry:

```
metadata.artifact_hash = "sha256:" + hex(sha256(signing base))
```

where the signing base is the exact bytes the artifact's own signature covers
([signing-base](signing-base.md)). It sits inside `metadata`, which is already inside the registration
payload the author signs, so **no wire field, no DS schema and no artifact signing base changes.** The
DS rejects a present-but-malformed value and lifts a valid one into the witness.

| Type | What `version` hashes | Witness binds |
|---|---|---|
| `pub.polis.post`, `pub.polis.comment` | the body | **the whole signed artifact** when `artifact_hash` is present; **the body only** when it is not |
| `pub.polis.attestation`, `pub.polis.tag` | the whole canonical signed record | the whole record — no `artifact_hash` needed |

⚠️ **Do not confuse two dates.** A post registration's `metadata.published_at` is the time of
**registration**, set by the client when it registers. The post's frontmatter `published:` is the
author's claimed signing time. They are different facts with similar names.

Registrations made before `artifact_hash` existed carry none; nothing backfills it, because a witness
cannot testify to what it did not see.

## 7 How a verifier checks a witness

For one artifact:

1. Find the records for its URL path in the site's witness set (§5).
2. A record **applies** to these bytes when its `artifact_hash` equals the artifact's own (if the
   record has one), or else when its `version` equals the artifact's `current-version`. A record for
   other bytes is **superseded** — true about a different version, and not a problem.
3. For each applicable record, fetch the DS key named by `ds` + `ds_key_id` (§2) and verify
   `signature` over the canonical form (§4).
4. Report **one of four states**:

| State | Meaning | A failure? |
|---|---|---|
| **witnessed** | an applicable record verified. Report its earliest `witnessed_at` — *these bytes existed by then* — and what it binds | no |
| **unwitnessed** | no applicable record exists. **The artifact still verifies on its own signature**; its date is only its own claim | ⛔ **never** — nothing may require a witness |
| **unverifiable** | an applicable record exists but its DS key could not be obtained. A bonus claim went unchecked; nothing is wrong with the artifact | ⛔ **never** |
| **invalid** | an applicable record's signature does **not** verify against the key its DS publishes | ⛔ **yes** |

⛔ **Exactly one case fails, and it is not absence.** A published witness is the site **asserting**
that a third party testified. A record that does not verify is a false claim about someone else —
strictly worse than silence — so a verifier reports it as a failure. **It takes precedence over
*witnessed*:** a genuine record beside a forged one does not make the published set honest. This is
the same rule as any signed artifact: no signature is a weaker claim, a bad signature is a finding.

What never fails: no witness at all; a witness whose DS cannot be reached or whose key cannot be
obtained; and a record for **other bytes** — a superseded version's witness in the set is normal.
**Verifying the artifact never touches a DS**; only verifying the optional witness does, and only to
fetch a key. The artifact's own signature is judged separately from its witness either way.

A key rotation is checked the same way, against the entry it rides in: a record applies when its
`domain`, `old_key`, `new_key`, `timestamp` and `transition_sig` match the rotation into that entry.
An applicable rotation record that does not verify is likewise a failure.

When a verifier fetches a DS key on behalf of a run that has a correlation id, it sends it as
`X-Request-Id`.

### What a verified witness lets a consumer notice

These are facts to report, never gates:

- **A retired-key signature first witnessed after the key retired.** If an artifact verifies only
  against a retired key, at a claimed `published:` inside that key's window
  ([key-history §4](key-history.md)), and its earliest witness is **after** the window closed, the
  only independent date contradicts the claim. That is what a signature backdated with a compromised
  retired key looks like.
- **A rotation witnessed far from its `valid_from`.** The DS refuses a rotation stamped more than five
  minutes from its own clock, so a verified witness more than five minutes from the entry's
  `valid_from` cannot describe that moment.

## 8 The discovery service's side, stated honestly

### What the DS keeps

For content, the DS retains the countersignature on the content row
(`service_attestation`, `attestation_key_id`, `attested_at`). ⛔ **The rule:**

> **The DS's witness columns describe the row's CURRENT version, or they are NULL.**

Rows are keyed by `(type, url)` and overwritten on re-registration, so the DS holds only the current
version's witness. **The DS is a cache; the site's published witness set is the record.** A NULL
column means *"the DS cannot tell you"*, never *"no witness ever existed"*.

⚠️ **Retained, not readable.** No DS endpoint yet returns a stored content witness, so a site cannot
currently rebuild its witness set from the DS. Anything a future read-back does not return is
superseded or uncached, not absent — and a rebuild merges (§5), never replaces.

**Nothing is backfilled.** A witness for a registration that happened before witnessing existed would
be a countersignature dated after the fact — exactly the lie a witness exists to prevent.

**Nothing is revoked.** Unregistering or unpublishing content does not invalidate its witness:
*"these bytes existed at T"* stays true forever.

### ⚠️ The soft gradient

**Registered artifacts carry more evidence than unregistered ones.** That is a real consequence of
this design, and it is stated rather than glossed. It is bounded by three things, which belong
together:

- **nothing fails** without a witness — an unwitnessed artifact is a weaker claim, not an invalid one;
- **a self-hoster can register too** — witnessing is not a hosted privilege;
- **multiple discovery services are possible** — the set is a list, so the gradient is not one
  party's to control.

### ⚠️ Custody: a witness does not protect you from the operator that issued it

An operator that runs its own DS holds that DS's key. So it is both the **witness** and, when a site's
witness set is repaired, the party that could **rebuild** the record. On a hosted deployment, where
the operator also holds the tenant's key ([custody](custody.md)), a countersignature from the
operator's own DS constrains nothing the operator does.

What a witness buys is the same thing custody buys: an honest operator is checkable, and a dishonest
one leaves a record that can be contradicted. The mitigation is the one above — **more than one
discovery service**, whose witnesses no single operator can issue.

## 9 What this is not

- **Not required** — by anything, for anything (§1).
- **Not a timestamp authority.** A witness is a DS that happened to verify a registration and says
  when; it offers no service beyond that.
- **Not `did:webvh` support.** polis's key history and witnesses carry every field a `did:webvh` log
  entry needs, and a test in the Go implementation asserts that sufficiency. That makes such a log
  projectable; it does not make polis a `did:webvh` implementation. See
  [projection](../concepts/projection.md).
