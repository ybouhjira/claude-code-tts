.PHONY: build install install-vscode-ext clean test lint run help

# Variables
BINARY_NAME=tts-server
CLI_BINARY_NAME=speak-text
READER_BINARY_NAME=tts-reader
INSTALL_DIR=$(HOME)/.claude/plugins/claude-code-tts
GO=go
GOFLAGS=-ldflags="-s -w"

# Default target
all: build

## build: Build the binaries
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p bin
	$(GO) build $(GOFLAGS) -o bin/$(BINARY_NAME) ./cmd/tts-server
	@echo "Built bin/$(BINARY_NAME)"
	@echo "Building $(CLI_BINARY_NAME)..."
	$(GO) build $(GOFLAGS) -o bin/$(CLI_BINARY_NAME) ./cmd/speak-text
	@echo "Built bin/$(CLI_BINARY_NAME)"
	@echo "Building $(READER_BINARY_NAME)..."
	$(GO) build $(GOFLAGS) -o bin/$(READER_BINARY_NAME) ./cmd/tts-reader
	@echo "Built bin/$(READER_BINARY_NAME)"

## install: Install plugin to Claude Code plugins directory
install: build
	@echo "Installing to $(INSTALL_DIR)..."
	@mkdir -p $(INSTALL_DIR)/bin
	@mkdir -p $(INSTALL_DIR)/.claude
	@mkdir -p $(INSTALL_DIR)/hooks
# Remove each installed binary before copying: overwriting a running
# program fails with "Text file busy", while removing first just unlinks
# the file (the running process keeps the old copy until it exits).
	@rm -f $(INSTALL_DIR)/bin/$(BINARY_NAME) $(INSTALL_DIR)/bin/$(CLI_BINARY_NAME) $(INSTALL_DIR)/bin/$(READER_BINARY_NAME)
	@cp bin/$(BINARY_NAME) $(INSTALL_DIR)/bin/
	@cp bin/$(CLI_BINARY_NAME) $(INSTALL_DIR)/bin/
	@cp bin/$(READER_BINARY_NAME) $(INSTALL_DIR)/bin/
	@cp scripts/speak-last $(INSTALL_DIR)/bin/
	@cp scripts/speak-file $(INSTALL_DIR)/bin/
	@cp scripts/tts-ctl $(INSTALL_DIR)/bin/
	@cp scripts/tts-read $(INSTALL_DIR)/bin/
	@cp hooks/auto-speak.sh $(INSTALL_DIR)/hooks/
	@cp hooks/tts-common.sh $(INSTALL_DIR)/hooks/
	@chmod +x $(INSTALL_DIR)/hooks/auto-speak.sh
	@chmod +x $(INSTALL_DIR)/bin/speak-last $(INSTALL_DIR)/bin/speak-file $(INSTALL_DIR)/bin/tts-ctl $(INSTALL_DIR)/bin/tts-read
	@mkdir -p $(INSTALL_DIR)/config
	@cp config/claude-code-tts.conf.example $(INSTALL_DIR)/config/
	@mkdir -p $(INSTALL_DIR)/commands
	@cp commands/*.md $(INSTALL_DIR)/commands/
	@mkdir -p $(HOME)/.claude/commands
	@cp commands/*.md $(HOME)/.claude/commands/
	@echo "Installed slash commands: /tts, /tts-last, /tts-read, /tts-full, /tts-sentence, /tts-off"
	@mkdir -p $(INSTALL_DIR)/.claude-plugin
	@cp .claude-plugin/plugin.json $(INSTALL_DIR)/.claude-plugin/
	@cp hooks/hooks.json $(INSTALL_DIR)/hooks/
	@cp .mcp.json $(INSTALL_DIR)/
	@cp .claude/settings.json $(INSTALL_DIR)/.claude/
	@cp README.md $(INSTALL_DIR)/
	@cp LICENSE $(INSTALL_DIR)/
	@echo "Installed successfully!"
	@echo ""
	@echo "Add this to your claude_desktop_config.json or run:"
	@echo "  claude mcp add tts $(INSTALL_DIR)/bin/$(BINARY_NAME)"

## install-vscode-ext: Install the companion VS Code extension (chat-panel context menu + zero-click reader)
EXT_VERSION=0.2.0
install-vscode-ext:
	@echo "Installing the claude-code-tts-bridge VS Code extension..."
	@rm -rf $(HOME)/.vscode/extensions/claude-code-tts.claude-code-tts-bridge-*
	@mkdir -p $(HOME)/.vscode/extensions/claude-code-tts.claude-code-tts-bridge-$(EXT_VERSION)
	@cp vscode-extension/package.json vscode-extension/extension.js \
		$(HOME)/.vscode/extensions/claude-code-tts.claude-code-tts-bridge-$(EXT_VERSION)/
	@echo "Installed. Reload the VS Code window once (Developer: Reload Window) to activate it."

## uninstall: Remove plugin from Claude Code plugins directory
uninstall:
	@echo "Uninstalling from $(INSTALL_DIR)..."
	@rm -rf $(INSTALL_DIR)
	@echo "Uninstalled successfully!"

## clean: Remove build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf bin/
	@echo "Cleaned."

## test: Run tests
test:
	@echo "Running tests..."
	$(GO) test -v ./...

## test-coverage: Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	$(GO) test -cover -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

## lint: Run linter
lint:
	@echo "Running linter..."
	@which golangci-lint > /dev/null || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	golangci-lint run ./...

## fmt: Format code
fmt:
	@echo "Formatting code..."
	$(GO) fmt ./...

## run: Run the server directly (for development)
run: build
	@echo "Running $(BINARY_NAME)..."
	./bin/$(BINARY_NAME)

## deps: Download dependencies
deps:
	@echo "Downloading dependencies..."
	$(GO) mod download
	$(GO) mod tidy

## update-deps: Update dependencies
update-deps:
	@echo "Updating dependencies..."
	$(GO) get -u ./...
	$(GO) mod tidy

## help: Show this help
help:
	@echo "Claude Code TTS Plugin - Makefile targets:"
	@echo ""
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'
