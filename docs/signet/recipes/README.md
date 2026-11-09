# The recipe book

*Standing up an identity, proving what you wrote, stating your terms, and checking somebody else's site*

Task-first. Each recipe starts from a problem and gives **the records it produces, the exact commands,
how to check the result from another machine, and what the result proves — and what it does not.**

⛔ **Every command on every page is run by the test suite.** A recipe that drifts from the software
teaches the wrong thing, so none of them is allowed to. [How that works](#-every-recipe-is-tested), if
you are writing one.

## Start with the question you have

The [six capabilities](../README.md#what-it-covers) polis covers, each as the question it answers:

| | The question | Start here |
|---|---|---|
| **Identity** | *Who is this, and who says so?* | [17 · Stand up an identity, and read it two ways](17-stand-up-an-identity.md) |
| **Attribution** | *Did they really write this, and is it unchanged?* | [15 · Publish a post, and have a stranger prove it is yours](15-publish-and-be-proven.md) |
| **Consent & licence** | *What am I allowed to do with it?* | [16 · State your terms, and watch them travel with the work](16-state-your-terms.md) |
| **Attestation** | *What has someone claimed about someone else?* | [1 · Make a claim about someone, then verify it from a different machine](01-make-a-claim.md) |
| **Delegation** | *What did something acting for them do?* | ⏸️ **designed, not yet demonstrable** — see [what you cannot build yet](#what-you-cannot-build-yet) |
| **Verification** | *Does any of this actually check out?* | [5 · Verify a site you do not own, end to end](05-verify-a-site.md) |

⛔ **That Delegation row names a gap instead of a recipe, deliberately, and will until it can do
otherwise.** The records exist and are specified, but nothing the test harness can run produces a
*marked agent act* — an agent acts only
inside the web app's server sync, and no command-line path issues a grant or runs an agent decision. A
recipe over a hand-written fixture would teach the format while proving no implementation writes it.

## The recipes

### Start here

The three most basic things, and the order most people meet them in.

| # | Recipe | Uses |
|---|---|---|
| 17 | [Stand up an identity, and read it two ways](17-stand-up-an-identity.md) | `init` · `.well-known/polis` · `.well-known/did.json` · `jq` |
| 15 | [Publish a post, and have a stranger prove it is yours](15-publish-and-be-proven.md) | `post` · `validate <url>` · `curl` · `jq` · `ssh-keygen -Y verify` |
| 16 | [State your terms, and watch them travel with the work](16-state-your-terms.md) | `license` · `post` · `render` · `robots.txt` · `rsl.xml` |

### Checking someone else's site

| # | Recipe | Uses |
|---|---|---|
| 5 | [Verify a site you do not own, end to end](05-verify-a-site.md) | `validate <url>` |
| 14 | [Verify a site's follow file by hand](14-verify-signed-edges.md) | `follow` · `validate <url>` · `jq` · `ssh-keygen -Y verify` |
| 13 | [Verify every attestation a site has issued, from outside](13-verify-a-sites-attestations.md) | `validate <url>` · `attest verify` · `rotate-key` |
| 4 | [List every attestation a site has issued](04-list-attestations.md) | `.well-known/polis` · `index.jsonl` |

### Making and checking claims

| # | Recipe | Uses |
|---|---|---|
| 1 | [Make a claim about someone, then verify it from a different machine](01-make-a-claim.md) | `attest issue` · `clone` · `attest verify` · `validate <url>` |
| 2 | [Pin a correction to exact bytes, so an edit cannot move it](02-pin-a-correction.md) | `preview` · `attest issue --subject-version` |
| 3 | [Withdraw a claim — and what a reader sees afterwards](03-withdraw-a-claim.md) | `attest withdraw` |
| 9 | [Prove a claim existed by a given time](09-prove-when.md) | discovery-service witnesses |
| 18 | [Prove when an attestation existed](18-prove-when-an-attestation-existed.md) | `attest issue` · `validate <url>` · discovery-service witnesses |

### Further in

| # | Recipe | Uses |
|---|---|---|
| 8 | [Keep old posts verifying across a key rotation](08-survive-a-key-rotation.md) | `rotate-key` · key history |
| 6 | [Prove two domains are the same party](06-prove-same-party.md) | a `same-as` pair |
| 7 | [Verify an operator's actor registry, and an actor's expected actions](07-check-an-actor.md) · *⏸️ not runnable in this release* | `actor register` · `actor verify <action>` |

⚠️ **The numbers are not an order and never were.** Published doc paths are permanent, so a recipe keeps
the number it was written with; the grouping above is the reading order. Gaps in the sequence are the
recipes that [cannot be written yet](#what-you-cannot-build-yet).

New to the model underneath? Read [the graph](../concepts/graph.md) first. It explains what the nodes
and edges are, and which parts of any answer are facts and which are your judgment.

## How to read them

- **The examples use fixture domains** — `alice.example`, `bob.example` and so on — and a discovery
  service at `ds.example`. Use your own domains; on the public network the discovery service is
  `ds.polis.pub`.
- **"Deploy your site"** means whatever you already do to put a site directory online at its domain.
  Every command that fetches `https://…` assumes the site it names is live.
- **`…` in an output** stands for a value that differs on every run — an id, a hash, a timestamp — or
  for text cut for length.
- The commands need the polis CLI, `curl` and `jq`.

## What you cannot build yet

**A recipe that cannot be written against a documented surface is a missing API**, so it is listed here
rather than papered over. Each names what would unblock it.

| Recipe | Why it cannot be written | Unblocked by |
|---|---|---|
| **10** · Find everyone who has vouched for a party | A site lists only its own attestations (recipe 4), so you must already know whom to ask. A discovery service can answer *"what has been said about X"* — `GET /v1/content?type=pub.polis.attestation&metadata.subject=<id>` ([API reference](../../ds/developer/api-reference.md)) — but only over attestations their issuers registered with it, and only against a live service. Its event stream carries `subject` but no `target_domain` for attestations, so a subscriber is not told when it is named | a discovery service the test harness can run (below); `target_domain` on attestation events |
| **11** · Countersign a claim made about you | No subject-assent predicate is reserved. The format can carry one — a record can be the subject of another — but what assent *means* is unsettled, and *"I agree this is true"* is the definition to avoid | reserving a subject-assent predicate — **not yet planned** |
| **12** · *"n of the people you follow have vouched for X"* | Needs recipe 10's query, joined with the signed follow file | whatever unblocks recipe 10 |
| Prove when an **attestation** existed, from outside | ✅ **No longer blocked — the recipe is written.** Live sites now publish both a `witnesses` pointer and witnessed attestations, `polis validate` checks an attestation's witness (`content.witness` for one record, `content.witnesses` across a site), and the test harness's discovery service witnesses every content registration whatever its type | [18 · Prove when an attestation existed](18-prove-when-an-attestation-existed.md) |
| Check a **stream event** against an actor's expected actions | `polis actor verify` reads attestation records. An operator's expected actions also name events such as `pub.polis.actor.registered`, and nothing verifies an event's own signature for a stranger | **not yet planned** |
| Ask a discovery service for an **operator's** attestations | `polis actor register` writes its disclosures without registering them, and `polis attest register <id>` then registers each one with a discovery service, which can answer for it. What a recipe cannot do yet is ask: the answer comes from a live service. Recipe 4 reads the site instead | a discovery service the test harness can run |
| **Verify a signed blessing list by hand** — recipe 14's check, for `blessed.json` | The only command-line path that signs a blessing list is `polis blessing grant <version>`. It resolves the pending request from the version, sends the comment's URL and the post it replies to, and writes the signed list once a discovery service accepts the grant, so the list it writes needs a service to accept one. The signing base is specified with byte-exact examples in [signing base §5.3](../spec/signing-base.md#the-blessing-list-blessedjson) | a discovery service the test harness can run |
| Anything through the **webapp REST API** | It has no attestation endpoints | **not yet planned** |
| **Check your own custody** — your operator's declaration beside who signed your events | `polis actor verify --custody <domain>` ([`custody.md` §12.5](../spec/custody.md#125--how-the-tenant-checks--and-what-is-honestly-independent)) reads the declaration and grants from fixture-able sites, but its *"Who did this?"* half reads `signed_by` / `authority` from a discovery service's stream. The declaration and grant half alone would teach a check that stops before the part that makes it useful | a discovery service the test harness can run (the row below) |
| **See exactly what an agent did for you** — the grants on your site, and every blessing entry marked with one | The records exist on disk and are readable with `jq` ([`delegation.md`](../spec/delegation.md) §5–§6), but **nothing the harness can run produces a marked act**: an agent acts only inside the web app's server sync, and no CLI path issues a grant non-interactively or makes an agent decision. A recipe over a hand-written fixture would teach the format without proving any implementation writes it | a command-line or test-harness path that runs an agent decision against a fixture site |
| A recipe that queries a **live** discovery service | `GET /v1/content` filters by `type`, `actor` and `metadata.*` and needs no auth ([API reference](../../ds/developer/api-reference.md#get-v1contenttypepubpolispost)) — but a recipe that calls a live service cannot be checked on every test run, and an unchecked recipe does not ship | a discovery service the test harness can run |
| Recipe 6 **on live domains** | The pair works between two independently keyed fixture sites; no such pair exists on the public network yet | publishing such a pair |

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
