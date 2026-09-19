# Pin a correction to exact bytes, so an edit cannot move it

*Recipe 2 · [the recipe book](README.md)*

*For* [Developers](../../README.md#building-on-polis) — *Kind* [Recipe](../../README.md#kinds-of-page) — *Component* [CLI](../../cli/README.md) — *See also* [concept](../concepts/assertions.md) · [spec](../spec/attestation.md)

Every command on this page is run by the test suite.

**The problem.** Bob published a figure that turned out to be wrong. You want to say so in a way that
stays attached to **what Bob actually wrote** — even if Bob edits the post afterwards.

**What it produces.** A `pub.polis.attestation.correction` record whose subject is Bob's post,
**pinned to a content hash.** The pin is one of the fields your signature covers, so nobody —
including you — can point the correction at different bytes without breaking it.

## Set up two sites

```bash
mkdir bob.example && cd bob.example
export POLIS_BASE_URL=https://bob.example
polis init --site-title "Bob"
printf '# The figure\n\nThe number is 42.\n' > figure.md
polis post figure.md
cd ..

mkdir alice.example && cd alice.example
export POLIS_BASE_URL=https://alice.example
polis init --site-title "Alice"
```

## 1 · Find the post, and the hash to pin

Bob's site lists its posts in its index, which you find through its identity document rather than by
guessing a path. `polis preview` then checks Bob's signature and shows the hash.

```bash
BUNDLE=$(curl -s https://bob.example/.well-known/polis | jq -r '.bundles["pub.polis.core"].path')
POST="https://bob.example/$(curl -s "https://bob.example/$(dirname "$BUNDLE")/index.jsonl" \
  | jq -r 'select(.type == "post") | .path' | head -1)"
polis --json preview "$POST" | jq '{current_version: .data.current_version,
  signature: .data.signature.status}'
```

```json output
{ "current_version": "sha256:…", "signature": "valid" }
```

## 2 · Issue the correction, pinned

```bash
PIN=$(polis --json preview "$POST" | jq -r .data.current_version)
polis --json attest issue \
  --predicate pub.polis.attestation.correction \
  --subject "$POST" --subject-version "$PIN" \
  --payload "note=the source revised the figure to 41" | tee correction.json | jq .data
```

```json output
{
  "predicate": "pub.polis.attestation.correction",
  "subject": "https://bob.example/content/pub.polis.core/post/….md",
  "subject_type": "uri",
  "subject_version": "sha256:…"
}
```

## 3 · Bob edits the post

```bash
cd ../bob.example
export POLIS_BASE_URL=https://bob.example
printf '# The figure\n\nThe number is 41.\n' > revised.md
polis republish "$(jq -r 'select(.type == "post") | .path' content/pub.polis.core/index.jsonl | head -1)" revised.md
cd ../alice.example
```

## 4 · What anyone sees afterwards

The post has a new hash. The correction, fetched from Alice's site, still names the old one.

```bash
NOW=$(polis --json preview "$POST" | jq -r .data.current_version)
PINNED=$(curl -s "$(jq -r .data.url correction.json)" | jq -r .subject.version)
[ "$PINNED" = "$PIN" ] && [ "$NOW" != "$PIN" ] && echo "the post has moved on; the correction has not"
```

```text output
the post has moved on; the correction has not
```

A reader holding the correction and the post compares `subject.version` with the post's
`current_version`. **If they match, the correction is about what they are reading. If they differ, it
is about an earlier version** — and both the correction and the edit stay on the record.

## What this proves — and what it does not

| It proves | It does not prove |
|---|---|
| Alice's correction is about the post whose content hashes to the pin | that Bob's figure was wrong — that is Alice's claim, and yours to weigh |
| an edit to the post cannot silently move the correction onto new words | anything about parts of the post the hash does not cover — for a post, `current_version` hashes the **body**, so a change to the title alone leaves the pin matching |
| | what the old words were — a pin **names** bytes, it does not keep them. If you may need to show them later, keep a copy (`polis clone`) |
