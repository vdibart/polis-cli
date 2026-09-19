# Verify every attestation a site has issued, from outside

*Recipe 13 · [the recipe book](README.md)*

*For* [Developers](../../README.md#building-on-polis) · [Reviewers](../../README.md#reviewing-the-security-and-identity-design) — *Kind* [Recipe](../../README.md#kinds-of-page) — *Component* [CLI](../../cli/README.md) — *See also* [concept](../concepts/graph.md) · [spec](../spec/attestation.md) · [guide](../guides/verify-content.md)

Every command on this page is run by the test suite.

**The problem.** You want to check **all** of a site's claims, not just one whose address you already
have — and to know they still check out after the site rotates its key.

**What it runs.** `polis validate <url>` verifies every attestation the site's index lists, each
against the key of the site that signed it. `polis attest verify` does the same for a site directory.

## Set up a site with a claim

```bash
mkdir alice.example && cd alice.example
export POLIS_BASE_URL=https://alice.example
polis init --site-title "Alice"
polis attest issue --predicate pub.polis.attestation.endorsement \
  --subject https://bob.example --subject-type identity > /dev/null
SITE_DIR=$PWD
```

## 1 · Check every claim, from somewhere else

```bash
cd "$(mktemp -d)"
unset POLIS_BASE_URL
polis --json validate https://alice.example \
  | jq -c '.checks[] | select(.id == "content.attestations") | {outcome, detail}'
```

```json output
{"outcome": "passed", "detail": "1 attestation examined: 1 signature(s) verified, 0 unsigned"}
```

## 2 · Rotate the key, and check again

The pause is there because a signing time has one-second precision — see
[recipe 8](08-survive-a-key-rotation.md#the-boundary).

```bash
cd "$SITE_DIR"
export POLIS_BASE_URL=https://alice.example
sleep 1
polis rotate-key > /dev/null
polis --json attest verify | jq -c '.data | {counts, retired: (.retired | length)}'
```

```json output
{"counts": {"valid": 1}, "retired": 1}
```

The claim still verifies, and `retired` names it: it verified against the key the site's history says
was current at its `asserted` time, not the key the site publishes now. A stranger gets the same answer:

```bash
cd "$(mktemp -d)"
unset POLIS_BASE_URL
polis --json validate https://alice.example \
  | jq -c '.checks[] | select(.id == "content.attestations") | {outcome, detail}'
```

```json output
{"outcome": "passed", "detail": "1 attestation examined: 1 signature(s) verified — 1 of them against a RETIRED key, at a claimed signing time — 0 unsigned"}
```

## What this proves — and what it does not

| It proves | It does not prove |
|---|---|
| every attestation the site's index lists is signed by the site that issued it | that the site has no claims its index does not list — an index is only as fresh as its last write, and HTTP does not list directories |
| a claim signed before a rotation verifies against the key that was current at its claimed time | that it was actually made then — `asserted` is the issuer's own claim; see [recipe 9](09-prove-when.md) |
| | that any claim is true — that is your judgment |
