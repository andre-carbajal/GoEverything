#!/usr/bin/env bash
set -euo pipefail

REPO="andre-carbajal/GoEverything"
BINARY="ge"
PROJECT="goeverything"

need_cmd() {
	local command_name="$1"
	command -v "$command_name" >/dev/null 2>&1 || { echo "error: $command_name is required" >&2; exit 1; }
}

need_cmd curl
need_cmd tar

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH_RAW="$(uname -m)"
case "$OS" in
  linux) GOOS="linux" ;;
  darwin) GOOS="darwin" ;;
  *) echo "error: unsupported OS: $OS" >&2; exit 1 ;;
esac

case "$ARCH_RAW" in
  x86_64|amd64) GOARCH="amd64" ;;
  arm64|aarch64) GOARCH="arm64" ;;
  *) echo "error: unsupported architecture: $ARCH_RAW" >&2; exit 1 ;;
esac

TAG="${1:-}"
if [[ -z "$TAG" ]]; then
	TAG="$(curl --proto '=https' --tlsv1.2 -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)"
fi

if [[ -z "$TAG" ]]; then
  echo "error: could not resolve release tag" >&2
  exit 1
fi

VERSION="${TAG#v}"
ASSET="${PROJECT}_${VERSION}_${GOOS}_${GOARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${TAG}/${ASSET}"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

echo "Downloading ${URL}..."
curl --proto '=https' --tlsv1.2 -fL "$URL" -o "$TMP_DIR/$ASSET"
tar -xzf "$TMP_DIR/$ASSET" -C "$TMP_DIR"

if [[ ! -f "$TMP_DIR/$BINARY" ]]; then
  echo "error: binary $BINARY not found in release archive" >&2
  exit 1
fi

TARGET_DIR="/usr/local/bin"
if [[ ! -w "$TARGET_DIR" ]]; then
  TARGET_DIR="$HOME/.local/bin"
  mkdir -p "$TARGET_DIR"
fi

install -m 0755 "$TMP_DIR/$BINARY" "$TARGET_DIR/$BINARY"

echo "Installed $BINARY to $TARGET_DIR/$BINARY"
if ! command -v "$BINARY" >/dev/null 2>&1; then
  echo "Add this to your shell profile:" >&2
  echo "  export PATH=\"$TARGET_DIR:\$PATH\"" >&2
fi
