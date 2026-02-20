#!/bin/sh
# install.sh — install dotsmith from GitHub Releases
#
# Usage:
#   curl -sSL https://raw.githubusercontent.com/andersosthus/dotsmith/main/install.sh | sh
#
# Environment variables:
#   DOTSMITH_VERSION     — pin a specific release tag (e.g. v1.2.0); defaults to latest
#   DOTSMITH_INSTALL_DIR — install binary here; defaults to ~/.local/bin
set -eu

REPO="andersosthus/dotsmith"
INSTALL_DIR="${DOTSMITH_INSTALL_DIR:-$HOME/.local/bin}"

# --- detect OS ---
os=$(uname -s)
case "$os" in
    Linux)  os_name="linux" ;;
    Darwin) os_name="darwin" ;;
    *)
        echo "error: unsupported OS: $os" >&2
        exit 1
        ;;
esac

# --- detect arch ---
arch=$(uname -m)
case "$arch" in
    x86_64)        arch_name="amd64" ;;
    aarch64|arm64) arch_name="arm64" ;;
    *)
        echo "error: unsupported architecture: $arch" >&2
        exit 1
        ;;
esac

# --- pick download tool ---
if command -v curl >/dev/null 2>&1; then
    fetch() { curl -sSfL "$1" -o "$2"; }
    fetch_stdout() { curl -sSfL "$1"; }
elif command -v wget >/dev/null 2>&1; then
    fetch() { wget -qO "$2" "$1"; }
    fetch_stdout() { wget -qO- "$1"; }
else
    echo "error: curl or wget is required" >&2
    exit 1
fi

# --- resolve version ---
if [ -z "${DOTSMITH_VERSION:-}" ]; then
    echo "Fetching latest release version..."
    version=$(fetch_stdout "https://api.github.com/repos/${REPO}/releases/latest" \
        | grep '"tag_name"' \
        | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
    if [ -z "$version" ]; then
        echo "error: could not determine latest release; set DOTSMITH_VERSION manually" >&2
        exit 1
    fi
else
    version="$DOTSMITH_VERSION"
fi

# GoReleaser archive names use the version without the leading 'v'.
version_num="${version#v}"
archive="dotsmith_${version_num}_${os_name}_${arch_name}.tar.gz"
base_url="https://github.com/${REPO}/releases/download/${version}"

# --- temp dir with guaranteed cleanup ---
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT INT TERM

echo "Downloading dotsmith ${version} (${os_name}/${arch_name})..."
fetch "${base_url}/${archive}" "${tmpdir}/${archive}"
fetch "${base_url}/checksums.txt" "${tmpdir}/checksums.txt"

# --- verify checksum ---
echo "Verifying checksum..."
if command -v sha256sum >/dev/null 2>&1; then
    (cd "$tmpdir" && grep "  ${archive}$" checksums.txt | sha256sum -c -)
elif command -v shasum >/dev/null 2>&1; then
    (cd "$tmpdir" && grep "  ${archive}$" checksums.txt | shasum -a 256 -c -)
else
    echo "warning: sha256sum and shasum not found — skipping checksum verification" >&2
fi

# --- extract and install ---
echo "Installing to ${INSTALL_DIR}/dotsmith..."
mkdir -p "$INSTALL_DIR"
tar -xzf "${tmpdir}/${archive}" -C "$tmpdir"
install -m755 "${tmpdir}/dotsmith" "${INSTALL_DIR}/dotsmith"

echo "dotsmith ${version} installed successfully."

# --- PATH warning ---
case ":${PATH}:" in
    *":${INSTALL_DIR}:"*) ;;
    *)
        echo "" >&2
        echo "warning: ${INSTALL_DIR} is not in your PATH." >&2
        echo "  Add this to your shell profile:" >&2
        echo "    export PATH=\"\${PATH}:${INSTALL_DIR}\"" >&2
        ;;
esac
