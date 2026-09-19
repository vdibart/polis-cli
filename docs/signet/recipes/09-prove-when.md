# Prove a claim existed by a given time

*Recipe 9 · [the recipe book](README.md)*

*For* [Developers](../../README.md#building-on-polis) — *Kind* [Recipe](../../README.md#kinds-of-page) — *Component* [CLI](../../cli/README.md) — *See also* [spec](../spec/witness.md)

Every command on this page is run by the test suite.

**The problem.** Everything a site signs carries dates the site wrote itself. You want to show that
something existed **no later than** a certain moment — in a way the author could not have faked.

**What makes it work.** When a site registers content with a discovery service, the service
**witnesses** it: it signs the exact bytes it saw and the time by its own clock, and the site
publishes that countersignature beside the content. Spec: [witness](../spec/witness.md).

⛔ **Nothing ever requires a witness.** An artifact without one verifies exactly as it always did; a
witness adds an independent date, and never takes anything away.

The examples' discovery service is `ds.example`; on the public network it is `ds.polis.pub`.

## Set up a registered site, and publish

Registering once is what lets a discovery service witness your content. A hosted polis site is already
registered.

```bash
mkdir alice.example && cd alice.example
export POLIS_BASE_URL=https://alice.example
polis init --site-title "Alice"
polis register
printf '# A claim with a date\n\nWritten before anyone asked.\n' > claim.md
polis post claim.md
SITE_DIR=$PWD
```

The site now publishes a witness set, found — like everything else — through its identity document:

```bash
jq -r .witnesses .well-known/polis
```

```text output
/content/witness/witnesses.json
```

## 1 · A reader checks the date

```bash
cd "$(mktemp -d)"
unset POLIS_BASE_URL
BUNDLE=$(curl -s https://alice.example/.well-known/polis | jq -r '.bundles["pub.polis.core"].path')
POST="https://alice.example/$(curl -s "https://alice.example/$(dirname "$BUNDLE")/index.jsonl" \
  | jq -r 'select(.type == "post") | .path' | head -1)"
polis --json preview "$POST" \
  | jq '.data.witness | {state, earliest_witnessed_at, earliest_binds, checks: [.checks[] | {ds, status}]}'
```

```json output
{
  "state": "witnessed",
  "earliest_witnessed_at": "…Z",
  "earliest_binds": "artifact",
  "checks": [ { "ds": "https://ds.example", "status": "verified" } ]
}
```

`verified` means the witness's signature checks out against the key the discovery service publishes —
fetched from the service the witness names, never from the witness itself. `earliest_binds: artifact`
means it covers the whole signed post, frontmatter included.

## 2 · Compare it with a deadline

Timestamps are UTC with a `Z`, so they compare as strings:

```bash
WHEN=$(polis --json preview "$POST" | jq -r .data.witness.earliest_witnessed_at)
DEADLINE=2100-01-01T00:00:00Z
[[ "$WHEN" < "$DEADLINE" ]] && echo "a discovery service saw these exact bytes before $DEADLINE"
```

```text output
a discovery service saw these exact bytes before 2100-01-01T00:00:00Z
```

## 3 · What a forged date looks like

Suppose the site edits its witness set to claim an earlier date:

```bash
cd "$SITE_DIR"
WITNESSES=".$(jq -r .witnesses .well-known/polis)"
jq '.witnesses |= map_values(map(.witnessed_at = "2020-01-01T00:00:00.000Z"))' "$WITNESSES" > witnesses.tmp
mv witnesses.tmp "$WITNESSES"
cd "$OLDPWD"
```

The witness signature covers `witnessed_at`, so the edit breaks it — and a published witness that does
not verify is the **one** witness case that fails a check, because the site is claiming testimony it
does not have:

```bash exit=1
polis --json validate https://alice.example \
  | jq -c '.checks[] | select(.id == "content.witnesses") | {outcome}'
```

```json output
{"outcome": "failed"}
```

## The five cases

| A reader finds | `state` | Fails a check? |
|---|---|---|
| no witness | `unwitnessed` | **never** — the artifact verifies on its own signature, and its date is only its own claim |
| a witness whose service's key cannot be fetched | `unverifiable` | **never** — a bonus claim went unexamined |
| a witness that verifies | `witnessed` | — `earliest_witnessed_at` is the independent date |
| a witness of **other** bytes — an earlier version | the check says `other_bytes` | **never** — true testimony about a superseded version |
| a witness that does **not** verify — forged or altered | `invalid` | ⛔ **yes** |

## What this proves — and what it does not

| It proves | It does not prove |
|---|---|
| a discovery service saw **these exact bytes** no later than `earliest_witnessed_at`, by its own clock | when they were written — only that they existed by then |
| the date is not the author's to choose | anything about the service's honesty — you are trusting its key and its clock, which is one party rather than none |
| | that the content was not seen earlier somewhere else, or that nothing was left unregistered |

⚠️ Attestation records are witnessed on registration too, but `polis preview` and the remote
`polis validate` check witnesses on **posts and comments** only — see the
[gap list](README.md#what-you-cannot-build-yet).
