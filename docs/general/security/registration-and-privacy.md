# Registration and Network Privacy

*For* [Writers](../../README.md#writing-on-polis) · [Reviewers](../../README.md#reviewing-the-security-and-identity-design) — *About* [Identity](../README.md#identity) — *Kind* [Concept](../../README.md#kinds-of-page) — *Component* [Discovery service](../../ds/README.md) — *See also* [reference](../../ds/developer/api-reference.md) · [guide](../../cli/user/command-reference.md)

This document explains how Discovery Service (DS) registration works in polis, what privacy guarantees it provides, and what you can do with and without registration.

## Core Promise

**A polis site that has never registered is invisible to the polis network.** No other polis user can discover you, see your posts, know you exist, or know who you follow. The DS does not index you, does not serve your content to others, and does not announce your activity.

⚠️ **Unregistering later does not make a site invisible again.** It stops new writes, but what the DS already holds about the site stays — see [What Happens When You Unregister](#what-happens-when-you-unregister).

Being unregistered does **not** make you invisible to the DS server itself. When you pull feeds or browse content, your IP address hits the DS like any HTTP client, and those requests appear in server logs. The privacy guarantee is about **network visibility to other humans**, not server-side observability.

The discovery service's source is not published, so running your own is not yet an option (see [Self-Hosting a Discovery Service](#self-hosting-a-discovery-service) below).

## Reads vs Writes

Polis draws a clear line between reading and writing:

| Operation | Registration required? | What happens |
|-----------|:---------------------:|--------------|
| Browse the feed | No | Pull content from the DS, see what registered authors publish |
| Follow an author | No | Saved to your local `following.json`; the author is not notified |
| Pull notifications | No | Read-only query to the DS |
| Discover new authors | No | Browse public content indexes |
| **Publish a post** | **Yes** | Your post is indexed by the DS and visible to the network |
| **Comment on a post** | **Yes** | Your comment is sent to the post author for blessing |
| **Bless/deny a comment** | **Yes** | Blessing decisions are recorded in the DS |
| **Announce a follow** | **Yes** | The followed author sees a notification |
| **Tag content** | **Yes** | Tags are synced to the DS |
| Rotate your signing key | No | Local key rotation always works; DS is notified only if registered |
| **Unpublish (DS removal)** | **Yes** | Tells the DS to stop indexing a post |

In short: **you can read the entire network without registering.** You only need to register when you want to *participate* — publish, comment, bless, or be discoverable.

## What Happens When You Register

1. You run `polis register` (CLI) or click Register in the webapp settings.
2. Your site's public key and domain are submitted to the DS with a cryptographic attestation.
3. The DS verifies your `.well-known/polis` is publicly accessible and records your registration.
4. A **local marker file** is written at `.polis/ds/{ds-domain}/registration.json`.
5. From this point, all write operations (publish, comment, bless, etc.) are enabled.
6. Your content is indexed by the DS and visible to other polis users.

## What Happens When You Unregister

1. You run `polis unregister` or click Unregister in the webapp settings.
2. The DS removes your site's registration record, and with it the key history it holds for your domain.
3. The local marker file is deleted.
4. Write operations are blocked again. Your site returns to read-only mode.

⚠️ **Today, unregistering a site that has published content fails.** The DS cannot remove the key history while records of your content still refer to it, so it refuses the request with a server error. Nothing is removed, the CLI or web app reports the failure, and your site stays registered. A site that never published anything unregisters as described above.

⚠️ **Unregistering does not remove what the DS already holds about your content.** Its records of your posts and comments, of your relationships (follows, blessings), and the stream events your site produced are **not deleted** — they persist, as copies of your content persist in other sites' caches.

## Rate Limits for Public Reads

The DS applies IP-based rate limits to its public read endpoints. These limits exist to prevent abuse, not to restrict legitimate use. The defaults:

| Endpoint | Limit |
|----------|-------|
| Content queries (`/v1/content`, comment counts and latest comments) | 600 requests/hour |
| Relationship queries | 600 requests/hour |
| Stream / feed sync (including `/pql/…`) | 1,200 requests/hour |
| Site check, content check | 300 requests/hour each |
| Site directory (`/v1/sites/list`) | 300 requests/hour |
| Key history (`/v1/sites/keys/history`) | 300 requests/hour |
| Stream health | 300 requests/hour |
| DS public key lookup | 30 requests/hour |

These limits are per IP address, counted in fixed one-hour windows. If you exceed them, the DS returns HTTP 429 with a `Retry-After` header giving the seconds until the window resets.

For normal usage (a single site pulling its feed every few minutes), these limits are well above what you'll need.

## Local Registration State

Polis tracks registration state locally using a marker file:

```
.polis/ds/{ds-domain}/registration.json
```

This file is the **local source of truth** for "is this site registered?" All write guards check this file — no network call is needed. The marker file is:

- **Written** when `polis register` succeeds
- **Deleted** when `polis unregister` succeeds

## Self-Hosting a Discovery Service

The discovery service is closed source for now, so running your own is not documented. A polis site works without registering: you can read the whole network unregistered, as described in [Reads vs Writes](#reads-vs-writes), and register only when you want to participate.

## Technical Summary

| Principle | Implementation |
|-----------|---------------|
| Unregistered = invisible to network | All DS write operations gated on local marker file |
| Reads are always allowed | Feed sync, content queries, relationship queries work without registration |
| Follow is split | Local `following.json` always updated; DS announcement only when registered |
| Local marker, no network check | `.polis/ds/{domain}/registration.json` checked by filesystem stat |
| Unregister removes the registration | DS deletes the registration record and its key history; content, relationship and stream records remain |
| Rate-limited public reads | IP-based limits on all DS read endpoints |
