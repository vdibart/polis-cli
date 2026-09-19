# Actors — Background Jobs & Operational Tools

*For* [Reviewers](../../README.md#reviewing-the-security-and-identity-design) · [Operators](../../README.md#running-polis) — *Kind* [Concept](../../README.md#kinds-of-page) — *Component* [Hosted service](../../ops/README.md) — *See also* [spec](../../signet/spec/custody.md) · [guide](../../signet/guides/network-health.md) · [recipe](../../signet/recipes/07-check-an-actor.md)

> **Audience:** Anyone who wants to know how a polis site stays healthy, and what the software running
> beside it may and may not do: reviewers of the security and identity design, operators deciding what
> to run, self-hosters, and readers who want to understand what the hosted service does in their name.
>
> This page is the design. It covers what each actor checks or does, what it deliberately does **not**
> do, the decisions behind it, and its known gaps. Operational detail (event names and payloads, per-alert
> runbooks, performance figures and standalone-binary procedure) is not documented publicly.

---

## Overview

The polis hosted service runs a set of independent background actors that maintain system health without
human intervention. Each actor has a single, well-defined responsibility. They do not overlap, and they do
not call each other. Their outputs are structured events on the operator's observability channel, where
monitors and dashboards surface problems.

The philosophy is layered: **detect**, **measure**, **heal**, **reconcile**, **verify**, **reclaim**,
**curate**. Patrol walks the filesystem looking for trouble. Medic fixes what Patrol finds. Judge
independently verifies trust across boundaries. Clerk measures drift between the tenant and the discovery
service. Chaplain reconciles what Clerk surfaces. Reaper manages the lifecycle of abandoned accounts. Medic also
keeps each tenant's content caches faithful and tidy. Tailor brings self-hosted sites up to spec.

Judge, Clerk, Chaplain, Reaper and Medic's cache upkeep are hosted-only: they run inside the hosted
service. Patrol and Medic's heal run there too. The operator actors' code, and the operator's standalone
`patrol` and `medic` commands, are part of the hosted service and are not published; the checks Patrol and
Judge share with `polis validate` are, in `cli-go/pkg/sitecheck`. Tailor is the tool built for self-hosters:
a standalone binary, run by hand. ⚠️ The name *Rosie* also belongs to a **user agent** that decides comment requests under a
tenant's own grant. That Rosie is not an operator actor; see [*Rosie*](#rosie).

Another way to read the roster is by *which protocol each actor keeps*. **Patrol, Medic, and Tailor
enforce the CLI protocol**: the on-disk shape a polis site must have (file layouts, salts, bundles,
policies, derived indexes). **Clerk and Chaplain enforce the DS protocol**: the state a tenant and the
discovery service must agree on. **Reaper enforces the operator protocol**: the lifecycle rules the hosting
operator imposes (reaping abandoned accounts).
**Judge is orthogonal**: it enforces no single protocol but independently verifies trust *across* all of
them. This is just a lens for the reader. It doesn't change what any actor does.

---

## The concepts above the roster

⭐ **The roster below answers *"what does each actor do?"* This section answers the questions that come
before it**, the ones an operator who is not polis.pub has to answer for themselves. **It is additive:
nothing in the roster changes because of it.**

⚠️ **For custody, grants, the operator settings, and what a self-hoster does and does not get, see
[the operator's guide](../../ops/admin/operator-guide.md).** What follows is only the part that is about
the *actors* specifically.

### 1. Five lanes, plus one agent — and the sixth is not a lane

**The lanes are domains of STATE.** They answer *"which part of the world is this actor responsible
for?"*

```
        PROTOCOL      SOCIAL      CRYPTO     LIFECYCLE      ← lanes (domains of state)
        Patrol        Clerk       Judge      Reaper         ← measure
        Medic         Chaplain    (—)        (—)            ← repair
```

| Lane | Actors | What it holds |
|---|---|---|
| **Protocol state** | Patrol · Medic | structure · bundle declarations · index · policies parse · migrations |
| **Social network state** | Clerk · Chaplain | parity between what a site holds and what the network was told |
| **Crypto validity** | Judge | signature verification · attestation validity · key continuity · tamper detection |
| **Lifecycle** | Reaper | reminders · reap · archive. ⭐ **The only actor that destroys, and its isolation is a feature** |
| ⚠️ **not a lane** | **Rosie** (user agent) | **acts in the tenant's name, under the tenant's grant — see below** |

⚠️ **Two known strains in the model, named rather than smoothed over.** Key-permission, key-leak and
directory-exposure checks, `chmod` and quarantine are **security posture** (*is this site safely
stored?*), not protocol conformance (*is it correctly formed?*): a key with world-readable permissions
violates no protocol. They stay in Patrol and Medic because they are the same local filesystem walk, and
they have their own heading in [Patrol's checks](#what-it-checks) so they are not read as protocol state.
And Judge's policy- and well-known-snapshot checks are
**change detection**, which is adjacent to validity rather than the same thing. **Both live where they
live for good reasons; neither is a clean fit, and pretending otherwise would make the model less
useful.**

⭐ **Index consistency is Judge's on purpose, although the table files "index" under protocol state.** It
is part of what Judge's signed integrity records attest (a record's `checks` names it when it fails), so
moving it to Patrol would take it out of the record. The predicate is shared: Judge and `polis validate`
run the same code.

⭐ **Rosie is not a peer of the other five.** They are domains of state; **she is a boundary of
authority.** Rosie is the tenant's **user agent**: she decides comment requests with the tenant's own
key, under the tenant's own grant, marks every act inside the signed bytes, and has no registry entry,
key or site of her own. That is exactly what a housekeeper is: not a domain expert, but the one trusted
to act inside your house.

⚠️ **The cache upkeep that used to share her name is Medic's.** It signs nothing, so it was never hers
to do. It is a third strain in the lane model: it reconciles against the discovery service and fetches
from other authors, which is social-lane-shaped work, inside a protocol-lane actor. It lives there
because it is keyless; see [*Medic*](#cache-upkeep).

⛔ **Two things the lane model does not cover, and saying so is the point:**

- **Serving has no lane.** *Does the handler actually serve what is on disk?* is not crypto and not quite
  protocol. ⚠️ **That gap is part of why a `robots.txt` generated correctly for weeks, with every unit test
  green, was refused by the handler and nobody noticed.** Judge's check 11 covers it now; the lane
  question is still open.
- **Custody and delegation have no lane.** Who may act with a tenant's key, and whether they stayed inside
  what was permitted, is state someone must be responsible for. **Rosie is the actor governed by it; she
  cannot also be its auditor.**

### 2. The authority rule — which actors may act on what

> ⭐ **An actor may perform a USER-AUTHORITY action only under a tenant's grant. OPERATOR-AUTHORITY
> actions — observing, and healing state the operator itself derived — need none.**

**Plus two narrower rules that are not about authority at all, and are easy to mistake for it:**

- ⛔ **Reaper excludes actors because they are not accounts.** A **category** rule, not an authority one:
  account lifecycle simply does not apply to a system actor.
- ⛔ **Judge excludes itself because self-audit is not audit.** Judge checking another actor is fine; Judge
  checking Judge proves nothing.

⚠️ **The blunt version — *"actors are excluded from everything"* — is the more dangerous mistake.** A
fleet where the actors are the only unmonitored sites is exactly where a problem hides longest.

⛔ **And these rules are the control — a directory location is not.** **Reaper finds its work by querying
the database for unverified accounts, so it cannot reach a system actor wherever that actor's files live;
Patrol, Medic and Judge find theirs by walking the tenants directory, so moving an actor's site out of it
silently stops the three that should be watching.** ⭐ **Placing files "somewhere safe" protects against
the one actor that was never a threat and hides them from the three that are meant to notice when
something breaks.** ⚠️ Note that *Judge excludes itself* is **not** a registry lookup either: Judge skips
exactly one site, the handle it is configured as, by handle equality when it lists the sites to sweep.
That is deliberately not derived from the registry, because the operator
lists itself there. Judge reports the skip in its sweep summary. See
[the operator's guide](../../ops/admin/operator-guide.md).

⚠️ **So nothing inside the fleet checks Judge's own site.** Its `.well-known/polis` changes are not
re-baselined or alerted on by any actor. Check it from outside, the way anyone else would:
`polis validate https://judge.polis.pub`.

**Three operator actors sign with tenant private keys.** Chaplain does so at five call sites
(registration, and post, comment, tag and follow repair). Medic does so at one: publishing the DM messages
key when it provisions the epoch-0 keyring. Tailor does the same on a self-hosted site. **They carry no
marker, by design**, because they restore or complete what the tenant already did. ⚠️ **A tenant still
cannot see what these did in their name on the wire.** Making that legible in the published artifact is
design work, and [the operator's guide](../../ops/admin/operator-guide.md) has the shape.

⭐ **Two other paths sign with a tenant's key, and both ARE legible in what they publish.** Rosie the user
agent's comment decisions carry an `agent` and `grant` marker inside the signed bytes. The hosted service's
default-grant pass signs a grant record whose `basis: hosting-terms` records that the tenant did not write
it.

⭐ **What DOES exist now is the audit trail on the operator's side: events for an act that signs name which
identity signed.** Chaplain's events carry `attribution: as-tenant` without exception, because that is
what Chaplain does. Judge's publishing events, the custody declarations and actor-key projection carry
`as-self`. ⚠️ **Medic's one signing act is the exception**: its event for the DM keyring carries no
attribution yet. Before actors had identities of their own there was one possible answer to *"whose key
signed this"*, so nothing recorded it. There are now two, and an event that does not say which is not an
audit trail.

### 2a. Actors have identities of their own — and that NARROWS what they can do

⚠️ **"Actors are getting keys" sounds like an expansion. It is the opposite.** Actors already hold the
strongest key material on the box, every tenant's identity key, and three of them sign with it. An actor
with its own key can speak **as itself** instead of borrowing an identity that can do everything a tenant
can do.

⭐ **An operator that runs actors publishes a signed ACTOR REGISTRY** naming each one's domain, whose
authority it exercises, and the actions the operator **expects** it to perform. Without it anyone can
stand up a lookalike and be cryptographically indistinguishable from the real thing.

⛔ **EXPECTED, NEVER ALLOWED.** It is not an allow-list and nothing enforces it: the operator holds the
keys, so no list can stop anything. What it buys is that deviation is **observable by anyone**, and the
remedy is social. ⚠️ **Do not describe it as a control**, in a dashboard, an incident review or a customer
conversation. An allow-list implies an enforcement that does not exist, which is worse than nothing
because it reads as assurance.

| | |
|---|---|
| Where the registry lives | `content/pub.polis.core/actor/registry.json` on the **operator's** site |
| How anything finds it | `.well-known/polis` → `actor_registry` — ⛔ **by pointer, never a path** |
| Who may write it | the operator, signing with its own site key (`polis actor register`) |
| Where actor keys live | the operator's **secret store**; the key file on disk is a projection written **only when absent** |
| Who reads it | Medic's cache upkeep, the custody declarer and the default-grant pass — **from local disk**, verifying its signature before operating on a site. ⚠️ Judge's self-exclusion and Reaper's exclusion do **not** read it (see §2) |

⛔ **A bootstrap with no registry excludes nothing and MUST SAY SO.** An actor that silently proceeds
because the registry is missing is the same failure as one that never checked.

Full mechanics: [the custody spec](../../signet/spec/custody.md).

### 3. Two channels, two audiences

| Channel | Speaks as | For | Status |
|---|---|---|---|
| `patrol.*` `medic.*` `judge.*` `clerk.*` `chaplain.*` `reaper.*` | **each actor, by name** | **operators** | ✅ **shipped** |
| **actions-in-your-name** | ⭐ **one voice, always** | **the tenant** | 🔧 **design, not built** |

⭐ **Why they are separate, and why only one of them consolidates.** An operator *needs* the actor name: it
is how you find the code, the cadence and the response. **A tenant never does**, and telling them is worse
than useless: eight names, seven of which will never mean anything to them, is how a notification becomes
noise. So the user channel speaks with a single voice for everything done in a tenant's name.

⚠️ **The user channel is ADDITIVE. It is not a rename.** ⛔ **The operator events stay exactly as they
are**: same namespaces, same cadences, same alerts.

⭐ **And the distinction that makes it safe: the record is the obligation; the notification is the
courtesy.** A tenant who silences notifications has declined the courtesy and kept the record.

### 4. Authored versus derived — the rule that decides what may be healed

⭐ **The rule you will reach for most often, and it is one rule with two opposite answers.**

| | Example | May an actor write it? |
|---|---|---|
| **Derived** — recomputable from something else on the site | `did.json` · `robots.txt` · `rsl.xml` · the key-history genesis entry | ✅ **Yes, unasked.** It asserts nothing the site was not already asserting, so a tampered copy is **rebuilt**, not reported |
| **Authored** — a signed statement of someone's intent | `license.json` · `following.json` · the key chain past genesis | ⛔ **Never.** Writing it is the operator asserting the author said something she did not |

**The tell is always: *if this were deleted, could it be rebuilt from something else?***

⚠️ **The consequences run through every actor below**, and they are the ones most likely to look like
bugs: **a failing licence signature is left strictly alone** (regenerating the public surfaces from
possibly-altered terms would launder a *detectable* tamper into a *published* one), and **a key chain is
never rebuilt even when it is wrong** (its later entries were signed by private keys that no longer exist,
so "repair" could only mean deleting the entries that fail, which erases real rotations and turns a
detected tamper into a clean-looking site).

### 5. What a signature actually covers — and where that is written down

Four things verify signatures — **Patrol** (local posts and comments, the licence), **Judge** (checks 1,
6, 8, 9, 10, 13), **Medic** (before it heals anything), and `polis validate` — and all four reconstruct
the **signing base**: the exact bytes a signature covers, which is never simply "the file". A `signature:`
line cannot cover itself, and a comment's `author:` line is written *after* signing, so both are outside
the bytes.

⛔ **They all call one implementation, and it is specified in public.**
[The signing-base spec](../../signet/spec/signing-base.md) states, per type, the exact field list in
order. The version is `pub.polis.signing-base.v1`, and v1 is the format already in production rather than
a new one. **Nothing was re-signed and no verifier stopped accepting anything.**

⚠️ **The consequence is a class of false alarm that no longer exists.** The reconstruction used to scan
the WHOLE DOCUMENT for a `signature:` prefix, so a post whose **body** contained such a line (a post
documenting this format, a quoted frontmatter block, a YAML code fence) was reconstructed wrongly and
reported as a failed signature. An old alert may have that cause; a new one does not.

### Where the rest of it lives

| Question | Document |
|---|---|
| What am I obliged to declare? What can I turn off? What does each setting cost? | [the operator's guide](../../ops/admin/operator-guide.md) |
| How is an actor's registration checked from outside? | [the custody spec](../../signet/spec/custody.md) · [checking an actor](../../signet/recipes/07-check-an-actor.md) |
| How do I check a site's health myself? | [the network-health guide](../../signet/guides/network-health.md) · [verifying content](../../signet/guides/verify-content.md) |

---

## Summary

| Actor | Schedule | Scope | Role |
|-------|----------|-------|------|
| [Patrol](#patrol) | Hourly | Hosted | Filesystem integrity |
| [Medic](#medic) | Hourly; cache upkeep daily | Hosted (cache upkeep: hosted + Tailor) | Auto-remediation, upgrades & cache custodianship |
| [Judge](#judge) | Hourly | Hosted only | Cross-boundary trust verification |
| [Clerk](#clerk) | Daily | Hosted only | State parity measurement |
| [Chaplain](#chaplain) | After Clerk | Hosted only | Cross-boundary reconciliation |
| [Reaper](#reaper) | Daily | Hosted only | Account lifecycle |
| [Rosie](#rosie) | Per tenant sync | Hosted | User agent — not an operator actor |
| [Tailor](#tailor) | Manual | Self-hosted | Site migration & upgrade |

---

## How Actors Interact

```
Patrol  ──detects──▶  Medic  ──heals──▶  (tenant fixed)
  │
  └──detects──▶  (alerts surfaced, human reviews)

Judge  ──verifies──▶  (independent trust audit, no downstream actor)

Clerk  ──detects──▶  Chaplain  ──reconciles──▶  (tenant fixed)

Reaper  ──manages──▶  (account lifecycle, independent of other actors)

Medic  ──curates──▶  (content caches reconciled/verified/GC'd, daily)

Chaplain  ──asks──▶  Medic  ──re-renders──▶  (after blessed.json changes)

Tailor  ≈  Patrol + Medic (+ the cache upkeep's offline GC) for self-hosted sites (manual, CLI-only)
```

No actor writes to another actor's state directory. No actor triggers another actor, with two exceptions:
Clerk triggers Chaplain (they run together, and Chaplain receives Clerk's sweep results), and Chaplain
asks Medic to re-render a site after it changes `blessed.json`, because there is one renderer and it is
Medic's. They are siblings, not a pipeline. Medic's cache upkeep is the periodic **backstop** for the
real-time blessed-comment ingest/evict path in the webapp's sync handlers: it acts only on confirmed
drift (missed events, cursor loss, downtime) and is idempotent. It never duplicates the real-time path, and an unreachable peer is
never an eviction signal (eviction requires a signed revocation).

### The checks are not the operator's alone — `polis validate`

The predicates that matter to an author — signature and hash verification (with key-history resolution),
licence integrity, key permissions, bundle and policy validity, and Judge's key-chain checks — live in
**`cli-go/pkg/sitecheck`**, and `polis validate` runs the same functions. It is not a reimplementation
that agrees with them; it is the same code, so for those checks the three cannot drift. ⚠️ **Not every
Patrol or Judge check is shared**: the snapshot baselines, provisioning and layout checks, and Judge's
loopback serving checks stay inside the actors.

That matters for one reason worth stating plainly: **a check that runs on the operator's machine cannot
answer a question about the operator.** A tenant who wants to know whether her site's signatures hold,
whether her stated terms are the ones she signed, or whether her key is the one being published, can ask
from her own laptop, over public URLs, without asking the host:

```bash
polis validate https://alice.polis.pub
```

⭐ **The URL form is also the only thing in the system that stands OUTSIDE the edge**, so it is the only
place one question can be asked at all: does the public `robots.txt` still say what the author signed?
Every actor fetches over loopback and cannot see an intermediary's injections. See
[*What Judge does NOT check*](#what-judge-does-not-check).

The command **reports and never repairs**. It is deliberately not an actor, has no `--fix`, and carries
none of the custody model actors carry. And it reports what it could NOT check, so a clean result from
outside never overstates its own coverage. See [verifying content](../../signet/guides/verify-content.md)
and [`polis validate`](../../cli/user/command-reference.md#polis-validate).

Self-hosters get the same command, and it is the first verification surface they have that is not Tailor.

---

## Patrol

*The beat cop — walks the filesystem hourly checking locks, permissions, and evidence of tampering.*

| | |
|---|---|
| Schedule | Hourly |
| Runs | Inside the hosted service (its operator's standalone `patrol` command is not published) |
| Code | Runs in the hosted service; not published (the predicates it shares with `polis validate` are in `cli-go/pkg/sitecheck`) |

### What it checks

**Identity & keys:**
- `.well-known/polis` exists and contains `public_key`
- Public key file readable and matches well-known
- Private key file valid (parsed and discarded immediately — never logged)

**Security posture** (*is this site safely stored*, not *is it correctly formed*). None of these is a
protocol rule, and a site that fails one still verifies. Each fails the check:
- **Key permissions**: the private key is `0600` (or `0400`). Medic `chmod`s it back
- **Key leak**: no private key in a public-facing directory. ⛔ **Medic does not touch it**: the key is
  exposed, and the fix is a rotation only the tenant can make
- **Directory exposure**: `.polis/` and `.git/` are not world-accessible. Medic `chmod`s them to `0700`

**Content integrity:**
- Every post under `content/pub.polis.core/post/` has a valid Ed25519 signature, and every comment under
  `content/pub.polis.core/comment/` its own, reconstructed with the **specified**
  [signing base](../../signet/spec/signing-base.md). That is why posts and comments are verified
  differently rather than identically. ⭐ **Current key first, then the key the tenant's published history
  resolves for the artifact's claimed signing time**, so after a rotation earlier posts keep verifying
  instead of arriving at Medic as non-correctable failures. Passes through a retired key are listed in the
  result.
- Every post content hash matches the frontmatter hash
- **Foreign content**: a post or comment on disk whose frontmatter `author` is not this tenant's own domain
  is alerted, and Medic quarantines it, with its rendered page, on its next sweep. Another author's words
  belong only in the isolated comment cache, never in this site's public tree.
- **Zeroed signature envelopes**: a signature whose embedded public key was zeroed by an old signer (the
  signature itself is valid against the author's real key) is flagged; Medic repairs it without a key.
- **Licence integrity**: when a tenant has stated terms, the `license` pointer in `.well-known/polis`
  resolves, `license.json` parses, its values come from the standard vocabularies, and its signature
  verifies against the tenant's own key. A failing signature is HIGH: it means the terms an author
  published no longer match what she signed. **A tenant who has stated nothing reports OK**: absent means
  unstated, not defective, and most tenants have said nothing.
- **Licence projection**: when a tenant has stated terms, the pages those terms point at are actually
  published: the terms page at the licence type's mount and the permanent explanation of the profile
  version. The finding is a **dead terms URL inside a signed, machine-readable licence surface**.
  `robots.txt` and `rsl.xml` advertise it and Medic repairs those two, so the reliable files end up lending
  credibility to a broken link. **A tenant who has stated nothing reports OK**, and so does one that has
  withdrawn its terms.

**Structure & provisioning:**
- Public and private policy files present
- Storage salt present (retained as a tamper canary; the legacy seed-derived DM re-seal that used it is
  retired)
- DM directories present with correct permissions
- **Epoch-0 DM keyring present**: `keyring.json` exists, has at least the bootstrap epoch, and is at the
  current schema version (the primary deploy-upgrade detector; Medic provisions it if missing)
- `bundle.json` present and valid, and its declarations match the core bundle's defaults; `index.jsonl`,
  `blessed.json` and `following.json` parse
- **Legacy DM layout**: the retired `conv/` directory and `conversations.json` file are reported; Medic
  quarantines them
- Avatar and favicon present

**Snapshot-based detection** (baselines stored outside tenant directories):
- Salt integrity: content hash change = HIGH alert (tamper), mtime-only change = MEDIUM
- File mtime tracking on private key, public key, well-known and policies. Only the two key files
  raise an alert (private HIGH, public MEDIUM); the rest are recorded at LOW and never emitted, because
  Judge watches well-known and the policies by content. The salt is skipped, having its own check above
- Unauthorized file creation in `.polis/keys/` (HIGH), `.polis/policies/` (MEDIUM), and
  `.polis/webapp/hooks/` (HIGH: a new hook script is a code-execution tripwire; hooks never run on the
  hosted platform, so a new one has no sanctioned source)

**Container allowlists** (static, no baseline; the legitimate set is known each sweep):
- Foreign bundles: only `pub.polis.core` is supported; any other directory under `.polis/bundles/` or any
  non-core `installed_bundles` entry in `registry.json` is flagged (HIGH, flag-only)
- Rogue or stale discovery-service state: any `.polis/ds/<domain>/` whose domain isn't the operator's
  expected DS (from the hosted configuration, **not** the tenant's `.env`) is flagged as a warning, because
  it is ambiguous between a rogue DS and a stale remnant of a discovery service the site used before.
  Skipped when no expected domain is configured (self-hosted)

**Infrastructure:**
- Volume mount writable with adequate free space
- Stale archives (90+ days)
- Root-level SSH/shell artifacts (`.ssh/`, shell rc and profile files, shell and REPL histories)
- Policy validation: every rule parses, and no rule duplicates or contradicts another

**Special:** monitors the discover.polis.pub quip supply and warns when unpublished quips drop below a
threshold.

### What it does NOT do

- **It repairs nothing.** Detection only; Medic heals what is safe to heal, and everything else goes to a
  person.
- **It never logs key content**, only file paths and pass/fail results.
- **It makes no network calls.** Everything it checks is on the local filesystem.
- **It does not check registration parity with the discovery service.** That moved to Clerk and Chaplain:
  registration is a cross-boundary consistency issue, not a filesystem health check.
- **It does not quarantine foreign content itself.** A post authored by another domain is alerted, and
  Medic moves it out on its next sweep.
- **It does not treat a missing bundle declaration as a failure.** After a release declares a new content
  type, every tenant lacks it until Medic merges it in, and Patrol reports that as an upgrade, not as an
  integrity failure. A declaration that is present and **disagrees** with the default is reported and
  deliberately not auto-repaired, because overwriting it would clobber a legitimate tenant edit.

### Key design decisions

- Snapshot baselines live **outside** tenant directories, so tenants cannot tamper with their own
  baselines.
- Private keys are parsed only to prove they are valid; the parsed key is discarded immediately.
- The first sweep for any tenant establishes baselines (no alerts). Subsequent sweeps compare against
  baselines.
- Upgrade findings are on a separate channel from integrity failures, so a fleet-wide provisioning gap
  after a deploy is recognisable for what it is, and a genuinely missing declaration in steady state is
  still visible.

### Known gaps

- No timestamp plausibility check (future-dated or backdated content)
- No tombstone tracking (indexed content removed without signaling)
- Container allowlists cover `.polis/bundles/` and `.polis/ds/` but **not** the `.polis/` top level or the
  per-content-type directories under `pub.polis.core/`. Those have real false-positive sources
  (`.polis/quips.txt` on the discover tenant, `.polis/tailor-backup/`, the blessed `cache/`) that need a
  curated allowlist; deferred.
- No actor garbage-collects a stale `.polis/ds/<old-domain>/` left behind when a site changed discovery
  service, so the foreign-DS check stays a warning (stale vs. rogue is ambiguous) rather than a hard fail.

---

## Medic

*The field medic — silently heals what Patrol finds and upgrades tenants to current spec without waking
anyone.*

| | |
|---|---|
| Schedule | Hourly; its [cache upkeep](#cache-upkeep) daily |
| Runs | Inside the hosted service (always applying; its operator's standalone `medic` command, which defaults to a dry run, is not published). The cache upkeep runs only in the hosted service |
| Code | Runs in the hosted service; not published |

### What it does

**Permission fixes:**
- `chmod 0600` on private keys with wrong permissions
- `chmod 0700` on `.polis/` and `.git/` when they are world-accessible (directory exposure)
- `chmod 0700` on DM directories with wrong permissions

⚠️ **The first two are security-posture repairs, not protocol repairs**, the same split as
[Patrol's *Security posture*](#what-it-checks). ⛔ **The third posture finding, a key leak, is never
repaired**: moving the file does not un-expose the key.

**Quarantine:**
- Moves suspicious files (executables, symlinks, oversized, suspicious extensions) to a quarantine
  directory outside the site, with a manifest recording what was moved and why

**Provisioning (transparent upgrades):**
- Creates missing public policy files (`policies/rules.jsonl`)
- Creates missing private policy files (`.polis/policies/rules.jsonl`)
- Appends missing DM policy rules to existing private policies
- Creates missing storage salt (`.polis/storage-salt`)
- Creates missing DM directories with 0700 permissions
- **Provisions missing epoch-0 DM keys**: creates `keyring.json` with a fresh bootstrap epoch and publishes
  the signed `public_key_messages` block into `.well-known/polis`. Epoch 0 is a server-held random keypair
  (no password), so this runs **without the user**. Gated on the legacy-content-path migration completing
  first, to avoid racing the rename of the old `dm/` subtree.
- Creates missing `bundle.json`, and merges in content-type declarations a release has added
- **Rebuilds `robots.txt` / `rsl.xml`** from the tenant's signed licence whenever they are **missing _or no
  longer say what that licence says_**. Content comparison, not a presence check: a projection edited to
  contradict the signed terms is repaired exactly like a deleted one. **`robots.txt` is what crawlers
  actually read**, so a file edited to `train-ai=y` while the licence says `n` would otherwise contradict
  the source indefinitely on a site that has not published in months. Medic and the renderer generate
  these files through **one shared function**, so a correct file is left completely alone: no rewrite, no
  mtime churn, no event.
- **Brings `index.jsonl` back in line** when its tag or attestation entries do not match the records on
  disk, with the same partial rebuild `polis rebuild --tags --attestations` runs. Gated on the entries, not
  the file bytes, so it never re-sorts a tenant's posts.
- **Publishes and heals `.well-known/did.json`**: the tenant's own public key, re-encoded as a W3C
  `did:web` DID Document, together with every key the tenant has retired. Written for **every** tenant,
  unasked; rebuilt whenever it is missing, stale after a key rotation, or edited. Needs the operator's base
  domain; a sweep without one publishes nothing rather than guessing a host.
- **Provisions the `public_key_history` genesis entry**: one entry naming the key the tenant already
  publishes, dated from the tenant's own `created`, with a `null` transition signature because nothing
  preceded it. ⛔ **Behind a guard, and PROVISION-ONLY — see below.**
- Generates missing avatars and favicons
- Creates missing theme directories

> **The key history is two different artifacts wearing one name, and Medic treats them differently.**
>
> ⭐ **The genesis entry is derived**, like `did.json`: it restates the tenant's own `public_key` and
> `created` as a chain and asserts nothing the identity document was not already asserting. So Medic
> writes it, unasked, for everyone, by the same reasoning that lets it publish a DID document.
>
> ⛔ **But it assumes the current key is the FIRST key, and that is not provable from the site alone.** A
> tenant that rotated before key history existed kept no local record of having done so. So before
> writing, Medic asks the discovery service what it witnessed, and **writes nothing for a domain the DS
> has seen hold more than one key**. It reports that site for a person to decide instead. Writing there
> would name the wrong key as first and erase real rotations, in a file whose entire purpose is to be a
> durable record.
>
> ⭐ **The DS is consulted for CONTRADICTION, never for permission.** It cannot authorise a genesis entry;
> it can only stop a false one. A chain the DS could veto would put identity permanence back inside a
> service, which is the thing publishing the chain exists to end. An unreachable DS therefore means *not
> now*: Medic writes nothing and retries next sweep.
>
> ⛔ **A chain that already exists is NEVER rebuilt, even when it is wrong.** Its later entries carry
> transition signatures made by private keys that no longer exist, so there is no key on the machine that
> could reproduce them. "Repair" could only mean deleting the entries that fail, which erases real
> rotations and turns a detected tamper into a clean-looking site. Judge reports it; nobody heals it.

> **Medic never writes `license.json`.** The distinction is authored versus derived, and it is the same
> line that separates `following.json` from `favicon.svg`. `license.json` is a *signed statement of the
> author's intent*: creating or repairing one would be the operator asserting that the author said
> something she did not. `robots.txt` and `rsl.xml` are *projections* of that signed file, fully
> determined by it, so rebuilding them is the operator restoring its own derived state.
>
> A licence that **fails verification** is left strictly alone too: Medic cannot tell an author's edit
> from an attacker's, and regenerating projections from possibly-altered terms would launder the
> alteration into the machine surfaces, turning a *detectable* tamper into a *published* one. Patrol
> reports it; nobody heals it silently.

> **`did.json` is the opposite case, from the same rule.** Medic writes it for every tenant without
> asking, because it **asserts nothing new**: it re-encodes the `public_key` the tenant already publishes
> in `.well-known/polis` into the shape a DID resolver reads. That is why a tampered or deleted DID
> Document can simply be **rebuilt** rather than reported: the correct bytes are recomputable from the
> tenant's own key and host, so no stored baseline is involved and nothing an attacker writes survives a
> sweep.
>
> Authored versus derived is **one rule with two opposite answers**, not an inconsistency: never author
> `license.json`, always rebuild `did.json`.

> **DM heal is non-destructive — structure only.** Medic only *creates* missing DM structure (the
> keyring's bootstrap epoch, directories, policy rules) and never deletes, rewrites, or re-seals message
> data. It never touches a keyring's **password epochs** or their wrapped keys: it cannot read them and
> must not disturb them. Removing legacy or abandoned message data is a deliberate **operator-owned manual
> step**, never an automatic Medic or Patrol action. The storage salt is kept as a tamper canary; it is no
> longer a key-derivation input.

**Keyless repairs and quarantine of retired layout:**
- **Repairs zeroed signature envelopes** Patrol flags: splices the tenant's published public key into the
  envelope, leaving the signature bytes identical, so nothing is re-signed
- Quarantines retired DM layout files (`conv/`, `conversations.json`) and orphaned theme directories

**Content fixes:**
- Removes deprecated/stale files (scoped feed caches, registration flags, old field names)
- Upgrades policy files to the current format
- Re-renders tenant sites if theme files were cleaned up or the active shape was upgraded, and when Chaplain
  asks after changing `blessed.json`

### What it does NOT do

- **It never writes `license.json`**, and never heals a licence whose signature fails (see above).
- **It never renders the licence pages.** A tenant whose terms page was never published is Patrol's
  finding and needs a deliberate re-render; putting the render pipeline inside an hourly sweep would buy
  an enormous surface for a problem the renderer fix removes.
- **It never rebuilds an existing key chain**, and never writes a genesis entry for a site the discovery
  service has seen hold more than one key.
- **It never deletes, rewrites or re-seals DM message data**, and never touches password epochs.
- **It never writes, heals or re-issues a user-agent grant.** Nothing regenerates a site's generated
  `agents.json` except a grant or withdrawal write.
- **It does not repair a bundle declaration that disagrees with the default**; that may be a legitimate
  tenant edit.
- **It no longer registers tenants with the discovery service.** Registration repair moved to
  Clerk/Chaplain, where cross-boundary consistency belongs.
- **It never forces a fix it isn't confident about.** Non-correctable issues are counted and skipped.

### Key design decisions

- **Safe, reversible fixes only.** Quarantined files are preserved with a manifest for investigation.
- The hosted service always runs Medic in apply mode. The operator's standalone command defaults to a dry
  run for safety.
- **Medic is the only actor that renders without being asked**, and it has the only renderer. It
  re-renders an entire site after removing stale theme files or upgrading the active shape, and when
  Chaplain asks after changing `blessed.json`. Rendering signs nothing, so it is Medic's work.
- The hourly heal's only network call is the key-history guard: asking the discovery service how many
  keys it has witnessed, for a tenant that publishes no chain. The daily cache upkeep is network work
  throughout.

### Known gaps

- **Its one signing act is not yet attributed.** Publishing the DM messages key signs with the tenant's
  key, and the event recording it does not yet say which identity signed (see §2).
- **A tenant cannot see on the wire what Medic signed in their name.** That is true of every operator
  actor that signs with a tenant key; see §2.

### Cache upkeep

*Keeps each tenant's content caches faithful and tidy.*

| |
|---|---|
| Schedule | Daily, and once on start — its own cadence, not Medic's hourly one |
| Runs | Inside the hosted service; self-hosters get the offline slice through Tailor |
| Code | The sweep runs in the hosted service and is not published; it works over `cli-go/pkg/cache` (the kind-agnostic cache contract), which is |

#### Why it exists, and why it is Medic's

No actor verified cached-**content** completeness or consistency before this sweep. Clerk and Chaplain
reconcile the `blessed.json` *index*; Patrol checks keys, policies and posts; Judge verifies trust. The
blessed-comment cache is the first cache that is **load-bearing for correctness**: a missing entry
silently drops a blessed comment, and a stale entry shows withdrawn content. As polis caches
more foreign content for durability, cache consistency becomes a first-class operational concern, and
this sweep sets the pattern.

It runs under Medic because it **signs nothing**: it is keyless operator work, and Medic is the keyless
healer. Chaplain signs with the tenant's key for almost everything it does, and says so on every event;
unsigned work does not belong under that statement. The sweep keeps its own daily cadence, because its
integrity pass re-fetches every cached entry from its author.

#### What it does

Per tenant, for each registered cache **kind** (today: `blessed`, blessed comments authored by another
tenant), generically over the `cli-go/pkg/cache` descriptor contract:

- **Reconcile** desired-vs-present. The desired set is `blessed.json` ∩ what the discovery service
  currently reports as granted. Re-fetch and verify entries that are missing; evict entries whose blessing
  is withdrawn or denied; keep the rest.
- **Verify integrity** of each cached entry by re-dereferencing the author's canonical artifact and
  verifying it against the author's published key: the current key first, then the retired key the
  author's published key history resolves for the artifact's signing time. Only a signature that verifies
  under neither raises a HIGH integrity alert (possible tamper or man-in-the-middle). An unreachable author
  is never a failure.
- **Garbage-collect** structural orphans (a cached body with no readable provenance sidecar) and stale
  per-scope feed caches.

**Boundary (critical):** the webapp sync handlers do the **real-time**, event-driven ingest and evict;
the upkeep is the **periodic backstop** for drift (missed events, cursor loss, purged events, downtime).
It acts only on confirmed drift and is **idempotent**. Both paths share one code path (`cache.Ingest` /
`cache.Evict`), so they cannot drift apart.

**Durability, not eviction.** An unreachable author is **evidence for durability, never eviction**: a
desired entry whose origin is down is kept (it stays missing if not yet cached, but is never dropped).
Eviction happens ONLY when the DS positively reports the blessing withdrawn or denied. If the
authoritative desired set can't be computed (DS down, or zero grants reported against a non-empty
`blessed.json`, treated as a query anomaly), the upkeep does **nothing** for that tenant.

**Migration mode.** A one-time data migration can reconcile a tenant with relocation enabled: when an
author is unreachable and no cache entry exists yet, it builds the entry from the tenant's pre-existing
local cross-tenant copy (so the entry is not lost) before that copy is quarantined. The daily actor never
does this. Fetches are throttled so a rebuild doesn't overload peers or trip their rate limits.

#### What it does NOT do

- **It never evicts on uncertainty**: not on an unreachable origin, not when the desired set cannot be
  computed. Eviction needs a positive signal from the DS.
- **It never relocates from local copies** in its daily sweep; that is migration only.
- **It never operates on an actor site, although the rest of Medic does.** The caches hold what a tenant
  blessed, and an actor is not a tenant. It reads the operator's signed actor registry to know which sites
  to skip, reports what it found and trusted on every sweep, and if the registry cannot be trusted it
  excludes nothing and says so, rather than proceeding silently.
- **It never makes comment decisions** and signs nothing. Comment decisions are Rosie's, under the
  tenant's grant.
- **It does not special-case a cache type** (see below).

#### Key design decisions

- **Kind-agnostic contract.** The upkeep never special-cases a cache type. Adopting avatar, reply-context
  or feed caches later means *registering a descriptor*, not editing the sweep.
- **Anti-drift by construction.** The real-time sync path and the upkeep's reconcile both go through the same
  generic ingest and evict operations, with a test asserting they converge.
- **Stateless.** The target is recomputed each sweep; there is no baseline to corrupt or migrate.
- **Fail-open, loudly, on the registry.** Without a trusted registry nothing is excluded, which is exactly
  what happened before actor sites existed. What it may not be is silent. ⛔ **An untrusted registry is
  never made authoritative**: entries make sites invisible to the upkeep, so trusting an unverified file would
  let anyone who can write it hide a real tenant's site.
- **Reaper interaction (conservative default):** a reaped author's comments that are blessed elsewhere
  **persist** in others' caches, consistent with the time-capsule goal. There is no Reaper→cache eviction.

#### Known gaps

- **Key rotation versus compromise.** A rotated author's older comments keep verifying through the
  author's published key history. What is **not built**: telling a rotation apart from a compromise, and
  what to do about a dead author's content.
- **Self-hosted sites have no periodic backstop** (see below).

#### Self-hosted

Self-hosters get the **offline** slice via `tailor --apply` (the `blessed-cache-gc` check): orphan
blessed-cache bodies are removed. A self-hosted webapp's own sync does the **real-time** ingest and evict,
verifying each comment as it is cached, through the same `cache.Ingest` / `cache.Evict`. ⚠️ **The periodic
backstop — daily reconcile and integrity re-verification — runs only in the hosted service;** a
self-hosted site has no equivalent.

---

## Judge

*The magistrate — independently verifies that trust claims hold across boundaries, from signatures to
attestations.*

| | |
|---|---|
| Schedule | Hourly |
| Runs | Inside the hosted service only |
| Code | Runs in the hosted service; not published (the predicates it shares with `polis validate` are in `cli-go/pkg/sitecheck`) |
| Publishes | Signed [integrity observations](../../signet/spec/attestation.md#41-integrity-is-an-observation-and-its-result-lives-in-the-payload) on Judge's own site, **only when a site changes health**, and only when the operator switches publishing on |

### The checks

| # | Check | I/O | Purpose |
|---|-------|-----|---------|
| 1 | **HTTP content verification** | Local loopback | Fetches posts via localhost HTTP, verifies signatures + hashes match disk — tests the full serving pipeline. Signing base: [the signing-base spec](../../signet/spec/signing-base.md) §4. ⭐ **Current key first, then the key the tenant's own published history resolves for the artifact's claimed signing time**, so a tenant's rotation does not report every earlier post as tampering. A retired-key pass is **named** in the check message (*"n/m content items verified against a RETIRED key"*), never folded into a silent OK |
| 2 | **DS attestation audit** | HTTP to DS | **Registration service attestation**: for each discovery-service registration on disk, fetches the DS's site-check, rebuilds the register canonical, fetches the DS key **by `attestation_key_id`** and verifies the signature. ⛔ **A signature that does not verify fails the check.** ⚠️ **A DS or key that cannot be reached is `NOT VERIFIED`, not a failure**: an unreachable DS says nothing about the attestation. A missing attestation is backfilled from the DS **only when the DS's copy verifies**. ⚠️ **Blessed comments are COUNTED here, not verified, and the message says so** (*"n blessed comments counted, NOT verified"*). ⭐ **Nothing on disk can be verified:** `blessed.json` records a comment's url, version and blessed_at, never the DS's blessing attestation, which rides only a stream event, and the stream is retention-pruned, so a sweep over it could never cover every blessing either. `blessed.json`'s own signature is check 10 |
| 3 | **Public key continuity** | Local FS | Tracks key fingerprints over time; alerts on unexpected changes (proto-TOFU) |
| 4 | **Policy snapshot verification** | Local FS | Hashes the tenant's policy files and alerts once on any change. It compares hashes and knows nothing about blessings |
| 5 | **Index consistency** | Local FS | Verifies `index.jsonl` entries match real signed files — detects orphans, phantoms, hash mismatches — for **every entry type: post, comment, tag, attestation**. Each type is hashed by its own rule (a markdown body hash, or the canonical signing JSON), declared in one table. A clean result **names what it checked** (*"34 entries checked (attestation 2, comment 23, post 8, tag 1)"*); an entry of a type with no rule is counted as **NOT checked**. Same predicate as `polis validate <dir>` |
| 6 | **Cross-site comment verification** | External HTTP | Verifies every comment file in the tenant's own comment directory against the key of the author it names. ⚠️ **Those are the tenant's own comments**: a commenter publishes on their own site, and another author's copy there is quarantined. So the author is normally the tenant itself; the name predates the comment cache. Key order: ⭐ **current key first, then the key the author's published history resolves for the comment's claimed signing time**, read from the same `.well-known/polis` fetch. A retired-key pass is named in the message. A comment whose author's key could not be read is **disclosed** (*"n of m signed comments verified, k could not be checked"*), never counted as checked and never a failure. ⚠️ Uses the **comment** signing base, which excludes the `author:` line written after signing; applying the post base here reports every valid comment as invalid, which is exactly how the blessed-comment cache once starved |
| 7 | **Well-known snapshot verification** | Local FS | Hashes `.well-known/polis` and alerts on tampering with identity metadata (handle, base_url, display_name, default_theme, etc.); complements check 3, which covers `public_key` specifically |
| 8 | **Messages-key signature verification** | Local FS | Verifies every `public_key_messages` entry carries a valid identity-key signature, detecting a forged or unsigned DM messages key (an operator or man-in-the-middle key swap). Stateless (no baseline): verifies against the current `public_key`, so an identity rotation that re-signs the block passes with no re-baseline; an absent block (pre-DM-encryption or mid-upgrade) is not an alarm |
| 9 | **Follow-file signature verification** | Local FS | Verifies `content/pub.polis.core/follow/following.json` against the identity key published in `.well-known/polis`. The follow graph is what the trust layer weights by, so an unsigned-and-editable roster is a hole under every attestation. Stateless, like check 8. ⚠️ **An unsigned follow file reports OK**: signing happens on the tenant's next follow or unfollow, so unsigned is the expected majority state and is a fact, not a defect. Only a *present-and-failing* signature is a finding |
| 10 | **Blessing-list signature verification** | Local FS | Verifies `content/pub.polis.core/comment/blessed.json` against the identity key published in `.well-known/polis`. The blessing list is what a site says it has admitted onto its own pages, so an unsigned-and-editable list is a way to put words on someone's site under their name. Stateless, like checks 8 and 9. ⚠️ **An unsigned blessing list reports OK**: signing happens on the tenant's next bless or unbless, so unsigned is the expected majority state for a long while and is a fact, not a defect. Only a *present-and-failing* signature is a finding |
| 11 | **Served-artifact verification** | Local loopback | Fetches `.well-known/did.json`, `.well-known/polis`, `robots.txt`, `rsl.xml` and the licence type's mount over loopback and compares each to the file it must have come from. Writing a file and serving a file are different things that can disagree: `robots.txt` was generated correctly for weeks while the handler refused it, every unit test green throughout. ⚠️ **Absence means different things for different artifacts.** `did.json` and `.well-known/polis` are DERIVED and provisioned for everyone, so absent is a finding; the licence surfaces are AUTHORED-CONDITIONAL and exist only for a tenant who stated terms, so absent is the normal state and only a disk/serve disagreement is a finding. ⛔ **Loopback only**: see [*What Judge does NOT check*](#what-judge-does-not-check) |
| 12 | **Licence origin** | Local FS | Compares the host in the signed licence's `terms` URL against the host this tenant is served at. `robots.txt`'s `Sitemap:` line and `rsl.xml`'s `License:` URLs are built from that address, so a site that has moved origin keeps publishing URLs pointing at an origin it may no longer control. **Report only, and it must stay that way**: `license.json` is authored and signed, and no actor may restate an author's terms |
| 13 | **Key-chain validity** | Local FS | Walks the tenant's published `public_key_history`: every `transition_sig` must verify against the key it succeeded, back to genesis, plus the structural rules (contiguous epochs, no gap or overlap between a key's `valid_until` and its successor's `valid_from`, genesis carries a null signature, `current` carries no end). ⭐ **Pure crypto over published bytes: the predicate lives in `pkg/sitecheck`, so `polis validate` runs the identical check** and a self-hoster is not left with less. ⚠️ **A tenant with no chain reports OK**: absence is honest, most sites had none before it shipped, and Medic provisions it. ⛔ **Never repaired** |
| 14 | **Key-head agreement** | Local FS | The three places a site states its current key must be the same key: the head of `public_key_history`, `public_key`, and the key `did.json`'s `assertionMethod` points at. Follows the `assertionMethod` **pointer** rather than taking `verificationMethod[0]`: since the DID document carries retired keys, those are no longer the same entry. ⛔ **Report only, and this one is a judgment no actor may make**: picking a winner decides which key is a person's identity |
| 15 | **Unrecognised signed fields** | Local FS | Parses every signed JSON artifact on the tenant and reports any member this build does not declare. Verifiers are tolerant: a file that fails and carries an unrecognised member reads `unknown`, never `invalid`; one that verifies reads `valid`, with the member flagged. So a tamperer can add a junk member and quiet an alarm; this check is the mitigation. ⭐ **It is a finding only because these are hosted tenants**, whose signed files were all written by this same build; the same observation about a stranger's site is not a finding. ⛔ **Report only**: the member sits inside bytes the author signed |

⭐ **Plus one global probe per sweep, before the per-tenant checks:** a TLS dial to each hostname the
operator configures (the apex, the discovery service and the tenant wildcard among them), reporting a
certificate expiring within 14 days, an expired certificate, or a failed handshake. Healthy certificates
report nothing.

### Scheduling and publishing

Judge keeps a small state record per site and works through the fleet **oldest-checked first**. A site
that keeps failing is checked less often (back-off), up to a ceiling, and earns its way back to the normal
rate as it passes again. A sweep that runs out of time stops; the sites it did not reach are first in line
next time, and the sweep reports how many it did not reach. **A growing unreached count is the fleet
outgrowing the hour.** ⭐ **Back-off makes a broken site be checked less often; it never makes it
reported less often.** Every sweep reports every unhealthy site, including those backed off and not
checked that sweep.

When a site's health **changes**, Judge can publish a signed
[integrity observation](../../signet/spec/attestation.md#41-integrity-is-an-observation-and-its-result-lives-in-the-payload)
about it on its own site and register it with the discovery service: **once when the site becomes
unhealthy, once when it recovers.** A healthy fleet publishes nothing. Each record also agrees with
Judge's most recent check of the site. A site that still counts as unhealthy but has started passing
gets nothing, and so does a site that still counts as healthy but has started failing.

The publishing rules are built around one asymmetry: ⭐ **a false halt only delays a record; a false
record is permanent.**

- **Publishing is off unless switched on**, and anything but an explicit "on" publishes nothing while
  checking continues. Switching it off and restarting is the safety cutoff.
- **A site is unhealthy only after at least two consecutive failed checks.** That threshold is also what
  stops a one-sweep glitch, including a Judge bug, from ever being published.
- **Publishing halts when failures look like one common cause**: when more than half of the sites Judge
  considers, and at least three, are unhealthy at once, or when at least three not-yet-reported sites fail
  the same check. While it holds, no *not-verified* observation is published, and when it lifts Judge
  publishes only what is *still* wrong. ⛔ **The halt suspects Judge before the sites.**
- **A rolling budget** caps how many records Judge may publish in a day. A healthy fleet never gets near
  it.
- **Settings are strict**: a set but invalid value stops the server from starting, rather than silently
  running a default the operator thinks they changed.
- **Judge publishes its own status publicly**: an unsigned status document on its own site, rewritten
  every sweep, saying whether publishing is active, muted or halted, and why. A stale timestamp there means
  Judge has stopped sweeping.

### What Judge does NOT check

**Judge fetches over LOOPBACK**, from inside the machine, upstream of any CDN or edge. Everything in check 1
and check 11 is therefore a statement about **the origin**, never about the public wire.

**So Judge cannot see, and will never report:**

- anything an intermediary **adds** to a response (an edge-managed `robots.txt` block is a real example),
- anything an intermediary **removes or rewrites**,
- a CDN serving a stale cached copy of an artifact the origin has since corrected,
- DNS, TLS or edge failures that make a correct origin unreachable (the certificate probe is a separate,
  narrower check).

⚠️ **Read that as a limit on what a green Judge sweep means.** Judge will report `robots.txt` OK, correctly
and forever, while the public file says something else. That is not a bug in the check. It is the boundary
the check was deliberately drawn at, because widening Judge to fetch publicly would give an hourly
per-tenant sweep a second reachability model and a second failure mode (network, DNS, edge outage) inside
it.

**The public surface is a separate question with a separate owner, and it has an answer:
`polis validate <url>`.** It fetches the public `robots.txt` and `rsl.xml` from outside the machine (the
only thing in the system that sees what the world sees) and asks whether they still say what the author
signed. See [`polis validate`](../../cli/user/command-reference.md#polis-validate).

⚠️ **It runs where the tenant runs it, not hourly on the fleet, and that is deliberate.** The edge is not
the operator's to police continuously, and its findings are things a tenant usually cannot change: a
managed `robots.txt` belongs to the host. A per-run answer the author can act on beats an hourly alert
nobody can.

**Judge also repairs nothing.** Every Judge finding is an operator's job, and for several (a follow file,
a blessing list or a licence that no longer verifies, a key chain that does not prove itself, a key head
that disagrees, an unrecognised signed field) the only party who can close it is the tenant, by
re-authoring with their own key. ⛔ **No actor may re-sign, strip, delete or rewrite those artifacts to
clear the finding**: each would either forge authorship or destroy the evidence that anything happened.
⛔ **And the tenant-vs-DS half of key history is not here**: reading both sides of the tenant↔DS ledger is
Clerk's job.

### Key design decisions

- While Patrol is **forensic** (local filesystem health), Judge is **accountability** (system-wide trust).
  They are complementary, not overlapping.
- Judge waits for the HTTP server to be ready before starting checks: it needs a working loopback to
  verify serving correctness.
- Judge keeps a key baseline, a policy snapshot and a well-known snapshot per site, outside tenant
  directories. **Missing state means first run: Judge records the baseline and raises no alert.**
- **Checks 8, 9, 10, 13 and 14 are stateless**: they verify what the tenant publishes right now, against
  itself. No baseline, so a legitimate rotation needs no re-baselining; a key chain is *expected* to grow,
  and growth is the thing being verified rather than the thing being alarmed about.
- **Checks 13 and 14 are `pkg/sitecheck` predicates**, so `polis validate` runs the identical code for a
  self-hoster who has no Judge. ⛔ **The tenant-vs-DS half is deliberately NOT here**: a second
  implementation in Judge would duplicate Clerk's reason to exist.
- External HTTP requests (check 6) use a short timeout. Unreachable domains produce warnings, not
  failures.
- **Unsigned is never invalid.** The follow-file and blessing-list censuses keep signed, unsigned, invalid
  and unknown as four separate numbers, because unsigned is the expected majority until every tenant has
  re-authored the file once, and counting it as invalid would read a healthy fleet as an outage.

### Known gaps

- No timestamp plausibility check (future-dated or backdated content): **designed, not built**
- No tombstone tracking (indexed content removed without signalling): **designed, not built**
- Nothing Judge checks can see the public wire (see [*What Judge does NOT check*](#what-judge-does-not-check)).
- An honest consequence of baselining: **a change made before Judge's first sweep of a site, or a
  well-known change after its one alert, passes without a further finding.**

---

## Clerk

*The auditor — reads both sides of the tenant↔DS ledger and reports discrepancies without touching
either.*

| | |
|---|---|
| Schedule | Daily |
| Runs | Inside the hosted service only (no CLI subcommand, no standalone binary) |
| Code | Runs in the hosted service; not published |

### What it measures

For each hosted tenant and each content type, Clerk compares what the tenant holds to what the discovery
service holds.

⚠️ **Every comparison reads a DURABLE DS source, never a replay of the event stream.** Events age out of
the live stream into an archive, so any check that replayed the stream under-counted every artifact once
it aged past the window: permanent, growing false drift, and a Chaplain repair loop chasing a number that
could never match. It bit follows and then tags before both were fixed. **A new parity check must diff
against durable content metadata or a live-plus-archive query, never against a stream replay.**

| Content type | Local source | DS source (durable) |
|--------------|-------------|----------|
| Follow | Entries in `following.json` | The DS's followed set: net-positive target domains over live **and** archived events |
| Post | Files under `content/pub.polis.core/post/` | Active `pub.polis.post` rows in the DS's content metadata |
| Comment | Authored files under `content/pub.polis.core/comment/` | Active `pub.polis.comment` rows in the DS's content metadata |
| Tag | Each target in each local tag file | Active `pub.polis.tag` rows in the DS's content metadata, one per tag+target pair |
| Blessed index | `blessed.json` | ⚠️ **Local only**: granted blessings against the materialised blessing list. No DS call; runs even when the DS is unreachable |
| Registration | The local registration marker | The DS's registered-site record |
| ⛔ **Key history** | The key sequence in `.well-known/polis` → `public_key_history` | The DS's witnessed key history (`GET /v1/sites/keys/history`), oldest first. **Never pruned** |

It also reports **duplicate DS rows** for one logical artifact, as detection only.

**Drift is directional** for the content types: a local artifact with no DS row is reported; DS surplus
(rows for artifacts disk no longer has) is tolerated.

### ⛔ Key-history parity is the one check that is not a count

⭐ **It is also the only check in polis that can catch a site quietly EDITING its own identity history**,
and the reason the DS is worth consulting at all here.

A published key chain proves it is **internally consistent** (every handover is signed by the key that
held authority) and proves nothing about whether it is **complete**. Two attacks live in that gap, and
neither is visible from the site alone:

| Attack | Why the chain cannot show it |
|---|---|
| **Omission** | The author holds all their own old keys, so they can build a chain that verifies perfectly and quietly skips one they would rather disown, along with everything it signed |
| **Backdating** | The rotation timestamp is inside the signature, but the signer chose it. Nothing in the chain proves *when* |

The DS answers both because it is append-only, third-party, and stamps its own creation time. **You can
rewrite your own history file; you cannot rewrite the DS's.**

**Three rules for anyone touching this check:**

1. ⛔ **It compares the SEQUENCE, not the count.** Every other axis here counts artifacts. Two chains can
   hold the same keys in a different order at the same length and be completely different claims about
   who could sign what, when. Equal lengths do not mean agreement.
2. ⚠️ **It ignores timestamps entirely, and must keep doing so.** The two sides date the same key
   differently on purpose: a site's genesis entry uses its own `created` (when the site was made), the
   DS's genesis row uses the time of registration (when it was told). Comparing them would report
   permanent, unfixable drift on every tenant on the fleet. There is a test pinning this.
3. ⛔ **Nothing reconciles it, and Chaplain has no repair.** The entries that would need writing carry
   signatures made by private keys that no longer exist. This axis is detection only, permanently.

A tenant with **no published chain** is not drift while the DS has witnessed one key or none: that is
"not provisioned yet", and Medic writes it on its next hourly sweep. It **is** reported when the DS has
witnessed more than one, which is the same guard Medic refuses to write past.

### What it does NOT do

- **It writes nothing, anywhere** (see below).
- **It does not measure attestations.** `pub.polis.attestation` records are registered with the DS but have
  no parity axis, deliberately. The cost of the gap is small and bounded: an attestation whose registration
  failed is simply not indexed, the record itself is unaffected, and `polis attest verify` still checks
  it. If the axis is added, it reads a durable source, never a stream replay.
- **It does not report DS surplus** for the content types.
- **It does not compare key-history timestamps.**

### Key design decisions

- **CRITICAL INVARIANT: Clerk is read-only.** It must not write to disk, must not emit DS events, and must
  not call any function that mutates state on either side. Clerk's measurements are the input to
  Chaplain's reconciliation. If Clerk auto-fixed something, it would create a feedback loop that masks the
  drift it is meant to surface.
- Clerk was born from an incident: a publish path silently returned success on a missing registration
  marker for the lifetime of the hosted service. Tenants accumulated drift undetected, and a human noticed
  only by reading the discovery service's events directly. That was an unacceptable failure mode.
- Clerk is intentionally not exposed as a CLI subcommand or standalone binary.
- No discovery-service client means no-op. Clerk has no reason to exist without DS access.

### Known gaps

- Removals are netted for post, comment, follow and tag, but only when the removal **was announced** to
  the DS. A removal that happened locally and was never announced (there is no local tombstone: unfollow
  and unpublish just delete) leaves the DS count above the local one. That is the `DS > local` direction
  Chaplain doesn't repair; closing it needs a local removal log. Netting strictly reduces false drift and
  never makes that direction worse.
- Per-tenant emission-rate anomaly detection needs 14+ days of baseline data, and is not built.

---

## Chaplain

*The parish caretaker — reconciles cross-boundary consistency issues that Clerk surfaces.*

| | |
|---|---|
| Schedule | After each Clerk sweep (daily) |
| Runs | Inside the hosted service only, in Clerk's loop |
| Code | Runs in the hosted service; not published |

### What it reconciles

| Issue | Detection (Clerk) | Repair (Chaplain) |
|-------|-------------------|-------------------|
| Blessed index drift | Granted blessings missing from `blessed.json` | Adds each missing entry (`metadata.AddBlessedComment`), then asks Medic to re-render the tenant |
| Registration marker missing | DS has a registration record but the local marker is absent | Re-registers with the DS, writes the local marker |
| Follow announcement drift | `following.json` has more targets than the DS's durable followed set | Reads the durable followed set (computed over live **and** archived events) and re-announces only the local follows absent from it, never a follow whose announcement merely aged out of the live stream |
| Post registration drift | Post files on disk with no active row in the DS's durable content metadata | Re-registers the missing posts |
| Comment registration drift | Authored comment files on disk with no active row in the DS's durable content metadata | Re-registers the missing authored comments as `pub.polis.comment`, skipping any older than the DS's served window (archived, not missing) |
| Tag registration drift | Tag files on disk whose targets aren't all announced | `tag.SyncTag()`, which idempotently registers every target of every tag file |
| ⛔ **Key-history drift** | The published chain does not match the witnessed one | ⛔ **NOTHING. Deliberately, and permanently.** The entries that would need writing carry signatures made by private keys that no longer exist, so no repair is possible even in principle, and an operator assembling a chain from DS rows would be asserting successions the author never attested from their own site. Detection only |

Chaplain **signs with the tenant's private key** for registration and for post, comment, tag and follow
repair. ⭐ **The bytes it sends are identical to what that tenant's own CLI would have sent**, and every
Chaplain event records `attribution: as-tenant` without exception.

### What it does NOT do

- **It never repairs key-history drift** (see the table).
- **It does not run Clerk.** It takes Clerk's pre-computed results, to avoid duplicating DS queries.
- **It does not repair the `DS > local` direction**, and does not remove duplicate DS rows.
- **It does not re-announce a follow merely because its announcement aged out** of the live stream.
- **It does not flood the discovery service.** Repairs are capped per sweep and stop at the first
  rate-limited response (see below).

### Key design decisions

- **Chaplain pairs with Clerk** like Medic pairs with Patrol. The boundary is clear: Patrol/Medic own
  filesystem health, Clerk/Chaplain own cross-boundary consistency.
- **Network-free core.** Chaplain's core reads local files and updates the blessing list.
  Network operations (DS re-registration) are handled by the hosted wrapper.
- **Runs in Clerk's loop.** No separate lifecycle: Chaplain is called at the end of each Clerk sweep, at
  the same cadence.
- **Asks Medic to re-render after `blessed.json` changes**, so HTML pages reflect the new comments.
  Rendering signs nothing, and there is one renderer; Chaplain no longer keeps its own copy.
- **Bounded blast radius.** At most 50 re-registrations per content type per tenant per sweep, and a
  rate-limited response stops that tenant's repair at once. Genuine drift is normally a handful; needing
  more is the signature of a mis-measurement, and a large real backlog drains over later daily sweeps
  instead of flooding the DS.
- **Every repair is joinable end to end.** Each operation carries one request id, sent to the discovery
  service too, so the operator's record of a repair and the DS's log of it can be matched.
- **Registration marker repair migrated from Medic.** It was architecturally a cross-boundary issue (DS
  state vs local state), and now lives in Clerk/Chaplain where it belongs.

### Known gaps

- **A tenant cannot see on the wire what Chaplain signed in their name.** The operator's events record it;
  the published artifact does not (see §2).
- It inherits Clerk's gaps: unannounced local removals, and no attestation axis.

---

## Reaper

*The estate manager — handles the lifecycle of abandoned accounts with archive-first, destroy-never
principles.*

| | |
|---|---|
| Schedule | Daily, and once on start |
| Runs | Inside the hosted service only |
| Code | No public package; implemented in the hosted service |

### What it does

**Account lifecycle:**
- **Day 7**: sends reminder emails to unverified tenants
- **Day 14**: reaps unverified tenants: archives them, then deletes the site
- Archives include full site data (posts, keys, config) for potential reinstatement

**Reinstatement:**
- During the grace period, the original owner can reclaim their handle via email recovery
- The archive (with keys) is restored to the tenant directory
- Once a **new user claims the same handle**, reinstatement becomes impossible, and Reaper strips
  `.polis/keys/` and `.polis/storage-salt` from the old archive to prevent key leakage

**Cleanup:**
- Cancels pending blessings when reaping
- Cleans expired magic links, widget tokens and sessions from the database
- Cleans stale rate-limiter entries

### What it does NOT do

- **It never reaches a verified account**, and so never reaches a system actor's site: an actor site's
  account is verified. **It does not consult the actor registry**; the exclusion is by category, not by
  location or list.
- **It never simply deletes.** Every reap is archived first.
- **It never strips keys at reap time**; the grace period needs them for reinstatement.
- **It does not evict a reaped author's comments from other sites' caches.** Those persist, for
  durability, and a reap emits no cache tombstone.
- **It never archives a retired `.old` key backup** from a departing user's site.

### Key design decisions

- **Archive-first, destroy-never.** Reaped data is compressed to an archive, never simply deleted.
  Archives persist for audit and legal purposes.
- Key stripping happens at the moment of reclaim, not at reap time: the grace period requires keys to be
  present for reinstatement. It is the only irreversible step.
- Reaper is the only actor that directly depends on the database and the email service.
- **System actors are out of its reach by category, not by location.** Reaper finds its work by querying
  the database for *unverified* accounts, and an actor site's account row is verified, so it cannot reach
  one wherever its files live.

### Known gaps

None are recorded beyond the cache interaction above: there is no mechanism to evict a reaped author's
content from other sites' caches, and that is currently the intended default.

---

## Rosie

*The housekeeper (ref: the Jetsons) — the tenant's own agent.*

⭐ **Rosie is a user agent, not an operator actor.** She approves or turns away comments for a tenant,
inside the tenant's own server sync, under the tenant's own grant, signing with the tenant's key and
marking every act inside the signed bytes. She has no loop, key, site or registry entry of her own. See
[the delegation spec](../../signet/spec/delegation.md).

⚠️ **The cache upkeep that used to carry her name is Medic's.** It signs nothing and decides nothing, so it
is operator work; see [*Medic — cache upkeep*](#cache-upkeep).

---

## Tailor

*The bespoke fitter — takes any polis site from any era and alters it to fit the current spec, one stitch
at a time.*

| | |
|---|---|
| Schedule | Manual (a CLI tool, not a background job) |
| Scope | **Self-hosted**: the actor built for self-hosters (Patrol and Medic run only inside the hosted service) |
| Code | `cli-go/pkg/tailor` |
| Backup directory | `.polis/tailor-backup/{YYYYMMDD-HHMMSS}/` |

### What it does

Tailor runs **51 ordered checks** (`allChecks` in `cli-go/pkg/tailor/tailor.go`), each with a reason
explaining **why** the change is needed. They are grouped in phases, listed here in the order they run:

| Phase | Checks |
|-------|--------|
| 1. Well-known identity | Version format, author-field migration, legacy config fields, bundle references, avatar config |
| 2. Bundle definition | `bundle.json` presence and validity |
| 3. Layout migration | Posts, comments, following, blessed content moved to content-type paths |
| 4. Cross-platform fixes | Path separator normalization (Windows) |
| 4.5 Bundle/registry/theme migrations | Reference payload, legacy content path, registry migration, legacy theme locations, active bundle fields |
| 4.7 Later migrations | Notification rules relocation, active shape upgrade, legacy v3 archives |
| 5. Derived data | Index rebuild, then site re-render |
| 6. Provisioning | Public/private policies, policy content convergence, tag directory, storage salt, DM directories, epoch-0 DM keyring, theme consolidation |
| 7. Config migration | Webapp config, view mode settings |
| 6.5 Content-aware integrity | Reference payload integrity, registry integrity, key consistency, bundle path integrity, bundle declarations, index entries, blessed/following structure |
| 6.7 Tailor-only self-heal | Active theme unset, registry schema version, registry theme/shape name sanity |
| 8. Cleanup | Obsolete manifests, empty metadata dirs, foreign content in public paths, blessed-cache GC, stale scoped feed files, stale feed viewed_at, legacy feed scaffolding, orphaned theme dirs, studio13 rename migration |
| 9. CLI binary | Check for CLI updates (network call, runs last) |

Like Medic, Tailor signs with the site's own key in one place: publishing the DM messages key when it
provisions the epoch-0 keyring. It carries no marker, because it completes what the site already did.

#### `bundle-declarations` (phase 6.5)

Compares the site's `content/pub.polis.core/bundle.json` against `bundle.DefaultCoreBundle()`. Missing
type, shape and theme declarations are **merged in** from the defaults; per-field drift on a type that is
already declared (`dir`, `mount`, `storage.pattern`, and the `emits` superset) is **reported and never
repaired**.

⚠️ **`dir` and `mount` are the ones worth understanding.** The core bundle's layout is fixed by design:
roughly seventy call sites navigate `content/pub.polis.core/{post,comment,tag,…}` directly, and that is
correct rather than debt. But fixed-by-design is only true if something checks it. A site owner who edits
`dir: post` → `dir: essays` gets a site where those call sites go to the old path while
`bundle.ContentDir()` returns the new one: divergence in both directions, and nothing else notices.

⛔ **Reported, never rewritten, and that is deliberate.** A drifted `dir` may mean content has already been
moved; rewriting the declaration back to the expected value would point the site at an empty directory and
destroy the trail. Reconcile by hand. Patrol runs the same comparison on hosted tenants.

⚠️ **Only `pub.polis.core`.** A future third-party bundle may legitimately declare any layout it likes;
this check asserts that the core bundle still matches the layout the binary assumes.

#### `index-rebuild` (phase 5)

Regenerates `content/pub.polis.core/index.jsonl` from the signed content on disk, **by calling
`cli-go/pkg/index`**, the same code `polis rebuild` runs. Tailor has no rebuilder of its own.

⚠️ **It used to have one**, and the copy walked `post/` only while comparing the whole file. Any site with
blessed comments therefore mismatched **permanently**: `index-rebuild` reported *"Content index needs
rebuild"* on every run and would have truncated the comment entries had it been applied. If you have been
seeing that check fail forever on an unchanged site, this is why: upgrade and run it once.

The check's id, message shapes and action payloads are unchanged. What changed is what it composes: every
content type contributes its own entries (post, comment, tag, attestation) and nothing owns lines it did
not produce.

### Usage

```bash
tailor [site-dir]              # Dry-run (default): diagnose only
tailor --apply [site-dir]      # Apply fixes with backup
tailor --json [site-dir]       # JSON output
tailor --quiet [site-dir]      # Suppress per-check detail
```

Exit codes: 0 = healthy, 1 = issues found, 2 = usage error.

Tailor emits no events: its findings are its own output, with per-check granularity in `--json`.

### What it does NOT do

- **It changes nothing without `--apply`**, and never changes anything without a backup first.
- **It never rewrites a drifted core-bundle `dir` or `mount`** (see above).
- **It does not download binaries.** It migrates site data and layout; the Go CLI ships as a single binary
  you replace directly. Its last phase only checks whether an update exists.
- **It does not run on a schedule** and provides no periodic backstop; see [*Medic — cache upkeep*](#self-hosted).

### Key design decisions

- **Dry-run by default.** Self-hosters must explicitly opt in to changes with `--apply`. Not all users have
  git, and the dry-run output doubles as a changelog of what would happen.
- Every check carries a **one-line reason**, often naming the CLI version that introduced the change
  (e.g., "v0.59.0 moved following.json into content/pub.polis.core/follow/"). Users should never have to
  guess what changed or why.
- Tailor creates timestamped backups at `.polis/tailor-backup/` before modifying anything.
- Tailor understands every historical layout: it can upgrade a v0.42.0 site to current spec in a single
  run.

### Relationship to Patrol + Medic

Tailor is the self-hosted equivalent of the Patrol→Medic pipeline. On the hosted platform, Patrol detects
issues and Medic fixes them transparently. Self-hosters run Tailor manually to get both detection and
repair in one tool, with human review via dry-run.

When a new provisioning check is added to Patrol/Medic, a corresponding check is typically added to Tailor
so self-hosters get the same upgrade path.

---

## Complete Check Matrix

The per-check matrix (every check with its scope, trigger, severity and remediating actor) is operational
detail and is not documented publicly. Each actor's section above lists what it checks or does and what it
leaves to a person.

### Reading the matrix

A few patterns become visible once everything is side by side:

- **Patrol & Medic pair up** on filesystem health: Patrol detects, Medic repairs *when the repair is safe
  and obvious* ("provision a missing file from the default," "chmod 0600 a private key"). Patrol findings
  that *aren't* safe to auto-fix (tampering, signature failures, suspicious key changes) stay with a person.
- **Clerk & Chaplain pair up** on cross-boundary consistency the same way. Clerk *never* writes, by hard
  invariant; Chaplain's repairs all flow from Clerk's findings.
- **Judge** stands alone: it watches for trust drift (key continuity, signature failures, policy
  edits, well-known tampering) where no auto-repair is appropriate. Judge findings are always a
  person's job.
- **Reaper** is the only actor that destroys data, and it does so on a strict 14-day clock with full
  archives and an email-recovery grace period. Key stripping is the only irreversible step, and even that
  happens only when reclaim by a new user makes reinstatement impossible.
- **Medic's cache upkeep** stands alone on content-cache fidelity: the only check that guards cached
  *foreign* content. Its defining stance is durability: it evicts only on a positive signed revocation from
  the DS, never on an unreachable origin, and does nothing when it can't establish the authoritative
  desired set.
- **Tailor** is the union of Patrol + Medic (+ the cache upkeep's offline GC) for self-hosters, with dry-run as the
  default safety stance (`--apply` to change anything).

---

## Event Catalog

Every operator actor reports what it finds as structured events on the operator's observability channel,
in its own namespace (`patrol.*`, `medic.*`, `judge.*`, `clerk.*`, `chaplain.*`, `reaper.*`).
Event names, payloads and severities are operational detail and are not documented publicly. Tailor emits
no events.

---

## Telemetry

The hosted service also runs a lightweight runtime stats logger (not a named actor). Every 15 minutes it
emits runtime metrics: goroutine count, heap allocation, system memory, and tenant cache metrics (size,
max, hits, misses, evictions).
