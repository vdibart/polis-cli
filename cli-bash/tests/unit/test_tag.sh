#!/bin/bash
# test_tag.sh - Unit tests for 'polis tag' command
#
# Tests covered:
#   - Tag list with no tags
#   - Tag apply creates tag file
#   - Tag show displays targets
#   - Tag apply duplicate is idempotent
#   - Tag remove removes target
#   - Tag delete removes file
#   - Tag name normalization
#   - JSON output mode

# Test: List with no tags
test_tag_list_empty() {
    setup_test_env "tag_list_empty"
    trap teardown_test_env EXIT

    "$POLIS_BIN" init > /dev/null 2>&1

    local result
    result=$("$POLIS_BIN" --json tag list 2>&1)
    local exit_code=$?

    assert_exit_code 0 "$exit_code" || return 1
    assert_valid_json "$result" || return 1
    assert_json_success "$result" "tag" || return 1

    local count
    count=$(echo "$result" | jq -r '.data.count')
    if [ "$count" != "0" ]; then
        log_error "Expected 0 tags, got: $count"
        return 1
    fi

    return 0
}

# Test: Apply creates tag and file
test_tag_apply() {
    setup_test_env "tag_apply"
    trap teardown_test_env EXIT

    "$POLIS_BIN" init > /dev/null 2>&1

    local result
    result=$("$POLIS_BIN" --json tag apply rust "https://example.com/posts/hello" 2>&1)
    local exit_code=$?

    assert_exit_code 0 "$exit_code" || return 1
    assert_valid_json "$result" || return 1
    assert_json_success "$result" "tag" || return 1

    # Check tag name
    local tag_name
    tag_name=$(echo "$result" | jq -r '.data.tag')
    if [ "$tag_name" != "rust" ]; then
        log_error "Expected tag 'rust', got: $tag_name"
        return 1
    fi

    # Check file exists
    assert_file_exists "content/pub.polis.core/tag/rust.json" || return 1

    # Verify file is valid JSON with signature
    local sig
    sig=$(jq -r '.signature' "content/pub.polis.core/tag/rust.json")
    if [ -z "$sig" ] || [ "$sig" = "null" ]; then
        log_error "Tag file missing signature"
        return 1
    fi

    return 0
}

# Test: Show targets
test_tag_show() {
    setup_test_env "tag_show"
    trap teardown_test_env EXIT

    "$POLIS_BIN" init > /dev/null 2>&1
    "$POLIS_BIN" tag apply rust "https://example.com/posts/hello" > /dev/null 2>&1

    local result
    result=$("$POLIS_BIN" --json tag show rust 2>&1)
    local exit_code=$?

    assert_exit_code 0 "$exit_code" || return 1
    assert_valid_json "$result" || return 1

    local count
    count=$(echo "$result" | jq -r '.data.count')
    if [ "$count" != "1" ]; then
        log_error "Expected 1 target, got: $count"
        return 1
    fi

    return 0
}

# Test: Duplicate apply is idempotent
test_tag_apply_duplicate() {
    setup_test_env "tag_apply_dup"
    trap teardown_test_env EXIT

    "$POLIS_BIN" init > /dev/null 2>&1
    "$POLIS_BIN" tag apply rust "https://example.com/a" > /dev/null 2>&1
    "$POLIS_BIN" tag apply rust "https://example.com/a" > /dev/null 2>&1

    local result
    result=$("$POLIS_BIN" --json tag show rust 2>&1)
    local count
    count=$(echo "$result" | jq -r '.data.count')
    if [ "$count" != "1" ]; then
        log_error "Duplicate apply should not add second target, got: $count"
        return 1
    fi

    return 0
}

# Test: Remove target
test_tag_remove() {
    setup_test_env "tag_remove"
    trap teardown_test_env EXIT

    "$POLIS_BIN" init > /dev/null 2>&1
    "$POLIS_BIN" tag apply rust "https://example.com/a" > /dev/null 2>&1
    "$POLIS_BIN" tag apply rust "https://example.com/b" > /dev/null 2>&1

    local result
    result=$("$POLIS_BIN" --json tag remove rust "https://example.com/a" 2>&1)
    local exit_code=$?

    assert_exit_code 0 "$exit_code" || return 1
    assert_valid_json "$result" || return 1

    local count
    count=$(echo "$result" | jq -r '.data.count')
    if [ "$count" != "1" ]; then
        log_error "Expected 1 target after removal, got: $count"
        return 1
    fi

    return 0
}

# Test: Delete tag
test_tag_delete() {
    setup_test_env "tag_delete"
    trap teardown_test_env EXIT

    "$POLIS_BIN" init > /dev/null 2>&1
    "$POLIS_BIN" tag apply rust "https://example.com/a" > /dev/null 2>&1

    local result
    result=$("$POLIS_BIN" --json tag delete rust 2>&1)
    local exit_code=$?

    assert_exit_code 0 "$exit_code" || return 1
    assert_valid_json "$result" || return 1

    assert_file_not_exists "content/pub.polis.core/tag/rust.json" || return 1

    return 0
}

# Test: Tag name normalization
test_tag_normalize() {
    setup_test_env "tag_normalize"
    trap teardown_test_env EXIT

    "$POLIS_BIN" init > /dev/null 2>&1
    "$POLIS_BIN" tag apply "My Tag" "https://example.com/a" > /dev/null 2>&1

    # Should normalize to "my-tag"
    assert_file_exists "content/pub.polis.core/tag/my-tag.json" || return 1

    local result
    result=$("$POLIS_BIN" --json tag show "My Tag" 2>&1)
    local tag_name
    tag_name=$(echo "$result" | jq -r '.data.tag')
    if [ "$tag_name" != "my-tag" ]; then
        log_error "Expected normalized name 'my-tag', got: $tag_name"
        return 1
    fi

    return 0
}

# Run tests
run_test "Tag List Empty" test_tag_list_empty
run_test "Tag Apply" test_tag_apply
run_test "Tag Show" test_tag_show
run_test "Tag Apply Duplicate" test_tag_apply_duplicate
run_test "Tag Remove" test_tag_remove
run_test "Tag Delete" test_tag_delete
run_test "Tag Normalize" test_tag_normalize
