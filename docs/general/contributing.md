# Contributing to Polis

Thank you for your interest in contributing to Polis! This document covers the development setup for each component and the conventions to follow.

## Repository Layout

Polis has four main components:

| Component | Language | Status | Description |
|-----------|----------|--------|-------------|
| `cli-go/` | Go | Active | Go CLI — core packages imported by the webapp |
| `webapp/` | Go | Active | Local web interface for managing a Polis site |
| `cli-bash/` | Bash | Frozen (v0.56.0) | Original CLI — functional but receives no new features |
| Discovery Service | TypeScript | Active (closed for now) | Hono server for discovery coordination. Source not yet in this public repo; planned for open-source release. The [DS API reference](../ds/developer/api-reference.md) is the stable public contract. |

**Key dependency rule:** The Go CLI (`cli-go/pkg/`) owns all core packages. The webapp imports from the CLI, never the reverse.

## How to Contribute

### Reporting Bugs

Before submitting a bug report:

1. Check existing issues to avoid duplicates
2. Use the latest version
3. Include in your report:
   - Which component (Go CLI, webapp, bash CLI, discovery service)
   - OS and version
   - Steps to reproduce
   - Expected vs actual behavior
   - Relevant error messages

### Suggesting Features

Feature requests are welcome! Please:

1. Check existing issues for similar requests
2. Describe the use case clearly
3. Specify which component it affects

### Submitting Changes

1. **Fork** the repository
2. **Create a branch** for your changes:
   ```bash
   git checkout -b feature/your-feature-name
   ```
3. **Make your changes** following the conventions below
4. **Test** your changes (see component-specific sections)
5. **Commit** with clear, descriptive messages
6. **Open a Pull Request** with:
   - Clear description of the changes
   - Which component(s) are affected
   - Reference to any related issues

## Go CLI (`cli-go/`)

### Prerequisites

- **Go 1.21+**

### Development

```bash
cd cli-go

# Build
go build ./...

# Run tests
go test ./...

# Build CLI binary
go build -o polis ./cmd/polis
```

### Package Structure

Core packages live in `cli-go/pkg/` and are designed to be importable:

- `pkg/publish/` — Post publishing logic
- `pkg/comment/` — Comment management
- `pkg/blessing/` — Blessing workflow
- `pkg/signing/` — Ed25519 cryptographic signing
- `pkg/render/` — Markdown to HTML rendering
- `pkg/template/` — Mustache-like template engine
- `pkg/discovery/` — Discovery service HTTP client
- `pkg/metadata/` — Public index (JSONL) management

See [cli-go/README.md](../../cli-go/README.md) for the full package list and library usage examples.

### Version Propagation

Packages that write version strings into files must follow this pattern:

1. Add `var Version = "dev"` after imports
2. Add `func GetGenerator() string { return "polis-cli-go/" + Version }`
3. Add `<pkg>.Version = Version` in `cmd/root.go` `Execute()`
4. Use `GetGenerator()` (not bare `Version`) when writing to metadata files
5. Add a test verifying the written version matches `GetGenerator()`

### Code Style

- Use `gofmt` (enforced)
- Follow existing patterns in the codebase
- Tests go in `*_test.go` files alongside implementation
- Use `t.TempDir()` for test fixtures

## Webapp (`webapp/`)

### Prerequisites

- **Go 1.21+**

### Development

```bash
cd webapp

# Build webapp-only binary
go build -o polis-server ./cmd/server

# Build bundled binary (CLI + serve)
go build -o polis-full ./cmd/polis-full

# Run tests
go test ./...

# Quick iteration: build and run
go build -o polis-server ./cmd/server && ./polis-server
```

### Critical Rules

- The webapp **imports from `cli-go/pkg/`**, never the reverse
- If you change packages in `cli-go/`, rebuild both: `cd cli-go && go build ./... && cd ../webapp && go build ./...`
- Add or update tests for every handler change
- Check bash CLI parity when modifying behavior that both CLIs share

See [webapp/CLAUDE.md](../../webapp/CLAUDE.md) for detailed patterns, handler conventions, and frontend architecture.

### Key Files

- `internal/server/handlers.go` — HTTP handlers
- `internal/server/server.go` — Server struct and configuration
- `internal/server/routes.go` — Route registration
- `internal/webui/www/app.js` — Frontend SPA
- `internal/webui/www/style.css` — Styles

## Bash CLI (`cli-bash/`)

> The bash CLI is **feature-frozen** at v0.56.0. Bug fixes are accepted but new features should be implemented in the Go CLI.

### Prerequisites

- **Bash 4.0+**, **OpenSSH 8.0+**, **jq**, **curl**, **sha256sum/shasum**
- **ShellCheck** (for linting)

### Development

```bash
cd cli-bash

# Lint
shellcheck bin/polis

# Run tests
./tests/run_tests.sh

# Run tests by category
./tests/run_tests.sh --category unit
./tests/run_tests.sh --skip-network
```

### Code Style

- Quote variables: `"$variable"` not `$variable`
- Use `[[ ]]` for conditionals
- Use `$(command)` not backticks
- Functions: `snake_case`; constants: `UPPER_SNAKE_CASE`
- All commands must support `--json` output mode

## Discovery Service

The DS source is not part of this public repo at this time, so contributions to the DS itself are not yet accepted via pull request. Issues, API-contract feedback, and proposed event-stream additions are very welcome — open an issue against this repo or follow the reporting channels below.

See the [DS API reference](../ds/developer/api-reference.md) and [stream architecture doc](../ds/developer/stream-architecture.md) for the stable public contract that the canonical deployment (and any future open-source release) honors.

## Pull Request Process

1. Ensure tests pass for affected components
2. Update documentation if you've changed user-facing functionality
3. Add entries to CHANGELOG.md for notable changes
4. Keep PRs focused — one feature or fix per PR

## Community Guidelines

- Be respectful and constructive in discussions
- Help others when you can

Thank you for contributing to Polis!
