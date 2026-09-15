# SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
# SPDX-License-Identifier: GPL-3.0-or-later

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
# which tree it came from. `devctl upgrade` compares a release against this
# value, and refuses to act on a describe-style one: it is not a release, and
# under semver it sorts BELOW the tag it carries.
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
.PHONY: help build cross install fmt fix pre-commit lint license-check test race vuln ci release release-preflight tools tool-lint tool-vuln tool-release clean

help:
	@echo "Targets:"
	@echo "  make build    build $(BIN) with version/commit stamped in"
	@echo "  make cross    compile every platform a release ships"
	@echo "  make install  go install devctl into GOBIN"
	@echo "  make fmt      apply the configured formatters (gofumpt + goimports)"
	@echo "  make fix      apply every automatic fix: go fix, the formatters, --fix linters"
	@echo "  make pre-commit  the fast subset a commit hook runs"
	@echo "  make lint     go vet + golangci-lint over the tree"
	@echo "  make license-check  every commentable tracked file carries its SPDX tag"
	@echo "  make test     go test -race -shuffle=on ./... with coverage"
	@echo "  make race     the multi-process store race gate, verbosely"
	@echo "  make vuln     govulncheck ./..."
	@echo "  make ci       lint + license-check + cross + test + vuln — must be green before a push"
	@echo "  make release  build the release artifacts and attach them to the GitHub release"
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

# Files that cannot carry a tag: go.sum has no comment syntax, and a licence
# text is never edited, which is the one rule every licensing standard agrees on.
LICENSE_EXEMPT := go.sum LICENSE

# Without this the header is aspiration rather than fact: it would vanish from
# the first file added and nobody would notice. Driven by `git ls-files`, so a
# file has to be tracked before it is judged.
#
# Two details it got wrong the first time. The match is anchored to the first
# lines, because both this file and the README mention the tag in their own
# prose and would otherwise vouch for themselves while untagged. And the list is
# read NUL-delimited, because word-splitting `git ls-files` turns a filename
# holding a space into two nonexistent files and reports both as untagged.
license-check:
	@missing=0; \
	while IFS= read -r -d '' f; do \
	  case " $(LICENSE_EXEMPT) " in *" $$f "*) continue;; esac; \
	  head -5 "$$f" | grep -q 'SPDX-License-Identifier:' || { \
	    echo "missing SPDX tag: $$f" >&2; missing=1; }; \
	done < <(git ls-files -z); \
	if [ $$missing -ne 0 ]; then \
	  echo "every tracked file that can hold a comment must carry the SPDX tag" >&2; \
	  exit 1; \
	fi
	@echo "license-check: every commentable tracked file carries its SPDX tag"

vuln:
	govulncheck ./...

ci: lint license-check cross test vuln

# Build the release artifacts and attach them to a release that ALREADY EXISTS.
#
# The division of labour is deliberate. The release itself is created by hand on
# GitHub, because its auto-generated notes are the reason to do it there, and
# this target never touches them. GoReleaser builds and archives, so the artifact
# format keeps ONE definition — .goreleaser.yml — instead of growing a second one
# here that would drift from it; --skip=publish is what keeps GoReleaser away
# from the release itself, and gh uploads what it produced.
#
# --clobber so that a second run replaces the assets instead of refusing. A
# release built twice from one tag is a normal thing to want; a half-uploaded
# one that cannot be repaired is not.
release: release-preflight ci
	@tag=$$(git describe --tags --exact-match); \
	echo "building $$tag"; \
	goreleaser release --clean --skip=publish; \
	echo "uploading to the $$tag release"; \
	gh release upload "$$tag" dist/*.tar.gz dist/checksums.txt --clobber; \
	echo "release: $$tag now carries $$(ls dist/*.tar.gz | wc -l | tr -d ' ') archives and checksums.txt"

# Everything that can refuse a release, and nothing that takes time.
#
# It is a SEPARATE target, and first in release's prerequisite list, so every
# refusal lands in a second. Folded into the recipe these checks would run after
# `ci`, which cross-compiles four platforms, and a release that does not exist
# yet would be reported a minute late every time.
#
# `ci` running at all is not ceremony: a release is the one build nobody
# re-checks afterwards, and while the Release workflow cannot run, this is the
# only gate between a broken tree and a published binary.
release-preflight:
	@command -v goreleaser >/dev/null 2>&1 || { \
	  echo "goreleaser is not installed; run 'make tool-release'" >&2; exit 1; }
	@command -v gh >/dev/null 2>&1 || { \
	  echo "gh is not installed; it is what uploads the artifacts" >&2; exit 1; }
	@tag=$$(git describe --tags --exact-match 2>/dev/null); \
	if [ -z "$$tag" ]; then \
	  echo "HEAD carries no tag: a release is built from the tag it is named after" >&2; \
	  exit 1; \
	fi; \
	if ! gh release view "$$tag" >/dev/null 2>&1; then \
	  echo "no GitHub release for $$tag yet. Create it first, which is where the" >&2; \
	  echo "generated notes come from, then run make release again:" >&2; \
	  echo "    gh release create $$tag --generate-notes" >&2; \
	  exit 1; \
	fi; \
	echo "release-preflight: $$tag is tagged here and has a release to attach to"

# Dev tools are PINNED here and installed with `go install`, not carried as
# go.mod `tool` directives: golangci-lint and goreleaser each drag a module graph
# far larger than devctl's own into go.sum, which every `go mod download` in
# every CI job would then pay for. This file is the single version of truth, and
# `make tool-*` is what CI runs, so CI and a laptop lint with the same binary.
GOLANGCI_VERSION    ?= v2.13.2
GOVULNCHECK_VERSION ?= v1.8.0
# Pinned to the same major the Release workflow asks for ("~> v2"), so an artifact
# built here and one built by the workflow come from the same generation of the
# tool. Deliberately NOT part of `tools`: CI never needs it, and `go install`ing
# it costs minutes.
GORELEASER_VERSION  ?= v2.12.7

tools: tool-lint tool-vuln

tool-lint:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)

tool-vuln:
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

tool-release:
	go install github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)

clean:
	rm -rf bin dist coverage.out coverage.html
