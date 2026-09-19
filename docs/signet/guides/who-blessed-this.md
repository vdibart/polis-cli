# Tell whether a blessing was decided by a person or by their agent

*and, if by an agent, under what grant — from another machine, with `curl`*

*For* [Writers](../../README.md#writing-on-polis) · [Reviewers](../../README.md#reviewing-the-security-and-identity-design) — *Kind* [Guide](../../README.md#kinds-of-page) — *See also* [concept](../concepts/delegation.md) · [spec](../spec/delegation.md)

The records quoted here were fetched on 2026-09-16.

⭐ **A guide, not a recipe, and the distinction is enforced.** Every page in [the recipe book](../recipes/README.md)
is executed by `TestRecipes` in a hermetic world — a fake discovery service, sites built by `polis init` in a
temp directory — and its printed output is checked. **This page reads artifacts that are already live on the
network instead**, so it cannot be hermetic, and one of its commands reads a stream event that ages out after
about 90 days. ⛔ **Putting it under `recipes/` would make the test suite depend on production data.**

A blessed comment appears under someone's post. Two different things could have happened: the author
read it and approved it, or software working for the author applied the author's own rules and approved
it on their behalf. **Both are signed with the author's key.** What tells them apart is a marker inside
the signature, and the grant the marker cites says, in `basis`, whether the user made it or the host
issued it. This guide reads both.

⛔ **The discovery service never decides this.** It records what the author's site told it and flags what
it notices. Everything below is fetched from **the author's own site**; the discovery service appears
once, as corroboration, and only for as long as its stream retains the event.

## The records involved

| Record | Where | What it carries |
|---|---|---|
| The blessing list | the **post author's** site, `content/pub.polis.core/comment/blessed.json` | one entry per blessed comment; **signed**, so the marker is inside the signature |
| The grant | the post author's site, under its attestation mount | what the author's agent is permitted to do; signed with the author's key, and `basis` says whether the user made it or the host issued it |
| The comment | the **commenter's** site | its own `current-version`, which the blessing pins |

## Read the entry

```bash
curl -s https://discover.polis.pub/content/pub.polis.core/comment/blessed.json \
  | jq '.comments[].blessed[] | select(.url | contains("20260916"))'
```

```json
{
  "url": "https://vdibart.polis.pub/content/pub.polis.core/comment/20260916/pub-post-20260916.md",
  "version": "sha256:b1c53daab80d1b3dd298f09b91f06cdd4d29351611bd875395cf6fd2b01ef1df",
  "blessed_at": "2026-09-16T04:45:27Z",
  "agent": "rosie",
  "grant": "https://discover.polis.pub/content/pub.polis.core/attestation/20260916T030755Z-3a2fe8a07fe6c5cc.json"
}
```

`agent` and `grant` are the marker. **An entry without them was not decided by an agent** — see *What
this does not prove*.

⭐ **The same file carries the contrast.** Older entries in that list look like this:

```json
{
  "url": "https://vdibart.polis.pub/comments/20260525/pub-post-20260525.md",
  "version": "",
  "blessed_at": "2026-05-25T05:41:40Z"
}
```

Empty `version`, no marker. Those were decided by a discovery service, in a design that recorded neither
**which bytes** were approved nor **who decided**. The newer shape records both.

## Check the version pin

The pin is over the comment's **signing base**, not its raw bytes, so `sha256sum` on the file will not
reproduce it. What you can check by hand is that it matches what the comment itself publishes:

```bash
curl -s https://vdibart.polis.pub/content/pub.polis.core/comment/20260916/pub-post-20260916.md \
  | grep current-version
# current-version: sha256:b1c53daab80d1b3dd298f09b91f06cdd4d29351611bd875395cf6fd2b01ef1df
```

Equal, so **the bytes blessed are the bytes served**. If the author of the comment edits it, its
`current-version` moves and the pin does not — which is the canonical *edited since blessing* signal, and
why the pin is never regenerated from a discovery service's records.

To verify the signature rather than eyeball the field:

```bash
polis validate https://vdibart.polis.pub/content/pub.polis.core/comment/20260916/pub-post-20260916.md
```

## Read the grant the agent acted under

```bash
curl -s https://discover.polis.pub/content/pub.polis.core/attestation/20260916T030755Z-3a2fe8a07fe6c5cc.json | jq
```

```json
{
  "issuer": "https://discover.polis.pub",
  "subject": { "type": "identity", "id": "https://discover.polis.pub" },
  "predicate": "pub.polis.attestation.grant",
  "payload": { "agent": "rosie", "basis": "hosting-terms", "behaviours": "rosie/1", "provider": "polis.pub" }
}
```

⭐ **Issuer and subject are the same site, and it is the site the blessing came from.** The grant is
signed with that site's key and is about that site, whoever wrote it; `basis` says whether the user made
it or the host issued it. An agent citing a grant issued by another site is not the same claim and should
not be read as one.

`basis` says how the grant came to exist. `hosting-terms` means the hosting provider issued it under the
terms of the service, **not** that the user sat down and wrote it.

## Corroborate at the discovery service — while it lasts

```bash
curl -s "https://ds.polis.pub/v1/stream?type=pub.polis.comment.blessing.granted&limit=5" | jq '.events[-1]'
```

```
signed_by:    discover.polis.pub     the post author's key signed the decision
principal:    discover.polis.pub     …on its own behalf, not someone else's
authority:    self                   no custody grant was involved
payload.agent:        rosie
payload.grant_state:  live           the grant resolved when the DS recorded this
payload.witness:      { ds, type, actor, agent, grant }
```

⚠️ **This is corroboration, not the source.** Stream events are retained for about 90 days; the site's own
records above are permanent. If the two ever disagree, the site is the authority — a discovery service
witnesses, it does not decide.

## What this proves

- The post author's site says this comment is blessed, **over exactly these bytes**.
- The decision was made by an agent named `rosie`, acting under **a grant the author's own site
  publishes**, and both facts are **inside the signature** on the blessing list rather than beside it.
- A discovery service saw the same thing and countersigned it, recording the signer as the author and the
  authority as `self`.

## What this does not prove

- ⛔ **That the decision was a good one.** The marker says who decided and under what permission. Whether
  the comment deserved blessing is the author's judgment, and the author remains answerable for it —
  delegation moves the labour, not the responsibility.
- ⛔ **That the grant is consent the user authored.** `basis: hosting-terms` says the opposite: the
  provider issued it. It is revocable and prospective, and it applies rules the user already published.
  ⚠️ **Never cite a default grant as evidence that a user asked for something.**
- ⛔ **That an unmarked blessing is suspicious.** No marker means no agent decided — the author approved
  it in person, or their software predates the marker. Absence is not evidence of concealment.
- ⛔ **That `grant_state: live` is true now.** It is what the discovery service observed when it recorded
  the decision. A grant withdrawn afterwards does not rewrite that row, and it should not: the row says
  what was true then. To know the state now, fetch the grant.
- ⛔ **That the agent stayed inside its grant generally.** This is one act. The grant lists behaviours; an
  act outside them would carry a marker too, and catching that is the reader's job, not the marker's.

## See also

- [The delegation spec](../spec/delegation.md) — the grant record, the marker, and what a discovery service does with it
- [Recipe 5 — verify a site you do not own](../recipes/05-verify-a-site.md)
- [Recipe 9 — prove a claim existed by a given time](../recipes/09-prove-when.md) — the witness machinery this leans on
