#!/bin/bash
# test_upgrade_verify.sh - Unit tests for polis-upgrade download verification
#
# Tests covered:
#   - Happy path: a script matching its published .sha256 is installed
#   - Checksum mismatch: aborts, installed file unchanged
#   - Missing .sha256: aborts, installed file unchanged
#   - Non-https URL: refused before anything is fetched
#   - Downloads pin https across redirects
#   - Malformed versions are rejected
#   - The discovery-service self-update path is gone
#
# No network: polis-upgrade is sourced in a child bash (its main only runs
# when executed) and curl is replaced by a function that serves files from a
# local fixture tree laid out like raw.githubusercontent.com.

UPGRADE_TEST_BIN="$(dirname "$POLIS_BIN")/polis-upgrade"
UPGRADE_TEST_URL="https://raw.githubusercontent.com/vdibart/polis-cli/v9.9.9/cli-bash/polis"

# Build fixtures in the current test dir:
#   served/vdibart/polis-cli/v9.9.9/cli-bash/polis[.sha256]
#   installed/polis (the currently installed script) + installed/polis.orig
# Usage: _upgrade_fixtures good|bad|missing
_upgrade_fixtures() {
    local sum_mode="$1"
    local served="served/vdibart/polis-cli/v9.9.9/cli-bash"
    mkdir -p "$served" installed
    printf '#!/usr/bin/env bash\nVERSION="9.9.9"\necho new\n' > "$served/polis"
    case "$sum_mode" in
        good)
            local hex
            hex=$(sha256sum "$served/polis" 2>/dev/null | awk '{print $1}')
            [[ -n "$hex" ]] || hex=$(shasum -a 256 "$served/polis" | awk '{print $1}')
            printf '%s  polis\n' "$hex" > "$served/polis.sha256"
            ;;
        bad)
            printf '%s  polis\n' "$(printf '0%.0s' {1..64})" > "$served/polis.sha256"
            ;;
        missing)
            ;;
    esac
    printf '#!/usr/bin/env bash\nVERSION="1.0.0"\necho old\n' > installed/polis
    cp installed/polis installed/polis.orig
    : > curl.log
}

# Run a polis-upgrade function in a child shell with curl stubbed.
# Usage: _upgrade_call FUNCTION [ARGS...]
_upgrade_call() {
    FIXTURE_ROOT="$(pwd)" UPGRADE_BIN="$UPGRADE_TEST_BIN" bash -c '
        source "$UPGRADE_BIN"
        curl() {
            local out="" url=""
            echo "ARGS $*" >> "$FIXTURE_ROOT/curl.log"
            while [[ $# -gt 0 ]]; do
                case "$1" in
                    -o|--proto|--proto-redir|--max-time) [[ "$1" == "-o" ]] && out="$2"; shift 2 ;;
                    -*) shift ;;
                    *) url="$1"; shift ;;
                esac
            done
            local src="$FIXTURE_ROOT/served/${url#https://raw.githubusercontent.com/}"
            [[ -f "$src" ]] || return 22
            cp "$src" "$out"
        }
        "$@"
    ' _ "$@"
}

_upgrade_installed_unchanged() {
    if cmp -s installed/polis installed/polis.orig; then
        log "  [OK] Installed file unchanged"
        return 0
    fi
    log_error "Installed file was modified"
    return 1
}

test_upgrade_happy_path() {
    setup_test_env "upgrade_happy_path"
    trap teardown_test_env EXIT
    _upgrade_fixtures good

    local output exit_code=0
    output=$(_upgrade_call install_verified_script "$UPGRADE_TEST_URL" "$(pwd)/installed/polis" polis 2>&1) || exit_code=$?
    assert_exit_code 0 "$exit_code" || { echo "$output"; return 1; }

    if cmp -s installed/polis served/vdibart/polis-cli/v9.9.9/cli-bash/polis; then
        log "  [OK] Verified script installed"
    else
        log_error "Installed file does not match the served script"
        return 1
    fi
    [[ -x installed/polis ]] || { log_error "Installed script not executable"; return 1; }
    grep -q "polis.sha256" curl.log || { log_error "Published checksum was not fetched"; return 1; }
    return 0
}

test_upgrade_checksum_mismatch() {
    setup_test_env "upgrade_checksum_mismatch"
    trap teardown_test_env EXIT
    _upgrade_fixtures bad

    local output exit_code=0
    output=$(_upgrade_call install_verified_script "$UPGRADE_TEST_URL" "$(pwd)/installed/polis" polis 2>&1) || exit_code=$?
    if [[ "$exit_code" -eq 0 ]]; then
        log_error "Install succeeded despite a checksum mismatch"
        return 1
    fi
    echo "$output" | grep -q "Checksum mismatch" || { log_error "No mismatch message: $output"; return 1; }
    _upgrade_installed_unchanged || return 1
    return 0
}

test_upgrade_checksum_missing() {
    setup_test_env "upgrade_checksum_missing"
    trap teardown_test_env EXIT
    _upgrade_fixtures missing

    local output exit_code=0
    output=$(_upgrade_call install_verified_script "$UPGRADE_TEST_URL" "$(pwd)/installed/polis" polis 2>&1) || exit_code=$?
    if [[ "$exit_code" -eq 0 ]]; then
        log_error "Install succeeded without a published checksum"
        return 1
    fi
    echo "$output" | grep -q "Could not fetch the published checksum" || { log_error "No fetch-failure message: $output"; return 1; }
    _upgrade_installed_unchanged || return 1
    return 0
}

test_upgrade_refuses_http() {
    setup_test_env "upgrade_refuses_http"
    trap teardown_test_env EXIT
    _upgrade_fixtures good

    local output exit_code=0
    output=$(_upgrade_call install_verified_script "http://raw.githubusercontent.com/vdibart/polis-cli/v9.9.9/cli-bash/polis" "$(pwd)/installed/polis" polis 2>&1) || exit_code=$?
    if [[ "$exit_code" -eq 0 ]]; then
        log_error "An http URL was accepted"
        return 1
    fi
    echo "$output" | grep -q "non-https" || { log_error "No refusal message: $output"; return 1; }
    if [[ -s curl.log ]]; then
        log_error "curl ran for an http URL: $(cat curl.log)"
        return 1
    fi
    _upgrade_installed_unchanged || return 1

    exit_code=0
    _upgrade_call fetch_https "http://example.com/x" "$(pwd)/x" >/dev/null 2>&1 || exit_code=$?
    [[ "$exit_code" -ne 0 ]] || { log_error "fetch_https accepted http"; return 1; }
    exit_code=0
    _upgrade_call fetch_https "ftp://example.com/x" "$(pwd)/x" >/dev/null 2>&1 || exit_code=$?
    [[ "$exit_code" -ne 0 ]] || { log_error "fetch_https accepted ftp"; return 1; }
    log "  [OK] Non-https URLs refused before fetching"
    return 0
}

test_upgrade_https_pinned_across_redirects() {
    setup_test_env "upgrade_https_redirects"
    trap teardown_test_env EXIT
    _upgrade_fixtures good

    _upgrade_call install_verified_script "$UPGRADE_TEST_URL" "$(pwd)/installed/polis" polis >/dev/null 2>&1 || true
    local lines pinned
    lines=$(grep -c '^ARGS' curl.log)
    pinned=$(grep -c -- "--proto =https --proto-redir =https" curl.log)
    if [[ "$lines" -ge 2 && "$lines" -eq "$pinned" ]]; then
        log "  [OK] Every download pins https, including redirects ($lines calls)"
        return 0
    fi
    log_error "Downloads not pinned to https: $(cat curl.log)"
    return 1
}

test_upgrade_rejects_malformed_versions() {
    setup_test_env "upgrade_malformed_versions"
    trap teardown_test_env EXIT

    _upgrade_call is_valid_version "0.69.0" || { log_error "Rejected a valid version"; return 1; }
    local bad
    for bad in "0.69.0/../../../other/repo/v1.0.0" "0.69" "1.2.3-rc1" 'a[$(id)]' ""; do
        if _upgrade_call is_valid_version "$bad"; then
            log_error "Accepted malformed version: $bad"
            return 1
        fi
    done
    log "  [OK] Only MAJOR.MINOR.PATCH accepted"
    return 0
}

test_upgrade_no_self_update_path() {
    setup_test_env "upgrade_no_self_update"
    trap teardown_test_env EXIT

    if grep -q "check_self_update\|download_url // empty" "$UPGRADE_TEST_BIN"; then
        log_error "The discovery-service self-update path is still present"
        return 1
    fi
    local output exit_code=0
    output=$(bash "$UPGRADE_TEST_BIN" --help 2>&1) || exit_code=$?
    assert_exit_code 0 "$exit_code" || return 1
    echo "$output" | grep -q "Usage: polis-upgrade" || { log_error "--help did not run: $output"; return 1; }
    log "  [OK] No self-update path; script still runs when executed"
    return 0
}

run_test "Upgrade: verified install (happy path)" test_upgrade_happy_path
run_test "Upgrade: checksum mismatch aborts" test_upgrade_checksum_mismatch
run_test "Upgrade: missing checksum aborts" test_upgrade_checksum_missing
run_test "Upgrade: non-https URL refused" test_upgrade_refuses_http
run_test "Upgrade: https pinned across redirects" test_upgrade_https_pinned_across_redirects
run_test "Upgrade: malformed versions rejected" test_upgrade_rejects_malformed_versions
run_test "Upgrade: no discovery-service self-update" test_upgrade_no_self_update_path
