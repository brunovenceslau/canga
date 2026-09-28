#!/bin/sh
# SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
# SPDX-License-Identifier: GPL-3.0-or-later

# ci-tools.sh - install the pinned shellcheck and jq the tests shell out to,
# for whichever of the four CI platforms this runs on, into <dir>:
#
#   scripts/ci-tools.sh "$RUNNER_TEMP/bin"
#
# Pinned by version and sha256, never taken from the runner image, whose copy
# changes with the image and would change a verdict with it. The digests are
# the ones GitHub serves for each release asset (and, for jq, the release's
# own sha256sum.txt agrees), recorded here on 2026-09-28.
#
# zsh is not pinned: TestCompletionScript only parses with `zsh -n`. macOS
# ships it; on Linux it is installed from the distribution when absent.

main() {
	set -eu

	if [ $# -ne 1 ]; then
		echo "usage: ci-tools.sh <install dir>" >&2
		exit 2
	fi

	dir=$1
	mkdir -p "$dir"
	work=$(mktemp -d)
	trap 'rm -rf "$work"' EXIT

	case "$(uname -s)/$(uname -m)" in
	Linux/x86_64)
		sc_platform=linux.x86_64 sc_sha=8c3be12b05d5c177a04c29e3c78ce89ac86f1595681cab149b65b97c4e227198
		jq_platform=linux-amd64 jq_sha=020468de7539ce70ef1bceaf7cde2e8c4f2ca6c3afb84642aabc5c97d9fc2a0d
		;;
	Linux/aarch64)
		sc_platform=linux.aarch64 sc_sha=12b331c1d2db6b9eb13cfca64306b1b157a86eb69db83023e261eaa7e7c14588
		jq_platform=linux-arm64 jq_sha=6bc62f25981328edd3cfcfe6fe51b073f2d7e7710d7ef7fcdac28d4e384fc3d4
		;;
	Darwin/x86_64)
		sc_platform=darwin.x86_64 sc_sha=3c89db4edcab7cf1c27bff178882e0f6f27f7afdf54e859fa041fca10febe4c6
		jq_platform=macos-amd64 jq_sha=e80dbe0d2a2597e3c11c404f03337b981d74b4a8504b70586c354b7697a7c27f
		;;
	Darwin/arm64)
		sc_platform=darwin.aarch64 sc_sha=56affdd8de5527894dca6dc3d7e0a99a873b0f004d7aabc30ae407d3f48b0a79
		jq_platform=macos-arm64 jq_sha=a9fe3ea2f86dfc72f6728417521ec9067b343277152b114f4e98d8cb0e263603
		;;
	*)
		echo "ci-tools: no pinned tools for $(uname -s)/$(uname -m)" >&2
		exit 1
		;;
	esac

	shellcheck_version=v0.11.0
	jq_version=1.8.1

	archive="shellcheck-${shellcheck_version}.${sc_platform}.tar.xz"
	fetch "https://github.com/koalaman/shellcheck/releases/download/${shellcheck_version}/${archive}" \
		"$work/$archive" "$sc_sha"
	tar -xJf "$work/$archive" -C "$work"
	install -m 0755 "$work/shellcheck-${shellcheck_version}/shellcheck" "$dir/shellcheck"

	fetch "https://github.com/jqlang/jq/releases/download/jq-${jq_version}/jq-${jq_platform}" \
		"$work/jq" "$jq_sha"
	install -m 0755 "$work/jq" "$dir/jq"

	"$dir/shellcheck" --version
	"$dir/jq" --version

	if ! command -v zsh >/dev/null 2>&1; then
		if [ "$(uname -s)" != Linux ]; then
			echo "ci-tools: zsh is missing and only installed on Linux here" >&2
			exit 1
		fi

		sudo apt-get update
		sudo apt-get install -y --no-install-recommends zsh
	fi

	zsh --version
}

# fetch downloads url to path and refuses it unless its sha256 is want.
fetch() {
	curl -fsSL -o "$2" "$1"

	if command -v sha256sum >/dev/null 2>&1; then
		got=$(sha256sum "$2")
	else
		got=$(shasum -a 256 "$2")
	fi

	got=${got%% *}
	if [ "$got" != "$3" ]; then
		echo "ci-tools: $1: sha256 $got, want $3" >&2
		exit 1
	fi
}

main "$@"
