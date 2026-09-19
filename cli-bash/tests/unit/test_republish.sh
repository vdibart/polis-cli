#!/bin/bash
# test_republish.sh - Unit tests for 'polis republish' command
#
# Tests covered:
#   - Basic republish updates version history
#   - Republish unpublished file returns error
#   - Republish without changes (edge case)

# Test: Basic republish works correctly
test_republish_basic() {
    setup_test_env "republish_basic"
    trap teardown_test_env EXIT

    # Initialize
    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    # Create and post
    create_sample_post "my-post.md" "Test Post"
    local post_result
    post_result=$("$POLIS_BIN" --json post my-post.md 2>&1)

    # Get canonical path and initial hash
    local canonical_path initial_hash
    canonical_path=$(echo "$post_result" | jq -r '.data.file_path')
    initial_hash=$(echo "$post_result" | jq -r '.data.content_hash')

    log "  Initial hash: $initial_hash"

    # Modify the file (append content)
    append_to_file "$canonical_path" "## Added Section

This content was added for republish testing."

    # Republish
    local result
    result=$("$POLIS_BIN" --json republish "$canonical_path" 2>&1)
    local exit_code=$?

    assert_exit_code 0 "$exit_code" || return 1
    assert_valid_json "$result" || return 1
    assert_json_success "$result" "republish" || return 1

    # Verify new hash is different
    local new_hash
    new_hash=$(echo "$result" | jq -r '.data.content_hash // .data.new_version')

    if [[ -n "$new_hash" && "$new_hash" != "null" ]]; then
        if [[ "$initial_hash" != "$new_hash" ]]; then
            log "  [OK] Hash changed: $new_hash"
        else
            log_error "Hash should have changed after content modification"
            return 1
        fi
    fi

    # Verify version history in frontmatter
    assert_file_contains "$canonical_path" "version-history:" || return 1

    # Count versions (should be at least 2)
    local version_count
    version_count=$(grep -c "sha256:" "$canonical_path" 2>/dev/null || echo "0")
    if [[ "$version_count" -ge 2 ]]; then
        log "  [OK] Version history has $version_count entries"
    else
        log_error "Expected at least 2 versions, got $version_count"
        return 1
    fi

    return 0
}

# Test: Republish unpublished file returns error
test_republish_unpublished() {
    setup_test_env "republish_unpublished"
    trap teardown_test_env EXIT

    # Initialize
    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    # Create a file but don't post it
    create_sample_post "unpublished.md" "Unpublished Post"

    local result
    result=$("$POLIS_BIN" --json republish unpublished.md 2>&1)
    local exit_code=$?

    assert_exit_code 1 "$exit_code" || return 1
    assert_valid_json "$result" || return 1
    assert_json_field "$result" ".status" "error" || return 1

    # Should indicate file is not in published format
    local error_code
    error_code=$(echo "$result" | jq -r '.error.code')
    log "  Error code: $error_code"

    [[ -n "$error_code" && "$error_code" != "null" ]] || return 1

    return 0
}

# Test: Republish nonexistent file returns error
test_republish_missing_file() {
    setup_test_env "republish_missing_file"
    trap teardown_test_env EXIT

    # Initialize
    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    local result
    result=$("$POLIS_BIN" --json republish nonexistent.md 2>&1)
    local exit_code=$?

    assert_exit_code 1 "$exit_code" || return 1
    assert_valid_json "$result" || return 1
    assert_json_field "$result" ".status" "error" || return 1

    return 0
}

# Test: Multiple republishes work correctly
test_republish_multiple() {
    setup_test_env "republish_multiple"
    trap teardown_test_env EXIT

    # Initialize
    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    # Create and post
    create_long_post "my-post.md" "Long Post"
    local post_result
    post_result=$("$POLIS_BIN" --json post my-post.md 2>&1)
    local canonical_path
    canonical_path=$(echo "$post_result" | jq -r '.data.file_path')

    # First republish
    append_to_file "$canonical_path" "## Version 2 content"
    "$POLIS_BIN" --json republish "$canonical_path" > /dev/null 2>&1

    # Second republish
    append_to_file "$canonical_path" "## Version 3 content"
    "$POLIS_BIN" --json republish "$canonical_path" > /dev/null 2>&1

    # Third republish
    append_to_file "$canonical_path" "## Version 4 content"
    local result
    result=$("$POLIS_BIN" --json republish "$canonical_path" 2>&1)
    local exit_code=$?

    assert_exit_code 0 "$exit_code" || return 1
    assert_valid_json "$result" || return 1

    # Count versions (should be 4 now)
    local version_count
    version_count=$(grep -c "sha256:" "$canonical_path" 2>/dev/null || echo "0")
    if [[ "$version_count" -ge 4 ]]; then
        log "  [OK] Version history has $version_count entries"
    else
        log_error "Expected at least 4 versions, got $version_count"
        return 1
    fi

    return 0
}

# Test: Republish refuses a post carrying licence terms
#
# This CLI rebuilds frontmatter from a fixed template, so it would drop a
# `license:` block and re-sign without it. The post would still verify, which
# is what makes the loss silent — so republish must FAIL rather than proceed.
# Licence support lives in the Go CLI; bash is not taught to preserve the block.
test_republish_refuses_licensed_post() {
    setup_test_env "republish_licensed"
    trap teardown_test_env EXIT

    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    # A published post carrying materialised licence terms, as the Go CLI writes
    # them: a nested block inside the signed frontmatter.
    mkdir -p content/pub.polis.core/post/20260827
    local licensed_path="content/pub.polis.core/post/20260827/licensed.md"
    cat > "$licensed_path" <<'LICENSED_EOF'
---
title: A licensed post
published: 2026-08-27T14:02:00Z
generator: polis-cli-go/0.67.0
current-version: sha256:abc123
version-history:
  - sha256:abc123 (2026-08-27T14:02:00Z)
license:
  v: pub.polis.license.v1
  profile: pub.polis.license.reserved/1
  train-ai: n
  search: y
signature: notarealsignature
---

Body text.
LICENSED_EOF

    local before
    before=$(cat "$licensed_path")

    local result
    result=$("$POLIS_BIN" --json republish "$licensed_path" 2>&1)
    local exit_code=$?

    assert_exit_code 1 "$exit_code" || return 1
    assert_valid_json "$result" || return 1
    assert_json_field "$result" ".status" "error" || return 1

    local error_code
    error_code=$(echo "$result" | jq -r '.error.code')
    if [[ "$error_code" != "UNSUPPORTED_CONTENT" ]]; then
        log_error "Expected UNSUPPORTED_CONTENT, got: $error_code"
        return 1
    fi

    # The point of failing is that nothing is lost — the file must be untouched.
    if [[ "$(cat "$licensed_path")" != "$before" ]]; then
        log_error "Refused republish still modified the post"
        return 1
    fi

    log "  [OK] Republish refused a licensed post and left it untouched"
    return 0
}

# Test: an unlicensed post republishes normally
#
# Guards the other direction: the licence check must not block the ordinary
# path, and a body that merely mentions "license:" is not a licence block.
test_republish_unlicensed_still_works() {
    setup_test_env "republish_unlicensed"
    trap teardown_test_env EXIT

    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    create_sample_post "mentions.md" "Mentions Licences"
    local result
    result=$("$POLIS_BIN" --json post mentions.md 2>&1)
    local canonical_path
    canonical_path=$(echo "$result" | jq -r '.data.file_path // .data.path')

    # A body line that looks like the key, but is not in the frontmatter.
    append_to_file "$canonical_path" "Some posts start with license: reserved in frontmatter."

    result=$("$POLIS_BIN" --json republish "$canonical_path" 2>&1)
    local exit_code=$?

    assert_exit_code 0 "$exit_code" || return 1
    assert_valid_json "$result" || return 1
    assert_json_field "$result" ".status" "success" || return 1

    log "  [OK] Unlicensed post republished; body mention did not trip the guard"
    return 0
}

# Run tests
run_test "Republish Basic" test_republish_basic
run_test "Republish Unpublished File" test_republish_unpublished
run_test "Republish Missing File" test_republish_missing_file
run_test "Republish Multiple Versions" test_republish_multiple
run_test "Republish Refuses Licensed Post" test_republish_refuses_licensed_post
run_test "Republish Unlicensed Still Works" test_republish_unlicensed_still_works
