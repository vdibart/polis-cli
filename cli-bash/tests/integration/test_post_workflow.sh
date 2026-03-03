#!/bin/bash
# test_post_workflow.sh - Integration tests for post workflow
#
# Tests covered:
#   - Full post -> republish cycle
#   - Version reconstruction with extract
#   - Stdin post mode
#   - Rebuild index command

# Test: Full post -> republish cycle
test_full_post_cycle() {
    setup_test_env "full_post_cycle"
    trap teardown_test_env EXIT

    # Initialize
    log "Step 1: Initialize"
    local init_result
    init_result=$("$POLIS_BIN" --json init 2>&1)
    assert_json_success "$init_result" "init" || return 1

    # Create and post
    log "Step 2: Create and post"
    create_sample_post "my-post.md" "Integration Test Post"

    local post_result
    post_result=$("$POLIS_BIN" --json post my-post.md 2>&1)
    assert_json_success "$post_result" "post" || return 1

    local canonical_path initial_hash
    canonical_path=$(echo "$post_result" | jq -r '.data.file_path')
    initial_hash=$(echo "$post_result" | jq -r '.data.content_hash')

    log "  Created at: $canonical_path"
    log "  Initial hash: $initial_hash"

    # Verify file structure
    assert_file_exists "$canonical_path" || return 1
    assert_file_not_exists "my-post.md" || return 1

    # Modify and republish
    log "Step 3: Modify and republish"
    append_to_file "$canonical_path" "## Updated Section

This content was added in an update."

    local republish_result
    republish_result=$("$POLIS_BIN" --json republish "$canonical_path" 2>&1)
    assert_json_success "$republish_result" "republish" || return 1

    # Verify version history
    log "Step 4: Verify version history"
    local version_count
    version_count=$(grep -c "sha256:" "$canonical_path" 2>/dev/null || echo "0")
    if [[ "$version_count" -lt 2 ]]; then
        log_error "Expected at least 2 versions"
        return 1
    fi
    log "  Version count: $version_count"

    # Verify index contains the post
    log "Step 5: Verify index"
    assert_file_contains "content/pub.polis.core/index.jsonl" "Integration Test Post" || return 1

    log "  [OK] Full post cycle completed successfully"
    return 0
}

# Test: Stdin post mode
test_stdin_post() {
    setup_test_env "stdin_post"
    trap teardown_test_env EXIT

    # Initialize
    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    # Post from stdin with filename
    local result
    result=$(echo "# Stdin Post

This post was created from stdin." | "$POLIS_BIN" --json post - --filename stdin-post.md 2>&1)
    local exit_code=$?

    assert_exit_code 0 "$exit_code" || return 1
    assert_valid_json "$result" || return 1
    assert_json_success "$result" "post" || return 1

    # Verify file was created
    local canonical_path
    canonical_path=$(echo "$result" | jq -r '.data.file_path')
    assert_file_exists "$canonical_path" || return 1

    # Verify content
    assert_file_contains "$canonical_path" "Stdin Post" || return 1
    assert_file_contains "$canonical_path" "created from stdin" || return 1

    log "  [OK] Stdin post works correctly"
    return 0
}

# Test: Rebuild index command
test_rebuild_index() {
    setup_test_env "rebuild_index"
    trap teardown_test_env EXIT

    # Initialize
    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    # Create multiple posts
    create_sample_post "post1.md" "First Post"
    "$POLIS_BIN" --json post post1.md > /dev/null 2>&1 || return 1

    create_sample_post "post2.md" "Second Post"
    "$POLIS_BIN" --json post post2.md > /dev/null 2>&1 || return 1

    create_sample_post "post3.md" "Third Post"
    "$POLIS_BIN" --json post post3.md > /dev/null 2>&1 || return 1

    # Count initial index entries
    local initial_count
    initial_count=$(wc -l < content/pub.polis.core/index.jsonl)
    log "  Initial index entries: $initial_count"

    # Clear and rebuild index
    echo "" > content/pub.polis.core/index.jsonl

    local result
    result=$("$POLIS_BIN" --json rebuild --posts 2>&1)
    local exit_code=$?

    # Rebuild may not have JSON mode, check for success either way
    if [[ $exit_code -ne 0 ]]; then
        log_error "Rebuild command failed"
        return 1
    fi

    # Verify index was rebuilt
    local rebuild_count
    rebuild_count=$(wc -l < content/pub.polis.core/index.jsonl)
    log "  Rebuilt index entries: $rebuild_count"

    if [[ "$rebuild_count" -ge 3 ]]; then
        log "  [OK] Index rebuilt with $rebuild_count entries"
    else
        log_error "Expected at least 3 entries after rebuild"
        return 1
    fi

    # Verify all posts are in index
    assert_file_contains "content/pub.polis.core/index.jsonl" "First Post" || return 1
    assert_file_contains "content/pub.polis.core/index.jsonl" "Second Post" || return 1
    assert_file_contains "content/pub.polis.core/index.jsonl" "Third Post" || return 1

    return 0
}

# Test: Version reconstruction with extract
test_version_reconstruction() {
    setup_test_env "version_reconstruction"
    trap teardown_test_env EXIT

    # Initialize
    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    # Create and post with specific content
    cat > original-post.md << 'EOF'
# Version Test

This is the original content that should be preserved in version 1.

## Original Section

Original paragraph here.
EOF

    local post_result
    post_result=$("$POLIS_BIN" --json post original-post.md 2>&1)
    local canonical_path v1_hash
    canonical_path=$(echo "$post_result" | jq -r '.data.file_path')
    v1_hash=$(echo "$post_result" | jq -r '.data.content_hash')

    log "  Version 1 hash: $v1_hash"

    # Modify and republish (v2)
    append_to_file "$canonical_path" "## Added in Version 2

New content for version 2."
    "$POLIS_BIN" --json republish "$canonical_path" > /dev/null 2>&1

    # Modify and republish (v3)
    append_to_file "$canonical_path" "## Added in Version 3

Even more content for version 3."
    "$POLIS_BIN" --json republish "$canonical_path" > /dev/null 2>&1

    # Try to reconstruct version 1
    log "  Attempting to reconstruct version 1..."
    local reconstructed
    reconstructed=$("$POLIS_BIN" extract "$canonical_path" "$v1_hash" 2>&1)
    local exit_code=$?

    if [[ $exit_code -ne 0 ]]; then
        log_error "extract command failed"
        log "  Output: $reconstructed"
        return 1
    fi

    # Verify reconstructed content has v1 content but not v2/v3
    if echo "$reconstructed" | grep -q "Original Section"; then
        log "  [OK] Reconstructed version contains original content"
    else
        log_error "Reconstructed version missing original content"
        return 1
    fi

    if echo "$reconstructed" | grep -q "Version 2"; then
        log_error "Reconstructed version should not contain v2 content"
        return 1
    fi

    if echo "$reconstructed" | grep -q "Version 3"; then
        log_error "Reconstructed version should not contain v3 content"
        return 1
    fi

    log "  [OK] Version reconstruction successful"
    return 0
}

# Run tests
run_test "Full Post Cycle" test_full_post_cycle
run_test "Stdin Post" test_stdin_post
run_test "Rebuild Index" test_rebuild_index
run_test "Version Reconstruction" test_version_reconstruction
