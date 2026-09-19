# Attestation — wire shapes

*for implementers, including anyone writing a second implementation*

*For* [Developers](../../README.md#building-on-polis) · [Reviewers](../../README.md#reviewing-the-security-and-identity-design) — *About* [Relationships](../../general/README.md#relationships) · [Content](../../general/README.md#content) — *Kind* [Spec](../../README.md#kinds-of-page) — *Code* [`cli-go/pkg/attestation`](../../../cli-go/pkg/attestation) — *See also* [concept](../concepts/assertions.md) · [recipe](../recipes/01-make-a-claim.md)

| Shape | Status |
|---|---|
| [The attestation record](#the-attestation-record) (`pub.polis.attestation`) | **implemented** — unreleased |
| [The signed follow file](#the-signed-follow-file) (`pub.polis.follow`) | **implemented** — unreleased |
| [The signed blessing list](#the-signed-blessing-list) | **implemented** — unreleased |

---

# The attestation record

**Type:** `pub.polis.attestation` · **Path:**
`content/pub.polis.core/attestation/<id>.json` · **Served:** publicly, as JSON

## 1 What it is

An attestation is a **claim event**: *on this date, this issuer asserted this predicate about this
subject.* One claim, one file, one signature.

That sentence decides the whole shape, so it is worth being exact about what it excludes. An
attestation is **not** a category, a rating, a score, a credential, or a review:

- **Not a category.** `pub.polis.tag` is self-directed structure an author puts on their own reading,
  and one tag file holds many targets under one timestamp. An attestation is other-directed and
  happens at a moment, so it holds exactly one subject. The two share a serialisation and nothing
  else.
- **Not a score.** There is no number and no aggregate anywhere in this format. Attestations are
  evidence; anything computed *over* them is a judgment held by whoever is asking, recomputed
  per-viewer, never signed and never stored.
- **Not a credential.** Nobody issues you anything and no authority is involved. A party signs a
  sentence with their own key; whether that is worth anything is the reader's call.

⚠️ **`issuer`, `predicate` and `subject` are the three parts of one noun.** They are not three
concepts. If you find yourself designing something that holds predicates, or a registry of subjects,
you have left the format.

**Why the same envelope as `pub.polis.tag`:** canonical JSON, a `sha256:` content version, one Ed25519
signature in SSH armour, an atomic write. Reusing a proven envelope rather than inventing a second one
is why this is a sibling type instead of a widening of tag.

## 2 The document

```json
{
  "type": "pub.polis.attestation",
  "issuer": "https://vdibart.polis.pub",
  "predicate": "pub.polis.attestation.correction",
  "subject": {
    "type": "uri",
    "id": "https://site.example/posts/20260901-claim.md",
    "version": "sha256:9f2a0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab"
  },
  "payload": {
    "note": "the figure cited was revised by the source on 2026-08-30"
  },
  "asserted": "2026-08-28T23:50:09Z",
  "generator": "polis-cli-go/0.67.0",
  "current_version": "sha256:f0be117c4e29310dc7e7ba4aba9112e8b3dc20c6999cba27669c14249dd39a0f",
  "signature": "-----BEGIN SSH SIGNATURE-----\n…\n-----END SSH SIGNATURE-----\n"
}
```

| Field | Required | Signed | Meaning |
|---|---|---|---|
| `type` | yes | **yes** | Always `pub.polis.attestation`. First field, so a record says what it is before anything else. |
| `issuer` | yes | **yes** | Base URL of the site making the claim. The verifying key is the one published *at that site*. |
| `predicate` | yes | **yes** | What is being asserted, **fully qualified** (§4). |
| `subject.type` | yes | **yes** | `uri` or `identity` (§5). A reader must accept other values. |
| `subject.id` | yes | **yes** | What the claim is about — an `https` URL either way. |
| `subject.version` | no | **yes** | Pins a `uri` subject to exact bytes: `sha256:` + 64 hex. **Omitted entirely when absent**, never `""`. |
| `payload` | no | **yes** | Predicate-specific detail. Flat, **string values only** (§3). Omitted entirely when empty. |
| `asserted` | yes | **yes** | When the claim was made. RFC 3339, `Z`, second precision. |
| `generator` | yes | **yes** | Who wrote the record — `polis-cli-go/<v>` or `polis-cli/<v>`. Stamped **before** signing. |
| `current_version` | yes | **NO** | `sha256:` of the signing base. Computed *over* the signed bytes, so it cannot be inside them. |
| `signature` | no | **NO** | Absent means **unsigned**, a legitimate state (§8). It cannot cover itself. |

### There is one timestamp, not two

`created`/`updated` would say a record can be edited. It cannot: a claim event happened once. A
correction to a claim is a **new record about the old one** (§6), not a rewrite of it.

### There is no `expires`, and its absence has a defined meaning

⚠️ **Absence means "no expiry asserted". It explicitly does NOT mean "valid forever."**

The record states *when it was asserted*; **freshness is consumer policy**, not a protocol fact. Two
readers may reasonably treat a three-year-old claim differently, and neither is wrong.

`expires` is **reserved** for a future statement of issuer *intent* — intent is not a computation, so
it may be signed. This paragraph exists now so that adding the field later does not leave every record
written before it ambiguous between *never expires* and *unknown*.

### The record's id is derived, never stored

The filename stem is `<compact asserted>-<first 16 hex of current_version>`, e.g.
`20260828T235009Z-f0be117c4e29310d`. The compact form of `asserted` is the RFC 3339 string with `-`
and `:` removed.

It is **not a field**. It is computed from the signed bytes, so it could not be inside them — and
signing a computation would be the wrong shape regardless. A verifier recomputes it, which means a
record served at a URL that disagrees with its own bytes is detectable.

## 3 The payload is string-valued, on purpose

`payload` is a **flat JSON object whose values are strings**. A count is `"1077"`, not `1077`.

This is the one place the format is narrower than JSON, and it is deliberate: **JSON number
canonicalisation is the most common interoperability failure in signed-JSON formats.** `1`, `1.0`,
`1e0` and `1.0e+0` are the same number and four different byte sequences, and getting agreement on
which one to sign is the entire reason RFC 8785 exists. Restricting the payload to strings removes the
question from this specification completely, so a stranger reproducing our bytes never has to guess.

Payload keys serialise in **lexicographic (byte) order**. That is part of the specification, not an
artifact of one implementation.

The payload's meaning is defined by the predicate, and a reader that does not know the predicate
should not try to interpret it.

## 4 Predicates

**Fully qualified, always, in the wire format.** A third party's predicate is `com.example.reviewed`.
The namespace is what lets a reader see *whose* predicate it is without recognising it — which is what
makes §7's tolerance rule meaningful.

⚠️ **In prose, the short form (`same-as`) is fine. In a file, never.**

**This table is the single source of truth for the reserved vocabulary.** Every other polis page
points here rather than restating it.

| Predicate | Subject | What it says |
|---|---|---|
| `pub.polis.attestation.same-as` | identity | this identity and that one are the same party |
| `pub.polis.attestation.integrity` | identity | an integrity **observation**: the issuer examined this site's signed artifacts against its published key, from a stated vantage, at a stated moment. ⛔ **What it found is in the payload, never in the predicate** — see [§4.1](#41-integrity-is-an-observation-and-its-result-lives-in-the-payload) |
| `pub.polis.attestation.correction` | uri **+ version pin** | this specific version of this work is corrected |
| `pub.polis.attestation.used-under-terms` | uri | I used this work, under these terms, on this date |
| `pub.polis.attestation.agent-disclosure` | identity | this identity is an automated agent, operated by X, scoped to Y — payload keys in [`custody.md` §5](custody.md#the-disclosure-record) |
| `pub.polis.attestation.endorsement` | identity | I vouch for this party |
| `pub.polis.attestation.withdrawal` | uri **+ version pin** | the record at this URL is retracted by its issuer |
| `pub.polis.attestation.custody` | identity | the issuer — an **operator** — holds this party's identity key, and says how it signs with it — payload keys in [`custody.md` §12](custody.md#12-custody-of-a-tenants-key--the-tenant-half) |
| `pub.polis.attestation.custody-grant` | identity | the issuer — a **tenant** — grants custody of its key to this operator, and says how the grant was obtained — payload keys in [`custody.md` §12](custody.md#12-custody-of-a-tenants-key--the-tenant-half) |
| `pub.polis.attestation.grant` | identity **= the issuer** | the issuer — a **user** — records what a user agent is to her: its name, who provides it, which versioned behaviours, and how the record was obtained. Revoked by `withdrawal`. ⛔ **Not custody.** Payload keys and the writer rule in [`delegation.md` §3](delegation.md#3-the-grant-record) |

⚠️ **This is a reserved list, NOT a registry.** Nothing validates against it, nothing rejects a
predicate outside it, and there is no lookup table. A registry is a thing to build once several
third-party predicates exist, and not before.

### 4.1 `integrity` is an observation, and its result lives in the payload

`integrity` names **what was examined**. It never names **what was found** — a predicate whose name
carries a polarity is a verdict built into the vocabulary, and a reader that ignored the payload would
come away with a confident reading that could be the opposite of the truth.

**Three payload keys are REQUIRED:**

| Key | Value |
|---|---|
| `result` | `verified` — every examination completed and verified · `not-verified` — at least one did not |
| `vantage` | where the issuer stood — which network position, which set of sites, which transport. Stated, never inferred |
| `observed` | when the examination completed, RFC 3339 with a `Z`, second precision. Distinct from `asserted`, which is when the record was signed |

⛔ **`result` has NO DEFAULT.** A record missing `result`, `vantage` or `observed`, or whose `result` is
any other value, is **not a well-formed integrity observation** and says nothing — ⛔ **it is never read
as `verified`.** A default is how a missing value turns a failure into a pass, silently. *(This is a
statement about the claim, not about the signature: such a record may still verify.)*

⛔⛔ **A reader that cannot read the payload MUST NOT interpret the record** — neither as a pass nor as a
failure. For this predicate §3's *“a reader that does not know the predicate should not try to
interpret the payload”* is a **safety property**: abstaining is the correct outcome.

**`not-verified` is an observation, not a verdict.** It says *“could not verify at `observed` from
`vantage`”* — never *“this site is invalid.”* Two optional keys keep the two different reasons apart,
because they are different facts:

| Key | Value |
|---|---|
| `unreachable` | a count: artifacts that could not be fetched. ⚠️ **Unreachable says nothing about the artifact** |
| `mismatched` | a count: artifacts that were fetched and did not verify |
| `checks` | the examinations that did not complete-and-verify, comma-separated, as the issuer names them |
| `consecutive` | a count: consecutive examinations with this `result` when the record was issued |

⭐ **A `verified` observation asserts a MOMENT, not an interval.** It says nothing about the time
between two observations; a reader composes those and can see the gap.

⚠️ **These are WRITER rules.** `polis attest issue` refuses an `integrity` record without the three
required keys. Readers still verify and render any record (§7).

## 5 Subjects

Two types, `uri` and `identity`, and the referent lives under a **uniform `id` key** in both cases.

| Type | `id` is | Pin |
|---|---|---|
| `uri` | a work, a comment, or **another attestation** — anything with a permanent URL | may carry `version` |
| `identity` | a party, named by their site's base URL | never |

**Why one `id` key rather than `uri:` / `identity:`.** A reader meeting a subject type it does not
recognise must still render the claim — issuer, subject, date — and it can only do that if the
referent sits under a key it can find *without understanding the type*. Per-type key names would make
an unknown subject type unreadable, which is the failure §7 forbids one level up.

### The pin is inside the signature, and that is the whole point

If `subject.version` were outside the signature, anyone could repoint a claim at different bytes and
it would still verify. That would destroy the one property a pin exists for.

So: **pin a `correction` and the claim is permanently scoped to the bytes the issuer actually saw.**
Republish the work and the attestation still visibly points at the old version. The correction and
the edit are both part of the permanent record, and neither can quietly erase the other.

## 6 A subject may be another attestation

A record has a permanent URL and a content hash, so it satisfies the `uri` subject type like anything
else. Nothing special is needed and nothing more is added.

That single fact is what gives the format:

| | How |
|---|---|
| **Counter-signing** | **two independent records referencing each other**, each signed by its own key — never one file with two signatures |
| **Withdrawal** | a record whose subject is the record being withdrawn |
| **Disputes, and corrections of corrections** | the same move, recursively |
| **Delegation** | an agent signs as itself; a separate grant record says the principal authorised it, so nobody forges authorship |

⚠️ **Withdrawal is a signed record, never a file deletion.** A `404` must never be readable as a
retraction: that teaches the network to read an outage, a moved site or a lapsed domain the same way,
and the evidence is genuinely gone. Unreachability is evidence for **durability** — keep serving the
cache — never for eviction.

**The predicate is `pub.polis.attestation.withdrawal`**, and its subject is the withdrawn record's URL,
**pinned to that record's `current_version`**. A claim event is immutable, so the pin costs nothing and
says exactly which bytes were retracted.

Three properties follow, and an implementer needs all three:

1. **Only the issuer may withdraw**, and ⚠️ **the check is that the record VERIFIES against the
   withdrawing site's key — not that its `issuer` field matches.** That field is a string anyone can
   write, and a record can sit in a site's directory without being theirs (cached, copied,
   hand-written). Trusting the field lets a site publish a record *claiming* to be another party's
   retraction, signed with its own key. A reader following §9 resolves the key from `issuer`, so such
   a record never verifies — it is not a working impersonation, it is a permanently invalid record
   that reads as tampering. **The rule is: you may retract what you can prove you signed.**
   A record signed by you saying *someone else's* claim is retracted is a different act — your opinion
   about their claim — and needs a different predicate.
2. **A withdrawal cannot itself be withdrawn**, and **there is at most one withdrawal per claim.**
   Retracting a retraction says nothing and starts a chain no reader can interpret; a *second*
   withdrawal of the same claim says nothing the first did not. ⚠️ Both matter more than they look,
   because withdrawals are **permanent and unwithdrawable** — a duplicate is litter nobody can ever
   clear, so the second attempt is refused rather than silently written.
3. **The withdrawn record stays published and stays valid.** It still verifies; nothing about its
   signed bytes changes. A reader learns it was retracted **by finding the withdrawal**, not by failing
   to fetch the original. ⚠️ **A verifier must never treat a fetch failure as a retraction** — see above.

**The forward reference.** When a record is withdrawn, its issuer adds one **unsigned** top-level field
to it: `withdrawn_by`, the URL of the withdrawal record.

```json
{ "type": "pub.polis.attestation", "…": "…",
  "current_version": "sha256:…",
  "withdrawn_by": "https://alice.example/content/pub.polis.core/attestation/20260914T120000Z-5d1c….json",
  "signature": "-----BEGIN SSH SIGNATURE-----\n…" }
```

- **It exists because a reader holding only the record has no index to search.** A discovery service
  can look for a withdrawal in its own rows; a stranger who fetched one record cannot. The pointer
  makes a withdrawn record self-describing.
- ⭐ **It is DISCOVERY, not proof.** Follow it, verify the withdrawal it names against the issuer's
  key (§9), and check that its subject is this record's URL pinned to this record's `current_version`.
  A pointer that fails any of those says nothing — discard it.
- **It is outside the signing base** (§8), so adding it changes neither the signature, nor
  `current_version`, nor the record's id or URL.
- ⚠️ **Its absence proves nothing.** It is unsigned, so whoever can write the file can remove it — and
  whoever can write the file is the party holding the key. A reader that needs certainty looks for the
  withdrawal itself.
- ⛔ **An implementation that rewrites record files must preserve it.** A writer that parses into a
  fixed set of fields and serialises them back drops anything it did not declare, which here would
  silently un-withdraw a record.

Polis ships `polis attest withdraw <id>`, which writes the new record and removes nothing. **There is
no delete operation, and there should not be one.**

### `same-as` is a pair, and the pair is the argument

`did:web` has one widely-known hole: your identity *is* your domain, so losing the domain loses the
identity. The DID spec's answer, `alsoKnownAs`, is **self-asserted only** — whoever hijacks the old
domain can claim it just as loudly.

Two records fix that:

```
old site: issuer=https://old.example  predicate=…same-as  subject.id=https://new.example
new site: issuer=https://new.example  predicate=…same-as  subject.id=https://old.example
```

Each is signed by its own site's key and verifies against its own site's published key. **Half a pair
proves nothing** — the forward record does not verify against the new key, and that asymmetry is
exactly what a self-assertion cannot offer. Together they are a portable identity migration with no
registry, no ledger, and no authority to ask.

## 7 ⚠️ Tolerance: what a reader MUST do with something it does not recognise

**This is the rule that decides whether the format can ever grow, and it is not optional.**

A record whose **predicate** or **subject type** you do not recognise **MUST**:

1. **verify normally** — the signature is over bytes, and bytes do not need to be understood;
2. **parse normally**, preserving the unrecognised values verbatim;
3. **render as an opaque claim**, with issuer, subject and date legible;
4. **be ignored** by anything computing over records.

It **MUST NOT** fail verification, fail parsing, or be dropped.

A reader that rejects unknown predicates means **no predicate can ever be added without breaking every
deployed reader** — so nobody outside the original implementation can ever build on the format. The
same precedent applies here as everywhere in this area: IETF AIPREF
(`draft-ietf-aipref-vocab-06` §6.4) requires unknown labels to be **ignored**, not treated as failure.

**Writers are held to more than readers, and that asymmetry is the design.** A polis implementation
refuses to *write* a bare predicate or an unmodelled subject type, because writing malformed records
puts junk on the network permanently. It refuses to *reject* either on read.

## 8 The signing base

**This section is the specification.** Reproduce these bytes exactly or your signatures will not
verify against a polis implementation, and vice versa.

> **The shared rules live in [`signing-base.md`](signing-base.md)** — the signature envelope, the
> canonical-JSON rules every signed JSON type obeys, the HTML-escaping trap, and the field lists for
> all eight signed shapes side by side. Read that first if you are implementing more than one type;
> this section is the same base stated in the attestation's own terms.

The signature covers a **canonical JSON serialisation of the signable fields**, in this order:

```
{"type":…,"issuer":…,"predicate":…,"subject":{"type":…,"id":…,"version":…},"payload":{…},"asserted":…,"generator":…}
```

`subject` serialises its present fields in the order `type`, `id`, `version`.

Rules, all of which matter:

1. **Compact.** No insignificant whitespace, no indentation, no trailing newline.
2. **Field order is fixed** as given above. It is not alphabetical, and it is not whatever order your
   JSON parser hands you.
3. **`signature` is excluded.** It cannot cover itself. Serialise as though the field were not there —
   *not* as `"signature":""`.
4. **`current_version` is excluded.** It is the sha256 **of these bytes**, so it cannot be among them.
   **`withdrawn_by` is excluded** too — it is a forward reference added after a record is withdrawn
   (§6), and a signed region cannot grow.
5. **`payload` and `subject.version` are omitted entirely when empty**, never emitted as `{}` or `""`.
   An absent payload and a present-but-empty one must have one byte sequence, not two.
6. **Payload keys sort lexicographically**, by byte.
7. **UTF-8.** Note that Go's `encoding/json` escapes `<`, `>` and `&` as `\u003c`, `\u003e` and
   `\u0026`. A URL containing them — rare, but legal — must be escaped the same way, or the bytes
   diverge.

Worked example — this record:

```json
{ "type": "pub.polis.attestation",
  "issuer": "https://vdibart.polis.pub",
  "predicate": "pub.polis.attestation.same-as",
  "subject": { "type": "identity", "id": "https://vincent.example.com" },
  "asserted": "2026-08-28T00:00:00Z",
  "generator": "polis-cli-go/test" }
```

produces exactly this signing base:

```
{"type":"pub.polis.attestation","issuer":"https://vdibart.polis.pub","predicate":"pub.polis.attestation.same-as","subject":{"type":"identity","id":"https://vincent.example.com"},"asserted":"2026-08-28T00:00:00Z","generator":"polis-cli-go/test"}
```

*(That string is checked against the implementation's golden-bytes test mechanically, on every test
run. It is not transcribed by hand.)*

Those bytes are then signed with the issuing site's Ed25519 identity key using the **SSH signature
format** (`ssh-keygen -Y sign`, namespace `file`, hash `sha512`) — the same envelope polis uses for
posts, comments and the follow file. The armored result goes in `signature`.

Then, and only then, `current_version` is set to `"sha256:" + hex(sha256(signing base))`.

> ⚠️ **The order is: canonicalise → hash into `current_version` → sign the canonical bytes.** Both
> derived values are computed from the same bytes, and neither is inside them. An implementation that
> stamps `generator` — or anything else in the signable set — *after* signing produces a record that
> writes cleanly, parses cleanly, and never verifies again.

> ⚠️ **Because the base is rebuilt from the parsed fields rather than hashed off the raw file,
> unknown top-level JSON fields are not covered by the signature.** A verifier must not infer that
> everything in the file was signed — only the fields listed above were. And the converse: if the
> rebuilt base does **not** verify and the record carries fields the verifier does not recognise, the
> signature may cover fields it cannot rebuild, so the result is `unknown`, never `invalid`
> ([`signing-base.md` §6.1](signing-base.md#61-unrecognised-fields--a-verifier-must-be-tolerant)).

## 9 Verifying

1. Read `issuer`.
2. Fetch `https://<issuer>/.well-known/polis` and read `public_key` and `public_key_history`.
3. Rebuild the signing base from the document per §8.
4. Verify `signature` over those bytes with the current key.
5. **Only if that fails**, resolve a retired key through the issuer's published
   [key history](key-history.md#the-resolution-rule), using the record's **`asserted`** as the claimed
   signing time, and verify against that key. Report which key verified it — a retired-key pass means
   *"verifies against the key that was current at the time the record claims"*, a weaker statement
   than a current-key pass, because `asserted` is the record's own claim.
6. Optionally recompute `current_version` and the record id, and check they agree with where the
   record was served from.

Verify against the **published** key and history, not a locally cached one: that is what any third party
on the network would use, so it answers the same question everyone else is asking. Without step 5,
every record an issuer signed reads `invalid` the day it rotates its key.

## 10 The four states, and why they are four

| State | Meaning | What a verifier reports |
|---|---|---|
| `valid` | signature present, verifies | valid |
| `unsigned` | **no `signature` field** | **unsigned — a fact, not a defect** |
| `invalid` | signature present, does not verify | a finding |
| `unknown` | could not check — no published key reachable, or the signature does not verify and the record carries fields this verifier does not recognise (§8) | not a finding |

⚠️ **`unsigned` and `invalid` are different facts and must never be collapsed.** ⚠️ **`unknown` is not
`invalid` either** — having failed to look is not the same as having looked and found a problem.

**A record whose signature does not verify is still readable.** Report it; do not hide the claim. A
wrong signature is evidence, and the likeliest cause is a bug or a mid-flight format change rather
than an attacker. Whether it is disqualifying is a judgment for whoever is asking.

## 11 Publishing and discovery

A record is served publicly as JSON at its content path:

```
https://<issuer>/content/pub.polis.core/attestation/<id>.json
```

That URL is what a countersignature, a correction, or a withdrawal points at, which is why it is the
**content path** and not a rendered mount — a reference to a URL that 404s is not a reference.

A site that is registered with a discovery service also announces each record there, with `version`
set to `current_version` — that is how the DS tells a changed row from an unchanged one — and metadata
carrying `predicate`, `subject`, `subject_type` and, when present, `subject_version`.

⚠️ **The DS is an index, not an authority.** Everything in it is re-derivable from the records
themselves, and a consumer that trusts a DS row it has not verified against the issuer's key has
skipped the only step that matters.

---

## The signed follow file

**Type:** `pub.polis.follow` · **Path:** `content/pub.polis.core/follow/following.json` ·
**Served:** publicly, as JSON

### 1 Why this file is signed

A follow list is not decoration. It is the graph every trust computation in polis weights by — *"nine
of the twelve people you follow have vouched for this author"* is a claim computed over this file. An
unsigned roster is editable by anything that can write to the disk, which puts a hole directly under
the trust layer: alter the graph and you alter every conclusion drawn from it, undetectably.

**It is authored, not derived**, and that distinction decides everything else here. Your action puts
an entry in it; it is the source of truth the discovery service mirrors; nothing writes it back from
DS state. (The *followers* list is the opposite — a genuine projection of data you do not control, and
it is not signed, because you did not author it.) So the signature is you asserting *these are my
follows* — a first-class claim, not a checksum over generated output.

### 2 The document

```json
{
  "version": "polis-cli-go/0.67.0",
  "following": [
    {
      "url": "https://alice.example",
      "added_at": "2026-08-28T00:00:00Z",
      "site_title": "Alice's Blog",
      "author_name": "Alice"
    },
    {
      "url": "https://bob.example",
      "added_at": "2026-08-28T00:01:00Z"
    }
  ],
  "signature": "-----BEGIN SSH SIGNATURE-----\n…\n-----END SSH SIGNATURE-----\n"
}
```

| Field | Required | Meaning |
|---|---|---|
| `version` | yes | ⚠️ **The generator string**, not a document version — `polis-cli-go/<v>` or `polis-cli/<v>`. A historical misnomer, kept because the field is published and widely read. **It is part of the signed content**, and it is **re-stamped by the writer on every write, before signing** — so it names whoever last wrote the file, and an implementation that stamps it *after* signing produces a file that parses and never verifies. |
| `following` | yes | The roster. May be `[]`. |
| `following[].url` | yes | The followed site's base URL. |
| `following[].added_at` | yes | RFC 3339, `Z`, second precision. |
| `following[].site_title` | no | Cached display metadata. **Omitted entirely when empty** — not `""`. |
| `following[].author_name` | no | As above. |
| `signature` | no | Absent means **unsigned**, which is a legitimate state (§5). |

There is deliberately **no `current_version` hash field**, though sibling types such as
`pub.polis.tag` carry one. That field exists so the discovery service can tell when a registered
content row changed; the follow file is never registered with the DS as content — the DS sees only
aggregate follow *events* — so the field would have no consumer, would sit outside the signature, and
would be recomputable from the file anyway. **Do not add one for symmetry.**

### 3 The signing base

**This section is the specification.** Reproduce these bytes exactly or your signatures will not
verify against a polis implementation, and vice versa.

The signature covers a **canonical JSON serialisation of the signable fields**, which are `version`
and `following`, **in that order**:

```
{"version":<version>,"following":[<entry>,…]}
```

Each entry serialises its present fields in this order: `url`, `added_at`, `site_title`,
`author_name`.

Rules, all of which matter:

1. **Compact.** No insignificant whitespace, no indentation, no trailing newline.
2. **`signature` is excluded.** It cannot cover itself. Serialise as though the field were not there —
   *not* as `"signature":""`.
3. **Field order is fixed** as given above. It is not alphabetical, and it is not the order a JSON
   parser happens to hand you.
4. **Empty optional entry fields are omitted**, not emitted as `""`.
5. **An empty roster is `[]`, never `null`.** A site that follows nobody has exactly one byte
   sequence.
6. **UTF-8.** Note that Go's `encoding/json` escapes the characters `<`, `>` and `&` as
   `\u003c`, `\u003e` and `\u0026`. A URL containing them — rare, but legal — must be escaped
   the same way, or the bytes diverge.

Worked example — this document:

```json
{ "version": "polis-cli-go/test",
  "following": [ { "url": "https://alice.example",
                   "added_at": "2026-08-28T00:00:00Z",
                   "site_title": "Alice" },
                 { "url": "https://bob.example",
                   "added_at": "2026-08-28T00:01:00Z" } ] }
```

produces exactly this signing base:

```
{"version":"polis-cli-go/test","following":[{"url":"https://alice.example","added_at":"2026-08-28T00:00:00Z","site_title":"Alice"},{"url":"https://bob.example","added_at":"2026-08-28T00:01:00Z"}]}
```

Those bytes are then signed with the site's Ed25519 identity key using the **SSH signature format**
(`ssh-keygen -Y sign`, namespace `file`, hash `sha512`) — the same envelope polis uses for posts and
comments. The armored result goes in `signature`.

> ⚠️ Because the base is rebuilt from the parsed fields rather than hashed off the raw file, **unknown
> top-level JSON fields are not covered by the signature.** A verifier must not infer that everything
> in the file was signed — only `version` and `following` were. If the base does **not** verify and the
> file carries fields the verifier does not recognise, the result is `unknown`, never `invalid`
> ([`signing-base.md` §6.1](signing-base.md#61-unrecognised-fields--a-verifier-must-be-tolerant)).

### 4 Verifying

1. Fetch `https://<site>/.well-known/polis` and read `public_key`.
2. Rebuild the signing base from the document per §3.
3. Verify `signature` over those bytes with that key.

Verify against the **published** key, not a locally cached one: that is the key any third party on the
network would use, so it answers the same question everyone else is asking.

⚠️ **The current key only.** The file carries no signing time, so a key history has no moment to resolve
a retired key for: a follow file signed before its site rotated reads `invalid` until its author next
follows or unfollows someone, which re-signs it with the current key.

### 5 The four states, and why they are four

| State | Meaning | What a verifier reports |
|---|---|---|
| `valid` | signature present, verifies | valid |
| `unsigned` | **no `signature` field** | **unsigned — a fact, not a defect** |
| `invalid` | signature present, does not verify | a finding |
| `unknown` | could not check — no published key reachable, or the signature does not verify and the file carries fields this verifier does not recognise (§3) | not a finding |

⚠️ **`unsigned` and `invalid` are different facts and must never be collapsed.** Signing happens when
the user next follows or unfollows someone, so a roster nobody has touched since signing shipped
legitimately has no signature — for a long while that is the *common* case. An implementation that
reads absent-as-failure reports a healthy network as broken.

⚠️ **`unknown` is not `invalid` either.** Having failed to look is not the same as having looked and
found a problem.

### 6 A failing signature is evidence, not a verdict

**A follow file whose signature does not verify is still loaded and still used.** Report it; do not
drop follows, do not refuse the read, do not attempt repair.

This is not leniency. A follow list is how a person reaches their network, so refusing to load it on a
signature failure disconnects them — as a *security response*, on evidence that most often points at a
software bug or a mid-flight format change rather than an attacker. Whether a failing signature is
disqualifying is a judgment for whoever is asking, and the protocol does not get to make it for them.

**Equally: nothing may re-sign the file on the author's behalf.** The follow list is authored, so an
operator or an automated agent signing it would be asserting under someone's own key that they follow
people they may not follow. Regenerating a *derived* artifact is maintenance; signing an *authored*
one is forgery.

---

## The signed blessing list

**Type:** none in the file — it carries no `type` member (operator tooling labels it `pub.polis.comment.blessing_index`) · **Path:**
`content/pub.polis.core/comment/blessed.json` · **Served:** publicly, as JSON

### 1 Why this file is signed

A blessing list is what a site says it has admitted onto its own pages. Comments arrive from other
people, and this file is the author's record of which of them they let in and at which version. An
unsigned list is editable by anything that can write to the disk, and the consequence is specific:
**you can put words on someone's site under their name** — a comment they never blessed appears
beside their post, and the version pin that would have shown it was edited after blessing can be
rewritten in the same pass.

**It is authored, not derived.** The causality runs one way — the blesser decides, the local record
is written, and the discovery service learns afterward. It never runs backwards. So this file is a
source, and the signature is the author asserting *these are the comments I have blessed*.

⚠️ **Signing it is not back-signing.** The claim is *"this is my current list"*, which is true of a
file written today no matter when each entry was granted. It does **not** assert *"I signed each of
these at the time"*, and no implementation should read it that way.

⚠️ **The DS row is not retired and is not a rival source of truth.** The DS is how the network
learns a blessing happened, and the event stream is where the history lives — a denial cannot be
laundered back into review by republishing. Truth about *what is blessed now* is this file.

### 2 The document

```json
{
  "version": "polis-cli-go/0.67.0",
  "comments": [
    {
      "post": "posts/20260101/hello-world.md",
      "blessed": [
        {
          "url": "https://bob.example/content/pub.polis.core/comment/20260101/c1.md",
          "version": "sha256:4abfd339…",
          "blessed_at": "2026-01-01T00:00:00Z"
        }
      ]
    }
  ],
  "signature": "-----BEGIN SSH SIGNATURE-----\n…\n-----END SSH SIGNATURE-----\n"
}
```

| Field | Required | Meaning |
|---|---|---|
| `version` | yes | ⚠️ **The generator string**, not a document version — the same historical misnomer as the follow file's, kept for the same reason. **It is part of the signed content** and is **re-stamped by the writer on every write, before signing.** Stamping it after signing produces a file that parses and never verifies. |
| `comments` | yes | Grouped by the post replied to. May be `[]`. |
| `comments[].post` | yes | Site-relative path of the post the blessed comments reply to. |
| `comments[].blessed` | yes | The blessed comments for that post. May be `[]`. |
| `comments[].blessed[].url` | yes | The blessed comment's URL, on its own author's site. |
| `comments[].blessed[].version` | yes | ⚠️ **The version pin — the load-bearing field.** It records *which version* was blessed, so divergence from the comment's live `current_version` is the canonical **"edited since blessing"** signal. An empty pin means *cannot tell*, which readers must not report as *not edited*. |
| `comments[].blessed[].blessed_at` | yes | RFC 3339, `Z`, second precision. |
| `comments[].blessed[].agent` | no | The **agent marker**: the user's name for the user agent that added this entry under the user's grant (e.g. `rosie`). **Omitted entirely on an entry the user added** — never `""`. See [`delegation.md` §5.2](delegation.md#52-a-blessing-list-entry--blessedjson). |
| `comments[].blessed[].grant` | no | The source URL of the grant record the agent acted under. Present exactly when `agent` is. |
| `signature` | no | Absent means **unsigned**, a legitimate state (§5). |

⚠️ **Nothing may reconstruct this file from discovery-service state.** The DS stores *which* comment
was blessed and never *which version* — so a rebuild from DS records silently erases every pin, and
with it the edit signal, and the loss is unrecoverable because the data was never there to fetch.
Recovering an entirely absent file from the DS is legitimate; the recovered entries must carry an
**empty** pin rather than an invented one, because filling it from the comment's current version
would claim the author blessed whatever it says today.

### 3 The signing base

**This section is the specification.** Reproduce these bytes exactly or your signatures will not
verify against a polis implementation, and vice versa.

The signature covers a **canonical JSON serialisation of the signable fields**, which are `version`
and `comments`, in that order, with `signature` excluded — it cannot cover itself.

```
{"version":"<generator>","comments":[{"post":"<path>","blessed":[{"url":"<url>","version":"<pin>","blessed_at":"<ts>"[,"agent":"<name>","grant":"<url>"]}, …]}, …]}
```

- Compact JSON: **no indentation, no spaces after `:` or `,`, no trailing newline.**
- Field order is the order above, at both levels. `url`, `version` and `blessed_at` are always present,
  even when empty; ⛔ **`agent` and `grant` are omitted entirely when absent**, so an entry without the
  marker has exactly the bytes it had before the marker existed.
- A nil list and an empty list must serialise identically, as `[]` — at the `comments` level and at
  each `blessed` level. *"I have blessed nothing"* has one byte sequence, not two.
- **Entry order is not normalised.** The signature covers the list as the author wrote it. Sorting
  before signing would let the bytes on disk differ from the bytes signed.

The reference implementation is `canonicalBlessedJSON` in `cli-go/pkg/metadata/blessed_signature.go`.
The byte-exact vectors, with and without the marker, are in [`signing-base.md` §5.3](signing-base.md#the-blessing-list-blessedjson).

### 4 Verifying

1. Fetch `https://<site>/.well-known/polis` and read `public_key`.
2. Rebuild the signing base from the document per §3.
3. Verify `signature` over those bytes with that key.

Verify against the **published** key, for the same reason the follow file does: it is the key any
third party on the network would use. ⚠️ **The current key only**, for the follow file's reason: the
list carries no signing time, so it is re-signed with the current key at its author's next blessing
decision. Tolerance of unrecognised fields is the follow file's too (§3 there).

### 5 The four states, and why they are four

Identical to the follow file's (§5 above), and for a sharper reason: **most sites will be `unsigned`
for a long time.** A blessing list is only re-signed when its author next blesses or unblesses
something, and every file written before this shipped has no signature at all. An implementation that
reads absent-as-failure reports the entire network as broken on its first sweep.

A fleet-level census must therefore count `unsigned` **separately** from `invalid`. Adding them
together makes a network behaving exactly as designed look like an outage.

### 6 A failing signature is evidence, not a verdict

**A blessing list whose signature does not verify is still loaded and still rendered.** Report it; do
not hide the comments, do not refuse the read, do not attempt repair. The likeliest cause is a bug or
a mid-flight format change, and whether a failing signature is disqualifying is a judgment for
whoever is asking.

**Equally: nothing may re-sign the file on the author's behalf.** Operator actors that reconcile this
file — repairing the render index, evicting a denied comment — write it **unsigned**, clearing any
stale signature rather than re-signing. That is not a gap: an unsigned write degrades to a *fact*,
where keeping a signature over changed content manufactures a false `invalid`. And an actor signing
an authored file under the tenant's key would be forging authorship, not repairing state.

⭐ **The line is WHOSE SOFTWARE HOLDS THE KEY, not whether a write is a repair** *(amended 2026-09-18)*. The paragraph above is about an **operator actor** — Chaplain, Medic, Clerk —
reconciling a tenant's file from outside. It is **not** about the author's own instance.

- **An operator actor: unsigned, always.** It is reconciling someone else's file, and a signature it
  makes is a claim the author never made.
- ⭐ **The author's own instance: SIGNED.** When the owner's server applies the owner's own decision
  with the owner's own key — a deny it recorded, a comment it unpublished — an unsigned write would
  *drop* a signature the author is entitled to, leaving the list weaker than the author left it. The
  key is on that machine because the author put it there; using it to record the author's own act is
  not forging authorship, it is the instance doing its job. Rosie's grant model rests on exactly this
  distinction, and it is marked in the signed bytes where an agent decided.

⚠️ **So "who wrote it" is the wrong question and "whose key, on whose instance, applying whose
decision" is the right one.** ⛔ A write that fails all three remains unsigned.

### 7 ⭐ List or document? — the rule this file settled

Both this file and the follow file are **lists**. `same-as`, `correction` and `used-under-terms` are
**documents** (attestations). The distinction is not stylistic and it decides which shape a new
signed thing should take:

| | **List** | **Document** |
|---|---|---|
| What is meaningful | **the set** | **the individual act** |
| Enumerable from the artifact alone | ✅ yes — fetch one file | ⛔ no — needs an index |
| Does anyone need to cite one entry, permanently and alone? | **no** | **yes** |
| Examples | `following.json`, `blessed.json`, tag files | `same-as`, `correction`, `used-under-terms` |

**The mechanism is enumeration.** A list is self-enumerating: fetch one file and you have every
entry. Scattered documents are not — answering *"everything Alice has blessed"* needs something that
indexes them, and that something is a service. **Splitting a list into per-entry documents therefore
ADDS a central dependency rather than removing one**, and publishing a list of the documents is a
list again with extra steps.

**Nobody needs to cite "Alice's blessing of Bob's comment #47" as a standalone permanent artifact.**
That is what separates it from a correction or an identity claim, where independent citation is the
entire point.

⚠️ **Apply this test before reaching for the attestation type.** Attestations are a good hammer and
not everything is that nail.

## See also

- [`../concepts/assertions.md`](../concepts/assertions.md) — what an attestation *is*, for a reader who is not implementing one
- [`../guides/attest.md`](../guides/attest.md) — issuing and reading one with the polis CLI
- [`../concepts/trust.md`](../concepts/trust.md) — why edges are signed at all
- [`../../general/concepts/content-types.md`](../../general/concepts/content-types.md) — where this file sits in the data model
- [`../../general/security/security-model.md`](../../general/security/security-model.md) — the signature envelope and threat model
- [`../../general/concepts/content-system.md`](../../general/concepts/content-system.md) — the version pin and the "edited since blessing" signal
