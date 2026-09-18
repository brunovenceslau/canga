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
	# script carry on. awk compares the filename field for equality; an asset
	# missing from checksums.txt yields no line, and sha256sum refuses empty
	# input.
	(cd "$work" && awk -v a="$asset" '$2 == a' checksums.txt | sha256sum -c -)

	tar -xzf "$work/$asset" -C "$work" canga
	install -m 0755 "$work/canga" "$dir/canga"
	"$dir/canga" --version
}

main "$@"
