# Check a site, from anywhere, trusting nothing

*For* [Writers](../../README.md#writing-on-polis) · [Reviewers](../../README.md#reviewing-the-security-and-identity-design) — *Kind* [Guide](../../README.md#kinds-of-page) — *Component* [CLI](../../cli/README.md) — *See also* [concept](../concepts/verification.md) · [recipe](../recipes/05-verify-a-site.md)

Polis signs things. This is how you find out whether the signatures hold — on your own site, on a
copy of someone else's, or on a single post you are looking at right now.

One command, two forms, and **neither one writes anything**:

```bash
polis validate                          # this directory
polis validate ./alice                  # any directory, including a clone
polis validate https://alice.example    # a whole site, over the network
polis validate https://alice.example/content/pub.polis.core/post/20260101/hello.md
```

The reference is in [command-reference.md](../../cli/user/command-reference.md#polis-validate); this
is the guide for what to do with it and how to read what comes back.

---

## Why this exists

Every verification polis added for years landed in the hosted actors. Patrol sweeps tenant
directories, Judge checks the trust chain, Medic repairs derived files — and all three run on the
operator's machine, for the operator's tenants.

That left two people with nothing.

**A self-hoster** runs the same code and signs the same artifacts and had no way to ask whether any
of it verified. **A tenant** who wanted to check her own host's behaviour could only ask the host.
Which is the problem: *if your operator is the party you are checking, a page served by your operator
is not evidence.*

So this runs on your machine, against public URLs, and it can be pointed at a site it has no
relationship with — no local copy, no key, no account.

## The one thing to understand about the output

Every check comes back as one of **three** outcomes that matter, not two — passed, failed, and not
checked:

```
signed-content-integrity
  ✓ content.posts            8 posts examined: 8 signature(s) verified, 0 unsigned
  ✗ content.comments         3 comments examined: 2 signature(s) verified, 0 unsigned
      · content/pub.polis.core/comment/20260101/abc.md: signature does not verify
  – identity.key_perms       NOT CHECKED: no .polis/ directory — this is a published copy rather
                             than a working site, so owner-only state is not present to check
```

**`NOT CHECKED` is the load-bearing one.** A validator that ran twelve checks out of twenty and
printed *"no errors"* would tell you your key permissions were fine when nobody looked. So the rule
is absolute:

> **A clean result means "I checked and it was fine." It never means "I did not check."**

There is a fourth, rarer outcome: a **warning** (`!`), for something true about a site's surroundings
rather than the site itself — a managed `robots.txt` an intermediary added directives to, say. A warning
never fails the run.

The summary line counts all four, and warns you when any check did not run. From a real run against
`https://vdibart.polis.pub` on 2026-09-16:

```
24 checks: 19 passed, 0 failed, 0 warning, 5 not applicable
[!] 5 check(s) did not run. This result covers only what was checked; see NOT CHECKED above.
[✓] Nothing checked was found wrong.
```

## What can be checked depends on what is there

Not on which form you ran. There is no mode flag, and there is nothing to configure.

| You are checking | It can see |
|---|---|
| **your own site** | everything — `.polis/`, the private key and its permissions, the storage salt, private policies, the bundle registry |
| **a clone** | `content/`, `.well-known/` — automatically less, with no special handling |
| **a URL** | whatever the site serves publicly |

A clone has no `.polis/` directory, so the owner-only checks report NOT CHECKED and name what was
missing. That is the whole of the clone handling: the validator does not need to know it is looking
at a clone, only to report what was not there.

## Unsigned is not broken

Most artifacts on most sites carry no signature. A site that has stated no licence terms has stated
nothing — not *lost* something. Both report fine.

Only **present-and-failing** is a finding: a signature that exists and does not verify, a body that
does not match the hash its own frontmatter claims, a licence pointer that leads nowhere.

But a passing check still tells you the shape of what it saw — *"12 posts examined: 9 signature(s)
verified, 3 unsigned"* — because that is a different fact from *"12 verified"*, and rolling them
together is how a number stops meaning anything.

## Checking one thing

The per-URL form answers the question a reader actually has: **is this thing signed by who it says?**

```bash
polis validate https://alice.example/content/pub.polis.core/post/20260101/hello.md
```

```
Checking https://alice.example/content/pub.polis.core/post/20260101/hello.md (remote, one artifact)
This result covers ONE artifact's signature and hash. Index consistency, key
permissions, policy files and everything else site-wide were not examined.

signed-content-integrity
  ✓ content.artifact         signature verifies against the key published at https://alice.example
  ✓ content.hash             body matches its current-version hash
  ✓ content.witness          unwitnessed: no discovery service countersignature covers these bytes. The artifact still verifies on its own signature; its date is only its own claim.
```

It fetches the artifact, the site's `.well-known/polis` for its key, and the witness set that document
points to. The key is cached per domain, so checking many artifacts on one site still costs one key
fetch. The same form takes a JSON record — an attestation, say — and checks its signature and its
`current_version`.

⚠️ **Read the caveat, and keep it attached.** This says the artifact verifies. It says nothing about
the index, the site's keys, or anything else — and the `--json` output carries `"scope": "artifact"`
and the same sentence in `note`, so a result cannot be quoted as a broader claim than it is.

## Checking a whole site you do not own

```bash
polis validate https://alice.example
```

Everything the site serves gets checked: the identity document, the DID document if there is one,
the key history, each declared bundle, every indexed post, comment, attestation and tag file, the
witnesses published for them, the follow file, the blessing list, the licence and its `robots.txt` and
`rsl.xml` projections, and the public policy file.

What HTTP cannot reach says so. Two limits in particular:

- **Owner-only state** — keys, permissions, private policies, the bundle registry — is never served,
  and never will be.
- **Only what the index lists.** Posts, comments, attestations and tags are found through the site's
  `index.jsonl` ([recipe 13](../recipes/13-verify-a-sites-attestations.md) verifies a site's attestations
  this way). ⚠️ An index is only as fresh as the site's last write or heal, so **a type with no entries
  means nothing indexed, never nothing exists** — and HTTP does not list directories, so a record the
  index omits cannot be found from outside.

## Keeping a copy

`polis validate` **never clones.** Downloading is [`polis clone`](../../cli/user/command-reference.md#polis-clone-url-target-dir)'s
job, and the two compose:

```bash
polis clone https://alice.example ./alice     # keeps the files, and --diff tracks changes over time
polis validate ./alice                        # it is just a path
```

Do that when you want the artifacts kept — for an archive, for a record of what a site said on a
given day, or to check the same site repeatedly without re-fetching everything.

## In a script

```bash
polis validate --json https://alice.example > report.json
```

Exit status is `0` when nothing checked was found wrong and `1` when something failed or the run
could not start.

⛔ **Do not read `failed == 0` as "the site is fine."** Read `not_applicable` too — those checks did
not run, and each carries a `reason` saying why. The JSON shape is in
[json-mode.md](../../cli/user/json-mode.md#polis-validate).

## What it will not do

**It reports. It never repairs** — there is no `--fix`, and there will not be one. A tool that
changes a site is an actor, actors act on someone's behalf, and acting on your behalf is a thing that
has to be declared and consented to rather than helpfully assumed. Fixing what this finds is your
move to make.

**It is not a verdict.** Every line is a fact: this signature verifies, this one does not, this was
not checked. What any of that *means* — whether an unsigned follow file matters to you, whether a
site with two bad signatures is worth reading — is yours to decide, not the tool's.

**It will sometimes say "could not be checked" about a file that is perfectly good, and that is
correct.** A signed JSON artifact may carry a field this build does not recognise — a newer polis, or
another implementation, signing something this one has not learned about yet. When that happens the
rebuild cannot match and the honest answer is *"signed with fields this verifier does not
understand"*, listed under `not_applicable` rather than as a failure. ⛔ **It is never reported as
invalid**, because calling a good file forged is worse than admitting you cannot read it. The rule is
[`spec/signing-base.md` §6.1](../spec/signing-base.md). Conversely, an unrecognised field *outside*
the signature leaves the check passing and the line says so: the signature verifies, and that field
is not covered by it.

---

**See also:** [concepts/graph.md](../concepts/graph.md) for what a reader verifies and what stays policy ·
[guides/set-your-terms.md](set-your-terms.md) for the licence this checks the signature on.
