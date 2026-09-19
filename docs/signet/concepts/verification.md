# Verification

*What a verifier is allowed to say, and what it must leave to you*

*For* [Reviewers](../../README.md#reviewing-the-security-and-identity-design) · [Developers](../../README.md#building-on-polis) — *Kind* [Concept](../../README.md#kinds-of-page) — *See also* [spec](../spec/attestation.md) · [guide](../guides/verify-content.md) · [guide](../guides/network-health.md)

A verifier in polis states **facts**. Whether those facts make something acceptable is a **judgment**,
and it belongs to whoever is asking. This page is about where that line falls, why it falls there,
and the places where a careless verifier would step over it.

---

## 1 Facts and judgments

| A verifier may say | A verifier must not say |
|---|---|
| this record is signed | this record is trustworthy |
| the signature verifies against the key this site publishes | this site is legitimate |
| it verified against a **retired** key, the one the site's own history says was current at the claimed signing time | old signatures are as good as new ones |
| this URL could not be fetched from here, at this moment | this content is gone, or was withdrawn |
| I examined this site from here, at this moment, and could not verify it | this site is invalid |

The left column is true or false, and anyone can check it. The right column depends on what the
asker needs, how much risk they will carry, and who they already trust — none of which the verifier
knows. **A verifier that answers the right column has taken a decision that was never its to take.**

## 2 Three distinctions a verifier must keep

**Unreachable is not invalid.** A fetch that failed says something about the network between two
machines at one moment. It says nothing about the artifact. A verifier that folds *could not fetch*
into *does not verify* turns every outage into an accusation.

**Unsigned is not invalid.** A record without a signature is a fact about the record — its author did
not sign it. A record whose signature does not verify is a different fact, and usually a far more
interesting one.

**An empty answer is not a finding.** *"No attestations"* from a site that publishes none looks
identical, over HTTP, to *"no attestations"* from a site whose index has not been rebuilt. **Absence is
never published as a fact about a site.**

## 3 When the verifier publishes: the integrity observation

The hosted operator's Judge checks every site it hosts and, when a site's state changes, publishes a
signed [`pub.polis.attestation.integrity`](../spec/attestation.md#41-integrity-is-an-observation-and-its-result-lives-in-the-payload)
record about it. That record is shaped by everything above:

- **The predicate names what was examined; the payload says what was found.** `result` is `verified`
  or `not-verified`, is required, and has no default. A reader that cannot read the payload must not
  interpret the record at all — abstaining is the correct outcome.
- **It is an observation, not a verdict.** `not-verified` means *"could not verify at `observed` from
  `vantage`"*. It never means *"this site is invalid."*
- **It says where the verifier stood.** `vantage` is part of the claim. Judge examines its own
  operator's sites, over loopback, and never itself. That is not independence, and the record does not
  pretend it is. It says exactly where it looked, so you can look from somewhere else.
- **It keeps the reasons apart.** `unreachable` and `mismatched` are separate counts, all the way into
  the published record.
- **It asserts a moment, never an interval.** A `verified` observation says *"at this moment,
  everything checked out"*. It says nothing about the time between two observations. You can see that
  gap, and what you conclude about it is up to you.
- **It is published on a change of state, not on every check.** A site that stays broken for a year
  produces two records — when it became unhealthy and when it recovered — not eight thousand.

## 4 What a publishing verifier does to avoid being wrong

A signed, published observation about somebody else's site cannot be taken back in any way that
reaches everyone who already has it. So the defence is not to publish a wrong one:

- **Consecutive failures before anything is published.** A problem that appears on one check and is
  gone the next produces nothing. That threshold is what keeps a verifier's own one-off bug from
  becoming a permanent record.
- **A record agrees with the latest check.** Health moves slowly on purpose, so a site can still count
  as unhealthy after it has started passing. Judge publishes `not-verified` only while the latest check
  is failing, and `verified` only while it is passing. A record that a halt held back is never
  published once the site has recovered.
- **A failure that looks like one cause halts publishing.** When most of the fleet fails at once, or
  three or more sites fail the *same* check, the likeliest explanation is the verifier or the platform
  they share, not the sites. Judge refuses to publish and alerts its operator instead. The bar is set
  low on purpose: **a false halt only delays a record, and a false record is permanent.**
- **A budget.** A healthy fleet publishes nothing. A verifier that finds itself with a lot to say in a
  single day stops and says so.

## 5 When the verifier is silent

A verifier can be switched off, halted by its own safety check, or out of budget. **Silence from a
muted verifier must never look like a clean fleet.** The operator's Judge serves an unsigned status
document on its own site saying whether it is publishing, why not, and since when. See
[network health](../guides/network-health.md#is-judge-publishing).

It is unsigned on purpose. *"Judge is muted"* is current state that will stop being true. Signing it
would turn a moment that is already over into a permanent claim.

## 6 When the verifier was wrong — and what cannot be undone

A verifier that publishes a false observation retracts it the same way anyone retracts a claim: by
publishing a [withdrawal](../spec/attestation.md#6-a-subject-may-be-another-attestation). The
original stays published and stays valid. A reader learns that it was retracted by finding the
withdrawal.

⛔ **A withdrawal does not reach copies that were already fetched.** Anyone who read the original and
never checks again will go on holding it. Nothing in polis can change that, and nothing tries to.
Revocation has the same limit everywhere — certificate revocation lists have it too — because
reaching every copy would mean tracking every reader.

Two further gaps are worth stating plainly:

- **A withdrawal is per record.** There is not yet a way for a verifier to say *"everything I
  published between these two times, from this vantage, is unreliable."* Until there is, a verifier
  with a systemic bug can only withdraw the records it can list.
- **The subject of an observation cannot yet answer it.** The format supports a record about a record,
  so a site could dispute an observation about itself, but no predicate for that exists yet. For now a
  consumer holds the verifier's claim and not the site's reply.
