// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brunovenceslau/devctl/internal/repo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// syncFixture is a clone that is one commit behind its upstream.
func syncFixture(t *testing.T) (upstream, local string) {
	t.Helper()

	hermeticGit(t)

	upstream = filepath.Join(t.TempDir(), "upstream")
	gitRun(t, "init", "-q", "-b", "main", upstream)
	gitCommit(t, upstream, "file", "one\n")

	local = filepath.Join(t.TempDir(), "local")
	gitRun(t, "clone", "-q", upstream, local)
	gitCommit(t, upstream, "file", "two\n")

	return upstream, local
}

// A branch that MOVED is a record on stdout, so a sweep over many repositories
// pipes. Everything else is a diagnostic and belongs on stderr.
//
//nolint:paralleltest // t.Setenv, which the hermetic git config needs, forbids it
func TestSyncCmd_RecordsOnStdout(t *testing.T) {
	_, local := syncFixture(t)

	stdout, stderr, err := executeSplit(t, "sync", "-C", local)
	require.NoError(t, err)
	assert.Equal(t, "main\torigin/main\n", stdout)
	assert.Empty(t, stderr, "a clean sync has nothing to warn about")
}

//nolint:paralleltest // t.Setenv, which the hermetic git config needs, forbids it
func TestSyncCmd_ReportsWhatItRefused(t *testing.T) {
	_, local := syncFixture(t)

	// A local commit diverges the branch, so the fast-forward is refused.
	gitCommit(t, local, "mine", "mine\n")

	stdout, stderr, err := executeSplit(t, "sync", "-C", local)
	require.NoError(t, err, "a diverged branch is a normal outcome, not a failure")
	assert.Empty(t, stdout, "nothing moved, so there is no record")
	assert.Contains(t, stderr, "skip main")
	assert.Contains(t, stderr, "not a fast-forward")
}

//nolint:paralleltest // t.Setenv, which the hermetic git config needs, forbids it
func TestSyncCmd_SkipsADirtyTree(t *testing.T) {
	_, local := syncFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(local, "file"), []byte("mine\n"), 0o600))

	stdout, stderr, err := executeSplit(t, "sync", "-C", local)
	require.NoError(t, err, "a dirty tree exits 0: there is nothing wrong, just nothing to do")
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "dirty working tree")
}

// Pointing sync somewhere that is not a repository is the caller standing in
// the wrong place, which is a usage error rather than a runtime failure.
//
//nolint:paralleltest // t.Setenv, which the hermetic git config needs, forbids it
func TestSyncCmd_ExitCodes(t *testing.T) {
	hermeticGit(t)

	_, err := execute(t, "sync", "-C", t.TempDir())
	require.ErrorIs(t, err, repo.ErrNotARepository)
	assert.Equal(t, exitUsage, exitCode(err))

	// sync takes no positional argument: the repository is named with -C.
	_, err = execute(t, "sync", "/some/path")
	require.Error(t, err)
	assert.Equal(t, exitUsage, exitCode(err))
}

func TestSyncCmd_IsRegistered(t *testing.T) {
	t.Parallel()

	out, err := execute(t, "help", "sync")
	require.NoError(t, err)
	assert.Contains(t, out, "never resets")
	assert.Contains(t, out, "-C")
}
