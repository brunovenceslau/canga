// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeBinary is a program that reports version the way `devctl --version`
// does. A shell script is enough: what the sanity check exercises is that the
// staged file RUNS and answers for itself, not that it is an ELF.
func fakeBinary(version string) []byte {
	return []byte("#!/bin/sh\necho \"" + version + " (abc1234, go1.27.0)\"\n")
}

// installedBinary lays down a file standing in for the devctl being replaced,
// and returns its path.
func installedBinary(t *testing.T, dir, version string) string {
	t.Helper()

	path := filepath.Join(dir, "devctl")
	require.NoError(t, os.WriteFile(path, fakeBinary(version), 0o755))

	return path
}

// stagingLeftovers lists the staging files a run may have abandoned in dir.
func stagingLeftovers(t *testing.T, dir string) []string {
	t.Helper()

	left, err := filepath.Glob(filepath.Join(dir, ".devctl-upgrade-*"))
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

		require.NoError(t, replace(t.Context(), path, fakeBinary(newerVersion), newerVersion))

		assert.Equal(t, string(fakeBinary(newerVersion)), readFile(t, path))
		assert.Empty(t, stagingLeftovers(t, dir))

		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, binaryPerm, info.Mode().Perm(), "the replacement has to be executable")
	})

	// The published v0.1.0 reports itself as "0.1.0" while its tag is installedVersion.
	// Normalizing both sides is what keeps that release installable at all.
	t.Run("accepts the goreleaser spelling of the same release", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := installedBinary(t, dir, "v0.0.1")

		require.NoError(t, replace(t.Context(), path, fakeBinary("0.1.0"), installedVersion))
	})

	t.Run("a binary reporting another version never lands", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := installedBinary(t, dir, installedVersion)

		err := replace(t.Context(), path, fakeBinary("v9.9.9"), newerVersion)
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

		err := replace(t.Context(), path, []byte("\x7fELF not really"), newerVersion)
		require.ErrorIs(t, err, ErrWrongBinary)

		assert.Equal(t, string(fakeBinary(installedVersion)), readFile(t, path))
		assert.Empty(t, stagingLeftovers(t, dir))
	})

	t.Run("a directory it may not write to", func(t *testing.T) {
		t.Parallel()

		if os.Geteuid() == 0 {
			t.Skip("root ignores the permission bits this case depends on")
		}

		if runtime.GOOS == "windows" {
			t.Skip("mode bits do not gate writes here")
		}

		dir := t.TempDir()
		path := installedBinary(t, dir, installedVersion)

		require.NoError(t, os.Chmod(dir, 0o500))
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

		err := replace(t.Context(), path, fakeBinary(newerVersion), newerVersion)
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
		require.NoError(t, verifyRuns(t.Context(), path, newerVersion))
	})

	t.Run("a file that is not executable", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(dir, "not-executable")
		require.NoError(t, os.WriteFile(path, fakeBinary(newerVersion), 0o644))

		require.ErrorIs(t, verifyRuns(t.Context(), path, newerVersion), ErrWrongBinary)
	})
}

// readFile is the assertion helper for "what is on disk now".
func readFile(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	require.NoError(t, err)

	return string(content)
}
