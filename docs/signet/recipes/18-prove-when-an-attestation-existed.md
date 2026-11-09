# Prove when an attestation existed

*Recipe 18 · [the recipe book](README.md)*

*For* [Developers](../../README.md#building-on-polis) · [Reviewers](../../README.md#reviewing-the-security-and-identity-design) — *Kind* [Recipe](../../README.md#kinds-of-page) — *Component* [CLI](../../cli/README.md) — *See also* [spec](../spec/witness.md) · [recipe](09-prove-when.md)

Every command on this page is run by the test suite.

**The problem.** An attestation carries an `asserted` time, and the issuer wrote it. You want to show,
to someone who does not trust the issuer, that the claim existed **no later than** a certain moment:
*this endorsement was on the record before the dispute started.*

**What makes it work.** When a registered site issues an attestation, `polis attest issue` registers it
with a discovery service, and the service **witnesses** it: it signs the record's `current_version` and
the time by its own clock, and the site publishes that countersignature in its witness set. For an
attestation, `current_version` is the hash of the **whole** signed record, so the witness binds every
field — subject, predicate, payload and `asserted` included. Spec: [witness](../spec/witness.md).
[Recipe 9](09-prove-when.md) does the same for a post.

⛔ **Nothing ever requires a witness.** An attestation without one verifies exactly as it always did; a
witness adds an independent date, and never takes anything away.

The examples' discovery service is `ds.example`; on the public network it is `ds.polis.pub`.

## Set up a registered site, and make a claim

Registering the site once is what lets a discovery service witness its records. A hosted polis site is
already registered.

```bash
mkdir alice.example && cd alice.example
export POLIS_BASE_URL=https://alice.example
polis init --site-title "Alice"
polis register
polis --json attest issue \
  --predicate pub.polis.attestation.endorsement \
  --subject https://bob.example --subject-type identity > issued.json
URL=$(jq -r .data.url issued.json)
SITE_DIR=$PWD
```

Issuing registered the record, and the witness that came back is published in the site's witness set,
found through its identity document like everything else:

```bash
jq -r .witnesses .well-known/polis
jq -c --arg u "$URL" '.witnesses[][] | select(.url == $u) | {type, ds, witnessed_at}' \
  ".$(jq -r .witnesses .well-known/polis)"
```

```text output
/content/witness/witnesses.json
{"type":"pub.polis.attestation","ds":"https://ds.example","witnessed_at":"…Z"}
```

If registration fails — no discovery service configured, or it is unreachable — the record is still
issued, just unwitnessed; `polis attest register <id>` registers it later, and the witness then carries
that later time.

## 1 · A reader checks the witness

Everything below uses only public HTTPS, from an empty directory, as a stranger would. One command
checks the record's signature, its hash, and its witness:

```bash
cd "$(mktemp -d)"
unset POLIS_BASE_URL
polis --json validate "$URL" | jq -c '.checks[] | {id, outcome}'
```

```json output
{"id": "content.artifact", "outcome": "passed"}
{"id": "content.hash", "outcome": "passed"}
{"id": "content.witness", "outcome": "passed"}
```

```bash
polis --json validate "$URL" | jq -r '.checks[] | select(.id == "content.witness") | .detail'
```

```text output
witnessed: a discovery service countersigned these bytes at …Z, so they existed by then (the witness covers its `current_version`, which hashes the whole signed record)
```

The witness's signature is checked against the key the discovery service publishes — fetched from
the service the witness names, never from the witness itself. The detail says what the witness covers:
the record's `current_version`, which for an attestation hashes the whole signed record, not just part
of it. (For a post or comment the same detail says the body, not the frontmatter.)

## 2 · Compare it with a deadline

With the witness verified, read its date from the site's published set: the earliest witness of
**this** record's `current_version`. Timestamps are UTC with a `Z`, so they compare as strings:

```bash
VERSION=$(curl -s "$URL" | jq -r .current_version)
POINTER=$(curl -s https://alice.example/.well-known/polis | jq -r .witnesses)
WHEN=$(curl -s "https://alice.example$POINTER" \
  | jq -r --arg u "$URL" --arg v "$VERSION" \
      '[.witnesses[][] | select(.url == $u and .version == $v) | .witnessed_at] | min')
DEADLINE=2100-01-01T00:00:00Z
[[ "$WHEN" < "$DEADLINE" ]] && echo "a discovery service saw this exact claim before $DEADLINE"
```

```text output
a discovery service saw this exact claim before 2100-01-01T00:00:00Z
```

⚠️ **Read the date only after `validate` has passed the witness.** The file is the site's to edit; the
check is what tells you its entries are the discovery service's. Matching on `version` matters too: a
witness of a different `current_version` is testimony about other bytes.

## 3 · What a forged date looks like

Suppose the site edits its witness set to claim the endorsement is older than it is:

```bash
cd "$SITE_DIR"
WITNESSES=".$(jq -r .witnesses .well-known/polis)"
jq '.witnesses |= map_values(map(.witnessed_at = "2020-01-01T00:00:00.000Z"))' "$WITNESSES" > witnesses.tmp
mv witnesses.tmp "$WITNESSES"
cd "$OLDPWD"
```

The witness signature covers `witnessed_at`, so the edit breaks it — and a published witness that does
not verify fails the check, because the site is claiming testimony it does not have:

```bash exit=1
polis --json validate "$URL" \
  | jq -c '.checks[] | select(.id == "content.artifact" or .id == "content.witness") | {id, outcome}'
```

```json output
{"id": "content.artifact", "outcome": "passed"}
{"id": "content.witness", "outcome": "failed"}
```

The attestation itself still verifies: the record is Alice's and unchanged. Only the date she claimed
for it is false. The same check runs across a whole site as `content.witnesses`, which counts
attestations beside posts, comments and tags:

```bash exit=1
polis --json validate https://alice.example \
  | jq -c '.checks[] | select(.id == "content.witnesses") | {outcome}'
```

```json output
{"outcome": "failed"}
```

## What this proves — and what it does not

| It proves | It does not prove |
|---|---|
| a discovery service saw **this exact record** — every signed field — no later than the witness time, **by its own clock** | when the claim was made or signed — only that it existed by then. `asserted` is still the issuer's own claim |
| the date is not the issuer's to choose, and a site that edits it is caught | anything about the discovery service's honesty — you are trusting its key and its clock, which is one party rather than none |
| the record was presented by a party that could sign as the issuing domain | that the claim is **true**, or that the issuer is who you think — that is the attestation's own signature, DNS, and your judgment |
| | that it was not seen earlier somewhere else, or that the issuer has no older claims left unregistered — an unwitnessed record is only unwitnessed |
| | that the claim still stands — a withdrawal is a later record of its own ([recipe 3](03-withdraw-a-claim.md)) |
