#!/bin/sh
# SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
# SPDX-License-Identifier: GPL-3.0-or-later

# Install the sandbox build of canga into /usr/local/bin, verified against the
# release's checksums.txt. Meant for a sandbox's provisioning step, run as root:
#
#   curl -fsSL https://raw.githubusercontent.com/brunovenceslau/canga/main/install_sandbox.sh | sh -s -- v0.5.0
#
# The tag is required. A sandbox pins its release rather than following the
# newest, so every sandbox built from one definition runs the same binary.

# Everything runs from main, called on the last line: a download cut off
# halfway defines a function and runs nothing, instead of running half a script.
main() {
	set -eu

	releases=https://github.com/brunovenceslau/canga/releases
	# /usr/local/bin, root-owned: on PATH for every user, and out of reach of a
	# process without root. That is no boundary against an agent that has sudo,
	# which a Docker Sandbox grants its agent user.
	dir=/usr/local/bin

	if [ $# -ne 1 ]; then
		echo "usage: install_sandbox.sh vX.Y.Z" >&2
		exit 2
	fi
	tag=$1
	case "$tag" in
	v[0-9]*) ;;
	*)
		echo "canga: \"$tag\" is not a release tag (vX.Y.Z)" >&2
		exit 2
		;;
	esac

	for tool in curl sha256sum tar awk install uname mktemp id; do
		if ! command -v "$tool" >/dev/null 2>&1; then
			echo "canga: $tool is not installed" >&2
			exit 1
		fi
	done

	# The oldest release this script installs: every release below it was
	# deleted for carrying no build provenance attestation, tags kept, and
	# could be recreated by anyone with write access, with bytes and a
	# checksums.txt of their own choosing (docs/HANDOFF.md, "Immutability is
	# not retroactive"). install_host.sh and internal/upgrade carry the same
	# value (min_version, MinReleaseTag); TestReleaseFloorMatchesAcrossInstallersAndCanga
	# (release_floor_test.go) keeps the three from drifting apart.
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

	# Said up front: otherwise the download runs first and `install` fails at
	# the end with a permission error that does not say why.
	if [ "$(id -u)" -ne 0 ]; then
		echo "canga: run this as root; it installs into $dir" >&2
		exit 1
	fi

	# Releases ship the sandbox build for linux only: sandboxes are linux VMs.
	if [ "$(uname -s)" != Linux ]; then
		echo "canga: the sandbox build is published for linux only, not $(uname -s)" >&2
		exit 1
	fi
	case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*)
		echo "canga: unsupported architecture $(uname -m)" >&2
		exit 1
		;;
	esac

	asset="canga-sandbox_${tag#v}_linux_${arch}.tar.gz"
	echo "canga: installing $tag ($asset) into $dir" >&2

	work=$(mktemp -d)
	trap 'rm -rf "$work"' EXIT

	curl -fsSL -o "$work/$asset" "$releases/download/$tag/$asset"
	curl -fsSL -o "$work/checksums.txt" "$releases/download/$tag/checksums.txt"

	# The checksum is the gate, so it is a step of its own: under set -e a
	# failing `check && extract` would only skip the extraction and let the
	# script carry on. awk compares the filename field for equality.
	#
	# The script, not the checker, refuses an asset checksums.txt does not
	# list exactly once. A checker handed no line may pass (the sha256sum on
	# the macOS CI runners exits 0 on empty input, where GNU's refuses it, and
	# nothing guarantees a sandbox's checker is GNU's), which would install an
	# unverified archive. And one handed the same line twice
	# checks it twice, so exactly one line is what reaches the checker.
	line=$(awk -v a="$asset" '$2 == a' "$work/checksums.txt")
	count=$(printf '%s\n' "$line" | awk 'NF { n++ } END { print n + 0 }')
	if [ "$count" -ne 1 ]; then
		echo "canga: checksums.txt lists $asset $count times, not exactly once; refusing to install it" >&2
		exit 1
	fi
	(cd "$work" && printf '%s\n' "$line" | sha256sum -c -)

	tar -xzf "$work/$asset" -C "$work" canga
	install -m 0755 "$work/canga" "$dir/canga"
	"$dir/canga" --version
}

main "$@"
