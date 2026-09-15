// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"context"
	"errors"
	"strings"
)

// ErrFetchFailed reports a fetch that did not complete. It is a runtime
// failure: the remote, the network or the credential is the problem, and the
// same command is worth running again once it is fixed.
var ErrFetchFailed = errors.New("fetch failed")

// BranchResult is what a sync did to one local branch that tracks an upstream.
type BranchResult struct {
	// Branch is the local branch.
	Branch string

	// Upstream is the branch it tracks, as `<remote>/<branch>`.
	Upstream string

	// Advanced reports whether the branch moved. False means the upstream is
	// not an ancestor of it, and the branch was left exactly where it was.
	Advanced bool
}

// SyncResult is what one sync did.
type SyncResult struct {
	// Dirty reports a working tree with modified TRACKED files. Nothing was
	// attempted: Branches is empty, and the tree is untouched.
	Dirty bool

	// Branches is one entry per local branch that tracks an upstream, in git's
	// own order. A branch with no upstream is absent rather than reported as
	// skipped: nothing was ever asked of it.
	Branches []BranchResult
}

// Sync brings every tracking branch of a repository up to its upstream, as far
// as that can be done without losing anything.
//
// It fetches with --prune, then fast-forwards. It NEVER resets, forces, merges
// non-linearly or deletes: a branch that has diverged from its upstream is
// reported and left alone, and a working tree with modified tracked files stops
// the run before any branch is touched.
//
// That is the whole contract. Everything it does is recoverable by definition,
// so it is safe to run across every repository on a machine without looking at
// them first.
func Sync(ctx context.Context, dir string) (SyncResult, error) {
	if _, err := Root(ctx, dir); err != nil {
		return SyncResult{}, err
	}

	// --prune drops remote-tracking refs for branches deleted upstream; --tags
	// brings tags that no fetched branch reaches. The transport hardening
	// applies here because this is the call that talks to a remote.
	_, err := gitWith(ctx, dir, transportFlags, ErrFetchFailed,
		[]string{"fetch", "--all", "--prune", "--tags"})
	if err != nil {
		return SyncResult{}, err
	}

	dirty, err := isDirty(ctx, dir)
	if err != nil {
		return SyncResult{}, err
	}

	if dirty {
		return SyncResult{Dirty: true}, nil
	}

	branches, err := localBranches(ctx, dir)
	if err != nil {
		return SyncResult{}, err
	}

	current := currentBranch(ctx, dir)

	var result SyncResult

	for _, branch := range branches {
		upstream, tracking := upstreamOf(ctx, dir, branch)
		if !tracking {
			continue
		}

		result.Branches = append(result.Branches, BranchResult{
			Branch:   branch,
			Upstream: upstream,
			Advanced: advance(ctx, dir, branch, upstream, branch == current),
		})
	}

	return result, nil
}

// advance moves one branch to its upstream, and reports whether it moved.
//
// The two arms exist because a CHECKED-OUT branch cannot be updated by writing
// its ref: the working tree and the index have to move with it, which is what
// `merge --ff-only` does and what makes it refuse rather than touch a tree it
// would have to change.
//
// The other arm is `fetch . <up>:<br>`, and the missing "+" in front of the
// refspec is the entire safety property: without it git refuses a non
// fast-forward update instead of overwriting the branch. A "+" here would turn
// every diverged branch into lost commits.
//
// A refusal is not an error. It is the expected answer for a branch with local
// commits, and reporting it as a failure would make a sync across many
// repositories fail whenever one of them was mid-work.
func advance(ctx context.Context, dir, branch, upstream string, checkedOut bool) bool {
	args := []string{"fetch", "--quiet", ".", upstream + ":" + branch}
	if checkedOut {
		args = []string{"merge", "--ff-only", "--quiet", upstream}
	}

	_, err := git(ctx, dir, ErrNotARepository, args...)

	return err == nil
}

// isDirty reports whether the working tree has modified TRACKED files.
//
// --untracked-files=no is deliberate: a fast-forward never touches an untracked
// file, and a real working tree almost always holds some — build output, a
// scratch file, an .env — so counting them would make sync a permanent no-op.
func isDirty(ctx context.Context, dir string) (bool, error) {
	out, err := git(ctx, dir, ErrNotARepository, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return false, err
	}

	return out != "", nil
}

// localBranches lists the repository's own branches, in git's order.
func localBranches(ctx context.Context, dir string) ([]string, error) {
	out, err := git(ctx, dir, ErrNotARepository, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return nil, err
	}

	if out == "" {
		return nil, nil
	}

	return strings.Split(out, "\n"), nil
}

// currentBranch is the checked-out branch, or the empty string under a detached
// HEAD.
//
// The failure IS the detached case: `symbolic-ref -q` exits 1 with no output
// when HEAD names a commit rather than a branch. By the time this runs the
// directory has already been proved a repository and a fetch has succeeded in
// it, so there is no other failure left for it to hide. An empty answer routes
// every branch through the fetch arm of advance, which is correct: with no
// branch checked out, no working tree has to move.
func currentBranch(ctx context.Context, dir string) string {
	branch, err := git(ctx, dir, ErrNotARepository, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		return ""
	}

	return branch
}

// upstreamOf resolves the branch a local branch tracks, and reports whether it
// tracks one at all. A branch with no upstream is not a failure: a local-only
// branch is a normal thing to have, and there is nothing to bring it up to.
func upstreamOf(ctx context.Context, dir, branch string) (string, bool) {
	upstream, err := git(ctx, dir, ErrNotARepository,
		"rev-parse", "--abbrev-ref", "--symbolic-full-name", branch+"@{upstream}")
	if err != nil || upstream == "" {
		return "", false
	}

	return upstream, true
}
