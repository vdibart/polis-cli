#!/bin/bash
# Polis CLI Installer
# Usage: curl -fsSL https://raw.githubusercontent.com/vdibart/polis-cli/main/scripts/install.sh | bash

set -e

REPO="vdibart/polis-cli"
INSTALL_DIR="${POLIS_INSTALL_DIR:-$HOME/.local/bin}"

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

verify_checksum() {
    local archive_name="$1"
    local checksums_file="$2"
    local expected actual

    expected="$(grep "  ${archive_name}$" "$checksums_file" | awk '{print $1}')"
    if [[ -z "$expected" ]]; then
        echo "Error: no checksum entry for ${archive_name} in checksums.txt" >&2
        exit 1
    fi

    actual="$(sha256_of "$archive_name")"
    if [[ "$expected" != "$actual" ]]; then
        echo "Error: checksum mismatch for ${archive_name}" >&2
        echo "  expected: ${expected}" >&2
        echo "  actual:   ${actual}" >&2
        echo "Refusing to install a tampered or corrupted archive." >&2
        exit 1
    fi
}

main() {
    local platform version archive_name download_url checksums_url ext

    platform="$(detect_platform)"
    version="${POLIS_VERSION:-$(get_latest_version)}"

    echo "[polis] Installing v${version} for ${platform}..."

    ext="tar.gz"
    [[ "$platform" == *"windows"* ]] && ext="zip"

    archive_name="polis-${platform}.${ext}"
    download_url="https://github.com/${REPO}/releases/download/v${version}/${archive_name}"
    checksums_url="https://github.com/${REPO}/releases/download/v${version}/checksums.txt"

    mkdir -p "$INSTALL_DIR"
    cd "$INSTALL_DIR"

    echo "[polis] Downloading archive..."
    curl -fsSL "$download_url" -o "$archive_name"

    echo "[polis] Downloading checksums..."
    curl -fsSL "$checksums_url" -o "checksums.txt"

    echo "[polis] Verifying checksum..."
    verify_checksum "$archive_name" "checksums.txt"
    echo "[polis] Checksum OK"

    echo "[polis] Extracting..."
    if [[ "$ext" == "zip" ]]; then
        unzip -o "$archive_name"
    else
        tar xzf "$archive_name"
    fi
    rm "$archive_name" "checksums.txt"
    chmod +x polis

    echo "[polis] Installed to ${INSTALL_DIR}/polis"

    if ! echo "$PATH" | grep -q "$INSTALL_DIR"; then
        echo ""
        echo "Add to your shell profile:"
        echo "  export PATH=\"\$PATH:$INSTALL_DIR\""
    fi
}

main "$@"
