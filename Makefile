# SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
# SPDX-License-Identifier: GPL-3.0-or-later

# Makefile — the repo's quality gates.
#
# Every gate lands here FIRST and CI (.github/workflows/) only invokes a target;
# a check that exists solely in a workflow file cannot be run before a push, so
# it is not a gate, it is a surprise. `make ci` is what must be green locally.

SHELL := /bin/bash

# One binary, canga, built in two roles from two main packages: cmd/host/canga
# (everything) and cmd/sandbox/canga (what an agent may run). Both are built,
# cross-compiled and released together; the sandbox role ships for linux only
# (see .goreleaser.yml). Each lands in bin/<role>/canga, because they share a
# name and would otherwise overwrite each other.
ROLES    := host sandbox
# Injected into main.version by ldflags. A release overrides it from the tag;
# a local build reports the git description so `canga --version` never lies about
# which tree it came from. `canga upgrade` compares a release against this
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
SANDBOX_PLATFORMS ?= linux/arm64 linux/amd64

# Prerequisite order is load-bearing in this file, and `make -j` does not keep
# it: GNU make only promises left-to-right processing in serial mode. `test`
# and `race` both run `go test -race` over the same packages, which is no
# faster in parallel. Scoping this to one target needs make 4.4; macOS ships
# 3.81.
.NOTPARALLEL:

.DEFAULT_GOAL := help
.PHONY: help build cross install fmt fix pre-commit lint license-check test race vuln ci release-preflight release-kit-bump tools tool-lint tool-vuln clean

help:
	@echo "Targets:"
	@echo "  make build    build bin/host/canga and bin/sandbox/canga with version/commit stamped in"
	@echo "  make cross    compile every binary for every platform a release ships"
	@echo "  make install  go install the host build into GOBIN (the sandbox build belongs in a sandbox)"
	@echo "  make fmt      apply the configured formatters (gofumpt + goimports)"
	@echo "  make fix      apply every automatic fix: go fix, the formatters, --fix linters"
	@echo "  make pre-commit  the fast subset a commit hook runs"
	@echo "  make lint     go vet + golangci-lint over the tree"
	@echo "  make license-check  every commentable tracked file carries its SPDX tag"
	@echo "  make test     go test -race -shuffle=on ./... with coverage"
	@echo "  make race     the multi-process store race gate, verbosely"
	@echo "  make vuln     govulncheck ./..."
	@echo "  make ci       lint + license-check + cross + test + vuln — must be green before a push"
	@echo "  make release-preflight  the checks to run on the signed tag at HEAD before pushing it"
	@echo "  make release-kit-bump TAG=vX.Y.Z  commit the sbx kit pin for a published release, on a branch"
	@echo "  make tools    install the pinned dev tools into GOBIN"

build:
	@set -e; for role in $(ROLES); do \
	  echo "go build -o bin/$$role/canga ./cmd/$$role/canga"; \
	  go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$$role/canga ./cmd/$$role/canga; \
	done

# CGO_ENABLED=0 matches what .goreleaser.yml sets, so this compiles the way a
# release does rather than however the host happens to be configured. The output
# goes nowhere: the question is whether it builds, not what it produces.
cross:
	@set -e; build() { \
	  goos=$${2%/*}; goarch=$${2#*/}; \
	  echo "GOOS=$$goos GOARCH=$$goarch go build ./cmd/$$1/canga"; \
	  CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch \
	    go build -trimpath -ldflags '$(LDFLAGS)' -o /dev/null ./cmd/$$1/canga; \
	}; \
	for platform in $(PLATFORMS); do build host $$platform; done; \
	for platform in $(SANDBOX_PLATFORMS); do build sandbox $$platform; done

install:
	go install -trimpath -ldflags '$(LDFLAGS)' ./cmd/host/canga

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

# Everything that can refuse a release, checked on the signed tag at HEAD
# BEFORE it is pushed: each check is a local git command or one GitHub query,
# never a build, so a refusal costs a second instead of a wasted Release run.
#
# HEAD carrying more than one tag has no single answer to "which release is
# this": GITHUB_REF_NAME on push names whichever ref triggered the workflow,
# so a second tag on the same commit is not a risk the workflow itself
# catches. Both checks below are the ones the Release workflow repeats for
# itself once the tag is pushed (.github/workflows/release.yml), so a release
# that would fail in CI is caught here first, before it costs a run.
#
# check-previous refuses until the kit at HEAD pins the newest published
# release below this tag, which is to say until the previous release's kit
# bump merged: the kit cannot name this tag, whose archives do not exist yet.
# check-clobber refuses to rebuild a tag whose archives a pushed or merged kit
# bump already pins, unless SBX_KIT_ALLOW_CLOBBER names that exact tag:
# rebuilt archives get new hashes, and every sandbox pinned to the old ones
# stops installing. A tag below the newest published release is refused
# unless SBX_KIT_OLDER_LINE names it. scripts/sbx-kit-pin.sh has the checks,
# and README "Move the sbx kit's pin" the reasons.
release-preflight:
	@set -e; \
	tags=$$(git tag --points-at HEAD); \
	count=$$(printf '%s' "$$tags" | grep -c . || true); \
	if [ "$$count" -eq 0 ]; then \
	  echo "HEAD carries no tag: create the signed tag first (README \"Releasing\")" >&2; \
	  exit 1; \
	fi; \
	if [ "$$count" -gt 1 ]; then \
	  echo "HEAD carries $$count tags, so which release this is has no answer:" >&2; \
	  printf '    %s\n' $$tags >&2; \
	  echo "delete the tag you are not releasing before pushing." >&2; \
	  exit 1; \
	fi; \
	tag=$$(printf '%s' "$$tags"); \
	scripts/sbx-kit-pin.sh check-previous "$$tag"; \
	scripts/sbx-kit-pin.sh check-clobber "$$tag"; \
	echo "release-preflight: $$tag is the only tag here, the sbx kit pins the release before it, and no kit pins this one yet"

# override REPO_SLUG: `make release-kit-bump REPO_SLUG=x` cannot change it,
# so $(REPO_SLUG) in the recipe stays as safe as the literal. This is the
# only make variable spliced into the recipe text; TAG differs because it
# legitimately varies per call, and is read from the shell's $$TAG instead
# (see the guard below). `override` only refuses an ordinary
# `REPO_SLUG=x` argument - a caller with `-f other.mk` or `--eval` could
# still redefine it, but a caller who can hand make its own makefile or
# --eval already has arbitrary shell execution, which this is not a defense
# against.
override REPO_SLUG := brunovenceslau/canga

# Moves the sbx kit's pin to a release the Release workflow already
# published, as a signed commit on chore/sbx-kit-<tag>, via
# scripts/sbx-kit-pin.sh bump run from a temporary detached worktree at the
# tag. README "Move the sbx kit's pin" has the recipe and the reasons;
# docs/HANDOFF.md ("PR #34") has the security history behind each invariant
# below (the TAG injection this target used to carry, the fetch refspec, the
# worktree, and REPO_SLUG).
#
# Invariants a maintainer changing this recipe must keep, in the order this
# recipe actually runs them:
# - The guard below refuses anything but a single line, at most 64
#   characters, matching ^vX.Y.Z$, before gh, git or mktemp run: a
#   line-oriented `grep -Eq` alone accepts a multi-line value whose first
#   line matches, and an unbounded value fails closed in git rather than in
#   this guard.
#   TAG is read only as the shell's $$TAG here and below, never make's
#   $(TAG): make splices $(TAG) into the recipe as raw, unquoted text before
#   the shell parses it.
# - GH_HOST is pinned to github.com before the first gh call, so a stray
#   GH_HOST in the caller's environment cannot redirect gh at another host.
# - The tag as fetched into this checkout is cross-checked against the
#   commit GitHub's API resolves it to, before any of it is trusted; the
#   fetch itself names an explicit refspec after `--` as defense in depth,
#   redundant with the guard above once it has run (nothing $$tag could
#   hold is then a fetch option in the first place).
# - Only once that cross-check passes: the temporary worktree is checked out
#   at the tag's own VERIFIED COMMIT sha ($$here), never the tag name, so it
#   cannot silently re-resolve to a different commit than the one just
#   checked.
# - This target then runs the TAG'S OWN copy of scripts/sbx-kit-pin.sh, from
#   inside that worktree, not the invoking checkout's: a tag cut before the
#   script existed is refused rather than silently run with a newer copy,
#   and a tag whose own copy predates later hardening does not gain it when
#   re-bumped today.
#
# See docs/HANDOFF.md ("PR #34") for the security history behind each of
# these, and README "Move the sbx kit's pin" for the recipe in plain prose.
release-kit-bump:
	@tag=$${TAG:-}; \
	lines=$$(printf '%s' "$$tag" | wc -l); \
	if [ "$$lines" -ne 0 ] || [ "$${#tag}" -gt 64 ] || ! printf '%s' "$$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$$'; then \
	  echo "usage: make release-kit-bump TAG=vX.Y.Z (got TAG='$$tag')" >&2; exit 1; \
	fi
	@command -v gh >/dev/null 2>&1 || { \
	  echo "gh is not installed; it is what downloads the release's assets" >&2; exit 1; }
	@set -e; \
	export GH_HOST=github.com; \
	tag="$$TAG"; \
	assets=$$(mktemp -d); \
	wtparent=$$(mktemp -d); \
	wt="$$wtparent/tag"; \
	cleanup() { \
	  git worktree remove --force "$$wt" >/dev/null 2>&1 || true; \
	  rm -rf "$$assets" "$$wtparent"; \
	}; \
	trap cleanup EXIT; \
	trap 'cleanup; exit 129' HUP; \
	trap 'cleanup; exit 130' INT; \
	trap 'cleanup; exit 143' TERM; \
	echo "fetching tag $$tag"; \
	git fetch -q origin -- "refs/tags/$$tag:refs/tags/$$tag"; \
	here=$$(git rev-parse "refs/tags/$$tag^{commit}"); \
	obj=$$(gh api "repos/$(REPO_SLUG)/git/ref/tags/$$tag" --jq '"\(.object.type) \(.object.sha)"'); \
	obj_type=$${obj%% *}; \
	obj_sha=$${obj#* }; \
	if [ "$$obj_type" = tag ]; then \
	  there=$$(gh api "repos/$(REPO_SLUG)/git/tags/$$obj_sha" --jq '.object.sha'); \
	else \
	  there="$$obj_sha"; \
	fi; \
	if [ "$$here" != "$$there" ]; then \
	  echo "$$tag is $$here in this checkout, but GitHub resolves it to $$there." >&2; \
	  echo "origin and $(REPO_SLUG) disagree about what this tag is; refusing." >&2; \
	  exit 1; \
	fi; \
	echo "downloading $$tag's checksums.txt and canga-sandbox_ archives"; \
	gh release download "$$tag" -R "$(REPO_SLUG)" -D "$$assets" \
	  -p checksums.txt -p 'canga-sandbox_*'; \
	git worktree add -q --detach "$$wt" "$$here"; \
	if [ ! -x "$$wt/scripts/sbx-kit-pin.sh" ]; then \
	  echo "$$tag predates scripts/sbx-kit-pin.sh (or it is not executable there);" >&2; \
	  echo "this target cannot bump a release from before the script existed." >&2; \
	  exit 1; \
	fi; \
	( cd "$$wt" && ./scripts/sbx-kit-pin.sh bump "$$tag" "$$assets/checksums.txt" )

# Dev tools are PINNED here and installed with `go install`, not carried as
# go.mod `tool` directives: golangci-lint drags a module graph far larger than
# this module's own into go.sum, which every `go mod download` in every CI job
# would then pay for. This file is the single version of truth, and
# `make tool-*` is what CI runs, so CI and a laptop lint with the same binary.
#
# GoReleaser itself is not installed here: nothing builds a release locally
# any more (the Release workflow does, pinning its own GoReleaser version via
# goreleaser-action), and release-kit-bump only downloads that workflow's
# artifacts, never builds them.
GOLANGCI_VERSION    ?= v2.13.2
GOVULNCHECK_VERSION ?= v1.8.0

tools: tool-lint tool-vuln

tool-lint:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)

tool-vuln:
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

clean:
	rm -rf bin dist coverage.out coverage.html
