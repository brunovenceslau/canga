// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every case here is one of dotfiles-host's tests/dev_test.sh, so the two
// implementations cannot drift while both exist.

// mainBranch is the branch every fixture below starts on.
const mainBranch = "main"

// syncPair is an upstream repository and a clone of it tracking its main.
func syncPair(t *testing.T) (upstream, local string) {
	t.Helper()

	upstream = filepath.Join(t.TempDir(), "upstream")
	runGit(t, "init", "-q", "-b", mainBranch, upstream)
	commitFile(t, upstream, "file", "one\n")

	local = filepath.Join(t.TempDir(), "local")
	runGit(t, "clone", "-q", upstream, local)

	return upstream, local
}

// gitSay runs a git command for its output, for the assertions that compare
// what two repositories point at.
func gitSay(t *testing.T, args ...string) string {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "git", args...).Output()
	require.NoErrorf(t, err, "git %v", args)

	return string(out)
}

// head is what a ref points at, for asserting that a branch moved, or did not.
func head(t *testing.T, dir, ref string) string {
	t.Helper()

	return gitSay(t, "-C", dir, "rev-parse", ref)
}

//nolint:paralleltest // t.Setenv, which the hermetic git config needs, forbids it
func TestSync(t *testing.T) {
	t.Run("fast-forwards the checked-out branch", func(t *testing.T) {
		hermeticGit(t)

		upstream, local := syncPair(t)
		commitFile(t, upstream, "file", "two\n")

		result, err := Sync(t.Context(), local)
		require.NoError(t, err)
		require.False(t, result.Dirty)
		require.Len(t, result.Branches, 1)
		assert.Equal(t, mainBranch, result.Branches[0].Branch)
		assert.Equal(t, "origin/"+mainBranch, result.Branches[0].Upstream)
		assert.Equal(t, BranchAdvanced, result.Branches[0].State)

		assert.Equal(t, head(t, upstream, "HEAD"), head(t, local, "HEAD"))
		// The merge arm has to move the working tree with the ref, or the
		// branch is advanced and the files are not.
		content, err := os.ReadFile(filepath.Join(local, "file"))
		require.NoError(t, err)
		assert.Equal(t, "two\n", string(content))
	})

	// A real working tree almost always carries untracked files, so counting
	// them as dirty would make sync a permanent no-op.
	t.Run("an untracked file does not block the sync", func(t *testing.T) {
		hermeticGit(t)

		upstream, local := syncPair(t)
		commitFile(t, upstream, "file", "two\n")
		require.NoError(t, os.WriteFile(filepath.Join(local, "scratch"), []byte("x"), 0o600))

		result, err := Sync(t.Context(), local)
		require.NoError(t, err)
		assert.False(t, result.Dirty)
		require.Len(t, result.Branches, 1)
		assert.Equal(t, BranchAdvanced, result.Branches[0].State)
		assert.FileExists(t, filepath.Join(local, "scratch"), "and it is still there")
	})

	// The tree is not touched, and no branch is attempted: a fast-forward of the
	// checked-out branch would have to move files the user is working on.
	t.Run("a dirty tree is skipped and left alone", func(t *testing.T) {
		hermeticGit(t)

		upstream, local := syncPair(t)
		commitFile(t, upstream, "file", "two\n")

		require.NoError(t, os.WriteFile(filepath.Join(local, "file"), []byte("mine\n"), 0o600))
		before := head(t, local, "HEAD")

		result, err := Sync(t.Context(), local)
		require.NoError(t, err)
		assert.True(t, result.Dirty)
		assert.Empty(t, result.Branches, "nothing was attempted")
		assert.Equal(t, before, head(t, local, "HEAD"))

		content, err := os.ReadFile(filepath.Join(local, "file"))
		require.NoError(t, err)
		assert.Equal(t, "mine\n", string(content), "the working tree is untouched")
	})

	// The property the whole command rests on: a branch with local commits is
	// reported and left where it is. Never reset, never forced.
	t.Run("a diverged checked-out branch is preserved", func(t *testing.T) {
		hermeticGit(t)

		upstream, local := syncPair(t)
		commitFile(t, upstream, "file", "theirs\n")
		commitFile(t, local, "mine", "mine\n")
		before := head(t, local, "HEAD")

		result, err := Sync(t.Context(), local)
		require.NoError(t, err)
		require.Len(t, result.Branches, 1)
		assert.Equal(t, BranchRefused, result.Branches[0].State)
		require.Error(t, result.Branches[0].Refusal)
		assert.Contains(t, result.Branches[0].Refusal.Error(), "fast-forward",
			"git's own words travel, rather than a guess at them")
		assert.Equal(t, before, head(t, local, "HEAD"), "the local commit is still there")
	})
	// A branch that is AHEAD of its upstream has nothing to bring in. It must
	// read the same way on both arms of advance, and `merge --ff-only` answering
	// "Already up to date" with exit 0 is exactly what would otherwise report it
	// as a branch that moved.
	t.Run("a branch ahead of its upstream has nothing to do", func(t *testing.T) {
		hermeticGit(t)

		_, local := syncPair(t)
		commitFile(t, local, "mine", "mine\n")
		before := head(t, local, "HEAD")

		result, err := Sync(t.Context(), local)
		require.NoError(t, err)
		require.Len(t, result.Branches, 1)
		assert.Equal(t, BranchUpToDate, result.Branches[0].State)
		assert.Equal(t, before, head(t, local, "HEAD"))
	})

	// The same state, on the other arm. Before the ancestor check these two
	// disagreed: this one was refused while the one above claimed to advance.
	t.Run("an ahead branch that is not checked out reads the same way", func(t *testing.T) {
		hermeticGit(t)

		upstream, local := syncPair(t)
		runGit(t, "-C", upstream, "checkout", "-q", "-b", "feature")
		commitFile(t, upstream, "feature-file", "f\n")
		runGit(t, "-C", upstream, "checkout", "-q", mainBranch)

		runGit(t, "-C", local, "fetch", "-q", "origin")
		runGit(t, "-C", local, "branch", "feature", "origin/feature")
		runGit(t, "-C", local, "checkout", "-q", "feature")
		commitFile(t, local, "local-only", "l\n")
		runGit(t, "-C", local, "checkout", "-q", mainBranch)

		result, err := Sync(t.Context(), local)
		require.NoError(t, err)
		assert.Equal(t, BranchUpToDate, stateOf(t, result, "feature"))
	})
}

// TestSync_OtherBranches covers the arm that needs no working tree: a branch
// that is not the checked-out one is updated by writing its ref.
//
//nolint:paralleltest // t.Setenv, which the hermetic git config needs, forbids it
func TestSync_OtherBranches(t *testing.T) {
	t.Run("fast-forwards a branch that is not checked out", func(t *testing.T) {
		hermeticGit(t)

		upstream, local := syncPair(t)
		runGit(t, "-C", upstream, "checkout", "-q", "-b", "feature")
		commitFile(t, upstream, "feature-file", "f\n")
		runGit(t, "-C", upstream, "checkout", "-q", mainBranch)

		runGit(t, "-C", local, "fetch", "-q", "origin")
		runGit(t, "-C", local, "branch", "feature", "origin/feature")

		// Now move the upstream's feature branch on, while local stays on main.
		runGit(t, "-C", upstream, "checkout", "-q", "feature")
		commitFile(t, upstream, "feature-file", "ff\n")
		runGit(t, "-C", upstream, "checkout", "-q", mainBranch)

		result, err := Sync(t.Context(), local)
		require.NoError(t, err)
		checkedOut, err := currentBranch(t.Context(), local)
		require.NoError(t, err)
		assert.Equal(t, mainBranch, checkedOut, "sync never changes the checkout")
		assert.Equal(t, head(t, upstream, "feature"), head(t, local, "feature"))
		assert.Equal(t, BranchAdvanced, stateOf(t, result, "feature"))
	})

	t.Run("a diverged branch that is not checked out is preserved", func(t *testing.T) {
		hermeticGit(t)

		upstream, local := syncPair(t)
		runGit(t, "-C", upstream, "checkout", "-q", "-b", "feature")
		commitFile(t, upstream, "feature-file", "f\n")
		runGit(t, "-C", upstream, "checkout", "-q", mainBranch)

		runGit(t, "-C", local, "fetch", "-q", "origin")
		runGit(t, "-C", local, "branch", "feature", "origin/feature")

		// Diverge both sides of feature, and leave local standing on main.
		runGit(t, "-C", local, "checkout", "-q", "feature")
		commitFile(t, local, "local-only", "l\n")
		runGit(t, "-C", local, "checkout", "-q", mainBranch)

		runGit(t, "-C", upstream, "checkout", "-q", "feature")
		commitFile(t, upstream, "feature-file", "ff\n")
		runGit(t, "-C", upstream, "checkout", "-q", mainBranch)

		before := head(t, local, "feature")

		result, err := Sync(t.Context(), local)
		require.NoError(t, err)
		assert.Equal(t, BranchRefused, stateOf(t, result, "feature"))
		assert.Equal(t, before, head(t, local, "feature"))
	})

	// With no branch checked out there is no working tree to move, so every
	// branch routes through the fetch arm. It must not be an error.
	t.Run("a detached HEAD is not an error", func(t *testing.T) {
		hermeticGit(t)

		upstream, local := syncPair(t)
		commitFile(t, upstream, "file", "two\n")
		runGit(t, "-C", local, "checkout", "-q", "--detach")

		result, err := Sync(t.Context(), local)
		require.NoError(t, err)
		require.Len(t, result.Branches, 1)
		assert.Equal(t, BranchAdvanced, result.Branches[0].State)
		assert.Equal(t, head(t, upstream, "HEAD"), head(t, local, mainBranch))
	})

	// A local-only branch is a normal thing to have. It is absent from the
	// result rather than reported as skipped: nothing was asked of it.
	t.Run("a branch with no upstream is left out", func(t *testing.T) {
		hermeticGit(t)

		upstream, local := syncPair(t)
		commitFile(t, upstream, "file", "two\n")
		runGit(t, "-C", local, "branch", "local-only")

		result, err := Sync(t.Context(), local)
		require.NoError(t, err)
		require.Len(t, result.Branches, 1, "only the tracking branch is reported")
		assert.Equal(t, mainBranch, result.Branches[0].Branch)
		assert.Equal(t, BranchAdvanced, result.Branches[0].State, "and it still synced")
	})
}

// TestSync_Refusals covers what stops a run outright, as opposed to the branch
// states it reports and leaves alone.
//
//nolint:paralleltest // t.Setenv, which the hermetic git config needs, forbids it
func TestSync_Refusals(t *testing.T) {
	t.Run("not a repository", func(t *testing.T) {
		hermeticGit(t)

		_, err := Sync(t.Context(), t.TempDir())
		require.ErrorIs(t, err, ErrNotARepository)
	})

	// A fetch that cannot complete is a runtime failure, not a skip: the branch
	// states below it would be decided against a stale view of the remote.
	t.Run("a failed fetch stops the run", func(t *testing.T) {
		hermeticGit(t)

		upstream, local := syncPair(t)
		require.NoError(t, os.RemoveAll(upstream))

		_, err := Sync(t.Context(), local)
		require.ErrorIs(t, err, ErrFetchFailed)
	})

	// A bare repository has no working tree, so neither arm of advance can move
	// a branch in it. The zsh original gated on `rev-parse --git-dir`, which a
	// bare repository passes, and then failed branch by branch; this refuses up
	// front, and git's own words say why.
	t.Run("a bare repository is refused", func(t *testing.T) {
		hermeticGit(t)

		bare := filepath.Join(t.TempDir(), "bare.git")
		runGit(t, "init", "-q", "--bare", bare)

		_, err := Sync(t.Context(), bare)
		require.ErrorIs(t, err, ErrNotARepository)
		assert.Contains(t, err.Error(), "work tree")
	})

	// The one a review found: every helper below the fetch answers a failure
	// with a legitimate-looking value — "not a fast-forward", "detached HEAD",
	// "no upstream" — so a Ctrl-C used to be reported as a dozen refused
	// branches and an exit code of 0. A failure to ASK is not an answer.
	t.Run("a cancelled context is never read as an answer", func(t *testing.T) {
		hermeticGit(t)

		_, local := syncPair(t)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := advance(ctx, local, mainBranch, "origin/"+mainBranch, mainBranch)
		require.ErrorIs(t, err, context.Canceled)

		_, err = currentBranch(ctx, local)
		require.ErrorIs(t, err, context.Canceled)

		_, _, err = upstreamOf(ctx, local, mainBranch)
		require.ErrorIs(t, err, context.Canceled)

		_, err = isDirty(ctx, local)
		require.ErrorIs(t, err, context.Canceled)

		_, err = Sync(ctx, local)
		require.ErrorIs(t, err, context.Canceled)
	})
}

// A result whose State was never assigned must not pass for one that was: the
// up-to-date state prints nothing, so a missed assignment would disappear.
func TestBranchState_ZeroIsNoState(t *testing.T) {
	t.Parallel()

	var unset BranchState

	assert.NotContains(t, []BranchState{BranchUpToDate, BranchAdvanced, BranchRefused}, unset)
}

// stateOf reports what a run did to one named branch.
func stateOf(t *testing.T, result SyncResult, branch string) BranchState {
	t.Helper()

	for _, got := range result.Branches {
		if got.Branch == branch {
			return got.State
		}
	}

	t.Fatalf("branch %q is not in the result", branch)

	return 0
}
