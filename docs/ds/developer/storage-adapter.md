# Storage Adapter

## What It Is

`StorageAdapter` is a TypeScript interface defined in `core/storage.ts` that abstracts all database operations. Core handlers call StorageAdapter methods instead of touching SQL or any database client directly. This is the single boundary between business logic and persistence.

## Why It Exists

- **Portability** -- The same business logic runs on Supabase (using their JS client) and Fly.io (using postgres.js) without changes to core handlers
- **Testability** -- Tests can use an in-memory adapter or mock without a real database
- **Future-proofing** -- Adding support for a new database (SQLite, CockroachDB, etc.) requires only a new adapter implementation

## Implementations

| File | Database | Used By |
|------|----------|---------|
| `supabase/functions/supabase-storage.ts` | Supabase Postgres (via `@supabase/supabase-js`) | Supabase Edge Functions |
| `server/postgres-storage.ts` | Any Postgres (via `postgres.js`) | Fly.io / standalone Deno server |

## Interface Reference

```typescript
interface StorageAdapter {
  // --- Sites ---
  registerSite(domain: string, publicKey: string, email: string, attestation: string): Promise<RegisteredSite>
  unregisterSite(domain: string): Promise<void>
  checkSite(domain: string): Promise<{ registered: boolean }>
  getSitePublicKey(domain: string): Promise<string | null>

  // --- Content ---
  registerContent(type: string, url: string, version: string, metadata: ContentMetadata): Promise<ContentRecord>
  unregisterContent(type: string, url: string): Promise<void>
  checkContent(type: string, url: string): Promise<ContentRecord | null>
  queryContent(type: string, filters: ContentFilters): Promise<{ count: number; items: ContentRecord[] }>

  // --- Relationships ---
  updateRelationship(type: string, sourceUrl: string, targetUrl: string, status: string, metadata?: any): Promise<RelationshipRecord>
  queryRelationships(type: string, filters: RelationshipFilters): Promise<{ count: number; items: RelationshipRecord[] }>

  // --- Stream ---
  publishEvent(type: string, data: any, namespace: string): Promise<EventRecord>
  queryEvents(filters: StreamQueryFilters): Promise<StreamEvent[]>
  queryEventsUnified(filters: StreamUnifiedFilters): Promise<StreamEvent[]>
  getStreamHealth(): Promise<{ mode: string; event_count: number }>

  // --- Admin ---
  getBlocks(): Promise<{ domains: string[]; types: string[] }>
  blockDomain(domain: string): Promise<void>
  unblockDomain(domain: string): Promise<void>
  blockType(type: string): Promise<void>
  unblockType(type: string): Promise<void>
  isDomainBlocked(domain: string): Promise<boolean>
  isTypeBlocked(type: string): Promise<boolean>
  setStreamMode(mode: string): Promise<void>
  purgeEvents(before: string): Promise<number>

  // --- Migrations ---
  registerMigration(oldDomain: string, newDomain: string): Promise<MigrationRecord>
  queryMigrations(domain: string): Promise<MigrationRecord[]>
}
```

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
