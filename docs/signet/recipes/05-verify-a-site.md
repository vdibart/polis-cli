# Verify a site you do not own, end to end

*Recipe 5 · [the recipe book](README.md)*

*For* [Developers](../../README.md#building-on-polis) · [Reviewers](../../README.md#reviewing-the-security-and-identity-design) — *Kind* [Recipe](../../README.md#kinds-of-page) — *Component* [CLI](../../cli/README.md) — *See also* [concept](../concepts/verification.md) · [guide](../guides/verify-content.md)

Every command on this page is run by the test suite.

**The problem.** You are about to rely on a site — link to it, quote it, build on it — and want to
know whether what it serves is signed and intact, without an account, a key, or any relationship with
it.

**What it runs.** `polis validate <url>`: one command that checks what a site serves over public
HTTPS, storing nothing, and reports **what it checked and what it could not.** The full guide is
[verify content](../guides/verify-content.md).

## Set up a site to check

```bash
mkdir alice.example && cd alice.example
export POLIS_BASE_URL=https://alice.example
polis init --site-title "Alice"
printf '# Hello\n\nFirst post.\n' > hello.md
polis post hello.md
SITE_DIR=$PWD
cd "$(mktemp -d)"
unset POLIS_BASE_URL
```

## 1 · Run it

```bash
polis --json validate https://alice.example | tee report.json | jq '{target, form, scope, totals}'
```

```json output
{
  "target": "https://alice.example",
  "form": "remote",
  "scope": "site",
  "totals": { "passed": "…", "failed": 0, "warning": 0, "not_applicable": "…" }
}
```

## 2 · Read what was checked

```bash
jq -c '.checks[] | select(.id == "content.posts") | {outcome, detail}' report.json
```

```json output
{"outcome": "passed", "detail": "1 post examined: 1 signature(s) verified, 0 unsigned"}
```

## 3 · Read what was NOT checked

⛔ **`not_applicable` is never folded into `passed`.** Each one says why it did not run. A script that
treats `failed == 0` as "the site is fine" is wrong whenever this list is not empty.

```bash
jq -r '.checks[] | select(.outcome == "not_applicable") | "\(.id): \(.reason)"' report.json
```

```text output
identity.key_files: a site's private state is never served over HTTP…
bundle.registry: …
content.attestations: the site's index lists no attestations…
```

## 4 · What a failure looks like

Something changes a post on the server without re-signing it:

```bash
POST_FILE=$(find "$SITE_DIR/content" -name 'hello.md' | head -1)
printf '\nA line added by hand, outside polis.\n' >> "$POST_FILE"
```

```bash exit=1
polis --json validate https://alice.example | jq -c '.checks[] | select(.id == "content.posts") | {outcome, findings}'
```

```json output
{
  "outcome": "failed",
  "findings": [
    "content/pub.polis.core/post/…/hello.md: signature does not verify",
    "content/pub.polis.core/post/…/hello.md: body does not match its current-version hash"
  ]
}
```

Two separate findings: the signature no longer covers the words, and the words no longer hash to the
`current-version` the post declares.

The command exits `1` when anything failed, and `0` otherwise — including when checks did not run, so a
CI job should read the report, not only the exit code.

## Reading the four outcomes

| Outcome | Means |
|---|---|
| `passed` | checked, and nothing was wrong |
| `failed` | checked, and something was wrong |
| `warning` | checked, and something **around** the site is worth reading — a CDN adding directives to its `robots.txt`, say. Not a defect in the site, and it does not fail the run |
| `not_applicable` | **not checked** — `reason` says why |

## What this proves — and what it does not

| It proves | It does not prove |
|---|---|
| what the site serves **now** verifies — or exactly which parts do not | that the site is honest, or its author trustworthy — that is your judgment |
| unsigned artifacts are reported as unsigned, never as broken | anything about owner-only state (keys, private policies), which is never served |
| | the site's key history against what a discovery service witnessed — that comparison runs only in the hosted fleet |
| | tags and attestations the site's index does **not** list — a site-wide run finds records only through the index. See [recipe 13](13-verify-a-sites-attestations.md) |
