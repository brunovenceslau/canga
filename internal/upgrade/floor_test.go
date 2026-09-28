// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckFloor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tag     string
		refused bool
	}{
		{name: "well below the floor", tag: "v0.1.0", refused: true},
		{name: "the release just below the floor", tag: "v0.10.4", refused: true},
		{name: "the floor itself", tag: MinReleaseTag},
		// checkFloor does NOT normalize: a caller that wants to accept the
		// goreleaser spelling (no leading "v") has to normalize first, the way
		// wantedTag does for an explicit --tag. Fed to checkFloor directly,
		// unnormalized, a tag missing its "v" is refused - the same shape rule
		// that closes a release published under a bare digit-leading tag the
		// repository's "v*" ruleset never protected (canonicalReleaseTag).
		{name: "the goreleaser spelling, unnormalized, is refused", tag: "0.10.5", refused: true},
		{name: "just above the floor", tag: "v0.10.6"},
		{name: "a later minor", tag: "v0.11.0"},
		{name: "a later major", tag: "v1.0.0"},
		// Compared as numbers, not text: v0.9.0 is below the floor even though
		// "0.9.0" sorts after "0.10.5" as a string.
		{name: "versions compare as numbers, not strings", tag: "v0.9.0", refused: true},
		// canonicalReleaseTag refuses every pre-release, including one above the
		// floor on the numeric core alone: isReleaseTag and isNewer treat
		// "v1.0.0-rc1" as a real, later release (version_test.go), but the floor
		// gate is a stricter, single shape with no carve-out for a later major.
		{name: "a pre-release of a later major is refused, unlike isNewer's answer", tag: "v1.0.0-rc1", refused: true},
		// The two edge shapes round-1 ship-gate finding 1 named: awk's numeric
		// coercion truncated the first at the dash, and read the second as if
		// the leading zero were not there, so both slipped through the old,
		// purely-numeric awk check as equal to the floor.
		{name: "a pre-release of the floor itself", tag: "v0.10.5-rc1", refused: true},
		{name: "a leading zero", tag: "v00.10.5", refused: true},
		{name: "build metadata", tag: "v0.10.5+build", refused: true},
		{name: "missing the patch component", tag: "v0.10", refused: true},
		{name: "not a version at all", tag: "latest", refused: true},
		{name: "empty", tag: "", refused: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := checkFloor(tt.tag)

			if !tt.refused {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, ErrBelowFloor)
			assert.ErrorContains(t, err, MinReleaseTag)
		})
	}
}
