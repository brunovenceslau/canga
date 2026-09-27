#!/bin/sh
# SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
# SPDX-License-Identifier: GPL-3.0-or-later

# sbx-kit-pin.sh - the one writer and the one reader of the release pin in
# sbx-kit/spec.yaml: CANGA_VERSION and the per-arch sha256 of the two
# canga-sandbox_ archives. README "Move the sbx kit's pin" has the why.
#
# Usage, from the repository root:
#   sbx-kit-pin.sh version                    print the kit's CANGA_VERSION
#   sbx-kit-pin.sh rewrite X.Y.Z <checksums>  rewrite the pin in the working
#                                             tree, refusing unless the sums
#                                             match release vX.Y.Z's published
#                                             digests, uploaded by a workflow
#                                             run in this repository (see
#                                             check_published below for what
#                                             that does and does not prove)
#   sbx-kit-pin.sh check-previous <tag>       refuse unless HEAD's kit pins
#                                             the newest published release
#                                             below <tag>, by its published
#                                             digests
#   sbx-kit-pin.sh check-clobber <tag>        refuse to rebuild <tag>'s
#                                             sandbox archives once its kit
#                                             bump is pushed or merged
#                                             (SBX_KIT_ALLOW_CLOBBER=<tag>
#                                             overrides, for that tag only)
#   sbx-kit-pin.sh bump <tag> <checksums>     commit the pin for <tag>, signed,
#                                             on branch chore/sbx-kit-<tag>
#
# A <tag> below the newest published release (a release on an older line) is
# refused by check-previous and bump unless SBX_KIT_OLDER_LINE=<tag>: the kit
# follows the newest line and never moves backwards, so such a release is a
# decision, not a default.
#
# gh is always pointed at brunovenceslau/canga on github.com (see repo=
# below and GH_HOST=github.com below that), the repository the kit downloads
# from, never at the checkout's remotes or a stray GH_HOST in the caller's
# environment.
#
# check_published (used by rewrite, check-previous and bump) also requires
# each canga-sandbox_ asset's uploader to be github-actions[bot]: GitHub
# reports every default-GITHUB_TOKEN upload under that one identity, so this
# proves the asset was uploaded by SOME workflow run in this repository (the
# repos/${repo}/... path scopes which repository), not specifically by the
# Release workflow - a different workflow in this repository granted
# `contents: write` could produce the same identity. What this closes: a
# NEW asset upload under a person's own token (`gh release upload
# --clobber`) is refused, digest match or not, because that upload's
# uploader is no longer github-actions[bot]. What it does not close: a
# metadata-only edit - the release's title or notes, or an asset's label
# or name - that never re-uploads the asset leaves its uploader field
# untouched and is not caught here. A rename still cannot swap archives:
# it only moves bytes already uploaded, and the digest check refuses
# them under the wrong name. Binding assets to release.yml specifically
# (build provenance attestation) is docs/HANDOFF.md's pending item 3.
# check_published also refuses a draft or a prerelease outright, and refuses
# an asset name it cannot match exactly (see its own comment for both).
#
# check-previous, check-clobber and bump read the kit as committed (at HEAD,
# or at origin's main), never the working tree, so a local edit, an
# assume-unchanged or a skip-worktree flag cannot change their answer.
#
# POSIX sh and POSIX awk only: it runs on the macs `make release-kit-bump` and
# `make release-preflight` run on, whose awk has no {n} interval expressions,
# so hex is checked by grep -E.

set -eu

# Pinned so a stray GH_HOST in the calling environment cannot silently
# redirect gh at an enterprise host: every gh call in this script is about
# brunovenceslau/canga on github.com, never wherever GH_HOST happened to
# point before this line ran.
GH_HOST=github.com
export GH_HOST

spec="sbx-kit/spec.yaml"
# The same repository the kit's install step downloads from.
repo="brunovenceslau/canga"

die() {
	echo "sbx-kit-pin: $*" >&2
	exit 1
}

# trap_cleanup_file/trap_cleanup_dir <path> remove <path> (a file, or a
# directory tree) on a normal exit AND on INT, TERM and HUP too: a bare
# `trap ... EXIT` does not fire for those signals in every /bin/sh (dash
# included), so a killed run (Ctrl-C, or a TERM from a job scheduler) can
# leave a scratch directory or a temp file behind. exit 128+signum is the
# portable way to report a signal death from a shell without re-raising it.
#
# ${target} is assigned before any trap is set, and every trap below is
# single-quoted with ${target} left unexpanded inside it (the pattern a
# shell linter's SC2064 check asks for): the variable is captured once,
# into a name these functions own, and expands only when the trap actually
# fires, not when trap_cleanup_file/trap_cleanup_dir themselves run.
trap_cleanup_file() {
	target="$1"
	trap 'rm -f "${target}"' EXIT
	trap 'rm -f "${target}"; exit 129' HUP
	trap 'rm -f "${target}"; exit 130' INT
	trap 'rm -f "${target}"; exit 143' TERM
}

trap_cleanup_dir() {
	target="$1"
	trap 'rm -rf "${target}"' EXIT
	trap 'rm -rf "${target}"; exit 129' HUP
	trap 'rm -rf "${target}"; exit 130' INT
	trap 'rm -rf "${target}"; exit 143' TERM
}

# trap_clear cancels every trap trap_cleanup_file/trap_cleanup_dir
# installed, once the thing it guarded has been moved or committed into
# place and no longer needs removing on the way out.
trap_clear() {
	trap - EXIT HUP INT TERM
}

# is_version X.Y.Z: a release version as the kit and the archive names spell
# it, without the tag's "v". One line, digits and dots only, no leading zeros.
is_version() {
	case "$1" in
	'' | *[!0-9.]*) return 1 ;;
	esac
	printf '%s\n' "$1" | grep -Eqx '(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)'
}

# tag_version vX.Y.Z prints X.Y.Z, and refuses anything else.
tag_version() {
	case "$1" in
	v*) is_version "${1#v}" && printf '%s\n' "${1#v}" && return 0 ;;
	esac
	die "not a release tag: '$1' (want vX.Y.Z)"
}

# sha256_of <file> prints its sha256, with whichever tool this machine has.
sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{ print $1 }'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{ print $1 }'
	else
		die "neither sha256sum nor shasum is installed"
	fi
}

# spec_version <file> prints the kit's CANGA_VERSION, refusing a kit that
# does not carry exactly one well-formed one.
spec_version() {
	lines=$(grep -Ec '^[[:space:]]*CANGA_VERSION="[^"]*"$' "$1" || true)
	[ "${lines}" = 1 ] || die "${spec}: want exactly one CANGA_VERSION=\"X.Y.Z\" line, found ${lines}"
	v=$(sed -n 's/^[[:space:]]*CANGA_VERSION="\([^"]*\)"$/\1/p' "$1")
	is_version "${v}" || die "${spec}: CANGA_VERSION=\"${v}\" is not X.Y.Z"
	printf '%s\n' "${v}"
}

# spec_sum <file> <arch> prints the sha256 the kit pins for arch.
spec_sum() {
	s=$(awk -v want="$2" '
		/^[[:space:]]*arch="[^"]*"$/ { cur = $0; sub(/^[[:space:]]*arch="/, "", cur); sub(/"$/, "", cur); next }
		/^[[:space:]]*sha256="[^"]*"$/ && cur == want { v = $0; sub(/^[[:space:]]*sha256="/, "", v); sub(/"$/, "", v); print v; n++ }
		END { exit n != 1 }
	' "$1") || die "${spec}: want exactly one sha256=\"...\" line for ${2}"
	printf '%s\n' "${s}" | grep -Eqx '[0-9a-f]{64}' || die "${spec}: the ${2} sha256 '${s}' is not a sha256"
	printf '%s\n' "${s}"
}

# committed_spec <rev> <out> writes the kit as committed at rev into out.
committed_spec() {
	git show "$1:${spec}" >"$2" 2>/dev/null || die "${spec} is not in $1"
}

# sandbox_sum <checksums> <version> <arch> prints the sha256 checksums.txt
# lists for that arch's canga-sandbox_ archive. Exactly one line, compared by
# filename equality, or it refuses: a missing arch must never leave the old
# hash in place next to a new version.
sandbox_sum() {
	asset="canga-sandbox_$2_linux_$3.tar.gz"
	sums=$(awk -v a="${asset}" 'NF == 2 && $2 == a { print $1 }' "$1")
	count=$(printf '%s' "${sums}" | grep -c . || true)
	[ "${count}" = 1 ] || die "$1: want exactly one line for ${asset}, found ${count}"
	printf '%s\n' "${sums}" | grep -Eqx '[0-9a-f]{64}' || die "$1: ${asset}: '${sums}' is not a sha256"
	printf '%s\n' "${sums}"
}

# render <version> <amd64 sum> <arm64 sum> <in> <out> writes <in> with its
# pin replaced into <out>, and refuses unless <in> has exactly one
# CANGA_VERSION line and one sha256 line under each arch's `arch="..."`
# line. Every other byte is copied.
#
# The sums are taken as already-parsed strings, never a checksums.txt path:
# every caller has already run sandbox_sum itself (to check_published
# against, or to check against a downloaded archive) before render ever
# runs, and a second sandbox_sum call here - re-reading the same file - would
# be both a needless re-parse and a TOCTOU window (the file could change
# between the two reads on a filesystem an attacker can write to). One
# parse, passed through, is both cheaper and the only version that means
# what check_published (or bump's archive check) already verified is
# exactly what gets written.
render() {
	spec_version "$4" >/dev/null

	if ! awk -v ver="$1" -v amd64="$2" -v arm64="$3" '
		/^[[:space:]]*CANGA_VERSION="[^"]*"$/ {
			nver++
			sub(/"[^"]*"/, "\"" ver "\"")
			print
			next
		}
		/^[[:space:]]*arch="[^"]*"$/ {
			cur = $0
			sub(/^[[:space:]]*arch="/, "", cur)
			sub(/"$/, "", cur)
			print
			next
		}
		/^[[:space:]]*sha256="[^"]*"$/ {
			if (cur == "amd64") { namd64++; sum = amd64 }
			else if (cur == "arm64") { narm64++; sum = arm64 }
			else { stray++; print; next }
			sub(/"[^"]*"/, "\"" sum "\"")
			cur = ""
			print
			next
		}
		{ print }
		END { exit !(nver == 1 && namd64 == 1 && narm64 == 1 && stray == 0) }
	' "$4" >"$5"; then
		die "${spec}: want one CANGA_VERSION line and one sha256=\"...\" line after each of arch=\"amd64\" and arch=\"arm64\"; refusing to rewrite it"
	fi
}

# published_tags prints the tag of every published (not draft, not
# pre-release) release.
published_tags() {
	command -v gh >/dev/null 2>&1 || die "gh is not installed; it is how the published releases are read"
	gh release list -R "${repo}" --exclude-drafts --exclude-pre-releases --limit 1000 --json tagName --jq '.[].tagName' ||
		die "could not list the published releases with gh"
}

# pick <below|above> <tag> <tags> prints, among the vX.Y.Z tags in <tags>, the
# newest one below <tag>, or the newest one above it. Compared as numbers:
# v0.10.0 is above v0.9.0.
pick() {
	printf '%s\n' "$3" | awk -v mode="$1" -v self="$2" '
		function key(t, p) { split(substr(t, 2), p, "."); return sprintf("%012d%012d%012d", p[1], p[2], p[3]) }
		$0 ~ /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/ {
			k = key($0)
			if ((mode == "below" && k < key(self)) || (mode == "above" && k > key(self)))
				if (best == "" || k > key(best)) best = $0
		}
		END { if (best != "") print best }
	'
}

# orDash(x) is jq syntax, defined once and reused by both --jq expressions
# below: jq's own "// default" operator substitutes only for null or false,
# NEVER for an empty string, so ".digest // \"-\"" alone would still read
# back as an empty field the day a value is "" instead of null. Folding ""
# into the same "-" here is what makes "never as an empty field" (see
# assets_jq's own comment) actually true, not just true for null.
# shellcheck disable=SC2016 # jq syntax, not a shell expansion
or_dash_def='def orDash(x): if (x == null or x == "") then "-" else x end;'

# asset_line_def is the "<name> <digest> <uploader>" jq snippet shared by
# assets_jq and release_jq below: defined once so the two --jq expressions
# cannot drift apart the way two hand-copied inline snippets could. A
# missing or empty digest or uploader prints as "-", never as an empty
# field (orDash above), so a field that is absent can never collapse into
# its neighbour under awk's default whitespace splitting (two real fields
# either side of one truly-empty one would otherwise read back as one
# field short).
# shellcheck disable=SC2016 # jq syntax, not a shell expansion
asset_line_def='def assetLine: "\(.name) \(orDash(.digest)) \(orDash(.uploader.login))";'

# The --jq that turns a release document into asset_line_def's lines, used
# by check-clobber alone (existence only: nothing check-clobber reads here
# is trusted or pinned, so it has no need for release_jq's _meta line
# below).
# shellcheck disable=SC2016 # jq syntax, not a shell expansion
assets_jq="${or_dash_def} ${asset_line_def} .assets[] | assetLine"

# release_jq is release_assets's own --jq: the same asset lines as
# assets_jq, preceded by one "_meta <draft> <prerelease>" line - both
# booleans straight from the release document, from the SAME gh api call
# release_assets makes, so check_published's draft/prerelease refusal (see
# release_assets below) costs no second round trip to GitHub.
# shellcheck disable=SC2016 # jq syntax, not a shell expansion
release_jq="${or_dash_def} ${asset_line_def} \"_meta \\(.draft) \\(.prerelease)\", (.assets[] | assetLine)"

# older_line <tag> <newer> refuses a release below the newest one unless
# SBX_KIT_OLDER_LINE names exactly this tag, and warns when it does.
older_line() {
	if [ "${SBX_KIT_OLDER_LINE:-}" != "$1" ]; then
		die "$1 is below the newest published release $2. The kit follows the newest line and never moves backwards; to release $1 on its older line anyway, set SBX_KIT_OLDER_LINE=$1"
	fi
	echo "sbx-kit-pin: WARNING: $1 is below the newest published release $2, and SBX_KIT_OLDER_LINE=$1: the kit keeps following the newest line" >&2
}

# release_assets <tag> prints "<name> <digest> <uploader>" for every asset
# of <tag>'s release, after refusing a draft or a prerelease outright: the
# digest is GitHub's own "sha256:<hex>" of the bytes it serves, and the
# uploader is who published the asset (its login, or "[bot]"-suffixed for
# an app such as the Release workflow, which is not the only workflow that
# can produce that suffix - see check_published's own comment). gh api, not
# gh release view: gh 2.46's view prints empty digests.
release_assets() {
	command -v gh >/dev/null 2>&1 || die "gh is not installed; it is how the published digests are read"
	doc=$(gh api "repos/${repo}/releases/tags/$1" --jq "${release_jq}") ||
		die "could not read the assets of release $1 with gh"
	meta=$(printf '%s\n' "${doc}" | awk 'NF == 3 && $1 == "_meta" { print; exit }')
	[ -n "${meta}" ] || die "release $1: gh answered without the expected _meta line; refusing to trust its assets"
	[ "$(printf '%s\n' "${meta}" | awk '{ print $2 }')" != "true" ] ||
		die "release $1 is a draft; refusing to trust an asset from a release that is not published"
	[ "$(printf '%s\n' "${meta}" | awk '{ print $3 }')" != "true" ] ||
		die "release $1 is a prerelease; refusing to trust an asset from a release GitHub has not fully published"
	printf '%s\n' "${doc}" | awk 'NF == 3 && $1 != "_meta"'
}

# check_published <tag> <version> <amd64 sum> <arm64 sum> refuses unless the
# release's canga-sandbox_ assets are served with exactly those sha256, each
# uploaded under the github-actions[bot] identity.
#
# What that identity proves, and what it does not: GitHub reports every
# asset uploaded with a workflow's default GITHUB_TOKEN under that one
# login, and the gh api call above is scoped to THIS repository
# (repos/${repo}/...), so a match here proves the asset was uploaded by
# SOME workflow run in brunovenceslau/canga - not specifically by the
# Release workflow (release.yml). A different workflow in this repository,
# granted `contents: write`, could run `gh release upload --clobber` itself
# and produce the same identity; this check does not distinguish that case
# from the Release workflow's own upload. What it does close: a NEW asset
# upload with a PERSON's own token (an interactive `gh release upload
# --clobber` run by hand) is refused, digest match or not, because that
# upload's uploader is no longer github-actions[bot]. What it does not
# close: a metadata-only edit - the release's title or notes, or an
# asset's label or name - that never re-uploads the asset leaves its
# uploader field untouched and passes here unnoticed. A rename still
# cannot swap archives: it only moves bytes already uploaded, and the
# digest check, which runs first, refuses them under the wrong name.
# Binding assets to release.yml specifically needs build provenance
# attestation (`gh attestation verify --signer-workflow
# <repo>/.github/workflows/release.yml`); that is docs/HANDOFF.md's pending
# item 3, not implemented here.
#
# Each refusal below is its own message, not folded into one: an asset
# missing from the release entirely, one the release lists more than once
# (ambiguous - refused rather than silently picking the first), one with no
# digest, one whose digest does not match, one with no uploader, and one
# whose uploader is not github-actions[bot] are six different problems, and
# conflating any two of them into the same wording would make the refusal
# harder to act on. The digest check runs before the uploader check, so a
# release wrong in both ways is refused for its digest.
check_published() {
	assets=$(release_assets "$1")
	for arch in amd64 arm64; do
		if [ "${arch}" = amd64 ]; then want=$3; else want=$4; fi
		# canga-sandbox_<version>_linux_<arch>.tar.gz. The NF == 3 guard
		# here is belt-and-suspenders: release_assets already dropped every
		# malformed line (most notably an asset name that itself contains
		# whitespace) before returning ${assets}, so this awk call never
		# actually sees an NF != 3 line today. It is kept anyway so that a
		# future caller of this loop over a differently-sourced ${assets}
		# fails the same safe way - matched only on an exact "<name>
		# <digest> <uploader>" line, never mis-split into digest/uploader
		# fields borrowed from inside the name - rather than silently
		# trusting release_assets to keep filtering forever.
		asset="canga-sandbox_$2_linux_${arch}.tar.gz"
		matches=$(printf '%s\n' "${assets}" | awk -v a="${asset}" 'NF == 3 && $1 == a')
		count=$(printf '%s' "${matches}" | grep -c . || true)
		case "${count}" in
		0) die "release $1 carries no ${asset} at all; refusing to trust an asset that is not in the release" ;;
		1) : ;;
		*) die "release $1 lists ${count} assets named ${asset}; refusing to trust an ambiguous release" ;;
		esac
		got=$(printf '%s\n' "${matches}" | awk '{ print $2 }')
		uploader=$(printf '%s\n' "${matches}" | awk '{ print $3 }')
		[ "${got}" != "-" ] || die "release $1 reports no digest for ${asset}; refusing to trust a sha256 GitHub does not confirm"
		[ "${got}" = "sha256:${want}" ] || die "release $1 serves ${asset} as ${got}, not sha256:${want}; the bytes on GitHub are not the ones pinned"
		[ "${uploader}" != "-" ] || die "release $1 reports no uploader for ${asset}; refusing to trust an asset GitHub does not attribute to a workflow run in this repository"
		[ "${uploader}" = "github-actions[bot]" ] ||
			die "release $1's ${asset} was uploaded by ${uploader}, not github-actions[bot]; only a workflow's own GITHUB_TOKEN in this repository may publish this pin's archives"
		echo "sbx-kit-pin: release $1 serves ${asset} as sha256:${want}, uploaded under the github-actions[bot] identity"
	done
}

cmd_version() {
	[ $# = 0 ] || die "usage: version"
	[ -f "${spec}" ] || die "${spec}: not found"
	spec_version "${spec}"
}

cmd_rewrite() {
	[ $# = 2 ] || die "usage: rewrite X.Y.Z <checksums.txt>"
	is_version "$1" || die "not a version: '$1' (want X.Y.Z, no v, no leading zeros)"
	[ -f "$2" ] || die "$2: not found"
	[ -f "${spec}" ] || die "${spec}: not found"

	# Always verified, with no bypass: rewrite is the manual recovery path
	# (used, by hand, for the v0.10.2 pin), and a manual path that trusts an
	# unverified checksums.txt is exactly the gap bump closes for the normal
	# path. check_published is the same check check-previous and bump run,
	# against the release the version names, so a claimed sum that GitHub
	# does not confirm - or an asset GitHub does not attribute to
	# github-actions[bot] - never reaches the kit.
	#
	# $2 (the checksums.txt path) is parsed exactly once, right here:
	# amd64/arm64 are passed into render below as plain strings, not
	# re-derived from $2 a second time. See render's own comment for why a
	# second parse of the same file is a TOCTOU window, not extra safety.
	amd64=$(sandbox_sum "$2" "$1" amd64)
	arm64=$(sandbox_sum "$2" "$1" arm64)
	check_published "v$1" "$1" "${amd64}" "${arm64}"

	tmp=$(mktemp "${spec}.XXXXXX")
	trap_cleanup_file "${tmp}"
	render "$1" "${amd64}" "${arm64}" "${spec}" "${tmp}"
	# mktemp creates 0600; keep the kit's own mode.
	chmod 0644 "${tmp}"
	mv "${tmp}" "${spec}"
	trap_clear
	echo "sbx-kit-pin: ${spec} now pins ${1}, verified against release v$1's published digests"
}

cmd_check_previous() {
	[ $# = 1 ] || die "usage: check-previous <tag>"
	tag_version "$1" >/dev/null

	scratch=$(mktemp -d)
	trap_cleanup_dir "${scratch}"
	committed_spec HEAD "${scratch}/spec.yaml"
	kit=$(spec_version "${scratch}/spec.yaml")

	tags=$(published_tags)
	prev=$(pick below "$1" "${tags}")
	[ -n "${prev}" ] || die "no published release below $1, so there is no kit pin to compare against"
	newer=$(pick above "$1" "${tags}")

	if [ -n "${newer}" ]; then
		# A release on an older line. The kit follows the newest line and a
		# bump never moves it backwards, so the older line's kit only has to
		# pin SOME published release below this tag.
		older_line "$1" "${newer}"
		printf '%s\n' "${tags}" | grep -qx "v${kit}" ||
			die "${spec} pins CANGA_VERSION=\"${kit}\", which is not a published release"
		[ "$(pick below "$1" "v${kit}")" = "v${kit}" ] ||
			die "${spec} pins CANGA_VERSION=\"${kit}\", which is not below $1"
		prev="v${kit}"
	elif [ "v${kit}" != "${prev}" ]; then
		cat >&2 <<EOF
sbx-kit-pin: ${spec} pins CANGA_VERSION="${kit}", but the newest published
release below $1 is ${prev}. The kit bump that ${prev}'s release left
behind has not merged, and releasing $1 now would leave the kit further
behind. Merge branch chore/sbx-kit-${prev} first, then tag on top of it. If
that branch is lost, README "Move the sbx kit's pin" shows how to rebuild
it.
EOF
		exit 1
	fi

	pinned_amd64=$(spec_sum "${scratch}/spec.yaml" amd64)
	pinned_arm64=$(spec_sum "${scratch}/spec.yaml" arm64)
	check_published "${prev}" "${prev#v}" "${pinned_amd64}" "${pinned_arm64}"
	echo "sbx-kit-pin: ${spec} pins ${prev}, the newest published release below $1 on its line"
}

cmd_check_clobber() {
	[ $# = 1 ] || die "usage: check-clobber <tag>"
	version=$(tag_version "$1")
	command -v gh >/dev/null 2>&1 || die "gh is not installed; it is how the published digests are read"

	scratch=$(mktemp -d)
	trap_cleanup_dir "${scratch}"
	# A release that does not exist yet has nothing to clobber: the Release
	# workflow runs this before GoReleaser creates it. Any other gh failure
	# is a refusal.
	if ! assets=$(gh api "repos/${repo}/releases/tags/$1" --jq "${assets_jq}" 2>"${scratch}/gh.err"); then
		if grep -q 'HTTP 404' "${scratch}/gh.err"; then
			# GitHub also answers 404 when the token cannot see the repository
			# at all, so the tag's 404 means "no release" only once the
			# repository itself answers, under exactly this name.
			seen=$(gh api "repos/${repo}" --jq .full_name 2>"${scratch}/gh-repo.err") || seen=""
			if [ "${seen}" != "${repo}" ]; then
				cat "${scratch}/gh-repo.err" >&2
				die "gh does not see ${repo} as itself; a 404 for $1 proves nothing (gh sees: ${seen:-nothing})"
			fi
			echo "sbx-kit-pin: $1 has no release yet; nothing to clobber"
			return 0
		fi
		cat "${scratch}/gh.err" >&2
		die "could not read the assets of release $1 with gh"
	fi
	if ! printf '%s\n' "${assets}" | grep -q '^canga-sandbox_'; then
		echo "sbx-kit-pin: $1 carries no canga-sandbox_ archive yet; nothing to clobber"
		return 0
	fi

	branch="chore/sbx-kit-$1"
	why=""
	pushed=$(git ls-remote --heads origin "refs/heads/${branch}") ||
		die "could not ask origin whether ${branch} was pushed"
	if [ -n "${pushed}" ]; then
		why="${branch} is on origin"
	else
		git fetch -q origin refs/heads/main || die "could not fetch origin's main to see what its kit pins"
		committed_spec FETCH_HEAD "${scratch}/main.yaml"
		main=$(spec_version "${scratch}/main.yaml")
		# Refused when main pins this release or a newer one.
		if [ "$(pick below "v${version}" "v${main}")" != "v${main}" ]; then
			why="origin's main already pins v${main}"
		fi
	fi

	if [ -z "${why}" ]; then
		echo "sbx-kit-pin: $1's archives are not pinned anywhere yet; rebuilding them is safe"
		return 0
	fi
	if [ "${SBX_KIT_ALLOW_CLOBBER:-}" = "$1" ]; then
		echo "sbx-kit-pin: WARNING: ${why}, and SBX_KIT_ALLOW_CLOBBER=$1: replacing $1's archives breaks every sandbox pinned to them" >&2
		return 0
	fi
	cat >&2 <<EOF
sbx-kit-pin: $1 already carries canga-sandbox_ archives and ${why}.
Rebuilding them changes their sha256 (the archives are not reproducible), and
every sandbox pinned to a kit that names them then fails to install. Cut a
new patch release instead. To replace them anyway, knowing that, set
SBX_KIT_ALLOW_CLOBBER=$1 (the tag itself, so the override cannot outlive it).
EOF
	exit 1
}

cmd_bump() {
	[ $# = 2 ] || die "usage: bump <tag> <checksums.txt>"
	version=$(tag_version "$1")
	[ -f "$2" ] || die "$2: not found"
	dir=$(dirname "$2")
	dist=$(cd "${dir}" && pwd)
	sums="${dist}/$(basename "$2")"
	branch="chore/sbx-kit-$1"

	[ -z "$(git status --porcelain)" ] || die "the working tree is dirty; commit or stash first"
	here=$(git tag --points-at HEAD)
	[ "${here}" = "$1" ] || die "HEAD must carry exactly the tag $1, and it carries: $(printf '%s' "${here:-no tag}" | tr '\n' ' ')"

	# Every refusal about the checksums, the archives or the kit lands before
	# git is written to.
	scratch=$(mktemp -d)
	trap_cleanup_dir "${scratch}"
	committed_spec HEAD "${scratch}/head.yaml"

	# ${sums} is parsed exactly once, right here: amd64/arm64 are plain
	# strings from this point on, reused below for the archive check, for
	# check_published, and passed into render rather than making render
	# re-derive them from ${sums} a second time (a needless re-parse, and a
	# TOCTOU window on a file bump does not own - see render's comment).
	amd64=$(sandbox_sum "${sums}" "${version}" amd64)
	arm64=$(sandbox_sum "${sums}" "${version}" arm64)
	render "${version}" "${amd64}" "${arm64}" "${scratch}/head.yaml" "${scratch}/spec.yaml"

	# checksums.txt is only a claim. The pin must name the bytes the archives
	# next to it actually contain (whether built locally or, as
	# `make release-kit-bump` does, downloaded from the release) AND the
	# bytes GitHub serves (the release's digests): a failed or repeated
	# upload leaves those two apart, and neither may reach a signed pin.
	for arch in amd64 arm64; do
		if [ "${arch}" = amd64 ]; then want=${amd64}; else want=${arm64}; fi
		archive="${dist}/canga-sandbox_${version}_linux_${arch}.tar.gz"
		[ -f "${archive}" ] || die "${archive}: not found; the pin must match the archive it names"
		got=$(sha256_of "${archive}")
		[ "${got}" = "${want}" ] || die "${archive} is sha256:${got}, but ${sums} says ${want}"
		echo "sbx-kit-pin: ${archive} is sha256:${want}"
	done
	check_published "$1" "${version}" "${amd64}" "${arm64}"

	# Two steps: nested in the pick, a failing published_tags would be
	# masked from set -e and read as "no newer release".
	tags=$(published_tags)
	newer=$(pick above "$1" "${tags}")
	if [ -n "${newer}" ]; then
		older_line "$1" "${newer}"
		echo "sbx-kit-pin: no branch for $1: the kit does not move backwards"
		return 0
	fi

	if git rev-parse --verify -q "refs/heads/${branch}" >/dev/null; then
		git show "${branch}:${spec}" >"${scratch}/existing.yaml" 2>/dev/null ||
			die "branch ${branch} exists but has no ${spec}; inspect it, delete it, and run again"
		cmp -s "${scratch}/spec.yaml" "${scratch}/existing.yaml" ||
			die "branch ${branch} exists but does not pin these $1 hashes; inspect it, delete it, and run again"
		[ "$(git rev-parse "${branch}^")" = "$(git rev-parse HEAD)" ] ||
			die "branch ${branch} pins these hashes but is not one commit on top of $1; inspect it, delete it, and run again"
		[ "$(git diff --name-only HEAD "${branch}")" = "${spec}" ] ||
			die "branch ${branch} changes more than ${spec}; inspect it, delete it, and run again"
		git verify-commit "${branch}" >/dev/null 2>&1 ||
			die "branch ${branch} pins these hashes but its commit does not verify (git verify-commit; is gpg.ssh.allowedSignersFile set?)"
		echo "sbx-kit-pin: ${branch} already pins $1 in a verified commit; nothing to do"
		next_steps "${branch}"
		return 0
	fi

	if cmp -s "${scratch}/spec.yaml" "${scratch}/head.yaml"; then
		echo "sbx-kit-pin: ${spec} already pins $1; no branch needed"
		return 0
	fi

	# A worktree of its own, so the checkout this ran from (a detached
	# checkout of the tag, when it is `make release-kit-bump`'s own temporary
	# worktree) is left exactly as it was.
	git worktree add -q -b "${branch}" "${scratch}/wt" HEAD
	# -S: the kit is a trust root pinned by commit, and this repository's
	# commits are signed; an unsigned bump fails here instead of at review.
	if ! {
		cat "${scratch}/spec.yaml" >"${scratch}/wt/${spec}" &&
			git -C "${scratch}/wt" commit -q -S -m "chore(sbx-kit): pin canga $1" \
				-m "Rewrites CANGA_VERSION and the canga-sandbox_ linux amd64 and arm64 sha256 in ${spec} from the checksums.txt built for $1, checked against the archives it names and the release's published digests." \
				-- "${spec}"
	}; then
		git worktree remove --force "${scratch}/wt"
		git branch -D "${branch}" >/dev/null
		die "could not commit the bump; ${branch} was not left behind"
	fi
	# Signed is not enough: the rerun above trusts this branch only through
	# git verify-commit, so a commit that does not verify here never stays.
	if ! git -C "${scratch}/wt" verify-commit HEAD >/dev/null 2>&1; then
		git worktree remove --force "${scratch}/wt"
		git branch -D "${branch}" >/dev/null
		die "the bump commit does not pass git verify-commit, so ${branch} was not left behind. gpg.ssh.allowedSignersFile must list the key that signed it"
	fi
	git worktree remove "${scratch}/wt"
	echo "sbx-kit-pin: committed the $1 kit pin on branch ${branch} ($(git rev-parse --short "${branch}"))"
	next_steps "${branch}"
}

next_steps() {
	cat <<EOF

Next steps (not done for you: both are outward-facing):
    git push -u origin $1
    gh pr create --base main --head $1 --fill
Once it merges, the merge commit is the sbx kit ref to pin, and the next
release's 'make release-preflight' stops refusing.
EOF
}

[ $# -ge 1 ] || die "usage: sbx-kit-pin.sh version|rewrite|check-previous|check-clobber|bump ..."
sub=$1
shift
case "${sub}" in
version) cmd_version "$@" ;;
rewrite) cmd_rewrite "$@" ;;
check-previous) cmd_check_previous "$@" ;;
check-clobber) cmd_check_clobber "$@" ;;
bump) cmd_bump "$@" ;;
*) die "unknown command: ${sub}" ;;
esac
