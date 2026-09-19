#!/bin/bash
# test_clone_paths.sh - Unit tests for clone's path check
#
# A clone copies a STRANGER's site, and every path it writes comes from that
# site's index.jsonl. Until 2026-09-17 clone_single_file joined the entry's
# path onto the target with no check, so an index listing "../../.bashrc"
# wrote outside the clone folder. Go had the same defect (pkg/clone safeDest).

# _clone_paths_source pulls the helper out of the CLI into this shell.
# Sourcing the whole script would execute the dispatch.
_clone_paths_source() {
    local fns
    fns=$(sed -n '/^clone_path_is_safe()/,/^}/p' "$POLIS_BIN")
    if ! echo "$fns" | grep -q '^clone_path_is_safe()'; then
        return 1
    fi
    eval "$fns"
}

# Test: ordinary content paths are accepted
test_clone_path_accepts_content_paths() {
    setup_test_env "clone_path_accepts_content_paths"
    trap teardown_test_env EXIT

    if ! _clone_paths_source; then
        log_error "clone_path_is_safe not found in $POLIS_BIN"
        return 1
    fi
    mkdir -p clone

    local p
    for p in "content/pub.polis.core/post/20260101/hello.md" \
             "content/pub.polis.core/post/20260101/.versions/hello.md" \
             "a/./b.md"; do
        if ! clone_path_is_safe clone "$p"; then
            log_error "a safe path was refused: $p"
            return 1
        fi
    done
    return 0
}

# Test: traversal and absolute paths are refused
test_clone_path_refuses_traversal_and_absolute() {
    setup_test_env "clone_path_refuses_traversal_and_absolute"
    trap teardown_test_env EXIT

    if ! _clone_paths_source; then
        log_error "clone_path_is_safe not found in $POLIS_BIN"
        return 1
    fi
    mkdir -p clone

    local p
    for p in "../escaped.md" \
             "content/../../escaped.md" \
             "content/pub.polis.core/post/../../../../x" \
             "/etc/passwd" \
             ""; do
        if clone_path_is_safe clone "$p"; then
            log_error "an unsafe path was accepted: '$p'"
            return 1
        fi
    done
    return 0
}

# Test: a symlink inside the target cannot lead the write out of it
test_clone_path_refuses_symlink_escape() {
    setup_test_env "clone_path_refuses_symlink_escape"
    trap teardown_test_env EXIT

    if ! _clone_paths_source; then
        log_error "clone_path_is_safe not found in $POLIS_BIN"
        return 1
    fi
    mkdir -p clone outside
    ln -s "$(pwd)/outside" clone/link
    ln -s "$(pwd)/outside/file.md" clone/filelink.md

    if clone_path_is_safe clone "link/evil.md"; then
        log_error "a write through a directory symlink out of the clone was accepted"
        return 1
    fi
    if clone_path_is_safe clone "link/deeper/evil.md"; then
        log_error "a write below a directory symlink out of the clone was accepted"
        return 1
    fi
    if clone_path_is_safe clone "filelink.md"; then
        log_error "a write through a file symlink was accepted"
        return 1
    fi
    return 0
}

# Test: a sibling directory whose name merely STARTS with the target's is outside
# ⛔ The separator is load-bearing: a containment check written as a string
# prefix ("$real_target"*) accepts clone-evil/ for target clone/. The
# symlink-escape test above cannot catch it — outside/ shares no prefix.
test_clone_path_refuses_sibling_with_target_prefix() {
    setup_test_env "clone_path_refuses_sibling_prefix"
    trap teardown_test_env EXIT

    if ! _clone_paths_source; then
        log_error "clone_path_is_safe not found in $POLIS_BIN"
        return 1
    fi
    mkdir -p clone clone-evil
    ln -s "$(pwd)/clone-evil" clone/link

    if clone_path_is_safe clone "link/evil.md"; then
        log_error "a write into a SIBLING directory whose name starts with the target's was accepted"
        return 1
    fi
    return 0
}

# Run tests
run_test "Clone path check accepts content paths" test_clone_path_accepts_content_paths
run_test "Clone path check refuses traversal and absolute paths" test_clone_path_refuses_traversal_and_absolute
run_test "Clone path check refuses symlink escape" test_clone_path_refuses_symlink_escape
run_test "Clone path check refuses sibling with target prefix" test_clone_path_refuses_sibling_with_target_prefix
