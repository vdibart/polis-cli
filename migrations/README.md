# Polis Migration Scripts

Migration scripts handle data/file structure changes between Polis versions. They are executed by `polis-upgrade` and fetched from GitHub via CURL (tag-pinned).

## Directory Structure

```
migrations/
├── cli/
│   ├── manifest.json              # Lists all CLI migrations with checksums
│   └── v0.36.0_to_v0.37.0.sh     # Example migration script
├── tui/
│   ├── manifest.json              # Lists all TUI migrations with checksums
│   └── v0.6.0_to_v0.7.0.sh       # Example migration script
└── README.md                      # This file
```

## When to Write a Migration

Create a migration script when a new version changes:
- File locations (e.g., moving `public.json` to a new path)
- Configuration format (e.g., renaming keys in `.env` or `.well-known/polis`)
- Directory structure (e.g., new required directories)
- Data formats (e.g., JSONL schema changes)

**Do NOT create a migration** for versions that only change CLI/TUI code behavior without affecting user files.

## Manifest Format

Each component has a `manifest.json` listing available migrations:

```json
{
  "component": "cli",
  "migrations": [
    {
      "from": "0.36.0",
      "to": "0.37.0",
      "script": "v0.36.0_to_v0.37.0.sh",
      "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
      "description": "Move public.json to .polis/ directory"
    }
  ]
}
```

Fields:
- `from` - Version this migration applies to (users at or above this version need it)
- `to` - Version after migration is applied
- `script` - Filename of the migration script
- `sha256` - SHA-256 checksum of the script (verified before execution)
- `description` - Human-readable summary shown during upgrade

## Writing a Migration Script

### Template

```bash
#!/usr/bin/env bash
# Migration: v0.36.0 -> v0.37.0
# Description: Brief description of what this migration does
set -euo pipefail

SOURCE_VERSION="0.36.0"
TARGET_VERSION="0.37.0"

# POLIS_BASE is set by polis-upgrade to the user's site directory
if [ -z "${POLIS_BASE:-}" ]; then
    echo "Error: POLIS_BASE must be set to your polis site directory" >&2
    exit 1
fi

# --- Migration logic ---
# Your changes here. Be idempotent (safe to run multiple times).

echo "Migration $SOURCE_VERSION -> $TARGET_VERSION complete"
```

### Guidelines

1. **Idempotent**: Scripts must be safe to run multiple times. Use existence checks before moving/creating files.
2. **Non-destructive**: Never delete user content. Move or rename, don't remove.
3. **Validate first**: Check preconditions before making changes. Exit early if nothing to do.
4. **Clear output**: Print what was done (or "no migration needed") so users understand the result.
5. **Fail fast**: Use `set -euo pipefail`. If something unexpected happens, stop immediately.
6. **No network**: Migration scripts should not require network access. All logic should be local file operations.

### Environment Variables

The following are available to migration scripts (set by `polis-upgrade`):

- `POLIS_BASE` - Absolute path to the user's polis site directory

## Adding a New Migration

1. Write the migration script in `migrations/{component}/`
2. Generate the SHA-256 checksum:
   ```bash
   sha256sum migrations/cli/v0.36.0_to_v0.37.0.sh
   ```
3. Add the entry to `migrations/{component}/manifest.json`
4. Commit and tag the release

## How `polis-upgrade` Uses Migrations

1. Detects current version from the installed `polis` script
2. Queries discovery service for the latest (or `--to`) version
3. Fetches `manifest.json` from the target version's tag on GitHub
4. Filters to applicable migrations (`from >= current` AND `to <= target`)
5. Downloads each script, verifies SHA-256 checksum
6. Executes scripts in version order
7. Offers to download the updated CLI/TUI binary

## Release Ordering

When releasing a version with migrations:

1. Write migration script(s)
2. Generate checksums and update manifest
3. Commit and tag (e.g., `v0.37.0`)
4. Push tag to GitHub
5. **Only after tag is live**: Insert version row into `polis_versions` table

The tag must exist on GitHub before the discovery service advertises the version, otherwise `polis-upgrade` will get a 404 when fetching the manifest.
