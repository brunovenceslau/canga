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

	# macOS ships shasum and no sha256sum; most Linux systems ship the reverse.
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
	Linux) os=linux ;;
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
