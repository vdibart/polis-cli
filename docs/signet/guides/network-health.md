# What Judge could not verify

*for anyone reading a hosted polis fleet from the outside — and for the operator who runs it*

*For* [Writers](../../README.md#writing-on-polis) · [Operators](../../README.md#running-polis) — *Kind* [Guide](../../README.md#kinds-of-page) — *Component* [Hosted service](../../ops/README.md) — *See also* [concept](../concepts/verification.md)

polis.pub's Judge checks every site it hosts and publishes a signed record when a site's state
changes. This guide covers how to find those records, how to answer *"what could Judge not verify in
the last 24 hours?"*, and how to tell a Judge with nothing to report from a Judge that is not reporting.

For what a record like this may and may not claim, read [verification](../concepts/verification.md)
first. For the record format, see the [spec](../spec/attestation.md#41-integrity-is-an-observation-and-its-result-lives-in-the-payload).

---

## What Judge publishes

One [`pub.polis.attestation.integrity`](../spec/attestation.md#41-integrity-is-an-observation-and-its-result-lives-in-the-payload)
record **each time a site's health changes** — never one per check:

- `result: not-verified` when a site has failed enough checks in a row to count as unhealthy
- `result: verified` when a site that Judge reported that way has since passed enough checks in a row

Either way, the record agrees with Judge's most recent check of the site, which is the one `observed`
names. A site that counts as unhealthy but passed its latest check gets no `not-verified`, even when a
halt or a mute held one back until then. A site that failed its latest check gets no `verified`.

A healthy fleet produces **no records at all**. The records are signed with Judge's own key, live on
`judge.polis.pub`, and are registered with the discovery service.

A record looks like this:

```json
{
  "type": "pub.polis.attestation",
  "issuer": "https://judge.polis.pub",
  "predicate": "pub.polis.attestation.integrity",
  "subject": { "type": "identity", "id": "https://bob.polis.pub" },
  "payload": {
    "checks": "http_verify",
    "consecutive": "2",
    "mismatched": "1",
    "observed": "2026-09-14T12:00:00Z",
    "result": "not-verified",
    "unreachable": "0",
    "vantage": "the operator's own hosted sites on polis.pub, examined over loopback by the operator's Judge, which does not examine itself"
  },
  "asserted": "2026-09-14T12:00:03Z",
  "generator": "polis-cli-go/…",
  "current_version": "sha256:…",
  "signature": "-----BEGIN SSH SIGNATURE-----…"
}
```

Read `result` before anything else. **`not-verified` means *"could not verify, at `observed`, from
`vantage`"* — not *"this site is bad."*** `unreachable` counts things Judge could not fetch, which says
nothing about those things. `mismatched` counts things Judge fetched that did not verify.

## The last 24 hours

The question has two parts, and only one of them is a query:

- **What Judge has published** is a fact, so it is a query.
- **Which of those were in the last 24 hours** is your own choice of window. That is a filter you
  apply, not something the query language does.

### 1 · Ask the discovery service for everything Judge has issued

```bash
curl -s -H 'Accept: application/json' \
  'https://ds.polis.pub/pql/all+pub.polis.attestation.issued+from+judge.polis.pub+by+date'
```

The sentence is `all pub.polis.attestation.issued from judge.polis.pub by date`. The answer lists
**every** attestation Judge has issued — each integrity observation, and anything else Judge has signed,
such as its own agent disclosure. Each item names the record's `url` and `predicate`, but not what the
record found.

⚠️ The discovery service keeps events for **90 days**. For anything older, list Judge's own site
instead: `https://judge.polis.pub/content/pub.polis.core/index.jsonl`.

### 2 · Keep the integrity observations, and read what each one found

```bash
curl -s -H 'Accept: application/json' \
  'https://ds.polis.pub/pql/all+pub.polis.attestation.issued+from+judge.polis.pub+by+date' |
jq -r '.items[]
       | select(.payload.predicate == "pub.polis.attestation.integrity")
       | .payload.url' |
while read -r url; do
  curl -s "$url"
done |
jq -s --arg since "$(date -u -d '24 hours ago' +%Y-%m-%dT%H:%M:%SZ)" '
  map(select(.payload.result == "not-verified" and .payload.observed >= $since))
  | map({site: .subject.id, observed: .payload.observed, checks: .payload.checks,
         unreachable: .payload.unreachable, mismatched: .payload.mismatched})'
```

*(On macOS, `date -u -v-24H +%Y-%m-%dT%H:%M:%SZ`.)*

The window is applied to `observed`, the moment Judge examined the site. The discovery service's own
timestamp records when the event arrived.

⚠️ **A record with no `result` is not a pass.** The filter above keeps only `not-verified`. A record
you cannot read is one you leave out, not one you count as clean.

### 3 · Check each record yourself

Do not take the discovery service's word for a record, or this guide's. Verify it against Judge's
published key:

```bash
polis validate "$url"
```

And check the site itself, from where you are:

```bash
polis validate https://bob.polis.pub
```

⭐ **That second command is the point.** Judge says where it stood. You can stand somewhere else and
see whether you get the same answer.

## An empty answer

If step 2 prints `[]`, there are two possible reasons, and you need to know which one applies:

1. **Nothing changed state.** Judge was checking and publishing, and no site crossed a threshold.
2. **Judge was not publishing.** The empty answer then says nothing about any site.

**The query cannot tell these apart.** Check which one it is before you read an empty answer as good
news.

## Is Judge publishing?

Judge rewrites a small status document on its own site after every sweep:

```bash
curl -s https://judge.polis.pub/.well-known/polis-judge-status.json
```

```json
{
  "type": "pub.polis.judge.status",
  "publishing": "muted",
  "reason": "switch",
  "since": "2026-09-14T09:00:00Z",
  "updated": "2026-09-14T12:00:04Z",
  "note": "Judge is NOT publishing (switch). Absence of an integrity observation since `since` says NOTHING about any site."
}
```

| `publishing` | Meaning |
|---|---|
| `active` | Judge is publishing. An empty answer means nothing changed state |
| `muted` | Judge is not publishing. `reason` is `switch` (turned off by its operator), `no_identity` (it has no key to sign with) or `budget` (it has published more records in the last day than a healthy fleet ever should, and stopped). **Records you would have seen since `since` do not exist.** |
| `halted` | The failures look like one shared cause — **more than half the sites Judge checks are unhealthy at once, or three or more are failing the same check.** Judge takes that as a sign that **Judge** (or the platform all its sites share) is broken, not that the sites are, and holds back new `not-verified` records until it clears |

Check `updated` as well. Judge sweeps hourly and a sweep can take most of that hour, so an `updated`
up to about two hours old is normal. **A status that has not been updated in over two hours means Judge
has stopped sweeping**, and then the document is only telling you what was true at that point.

⚠️ **This document is not signed, and that is deliberate.** Whether Judge is publishing right now is a
current state that will change. It is not a claim to keep forever. Treat it as a hint from the host,
and confirm anything that matters with `polis validate` from your own machine.

## When Judge was wrong

Judge retracts a record by publishing a [withdrawal](../spec/attestation.md#6-a-subject-may-be-another-attestation):
a new record, issued by Judge, whose subject is the record being retracted. It shows up in the same
query under the event type `pub.polis.attestation.withdrawn`:

```bash
curl -s -H 'Accept: application/json' \
  'https://ds.polis.pub/pql/all+pub.polis.attestation.withdrawn+from+judge.polis.pub+by+date'
```

The original record stays published and still verifies. **If you saved a copy earlier, the withdrawal
does not update it.** You find out it was retracted only by checking again.
