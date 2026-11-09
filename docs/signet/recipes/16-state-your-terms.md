# State your terms, and watch them travel with the work

*Recipe 16 · [the recipe book](README.md)*

*For* [Developers](../../README.md#building-on-polis) · [Reviewers](../../README.md#reviewing-the-security-and-identity-design) — *Kind* [Recipe](../../README.md#kinds-of-page) — *Component* [CLI](../../cli/README.md) — *See also* [guide](../guides/set-your-terms.md) · [spec](../spec/license.md)

Every command on this page is run by the test suite.

**The problem.** Your work is going to be read by machines. Some of that you want — a
search engine that indexes you and sends readers back. Some of it you may not — a model
trained on your writing, or an answer that summarises you so the reader never visits. You
want to say which, **once**, in a form a crawler can read, and have that statement travel
with the work instead of living on a terms page nobody fetches.

**What it runs.** `polis license` states the terms. `polis render` generates everything
downstream from them. Then a reader fetches one post and finds the terms inside it,
without asking you.

⚠️ **This is the opposite direction from [policy](../../general/reference/policy-grammar.md).**
Policy governs *others → you*: who may comment, what you accept. A licence governs
*you → the world*: what anyone may do with work you made. They share the word "rules" and
nothing else, and this page is entirely about the second.

## A site that has said nothing

```bash
mkdir alice.example && cd alice.example
export POLIS_BASE_URL=https://alice.example
polis init --site-title "Alice" > /dev/null
polis license
```

```text output
[i] This site states no terms.
    Your posts don't carry terms describing how others may use them.
```

⛔ **That is the honest starting state, and nothing changes it on your behalf.** No
default is applied at `polis init` unless you pass `--license`, no actor writes one
later, and no hosted signup picks one for you. A statement of intent nobody made is not a
statement of intent.

Publish something before deciding, so there is a "before":

```bash
printf 'An early post.\n\nWritten before any terms were stated.\n' > early.md
polis post -filename early early.md | grep '^Published:'
SITE_DIR=$PWD
```

```text output
Published: content/pub.polis.core/post/…/early.md
```

## 1 · Choose

```bash
polis license reserved
```

```text output
[✓] Licence: pub.polis.license.reserved/1
    train-ai=n, search=y
    ai-input=n (RSL — summarising into an answer)
    attribution=required
    Terms are not retroactive: posts already published keep the
    terms they were signed with.
```

Four keys, and **they come from two different standards** — which matters, because one of
them is only a preference:

| Key | Layer | Means |
|---|---|---|
| `train-ai: n` | **AIPREF** — a *preference* | do not train on this |
| `search: y` | **AIPREF** — a *preference* | indexing and linking back is welcome |
| `ai-input: n` | **RSL** — a *licence term* | do not summarise it into an answer that replaces the visit |
| `attribution: required` | **RSL** — a *licence term* | credit it wherever it is used |

⛔ **`train-ai` and `search` are the whole of AIPREF's vocabulary.** There is no `ai` key
and there cannot be one — the specification forbids supersets. Anything else you see in a
licence block, including the two RSL keys above, is from somewhere else. Say which, or
you are telling a crawler something it will not understand.

The signed source is one file, and `.well-known/polis` says where it is — the path is
configurable, so never hardcode it:

```bash
LICENSE_PATH=$(jq -r .license .well-known/polis)
echo "$LICENSE_PATH"
jq -c '.terms | {v, profile, "train-ai", search, "ai-input", attribution}' ".${LICENSE_PATH}"
```

```text output
/content/pub.polis.core/license/license.json
{"v":"pub.polis.license.v1","profile":"pub.polis.license.reserved/1","train-ai":"n","search":"y","ai-input":"n","attribution":"required"}
```

⭐ **Everything from here is generated from that file.** You never author the surfaces
below, and editing one of them changes nothing.

## 2 · Publish again, and watch the terms go into the bytes

```bash
printf 'A later post.\n\nWritten after the terms were stated.\n' > later.md
polis post -filename later later.md > /dev/null
polis render > /dev/null
sed -n '/^license:/,/^  asserted/p' "$SITE_DIR"/content/pub.polis.core/post/*/later.md
```

```text output
license:
  v: pub.polis.license.v1
  profile: pub.polis.license.reserved/1
  train-ai: n
  search: y
  ai-input: n
  attribution: required
  terms: https://alice.example/license
  contact: https://alice.example/license
  asserted: …
```

### The terms are inside the signature, and here is the proof

Not an assertion — take the published post, delete only the licence block, and ask
OpenSSH:

```bash exit=255
cd "$(mktemp -d)"
curl -s https://alice.example/content/pub.polis.core/index.jsonl \
  | jq -r 'select(.type == "post") | .path' | grep later > p
curl -s "https://alice.example/$(cat p)" > later.md
curl -s https://alice.example/.well-known/polis | jq -r .public_key > key.pub
printf 'alice.example %s\n' "$(cat key.pub)" > allowed_signers
awk '/^signature: /{print substr($0, 12); exit}' later.md | fold -w 70 > sig.b64
{ echo '-----BEGIN SSH SIGNATURE-----'; cat sig.b64; echo '-----END SSH SIGNATURE-----'; } > later.sig
awk 'BEGIN{fm=0;drop=0} /^---$/{fm++}
     {if (fm<2 && /^license:/) {drop=1; next}
      if (fm<2 && drop && /^  /) next
      drop=0
      if (fm<2 && /^signature: /) next
      print}' later.md > base
ssh-keygen -Y verify -f allowed_signers -I alice.example -n file -s later.sig < base
```

`ssh-keygen` exits 255. ⭐ **A licence block cannot be stripped in transit** — remove it
and the post stops being provably Alice's, which is a worse trade than leaving it in. The
terms reached the reader because they are part of what was signed, not because a server
was asked nicely.

## 3 · The same terms, on surfaces you never wrote

```bash
cd "$SITE_DIR"
grep -E '^(Content-Usage|License):' robots.txt
grep -oE '<link rel="license"[^>]*>|<meta name="content-usage"[^>]*>' posts/*/later.html | head -2
```

```text output
Content-Usage: train-ai=n, search=y
License: https://alice.example/rsl.xml
<link rel="license" href="https://alice.example/license">
<meta name="content-usage" content="train-ai=n, search=y">
```

⚠️ **`robots.txt` carries only the two AIPREF keys.** That is not an omission — AIPREF has
no way to express *"do not summarise this"* or *"credit me"*. Those live in `rsl.xml`,
which the `License:` line points at. One authored file, two vocabularies, each carrying
what it is able to carry.

## 4 · What the earlier post says

Nothing. It was published before Alice decided, and it stayed that way:

```bash
sed -n '/^---$/,/^---$/p' "$SITE_DIR"/content/pub.polis.core/post/*/early.md \
  | grep -c '^license:' || true
```

```text output
0
```

⛔ **Terms are not retroactive, and this is why.** Back-filling the block would mean
re-signing a post, in Alice's name, to assert something she had not said when she
published it. A newer `license.json` never reaches older work — it is the terms of the
work, fixed at publication, not a setting that applies backwards.

If you want the earlier post covered, republish it. That is a decision, and it looks like
one.

## What this gives you — and what it does not

| | |
|---|---|
| ✅ One authored statement, many machine-readable surfaces | you write `license.json`; `robots.txt`, `rsl.xml`, the page head and the post block are generated |
| ✅ The terms travel with the work | they are inside the signature, so a copy carries them or stops verifying |
| ✅ A crawler can read them without fetching a terms page | frontmatter, `<meta>`, `robots.txt` and `rsl.xml` all answer |
| ⛔ **Nothing enforces any of it** | AIPREF is a *preference* and RSL is a *licence*. Both are statements. A crawler that ignores them is not stopped by anything here — it is only unable to say it did not know |
| ⛔ **Terms are not retroactive** | older posts keep what they were signed with |
| ⛔ **Nothing states terms for you** | if you never choose, your posts carry no block, and that is a true statement about your intent |

## Next

- [Publish a post, and have a stranger prove it is yours](15-publish-and-be-proven.md) —
  the signature these terms ride inside
- [Set your terms](../guides/set-your-terms.md) — the guide, for choosing between the
  profiles rather than running the commands
