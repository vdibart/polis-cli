# The Vision of polis

> **polis** *(noun)* · from the Ancient Greek **πόλις**, *city* — understood not as its walls or buildings but as its citizens and the public life they made in common. The root of *politics*, *metropolis*, and *cosmopolitan*.

## Why "polis"

Picture the internet as a polis — a place that belongs to the people in it.  A living, breathing public square that's open to everyone but owned by no one. The web could be this again, built on a very simple foundation — a domain, and a private key. A key to prove that what you make is yours; a domain to give it a place to stand. Hold both, and the public square is open to us again.

## The roadmap

Today polis takes the shape of a social network, but in truth that's the most convenient beachhead. The vision extends out well beyond today's fleet of same-same social networking sites. This is how we get there:

1. **Create an internet-wide identity** — one that belongs to the person, not the platform. *(The mechanism: pair a private key with a domain.)*
2. **Put it within everyone's reach** — make that identity as easy to get, and to keep, as an email address.
3. **Make it worth holding** — give people something to do with it *today*, not someday.
4. **Let the network take hold** — reach the density where it has gravity of its own.
5. **Let the identity consolidate** — one identity you carry across the whole web, instead of an account per app.
6. **Invert the model** — applications stop holding you and start asking to read you.

We are between steps three and four. Here is what each step means, why it matters — for you and for the web — and why none of it is a pipe dream.

## Step one — create an internet-wide identity

The internet never got a real identity layer of its own. It has *addresses* — domains, email — but your *identity*, the thing that says "this is me, and I made this," was left to each platform to issue, hold, and revoke. So everyone ended up with a drawer full of identities, none of them theirs. The first step is to give the internet the identity layer it skipped: one that belongs to the person.

The mechanism pairs two ordinary things. A **domain** is a web address you hold — `you.com`, `alice.blog` — rented from a registrar or handed to you by a host. A **keypair** is two matched halves: a secret one you keep, and a public one you publish. You *sign* a piece of writing with the secret half — a small cryptographic stamp — and anyone holding the public half can confirm two things at once: that it came from you, and that not a character has changed since. No company sits in the middle; the math is the proof.

Put those together — a key that signs, a domain that addresses, a published public half anyone can check against — and you have an identity that is yours in a way a platform account never is. It can't be issued, because you made it. It can't be revoked, because no one granted it.

The conceptual shift is starkest in miniature. Take alice. On Instagram she is `instagram.com/alice`. Her identity is quite literally scoped to their domain. It is meaningless beyond the confines of Instagram. So she keeps another identity on LinkedIn and another on Reddit and another on Hacker News...

On polis she is `alice.polis.pub` (default managed hosting), or `alice.otherpolisprovider.com` (an independent managed host), or `alice.com` (self-hosted). She is a place, not a record. She is the destination, not the sidepath.

Concretely, on day one, with no network at all, this gives you a website you own outright — your writing, your identity, your archive, as plain files on a domain no company serves to you and no company can take down. That is true for a single person. Everything after this step is what happens when many people hold the same thing.

## Step two — put it within everyone's reach

An identity only matters if ordinary people can actually hold it. So a key and a domain have to be as easy to come by as an email address — no server to run, nothing to configure, no cryptography to understand. That is what the hosted on-ramp, `polis.pub`, is for: the easy first address, a showcase and a waypoint, never the destination. The destination is the protocol itself, and, when you want it, a domain of your own.

But "easy" must never quietly mean "we kept your keys and you didn't notice." The promise has to hold all the way down the ladder of convenience — identical content, identical signatures, identical export — whether you read RFCs for fun or just want to write.

### Meeting everyone where they are

Ownership that only a developer can claim isn't ownership; it's a hobby. So the same guarantee has to reach the careful tinkerer and the person who only wants to post, through a ladder of convenience that never changes what's underneath.

| Level | Archetype | Key Location | Content Location | What polis provides |
|-------|-----------|--------------|------------------|---------------------|
| 1 | **Builder** | Local, self-managed | Local, self-hosted | Spec + CLI + reference impl |
| 2 | **Developer** | Local | Local + deploy templates | CLI + templates + discovery |
| 3 | **Power User** | Local (guided) | Local (guided) | Installer + guided setup |
| 4 | **Technical Writer** | Local (in app) | Local (visible folder) | Desktop app wrapping CLI |
| 5 | **Blogger** | Browser/local | Managed (markdown files) | Web app + storage backend |
| 6 | **Burned Before** | Custodied (encrypted) | Managed | Full social + visible safety net |
| 7 | **Casual** | Custodied (encrypted) | Managed | Turnkey, indistinguishable from platforms |

The two ends look nothing alike and are identical underneath. **Level 1** is raw CLI and manual everything. **Level 4** wraps that same CLI in a desktop app whose files sit in a normal, browsable folder — the moment a careful person realizes the data was theirs all along. **Level 6** is mobile-first social with a visible safety net: the "Download Everything" button matters less for being clicked than for being *seen*. **Level 7** is a username, a password, and a post box indistinguishable from any platform — and the promise still holds, because the data is still markdown and the key is still theirs.

What keeps that true is a short list of things that must hold at *every* level, for *every* user:

1. **Content is always markdown files** — never a proprietary format or a database schema.
2. **Signatures are always the same format** — made the same way, verified the same way.
3. **Keys are always exportable** — even custodied keys can be downloaded, rotated, and content re-signed.
4. **Export produces identical output** — a Level 7 export is byte-for-byte usable at Level 1.
5. **The CLI is always the foundation** — every layer above is calling it, or faithfully reimplementing it.

These aren't features to be reprioritized later. They're the terms of the deed.

## Step three — make it worth holding

Nobody signs up for a foundation. People come for something to do and someone to do it with — and that is what makes holding your own identity worth it *now*, instead of in some better future.

So the things you already do online get rebuilt on owned ground. **Publishing** becomes signing files to your domain. **Following** becomes a list of URLs, with no algorithm deciding what you see. **Conversation** becomes linked posts across domains, where no single party owns the thread.

That last one needs care, and it's where the **blessing** model comes in: anyone can reply to your work — their reply lives on *their* domain, signed by *their* key — and you choose which replies your own site amplifies. Nothing is censored; the reply exists whether you bless it or not. What you control is your own front page, not other people's speech — which is [why polis says *bless*, not *moderate*](reference/in-defense-of-bless.md). The connective tissue is the **Discovery Service** — a thin coordination layer that stores metadata about content on the network, never the content itself, so people can find and follow each other across domains. Most will use the canonical one; some will run their own; some will use none, and nothing breaks.

The social experience is what brings people in. The ownership is what they keep.

## Step four — let the network take hold

This is the hard step, and the honest one. A network with no one in it is useful to no one, and the door only swings once enough people are already through it. Step four is where most attempts at this have died. Everything past it is earned, not promised.

What makes the bet survivable is a property most of those attempts never had: polis is useful *before* step four, and useful to exactly one person. A social network with no one in it is nothing; a polis site with no one else in it is still your website, your signed archive, your owned identity — complete, and worth having on its own. The network is upside, not a precondition. So the work keeps paying out even if the crowd is slow to arrive — and when it comes, it can come by feeding the networks that already exist rather than waiting to replace them.

## Step five — let the identity consolidate

Once the network is real, the domain-and-key you've been carrying stops being a technical detail and becomes something larger: your one identity online. Not an account per app — the drawer of logins from step one, finally collapsed into a single self you hold. It is simply *who you are*, everywhere — portable, provable, yours.

You already know the shape of this. `you@gmail.com` is a name with a place on the other side of it: recognizably yours, routable by other systems, handed out as shorthand for "this is where to find me." Email got one thing right that the platforms later un-learned — identity as an address, not the property of any single app.

Polis is that shape with the missing piece restored. `you@gmail.com` you rent; the provider holds the account, and your name ends the day they close it. `you.polis.pub` you can walk away from: your key and your writing come with you, and because your identity is the key — not the address — you are still you at the next one. The address can change (and old links to the old one break); the identity cannot be repossessed. Email made your name an address. Polis makes your name something you hold the deed to.

## Step six — invert the model

Here is where it stops being a better way to publish and becomes a different arrangement entirely.

Today the application is the noun and you are a filter within it. You keep one self at each address that will have you, each holding its own slice of who you are, each the property of the company that runs it. Owning a key and a domain turns that around. **You become the noun; applications become verbs.** There is one you — a key and a body of signed content at your domain — and applications no longer keep an account *of* you. They ask for access *to* you and render a view. You don't log into them; they check in with you.

This isn't a leap the [architecture](concepts/architecture.md) has to make someday. It is the pattern it already runs, pointed outward: a theme is one rendering of your content, another theme is another, and the same signed files can be read by any client that speaks the protocol. The only open question is what would make *outside* applications start doing the same.

**What flips it.** Not goodwill, and not our say-so — incentive. A company whose business is holding your data will not volunteer to become a window onto data it no longer holds. The flip comes when the arithmetic changes: when enough people keep their identity and content on their own ground that the writing worth reading increasingly lives *outside* any single platform's walls. Past that point, an application that reads user-owned content begins each day with the whole network's work and audience available to it, while one that hoards begins with only what it can capture and cage. When reading what people already own out-competes locking them in, the first movers are new applications built for it — and the incumbents follow not because they had a change of heart, but because the cost of staying closed finally exceeds the cost of opening. That threshold is step four. The inversion is earned by adoption, not a switch we get to throw.

**What changes after.** The relationship between people and software stops being one of captivity. Leaving an application costs nothing, because your data never moves — the app you quit simply stops rendering content that was never its to keep. Competition shifts from *who can lock in the most users* to *who renders the best experience over the same open content*. New applications launch without a cold start, reading a network of content and audience that already exists. And deplatforming loses its teeth: an application can refuse to show you, but it cannot erase you — you are still at your address, signed, readable, findable. You grant access and you revoke it; the app that abuses the privilege loses the privilege, not you losing your work.

There is a consequence at the scale of the whole web, too. Because content is already separated from its presentation and openly readable — public files served over ordinary HTTPS, no login wall, no API key between a reader and the words, person or machine — the network behaves like one structured body of content the size of the internet, that no one administers. (Private content stays private; this is the public tier, by design.) It is exactly the substrate that software agents should read from and write to as well — under consent their owner grants and revokes, never a platform's.

**What polis has to build to make it real.** This is the honest part: the inversion is not free, and most of the remaining work lives here.

- **A consent layer.** Scoped, revocable permission — "this application may post comments as me, and nothing else" — granted in pieces and taken back at will. The reassuring part is that this rides standard web authorization, not new cryptography to invent.
- **Signing without surrendering the key.** An application acting for you must never hold your secret key. Instead it asks *your* host — the custodian of your key, which at the higher convenience levels is already signing on your behalf — to sign and publish. The custody model exists; the delegation plumbing is the work.
- **A shared vocabulary of content types.** For applications to read and write each other's content, they need agreed definitions of what a "post," a "photo," a "review" actually is — extensible, namespaced, common. (This is also what keeps one body of content from being one undifferentiated heap: types and visibility scopes let a working note, a holiday photo, and a half-serious quip stay different things for different rooms, surfaced where you choose.) Today only the reference set ships; a real ecosystem of these is the prize that makes step six more than a demonstration.
- **An identity that survives time.** Keys rotate, domains change, people move. The identity has to stay continuously, provably *you* across all of it, or the whole structure is brittle.

None of these is a research problem. They are engineering and adoption — which is to say, they are the work, and the reason the unglamorous early steps matter.

## Why this isn't a pipe dream

It's fair to wonder, at the end of a vision like this, whether it's only a vision. Three things keep it on the ground.

**The pieces already exist.** Nothing here waits on an invention. Ed25519 signatures, static web hosting, domains, `.well-known` files, ordinary HTTPS — decades old, boring, everywhere. Polis is an assembly of proven parts, not a bet on a breakthrough.

**Every layer can be replaced — including us.** A promise of ownership is worth exactly the ease of walking away from it, and polis's is structural. The binary, the authoring app, where your files live, how your key is held, the rendering, the theme, the host, the discovery service, the reader — each is a separate concern with a swappable default. Don't like the app? The files are plain markdown. Don't like the host? They're static files; move them. Don't like the discovery service? Run your own, or none. Don't like polis? Take your key and your content and go. What *can't* be swapped is a small, fixed contract — Ed25519 signatures over canonical content, an identity file at `.well-known/polis`, a published namespace, a discovery API, the minimum shape of the core content types — and everything else is a choice. That is what makes the ownership real instead of rhetorical: you don't own what you can't leave with. (The full layer-by-layer account is in [snap-off-architecture.md](concepts/snap-off-architecture.md).)

**It pays for itself without locking you in.** The line is simple, and everything else defends it: everything below the ownership bar is free and open; above it, paid services buy convenience, never permission. Always free — your content, the baseline tools (CLI and webapp), the protocol spec, the data format, and the ability to crawl your own content and switch providers. Potentially paid — managed hosting, domain provisioning, a real-time firehose, advanced analytics, specialized discovery. The test for which side a thing falls on: *can a technical user do it for free?* If yes, the paid version is convenience, and you are never locked in.

---

None of this is a prediction that the web *will* turn this way. It is a bet that it can, and a wager on how: not a new platform to win, but a primitive to spread — a key, a domain, and the stubborn insistence that what you sign is yours. We are early. The foundation is real; the rest is the work.

---

## Appendix: how polis relates to neighboring work

Polis did not invent cryptographic identity, owned data, or applications-as-views — it assembles them in a particular way. Several efforts share parts of the same horizon; this is a quick orientation for readers who know them, not a scorecard, and several are kindred work rather than rivals.

| Project | Shared ground | Where polis is its own thing |
|---|---|---|
| **IndieWeb** | Own your domain; your site is your identity; publish on your own site, syndicate outward | The same philosophy, productized and cryptographically signed — a protocol and tools, not a kit you assemble yourself |
| **AT Protocol / Bluesky** | Portable key-based identity; signed personal data; applications as views over data you own | No personal server to run — your content is static files and a real, indexable website; human-readable markdown, not binary repositories |
| **Nostr** | Your keypair is your identity; content is signed | Identity is a domain, not a raw key; your content is durable files on your own site, not events held by relays that may drop them |
| **ActivityPub / Mastodon** | Decentralized social, no single owner | No instance to join or be bound to; your identity is not an account on someone else's server |
| **Solid** | You own your data; applications request access to it | Plain files on the open web — not a personal-server-plus-linked-data stack — lighter, and a website by default |

What none of them is, and polis is: **static files on your own domain — a real, indexable website — signed by your key, already useful with zero other users, and able to syndicate *into* the networks above rather than having to defeat them.**
