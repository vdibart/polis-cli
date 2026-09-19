# Polis CLI JSON Mode

*For* [Developers](../../README.md#building-on-polis) — *Kind* [Reference](../../README.md#kinds-of-page) — *Component* [CLI](../README.md) — *Code* [`cli-go/pkg/cmd`](../../../cli-go/pkg/cmd)

## Feature Description

JSON mode adds machine-readable output to the polis CLI via a global `--json` flag. The flag can be
placed anywhere in the command:

```bash
polis --json post article.md   # Flag at start
polis post article.md --json   # Flag at end (also works)
```

When `--json` is enabled:
- Output is JSON on **stdout** — errors included (see *Errors*)
- Interactive prompts are skipped: `polis init --json` asks neither the licence nor the Rosie question,
  and states nothing about either
- Exit code is `0` on success and `1` on failure, with the per-command exceptions noted below
  (`validate`, `attest verify`, `actor verify` exit `1` on a *finding*, not only on a failure to run)

⚠️ **This page documents the Go CLI**, the recommended binary. The bash CLI is feature-frozen and its
JSON differs: it wraps nearly every result in the standard envelope, writes errors to **stderr** with a
`code`, and uses different field names for several commands (called out per command below). A script
that must run against both should test against both.

## Use Cases

### 1. Automated Publishing Workflows

```bash
# Publish multiple drafts and collect their versions
for draft in drafts/*.md; do
  result=$(polis --json post "$draft")
  echo "$draft: $(echo "$result" | jq -r '.version')"
done
```

⚠️ `polis post` **moves** its input file into the content tree — the draft is gone from `drafts/`
afterwards.

### 2. Comment Management Automation

```bash
# Auto-approve all pending blessing requests
requests=$(polis --json blessing requests)
# blessing grant takes the comment version, not the record id
echo "$requests" | jq -r '.data.requests[].comment_version' | while read version; do
  polis --json blessing grant "$version"
done
```

### 3. CI/CD Integration

```bash
# Publish in a CI pipeline
if ! result=$(polis --json post article.md); then
  echo "::error::Publication failed: $(echo "$result" | jq -r '.error')"
  exit 1
fi
echo "version=$(echo "$result" | jq -r '.version')" >> "$GITHUB_OUTPUT"
```

### 4. Content Validation

```bash
# Where init put the keys
polis --json init > init-result.json
jq '.data.key_paths' init-result.json

# Rebuild the content index and see what changed
polis --json rebuild --all | jq '.data.content_index'

# Check the whole site; fail the job on any failed check
polis --json validate . | jq '.totals'
```

## Response Shapes

⚠️ **The Go CLI does not use one shape for every command.** There are three success shapes and one
error shape. Every map-shaped response also carries a top-level **`request_id`** (a UUID correlating
the run with discovery-service logs).

### The standard envelope

Most commands — `about`, `actor`, `attest`, `blessing requests`, `blessing sync`, `blessing beseech`,
`clone`, `did`, `discover`, `dm`, `extract`, `follow`, `index`, `init`, `license`, `notifications`,
`preview`, `rebuild`, `rotate-key`, `site`, `tag`, `unfollow`, `unpublish`, `version`:

```json
{
  "status": "success",
  "command": "command-name",
  "data": { },
  "request_id": "b1b9e295-6e5f-4076-9479-4b3281c89f8a"
}
```

`command` is not always the typed name: every `attest`, `actor` and `tag` subcommand reports `attest`,
`actor` and `tag`, and the blessing subcommands report `blessing-requests`, `blessing-sync` and
`blessing-beseech`.

### A flat object

`post`, `republish`, `comment draft`, `comment sign`, `render`, `register`, `unregister`, `blessing grant`
and `blessing deny` put their fields at the **top level**, with a `success` boolean and no `data`
(`comment list` and `comment sync` are flat too, and carry no `success`):

```json
{ "success": true, "path": "…", "version": "sha256:…", "request_id": "…" }
```

### A report

`polis validate` emits its report object directly — see [`polis validate`](#polis-validate).

### Errors

A command that fails prints, **on stdout**, and exits `1`:

```json
{ "success": false, "error": "Failed to read file: open hello.md: no such file or directory" }
```

There is **no error code** — `error` is a human-readable sentence, and a script should branch on the
exit status, not parse it. The one differently shaped error is `blessing beseech` for a comment the
discovery service does not know, which returns `{"status": "error", "command": "blessing-beseech",
"error": {"code": "NOT_FOUND", "message": …}}` — and, like every other failure, exits `1`.

> **bash:** errors are `{"status": "error", "command": …, "error": {"code", "message", "details"}}` on
> **stderr**. Its codes include `FILE_NOT_FOUND`, `INVALID_INPUT`, `API_ERROR`, `SIGNATURE_ERROR`,
> `MISSING_DEPENDENCY`, `PERMISSION_ERROR`, `INVALID_STATE` and `UNSUPPORTED_CONTENT`.

## Command-Specific JSON Responses

### `polis init`

Captured from `POLIS_BASE_URL=https://alice.example polis --json init` (lists abridged):

```json
{
  "status": "success",
  "command": "init",
  "data": {
    "did": "did:web:alice.example",
    "directories_created": [".well-known", ".polis", ".polis/keys", "content/pub.polis.core/post", "content/pub.polis.core/comment", "…"],
    "files_created": [
      ".well-known/did.json",
      "content/pub.polis.core/bundle.json",
      "content/pub.polis.core/index.jsonl",
      "content/pub.polis.core/follow/following.json",
      "content/pub.polis.core/comment/blessed.json",
      ".well-known/polis",
      ".polis/keys/id_ed25519",
      ".polis/keys/id_ed25519.pub",
      "…"
    ],
    "key_paths": {
      "private": ".polis/keys/id_ed25519",
      "public": ".polis/keys/id_ed25519.pub"
    }
  },
  "request_id": "…"
}
```

### `polis about`

Captured from a freshly initialised site:

```json
{
  "status": "success",
  "command": "about",
  "data": {
    "cli_version": "<version>",
    "config": {
      "comments_dir": "content/pub.polis.core/comment",
      "keys_dir": ".polis/keys",
      "posts_dir": "content/pub.polis.core/post",
      "snippets_dir": "site/snippets",
      "themes_dir": "site/themes"
    },
    "discovery": {
      "registered_at": "",
      "registration_status": "not registered",
      "url": "https://ds.polis.pub"
    },
    "site": {
      "active_theme": "zane",
      "author_name": "alice",
      "base_url": "https://alice.example",
      "comment_count": 0,
      "created": "2026-09-16T17:13:10Z",
      "domain": "alice.example",
      "following_count": 0,
      "post_count": 0,
      "public_key": "ssh-ed25519 AAAA… polis-local",
      "site_title": "alice"
    }
  },
  "request_id": "…"
}
```

> **bash:** a different shape — `site`, `versions`, `configuration`, `keys`, `discovery`, `project`.

### `polis post <file>`

A flat object. Captured:

```json
{
  "success": true,
  "path": "content/pub.polis.core/post/20260916/hello-world.md",
  "title": "Hello World",
  "version": "sha256:2fdd0b9dc35e735ad5600d2ebfc2228829d24a864aae0f86c56673f3b89e5174",
  "signature": "-----BEGIN SSH SIGNATURE-----\n…\n-----END SSH SIGNATURE-----\n",
  "request_id": "…"
}
```

> **bash:** the envelope, with `file_path`, `content_hash`, `timestamp`, `signature`, `canonical_url`.

### `polis republish <file> [new-content.md]`

A post returns the same flat shape as `post` — `success`, `path`, `title`, `version` (the **new**
version), `signature`. A comment returns `success`, `comment_id`, `path`, `version`, `blessing_state`,
`rebeseeched` and `deferred`.

> **bash:** the envelope, with `file_path`, `previous_version`, `new_version`, `timestamp`, `signature`.

### `polis preview <url>`

```json
{
  "status": "success",
  "command": "preview",
  "data": {
    "url": "https://alice.com/posts/2026/01/hello.md",
    "type": "post",
    "title": "Hello World",
    "published": "2026-01-15T12:00:00Z",
    "current_version": "sha256:abc123...",
    "generator": "polis-cli-go/0.65.0",
    "in_reply_to": null,
    "author": "alice@example.com",
    "signature": {
      "status": "valid",
      "message": "Signature verified against author's public key",
      "key": {
        "source": "current",
        "epoch": 0
      }
    },
    "hash": {
      "status": "valid"
    },
    "validation_issues": [],
    "body": "# Hello World\n\nThis is my first post..."
  }
}
```

**`signature.key`** — present when `status` is `"valid"`, and says **which** of the site's keys
verified it:

- **`source: "current"`** — the key the site publishes now. `epoch` is its position in the site's
  published key history, or `null` when the site publishes no usable history.
- **`source: "retired"`** — the current key did **not** verify it; a key the site's published history
  says was current at the artifact's **claimed** signing time did. Only a post signed before a key
  rotation looks like this:

```json
"signature": {
  "status": "valid",
  "message": "Signature verified against a RETIRED key (epoch 0, current 2026-03-03T05:35:09Z → 2026-09-15T10:22:03Z), the key the site's published history says was current at the artifact's CLAIMED signing time 2026-05-01T09:00:00Z — not against the key the site publishes now. The signing time is the artifact's own claim and nothing here checks it.",
  "key": {
    "source": "retired",
    "epoch": 0,
    "claimed_signing_time": "2026-05-01T09:00:00Z",
    "valid_from": "2026-03-03T05:35:09Z",
    "valid_until": "2026-09-15T10:22:03Z"
  }
}
```

⚠️ **`status` is `"valid"` in both cases — read `key.source` if the difference matters to you.** A
retired-key pass says *the artifact verifies against the key that was current at its claimed signing
time*; `published:` is self-reported, so it is a weaker claim than a current-key pass. Which one you
accept is your policy. See [`key-history.md` §4](../../signet/spec/key-history.md).

**`witness`** — whether a discovery service countersigned **these exact bytes**, read from the site's
published witness set. Always present on a successful preview:

```json
"witness": {
  "state": "witnessed",
  "earliest_witnessed_at": "2026-09-13T12:00:01.500Z",
  "earliest_binds": "artifact",
  "checks": [
    { "ds": "https://ds.polis.pub", "ds_key_id": "ds-primary",
      "witnessed_at": "2026-09-13T12:00:01.500Z", "status": "verified", "binds": "artifact" }
  ]
}
```

- **`state`** — `witnessed`, `unwitnessed`, `unverifiable` (a witness names these bytes but its
  discovery service's key could not be fetched), or `invalid` (a published witness for these bytes
  does **not** verify against its discovery service's key — the site claims testimony it does not
  have). ⛔ **None of the four affects `signature` or `hash`.** Only `invalid` is a failure; no witness
  and an unreachable discovery service never are.
- **`earliest_binds`** — `artifact` (the whole signed post, via `artifact_hash`) or `version` (only
  what `current_version` hashes — for a post or comment, the body).
- **`checks[].status`** — `verified`, `invalid` (a record whose signature does not verify — makes
  `state` `invalid`), `not_checked` (with `reason`), or `other_bytes` (true testimony about a
  different version of this artifact — normal, never a failure).

See [`witness.md`](../../signet/spec/witness.md).

### `polis comment`

In the Go CLI a comment is written in steps — `draft`, then `sign`, then `sync` — and each step returns
a flat object.

`polis comment draft <url>`:

```json
{ "success": true, "id": "…", "in_reply_to": "https://bob.example/content/pub.polis.core/post/20260101/hello.md" }
```

`polis comment sign <id>` (moves the draft to pending):

```json
{ "success": true, "id": "…", "version": "sha256:…", "signature": "-----BEGIN SSH SIGNATURE-----…" }
```

`polis comment list [drafts|pending|blessed|denied|all]` returns `{"comments": [...]}`; with `all`, each
entry is `{"status", "id"}`.

`polis comment sync` returns the ids that moved: `{"blessed": [...], "denied": [...], "still_pending":
[...], "errors": [...]}`.

> **bash:** `polis comment <file> <url>` in one step, returning the envelope with `file_path`,
> `content_hash`, `in_reply_to`, `timestamp` and the beseech result under `beseech`.

### `polis blessing sync`

```json
{
  "status": "success",
  "command": "blessing-sync",
  "data": {
    "synced": 3,
    "existing": 20,
    "total": 23
  }
}
```

### `polis blessing requests`

```json
{
  "status": "success",
  "command": "blessing-requests",
  "data": {
    "count": 3,
    "requests": [
      {
        "id": "42",
        "comment_url": "https://alice.com/comments/reply.md",
        "comment_version": "sha256:f4bac5d0...",
        "in_reply_to": "https://bob.com/posts/hello.md",
        "root_post": "",
        "author": "alice.com",
        "timestamp": "",
        "created_at": "2026-01-05T12:00:00Z"
      }
    ]
  }
}
```

Notes:

- `id` is a **string**, not a number.
- `comment_version` is the value `blessing grant` and `blessing deny` take as
  their argument — not `id`.
- `root_post` and `timestamp` are **reserved and currently always `""`**. They
  are part of the serialized shape but no code populates them; do not depend on
  them.
- `count` reflects the requests returned — incoming requests only, i.e. those
  whose `in_reply_to` is a post on your own domain.

### `polis blessing grant <version>`

A flat object:

```json
{
  "success": true,
  "comment_url": "https://alice.com/comments/reply.md",
  "comment_version": "sha256:f4bac5d0...",
  "post_path": "…"
}
```

### `polis blessing deny <version>`

A flat object:

```json
{
  "success": true,
  "comment_version": "sha256:f4bac5d0..."
}
```

### `polis blessing beseech <version>`

⚠️ **The Go CLI does not re-beseech from this command.** It checks that the discovery service knows the
comment and points at `polis comment sync`, which re-beseeches pending comments:

```json
{
  "status": "success",
  "command": "blessing-beseech",
  "data": {
    "comment_version": "sha256:abc123...",
    "message": "Use 'polis comment sync' to re-beseech pending comments"
  }
}
```

A comment the service does not know returns the `NOT_FOUND` error object described under *Errors*.

### `polis follow <url>`

```json
{
  "status": "success",
  "command": "follow",
  "data": {
    "author_url": "https://alice.com",
    "author_email": "alice@example.com",
    "blessed_by": "bob@example.com",
    "comments_found": 5,
    "comments_blessed": 5,
    "added_to_following": true
  }
}
```

### `polis unfollow <url>`

The envelope, with `author_url`, `comments_found`, `comments_denied` and `removed_from_following`.

### `polis rebuild`

A `--posts` run:

```json
{
  "status": "success",
  "command": "rebuild",
  "data": {
    "posts_rebuilt": 8,
    "comments_rebuilt": 0,
    "notifications_cleared": 0,
    "content_index": {
      "rebuilt": {"post": 8},
      "preserved": {"comment": 23},
      "skipped": {"post": 1},
      "total": 31,
      "changed": true
    }
  }
}
```

`content_index` is present whenever the run touched `index.jsonl`, and reports
**per entry type**:

- `rebuilt` — lines this run regenerated
- `preserved` — lines carried through byte-identically because no flag in this
  run owned them. An entry type this CLI does not recognise appears here under
  its own name; a line that is not valid JSON appears as `(unreadable)`
- `skipped` — files walked and **not** indexed, e.g. a post with no `published`
- `changed` — `false` when the composed file is byte-identical to what was
  already on disk

`posts_rebuilt` and `comments_rebuilt` are kept for compatibility.
`comments_rebuilt` counts blessing records reconciled in `blessed.json`, which is
a different number from `content_index.rebuilt.comment`.

### `polis render`

A flat object. Captured:

```json
{
  "success": true,
  "posts_rendered": 1,
  "posts_skipped": 0,
  "comments_rendered": 0,
  "comments_skipped": 0,
  "index_generated": true
}
```

### `polis index`

Returns the entries of the site's content index (`index.jsonl`). Captured:

```json
{
  "status": "success",
  "command": "index",
  "data": {
    "count": 1,
    "entries": [
      {
        "type": "post",
        "path": "content/pub.polis.core/post/20260916/hello-world.md",
        "title": "Hello World",
        "published": "2026-09-16T17:13:16Z",
        "current_version": "sha256:2fdd0b9d…"
      }
    ],
    "skipped": 0,
    "skipped_lines": []
  }
}
```

`skipped` counts lines of `index.jsonl` that are not a JSON object, and `skipped_lines` gives their
1-based line numbers. They are left out of `entries` and `count`, and said here, so that a listing of
the readable part is never mistaken for the whole index. In human mode the same is printed on stderr.

Entries are the index lines as written, so a comment entry also carries `in_reply_to`, and tag and
attestation entries appear with `type` `tag` and `attestation`. The schema is in
[`content-system.md`](../../general/concepts/content-system.md).

> **bash:** an unwrapped `{"version", "posts": [...], "comments": [...]}` object.

### `polis clone <url> [target-dir]`

The envelope. Captured:

```json
{
  "status": "success",
  "command": "clone",
  "data": {
    "target_dir": "./alice",
    "posts_downloaded": 12,
    "comments_downloaded": 4,
    "blessed_comments_synced": 3,
    "tags_downloaded": 0,
    "attestations_downloaded": 1,
    "license_cloned": true,
    "did_document_cloned": true,
    "not_enumerable": ["pub.polis.tag"],
    "rejected_paths": [],
    "errors": 0
  }
}
```

`not_enumerable` names content types the source's index carries no line of, so the clone could not
collect them — *"no entries"*, never *"none exist"*.

⛔ **`rejected_paths` names paths the SOURCE SITE supplied that this clone refused to write**, because
they would have landed outside the clone folder: an absolute path, one climbing out with `..`, or one
leaving through a symlink already inside the target. Every path a clone writes comes from the source —
index entries, bundle declarations, the licence pointer — so each is checked first. A refused path is
skipped and the rest of the clone continues, which is why it is **not** counted in `errors`: nothing
failed, something was declined. A non-empty array on a site you did not expect it from is worth looking
at directly.

`errors` counts fetches or writes that failed, which is an ordinary network outcome and never fatal.

> **bash:** a different payload in the same envelope, and a different one per mode. Full:
> `mode`, `server_url`, `target_dir`, `posts_downloaded`, `comments_downloaded`, `versions_downloaded`,
> `html_downloaded`, `blessed_comments_cached`, `errors`. Incremental: `mode`, `server_url`,
> `target_dir`, `new_files`, `updated_files`, `unchanged_files`, `versions_downloaded`,
> `html_downloaded`, `blessed_comments_synced`, `errors`. Neither carries `license_cloned`,
> `did_document_cloned`, `not_enumerable` or `rejected_paths`, and bash counts a refused path in
> `errors` beside a failed download, so nothing distinguishes the two.

### `polis notifications`

```json
{
  "status": "success",
  "command": "notifications",
  "data": {
    "notifications": [
      {
        "id": "…",
        "rule_id": "…",
        "actor": "bob.example",
        "icon": "…",
        "message": "…",
        "link": "…",
        "event_ids": [101],
        "created_at": "2026-09-15T12:00:00Z"
      }
    ],
    "pending_blessings": [
      {
        "author": "alice@example.com",
        "in_reply_to": "https://bob.example/content/pub.polis.core/post/20260101/hello.md",
        "comment_url": "https://alice.example/content/pub.polis.core/comment/20260102/reply.md"
      }
    ]
  }
}
```

- **`notifications`** — unread local notifications (`--all` includes read ones, which carry `read_at`).
  `link` and `payload` are omitted when empty.
- **`pending_blessings`** — blessing requests still pending where **this site is the commenter**, read
  from the discovery service. ⚠️ It is `null`, not `[]`, when there are none or `POLIS_BASE_URL` is unset.

`polis notifications clear` returns `command: "notifications clear"` and `data.notifications_cleared`.

### `polis license`

With no argument, reports the terms the site currently states. `terms` is `null` when the site has
stated nothing (and `pointer` is `""`) — **absent means unstated**, which is a defined state and not
an error, so scripts should test for `null` rather than treating it as a failure.

```json
{
  "status": "success",
  "command": "license",
  "data": {
    "terms": {
      "v": "pub.polis.license.v1",
      "profile": "pub.polis.license.reserved/1",
      "train-ai": "n",
      "search": "y",
      "ai-input": "n",
      "attribution": "required",
      "terms": "https://alice.example/license",
      "contact": "https://alice.example/license",
      "asserted": "2026-08-27T14:02:00Z"
    },
    "pointer": "/content/pub.polis.core/license/license.json"
  }
}
```

Stating a profile returns the terms that were just signed. (`pointer` is not repeated — the location
did not change.)

```json
{
  "status": "success",
  "command": "license",
  "data": {
    "terms": {
      "v": "pub.polis.license.v1",
      "profile": "pub.polis.license.open/1",
      "train-ai": "y",
      "search": "y",
      "ai-input": "y",
      "terms": "https://alice.example/license",
      "contact": "https://alice.example/license",
      "asserted": "2026-08-28T09:15:00Z"
    }
  }
}
```

`polis license none` withdraws the terms and returns the same shape as a site that never stated any —
`terms: null`, `pointer: ""` — so a script has one answer to *"what does this site say"* rather than
two.

### `polis validate`

⚠️ **Not the standard envelope.** `polis validate` emits the report object directly, because the
report *is* the answer — there is no `data` payload wrapped around a status word.

Captured from a real run against a clone (`polis --json validate ./alice`), abridged:

```json
{
  "target": "./alice",
  "form": "local",
  "scope": "site",
  "checks": [
    {
      "id": "identity.well_known",
      "family": "key-handle-alignment",
      "outcome": "passed",
      "detail": "identity document present, parseable, and publishes a key",
      "examined": 1
    },
    {
      "id": "identity.key_perms",
      "family": "key-handle-alignment",
      "outcome": "not_applicable",
      "reason": "no .polis/ directory — this is a published copy rather than a working site, so owner-only state (keys, permissions, private policies, bundle registry) is not present to check",
      "examined": 0
    },
    {
      "id": "content.posts",
      "family": "signed-content-integrity",
      "outcome": "passed",
      "detail": "8 posts examined: 8 signature(s) verified, 0 unsigned",
      "examined": 8
    }
  ],
  "totals": { "passed": 10, "failed": 0, "warning": 0, "not_applicable": 7 }
}
```

⛔ **`outcome` has FOUR values — `passed`, `failed`, `warning`, `not_applicable` — and `totals` counts
each.** A script that reads `failed == 0` as "the site is fine" is wrong whenever `not_applicable > 0` —
those checks did not run, and `reason` says why. `warning` is something true about the site's
**surroundings** rather than the site (a managed `robots.txt` an intermediary added directives to): it
never fails the run and is never used for a defect the owner can fix. Read all four numbers.

- **`form`** — `"local"` (a directory) or `"remote"` (fetched over HTTP, storing nothing).
- **`scope`** — `"site"` or `"artifact"`. An `"artifact"` report covers ONE file's signature and hash
  and says nothing about the site around it; `note` carries that caveat in prose so the result cannot
  be quoted as a site-level claim.
- **`family`** — one of `signed-content-integrity`, `index-consistency`, `policy-parseability`,
  `key-handle-alignment`, `bundle-registry-health`.
- **`findings`** — present on failures, one string per problem. ⚠️ Also present on a **passed**
  `content.posts` / `content.comments` / `content.artifact` check when any artifact verified only
  against a **retired** key (a post signed before a key rotation): the `detail` counts them
  (`"2 signature(s) verified — 1 of them against a RETIRED key, at a claimed signing time — 0
  unsigned"`) and each finding names the artifact, the key's epoch and window, and its claimed signing
  time. A passed check with findings is still passed.
- **The witness checks** — ⛔ **`failed` for exactly one reason: a published witness that does not
  verify** against its discovery service's key. No witness, an unreachable discovery service, and a
  witness of a different version never fail:
  - **`content.witnesses`** — a census across the site's content (posts and comments; locally also
    attestations and tags): `detail` counts witnessed, unwitnessed, witnessed-but-unverifiable, and
    invalid-witness artifacts. `not_applicable` when the site publishes no witness set, or its
    `witnesses` pointer leads nowhere. `findings` name each witness that does not verify (first),
    a witness whose key could not be fetched, or a retired-key pass whose earliest witness post-dates
    the key's retirement.
  - **`content.witness`** — the same question for the one artifact of a per-URL run.
  - **`identity.key_history_witness`** — `passed` when a rotation in the chain carries a verified
    discovery-service witness (one finding per rotation, naming its witness and date); `failed` when a
    rotation witness does not verify; `not_applicable` when no rotation is witnessed or none could be
    checked. Either way the text says a witness cannot reveal a rotation left out of the chain.
- **`fatal_error`** — set when the run could not start at all (unreadable directory, unreachable
  host). When it is set, `checks` is empty and the absence of failures means nothing.

A per-URL run (`polis --json validate https://alice.example/content/pub.polis.core/post/20260101/hello.md`):

```json
{
  "target": "https://alice.example/content/pub.polis.core/post/20260101/hello.md",
  "form": "remote",
  "scope": "artifact",
  "note": "This result covers ONE artifact's signature and hash. Index consistency, key permissions, policy files and everything else site-wide were not examined.",
  "checks": [
    {
      "id": "content.artifact",
      "family": "signed-content-integrity",
      "outcome": "passed",
      "detail": "signature verifies against the key published at https://alice.example",
      "examined": 1
    },
    {
      "id": "content.hash",
      "family": "signed-content-integrity",
      "outcome": "passed",
      "detail": "body matches its current-version hash",
      "examined": 1
    }
  ],
  "totals": { "passed": 2, "failed": 0, "warning": 0, "not_applicable": 0 }
}
```

A per-URL run against a **JSON record** has the same shape; `detail` names the record type and the site
whose key verified it — for an attestation, its `issuer`. Captured from a real run against a live
record (`polis --json validate https://polis.polis.pub/content/pub.polis.core/attestation/20260913T020718Z-411630ed319bf4db.json`), `note` abridged:

```json
{
  "target": "https://polis.polis.pub/content/pub.polis.core/attestation/20260913T020718Z-411630ed319bf4db.json",
  "form": "remote",
  "scope": "artifact",
  "note": "This result covers ONE artifact's signature and hash. …",
  "checks": [
    {
      "id": "content.artifact",
      "family": "signed-content-integrity",
      "outcome": "passed",
      "detail": "attestation signature verifies against the key published at https://polis.polis.pub",
      "examined": 1
    },
    {
      "id": "content.hash",
      "family": "signed-content-integrity",
      "outcome": "passed",
      "detail": "record bytes match its current_version",
      "examined": 1
    },
    {
      "id": "content.witness",
      "family": "signed-content-integrity",
      "outcome": "not_applicable",
      "reason": "this site publishes no witnesses (no `witnesses` pointer in .well-known/polis), so none of its content carries an independent date. That is a weaker claim, not a defect: every artifact still verifies, or not, on its own signature.",
      "examined": 0
    }
  ],
  "totals": { "passed": 2, "failed": 0, "warning": 0, "not_applicable": 1 }
}
```

A record type that carries no `current_version` (follow file, blessing list) reports
`content.hash` as `not_applicable`. A JSON document of no record type the command recognises is a
`failed` `content.artifact`.

**Exit status:** `0` when nothing checked was found wrong, `1` otherwise (a `fatal_error` also exits
`1`). `warning` and `not_applicable` do not affect it.

### `polis did`

```json
{
  "status": "success",
  "command": "did",
  "data": {
    "did": "did:web:alice.polis.pub",
    "url": "https://alice.polis.pub/.well-known/did.json",
    "path": ".well-known/did.json",
    "document": { "@context": ["..."], "id": "did:web:alice.polis.pub" },
    "published": false,
    "on_disk": "up to date"
  }
}
```

`document` is the full DID Document inline. `published` is `true` only when `--write` was passed.
`on_disk` is `"up to date"`, `"missing or stale — run `polis did --write`"`, or `"unknown"` — the
stale case is worth checking in a script, because a stale document answers `200` with a key the site
has retired and a resolver cannot tell.

### `polis rotate-key`

Captured:

```json
{
  "status": "success",
  "command": "rotate-key",
  "data": {
    "new_public_key": "ssh-ed25519 AAAA… polis-local",
    "key_history_epoch": 1,
    "ds_rotation": "success",
    "did_published": true,
    "did_removed": false
  }
}
```

`key_history_epoch` is which entry in your site's own published key chain this rotation became — `1`
after the first rotation, `2` after the second. Genesis is `0` and is written by `polis init`. It is a
useful thing to assert in a script: it is the number that proves the handover was recorded where a
verifier will look for it.

`ds_rotation` says what happened at the discovery service: `"success"` when it was notified and accepted
the rotation, `"skipped"` when the site is not registered with one and nothing was sent. There is no
`"failed"`: a rejected or unreachable notification fails the whole command with an error envelope before
any key changes.

`did_published` is `true` when `.well-known/did.json` was republished with the new key (retired keys
stay in `verificationMethod`). If that republish fails, the document — which now names a retired key —
is removed and `did_removed` is `true`; run `polis did --write` to republish it. The rotation itself
always succeeds — a security operation is never blocked by a derived file.

⚠️ **`old_key` is gone**, along with `--delete-old-key` and the `id_ed25519.old` file it described.
No retired private key is kept any more; the published chain replaces it.

> **bash:** `new_key_fingerprint`, `key_history_epoch`, `ds_rotation`, `did_document` (`"deleted"` or
> `"absent"`). bash has never written `did.json`, so it removes rather than rebuilds it.
> `key_history_epoch` and `ds_rotation` are the only fields both emit — scripts that must work against both should branch on
> the keys they find.

### Tags — `polis tag list | show | apply | remove | delete`

Every tag subcommand reports `command: "tag"`. Captured:

```json
{"status": "success", "command": "tag", "data": {"count": 1, "tags": [{"tag": "favorite", "count": 1, "updated": "2026-09-16T17:14:11Z"}]}}
```

`tag show <name>` — `data`: `{"tag", "count", "targets": [{"uri", "added"}]}`.

`tag apply <name> <uri>` and `tag remove <name> <uri>` — `data`: `{"tag", "target_uri", "count"}`, where
`count` is the tag's target count **after** the change.

`tag delete <name>` — `data`: `{"tag", "deleted": true}`.

> **bash:** `command` is `tag-list`, `tag-apply`, `tag-remove`, `tag-delete`, with different `data`
> fields.

### `polis attest issue`

```json
{
  "status": "success",
  "command": "attest",
  "request_id": "74dfc568-de54-401a-aee3-bbaabd853765",
  "data": {
    "id": "20260828T235009Z-ce920cf135bf742f",
    "url": "https://alice.example/content/pub.polis.core/attestation/20260828T235009Z-ce920cf135bf742f.json",
    "issuer": "https://alice.example",
    "predicate": "pub.polis.attestation.correction",
    "subject": "https://site.example/posts/20260901-claim.md",
    "subject_type": "uri",
    "subject_version": "sha256:9f2a0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab",
    "asserted": "2026-08-28T23:50:09Z",
    "current_version": "sha256:ce920cf135bf742f374de7e41b3be1293aaf44c3093ed9ba83461fc53866a914"
  }
}
```

`id` is derived from `asserted` and `current_version` and is the record's filename stem; `url` is where
it is served. `subject_version` is `""` when the claim is not pinned.

### `polis attest withdraw <id>`

Captured from real command output, not written from the struct.

```json
{
  "status": "success",
  "command": "attest",
  "request_id": "45b48567-1119-4df9-b483-37f5fea5f9ca",
  "data": {
    "id": "20260829T042727Z-fe223808ac61a68f",
    "url": "https://alice.example/content/pub.polis.core/attestation/20260829T042727Z-fe223808ac61a68f.json",
    "predicate": "pub.polis.attestation.withdrawal",
    "withdrew": "20260829T042727Z-67e7d118db7385a3",
    "withdrew_url": "https://alice.example/content/pub.polis.core/attestation/20260829T042727Z-67e7d118db7385a3.json",
    "withdrew_version": "sha256:67e7d118db7385a39783290716b0c8bdc890555095cb823411c8f362dd38bbff",
    "asserted": "2026-08-29T04:27:27Z",
    "current_version": "sha256:fe223808ac61a68f806bc3eb3ce7afa877d2fd5452c113e67e2af897b2d8929a"
  }
}
```

⚠️ `id` is the **withdrawal**; `withdrew` is what it retracts. Both records remain published and both
still verify — a script must not treat a withdrawal as a signal to stop serving or fetching the
original.

### `polis attest register <id>`

Written from the handler, not captured (no discovery service in the capture harness).

```json
{
  "status": "success",
  "command": "attest",
  "data": {
    "id": "20260829T042727Z-67e7d118db7385a3",
    "url": "https://alice.example/content/pub.polis.core/attestation/20260829T042727Z-67e7d118db7385a3.json",
    "predicate": "pub.polis.attestation.agent-disclosure",
    "current_version": "sha256:67e7d118db7385a39783290716b0c8bdc890555095cb823411c8f362dd38bbff",
    "registered_with": "https://ds.polis.pub"
  }
}
```

A refusal is an error envelope and a non-zero exit — never a silent success.

### `polis attest list`

```json
{
  "status": "success",
  "command": "attest",
  "data": {
    "count": 1,
    "attestations": [
      {
        "id": "20260828T235009Z-ce920cf135bf742f",
        "predicate": "pub.polis.attestation.correction",
        "subject": "https://site.example/posts/20260901-claim.md",
        "asserted": "2026-08-28T23:50:09Z"
      }
    ]
  }
}
```

Ordered by `id`, which orders by assertion date.

### `polis attest show <id>`

```json
{
  "status": "success",
  "command": "attest",
  "data": {
    "id": "20260828T235009Z-ce920cf135bf742f",
    "issuer": "https://alice.example",
    "predicate": "pub.polis.attestation.correction",
    "subject": "https://site.example/posts/20260901-claim.md",
    "subject_type": "uri",
    "subject_version": "sha256:9f2a0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab",
    "payload": { "note": "the figure cited was revised" },
    "asserted": "2026-08-28T23:50:09Z",
    "generator": "polis-cli-go/0.67.0",
    "current_version": "sha256:ce920cf135bf742f374de7e41b3be1293aaf44c3093ed9ba83461fc53866a914",
    "signature_status": "valid"
  }
}
```

`signature_status` is one of `valid`, `unsigned`, `invalid`, `unknown`. A `signature_detail` key
carries the explanation when the status is `invalid` or `unknown`. A `signature_key` object
(`source`, `epoch`, and for a retired key `claimed_signing_time`, `valid_from`, `valid_until`) says
which key verified a valid record — `source: "retired"` means a key the site's published history says
was current at the record's `asserted` time, not the key it publishes now.

⚠️ **`unknown` includes "signed with fields this verifier does not understand."** A record carrying a
field this build does not declare cannot have its signing base rebuilt, so the honest answer is that
it was not checked — ⛔ **never `invalid`**, which would call a good record forged the day a newer
writer adds a field. `signature_detail` names the fields. A script must not treat `unknown` as a
failure, and must not treat it as a pass either: it is the third answer on purpose. The rule is
[`docs/signet/spec/signing-base.md` §6.1](../../signet/spec/signing-base.md).

### `polis attest verify`

```json
{
  "status": "success",
  "command": "attest",
  "data": {
    "records": { "20260828T235009Z-ce920cf135bf742f": "valid" },
    "counts": { "valid": 1 },
    "invalid": 0
  }
}
```

⚠️ **Branch on `invalid`, not on `status`.** The envelope `status` reports whether the *command* ran;
`data.invalid` reports whether anything failed to verify. `unsigned` and `unknown` are counted and are
**not** failures — the first is a fact about a record, the second means the check could not be made.
The process exit code follows the same rule and is non-zero only when `data.invalid` is above zero.

A `retired` object — id → sentence — appears only when some record verified against a **retired** key
(signed before a key rotation). Those records are still `valid` in `records`; `retired` says they are
the weaker claim.

### `polis actor register`

```json
{
  "status": "success",
  "command": "actor",
  "data": {
    "operator": "polis.polis.pub",
    "domain": "judge.polis.pub",
    "authority": "operator",
    "expected_actions": ["pub.polis.attestation.integrity"],
    "countersigned": true,
    "updated": false,
    "attestation_id": "20260905T140200Z-3f2a9c1d4e5b6a70",
    "event": "pub.polis.actor.registered",
    "announced": true,
    "registry_url": "https://polis.polis.pub/content/pub.polis.core/actor/registry.json"
  }
}
```

⚠️ **`announced: false` is not a failure.** A registry that exists and is not yet announced is a
normal, recoverable state — the file and the attestation are the durable artifacts, and the stream
event is delivery. It is `false` whenever `--no-announce` was passed, the discovery service was
unreachable, or the site is not registered with one.

`updated` distinguishes `pub.polis.actor.registered` from `.reregistered`, which is also in `event`.

**On a site that is not an operator it does nothing**, and says so in the same envelope — never a bare
`success` flag:

```json
{
  "status": "success",
  "command": "actor",
  "data": {
    "noop": true,
    "domain": "judge.polis.pub",
    "reason": "This site is not an operator (it publishes no actor_registry pointer), so polis actor register does nothing here: registering actors is operator administration."
  }
}
```

⚠️ **Check `data.noop` before reading the other fields** — a no-op carries none of them. On a site that
has a registry file but no `actor_registry` pointer the command **fails** instead (non-zero exit, error
object): that is a broken operator, not an ordinary site.

### `polis actor announce <domain>`

```json
{
  "status": "success",
  "command": "actor",
  "data": {
    "operator": "polis.polis.pub",
    "domain": "judge.polis.pub",
    "event": "pub.polis.actor.registered",
    "announced": true,
    "registry_url": "https://polis.polis.pub/content/pub.polis.core/actor/registry.json"
  }
}
```

⚠️ **Unlike `register`, a failed announcement is an error here** — announcing is this command's whole
job, so `announced` is always `true` on success.

### `polis actor withdraw <domain>`

```json
{
  "status": "success",
  "command": "actor",
  "data": {
    "operator": "polis.polis.pub",
    "domain": "judge.polis.pub",
    "remaining": 1,
    "disclosures_retired": 1,
    "event": "pub.polis.actor.withdrawn",
    "announced": true
  }
}
```

⚠️ **`disclosures_retired` counts WITHDRAWAL RECORDS WRITTEN, not records deleted.** The disclosures
themselves stay published and still verify — retraction is a signed claim, never a file deletion.

### `polis actor list`

```json
{
  "status": "success",
  "command": "actor",
  "data": {
    "operator": "polis.polis.pub",
    "asserted": "2026-09-05T14:02:00Z",
    "actors": [
      {
        "domain": "judge.polis.pub",
        "authority": "operator",
        "expected_actions": ["pub.polis.attestation.integrity"],
        "countersigned": true
      }
    ],
    "count": 1
  }
}
```

A site that publishes no registry returns `count: 0` and an empty list — **not an error.** Nearly
every site runs no actors.

### `polis actor verify`

```json
{
  "status": "success",
  "command": "actor",
  "data": {
    "registry_signature": "valid",
    "entries": [
      { "domain": "judge.polis.pub", "countersignature": "valid" },
      { "domain": "polis.polis.pub", "countersignature": "unsigned" }
    ]
  }
}
```

Both fields use the same four values as `attest verify`: `valid`, `unsigned`, `invalid`, `unknown`.

⚠️ **`countersignature: unsigned` is a weaker claim, not a broken one** — the entry says *"we say this
is ours"* rather than *"and the actor agrees"*. ⚠️ **`unknown` means the actor's published key was not
available to check against**, which is what you get without `--tenants-dir`; it is not a finding.
The process exits non-zero only when `registry_signature` is not `valid`.

### `polis actor verify <action>`

**The stranger's check** — `custody.md` §7 over one signed attestation, run from anywhere. Captured from
a real run against `polis.polis.pub`, which lists itself (details abridged):

```json
{
  "status": "success",
  "command": "actor",
  "data": {
    "action": {
      "source": "https://polis.polis.pub/content/pub.polis.core/attestation/20260913T020718Z-b185374e8127346a.json",
      "type": "pub.polis.attestation.agent-disclosure",
      "signer": "polis.polis.pub"
    },
    "operator": "polis.polis.pub",
    "operator_source": "self",
    "registry_url": "https://polis.polis.pub/content/pub.polis.core/actor/registry.json",
    "steps": [
      { "step": 1, "name": "signature", "outcome": "passed", "detail": "signature verifies against the key published at https://polis.polis.pub" },
      { "step": 2, "name": "operator", "outcome": "passed", "detail": "…" },
      { "step": 3, "name": "registry", "outcome": "passed", "detail": "…" },
      { "step": 4, "name": "entry", "outcome": "passed", "detail": "polis.polis.pub lists polis.polis.pub (operator authority)" },
      { "step": 5, "name": "countersignature", "outcome": "weaker", "detail": "no countersignature — …" },
      { "step": 6, "name": "expected_action", "outcome": "passed", "detail": "…" }
    ],
    "findings": []
  }
}
```

- **`action.type`** is what step 6 compares — for an attestation, its **predicate**.
- **`operator_source`** — `claimed` (the signer's own `operator` field; ⛔ a claim, which step 4
  corroborates), `given` (`--operator`), or `self` (the signer publishes its own registry). Absent when
  there was no operator to consult.
- **`steps[].outcome`** — `passed`, `failed` (always paired with a finding), `weaker` (step 5: no
  countersignature, or one that verifies only against a **retired** key of the actor — a countersignature
  carries no signing time, so that is never a full pass), `unknown` (could not check — a fetch failed, or
  at step 1, 3 or 5 a signature that does not verify while the document carries a field this build does
  not model), `not_applicable` (step 2, the signer
  names no operator and none was given), `not_reached` (an earlier step stopped the check, as §7
  requires).
- **`steps[].key_used`** — present **only** when a **retired** key verified that step's signature (step 1's
  action, step 3's registry, step 5's countersignature); **absent** on a current-key pass and on every step
  that verified nothing. Shape: `{"source":"retired","epoch":0,"valid_from":"…","valid_until":"…",
  "claimed_signing_time":"…"}`, the same object `polis validate` reports. At step 5 it has **no**
  `claimed_signing_time` — a countersignature signs none, which is why that case is `weaker`. ⭐ It is how
  a script tells step 5's two `weaker` cases apart (absent: no countersignature; present: a retired key)
  without reading `detail`, which says the same thing in prose.
- **`steps[].detail`** of a `passed` step ends with *"… not covered by the signature"* when the signed
  document carries members this build does not model — the signature verified over the fields it knows,
  and those are not covered by it.
- **`findings[].kind`** — ⛔ **read the kind, never just the count.** They are different facts:

| Kind | Step | Means |
|---|---|---|
| `unsigned` | 1 | nothing binds the action to its signer |
| `signature_invalid` | 1 | the action is not from the domain it names. Step 1 checks the signer's current key, then the key the signer's own published history resolves for the action's `asserted` time — so an action signed before the signer rotated passes, and its step-1 detail says a **retired** key verified it |
| `uncorroborated` | 2 | the operator publishes no registry, or its pointer leaves its own origin |
| `registry_untrusted` | 3 | the registry does not verify, or names a different operator — nothing in it counts. Step 3 walks the operator's key history by the registry's `asserted`, so a registry signed before the operator rotated still counts, and its step-3 detail says a **retired** key verified it |
| `lookalike` | 4 | **the operator does not list the signer.** Conclusive |
| `countersignature_invalid` | 5 | the operator changed the entry after the actor agreed to it — or it was signed by a key the actor's published history never names |
| `deviation` | 6 | **a listed actor did something off its list.** ⚠️ Evidence to report and ask about — not a violation anything blocked |

`findings` is always a list, `[]` when there is nothing to report. **Exit status:** `1` when there is
any finding, `0` otherwise — including when a step was `unknown`, so read `steps` too.

### `polis actor verify --custody <domain>`

**A site's own check of custody** — [`custody.md` §12.5](../../signet/spec/custody.md#125--how-the-tenant-checks--and-what-is-honestly-independent),
run from anywhere with no site and no key. Shape (values abridged):

```json
{
  "status": "success",
  "command": "actor",
  "data": {
    "site": "alice.polis.pub",
    "operator": "polis.polis.pub",
    "operator_source": "granted",
    "declarations": [
      {
        "url": "https://polis.polis.pub/content/pub.polis.core/attestation/….json",
        "issuer": "https://polis.polis.pub",
        "subject": "https://alice.polis.pub",
        "asserted": "2026-09-14T12:00:00Z",
        "payload": { "attribution": "as-tenant", "holds": "identity-key" },
        "signature": "valid",
        "withdrawal": "not_withdrawn"
      }
    ],
    "grants": [],
    "events": {
      "source": "https://ds.polis.pub",
      "examined": 23,
      "signed_with_your_key": 22,
      "signed_by_you": 19,
      "signed_by_agent": 3,
      "agent_acts": [
        { "id": "…", "type": "pub.polis.comment.blessing.granted", "created_at": "…", "agent": "rosie", "grant": "https://alice.polis.pub/content/pub.polis.core/attestation/….json", "grant_state": "active" }
      ],
      "not_recorded": 0,
      "authority": { "self": 22, "none": 1 },
      "signed_by_other": [
        { "id": "…", "type": "pub.polis.comment.blessing.granted", "created_at": "…", "signed_by": "bob.polis.pub", "authority": "none" }
      ]
    },
    "notes": [],
    "findings": [],
    "limit": "This makes custody VISIBLE; it does not reduce it. …"
  }
}
```

- **`operator_source`** — `given` (`--operator`) or `granted` (the subject of the site's newest grant
  that verifies and is not withdrawn). Absent, with a note, when there was neither.
- **`declarations[]` / `grants[]`** — newest first. `signature` is `valid`, `invalid`, `unsigned` or
  `unknown`; `key_note` is present when a **retired** key verified the record. `withdrawal` is
  `not_withdrawn`, `withdrawn` or `unknown` (the issuer's withdrawals could not all be read).
  `withdrawn_by` is the record's own pointer, reported as found — ⚠️ **discovery, not proof**; a
  pointer that does not verify is discarded and named in `notes`.
- **`events`** — absent only when no discovery service was given. ⚠️ **`signed_by` and `authority`
  are the service's recording and are not re-verified.** `authority` counts use `self`, `none`,
  `withdrawn`, `unknown`, `grant` (any grant URL) and `not_recorded` (stored before the service
  recorded authority). `signed_by_other` lists up to 50. **`signed_with_your_key` splits into
  `signed_by_you` and `signed_by_agent`**: an act a user agent (Rosie) signed with your key under your
  grant carries the agent marker, which the service copies from the signed act into the event
  payload, and `agent_acts` lists up to 50 of them with the grant each cites and the service's
  `grant_state` (`active`, `withdrawn`, `not-found`, `unknown`). A marker needs both `agent` and
  `grant`. ⚠️ `signed_by_you` still includes anything your operator signed as you — an operator
  actor never carries a marker. `error` is set when the service could not be
  read — **not a finding**. `truncated` is set when more events exist than were read.
- **`findings[].kind`** — only `declaration_signature_invalid` and `grant_signature_invalid`. A missing
  declaration, a withdrawn grant and an unreachable service are `notes`, never findings.
- **`limit`** — the sentence every surface must carry, verbatim.

**Exit status:** `1` when there is any finding, `0` otherwise.

## Example Usage

### Chained Commands

```bash
# Draft, sign and send a comment
id=$(polis --json comment draft https://bob.example/content/pub.polis.core/post/20260101/hello.md | jq -r '.id')
"$EDITOR" ".polis/bundles/pub.polis.core/comments/drafts/$id.md"
polis --json comment sign "$id" | jq -r '.version'
polis --json comment sync
```

### Automated Blessing Workflow

```bash
# Check blessing requests and auto-grant the first one
requests=$(polis --json blessing requests)
first_version=$(echo "$requests" | jq -r '.data.requests[0].comment_version')
polis --json blessing grant "$first_version"
```

### Error Handling in Scripts

```bash
# Branch on the exit status; the error is a sentence, not a code
if ! result=$(polis --json post test.md); then
    echo "Error: $(echo "$result" | jq -r '.error')" >&2
    exit 1
fi
echo "Published $(echo "$result" | jq -r '.path') at $(echo "$result" | jq -r '.version')"
```

### Integration with jq Filters

```bash
# Extract specific fields from complex responses
polis --json follow https://alice.com | \
  jq '{
    author: .data.author_email,
    blessed: .data.comments_blessed,
    success: (.data.comments_blessed > 0)
  }'
```

### Conditional Logic

```bash
# Only proceed if blessing requests exist
count=$(polis --json blessing requests | jq -r '.data.count')

if [ "$count" -gt 0 ]; then
    echo "Processing $count blessing requests..."
else
    echo "No pending requests"
fi
```

## Best Practices

1. **Always validate JSON output**
   ```bash
   result=$(polis --json about)
   echo "$result" | jq empty || exit 1  # Fail on invalid JSON
   ```

2. **Know which shape a command returns** — `.data.x` for the envelope, `.x` for the flat commands,
   `.totals` for `validate`. See *Response Shapes*.

3. **Use jq defaults for fields that may be absent**
   ```bash
   version=$(echo "$result" | jq -r '.version // "unknown"')
   ```

4. **Check exit codes before parsing**
   ```bash
   if result=$(polis --json post test.md); then
       : # parse the success shape
   else
       : # parse {"success": false, "error": "..."}
   fi
   ```
