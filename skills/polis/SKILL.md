---
name: polis
description: |
  Polis CLI for decentralized social networking. Publish signed posts,
  discover conversations, comment on posts, manage blessing requests,
  send direct messages, tag content, and view activity status. Use when
  user wants to: publish, comment, discover topics, manage blessings,
  send/read DMs, tag posts, check status, or work with their polis site.
allowed-tools:
  - Read
  - Write
  - Bash(polis:*)
  - Bash(curl:*)
  - Bash(jq:*)
  - Bash(git:*)
---

# Polis Skill

AI-powered workflows for decentralized social networking using the polis CLI.

## Core Principles

1. **Always use `--json` flag** for structured output parsing
2. **Handle errors gracefully** using structured error codes
3. **Confirm destructive actions** before execution
4. **Preserve user content** - never overwrite without confirmation
5. **Leave git control with user** - offer commits, don't auto-commit

## CLI Location

The polis CLI binary is `polis` (Go CLI). Ensure it is on your PATH or use the full path to the binary.

All commands should use the `--json` flag for machine-readable output:
```bash
polis --json <command> [options]
```

## Environment Requirements

The following environment variables should be configured:
- `POLIS_BASE_URL` - Your polis site URL (e.g., https://yourdomain.com)
- `DISCOVERY_SERVICE_URL` - Discovery service URL
- `DISCOVERY_SERVICE_KEY` - API authentication key

---

## Feature 1: Publish

**Trigger phrases**: "publish", "post", "sign and publish", "deploy post"

### Workflow

1. **Read the draft file** to understand content and extract title

2. **Suggest/refine title** if needed:
   - Review the markdown heading
   - Suggest improvements if title could be clearer or more engaging
   - Let user approve or modify

3. **Check for existing frontmatter**:
   - If file has polis frontmatter (already published): note to user, use `republish`
   - If no frontmatter: use `post`

4. **Execute the command**:
   ```bash
   # For new posts
   polis --json post <file>

   # For updates to existing posts
   polis --json republish <file>

   # For inline content (stdin)
   echo "<content>" | polis --json post - --filename <name.md> --title "<title>"
   ```

5. **Parse and report results**:
   - File path (canonical location)
   - Content hash
   - Canonical URL
   - Signature confirmation

6. **Offer git commit** (don't auto-commit):
   - Show what would be committed
   - Ask user if they want to commit
   - If yes, stage and commit with descriptive message

### Notes
- Accept drafts from any path (no restriction to specific folders)
- Support stdin for inline content creation

---

## Feature 2: Discover

**Trigger phrases**: "discover", "find posts about", "search for", "what's being discussed"

### Workflow

1. **Load following list**:
   ```bash
   cat metadata/following.json
   ```

2. **Fetch local index** (your own content):
   ```bash
   polis --json index
   ```

3. **Fetch followed authors' indexes**:
   For each author in following.json:
   ```bash
   curl -s <author-url>/metadata/public.jsonl
   ```
   - Parse and aggregate posts/comments
   - Handle unreachable sites gracefully (skip with note)

4. **Semantic matching**:
   - Match the topic/query against titles and excerpts
   - Consider both your content and followed authors' content
   - Rank by relevance

5. **Present results**:
   - Show matching posts with: author, title, URL, brief excerpt
   - Group by author or relevance as appropriate

6. **Offer next actions**:
   - "Preview this post?" -> use preview command
   - "Comment on this?" -> switch to comment workflow

### Technical Notes
- Following list format: `metadata/following.json` contains author URLs
- Each author's public index at: `<author-base-url>/metadata/public.jsonl`
- This is following-based discovery (your network only)

---

## Feature 3: Comment

**Trigger phrases**: "comment on", "reply to", "respond to"

### Workflow

1. **Preview the target post/comment**:
   ```bash
   polis --json preview <url>
   ```

2. **Present content to user**:
   - Show title, author, content body
   - Show signature verification status
   - Summarize the key points

3. **Gather context for drafting**:
   - Read user's previous comments from local index to learn their tone/style
   - If enough prior comments exist: draft a reply matching their voice
   - If not enough context: ask user what they want to say

4. **Present draft inline** for user to edit:
   - Show the suggested draft
   - Let user modify directly before proceeding

5. **Create the comment**:
   ```bash
   echo "<approved-content>" | polis --json comment - <url> --filename <reply-name.md>
   ```

6. **Report beseech status**:
   - CLI automatically sends blessing request
   - Report the pending status to user

### Tone Matching
- Read user's previous comments to learn their voice
- Match that tone (formal, casual, technical, etc.)
- Fall back to neutral professional if no prior comments exist

---

## Feature 4: Manage Blessings

**Trigger phrases**: "manage blessings", "review blessings", "blessing requests", "pending comments"

### Workflow

1. **Sync and fetch requests**:
   ```bash
   polis --json blessing sync
   polis --json blessing requests
   ```

2. **Report auto-blessed comments** (FYI):
   - Note any comments that were auto-blessed from followed authors
   - These don't need review, just confirmation they were handled

3. **Walk through pending requests one-at-a-time**:

   For each pending request:

   a. **Preview the comment**:
      ```bash
      polis --json preview <comment_url>
      ```

   b. **Assess quality** using these signals (weighted):
      1. **Signature validity** (required) - invalid = recommend deny
      2. **Thread-specific trust** (high weight) - has this author been blessed on other posts?
      3. **On-topic relevance** - is it about the post?
      4. **Constructiveness** - thoughtful vs spam/trolling
      5. **Network reputation** (lower weight) - followed by people you follow?

   c. **Present with recommendation**:
      ```
      Request <hash> from <author>
      On: <post-url>
      Comment: "<excerpt>..."
      Signature: VALID/INVALID

      AI Recommendation: GRANT/DENY (reasoning)

      [Grant] [Deny] [Skip]
      ```

   d. **Execute user's decision**:
      ```bash
      polis --json blessing grant <hash>
      # or
      polis --json blessing deny <hash>
      ```

   e. **Move to next request**

4. **Deny silently** (no rationale sent to commenter - future feature)

---

## Feature 5: Status

**Trigger phrases**: "status", "polis status", "what's my status", "show activity"

### Workflow

1. **Gather data**:
   ```bash
   # Your published content
   polis --json index

   # Pending blessing requests (others wanting your blessing)
   polis --json blessing requests

   # Git status for uncommitted changes
   git status --porcelain 2>/dev/null || true

   # Discovery service health
   curl -s -o /dev/null -w "%{http_code}" "${DISCOVERY_SERVICE_URL}/health" 2>/dev/null || echo "unreachable"

   # Following count
   cat metadata/following.json | jq 'length' 2>/dev/null || echo "0"
   ```

2. **Parse and present dashboard**:
   ```
   === Polis Status ===

   Published Content:
   - X posts (latest: "<title>" - <relative-time>)
   - Y comments (latest: reply to <domain> - <relative-time>)

   Pending Actions:
   - N blessing requests awaiting YOUR review
   - M of YOUR comments awaiting blessing from others
   - P uncommitted files

   Network:
   - Following: Q authors
   - Discovery service: connected/disconnected
   ```

### Dashboard Includes
- Post/comment counts with latest activity
- Pending blessing requests (others -> you)
- Pending beseech status (your comments -> others)
- Uncommitted git changes
- Following count
- Discovery service health check

---

## Feature 6: Notifications

**Trigger phrases**: "notifications", "pending actions", "what needs attention", "any updates", "check for updates"

### Workflow

1. **Fetch notifications**:
   ```bash
   # List unread notifications
   polis --json notifications

   # List all notifications (including read)
   polis --json notifications list --all

   # Filter by type
   polis --json notifications list --type version_available,new_follower
   ```

2. **Present categorized notifications**:
   ```
   === Notifications ===

   Updates (1 unread):
   - [version_available] CLI v0.35.0 available (you have v0.34.0)

   Pending Actions (2 unread):
   - [version_pending] Metadata files need update - run 'polis rebuild'

   Social (3 unread):
   - [new_follower] alice.com started following you
   - [new_post] bob.com published "New Article"
   - [blessing_changed] Your comment was blessed on charlie.com
   ```

3. **Offer actions based on notification type**:
   - `version_available` - "Upgrade available. Show release notes?"
   - `version_pending` - "Run `polis rebuild --all` to update metadata?"
   - `new_follower` - "Follow them back?"
   - `new_post` - "Preview this post?"
   - `blessing_changed` - "View the comment?"

4. **Mark notifications as handled**:
   ```bash
   # Mark specific notification as read
   polis --json notifications read <id>

   # Mark all as read
   polis --json notifications read --all

   # Dismiss old notifications
   polis --json notifications dismiss --older-than 30d
   ```

### Notification Types

| Type | Source | Description |
|------|--------|-------------|
| `version_available` | Discovery service | New CLI version released |
| `version_pending` | Local detection | CLI upgraded but metadata needs rebuild |
| `new_follower` | Discovery service | Someone followed you |
| `new_post` | Discovery service | Followed author published |
| `blessing_changed` | Discovery service | Your comment was blessed/unblessed |

### Syncing Notifications

```bash
# Fetch new notifications from discovery service
polis --json notifications sync

# Reset and do full re-sync
polis --json notifications sync --reset
```

### Configuring Preferences

```bash
# Show current config
polis --json notifications config

# Set poll interval
polis notifications config --poll-interval 30m

# Enable/disable notification types
polis notifications config --enable new_post
polis notifications config --disable version_available

# Mute specific domains
polis notifications config --mute spam.com
polis notifications config --unmute spam.com
```

### Local Storage

Notifications are stored locally:
- `.polis/notifications.jsonl` - Notification log (one per line)
- `.polis/notifications-manifest.json` - Preferences and sync state

---

## Feature 7: Site Registration

**Trigger phrases**: "register", "register site", "make discoverable", "unregister"

### Workflow

1. **Register with discovery service**:
   ```bash
   polis --json register
   ```

2. **Report registration status**:
   - Site URL registered
   - Public key stored
   - Now discoverable by other polis users

3. **Unregister if requested** (destructive - confirm first):
   ```bash
   polis --json unregister --force
   ```

### Notes
- Registration makes your site discoverable
- Required for receiving blessing requests
- `polis init --register` can auto-register during initialization

---

## Feature 8: Clone Remote Site

**Trigger phrases**: "clone", "download site", "fetch site", "mirror"

### Workflow

1. **Clone a remote polis site**:
   ```bash
   # Clone to auto-named directory
   polis --json clone https://alice.com

   # Clone to specific directory
   polis --json clone https://alice.com ./alice-site

   # Force full re-download
   polis --json clone https://alice.com --full

   # Only fetch changes (incremental)
   polis --json clone https://alice.com --diff
   ```

2. **Report what was downloaded**:
   - Posts fetched
   - Comments fetched
   - Metadata files

### Use Cases
- Local backup of followed authors
- Offline reading
- Analysis of site structure

---

## Feature 9: About / Site Info

**Trigger phrases**: "about", "site info", "show config", "what's my setup"

### Workflow

1. **Get comprehensive site info**:
   ```bash
   polis --json about
   ```

2. **Present dashboard**:
   ```
   === Site ===
   URL: https://yoursite.com
   Title: Your Site Name

   === Versions ===
   CLI: 0.34.0
   Manifest: 0.34.0

   === Keys ===
   Status: configured
   Fingerprint: SHA256:abc123...

   === Discovery ===
   Service: https://discovery.polis.pub
   Registered: yes
   ```

### Notes
- Replaces the old `polis config` command
- Shows version mismatch warnings (CLI vs manifest)

---

## Feature 10: Direct Messages

**Trigger phrases**: "dm", "message", "send a message", "direct message", "read messages"

### Workflow

1. **List conversations**:
   ```bash
   polis --json dm list
   ```

2. **Present conversation list**:
   - Show peer domain, unread count, last message preview
   - Highlight conversations with unread messages

3. **Read a conversation** when user picks one:
   ```bash
   polis --json dm read <conversation_id>
   ```
   - Auto-marks messages as read

4. **Send a message** when requested:
   ```bash
   polis --json dm send <recipient_url> "<message>"
   ```
   - Message is encrypted end-to-end
   - If delivery fails, message is saved locally for retry

5. **Retry failed deliveries**:
   ```bash
   # Retry all unsent
   polis --json dm retry

   # Retry specific conversation
   polis --json dm retry <conversation_id>
   ```

6. **Show DM acceptance policy**:
   ```bash
   polis --json dm config
   ```
   - Shows private and public policy rules
   - Explains default (allow all) if no rules configured

### Notes
- DMs are encrypted with the recipient's public key
- Messages that fail to deliver are saved locally with status "unsent"
- Requires `POLIS_BASE_URL` to be set for sending
- DM policies are managed via `.polis/policies/rules.jsonl`

---

## Feature 11: Content Tagging

**Trigger phrases**: "tag", "label", "categorize", "tag this post", "show tags"

### Workflow

1. **List all tags**:
   ```bash
   polis --json tag list
   ```

2. **Show targets for a specific tag**:
   ```bash
   polis --json tag show <name>
   ```

3. **Apply a tag to content**:
   ```bash
   polis --json tag apply <name> <target-uri>
   ```
   - Tag names are normalized (lowercase, trimmed)
   - Automatically syncs to discovery service if configured

4. **Remove a target from a tag**:
   ```bash
   polis --json tag remove <name> <target-uri>
   ```

5. **Delete an entire tag**:
   ```bash
   polis --json tag delete <name>
   ```
   - Confirm before deleting (destructive)
   - Unregisters all targets from discovery service

### Notes
- Tags are stored locally in the polis data directory
- Tag operations are signed with the site's private key
- Discovery service sync happens automatically but is non-fatal if it fails
- Tags use content type `pub.polis.tag`

---

## Error Handling

When commands fail, parse the JSON error response:

```json
{
  "status": "error",
  "command": "...",
  "error": {
    "code": "ERROR_CODE",
    "message": "Human-readable message"
  }
}
```

Common error codes and responses:
- `FILE_NOT_FOUND` - Help user locate the correct file
- `INVALID_INPUT` - Explain required format
- `API_ERROR` - Check network/discovery service status
- `SIGNATURE_ERROR` - Report verification failure
- `INVALID_STATE` - Guide user (e.g., run `polis init` first)

---

## References

For detailed command syntax and JSON response schemas, see:
- [commands.md](references/commands.md) - CLI command reference
- [json-responses.md](references/json-responses.md) - JSON response schemas
