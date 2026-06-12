# Registration and Network Privacy

This document explains how Discovery Service (DS) registration works in polis, what privacy guarantees it provides, and what you can do with and without registration.

## Core Promise

**An unregistered polis site is invisible to the polis network.** No other polis user can discover you, see your posts, know you exist, or know who you follow. The DS does not index you, does not serve your content to others, and does not announce your activity.

Being unregistered does **not** make you invisible to the DS server itself. When you pull feeds or browse content, your IP address hits the DS like any HTTP client, and those requests appear in server logs. The privacy guarantee is about **network visibility to other humans**, not server-side observability.

If you want complete privacy from the DS server, you can run your own DS (see [Self-Hosting a Discovery Service](#self-hosting-a-discovery-service) below).

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
2. The DS performs a **hard delete** — all registration data, content records, and relationship data for your domain are permanently removed.
3. The local marker file is deleted.
4. Write operations are blocked again. Your site returns to read-only mode.
5. You are once again invisible to the polis network.

The hard delete is a **privacy promise**: unregistration is not a soft flag. The DS removes all traces of your domain.

## Rate Limits for Public Reads

The DS applies IP-based rate limits to all read endpoints. These limits exist to prevent abuse, not to restrict legitimate use:

| Endpoint | Limit |
|----------|-------|
| Content queries | 600 requests/hour |
| Stream / feed sync | 1,200 requests/hour |
| Site check | 300 requests/hour |
| Public key lookup | 30 requests/hour |

These limits are per IP address across a 1-hour sliding window. If you exceed them, the DS returns HTTP 429 with a `Retry-After` header.

For normal usage (a single site pulling its feed every few minutes), these limits are well above what you'll need.

## Local Registration State

Polis tracks registration state locally using a marker file:

```
.polis/ds/{ds-domain}/registration.json
```

This file is the **local source of truth** for "is this site registered?" All write guards check this file — no network call is needed. The marker file is:

- **Written** when `polis register` succeeds
- **Deleted** when `polis unregister` succeeds
- **Backfilled** on first startup after upgrading (one-time migration)

### Migration for Existing Sites

If you registered before this feature was introduced, the first startup after upgrading will make a one-time call to `CheckSiteRegistration` to determine your status. If you're registered, the marker file is created automatically. A flag file (`.polis/ds/{ds-domain}/registration_checked`) prevents this check from repeating.

## Self-Hosting a Discovery Service

If you want full control over your data, you can run your own DS. This gives you:

- **Complete server-side privacy** — your DS, your logs, your rules
- **Custom rate limits** — configure via environment variables
- **Custom policies** — block domains, moderate content, tune query limits

To configure your site to use a custom DS:

```bash
export DISCOVERY_SERVICE_URL=https://your-ds.example.com
export DISCOVERY_SERVICE_KEY=your-api-key  # optional
```

See [DS Deployment Guide](../../ds/admin/deployment.md) for setup instructions.

## Technical Summary

| Principle | Implementation |
|-----------|---------------|
| Unregistered = invisible to network | All DS write operations gated on local marker file |
| Reads are always allowed | Feed sync, content queries, relationship queries work without registration |
| Follow is split | Local `following.json` always updated; DS announcement only when registered |
| Local marker, no network check | `.polis/ds/{domain}/registration.json` checked by filesystem stat |
| Hard delete on unregister | DS permanently removes all domain data |
| Rate-limited public reads | IP-based limits on all DS read endpoints |
