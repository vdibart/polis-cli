#!/bin/bash
# test_unregister.sh - Unit tests for 'polis unregister' confirmation
#
# --json cannot show a prompt. Unregister under --json used to skip the
# confirmation and go ahead; it now refuses without --force, as the Go CLI does.

test_unregister_json_without_force_refuses() {
    setup_test_env "unregister_json_refuses"
    trap teardown_test_env EXIT

    export POLIS_BASE_URL="https://test.example.com"
    export DISCOVERY_SERVICE_URL="http://127.0.0.1:1"
    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    local out exit_code
    out=$("$POLIS_BIN" --json unregister 2>&1)
    exit_code=$?
    if [ "$exit_code" -eq 0 ]; then
        log "  FAIL: unregister --json without --force exited 0: $out"
        return 1
    fi
    assert_valid_json "$out" || return 1
    if [ "$(echo "$out" | jq -r '.error.code')" != "CONFIRMATION_REQUIRED" ]; then
        log "  FAIL: want CONFIRMATION_REQUIRED, got: $out"
        return 1
    fi
    return 0
}

test_unregister_json_with_force_proceeds() {
    setup_test_env "unregister_json_force"
    trap teardown_test_env EXIT

    export POLIS_BASE_URL="https://test.example.com"
    export DISCOVERY_SERVICE_URL="http://127.0.0.1:1"
    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    # The discovery service is unreachable, so the command fails — but at the
    # registration check, past the confirmation, which is what is asserted.
    local out code
    for flag in --force -f; do
        out=$("$POLIS_BIN" --json unregister "$flag" 2>&1)
        code=$(echo "$out" | jq -r '.error.code // empty' 2>/dev/null)
        if [ "$code" = "CONFIRMATION_REQUIRED" ]; then
            log "  FAIL: unregister --json $flag still asked for confirmation: $out"
            return 1
        fi
    done
    return 0
}

run_test "Unregister --json Without --force Refuses" test_unregister_json_without_force_refuses
run_test "Unregister --json With --force Proceeds" test_unregister_json_with_force_proceeds
