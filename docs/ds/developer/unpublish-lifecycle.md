# Unpublish Lifecycle

Unpublish is the mechanism for authors to retract published posts or comments. It is a **clean break** — all ties to the published identity are severed, and any future republish is treated as a completely fresh publication.

## What Unpublish Does

When an author unpublishes content:

- **DS record**: Content status transitions from `active` to `unpublished`
- **Local files**: Frontmatter (signature, version hash, version history, generator) is stripped. Only the title and body are preserved as a draft. Version history files (`.versions/`) are permanently deleted for posts.
- **Rendered HTML**: Removed from the site
- **Index**: Entry removed from `index.jsonl`

The content body is preserved as a draft for potential reuse, but all published identity (signature, version, history) is erased.

## Post Unpublish

When a post is unpublished, blessings on comments that reference the post are cascaded:

| Before | After | Rationale |
|--------|-------|-----------|
| `granted` (blessed) | `orphaned` | Post is gone; blessing relationship severed permanently |
| `pending` | `denied` | No active post to evaluate against; auto-deny |
| `denied` | `denied` | Already denied; no change |

**Orphaned is permanent.** The `orphaned` status is not restored if the post is later republished. Comment authors must re-beseech to request fresh blessing.

Comments themselves (their content, ownership, and DS content records) are **not affected** by post unpublish — only their blessing relationship status changes.

## Comment Unpublish

When a comment is unpublished:

- The comment's DS status transitions to `unpublished`
- The comment's **own blessing relationship** is reset to `pending` (so republish triggers fresh policy evaluation)
- **No cascade to child comments.** If other comments have `in_reply_to` pointing to this comment, they are unaffected. Their `root_post` (the original post) is still valid, and their blessing relationship is with the post author, not the parent commenter. The thread has a gap, but each comment remains independently valid.
- The comment is saved as a draft locally with `in-reply-to` metadata preserved

## Republishing After Unpublish

### Republishing an unpublished post

- The draft has no version history or signature — it goes through the normal publish flow as if brand new
- DS receives a fresh `registerContent` call which upserts the record back to `active` with a new version hash
- **Orphaned blessings are NOT restored.** Comment authors must re-beseech to request fresh blessing
- **Denied blessings stay denied**
- Rationale: the post content may have changed, and the post author should re-evaluate each comment

### Republishing an unpublished comment

- The draft has no signature or version — it goes through the normal sign, beseech, and blessing workflow as if brand new
- DS receives a fresh `registerContent` call which upserts back to `active`
- `handleCommentBlessing` runs fresh policy evaluation (the blessing was reset to `pending` on unpublish)
- The post author must grant fresh approval

## Rationale for Clean Break

1. **No content leakage.** Version history files contain full content of every previous version. If someone unpublishes because they accidentally published sensitive content, the history should not remain on disk.
2. **Stale blessings.** Post content may change between unpublish and republish, making old blessings stale. Fresh evaluation ensures the post author re-consents to each comment.
3. **Clean mental model.** Unpublish severs all ties to the published identity. The author gets a fresh start.

## State Diagram

```
Content:    active ──unpublish──> unpublished ──republish──> active (fresh, disconnected)

Blessings (post unpublish):
  granted ──> orphaned (permanent)
  pending ──> denied (permanent)
  denied  ──> denied (no change)

Blessing (comment unpublish):
  any ──> pending ──republish+beseech──> re-evaluated by post author policies
```

## New Status Values

| Table | Status | Meaning |
|-------|--------|---------|
| `ds_content_metadata` | `unpublished` | Author-retracted via clean break |
| `ds_relationship_metadata` | `orphaned` | Blessed comment whose parent post was unpublished |

## API Endpoints

| Action | Endpoint | Content Types |
|--------|----------|---------------|
| Unpublish | `POST /v1/content/unpublish` | `pub.polis.post`, `pub.polis.comment` |
| Unregister | `POST /v1/content/unregister` | `pub.polis.tag` only |

## Event Types

| Event | Emitted When |
|-------|-------------|
| `pub.polis.post.unpublished` | Post unpublished |
| `pub.polis.comment.unpublished` | Comment unpublished |
