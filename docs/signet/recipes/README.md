# The recipe book

*Issuing claims from an app, reading the graph, verifying a chain*

Task-first. Each recipe starts from a problem and gives **the records it produces, the exact commands,
how to check the result from another machine, and what the result proves — and what it does not.**

New to the model underneath? Read [the graph](../concepts/graph.md) first. It explains what the nodes
and edges are, and which parts of any answer are facts and which are your judgment.

## The recipes

| # | Recipe | Uses |
|---|---|---|
| 1 | [Make a claim about someone, then verify it from a different machine](01-make-a-claim.md) | `attest issue` · `clone` · `attest verify` · `validate <url>` |
| 2 | [Pin a correction to exact bytes, so an edit cannot move it](02-pin-a-correction.md) | `preview` · `attest issue --subject-version` |
| 3 | [Withdraw a claim — and what a reader sees afterwards](03-withdraw-a-claim.md) | `attest withdraw` |
| 4 | [List every attestation a site has issued](04-list-attestations.md) | `.well-known/polis` · `index.jsonl` |
| 5 | [Verify a site you do not own, end to end](05-verify-a-site.md) | `validate <url>` |
| 6 | [Prove two domains are the same party](06-prove-same-party.md) | a `same-as` pair |
| 7 | [Verify an operator's actor registry, and an actor's expected actions](07-check-an-actor.md) · *⏸️ not runnable in this release* | `actor register` · `actor verify <action>` |
| 8 | [Keep old posts verifying across a key rotation](08-survive-a-key-rotation.md) | `rotate-key` · key history |
| 9 | [Prove a claim existed by a given time](09-prove-when.md) | discovery-service witnesses |
| 13 | [Verify every attestation a site has issued, from outside](13-verify-a-sites-attestations.md) | `validate <url>` · `attest verify` · `rotate-key` |
| 14 | [Verify a site's follow file by hand](14-verify-signed-edges.md) | `follow` · `validate <url>` · `jq` · `ssh-keygen -Y verify` |

## How to read them

- **The examples use fixture domains** — `alice.example`, `bob.example` and so on — and a discovery
  service at `ds.example`. Use your own domains; on the public network the discovery service is
  `ds.polis.pub`.
- **"Deploy your site"** means whatever you already do to put a site directory online at its domain.
  Every command that fetches `https://…` assumes the site it names is live.
- **`…` in an output** stands for a value that differs on every run — an id, a hash, a timestamp — or
  for text cut for length.
- The commands need the polis CLI, `curl` and `jq`.

## ⛔ Every recipe is tested

A recipe that drifts from the software teaches the wrong thing, which is worse than no recipe. So each
page here is **run on every test run**: `cli-go/cmd/polis/recipes_test.go` reads the markdown, runs
every `bash` block in order against real sites, and compares each output block with what the command
printed. Change a command's output and the recipe that shows it fails.

Writing one:

- A `bash` block **always runs** — there is no way to mark one as illustration. Put anything that should
  not run in a `text` block.
- `bash exit=1` runs a block that is supposed to fail, and checks it does.
- ⏸️ **One exception, for a whole page and never hidden:** a recipe with a paragraph opening *not runnable
  in this release* is still read and checked for shape, but not run. It says so on its page and in the
  table above, and the test suite fails if either says so without the other. It keeps a page readable
  while something it needs is unavailable; it is never a way to hide a recipe that has drifted.
- `json output` after a `bash` block checks the JSON it printed: every key shown must match, keys not
  shown are ignored, and `"…"` matches anything. `text output` checks that its lines appear, in order.
- Each recipe starts in an empty directory with a fresh network, and a site made with
  `mkdir <name>.example && cd <name>.example && polis init` is live at `https://<name>.example`.

⭐ **Every capability that ships adds or unlocks its recipes as part of being done.** This book is the
frame; it grows with the work.

## What you cannot build yet

**A recipe that cannot be written against a documented surface is a missing API**, so it is listed here
rather than papered over. Each names what would unblock it.

| Recipe | Why it cannot be written | Unblocked by |
|---|---|---|
| **10** · Find everyone who has vouched for a party | A site lists only its own attestations (recipe 4), so you must already know whom to ask. A discovery service can answer *"what has been said about X"* — `GET /v1/content?type=pub.polis.attestation&metadata.subject=<id>` ([API reference](../../ds/developer/api-reference.md)) — but only over attestations their issuers registered with it, and only against a live service. Its event stream carries `subject` but no `target_domain` for attestations, so a subscriber is not told when it is named | a discovery service the test harness can run (below); `target_domain` on attestation events |
| **11** · Countersign a claim made about you | No subject-assent predicate is reserved. The format can carry one — a record can be the subject of another — but what assent *means* is unsettled, and *"I agree this is true"* is the definition to avoid | reserving a subject-assent predicate — **not yet planned** |
| **12** · *"n of the people you follow have vouched for X"* | Needs recipe 10's query, joined with the signed follow file | whatever unblocks recipe 10 |
| Prove when an **attestation** existed, from outside | ⚠️ **The reason this was listed no longer holds.** It said no live site published both a `witnesses` pointer and a witnessed attestation; live sites now do, and `polis validate` counts attestations among the artifacts it checks witnesses for (`content.witnesses`). The test harness's discovery service witnesses every content registration whatever its type, and registering an attestation publishes the witness that comes back — so nothing is known to block it | writing the recipe |
| Check a **stream event** against an actor's expected actions | `polis actor verify` reads attestation records. An operator's expected actions also name events such as `pub.polis.actor.registered`, and nothing verifies an event's own signature for a stranger | **not yet planned** |
| Ask a discovery service for an **operator's** attestations | `polis actor register` writes its disclosures without registering them, and `polis attest register <id>` then registers each one with a discovery service, which can answer for it. What a recipe cannot do yet is ask: the answer comes from a live service. Recipe 4 reads the site instead | a discovery service the test harness can run |
| **Verify a signed blessing list by hand** — recipe 14's check, for `blessed.json` | The only command-line path that signs a blessing list is `polis blessing grant <version>`. It resolves the pending request from the version, sends the comment's URL and the post it replies to, and writes the signed list once a discovery service accepts the grant, so the list it writes needs a service to accept one. The signing base is specified with byte-exact examples in [signing base §5.3](../spec/signing-base.md#the-blessing-list-blessedjson) | a discovery service the test harness can run |
| Anything through the **webapp REST API** | It has no attestation endpoints | **not yet planned** |
| **Check your own custody** — your operator's declaration beside who signed your events | `polis actor verify --custody <domain>` ([`custody.md` §12.5](../spec/custody.md#125--how-the-tenant-checks--and-what-is-honestly-independent)) reads the declaration and grants from fixture-able sites, but its *"Who did this?"* half reads `signed_by` / `authority` from a discovery service's stream. The declaration and grant half alone would teach a check that stops before the part that makes it useful | a discovery service the test harness can run (the row below) |
| **See exactly what an agent did for you** — the grants on your site, and every blessing entry marked with one | The records exist on disk and are readable with `jq` ([`delegation.md`](../spec/delegation.md) §5–§6), but **nothing the harness can run produces a marked act**: an agent acts only inside the web app's server sync, and no CLI path issues a grant non-interactively or makes an agent decision. A recipe over a hand-written fixture would teach the format without proving any implementation writes it | a command-line or test-harness path that runs an agent decision against a fixture site |
| A recipe that queries a **live** discovery service | `GET /v1/content` filters by `type`, `actor` and `metadata.*` and needs no auth ([API reference](../../ds/developer/api-reference.md#get-v1contenttypepubpolispost)) — but a recipe that calls a live service cannot be checked on every test run, and an unchecked recipe does not ship | a discovery service the test harness can run |
| Recipe 6 **on live domains** | The pair works between two independently keyed fixture sites; no such pair exists on the public network yet | publishing such a pair |
