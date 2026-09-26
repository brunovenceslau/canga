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
#                                             tree (no checks against GitHub)
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
# gh is always pointed at brunovenceslau/canga (see repo= below), the
# repository the kit downloads from, never at the checkout's remotes.
#
# check-previous, check-clobber and bump read the kit as committed (at HEAD,
# or at origin's main), never the working tree, so a local edit, an
# assume-unchanged or a skip-worktree flag cannot change their answer.
#
# POSIX sh and POSIX awk only: it runs on the macs `make release` runs on, whose
# awk has no {n} interval expressions, so hex is checked by grep -E.

set -eu

spec="sbx-kit/spec.yaml"
# The same repository the kit's install step downloads from.
repo="brunovenceslau/canga"

die() {
	echo "sbx-kit-pin: $*" >&2
	exit 1
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

# render <version> <checksums> <in> <out> writes <in> with its pin replaced
# into <out>, and refuses unless <in> has exactly one CANGA_VERSION line and
# one sha256 line under each arch's `arch="..."` line. Every other byte is
# copied.
render() {
	amd64=$(sandbox_sum "$2" "$1" amd64)
	arm64=$(sandbox_sum "$2" "$1" arm64)
	spec_version "$3" >/dev/null

	if ! awk -v ver="$1" -v amd64="${amd64}" -v arm64="${arm64}" '
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
	' "$3" >"$4"; then
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

# The --jq that turns a release document into "<name> <digest>" lines.
# shellcheck disable=SC2016 # jq syntax, not a shell expansion
assets_jq='.assets[] | "\(.name) \(.digest // "")"'

# older_line <tag> <newer> refuses a release below the newest one unless
# SBX_KIT_OLDER_LINE names exactly this tag, and warns when it does.
older_line() {
	if [ "${SBX_KIT_OLDER_LINE:-}" != "$1" ]; then
		die "$1 is below the newest published release $2. The kit follows the newest line and never moves backwards; to release $1 on its older line anyway, set SBX_KIT_OLDER_LINE=$1"
	fi
	echo "sbx-kit-pin: WARNING: $1 is below the newest published release $2, and SBX_KIT_OLDER_LINE=$1: the kit keeps following the newest line" >&2
}

# release_assets <tag> prints "<name> <digest>" for every asset of <tag>'s
# release, the digest being GitHub's own "sha256:<hex>" of the bytes it
# serves. gh api, not gh release view: gh 2.46's view prints empty digests.
release_assets() {
	command -v gh >/dev/null 2>&1 || die "gh is not installed; it is how the published digests are read"
	gh api "repos/${repo}/releases/tags/$1" --jq "${assets_jq}" ||
		die "could not read the assets of release $1 with gh"
}

# check_published <tag> <version> <amd64 sum> <arm64 sum> refuses unless the
# release's canga-sandbox_ assets are served with exactly those sha256. A
# missing digest is a refusal, not a pass.
check_published() {
	assets=$(release_assets "$1")
	for arch in amd64 arm64; do
		if [ "${arch}" = amd64 ]; then want=$3; else want=$4; fi
		asset="canga-sandbox_$2_linux_${arch}.tar.gz"
		got=$(printf '%s\n' "${assets}" | awk -v a="${asset}" '$1 == a { print $2 }')
		[ -n "${got}" ] || die "release $1 serves no digest for ${asset}; refusing to trust a sha256 GitHub does not confirm"
		[ "${got}" = "sha256:${want}" ] || die "release $1 serves ${asset} as ${got}, not sha256:${want}; the bytes on GitHub are not the ones pinned"
		echo "sbx-kit-pin: release $1 serves ${asset} as sha256:${want}"
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

	tmp=$(mktemp "${spec}.XXXXXX")
	trap 'rm -f "${tmp}"' EXIT
	render "$1" "$2" "${spec}" "${tmp}"
	# mktemp creates 0600; keep the kit's own mode.
	chmod 0644 "${tmp}"
	mv "${tmp}" "${spec}"
	trap - EXIT
	echo "sbx-kit-pin: ${spec} now pins ${1}"
}

cmd_check_previous() {
	[ $# = 1 ] || die "usage: check-previous <tag>"
	tag_version "$1" >/dev/null

	scratch=$(mktemp -d)
	trap 'rm -rf "${scratch}"' EXIT
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
	trap 'rm -rf "${scratch}"' EXIT
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
				die "gh cannot see ${repo}; a 404 for $1 proves nothing"
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
	trap 'rm -rf "${scratch}"' EXIT
	committed_spec HEAD "${scratch}/head.yaml"
	render "${version}" "${sums}" "${scratch}/head.yaml" "${scratch}/spec.yaml"

	# checksums.txt is only a claim. The pin must name the bytes this build
	# produced (the archives next to it) AND the bytes GitHub serves (the
	# release's digests): a failed upload, or a rebuild after one, leaves
	# those two apart, and neither may reach a signed pin.
	amd64=$(sandbox_sum "${sums}" "${version}" amd64)
	arm64=$(sandbox_sum "${sums}" "${version}" arm64)
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

	# A worktree of its own, so the checkout `make release` ran in (the tag,
	# often detached) is left exactly as it was.
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
