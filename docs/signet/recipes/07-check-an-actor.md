# Verify an operator's actor registry, and an actor's expected actions

*Recipe 7 · [the recipe book](README.md)*

*For* [Developers](../../README.md#building-on-polis) · [Reviewers](../../README.md#reviewing-the-security-and-identity-design) — *Kind* [Recipe](../../README.md#kinds-of-page) — *Component* [CLI](../../cli/README.md) — *See also* [concept](../../general/concepts/actors.md) · [spec](../spec/custody.md)

⏸️ not runnable in this release — the test suite does not run this page; the commands are written as they are meant to run.
The page is kept because it shows how the check works.

⚠️ **Why it does not run today.** In this release `polis actor register` does nothing on a site that is
not already an operator, because making a site an operator is operator administration and not a job
for a command-line tool anyone can run. The setup below starts from a fresh site, so it stops at the
first `register`. **The checks after it are the point of the page**, and `polis actor verify <action>`
itself is unaffected.

**The problem.** A record arrives signed by `judge.example`, which says it is an operator's verifier.
You want to know three different things: **is it really one of that operator's actors, did it agree
to how it is listed, and is this the kind of thing the operator said it would do?**

**What it runs.** `polis actor verify <action>` — the stranger's check from
[`custody.md` §7](../spec/custody.md#7--how-a-stranger-checks--with-nothing-but-curl-and-a-signature-verifier),
from any machine, with no site and no key.

⛔ **The registry is EXPECTED, never ALLOWED.** The operator holds its actors' keys, so no published
list can stop an actor doing anything. What the list buys is that a deviation is **visible to
anyone** — and the remedy for one is social: ask, report, publish. Nothing below is enforcement.

## Set up an operator, its actor, and a lookalike

```bash
WORK=$PWD
mkdir judge.example lookalike.example bob.example operator.example
(cd judge.example && POLIS_BASE_URL=https://judge.example polis init --site-title "Judge")
(cd lookalike.example && POLIS_BASE_URL=https://lookalike.example polis init --site-title "Judge")
(cd bob.example && POLIS_BASE_URL=https://bob.example polis init --site-title "Also Judge")
cd operator.example
export POLIS_BASE_URL=https://operator.example
polis init --site-title "Operator"
```

The operator lists Judge, expecting one kind of action. `--countersign-with` has the actor's own key
agree to the entry, and writes the actor's `operator` claim — it needs both sites on one machine; an
actor elsewhere runs `polis actor declare-operator` itself.

```bash
polis --json actor register judge.example --authority operator \
  --expects pub.polis.attestation.integrity \
  --countersign-with ../judge.example --no-announce | jq -c '.data | {domain, expected_actions, countersigned}'
```

```json output
{"domain": "judge.example", "expected_actions": ["pub.polis.attestation.integrity"], "countersigned": true}
```

Now three signed records: Judge doing what it is expected to, Judge doing something else, and a
lookalike claiming the same operator.

```bash
cd "$WORK/judge.example"
export POLIS_BASE_URL=https://judge.example
OBSERVATION="--payload result=verified --payload vantage=recipe --payload observed=2026-09-14T12:00:00Z"
EXPECTED=$(polis --json attest issue --predicate pub.polis.attestation.integrity \
  --subject https://alice.example --subject-type identity $OBSERVATION | jq -r .data.url)
OFFLIST=$(polis --json attest issue --predicate pub.polis.attestation.endorsement \
  --subject https://alice.example --subject-type identity | jq -r .data.url)

cd "$WORK/lookalike.example"
export POLIS_BASE_URL=https://lookalike.example
polis actor declare-operator operator.example
LOOKALIKE=$(polis --json attest issue --predicate pub.polis.attestation.integrity \
  --subject https://alice.example --subject-type identity $OBSERVATION | jq -r .data.url)

cd "$WORK/bob.example"
export POLIS_BASE_URL=https://bob.example
SILENT=$(polis --json attest issue --predicate pub.polis.attestation.integrity \
  --subject https://alice.example --subject-type identity $OBSERVATION | jq -r .data.url)

cd "$(mktemp -d)"
unset POLIS_BASE_URL
```

## 1 · An expected action

```bash
polis --json actor verify "$EXPECTED" \
  | jq -c '{operator: .data.operator, steps: [.data.steps[].outcome], findings: [.data.findings[].kind]}'
```

```json output
{"operator": "operator.example", "steps": ["passed", "passed", "passed", "passed", "passed", "passed"], "findings": []}
```

The six steps: the action verifies against Judge's key · Judge names an operator, which publishes a
registry · the registry verifies against the operator's key · the registry lists Judge · Judge
countersigned its entry · the action's predicate is among Judge's expected actions.

## 2 · A deviation

```bash exit=1
polis actor verify "$OFFLIST"
```

```text output
[✓] 1 signature …
[✓] 4 entry … lists judge.example …
[✓] 5 countersignature …
[✗] 6 expected_action … pub.polis.attestation.endorsement is NOT among judge.example's expected actions …
[!] DEVIATION (step 6)
```

Everything about the signer checks out, and **the action is still outside what the operator said it
would do.** That is information to report and ask about — it is not a violation anything blocked.

## 3 · A lookalike

```bash exit=1
polis --json actor verify "$LOOKALIKE" | jq -c '{steps: [.data.steps[].outcome], findings: [.data.findings[].kind]}'
```

```json output
{"steps": ["passed", "passed", "passed", "failed", "not_reached", "not_reached"], "findings": ["lookalike"]}
```

`lookalike.example` claims the operator, and its signature is genuinely its own — but the operator's
signed registry does not list it. **This one is conclusive**, and the check stops: a domain nobody
lists has no expected actions to deviate from.

## 4 · A lookalike that claims nobody

A lookalike does not have to claim an operator at all. Without a claim there is nothing to follow — so
name the operator you are asking about:

```bash
polis --json actor verify "$SILENT" | jq -c '[.data.steps[].outcome]'
```

```json output
["passed", "not_applicable", "not_reached", "not_reached", "not_reached", "not_reached"]
```

```bash exit=1
polis --json actor verify "$SILENT" --operator operator.example | jq -c '[.data.findings[].kind]'
```

```json output
["lookalike"]
```

## The findings are different facts

| Finding | Step | Means |
|---|---|---|
| `lookalike` | 4 | the operator does not list the signer. **Conclusive** |
| `countersignature_invalid` | 5 | the operator changed the entry after the actor agreed to it — including widening what it claims the actor does |
| `deviation` | 6 | a listed actor did something off its list. **Evidence**, not a verdict |

An entry with **no** countersignature is reported as `weaker` — *"we say this is ours"*, not *"and it
agrees"* — and is not a finding. Neither is a fetch that failed. The full list is in the
[JSON contract](../../cli/user/json-mode.md#polis-actor-verify-action).

## What this proves — and what it does not

| It proves | It does not prove |
|---|---|
| whether the operator's signed registry lists the signer, and under what expectations | that a listed actor behaves — the operator holds the keys and can sign anything |
| whether the actor itself agreed to that entry | that anyone is watching. The check is cheap so that someone can; at the time of writing, few do |
| that an action falls outside what was published | that the deviation was wrong — expectations are descriptive, and an operator may simply not have updated its list |

⚠️ Only signed **attestation records** can be checked this way today. An operator also lists stream
events among an actor's expected actions, and nothing yet checks one of those — see the
[gap list](README.md#what-you-cannot-build-yet).
