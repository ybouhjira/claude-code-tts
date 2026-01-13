.PHONY: build install clean test lint run help

# Variables
BINARY_NAME=tts-server
INSTALL_DIR=$(HOME)/.claude/plugins/claude-code-tts
GO=go
GOFLAGS=-ldflags="-s -w"

# Default target
all: build

## build: Build the binary
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p bin
	$(GO) build $(GOFLAGS) -o bin/$(BINARY_NAME) ./cmd/tts-server
	@echo "Built bin/$(BINARY_NAME)"

## install: Install plugin to Claude Code plugins directory
install: build
	@echo "Installing to $(INSTALL_DIR)..."
	@mkdir -p $(INSTALL_DIR)/bin
	@mkdir -p $(INSTALL_DIR)/.claude
	@cp bin/$(BINARY_NAME) $(INSTALL_DIR)/bin/
	@cp plugin.json $(INSTALL_DIR)/
	@cp .mcp.json $(INSTALL_DIR)/
	@cp .claude/settings.json $(INSTALL_DIR)/.claude/
	@cp README.md $(INSTALL_DIR)/
	@cp LICENSE $(INSTALL_DIR)/
	@echo "Installed successfully!"
	@echo ""
	@echo "Add this to your claude_desktop_config.json or run:"
	@echo "  claude mcp add tts $(INSTALL_DIR)/bin/$(BINARY_NAME)"

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
