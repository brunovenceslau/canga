// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The build shapes every test in this package speaks in. They are constants
// because each one stands for a distinct KIND of build, not because the exact
// spelling is incidental — the spelling is most of what is under test.
//
// installedVersion and newerVersion sit at or above MinReleaseTag on
// purpose: Run and wantedTag refuse a release below the floor (floor.go),
// so a fixture below it would fail every test in this package for the
// floor's reason instead of whatever each test actually exercises.
const (
	installedVersion = "v0.10.6"            // a published release
	newerVersion     = "v0.10.7"            // a later published release
	goInstallVersion = "dev"                // what a plain `go build` or `go install` stamps
	preRelease       = "v1.0.0-rc1"         // a release candidate, which IS a release
	describedVersion = "v0.10.6-3-gabc1234" // a tree that has moved past its last tag
	dirtyVersion     = "v0.10.6-dirty"      // uncommitted changes at the tag
)

func TestNormalizeTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		version  string
		expected string
	}{
		{name: "goreleaser spelling gains the v", version: "0.10.6", expected: installedVersion},
		{name: "makefile spelling is left alone", version: installedVersion, expected: installedVersion},
		{name: "surrounding space is trimmed", version: "  v1.2.3\n", expected: "v1.2.3"},
		{name: "a word is not a version", version: goInstallVersion, expected: goInstallVersion},
		{name: "empty stays empty", version: "", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.expected, normalizeTag(tt.version))
		})
	}
}

func TestReleaseTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		version   string
		expected  string
		isRelease bool
	}{
		{name: "a tag", version: installedVersion, expected: installedVersion, isRelease: true},
		{name: "a tag as goreleaser spells it", version: "0.10.6", expected: installedVersion, isRelease: true},
		{name: "a pre-release tag", version: preRelease, expected: preRelease, isRelease: true},
		{name: "a go install build", version: goInstallVersion, expected: goInstallVersion},
		// The three git describe shapes. Every one of them is VALID semver that
		// sorts below the tag it carries, which is why none of them may be
		// treated as a release: doing so offers that tag as an upgrade and
		// replaces a newer local build with older code.
		{name: "a build past the tag", version: describedVersion, expected: describedVersion},
		{name: "a dirty build past the tag", version: "v0.10.6-3-gabc1234-dirty", expected: "v0.10.6-3-gabc1234-dirty"},
		{name: "a dirty build at the tag", version: dirtyVersion, expected: dirtyVersion},
		{name: "an untagged tree describes as a hash", version: "76b3a75", expected: "v76b3a75"},
		// An all-digit hash is the one that gets through: "v1234567" is valid
		// semver, so only the canonical check rejects it.
		{name: "an all-digit describe hash", version: "1234567", expected: "v1234567"},
		{name: "a bare major is the shape a hash takes", version: "v1", expected: "v1"},
		// Accepted on purpose. It is a tag a person could push, semver orders it
		// correctly, and --tag accepts it too — which is the point: one
		// predicate, so the same string cannot be a release on one path only.
		{name: "a version with no patch", version: "v1.2", expected: "v1.2", isRelease: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tag, isRelease := releaseTag(tt.version)
			assert.Equal(t, tt.expected, tag)
			assert.Equal(t, tt.isRelease, isRelease)
		})
	}
}

// TestReleaseTagRejectsWhatSemverWouldAccept states the trap on its own, so a
// future simplification down to semver.IsValid fails here with the reason
// attached rather than shipping a silent downgrade.
func TestReleaseTagRejectsWhatSemverWouldAccept(t *testing.T) {
	t.Parallel()

	for _, version := range []string{describedVersion, dirtyVersion} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()

			_, isRelease := releaseTag(version)
			assert.False(t, isRelease)
			assert.True(t, isNewer(version, installedVersion),
				"the tag this build came from sorts ABOVE it, which is what makes treating it as a release a downgrade")
		})
	}
}

func TestIsNewer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		current  string
		tag      string
		expected bool
	}{
		{name: "a later minor", current: installedVersion, tag: newerVersion, expected: true},
		{name: "a later patch", current: installedVersion, tag: "v0.10.7", expected: true},
		{name: "an earlier release", current: newerVersion, tag: installedVersion},
		{name: "the same release, spelled differently", current: "0.10.6", tag: installedVersion},
		{name: "a release over its own pre-release", current: preRelease, tag: "v1.0.0", expected: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.expected, isNewer(tt.current, tt.tag))
		})
	}
}

func TestSameTag(t *testing.T) {
	t.Parallel()

	// The first case is the one that matters: the published v0.10.6 reports
	// itself as "0.10.6", so the check run against a freshly downloaded binary
	// has to accept both spellings of one release.
	assert.True(t, sameTag("0.10.6", installedVersion))
	assert.True(t, sameTag(installedVersion, installedVersion))
	assert.False(t, sameTag(installedVersion, "v0.10.7"))
	assert.False(t, sameTag("", installedVersion))
}

// TestReleaseTagAndWantedTagAgree pins the property the shared predicate
// exists for: --tag asks whether a string COULD name a release, releaseTag
// asks whether a binary was built from one, and the two must not disagree
// about that - a version isReleaseTag calls real must never be the one
// wantedTag calls ErrBadTag, and vice versa.
//
// It stops short of full equality, unlike before floor.go's canonical shape
// existed: isReleaseTag itself stayed exactly as lenient as it always was
// ("v1.2" and preRelease are both still a real release, version_test.go
// elsewhere), but wantedTag now ALSO applies checkFloor, a stricter, single
// shape (canonicalReleaseTag) that "v1.2" and preRelease do not match. A
// version can therefore be a real release by isReleaseTag's answer and
// still be refused by wantedTag - deliberately, per floor.go's own
// comment - so long as it is refused with ErrBelowFloor and not ErrBadTag:
// that is the one distinction this test still requires of the two.
//
// A `git describe` build is deliberately absent: "v0.10.6-3-gabc1234" is not a
// release BUILD, while as a --tag it is a release that simply does not exist.
// Those are different questions with different right answers.
func TestReleaseTagAndWantedTagAgree(t *testing.T) {
	t.Parallel()

	versions := []string{
		installedVersion,
		"v1.2",
		"v1",
		"v1234567",
		goInstallVersion,
		preRelease,
		"",
	}

	for _, version := range versions {
		t.Run("version "+version, func(t *testing.T) {
			t.Parallel()

			_, isRelease := releaseTag(version)

			_, err := wantedTag(Options{Tag: version, Current: installedVersion})

			// An empty --tag is not a tag at all, so that one case asks the
			// other question and is expected to succeed either way.
			if version == "" {
				assert.NoError(t, err)

				return
			}

			if !isRelease {
				require.ErrorIs(t, err, ErrBadTag,
					"a version releaseTag refuses must not be the one wantedTag accepts")

				return
			}

			if err != nil {
				require.ErrorIs(t, err, ErrBelowFloor,
					"a version releaseTag accepts must be refused by the floor, not called a bad tag")
			}
		})
	}
}
