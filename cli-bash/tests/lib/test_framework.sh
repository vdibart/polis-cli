#!/bin/bash
# test_framework.sh - Core test framework utilities for Polis CLI tests
#
# Tests run inside an isolated test-data/ subdirectory within the existing
# git repo. Each test gets a fresh test-data/ directory; polis commands
# execute with CWD=test-data/ so all paths are relative to it.
#
# Provides:
#   - Test environment setup/teardown (cd into test-data/ subdirectory)
#   - Test execution and tracking
#   - Output formatting (human-readable or JSON)

# Test state
TEST_COUNT=0
PASS_COUNT=0
FAIL_COUNT=0
SKIP_COUNT=0
TEST_RESULTS=()

# Configuration (set by run_tests.sh or environment)
: "${JSON_OUTPUT:=false}"
: "${SKIP_NETWORK:=false}"
: "${AUTO_PUSH:=false}"
TEST_DATA_DIR="test-data"
ORIGINAL_TEST_DIR=""

# Color codes (disabled in JSON mode)
if [[ "$JSON_OUTPUT" != "true" ]]; then
    RED='\033[0;31m'
    GREEN='\033[0;32m'
    YELLOW='\033[0;33m'
    BLUE='\033[0;34m'
    NC='\033[0m'
else
    RED=''
    GREEN=''
    YELLOW=''
    BLUE=''
    NC=''
fi

# Check if test-data directory exists (called from repo root)
has_test_data() {
    [[ -d "$TEST_DATA_DIR" ]]
}

# Emergency cleanup for failed tests or manual recovery
emergency_cleanup() {
    if [[ "$JSON_OUTPUT" != "true" ]]; then
        echo "  Running emergency cleanup..."
    fi

    # Ensure we're at repo root
    if [[ -n "$ORIGINAL_TEST_DIR" ]]; then
        cd "$ORIGINAL_TEST_DIR"
    fi

    if [[ -d "$TEST_DATA_DIR" ]]; then
        # Unstage any test files from git index
        git reset HEAD "$TEST_DATA_DIR" 2>/dev/null || true

        # Force remove
        rm -rf "$TEST_DATA_DIR" 2>/dev/null || true
    fi

    ORIGINAL_TEST_DIR=""

    if [[ "$JSON_OUTPUT" != "true" ]]; then
        echo "  Cleanup complete"
    fi
}

# Initialize test run (called once at start of test suite)
init_test_run() {
    # Check for leftover test data from previous failed run
    if has_test_data; then
        if [[ "$JSON_OUTPUT" != "true" ]]; then
            echo "[WARN] Found orphaned test-data/ directory. Cleaning up..."
        fi
        emergency_cleanup
    fi
}

# Initialize test environment
# Creates test-data/ subdirectory and cds into it.
# Polis commands run with CWD=test-data/ so all paths are relative.
# Does NOT export path env vars — lets polis use its built-in defaults.
setup_test_env() {
    local test_name="$1"

    # Ensure we start from repo root
    if [[ -n "$ORIGINAL_TEST_DIR" ]]; then
        cd "$ORIGINAL_TEST_DIR"
    fi

    # Verify we're in a git repo
    if ! git rev-parse --git-dir > /dev/null 2>&1; then
        log_error "Not in a git repository. Tests require an existing git repo."
        exit 1
    fi

    ORIGINAL_TEST_DIR="$(pwd)"

    # Clean up any leftover test data from previous test
    if [[ -d "$TEST_DATA_DIR" ]]; then
        git reset HEAD "$TEST_DATA_DIR" 2>/dev/null || true
        rm -rf "$TEST_DATA_DIR"
    fi

    # Unset polis path env vars so polis uses built-in defaults
    unset POSTS_DIR COMMENTS_DIR KEYS_DIR SNIPPETS_DIR THEMES_DIR
    unset PUBLIC_INDEX BLESSED_COMMENTS FOLLOWING_INDEX MANIFEST
    unset VERSIONS_DIR_NAME METADATA_DIR

    # Create fresh test directory and enter it
    mkdir -p "$TEST_DATA_DIR"
    cd "$TEST_DATA_DIR"

    if [[ "$JSON_OUTPUT" != "true" ]]; then
        echo "  [SETUP] Test directory: $(pwd) (test: $test_name)"
    fi
}

# Clean up test environment
teardown_test_env() {
    # Return to repo root
    if [[ -n "$ORIGINAL_TEST_DIR" ]]; then
        cd "$ORIGINAL_TEST_DIR"
    fi

    if [[ -d "$TEST_DATA_DIR" ]]; then
        # Unstage any test files from git index
        git reset HEAD "$TEST_DATA_DIR" 2>/dev/null || true

        # Remove test directory
        rm -rf "$TEST_DATA_DIR" 2>/dev/null || true

        if [[ "$JSON_OUTPUT" != "true" ]]; then
            echo "  [TEARDOWN] Cleaned up: $TEST_DATA_DIR"
        fi
    fi

    ORIGINAL_TEST_DIR=""
}

# Run a single test
# Usage: run_test "Test Name" test_function
run_test() {
    local test_name="$1"
    local test_func="$2"
    local start_time end_time duration
    local saved_dir="$(pwd)"

    TEST_COUNT=$((TEST_COUNT + 1))
    start_time=$(date +%s%N)

    if [[ "$JSON_OUTPUT" != "true" ]]; then
        echo ""
        echo -e "${BLUE}=== TEST: $test_name ===${NC}"
    fi

    # Run the test function and capture result
    local result=0
    if $test_func; then
        result=0
    else
        result=1
    fi

    end_time=$(date +%s%N)
    duration=$(( (end_time - start_time) / 1000000 ))  # milliseconds

    # Ensure we're back at the original directory and cleaned up
    cd "$saved_dir"
    if [[ -d "$TEST_DATA_DIR" ]]; then
        git reset HEAD "$TEST_DATA_DIR" 2>/dev/null || true
        rm -rf "$TEST_DATA_DIR" 2>/dev/null || true
    fi
    ORIGINAL_TEST_DIR=""

    if [[ $result -eq 0 ]]; then
        PASS_COUNT=$((PASS_COUNT + 1))
        if [[ "$JSON_OUTPUT" != "true" ]]; then
            echo -e "${GREEN}[PASS]${NC} $test_name (${duration}ms)"
        fi
        TEST_RESULTS+=("{\"name\":\"$test_name\",\"status\":\"pass\",\"duration_ms\":$duration}")
    else
        FAIL_COUNT=$((FAIL_COUNT + 1))
        if [[ "$JSON_OUTPUT" != "true" ]]; then
            echo -e "${RED}[FAIL]${NC} $test_name (${duration}ms)"
        fi
        TEST_RESULTS+=("{\"name\":\"$test_name\",\"status\":\"fail\",\"duration_ms\":$duration}")
    fi

    # Always return 0 — failures are tracked via FAIL_COUNT
    return 0
}

# Skip a test (for conditional skipping)
skip_test() {
    local test_name="$1"
    local reason="$2"

    TEST_COUNT=$((TEST_COUNT + 1))
    SKIP_COUNT=$((SKIP_COUNT + 1))

    if [[ "$JSON_OUTPUT" != "true" ]]; then
        echo ""
        echo -e "${YELLOW}[SKIP]${NC} $test_name: $reason"
    fi
    TEST_RESULTS+=("{\"name\":\"$test_name\",\"status\":\"skip\",\"reason\":\"$reason\"}")
}

# Print test summary
print_summary() {
    if [[ "$JSON_OUTPUT" == "true" ]]; then
        # JSON output
        local results_json
        results_json=$(printf '%s\n' "${TEST_RESULTS[@]}" | paste -sd ',' -)
        cat <<EOF
{
  "summary": {
    "total": $TEST_COUNT,
    "passed": $PASS_COUNT,
    "failed": $FAIL_COUNT,
    "skipped": $SKIP_COUNT
  },
  "results": [$results_json],
  "success": $([ $FAIL_COUNT -eq 0 ] && echo "true" || echo "false")
}
EOF
    else
        # Human-readable output
        echo ""
        echo "=========================================="
        echo "TEST SUMMARY"
        echo "=========================================="
        echo "Total:   $TEST_COUNT"
        echo -e "Passed:  ${GREEN}$PASS_COUNT${NC}"
        echo -e "Failed:  ${RED}$FAIL_COUNT${NC}"
        echo -e "Skipped: ${YELLOW}$SKIP_COUNT${NC}"
        echo ""

        if [[ $FAIL_COUNT -eq 0 ]]; then
            echo -e "${GREEN}All tests passed!${NC}"
        else
            echo -e "${RED}Some tests failed!${NC}"
        fi
    fi

    [[ $FAIL_COUNT -eq 0 ]]
}

# Check if network tests should be skipped
should_skip_network() {
    [[ "$SKIP_NETWORK" == "true" ]]
}

# Log message (only in human mode)
log() {
    if [[ "$JSON_OUTPUT" != "true" ]]; then
        echo "  $*"
    fi
}

# Log error (only in human mode)
log_error() {
    if [[ "$JSON_OUTPUT" != "true" ]]; then
        echo -e "  ${RED}ERROR:${NC} $*" >&2
    fi
}
