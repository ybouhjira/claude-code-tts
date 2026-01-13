#!/bin/bash
set -e

# Claude Code TTS Plugin Installer
# Usage: curl -fsSL https://raw.githubusercontent.com/yourusername/claude-code-tts/main/install.sh | bash

REPO="yourusername/claude-code-tts"
INSTALL_DIR="$HOME/.claude/plugins/claude-code-tts"

echo "Installing Claude Code TTS Plugin..."
echo ""

# Check for Go
if ! command -v go &> /dev/null; then
    echo "Error: Go is not installed."
    echo "Please install Go 1.21+ from https://golang.org/dl/"
    exit 1
fi

# Check Go version
GO_VERSION=$(go version | grep -oE 'go[0-9]+\.[0-9]+' | sed 's/go//')
REQUIRED_VERSION="1.21"
if [ "$(printf '%s\n' "$REQUIRED_VERSION" "$GO_VERSION" | sort -V | head -n1)" != "$REQUIRED_VERSION" ]; then
    echo "Error: Go $REQUIRED_VERSION+ is required (found $GO_VERSION)"
    exit 1
fi

# Check for OpenAI API key
if [ -z "$OPENAI_API_KEY" ]; then
    echo "Warning: OPENAI_API_KEY is not set."
    echo "Set it before using the plugin:"
    echo "  export OPENAI_API_KEY=\"sk-...\""
    echo ""
fi

# Clone or update repository
if [ -d "$INSTALL_DIR" ]; then
    echo "Updating existing installation..."
    cd "$INSTALL_DIR"
    git pull --quiet
else
    echo "Cloning repository..."
    mkdir -p "$(dirname "$INSTALL_DIR")"
    git clone --quiet "https://github.com/$REPO.git" "$INSTALL_DIR"
    cd "$INSTALL_DIR"
fi

# Build
echo "Building..."
make build --quiet

# Create plugin structure
mkdir -p "$INSTALL_DIR/bin"
mkdir -p "$INSTALL_DIR/.claude"
cp bin/tts-server "$INSTALL_DIR/bin/"

echo ""
echo "Installation complete!"
echo ""
echo "Next steps:"
echo "  1. Ensure OPENAI_API_KEY is set in your environment"
echo "  2. Add the MCP server to Claude Code:"
echo "     claude mcp add tts $INSTALL_DIR/bin/tts-server"
echo ""
echo "Or add to ~/.config/claude-code/claude_desktop_config.json:"
echo '  {
    "mcpServers": {
      "tts": {
        "command": "'$INSTALL_DIR'/bin/tts-server",
        "env": {
          "OPENAI_API_KEY": "your-key-here"
        }
      }
    }
  }'
