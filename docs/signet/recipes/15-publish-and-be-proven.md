# Publish a post, and have a stranger prove it is yours

*Recipe 15 · [the recipe book](README.md)*

*For* [Developers](../../README.md#building-on-polis) · [Reviewers](../../README.md#reviewing-the-security-and-identity-design) — *Kind* [Recipe](../../README.md#kinds-of-page) — *Component* [CLI](../../cli/README.md) — *See also* [concept](../concepts/assertions.md) · [spec](../spec/signing-base.md)

Every command on this page is run by the test suite.

**The problem.** You wrote something. It gets copied — quoted, reposted, scraped into
somebody's archive. You cannot stop that and this page will not pretend otherwise. What
you want is narrower and more useful: **that a copy cannot lie about who wrote it**, and
that anyone can check, without asking you, without an account, and without trusting the
software that made it.

**What it runs.** `polis post` publishes. Then a reader who has never heard of you fetches
your key from your domain and confirms the post is yours and unaltered — first with
`polis validate`, then by hand with `curl`, `jq` and `ssh-keygen`, so you can see there is
nothing up the sleeve.

## Publish something

```bash
mkdir alice.example && cd alice.example
export POLIS_BASE_URL=https://alice.example
polis init --site-title "Alice" > /dev/null
printf 'A short post.\n\nAnyone can copy this. Only I can sign it.\n' > hello.md
polis post -filename hello hello.md | grep '^Published:'
SITE_DIR=$PWD
```

```text output
Published: content/pub.polis.core/post/…/hello.md
```

The file on disk now carries a `signature:` line that was not there when you wrote it:

```bash
sed -n '/^---$/,/^---$/p' "$SITE_DIR"/content/pub.polis.core/post/*/hello.md \
  | cut -c1-28
```

```text output
---
title: A short post.
published: …
generator: polis-cli-go/…
current-version: sha256:…
version-history:
  - sha256:…
signature: U1NIU0lHAAAAAQAAA…
---
```

## 1 · The easy way

Somewhere else entirely — a different machine, a reader who knows only your domain:

```bash
cd "$(mktemp -d)"
unset POLIS_BASE_URL
polis --json validate https://alice.example \
  | jq -c '.checks[] | select(.id == "content.posts") | {outcome, detail}'
```

```json output
{"outcome": "passed", "detail": "1 post examined: 1 signature(s) verified, 0 unsigned"}
```

One line, and it reads like magic. The rest of this page is the same answer with the
magic taken out.

## 2 · By hand

Two fetches. The post, and the key the author's own domain publishes — **not** a key the
post came with, which would prove nothing.

```bash
POST_URL=$(curl -s https://alice.example/content/pub.polis.core/index.jsonl \
  | jq -r 'select(.type == "post") | .path' | head -1)
curl -s "https://alice.example/$POST_URL" > post.md
curl -s https://alice.example/.well-known/polis | jq -r .public_key > key.pub
cut -c1-32 key.pub
```

```text output
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5
```

**The signing base is the file with its `signature:` line taken out**, then canonicalized:
line endings become LF, trailing whitespace on each line and blank lines at the start and
end are dropped, and it ends in one newline. Nothing is reordered. A signature cannot
cover itself, so that one line is the only field a post leaves out, and the post fetched
here is already canonical, so taking it out is all this recipe has to do
([signing base §4](../spec/signing-base.md)).

⚠️ **Delete that line only where it is a frontmatter field**, not wherever the text
`signature:` appears. A post *about* signatures may open a body line with the word, and a
verifier that strips by pattern instead of by position will quietly refuse a perfectly
good post. The `awk` below stops at the closing `---`.

```bash
awk 'BEGIN{fm=0} /^---$/{fm++} {if (fm<2 && /^signature: /) next; print}' post.md > base
awk '/^signature: /{n++} END{print n+0}' base
```

```text output
0
```

The signature itself is base64 in the frontmatter. OpenSSH wants it wrapped:

```bash
awk '/^signature: /{print substr($0, 12); exit}' post.md | fold -w 70 > sig.b64
{ echo '-----BEGIN SSH SIGNATURE-----'; cat sig.b64; echo '-----END SSH SIGNATURE-----'; } > post.sig
head -1 post.sig
```

```text output
-----BEGIN SSH SIGNATURE-----
```

⚠️ **A post stores its signature bare; the [follow file](14-verify-signed-edges.md) stores
it already armoured.** Same signature format, different envelope, because one lives in
YAML frontmatter and the other in JSON. Wrap it if it is not wrapped.

Now it is an ordinary SSH signature, namespace `file`:

```bash
printf 'alice.example %s\n' "$(cat key.pub)" > allowed_signers
ssh-keygen -Y verify -f allowed_signers -I alice.example -n file -s post.sig < base
```

```text output
Good "file" signature for alice.example
```

⭐ **Nothing in that check trusted polis.** Two HTTPS fetches, one OpenSSH invocation. The
link between the key and the name `alice.example` came from DNS and the web PKI, which the
reader was already relying on to fetch the page at all.

## 3 · Now copy it somewhere else

This is the part that matters. Bob takes Alice's bytes and serves them from his own site —
exactly what a quote, a repost or a scrape does:

```bash
BOB=$(mktemp -d) && cp post.md "$BOB/stolen.md"
cd "$BOB"
awk 'BEGIN{fm=0} /^---$/{fm++} {if (fm<2 && /^signature: /) next; print}' stolen.md > base
awk '/^signature: /{print substr($0, 12); exit}' stolen.md | fold -w 70 > sig.b64
{ echo '-----BEGIN SSH SIGNATURE-----'; cat sig.b64; echo '-----END SSH SIGNATURE-----'; } > stolen.sig
curl -s https://alice.example/.well-known/polis | jq -r .public_key > alice.pub
printf 'alice.example %s\n' "$(cat alice.pub)" > allowed_signers
ssh-keygen -Y verify -f allowed_signers -I alice.example -n file -s stolen.sig < base
```

```text output
Good "file" signature for alice.example
```

⭐ **The copy still says Alice.** Authorship travelled with the bytes, off her site,
onto someone else's disk, with no discovery service and no registry in the loop. That is
the whole of the Attribution capability, and it is what a platform structurally cannot
give you: on a platform, the proof that you wrote something *is* the platform saying so.

## 4 · What happens when the copy is edited

Bob changes one character:

```bash exit=255
sed -i 's/Only I can sign it./Only I can sign it, says Bob./' stolen.md
awk 'BEGIN{fm=0} /^---$/{fm++} {if (fm<2 && /^signature: /) next; print}' stolen.md > base
ssh-keygen -Y verify -f allowed_signers -I alice.example -n file -s stolen.sig < base
```

`ssh-keygen` exits 255 and prints no *"Good signature"* line. The edited copy can no
longer claim Alice wrote it.

## What this proves — and what it does not

| | |
|---|---|
| ✅ These exact bytes were signed by the key `alice.example` publishes | the signature verifies |
| ✅ They have not changed since | one edited character breaks it |
| ✅ A reader can establish both alone | two fetches and OpenSSH, no account, no service |
| ⛔ **It does not stop the copy** | Bob still has the file, and always will |
| ⛔ **It does not prove Alice is a person, or trustworthy** | it binds bytes to a domain, and nothing further |
| ⛔ **It does not prove *when*** | the `published:` field is inside the signature, so Alice cannot change it later without breaking it — but Alice chose it. See [recipe 9](09-prove-when.md) for a witness |

⚠️ **"Only I can sign it" means only whoever holds the key.** The signature binds the post
to `alice.example`'s published key. If that key is copied, or the domain changes hands, the
binding follows the key — not the person. [Recipe 8](08-survive-a-key-rotation.md) covers
what happens to this post when Alice rotates.

## Next

- [Verify a site you do not own, end to end](05-verify-a-site.md) — the same check, for
  every artifact a site publishes, rather than one post
- [Keep old posts verifying across a key rotation](08-survive-a-key-rotation.md) — what
  this post's signature does when the key that made it is retired
- [State your terms, and watch them travel with the work](16-state-your-terms.md) — the
  copy is going to happen; this is how you say what may be done with it
