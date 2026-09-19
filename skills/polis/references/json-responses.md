# Polis CLI JSON Response Schemas

Reference for parsing JSON responses from polis CLI commands.

## Standard Response Format

### Success Response
```json
{
  "status": "success",
  "command": "command-name",
  "data": {
    // Command-specific fields
  }
}
```

### Error Response
```json
{
  "status": "error",
  "command": "command-name",
  "error": {
    "code": "ERROR_CODE",
    "message": "Human-readable error message",
    "details": {}
  }
}
```

---

## Command Responses

### `polis init`
```json
{
  "status": "success",
  "command": "init",
  "data": {
    "directories_created": [".polis/keys", "posts", "comments", "metadata"],
    "files_created": [
      ".well-known/polis",
      "metadata/public.json",
      "metadata/blessed-comments.json",
      "metadata/following.json"
    ],
    "key_paths": {
      "private": ".polis/keys/id_ed25519",
      "public": ".polis/keys/id_ed25519.pub"
    }
  }
}
```

### `polis post`
```json
{
  "status": "success",
  "command": "post",
  "data": {
    "file_path": "posts/20260106/my-post.md",
    "content_hash": "sha256:abc123...",
    "timestamp": "2026-01-06T12:00:00Z",
    "signature": "-----BEGIN SSH SIGNATURE-----...",
    "canonical_url": "https://example.com/posts/20260106/my-post.md"
  }
}
```

### `polis comment`
```json
{
  "status": "success",
  "command": "comment",
  "data": {
    "file_path": "comments/20260106/reply.md",
    "content_hash": "sha256:def456...",
    "in_reply_to": "https://bob.com/posts/original.md",
    "timestamp": "2026-01-06T12:30:00Z",
    "beseech_status": "pending"
  }
}
```

### `polis preview`
```json
{
  "status": "success",
  "command": "preview",
  "data": {
    "url": "https://alice.com/posts/hello.md",
    "type": "post",
    "title": "Hello World",
    "published": "2026-01-05T12:00:00Z",
    "current_version": "sha256:abc123...",
    "generator": "polis-cli/0.2.0",
    "in_reply_to": null,
    "author": "alice@example.com",
    "signature": {
      "status": "valid",
      "message": "Signature verified against author's public key"
    },
    "hash": {
      "status": "valid"
    },
    "validation_issues": [],
    "body": "# Hello World\n\nThis is my first post..."
  }
}
```

### `polis blessing requests`
```json
{
  "status": "success",
  "command": "blessing-requests",
  "data": {
    "count": 3,
    "requests": [
      {
        "id": "42",
        "comment_url": "https://alice.com/comments/reply.md",
        "comment_version": "sha256:f4bac5d0...",
        "in_reply_to": "https://bob.com/posts/hello.md",
        "root_post": "",
        "author": "alice.com",
        "timestamp": "",
        "created_at": "2026-01-05T12:00:00Z"
      }
    ]
  }
}
```

### `polis blessing grant`
```json
{
  "status": "success",
  "command": "blessing-grant",
  "data": {
    "comment_version": "sha256:f4bac5d0...",
    "comment_url": "https://alice.com/comments/reply.md",
    "blessed_at": "2026-01-06T13:00:00Z",
    "blessed_by": "bob@example.com"
  }
}
```

### `polis blessing deny`
```json
{
  "status": "success",
  "command": "blessing-deny",
  "data": {
    "comment_version": "sha256:f4bac5d0...",
    "comment_url": "https://alice.com/comments/reply.md",
    "denied_at": "2026-01-06T13:00:00Z",
    "denied_by": "bob@example.com"
  }
}
```

### `polis follow`
```json
{
  "status": "success",
  "command": "follow",
  "data": {
    "author_url": "https://alice.com",
    "author_email": "alice@example.com",
    "comments_found": 5,
    "comments_blessed": 5,
    "added_to_following": true
  }
}
```

### `polis unfollow`
```json
{
  "status": "success",
  "command": "unfollow",
  "data": {
    "author_url": "https://alice.com",
    "removed_from_following": true,
    "comments_denied": 3
  }
}
```

### `polis index`
Returns grouped JSON with posts and comments:
```json
{
  "status": "success",
  "command": "index",
  "data": {
    "posts": [
      {
        "title": "My Post",
        "canonical_url": "https://example.com/posts/20260106/my-post.md",
        "published": "2026-01-06T12:00:00Z",
        "content_hash": "sha256:..."
      }
    ],
    "comments": [
      {
        "title": "Re: Hello",
        "canonical_url": "https://example.com/comments/20260106/reply.md",
        "in_reply_to": "https://alice.com/posts/hello.md",
        "published": "2026-01-06T12:30:00Z"
      }
    ]
  }
}
```

### `polis rebuild`
```json
{
  "status": "success",
  "command": "rebuild",
  "data": {
    "posts_rebuilt": true,
    "comments_rebuilt": true,
    "notifications_reset": true,
    "posts": 12,
    "comments_indexed": 5,
    "blessed": 34
  }
}
```

Fields depend on which flags were used (`--posts`, `--comments`, `--notifications`, `--all`).

### `polis migrate`
```json
{
  "status": "success",
  "command": "migrate",
  "data": {
    "old_domain": "olddomain.com",
    "new_domain": "newdomain.com",
    "posts_updated": 3,
    "comments_updated": 5,
    "database_updated": true,
    "database_rows": 5
  }
}
```

### `polis notifications`
```json
{
  "status": "success",
  "command": "notifications",
  "data": {
    "notifications": [
      {
        "id": "notif_1737388800_abc123",
        "type": "version_available",
        "timestamp": "2026-01-20T12:00:00Z",
        "read": false,
        "data": {
          "latest": "0.35.0",
          "current": "0.34.0",
          "download_url": "https://github.com/..."
        }
      },
      {
        "id": "notif_1737388801_def456",
        "type": "new_follower",
        "timestamp": "2026-01-20T12:05:00Z",
        "read": false,
        "data": {
          "follower_domain": "alice.com"
        }
      }
    ],
    "unread_count": 2,
    "total_count": 5
  }
}
```

### `polis notifications read`
```json
{
  "status": "success",
  "command": "notifications-read",
  "data": {
    "marked_read": 1,
    "notification_id": "notif_1737388800_abc123"
  }
}
```

### `polis notifications sync`
```json
{
  "status": "success",
  "command": "notifications-sync",
  "data": {
    "new_notifications": 3,
    "types": {
      "version_available": 1,
      "new_follower": 2
    }
  }
}
```

### `polis about`
```json
{
  "status": "success",
  "command": "about",
  "data": {
    "site": {
      "url": "https://yoursite.com",
      "title": "Your Site Name"
    },
    "versions": {
      "cli": "0.34.0",
      "manifest": "0.34.0",
      "following": "1.0",
      "blessed_comments": "1.0"
    },
    "keys": {
      "status": "configured",
      "fingerprint": "SHA256:abc123...",
      "public_key_path": ".polis/keys/id_ed25519.pub"
    },
    "discovery": {
      "service_url": "https://discovery.polis.pub",
      "registered": true
    }
  }
}
```

### `polis register`
```json
{
  "status": "success",
  "command": "register",
  "data": {
    "domain": "yoursite.com",
    "registered_at": "2026-01-20T12:00:00Z",
    "public_key_stored": true
  }
}
```

### `polis unregister`
```json
{
  "status": "success",
  "command": "unregister",
  "data": {
    "domain": "yoursite.com",
    "unregistered_at": "2026-01-20T12:00:00Z"
  }
}
```

### `polis clone`
```json
{
  "status": "success",
  "command": "clone",
  "data": {
    "source_url": "https://alice.com",
    "local_path": "./alice.com",
    "posts_fetched": 15,
    "comments_fetched": 42,
    "mode": "full"
  }
}
```

### `polis discover`
```json
{
  "status": "success",
  "command": "discover",
  "data": {
    "authors_checked": 5,
    "new_posts": [
      {
        "author": "https://alice.com",
        "title": "New Article",
        "url": "https://alice.com/posts/20260120/new-article.md",
        "published": "2026-01-20T10:00:00Z"
      }
    ],
    "new_comments": [
      {
        "author": "https://bob.com",
        "url": "https://bob.com/comments/20260120/reply.md",
        "in_reply_to": "https://yoursite.com/posts/my-post.md",
        "published": "2026-01-20T11:00:00Z"
      }
    ]
  }
}
```

### `polis migrations apply`
```json
{
  "status": "success",
  "command": "migrations-apply",
  "data": {
    "migrations_applied": 1,
    "files_updated": [
      "metadata/following.json",
      "comments/20260106/reply.md"
    ]
  }
}
```

---

## Error Codes

| Code | Description | Recovery Action |
|------|-------------|-----------------|
| `FILE_NOT_FOUND` | Required file doesn't exist | Check file path, help user locate correct file |
| `INVALID_INPUT` | User input validation failed | Explain required format |
| `API_ERROR` | Remote API call failed | Check network, verify discovery service status |
| `SIGNATURE_ERROR` | Signature verification failed | Report verification failure, check key |
| `MISSING_DEPENDENCY` | Required tool not found | Guide user to install (jq, ssh-keygen, etc.) |
| `PERMISSION_ERROR` | File/directory permission denied | Check permissions |
| `INVALID_STATE` | Operation not valid in current state | Guide user (e.g., run `polis init` first) |

### Error Example
```json
{
  "status": "error",
  "command": "post",
  "error": {
    "code": "FILE_NOT_FOUND",
    "message": "File not found: article.md",
    "details": {}
  }
}
```

---

## Parsing Tips

### Extract specific fields with jq
```bash
# Get content hash from publish
polis --json post post.md | jq -r '.data.content_hash'

# Get pending request count
polis --json blessing requests | jq -r '.data.count'

# List the comment versions to grant or deny (grant takes a version, not an id)
polis --json blessing requests | jq -r '.data.requests[].comment_version'

# Check for error
result=$(polis --json post test.md 2>&1)
if echo "$result" | jq -e '.status == "error"' > /dev/null; then
  echo "Error: $(echo "$result" | jq -r '.error.message')"
fi
```

### Handle missing fields gracefully
```bash
# Use // for fallback values
hash=$(echo "$result" | jq -r '.data.content_hash // "unknown"')
```
