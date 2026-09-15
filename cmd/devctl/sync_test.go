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
	assert.Contains(t, stderr, "fast-forward", "git's own refusal reaches the user")
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

// A fetch that cannot complete is a RUNTIME failure: the remote or the network
// is the problem and the same command is worth running again. Pinned here
// because the code it exits with is documented, and it is reached through
// exitCode's default branch, where nothing else would notice it moving.
//
//nolint:paralleltest // t.Setenv, which the hermetic git config needs, forbids it
func TestSyncCmd_AFailedFetchIsARuntimeFailure(t *testing.T) {
	upstream, local := syncFixture(t)
	require.NoError(t, os.RemoveAll(upstream))

	_, err := execute(t, "sync", "-C", local)
	require.ErrorIs(t, err, repo.ErrFetchFailed)
	assert.Equal(t, exitFailure, exitCode(err))
}

// A branch with nothing to bring in is the common case. It prints nothing at
// all, so a sweep across a machine is not a wall of "already up to date".
//
//nolint:paralleltest // t.Setenv, which the hermetic git config needs, forbids it
func TestSyncCmd_SaysNothingWhenThereIsNothingToDo(t *testing.T) {
	_, local := syncFixture(t)

	// Sync once to catch up, then again with nothing left to do.
	_, _, err := executeSplit(t, "sync", "-C", local)
	require.NoError(t, err)

	stdout, stderr, err := executeSplit(t, "sync", "-C", local)
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

func TestSyncCmd_IsRegistered(t *testing.T) {
	t.Parallel()

	out, err := execute(t, "help", "sync")
	require.NoError(t, err)
	assert.Contains(t, out, "never resets")
	assert.Contains(t, out, "-C")
}
