# Makefile — the repo's quality gates.
#
# Every gate lands here FIRST and CI (.github/workflows/) only invokes a target;
# a check that exists solely in a workflow file cannot be run before a push, so
# it is not a gate, it is a surprise. `make ci` is what must be green locally.

SHELL := /bin/bash

BIN      := bin/devctl
PKG      := ./cmd/devctl
# Injected into main.version by ldflags. A release overrides it from the tag;
# a local build reports the git description so `devctl version` never lies about
# which tree it came from. `devctl upgrade` will compare against this value.
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS  := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

# Race-test process count. The gate runs a small, CI-friendly fan-out by
# default; RACE_PROCS raises it for a local soak run without editing the test.
RACE_PROCS ?=

# RACE_STORE_DIR points the gates at a filesystem of the caller's choosing. The
# invariants the store rests on are the FILESYSTEM's, not Go's, so being able to
# aim them at a virtiofs mount is the difference between measuring the claim and
# assuming it.
RACE_STORE_DIR ?=

# The platforms a release ships, kept in step with .goreleaser.yml. Compiling
# every one of them is a real gate rather than a formality: a build tag, a
# syscall, or a constant that exists on only one of them fails here, on any
# machine, instead of at release time on a runner nobody is watching. It is also
# why the test matrix does not need a second operating system to catch a
# platform-specific compile error.
PLATFORMS ?= darwin/arm64 darwin/amd64 linux/arm64 linux/amd64

.DEFAULT_GOAL := help
.PHONY: help build cross install fmt fix pre-commit lint test race vuln ci tools tool-lint tool-vuln clean

help:
	@echo "Targets:"
	@echo "  make build    build $(BIN) with version/commit stamped in"
	@echo "  make cross    compile every platform a release ships"
	@echo "  make install  go install devctl into GOBIN"
	@echo "  make fmt      apply the configured formatters (gofumpt + goimports)"
	@echo "  make fix      apply every automatic fix: go fix, the formatters, --fix linters"
	@echo "  make pre-commit  the fast subset a commit hook runs"
	@echo "  make lint     go vet + golangci-lint over the tree"
	@echo "  make test     go test -race -shuffle=on ./... with coverage"
	@echo "  make race     the multi-process store race gate, verbosely"
	@echo "  make vuln     govulncheck ./..."
	@echo "  make ci       lint + cross + test + vuln — must be green before a push"
	@echo "  make tools    install the pinned dev tools into GOBIN"

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) $(PKG)

# CGO_ENABLED=0 matches what .goreleaser.yml sets, so this compiles the way a
# release does rather than however the host happens to be configured. The output
# goes nowhere: the question is whether it builds, not what it produces.
cross:
	@set -e; for platform in $(PLATFORMS); do \
	  goos=$${platform%/*}; goarch=$${platform#*/}; \
	  echo "GOOS=$$goos GOARCH=$$goarch go build $(PKG)"; \
	  CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch \
	    go build -trimpath -ldflags '$(LDFLAGS)' -o /dev/null $(PKG); \
	done

install:
	go install -trimpath -ldflags '$(LDFLAGS)' $(PKG)

fmt:
	golangci-lint fmt ./...

# Every fix a tool can apply on its own. `go fix` is a real analyzer-driven
# rewriter since Go 1.27, not the legacy API updater it used to be, so it earns
# its place beside the formatters.
fix:
	go fix ./...
	golangci-lint fmt ./...
	golangci-lint run --fix ./...

# What a commit hook runs. Deliberately NOT `make ci`: a hook slow enough to be
# annoying is a hook that gets --no-verify'd, and then it guards nothing.
# golangci-lint's full run belongs in `make lint`, which is a gate, not a hook.
pre-commit:
	@command -v golangci-lint >/dev/null 2>&1 || { \
	  echo "golangci-lint is not installed; run 'make tool-lint'" >&2; exit 1; }
	go fix ./...
	golangci-lint fmt ./...
	go vet ./...

# go vet runs separately from golangci-lint: it is the one static check that
# needs no third-party binary, so it still reports something useful on a machine
# where golangci-lint is missing.
lint:
	go vet ./...
	golangci-lint run ./...

test:
	go test -race -shuffle=on -coverprofile=coverage.out ./...

# The store's concurrency claim is a REAL gate, not a comment: this target runs
# it alone and verbosely so its invariant counts are readable in a CI log.
# -args MUST come after the package list: everything following it is handed to
# the test binary, so a package named after it is read as a test flag.
race:
	go test -race -count=1 -run 'TestStore_.*Concurrent' -v ./internal/store \
	  $(if $(RACE_PROCS)$(RACE_STORE_DIR),-args,) \
	  $(if $(RACE_PROCS),-race-procs=$(RACE_PROCS),) \
	  $(if $(RACE_STORE_DIR),-race-store-dir=$(RACE_STORE_DIR),)

vuln:
	govulncheck ./...

ci: lint cross test vuln

# Dev tools are PINNED here and installed with `go install`, not carried as
# go.mod `tool` directives: golangci-lint and goreleaser each drag a module graph
# far larger than devctl's own into go.sum, which every `go mod download` in
# every CI job would then pay for. This file is the single version of truth, and
# `make tool-*` is what CI runs, so CI and a laptop lint with the same binary.
GOLANGCI_VERSION    ?= v2.13.2
GOVULNCHECK_VERSION ?= v1.8.0

tools: tool-lint tool-vuln

tool-lint:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)

tool-vuln:
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

clean:
	rm -rf bin dist coverage.out coverage.html
