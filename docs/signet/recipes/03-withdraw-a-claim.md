# Withdraw a claim — and what a reader sees afterwards

*Recipe 3 · [the recipe book](README.md)*

*For* [Developers](../../README.md#building-on-polis) — *Kind* [Recipe](../../README.md#kinds-of-page) — *Component* [CLI](../../cli/README.md) — *See also* [concept](../concepts/assertions.md) · [spec](../spec/attestation.md)

Every command on this page is run by the test suite.

**The problem.** You vouched for someone and no longer do. You want to retract it in a way that
reaches anyone who looks — without pretending you never said it.

**What it produces.** A **second** record, `pub.polis.attestation.withdrawal`, whose subject is the
first record's URL, pinned to its exact bytes. **Nothing is deleted.** The original stays published
and still verifies; the withdrawal is how a reader learns it was retracted.

⛔ **A withdrawal is never a deletion, and a missing record is never a withdrawal.** A `404` can mean
an outage, a moved site or a lapsed domain. If the network read absence as retraction, all of those
would silently retract claims — and the evidence would be gone.

## Set up, and make the claim

```bash
mkdir alice.example && cd alice.example
export POLIS_BASE_URL=https://alice.example
polis init --site-title "Alice"
ID=$(polis --json attest issue --predicate pub.polis.attestation.endorsement \
  --subject https://bob.example --subject-type identity | jq -r .data.id)
```

## 1 · Withdraw it

```bash
polis --json attest withdraw "$ID" | tee withdrawal.json | jq .data
```

```json output
{
  "predicate": "pub.polis.attestation.withdrawal",
  "withdrew": "…",
  "withdrew_url": "https://alice.example/content/pub.polis.core/attestation/….json",
  "withdrew_version": "sha256:…"
}
```

Your site now publishes both:

```bash
polis --json attest list | jq -c '.data.attestations | sort_by(.predicate)[] | {predicate}'
```

```json output
{"predicate": "pub.polis.attestation.endorsement"}
{"predicate": "pub.polis.attestation.withdrawal"}
```

## 2 · The two things polis refuses

A second withdrawal of the same claim says nothing new — and since withdrawals are permanent, it would
be litter nobody could ever clear:

```bash exit=1
polis attest withdraw "$ID" 2>&1
```

```text output
already withdrawn
```

And a withdrawal cannot be written with `issue`, because only `withdraw` checks that the claim is
yours and not already withdrawn:

```bash exit=1
polis attest issue --predicate pub.polis.attestation.withdrawal \
  --subject "$(jq -r .data.withdrew_url withdrawal.json)" \
  --subject-version "$(jq -r .data.withdrew_version withdrawal.json)" 2>&1
```

```text output
is issued only by withdrawing a claim
```

## 3 · What a reader sees

From anywhere, with nothing but HTTPS:

```bash
cd "$(mktemp -d)"
polis clone https://alice.example ./alice > /dev/null
polis --json --data-dir ./alice attest verify | jq -c .data.counts
```

```json output
{"valid": 2}
```

**Both verify.** Whether a claim is withdrawn is a separate question, answered by looking for a
withdrawal from the same issuer whose subject is that record, pinned to its bytes. A record's URL ends
in its id, and its id ends in the first 16 hex digits of its `current_version`:

```bash
jq -s '
  [ .[] | select(.predicate == "pub.polis.attestation.withdrawal") | .subject ] as $withdrawals
  | [ .[] | select(.predicate != "pub.polis.attestation.withdrawal")
      | .current_version as $v
      | { predicate, subject: .subject.id,
          withdrawn: any($withdrawals[]; .version == $v and (.id | endswith($v[7:23] + ".json"))) } ]
' ./alice/content/pub.polis.core/attestation/*.json
```

```json output
[
  { "predicate": "pub.polis.attestation.endorsement", "subject": "https://bob.example", "withdrawn": true }
]
```

A withdrawal only counts when it **verifies against the same issuer's key** — step 3's `attest verify`
checked that. A record signed by somebody else saying Alice's claim is withdrawn is their opinion about
it, not a withdrawal.

## What this proves — and what it does not

| It proves | It does not prove |
|---|---|
| the key that signed the claim later signed its retraction, of those exact bytes | why — a withdrawal carries no reason unless you add one as a payload |
| the claim and its retraction are both on the record, permanently | that anyone who copied the claim has seen the withdrawal — copies only learn it by looking again |
| | that the claim was false. Retracting a claim is a claim about your own earlier claim |

There is no way to withdraw a withdrawal. If you change your mind again, issue a new claim.
