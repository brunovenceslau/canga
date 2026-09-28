#!/bin/sh
# SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
# SPDX-License-Identifier: GPL-3.0-or-later

# Install the host build of canga into ~/.local/bin, verified against the
# release's checksums.txt:
#
#   curl -fsSL https://raw.githubusercontent.com/brunovenceslau/canga/main/install_host.sh | sh
#
# The newest release by default. Pin one by passing its tag:
#
#   curl -fsSL .../install_host.sh | sh -s -- v0.5.0
#
# Run it once. From then on `canga upgrade` replaces the binary.

# Everything runs from main, called on the last line: a download cut off
# halfway defines a function and runs nothing, instead of running half a script.
main() {
	set -eu

	releases=https://github.com/brunovenceslau/canga/releases
	dir="$HOME/.local/bin"

	# macOS ships shasum and no sha256sum; a Mac with coreutils has both.
	if command -v sha256sum >/dev/null 2>&1; then
		sha256="sha256sum"
	elif command -v shasum >/dev/null 2>&1; then
		sha256="shasum -a 256"
	else
		echo "canga: neither sha256sum nor shasum is installed" >&2
		exit 1
	fi
	for tool in curl tar awk uname mktemp; do
		if ! command -v "$tool" >/dev/null 2>&1; then
			echo "canga: $tool is not installed" >&2
			exit 1
		fi
	done

	case "$(uname -s)" in
	Darwin) os=darwin ;;
	Linux)
		# The host build is published for macOS only: Linux here only ever
		# runs the sandbox build, which has its own installer.
		echo "canga: the host build is published for macOS only, not Linux; in a sandbox, use install_sandbox.sh" >&2
		exit 1
		;;
	*)
		echo "canga: unsupported system $(uname -s)" >&2
		exit 1
		;;
	esac
	case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*)
		echo "canga: unsupported architecture $(uname -m)" >&2
		exit 1
		;;
	esac

	if [ $# -gt 0 ]; then
		tag=$1
	else
		# /releases/latest redirects to the newest release's page, so the URL
		# curl ends on names its tag. -L is what makes curl follow it: without
		# -L the effective URL is /releases/latest itself, and the tag comes out
		# as "latest".
		url=$(curl -fsSL -o /dev/null -w '%{url_effective}' "$releases/latest")
		tag=${url##*/}
	fi
	case "$tag" in
	v[0-9]*) ;;
	*)
		echo "canga: \"$tag\" is not a release tag (vX.Y.Z)" >&2
		exit 1
		;;
	esac

	# The oldest release this script installs, whether $tag came from the
	# command line or from following /releases/latest above: every release
	# below it was deleted for carrying no build provenance attestation, tags
	# kept, and could be recreated by anyone with write access, with bytes
	# and a checksums.txt of their own choosing (docs/HANDOFF.md,
	# "Immutability is not retroactive"). install_sandbox.sh and
	# internal/upgrade carry the same value (min_version, MinReleaseTag);
	# TestReleaseFloorMatchesAcrossInstallersAndCanga (release_floor_test.go)
	# keeps the three from drifting apart.
	min_version=0.10.5

	# Two checks in one awk, because both are about $tag's SHAPE before
	# comparing it to the floor at all: exactly three dot-separated fields,
	# none with a leading zero (rejects "v00.10.5"), and nothing else after
	# them (rejects a pre-release like "v0.10.5-rc1" or build metadata like
	# "v0.10.5+build" - the "v[0-9]*" case above lets both through, and
	# "v0.10.5-rc1" in particular would otherwise compare EQUAL to the floor
	# below: awk's `+ 0` reads only the digits up to the first non-digit, so
	# "5-rc1" and "5" coerce to the same number). Only once the shape is
	# confirmed are the three fields compared as numbers, never as text:
	# v0.9.10 is above v0.9.9.
	#
	# set +e/-e around the call: awk's exit status has to be inspected (2 for
	# a bad shape, 1 for below the floor, 0 for accepted), and under `set -e`
	# a bare command failing aborts the script before that inspection ever
	# runs; an `if` statement's own condition is the one context set -e
	# already exempts, but that only gets the pass/fail, not which of the two
	# refusals it was.
	set +e
	awk -v got="${tag#v}" -v want="$min_version" 'BEGIN {
		n = split(got, g, ".")
		shape = (n == 3)
		if (shape) {
			for (i = 1; i <= 3; i++) if (g[i] !~ /^(0|[1-9][0-9]*)$/) shape = 0
		}
		if (!shape) exit 2
		split(want, w, ".")
		for (i = 1; i <= 3; i++) {
			gi = g[i] + 0
			wi = w[i] + 0
			if (gi != wi) exit (gi < wi)
		}
		exit 0
	}'
	floor_status=$?
	set -e

	case "$floor_status" in
	0) ;;
	2)
		echo "canga: \"$tag\" is not vX.Y.Z (no pre-release, no build metadata, no" >&2
		echo "canga: leading zeros); refusing to compare it against v$min_version" >&2
		exit 1
		;;
	*)
		echo "canga: \"$tag\" is older than v$min_version, the first release with a build" >&2
		echo "canga: provenance attestation; refusing to install a release that could have" >&2
		echo "canga: been recreated with unverified bytes" >&2
		exit 1
		;;
	esac

	asset="canga-host_${tag#v}_${os}_${arch}.tar.gz"
	echo "canga: installing $tag ($asset) into $dir" >&2

	work=$(mktemp -d)
	trap 'rm -rf "$work"' EXIT

	curl -fsSL -o "$work/$asset" "$releases/download/$tag/$asset"
	curl -fsSL -o "$work/checksums.txt" "$releases/download/$tag/checksums.txt"

	# The checksum is the gate, so it is a step of its own: under set -e a
	# failing `check && extract` would only skip the extraction and let the
	# script carry on. awk compares the filename field for equality (grep would
	# read the dots as wildcards); an asset missing from checksums.txt yields no
	# line, and the checker refuses empty input.
	(cd "$work" && awk -v a="$asset" '$2 == a' checksums.txt | $sha256 -c -)

	mkdir -p "$dir"
	# The archive also carries LICENSE and README.md; naming canga extracts the
	# binary alone.
	tar -xzf "$work/$asset" -C "$dir" canga
	"$dir/canga" --version

	case ":$PATH:" in
	*":$dir:"*) ;;
	*) echo "canga: $dir is not on your PATH; add it to run canga by name" >&2 ;;
	esac
}

main "$@"
