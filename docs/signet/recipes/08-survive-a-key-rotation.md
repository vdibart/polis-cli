# Keep old posts verifying across a key rotation

*Recipe 8 · [the recipe book](README.md)*

*For* [Developers](../../README.md#building-on-polis) · [Reviewers](../../README.md#reviewing-the-security-and-identity-design) — *Kind* [Recipe](../../README.md#kinds-of-page) — *Component* [CLI](../../cli/README.md) — *See also* [concept](../concepts/identity.md) · [spec](../spec/key-history.md)

Every command on this page is run by the test suite.

**The problem.** You need to replace your site's signing key — it may have leaked, or you are simply
rotating. Everything you signed with the old key must still check out afterwards, for anyone, from
your site alone.

**What makes it work.** Your site publishes its **key history** in `.well-known/polis`: every key it
has held, each handover signed by the key it replaced. A verifier that meets a signature the current
key does not match looks up the key that was current **at the time the artifact says it was signed**.
Spec: [key history](../spec/key-history.md).

## Set up a site with a post

```bash
mkdir alice.example && cd alice.example
export POLIS_BASE_URL=https://alice.example
polis init --site-title "Alice"
printf '# Before the rotation\n\nSigned with the first key.\n' > before.md
polis post before.md
```

## 1 · Rotate the key

The pause is only there because this recipe runs in seconds — see *the boundary* below.

```bash
sleep 1
polis rotate-key
```

```bash
jq '{current_epoch: .public_key_history.current.epoch, keys_retired: (.public_key_history.history | length)}' \
  .well-known/polis
```

```json output
{"current_epoch": 1, "keys_retired": 1}
```

## 2 · Check the old post from somewhere else

```bash
cd "$(mktemp -d)"
unset POLIS_BASE_URL
polis --json validate https://alice.example \
  | jq -c '.checks[] | select(.id == "identity.key_history" or .id == "content.posts") | {id, outcome}'
```

```json output
{"id": "identity.key_history", "outcome": "passed"}
{"id": "content.posts", "outcome": "passed"}
```

The post still verifies. `polis preview` shows **which** key verified it:

```bash
BUNDLE=$(curl -s https://alice.example/.well-known/polis | jq -r '.bundles["pub.polis.core"].path')
POST="https://alice.example/$(curl -s "https://alice.example/$(dirname "$BUNDLE")/index.jsonl" \
  | jq -r 'select(.type == "post") | .path' | head -1)"
polis --json preview "$POST" | jq '.data.signature | {status, key: (.key | {source, epoch, claimed_signing_time})}'
```

```json output
{
  "status": "valid",
  "key": { "source": "retired", "epoch": 0, "claimed_signing_time": "…" }
}
```

`status` is `valid` either way. **Read `key.source` if the difference matters to you:** `current`
means the key the site publishes now; `retired` means a key the site's own history says was current at
the post's claimed signing time.

## The honest sentence

⛔ **A retired-key pass says the artifact verifies against the key that was current at its CLAIMED
signing time. It does not say the artifact is authentic.** The signing time is the post's own
`published:` field — the author's claim. Someone holding a retired key could sign something new and
date it into that key's window, and this check alone could not tell.

What bounds that is a **witness**: a discovery service's signature saying it saw those exact bytes at a
time by its own clock. A post witnessed before the key was retired cannot have been backdated after.
That is [recipe 9](09-prove-when.md), and `polis validate` reports a witness that post-dates the key's
retirement.

## The boundary

Signing times have **one-second precision**, and a key's window begins at the second of the rotation.
An artifact claiming the same second as the rotation falls in the **new** key's window — so a post
signed with the old key in that second does not resolve to it. In practice a rotation does not happen
in the same second as a post; a script that publishes and rotates back to back should pause.

## What this proves — and what it does not

| It proves | It does not prove |
|---|---|
| the site's chain of keys is unbroken, each handover signed by the key before | when anything was signed — every date here is the site's own claim |
| an old artifact verifies against the key its site says was current at its claimed signing time | that it was actually signed then — see *the honest sentence* |
| | that the new key belongs to the same person — a rotation needs only the old key's signature, so whoever holds it can hand the identity to anyone |
