#!/bin/bash
# test_json_error.sh - Unit tests for json_error's details argument
#
# json_error once declared `local details="${4:-{}}"`, which bash reads as
# ${4:-{} followed by a literal }: a 4th argument came out with a stray }
# appended, which is not JSON, and jq --argjson rejected it. No caller passed
# a 4th argument yet, so it was latent.

# Load json_error on its own from the CLI (it exits, so each call runs in a
# subshell).
load_json_error() {
    eval "$(sed -n '/^json_error()/,/^}/p' "$POLIS_BIN")"
}

test_json_error_with_details() {
    setup_test_env "json_error_details"
    trap teardown_test_env EXIT
    load_json_error || return 1

    local out
    out=$( (JSON_MODE=true; json_error "demo" "BAD" "something failed" '{"field":"x"}') 2>&1 )
    assert_valid_json "$out" || return 1
    if [ "$(echo "$out" | jq -r '.error.details.field')" != "x" ]; then
        log "  FAIL: details not passed through: $out"
        return 1
    fi
    return 0
}

test_json_error_without_details() {
    setup_test_env "json_error_no_details"
    trap teardown_test_env EXIT
    load_json_error || return 1

    local out
    out=$( (JSON_MODE=true; json_error "demo" "BAD" "something failed") 2>&1 )
    assert_valid_json "$out" || return 1
    if [ "$(echo "$out" | jq -c '.error.details')" != "{}" ]; then
        log "  FAIL: details should default to {}: $out"
        return 1
    fi
    return 0
}

run_test "json_error With Details" test_json_error_with_details
run_test "json_error Without Details" test_json_error_without_details
