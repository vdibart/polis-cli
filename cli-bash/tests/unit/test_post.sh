#!/bin/bash
# test_post.sh - Unit tests for 'polis post' command
#
# Tests covered:
#   - Basic post creates canonical file with frontmatter
#   - Post missing file returns error
#   - Post updates index

# Test: Basic post works correctly
test_post_basic() {
    setup_test_env "post_basic"
    trap teardown_test_env EXIT

    # Initialize first
    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    # Create sample post
    create_sample_post "my-post.md" "Test Post"

    local result
    result=$("$POLIS_BIN" --json post my-post.md 2>&1)
    local exit_code=$?

    assert_exit_code 0 "$exit_code" || return 1
    assert_valid_json "$result" || return 1
    assert_json_success "$result" "post" || return 1

    # Verify data fields exist
    assert_json_has_field "$result" ".data.file_path" || return 1
    assert_json_has_field "$result" ".data.content_hash" || return 1
    assert_json_has_field "$result" ".data.timestamp" || return 1
    assert_json_has_field "$result" ".data.signature" || return 1

    # Get canonical path from response
    local canonical_path
    canonical_path=$(echo "$result" | jq -r '.data.file_path')

    # Verify canonical file was created
    assert_file_exists "$canonical_path" || return 1

    # Verify original file was moved (no longer exists at original location)
    assert_file_not_exists "my-post.md" || return 1

    # Verify frontmatter was added
    assert_file_contains "$canonical_path" "^---" || return 1
    assert_file_contains "$canonical_path" "title:" || return 1
    assert_file_contains "$canonical_path" "current-version:" || return 1
    assert_file_contains "$canonical_path" "signature:" || return 1

    # Verify index was updated
    assert_file_contains "content/pub.polis.core/index.jsonl" "Test Post" || return 1

    # Verify hash format
    local content_hash
    content_hash=$(echo "$result" | jq -r '.data.content_hash')
    assert_hash_format "$content_hash" || return 1

    return 0
}

# Test: Post missing file returns error
test_post_missing_file() {
    setup_test_env "post_missing_file"
    trap teardown_test_env EXIT

    # Initialize first
    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    local result
    result=$("$POLIS_BIN" --json post nonexistent.md 2>&1)
    local exit_code=$?

    assert_exit_code 1 "$exit_code" || return 1
    assert_valid_json "$result" || return 1
    assert_json_field "$result" ".status" "error" || return 1

    # Check for appropriate error code
    local error_code
    error_code=$(echo "$result" | jq -r '.error.code')
    log "  Error code: $error_code"

    # Should be FILE_NOT_FOUND or similar
    [[ -n "$error_code" && "$error_code" != "null" ]] || return 1

    return 0
}

# Test: Post without init returns error
test_post_without_init() {
    setup_test_env "post_without_init"
    trap teardown_test_env EXIT

    # Do NOT initialize - try to post directly
    create_sample_post "my-post.md" "Test Post"

    local result
    result=$("$POLIS_BIN" --json post my-post.md 2>&1)
    local exit_code=$?

    assert_exit_code 1 "$exit_code" || return 1
    assert_valid_json "$result" || return 1
    assert_json_field "$result" ".status" "error" || return 1

    return 0
}

# Run tests
# Test: post refuses a source that states its own licence terms
#
# This CLI cannot carry a `license:` block into a new post, so publishing would
# silently drop the author's per-work terms. It must FAIL instead, from a file
# and from stdin (where the already-published check is skipped), and leave the
# source where it was.
test_post_refuses_licensed_source() {
    setup_test_env "post_licensed"
    trap teardown_test_env EXIT

    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    printf '%s\n' '---' 'title: Terms of my own' 'license:' '  train-ai: y' '---' '' 'Body text.' > licensed.md
    local before
    before=$(cat licensed.md)

    local result exit_code error_code
    result=$("$POLIS_BIN" --json post licensed.md 2>&1)
    exit_code=$?
    assert_exit_code 1 "$exit_code" || return 1
    assert_valid_json "$result" || return 1
    error_code=$(echo "$result" | jq -r '.error.code')
    if [[ "$error_code" != "UNSUPPORTED_CONTENT" ]]; then
        log_error "file: expected UNSUPPORTED_CONTENT, got: $error_code"
        return 1
    fi
    if [[ "$(cat licensed.md)" != "$before" ]]; then
        log_error "Refused post still moved or changed the source"
        return 1
    fi

    result=$("$POLIS_BIN" --json post - < licensed.md 2>&1)
    exit_code=$?
    assert_exit_code 1 "$exit_code" || return 1
    error_code=$(echo "$result" | jq -r '.error.code')
    if [[ "$error_code" != "UNSUPPORTED_CONTENT" ]]; then
        log_error "stdin: expected UNSUPPORTED_CONTENT, got: $error_code"
        return 1
    fi
    if [[ -n "$(find content -path '*/post/*' -name '*.md' 2>/dev/null)" ]]; then
        log_error "A refused post still wrote a post file"
        return 1
    fi

    log "  [OK] post refused a source with licence terms, from a file and from stdin"
    return 0
}

# Test: a body that mentions "license:" is not a licence block
test_post_body_mentioning_license_still_publishes() {
    setup_test_env "post_mentions_license"
    trap teardown_test_env EXIT

    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    printf '%s\n' '# Mentions' '' 'license: is a word some frontmatter uses.' > mentions.md
    local result
    result=$("$POLIS_BIN" --json post mentions.md 2>&1)
    assert_exit_code 0 "$?" || return 1
    assert_json_success "$result" "post" || return 1
    return 0
}

run_test "Post Basic" test_post_basic
run_test "Post Refuses Licensed Source" test_post_refuses_licensed_source
run_test "Post Body Mentioning License Still Publishes" test_post_body_mentioning_license_still_publishes
run_test "Post Missing File" test_post_missing_file
run_test "Post Without Init" test_post_without_init
