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

# Test: a rotation drops the signature from the follow file and blessing list
#
# Both verify against the current key only, so a signature made with the old
# key reads `invalid` after the rotation. Go re-signs them; bash cannot sign
# either file, so it drops the signature (the file then reads `unsigned`, which
# reports OK) and leaves every entry as it was.
test_rotate_key_drops_list_signatures() {
    setup_test_env "rotate_key_lists"
    trap teardown_test_env EXIT

    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1

    mkdir -p content/pub.polis.core/follow content/pub.polis.core/comment
    echo '{"version":"polis-cli-go/test","following":[{"url":"https://bob.polis.pub","added_at":"2026-09-01T00:00:00Z"}],"signature":"-----BEGIN SSH SIGNATURE-----\nold\n-----END SSH SIGNATURE-----"}' \
        > content/pub.polis.core/follow/following.json
    echo '{"version":"polis-cli-go/test","comments":[{"post":"posts/20260901/hello.md","blessed":[{"url":"https://bob.polis.pub/c/1.md","version":"sha256:aaa","blessed_at":"2026-09-01T00:00:00Z","agent":"rosie","grant":"https://example.com/g.json"}]}],"signature":"-----BEGIN SSH SIGNATURE-----\nold\n-----END SSH SIGNATURE-----"}' \
        > content/pub.polis.core/comment/blessed.json

    local result
    result=$(POLIS_BASE_URL="https://example.com" "$POLIS_BIN" --json rotate-key 2>&1)
    assert_exit_code 0 "$?" || return 1
    assert_valid_json "$result" || return 1
    assert_json_field "$result" ".data.following_resign" "signature-removed" || return 1
    assert_json_field "$result" ".data.blessed_resign" "signature-removed" || return 1

    if [ "$(jq -r 'has("signature")' content/pub.polis.core/follow/following.json)" != "false" ]; then
        echo "    The follow file still carries a signature made with the retired key"
        return 1
    fi
    if [ "$(jq -r 'has("signature")' content/pub.polis.core/comment/blessed.json)" != "false" ]; then
        echo "    The blessing list still carries a signature made with the retired key"
        return 1
    fi
    # Entries, and the agent marker, are untouched.
    if [ "$(jq -r '.following[0].url' content/pub.polis.core/follow/following.json)" != "https://bob.polis.pub" ]; then
        echo "    The follow file's entries changed"
        return 1
    fi
    if [ "$(jq -r '.comments[0].blessed[0].agent' content/pub.polis.core/comment/blessed.json)" != "rosie" ]; then
        echo "    The blessing list lost its agent marker"
        return 1
    fi
    # The follow file names bash as its last writer, as every bash write does.
    if [ "$(jq -r '.version' content/pub.polis.core/follow/following.json)" != "polis-cli/$(sed -n 's/^VERSION="\(.*\)"$/\1/p' "$POLIS_BIN" | head -1)" ]; then
        echo "    The follow file's generator was not restamped"
        return 1
    fi

    return 0
}

# Test: a rotation with no lists creates none
test_rotate_key_creates_no_lists() {
    setup_test_env "rotate_key_no_lists"
    trap teardown_test_env EXIT

    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1
    rm -f content/pub.polis.core/follow/following.json content/pub.polis.core/comment/blessed.json

    local result
    result=$(POLIS_BASE_URL="https://example.com" "$POLIS_BIN" --json rotate-key 2>&1)
    assert_exit_code 0 "$?" || return 1
    assert_json_field "$result" ".data.following_resign" "absent" || return 1
    assert_json_field "$result" ".data.blessed_resign" "absent" || return 1
    assert_file_not_exists "content/pub.polis.core/follow/following.json" || return 1
    assert_file_not_exists "content/pub.polis.core/comment/blessed.json" || return 1

    return 0
}

# Test: a rotation keeps block-level public_key_history members it does not model
#
# A chain is append-only and never rebuilt. The rotation used to rebuild the
# block as a literal {current, history}, silently dropping anything else a
# newer writer had put in it.
test_rotate_key_keeps_unmodelled_chain_members() {
    setup_test_env "rotate_key_chain_extra"
    trap teardown_test_env EXIT

    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1
    jq '.public_key_history.from_the_future = {"kept": true}' .well-known/polis > wk.tmp && mv wk.tmp .well-known/polis

    local result
    result=$(POLIS_BASE_URL="https://example.com" "$POLIS_BIN" --json rotate-key 2>&1)
    assert_exit_code 0 "$?" || return 1
    assert_json_field "$result" ".data.key_history_epoch" "1" || return 1

    if [ "$(jq -c '.public_key_history.from_the_future' .well-known/polis)" != '{"kept":true}' ]; then
        echo "    An unmodelled block-level member was dropped by the rotation"
        return 1
    fi
    if [ "$(jq -r '.public_key_history.history | length' .well-known/polis)" != "1" ] || \
       [ "$(jq -r '.public_key_history.current.key' .well-known/polis)" != "$(jq -r '.public_key' .well-known/polis)" ]; then
        echo "    The chain did not append as it should"
        return 1
    fi
    # Key order within the block is what it always was: current, history, then
    # the member this CLI does not model.
    if [ "$(jq -c '.public_key_history | keys_unsorted' .well-known/polis)" != '["current","history","from_the_future"]' ]; then
        echo "    Block key order changed: $(jq -c '.public_key_history | keys_unsorted' .well-known/polis)"
        return 1
    fi
    return 0
}

# Run tests
run_test "Rotate Key Keeps Unmodelled Chain Members" test_rotate_key_keeps_unmodelled_chain_members
run_test "Rotate Key Drops List Signatures" test_rotate_key_drops_list_signatures
run_test "Rotate Key Creates No Lists" test_rotate_key_creates_no_lists
run_test "Rotate Key Appends To Key History" test_rotate_key_appends_to_key_history
run_test "Rotate Key Twice Keeps Both Retired Keys" test_rotate_key_twice_keeps_both_retired_keys
run_test "Rotate Key Writes No Old Key File" test_rotate_key_writes_no_old_key_file
run_test "Rotate Key Rejects Delete Old Key Flag" test_rotate_key_rejects_delete_old_key_flag
run_test "Rotate Key Removes Stale DID Document" test_rotate_key_removes_stale_did_document
run_test "Rotate Key Without DID Document" test_rotate_key_without_did_document

# Test: a staged key from an interrupted rotation is resumed, not refused, and
# never replaced by a freshly generated one
test_rotate_key_resumes_a_staged_key() {
    setup_test_env "rotate_key_resume"
    trap teardown_test_env EXIT

    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1
    # The Go paths stage only the private key, so leave no .new.pub.
    ssh-keygen -q -t ed25519 -f .polis/keys/id_ed25519.new -N "" -C staged > /dev/null 2>&1 || return 1
    rm -f .polis/keys/id_ed25519.new.pub
    local staged
    staged=$(ssh-keygen -y -f .polis/keys/id_ed25519.new | awk '{print $1" "$2}')

    POLIS_BASE_URL="https://example.com" "$POLIS_BIN" --json rotate-key > /dev/null 2>&1 || {
        echo "    rotation with a staged key failed"
        return 1
    }
    if [ "$(awk '{print $1" "$2}' .polis/keys/id_ed25519.pub)" != "$staged" ]; then
        echo "    the staged key was not the one installed"
        return 1
    fi
    if [ "$(jq -r '.public_key_history.current.key' .well-known/polis | awk '{print $1" "$2}')" != "$staged" ]; then
        echo "    the key history did not learn the staged key"
        return 1
    fi
    assert_file_not_exists ".polis/keys/id_ed25519.new" || return 1
    return 0
}

# Test: ⛔ a discovery service that never answers is NOT a refusal — the staged
# key may be the only copy of a key it took, so it is kept and nothing changes
test_rotate_key_keeps_the_staged_key_when_the_ds_does_not_answer() {
    setup_test_env "rotate_key_no_answer"
    trap teardown_test_env EXIT

    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1
    # Registered with a discovery service at an address nothing listens on.
    mkdir -p ".polis/ds/127.0.0.1:9" && echo "{}" > ".polis/ds/127.0.0.1:9/registration.json"
    local before result
    before=$(cat .polis/keys/id_ed25519)
    result=$(POLIS_BASE_URL="https://example.com" DISCOVERY_SERVICE_URL="http://127.0.0.1:9" \
        "$POLIS_BIN" --json rotate-key 2>&1)
    if [ $? -eq 0 ]; then
        echo "    rotation succeeded without a discovery service answer"
        return 1
    fi
    assert_json_error_code "$result" "ROTATION_PENDING" || return 1
    assert_file_exists ".polis/keys/id_ed25519.new" || return 1
    if [ "$(cat .polis/keys/id_ed25519)" != "$before" ]; then
        echo "    the current key changed"
        return 1
    fi
    return 0
}

run_test "Rotate Key Resumes A Staged Key" test_rotate_key_resumes_a_staged_key
run_test "Rotate Key Keeps The Staged Key When The DS Does Not Answer" test_rotate_key_keeps_the_staged_key_when_the_ds_does_not_answer

# _crash_after_install leaves the site exactly as a rotation that died between
# installing the new key and writing the key history: the new key installed,
# the staged copy and the retired key (.prev) still on disk, and
# .well-known/polis still publishing the old key.
_crash_after_install() {
    ssh-keygen -q -t ed25519 -f .polis/keys/id_ed25519.new -N "" -C staged > /dev/null 2>&1 || return 1
    cp .polis/keys/id_ed25519 .polis/keys/id_ed25519.prev
    cp .polis/keys/id_ed25519.new .polis/keys/id_ed25519
    cp .polis/keys/id_ed25519.new.pub .polis/keys/id_ed25519.pub
}

# _transition_verifies checks the chain head's transition_sig against the key
# it succeeded, over the canonical rotation message, as a verifier walking the
# chain does.
_transition_verifies() {
    local domain="$1" old_key new_key ts sig
    old_key=$(jq -r '.public_key_history.history[-1].key' .well-known/polis)
    new_key=$(jq -r '.public_key_history.current.key' .well-known/polis)
    ts=$(jq -r '.public_key_history.current.valid_from' .well-known/polis)
    sig=$(jq -r '.public_key_history.current.transition_sig' .well-known/polis)
    printf '%s' "$sig" > transition.sig
    echo "polis $old_key" > allowed_signers
    printf '{"action":"key-rotation","domain":"%s","new_key":"%s","old_key":"%s","timestamp":"%s"}' \
        "$domain" "$new_key" "$old_key" "$ts" |
        ssh-keygen -Y verify -f allowed_signers -I polis -n file -s transition.sig > /dev/null 2>&1
}

# Test: ⛔⛔ a crash after the install and before the key history is written is
# FINISHED by the next rotation, signed by the retired key, and the chain
# verifies; then a further rotation extends it
test_rotate_key_finishes_a_rotation_crashed_before_the_history_write() {
    setup_test_env "rotate_key_crash"
    trap teardown_test_env EXIT

    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1
    local genesis staged
    genesis=$(jq -r '.public_key' .well-known/polis)
    _crash_after_install || return 1
    staged=$(awk '{print $1" "$2}' .polis/keys/id_ed25519.new.pub)

    local result
    result=$(POLIS_BASE_URL="https://example.com" "$POLIS_BIN" --json rotate-key 2>&1)
    assert_exit_code 0 "$?" || { echo "$result"; return 1; }
    if [ "$(jq -r '.public_key_history.current.key' .well-known/polis | awk '{print $1" "$2}')" != "$staged" ]; then
        echo "    the chain's head is not the installed key"
        return 1
    fi
    if [ "$(jq -r '.public_key_history.history[-1].key' .well-known/polis)" != "$genesis" ]; then
        echo "    the retired key in history is not the key that was published"
        return 1
    fi
    _transition_verifies "example.com" || {
        echo "    ⛔ the handover does not verify against the key it retired"
        return 1
    }
    assert_file_not_exists ".polis/keys/id_ed25519.prev" || return 1
    assert_file_not_exists ".polis/keys/id_ed25519.new" || return 1

    result=$(POLIS_BASE_URL="https://example.com" "$POLIS_BIN" --json rotate-key 2>&1)
    assert_exit_code 0 "$?" || { echo "$result"; return 1; }
    _transition_verifies "example.com" || {
        echo "    ⛔ the next rotation after a resumed one does not verify"
        return 1
    }
    return 0
}

# Test: ⛔⛔ a crash INSIDE the install — the new private key renamed into place,
# id_ed25519.pub not yet — is finished by the next rotation, signed by the
# retired key, without overwriting it, and the chain verifies
test_rotate_key_finishes_a_rotation_crashed_between_the_key_writes() {
    setup_test_env "rotate_key_crash_mid_install"
    trap teardown_test_env EXIT

    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1
    local genesis staged
    genesis=$(jq -r '.public_key' .well-known/polis)
    ssh-keygen -q -t ed25519 -f .polis/keys/id_ed25519.new -N "" -C staged > /dev/null 2>&1 || return 1
    cp .polis/keys/id_ed25519 .polis/keys/id_ed25519.prev
    cp .polis/keys/id_ed25519.new .polis/keys/id_ed25519
    # ✂ crash: id_ed25519.pub still holds the genesis key.
    staged=$(awk '{print $1" "$2}' .polis/keys/id_ed25519.new.pub)

    local result
    result=$(POLIS_BASE_URL="https://example.com" "$POLIS_BIN" --json rotate-key 2>&1)
    assert_exit_code 0 "$?" || { echo "$result"; return 1; }
    if [ "$(jq -r '.public_key_history.history[-1].key' .well-known/polis)" != "$genesis" ]; then
        echo "    the retired key in history is not the key that was published"
        return 1
    fi
    if [ "$(awk '{print $1" "$2}' .polis/keys/id_ed25519.pub)" != "$staged" ]; then
        echo "    the resume did not finish writing id_ed25519.pub"
        return 1
    fi
    _transition_verifies "example.com" || {
        echo "    ⛔ the handover was not signed by the key it retired"
        return 1
    }
    return 0
}

# Test: ⛔ a key on disk that is not the published one — what an older build
# left after that crash — is refused, naming the state, and nothing changes
test_rotate_key_refuses_a_key_that_is_not_published() {
    setup_test_env "rotate_key_not_published"
    trap teardown_test_env EXIT

    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1
    rm -f .polis/keys/id_ed25519 .polis/keys/id_ed25519.pub
    ssh-keygen -q -t ed25519 -f .polis/keys/id_ed25519 -N "" -C other > /dev/null 2>&1 || return 1
    local before result
    before=$(cat .well-known/polis)
    result=$(POLIS_BASE_URL="https://example.com" "$POLIS_BIN" --json rotate-key 2>&1)
    if [ $? -eq 0 ]; then
        echo "    rotation succeeded from a key the site does not publish"
        return 1
    fi
    assert_json_error_code "$result" "KEY_NOT_PUBLISHED" || return 1
    if [ "$(cat .well-known/polis)" != "$before" ]; then
        echo "    ⛔ a refused rotation changed .well-known/polis"
        return 1
    fi
    assert_file_not_exists ".polis/keys/id_ed25519.new" || return 1
    return 0
}

# Test: ⛔ a 409 on a RESUMED re-send keeps the staged key and finishes when the
# DS turns out to hold it — the earlier request landed after the history check.
# A curl shim plays the DS: history says the old key, the re-send answers 409
# and the earlier request lands, history then says the staged key.
test_rotate_key_resumed_409_keeps_the_staged_key() {
    setup_test_env "rotate_key_resumed_409"
    trap teardown_test_env EXIT

    "$POLIS_BIN" --json init > /dev/null 2>&1 || return 1
    mkdir -p ".polis/ds/ds.test" && echo "{}" > ".polis/ds/ds.test/registration.json"
    ssh-keygen -q -t ed25519 -f .polis/keys/id_ed25519.new -N "" -C staged > /dev/null 2>&1 || return 1
    local old staged
    old=$(cat .polis/keys/id_ed25519.pub | tr -d '\n')
    staged=$(cat .polis/keys/id_ed25519.new.pub | tr -d '\n')

    mkdir -p shim
    echo "$old" > shim/ds_current
    cat > shim/curl <<SHIM
#!/bin/bash
state="$(pwd)/shim/ds_current"
case "\$*" in
    *"-X POST"*) echo "$staged" > "\$state"; printf '{"error":"old_key does not match current active key"}\n409' ;;
    *keys/history*) printf '{"keys":[{"public_key":"%s","valid_until":null}]}\n200' "\$(cat "\$state")" ;;
    *) printf '{}\n200' ;;
esac
SHIM
    chmod +x shim/curl

    local result
    result=$(PATH="$(pwd)/shim:$PATH" POLIS_BASE_URL="https://example.com" DISCOVERY_SERVICE_URL="https://ds.test" \
        "$POLIS_BIN" --json rotate-key 2>&1)
    assert_exit_code 0 "$?" || { echo "$result"; return 1; }
    if [ "$(jq -r '.public_key_history.current.key' .well-known/polis | awk '{print $1" "$2}')" != "$(_ssh_key_material_t "$staged")" ]; then
        echo "    the rotation did not finish with the staged key the DS holds"
        return 1
    fi

    # And when the DS still holds the old key after the refusal, the staged key
    # is kept and the rotation reported pending.
    ssh-keygen -q -t ed25519 -f .polis/keys/id_ed25519.new -N "" -C staged2 > /dev/null 2>&1 || return 1
    cat > shim/curl <<'SHIM'
#!/bin/bash
case "$*" in
    *"-X POST"*) printf '{"error":"refused"}\n409' ;;
    *keys/history*) printf '{"keys":[{"public_key":"%s","valid_until":null}]}\n200' "$(cat .polis/keys/id_ed25519.pub)" ;;
    *) printf '{}\n200' ;;
esac
SHIM
    result=$(PATH="$(pwd)/shim:$PATH" POLIS_BASE_URL="https://example.com" DISCOVERY_SERVICE_URL="https://ds.test" \
        "$POLIS_BIN" --json rotate-key 2>&1)
    if [ $? -eq 0 ]; then
        echo "    a refused re-send with the DS on the old key reported success"
        return 1
    fi
    assert_json_error_code "$result" "ROTATION_PENDING" || return 1
    assert_file_exists ".polis/keys/id_ed25519.new" || return 1
    return 0
}

_ssh_key_material_t() {
    echo "$1" | awk '{print $1" "$2}'
}

run_test "Rotate Key Finishes A Rotation Crashed Before The History Write" test_rotate_key_finishes_a_rotation_crashed_before_the_history_write
run_test "Rotate Key Finishes A Rotation Crashed Between The Key Writes" test_rotate_key_finishes_a_rotation_crashed_between_the_key_writes
run_test "Rotate Key Refuses A Key That Is Not Published" test_rotate_key_refuses_a_key_that_is_not_published
run_test "Rotate Key Resumed 409 Keeps The Staged Key" test_rotate_key_resumed_409_keeps_the_staged_key
