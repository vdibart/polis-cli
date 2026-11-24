#!/bin/bash
# Polis CLI Installer
# Usage: curl -fsSL https://raw.githubusercontent.com/vdibart/polis-cli/main/scripts/install.sh | bash
#
# It verifies what it installs: the release's checksums.txt must carry a valid
# signature (checksums.txt.sig) by the polis release key pinned below, and the
# archive must match its line in that signed list. On any failure nothing is
# installed. Needs ssh-keygen (OpenSSH 8.0+) for the signature.
#
# A release made before releases were signed has no checksums.txt.sig and is
# refused, unless POLIS_ALLOW_UNSIGNED=1 is set (or --allow-unsigned is given,
# with `| bash -s -- --allow-unsigned`): then the archive is checked only
# against the checksums.txt beside it, which shows it arrived intact, not that
# the release key signed it.

set -e

REPO="vdibart/polis-cli"
INSTALL_DIR="${POLIS_INSTALL_DIR:-$HOME/.local/bin}"
ALLOW_UNSIGNED="${POLIS_ALLOW_UNSIGNED:-}"

# ⛔ The polis release key, PINNED: a key fetched from where the files come from
# would prove nothing. Generated from release/release-signing-key.pub by the
# release tooling; the release checks fail if they differ.
RELEASE_SIGNING_KEY="ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAING2XN2oftAR/Z2d29gQm1vSaTsRyvlTeWt0WxNugooY polis-release"
RELEASE_NAMESPACE="polis-release"
MANUAL_STEPS="https://github.com/${REPO}/blob/main/SECURITY.md#security-best-practices-for-users"

detect_platform() {
    local os arch
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    arch="$(uname -m)"

    case "$os" in
        linux) os="linux" ;;
        darwin) os="darwin" ;;
        mingw*|msys*|cygwin*) os="windows" ;;
        *) echo "Unsupported OS: $os" >&2; exit 1 ;;
    esac

    case "$arch" in
        x86_64|amd64) arch="amd64" ;;
        arm64|aarch64) arch="arm64" ;;
        *) echo "Unsupported architecture: $arch" >&2; exit 1 ;;
    esac

    echo "${os}-${arch}"
}

get_latest_version() {
    curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | \
        grep '"tag_name"' | sed -E 's/.*"v?([^"]+)".*/\1/'
}

sha256_of() {
    local file="$1"
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$file" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$file" | awk '{print $1}'
    else
        echo "Error: neither sha256sum nor shasum is available; cannot verify download" >&2
        exit 1
    fi
}

# refuse MESSAGE...: say why, say nothing was installed, and stop.
refuse() {
    local line
    for line in "$@"; do echo "Error: ${line}" >&2; done
    echo "Nothing was installed." >&2
    exit 1
}

# verify_signature DIR VERSION: checksums.txt in DIR must be signed by the
# pinned release key (checksums.txt.sig beside it).
verify_signature() {
    local dir="$1" version="$2" typ b64 rest
    read -r typ b64 rest <<<"$RELEASE_SIGNING_KEY"
    if ! command -v ssh-keygen >/dev/null 2>&1; then
        refuse "ssh-keygen (OpenSSH 8.0+) is needed to verify the release signature, and it is not installed." \
            "Install OpenSSH and run this again, or verify the download by hand: ${MANUAL_STEPS}"
    fi
    printf '%s namespaces="%s" %s %s\n' "$RELEASE_NAMESPACE" "$RELEASE_NAMESPACE" "$typ" "$b64" > "${dir}/allowed_signers"
    if ! ssh-keygen -Y verify -f "${dir}/allowed_signers" -I "$RELEASE_NAMESPACE" -n "$RELEASE_NAMESPACE" \
            -s "${dir}/checksums.txt.sig" < "${dir}/checksums.txt" >/dev/null 2>&1; then
        refuse "the checksums.txt of v${version} is not signed by the polis release key." \
            "(An ssh-keygen older than OpenSSH 8.0 cannot check it either: verify by hand, ${MANUAL_STEPS})"
    fi
}

# verify_checksum FILE NAME CHECKSUMS: FILE must match NAME's one line.
verify_checksum() {
    local file="$1" name="$2" checksums_file="$3"
    local expected actual

    if ! expected="$(awk -v n="$name" 'substr($0, 67) == n { print tolower(substr($0, 1, 64)); c++ } END { exit c != 1 }' "$checksums_file")"; then
        refuse "checksums.txt does not list ${name} exactly once."
    fi

    actual="$(sha256_of "$file")"
    if [[ "$expected" != "$actual" ]]; then
        refuse "checksum mismatch for ${name}" \
            "  expected: ${expected}" \
            "  actual:   ${actual}" \
            "Refusing to install a tampered or corrupted archive."
    fi
}

main() {
    local platform version archive_name base ext signed=true

    for arg in "$@"; do
        case "$arg" in
            --allow-unsigned) ALLOW_UNSIGNED=1 ;;
            *) echo "Unknown option: $arg" >&2; exit 1 ;;
        esac
    done

    platform="$(detect_platform)"
    version="${POLIS_VERSION:-$(get_latest_version)}"

    echo "[polis] Installing v${version} for ${platform}..."

    ext="tar.gz"
    [[ "$platform" == *"windows"* ]] && ext="zip"

    archive_name="polis-${platform}.${ext}"
    base="https://github.com/${REPO}/releases/download/v${version}"

    tmp="$(mktemp -d)"   # not local: the EXIT trap runs after main has returned
    trap 'rm -rf "$tmp"' EXIT

    echo "[polis] Downloading archive..."
    curl -fsSL "${base}/${archive_name}" -o "${tmp}/${archive_name}"

    echo "[polis] Downloading checksums and their signature..."
    curl -fsSL "${base}/checksums.txt" -o "${tmp}/checksums.txt"
    if ! curl -fsSL "${base}/checksums.txt.sig" -o "${tmp}/checksums.txt.sig" 2>/dev/null; then
        if [[ "$ALLOW_UNSIGNED" != "1" ]]; then
            refuse "v${version} has no checksums.txt.sig: it was made before releases were signed, or the signature is missing." \
                "Install the latest release instead, or set POLIS_ALLOW_UNSIGNED=1 to accept a checksum check alone."
        fi
        signed=false
        echo "[polis] WARNING: v${version} is unsigned. Its archive is checked only against the checksums.txt" >&2
        echo "[polis]          beside it: that shows it arrived intact, not that the release key signed it." >&2
    fi

    if [[ "$signed" == true ]]; then
        echo "[polis] Verifying the release signature..."
        verify_signature "$tmp" "$version"
        echo "[polis] Signature OK (polis release key)"
    fi

    echo "[polis] Verifying checksum..."
    verify_checksum "${tmp}/${archive_name}" "$archive_name" "${tmp}/checksums.txt"
    echo "[polis] Checksum OK"

    mkdir -p "$INSTALL_DIR"
    cd "$INSTALL_DIR"

    echo "[polis] Extracting..."
    if [[ "$ext" == "zip" ]]; then
        unzip -o "${tmp}/${archive_name}"
    else
        tar xzf "${tmp}/${archive_name}"
    fi
    chmod +x polis

    echo "[polis] Installed to ${INSTALL_DIR}/polis"

    if ! echo "$PATH" | grep -q "$INSTALL_DIR"; then
        echo ""
        echo "Add to your shell profile:"
        echo "  export PATH=\"\$PATH:$INSTALL_DIR\""
    fi
}

main "$@"
