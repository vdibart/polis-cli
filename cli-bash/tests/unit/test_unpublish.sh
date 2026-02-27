#!/bin/bash
# test_unpublish.sh - Unit tests for 'polis unpublish' command
#
# Tests covered:
#   - Unpublish removes .md and .html files
#   - Unpublish updates public.jsonl index
#   - Unpublish rejects paths outside posts/
#   - Unpublish rejects path traversal

# Test: Unpublish removes files
test_unpublish_removes_files() {
    setup_test_env "unpublish_removes_files"
    trap teardown_test_env EXIT

    # Initialize
    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    # Create and post
    create_sample_post "my-post.md" "Unpublish Test"

    local post_result
    post_result=$("$POLIS_BIN" --json post my-post.md 2>&1)
    local exit_code=$?

    assert_exit_code 0 "$exit_code" || return 1
    assert_valid_json "$post_result" || return 1

    # Get canonical path from response
    local canonical_path
    canonical_path=$(echo "$post_result" | jq -r '.data.file_path')

    # Verify post file exists before unpublish
    assert_file_exists "$canonical_path" || return 1

    # Create a fake .html file alongside the .md
    local html_path="${canonical_path%.md}.html"
    echo "<html><body>Test</body></html>" > "$html_path"
    assert_file_exists "$html_path" || return 1

    # Unpublish
    local unpublish_result
    unpublish_result=$("$POLIS_BIN" --json unpublish "$canonical_path" 2>&1)
    exit_code=$?

    # Allow non-zero exit if DS is unreachable (the command still removes local files)
    # Check that files are removed
    assert_file_not_exists "$canonical_path" || return 1
    assert_file_not_exists "$html_path" || return 1

    log "  [OK] Both .md and .html files removed"
    return 0
}

# Test: Unpublish updates index
test_unpublish_updates_index() {
    setup_test_env "unpublish_updates_index"
    trap teardown_test_env EXIT

    # Initialize
    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    # Create and post two posts
    create_sample_post "post-one.md" "Post One"
    local result1
    result1=$("$POLIS_BIN" --json post post-one.md 2>&1)
    assert_exit_code 0 $? || return 1

    create_sample_post "post-two.md" "Post Two"
    local result2
    result2=$("$POLIS_BIN" --json post post-two.md 2>&1)
    assert_exit_code 0 $? || return 1

    # Verify both in index
    assert_file_contains "metadata/public.jsonl" "Post One" || return 1
    assert_file_contains "metadata/public.jsonl" "Post Two" || return 1

    # Unpublish post one
    local path1
    path1=$(echo "$result1" | jq -r '.data.file_path')

    "$POLIS_BIN" --json unpublish "$path1" > /dev/null 2>&1

    # Post One should be gone from index, Post Two should remain
    assert_file_not_contains "metadata/public.jsonl" "Post One" || return 1
    assert_file_contains "metadata/public.jsonl" "Post Two" || return 1

    log "  [OK] Index entry removed, other posts preserved"
    return 0
}

# Test: Unpublish rejects paths outside posts/
test_unpublish_invalid_path() {
    setup_test_env "unpublish_invalid_path"
    trap teardown_test_env EXIT

    # Initialize
    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    # Try to unpublish a path that doesn't start with posts/
    local result
    result=$("$POLIS_BIN" --json unpublish "metadata/public.jsonl" 2>&1)
    local exit_code=$?

    assert_exit_code 1 "$exit_code" || return 1
    assert_valid_json "$result" || return 1
    assert_json_field "$result" ".status" "error" || return 1
    assert_json_field "$result" ".error.code" "INVALID_INPUT" || return 1

    log "  [OK] Rejected path outside posts/"
    return 0
}

# Test: Unpublish rejects path traversal
test_unpublish_path_traversal() {
    setup_test_env "unpublish_path_traversal"
    trap teardown_test_env EXIT

    # Initialize
    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    # Try path traversal
    local result
    result=$("$POLIS_BIN" --json unpublish "posts/../../etc/passwd" 2>&1)
    local exit_code=$?

    assert_exit_code 1 "$exit_code" || return 1
    assert_valid_json "$result" || return 1
    assert_json_field "$result" ".status" "error" || return 1
    assert_json_field "$result" ".error.code" "INVALID_INPUT" || return 1

    log "  [OK] Rejected path traversal attempt"
    return 0
}

# Run tests
run_test "Unpublish Removes Files" test_unpublish_removes_files
run_test "Unpublish Updates Index" test_unpublish_updates_index
run_test "Unpublish Invalid Path" test_unpublish_invalid_path
run_test "Unpublish Path Traversal" test_unpublish_path_traversal
