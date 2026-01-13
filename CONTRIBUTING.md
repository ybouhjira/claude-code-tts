# Contributing to Claude Code TTS

Thank you for your interest in contributing! This document provides guidelines and steps for contributing.

## Code of Conduct

Be respectful, inclusive, and constructive. We're all here to make something great together.

## How to Contribute

### Reporting Bugs

1. Check existing issues to avoid duplicates
2. Use the bug report template
3. Include:
   - Go version (`go version`)
   - OS and version
   - Steps to reproduce
   - Expected vs actual behavior
   - Error messages/logs

### Suggesting Features

1. Check existing issues/discussions
2. Describe the use case
3. Explain why it would benefit users
4. Consider implementation complexity

### Pull Requests

1. **Fork** the repository
2. **Create a branch** from `main`:
   ```bash
   git checkout -b feature/my-feature
   # or
   git checkout -b fix/bug-description
   ```
3. **Make your changes**
4. **Test** your changes:
   ```bash
   make test
   make build
   ```
5. **Commit** with clear messages:
   ```bash
   git commit -m "feat: add support for custom audio format"
   # or
   git commit -m "fix: handle empty text input gracefully"
   ```
6. **Push** and create a PR

## Development Setup

```bash
# Clone your fork
git clone https://github.com/YOUR_USERNAME/claude-code-tts.git
cd claude-code-tts

# Install dependencies
go mod download

# Build
make build

# Run tests
make test

# Run linter
make lint
```

## Code Style

- Follow standard Go conventions
- Use `gofmt` for formatting
- Run `golangci-lint` before committing
- Write tests for new functionality
- Document exported functions

## Commit Message Convention

We use [Conventional Commits](https://www.conventionalcommits.org/):

- `feat:` - New features
- `fix:` - Bug fixes
- `docs:` - Documentation changes
- `test:` - Test additions/changes
- `refactor:` - Code refactoring
- `chore:` - Build/tooling changes

## Project Structure

```
internal/
├── audio/     # Audio playback (platform-specific)
├── server/    # MCP server & worker pool
└── tts/       # OpenAI TTS client

cmd/
└── tts-server/  # Main entry point
```

## Testing

- Unit tests go next to the code they test
- Use table-driven tests where appropriate
- Mock external services (OpenAI API)

```bash
# Run all tests
make test

# Run with coverage
go test -cover ./...

# Run specific test
go test -run TestSomething ./internal/...
```

## Questions?

Open a discussion or issue - we're happy to help!
