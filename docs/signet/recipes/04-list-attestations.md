# List every attestation a site has issued

*Recipe 4 · [the recipe book](README.md)*

*For* [Developers](../../README.md#building-on-polis) — *Kind* [Recipe](../../README.md#kinds-of-page) — *Component* [CLI](../../cli/README.md) — *See also* [concept](../concepts/graph.md) · [spec](../spec/attestation.md)

Every command on this page is run by the test suite.

**The problem.** You want to know what a site has claimed about others — every attestation it has
issued — without knowing any record's URL in advance.

**What it answers from.** The site's own `index.jsonl`, which lists each record it has indexed. The
site is the record; everything else is a copy of it.

## Set up a site with some claims

One reserved predicate, and one of the site's own — a predicate outside the reserved list is indexed
and served the same way.

```bash
mkdir alice.example && cd alice.example
export POLIS_BASE_URL=https://alice.example
polis init --site-title "Alice"
polis attest issue --predicate pub.polis.attestation.endorsement \
  --subject https://bob.example --subject-type identity
polis attest issue --predicate com.example.reviewed \
  --subject https://bob.example/some-work.md
cd ..
```

## 1 · Find the index, by pointer

A site's layout is configurable, so the index is found through the site's identity document, never by
assuming a path. It sits beside the bundle manifest `.well-known/polis` points at.

```bash
BUNDLE=$(curl -s https://alice.example/.well-known/polis | jq -r '.bundles["pub.polis.core"].path')
INDEX="https://alice.example/$(dirname "$BUNDLE")/index.jsonl"
echo "$INDEX"
```

```text output
https://alice.example/content/pub.polis.core/index.jsonl
```

## 2 · List the attestations

```bash
curl -s "$INDEX" | jq -c 'select(.type == "attestation") | {title, published, path}' | sort
```

```json output
{"title": "com.example.reviewed", "published": "…", "path": "content/pub.polis.core/attestation/….json"}
{"title": "pub.polis.attestation.endorsement", "published": "…", "path": "content/pub.polis.core/attestation/….json"}
```

An entry is a **pointer**: `title` is the predicate, `published` is the record's `asserted` time, and
`path` is where the record is. It does not carry the subject — for that, fetch the record.

## 3 · Fetch each record

```bash
curl -s "$INDEX" | jq -r 'select(.type == "attestation") | .path' | while read -r path; do
  curl -s "https://alice.example/$path" | jq -c '{predicate, subject: .subject.id}'
done | sort
```

```json output
{"predicate": "com.example.reviewed", "subject": "https://bob.example/some-work.md"}
{"predicate": "pub.polis.attestation.endorsement", "subject": "https://bob.example"}
```

These are **unverified** until you check each signature against the key `alice.example` publishes —
[recipe 1](01-make-a-claim.md) does that for every record at once.

## Which source answers which question

| Question | Ask | Note |
|---|---|---|
| What has `alice.example` attested? | **its index** — this recipe | as fresh as the site's last write or repair |
| Is this record really Alice's? | **the record and Alice's published key** — [recipe 1](01-make-a-claim.md) | nothing else can answer it |
| Is any of it withdrawn? | **Alice's other records** — [recipe 3](03-withdraw-a-claim.md) | |
| Which records does a discovery service hold for `alice.example`? | `GET /v1/content?type=pub.polis.attestation&actor=alice.example` — [API reference](../../ds/developer/api-reference.md#get-v1contenttypepubpolispost) | ⚠️ **a cache, not the record.** It holds only what was registered with it. An operator's actor disclosures, for one, are never registered |
| Who has attested about `bob.example`? | **no documented answer yet** | on the [gap list](README.md#what-you-cannot-build-yet) |

## What this proves — and what it does not

| It tells you | It does not tell you |
|---|---|
| what the site's index lists, right now | that nothing else exists. **An empty list means nothing indexed, never nothing issued** — an index is as fresh as the site's last write or repair, and HTTP does not list directories |
| where each record is | that any record is authentic — the index is unsigned; each record carries its own signature |
