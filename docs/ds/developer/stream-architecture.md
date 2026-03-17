# Discovery Stream Architecture

Polis solved content ownership: files, signatures, static hosting. Your posts live on
your domain. Nobody can take them down or alter them.

But content ownership doesn't give you social. Social requires knowing what happened
*across the network*. Alice published a new post. Bob followed Carol. A blessing was
granted on a thread you're watching. These are *events*, and without a way to learn
about them, every polis site is an island.

The Discovery Stream is the missing piece. It's an ordered event log that carries social
signals across the network—without building a social platform.

---

## Two Services, Two Questions

Polis discovery is split into two complementary services:

| Service | Question it answers | Data shape | Access pattern |
|---------|-------------------|------------|----------------|
| **Discovery Service** | "What exists?" | Structured records (sites, posts, comments) | Point lookups, filtered queries |
| **Discovery Stream** | "What happened?" | Ordered events (follows, publishes, blessings) | Sequential reads from a cursor |

The Service is the system of record. It stores the current state of things: which sites
are registered, which posts exist, which comments are pending or blessed. Mutations go
through the Service—register, publish, beseech, grant, deny.

The Stream is the social signal. It records that something *happened*, in order. Alice
followed Bob. Carol published "On Gardens." A blessing was granted on thread #47. The
Stream doesn't store state—it stores transitions. Clients build their own state from
the event sequence.

**Why separate them?** Different access patterns, different scaling characteristics,
different trust models. The Service handles point queries with strong consistency (is
this site registered? what's the current version of this post?). The Stream handles
sequential reads that tolerate eventual consistency (what happened since cursor 4521?).
Conflating them would compromise both.

---

## Design Principles

Seven principles governed every decision in this system.

### 1. Stream is the only source of truth for social signals

There are no server-side follower tables, friend graphs, or activity feeds. The events
table has exactly six columns: `id`, `type`, `created_at`, `actor`, `signature`, `payload`.
Everything else—follower counts, activity timelines, notification badges—is computed
client-side from the raw event sequence.

This is a feature, not a limitation. If the server maintained a follower count, it would
be the server's assertion about your followers. With client-side projection, your follower
count is *your computation* over events you can independently verify.

### 2. All event emission happens inside server-side handlers

There are two emission patterns, and both happen server-side:

**Side-effect emission.** The Service mutation handlers (`POST /v1/content`,
`POST /v1/relationships`) emit events as a non-fatal side effect after the primary
operation succeeds. The client doesn't need to know about the stream—it just calls the
Service, and the event happens automatically.

**Explicit emission.** Clients call `POST /v1/stream` with a signed payload. This is
itself a handler that validates signatures and inserts the event. Follow/unfollow
events use this pattern—there's no Service mutation for "follow," so the client publishes
the event directly.

In both cases, the actual INSERT into the events table happens server-side. Clients never
write to the events table directly.

### 3. Fire-and-forget

Primary operations always succeed regardless of stream health. If the events table is
down, your post still publishes. If the stream publish call fails, your follow is still
recorded locally. Stream emission is wrapped in catch blocks and logged as warnings.

This is non-negotiable. The stream is a social convenience, not a dependency. Every
polis feature that works today continues to work if the stream disappears.

### 4. Signed everything

Every event carries the original Ed25519 signature from the actor who caused it. For
side-effect events (publish, bless, deny), this is the signature from the original
Service mutation. For explicit events (follow, unfollow), the client signs a canonical
payload before publishing.

This means any client can independently verify any event. You don't have to trust the
stream operator—you can check the signatures yourself. The verification chain goes all
the way back to the actor's `/.well-known/polis` public key.

### 5. Operator sovereignty

Each discovery service operator sets their own moderation policy. The architecture provides
mechanisms—domain blocking, type blocking, allowlist/blocklist modes, event purging—but
prescribes no policy. One operator may run a permissive stream open to all registered sites.
Another may maintain a strict allowlist. Both are valid deployments of the same protocol.

What operators *cannot* do: block core `pub.polis.*` event types (they're essential to
protocol operation), or modify event content after insertion (events are immutable once
written).

### 6. Client-side projections

Clients maintain their own local state by projecting events. Each projection tracks its
own cursor position and materializes whatever state it needs. The follow projection
maintains a follower set. An activity projection maintains a timeline. A hypothetical
book-club projection maintains a reading list.

The projection pattern is always the same:

```
load cursor → query stream → process events → save state → advance cursor
```

Different clients can project the same events differently. Privacy follows naturally:
the server doesn't know what you're tracking.

### 7. Namespace extensibility

Core event types live under the `pub.polis.*` namespace. Third-party types use reverse-domain
namespacing (`com.bookclub.recommendation`, `org.writers.prompt`) and pass through the
stream untouched. No registration or permission required—if your site is registered and
your signature is valid, you can publish events with any non-`pub.polis.*` type.

---

## The Event Model

Every event in the stream has the same shape:

```json
{
  "id": 4521,
  "type": "pub.polis.post.published",
  "created_at": "2026-02-08T14:30:00Z",
  "actor": "alice.com",
  "signature": "-----BEGIN SSH SIGNATURE-----\n...",
  "payload": {
    "url": "https://alice.com/posts/20260208/on-gardens.md",
    "version": "sha256:abc123...",
    "title": "On Gardens"
  }
}
```

The `id` is a monotonically increasing bigint. It serves as the cursor—clients request
"give me everything after ID 4521." The `type` determines what the event means. The
`actor` is the domain that caused the event. The `signature` enables independent
verification. The `payload` is type-specific data.

### Core Event Types

| Type | Emitted by | Actor | Payload |
|------|-----------|-------|---------|
| `pub.polis.post.published` | `POST /v1/content` (new post) | Author domain | url, version, title |
| `pub.polis.post.republished` | `POST /v1/content` (update post) | Author domain | url, version, title |
| `pub.polis.post.removed` | `POST /v1/content/unregister` | Author domain | url, type |
| `pub.polis.comment.published` | `POST /v1/content` (new comment) | Commenter domain | url, version, in_reply_to, root_post, target_domain, source_domain |
| `pub.polis.comment.republished` | `POST /v1/content` (update comment) | Commenter domain | url, version, in_reply_to, root_post, target_domain, source_domain |
| `pub.polis.comment.blessing.requested` | `POST /v1/content` (beseech) | Commenter domain | comment_url, in_reply_to, root_post, target_domain, source_domain |
| `pub.polis.comment.blessing.granted` | `POST /v1/relationships`, auto-bless | Post author domain | source_url, target_url, action, target_domain, source_domain |
| `pub.polis.comment.blessing.denied` | `POST /v1/relationships` | Post author domain | source_url, target_url, action, target_domain, source_domain |
| `pub.polis.follow.announced` | `POST /v1/stream` (client) | Follower domain | target_domain |
| `pub.polis.follow.removed` | `POST /v1/stream` (client) | Unfollower domain | target_domain |
| `pub.polis.site.registered` | `POST /v1/sites` (new) | Site domain | domain, registry_url |
| `pub.polis.site.reregistered` | `POST /v1/sites` (existing) | Site domain | domain, registry_url |
| `pub.polis.site.key_rotated` | `POST /v1/sites/keys/rotate` | Site domain | old_key_id, new_key_id |

**Auto-bless payloads** include additional fields: `auto_blessed`, `bless_reason`,
`policy_rule`, `policy_source`, `blessed_by`, `source_domain`, `target_domain`
(and conditionally `fallback_reason`).

### Canonical Signing

For explicit events (those published via `POST /v1/stream`), the signed payload is:

```
JSON.stringify({ type: eventType, payload: payloadObject })
```

For side-effect events, the signature comes from the original Service mutation (e.g.,
the post registration signature, the blessing grant signature).

### Third-Party Event Types

Any registered actor can publish events with custom types:

```json
{
  "type": "com.bookclub.recommendation",
  "actor": "carol.com",
  "payload": {
    "book": "Braiding Sweetgrass",
    "review_url": "https://carol.com/posts/20260205/sweetgrass.md"
  }
}
```

The stream accepts these without modification. Clients that understand the type can
consume them; clients that don't will ignore them. No coordination with the stream
operator is needed. This is how the polis social layer extends beyond what its creators
imagined.

---

## Client-Side Projections: Local Sovereignty

This is the architectural insight that separates polis from conventional social platforms.

Conventional: the server maintains a followers table, an activity feed, notification
counts. When you ask "how many followers do I have?" you're asking the server to look
up a number it maintains. You trust the server's answer because you have no alternative.

Polis: the server maintains an ordered event log. When you ask "how many followers do I
have?" your client reads follow events from the stream, filters for your domain, deduplicates,
and counts. The answer comes from your computation over independently verifiable data.

This matters because:

- **Your projections are yours.** No server can claim you have fewer followers than you do.
  No algorithm can suppress events. You see what happened, in order.
- **Different clients project differently.** A mobile app might show a simple follower count.
  A desktop app might show a timeline. An analytics tool might track trends. Same events,
  different views—no API changes needed.
- **Privacy.** The server doesn't know what you're projecting. It just serves events
  sequentially. Whether you're tracking followers, building an activity feed, or computing
  custom metrics—the server can't tell.
- **Resilience.** Projections survive server changes. If the operator restructures their
  backend, your projection keeps working as long as the event format is stable.
- **The replay guarantee.** Reset your cursor to 0, replay all events, arrive at the same
  state. This is testable, debuggable, and auditable.

### The Projection Loop

Every projection follows the same pattern:

```
1. Load cursor position from disk
2. Query stream for events since that cursor
3. Load existing materialized state (if any)
4. Process new events through the handler → updated state
5. Save updated state to disk
6. Advance cursor
```

The handler is the only piece that varies. Everything else is generic infrastructure.

### ProjectionHandler Interface

```go
type ProjectionHandler interface {
    TypePrefix() string                    // "pub.polis.follow"
    EventTypes() []string                  // ["pub.polis.follow.announced", "pub.polis.follow.removed"]
    NewState() interface{}                 // &FollowerState{}
    Process(events []discovery.StreamEvent, state interface{}) (interface{}, error)
}
```

Adding a new projection means: implement this interface, register the handler, add an
API endpoint that runs the projection loop and reads the state. The loop itself, the
cursor management, the file I/O—all reused.

The webapp uses a `SyncHandler` variant for its unified sync loop:

```go
type SyncHandler interface {
    Name() string                                    // Human-readable handler name (for logging)
    EventTypes() []string                            // Event types this handler processes
    Process(events []discovery.StreamEvent) HandlerResult
}

type HandlerResult struct {
    FilesChanged bool  // triggers RenderSite
    NewItems     int   // count of items created (notifications, feed entries, etc.)
    Error        error // non-fatal; logged but doesn't block other handlers
}
```

`SyncHandler` is simpler than `ProjectionHandler`—it manages its own state internally
and reports what changed via `HandlerResult`.

### Stateful vs. Stateless Projections

Some projections need materialized state. The follow projection maintains a set of
follower domains—it needs to know the current set to correctly handle add/remove.

Other projections are stateless. An activity timeline doesn't need to remember previous
events—it just renders whatever comes back from the stream query. The cursor position
is the only state.

The Store supports both: `SaveState/LoadState` for stateful projections, `GetCursor/SetCursor`
for all projections.

---

## The Stream Store

Projection state lives on disk, namespaced by discovery service domain and bundle:

```
.polis/ds/
└── ds.polis.pub/
    └── pub.polis.core/
        ├── config/                        # User preferences (survives resets)
        │   ├── notifications.json         # Notification rules, muted domains
        │   └── feed.json                  # Staleness, max items, max age
        └── state/                         # Computed/derived (safely deletable)
            ├── cursors.json               # Per-projection cursor positions
            ├── pub.polis.follow.json       # Materialized follower set
            ├── pub.polis.blessing.json     # Blessings state
            ├── pub.polis.notification.jsonl # Notification entries
            └── pub.polis.feed.jsonl         # Feed items
```

### Why namespaced by discovery service domain?

Because a polis site might connect to multiple discovery services in the future. Each
service has its own event sequence with its own cursor space. Namespacing by domain
prevents cursor collisions. No hashing—the domain name is the directory name, readable
and debuggable.

### Cursor file format

```json
{
  "cursors": {
    "pub.polis.follow": {"position": "4521", "last_updated": "2026-03-01T00:00:00Z"},
    "pub.polis.notification": {"position": "4600", "last_updated": "2026-03-01T00:00:00Z"}
  }
}
```

Each projection type tracks its own cursor independently. The follow projection might
be caught up to event 4521 while the notification projection has advanced to 4600. This
is by design—projections that process different event types naturally advance at different
rates. State filenames match their cursor key (e.g. cursor `pub.polis.feed` → file
`pub.polis.feed.jsonl`).

### State file format

Projection-specific. The follow projection stores:

```json
{
  "followers": ["alice.com", "bob.com"],
  "count": 2
}
```

No file means no state—the projection starts from scratch (using `NewState()`).

---

## Security: Eight Layers Without Gatekeeping

The tension: an open system where anyone can publish events, combined with the reality
that some actors will be malicious. The resolution: layered defense that provides safety
without requiring centralized approval.

| Layer | Mechanism | Enforced by |
|-------|-----------|-------------|
| 1. Registration gate | Actor must be registered with discovery service | `POST /v1/stream`, emitEvent |
| 2. Signature verification | Ed25519 signature over canonical payload | `POST /v1/stream` |
| 3. Namespace restriction | `pub.polis.*` types restricted to server-side emission | `POST /v1/stream` validation |
| 4. Rate limiting | 100 events/hour/actor | `POST /v1/stream` |
| 5. Payload constraints | JSON object, < 8KB serialized | `POST /v1/stream` validation |
| 6. Operator controls | Domain/type blocking, mode switching, event purging | Admin handlers |
| 7. Community reporting | (Deferred—designed but not yet implemented) | — |
| 8. Client-side filtering | Clients can ignore any event type or actor | Client code |

### Operator Controls

Operators have six control surfaces:

- **Block a domain** — all events from that actor are rejected (scope: stream only or full)
- **Block an event type** — all events of that type are rejected
- **Set stream mode** — blocklist (default: everything allowed, exceptions blocked) or
  allowlist (default: everything blocked, exceptions allowed)
- **Purge events** — hard-delete events by actor, type, or time range
- **View current blocks** — inspect the current enforcement state
- **Public policy statements** — operators publish per-content-type policies at
  `/policies/rules.jsonl`, declaring what they allow/deny and under what conditions
  (e.g. `allow pub.polis.comment from all`, `emit pub.polis.comment.blessing from following`).
  Clients and other services can fetch these to understand the operator's stance before
  interacting.

What operators *cannot* do:

- Block core `pub.polis.*` event types—they're essential to protocol operation
- Modify event content after insertion—events are immutable
- Forge signatures—events carry the original actor's signature

The philosophy: provide mechanisms, not policy. The operator decides what's acceptable
for their community. The architecture enforces whatever they decide.

---

## What This Makes Possible

### Follow someone → their posts appear in your feed

Alice follows Bob. Her client publishes a `pub.polis.follow.announced` event to the stream.
Alice's feed aggregator already checks Bob's site directly (via RSS-like polling). But
now the stream also carries Bob's `pub.polis.post.published` events, giving Alice real-time
awareness of new content across the network.

No algorithm. No platform intermediary. Just events flowing through a stream and a client
projecting them locally.

### Your follower count is a verifiable computation

Bob opens his webapp. The follower count handler queries the stream for `pub.polis.follow.*`
events, filters for those targeting `bob.com`, and materializes the current follower set.
The count is the length of that set.

Bob can replay from cursor 0 and arrive at the same number. He can inspect every event
that contributed to the count. If Alice unfollows, the `pub.polis.follow.removed` event
removes her from the set. No server assertion—just deterministic computation over
signed events.

### A book club creates custom event types

Members publish `com.bookclub.recommendation` events with book titles and review links.
Their clients subscribe to the type and build local reading lists. The stream carries
these events alongside core polis events—no permission needed, no coordination with
the operator, no protocol changes.

### Different operators, different policies

Community A runs a permissive discovery service: any registered site can publish any
event type. Community B runs a strict service: allowlist mode, only approved event types.
Both use the same protocol, the same client software, the same event format. The operator
policy is the only difference.

Users choose which discovery service to connect to. Or they connect to multiple. The
stream store namespaces by domain, so there's no collision.

### An AI agent consumes the stream

The same events that power your follower count and activity feed are available to any
consumer. An AI agent could build cross-author topic graphs, recommend conversations,
or detect emerging discussions—all from the public event stream, with no special API
access.

---

## Relationship to Other Documents

| Document | Relationship |
|----------|-------------|
| [MANIFESTO.md](MANIFESTO.md) | The philosophical foundation—why content ownership isn't enough, why social needs to work differently |
| [discovery-api-spec.md](discovery-api-spec.md) | The formal API contract for both the Service and Stream—endpoint definitions, request/response schemas, error codes |
| [SECURITY-MODEL.md](SECURITY-MODEL.md) | The cryptographic foundations—Ed25519 key management, signature verification, trust model |

---

## Appendix: Event Flow Diagrams

### Side-Effect Emission (Publish)

```
Client                    Discovery Service              Events Table
  |                              |                           |
  |  POST /v1/content            |                           |
  |  {url, version, sig, ...}    |                           |
  |----------------------------->|                           |
  |                              |  UPSERT posts_metadata    |
  |                              |-------------------------->|
  |                              |                           |
  |                              |  INSERT event             |
  |                              |  type: pub.polis.post.published
  |                              |  (fire-and-forget)        |
  |                              |-------------------------->|
  |                              |                           |
  |  201 {success: true}         |                           |
  |<-----------------------------|                           |
```

### Explicit Emission (Follow)

```
Client                    POST /v1/stream              Events Table
  |                              |                           |
  |  POST /v1/stream             |                           |
  |  {type, actor, payload, sig} |                           |
  |----------------------------->|                           |
  |                              |  Verify registration      |
  |                              |  Verify signature         |
  |                              |  Check blocks             |
  |                              |  Check rate limit         |
  |                              |                           |
  |                              |  INSERT event             |
  |                              |  type: pub.polis.follow.announced
  |                              |-------------------------->|
  |                              |                           |
  |  201 {event_id: 4521}       |                           |
  |<-----------------------------|                           |
```

### Client-Side Projection (Follower Count)

```
Client                    Stream (GET /v1/stream)      Local Disk
  |                              |                           |
  |  Load cursor                 |                           |
  |<---------------------------------------------------------|
  |  cursor = "4500"             |                           |
  |                              |                           |
  |  GET /v1/stream?since=4500   |                           |
  |  &type=pub.polis.follow.*    |                           |
  |----------------------------->|                           |
  |                              |                           |
  |  {events: [...], cursor: "4521"}                         |
  |<-----------------------------|                           |
  |                              |                           |
  |  Process events:             |                           |
  |  +alice.com, -carol.com      |                           |
  |                              |                           |
  |  Save state + cursor         |                           |
  |---------------------------------------------------------→|
  |  {followers: [alice, bob], count: 2}                     |
  |  cursor = "4521"             |                           |
```
