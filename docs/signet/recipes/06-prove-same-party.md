# Prove two domains are the same party

*Recipe 6 · [the recipe book](README.md)*

*For* [Developers](../../README.md#building-on-polis) — *Kind* [Recipe](../../README.md#kinds-of-page) — *Component* [CLI](../../cli/README.md) — *See also* [concept](../concepts/identity.md) · [spec](../spec/attestation.md)

Every command on this page is run by the test suite.

**The problem.** You are moving from `old.example` to `new.example`, or you publish from both. You want
anyone to be able to confirm they are the same party — with no registry, no ledger, and no authority
to ask.

**What it produces.** **Two** `pub.polis.attestation.same-as` records, one from each site, each naming
the other, each signed by its own site's key. **The pair is the argument.** One site saying *"I am
also that one"* is a self-assertion that whoever controls it can make; two independently keyed sites
each saying it of the other is not.

⚠️ **Issue the old site's half while you still control the old domain.** Once you have lost it, you can
never sign with its key again, and the pair can never be completed.

## Set up two independently keyed sites

Each `polis init` makes its own key.

```bash
mkdir old.example && cd old.example
POLIS_BASE_URL=https://old.example polis init --site-title "Old"
cd ..
mkdir new.example && cd new.example
POLIS_BASE_URL=https://new.example polis init --site-title "New"
cd ..
```

## 1 · Each site states its half

```bash
(cd old.example && POLIS_BASE_URL=https://old.example polis --json attest issue \
  --predicate pub.polis.attestation.same-as \
  --subject https://new.example --subject-type identity | jq -r .data.url)
(cd new.example && POLIS_BASE_URL=https://new.example polis --json attest issue \
  --predicate pub.polis.attestation.same-as \
  --subject https://old.example --subject-type identity | jq -r .data.url)
```

```text output
https://old.example/content/pub.polis.core/attestation/….json
https://new.example/content/pub.polis.core/attestation/….json
```

## 2 · Check the pair from somewhere else

```bash
cd "$(mktemp -d)"
polis clone https://old.example ./old > /dev/null
polis clone https://new.example ./new > /dev/null
polis --json --data-dir ./old attest verify | jq -c .data.counts
polis --json --data-dir ./new attest verify | jq -c .data.counts
```

```json output
{"valid": 1}
{"valid": 1}
```

Each half verifies against **its own** site's published key. Now check that they point at each
other:

```bash
jq -r 'select(.predicate == "pub.polis.attestation.same-as") | "\(.issuer) same-as \(.subject.id)"' \
  ./old/content/pub.polis.core/attestation/*.json ./new/content/pub.polis.core/attestation/*.json
```

```text output
https://old.example same-as https://new.example
https://new.example same-as https://old.example
```

And that two keys really are involved:

```bash
[ "$(jq -r .public_key ./old/.well-known/polis)" != "$(jq -r .public_key ./new/.well-known/polis)" ] \
  && echo "two different keys, each vouching for the other"
```

```text output
two different keys, each vouching for the other
```

## What this proves — and what it does not

| It proves | It does not prove |
|---|---|
| whoever held `old.example`'s key and whoever held `new.example`'s key each said they are the same party | who that party is |
| neither half can be produced by someone holding only the other key | anything with **half** a pair — a lone record is a self-assertion |
| | what the old half is worth after the old domain changes hands. That is unsettled: the record still verifies against the key history, but a new owner of the domain speaks for it from then on |

⚠️ **The pair above is two fixture sites.** A live pair on two independently keyed public domains does
not exist on the network yet.
