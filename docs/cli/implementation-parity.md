# CLI implementation parity — Go and bash

*For* [Writers](../README.md#writing-on-polis) · [Contributors](../README.md#contributing-to-polis) — *Kind* [Reference](../README.md#kinds-of-page) — *Component* [CLI](README.md)

**Audience:** CLI users deciding which binary they need, and developers changing either one.

Polis has two CLI implementations. They share **data formats** — a site written by one is readable
and writable by the other. They no longer share a **feature set**.

> ⚠️ **The Go CLI leads. The bash CLI is a REFERENCE IMPLEMENTATION** — and that is a stronger claim
> than *legacy*. ⭐ **`docs/general/concepts/snap-off-architecture.md` points at it as the way to build
> your own client on the same file format, which means bash's coverage tells a third party what the
> protocol IS.** Parity is kept where it is feasible, and where it is not the gap is documented here.
> bash will eventually be frozen outright; today it is still part of how polis is presented.
>
> **So the order is: keep parity where feasible → document the gap where not → never ship a silent
> divergence.** It receives correctness and security fixes, and **protocol-format changes get a
> parity attempt before they get a gap entry.** Its `VERSION` tracks the Go CLI's so the two never disagree about what
> release they belong to, but a matching version number does **not** mean matching capability.

## Command matrix

| Command | bash | Go | Note |
|---|---|---|---|
| `about` `blessing` `comment` `discover` `extract` `follow` `index` `init` `notifications` `post` `preview` `rebuild` `register` `render` `republish` `tag` `unfollow` `unpublish` `unregister` `version` | ✅ | ✅ | shared |
| `rotate-key` | ✅ | ✅ | ⭐ **Key-history parity kept — see below** |
| `clone` | ✅ | ✅ | ⚠️ **behaviour differs — see below** |
| `actor` | — | ✅ | an operator's actor registry. ⭐ **Not a parity gap worth closing** — it is an OPERATOR command, and a bash-CLI self-hoster running system actors of their own is not a shape that exists. ⚠️ **`actor verify <action>` is the exception to that reasoning**: the stranger's check is run by *anyone*, not an operator — a bash user has no one-command way to check an actor from outside, and `custody.md` §7 is the by-hand procedure. ⚠️ **`actor verify --custody <domain>` is the same exception**: a tenant's own check of custody, run by the tenant rather than an operator — a bash user follows `custody.md` §12.5 by hand. Bash has no `attest` command, so it neither issues custody records nor rewrites them (no `withdrawn_by` drift is possible) |
| `attest` | — | ✅ | signed attestations |
| `did` | — | ✅ | `did:web` identity document |
| `dm` | — | ✅ | encrypted direct messages |
| `license` | — | ✅ | outbound licence |
| `site` | — | ✅ | `site set author-name` / `site set avatar` edit `.well-known/polis` through the lossless writer; `site rewrite-unsigned` is the escape hatch for a signed file a newer polis wrote. ⭐ **Not a parity gap worth closing for the edits** — bash edits the document with `jq`, which already keeps every member. ⚠️ **The escape hatch has no bash counterpart** — bash's one refusing writer (tags) points at the Go CLI's: see *Signed files a newer polis wrote — refused, never re-signed* below |
| `serve` | — | ✅ | webapp-only; **not a divergence** — bash has no HTTP server by design |
| `validate` | — | ✅ | site verification — per-URL JSON records, index consistency across all four entry types. Bash has no counterpart, so a bash user checks a record by hand against [`spec/attestation.md`](../signet/spec/attestation.md) §9. ⚠️ **Judge and Patrol are Go-only hosted actors**; their checks of registration attestations and retired-key resolution have no bash surface to diverge from |

**Eight Go-only commands** (seven, not counting `serve`). **Zero bash-only commands.** bash is a strict subset.

## Behavioural divergence within shared commands

### Smaller divergences in shared commands

| Command | bash | Go |
|---|---|---|
| **Flag position** | flags anywhere | ⚠️ **flags must come before positional arguments** — Go uses the standard `flag` package, which stops at the first positional, so `polis post file.md --filename x` silently ignores `--filename` |
| `comment` | `comment <file> [<reply-to-url>]` and `comment - <url>` (stdin); signs, publishes and sends the blessing request in one step (`cmd_comment`) | `comment draft <url>` · `sign <id>` · `list` · `sync` (`pkg/cmd/comment.go`). ⚠️ **No Go CLI command sends the blessing request** — `sync` only checks status; the web app does it |
| `post` from stdin | `polis post - [--filename] [--title]` | ❌ a file argument only |
| `post` and the site's terms | ❌ a new post carries no `license:` block, even on a site that states terms; there is no `--license` | ✅ stamps the site's terms (or `--license`) into the post's signed frontmatter |
| `init` flags | `--site-title` plus path flags (`--posts-dir`, `--comments-dir`, `--keys-dir`, `--versions-dir`, …) | `--site-title` `--author` `--email` `--theme` `--license`; no path flags — paths are fixed by the bundle |
| `index --json` | `{version, posts, comments}`; entries of any other type dropped | envelope with `data.entries` and `data.count`, every type |
| `blessing grant` / `deny <ref>` | `<ref>` is a full version or a **short hash** (prefix/suffix match); grant also checks the comment URL is reachable first | `<ref>` is the full version or the comment URL — no short hashes, no reachability check. Both resolve the pending request from it before telling the discovery service |
| `unregister` | asks for confirmation; `--force` / `-f` skips it | asks nothing, has no `--force` |
| `discover --since <date>` | ✅ | ❌ (only `--author`) |
| `notifications clear` | ❌ (`list` only) | ✅ |
| `follow` announces | ✅ emits `pub.polis.follow.announced` | ❌ writes `following.json` only; the web app's follow announces (`following.FollowWithBlessing`) |

### `init` — the Rosie question

Interactive Go `polis init` asks whether to switch Rosie on (nothing pre-selected) and, on `yes`, writes a
`pub.polis.attestation.grant` on the new site. **bash `init` does not ask**, and writes no grant.
⭐ **Not a gap worth closing:** Rosie runs only inside the web app's server sync, which bash has no
counterpart to, so a grant written by bash would switch on a helper that never runs. A bash user who later
runs the web app switches her on in **Settings → Rosie**. Bash has no `attest` command and never rewrites
attestation records, so it cannot drop a grant's `withdrawn_by` pointer. ⚠️ **Bash never writes the
marker either**: bash blessing writes append with `jq`, which keeps unknown entry keys, but bash clears the
list's signature on write — a marked list touched by bash is unsigned afterwards, with its markers intact.

### `clone`

| Aspect | bash | Go |
|---|---|---|
| `.well-known/polis` stored verbatim | ✅ | ✅ *(fixed — it was being re-serialized, dropping `bundles`, `license` and `public_key_messages`)* |
| Comments downloaded | ✅ | ✅ *(fixed — the download loop filtered on type `post`)* |
| `license` carried | ❌ | ✅ found by **pointer**, never by convention |
| `.well-known/did.json` carried | ❌ | ✅ |
| Content dirs resolved from the bundle | ❌ hardcodes `content/pub.polis.core` | ✅ |
| `tag` / `attestation` carried | ❌ | ✅ when the source enumerates them in `index.jsonl`. A source that carries no line of a type is still reported in `not_enumerable` — a site with no tags and a site running an older CLI are indistinguishable over HTTP |
| A source-supplied path that would land **outside** the clone folder | ✅ refused, but **not named**: an entry path lands in the generic `errors` count beside a failed download, and the optional `.versions` / `.html` paths derived from it are not counted at all | ✅ refused, **named**: `rejected_paths` in `--json`, *"Refused (would write outside the clone folder)"* in human output. Either way the rest of the clone continues |
| Which paths are checked | the index entry path (and the `.versions` / `.html` paths derived from it) — **the only remote-supplied paths bash uses** | every remote-supplied path: index entries, each `bundle.json` location, a bundle's own name and each type's `dir`, the `blessed.json` / `following.json` paths those resolve to, the `license` pointer, `.well-known/{polis,did.json}` |

⚠️ **The second row is a consequence of the row above it, not a second gap.** bash fetches fixed paths
and never reads a foreign site's bundle declarations, so the declaration-supplied paths do not exist for
it to check. What both refuse: an absolute path, one that climbs out with `..`, and one that would leave
through a symlink already inside the target.

**Consequence:** a bash clone cannot be checked for licence signatures, and `polis validate <path>`
(Go-only anyway) will report more `not_applicable` families against one.

### `rebuild`

⭐ **Bash was the correct implementation until the Go rebuild was fixed.** Its
`rebuild_index()` walks content, reads `type:` from each file's frontmatter, and
writes both post and comment entries — one walk, every type, one write. The Go
port walked `post/` only and overwrote the whole file, deleting every comment
entry, in a third entry schema the site's own integrity check rejected.

Go's rebuild is now correct **and ahead**. They diverge in four ways:

| Aspect | bash | Go |
|---|---|---|
| Content types indexed | post, comment | post, comment, **tag, attestation** |
| What a partial flag touches | `--posts` rewrites the post and comment lines from both dirs and carries every other line through | each flag rebuilds **only its own type's entries** and preserves every other line byte-identically |
| Flags | `--posts` `--comments` `--notifications` `--all` | adds `--tags` `--attestations`; `--notifications` is a deprecated alias for `polis notifications clear` and is no longer part of `--all` |
| Content dirs | hardcodes the default layout | resolved from the bundle pointer |

**Consequence:** bash does not index tags or attestations, and its tag commands
do not refresh the index — but a bash rebuild (and so bash `rotate-key`, which
calls it) **preserves the lines it does not produce**, so it no longer
un-publishes what the Go CLI indexed. *(Earlier versions truncated the
file and dropped them.)* A tag edited or deleted with bash
leaves its index line stale until a Go write, a Go `rebuild`, or Medic on a
hosted site brings it back in line.

⚠️ **This is a divergence, not a bug to port.** bash is feature-frozen; the
entry to make here is that it is now the *behind* implementation for this
command, where it used to be the reference.

### `rotate-key`

⭐ **Parity was KEPT here, deliberately, and that decision is the interesting part.**

Every polis site publishes a key history — `public_key_history` in
`.well-known/polis`, one entry per key, each carrying the previous key's signature over the handover —
so that a rotation stops making everything the site ever signed unverifiable. There are **three**
rotation implementations (the Go CLI, the webapp handler, and bash), and **all three append**.

⛔ **The first proposal was to make bash `rotate-key` REFUSE. That was reversed, and the
reasoning is the reason this section exists.** `snap-off-architecture.md` points a third party at bash
and says *"build a new one on the same file format."* **If the reference implementation does not
maintain a key chain, the reference says a key chain is not part of the protocol** — and anyone
building from it inherits the gap. A refusal would have fenced off the very implementation that
demonstrates the format is implementable without Go.

It turned out to be cheap: `cmd_rotate_key` already computed everything an entry needs — the
transition signature, the timestamp, and both keys — and `update_wellknown_pubkey` already edited the
identity document with `jq`. The append is one more expression in a helper that already existed.
`cmd_init` writes the genesis entry in the same `jq -n` that builds the document.

| Aspect | bash | Go |
|---|---|---|
| Appends to `public_key_history` on rotation | ✅ | ✅ |
| Writes the genesis entry at `init` | ✅ | ✅ |
| Chain verifies with the other implementation's verifier | ✅ **asserted by a test** | ✅ |
| Writes `id_ed25519.old` | ❌ *(retired)* | ❌ *(retired)* |
| `--delete-old-key` | ❌ *(retired)* | ❌ *(retired)* |
| Rebuilds `.well-known/did.json` after rotation | ❌ **removes it** | ✅ republishes, with retired keys in `verificationMethod` |
| `preview` verifies a post signed **before** a rotation, through the published chain | ❌ current key only | ✅ current key first, then the key the chain resolves for the artifact's claimed `published:` |
| Carries the discovery service's rotation **witness** in the new entry | ❌ discards it | ✅ |

⚠️ **Accepted divergence: bash neither PUBLISHES nor READS witnesses.** Bash
registration sends no `metadata.artifact_hash`, ignores the `witness` the discovery service returns
from content registration and from key rotation, and writes no witness set or `witnesses` pointer.
Its `preview` reports no witness axis. **Nothing breaks:** a witness is never required, so a
bash-published or bash-rotated site is simply unwitnessed — a weaker claim, stated as such by any
verifier — and the chain bash writes stays byte-compatible (`witnesses` is omitted when empty). Left
behind deliberately: bash is feature-frozen, and publishing witnesses correctly needs a merge that
never removes a record, which is Go code rather than a `jq` expression. The Go CLI and webapp carry
it; the format is in `docs/signet/spec/witness.md`.

⚠️ **Accepted divergence: bash `preview` READS no key history.** Bash *writes* the
chain correctly — the row above is about *reading* it. `cmd_preview` verifies against the site's
current `public_key` only, so after a rotation it reports every earlier post as a signature failure,
which the Go CLI's `preview` and `validate` do not. Left behind deliberately: bash is feature-frozen,
and resolving a retired key means verifying every handover signature in the chain before trusting an
entry — real verification code, not a `jq` expression. ⭐ The format stays implementable from bash;
this is a missing *reader*, and `docs/signet/spec/key-history.md` §4 carries the rule.

⭐ **The parity claim is tested, not asserted.** `cli-go/pkg/site/testdata/bash_rotated_wellknown.json`
is real output — `polis init` plus two `polis rotate-key` runs against the bash `polis` script, captured
verbatim — and `TestChainProducedByTheBashCLIVerifiesHere` requires the Go verifier to accept it. The
two maintain the chain through completely different code (a `jq` filter and a Go struct), so nothing
short of that would catch a drift in field names, ordering, or timestamp formatting.

**The one accepted gap: `did.json`.** bash removes the document rather than rebuilding it, because it
has never written that file and projecting a JWK out of an OpenSSH key in bash is not worth the
parity. Removal stays correct — the document is derived, so `polis did --write` rebuilds it, and an
absent document fails honestly where a stale one answers `200` with a retired key. ⚠️ **A tenant
rotated with bash will therefore have no DID document until someone republishes it**, which Judge's
key-head check reports and Medic heals on the hosted fleet.

⚠️ **`cmd_rotate_key` calls `cmd_rebuild --posts`,** so the `rebuild` divergence above applies to a
bash rotation on a site with tags or attestations: their index lines are carried through untouched,
not refreshed.

### `republish` — a post carrying licence terms is refused

bash rebuilds a post's frontmatter from a fixed field list on republish, so it cannot carry a
`license:` block through. Rather than drop the author's signed terms and re-sign a post that would
still verify, **bash `republish` refuses a post with a top-level `license:` key** (`UNSUPPORTED_CONTENT`)
and points at the Go CLI, which preserves the block. bash has no `license` command, so this only
arises on a site whose terms were set with the Go CLI or the web app.

### Signed files a newer polis wrote — refused, never re-signed

The Go writers of the six signed JSON types **refuse** to rewrite a file carrying a member they do not
recognise ([signing base §6.2](../signet/spec/signing-base.md)). bash edits with `jq`, which keeps
every member, so it never drops one — and it splits by what it does next:

| File | bash | Consequence |
|---|---|---|
| `following.json`, `blessed.json` | edits with `jq`, then `del(.signature)` | ✅ **safe** — preserved and unsigned asserts nothing |
| tag files (`polis tag apply/remove`) | **refuses**, naming the members, before any edit — the same rule and wording as Go | ✅ **resolved** — it used to keep the member with `jq` and re-sign over its own field list, putting a signature on a file carrying content the signature did not cover. bash has no `site rewrite-unsigned`; the refusal points at the Go CLI's |

## The policy, and why it is not just neglect

Core logic lives in `cli-go/pkg/`, which the webapp imports. A capability implemented there is
available to the CLI, the bundled binary and the web UI at once. The same capability in bash would
be a fourth copy that nothing else can call — and the rule *create no new copies* exists
because copies drift silently.

`polis validate` is the clearest case: it runs the **actors' own predicates** from
`cli-go/pkg/sitecheck`, the same code Patrol and Judge run in production. A bash reimplementation
would be a second opinion that agrees until it doesn't, which is worse than not having one.

So bash falls behind **deliberately**, on capability. It does not fall behind on correctness.

## ⚠️ For developers: bash is a reference implementation, not just a legacy artifact

Two of the three `clone` bugs fixed in the Go CLI, and the `rebuild` bug described above, were
**regressions introduced by the Go port** — bash had them right. When changing a command that exists
in both, read the bash implementation first. It is often the older, more carefully-worn path. The Go
`rebuild` fix did exactly that: bash's `rebuild_index()` is the working reference the Go contributor seam was
designed against.

The reverse check still applies in its original direction: **when touching `cli-go/` or `webapp/`,
check the bash CLI's `polis` script (`cmd_*`) for parity and flag divergence.** This document is where a
newly-accepted divergence gets recorded.

## When bash is still the right tool

- No Go toolchain, no binary for your platform, and you can read the script you are running
- Auditing what the CLI does without compiling anything — it is one readable file
- Systems where a ~9,000-line bash script with `jq` is easier to justify than a ~15 MB binary

## Related

- `docs/cli/README.md` — architecture and build targets
- `docs/cli/user/command-reference.md` — full command documentation
- `docs/general/concepts/content-system.md` — **the `index.jsonl` entry schema**, which both
  implementations write

⚠️ **Dead bash code, not a divergence:** `update_domain_in_blessed_comments` and
`update_domain_in_comment_file` are left over from a removed domain-migration command and have no caller.
The first also filters on a `.posts` key `blessed.json` does not have. Neither CLI has a domain-move command.
