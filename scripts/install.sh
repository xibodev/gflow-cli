#!/usr/bin/env bash
set -e

REPO="xibodev/gflow-cli"
INSTALL_DIR="$HOME/.gflow/bin"

echo "⚡ Installing gflow-cli for $(uname -s)..."

mkdir -p "$INSTALL_DIR"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

RELEASE_URL="https://api.github.com/repos/$REPO/releases/latest"
DOWNLOAD_URL=$(curl -s "$RELEASE_URL" | grep "browser_download_url" | grep "$OS" | grep "$ARCH" | cut -d '"' -f 4 | head -n 1)

if [ -n "$DOWNLOAD_URL" ]; then
  echo "Downloading from $DOWNLOAD_URL..."
  TMP_TAR="/tmp/gflow.tar.gz"
  curl -fsSL "$DOWNLOAD_URL" -o "$TMP_TAR"
  tar -xzf "$TMP_TAR" -C "$INSTALL_DIR"
  rm -f "$TMP_TAR"
  chmod +x "$INSTALL_DIR/gflow"
else
  echo "Prebuilt binary not found. Falling back to go install..."
  go install "github.com/$REPO/cmd/gflow@latest"
fi

# Add to PATH hint
if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
  echo ""
  echo "Add gflow to your PATH by adding this line to your ~/.bashrc or ~/.zshrc:"
  echo "  export PATH=\"\$HOME/.gflow/bin:\$PATH\""
fi

echo ""
echo "✔ gflow installed successfully!"
echo "Run 'gflow setup' to get started."
