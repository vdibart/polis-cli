# Trust

*Why polis computes trust instead of storing it, and what has to be signed for that to work*

*For* [Reviewers](../../README.md#reviewing-the-security-and-identity-design) · [Developers](../../README.md#building-on-polis) — *About* [Relationships](../../general/README.md#relationships) — *Kind* [Concept](../../README.md#kinds-of-page) — *See also* [spec](../spec/attestation.md#the-signed-follow-file) · [recipe](../recipes/14-verify-signed-edges.md)

This page covers **why edges are signed**. *Why reputation is computed and never stored* is not yet
written — the fuller argument, and what a first-degree reputation query actually returns.

---

## Why edges are signed

Polis has always signed **nouns**: a post, a comment, a licence. Each carries its author's signature,
so anyone can check that the thing they are reading is the thing that was written.

It did not sign **edges** — the statements about *relationships between* people. The most important
of these is the follow list: `content/pub.polis.core/follow/following.json`, a public file naming
everyone you follow. It was published as plain JSON, with no signature at all.

That is a strange gap once you see what the file is for.

### The graph is the input to every trust computation

Signet's position on reputation is that it is **computed, never stored**: there is no score in polis,
because a score is a judgment, and judgments belong to whoever is asking rather than to the system.
What polis stores instead is **evidence** — signed statements — and each consumer computes over the
evidence in whatever way answers *their* question.

Almost every such computation is weighted by the follow graph. The sentence a person actually wants
is not *"this author scores 4.8"* but:

> *Nine of the twelve people you follow have vouched for this author.*

Notice what that sentence rests on. The vouching is signed — those are attestations. The **"people you
follow" is the follow file.** Sign every attestation perfectly and leave the roster unsigned, and the
result is still worthless: anything able to write that file can insert or remove the people whose
opinions get counted, and the arithmetic dutifully reports a number computed over a graph nobody
authored.

**An unsigned edge is a hole directly underneath the signed nouns.** The evidence is tamper-evident
and the thing that decides *which evidence counts* is not.

### What signing an edge actually asserts

A follow file is **authored, not derived.** Your action puts each entry there, and nothing writes it
back from server-side state. So its signature is not a checksum over generated output — it is a claim,
in the same sense a post is a claim:

> *These are my follows. I say so, with my key.*

The distinction does real work elsewhere in the design. The *followers* list — who follows **you** —
is authored nowhere you control and is a genuine projection of other people's actions; it is not
signed, because you did not say it. `license.json` is authored. `did.json` is derived. The line is
**who said it**, and only authored things get a signature.

It also settles a question that would otherwise be tempting: **may an automated agent sign a follow
file it finds unsigned?** Regenerating a derived artifact is maintenance and is fine. Signing an
authored one is asserting, under someone's own key, something they did not say. That is not
maintenance; it is authorship. Nothing acting on an operator's behalf may do it, and anything acting
on the *user's* behalf may do it only under an explicit grant from that user.

### Signing is only half — someone has to check

A signature nobody verifies closes nothing. It is worse than that: an unverified signature is a
guarantee that is *implied* and never *tested*, so a broken signing implementation stays invisible —
the file is written, the code runs, the tests pass, and nothing on the network ever disagrees.

So the follow-file signature ships with a verifier that runs continuously over hosted sites and
reports what it finds. The reporting rules are where the care is, and they follow from Law 2:

| What is found | What is reported |
|---|---|
| a signature that verifies | valid |
| **no signature at all** | **unsigned — a fact, not a defect** |
| a signature that does not verify | a finding, for a human |

⚠️ **Unsigned is the common case, and treating it as failure is the mistake to avoid.** Files get
signed when their author next follows or unfollows someone, so a roster nobody has touched since the
capability shipped legitimately has none. A verifier that reads absent-as-invalid describes a healthy
network as a broken one — and *a signature is not a requirement retroactively imposed on people who
never made one.*

⚠️ **And a failing signature is evidence, not a verdict.** A follow file that does not verify is still
loaded and still used. Refusing to load it would disconnect a person from their entire network as a
*security response*, on evidence that usually points at a bug. The system's job is to say what it
found. What that means is the consumer's call.

## See also

- [`../spec/attestation.md`](../spec/attestation.md) — the signed follow file's exact wire shape and signing base
- [`../recipes/14-verify-signed-edges.md`](../recipes/14-verify-signed-edges.md) — verify a site's follow file by hand, and watch a changed one fail
- [`assertions.md`](assertions.md) — what polis lets someone say
- [`../../general/concepts/content-types.md`](../../general/concepts/content-types.md) — where the follow file sits in the data model
