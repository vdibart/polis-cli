# Make a claim about someone, then verify it from a different machine

*Recipe 1 · [the recipe book](README.md)*

*For* [Developers](../../README.md#building-on-polis) — *Kind* [Recipe](../../README.md#kinds-of-page) — *Component* [CLI](../../cli/README.md) — *See also* [concept](../concepts/assertions.md) · [spec](../spec/attestation.md) · [guide](../guides/attest.md)

Every command on this page is run by the test suite.

**The problem.** You want to say something about another party — *I vouch for bob.example* — so that
anyone can check it later, from anywhere, without asking you and without trusting whoever passed it
along.

**What it produces.** One signed `pub.polis.attestation` record, published on your site at a
permanent URL, and an entry for it in your site's index.

**You need.** A polis site with its key, published at its domain. The examples use `alice.example`;
use your own.

## Set up a site

Skip this if you have one. `polis init` makes the key; putting the directory online at its domain is
whatever you already do to deploy.

```bash
mkdir alice.example && cd alice.example
export POLIS_BASE_URL=https://alice.example
polis init --site-title "Alice"
```

## 1 · Issue the claim

```bash
polis --json attest issue \
  --predicate pub.polis.attestation.endorsement \
  --subject https://bob.example --subject-type identity | tee issued.json
```

```json output
{
  "status": "success",
  "command": "attest",
  "data": {
    "id": "…",
    "url": "https://alice.example/content/pub.polis.core/attestation/….json",
    "issuer": "https://alice.example",
    "predicate": "pub.polis.attestation.endorsement",
    "subject": "https://bob.example",
    "subject_type": "identity",
    "current_version": "sha256:…"
  }
}
```

The `url` is the record. Once your site is deployed, anyone can fetch it:

```bash
URL=$(jq -r .data.url issued.json)
curl -s "$URL" | jq '{issuer, predicate, subject, asserted,
  signed: (.signature | startswith("-----BEGIN SSH SIGNATURE-----"))}'
```

```json output
{
  "issuer": "https://alice.example",
  "predicate": "pub.polis.attestation.endorsement",
  "subject": { "type": "identity", "id": "https://bob.example" },
  "asserted": "…",
  "signed": true
}
```

The predicate is written out in full. The reserved ones are listed in
[the spec](../spec/attestation.md#4-predicates), and you may use your own — `com.yourdomain.reviewed`
is issued, published and verified the same way.

## 2 · Verify it from somewhere else

Everything below uses only public HTTPS: no key, no account, nothing from Alice's machine. Start in an
empty directory, as a stranger would.

```bash
cd "$(mktemp -d)"
unset POLIS_BASE_URL
polis clone https://alice.example ./alice
```

```text output
Attestations downloaded: 1
```

```bash
polis --json --data-dir ./alice attest verify
```

```json output
{
  "status": "success",
  "command": "attest",
  "data": { "counts": { "valid": 1 }, "invalid": 0 }
}
```

`valid` means the record's signature verifies against the key **published at `alice.example`** — the
copy of it you just fetched, not anything Alice handed you. The four possible values are `valid`,
`unsigned`, `invalid` and `unknown`, and only `invalid` means something is wrong
([spec §10](../spec/attestation.md#10-the-four-states-and-why-they-are-four)). Read `data.invalid`,
not the envelope `status`, which only says the command ran.

## 3 · Or check the one record by its URL

Cloning checks every record a site has issued. When you have one address, check just that — two
fetches, the record and the issuer's `.well-known/polis`, storing nothing:

```bash
polis --json validate "$URL" | jq -c '.checks[] | select(.id == "content.artifact" or .id == "content.hash") | {id, outcome, detail}'
```

```json output
{"id": "content.artifact", "outcome": "passed", "detail": "attestation signature verifies against the key published at https://alice.example"}
{"id": "content.hash", "outcome": "passed", "detail": "record bytes match its current_version"}
```

The key is the one published by the record's **`issuer`**, not by whoever served you the file
([spec §9](../spec/attestation.md#9-verifying)), so a copy of Alice's record hosted anywhere
verifies against Alice's key or not at all. `content.hash` recomputes `current_version` from the
record's signed fields, so a record whose bytes were edited fails there too.

## What this proves — and what it does not

| It proves | It does not prove |
|---|---|
| the key published at `alice.example` signed exactly these fields | that `alice.example` is who you think — that binding is DNS and the web PKI |
| nothing in those fields has changed since | that the claim is **true**, or that Alice's endorsement is worth anything to you — that is your judgment, not the protocol's |
| Alice says she asserted it at `asserted` | when it was **really** made — `asserted` is her own claim. See [recipe 9](09-prove-when.md) |
| | that Bob agrees — there is no way to say so yet ([gap list](README.md#what-you-cannot-build-yet)) |
| | that Alice has not withdrawn it since — look for a withdrawal ([recipe 3](03-withdraw-a-claim.md)) |
