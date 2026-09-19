# The signing base — `pub.polis.signing-base.v1`

*for implementers, including anyone writing a second implementation*

*For* [Developers](../../README.md#building-on-polis) · [Reviewers](../../README.md#reviewing-the-security-and-identity-design) — *Kind* [Spec](../../README.md#kinds-of-page) — *Code* [`cli-go/pkg/signing/base.go`](../../../cli-go/pkg/signing/base.go) — *See also* [concept](../concepts/identity.md) · [recipe](../recipes/05-verify-a-site.md)

Every signed artifact polis produces is covered by this page.

> **The test for this document is that you can build a verifier from it without reading any Go.**
> If you cannot, that is a defect in the page, not in your reading. Say so.

---

## 1 What a signing base is, and why this page exists

A **signing base** is the exact byte sequence a signature covers. Everything else — the file on disk,
the JSON with its indentation, the frontmatter with its `signature:` line — is a *container*. Two
implementations that disagree about the container can still interoperate. Two that disagree about the
signing base cannot: each concludes the other is forging.

There is nothing exotic here. There is one genuinely non-obvious rule and it is the reason this page
is written down:

> ⛔ **Field order in the JSON families is a FIXED SEQUENCE, and it is not alphabetical.**
> Every canonical-JSON convention you know — RFC 8785 included — sorts keys. **Polis does not.** The
> order is given per type in §5, it is the order the fields appear in the published file, and an
> implementation that sorts produces different bytes and concludes our signatures are invalid.

Everything else follows from stating the fields and being careful with whitespace.

### What is signed, and what only looks like it

⚠️ **The base is REBUILT from named fields, never hashed off the raw file.** So a field that is not
named below is *not covered by the signature*, even though it sits in the same document. A verifier
must not tell a reader "this file is signed" and mean "every byte of this file is signed." These
fields are outside the base:

| Field | Family | Why it cannot be inside |
|---|---|---|
| `signature` | both | It cannot cover itself. |
| `current_version` | canonical JSON (§5) | It is a hash **of** the serialised signing base, so it is computed after and cannot be among the bytes it describes. |

⚠️ **The markdown `current-version` is NOT on this list. It is inside the base.** It hashes the body
alone (§4.6), not the signing base, so it is written before signing and the signature covers it — see
the worked example in §4.4. Only `signature` (and, for a comment, `author`) is stripped from a
markdown base (§4).

Anything else you find in a polis artifact and cannot locate in §4 or §5 is **unsigned**. That is a
fact about the artifact, not a defect in it.

⚠️ **The converse is the harder one, and §6.1 is its whole answer.** A field you cannot locate here
might instead be one a *newer* writer signs and this page has not caught up with. You cannot tell the
two apart, and you must not guess: a field you do not recognise makes the artifact **uncheckable**,
never invalid.

## 2 Version — and why v1 carries no marker

The base is versioned so canonicalization can ever change without invalidating history. **v1 is not a
new format: it is the format already in production, written down.**

⚠️ **v1 is the UNMARKED version, and that is a consequence rather than an oversight.** A version
marker is only worth having if it is *inside* the signed bytes — otherwise stripping it downgrades
the artifact, which is the JWT `alg` mistake. But adding a field inside the signed bytes of a type
that already exists would invalidate every artifact of that type. So:

- **No marker ⇒ `pub.polis.signing-base.v1`.** This is every artifact in existence today.
- **A future v2 announces itself in a signed field**, and an implementation that does not recognise
  the version must report *"I cannot check this"* — never *"invalid"*. Whether an uncheckable
  artifact is acceptable is the consumer's judgment, not the verifier's.
- **v1 is never retired.** A comment is signed once by its author and then cached on other people's
  sites, in followers' feeds, in archives. The author cannot reach those copies. Old bytes must
  verify forever, so every verifier carries v1 indefinitely.

## 3 The signature envelope — shared by every family

Once you have the signing-base bytes, every polis signature is made the same way, regardless of
family.

**Algorithm:** Ed25519, in the **SSH signature format** — byte-compatible with
`ssh-keygen -Y sign -n file`, **namespace `file`, hash `sha512`**.

The bytes actually passed to Ed25519 are not the signing base itself but the standard SSHSIG
pre-image:

```
"SSHSIG"                       6 literal bytes, NOT length-prefixed
string("file")                 namespace
string("")                     reserved, empty
string("sha512")               hash algorithm
string(sha512(signing base))   64 raw bytes, length-prefixed
```

where `string(x)` is a big-endian `uint32` length followed by `x`.

**The key** is the site's Ed25519 identity key, in OpenSSH public-key form
(`ssh-ed25519 <base64> [comment]`), published at `https://<site>/.well-known/polis` as `public_key`.
Verify against the **published** key, not a cached one: that is the key any third party on the network
would fetch. If the site publishes a `public_key_history`, an old signature resolves to the key that
made it — see [`key-history.md`](key-history.md).

**How the signature is stored** differs by family, and it is a container detail, not a signing one:

| Family | Storage |
|---|---|
| Markdown | the base64 body **with the PEM armour and newlines stripped**, on one line, as the frontmatter `signature:` value |
| JSON | the full PEM block, armour and `\n` included, as the `signature` string |

⚠️ **The public key embedded in the SSHSIG envelope is metadata and is NOT covered by the signature.**
Do not verify against it. polis 0.59.0 wrote an all-zero key into that field on some artifacts; those
signatures are valid and standard `ssh-keygen -Y verify` rejects them. Verify against the site's
published key and the question does not arise.

## 4 Family 1 — Markdown (`pub.polis.post`, `pub.polis.comment`)

A post or comment is a Markdown file with a leading YAML frontmatter block. The base is the whole
file — frontmatter *and* body — minus the fields written after signing, canonicalized.

### 4.1 The rule

1. **Locate the frontmatter block.** Line 0 (after splitting on `\n`) must be `---` once surrounding
   whitespace is trimmed. The block ends at the next line that is `---`. **If there is no leading
   `---`, there is no frontmatter and nothing is excluded.** If the opening fence is never closed,
   the block extends to the end of the document.
2. **Drop the type's unsigned fields — FROM THAT BLOCK ONLY.** A line is dropped when it begins **at
   column 0** with the field name followed by `:`.
3. **Canonicalize** what remains (§4.3).

**Unsigned fields, per type:**

| Type | Fields excluded from the signing base |
|---|---|
| `pub.polis.post` | `signature` |
| `pub.polis.comment` | `signature`, `author` |

A comment's `author` is written into the final frontmatter *after* signing, which is why it is
excluded. A post has no such field: **a post's `author:` line, if one exists, is inside the
signature.** The two rules are not interchangeable.

**Choosing the type.** An artifact is a comment when its frontmatter carries `type: comment` **or**
an `in-reply-to:` block. Otherwise it is a post. Get this wrong and a perfectly good comment is
reported as tampered-with.

### 4.2 ⛔ The two traps, both of which have been live bugs

**Trap 1 — do not scan the document for the prefix.** The obvious implementation drops every line
starting with `signature:` anywhere in the file. It is wrong. A **body** line may legitimately begin
that way — a post documenting this very format, a quoted frontmatter block, a YAML code fence — and
dropping it produces bytes that are not what was signed, so **authentic content fails verification**
and is reported as tampering. Anchor the strip to the frontmatter block.

**Trap 2 — do not trim the line before matching the key.** An *indented* `signature:` is a **child**
of the key above it — inside a `license:` block, say — and is a different field that happens to share
a name. Match at column 0. The same trap applies to any frontmatter parser: trimming first means a
nested key reads as top-level.

Both traps are one mistake: **matching a key by its shape instead of by its structural position.**

### 4.3 Canonicalization

Applied to the whole remaining document, in this order:

1. Replace every `\r\n` with `\n`, then every remaining `\r` with `\n`.
2. Remove leading newline characters from the start of the document. *(Newlines only — not spaces or
   tabs.)*
3. Split on `\n`. From each line, remove trailing spaces, tabs and carriage returns.
4. Remove trailing empty lines.
5. Join with `\n`. If the result is non-empty, append exactly one `\n`. An empty document stays
   empty.

Encoding is UTF-8 throughout, unchanged.

### 4.4 Worked example — a post

Input file (`\n` line endings), including a body line that begins `signature:`:

```markdown
---
title: Hello, World
published: 2026-08-27T14:02:00Z
generator: polis-cli-go/0.67.0
current-version: sha256:2ef7bde608ce5404e97d5f042f95f89f1c232871d1e0e6f2d3f5a2b4c6d8e0f1
version-history:
  - sha256:2ef7bde608ce5404e97d5f042f95f89f1c232871d1e0e6f2d3f5a2b4c6d8e0f1 (2026-08-27T14:02:00Z)
license:
  v: pub.polis.license.v1
  profile: pub.polis.license.reserved/1
  train-ai: n
  search: y
  terms: https://alice.example/license
  asserted: 2026-08-27T14:02:00Z
signature: U1NIU0lHAAAAAQ...
---

A body line can begin `signature:` and still verify:

signature: not-the-frontmatter-one
```

Signing base — one `signature:` line removed, the other kept, everything else byte-identical:

```
---
title: Hello, World
published: 2026-08-27T14:02:00Z
generator: polis-cli-go/0.67.0
current-version: sha256:2ef7bde608ce5404e97d5f042f95f89f1c232871d1e0e6f2d3f5a2b4c6d8e0f1
version-history:
  - sha256:2ef7bde608ce5404e97d5f042f95f89f1c232871d1e0e6f2d3f5a2b4c6d8e0f1 (2026-08-27T14:02:00Z)
license:
  v: pub.polis.license.v1
  profile: pub.polis.license.reserved/1
  train-ai: n
  search: y
  terms: https://alice.example/license
  asserted: 2026-08-27T14:02:00Z
---

A body line can begin `signature:` and still verify:

signature: not-the-frontmatter-one
```

Note what survived: the closing `---`, the blank line, the whole body, and the **indented** children
of `license:`. Note what did not: only the column-0 frontmatter `signature:`.

### 4.5 Worked example — a comment

The comment rule removes `author:` as well. Given frontmatter containing:

```yaml
published: 2026-08-28T09:15:00Z
author: https://bob.example
generator: polis-cli-go/0.67.0
```

the signing base contains:

```yaml
published: 2026-08-28T09:15:00Z
generator: polis-cli-go/0.67.0
```

⚠️ **Only for `pub.polis.comment`.** Applying it to a post changes the post's bytes and every post
signature fails.

### 4.6 The body hash is a separate thing

`current-version` is `sha256:` + hex of the **canonicalized body alone** — not the frontmatter, not
the signing base. It answers *"has the body moved?"*; the signature answers *"is this the author's?"*
They fail for different reasons and must be reported separately.

## 5 Family 2 — Canonical JSON

Six artifact types are signed as JSON: the licence, the follow file, a tag file, the blessing list,
an attestation record, and an operator's actor registry. **All six share one rule.**

⚠️ **One of them, the actor registry, carries a SECOND signature over a sub-object** — an entry's
optional `countersignature`, made by a different key than the file's. It is the same rule applied to a
smaller set of fields, and §5.3 gives both sequences.

### 5.1 The rule

Serialise the type's **signable fields**, in the order given for that type, as compact JSON:

1. **Compact.** No insignificant whitespace, no indentation, no trailing newline.
2. **Field order is the fixed sequence in §5.3.** Not alphabetical. Not parser order.
3. **`signature` and `current_version` are excluded** — see §1. Serialise as though the fields were
   not there, **never** as `"signature":""`.
4. **Optional fields are omitted entirely when empty**, never written as `""`, `{}` or `null`. An
   absent value and a present-but-empty one must have **one** byte sequence.
5. **An empty list is `[]`, never `null`.** *"I follow nobody"* has one byte sequence.
6. **Map keys sort lexicographically by byte** (the attestation `payload` is the only map).
7. **UTF-8**, with the escaping rule in §5.2.

Then sign those bytes per §3, and only then compute `current_version` = `sha256:` + hex of the same
bytes.

> ⚠️ **The order is: serialise → hash into `current_version` → sign the serialised bytes.** Every
> signable field must hold its final value *before* serialisation. Stamping `generator` or `version`
> after signing produces a file that writes cleanly, parses cleanly, and never verifies again.

### 5.2 ⛔ HTML escaping — the one that will catch you

**`<`, `>` and `&` are written as `\u003c`, `\u003e` and `\u0026`.**

None of the three is a JSON metacharacter, and essentially no serialiser outside Go escapes them.
Go's `encoding/json` does, by default, and the escaping is **inside the bytes that were hashed**. A
`terms` URL of `https://x.example/license?ref=a&b` is signed as
`https://x.example/license?ref=a\u0026b`. Write the raw `&` and your digest differs from ours.

⚠️ **As of 2026-09-04 no artifact anywhere on the network contains an escaped sequence** — no value
has yet held one of these characters. So this rule is specified and tested and has never executed in
the wild. The first query string in a terms URL is when it starts to matter, in both implementations
at once.

### 5.3 The field lists, per type

Each block below is the exact signable sequence, followed by the exact bytes a worked example
produces. **Those byte strings are checked against the implementation mechanically on every test
run**, not transcribed by hand.

---

#### `pub.polis.license` — a site's stated terms

Signable fields, in order: **`type`, `terms`, `created`, `updated`, `generator`.**
`terms` is an object whose own fields, in order, are:
**`v`, `profile`, `train-ai`, `search`, `ai-input`, `attribution`, `terms`, `contact`, `asserted`** —
each omitted when empty except `v` and `profile`.

Excluded: `current_version`, `signature`.

```
{"type":"pub.polis.license","terms":{"v":"pub.polis.license.v1","profile":"pub.polis.license.reserved/1","train-ai":"n","search":"y","ai-input":"n","attribution":"required","terms":"https://alice.example/license","contact":"https://alice.example/license","asserted":"2026-08-27T14:02:00Z"},"created":"2026-08-27T14:02:00Z","updated":"2026-08-27T14:02:00Z","generator":"polis-cli-go/0.67.0"}
```

Semantics: [`license.md`](license.md).

---

#### `pub.polis.follow` — the follow file (`following.json`)

Signable fields, in order: **`version`, `following`.**
Each entry, in order: **`url`, `added_at`, `site_title`, `author_name`** — the last two omitted when
empty.

⚠️ **`version` is not a version.** It holds the *generator* string. The name is wrong, it is
published, and it is deliberately left alone.

Excluded: `signature`. There is no `current_version` in this type.

```
{"version":"polis-cli-go/0.67.0","following":[{"url":"https://bob.example","added_at":"2026-08-28T09:15:00Z","site_title":"Bob's Notes","author_name":"Bob"}]}
```

An empty roster — note `[]`, not `null`:

```
{"version":"polis-cli-go/0.67.0","following":[]}
```

Semantics: [`attestation.md#the-signed-follow-file`](attestation.md).

---

#### `pub.polis.tag` — a tag file

Signable fields, in order: **`tag`, `targets`, `created`, `updated`, `generator`.**
Each target, in order: **`uri`, `added`.** An empty `targets` is `[]`.

Excluded: `current_version`, `signature`.

```
{"tag":"reading","targets":[{"uri":"https://alice.example/content/pub.polis.core/post/20260827/hello.md","added":"2026-08-27T14:02:00Z"}],"created":"2026-08-27T14:02:00Z","updated":"2026-08-27T14:02:00Z","generator":"polis-cli-go/0.67.0"}
```

---

#### The blessing list (`blessed.json`)

Signable fields, in order: **`version`, `comments`.**
Each post entry, in order: **`post`, `blessed`** — `blessed` is `[]` when empty, never `null`.
Each blessed comment, in order: **`url`, `version`, `blessed_at`**, then **`agent`, `grant`** — the
agent marker, ⛔ **omitted entirely when absent** (never written as `""`).

⚠️ **Entry order is not normalised.** The signature covers the list as the author wrote it. Sorting
before signing would mean the bytes on disk and the bytes signed could differ.

Excluded: `signature`.

```
{"version":"polis-cli-go/0.67.0","comments":[{"post":"https://alice.example/content/pub.polis.core/post/20260827/hello.md","blessed":[{"url":"https://bob.example/content/pub.polis.core/comment/20260828/re-hello.md","version":"sha256:0000000000000000000000000000000000000000000000000000000000000000","blessed_at":"2026-08-30T08:00:00Z"}]}]}
```

**An entry a user agent added** carries the marker: `agent` is the user's name for
the agent, `grant` the source URL of the grant record it acted under
([`delegation.md`](delegation.md)). Only that entry changes; an unmarked entry beside it is
byte-identical to the vector above, which is why no signature made before the marker existed stops
verifying.

```
{"version":"polis-cli-go/0.67.0","comments":[{"post":"https://alice.example/content/pub.polis.core/post/20260827/hello.md","blessed":[{"url":"https://bob.example/content/pub.polis.core/comment/20260828/re-hello.md","version":"sha256:0000000000000000000000000000000000000000000000000000000000000000","blessed_at":"2026-08-30T08:00:00Z","agent":"rosie","grant":"https://alice.example/content/pub.polis.core/attestation/20260915T120000Z-0123456789abcdef.json"}]}]}
```

⚠️ **A verifier that predates these two fields rebuilds the entry from three, so its rebuild fails.**
No such verifier was ever released — and under §6.1 the general answer is now
`unknown`, not `invalid`: a marked list reads *"signed with fields this verifier does not
understand"*, naming `agent` and `grant`.

Semantics: [`attestation.md`](attestation.md).

---

#### `pub.polis.attestation` — a claim event

Signable fields, in order: **`type`, `issuer`, `predicate`, `subject`, `payload`, `asserted`,
`generator`.**
`subject`, in order: **`type`, `id`, `version`** — `version` omitted when absent.
`payload` is omitted entirely when empty, and its keys sort lexicographically.

Excluded: `current_version`, `signature`, and `withdrawn_by` — the unsigned forward reference a
withdrawn record gains afterwards ([`attestation.md` §6](attestation.md#6-a-subject-may-be-another-attestation)).

```
{"type":"pub.polis.attestation","issuer":"https://judge.example","predicate":"pub.polis.attestation.integrity","subject":{"type":"uri","id":"https://alice.example/content/pub.polis.core/post/20260827/hello.md","version":"sha256:0000000000000000000000000000000000000000000000000000000000000000"},"payload":{"checks":"14","result":"pass"},"asserted":"2026-08-29T11:00:00Z","generator":"polis-cli-go/0.67.0"}
```

⚠️ **These vectors pin BYTES, not meaning.** Their predicate is `integrity` because it was a convenient
reserved name; neither is a well-formed integrity observation under
[`attestation.md` §4.1](attestation.md#41-integrity-is-an-observation-and-its-result-lives-in-the-payload)
(a `uri` subject, `result: pass`, no `vantage` or `observed`), and `polis attest issue` would refuse
both. ⛔ **Do not "fix" them** — a test holds these exact strings, and canonicalisation does not depend
on what a payload means.

The minimal record — no payload, no subject version, both **omitted** rather than empty:

```
{"type":"pub.polis.attestation","issuer":"https://judge.example","predicate":"pub.polis.attestation.integrity","subject":{"type":"identity","id":"https://alice.example"},"asserted":"2026-08-29T11:00:00Z","generator":"polis-cli-go/0.67.0"}
```

Semantics, including the predicate vocabulary: [`attestation.md`](attestation.md).

---

#### `pub.polis.actor` — an operator's actor registry (`registry.json`)

An operator's signed statement of which system actors it runs. Two signatures live in this file and
they are made by **different keys**, so it has two signable sequences.

**The file**, signed by the **operator's** key. Signable fields, in order:
**`v`, `operator`, `actors`, `asserted`, `generator`.**
Each entry, in order: **`domain`, `authority`, `expected_actions`, `countersignature`** — the last
omitted when absent. `actors` and `expected_actions` are `[]` when empty, never `null`.

Excluded: `signature`. There is no `current_version` in this type.

```
{"v":"pub.polis.actor-registry.v1","operator":"polis.polis.pub","actors":[{"domain":"judge.polis.pub","authority":"operator","expected_actions":["pub.polis.attestation.integrity"],"countersignature":"U1NIU0lHAAAAAWp1ZGdl"}],"asserted":"2026-09-05T14:02:00Z","generator":"polis-cli-go/0.67.0"}
```

An operator that runs no actors — note `[]`, not `null`:

```
{"v":"pub.polis.actor-registry.v1","operator":"polis.polis.pub","actors":[],"asserted":"2026-09-05T14:02:00Z","generator":"polis-cli-go/0.67.0"}
```

**An entry's `countersignature`**, signed by **that actor's own** key — the key published at
`https://<domain>/.well-known/polis`, never a key stored in the registry. Signable fields, in order:
**`v`, `operator`, `domain`, `authority`, `expected_actions`.**

Excluded: `countersignature` itself, and also **`generator` and `asserted`** — an actor consents to
*what is claimed about it*, not to which build wrote the file or when the operator last restated the
list. Including either would break every countersignature on every unrelated re-statement.

```
{"v":"pub.polis.actor-registry.v1","operator":"polis.polis.pub","domain":"judge.polis.pub","authority":"operator","expected_actions":["pub.polis.attestation.integrity"]}
```

⛔ **`v` and `operator` are inside the entry base deliberately, and dropping either is a
vulnerability rather than an untidiness.** Without `operator` the countersignature is **replayable**:
a hostile operator lists the same actor in *its* registry, pastes the countersignature across, and it
verifies — so the file says the actor agreed to be that operator's. *"The actor agrees it is listed"*
is meaningless without *"listed by whom"*.

⚠️ **The countersignature is INSIDE the file's signing base.** Excluding it would let anyone with
write access strip one, silently downgrading an entry from *"the actor agrees"* to *"we say so"*
while the file signature still verified. The order of operations is: draft the entry → the actor
countersigns it → the operator signs the file.

⚠️ **`v` here is the ARTIFACT SCHEMA version, not the signing-base version.** This type's signing
base is `pub.polis.signing-base.v1` like every other, and §2's rule still holds — the base itself
carries no marker. `pub.polis.actor-registry.v1` versions the *field set*, and this type could afford
one because it had no published artifacts when it was defined.

Semantics: [`custody.md`](custody.md).

## 6 Verifying, end to end

1. Determine the type — the file's own `type` field, or, for markdown, §4.1's discriminator.
2. Find the signing key: fetch `https://<site>/.well-known/polis` and read `public_key`. If the
   artifact predates a rotation, walk `public_key_history` ([`key-history.md`](key-history.md)).
3. Rebuild the signing base per §4 or §5.
4. Rewrap the stored signature into a PEM `SSH SIGNATURE` block if it was stored bare (§3).
5. Verify with Ed25519 over the SSHSIG pre-image of §3.
6. Optionally recompute the content hash (`current-version` / `current_version`) and check it agrees.

### The four outcomes, and why there are four

| Outcome | Meaning |
|---|---|
| **valid** | A signature was present and verified. |
| **invalid** | A signature was present and did **not** verify. |
| **unsigned** | There is no signature. |
| **unknown** | You could not check — no key, an unreachable site, a version you do not know, or a **field you do not recognise** (§6.1). |

⛔ **`unsigned` is a fact, not a failure.** Most artifacts on most sites are unsigned: signing arrived
per type over time and **nothing backfills a signature**, because backfilling would mean an operator
asserting something on the author's behalf. A verifier that reports unsigned as invalid turns an
ordinary state into an alarm.

Equally, **`invalid` is evidence, not a verdict.** Report it; do not evict, quarantine or refuse to
load on the strength of it. The likeliest cause of a failing signature is a bug in a verifier, and
the second likeliest is a file edited in place by its own author.

And if you cannot check at all — no key, unreachable site, a version you do not know — say *that*.
"Having failed to look" is not the same as "having looked and found a problem."

### 6.1 Unrecognised fields — a verifier must be tolerant

> ⛔ **A SHIPPED VERIFIER IS FROZEN.** The bases in §5 are rebuilt from a **named field list**. The
> day a writer signs a field your list does not have, your rebuild produces different bytes and your
> verifier reports `invalid` for a file its author signed correctly — and nobody can patch you. You
> are on a self-hoster's laptop, in another operator's deployment, in an archive opened in ten years.
> Two things follow, and the second is worse: a reader who has seen `invalid` on a good file learns
> to ignore `invalid`, and the format ossifies, because adding a field breaks the network.

**The rule: a verifier that meets a field it does not recognise reports `unknown`, never `invalid`.**

| Rebuild from the fields you know… | Unrecognised fields present? | Report |
|---|---|---|
| verifies | no | `valid` |
| verifies | yes | `valid`, **and say the extra fields are not covered** by the signature |
| fails | yes | `unknown` — *"signed with fields this verifier does not understand"*, naming them |
| fails | no | `invalid` |

**"Recognised" is per nesting level, including list entries.** A rule applied only to the top-level
object misses the shape this is most needed for: the blessing list's agent marker sits on a per-post
`blessed` entry, two levels down.

⚠️ **A map's KEYS are data, not schema.** The attestation `payload` is the only map in §5, and an
unrecognised payload key is exactly what an open map is for. It is signed, it verifies, and it is not
an unrecognised *field*.

⭐ **This locks nothing in.** There is no format marker, no dispatch table and no schema registry —
the recognised set is simply the field list you implement. ⛔ **Do not dispatch on `generator`**: it
teaches new readers about old files, while the danger is old readers meeting new files; it names an
implementation rather than a format; and it has been wrong in published artifacts before (§5.3's
warning about frozen derived values).

⚠️ **THE COST, STATED PLAINLY.** A tamperer can add a junk field so that `invalid` reads `unknown`,
quieting an alarm. **Nothing tampered is ever accepted** — `unknown` is never `valid`, and Law 2
leaves the judgment with the consumer — but the downgrade is real. The mitigation is **consumer
policy, not verifier behaviour**: a consumer that knows who wrote a file may treat an unrecognised
field in it as a finding, because *its own* writer never emits a field *its own* verifier lacks. That
reasoning is narrow and does not generalise — the same observation about a stranger's site is the
ordinary state of a network whose implementations ship at different times, which is precisely what
this rule exists to accommodate.

### 6.2 Rewriting — tolerant reading is not tolerant writing

§6.1 makes a reader honest about a file a newer writer made. It says nothing about what happens when
that reader then **changes** the file — and a writer that rebuilds the document from the field list
it implements drops every member it does not know, then signs what is left **with the author's key**.
The author's record is rewritten, and the signature vouches for the result.

**The rule: a writer that meets a member it does not recognise refuses to rewrite the file.** There
are exactly three outcomes, and no silent fourth:

| The file on disk… | Writer does |
|---|---|
| holds nothing unrecognised (or does not exist) | rewrite, and sign |
| holds an unrecognised member, at any nesting level | **refuse**, naming the members, and say the remedy is to upgrade |
| holds one, **and the user explicitly asks** | rewrite **unsigned**, dropping the members, and say which |

⛔ **Never preserve-and-re-sign.** Carrying the unknown member through and signing the file would have
the author's key assert bytes the signer's software could not interpret — and there is no
canonicalisation answer for it anyway: §5's field order is declaration order, and an unknown member
has no place in that sequence.

⭐ **Check the file on disk, not the value in memory.** A writer that builds its document from scratch,
or copies entries out of a loaded one, loses the unknown members before it ever reaches the write. The
refusal belongs at the write point, reading the bytes it is about to replace.

⚠️ **This is the opposite of `.well-known/polis`**, which is unsigned and therefore **preserves** what
it does not model — as do the other unsigned files polis rewrites (the witness set, the bundle
declaration, `index.jsonl`, and the private registry, webapp settings, DM keyring and message logs). Signing is the whole difference: an unsigned document asserts nothing a preserved
member could smuggle in.

## 7 Conformance checklist

A second implementation is compatible when it can:

- [ ] Reproduce every worked example in §4 and §5, byte for byte.
- [ ] Verify a live artifact fetched from any polis site against that site's published key.
- [ ] Keep a body line beginning `signature:` **inside** a post's signing base (§4.2, trap 1).
- [ ] Keep an **indented** `signature:` inside the base (§4.2, trap 2).
- [ ] Exclude `author:` for a comment and **include** it for a post (§4.1).
- [ ] Escape `<`, `>` and `&` in JSON (§5.2).
- [ ] Emit `[]` rather than `null` for an empty list (§5.1).
- [ ] Report unsigned, invalid and could-not-check as three different answers (§6).
- [ ] Report `unknown` rather than `invalid` for an artifact carrying a field it does not recognise,
      at any nesting level including list entries, and name the fields (§6.1).
- [ ] Report `valid` for an artifact whose signature verifies despite an unrecognised field, **and
      say that field is not covered** (§6.1).
- [ ] Refuse to rewrite a signed artifact carrying a member it does not recognise, naming it — and
      never write one signed having dropped or preserved such a member (§6.2).
- [ ] Bind an actor-registry countersignature to the **operator listing it**, and reject one lifted
      from another operator's registry (§5.3).

## 8 Reference implementation

| Piece | Where |
|---|---|
| Envelope, canonicalization, markdown base | `cli-go/pkg/signing/` (`signing.go`, `base.go`) |
| Tolerant reading (§6.1), refusing writing (§6.2) | `cli-go/pkg/signing/` (`tolerance.go`, `rewrite.go`) |
| Licence | `cli-go/pkg/license/license.go` |
| Follow file | `cli-go/pkg/following/following.go` |
| Tag | `cli-go/pkg/tag/tag.go` |
| Blessing list | `cli-go/pkg/metadata/blessed_signature.go` |
| Attestation | `cli-go/pkg/attestation/attestation.go` |
| Actor registry | `cli-go/pkg/actor/registry.go` |

## See also

- [`license.md`](license.md) · [`attestation.md`](attestation.md) · [`key-history.md`](key-history.md) · [`did-web.md`](did-web.md)
- [`../guides/verify-content.md`](../guides/verify-content.md) — checking a site with the shipped tooling
- [`../../general/security/security-model.md`](../../general/security/security-model.md) — threat model
