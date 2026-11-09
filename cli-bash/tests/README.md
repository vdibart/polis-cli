# Polis CLI Test Suite

Automated testing framework for the Polis CLI. Tests run in the current working directory using the existing git repository.

## Quick Start

```bash
# Run all tests
./tests/run_tests.sh

# Skip network API calls
./tests/run_tests.sh --skip-network

# Run specific category
./tests/run_tests.sh --category unit

# Auto-push commits (for CI/deploy workflows)
./tests/run_tests.sh --auto-push

# Cleanup orphaned test data (from failed tests)
./tests/run_tests.sh --cleanup

# JSON output for automation
./tests/run_tests.sh --json
```

## How It Works

Tests create artifacts in a `test-data/` subdirectory:

```
your-repo/
├── posts/              # Your real posts (untouched)
├── comments/           # Your real comments (untouched)
├── test-data/          # Test artifacts (created/destroyed per test)
│   ├── posts/
│   ├── comments/
│   └── metadata/
└── cli/tests/
    └── run_tests.sh
```

**Test lifecycle:**
1. `setup_test_env()` creates `test-data/` with subdirectories
2. Test runs, creating posts/comments in `test-data/`
3. `teardown_test_env()` commits artifacts, then `git rm`s them
4. If `--auto-push`, pushes commits to trigger deploy

## Use Case: Vercel Deploy Testing

Perfect for testing with a deploy-on-push setup:

```bash
# 1. Clone/copy polis-cli to a test repo
git clone your-test-repo.git polis-tester
cd polis-tester

# 2. Configure environment
export POLIS_BASE_URL=https://your-vercel-deploy.vercel.app

# 3. Run tests with auto-push to trigger deploys
./cli/tests/run_tests.sh --auto-push
```

## Test Categories

### Unit Tests (`--category unit`)
Test individual commands in isolation:
- `test_init.sh` - polis init command
- `test_post.sh` - polis post command
- `test_republish.sh` - polis republish command
- `test_version.sh` - polis version command

### Integration Tests (`--category integration`)
Test command sequences and workflows:
- `test_post_workflow.sh` - Full init → post → republish cycle

### E2E Tests (`--category e2e`)
End-to-end tests with real API calls:
- `test_blessing_workflow.sh` - Comment and blessing workflow

**Prerequisites for E2E tests:**
- `POLIS_BASE_URL` environment variable set
- Discovery service deployed and reachable

## Options

| Option | Description |
|--------|-------------|
| `--json` | Output results in JSON format |
| `--category TYPE` | Run only tests of TYPE (unit, integration, e2e, all) |
| `--skip-network` | Skip actual API calls, assume success |
| `--auto-push` | Auto-push commits after test cleanup |
| `--cleanup` | Cleanup orphaned test data (from failed tests) |
| `--help` | Show help message |

## Directory Structure

```
tests/
├── run_tests.sh              # Main test runner
├── lib/
│   ├── test_framework.sh     # Core utilities (setup/teardown)
│   ├── assertions.sh         # JSON/file assertions
│   └── fixtures.sh           # Test data generators
├── unit/                     # Unit tests
├── integration/              # Integration tests
└── e2e/                      # End-to-end tests
```

## Writing New Tests

1. Create a test file in the appropriate directory (unit/, integration/, e2e/)
2. Define test functions using the framework pattern
3. Call `run_test` for each test

Example:

```bash
#!/bin/bash
# test_my_feature.sh

test_my_feature() {
    setup_test_env "my_feature"
    trap teardown_test_env EXIT

    # Initialize polis
    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    # Create test post (uses POSTS_DIR from setup)
    local post_path
    post_path=$(create_sample_post "my-post.md" "Test Post")

    # Run command
    local result
    result=$("$POLIS_BIN" --json post "$post_path" 2>&1)

    # Assertions
    assert_exit_code 0 $? || return 1
    assert_json_success "$result" "post" || return 1

    return 0
}

run_test "My Feature" test_my_feature
```

## Test Isolation

Each test runs in an isolated `test-data/` subdirectory:
- Created fresh at test start
- Committed and removed at test end
- No interference between tests

## Git Workflow

Tests use git-tracked cleanup:
1. Test artifacts are staged and committed (`test-artifacts: <timestamp>`)
2. Artifacts are removed via `git rm`
3. Removal is committed (`test-cleanup: removed test-data`)
4. If `--auto-push`, commits are pushed

## Recovering from Failed Tests

If a test fails or is interrupted:

```bash
# Clean up orphaned test-data/
./tests/run_tests.sh --cleanup

# Or manually
rm -rf test-data/
git status  # Check for uncommitted changes
```

The framework also auto-cleans orphaned `test-data/` at the start of each run.
