# Upgrade a self-hosted site, one change at a time

*For* [Writers](../../README.md#writing-on-polis) · [Operators](../../README.md#running-polis) — *Kind* [Guide](../../README.md#kinds-of-page) — *Component* [CLI](../README.md) — *See also* [concept](../../general/concepts/actors.md#tailor) · [reference](command-reference.md#upgrading)

When polis changes how a site is laid out on disk, an existing site has to be brought up to date. On polis.pub that happens
for you. On a site you run yourself, **Tailor** does it: `tailor --apply` runs every upgrade step in one pass. This page lists
every one of those steps, what it changes and why, and how to do it yourself with ordinary shell tools, `polis` commands and
the occasional hand edit. With it you, or an AI assistant you work with, can take the changes you want, leave the ones you
don't, and spread them over as many sittings as you like.

You do not have to choose between Tailor and doing it by hand. `tailor --only` applies the steps you name and nothing else,
and Tailor's dry run checks whatever you did by hand.

## 1 · Before you start

### What Tailor promises

- **Nothing changes without `--apply`.** `tailor` on its own is a dry run: it reports what it would do and touches nothing.
- **Everything it changes is backed up first.** Before each change it copies every file the change rewrites or removes into
  `.polis/tailor-backup/<timestamp>/`, and that directory's `manifest.json` lists every change the run made. Rendered HTML is the
  one exception, because it is regenerated from your signed sources.
- **It never loses a customisation.** A theme under a name polis never shipped is yours. Tailor leaves it in place, and it stays
  active if it was. A shipped file you edited is reinstalled, and your edited copy is kept in the backup. A policy file holding
  your own rules is never replaced.
- **It never edits a signed file.** Signed files are moved byte for byte, so every signature still verifies.

### The dry run is the checker

Run it before and after each step. The step you just did, by hand or with `--only`, should now report `pass`:

```bash
tailor --json "$SITE" | jq -r '.checks[] | select(.status!="pass") | "\(.status)\t\(.name)\t\(.message)"'
```

After `--apply`, each step that found something says how it came out, decided by checking again rather than taking the step's
word for it: **fixed**, **NOT APPLIED** (the change did not take; nothing claims otherwise), or **reported** (there was
nothing to apply, only something for you to decide). `--json` carries the same as each check's `outcome`.

`tailor --list` names every step, in the order Tailor runs them, with its phase, its kind (below), a one-line summary and the
steps it needs first.

### Set up

```bash
export SITE=/path/to/your/site
export POLIS_BASE_URL=https://your.domain       # set this before the step that removes base_url
cd "${SITE:?set SITE first}" && test -f .well-known/polis || echo "STOP: $PWD is not a polis site"

# A backup of your own: Tailor backs up what IT changes, not what you change by hand.
# ⛔ It holds your private key. Make it unreadable to others, and keep it out of any directory a web server serves.
mkdir -p -m 700 ~/polis-backups
(umask 077; tar czf ~/polis-backups/"$(basename "$SITE")-$(date +%Y%m%d-%H%M%S).tgz" .)

# A reference site: a fresh `polis init` holds every current default file, to copy or compare against.
# ⛔ Never copy its keys, its .well-known/polis, or anything under .polis/keys or dm/: they are ITS identity.
export REF=$(mktemp -d)
(cd "$REF" && polis init --site-title ref --author ref </dev/null)

# jq in place, keeping the file's permissions
jqi() { local f=$1; shift; local t; t=$(mktemp) && jq "$@" "$f" > "$t" && cat "$t" > "$f"; rm -f "$t"; }

WK=.well-known/polis; BJ=content/pub.polis.core/bundle.json; REG=.polis/bundles/registry.json
```

⚠️ **Every recipe below assumes this shell:** run from the site's root, with these variables and `jqi` defined. Several
recipes move or remove files by relative path, so run in the wrong directory they act on the wrong tree. If you, or an
assistant whose shell does not keep state between commands, start a new shell, repeat this block first, and begin each
recipe with `cd "${SITE:?}" && test -f .well-known/polis &&`.

⛔ **Never edit a signed file**, with `jqi` or anything else: `follow/following.json`, `comment/blessed.json`,
`license/license.json`, tags, attestations, or a post or comment. Moving a signed file is safe, because its bytes do not change.
Editing it breaks its signature. `.well-known/polis`, `bundle.json`, `registry.json`, policy files and the web app's config are
not signed, and are safe to edit.

⛔ **In `.well-known/polis`, never touch `public_key`, `public_key_history` or `public_key_messages`.** A key changes only
through [`polis rotate-key`](command-reference.md#polis-rotate-key).

## 2 · How to phase it

Every step has one of five kinds. The kind tells you how freely you may choose.

| Kind | Meaning | Take it on its own? |
|---|---|---|
| **chain** | moves something later steps assume has moved | take the whole chain, then rebuild and render |
| **provision** | creates something missing; changes nothing you had | yes |
| **overwrite** | replaces something you may have changed yourself | yes — this is where your choices are. Compare first |
| **cleanup** | removes something obsolete | yes; skipping it leaves clutter, rarely breakage |
| **report** | never changes anything | nothing to apply: read it and decide |

**Ordering rules.** `tailor --apply --only …` refuses, changing nothing, if a step you chose needs one you left out that has not
happened yet, and it names the missing step. By hand, keep to the same rules:

- Every layout move (phases 3, 4.5 and 4.7) comes before `index-rebuild` and `render-site`.
- `bundle-reference-payload` comes before `registry-migration` and `legacy-theme-locations`.
- `legacy-content-path` comes before `layout-private-drafts`, `dm-directories` and `dm-keyring` (it refuses to merge into a
  directory those create).
- `active-shape-upgrade` comes before `v3-legacy-archives`.
- `unpublished-emits-migration` and `theme-shapes-migration` come before `bundle-declarations`.
- `author-field-migration` comes before `bundle-path-integrity`.
- Finish every sitting with `polis rebuild --all && polis render --force`, or with `tailor --apply --only index-rebuild,render-site`.

**A phasing that works.** Three sittings:

1. **Safe:** every provision and cleanup step, plus phase 1. Nothing about your site's look changes.
2. **Layout:** the chain steps, then rebuild and render. The site looks the same, but its sources have moved.
3. **Visible:** the overwrite steps you choose, then render. Switching from the blog layout to the stream is your choice too:
   Tailor reports it (`active-shape-upgrade`) and never makes it.

**With Tailor instead of by hand:**

```bash
tailor --list                                        # every step, with what it needs first
tailor --only avatar-config,tag-directory            # dry run of two steps
tailor --apply --only avatar-config,tag-directory    # apply just those
tailor --apply --skip foreign-content-in-public-path   # everything except this
```

### Working with an AI assistant

Give it this page and the output of `tailor --json` for your site. Ask it which steps your site needs, which of them are
overwrite steps, and what each would change. Then decide, and have it run either the recipe below or `tailor --apply --only`
for the steps you accept, one sitting at a time, with the dry run after each. Keep your own backup from §1 until you are
satisfied.

## 3 · The steps, in the order Tailor runs them

Each step gives its kind, the release that introduced it where there is one, what it does and why, and how to **detect**,
**apply** and **verify** it. Verify is always the same: the step reports `pass` in `tailor --only <step>`.

### Phase 1 — Your site's identity document

#### `wellknown-version`

*provision · v0.50.0.* The `version` field names the tool that wrote it (`polis-cli-go/X.Y.Z`) instead of a bare version
number. Cosmetic; safe to skip.

- Detect: `jq -r '.version // "MISSING"' $WK` prints a bare `0.49.0`-style value, or MISSING.
- Apply: `jqi $WK '.version = "polis-cli-go/X.Y.Z"'`, with X.Y.Z from `polis version`.

#### `author-field-migration`

*chain.* The `author` field is renamed `author_name`.

- Detect: `jq '(.author|type)=="string" and (.author_name|type)!="string"' $WK` prints `true`.
- Apply: `jqi $WK '.author_name = .author | del(.author)'`.

#### `wellknown-legacy-config`

*cleanup · v0.50.0.* Per-site directory settings left `.well-known/polis`; the bundle defines paths now. ⚠️ Set
`POLIS_BASE_URL` first (§1): Tailor reads `base_url` and `domain` from here before removing them, and later steps need them.

- Detect: `jq '[keys[] | select(.=="config" or .=="domain" or .=="base_url" or .=="subdomain")]' $WK`
- Apply: `jqi $WK 'del(.config, .domain, .base_url, .subdomain)'`.

#### `wellknown-bundles`

*chain · v0.58.0.* `.well-known/polis` points at the bundle declaration, so tools can find your content.

- Detect: `jq '.bundles | length' $WK` prints `0` or `null`.
- Apply: `jqi $WK '.bundles = {"pub.polis.core": {"path": "content/pub.polis.core/bundle.json"}}'`.

#### `avatar-config`

*provision.* Adds a default avatar, and the `favicon.svg` drawn from it. Cosmetic.

- Detect: `jq '.avatar' $WK` prints `null`.
- Apply: `polis site set avatar --bg '#8766aa' --fg '#ffffff' --pattern none` with your own colours (Tailor picks random ones).

### Phase 2 — The bundle declaration

#### `bundle-json`

*chain · v0.58.0.* `content/pub.polis.core/bundle.json` declares your content types, shapes and themes. When it exists, Tailor
only adds what is missing and never changes what you declared.

- Detect: `test -f $BJ || echo MISSING`.
- Apply, if missing: `mkdir -p content/pub.polis.core && cp $REF/$BJ $BJ`.
- Apply, if present (yours win):
  `jqi $BJ --slurpfile r $REF/$BJ '.types = ($r[0].types + .types) | .shapes = ($r[0].shapes + .shapes) | .themes = ($r[0].themes + .themes)'`

### Phase 3 — Where your content lives

Do all of this phase, then rebuild and render. Moving a signed file does not change its bytes, so every signature still verifies.

#### `layout-posts`

*chain · v0.58.0.* Post sources move from `posts/YYYYMMDD/` to `content/pub.polis.core/post/YYYYMMDD/`, with their `.versions/`.
`posts/` stays: it is where the rendered HTML goes.

- Detect: `find posts -name '*.md' -not -path '*/.versions/*' | head`.
- Apply:

  ```bash
  for d in posts/*/; do [ -d "$d" ] || continue; n=$(basename "$d"); dst=content/pub.polis.core/post/$n; mkdir -p "$dst"
    for f in "$d"*.md; do [ -e "$f" ] && [ ! -e "$dst/$(basename "$f")" ] && mv "$f" "$dst/"; done
    [ -d "$d.versions" ] && [ ! -e "$dst/.versions" ] && mv "$d.versions" "$dst/"
  done
  ```

#### `layout-comments`

*chain · v0.58.0.* Every `.md` under `comments/` moves to `content/pub.polis.core/comment/`, keeping its sub-path.

- Apply:

  ```bash
  [ -d comments ] && (cd comments && find . -name '*.md' -type f) | while read -r r; do
    dst=content/pub.polis.core/comment/${r#./}; [ -e "$dst" ] || { mkdir -p "$(dirname "$dst")"; mv "comments/$r" "$dst"; }
  done
  ```

#### `layout-following`

*chain · v0.58.0.* `metadata/following.json` moves to `content/pub.polis.core/follow/following.json`, unchanged.

- Apply: `mkdir -p content/pub.polis.core/follow && [ ! -e content/pub.polis.core/follow/following.json ] && mv metadata/following.json content/pub.polis.core/follow/`

#### `layout-blessed`

*chain · v0.58.0.* `metadata/blessed-comments.json` moves to `content/pub.polis.core/comment/blessed.json`, unchanged.

- Apply: `mkdir -p content/pub.polis.core/comment && [ ! -e content/pub.polis.core/comment/blessed.json ] && mv metadata/blessed-comments.json content/pub.polis.core/comment/blessed.json`

### Phase 4 — Sites written on Windows

#### `path-separators`

*overwrite.* Windows backslash paths in `index.jsonl` (and older metadata files) break lookups everywhere else. Only a site
ever written on Windows needs this. Tailor rewrites only the `"path"` values of the index, nothing else in it, and reports a
backslash path in the signed follow file or blessing list without touching either.

- Detect: `grep -n '\\\\' content/pub.polis.core/index.jsonl metadata/*.json* 2>/dev/null`
- Apply: `polis rebuild --all` regenerates `index.jsonl` cleanly. ⛔ Do not edit `following.json` or `blessed.json` by hand,
  even to fix a path: both are signed.

### Phase 4.5 — Bundles, the registry and themes

Do all of this phase before rebuilding and rendering.

#### `bundle-reference-payload`

*overwrite.* The shapes and theme stylesheets that ship inside polis are installed to `.polis/bundles/pub.polis.core/`, and
refreshed when polis changes them. ⚠️ Any shipped file you edited there is replaced; Tailor keeps your copy in its backup. To
see your edits first: `diff -r $REF/.polis/bundles/pub.polis.core .polis/bundles/pub.polis.core`. Keep a change you want in a
theme of your own ([themes guide](../../general/guides/themes.md)).

- Detect: `test -f $REG || echo MISSING`.
- Apply: `polis render` installs the payload before it renders, keeping a copy of any shipped file you edited in
  `.polis/render-backup/`. If it has to create the registry, a site that already has posts keeps the blog layout
  (`pub.polis.shapes.v3`), which is how every site rendered before the registry existed, and it picks a random theme from
  the ones polis ships, never one of yours: choose your own afterwards, as in `registry-integrity`.

#### `legacy-content-path`

*chain.* Private state moves from `.polis/content/<bundle>/` (releases 0.58 to 0.62) to `.polis/bundles/<bundle>/`. Nothing is
merged into a directory that already exists.

- Apply:

  ```bash
  [ -d .polis/content ] && for b in .polis/content/*/; do [ -d "$b" ] || continue; n=$(basename "$b")
    for s in "$b"*/; do [ -d "$s" ] || continue; t=.polis/bundles/$n/$(basename "$s")
      if [ -e "$t" ]; then echo "CONFLICT: $t exists — reconcile by hand"; else mkdir -p -m 700 ".polis/bundles/$n"; mv "$s" "$t"; fi
    done; done
  [ -d .polis/content ] && rmdir .polis/content/* .polis/content
  ```

  The `[ -d … ]` guards matter: on a site with no `.polis/content`, an unguarded glob stays literal and creates a directory
  named `*`.

#### `layout-private-drafts`

*chain · v0.58.0.* Needs `legacy-content-path`. Drafts, and comments waiting on a blessing (some already signed), move from `.polis/comments/{drafts,pending,denied}/`
and `.polis/posts/drafts/` into `.polis/bundles/pub.polis.core/`. Nothing reads the old paths, so until this moves they are
missing from polis and the web app. A file that would land on an existing one stays where it is, for you to reconcile.

- Detect: `find .polis/comments .polis/posts -type f 2>/dev/null | head`.
- Apply:

  ```bash
  for pair in comments/drafts comments/pending comments/denied posts/drafts; do
    src=.polis/$pair dst=.polis/bundles/pub.polis.core/$pair; [ -d "$src" ] || continue
    (cd "$src" && find . -type f) | while read -r r; do r=${r#./}
      if [ -e "$dst/$r" ]; then echo "CONFLICT: $dst/$r exists"; continue; fi
      mkdir -p -m 700 "$(dirname "$dst/$r")" && mv "$src/$r" "$dst/$r"; done
  done
  ```

#### `registry-migration`

*chain.* `active_theme` moves out of the public `.well-known/polis` into the private registry, fully qualified (`vice` becomes
`pub.polis.themes.vice`). Needs `bundle-reference-payload`.

- Detect: `jq '.active_theme' $WK` is not `null`.
- Apply:

  ```bash
  t=$(jq -r .active_theme $WK); case $t in pub.polis.themes.*) ;; *) t=pub.polis.themes.$t;; esac
  jqi $REG --arg t "$t" '.active_theme = $t' && jqi $WK 'del(.active_theme)'
  ```

#### `legacy-theme-locations`

*overwrite.* Shipped themes now live in `.polis/bundles/pub.polis.core/themes/`. Their old copies in `site/themes/` and
`.polis/themes/` are removed. A theme of your own is kept: one in `site/themes/` stays there, because render still reads it,
and it is the only place a theme's snippets are read from. One in `.polis/themes/`, which nothing reads, moves to
`site/themes/`.

- Detect: `ls site/themes .polis/themes 2>/dev/null`.
- Apply: for each directory named after a theme polis ships (`tailor --list` does not list themes; the
  [themes concept](../../general/concepts/themes.md#what-ships-today) does), plus `_base`:
  `rm -rf site/themes/<name>`. Then move each of your own from `.polis/themes/<name>` to `site/themes/<name>` (if a theme of
  that name is already in `site/themes/`, reconcile the two by hand), and remove `.polis/themes` only once it holds nothing of
  yours.

#### `bundle-active-fields`

*cleanup.* The `active: true` flag on bundles was always true and never read.

- Apply: `jqi $WK 'del(.bundles[]?.active)'` and `jqi $REG 'del(.installed_bundles[]?.active)'`.

### Phase 4.7 — Later migrations

#### `notification-rules-migration`

*chain.* Notification rules move from the public `bundle.json` (one list per type) to the private registry (one flat list).
Rules are matched by `id`; where both have one, the registry's wins.

- Detect: `jq '[.types[] | select(.notifications)] | length' $BJ` prints more than 0.
- Apply:

  ```bash
  jqi $REG --slurpfile b $BJ '.notifications = ((.notifications // []) as $have
    | $have + [ $b[0].types[] | .notifications // [] | .[] | select(.id as $i | ($have|map(.id)|index($i)) == null) ])'
  jqi $BJ '.types |= map_values(del(.notifications))'
  ```

#### `active-shape-upgrade`

*report.* Your site keeps the layout you chose. Tailor reports a site on `pub.polis.shapes.v3` (the blog layout, a page per
post) or `pub.polis.shapes.v4` (the stream), which still render but are no longer supported, and tells you how to switch to
`pub.polis.shapes.v5`, the default every new site starts on. It never switches for you. (On polis.pub every site is on v5;
the service moved them there.)

- Detect: `jq -r .active_shape $REG` prints `pub.polis.shapes.v3` or `pub.polis.shapes.v4`.
- Apply, only if you want v5: `jqi $REG '.active_shape = "pub.polis.shapes.v5"'`, then `polis render --force`. To go back,
  set the shape you had and render again.

#### `v3-legacy-archives`

*cleanup.* Only after `active-shape-upgrade`. The blog shape's archive pages are neither written nor removed by the stream
or the v5 shape, so they would go on being served stale.

- Apply, only if on v4 or v5: `rm -f posts/index.html comments/index.html tag/index.html comment/index.html`.

### Phase 5 — Rebuild and render

#### `index-rebuild`

*provision.* Regenerates `content/pub.polis.core/index.jsonl` from your signed content, and removes the old
`metadata/public.jsonl`.

- Apply: `polis rebuild --all && rm -f metadata/public.jsonl`.

#### `render-site`

*provision.* Re-renders your HTML. The dry run reports it only when a render is owed: a page older than its source, no
`index.html`, or a theme or shape change since the last render.

- Apply: `polis render --force`.

### Phase 6 — Files a current site has

None of these changes anything you already had.

#### `policies-public`

*provision · v0.57.0.* The public policy file: who may comment, and which comments are blessed automatically
([policies](policies.md)).

- Apply: `test -f policies/rules.jsonl || { mkdir -p policies; cp $REF/policies/rules.jsonl policies/; }`

#### `policies-private`

*provision · v0.57.0.* The private policy file, for which content types you accept and DM filtering.

- Apply: `test -f .polis/policies/rules.jsonl || { mkdir -p -m 700 .polis/policies; install -m 600 $REF/.polis/policies/rules.jsonl .polis/policies/; }`

#### `policy-content-converge`

*overwrite.* A policy file that still holds a default an earlier release wrote is replaced with today's default; this is how an
older site gets back the rule allowing DMs from the people it follows. A file holding your own rules is never touched, and one
that does not parse is reported for you to fix.

- Detect: only Tailor knows every past default, so ask it:
  `tailor --only policy-content-converge --json | jq '.checks[0].actions'`.
- Apply, for a file it reports as a past default: copy the `$REF` file over it, as in the two steps above.

#### `tag-directory`

*provision · v0.62.0.* `mkdir -p content/pub.polis.core/tag`

#### `storage-salt`

*provision.* A site-wide random value for direct messages: 32 bytes, hex, with no trailing newline. Create it only if it is
absent, and never replace one.

- Apply: `test -f .polis/storage-salt || { od -An -tx1 -N32 /dev/urandom | tr -d ' \n' > .polis/storage-salt; chmod 600 .polis/storage-salt; }`

#### `dm-directories`

*provision.* `mkdir -p .polis/bundles/pub.polis.core/dm/conversations && chmod 700 .polis/bundles/pub.polis.core/dm/conversations`

#### `dm-keyring`

*provision.* The keys encrypted direct messages need, and the public half published in `.well-known/polis`, signed with your
key.

- Apply: `polis dm publish-key`. It creates the keyring (and the storage salt) when your site has none, and never
  replaces one that exists. It refuses when your identity document already publishes a messages key, because
  then a keyring exists somewhere (another machine, a backup not restored, or `.polis/content/` before `legacy-content-path`):
  find it rather than make a new one, or people will write to you with a key you no longer hold. Only if that keyring is
  truly gone, `polis dm publish-key --create` makes a new one and replaces the published key.

#### `theme-consolidation`

*cleanup.* Removes files in `site/themes/<name>/` that are identical to `site/themes/_base/`. After `legacy-theme-locations`
there is no `_base` left, so this finds nothing.

- Apply, if you kept `site/themes/_base`: `cmp` each file against its `_base` twin and delete only exact duplicates.

### Phase 7 — The web app's settings

#### `webapp-config`

*chain · v0.55.0.* `.polis/webapp-config.json` moves to `.polis/webapp/config.json`.

- Apply: `[ -e .polis/webapp/config.json ] || { mkdir -p -m 700 .polis/webapp; mv .polis/webapp-config.json .polis/webapp/config.json; }`

#### `webapp-view-mode`

*cleanup.* The `view_mode` setting is retired; the web app has one layout. Needs `webapp-config`.

- Apply: `jqi .polis/webapp/config.json 'del(.view_mode)'`

### Phase 6.5 — Consistency checks

#### `reference-payload-integrity`

*overwrite.* Like `bundle-reference-payload`, but file by file: a shipped shape or theme file whose bytes differ from the copy
inside polis is reinstalled. This is where an edit to a shipped stylesheet is replaced; Tailor keeps your copy in its backup
and names it.

- Detect: `diff -rq $REF/.polis/bundles/pub.polis.core .polis/bundles/pub.polis.core`
- Apply: `polis render`. Skip it to keep your edits in place; Tailor will go on reporting them.

#### `registry-integrity`

*overwrite.* Repairs a malformed registry. A theme or shape given by its short name is fully qualified. A theme that does not
exist, or an empty one, is replaced by a random shipped theme, and a shape that does not exist by the stream shape. A theme of
your own that is installed is kept, and stays active. Malformed JSON, or a registry newer than this polis, is only reported.

- Apply: choose the theme yourself instead of leaving it to chance. `jq -r '.themes | keys[]' $BJ` lists the shipped ones; then
  `jqi $REG '.active_theme = "pub.polis.themes.<name>"'`.

#### `key-consistency`

*report.* `.polis/keys/id_ed25519.pub` must match `public_key` in `.well-known/polis`. Tailor only reports a mismatch, on
purpose: which one is right is a question of trust, not a repair.

- Detect: compare `cat .polis/keys/id_ed25519.pub` with `jq -r .public_key $WK`.
- ⛔ Never resolve it by editing `public_key`.

#### `bundle-path-integrity`

*chain.* The core bundle's path in `.well-known/polis` must point at a readable file. Tailor repairs that path. It reports, and
does not change, an empty `author_name` and a broken path to any other bundle. Needs `author-field-migration`.

- Apply: `jqi $WK '.bundles["pub.polis.core"].path = "content/pub.polis.core/bundle.json"'`, and
  `polis site set author-name "Your Name"` if yours is empty.

#### `unpublished-emits-migration`

*provision.* Declares two events the discovery service already sends, `pub.polis.post.unpublished` and
`pub.polis.comment.unpublished`, on the two types they belong to. It only adds.

- Apply:

  ```bash
  jqi $BJ '(.types["pub.polis.post"].emits |= ((. // []) + ["pub.polis.post.unpublished"] | unique))
         | (.types["pub.polis.comment"].emits |= ((. // []) + ["pub.polis.comment.unpublished"] | unique))'
  ```

#### `theme-shapes-migration`

*provision.* Older sites declare their themes as working with the blog shape only. This adds every shape the defaults list for
each theme, and removes none.

- Apply:

  ```bash
  jqi $BJ --slurpfile r $REF/$BJ '.themes |= with_entries(.key as $k
    | .value.compatible_shapes = (((.value.compatible_shapes // []) + ($r[0].themes[$k].compatible_shapes // [])) | unique))'
  ```

#### `bundle-declarations`

*provision.* Adds any type, shape or theme declaration you are missing, as `bundle-json` does. It **reports, and never
changes,** a type you declared differently from the default (`dir`, `mount`, `storage.pattern`, or a missing event). A changed
`dir` may mean your content has moved, and rewriting it would point polis at an empty directory. Needs
`unpublished-emits-migration` and `theme-shapes-migration`.

- Apply: the merge in `bundle-json`.
- Read the differences:
  `diff <(jq -S '.types | map_values({dir,mount,storage})' $REF/$BJ) <(jq -S '.types | map_values({dir,mount,storage})' $BJ)`

#### `index-entries`

*report.* Every line of `index.jsonl` must parse and carry `type`, `path`, `published` (an RFC 3339 time) and
`current_version` (`sha256:…`). The fix is `polis rebuild --all`.

#### `blessed-following-structure`

*report.* `blessed.json` is `{comments: [{post, blessed: [...]}]}` and `following.json` is `{following: [...]}`. Both are signed,
so fix them by re-running the command that writes them (`polis follow`, `polis rebuild --comments`), never by hand.

### Phase 6.7 — Loose ends

#### `active-theme-unset`

*provision.* A site with no active theme gets a random one of the themes polis ships for its layout, never one of yours.
Choose your own instead, as in `registry-integrity`.

#### `registry-schema-version`

*report.* Reports a registry written in an older format than this polis supports. No migration exists yet; nothing to do.

#### `registry-fqn-sanity`

*report.* Names the exact problem with a malformed theme or shape name. `registry-integrity` does the repair.

### Phase 8 — Cleanup

Each of these is independent, and each is safe to skip.

#### `manifest-obsolete`

*cleanup.* `rm -f metadata/manifest.json`

#### `empty-metadata-dir`

*cleanup.* `rmdir metadata 2>/dev/null` (it fails harmlessly if the directory is not empty, which is what Tailor does too).

#### `foreign-content-in-public-path`

*overwrite.* Removes posts and comments by **someone else** from your public source tree, with their rendered pages: copies an
older blessing flow left there. Someone else's work does not belong in your public tree; the blessed-comment cache is its only
home.

- Detect:

  ```bash
  D=$(echo "$POLIS_BASE_URL" | sed -E 's#^[a-z]+://##; s#[/:].*##')
  find content/pub.polis.core/comment content/pub.polis.core/post -name '*.md' -not -path '*/.versions/*' | while read -r f; do
    a=$(awk 'NR==1&&$0!="---"{exit} NR>1&&$0=="---"{exit} /^author:/{sub(/^author:[ ]*/,"");print;exit}' "$f")
    [ -n "$a" ] && [ "$a" != "$D" ] && echo "$a	$f"; done
  ```

- Apply: review the list (check what your own `author` values look like first), then move each file, and its `.html` under
  `comments/` or `posts/`, out of the site.

#### `blessed-cache-gc`

*cleanup.* A cached blessed comment with no readable `.meta.json` beside it has lost its record of where it came from. It is
unsafe to show, and the next sync fetches it again.

- Detect: `find .polis/ds/*/pub.polis.core/cache/blessed -name '*.md' 2>/dev/null | while read -r f; do jq -e . "$f.meta.json" >/dev/null 2>&1 || echo "$f"; done`
- Apply: remove each file listed, and its `.meta.json` if there is one.

#### `stale-scoped-feed`

*cleanup.* The followers and me feeds are filters now, not files.
`rm -f .polis/ds/*/pub.polis.core/state/pub.polis.feed.followers.jsonl .polis/ds/*/pub.polis.core/state/pub.polis.feed.me.jsonl`

#### `stale-feed-viewed-at`

*cleanup.* The time-based feed cursor was replaced by a position-based one.
`for c in .polis/ds/*/pub.polis.core/state/cursors.json; do [ -f "$c" ] && jqi "$c" 'del(.cursors["pub.polis.feed.viewed_at"])'; done`

#### `legacy-feed-scaffolding`

*cleanup.* `pub.polis.feed` is no longer a public content type.
`rmdir content/pub.polis.core/feed 2>/dev/null; jqi $BJ 'del(.types["pub.polis.feed"])'`
(Tailor leaves a non-empty `feed/` in place and reports it.)

#### `orphaned-theme-dirs`

*cleanup.* Removes leftovers of themes polis once shipped from `.polis/bundles/pub.polis.core/themes/` when the registry no
longer lists them. A theme of your own there is kept.

- Detect: `comm -23 <(ls .polis/bundles/pub.polis.core/themes | sort) <(jq -r '.installed_bundles[] | select(.name=="pub.polis.core") | .theme_versions | keys[]' $REG | sort)`
- Apply: remove only the directories in that list that are named after a theme polis shipped.

#### `studio13-rename-migration`

*overwrite.* The old `studio13` theme was renamed `studio13-nk`. Only on the blog shape.
`[ "$(jq -r .active_theme $REG)" = pub.polis.themes.studio13 ] && jqi $REG '.active_theme = "pub.polis.themes.studio13-nk"'`

### Phase 9 — The polis binary

#### `cli-update`

*report.* Asks GitHub for the latest polis release and, when it is newer than the polis you run, says so. It never downloads
anything. To upgrade, download the release from [the releases page](https://github.com/vdibart/polis-cli/releases), verify
that release's `checksums.txt` with its `checksums.txt.sig` and the polis release key, check your download against
`checksums.txt` (both steps are in [SECURITY.md](../../general/security/SECURITY.md#security-best-practices-for-users)),
and replace your binary. For `polis` itself, the installer in the README does both checks for you and
installs nothing if either fails; `tailor` and the other binaries you download and verify by hand. Then run `tailor` again: a newer polis may carry upgrade steps this one does not know. This is Tailor's only network call; `--skip cli-update` leaves it out.

## See also

- [Tailor](../../general/concepts/actors.md#tailor): what it is for, and what it does not do.
- [Choose a theme, or write your own](../../general/guides/themes.md): where a theme of your own lives.
- [Policies](policies.md): the rules the two policy steps install.
- [Command reference](command-reference.md): every `polis` command used above.
