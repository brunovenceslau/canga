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

# Fixed, not user-overridable: `override` refuses even a caller's own
# `make release-kit-bump REPO_SLUG=x`, so $(REPO_SLUG) below is exactly as
# safe as writing the literal at each call site - it can never carry
# attacker-chosen text the way TAG can. That is also why this is the only
# make variable spliced into this recipe: a value that legitimately VARIES
# per invocation (TAG) is read from the shell's $$TAG instead, never from
# make's own $(TAG); see the guard below for why that split matters.
override REPO_SLUG := brunovenceslau/canga

# Move the sbx kit's pin to a release the Release workflow already published,
# as a signed commit on a branch of its own (chore/sbx-kit-<tag>), for a pull
# request. README "Move the sbx kit's pin" has the reasons; scripts/sbx-kit-pin.sh
# `bump` does the actual checks and the commit - this target's job is only to
# get it a clean HEAD to run from.
#
# TAG is user input, referenced only as the shell variable "$$TAG", never as
# make's own $(TAG): make splices $(TAG) into the recipe as raw, unquoted
# text before the shell parses anything, so a value like
# `v1'; rm -rf /; echo '` runs as shell no matter how it looks quoted. $$TAG
# is an ordinary shell expansion instead, so the shell only ever sees data.
# The guard below requires a SINGLE line matching ^vX.Y.Z$: a line-oriented
# `grep -Eq` alone would accept a value whose first line looks right and
# whose second line does not, since grep succeeds as soon as ANY line
# matches - `wc -l` on the unterminated value catches that by requiring zero
# embedded newlines. `make TARGET TAG=v1.2.3` and `TAG=v1.2.3 make TARGET`
# both land in the recipe's $$TAG the same way, so the guard covers both
# without an `export TAG` directive, and runs before anything else (gh, git,
# mktemp - all of it).
#
# `bump` needs a HEAD carrying exactly the tag being bumped, which the
# operator's own checkout rarely is (tagging happens on main; the release is
# built later, by CI). This target gets there itself: a temporary DETACHED
# worktree checked out at the tag's own VERIFIED COMMIT - never `git
# checkout` (leaves the invoking checkout untouched), and never the tag NAME
# (so it cannot silently re-resolve to a different commit than the one just
# cross-checked below). That worktree sits beside the one `bump` itself
# makes internally to hold the commit. It also runs scripts/sbx-kit-pin.sh
# FROM that worktree, not the invoking checkout's copy: the tag's own,
# reviewed script judges the tag's own release. A tag cut before the script
# existed has none to run, and is refused rather than silently falling back
# to a newer copy - which also means a tag whose OWN script copy predates
# some later hardening never gains it, even when re-bumped today; only a
# new release ships with the newer script.
#
# Checksums and archives come from the tag's GitHub release, not a local
# `dist/`: nothing builds them locally any more, and the Release workflow's
# artifacts are the only ones a sandbox ever installs. Both downloads share
# one scratch directory because `bump` checks the archives against
# checksums.txt and against GitHub's served digests, expecting them side by
# side.
#
# Before any of that is trusted, the tag as fetched into this checkout - by
# an explicit refspec after `--`, so nothing $$tag could hold is ever read
# as a fetch option - is compared against the commit GitHub itself resolves
# it to (dereferencing an annotated tag object through /git/tags/<sha>,
# since /git/ref/tags/<tag> names the tag object, not its commit). A
# mismatch means this checkout's `origin` and the canonical repository
# disagree about what the tag is - exactly the situation the pin must never
# be built from.
release-kit-bump:
	@tag=$${TAG:-}; \
	lines=$$(printf '%s' "$$tag" | wc -l); \
	if [ "$$lines" -ne 0 ] || ! printf '%s' "$$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$$'; then \
	  echo "usage: make release-kit-bump TAG=vX.Y.Z (got TAG='$$tag')" >&2; exit 1; \
	fi
	@command -v gh >/dev/null 2>&1 || { \
	  echo "gh is not installed; it is what downloads the release's assets" >&2; exit 1; }
	@set -e; \
	tag="$$TAG"; \
	assets=$$(mktemp -d); \
	wtparent=$$(mktemp -d); \
	wt="$$wtparent/tag"; \
	cleanup() { \
	  git worktree remove --force "$$wt" >/dev/null 2>&1 || true; \
	  rm -rf "$$assets" "$$wtparent"; \
	}; \
	trap cleanup EXIT; \
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
	gh release download "$$tag" -R $(REPO_SLUG) -D "$$assets" \
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
