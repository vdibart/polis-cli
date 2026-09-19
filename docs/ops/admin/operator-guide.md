# Running polis — the operator's manual

*For* [Operators](../../README.md#running-polis) · [Reviewers](../../README.md#reviewing-the-security-and-identity-design) — *Kind* [Concept](../../README.md#kinds-of-page) — *Component* [Hosted service](../README.md) — *See also* [spec](../../signet/spec/custody.md) · [guide](../../signet/guides/network-health.md)

> **Audience: an operator who is not polis.pub.** Someone who has a copy of the code, a volume,
> a domain, and other people's sites on it.
>
> [The actors](../../general/concepts/actors.md) tells you **what the software does**. This tells you **what you are now
> responsible for, and what you get to choose.** Several of the things below
> are *choices*, not facts about polis — and ⚠️ **an undocumented choice is one every operator makes
> by accident.**

---

## How to read this document

⚠️ **Some of what follows has shipped and some of it is designed and not built.** Every section is
marked, and the marks are not decoration — a manual that describes unbuilt behaviour in the present
tense is worse than one that omits it.

| Mark | Means |
|---|---|
| ✅ **shipped** | It is in the code today. You can act on it now. |
| 🔧 **design** | Scoped, not built. **Do not plan around it as though it exists**, and do not go looking for the setting. |

⛔ **Nothing here is a rule for you.** polis.pub is one operator with one set of tradeoffs. Where this
document records a position, it says whose it is. **An instance for a school, a company, or one
family will reasonably answer differently**, and the job of this manual is to give you the concepts
and the triage — never the verdict.

**See also:** [the actors](../../general/concepts/actors.md) (the eight actors, in detail) ·
[`hosted-service.md`](hosted-service.md) (the hosted service's architecture) ·
[`../../ds/admin/configuration.md`](../../ds/admin/configuration.md) (what a discovery-service operator can tune).
The hosted service's settings, alerts and runbooks are not documented publicly: its source is not in the
public repository.

---

## 1. Custody — you hold your tenants' keys

✅ **shipped — this is the situation you are already in.**

Every hosted tenant's Ed25519 private key sits on your volume at
`{DATA_DIR}/tenants/{handle}/.polis/keys/id_ed25519`, and your process reads it. One function does
the borrowing: `LocalTenantStorage.ReadPrivateKey(handle)`
(`webapp/internal/hosted/local_storage.go`).

⚠️ **The actors are not why custody exists.** The webapp signs a user's post with the user's key when
that user clicks publish — that is the whole product, and it requires the key. **The actors are the
part that acts when the user is not there**, which is a different question and the one worth being
careful about.

### The seven places software signs as a tenant with nobody watching

| # | Actor | What it signs as the tenant | Autonomous |
|---|---|---|---|
| 1 | Chaplain | the **site** registration | ✅ daily (after each Clerk sweep) |
| 2 | Chaplain | a **post** registration |✅|
| 3 | Chaplain | a **comment** registration |✅|
| 4 | Chaplain | a **tag** registration |✅|
| 5 | Chaplain | a **`pub.polis.follow.announced`** event |✅|
| 6 | Medic | the **epoch-0 DM messages key** |✅|
| 7 | Tailor | the same | ❌ **invoked, not scheduled** |

*(Patrol reads key **paths** for permission checks and never signs. Judge reads tenants' **public** keys
only; the one private key it reads is **its own**, to sign its published findings as itself
(`webapp/internal/hosted/judgepublish.go`). Custody declarations are signed with the **operator's** key
(`custody.go`). None of those is on this list.)*

⚠️ **Two more as-tenant signatures happen with nobody watching, and they are not actors':** the hosted
service's default-grant pass signs each tenant's Rosie grant with that tenant's key
(`rosie_grants.go`), and Rosie signs the decisions she makes under it. Both are covered in
[Rosie](#rosie--polis_rosie) below, because Rosie is the tenant's agent, not yours.

**1–5 are projections of things the tenant already authored and signed.** Re-creating a DS
registration re-asserts a fact the tenant already stated; nothing new is claimed. **#5 is the one
worth understanding**: the tenant *did* follow, the announcement was *supposed* to reach the target
and did not, and Chaplain is completing an interrupted transaction. ⭐ **Completing a statement is
not originating one.**

⚠️ **The danger there is not the act — it is a false positive in detecting that something needs
completing.** An actor that thinks a completed thing is incomplete will redo it forever. **This has
happened**: Clerk's follow/tag parity could not distinguish *retention-pruned* from *never
happened*, so Chaplain re-announced daily against a number that could never match. If you add a
parity check, diff against a **durable** source, never a stream replay.

**6–7 are the least concerning of the seven, which is the opposite of where most people expect to
land.** The DM epoch-0 keypair is *declared* server-held in the type itself (`dm/keyring.go`:
`ServerHeld`, `ServerDEK  // plaintext, bootstrap epoch only — operator-readable by design`). Key
material sounds worse than an index repair and is not, **because the boundary is explicit.**

### The test that sorts all of them, including yours

> ⭐ **Did someone other than the principal decide to do this?**
>
> **No** → no delegation. Nothing to disclose. It is the user's own software.
> **Yes** → delegation. *Then* ask whether the principal could reasonably decline:
> **could not decline** → disclosure · **could decline** → a per-tenant grant.

**Check it against the cases.** A self-hoster running `polis validate` — *they* decided; no
delegation. A self-hoster's **cron** running `polis publish` — they decided **once, in advance**,
which is still them. Your Chaplain re-registering a hosted tenant's comment — **your scheduler
decided**, so it is delegation.

⭐ **This is why the rule degenerates correctly.** A self-hoster who starts hosting one friend's site
becomes an operator with no moment at which they cross a line. **One rule that returns "nothing to
disclose" while principal and decider coincide, and starts returning "disclose" the day they
diverge, needs no second mode and no migration nobody would perform.**

### Rosie — `POLIS_ROSIE`

**Rosie is a user agent, not one of your actors.** She approves or turns away comments for each tenant,
under that tenant's own **grant** record, signing with the tenant's key and marking every act with her name
and the grant's URL. She has no key, no site and no registry entry. Full format:
[`delegation.md`](../../signet/spec/delegation.md).

**On a `polis-hosted` deployment:** `POLIS_ROSIE=on` (off unless set; a set-but-invalid value refuses to
boot).

| | Off | On |
|---|---|---|
| Default grants | none written | an idempotent boot pass gives every existing tenant a `basis: hosting-terms` grant **once**, and each signup gets one |
| Rosie decides | for nobody | for every tenant with a live grant |
| Settings → Rosie | ✅ works — saves the tenant's choice and says *"has not started yet"* | ✅ works |
| Catch-up | — | each tenant whose grant has not had its pass is loaded and woken, 2 s apart, and decides requests left pending (≤ 30 days, ≤ 200) |

⛔ **The switch goes on only after the discovery service understands the marker.** A marked relationship
request sent to a discovery service that predates the marker fails verification (401).

⚠️ **Switching it on changes every tenant's identity document once.** The pass writes an `agents` pointer
into each tenant's `.well-known/polis`, so the integrity actors see one change per tenant and re-baseline.
⭐ **Nothing heals the pointer**: no Medic or Patrol path restores `.well-known/polis` from a snapshot, and
every writer of that file keeps keys it does not model.

⛔ **Never re-issue a grant a tenant withdrew, and never cite `hosting-terms` as consent.** The pass cannot
re-issue — a withdrawal blocks every later default for that tenant — and nothing here should be worked
around. A tenant who switched Rosie off stays off.

### What custody obliges you to declare

✅ **Shipped — `pub.polis.attestation.custody` and
`pub.polis.attestation.custody-grant`.** Full record format:
[`custody.md` §12](../../signet/spec/custody.md#12-custody-of-a-tenants-key--the-tenant-half).

**The obligation, in one sentence:** if you hold a tenant's key, publish a signed `custody` declaration
about that tenant, **from your own polis site, with your own key**, saying whether you sign `as-tenant`
or `co-signed` — and keep it true. Nobody can make you; that is what makes it worth something to the
tenants of the operators who do.

**On a `polis-hosted` deployment it is one setting:** `POLIS_CUSTODY_DECLARE=on`. The service then
declares custody of every tenant key it holds — an idempotent pass at every boot, and one declaration
at each signup — on the single operator site whose actor registry it trusts. It skips that site, every
actor your registry lists, and any directory without a key. ⚠️ **Off by default**: signed claims about
every tenant should start with your decision, not a deploy. A set-but-invalid value refuses to boot.
It writes **nothing** onto a tenant's site, and logs each pass as `custody.declaration.*` events.

⚠️ **The declarations are authored records.** Nothing regenerates them — Medic and Patrol leave them
alone — and a declaration about a tenant who has since left stays standing until you withdraw it
(`polis attest withdraw` from the operator site).

The shape:

| Record | Direction | Signed with | Can you manufacture it? | What it is for |
|---|---|---|---|---|
| **custody declaration** | operator → tenant | **your own key** | ⛔ **no** | the accountable promise a tenant can test you against |
| **custody grant** | tenant → operator | the tenant's key *(which you hold)* | ⚠️ **yes** | the machine-checkable authority link a DS records |

**Neither alone works.** Operator-only is a unilateral claim about someone else — anyone could claim
custody of anyone. Tenant-only is manufacturable by whoever holds the key, so **you would be
committing to nothing.** ⭐ **Your half is the one that matters, because it is the only one you
cannot disclaim.**

⛔ **This does not reduce your power by one bit.** It makes the power *legible*, and gives the one
person who can detect misuse — the tenant — something to check against. ⚠️ **State that limit
wherever you repeat any of this.** A reader who concludes that declaring custody constrains an
operator has been misled.

⛔ **And custody is not detectable from outside.** Nobody can tell a managed site from a self-hosted
one by looking. **Do not build inference from hosting topology**, and see §5 for the consequence that
matters most.

### Grants, and why a hosted grant is not consent

✅ **The record ships; issuing one for a tenant does not.** A tenant can issue her own custody grant
with `polis attest issue`. ⛔ **`polis-hosted` issues no custody grant** — not at signup, not at boot
(`webapp/internal/hosted/custody.go` writes only the operator's declarations): a custody grant is
signed with the tenant's key, in the tenant's name, and hosted signup states nothing on a tenant's
behalf. ⚠️ **Do not confuse it with the Rosie grant** above, which `polis-hosted` does
issue (with `POLIS_ROSIE=on`): that is a different record (`pub.polis.attestation.grant`) turning on
the tenant's own agent, not a grant of custody to you.

A grant is **`scope: custodial`** — one value, no enumeration of tasks. ⚠️ **Enumerating tasks would
be a fiction of per-action consent.** Custody authorises everything the key can do, and the record
has to say that rather than imply a list it does not have. Granular revocation, the only benefit of
enumeration, is decorative anyway while you hold the key.

**`basis` records where the grant came from:**

| `basis` | Means |
|---|---|
| `hosting-terms` | ⚠️ **Nobody signed anything by hand.** It exists because the tenant accepted your terms and you hold the key. |
| `user-signed` | A present human deliberately signed it. |

⛔ **Without `basis`, a hosted grant is indistinguishable from one a present person signed, and
silently claims consent it does not carry.** That is the whole reason the field exists. You *can* lie
in it — but that is a **false signed claim**, not merely exercising authority you already hold, and
it is catchable.

⚠️ **A grant does not create your authority. It documents authority that already exists.** A tenant
on managed hosting granted you everything by choosing managed hosting. **Asking permission for
something that cannot be declined is theatre** — which is why the operational baseline (repair,
re-registration, provisioning) is a matter for *disclosure*, and only genuinely discretionary acts
need a per-tenant grant.

⚠️ **Disclosure has no revocation story, and you should say so rather than let a tenant discover
it.** A tenant cannot decline the operational baseline. *"Leave"* is an answer; it should be a
**stated** one.

### `attribution` — `co-signed` vs `as-tenant`

✅ **Shipped.**

| Value | Means |
|---|---|
| `co-signed` | The actor signed with **its own** key alongside the tenant's. |
| `as-tenant` | ⚠️ **The actor signed with the tenant's key. Full stop.** |

**Today, every use of a tenant's key on a polis deployment is `as-tenant`**, and that is what
`polis-hosted` declares. ⚠️ Actors now have identities of their own and Judge signs as itself — but
Judge never uses a tenant's key, so that does not make any deployment `co-signed`.

**What a discovery service records against it.** Every event it accepts carries `signed_by`,
`principal` and `authority` (`self` · a grant URL · `none` · `withdrawn` · `unknown`). Under
`as-tenant`, what you sign with a tenant's key records `authority: self` — accurate, and exactly why the
declaration matters: **the tenant is the only one who knows which of those she made**, and
`polis actor verify --custody <her domain>` puts your declaration beside her events.

⭐ **Declaring `as-tenant` honestly is BETTER than not declaring at all**, and the design accepts
`as-tenant` precisely so that it is. An operator who signs as the tenant **and says so** is being
honest about worse behaviour; if `co-signed` were the only legal value, that operator would simply
not declare. **Widening the honest path matters more than making the good value the only value.**

⚠️ **So do not treat `as-tenant` as an admission of a defect to be hidden until you can ship
`co-signed`.** An honest declaration of a bad state beats a delayed declaration of a good one.

---

## 2. The authority rule — which actors may act on what

⭐ **One rule generates almost every answer in this document:**

> **An actor may perform a USER-AUTHORITY action only under a tenant's grant. OPERATOR-AUTHORITY
> actions — observing, and healing state the operator itself derived — need none.**

**Plus two narrower rules that are not about authority at all**, and are easy to mistake for it:

- ⛔ **Reaper excludes actors because they are not accounts.** A **category** rule. Account lifecycle
  simply does not apply to a system actor; it is not that reaping one would be unauthorised, it is
  that an actor is not a candidate at all.
- ⛔ **Judge excludes itself because self-audit is not audit.** Judge checking another actor's site is
  fine. Judge checking Judge proves nothing.

### Applied to actor-owned sites

✅ **shipped.** Actor sites live under the tenants directory beside every tenant
(`{DATA_DIR}/tenants/{handle}`), with keys projected from `POLIS_ACTOR_KEY_<HANDLE>`
(`webapp/internal/hosted/actorkeys.go`). The mapping:

| Actor | On another actor's site | Why |
|---|---|---|
| **Rosie** | ⛔ **never** | Her actions need a **tenant's** grant, and an actor is not a tenant. **No grant can exist.** |
| **Reaper** | ⛔ **never** | Category, not authority — see above. |
| **Patrol** | ✅ **yes, and it should** | Observation needs no grant. ⭐ **Excluding it means nobody notices when Judge's site breaks.** |
| **Medic** | ✅ yes | Healing *derived* state on an actor's own site is you maintaining your own infrastructure. |
| **Judge** | ✅ on **other** actors · ⛔ on **itself** | Self-audit. |

⚠️ **Getting this wrong in the blunt direction — "actors are excluded, full stop" — is the more
dangerous mistake.** A fleet where the actors are the only unmonitored sites is exactly where a
problem hides longest.

#### ⛔ This table is the control. Directory placement is NOT — and the instinct to rely on it is backwards.

**The natural assumption is that putting an actor's site outside the tenants directory keeps the
destructive actors away from it. It does the opposite**, because the actors do not all find their work
the same way:

| Actor | Finds its work by | Consequence for a site outside the tenants directory |
|---|---|---|
| **Reaper** | ⭐ **querying the database** for unverified accounts | **The directory is irrelevant.** Reaper reaps **rows**: an actor site's row is marked `verified = true`, and that row is the whole of its protection **wherever the directory sits** — a row created the convenient way defaults to unverified and becomes a reap candidate at day 14 (`store.go`, `actor_reaper_test.go`) |
| **Patrol** · **Medic** · **Judge** | ⛔ **walking the tenants directory** | ⛔ **Silently skipped** — the three that *should* be watching are the three that stop. ✅ So actor sites sit **in** the tenants directory, and all three reach them with the same walk (pinned by Patrol's and Medic's own tests, which run with the hosted service and are not published) |

⭐ **So moving an actor's site "somewhere safe" protects it from the one actor that was never a threat
and hides it from the three that are meant to notice when it breaks.** ⛔ **Rely on the table above,
enforced by tests — not on where a directory lives.** A rule that holds only because of a path is a
coincidence, and nothing fails when it stops being true.

⚠️ **One row is not free.** Judge's fleet sweep walks every directory it finds, so *Judge ⛔ on itself*
has to be written, and it is: `POLIS_JUDGE_HANDLE` names the **one** site the sweep skips
(Judge's `SelfHandle`, in the hosted service). ⛔ It is a handle, not "every actor site" — Judge checking
the operator's site or another actor's is exactly what should happen.

### The authored-vs-derived line, which decides what may be healed

⭐ **This is the rule you will reach for most often**, and it is one rule with two opposite answers.

| | Example | May an actor write it? |
|---|---|---|
| **Derived** — recomputable from something else on the site | `did.json`, `robots.txt`, `rsl.xml`, the DID document, the key-history genesis entry | ✅ **Yes, unasked.** Generating it asserts nothing the site was not already asserting. A tampered copy can simply be **rebuilt** rather than reported. |
| **Authored** — a signed statement of someone's intent | `license.json`, `following.json`, the key-history chain past genesis | ⛔ **Never.** Writing it is you asserting the author said something she did not. |

**The tell is always: *if this were deleted, could it be rebuilt from something else?*** If yes, it
is a projection — never sign it, never treat it as a permanent address, and never let an actor's
failure to regenerate it look like the source is gone.

⚠️ **The sharpest case, because it looks like a bug and is not:** a licence that **fails
verification** is left strictly alone. Medic cannot tell an author's edit from an attacker's, and
regenerating the public surfaces from possibly-altered terms would **launder a detectable tamper into
a published one.** Patrol reports it; nobody heals it. See the
Patrol section of [the actors](../../general/concepts/actors.md).

⛔ **Same reasoning, harder consequence: a key-history chain is append-only and is never rebuilt,
even when it is wrong.** Its later entries carry signatures made by private keys that no longer
exist, so nothing on the machine could reproduce them. "Repair" could only mean deleting the entries
that fail — which erases real rotations and turns a detected tamper into a clean-looking site.

---

## 3. The two channels

**There are two audiences for what your actors do, and they are not the same people.**

| Channel | Speaks as | For | Status |
|---|---|---|---|
| `patrol.*` `medic.*` `judge.*` `clerk.*` `chaplain.*` `reaper.*`, plus the hosted service's own `custody.*`, `agent.*` and `actor.*` — see [the actors](../../general/concepts/actors.md) | **each actor, by name** | **you** | ✅ **shipped** |
| **actions-on-your-behalf** | ⭐ **one voice, always** | **the tenant** | 🔧 **design** |

⭐ **Why they are separate, and why only one of them consolidates.** You need to know it was *Medic*
— the actor name is how you find the code, the cadence and the runbook. **A tenant never needs to
know that, and telling them is worse than useless**: eight names, seven of which will never mean
anything to them, is how a notification becomes noise. The user channel therefore speaks with a
single voice for everything done in a tenant's name.

⚠️ **The user channel is ADDITIVE. It is not a rename.** ⛔ **Do not plan to retire or consolidate the
operator events when it arrives** — every actor keeps its own namespace, its own cadence and its own
alerts.

### 🔧 How the tenant would hear about it

🔧 **design.**

The mechanism is an **@-mention: a public post that happens to notify.** Those are exactly the two
properties needed, and it is tempting to treat them as separate problems:

| Need | Provided by |
|---|---|
| a permanent, public, auditable record of what was done | it is **a post** |
| the affected person actually finds out | it is **a mention** |
| no prior relationship required | mentions notify **even when neither party follows the other** |
| anyone can verify it independently | the post is public |

⭐ **The distinction that makes the whole thing safe: the record is the obligation; the notification
is the courtesy.** A user who mutes mentions has declined the courtesy and kept the record. **That is
theirs to choose, and it does not make you dishonest** — you can never guarantee a tenant *learns* of
a betrayal, only that **you are not the one hiding it**, and a public post satisfies that whether or
not it is read.

⚠️ **This is also the exact reason a mention kill switch, whenever one is built, is safe:** it must
suppress the mention event — the courtesy — and never the post, which is the record.

---

## 4. The actor registry

✅ **shipped.** You publish a signed list naming the system actors you run, so a reader can tell a real
actor from an identity shaped like one. ⛔ **It is not an allow-list and nothing enforces it** — you hold
your actors' keys, so no published list can stop one doing anything; what it buys is that deviation is
observable. The record, how it is discovered and how a stranger checks it are specified in
[Custody](../../signet/spec/custody.md).

---

## 5. What a self-hoster gets — and what they do not

⚠️ **Read this even if you only run managed hosting**, because it is the section that determines
whether your monitoring is honest.

| | Managed tenant | Self-hoster |
|---|---|---|
| **Tailor** | not used | ✅ the upgrade path — `tailor --apply` |
| **`polis validate`** | ✅ | ✅ **the same code, the same predicates** |
| **Patrol / Medic** | ✅ hosted goroutines, including Medic's daily cache upkeep | ⛔ **not published** (they run inside the hosted service); the checks they share with `polis validate`, and the cache upkeep's offline slice (blessed-cache GC) via `tailor --apply` |
| **Judge** | ✅ | ⛔ **nothing.** Hosted-only |
| **Clerk / Chaplain** | ✅ | ⛔ nothing |
| **Reaper** | ✅ | ⛔ nothing — no accounts to reap |
| **Rosie** | ✅ under each tenant's grant | ✅ **the same decisions**, in the webapp (`polis serve`), once the self-hoster issues their own Rosie grant (Settings → Rosie) — nothing is issued for them |

⛔ **Say the Judge gap plainly rather than letting someone infer it.** Every verification Signet adds
lands in Judge, and Judge is hosted-only. A self-hoster runs the same code, signs the same artifacts,
and **has no way to ask whether any of it verifies.** The countermeasure is `pkg/sitecheck`: checks
written as shared predicates are run by Judge, by Patrol, **and by `polis validate`**, so the three
cannot drift. ⚠️ **But not every check has been written that way yet**, and each one that is not
widens the gap invisibly — the hosted tests are green either way.

```bash
polis validate https://alice.polis.pub    # from anywhere, over public URLs, no operator involved
```

⭐ **The URL form is the only thing in the system that stands OUTSIDE your edge**, and therefore the
only place one question can be asked at all: *does the public `robots.txt` still say what the author
signed?* Every actor fetches over loopback and cannot see an intermediary's injections. **A tenant
who wants to check you rather than trust you needs a tool that does not run on your infrastructure**,
and this is it.

### ⛔ Absence of a declaration is NEVER a finding

🔧 **design in its specifics, but decide it now, because it constrains what you build.**

**If a self-hoster has nothing to disclose because there is no delegation, then a site with no actor
disclosure is not a worse site — it is a site with nothing to declare.** ⚠️ **And an outside observer
cannot tell those two apart**, because *"is this site managed?"* is not visible from outside.

⛔ **So no verifier you build may treat a missing custody declaration, a missing grant, or a silent
agent as a defect.** **Absence is a finding only for things that are universal.** Actor disclosure is
conditional on delegation existing, so its absence says nothing at all.

**The same discipline already runs through the shipped checks, and it is worth seeing the pattern:**
an unsigned `following.json` reports OK · an unsigned `blessed.json` reports OK · a tenant who has
stated no licence terms reports OK · a tenant with no published key chain reports OK. ⭐ **In every
case, absent means *unstated*, not *defective*** — and in every case that is the majority state.

⚠️ **Related and equally important: `none` is not `unknown`.** *"No record found"* and *"I could not
look"* are different facts, and collapsing them turns an outage into an accusation. Keep them apart
in anything you build, and never render either as "unauthorised."

---

## 6. Tenant conduct

⚠️ **You will face this in week one, and the answer is not derivable from the code.**

### ⛔ polis has no vocabulary for this, on purpose

**Policy is inbound** — *who may comment on me, what I accept.* **A licence is outbound** — *what you
may do with my work.* ⛔ **Neither governs "may this person publish on my service."**

**That is hosting terms. It is an operator concern, and it lives outside the protocol
deliberately** — your conduct rules are yours, not something polis encodes and ships to every
instance. ⚠️ **An operator who goes looking for a policy verb to ban a tenant will not find one and
may conclude the system is incomplete. It is not — it is scoped.**

### The triage: is it registered, does it reach a shared surface, does anyone follow it

**A worked example.** Suppose a tenant signs up through the ordinary hosted signup and publishes
keyword-stuffed marketing copy — **both an organic signup and SEO spam** — and the three checks come
back like this:

| Check | Answer |
|---|---|
| Registered with the DS | ✅ — its posts announce like any tenant's |
| On the public discover surface | ❌ — reaches no shared page |
| Followed by anyone | ❌ — it follows only the signup default |

⭐ **So it is socially inert**: it publishes into its own subdomain and the DS event log, and reaches
nobody else's feed. Whatever SEO value is being extracted comes from **the subdomain existing as an
indexed page, not from anything inside polis.**

⭐ **Socially inert is a materially different situation from amplified.** What you do about either is
your hosting terms' call; the difference tells you what is at stake. **Run these three checks before
deciding anything.**

### Where the actual levers are

| Lever | What it governs | ⚠️ What it does not |
|---|---|---|
| **Your hosting terms** | conduct | nothing automatic — it is a document, not a switch |
| **The signup path** | who gets in. **A per-IP limiter applies** — 3 requests/hour, shared with the auth endpoints, keyed on the Fly-provided client IP | conduct — it limits how fast requests arrive, not what anyone does |
| **Handle policy** (`hosted/handle_policy.go`) | which handles may be claimed | conduct — it is a name blocklist |
| **Reaper** | **inactivity** — day-7 reminder, day-14 reap of unverified accounts | ⛔ **not conduct, and not rate.** Do not reach for it here |
| **DS operator policies** (`deny all from all at <domain>`) | what **your DS** will ingest and index | ⛔ **not what a site publishes.** Blocking a domain at the DS removes it from your index; the site stays up and stays readable |

⚠️ **The last row is the one most likely to be misread.** A DS block is an **indexing** decision, and
if you also run the hosting, it is not the same action as suspending the account. **Decide which one
you mean.** The discovery service's side is its operator policy model — see
[Admin API](../../ds/admin/configuration.md#admin-api).
