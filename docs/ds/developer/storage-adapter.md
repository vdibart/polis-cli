# Storage Adapter

## What It Is

`StorageAdapter` is a TypeScript interface defined in `core/storage.ts` that abstracts all database operations. Core handlers call StorageAdapter methods instead of touching SQL or any database client directly. This is the single boundary between business logic and persistence.

## Why It Exists

- **Portability** -- The same business logic runs against any compatible store without changes to core handlers
- **Testability** -- Tests can use an in-memory adapter or mock without a real database
- **Future-proofing** -- Adding support for a new database (SQLite, CockroachDB, etc.) requires only a new adapter implementation

## Implementations

| File | Database | Used By |
|------|----------|---------|
| `server/postgres-storage.ts` | Any Postgres (via `postgres.js`) | Fly.io / standalone Deno server (production) |
| `_archived/supabase/supabase-storage.ts` | Supabase Postgres | Supabase Edge Functions (retired) |

> The Supabase adapter is no longer maintained — it lives under `_archived/` as a reference for anyone writing a new adapter. `server/postgres-storage.ts` is the only adapter exercised in production today.

## Interface Reference

The interface in `core/storage.ts` is the source of truth and currently exposes ~50 methods. The snapshot below groups them by purpose. Open `core/storage.ts` for exact signatures, return types, and JSDoc.

```typescript
interface StorageAdapter {
  // --- Lifecycle ---
  close(): Promise<void>                 // graceful shutdown

  // --- Sites + key management ---
  registerSite(...): Promise<RegisteredSite>
  registerSiteAtomic(...): Promise<RegisteredSite>   // upsert site + key history + attestation key id in one tx
  unregisterSite(domain: string): Promise<void>
  checkSite(domain: string): Promise<{ registered: boolean }>
  getSitePublicKey(domain: string): Promise<string | null>
  listSites(filters: SitesListFilters): Promise<{ count: number; items: SiteRecord[] }>

  // --- Keys (rotation + history) ---
  getCurrentKey(domain: string): Promise<KeyRecord | null>
  getKeyHistory(domain: string): Promise<KeyRecord[]>
  closeKey(domain: string, keyId: string): Promise<void>
  rotateKey(domain: string, newKey: KeyRecord): Promise<void>
  insertKeyHistory(domain: string, key: KeyRecord): Promise<void>

  // --- Content ---
  registerContent(...): Promise<ContentRecord>
  unregisterContent(type: string, url: string): Promise<void>
  unpublishContent(type: string, url: string): Promise<void>      // post/comment unpublish (clean break)
  checkContent(type: string, url: string): Promise<ContentRecord | null>
  queryContent(type: string, filters: ContentFilters): Promise<{ count: number; items: ContentRecord[] }>
  getCommentCounts(urls: string[]): Promise<Record<string, { total: number; blessed: number }>>

  // --- Relationships (blessings, follows) ---
  updateRelationship(...): Promise<RelationshipRecord>
  queryRelationships(type: string, filters: RelationshipFilters): Promise<{ count: number; items: RelationshipRecord[] }>
  orphanBlessingsForPost(postUrl: string): Promise<number>        // unpublish cascade: blessed → orphaned
  resetCommentBlessing(commentUrl: string): Promise<void>         // unpublish: comment’s blessing → pending

  // --- Stream ---
  publishEvent(type: string, data: any, namespace: string): Promise<EventRecord>
  queryEvents(filters: StreamQueryFilters): Promise<StreamEvent[]>
  queryEventsUnified(filters: StreamUnifiedFilters): Promise<StreamEvent[]>
  getStreamHealth(): Promise<{ mode: string; event_count: number }>
  purgeEvents(filter: PurgeFilter): Promise<number>

  // --- Admin / operator policies ---
  getOperatorPolicies(): Promise<PolicyRecord[]>                  // policies in evaluation order
  listOperatorPolicies(filters?: PolicyListFilters): Promise<PolicyRecord[]>
  addOperatorPolicy(rule: string, reason: string): Promise<PolicyRecord>
  removeOperatorPolicy(id: number): Promise<void>
  removeOperatorPoliciesByRule(rule: string): Promise<number>     // used by the block/domain convenience endpoints
  updateOperatorPolicy(id: number, patch: { active?: boolean }): Promise<PolicyRecord>

  // --- Migrations ---
  registerMigration(oldDomain: string, newDomain: string): Promise<MigrationRecord>
  queryMigrations(domain: string): Promise<MigrationRecord[]>
}
```

The legacy block-table API (`getBlocks`, `blockDomain`, `blockType`, `setStreamMode`, etc.) has been removed — all blocking is now policy-based. See `getOperatorPolicies` / `addOperatorPolicy` above.

## Writing a Custom Adapter

To add support for a new database or hosting platform:

### 1. Implement the Interface

Create a new file implementing every method in `StorageAdapter`. Each method maps to one or more SQL queries (or equivalent operations for non-SQL stores).

### 2. Handle Errors Consistently

Throw typed errors that core handlers expect:
- Duplicate inserts should throw with a `DUPLICATE` code
- Missing records should return `null` or throw with `NOT_FOUND`
- Use the error types from `core/types.ts`

### 3. Wire It Up

In your adapter's HTTP layer, instantiate your storage implementation and pass it to the core handlers:

```typescript
import { handleSitesRegister } from "../core/handlers/sites-register.ts";
import { MyStorageAdapter } from "./my-storage.ts";

const storage = new MyStorageAdapter(connectionConfig);

app.post("/sites-register", async (c) => {
  const body = await c.req.json();
  const result = await handleSitesRegister(storage, body);
  return c.json(result.data, result.status);
});
```

### 4. Test Against Core Handlers

The core handlers have no platform dependencies. You can test your adapter by running the core test suite with your implementation:

```typescript
import { createStorageTests } from "../core/tests/storage-tests.ts";
import { MyStorageAdapter } from "./my-storage.ts";

createStorageTests(() => new MyStorageAdapter(testConfig));
```

## Example: Skeleton Adapter

```typescript
import type { StorageAdapter, RegisteredSite, ContentRecord } from "../core/storage.ts";

export class SkeletonStorageAdapter implements StorageAdapter {
  constructor(private db: SomeDbClient) {}

  async registerSite(domain: string, publicKey: string, email: string, attestation: string): Promise<RegisteredSite> {
    const row = await this.db.query(
      "INSERT INTO ds_registered_sites (domain, public_key, email, attestation) VALUES ($1, $2, $3, $4) RETURNING *",
      [domain, publicKey, email, attestation]
    );
    return this.mapSiteRow(row[0]);
  }

  async unregisterSite(domain: string): Promise<void> {
    await this.db.query("DELETE FROM ds_registered_sites WHERE domain = $1", [domain]);
  }

  async checkSite(domain: string): Promise<{ registered: boolean }> {
    const rows = await this.db.query(
      "SELECT 1 FROM ds_registered_sites WHERE domain = $1",
      [domain]
    );
    return { registered: rows.length > 0 };
  }

  // ... implement remaining methods
}
```
