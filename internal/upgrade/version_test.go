// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The build shapes every test in this package speaks in. They are constants
// because each one stands for a distinct KIND of build, not because the exact
// spelling is incidental — the spelling is most of what is under test.
const (
	installedVersion = "v0.1.0"            // a published release
	newerVersion     = "v0.2.0"            // a later published release
	goInstallVersion = "dev"               // what a plain `go build` or `go install` stamps
	describedVersion = "v0.1.0-3-gabc1234" // a tree that has moved past its last tag
	dirtyVersion     = "v0.1.0-dirty"      // uncommitted changes at the tag
)

func TestNormalizeTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		version  string
		expected string
	}{
		{name: "goreleaser spelling gains the v", version: "0.1.0", expected: installedVersion},
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
		{name: "a tag as goreleaser spells it", version: "0.1.0", expected: installedVersion, isRelease: true},
		{name: "a pre-release tag", version: "v1.0.0-rc1", expected: "v1.0.0-rc1", isRelease: true},
		{name: "a go install build", version: goInstallVersion, expected: goInstallVersion},
		// The three git describe shapes. Every one of them is VALID semver that
		// sorts below the tag it carries, which is why none of them may be
		// treated as a release: doing so offers that tag as an upgrade and
		// replaces a newer local build with older code.
		{name: "a build past the tag", version: describedVersion, expected: describedVersion},
		{name: "a dirty build past the tag", version: "v0.1.0-3-gabc1234-dirty", expected: "v0.1.0-3-gabc1234-dirty"},
		{name: "a dirty build at the tag", version: dirtyVersion, expected: dirtyVersion},
		{name: "an untagged tree describes as a hash", version: "76b3a75", expected: "v76b3a75"},
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
		{name: "a later patch", current: installedVersion, tag: "v0.1.1", expected: true},
		{name: "an earlier release", current: newerVersion, tag: installedVersion},
		{name: "the same release, spelled differently", current: "0.1.0", tag: installedVersion},
		{name: "a release over its own pre-release", current: "v1.0.0-rc1", tag: "v1.0.0", expected: true},
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

	// The first case is the one that matters: the published v0.1.0 reports
	// itself as "0.1.0", so the check run against a freshly downloaded binary
	// has to accept both spellings of one release.
	assert.True(t, sameTag("0.1.0", installedVersion))
	assert.True(t, sameTag(installedVersion, installedVersion))
	assert.False(t, sameTag(installedVersion, "v0.1.1"))
	assert.False(t, sameTag("", installedVersion))
}
