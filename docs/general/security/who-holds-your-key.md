# Who holds your key

*For* [Writers](../../README.md#writing-on-polis) — *About* [Identity](../README.md#identity) — *Kind* [Concept](../../README.md#kinds-of-page) — *Component* [CLI](../../cli/README.md) · [Hosted service](../../ops/README.md) — *See also* [spec](../../signet/spec/custody.md#12-custody-of-a-tenants-key--the-tenant-half) · [recipe](../../signet/recipes/08-survive-a-key-rotation.md)

Everything your site publishes is signed with one private key. Anyone can check that a post was signed
with your site's key and has not changed since. **No one can check who used the key.** A signature proves
which key signed, never who was holding it. So the question that matters is who holds yours.

There are two answers, depending on where your site runs.

## On your own machine

When you run `polis init`, the CLI generates the key on your machine and stores it in your site
directory, at `.polis/keys/id_ed25519`. It is never published; only the matching public key is, in your
site's `.well-known/polis`.

**What that means:**

- **You are the only holder**, as long as no copy leaves your control. A backup, a sync service, a shared
  machine or a person you gave it to is a second holder, and anything they sign looks exactly like
  something you signed.
- **You are your own backup.** Lose the only copy and nobody can recover it.
- **If you think someone else has a copy, rotate** with [`polis rotate-key`](../../cli/user/command-reference.md#polis-rotate-key).
  Your site publishes a history of every key it has held, so posts signed before the rotation keep
  verifying ([recipe 8](../../signet/recipes/08-survive-a-key-rotation.md)). ⚠️ Rotating does not make
  the old key useless: whoever holds it can still sign something that *claims* to date from before you
  rotated ([key history](../../signet/spec/key-history.md)).

## On polis.pub, or any hosted service

**The service holds your key.** It generated the key when you signed up and stores it on its servers
with your site, and its software signs with it. That is what hosting a polis site is, and it has a name:
**custody**.

**What the software signs with your key.** polis.pub's privacy policy (`polis.pub/legal/privacy`, *Your
signing key*) lists it: what you publish, comment on and follow, when you do it; your site's first
registration with the discovery service; background repairs with nobody watching, which re-register your
site and content when the discovery service's copy has gone missing and re-send follow announcements
that did not arrive; and setting up the separate key your direct messages use. In each of these the
software **signs as you**: the result is byte-identical to a signature you made yourself.

**Rosie is different, and you can switch her off.** Rosie is a user agent: software that blesses or
denies comments on your posts under your own rules, public and private. She signs with your key too, but only
under a **grant** on your site, and every decision she makes carries a mark inside the signed bytes
naming her and the grant. So her acts can be told apart from yours. You switch her off in Settings →
Rosie, which withdraws the grant; after that she decides nothing for you, and no default grant is issued
again. ⚠️ Where the service turned her on for you, the grant says so (`basis: hosting-terms`). That grant
is not your consent, and it is not terms. See [delegation](../../signet/spec/delegation.md).

**What you cannot prevent.** Nothing technical limits a key to a list. Whoever holds your key can sign
anything as you: the service, anyone who breaks into its servers, anyone with a copy of one of its
snapshots. Its background repairs carry no mark, so nothing on the wire tells them apart from you.

**What you can check.** A service can publish a signed **custody declaration** about your site, on its
own site and signed with its own key, saying that it holds your key and how it uses it. From your own
machine, needing no key:

```
polis actor verify --custody yourname.polis.pub
```

shows the service's declarations about you, the custody records published on your site, and recent events at the
discovery service signed with your key. You are the only one who knows which of those you did yourself;
something you did not do is the finding. ⛔ **None of this limits what the service can do.** A declaration
makes custody visible, so a deviation can be noticed and raised; it does not make it smaller. The same
check reads data the service may also host, so it can show you what was said, not guarantee you were
shown everything ([how the tenant checks](../../signet/spec/custody.md#125--how-the-tenant-checks--and-what-is-honestly-independent)).
A service that has published no declaration tells you nothing either way.

## Moving from hosted to your own machine

The only thing that reduces custody is holding your own key. On polis.pub: download your export from
Settings, which includes your key, run the site with the polis CLI on a machine you control, and run
`polis rotate-key` so that what you sign from then on uses a key only you have held.

What that does not do:

- **The service still has the old key** until your account is deleted, and a snapshot taken before then
  can still contain it for up to 7 days. The old key can still sign things that claim to predate your rotation.
- **Rotate soon.** Anyone holding the old key can rotate it too, and whoever rotates first controls the
  key the discovery service treats as current.
- **Your address changes.** `yourname.polis.pub` stays behind; you need your own domain, and your
  followers need to follow the new address.

## Your direct messages use a different key

Once you set a message password, direct messages are sealed under a key that only that password unlocks, in
your browser, and the service never receives it. Until you set one, the service can read them. That is a separate arrangement, with its own limits:
[direct-message encryption](dm-encryption.md).

## Deeper

- [Security model: key custody](security-model.md#key-custody): the same question for any holder, in
  implementer's terms
- [Custody](../../signet/spec/custody.md): the declaration, the custody record on your site, and exactly what a check can and
  cannot establish
- [The actors](../concepts/actors.md): the background software on a hosted service, and what each does
