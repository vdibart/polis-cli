#!/bin/bash
# test_rebuild.sh - Unit tests for 'polis rebuild'
#
# Tests covered:
#   - Rebuild preserves tag and attestation index lines (Signet epic 37 D3)
#   - A second rebuild does not duplicate preserved lines

REBUILD_TAG_LINE='{"type":"tag","path":"content/pub.polis.core/tag/reading.json","title":"reading","published":"2026-01-03T00:00:00Z","current_version":"sha256:cccc"}'
REBUILD_ATTESTATION_LINE='{"type":"attestation","path":"content/pub.polis.core/attestation/20260104T000000Z-abcd.json","title":"pub.polis.attestation.agent-disclosure","published":"2026-01-04T00:00:00Z","current_version":"sha256:dddd"}'
REBUILD_INDEX="content/pub.polis.core/index.jsonl"

# seed_site_with_records: init, publish one post, then append the tag and
# attestation lines the Go CLI writes into the same index.
seed_site_with_records() {
    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1
    create_sample_post "my-post.md" "Test Post" > /dev/null
    "$POLIS_BIN" --json post my-post.md > /dev/null 2>&1 || return 1

    if [ ! -f "$REBUILD_INDEX" ]; then
        log_error "Expected $REBUILD_INDEX after publishing"
        return 1
    fi
    printf '%s\n%s\n' "$REBUILD_TAG_LINE" "$REBUILD_ATTESTATION_LINE" >> "$REBUILD_INDEX"
}

# Test: a bash rebuild used to truncate index.jsonl and walk only markdown, so
# it un-published every tag and attestation a site had indexed.
test_rebuild_preserves_tag_and_attestation_lines() {
    setup_test_env "rebuild_preserves_records"
    trap teardown_test_env EXIT

    seed_site_with_records || return 1

    local exit_code=0
    "$POLIS_BIN" --json rebuild --posts > /dev/null 2>&1 || exit_code=$?
    assert_exit_code 0 "$exit_code" || return 1

    if ! grep -qxF "$REBUILD_TAG_LINE" "$REBUILD_INDEX"; then
        log_error "Tag line was removed by rebuild:"
        cat "$REBUILD_INDEX" >&2
        return 1
    fi
    if ! grep -qxF "$REBUILD_ATTESTATION_LINE" "$REBUILD_INDEX"; then
        log_error "Attestation line was removed by rebuild:"
        cat "$REBUILD_INDEX" >&2
        return 1
    fi
    if ! grep -q '"type":"post"' "$REBUILD_INDEX"; then
        log_error "Post line missing after rebuild"
        return 1
    fi

    log "  [OK] Tag and attestation lines survived rebuild byte-identically"
    return 0
}

# Test: preserved lines are carried, not appended again on every run.
test_rebuild_twice_does_not_duplicate_preserved_lines() {
    setup_test_env "rebuild_twice_records"
    trap teardown_test_env EXIT

    seed_site_with_records || return 1

    "$POLIS_BIN" --json rebuild --posts > /dev/null 2>&1 || return 1
    "$POLIS_BIN" --json rebuild --posts > /dev/null 2>&1 || return 1

    local tags attestations posts
    tags=$(grep -cxF "$REBUILD_TAG_LINE" "$REBUILD_INDEX")
    attestations=$(grep -cxF "$REBUILD_ATTESTATION_LINE" "$REBUILD_INDEX")
    posts=$(grep -c '"type":"post"' "$REBUILD_INDEX")
    if [ "$tags" != "1" ] || [ "$attestations" != "1" ] || [ "$posts" != "1" ]; then
        log_error "Expected one line each, got tag=$tags attestation=$attestations post=$posts"
        cat "$REBUILD_INDEX" >&2
        return 1
    fi

    log "  [OK] Two rebuilds leave exactly one line per entry"
    return 0
}

# Run tests
run_test "Rebuild Preserves Tag And Attestation Lines" test_rebuild_preserves_tag_and_attestation_lines
run_test "Rebuild Twice Does Not Duplicate Preserved Lines" test_rebuild_twice_does_not_duplicate_preserved_lines
