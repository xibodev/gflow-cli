#!/usr/bin/env bash
set -euo pipefail

REPO="xibodev/gflow-cli"
INSTALL_DIR="$HOME/.gflow/bin"

echo "⚡ Installing gflow-cli for $(uname -s)..."

mkdir -p "$INSTALL_DIR"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH (supported: x86_64/amd64, aarch64/arm64)" >&2; exit 1 ;;
esac

case "$OS" in
  linux|darwin) ;;
  *) echo "Unsupported OS: $OS (supported: linux, darwin; Windows uses install.ps1)" >&2; exit 1 ;;
esac

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/gflow-install.XXXXXX")"
cleanup() { rm -rf "$TMP_DIR"; }
trap cleanup EXIT

use_gh=false
if command -v gh >/dev/null 2>&1; then
  use_gh=true
fi

# Authenticated gh is preferred for private releases; fall back to curl.
if [ "$use_gh" = true ]; then
  ASSET_PATTERN="*${OS}_${ARCH}.tar.gz"
  if ! gh release download -R "$REPO" --pattern "$ASSET_PATTERN" -D "$TMP_DIR"; then
    echo "No prebuilt release asset found via gh. Falling back to go install..." >&2
    GOBIN="$INSTALL_DIR" go install "github.com/$REPO/cmd/gflow@latest"
    exit 0
  fi
  ARCHIVE="$(ls "$TMP_DIR"/*"${OS}_${ARCH}".tar.gz 2>/dev/null | head -n 1)"
else
  RELEASE_URL="https://api.github.com/repos/$REPO/releases/latest"
  DOWNLOAD_URL=$(curl -fsSL "$RELEASE_URL" | grep "browser_download_url" | grep "$OS" | grep "$ARCH" | cut -d '"' -f 4 | head -n 1)
  if [ -z "${DOWNLOAD_URL:-}" ]; then
    echo "Prebuilt binary not found. Falling back to go install..." >&2
    GOBIN="$INSTALL_DIR" go install "github.com/$REPO/cmd/gflow@latest"
    exit 0
  fi
  ARCHIVE="$TMP_DIR/gflow.tar.gz"
  echo "Downloading from $DOWNLOAD_URL..."
  curl -fsSL "$DOWNLOAD_URL" -o "$ARCHIVE"
fi

# Verify checksum when the release publishes checksums.txt alongside the asset.
if [ "$use_gh" = true ]; then
  if gh release download -R "$REPO" --pattern "checksums.txt" -D "$TMP_DIR" 2>/dev/null; then
    (cd "$TMP_DIR" && sha256sum -c --status --ignore-missing checksums.txt) || {
      echo "Checksum verification failed; aborting." >&2
      exit 1
    }
    echo "Checksum verified."
  fi
fi

tar -xzf "$ARCHIVE" -C "$INSTALL_DIR"
chmod +x "$INSTALL_DIR/gflow"

# Add to PATH hint
if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
  echo ""
  echo "Add gflow to your PATH by adding this line to your ~/.bashrc or ~/.zshrc:"
  echo "  export PATH=\"\$HOME/.gflow/bin:\$PATH\""
fi

echo ""
echo "✔ gflow installed successfully!"
echo "Location: $INSTALL_DIR/gflow"
echo ""
echo "Quickstart & Getting Started:"
echo "  1. Check provider status:"
echo "     gflow status"
echo "  2. Test chat in terminal (Gemini Flash):"
echo "     gflow chat \"Hello Gemini\""
echo "  3. Generate an image (Imagen 3, extension-free):"
echo "     gflow image \"cyberpunk cat on a neon roof\""
echo "  4. Generate a video (Veo / MiniMax H3):"
echo "     gflow video \"ocean waves crashing against cliffs\""
echo ""
echo "Supported Providers:"
echo "  • Gemini (default): Extension-free Imagen 3 images, Veo video, Lyria music, Flash chat."
echo "  • MiniMax: H3 video via captured desktop token ('gflow video ... -P minimax')."
echo "  • Google Flow: Extension-free via CDP ('gflow browser', then 'gflow image ... -P flow')."
echo ""
echo "MCP for AI Coding Assistants (Cursor / Claude / OpenCode / Windsurf):"
echo "  Command: gflow"
echo "  Args:    [\"mcp\"]"
echo ""
