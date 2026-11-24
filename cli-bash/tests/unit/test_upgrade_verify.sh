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
#   - The latest version comes from the latest GitHub release, and a failure
#     to find it (no network, a malformed tag, the tui) says so and fails
#   - The release signature: checksums.txt.sig verifies with the pinned key;
#     a tampered checksums.txt, a missing signature, another key, or a script
#     or manifest that differs from the signed list are each refused; and an
#     unsigned release needs --allow-unsigned
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
# Usage: [UPGRADE_KEY=<pub>] [UPGRADE_ALLOW_UNSIGNED=1] [UPGRADE_SIGNED_TAG=X.Y.Z] _upgrade_call FUNCTION [ARGS...]
#   UPGRADE_KEY pins a test release key; UPGRADE_SIGNED_TAG verifies that release's signature first, as main does.
_upgrade_call() {
    FIXTURE_ROOT="$(pwd)" UPGRADE_BIN="$UPGRADE_TEST_BIN" bash -c '
        source "$UPGRADE_BIN"
        [[ -n "${UPGRADE_KEY:-}" ]] && RELEASE_SIGNING_KEY="$UPGRADE_KEY"
        [[ -n "${UPGRADE_ALLOW_UNSIGNED:-}" ]] && ALLOW_UNSIGNED=true
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
            [[ "$url" == https://api.github.com/* ]] && src="$FIXTURE_ROOT/api/${url#https://api.github.com/}"
            [[ "$url" == https://github.com/vdibart/polis-cli/releases/download/* ]] && src="$FIXTURE_ROOT/releases/${url#https://github.com/vdibart/polis-cli/releases/download/}"
            [[ -f "$src" ]] || return 22
            cp "$src" "$out"
        }
        if [[ -n "${UPGRADE_SIGNED_TAG:-}" ]]; then
            mkdir -p "$FIXTURE_ROOT/work"
            fetch_signed_checksums "$UPGRADE_SIGNED_TAG" "$FIXTURE_ROOT/work" || exit $?
        fi
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
    output=$(UPGRADE_ALLOW_UNSIGNED=1 _upgrade_call install_verified_script "$UPGRADE_TEST_URL" "$(pwd)/installed/polis" polis 2>&1) || exit_code=$?
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
    output=$(UPGRADE_ALLOW_UNSIGNED=1 _upgrade_call install_verified_script "$UPGRADE_TEST_URL" "$(pwd)/installed/polis" polis 2>&1) || exit_code=$?
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
    output=$(UPGRADE_ALLOW_UNSIGNED=1 _upgrade_call install_verified_script "$UPGRADE_TEST_URL" "$(pwd)/installed/polis" polis 2>&1) || exit_code=$?
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
    output=$(UPGRADE_ALLOW_UNSIGNED=1 _upgrade_call install_verified_script "http://raw.githubusercontent.com/vdibart/polis-cli/v9.9.9/cli-bash/polis" "$(pwd)/installed/polis" polis 2>&1) || exit_code=$?
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

    UPGRADE_ALLOW_UNSIGNED=1 _upgrade_call install_verified_script "$UPGRADE_TEST_URL" "$(pwd)/installed/polis" polis >/dev/null 2>&1 || true
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

# Serve api.github.com/repos/vdibart/polis-cli/releases/latest with BODY,
# or nothing (an unreachable API) when BODY is empty.
_upgrade_latest_fixture() {
    local body="$1"
    : > curl.log
    if [[ -n "$body" ]]; then
        mkdir -p api/repos/vdibart/polis-cli/releases
        printf '%s' "$body" > api/repos/vdibart/polis-cli/releases/latest
    fi
}

test_upgrade_latest_from_github_release() {
    setup_test_env "upgrade_latest_release"
    trap teardown_test_env EXIT
    _upgrade_latest_fixture '{"tag_name": "v0.70.0", "draft": false}'

    local output exit_code=0
    output=$(_upgrade_call latest_release_version cli 2>&1) || exit_code=$?
    assert_exit_code 0 "$exit_code" || { echo "$output"; return 1; }
    [[ "$output" == "0.70.0" ]] || { log_error "Expected 0.70.0, got: $output"; return 1; }
    grep -q -- "--proto =https --proto-redir =https .*https://api.github.com/repos/vdibart/polis-cli/releases/latest" curl.log \
        || { log_error "Latest release not fetched over pinned https: $(cat curl.log)"; return 1; }
    if grep -q "polis-version\|DISCOVERY_SERVICE_URL" "$UPGRADE_TEST_BIN"; then
        log_error "polis-upgrade still asks the discovery service"
        return 1
    fi
    log "  [OK] Latest version read from the latest GitHub release"
    return 0
}

test_upgrade_latest_failures_say_so() {
    setup_test_env "upgrade_latest_failures"
    trap teardown_test_env EXIT

    local output exit_code body
    for body in "" '{"tag_name": "v0.70"}' '{"tag_name": "0.70.0"}' '{"message": "Not Found"}' 'not json'; do
        rm -rf api
        _upgrade_latest_fixture "$body"
        exit_code=0
        output=$(_upgrade_call latest_release_version cli 2>&1) || exit_code=$?
        if [[ "$exit_code" -eq 0 ]]; then
            log_error "Succeeded on release body '${body}': $output"
            return 1
        fi
        echo "$output" | grep -q -- "--to VERSION" || { log_error "No --to hint for '${body}': $output"; return 1; }
    done

    rm -rf api
    _upgrade_latest_fixture '{"tag_name": "v0.70.0"}'
    exit_code=0
    output=$(_upgrade_call latest_release_version tui 2>&1) || exit_code=$?
    [[ "$exit_code" -ne 0 ]] || { log_error "Guessed a tui version from a CLI release: $output"; return 1; }
    echo "$output" | grep -q -- "--to VERSION" || { log_error "No --to hint for tui: $output"; return 1; }
    [[ ! -s curl.log ]] || { log_error "Fetched a release for the tui: $(cat curl.log)"; return 1; }
    log "  [OK] Unreachable API, malformed tags and the tui each fail and name --to"
    return 0
}

# A signed v9.9.9 release beside the raw tree: releases/v9.9.9/checksums.txt (listing cli-bash/polis and the cli
# migration manifest) and checksums.txt.sig by a throwaway key made here. Prints the key's public half.
# Usage: _upgrade_signed_release good|tampered|nosig|stale
#   tampered: checksums.txt changed after signing · nosig: no signature published · stale: the served script is
#   not the one listed (signed checksums, swapped file)
_upgrade_signed_release() {
    local mode="$1" rel="releases/v9.9.9" raw="served/vdibart/polis-cli/v9.9.9"
    _upgrade_fixtures good
    mkdir -p "$rel" "$raw/migrations/cli"
    printf '{"component":"cli","migrations":[]}\n' > "$raw/migrations/cli/manifest.json"
    ssh-keygen -q -t ed25519 -N '' -C test -f release-key >/dev/null
    {
        printf '%s  cli-bash/polis\n' "$(sha256sum "$raw/cli-bash/polis" | awk '{print $1}')"
        printf '%s  migrations/cli/manifest.json\n' "$(sha256sum "$raw/migrations/cli/manifest.json" | awk '{print $1}')"
    } > "$rel/checksums.txt"
    ssh-keygen -Y sign -q -f release-key -n polis-release "$rel/checksums.txt" >/dev/null 2>&1
    case "$mode" in
        tampered) sed -i 's/^./0/' "$rel/checksums.txt" ;;
        nosig) rm -f "$rel/checksums.txt.sig" ;;
        stale) printf '#!/usr/bin/env bash\necho swapped\n' > "$raw/cli-bash/polis" ;;
    esac
    : > curl.log
    cat release-key.pub
}

test_upgrade_signed_release_installs() {
    setup_test_env "upgrade_signed_installs"
    trap teardown_test_env EXIT
    command -v ssh-keygen >/dev/null || { mark_skip "ssh-keygen not installed"; return 0; }
    local key output exit_code=0
    key=$(_upgrade_signed_release good)
    output=$(UPGRADE_KEY="$key" UPGRADE_SIGNED_TAG=9.9.9 _upgrade_call install_verified_script "$UPGRADE_TEST_URL" "$(pwd)/installed/polis" polis 2>&1) || exit_code=$?
    assert_exit_code 0 "$exit_code" || { echo "$output"; return 1; }
    cmp -s installed/polis served/vdibart/polis-cli/v9.9.9/cli-bash/polis || { log_error "Signed script not installed"; return 1; }
    grep -q 'checksums.txt.sig' curl.log || { log_error "The signature was not fetched"; return 1; }
    if grep -q 'polis.sha256' curl.log; then log_error "The unsigned .sha256 was consulted for a signed release"; return 1; fi
    exit_code=0
    output=$(UPGRADE_KEY="$key" UPGRADE_SIGNED_TAG=9.9.9 _upgrade_call fetch_manifest cli 9.9.9 2>&1) || exit_code=$?
    assert_exit_code 0 "$exit_code" || { echo "$output"; return 1; }
    log "  [OK] A signed release installs, and its manifest verifies against the signed list"
    return 0
}

test_upgrade_signed_release_refusals() {
    setup_test_env "upgrade_signed_refusals"
    trap teardown_test_env EXIT
    command -v ssh-keygen >/dev/null || { mark_skip "ssh-keygen not installed"; return 0; }
    local key output exit_code

    key=$(_upgrade_signed_release tampered); exit_code=0
    output=$(UPGRADE_KEY="$key" UPGRADE_SIGNED_TAG=9.9.9 _upgrade_call true 2>&1) || exit_code=$?
    [[ "$exit_code" -eq 1 ]] && grep -q "not signed by the polis release key" <<<"$output" \
        || { log_error "A tampered checksums.txt was not refused ($exit_code): $output"; return 1; }

    rm -rf releases served installed work release-key*; key=$(_upgrade_signed_release nosig); exit_code=0
    UPGRADE_KEY="$key" UPGRADE_SIGNED_TAG=9.9.9 _upgrade_call true >/dev/null 2>&1 || exit_code=$?
    [[ "$exit_code" -eq 3 ]] || { log_error "A missing signature was not reported as unsigned (exit $exit_code)"; return 1; }
    exit_code=0
    output=$(UPGRADE_KEY="$key" _upgrade_call install_verified_script "$UPGRADE_TEST_URL" "$(pwd)/installed/polis" polis 2>&1) || exit_code=$?
    [[ "$exit_code" -ne 0 ]] || { log_error "An unsigned release installed without --allow-unsigned"; return 1; }
    _upgrade_installed_unchanged || return 1

    rm -rf releases served installed work release-key*; _upgrade_signed_release good >/dev/null; exit_code=0
    ssh-keygen -q -t ed25519 -N '' -C other -f other-key >/dev/null
    output=$(UPGRADE_KEY="$(cat other-key.pub)" UPGRADE_SIGNED_TAG=9.9.9 _upgrade_call true 2>&1) || exit_code=$?
    [[ "$exit_code" -eq 1 ]] || { log_error "A signature by another key was accepted (exit $exit_code)"; return 1; }

    rm -rf releases served installed work release-key* other-key*; key=$(_upgrade_signed_release stale); exit_code=0
    output=$(UPGRADE_KEY="$key" UPGRADE_SIGNED_TAG=9.9.9 _upgrade_call install_verified_script "$UPGRADE_TEST_URL" "$(pwd)/installed/polis" polis 2>&1) || exit_code=$?
    [[ "$exit_code" -ne 0 ]] && grep -q "not the file the release signed" <<<"$output" \
        || { log_error "A script that differs from the signed list was installed: $output"; return 1; }
    _upgrade_installed_unchanged || return 1
    printf '{"component":"cli","migrations":[{"from":"0.0.1"}]}\n' > served/vdibart/polis-cli/v9.9.9/migrations/cli/manifest.json
    exit_code=0
    UPGRADE_KEY="$key" UPGRADE_SIGNED_TAG=9.9.9 _upgrade_call fetch_manifest cli 9.9.9 >/dev/null 2>&1 || exit_code=$?
    [[ "$exit_code" -ne 0 ]] || { log_error "A manifest that differs from the signed list was accepted"; return 1; }

    exit_code=0
    # A copy built before the key existed carries the placeholder: it must refuse, never verify.
    output=$(UPGRADE_KEY="PLACEHOLDER-NOT-A-KEY no release-signing key has been created yet" UPGRADE_SIGNED_TAG=9.9.9 _upgrade_call true 2>&1) || exit_code=$?
    [[ "$exit_code" -eq 1 ]] && grep -q "no release key pinned" <<<"$output" \
        || { log_error "The placeholder key verified something: $output"; return 1; }
    log "  [OK] Tampered, unsigned, other-key, swapped-file and no-key cases are each refused"
    return 0
}

run_test "Upgrade: verified install (happy path)" test_upgrade_happy_path
run_test "Upgrade: checksum mismatch aborts" test_upgrade_checksum_mismatch
run_test "Upgrade: missing checksum aborts" test_upgrade_checksum_missing
run_test "Upgrade: non-https URL refused" test_upgrade_refuses_http
run_test "Upgrade: https pinned across redirects" test_upgrade_https_pinned_across_redirects
run_test "Upgrade: malformed versions rejected" test_upgrade_rejects_malformed_versions
run_test "Upgrade: no discovery-service self-update" test_upgrade_no_self_update_path
run_test "Upgrade: latest version from the GitHub release" test_upgrade_latest_from_github_release
run_test "Upgrade: no latest version is a clear failure" test_upgrade_latest_failures_say_so
run_test "Upgrade: a signed release installs" test_upgrade_signed_release_installs
run_test "Upgrade: unsigned, tampered and other-key releases are refused" test_upgrade_signed_release_refusals
