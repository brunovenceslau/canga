// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"regexp"
	"strings"

	"golang.org/x/mod/semver"
)

// describedBuild matches the tail `git describe --tags` appends to a tag once
// the tree has moved past it: "-<commits>-g<hash>", optionally "-dirty".
//
// It has to be recognised SEPARATELY from semver, because such a string is
// perfectly valid semver and sorts BELOW the tag it was derived from:
// "v0.1.0-3-gabc1234" reads as a pre-release of v0.1.0, so a build made three
// commits AFTER v0.1.0 would be offered v0.1.0 as an upgrade and quietly
// replaced by older code. Measured, not assumed; see version_test.go.
//
// Compiled once, at package scope, because compiling a regexp allocates.
var describedBuild = regexp.MustCompile(`-[0-9]+-g[0-9a-f]{7,}(-dirty)?$`)

// normalizeTag puts a version into the one spelling the rest of this package
// compares against.
//
// The two builds devctl ships spell the same release differently: GoReleaser's
// {{.Version}} drops the leading "v" ("0.1.0") while the Makefile's `git
// describe` keeps it ("v0.1.0"). That is a historical fact of the releases
// already published — .goreleaser.yml now stamps {{.Tag}} — so every
// comparison normalizes both sides rather than trusting either spelling.
func normalizeTag(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return ""
	}

	if version[0] >= '0' && version[0] <= '9' {
		return "v" + version
	}

	return version
}

// releaseTag reports the release a binary was built from, and whether it was
// built from one at all.
//
// "dev" (a plain `go build` or `go install`) and a `git describe` build are
// both reported as NOT a release: neither identifies a published artifact, so
// there is nothing meaningful to compare a release against.
func releaseTag(version string) (tag string, isRelease bool) {
	tag = normalizeTag(version)

	// "-dirty" is checked separately from the regexp, because `git describe`
	// appends it on its own when the tree is dirty AT the tag: "v0.1.0-dirty"
	// carries no commit count and no hash, yet is still not the release — and,
	// being a valid semver pre-release, it too sorts below the tag it names.
	if describedBuild.MatchString(tag) || strings.HasSuffix(tag, "-dirty") {
		return tag, false
	}

	return tag, isReleaseTag(tag)
}

// isReleaseTag reports whether tag can name a published release. It is the ONE
// answer to that question: --tag asks it of what the user typed and releaseTag
// asks it of what the binary reports, and the two disagreeing would make the
// same string a release on one path and not on the other.
//
// Valid semver is not enough on its own. semver accepts a bare major, and
// `git describe --always` falls back to a bare commit hash — so an all-digit
// hash like "1234567" normalizes to "v1234567", which IS valid semver, and such
// a build would read as release 1234567.0.0, compare above every real tag, and
// answer "nothing to do" forever. Roughly one hash in thirty is all digits.
//
// A dot in the version core is what separates the two: a hex hash carries none,
// and every tag this project publishes carries two. The core is taken before
// any pre-release, so a dot inside "-rc1.2" cannot vouch for a bare major.
//
// It deliberately does NOT demand all three components. "v1.2" is a tag a person
// could plausibly push, semver orders it correctly, and rejecting it would only
// move the disagreement rather than remove it.
func isReleaseTag(tag string) bool {
	core, _, _ := strings.Cut(tag, "-")

	return semver.IsValid(tag) && strings.Contains(core, ".")
}

// isNewer reports whether tag names a later release than current. Both sides
// are normalized first, so "0.1.0" and "v0.1.0" are one version.
func isNewer(current, tag string) bool {
	return semver.Compare(normalizeTag(tag), normalizeTag(current)) > 0
}

// sameTag reports whether two spellings name the same release. It is string
// equality after normalization rather than semver.Compare, because Compare
// answers 0 for two INVALID versions as readily as for two equal ones, and
// this decides whether a freshly downloaded binary is the one that was asked
// for.
func sameTag(a, b string) bool {
	return normalizeTag(a) == normalizeTag(b)
}
