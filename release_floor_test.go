// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package install_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/brunovenceslau/canga/internal/upgrade"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The release floor lives in four places, none of them derived from any of
// the others: install_host.sh and install_sandbox.sh have no working tree to
// read a shared value from when piped straight from curl into sh,
// scripts/sbx-kit-pin.sh's attested_from predates this floor by two PRs and
// is read by a different program (gh-based release tooling, not curl and
// sh), and internal/upgrade.MinReleaseTag is Go. TestReleaseFloorMatches
// AcrossInstallersAndCanga is what stands between one of the four getting
// bumped and the other three quietly staying behind.
var (
	_installFloorLine = regexp.MustCompile(`(?m)^\tmin_version=([0-9]+\.[0-9]+\.[0-9]+)$`)
	_attestedFromLine = regexp.MustCompile(`(?m)^attested_from="([0-9]+\.[0-9]+\.[0-9]+)"$`)
)

// installScriptFloor reads path's own min_version=X.Y.Z line, refusing
// unless there is exactly one.
func installScriptFloor(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	matches := _installFloorLine.FindAllStringSubmatch(string(data), -1)
	require.Len(t, matches, 1, "%s: want exactly one \"\\tmin_version=X.Y.Z\" line", path)

	return matches[0][1]
}

// sbxKitPinAttestedFrom reads scripts/sbx-kit-pin.sh's own attested_from
// value, the same one check_published (in that script) already uses to
// decide which releases must carry a build provenance attestation.
func sbxKitPinAttestedFrom(t *testing.T) string {
	t.Helper()

	path := "scripts/sbx-kit-pin.sh"
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	matches := _attestedFromLine.FindAllStringSubmatch(string(data), -1)
	require.Len(t, matches, 1, "%s: want exactly one attested_from=\"X.Y.Z\" line", path)

	return matches[0][1]
}

// TestReleaseFloorMatchesAcrossInstallersAndCanga guards the one value that
// has to read the same in all four places: below it, a release could have
// been recreated with unverified bytes by anyone with contents: write
// (docs/HANDOFF.md, "Immutability is not retroactive", Path B).
func TestReleaseFloorMatchesAcrossInstallersAndCanga(t *testing.T) {
	t.Parallel()

	want := strings.TrimPrefix(upgrade.MinReleaseTag, "v")

	assert.Equal(t, want, installScriptFloor(t, "install_host.sh"), "install_host.sh's min_version")
	assert.Equal(t, want, installScriptFloor(t, "install_sandbox.sh"), "install_sandbox.sh's min_version")
	assert.Equal(t, want, sbxKitPinAttestedFrom(t), "scripts/sbx-kit-pin.sh's attested_from")
}

// releaseFloorEdgeCases is the single source of truth
// TestReleaseFloorShapeAgreesAcrossInstallersAndGo runs through
// install_host.sh, install_sandbox.sh and upgrade.CheckReleaseFloor alike:
// every tag shape round-1 ship-gate finding 1 named (docs/HANDOFF.md,
// "Release floor for installers and canga upgrade (PR #41)"), each with the
// ONE accept/refuse answer all three checks have to agree on.
var releaseFloorEdgeCases = []struct {
	tag      string
	accepted bool
}{
	{tag: "v0.10.5", accepted: true}, // the floor itself
	{tag: "v0.10.6", accepted: true},
	{tag: "v0.10.10", accepted: true}, // a later patch that sorts BELOW .5 as text
	{tag: "v0.11.0", accepted: true},
	{tag: "v1.0.0", accepted: true},
	{tag: "v0.10.4"},       // just below the floor
	{tag: "v0.9.99"},       // below the floor, sorts ABOVE it as text
	{tag: "v0.1.0"},        // well below the floor
	{tag: "v0.10.5-rc1"},   // a pre-release of the floor itself
	{tag: "v0.10.4-rc9"},   // a pre-release below the floor
	{tag: "v1.0.0-rc1"},    // a pre-release of a later major
	{tag: "v0.10.5+build"}, // build metadata
	{tag: "v00.10.5"},      // a leading zero
	{tag: "v0.10"},         // missing the patch component
	{tag: "0.10.5"},        // the goreleaser spelling: no "v"
	{tag: _laterMajor},     // no "v": not covered by the repository's v* tag ruleset
	{tag: ""},              // empty
	{tag: "latest"},        // not a version at all
}

// TestReleaseFloorShapeAgreesAcrossInstallersAndGo runs every
// releaseFloorEdgeCases tag through install_host.sh, install_sandbox.sh and
// upgrade.CheckReleaseFloor, and requires all three to answer accept/refuse
// alike. This is exactly what round-1 ship-gate finding 1 found missing:
// awk's numeric coercion truncated a dash-suffixed field and read a leading
// zero as if it were not there, so both shapes slipped through the shell
// scripts as accepted while Go's semver.Compare correctly refused them.
//
// "Accepted" is measured as "reached the network": every tag here either
// has no release fixture on disk (so an accepted download 404s) or is
// _tag itself (v0.10.5, which succeeds outright) - either way, a request
// was made. A refused tag never reaches curl at all.
//
//nolint:paralleltest // serial so newBins writes the fakes while no other test in this package forks
func TestReleaseFloorShapeAgreesAcrossInstallersAndGo(t *testing.T) {
	bins := newBins(t)

	sha := _sha256sum
	if _, err := exec.LookPath(sha); err != nil {
		sha = _shasum
	}

	bin, ok := bins[sha]
	require.True(t, ok, "%s is not installed here", sha)

	sh, err := exec.LookPath("sh")
	require.NoError(t, err)

	shell := []string{sh}

	hostScript, err := filepath.Abs("install_host.sh")
	require.NoError(t, err)
	sandboxScript, err := filepath.Abs("install_sandbox.sh")
	require.NoError(t, err)

	for _, tt := range releaseFloorEdgeCases {
		t.Run(fmt.Sprintf("%q", tt.tag), func(t *testing.T) {
			t.Parallel()

			goErr := upgrade.CheckReleaseFloor(tt.tag)
			if tt.accepted {
				require.NoError(t, goErr, "upgrade.CheckReleaseFloor")
			} else {
				require.Error(t, goErr, "upgrade.CheckReleaseFloor")
			}

			hostEnv := newEnv(t, bin)
			hostRes := hostEnv.run(t, shell, hostScript, []string{tt.tag},
				"HOME="+hostEnv.root, "FAKE_OS="+_darwin, "FAKE_ARCH="+_amd64)
			assert.Equal(t, tt.accepted, len(hostRes.requests) > 0,
				"install_host.sh %q: reached the network = %v; stderr:\n%s", tt.tag, len(hostRes.requests) > 0, hostRes.stderr)

			sandboxEnv := newEnv(t, bin)
			sandboxRes := sandboxEnv.run(t, shell, sandboxScript, []string{tt.tag},
				"HOME="+sandboxEnv.root, "FAKE_OS="+_linux, "FAKE_ARCH="+_aarch64, "FAKE_UID=0")
			assert.Equal(t, tt.accepted, len(sandboxRes.requests) > 0,
				"install_sandbox.sh %q: reached the network = %v; stderr:\n%s", tt.tag, len(sandboxRes.requests) > 0, sandboxRes.stderr)
		})
	}
}
