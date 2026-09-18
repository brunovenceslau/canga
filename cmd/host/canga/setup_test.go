// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunovenceslau/canga/internal/testrepo"

	"github.com/brunovenceslau/canga/internal/cli"
	"github.com/brunovenceslau/canga/internal/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetupHooks drives the command end to end, because the thing worth
// asserting is what a caller sees: the hook names on stdout so the output
// pipes, and everything else on stderr.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestSetupHooks(t *testing.T) {
	dir := testrepo.New(t, "git@github.com:acme/widget.git")

	source := filepath.Join(dir, filepath.FromSlash(hooks.SourceDir))
	require.NoError(t, os.MkdirAll(source, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(source, "pre-commit"), []byte("#!/bin/sh\n"), 0o755))

	out, err := execute(t, "setup", "hooks", "-C", dir)
	require.NoError(t, err)
	assert.Contains(t, out, "pre-commit")
}

//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestSetupHooks_OutsideARepositoryIsAUsageError(t *testing.T) {
	testrepo.New(t, "git@github.com:acme/widget.git")

	_, err := execute(t, "setup", "hooks", "-C", t.TempDir())
	require.Error(t, err)
	assert.Equal(t, cli.ExitUsage, exitCode(err))
	assert.Contains(t, err.Error(), "not inside a git repository")
}

//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestSetupHooks_NothingToInstallIsARuntimeFailure(t *testing.T) {
	dir := testrepo.New(t, "git@github.com:acme/widget.git")

	_, err := execute(t, "setup", "hooks", "-C", dir)
	require.Error(t, err)
	assert.Equal(t, cli.ExitFailure, exitCode(err))
	assert.Contains(t, err.Error(), hooks.SourceDir)
}

// TestSetupHooks_PartialInstallIsReported covers the half of the fix the
// library tests cannot reach. Reverting the command back to returning the error
// without printing the report leaves every other test green, so without this
// the fix could be undone silently.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestSetupHooks_PartialInstallIsReported(t *testing.T) {
	dir := testrepo.New(t, "git@github.com:acme/widget.git")

	source := filepath.Join(dir, filepath.FromSlash(hooks.SourceDir))
	require.NoError(t, os.MkdirAll(source, 0o755))

	for _, name := range []string{"pre-commit", "pre-push"} {
		require.NoError(t, os.WriteFile(filepath.Join(source, name), []byte("#!/bin/sh\n"), 0o755))
	}

	gitHooks := filepath.Join(dir, ".git", "hooks")
	require.NoError(t, os.MkdirAll(gitHooks, 0o755))

	// pre-commit is backed up and linked; pre-push then fails, because its
	// backup slot is already taken.
	require.NoError(t, os.WriteFile(filepath.Join(gitHooks, "pre-commit"), []byte("mine\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(gitHooks, "pre-push"), []byte("other\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(gitHooks, "pre-push.bak"), []byte("pristine\n"), 0o755))

	stdout, stderr, err := executeSplit(t, "setup", "hooks", "-C", dir, "--symlink", "--force")
	require.Error(t, err)

	assert.Contains(t, stderr, "pre-commit.bak", "the rename already made must be reported")
	assert.Contains(t, stderr, "stopped part way")
	assert.Contains(t, stdout, "pre-commit", "what was installed belongs on stdout")
	assert.NotContains(t, stdout, "stopped part way", "only records go to stdout")
}

// TestSetupHooks_RefusalClaimsNoChange: a plain refusal must not say the
// repository was left half-changed, because nothing was touched at all.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestSetupHooks_RefusalClaimsNoChange(t *testing.T) {
	dir := testrepo.New(t, "git@github.com:acme/widget.git")

	source := filepath.Join(dir, filepath.FromSlash(hooks.SourceDir))
	require.NoError(t, os.MkdirAll(source, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(source, "pre-commit"), []byte("#!/bin/sh\n"), 0o755))

	gitHooks := filepath.Join(dir, ".git", "hooks")
	require.NoError(t, os.MkdirAll(gitHooks, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(gitHooks, "pre-commit"), []byte("mine\n"), 0o755))

	stdout, stderr, err := executeSplit(t, "setup", "hooks", "-C", dir, "--symlink")
	require.Error(t, err)
	assert.Equal(t, cli.ExitUsage, exitCode(err), "a conflict is fixed by changing the command, not repeating it")
	assert.NotContains(t, stderr, "stopped part way")
	assert.NotContains(t, stderr, "installed via")
	assert.Empty(t, stdout)
}

//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestSetupHooks_UsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"setup", "bogus"},
		{"setup", "hooks", "extra"},
		{"setup", "hooks", "--nope"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, err := execute(t, args...)
			require.Error(t, err)
			assert.Equal(t, cli.ExitUsage, exitCode(err))
		})
	}
}
