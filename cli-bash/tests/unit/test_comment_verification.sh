#!/bin/bash
# test_comment_verification.sh - bash verifies what the Go CLI signed
#
# Spec: docs/signet/spec/signing-base.md §4.
#
# ⛔ THE REGRESSION THESE GUARD. verify_remote_signature stripped `signature:`
# and never `author:`. A comment's `author:` line is written AFTER signing, so
# for every authentic comment bash hashed a line the signer never signed, and
# `polis preview` reported "SIGNATURE DOES NOT MATCH — content may have been
# tampered with". The report ACCUSED an honest author.
#
# ⚠️ The fixtures CROSS THE IMPLEMENTATION BOUNDARY: tests/fixtures/go-signed/
# is real output of the Go CLI's own signing paths (comment.SignComment and
# publish.PublishPost, with reserved terms stated so the nested licence block
# is present), generated 2026-09-17. A bash fixture verifying a bash fixture
# would prove nothing — the defect is that the two disagreed.

_comment_verification_source() {
    local fns
    fns=$(sed -n '/^extract_frontmatter_block()/,/^}/p;/^strip_frontmatter_key()/,/^}/p;/^canonicalize_content()/,/^}/p;/^markdown_object_type()/,/^}/p;/^verify_remote_signature()/,/^}/p' "$POLIS_BIN")
    local fn
    for fn in extract_frontmatter_block strip_frontmatter_key canonicalize_content markdown_object_type verify_remote_signature; do
        if ! echo "$fns" | grep -q "^${fn}()"; then
            log_error "$fn not found in $POLIS_BIN"
            return 1
        fi
    done
    eval "$fns"
}

_go_fixture() {
    cat "$SCRIPT_DIR/fixtures/go-signed/$1"
}

_go_pubkey() {
    awk '{print $1" "$2}' "$SCRIPT_DIR/fixtures/go-signed/go_signed.pub"
}

# Test: a comment the Go CLI signed verifies
test_bash_verifies_go_signed_comment() {
    _comment_verification_source || return 1
    if ! command -v ssh-keygen > /dev/null 2>&1; then
        mark_skip "ssh-keygen not available"
        return 0
    fi

    if ! verify_remote_signature "$(_go_fixture go_signed_comment.md)" "$(_go_pubkey)" "maya@maya.example"; then
        log_error "an authentic Go-signed comment failed verification — author: is inside the reconstructed base again"
        return 1
    fi
    return 0
}

# Test: a post the Go CLI signed still verifies (the post path is unchanged)
test_bash_verifies_go_signed_post() {
    _comment_verification_source || return 1
    if ! command -v ssh-keygen > /dev/null 2>&1; then
        mark_skip "ssh-keygen not available"
        return 0
    fi

    if ! verify_remote_signature "$(_go_fixture go_signed_post.md)" "$(_go_pubkey)" "maya@maya.example"; then
        log_error "an authentic Go-signed post failed verification"
        return 1
    fi
    return 0
}

# Test: a tampered comment still fails — the fix must not make the verifier permissive
test_bash_rejects_tampered_comment() {
    _comment_verification_source || return 1
    if ! command -v ssh-keygen > /dev/null 2>&1; then
        mark_skip "ssh-keygen not available"
        return 0
    fi

    local original tampered_body tampered_fm
    original=$(_go_fixture go_signed_comment.md)
    tampered_body=$(printf '%s\n' "$original" | sed 's/I agree with this\./I disagree with this./')
    tampered_fm=$(printf '%s\n' "$original" | sed 's|^  url: https://bob.example/posts/20260101/hello.md|  url: https://bob.example/posts/20260101/other.md|')

    if verify_remote_signature "$tampered_body" "$(_go_pubkey)" "maya@maya.example"; then
        log_error "a comment with an edited BODY verified"
        return 1
    fi
    if verify_remote_signature "$tampered_fm" "$(_go_pubkey)" "maya@maya.example"; then
        log_error "a comment with an edited in-reply-to verified"
        return 1
    fi
    return 0
}

# Test: a POST is not granted the comment strip — its author: is signed
test_bash_post_author_line_is_signed() {
    _comment_verification_source || return 1
    if ! command -v ssh-keygen > /dev/null 2>&1; then
        mark_skip "ssh-keygen not available"
        return 0
    fi

    # Insert an author: line into the post. If bash applied the comment rule
    # to posts, the insertion would be stripped and the post would still verify.
    local tampered
    tampered=$(printf '%s\n' "$(_go_fixture go_signed_post.md)" | sed '2a author: mallory@evil.example')
    if verify_remote_signature "$tampered" "$(_go_pubkey)" "maya@maya.example"; then
        log_error "a post with an inserted author: line verified — the comment strip was applied to a post"
        return 1
    fi
    return 0
}

# Test: type detection matches Go's MarkdownObjectTypeFor — type: comment OR in-reply-to, frontmatter only
test_markdown_object_type_matches_go() {
    _comment_verification_source || return 1

    local got
    got=$(markdown_object_type "$(printf -- '---\ntitle: x\ntype: comment\n---\n\nbody\n')")
    [ "$got" = "comment" ] || { log_error "type: comment → '$got', want comment"; return 1; }

    got=$(markdown_object_type "$(printf -- '---\ntitle: x\nin-reply-to:\n  url: https://a.example/p.md\n---\n\nbody\n')")
    [ "$got" = "comment" ] || { log_error "in-reply-to without type → '$got', want comment"; return 1; }

    got=$(markdown_object_type "$(printf -- '---\ntitle: x\n---\n\ntype: comment\nin-reply-to:\n')")
    [ "$got" = "post" ] || { log_error "body lines were read as frontmatter → '$got', want post"; return 1; }

    got=$(markdown_object_type "$(printf -- '---\ntitle: x\nlicense:\n  type: comment\n---\n\nbody\n')")
    [ "$got" = "post" ] || { log_error "an indented child type: was read as top-level → '$got', want post"; return 1; }

    got=$(markdown_object_type "$(printf -- '---\ntitle: x\ntype: post\n---\n\nbody\n')")
    [ "$got" = "post" ] || { log_error "type: post → '$got', want post"; return 1; }
    return 0
}

# Run tests
run_test "Bash Verifies Go Signed Comment" test_bash_verifies_go_signed_comment
run_test "Bash Verifies Go Signed Post" test_bash_verifies_go_signed_post
run_test "Bash Rejects Tampered Comment" test_bash_rejects_tampered_comment
run_test "Bash Post Author Line Is Signed" test_bash_post_author_line_is_signed
run_test "Markdown Object Type Matches Go" test_markdown_object_type_matches_go
