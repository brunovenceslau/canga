// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeBuild is a program that reports version and role the way
// `canga --version` does. A shell script is enough: what the sanity check
// exercises is that the staged file RUNS and answers for itself, not that it
// is an ELF.
func fakeBuild(version, role string) []byte {
	return []byte("#!/bin/sh\necho \"" + version + " (" + role + ", abc1234, go1.27.0)\"\n")
}

// fakeBinary is fakeBuild for the host build, which most tests run as.
func fakeBinary(version string) []byte {
	return fakeBuild(version, RoleHost)
}

// installedBinary lays down a file standing in for the canga being replaced,
// and returns its path.
func installedBinary(t *testing.T, dir, version string) string {
	t.Helper()

	path := filepath.Join(dir, "canga")
	require.NoError(t, os.WriteFile(path, fakeBinary(version), 0o755))

	return path
}

// stagingLeftovers lists the staging files a run may have abandoned in dir.
func stagingLeftovers(t *testing.T, dir string) []string {
	t.Helper()

	left, err := filepath.Glob(filepath.Join(dir, ".canga-upgrade-*"))
	require.NoError(t, err)

	return left
}

func TestTarget(t *testing.T) {
	t.Parallel()

	path, invoked, err := target()
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(path))

	_, err = os.Stat(path)
	require.NoError(t, err, "the resolved target must be a file that exists")

	// invoked is set only when a symlink was followed, which is exactly what
	// happens on darwin: the test binary lives under /var/folders, and /var is
	// a symlink to /private/var. CI only runs linux, so this arm is the one
	// that would otherwise be discovered on the mac and nowhere else.
	if invoked != "" {
		assert.NotEqual(t, invoked, path)
		assert.True(t, filepath.IsAbs(invoked))
	}
}

func TestReplace(t *testing.T) {
	t.Parallel()

	t.Run("swaps the file and leaves nothing behind", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := installedBinary(t, dir, installedVersion)

		require.NoError(t, replace(t.Context(), path, fakeBinary(newerVersion), newerVersion, RoleHost))

		assert.Equal(t, string(fakeBinary(newerVersion)), readFile(t, path))
		assert.Empty(t, stagingLeftovers(t, dir))

		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, defaultBinaryPerm, info.Mode().Perm(), "the replacement has to be executable")
	})

	// An upgrade changes the version, not the policy. A canga deliberately
	// kept private on a shared host must not come back world-executable.
	t.Run("keeps the mode of the install it replaces", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := installedBinary(t, dir, installedVersion)
		require.NoError(t, os.Chmod(path, 0o700))

		require.NoError(t, replace(t.Context(), path, fakeBinary(newerVersion), newerVersion, RoleHost))

		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	})

	// Preserving a deliberate 0o700 is the point; preserving a 0o777 that came
	// from a zero umask or a FAT volume is preserving a mistake — and it would
	// put a world-writable executable in the install directory for the window
	// between staging and rename, before anything has verified it.
	t.Run("does not carry group and world write bits forward", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := installedBinary(t, dir, installedVersion)
		require.NoError(t, os.Chmod(path, 0o777))

		require.NoError(t, replace(t.Context(), path, fakeBinary(newerVersion), newerVersion, RoleHost))

		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
	})

	// The owner has to be able to run what they just installed, whatever the
	// file that was there said.
	t.Run("restores the owner execute bit", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := installedBinary(t, dir, installedVersion)
		require.NoError(t, os.Chmod(path, 0o644))

		require.NoError(t, replace(t.Context(), path, fakeBinary(newerVersion), newerVersion, RoleHost))

		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o744), info.Mode().Perm())
	})

	// The published v0.1.0 reports itself as "0.1.0" while its tag is installedVersion.
	// Normalizing both sides is what keeps that release installable at all.
	t.Run("accepts the goreleaser spelling of the same release", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := installedBinary(t, dir, "v0.0.1")

		require.NoError(t, replace(t.Context(), path, fakeBinary("0.1.0"), installedVersion, RoleHost))
	})

	t.Run("a binary reporting another version never lands", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := installedBinary(t, dir, installedVersion)

		err := replace(t.Context(), path, fakeBinary("v9.9.9"), newerVersion, RoleHost)
		require.ErrorIs(t, err, ErrWrongBinary)

		assert.Equal(t, string(fakeBinary(installedVersion)), readFile(t, path), "the working binary must survive")
		assert.Empty(t, stagingLeftovers(t, dir))
	})

	// What a wrong-platform download looks like from here: bytes that will not
	// execute at all. Without this check they would take the place of a working
	// install and only be discovered by the next invocation.
	t.Run("a binary that does not run never lands", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := installedBinary(t, dir, installedVersion)

		err := replace(t.Context(), path, []byte("\x7fELF not really"), newerVersion, RoleHost)
		require.ErrorIs(t, err, ErrWrongBinary)

		assert.Equal(t, string(fakeBinary(installedVersion)), readFile(t, path))
		assert.Empty(t, stagingLeftovers(t, dir))
	})

	t.Run("a directory it may not write to", func(t *testing.T) {
		t.Parallel()

		if os.Geteuid() == 0 {
			t.Skip("root ignores the permission bits this case depends on")
		}

		dir := t.TempDir()
		path := installedBinary(t, dir, installedVersion)

		require.NoError(t, os.Chmod(dir, 0o500))
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

		err := replace(t.Context(), path, fakeBinary(newerVersion), newerVersion, RoleHost)
		require.Error(t, err)
		assert.ErrorContains(t, err, "stage a new binary beside",
			"the refusal has to name the staging step, which is where the permission is missing")
	})
}

func TestVerifyRuns(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	t.Run("the expected version", func(t *testing.T) {
		t.Parallel()

		path := installedBinary(t, t.TempDir(), newerVersion)
		require.NoError(t, verifyRuns(t.Context(), path, newerVersion, RoleHost))
	})

	// The check that keeps the host build out of a sandbox even if a release
	// misnamed an archive, and the sandbox build off a host.
	t.Run("the other build of the expected version", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "canga")
		require.NoError(t, os.WriteFile(path, fakeBuild(newerVersion, RoleHost), 0o755))

		err := verifyRuns(t.Context(), path, newerVersion, RoleSandbox)
		require.ErrorIs(t, err, ErrWrongBinary)
		assert.ErrorContains(t, err, "(host)")
	})

	t.Run("a file that is not executable", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(dir, "not-executable")
		require.NoError(t, os.WriteFile(path, fakeBinary(newerVersion), 0o644))

		require.ErrorIs(t, verifyRuns(t.Context(), path, newerVersion, RoleHost), ErrWrongBinary)
	})
}

func TestParseVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		give        string
		wantVersion string
		wantRole    string
	}{
		{name: "host build", give: newerVersion + " (host, 7f80975, go1.27.0)\n", wantVersion: newerVersion, wantRole: RoleHost},
		{name: "sandbox build", give: newerVersion + " (sandbox, 7f80975, go1.27.0)", wantVersion: newerVersion, wantRole: RoleSandbox},
		// devctl and agtctl printed no role, so their commit lands where the
		// role would be. That is no build's name, so the comparison fails.
		{name: "no role", give: installedVersion + " (7f80975, go1.26.0)", wantVersion: installedVersion, wantRole: "7f80975"},
		{name: "version only", give: newerVersion, wantVersion: newerVersion},
		{name: "nothing", give: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			version, role := parseVersion(tt.give)
			assert.Equal(t, tt.wantVersion, version)
			assert.Equal(t, tt.wantRole, role)
		})
	}
}

// readFile is the assertion helper for "what is on disk now".
func readFile(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	require.NoError(t, err)

	return string(content)
}
