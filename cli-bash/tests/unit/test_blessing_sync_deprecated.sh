#!/bin/bash
# test_blessing_sync_deprecated.sh - bash's broken blessing read is gone
#
# bash rebuilt blessed.json from the wrong set of blessings: it asked the
# discovery service for blessings whose ACTOR is this site, and a blessing's
# actor is the commenter. That read ran behind three commands. It is deprecated:
# 'blessing sync' refuses and points at the Go CLI, 'blessing requests' no
# longer runs it, and 'rebuild --comments' leaves blessed.json alone.

test_blessing_sync_refuses() {
    setup_test_env "blessing_sync_deprecated"
    trap teardown_test_env EXIT
    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    local out
    out=$(POLIS_BASE_URL="https://example.com" "$POLIS_BIN" --json blessing sync 2>&1)
    local rc=$?
    if [ $rc -eq 0 ]; then
        log "  FAIL: blessing sync exited 0"
        return 1
    fi
    if [ "$(echo "$out" | jq -r '.error.code' 2>/dev/null)" != "DEPRECATED" ]; then
        log "  FAIL: expected error code DEPRECATED: $out"
        return 1
    fi
    return 0
}

test_rebuild_comments_leaves_blessed_json_alone() {
    setup_test_env "rebuild_comments_blessed"
    trap teardown_test_env EXIT
    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    mkdir -p content/pub.polis.core/comment
    local list='{"version":"polis-cli-go/test","comments":[{"post":"posts/20260901/p.md","blessed":[{"url":"https://bob.example/c/1.md","version":"sha256:aaa","blessed_at":"2026-09-01T00:00:00Z"}]}]}'
    echo "$list" > content/pub.polis.core/comment/blessed.json
    local before
    before=$(cat content/pub.polis.core/comment/blessed.json)

    POLIS_BASE_URL="https://example.com" "$POLIS_BIN" rebuild --comments > /dev/null 2>&1

    if [ "$(cat content/pub.polis.core/comment/blessed.json)" != "$before" ]; then
        log "  FAIL: rebuild --comments rewrote blessed.json"
        return 1
    fi
    return 0
}

run_test "Blessing Sync Refuses" test_blessing_sync_refuses
run_test "Rebuild Comments Leaves blessed.json Alone" test_rebuild_comments_leaves_blessed_json_alone
