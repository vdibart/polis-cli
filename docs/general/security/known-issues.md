# Known integrity issues

*For* [Writers](../../README.md#writing-on-polis) · [Reviewers](../../README.md#reviewing-the-security-and-identity-design) — *Kind* [Reference](../../README.md#kinds-of-page)

This page is a public, plain-language record of known imperfections in polis's
integrity/verifiability surface — what they are, whether they affect
authenticity, and what we're doing about them. It is written to be checked: each
entry names the exact scope and the exact limit. polis's core promise is that
published content is cryptographically signed with its site's key and
independently verifiable; where reality falls short of the cleanest form of that promise, it
belongs here rather than in a footnote.

Two kinds of entry live here. Some are **defects** — a signature that is
genuinely valid but packaged in a way a third-party tool might not accept (§1).
Others are **limits** — things a signature cannot prove, however correctly it is
made (§2). Neither kind is a compromise of content integrity, but a limit is
not something a fix will remove, and it is listed so nobody reads more into a
signature than it says.

---

## 1. Zeroed signature-envelope on some early content (`polis-cli-go 0.59.0`)

**Where it stands:** served content — **repaired** (fleet-wide keyless correction applied
2026-07-16; standard `ssh-keygen -Y verify` now accepts the previously-affected
content, confirmed on real data). Discovery-service copies — deferred as harmless
(see "The DS-held copies").

### What happened

polis signs each post and comment with an Ed25519 key using the standard SSH
signature (`SSHSIG`) format. An `SSHSIG` blob carries two things: the actual
64-byte signature, and — as *metadata* — a copy of the signer's public key (the
"envelope"). One released version of the tool, `polis-cli-go 0.59.0`, had a bug:
it wiped the private key from memory a moment too early, so the public-key field
in the envelope was written as **all zeros**. The 64-byte signature itself was
computed correctly, with the real key, before the wipe.

### Why it does not affect authenticity

The embedded public-key field is **not covered by the signature** — it is a
convenience copy, not part of what was signed. Every polis verifier (the site
itself, the discovery service, the Judge and Patrol integrity actors, and the
in-browser reader) verifies each signature against the author's **real,
published key** from their `.well-known/polis`, and ignores the embedded copy.
So affected content is, and always was, **authentic and correctly signed** — an
independent audit of the full corpus confirmed every item validates against its
author's real key.

### What it does affect

If someone verifies the raw signature with **standard SSH tooling**
(`ssh-keygen -Y verify`), which trusts the envelope's embedded key, that tool
rejects affected items — because it is checking against 32 zero bytes instead of
the real key. This is an **interoperability** shortfall, not a security one: the
signature is valid; only the identity field it is *advertised under* is wrong.

### Scope

- **Only** content published with `polis-cli-go 0.59.0`. The signer was fixed in
  the next release and a regression test now guards it, so **no new content is
  affected** and the set is fixed and finite.
- Concentrated overwhelmingly in a single showcase handle; a small number of
  other sites have a handful of items each.
- Content hashes, version history, and authorship are **unaffected**.

### The fix

Because the broken field is public metadata not covered by the signature, it can
be corrected with **public information only — no private key, no re-signing**: we
splice the author's real public key back into the envelope and leave the 64-byte
signature byte-for-byte identical. After repair, the item is accepted by standard
`ssh-keygen` as well. On hosted sites this runs as a keyless, idempotent
maintenance pass (the Patrol actor detects affected items; the Medic actor
repairs them). Self-hosted operators can run the same correction with their own
tooling.

### The DS-held copies (deferred, harmless)

The discovery service keeps its own signature for each registration, produced by
the same tool, so `0.59.0` registrations carry the same zeroed envelope there
too. These are **inert**: like everywhere else in polis, they are only ever
verified against the author's real published key, never the embedded field. They
are not served as independently-verifiable artifacts. We have therefore chosen
**not** to rewrite them (they live in a database and an append-only event log,
where mutation carries more risk than the near-zero benefit). This decision would
be revisited only if a feature ever exported those copies for third-party
verification with standard tooling.

---

## 2. A signature proves the key, not the person

**Where it stands:** a limit of what any signature can prove. Not a bug, and no release
will remove it.

### What it means

Every post and comment is signed with the site's private key. When a signature
checks out, you know that **the site's key** made it, and that the content has
not changed since. You do **not** know who was holding the key.

Usually that is the author. But anyone else who has a copy of the key can sign,
and their signatures look exactly the same — there is no way to tell them apart.
Someone else has a copy whenever:

- a service generated your key for you, or keeps it for you;
- your site lives on a shared or managed machine;
- a backup, snapshot, or sync tool copies your site's private files;
- you gave the key to someone — a collaborator, a team;
- someone stole it.

Software counts too. A program that signs with your key while you are not
there — a scheduled job, a maintenance task — produces signatures identical to
the ones you make yourself.

### What it does not affect

A signature still proves the content has not been altered, and that it came
from whoever controls that site's key. Nothing about how signatures are checked
is wrong. The limit is in what the check can see.

### What you can do

- **Hold your own key.** `polis init` generates the key on your own machine. If
  you never let a copy out, you are its only holder. That is the one arrangement
  where "signed with my key" means "signed by me" — and it is something you can
  know, not something you can prove to anyone else.
- **If someone else has had a copy, rotate.** `polis rotate-key` switches future
  signing to a new key. Two limits, stated plainly: the old key can still sign
  posts that *claim* to be from before the switch; and anyone holding the old key
  can rotate too — **whoever does it first wins**, and the other cannot undo it.
  So rotate promptly.
- **Know the cost.** Being the only holder means being the only backup. Lose
  the key with no copy and nobody can recover it for you.

The precise version is in the [security model](security-model.md#what-a-signature-does-not-tell-you).

---

*Found something that looks like an integrity gap and isn't listed here? See
[`SECURITY.md`](SECURITY.md) for how to report it.*
