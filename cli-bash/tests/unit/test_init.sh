#!/bin/bash
# test_init.sh - Unit tests for 'polis init' command
#
# Tests covered:
#   - Basic initialization creates correct structure
#   - Custom directory paths work
#   - Double-init returns error

# Test: Basic init creates correct structure
test_init_basic() {
    setup_test_env "init_basic"
    trap teardown_test_env EXIT

    local result
    result=$("$POLIS_BIN" --json init 2>&1)
    local exit_code=$?

    # Check exit code
    assert_exit_code 0 "$exit_code" || return 1

    # Validate JSON response
    assert_valid_json "$result" || return 1
    assert_json_field "$result" ".status" "success" || return 1
    assert_json_field "$result" ".command" "init" || return 1

    # Verify directories created
    assert_dir_exists ".polis/keys" || return 1
    assert_dir_exists "content/pub.polis.core/post" || return 1
    assert_dir_exists "content/pub.polis.core/comment" || return 1
    assert_dir_exists "content/pub.polis.core/follow" || return 1
    assert_dir_exists "site/snippets" || return 1
    assert_dir_exists "site/themes" || return 1
    assert_dir_exists ".well-known" || return 1

    # Verify key files created
    assert_file_exists ".polis/keys/id_ed25519" || return 1
    assert_file_exists ".polis/keys/id_ed25519.pub" || return 1

    # Verify config files created
    assert_file_exists ".well-known/polis" || return 1
    assert_file_exists "content/pub.polis.core/index.jsonl" || return 1
    assert_file_exists "content/pub.polis.core/comment/blessed.json" || return 1
    assert_file_exists "content/pub.polis.core/follow/following.json" || return 1

    # Verify .well-known/polis contains valid JSON with public key and bundle registry
    assert_file_contains ".well-known/polis" '"public_key"' || return 1
    assert_file_contains ".well-known/polis" '"version"' || return 1
    assert_file_contains ".well-known/polis" '"bundles"' || return 1

    return 0
}

# Test: Init with custom directories
test_init_custom_dirs() {
    setup_test_env "init_custom_dirs"
    trap teardown_test_env EXIT

    local result
    result=$("$POLIS_BIN" --json init \
        --posts-dir articles \
        --comments-dir replies 2>&1)
    local exit_code=$?

    assert_exit_code 0 "$exit_code" || return 1
    assert_valid_json "$result" || return 1
    assert_json_field "$result" ".status" "success" || return 1

    # Verify custom directories created
    assert_dir_exists "articles" || return 1
    assert_dir_exists "replies" || return 1

    # Default content directories should NOT exist
    assert_file_not_exists "content/pub.polis.core/post" || return 1
    assert_file_not_exists "content/pub.polis.core/comment" || return 1

    return 0
}

# Test: Init fails if already initialized
test_init_already_initialized() {
    setup_test_env "init_already_initialized"
    trap teardown_test_env EXIT

    # First init should succeed
    "$POLIS_BIN" --json init > /dev/null 2>&1

    # Second init should fail
    local result
    result=$("$POLIS_BIN" --json init 2>&1)
    local exit_code=$?

    assert_exit_code 1 "$exit_code" || return 1
    assert_valid_json "$result" || return 1
    assert_json_field "$result" ".status" "error" || return 1

    # Should be INVALID_STATE or similar error
    local error_code
    error_code=$(echo "$result" | jq -r '.error.code')
    log "  Error code: $error_code"

    # Accept any error indicating already initialized
    [[ -n "$error_code" && "$error_code" != "null" ]] || return 1

    return 0
}

# Run tests
run_test "Init Basic" test_init_basic
run_test "Init Custom Directories" test_init_custom_dirs
run_test "Init Already Initialized" test_init_already_initialized
