# Verify a site's follow file by hand

*Recipe 14 · [the recipe book](README.md)*

*For* [Developers](../../README.md#building-on-polis) · [Reviewers](../../README.md#reviewing-the-security-and-identity-design) — *Kind* [Recipe](../../README.md#kinds-of-page) — *Component* [CLI](../../cli/README.md) — *See also* [concept](../concepts/trust.md) · [spec](../spec/attestation.md#the-signed-follow-file)

Every command on this page is run by the test suite.

**The problem.** Trust over polis is often weighed by who follows whom — *"nine of the
twelve people you follow have vouched for this author"* — and a site's follow file is where that edge
is recorded. You want to know that the roster a site serves is the one its author signed, **without
trusting polis to tell you**: with `curl`, `jq` and OpenSSH.

**What it runs.** `polis follow` makes the edge. `polis validate <url>` checks it the easy way. Then
the same check is done by hand, from the [signing base](../spec/signing-base.md#pubpolisfollow--the-follow-file-followingjson)
up, and a tampered roster is shown failing both.

## Set up two sites, one following the other

```bash
mkdir bob.example && (cd bob.example && POLIS_BASE_URL=https://bob.example polis init --site-title "Bob" > /dev/null)
mkdir alice.example && cd alice.example
export POLIS_BASE_URL=https://alice.example
polis init --site-title "Alice" > /dev/null
polis follow https://bob.example 2> /dev/null | grep following.json
SITE_DIR=$PWD
```

```text output
Added to following.json
```

⚠️ `polis follow` also asks a discovery service for comments from the followed site that are pending or were
denied, and **blesses them all with your key**, and the test network does not answer that question, so its warning is discarded above. The
follow file does not depend on it.

## 1 · The easy way

```bash
cd "$(mktemp -d)"
unset POLIS_BASE_URL
polis --json validate https://alice.example \
  | jq -c '.checks[] | select(.id == "content.following") | {outcome, detail}'
```

```json output
{"outcome": "passed", "detail": "following.json: signature verifies"}
```

## 2 · By hand

Fetch the file and the key it must verify against. The key is the one the site **publishes**, not one
you cached: that is the key anyone else on the network would use.

```bash
curl -s https://alice.example/content/pub.polis.core/follow/following.json > following.json
curl -s https://alice.example/.well-known/polis | jq -r .public_key > key.pub
jq -c '{version, following: [.following[] | .url]}' following.json
```

```json output
{"version": "polis-cli-go/…", "following": ["https://bob.example"]}
```

Rebuild the signing base: the signed fields, `version` then `following`, as compact JSON; each entry's
fields in the order `url`, `added_at`, `site_title`, `author_name`, the last two only when present; no
trailing newline. The signature itself is left out, because it cannot cover itself.

```bash
jq -cj '{version, following: [.following[]
  | {url, added_at}
  + (if (.site_title // "") != "" then {site_title} else {} end)
  + (if (.author_name // "") != "" then {author_name} else {} end)]}' following.json > base
cat base; echo
```

```text output
{"version":"polis-cli-go/…","following":[{"url":"https://bob.example","added_at":"…"}]}
```

⚠️ **`jq` does not escape `<`, `>` and `&`; the signing base does**, as `\u003c`, `\u003e` and `\u0026`
([signing base §5.2](../spec/signing-base.md#52--html-escaping--the-one-that-will-catch-you)). No URL
in this roster contains one. A general-purpose verifier must apply that escaping before it checks.

The signature is an ordinary SSH signature, namespace `file`, so OpenSSH checks it:

```bash
jq -r .signature following.json > following.sig
printf 'alice.example %s\n' "$(cat key.pub)" > allowed_signers
ssh-keygen -Y verify -f allowed_signers -I alice.example -n file -s following.sig < base
```

```text output
Good "file" signature for alice.example
```

## 3 · Change the roster, and check again

Remove Bob from the file on Alice's site, the way anything with write access to her disk could, without
her key:

```bash exit=1
cd "$SITE_DIR"
jq '.following = []' content/pub.polis.core/follow/following.json > roster.tmp
mv roster.tmp content/pub.polis.core/follow/following.json
cd "$(mktemp -d)"
polis --json validate https://alice.example \
  | jq -c '.checks[] | select(.id == "content.following") | {outcome, detail}'
```

```json output
{"outcome": "failed", "detail": "following.json: signature does NOT verify"}
```

`polis validate` exits with status 1 when any check fails, which is what a script should test. By hand, the
same roster fails the same way; `ssh-keygen` exits with status 255:

```bash exit=255
curl -s https://alice.example/content/pub.polis.core/follow/following.json > following.json
curl -s https://alice.example/.well-known/polis | jq -r .public_key > key.pub
jq -cj '{version, following: [.following[]
  | {url, added_at}
  + (if (.site_title // "") != "" then {site_title} else {} end)
  + (if (.author_name // "") != "" then {author_name} else {} end)]}' following.json > base
jq -r .signature following.json > following.sig
printf 'alice.example %s\n' "$(cat key.pub)" > allowed_signers
ssh-keygen -Y verify -f allowed_signers -I alice.example -n file -s following.sig < base
```

⭐ **A failing signature is evidence, not a verdict.** The file is still a roster, and software that reads
it keeps using it ([spec §6](../spec/attestation.md#6-a-failing-signature-is-evidence-not-a-verdict)). What the
failure tells you is that the roster served is not the one its author last signed.

## The blessing list

A site's [blessing list](../spec/attestation.md#the-signed-blessing-list), `blessed.json`, is the other signed
edge: which comments its author admitted onto their pages, pinned to a version. It is checked the same way:
same key, same envelope, its own field order. A new site's list is empty and **unsigned**, which is a legal
state and not a failure:

```bash
polis --json validate https://bob.example \
  | jq -c '.checks[] | select(.id == "content.blessed") | {outcome, detail}'
```

```json output
{"outcome": "passed", "detail": "blessed.json: unsigned — nothing to verify, which is a legal state"}
```

⏸️ **Verifying a *signed* blessing list by hand is not on this page yet**: nothing the test suite can run
produces one (see [what you cannot build yet](README.md#what-you-cannot-build-yet)). The signing base
is specified, with byte-exact examples, in [signing base §5.3](../spec/signing-base.md#the-blessing-list-blessedjson).

## What this proves — and what it does not

| It proves | It does not prove |
|---|---|
| the roster served is the one signed by the key the site publishes now | that the author made those follows. A key is not a person, and an operator who holds a tenant's key can sign as that tenant ([custody](../spec/custody.md)) |
| a changed roster no longer verifies, whoever changed it | who changed it, or why. A software bug fails the same way |
| | anything about a roster signed before the site rotated its key: a follow file carries no signing time, so it verifies only against the current key until its author next follows or unfollows someone ([spec §4](../spec/attestation.md#4-verifying)) |
| | that following someone means trusting them. What a follow is worth is your judgment ([trust](../concepts/trust.md)) |
