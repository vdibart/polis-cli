#!/usr/bin/env bash
# The public upgrade guide's hand recipes for phases 1 to 5, so
# TestEraRunbookRecipes can compare their result with tailor's on the same old
# site.
#
# Usage: runbook-phase1-5.sh <site-dir>
# Needs on PATH: the CURRENT polis binary, and jq. POLIS_BASE_URL must be set
# (the guide's "before you start" step).
#
# ⛔ Every line between a "# guide>" and a "# <guide" marker is the guide's own
# text, copied verbatim (whitespace aside). TestRunbookScriptQuotesTheGuide
# fails when one of them no longer appears in docs/cli/user/upgrade-a-site.md,
# so a recipe changed in the guide must be changed here too, and the era run
# then tests the new one. Lines outside the markers are scaffolding: the
# "Detect" conditions a person would check first, and logging.
#
# Each step prints "== <check id>" and what it did, so a failing comparison can
# be traced to the recipe that produced it.

set -u
SITE=$1
cd "$SITE" || exit 1
: "${POLIS_BASE_URL:?set POLIS_BASE_URL first}"

# --- Before you start ---
REF=$(mktemp -d)
export REF
(cd "$REF" && polis init --site-title ref --author ref </dev/null) >/dev/null 2>&1 || { echo "reference init failed"; exit 1; }
# guide>
jqi() { local f=$1; shift; local t; t=$(mktemp) && jq "$@" "$f" > "$t" && cat "$t" > "$f"; rm -f "$t"; }
WK=.well-known/polis; BJ=content/pub.polis.core/bundle.json; REG=.polis/bundles/registry.json
# <guide
step() { echo "== $1"; }

# --- Phase 1 ---
step wellknown-version
# The guide's X.Y.Z is a placeholder for the version `polis version` prints.
v=$(jq -r '.version // "MISSING"' $WK)
case $v in
  polis-cli-go/*|polis-cli/*) echo "already generator format: $v" ;;
  *) X=$(polis version | awk '{print $2}'); X=${X#v}
     jqi $WK --arg v "polis-cli-go/$X" '.version = $v'; echo "set polis-cli-go/$X" ;;
esac

step author-field-migration
if [ "$(jq '(.author|type)=="string" and (.author_name|type)!="string"' $WK)" = true ]; then
# guide>
jqi $WK '.author_name = .author | del(.author)'
# <guide
  echo applied
fi

step wellknown-legacy-config
if [ "$(jq '[keys[] | select(.=="config" or .=="domain" or .=="base_url" or .=="subdomain")] | length' $WK)" != 0 ]; then
# guide>
jqi $WK 'del(.config, .domain, .base_url, .subdomain)'
# <guide
  echo applied
fi

step wellknown-bundles
case $(jq '.bundles | length' $WK) in
  0|null)
# guide>
jqi $WK '.bundles = {"pub.polis.core": {"path": "content/pub.polis.core/bundle.json"}}'
# <guide
  echo applied ;;
esac

step avatar-config
if [ "$(jq '.avatar' $WK)" = null ]; then
# guide>
polis site set avatar --bg '#8766aa' --fg '#ffffff' --pattern none
# <guide
fi

# --- Phase 2 ---
step bundle-json
if [ ! -f $BJ ]; then
# guide>
mkdir -p content/pub.polis.core && cp $REF/$BJ $BJ
# <guide
  echo "copied from reference"
else
# guide>
jqi $BJ --slurpfile r $REF/$BJ '.types = ($r[0].types + .types) | .shapes = ($r[0].shapes + .shapes) | .themes = ($r[0].themes + .themes)'
# <guide
  echo merged
fi

# --- Phase 3 ---
step layout-posts
# guide>
for d in posts/*/; do [ -d "$d" ] || continue; n=$(basename "$d"); dst=content/pub.polis.core/post/$n; mkdir -p "$dst"
  for f in "$d"*.md; do [ -e "$f" ] && [ ! -e "$dst/$(basename "$f")" ] && mv "$f" "$dst/"; done
  [ -d "$d.versions" ] && [ ! -e "$dst/.versions" ] && mv "$d.versions" "$dst/"
done
# <guide

step layout-comments
# guide>
[ -d comments ] && (cd comments && find . -name '*.md' -type f) | while read -r r; do
  dst=content/pub.polis.core/comment/${r#./}; [ -e "$dst" ] || { mkdir -p "$(dirname "$dst")"; mv "comments/$r" "$dst"; }
done
# <guide

step layout-following
if [ -e metadata/following.json ]; then
# guide>
mkdir -p content/pub.polis.core/follow && [ ! -e content/pub.polis.core/follow/following.json ] && mv metadata/following.json content/pub.polis.core/follow/
# <guide
fi

step layout-blessed
if [ -e metadata/blessed-comments.json ]; then
# guide>
mkdir -p content/pub.polis.core/comment && [ ! -e content/pub.polis.core/comment/blessed.json ] && mv metadata/blessed-comments.json content/pub.polis.core/comment/blessed.json
# <guide
fi

# --- Phase 4: path-separators applies only to sites written on Windows ---

# --- Phase 4.5 ---
step bundle-reference-payload
# guide>
polis render
# <guide
echo "polis render: exit $?"

step legacy-content-path
# guide>
[ -d .polis/content ] && for b in .polis/content/*/; do [ -d "$b" ] || continue; n=$(basename "$b")
  for s in "$b"*/; do [ -d "$s" ] || continue; t=.polis/bundles/$n/$(basename "$s")
    if [ -e "$t" ]; then echo "CONFLICT: $t exists — reconcile by hand"; else mkdir -p -m 700 ".polis/bundles/$n"; mv "$s" "$t"; fi
  done; done
[ -d .polis/content ] && rmdir .polis/content/* .polis/content
# <guide

step layout-private-drafts
# guide>
for pair in comments/drafts comments/pending comments/denied posts/drafts; do
  src=.polis/$pair dst=.polis/bundles/pub.polis.core/$pair; [ -d "$src" ] || continue
  (cd "$src" && find . -type f) | while read -r r; do r=${r#./}
    if [ -e "$dst/$r" ]; then echo "CONFLICT: $dst/$r exists"; continue; fi
    mkdir -p -m 700 "$(dirname "$dst/$r")" && mv "$src/$r" "$dst/$r"; done
done
# <guide

step registry-migration
if [ "$(jq '.active_theme' $WK)" != null ]; then
# guide>
t=$(jq -r .active_theme $WK); case $t in pub.polis.themes.*) ;; *) t=pub.polis.themes.$t;; esac
jqi $REG --arg t "$t" '.active_theme = $t' && jqi $WK 'del(.active_theme)'
# <guide
  echo applied
fi

step legacy-theme-locations
# The guide gives this one in prose: remove each shipped theme's old copy
# (plus _base), keep the user's own in site/themes/, move the user's own from
# .polis/themes/ to site/themes/ (a clash is reconciled by hand), and remove
# .polis/themes only once it holds nothing of theirs. The shipped names are the
# reference site's installed themes.
shipped() { [ "$1" = _base ] || [ -d "$REF/.polis/bundles/pub.polis.core/themes/$1" ]; }
for d in site/themes/*/; do [ -d "$d" ] || continue; n=$(basename "$d")
  if shipped "$n"; then rm -rf "site/themes/$n"; echo "removed site/themes/$n"; else echo "kept site/themes/$n"; fi
done
for d in .polis/themes/*/; do [ -d "$d" ] || continue; n=$(basename "$d")
  if shipped "$n"; then rm -rf ".polis/themes/$n"; echo "removed .polis/themes/$n"
  elif [ -e "site/themes/$n" ]; then echo "CONFLICT: site/themes/$n exists — reconcile by hand"
  else mkdir -p site/themes && mv ".polis/themes/$n" "site/themes/$n"; echo "moved .polis/themes/$n to site/themes/"; fi
done
[ -d .polis/themes ] && rmdir .polis/themes 2>/dev/null
[ -d site/themes ] && rmdir site/themes 2>/dev/null

step bundle-active-fields
# guide>
jqi $WK 'del(.bundles[]?.active)'
jqi $REG 'del(.installed_bundles[]?.active)'
# <guide

# --- Phase 4.7 ---
step notification-rules-migration
if [ "$(jq '[.types[] | select(.notifications)] | length' $BJ)" != 0 ]; then
# guide>
jqi $REG --slurpfile b $BJ '.notifications = ((.notifications // []) as $have
  | $have + [ $b[0].types[] | .notifications // [] | .[] | select(.id as $i | ($have|map(.id)|index($i)) == null) ])'
jqi $BJ '.types |= map_values(del(.notifications))'
# <guide
  echo applied
fi

step active-shape-upgrade
# Report only: a self-hosted site keeps the shape its owner set.

step v3-legacy-archives
if [ "$(jq -r .active_shape $REG)" = pub.polis.shapes.v4 ]; then
# guide>
rm -f posts/index.html comments/index.html tag/index.html comment/index.html
# <guide
fi

# --- Phase 5 ---
step index-rebuild
# guide>
polis rebuild --all && rm -f metadata/public.jsonl
# <guide
echo "exit $?"

step render-site
# guide>
polis render --force
# <guide
echo "exit $?"

rm -rf "$REF"
