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

.DEFAULT_GOAL := help
.PHONY: help build install fmt lint test race vuln ci tools tool-lint tool-vuln clean

help:
	@echo "Targets:"
	@echo "  make build    build $(BIN) with version/commit stamped in"
	@echo "  make install  go install devctl into GOBIN"
	@echo "  make fmt      apply the configured formatters (gofumpt + goimports)"
	@echo "  make lint     go vet + golangci-lint over the tree"
	@echo "  make test     go test -race -shuffle=on ./... with coverage"
	@echo "  make race     the multi-process store race gate, verbosely"
	@echo "  make vuln     govulncheck ./..."
	@echo "  make ci       lint + test + vuln — must be green before a push"
	@echo "  make tools    install the pinned dev tools into GOBIN"

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) $(PKG)

install:
	go install -trimpath -ldflags '$(LDFLAGS)' $(PKG)

fmt:
	golangci-lint fmt ./...

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
	go test -race -count=1 -run 'TestStore_.*Concurrent' -v ./internal/store $(if $(RACE_PROCS),-args -race-procs=$(RACE_PROCS),)

vuln:
	govulncheck ./...

ci: lint test vuln

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
