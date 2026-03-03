# Polis Build Makefile
#
# Build targets (output to dist/):
#   make cli               Build polis CLI binary (~8-9 MB)
#   make webapp            Build polis-server webapp binary (~11 MB)
#   make bundled           Build polis-full CLI+webapp binary (~11-12 MB)
#   make all               Build cli + webapp + bundled (default)
#
# Test & clean:
#   make test              Run all Go tests (cli-go + webapp)
#   make clean             Remove dist/ and stale binaries
#
# Release (cross-compile for linux/darwin/windows):
#   make release           Build all release binaries
#   make release-cli       CLI only
#   make release-webapp    Webapp only
#   make release-bundled   Bundled only

.PHONY: all cli webapp bundled clean test

# ── Configuration ──────────────────────────────────────────────────

# All Go binaries share the same version from cli-go/version.txt
CLI_VERSION := $(shell cat cli-go/version.txt)

# Build output directory
DIST := dist

# Default target: build all local binaries
all: cli webapp bundled

# ── Build Targets ──────────────────────────────────────────────────

# CLI-only binary: cli-go/cmd/polis -> dist/polis
cli:
	@mkdir -p $(DIST)
	cd cli-go && go build -ldflags "-X main.Version=$(CLI_VERSION)" -o ../$(DIST)/polis ./cmd/polis
	@echo "Built $(DIST)/polis (version $(CLI_VERSION))"

# Webapp-only binary: webapp/cmd/server -> dist/polis-server
webapp:
	@mkdir -p $(DIST)
	cd webapp && go build -ldflags "-X main.Version=$(CLI_VERSION)" -o ../$(DIST)/polis-server ./cmd/server
	@echo "Built $(DIST)/polis-server (version $(CLI_VERSION))"

# Bundled CLI+webapp binary: webapp/cmd/polis-full -> dist/polis-full
bundled:
	@mkdir -p $(DIST)
	cd webapp && go build -ldflags "-X main.Version=$(CLI_VERSION)" -o ../$(DIST)/polis-full ./cmd/polis-full
	@echo "Built $(DIST)/polis-full (version $(CLI_VERSION))"

# ── Test & Clean ───────────────────────────────────────────────────

# Run all Go tests across both modules.
test:
	cd cli-go && go test ./...
	cd webapp && go test ./...

# Remove all build artifacts
clean:
	rm -rf $(DIST)
	rm -f cli-go/polis
	rm -f webapp/server
	rm -f webapp/polis-full

# ── Release Targets ────────────────────────────────────────────────
# Cross-compile for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64.

.PHONY: release-cli release-webapp release-bundled release

release-cli:
	@mkdir -p $(DIST)
	cd cli-go && GOOS=linux GOARCH=amd64 go build -ldflags "-X main.Version=$(CLI_VERSION)" -o ../$(DIST)/polis-linux-amd64 ./cmd/polis
	cd cli-go && GOOS=linux GOARCH=arm64 go build -ldflags "-X main.Version=$(CLI_VERSION)" -o ../$(DIST)/polis-linux-arm64 ./cmd/polis
	cd cli-go && GOOS=darwin GOARCH=amd64 go build -ldflags "-X main.Version=$(CLI_VERSION)" -o ../$(DIST)/polis-darwin-amd64 ./cmd/polis
	cd cli-go && GOOS=darwin GOARCH=arm64 go build -ldflags "-X main.Version=$(CLI_VERSION)" -o ../$(DIST)/polis-darwin-arm64 ./cmd/polis
	cd cli-go && GOOS=windows GOARCH=amd64 go build -ldflags "-X main.Version=$(CLI_VERSION)" -o ../$(DIST)/polis-windows-amd64.exe ./cmd/polis
	@echo "Built CLI release binaries"

release-webapp:
	@mkdir -p $(DIST)
	cd webapp && GOOS=linux GOARCH=amd64 go build -ldflags "-X main.Version=$(CLI_VERSION)" -o ../$(DIST)/polis-server-linux-amd64 ./cmd/server
	cd webapp && GOOS=linux GOARCH=arm64 go build -ldflags "-X main.Version=$(CLI_VERSION)" -o ../$(DIST)/polis-server-linux-arm64 ./cmd/server
	cd webapp && GOOS=darwin GOARCH=amd64 go build -ldflags "-X main.Version=$(CLI_VERSION)" -o ../$(DIST)/polis-server-darwin-amd64 ./cmd/server
	cd webapp && GOOS=darwin GOARCH=arm64 go build -ldflags "-X main.Version=$(CLI_VERSION)" -o ../$(DIST)/polis-server-darwin-arm64 ./cmd/server
	cd webapp && GOOS=windows GOARCH=amd64 go build -ldflags "-X main.Version=$(CLI_VERSION)" -o ../$(DIST)/polis-server-windows-amd64.exe ./cmd/server
	@echo "Built webapp release binaries"

release-bundled:
	@mkdir -p $(DIST)
	cd webapp && GOOS=linux GOARCH=amd64 go build -ldflags "-X main.Version=$(CLI_VERSION)" -o ../$(DIST)/polis-full-linux-amd64 ./cmd/polis-full
	cd webapp && GOOS=linux GOARCH=arm64 go build -ldflags "-X main.Version=$(CLI_VERSION)" -o ../$(DIST)/polis-full-linux-arm64 ./cmd/polis-full
	cd webapp && GOOS=darwin GOARCH=amd64 go build -ldflags "-X main.Version=$(CLI_VERSION)" -o ../$(DIST)/polis-full-darwin-amd64 ./cmd/polis-full
	cd webapp && GOOS=darwin GOARCH=arm64 go build -ldflags "-X main.Version=$(CLI_VERSION)" -o ../$(DIST)/polis-full-darwin-arm64 ./cmd/polis-full
	cd webapp && GOOS=windows GOARCH=amd64 go build -ldflags "-X main.Version=$(CLI_VERSION)" -o ../$(DIST)/polis-full-windows-amd64.exe ./cmd/polis-full
	@echo "Built bundled release binaries"

# Build all release binaries (CLI + webapp + bundled) for all platforms
release: release-cli release-webapp release-bundled
