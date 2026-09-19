#!/bin/bash
# test_rotate_key.sh - Unit tests for 'polis rotate-key'
#
# Tests covered:
#   - A rotation appends to the site's own published key history
#   - A second rotation appends again, keeping both retired keys
#   - No .old key file is written by either rotation
#   - --delete-old-key is gone
#   - A DID document stating the old key is removed, and the rotation succeeds
#   - A site with no DID document rotates unchanged
#
# Why the key history matters (SIGNET epic 16): a site that publishes only its
# CURRENT key stops being able to verify its own past the moment it rotates.
# Every post, comment, follow file and attestation signed under the old key
# fails against what the site publishes, and the only surviving record was a
# row in the discovery service. The chain moves that record onto the site.
#
# ⭐ bash is a REFERENCE IMPLEMENTATION, so its coverage defines what the
# protocol IS for anyone building from it. A bash that did not maintain the
# chain would say a key chain is not part of polis.
#
# Why the DID document is DELETED rather than rewritten: rotation is a security
# operation and must never be blocked, and this CLI has never written did.json.
# A stale document is worse than an absent one (it answers 200 with a retired
# key and a resolver cannot tell), and deleting is safe because the document is
# derived: `polis did --write` rebuilds it. See docs/cli/implementation-parity.md.

# Test: a rotation appends the handover to public_key_history
test_rotate_key_appends_to_key_history() {
    setup_test_env "rotate_key_history"
    trap teardown_test_env EXIT

    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    # init publishes the genesis entry: the current key, at epoch 0, with a
    # null transition_sig because nothing preceded it.
    local genesis_key genesis_epoch genesis_sig genesis_from created
    genesis_key=$(jq -r '.public_key_history.current.key' .well-known/polis)
    genesis_epoch=$(jq -r '.public_key_history.current.epoch' .well-known/polis)
    genesis_sig=$(jq -r '.public_key_history.current.transition_sig' .well-known/polis)
    genesis_from=$(jq -r '.public_key_history.current.valid_from' .well-known/polis)
    created=$(jq -r '.created' .well-known/polis)

    if [ "$genesis_epoch" != "0" ]; then
        echo "    Expected genesis at epoch 0, got: $genesis_epoch"
        return 1
    fi
    if [ "$genesis_sig" != "null" ]; then
        echo "    Genesis must carry a null transition_sig, got: $genesis_sig"
        return 1
    fi
    if [ "$genesis_from" != "$created" ]; then
        echo "    Genesis valid_from must be the site's own created ($created), got: $genesis_from"
        return 1
    fi
    if [ "$(jq -r '.public_key_history.history | length' .well-known/polis)" != "0" ]; then
        echo "    A site that has never rotated must publish an empty history[]"
        return 1
    fi

    local result
    result=$(POLIS_BASE_URL="https://example.com" "$POLIS_BIN" --json rotate-key 2>&1)
    assert_exit_code 0 "$?" || return 1
    assert_valid_json "$result" || return 1
    assert_json_success "$result" "rotate-key" || return 1
    assert_json_field "$result" ".data.key_history_epoch" "1" || return 1
    # Not registered, so nothing was sent — and the payload must say so rather
    # than report a notification that never happened.
    assert_json_field "$result" ".data.ds_rotation" "skipped" || return 1

    # The chain head is the key the site now publishes, and the two moved
    # together — a published key its own history never learned about is the
    # exact failure this epic exists to prevent.
    local new_key head_key head_epoch head_sig head_from
    new_key=$(jq -r '.public_key' .well-known/polis)
    head_key=$(jq -r '.public_key_history.current.key' .well-known/polis)
    head_epoch=$(jq -r '.public_key_history.current.epoch' .well-known/polis)
    head_sig=$(jq -r '.public_key_history.current.transition_sig' .well-known/polis)
    head_from=$(jq -r '.public_key_history.current.valid_from' .well-known/polis)

    if [ "$new_key" != "$head_key" ]; then
        echo "    public_key and the chain head disagree after a rotation"
        return 1
    fi
    if [ "$new_key" = "$genesis_key" ]; then
        echo "    The rotation did not change the published key"
        return 1
    fi
    if [ "$head_epoch" != "1" ]; then
        echo "    Expected epoch 1 after one rotation, got: $head_epoch"
        return 1
    fi
    if [ "$head_sig" = "null" ] || [ -z "$head_sig" ]; then
        echo "    A rotation entry must carry the transition signature the old key made"
        return 1
    fi
    case "$head_sig" in
        *"BEGIN SSH SIGNATURE"*) ;;
        *) echo "    transition_sig is not an SSH signature: $head_sig"; return 1 ;;
    esac

    # The retired key lands in history, dated from its own start to the moment
    # the new key took over.
    if [ "$(jq -r '.public_key_history.history | length' .well-known/polis)" != "1" ]; then
        echo "    Expected exactly one retired key in history"
        return 1
    fi
    local retired_key retired_until retired_epoch
    retired_key=$(jq -r '.public_key_history.history[0].key' .well-known/polis)
    retired_until=$(jq -r '.public_key_history.history[0].valid_until' .well-known/polis)
    retired_epoch=$(jq -r '.public_key_history.history[0].epoch' .well-known/polis)

    if [ "$retired_key" != "$genesis_key" ]; then
        echo "    The retired key in history is not the key that was replaced"
        return 1
    fi
    if [ "$retired_epoch" != "0" ]; then
        echo "    The retired key kept the wrong epoch: $retired_epoch"
        return 1
    fi
    # A gap or an overlap here means the chain does not cover every moment, and
    # an artifact signed in the gap resolves to no key at all.
    if [ "$retired_until" != "$head_from" ]; then
        echo "    The retired key ends at $retired_until but the new one begins at $head_from"
        return 1
    fi

    # The rest of the identity document must survive the rewrite: the jq filter
    # EDITS the document, it does not rebuild it. (This CLI rebuilding a file
    # from a fixed template and dropping what it does not model is a known
    # pre-existing hazard elsewhere — see docs/cli/implementation-parity.md.)
    assert_file_contains ".well-known/polis" '"avatar"' || return 1
    assert_file_contains ".well-known/polis" '"bundles"' || return 1
    if [ "$(jq -r '.created' .well-known/polis)" != "$created" ]; then
        echo "    The rotation changed the site's created timestamp"
        return 1
    fi

    return 0
}

# Test: rotating twice keeps BOTH retired keys, oldest first
test_rotate_key_twice_keeps_both_retired_keys() {
    setup_test_env "rotate_key_twice"
    trap teardown_test_env EXIT

    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1
    local k0
    k0=$(jq -r '.public_key' .well-known/polis)

    POLIS_BASE_URL="https://example.com" "$POLIS_BIN" --json rotate-key > /dev/null 2>&1 || return 1
    local k1
    k1=$(jq -r '.public_key' .well-known/polis)

    POLIS_BASE_URL="https://example.com" "$POLIS_BIN" --json rotate-key > /dev/null 2>&1 || return 1
    local k2
    k2=$(jq -r '.public_key' .well-known/polis)

    # ⭐ This is the case the old .old backup could not survive: a single fixed
    # filename meant the second rotation overwrote the first backup and the
    # earliest key was simply gone. The chain keeps every one.
    if [ "$(jq -r '.public_key_history.history | length' .well-known/polis)" != "2" ]; then
        echo "    Expected two retired keys after two rotations"
        return 1
    fi
    if [ "$(jq -r '.public_key_history.history[0].key' .well-known/polis)" != "$k0" ]; then
        echo "    history[0] must be the OLDEST key — the chain is oldest-first"
        return 1
    fi
    if [ "$(jq -r '.public_key_history.history[1].key' .well-known/polis)" != "$k1" ]; then
        echo "    history[1] must be the key that immediately preceded the current one"
        return 1
    fi
    if [ "$(jq -r '.public_key_history.current.key' .well-known/polis)" != "$k2" ]; then
        echo "    The chain head must be the key the site now publishes"
        return 1
    fi
    if [ "$(jq -r '.public_key_history.current.epoch' .well-known/polis)" != "2" ]; then
        echo "    Epoch must advance once per rotation"
        return 1
    fi
    # Contiguous epochs: a gap is how a naively omitted entry shows up.
    if [ "$(jq -r '[.public_key_history.history[].epoch] | @csv' .well-known/polis)" != "0,1" ]; then
        echo "    Retired epochs must run 0,1 with no gap"
        return 1
    fi
    # Each retired key ends exactly where its successor begins.
    if [ "$(jq -r '.public_key_history.history[0].valid_until' .well-known/polis)" != \
         "$(jq -r '.public_key_history.history[1].valid_from' .well-known/polis)" ]; then
        echo "    The chain has a gap between epoch 0 and epoch 1"
        return 1
    fi

    return 0
}

# Test: no .old key file is written, by any rotation
test_rotate_key_writes_no_old_key_file() {
    setup_test_env "rotate_key_no_old"
    trap teardown_test_env EXIT

    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1
    POLIS_BASE_URL="https://example.com" "$POLIS_BIN" --json rotate-key > /dev/null 2>&1 || return 1

    # The published chain replaces the backup. A spare copy of a retired PRIVATE
    # key was never what made old signatures checkable; it was only a liability.
    assert_file_not_exists ".polis/keys/id_ed25519.old" || return 1
    assert_file_not_exists ".polis/keys/id_ed25519.old.pub" || return 1
    assert_file_not_exists ".polis/keys/id_ed25519.pub.old" || return 1

    # A second rotation must not start writing them either.
    POLIS_BASE_URL="https://example.com" "$POLIS_BIN" --json rotate-key > /dev/null 2>&1 || return 1
    assert_file_not_exists ".polis/keys/id_ed25519.old" || return 1
    assert_file_not_exists ".polis/keys/id_ed25519.old.pub" || return 1

    # And the live keys are still the live keys, with the right permissions.
    assert_file_exists ".polis/keys/id_ed25519" || return 1
    assert_file_exists ".polis/keys/id_ed25519.pub" || return 1
    local mode
    mode=$(stat -c '%a' .polis/keys/id_ed25519 2>/dev/null || stat -f '%Lp' .polis/keys/id_ed25519)
    if [ "$mode" != "600" ]; then
        echo "    Private key permissions are $mode, expected 600"
        return 1
    fi

    return 0
}

# Test: --delete-old-key is retired
test_rotate_key_rejects_delete_old_key_flag() {
    setup_test_env "rotate_key_flag_gone"
    trap teardown_test_env EXIT

    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    # The flag existed only to opt out of a backup that is no longer made.
    local result
    result=$(POLIS_BASE_URL="https://example.com" "$POLIS_BIN" --json rotate-key --delete-old-key 2>&1)
    local exit_code=$?

    if [ "$exit_code" -eq 0 ]; then
        echo "    --delete-old-key was accepted; it should be gone"
        return 1
    fi
    assert_json_error_code "$result" "INVALID_INPUT" || return 1

    return 0
}

# Test: rotation removes a DID document that now states the old key
test_rotate_key_removes_stale_did_document() {
    setup_test_env "rotate_key_did"
    trap teardown_test_env EXIT

    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    # Stand in for a document written by the Go CLI or the hosted sweep. Its
    # contents do not matter — what matters is that it names a key that the
    # rotation is about to retire.
    cat > ".well-known/did.json" <<'JSON'
{
  "id": "did:web:example.com",
  "verificationMethod": [{ "id": "did:web:example.com#key-1" }]
}
JSON
    assert_file_exists ".well-known/did.json" || return 1

    local result
    result=$(POLIS_BASE_URL="https://example.com" "$POLIS_BIN" --json rotate-key 2>&1)
    local exit_code=$?

    # The rotation must SUCCEED. Refusing would block a security operation.
    assert_exit_code 0 "$exit_code" || return 1
    assert_valid_json "$result" || return 1
    assert_json_success "$result" "rotate-key" || return 1
    assert_json_field "$result" ".data.did_document" "deleted" || return 1

    # And the false document must be gone.
    assert_file_not_exists ".well-known/did.json" || return 1

    # The rotation itself still happened, and it is recorded where it lasts.
    assert_file_exists ".polis/keys/id_ed25519" || return 1
    assert_json_field "$result" ".data.key_history_epoch" "1" || return 1

    return 0
}

# Test: a site that never had a DID document rotates unchanged
test_rotate_key_without_did_document() {
    setup_test_env "rotate_key_no_did"
    trap teardown_test_env EXIT

    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1
    assert_file_not_exists ".well-known/did.json" || return 1

    local result
    result=$(POLIS_BASE_URL="https://example.com" "$POLIS_BIN" --json rotate-key 2>&1)
    local exit_code=$?

    assert_exit_code 0 "$exit_code" || return 1
    assert_valid_json "$result" || return 1
    assert_json_field "$result" ".data.did_document" "absent" || return 1
    assert_file_not_exists ".well-known/did.json" || return 1

    return 0
}

# Run tests
run_test "Rotate Key Appends To Key History" test_rotate_key_appends_to_key_history
run_test "Rotate Key Twice Keeps Both Retired Keys" test_rotate_key_twice_keeps_both_retired_keys
run_test "Rotate Key Writes No Old Key File" test_rotate_key_writes_no_old_key_file
run_test "Rotate Key Rejects Delete Old Key Flag" test_rotate_key_rejects_delete_old_key_flag
run_test "Rotate Key Removes Stale DID Document" test_rotate_key_removes_stale_did_document
run_test "Rotate Key Without DID Document" test_rotate_key_without_did_document
