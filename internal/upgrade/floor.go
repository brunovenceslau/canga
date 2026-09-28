// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"errors"
	"fmt"
	"regexp"

	"golang.org/x/mod/semver"
)

// MinReleaseTag is the oldest release this package will resolve or install,
// whether named by --tag or reached by following the newest published
// release: the first one release.yml attests with build provenance
// (attested_from in scripts/sbx-kit-pin.sh). install_host.sh and
// install_sandbox.sh carry the same value as min_version, and
// TestReleaseFloorMatchesAcrossInstallersAndCanga (release_floor_test.go, at
// the repository root) keeps the three from drifting apart.
//
// Every release below it - v0.1.0 through v0.10.4 - was deleted on
// 2026-09-27 for carrying no attestation, tags kept (docs/HANDOFF.md,
// "Immutability is not retroactive"). Anyone with contents: write can still
// recreate one of those releases with arbitrary bytes and a checksums.txt
// they wrote themselves to match, and this package's own verification
// (verify.go) checks a download only against that same release's own
// checksums.txt, so nothing here can tell a forgery like that apart from
// the original. A fixed constant: raising or lowering it is a decision, not
// a flag.
const MinReleaseTag = "v0.10.5"

// ErrBelowFloor reports a release below MinReleaseTag, or a tag that is not
// canonicalReleaseTag's one accepted shape and so cannot be compared against
// it at all.
var ErrBelowFloor = errors.New("release predates the build provenance attestation floor")

// canonicalReleaseTag is the ONE tag shape checkFloor accepts: "v" followed
// by three dot-separated non-negative integers, none carrying a leading
// zero, with no pre-release or build-metadata suffix. install_host.sh and
// install_sandbox.sh enforce the identical shape in POSIX sh awk, and
// TestReleaseFloorShapeAgreesAcrossInstallersAndGo (release_floor_test.go, at
// the repository root) runs the same table of tags through both to keep the
// two definitions from drifting apart.
//
// This is stricter than isReleaseTag, which stays exactly as lenient as it
// always was for everything else in this package: "v1.2" and a pre-release
// both still name a real release as far as isNewer and sameTag are
// concerned (version_test.go). Only the floor gate needs the stricter
// shape, because it has to refuse found.Tag - the tag GitHub's own
// /releases/latest resolves to - BEFORE normalizeTag's leniency (silently
// prepending "v" to a digit-leading tag) can wave through a release
// published under a tag the repository's "v*" ruleset never protected in
// the first place: a release tagged plain "1.0.0" is reachable by any
// contents: write actor, no admin rights needed, and would otherwise clear
// the floor once normalized to "v1.0.0".
var canonicalReleaseTag = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// checkFloor refuses tag unless it is both canonicalReleaseTag's one
// accepted shape and at or above MinReleaseTag.
//
// It deliberately does NOT normalize tag itself: a caller that wants to
// accept the GoReleaser spelling (a tag with no leading "v") has to
// normalize it before calling this, the way wantedTag does for an explicit
// --tag. Run's newest-release path passes found.Tag through unnormalized on
// purpose, which is what makes a release published under a bare "1.0.0" tag
// refused here rather than silently accepted as "v1.0.0".
func checkFloor(tag string) error {
	if !canonicalReleaseTag.MatchString(tag) {
		return fmt.Errorf(
			"%w: %q is not vX.Y.Z (no pre-release, no build metadata, no leading zeros, no missing \"v\"), so it cannot be compared against %s",
			ErrBelowFloor, tag, MinReleaseTag)
	}

	if semver.Compare(tag, MinReleaseTag) >= 0 {
		return nil
	}

	return fmt.Errorf("%w: %s is older than %s, the first release with a build provenance attestation",
		ErrBelowFloor, tag, MinReleaseTag)
}

// CheckReleaseFloor reports whether tag would be refused by the release
// floor: the same check Run and wantedTag apply to an explicit --tag and to
// the release GitHub's own /releases/latest resolves to. It is exported so
// release_floor_test.go, outside this package, can assert install_host.sh
// and install_sandbox.sh agree with it, tag shape for tag shape
// (TestReleaseFloorShapeAgreesAcrossInstallersAndGo).
func CheckReleaseFloor(tag string) error {
	return checkFloor(tag)
}
