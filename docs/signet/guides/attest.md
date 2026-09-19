# Make a claim about someone else's work

*For* [Writers](../../README.md#writing-on-polis) · [Developers](../../README.md#building-on-polis) — *Kind* [Guide](../../README.md#kinds-of-page) — *Component* [CLI](../../cli/README.md) — *See also* [concept](../concepts/assertions.md) · [spec](../spec/attestation.md) · [recipe](../recipes/01-make-a-claim.md)

Everything else polis signs is about **your own** content — this is yours, here are your terms. An
attestation points outward: *on this date, I asserted this about that.*

This is the practical guide. The format is specified in
[spec/attestation.md](../spec/attestation.md); what an attestation *is* and why it is not a rating is
in [concepts/assertions.md](../concepts/assertions.md).

---

## What you get

A single signed JSON file, published at a permanent URL on your site, that anyone can fetch and check
against your published key — forever, without asking you, and without a service in the middle.

It is worth stating the limit up front, the same way the licence guide does: **this is evidence, not
enforcement.** Nothing is compelled by a signature. What you get is a claim that is dated, provable,
scoped to exact bytes if you want it to be, and unambiguously yours.

## The sentence

Every attestation is the same sentence with different values:

```
<issuer> asserts <predicate> about <subject>
```

- **issuer** — your site. Taken from `POLIS_BASE_URL`; you never type it.
- **predicate** — what you are asserting.
- **subject** — a *work* (`uri`) or a *party* (`identity`).

Plus an optional **payload** for detail the predicate calls for, and the date, which is filled in for
you.

## Issuing one

```bash
polis attest issue \
  --predicate pub.polis.attestation.endorsement \
  --subject https://maya.example \
  --subject-type identity
```

```
[✓] Attested pub.polis.attestation.endorsement about https://maya.example
    https://you.example/content/pub.polis.core/attestation/20260828T235009Z-0cc750dc19373d0e.json
```

That URL is the record. It is public, it is JSON, and it is what anyone else's tooling will fetch.

### Predicates are written out in full

In conversation, *"a same-as attestation"* is fine. In the file, always the fully-qualified name:

| Short, in prose | In the file |
|---|---|
| same-as | `pub.polis.attestation.same-as` |
| integrity | `pub.polis.attestation.integrity` |
| correction | `pub.polis.attestation.correction` |
| used-under-terms | `pub.polis.attestation.used-under-terms` |
| agent-disclosure | `pub.polis.attestation.agent-disclosure` |
| endorsement | `pub.polis.attestation.endorsement` |
| withdrawal | `pub.polis.attestation.withdrawal` — ⛔ written only by `polis attest withdraw` (below); `issue` refuses it |

The namespace is what lets someone who has never heard of your predicate still see whose it is. The
[spec](../spec/attestation.md#4-predicates) says what each one means, and that table is the only
place they are defined.

**You are not limited to the reserved list.** A predicate of your own looks like `com.yourdomain.reviewed`,
and polis will issue it, publish it and verify it exactly like the reserved ones. Readers that do not
know it must still verify and display it — that rule is in the spec, so building on this does not
require anyone's permission.

## Pinning a claim to exact bytes

This is the feature worth learning, and it takes one extra flag.

A claim about a URL is a claim about something that can change. Pin it, and your claim is permanently
scoped to the bytes you actually saw:

```bash
polis attest issue \
  --predicate pub.polis.attestation.correction \
  --subject https://site.example/posts/20260901-claim.md \
  --subject-version sha256:9f2a0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab \
  --payload "note=the figure cited was revised by the source on 2026-08-30"
```

If that post is later edited, your attestation **still visibly points at the old version.** The
correction and the edit are both part of the record, and neither can quietly erase the other.

The pin is inside your signature, so nobody — including you — can repoint the claim at different bytes
without invalidating it.

> **Where to get the hash.** For a post or comment it is the `current-version` in the work's own signed
> frontmatter, which `polis preview <url>` shows for a remote work. For an attestation it is the
> record's `current_version` field.

## Payloads

`--payload key=value`, repeatable, for whatever the predicate calls for:

```bash
polis attest issue \
  --predicate pub.polis.attestation.used-under-terms \
  --subject https://site.example/posts/20260901-essay.md \
  --payload "use=ai-input" \
  --payload "terms=https://site.example/license" \
  --payload "on=2026-09-01"
```

Values are **strings** — a count is `"1077"`. That restriction exists so the bytes you sign are
reproducible by anyone, in any language, without arguing about how to serialise a number. Only the
first `=` separates, so a value can contain more.

## Identity continuity — the one that needs two sites

If you move domains, you can prove the new identity is you, with no registry and no authority
involved. It takes **two records, one from each site**:

```bash
# from the old site
POLIS_BASE_URL=https://old.example polis attest issue \
  --predicate pub.polis.attestation.same-as \
  --subject https://new.example --subject-type identity

# from the new site
POLIS_BASE_URL=https://new.example polis attest issue \
  --predicate pub.polis.attestation.same-as \
  --subject https://old.example --subject-type identity
```

Each verifies against its own site's published key, which is exactly why the pair means something:
**half a pair proves nothing.** Anyone who seizes your old domain can assert whatever they like from
it, and without the matching record from the new side it stays an unsupported claim.

⚠️ **Issue the forward half while you still control the old domain.** After you lose it you cannot
sign anything with its key, and the pair can never be completed.

## Reading them back

```bash
polis attest list
```

```
  20260828T235009Z-0cc750dc19373d0e  pub.polis.attestation.endorsement → https://maya.example
  20260828T235009Z-f0be117c4e29310d  pub.polis.attestation.correction → https://site.example/posts/20260901-claim.md
```

The ids sort by date, because that is what they start with.

```bash
polis attest show 20260828T235009Z-f0be117c4e29310d
```

shows the whole claim plus the signature status. Add `--json` to either for scripting.

## Checking your own records

```bash
polis attest verify
```

```
  [✓] 20260828T235009Z-0cc750dc19373d0e  valid
  [✓] 20260828T235009Z-f0be117c4e29310d  valid
```

This checks each record against the key in **your site's `.well-known/polis`** — the file a stranger
fetches — rather than the key pair under `.polis/keys`, so it answers the same question everyone else is
asking.

Four results, and the differences matter:

| | Meaning |
|---|---|
| `valid` | it verifies |
| `unsigned` | there is no signature. A fact, not a problem |
| `invalid` | there is a signature and it does not verify. **Worth chasing** |
| `unknown` | it could not be checked — your site's `.well-known/polis` is missing, unreadable, or names no key |

The command exits non-zero only for `invalid`. Having failed to look is not the same as having looked
and found a problem.

> ⭐ **After rotating your key**, records signed with the old one still report `valid`: your site
> publishes its key history, and `polis attest verify` and `polis validate` both check a record
> against the key that was current at its `asserted` time. Each says so when a retired key did it — a weaker claim, because `asserted`
> is your own statement. See [recipe 13](../recipes/13-verify-a-sites-attestations.md).

## What you cannot do, on purpose

**There is no `polis attest delete`. There is `polis attest withdraw`, and it adds a record.**

```
polis attest withdraw 20260828T140200Z-3f2a9c1d4e5b6a70
```

It issues a new `pub.polis.attestation.withdrawal` record whose subject is the claim you are
retracting, pinned to that claim's exact bytes. Afterwards **both** records are published and **both**
verify. You can only withdraw a claim you issued, and a withdrawal cannot itself be withdrawn.

Deleting a claim would teach the network to read a `404` as a retraction — and then an outage, a moved
site, or a lapsed domain all read as retractions too, while the actual evidence is gone. Withdrawing a
claim is a *signed record saying so*, which leaves both the claim and the withdrawal permanently
checkable.

Nothing else in polis will remove one for you either: no background actor rewrites, re-signs, or
deletes a record you authored. Regenerating something derived is maintenance; touching something you
signed is forgery.

## See also

- [spec/attestation.md](../spec/attestation.md) — the wire format, for implementers
- [concepts/assertions.md](../concepts/assertions.md) — what an attestation is, and what it is not
- [set-your-terms.md](set-your-terms.md) — the outbound half: terms on your own work
