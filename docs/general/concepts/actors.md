# Actors — Background Jobs & Operational Tools

> **Audience:** Anyone curious how a polis site stays healthy — self-hosters running
> their own site, and readers who want to understand what the hosted service does on
> their behalf.

---

## Overview

The polis hosted service runs a set of independent background actors that maintain
system health without human intervention. Each actor has a single, well-defined
responsibility — they do not overlap, and they do not call each other. Their outputs are
structured events surfaced on monitors and dashboards where problems become visible.

The philosophy is layered: **detect**, **measure**, **heal**, **reconcile**, **verify**,
**reclaim**, **keep house**. Patrol walks the filesystem looking for trouble. Medic fixes
what Patrol finds. Judge independently verifies trust across boundaries. Clerk measures
drift between the tenant and the discovery service. Chaplain reconciles what Clerk surfaces.
Reaper manages the lifecycle of abandoned accounts. Rosie keeps each tenant's content
caches faithful to reality. Tailor brings self-hosted sites up to spec.

All actors except Tailor are hosted-only — they run inside the hosted service. Tailor is
the only actor available to self-hosters, distributed as a standalone binary.

---

## Summary

| Actor | Schedule | Scope | Role | Events |
|-------|----------|-------|------|--------|
| [Patrol](#patrol) | Hourly | Hosted + CLI | Filesystem integrity | `patrol.*` |
| [Medic](#medic) | Hourly | Hosted + CLI | Auto-remediation & upgrades | `medic.*` |
| [Judge](#judge) | Hourly | Hosted only | Cross-boundary trust verification | `judge.*` |
| [Clerk](#clerk) | Daily | Hosted only | State parity measurement | `clerk.*` |
| [Chaplain](#chaplain) | After Clerk | Hosted only | Cross-boundary reconciliation | `chaplain.*` |
| [Reaper](#reaper) | Daily | Hosted only | Account lifecycle | `reaper.*` |
| [Rosie](#rosie) | Daily | Hosted + Tailor offline | Content-cache custodianship | `rosie.*` |
| [Tailor](#tailor) | Manual | Self-hosted | Site migration & upgrade | CLI output only |

---

## How Actors Interact

```
Patrol  ──detects──▶  Medic  ──heals──▶  (tenant fixed)
  │
  └──detects──▶  (alerts surfaced, human reviews)

Judge  ──verifies──▶  (independent trust audit, no downstream actor)

Clerk  ──detects──▶  Chaplain  ──reconciles──▶  (tenant fixed)

Reaper  ──manages──▶  (account lifecycle, independent of other actors)

Rosie   ──reconciles──▶  (content caches kept faithful; periodic backstop to real-time sync)

Tailor  ≈  Patrol + Medic for self-hosted sites (manual, CLI-only)
```

No actor writes to another actor's state. No actor triggers another actor — with one
exception: Clerk triggers Chaplain (they run together, and Chaplain receives Clerk's
sweep results). They are siblings, not a pipeline.

---

## Patrol

*The beat cop — walks the filesystem hourly checking locks, permissions, and evidence of tampering.*

| Attribute | Value |
|-----------|-------|
| Schedule | Hourly |
| Scope | Hosted background + standalone CLI binary |

### What it checks

**Identity & keys:**
- `.well-known/polis` exists and contains `public_key`
- Public key file readable and matches well-known
- Private key file valid (parsed and discarded immediately — never logged)
- Key permissions are restrictive (0600)
- No private key leaked to public-facing directories

**Content integrity:**
- Every post in `posts/` has a valid Ed25519 signature
- Every post content hash matches the frontmatter hash

**Structure & provisioning:**
- Public and private policy files present
- Storage salt present
- DM directories present with correct permissions
- `bundle.json` present and valid
- Avatar and favicon present

**Snapshot-based detection** (baselines stored outside tenant directories, so tenants
cannot tamper with their own baselines):
- Salt integrity — content hash change = HIGH alert (tamper), mtime-only change = MEDIUM
- File mtime tracking on private key, public key, well-known, salt, policies
- Unauthorized file creation in `.polis/keys/` (HIGH) and `.polis/policies/` (MEDIUM)

**Infrastructure:**
- Volume mount writable with adequate free space
- Stale archives (90+ days)
- Root-level SSH/shell artifacts (`.ssh/`, `.bashrc`, `.profile`)
- Policy syntax validation (parse every rule, report errors)

### Events

| Event | When |
|-------|------|
| `patrol.sweep` | Every sweep (tenants, passed, failed, duration) |
| `patrol.fail` | Per-tenant check failure |
| `patrol.warn` | Non-critical warning |
| `patrol.warn.policy` | Invalid policy rule syntax |
| `patrol.warn.dm_domain_case` | Mixed-case domain in DM conversations |
| `patrol.alert.volume` | Disk space or writability issue |
| `patrol.alert.salt_tamper` | Storage salt hash changed |
| `patrol.alert.mtime` | Sensitive file mtime changed |
| `patrol.alert.file_creation` | New file in keys/ or policies/ dir |
| `patrol.alert.root_artifact` | SSH/shell artifact in root |
| `patrol.upgrade` | Tenant needs upgrade (informational) |
| `patrol.policy_format_drift` | Policy file format doesn't match current spec |
| `patrol.info.stale_archive` | Archive older than 90 days |

### Key design decisions

- Snapshot baselines live **outside** tenant directories — tenants cannot tamper with
  their own baselines.
- Patrol **never** logs key content. Only file paths and pass/fail booleans. Private-key
  validation returns error/nil only — the parsed key is discarded immediately.
- First sweep for any tenant establishes baselines (no alerts). Subsequent sweeps compare
  against baselines.

### Performance

Sub-second per tenant for filesystem checks. A full sweep completes quickly even across
many tenants. No network I/O.

### Standalone CLI

```bash
patrol [tenants-dir]
```

JSON result to stdout, human summary to stderr. Exit 0 = all pass, 1 = failures, 2 =
missing directory.

---

## Medic

*The field medic — silently heals what Patrol finds and upgrades tenants to current spec without waking anyone.*

| Attribute | Value |
|-----------|-------|
| Schedule | Hourly |
| Scope | Hosted background + standalone CLI binary |

### What it does

**Permission fixes:**
- `chmod 0600` on private keys with wrong permissions
- `chmod 0700` on DM directories with wrong permissions

**Quarantine:**
- Moves suspicious files (executables, symlinks, oversized, suspicious extensions) to a
  quarantine area with a manifest recording what was moved and why

**Provisioning (transparent upgrades):**
- Creates missing public policy files (`policies/rules.jsonl`)
- Creates missing private policy files (`.polis/policies/rules.jsonl`)
- Appends missing DM policy rules to existing private policies
- Creates missing storage salt (`.polis/storage-salt`)
- Creates missing DM directories with 0700 permissions
- Creates missing `bundle.json`
- Generates missing avatars and favicons
- Creates missing theme directories

**Content fixes:**
- Encrypts plaintext DM preview excerpts at rest
- Normalizes mixed-case domains in DM conversation files
- Removes deprecated/stale files (scoped feed caches, registration flags, old field names)
- Re-renders tenant sites if theme files were cleaned up

### Events

| Event | When |
|-------|------|
| `medic.sweep` | Every sweep (tenants, fixed, quarantined, provisioned, duration) |
| `medic.fix` | Permission correction applied |
| `medic.quarantine` | File quarantined |
| `medic.provision` | Missing file created |
| `medic.policy_upgrade` | Policy file upgraded to current format |
| `medic.skip` | Non-correctable issue counted and skipped |
| `medic.rerender` | Site re-rendered after file changes |

### Key design decisions

- **Safe, reversible fixes only.** Quarantined files are preserved with a manifest for
  investigation. Non-correctable issues are counted and skipped — Medic never forces a fix
  it isn't confident about.
- Hosted mode always runs in apply mode. The CLI binary defaults to dry-run for safety.
- Medic is the **only** actor that renders content without being asked — it re-renders an
  entire site after cleaning up theme files.
- Key encryption at rest (AES-256-GCM via a master encryption key) is Medic's
  responsibility — not a one-time migration but continuous enforcement. Plaintext previews
  never survive more than one sweep.

### Performance

Sub-second per tenant for most fixes. Re-rendering a site takes a second or two depending
on post count.

### Standalone CLI

```bash
medic [--dry-run|--apply] [--quarantine-dir DIR] [tenants-dir]
```

Default mode is `--dry-run` (preview only). Must explicitly pass `--apply` to make changes.

---

## Judge

*The magistrate — independently verifies that trust claims hold across boundaries, from signatures to attestations.*

| Attribute | Value |
|-----------|-------|
| Schedule | Hourly |
| Scope | Hosted only |

### The checks

| # | Check | I/O | Purpose |
|---|-------|-----|---------|
| 1 | **HTTP content verification** | Local loopback | Fetches posts via localhost HTTP, verifies signatures + hashes match disk — tests the full serving pipeline |
| 2 | **DS attestation audit** | HTTP to DS | Verifies blessed comments have valid DS autobless attestation signatures |
| 3 | **Public key continuity** | Local FS | Tracks key fingerprints over time; alerts on unexpected changes (proto-TOFU) |
| 4 | **Policy snapshot verification** | Local FS | Detects retroactive policy changes after blessings were granted |
| 5 | **Index consistency** | Local FS | Verifies `index.jsonl` entries match real signed files — detects orphans, phantoms, hash mismatches |
| 6 | **Cross-site comment verification** | External HTTP | Verifies blessed comment signatures against the claimed author's current public key |
| 7 | **Well-known snapshot verification** | Local FS | Hashes `.well-known/polis` and alerts on tampering with identity metadata (handle, base_url, display_name, default_theme, etc.); complements check 3, which covers `public_key` specifically |

### Events

| Event | When |
|-------|------|
| `judge.sweep` | Every sweep (tenants, passed, failed, findings, duration) |
| `judge.fail.signature` | Post signature/hash failed HTTP verification |
| `judge.fail.attestation` | DS attestation invalid |
| `judge.fail.index` | Index inconsistency (orphan, phantom, hash mismatch) |
| `judge.fail.cross_site` | Remote comment signature invalid |
| `judge.alert.key_change` | Public key changed since last baseline |
| `judge.alert.policy_change` | Policy file changed since last snapshot |
| `judge.alert.wellknown_change` | `.well-known/polis` changed since last snapshot |
| `judge.info.baseline` | First-run baseline recorded (key, policy, or wellknown) |

### Key design decisions

- While Patrol is **forensic** (local filesystem health), Judge is **accountability**
  (system-wide trust). They are complementary, not overlapping.
- Judge waits for the HTTP server to be ready before starting checks — it needs a working
  loopback to verify serving correctness.
- State is stored outside tenant directories. Missing state = first run = record baseline,
  no alert.
- External HTTP requests (check 6) use a short timeout. Unreachable domains produce
  warnings, not failures.

### Performance

Most checks are sub-second. Cross-site comment verification dominates runtime because it
makes external HTTP calls to other domains.

---

## Clerk

*The auditor — reads both sides of the tenant↔DS ledger and reports discrepancies without touching either.*

| Attribute | Value |
|-----------|-------|
| Schedule | Daily |
| Scope | Hosted only (no CLI, no standalone binary) |

### What it measures

For each hosted tenant and each content type, Clerk compares the local artifact count to
the DS event count:

| Content type | Local source | DS query |
|--------------|-------------|----------|
| Follow | Entries in `following.json` | `pub.polis.follow.announced` events for this actor |
| Post | Files under `content/pub.polis.core/post/` | `pub.polis.post.published` unique URLs |
| Comment | Files under `content/pub.polis.core/comment/` | `pub.polis.comment.published` unique URLs |
| Tag | Local tag files | `pub.polis.tag.applied` minus `tag.removed` |
| Registration | Presence of the local registration marker | Registered-sites record at the DS |

### Events

| Event | When |
|-------|------|
| `clerk.sweep` | Every sweep (tenants, drift_tenants, duration) |
| `clerk.parity` | Per-tenant per-type result (local_count, ds_count, drift boolean) |
| `clerk.error` | Measurement failure (DS query failed, not drift) |

### Key design decisions

- **CRITICAL INVARIANT: Clerk is read-only.** It must not write to disk, must not emit DS
  events, and must not call any function that mutates state on either side. This invariant
  exists because Clerk's measurements are the input to Chaplain's reconciliation phase. If
  Clerk auto-fixed something, it would create a feedback loop that masks the very drift it
  is meant to surface. Chaplain receives Clerk's results and performs the actual repairs.
- Clerk is intentionally not exposed as a CLI subcommand or standalone binary. The hosted
  wrapper is the only supported entry point.
- No DS client = no-op. Clerk has no reason to exist without DS access.

### Performance

Daily cadence. Each sweep queries the DS for every tenant and content type; DS round-trip
time is the bottleneck.

---

## Chaplain

*The parish caretaker — reconciles cross-boundary consistency issues that Clerk surfaces.*

| Attribute | Value |
|-----------|-------|
| Schedule | After each Clerk sweep (daily) |
| Scope | Hosted only |

### What it reconciles

| Issue | Detection (Clerk) | Repair (Chaplain) |
|-------|-------------------|-------------------|
| Blessed index drift | Granted blessings missing from `blessed.json` | Adds each missing entry, re-renders tenant |
| Registration marker missing | DS has a registration record but the local marker is absent | Re-registers with the DS, writes local marker |
| Follow announcement drift | `following.json` has entries with no corresponding DS events | Re-publishes missing `pub.polis.follow.announced` events |
| Post registration drift | Post files on disk with no corresponding DS events | Re-registers missing posts |
| Comment registration drift | Authored comment files on disk with no corresponding DS events | Re-registers missing authored comments |
| Tag registration drift | Tag files on disk whose targets aren't all announced | Idempotently registers every target of every tag file |

### Events

| Event | When |
|-------|------|
| `chaplain.sweep` | Every sweep (tenants, total_fixed, total_skipped, duration) |
| `chaplain.fix` | Per-fix action |
| `chaplain.skip` | Non-reconcilable issues |
| `chaplain.rerender` | Site re-rendered after blessed.json changes |
| `chaplain.registration_repair` | Registration repair attempt |
| `chaplain.follow_repair` | Follow re-announcement attempt |
| `chaplain.post_repair` | Post re-registration attempt |
| `chaplain.comment_repair` | Comment re-registration attempt |
| `chaplain.tag_repair` | Tag sync attempt |

### Key design decisions

- **Chaplain pairs with Clerk** like Medic pairs with Patrol. The boundary is clear:
  Patrol/Medic own filesystem health, Clerk/Chaplain own cross-boundary consistency.
- **Network-free core.** The reconciliation logic reads local files; network operations
  (DS re-registration) are handled by the hosted wrapper.
- **Receives Clerk results, doesn't run Clerk.** Chaplain takes Clerk's pre-computed
  results to avoid duplicating DS queries.
- **Runs with Clerk** — same cadence, called at the end of each Clerk sweep.
- **Re-renders after blessed.json changes** so HTML pages reflect newly added comments.

---

## Reaper

*The estate manager — handles the lifecycle of abandoned accounts with archive-first, destroy-never principles.*

| Attribute | Value |
|-----------|-------|
| Schedule | Daily, runs once on start |
| Scope | Hosted only |

### What it does

**Account lifecycle:**
- **Day 7**: Sends reminder emails to unverified tenants
- **Day 14**: Reaps unverified tenants — archives to a compressed bundle, then deletes
- Archives include full site data (posts, keys, config) for potential reinstatement

**Reinstatement:**
- During the grace period, the original owner can reclaim their handle via email recovery
- The archive (with keys) is restored to the tenant directory
- Once a **new user claims the same handle**, reinstatement becomes impossible — at that
  point Reaper strips `.polis/keys/` and the storage salt from the old archive to prevent
  key leakage

**Cleanup:**
- Cancels pending blessings when reaping
- Cleans expired magic links, widget tokens, and sessions
- Cleans stale rate-limiter entries

### Events

| Event | When |
|-------|------|
| `reaper.sweep` | Every sweep (reminders_sent, reaped, cleaned counts) |
| `reaper.reminder` | Day-7 reminder email sent |
| `reaper.reap` | Tenant archived and deleted |
| `reaper.reinstate` | Reaped tenant restored from archive |
| `reaper.keys_stripped` | Private keys removed from archive (handle reclaimed by new user) |

### Key design decisions

- **Archive-first, destroy-never.** Reaped data is compressed and retained, never simply
  deleted. Archives persist for audit purposes.
- Key stripping happens at the moment of reclaim, not at reap time — the grace period
  requires keys to be present for reinstatement.
- The Reaper is the only actor that depends on the database and the email provider.

---

## Rosie

*The housekeeper (ref: the Jetsons) — keeps each tenant's content caches faithful and tidy.*

| Attribute | Value |
|-----------|-------|
| Schedule | Daily, runs once on start |
| Scope | Hosted goroutine + Tailor (offline GC) for self-hosters |
| State directory | None — stateless; the target is recomputed each sweep from `blessed.json ∩ DS-status` |

### What it does

Per tenant, for each registered cache **kind** (today: blessed comments authored by another
tenant), Rosie keeps the local cache faithful to reality:

- **Reconcile** desired-vs-present. The desired set is `blessed.json ∩ DS-current-status`
  (comments still granted, per the discovery service). Missing entries are re-fetched and
  verified; entries whose blessing was withdrawn or denied are evicted; the rest are kept.
- **Verify integrity** of each cached entry by re-checking the author's canonical artifact
  against their **current** published key. A clean key rotation that re-signs verifies fine;
  only a signature that fails the current key raises an integrity alert (possible tampering).
- **Garbage-collect** structural orphans (a cached body with no readable provenance) and
  stale per-scope feed caches.

**Real-time vs. backstop.** The webapp's sync handlers do the real-time, event-driven
ingest and eviction as blessings change. Rosie is the **periodic backstop** for drift —
missed events, cursor loss, downtime. Both paths share one code path, so they cannot drift
apart, and Rosie is idempotent: she never duplicates real-time work.

**Durability, not eviction.** An unreachable author is *evidence for durability, never
eviction*: a desired entry whose origin is down is kept. Eviction happens **only** when the
discovery service positively reports the blessing withdrawn or denied. If the authoritative
desired set can't be computed (DS down, or an anomalous response), Rosie does nothing for
that tenant — she never evicts on uncertainty.

### Events

`source: "landlord"`, `rosie.*` namespace, correlated by a per-sweep `sweep_id`.

| Event | When |
|-------|------|
| `rosie.sweep` | Every sweep (tenants, files checked, refetched, evicted, gc_removed, integrity_failures, duration) |
| `rosie.refetch` | A kind had missing entries re-fetched |
| `rosie.evict` | A kind had withdrawn/denied entries evicted |
| `rosie.gc` | Orphan cache entries or stale feed caches removed |
| `rosie.integrity_fail` | A cached signature no longer verifies against the author's current key — **possible tampering, HIGH** |
| `rosie.error` | Reconcile error (DS down / safety valve), or no discovery URL configured |

### Key design decisions

- **Kind-agnostic.** Rosie never special-cases a cache type. Adding avatar/reply-context/feed
  caches later means registering a descriptor, not editing Rosie.
- **Anti-drift by construction.** The real-time sync path and Rosie's reconcile go through the
  same generic cache operations, with a test asserting they converge.
- **Stateless.** The target is recomputed each sweep; there is no baseline to corrupt.
- **Reaper interaction (conservative default):** a reaped author's comments that are blessed
  elsewhere **persist** in others' caches (durability — consistent with the time-capsule goal).
  A reap emits no cache tombstone.

### Self-hosted

Self-hosters get the **offline** slice via `polis tailor apply`: orphaned blessed-cache bodies
are removed. Full reconcile + integrity (which need DS access and network) run when the
self-hosted webapp syncs; the standalone Rosie goroutine is hosted-only.

---

## Tailor

*The bespoke fitter — takes any polis site from any era and alters it to fit the current spec, one stitch at a time.*

| Attribute | Value |
|-----------|-------|
| Schedule | Manual (CLI tool, not a background job) |
| Scope | **Self-hosted** — the only actor available outside hosted |
| Backup directory | `.polis/tailor-backup/{YYYYMMDD-HHMMSS}/` |

### What it does

Tailor runs 25+ ordered checks across 9 phases, each with a reason explaining **why** the
change is needed:

| Phase | Checks |
|-------|--------|
| 1. Well-known identity | Version format, legacy config fields, bundle references, avatar config |
| 2. Bundle definition | `bundle.json` presence and validity |
| 3. Layout migration | Posts, comments, following, blessed content moved to content-type paths |
| 4. Cross-platform fixes | Path separator normalization (Windows) |
| 5. Derived data | Index rebuild, site re-render |
| 6. Provisioning | Public/private policies, policy content convergence, tag directory, storage salt, DM directories, DM domain case, DM preview encryption, theme consolidation |
| 7. Config migration | Webapp config, view mode settings |
| 8. Cleanup | Obsolete manifests, empty metadata dirs, stale scoped feed files, stale feed viewed_at |
| 9. CLI binary | Check for CLI updates (network call, runs last) |

### Usage

```bash
tailor [site-dir]              # Dry-run (default): diagnose only
tailor --apply [site-dir]      # Apply fixes with backup
tailor --json [site-dir]       # JSON output
tailor --quiet [site-dir]      # Suppress per-check detail
```

Exit codes: 0 = healthy, 1 = issues found, 2 = usage error.

### Key design decisions

- **Dry-run by default.** Self-hosters must explicitly opt in to changes with `--apply`.
  Not all users have git — the dry-run output doubles as a changelog of what would happen.
- Every action includes a **one-line reason** referencing the CLI version that introduced
  the change (e.g., "v0.59.0 moved following.json into content/pub.polis.core/follow/").
  Users should never have to guess what changed or why.
- Tailor creates timestamped backups at `.polis/tailor-backup/` before modifying anything.
- Tailor understands every historical layout — it can upgrade a very old site to current
  spec in a single run.

### Relationship to Patrol + Medic

Tailor is the self-hosted equivalent of the Patrol→Medic pipeline. On the hosted platform,
Patrol detects issues and Medic fixes them transparently. Self-hosters run Tailor manually
to get both detection and repair in one tool, with human review via dry-run.

When a new provisioning check is added to Patrol/Medic, a corresponding check is typically
added to Tailor so self-hosters get the same upgrade path.

---

## Complete Check Matrix

Every check the actors perform, in one table. Read this when you need to answer "what does
the system actually watch for?" or "when X goes wrong, which actor surfaces it?"

**Columns:**
- **Actor** — which background actor runs this check.
- **Check** — the specific thing being verified.
- **Scope** — per-tenant, global, or per-archive.
- **Trigger** — what cadence or condition causes the check to run.
- **On fail** — emit event + severity (Critical / High / Error / Warn / Info).
- **Remediation** — which actor (if any) auto-repairs, or "Operator" for issues that
  require human investigation.

### Patrol — local filesystem health

| Check | Scope | Trigger | On fail | Remediation |
|---|---|---|---|---|
| `.well-known/polis` exists + has `public_key` | per-tenant | hourly | `patrol.fail` (Error) | Operator |
| Public key file readable + matches well-known | per-tenant | hourly | `patrol.fail` (Error) | Operator |
| Private key file parseable | per-tenant | hourly | `patrol.fail` (Error) | Operator |
| Private key permissions = 0600 | per-tenant | hourly | `patrol.fail` (Error) | Medic chmod |
| No private key in public-facing dirs | per-tenant | hourly | `patrol.fail` (Critical) | Operator (manual remove) |
| Every post has valid Ed25519 signature | per-tenant | hourly | `patrol.fail` (Error) | Operator (re-sign or investigate tampering) |
| Post content hash matches frontmatter hash | per-tenant | hourly | `patrol.fail` (Error) | Operator |
| Public/private policy files present | per-tenant | hourly | `patrol.warn` (Warn) | Medic provisions defaults |
| Policy file syntax parseable | per-tenant | hourly | `patrol.warn.policy` (Warn) | Operator fixes syntax |
| Storage salt present | per-tenant | hourly | `patrol.warn` (Warn) | Medic provisions |
| DM directories present + 0700 perms | per-tenant | hourly | `patrol.warn` (Warn) | Medic provisions + chmod |
| `bundle.json` present + valid | per-tenant | hourly | `patrol.warn` (Warn) | Medic provisions |
| Avatar + favicon present | per-tenant | hourly | `patrol.warn` (Info) | Medic provisions |
| Salt **content** hash unchanged (tamper detection) | per-tenant | hourly | `patrol.alert.salt_tamper` (High) | Operator investigation |
| Salt mtime unchanged (when content matches) | per-tenant | hourly | `patrol.alert.salt_tamper` (Medium) | Operator |
| File mtimes on key/well-known/salt/policies unchanged | per-tenant | hourly | `patrol.alert.mtime` (Medium) | Operator |
| No new files in `.polis/keys/` since baseline | per-tenant | hourly | `patrol.alert.file_creation` (High) | Operator |
| No new files in `.polis/policies/` since baseline | per-tenant | hourly | `patrol.alert.file_creation` (Medium) | Operator |
| Volume mount writable + has space | global | hourly | `patrol.alert.volume` (Critical) | Operator (capacity) |
| No stale archives older than 90 days | global | hourly | `patrol.info.stale_archive` (Info) | Operator review |
| No SSH/shell artifacts at root (`.ssh/`, `.bashrc`, `.profile`) | global | hourly | `patrol.alert.root_artifact` (High) | Operator |
| DM domain case is lowercase | per-tenant | hourly | `patrol.warn.dm_domain_case` (Warn) | Medic normalizes |
| Bundle/policy/well-known formats match current spec | per-tenant | hourly | `patrol.upgrade` (Info) | Medic migrates |

### Medic — silent repair

Medic doesn't *check* — it *acts on* Patrol's findings (and a few of its own provisioning
passes). Each row is the action:

| Action | Scope | Trigger | Event | Notes |
|---|---|---|---|---|
| `chmod 0600` on private keys | per-tenant | wrong perms | `medic.fix` | Idempotent |
| `chmod 0700` on DM dirs | per-tenant | wrong perms | `medic.fix` | Idempotent |
| Quarantine suspicious files (executables, symlinks, oversized) | per-tenant | suspicious file present | `medic.quarantine` | Files preserved in a quarantine area |
| Provision missing public policy file | per-tenant | file absent | `medic.provision` | From built-in defaults |
| Provision missing private policy file | per-tenant | file absent | `medic.provision` | Includes DM policy rules |
| Append missing DM policy rules to existing private policies | per-tenant | rules absent | `medic.provision` | Surgical insert, not overwrite |
| Provision missing storage salt | per-tenant | file absent | `medic.provision` | Fresh random salt |
| Provision missing DM dirs (with 0700) | per-tenant | dirs absent | `medic.provision` | — |
| Provision missing `bundle.json` | per-tenant | file absent | `medic.provision` | Pulled from embedded fixture |
| Generate missing avatar + favicon | per-tenant | file absent | `medic.provision` | Default avatar based on handle |
| Provision missing theme directories | per-tenant | dirs absent | `medic.provision` | — |
| Encrypt plaintext DM previews at rest | per-tenant | plaintext detected | `medic.fix` | AES-256-GCM via a master encryption key |
| Normalize mixed-case DM domain names | per-tenant | mixed case detected | `medic.fix` | Renames + updates references |
| Remove deprecated/stale files | per-tenant | stale file present | `medic.fix` | Old scoped feed caches, registration flags |
| Upgrade policy file format | per-tenant | format drift | `medic.policy_upgrade` | v1 → v2 silent upgrade |
| Re-render tenant site after theme cleanup | per-tenant | theme files changed | `medic.rerender` | The only actor that renders unprompted |
| Skip non-correctable issue | per-tenant | unsafe to auto-fix | `medic.skip` | Logged, not repaired |

### Judge — cross-boundary trust

| Check | Scope | Trigger | On fail | Remediation |
|---|---|---|---|---|
| HTTP content verification — fetch posts via loopback, verify signatures + hashes match disk | per-tenant | hourly | `judge.fail.signature` (Error) | Operator investigation |
| DS attestation audit — blessed comments have valid DS autobless signatures | per-tenant | hourly | `judge.fail.attestation` (Error) | Operator |
| Public key continuity (proto-TOFU) — key fingerprint unchanged since baseline | per-tenant | hourly | `judge.alert.key_change` (High) | Operator (key change requires verification) |
| Policy snapshot — policy files unchanged since blessings were granted | per-tenant | hourly | `judge.alert.policy_change` (Warn) | Operator (retroactive policy edit is suspicious) |
| Index consistency — `index.jsonl` entries match real signed files | per-tenant | hourly | `judge.fail.index` (Error) | Tailor / rebuild |
| Cross-site comment verification — blessed comment signatures verify against author's current public key | per-tenant | hourly | `judge.fail.cross_site` (Error) | Operator |
| Well-known snapshot — `.well-known/polis` identity metadata unchanged | per-tenant | hourly | `judge.alert.wellknown_change` (Warn) | Operator (re-baselines silently after firing once) |
| First-run baseline established | per-tenant | first sweep | `judge.info.baseline` (Info) | None — establishes future detection |

### Clerk — read-only cross-boundary parity

Clerk measures; it never repairs. Its findings feed into Chaplain.

| Check (parity comparison) | Scope | Trigger | On drift | Repaired by |
|---|---|---|---|---|
| `following.json` entries vs `pub.polis.follow.announced` events at DS | per-tenant | daily | `clerk.parity` w/ drift=true | Chaplain re-announces |
| Post files on disk vs `pub.polis.post.published` unique URLs at DS | per-tenant | daily | `clerk.parity` w/ drift=true | Chaplain re-registers |
| Comment files (authored) on disk vs `pub.polis.comment.published` URLs at DS | per-tenant | daily | `clerk.parity` w/ drift=true | Chaplain re-registers |
| Tag files on disk vs `pub.polis.tag.applied`/`removed` events at DS | per-tenant | daily | `clerk.parity` w/ drift=true | Chaplain re-syncs |
| Local registration marker vs DS registered-sites record | per-tenant | daily | `clerk.parity` w/ drift=true | Chaplain re-registers |
| Blessed comments index parity: granted blessings vs entries in `blessed.json` | per-tenant | daily | `clerk.parity` w/ drift=true | Chaplain re-adds entries |
| DS query failed during measurement | per-tenant | daily | `clerk.error` (Error) | Operator (DS connectivity / auth issue) |

### Chaplain — cross-boundary repair

Triggered by Clerk results (same cadence). Each row is the repair Chaplain performs when
Clerk surfaces drift:

| Repair | Trigger | Event | Notes |
|---|---|---|---|
| Add missing entries to `blessed.json` + re-render tenant | Blessed index drift | `chaplain.fix`, `chaplain.rerender` | — |
| Re-register with DS + write local marker | Registration drift | `chaplain.registration_repair` | — |
| Re-publish missing `pub.polis.follow.announced` events | Follow drift | `chaplain.follow_repair` | Per-target detail in `chaplain.follow_repair_error` |
| Re-register missing posts | Post drift | `chaplain.post_repair` | Per-post errors in `chaplain.post_repair_error` |
| Re-register missing authored comments | Comment drift | `chaplain.comment_repair` | Per-comment errors in `chaplain.comment_repair_error` |
| Idempotently re-sync every target of every tag | Tag drift | `chaplain.tag_repair` | Per-tag errors in `chaplain.tag_repair_error` |
| Skip non-reconcilable issue | Drift Chaplain doesn't know how to fix | `chaplain.skip` | Counted, not repaired |

### Reaper — account lifecycle

| Action | Scope | Trigger | Event | Notes |
|---|---|---|---|---|
| Send day-7 reminder email | per-tenant | unverified tenant aged 7 days | `reaper.reminder` | Email hashed in event |
| Reap unverified tenant | per-tenant | unverified tenant aged 14 days | `reaper.reap` | Archived, then deleted |
| Reinstate from archive | per-tenant | user reclaims via email recovery in grace period | `reaper.reinstate` | Restores keys + content |
| Strip private keys from archive | per-archive | new user claims same handle (no longer reinstateable) | `reaper.keys_stripped` | Removes `.polis/keys/` + storage salt from old archive |
| Cancel pending blessings on reap | per-tenant | reap | (part of `reaper.reap`) | — |
| DB cleanup: expired magic links / widget tokens / sessions / rate-limiter entries | global | daily | (part of `reaper.sweep`) | Aggregated counts in sweep summary |

### Rosie — content-cache custodianship

| Action | Scope | Trigger | Event | Notes |
|---|---|---|---|---|
| Re-fetch missing desired cache entries | per-tenant, per-kind | entry in `blessed.json ∩ DS-status` not present | `rosie.refetch` | Re-fetched + verified before caching |
| Evict withdrawn/denied entries | per-tenant, per-kind | DS reports blessing withdrawn/denied | `rosie.evict` | Only eviction path; never on uncertainty |
| Verify cached-entry integrity | per-tenant, per-kind | each present entry, daily | `rosie.integrity_fail` (HIGH) | Signature fails author's **current** key — possible tamper |
| GC structural orphans + stale feed caches | per-tenant | orphaned body / stale scoped feed | `rosie.gc` | — |
| Skip tenant on unresolvable desired set | per-tenant | DS down / anomalous response | `rosie.error` | Does nothing — durability over eviction |

### Tailor — self-hosted, manual

Tailor's 25+ checks across 9 phases are documented in the [Tailor section](#tailor) above.
Each phase contains multiple individual checks; per-check granularity is in the Tailor
CLI's `--json` output. When a new provisioning check lands in Patrol/Medic for hosted
tenants, a corresponding check typically lands in Tailor so self-hosters get the same
upgrade path.

### Reading the matrix

A few patterns become visible once everything is in one table:

- **Patrol & Medic pair up** on filesystem health: Patrol detects, Medic repairs *when the
  repair is safe and obvious* ("provision a missing file from the default," "chmod 0600 a
  private key"). Patrol findings that *aren't* safe to auto-fix (tampering, signature
  failures, suspicious key changes) stay on the Operator track.
- **Clerk & Chaplain pair up** on cross-boundary consistency the same way Patrol & Medic
  pair up on filesystem. Clerk *never* writes, by hard invariant; Chaplain's repairs all
  flow from Clerk's findings.
- **Judge** stands alone — it watches for trust drift (key continuity, signature failures,
  retroactive policy edits, well-known tampering) where no auto-repair is appropriate.
  Judge findings are always the Operator's job.
- **Reaper** is the only actor that destroys data, and it does so on a strict 14-day clock
  with full archives and an email-recovery grace period. Key stripping is the only
  irreversible step, and even that happens only when reclaim by a new user makes
  reinstatement impossible.
- **Tailor** is the union of Patrol + Medic for self-hosters, with `--dry-run` as the
  default safety stance.

The matrix is also a contract: an operator or LLM answering "what does polis check?" should
be able to read this table top-to-bottom and answer comprehensively.

---

## Event Catalog

All actor events are emitted as structured JSON to the hosted runtime's observability
backend.

### Patrol events

| Event | Fields | Severity |
|-------|--------|----------|
| `patrol.sweep` | tenants, passed, failed, warnings, duration | Info |
| `patrol.fail` | handle, checks (which failed), first_error | Error |
| `patrol.warn` | handle, message | Warning |
| `patrol.warn.policy` | handle, file, line, rule, error | Warning |
| `patrol.warn.dm_domain_case` | handle, path | Warning |
| `patrol.alert.volume` | mount, free_bytes, writable | Critical |
| `patrol.alert.salt_tamper` | handle, severity, previous_hash, current_hash | Critical |
| `patrol.alert.mtime` | handle, file, severity, prev_mtime, curr_mtime | High |
| `patrol.alert.file_creation` | handle, dir, file, severity | High |
| `patrol.alert.root_artifact` | path | High |
| `patrol.upgrade` | handle, check, detail | Info |
| `patrol.policy_format_drift` | handle, detail | Warning |
| `patrol.info.stale_archive` | path, age_days | Info |

### Medic events

| Event | Fields |
|-------|--------|
| `medic.sweep` | tenants, total_fixed, total_quarantined, total_provisioned, duration |
| `medic.fix` | handle, path, action, detail |
| `medic.quarantine` | handle, path, reason, quarantine_dir |
| `medic.provision` | handle, path, detail |
| `medic.policy_upgrade` | handle, detail |
| `medic.skip` | handle, check, reason |
| `medic.rerender` | handle, result, detail |

### Judge events

| Event | Fields |
|-------|--------|
| `judge.sweep` | tenants, passed, failed, findings, duration |
| `judge.fail.signature` | handle, post_path, reason |
| `judge.fail.attestation` | handle, comment_url, reason |
| `judge.fail.index` | handle, type (orphan/phantom/hash_mismatch), path |
| `judge.fail.cross_site` | handle, comment_url, author_domain, reason |
| `judge.alert.key_change` | handle, old_fingerprint, new_fingerprint |
| `judge.alert.policy_change` | handle, file, old_hash, new_hash |
| `judge.alert.wellknown_change` | handle, message |
| `judge.info.baseline` | handle, type (key/policy/wellknown) |

### Clerk events

| Event | Fields |
|-------|--------|
| `clerk.sweep` | tenants, drift_tenants, duration |
| `clerk.parity` | handle, content_type, local_count, ds_count, drift |
| `clerk.error` | handle, content_type, error |

### Chaplain events

| Event | Fields |
|-------|--------|
| `chaplain.sweep` | tenants, total_fixed, total_skipped, duration |
| `chaplain.fix` | handle, path, action, detail |
| `chaplain.skip` | handle, skipped |
| `chaplain.rerender` | handle, posts, comments |
| `chaplain.registration_repair` | handle, domain, result, error |
| `chaplain.follow_repair` | handle, domain, result, announced, total |
| `chaplain.post_repair` | handle, domain, result, registered, failed |
| `chaplain.comment_repair` | handle, domain, result, registered, failed |
| `chaplain.tag_repair` | handle, domain, result, synced, failed |

### Reaper events

| Event | Fields |
|-------|--------|
| `reaper.sweep` | reminders_sent, reaped, duration |
| `reaper.reminder` | handle, email_hash |
| `reaper.reap` | handle, archive_size |
| `reaper.reinstate` | handle |
| `reaper.keys_stripped` | handle, archives_processed |

### Rosie events

| Event | Fields |
|-------|--------|
| `rosie.sweep` | sweep_id, tenants, cache_files_checked, refetched, evicted, gc_removed, integrity_failures, duration |
| `rosie.refetch` | handle, kind, refetched, urls |
| `rosie.evict` | handle, kind, evicted, urls |
| `rosie.gc` | handle, removed, paths |
| `rosie.integrity_fail` | handle, kind, url, detail |
| `rosie.error` | handle, detail |

---

## Telemetry

The hosted service also runs a lightweight runtime stats logger (not a named actor). Every
15 minutes it emits runtime metrics — goroutine count, heap allocation, system memory, and
tenant cache metrics (size, max, hits, misses, evictions).
