#!/bin/bash
# test_signing_base.sh - Unit tests for the markdown signing base
#
# Spec: docs/signet/spec/signing-base.md §4.
#
# ⛔ THE REGRESSION THESE GUARD. Until 2026-09-04 the bash verifier rebuilt the
# signed bytes with `sed '/^signature:/d'` — a prefix scan over the WHOLE
# DOCUMENT. A body line legitimately beginning `signature:` (a post documenting
# this format, a quoted frontmatter block, a YAML fence) was dropped, the
# reconstruction no longer matched what was signed, and AUTHENTIC CONTENT was
# reported to the user as possible tampering. The Go CLI had the same defect.
#
# These tests call the strip and the extractor directly rather than going
# through `polis verify`, because the bug is in byte reconstruction and a
# round-trip test would only tell us that something failed, not what.

# _signing_base_source pulls the helpers out of the CLI into this shell.
#
# ⚠️ Sourcing the whole script is not an option — it would execute the dispatch.
# So the two functions are extracted by name; if either is renamed these tests
# fail loudly rather than silently passing on nothing.
_signing_base_source() {
    local fns
    fns=$(sed -n '/^extract_frontmatter_block()/,/^}/p;/^strip_frontmatter_key()/,/^}/p' "$POLIS_BIN")
    if ! echo "$fns" | grep -q '^strip_frontmatter_key()'; then
        return 1
    fi
    eval "$fns"
}

# The fixture carries every case at once: a real frontmatter signature, a body
# line at column 0, and an INDENTED one. The two body forms failed differently
# under the old rule — the unindented one was dropped and the indented one
# survived — so a test using only one of them would have passed against the bug.
_signing_base_fixture() {
    printf -- '---\ntitle: On signing\nsignature: THEREALFRONTMATTERSIG\n---\n\nA polis post carries its signature in frontmatter:\n\nsignature: <base64 SSHSIG>\n  signature: this one is indented\n\nThat is the whole trick.\n'
}

# Test: a body line beginning `signature:` stays in the signing base
test_signing_base_keeps_body_lines() {
    if ! _signing_base_source; then
        mark_skip "extract_frontmatter_block/strip_frontmatter_key not found in $POLIS_BIN"
        return 0
    fi

    local base
    base=$(strip_frontmatter_key "$(_signing_base_fixture)" 'signature')

    if echo "$base" | grep -q 'THEREALFRONTMATTERSIG'; then
        log_error "the frontmatter signature: line must be excluded from the signing base"
        return 1
    fi
    if ! echo "$base" | grep -q '^signature: <base64 SSHSIG>$'; then
        log_error "an unindented BODY line beginning 'signature:' was dropped — the prefix scan is back"
        return 1
    fi
    if ! echo "$base" | grep -q '^  signature: this one is indented$'; then
        log_error "an indented body line beginning 'signature:' was dropped"
        return 1
    fi
    if ! echo "$base" | grep -q 'That is the whole trick.'; then
        log_error "the body was truncated"
        return 1
    fi

    log "  [OK] the strip is anchored to the frontmatter block"
    return 0
}

# Test: the signature is read from the frontmatter, never from the body
test_signing_base_extracts_frontmatter_signature() {
    if ! _signing_base_source; then
        mark_skip "extract_frontmatter_block/strip_frontmatter_key not found in $POLIS_BIN"
        return 0
    fi

    local sig
    sig=$(extract_frontmatter_block "$(_signing_base_fixture)" | grep '^signature:' | head -1 | sed 's/^signature: *//')

    if [[ "$sig" != "THEREALFRONTMATTERSIG" ]]; then
        log_error "expected the frontmatter signature, got: '$sig'"
        return 1
    fi

    # A document with no frontmatter has no signature to find, even when a body
    # line looks like one.
    local none
    none=$(extract_frontmatter_block "$(printf 'no frontmatter here\nsignature: not-a-signature\n')" | grep '^signature:' | head -1)
    if [[ -n "$none" ]]; then
        log_error "a body line was read as a signature in a document with no frontmatter: '$none'"
        return 1
    fi

    log "  [OK] the signature is read from the frontmatter block only"
    return 0
}

# Run tests
run_test "Signing Base Keeps Body Lines" test_signing_base_keeps_body_lines
run_test "Signing Base Extracts Frontmatter Signature" test_signing_base_extracts_frontmatter_signature
