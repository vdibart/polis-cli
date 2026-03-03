# Polis CLI JSON Mode

## Feature Description

JSON Mode adds machine-readable output to all polis CLI commands via a global `--json` flag. The flag can be placed at the **start or end** of the command:

```bash
polis --json post article.md   # Flag at start
polis post article.md --json   # Flag at end (also works)
```

This enables:

- **Scriptable workflows** - Chain commands together programmatically
- **Error handling** - Structured error codes for better automation
- **Testing** - Validate command output with JSON parsers
- **Integration** - Connect polis with other tools and pipelines

When `--json` is enabled:
- Output is valid JSON to stdout (success) or stderr (errors)
- Interactive prompts are auto-skipped with logged defaults
- ANSI color codes are disabled
- Exit codes indicate success (0) or failure (1)

## Use Cases

### 1. Automated Publishing Workflows

```bash
# Publish multiple posts and collect hashes
for post in posts/*.md; do
  result=$(polis --json post "$post")
  hash=$(echo "$result" | jq -r '.data.content_hash')
  echo "$post: $hash"
done
```

### 2. Comment Management Automation

```bash
# Auto-approve all pending blessing requests
requests=$(polis --json blessing requests)
echo "$requests" | jq -r '.data.requests[].id' | while read id; do
  polis --json blessing grant "$id"
done
```

### 3. CI/CD Integration

```bash
# Publish and verify in CI pipeline
if ! result=$(polis --json post article.md 2>&1); then
  error_code=$(echo "$result" | jq -r '.error.code')
  echo "::error::Publication failed: $error_code"
  exit 1
fi

hash=$(echo "$result" | jq -r '.data.content_hash')
echo "::set-output name=hash::$hash"
```

### 4. Content Validation

```bash
# Extract and validate all metadata
polis --json init > init-result.json
jq '.data.key_paths' init-result.json

# Verify index integrity
polis --json rebuild | jq '.data | {posts, comments, index_path}'
```

### 5. Batch Comment Processing

```bash
# Read URLs from file and comment on each
cat post-urls.txt | while read url; do
  echo "Great post!" | polis --json comment "$url"
done
```

## Default Values in JSON Mode

When interactive prompts are auto-skipped in JSON mode, these defaults are used:

| Command | Prompt | Default Value | Logged Message |
|---------|--------|---------------|----------------|
| `comment` | "Comment URL:" | Derived from `POLIS_BASE_URL` + canonical path | `[default] Using comment URL from POLIS_BASE_URL + derived path` |
| `follow` | "Grant all pending blessings? (y/N):" | `y` (yes) | `[default] Auto-confirming: yes` |
| `unfollow` | "Deny all pending blessings? (y/N):" | `y` (yes) | `[default] Auto-confirming: yes` |

**Note:** Default messages are written to stderr so they don't interfere with JSON output on stdout.

## Standard JSON Response Format

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

## Command-Specific JSON Responses

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

### `polis about`

Returns complete system information including site details, versions, configuration, keys, and discovery status.

```json
{
  "status": "success",
  "command": "about",
  "data": {
    "site": {
      "url": "https://example.com",
      "title": "My Blog"
    },
    "versions": {
      "cli": "0.29.0",
      "well_known_polis": "1.0",
      "following": "1.0",
      "blessed_comments": "1.0",
      "manifest": "1.0"
    },
    "configuration": {
      "directories": {
        "keys": ".polis/keys",
        "posts": "posts",
        "comments": "comments",
        "snippets": "snippets",
        "versions": ".versions"
      },
      "files": {
        "public_index": "metadata/public.jsonl",
        "blessed_comments": "metadata/blessed-comments.json",
        "following": "metadata/following.json",
        "manifest": "metadata/manifest.json"
      }
    },
    "keys": {
      "status": "initialized",
      "fingerprint": "SHA256:abc123...",
      "public_key_path": ".polis/keys/id_ed25519.pub"
    },
    "discovery": {
      "service_url": "https://ds.polis.pub",
      "api_key_set": true,
      "registration": {
        "status": "registered",
        "registry_url": "https://...",
        "registered_at": "2026-01-10T12:00:00Z"
      }
    },
    "project": {
      "repository": "https://github.com/vdibart/polis",
      "license": "AGPL-3.0"
    }
  }
}
```

Note: Sensitive values like the API key show `api_key_set: true/false` instead of the actual value.

### `polis post <file>`

```json
{
  "status": "success",
  "command": "post",
  "data": {
    "file_path": "posts/2026/01/my-post.md",
    "content_hash": "sha256:abc123...",
    "timestamp": "2026-01-15T12:00:00Z",
    "signature": "-----BEGIN SSH SIGNATURE-----...",
    "canonical_url": "https://example.com/posts/2026/01/my-post.md"
  }
}
```

### `polis republish <file>`

```json
{
  "status": "success",
  "command": "republish",
  "data": {
    "file_path": "posts/2026/01/my-post.md",
    "previous_version": "sha256:abc123...",
    "new_version": "sha256:def456...",
    "timestamp": "2026-01-15T14:00:00Z",
    "signature": "-----BEGIN SSH SIGNATURE-----..."
  }
}
```

### `polis preview <url>`

```json
{
  "status": "success",
  "command": "preview",
  "data": {
    "url": "https://alice.com/posts/2026/01/hello.md",
    "type": "post",
    "title": "Hello World",
    "published": "2026-01-15T12:00:00Z",
    "current_version": "sha256:abc123...",
    "generator": "polis-cli/0.16.0",
    "in_reply_to": null,
    "author": "alice@example.com",
    "signature": {
      "status": "valid",
      "message": "Good signature from alice@example.com"
    },
    "hash": {
      "status": "valid"
    },
    "validation_issues": [],
    "body": "# Hello World\n\nThis is my first post..."
  }
}
```

### `polis comment <file> <url>`

```json
{
  "status": "success",
  "command": "comment",
  "data": {
    "file_path": "comments/2026/01/reply.md",
    "content_hash": "sha256:def456...",
    "in_reply_to": "https://bob.com/posts/original.md",
    "timestamp": "2026-01-15T12:30:00Z",
    "beseech_status": "pending"
  }
}
```

### `polis blessing sync`

```json
{
  "status": "success",
  "command": "blessing-sync",
  "data": {
    "synced_count": 3
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
        "id": 1,
        "comment_url": "https://alice.com/comments/reply.md",
        "author": "alice@example.com",
        "timestamp": "2025-01-15T12:00:00Z"
      }
    ]
  }
}
```

### `polis blessing grant <hash>`

```json
{
  "status": "success",
  "command": "blessing-grant",
  "data": {
    "comment_version": "sha256:f4bac5d0...",
    "comment_url": "https://alice.com/comments/reply.md",
    "blessed_at": "2026-01-15T13:00:00Z",
    "blessed_by": "bob@example.com"
  }
}
```

### `polis blessing deny <hash>`

```json
{
  "status": "success",
  "command": "blessing-deny",
  "data": {
    "comment_version": "sha256:f4bac5d0...",
    "comment_url": "https://alice.com/comments/reply.md",
    "denied_at": "2026-01-15T13:00:00Z",
    "denied_by": "bob@example.com"
  }
}
```

### `polis blessing beseech <hash>`

Re-request blessing for a comment by its content hash (short form like `abc123-def456` or full SHA256).

```json
{
  "status": "success",
  "command": "blessing-beseech",
  "data": {
    "comment_url": "https://alice.com/comments/reply.md",
    "comment_version": "sha256:abc123...",
    "in_reply_to": "https://bob.com/posts/original.md",
    "discovery_response": {
      "success": true,
      "message": "Beseech request recorded",
      "status": "pending"
    }
  }
}
```

If already blessed:

```json
{
  "status": "success",
  "command": "blessing-beseech",
  "data": {
    "status": "already_blessed",
    "comment_version": "sha256:abc123..."
  }
}
```

### `polis follow <url>`

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

### `polis unfollow <url>`

Similar structure to `follow`, with `removed_from_following` and `comments_denied` fields.

### `polis rebuild`

```json
{
  "status": "success",
  "command": "rebuild",
  "data": {
    "posts_indexed": 12,
    "comments_indexed": 34,
    "index_path": "metadata/public.json"
  }
}
```

### `polis render`

```json
{
  "status": "success",
  "command": "render",
  "data": {
    "posts_rendered": 5,
    "posts_skipped": 12,
    "comments_rendered": 3,
    "comments_skipped": 8,
    "index_generated": true
  }
}
```

### `polis index --json`

Returns posts and comments grouped from the public index.

```json
{
  "version": "0.16.0",
  "posts": [
    {
      "title": "My First Post",
      "url": "https://example.com/posts/2026/01/my-first-post.md",
      "published": "2026-01-15T12:00:00Z",
      "version": "sha256:abc123..."
    }
  ],
  "comments": [
    {
      "title": "Re: Their Post",
      "url": "https://example.com/comments/2026/01/reply.md",
      "published": "2026-01-15T14:00:00Z",
      "in_reply_to": "https://other.com/posts/their-post.md",
      "version": "sha256:def456..."
    }
  ]
}
```

Note: This command outputs JSON directly (not wrapped in success envelope) for compatibility with JSONL tooling.

### `polis notifications`

```json
{
  "status": "success",
  "command": "notifications",
  "data": {
    "pending_blessings": [
      {
        "id": 42,
        "comment_url": "https://alice.com/comments/reply.md",
        "author": "alice@example.com",
        "in_reply_to": "https://example.com/posts/my-post.md",
        "timestamp": "2026-01-15T12:00:00Z"
      }
    ],
    "domain_migrations": [
      {
        "old_domain": "old-site.com",
        "new_domain": "new-site.com",
        "migrated_at": "2026-01-14T10:00:00Z"
      }
    ]
  }
}
```

### `polis migrate <new-domain>`

```json
{
  "status": "success",
  "command": "migrate",
  "data": {
    "old_domain": "old-site.com",
    "new_domain": "new-site.com",
    "posts_updated": 12,
    "comments_updated": 5,
    "database_updated": true,
    "database_rows": 17
  }
}
```

### `polis rotate-key`

```json
{
  "status": "success",
  "command": "rotate-key",
  "data": {
    "posts_resigned": 12,
    "posts_failed": 0,
    "comments_resigned": 5,
    "comments_failed": 0,
    "old_key": "archived",
    "new_key_fingerprint": "SHA256:abc123..."
  }
}
```

The `old_key` field is either `"archived"` or `"deleted"` depending on whether `--delete-old-key` was used.

## Error Codes

| Code | Description | Common Causes | Example |
|------|-------------|---------------|---------|
| `FILE_NOT_FOUND` | Required file doesn't exist | Missing input file, file deleted | `polis --json post missing.md` |
| `INVALID_INPUT` | User input validation failed | Missing argument, invalid format | `polis --json post` (no file) |
| `API_ERROR` | Remote API call failed | Network issue, endpoint down, HTTP error | Discovery service unreachable |
| `SIGNATURE_ERROR` | Signature verification failed | Invalid key, corrupted file, wrong algorithm | Signature mismatch in beseech |
| `MISSING_DEPENDENCY` | Required tool not found | jq, ssh-keygen, git not installed | `command not found: jq` |
| `PERMISSION_ERROR` | File/directory permission denied | Read-only filesystem, insufficient permissions | Cannot write to .polis/ |
| `INVALID_STATE` | Operation not valid in current state | Polis not initialized, file already published | `polis post` before `polis init` |

### Error Response Example

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

## Future Enhancements

### Planned Features

1. **`--quiet` flag** - Suppress `[default]` log messages
   ```bash
   polis --json --quiet publish test.md
   # No stderr output, only JSON result
   ```

2. **Alternative output formats**
   ```bash
   polis --format=yaml publish test.md
   polis --format=toml publish test.md
   ```

3. **JSON formatting options**
   ```bash
   polis --json --pretty publish test.md    # Pretty-printed
   polis --json --compact publish test.md   # Minified (default)
   ```

4. **Batch mode**
   ```bash
   # Process multiple operations from JSON input
   cat operations.json | polis --json --batch
   ```

5. **Webhook integration**
   ```bash
   # Post results to webhook
   polis --json --webhook=https://api.example.com/hooks publish test.md
   ```

6. **Progress tracking for long operations**
   ```json
   {
     "status": "in_progress",
     "command": "follow",
     "progress": {
       "current": 3,
       "total": 10,
       "message": "Blessing comment 3 of 10"
     }
   }
   ```

### Community Requests

- `--dry-run` flag for testing without side effects
- `--verbose` flag for detailed operation logs in JSON
- Structured logging with log levels (DEBUG, INFO, WARN, ERROR)
- Machine-readable timestamps (ISO 8601 vs Unix epoch option)

## Example Usage

### Basic Publishing

```bash
# Publish a post and capture result
result=$(polis --json post my-post.md)
echo "$result" | jq -r '.data.content_hash'
```

### Chained Commands

```bash
# Publish and beseech in one script
publish_result=$(polis --json post comment.md)
comment_url=$(echo "$publish_result" | jq -r '.data.canonical_url')
polis --json beseech "$comment_url"
```

### Automated Blessing Workflow

```bash
# Check blessing requests and auto-grant first one
requests=$(polis --json blessing requests)
first_id=$(echo "$requests" | jq -r '.data.requests[0].id')
polis --json blessing grant "$first_id"
```

### Error Handling in Scripts

```bash
# Proper error handling with structured codes
if ! result=$(polis --json post test.md 2>&1); then
    error_code=$(echo "$result" | jq -r '.error.code')
    error_msg=$(echo "$result" | jq -r '.error.message')

    case "$error_code" in
        FILE_NOT_FOUND)
            echo "Error: File doesn't exist"
            exit 2
            ;;
        INVALID_STATE)
            echo "Error: Run 'polis init' first"
            exit 3
            ;;
        *)
            echo "Error: $error_msg"
            exit 1
            ;;
    esac
fi

echo "Success! Hash: $(echo "$result" | jq -r '.data.content_hash')"
```

### Batch Processing

```bash
# Process all markdown files in a directory
for file in posts/*.md; do
    if result=$(polis --json post "$file" 2>&1); then
        hash=$(echo "$result" | jq -r '.data.content_hash')
        echo "✓ $file → $hash"
    else
        error=$(echo "$result" | jq -r '.error.code')
        echo "✗ $file → $error"
    fi
done
```

### Integration with jq Filters

```bash
# Extract specific fields from complex responses
polis --json follow https://alice.com | \
  jq '{
    author: .data.author_email,
    blessed: .data.comments_blessed,
    success: (.data.comments_blessed > 0)
  }'
```

### Conditional Logic

```bash
# Only proceed if blessing requests exist
count=$(polis --json blessing requests | jq -r '.data.count')

if [ "$count" -gt 0 ]; then
    echo "Processing $count blessing requests..."
    # Auto-approve logic here
else
    echo "No pending requests"
fi
```

## Migration Guide

### Updating Existing Scripts

**Before (human-readable output):**
```bash
polis post article.md
# Output: [✓] Published posts/2025/01/article.md
```

**After (JSON mode):**
```bash
polis --json post article.md | jq -r '.data.file_path'
# Output: posts/2025/01/article.md
```

### Backwards Compatibility

- Default behavior unchanged - existing scripts work without modification
- `--json` is opt-in, not breaking
- Exit codes remain consistent (0 = success, 1 = error)

## Best Practices

1. **Always validate JSON output**
   ```bash
   result=$(polis --json init)
   echo "$result" | jq empty || exit 1  # Fail on invalid JSON
   ```

2. **Capture stderr separately for debugging**
   ```bash
   result=$(polis --json post test.md 2> error.log)
   ```

3. **Use jq for robust parsing**
   ```bash
   # Good - handles missing fields gracefully
   hash=$(echo "$result" | jq -r '.data.content_hash // "unknown"')

   # Bad - fails on missing field
   hash=$(echo "$result" | grep -o '"content_hash":"[^"]*"')
   ```

4. **Check exit codes before parsing**
   ```bash
   if result=$(polis --json post test.md 2>&1); then
       # Parse success response
   else
       # Parse error response
   fi
   ```
