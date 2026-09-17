// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"context"
	"errors"
	"strings"
)

var (
	// ErrFetchFailed reports a fetch that did not complete. It is a runtime
	// failure: the remote, the network or the credential is the problem, and the
	// same command is worth running again once it is fixed.
	ErrFetchFailed = errors.New("fetch failed")

	// ErrGitRefused reports a git command that RAN and refused, inside a
	// directory already proved to be a repository.
	//
	// It is separate from ErrNotARepository because the two have opposite
	// answers. A lock held by another git process, a rebase in progress, a
	// branch checked out in a linked worktree: all are retryable runtime
	// failures (exit 1), and reporting them as "not inside a git repository"
	// would be both false and the wrong exit code.
	ErrGitRefused = errors.New("git refused")
)

// BranchState is what a sync did to one branch.
//
// The states start at one, so the zero value names none of them. A result whose
// State was never assigned must not read as BranchUpToDate: that state prints
// nothing, and a missed assignment would vanish from the report instead of
// failing a test.
type BranchState int

const (
	// BranchUpToDate means the upstream was already an ancestor, so there was
	// nothing to bring in. It covers a branch level with its upstream and one
	// that is ahead of it: neither has anything to fast-forward.
	BranchUpToDate BranchState = iota + 1

	// BranchAdvanced means the branch was fast-forwarded onto its upstream. It
	// is only ever reported when the upstream was NOT already an ancestor, so
	// it means the branch moved.
	BranchAdvanced

	// BranchRefused means git would not move the branch. Refusal carries git's
	// own words, because the reason is not always divergence: a branch checked
	// out in a linked worktree, a rebase in progress, or an incoming commit that
	// would overwrite an untracked file all land here.
	BranchRefused

	// BranchUpstreamGone means the branch tracks an upstream that no longer
	// exists, typically one deleted after its pull request merged and then
	// pruned by the fetch. Nothing was attempted. It is REPORTED rather than
	// left out like a branch with no upstream, because it may hold commits that
	// were never pushed anywhere else.
	BranchUpstreamGone
)

// BranchResult is what a sync did to one local branch that tracks an upstream.
type BranchResult struct {
	// Branch is the local branch.
	Branch string

	// Upstream is the branch it tracks, as `<remote>/<branch>`.
	Upstream string

	// State is what happened.
	State BranchState

	// Refusal is git's own explanation, set only when State is BranchRefused.
	Refusal error
}

// SyncResult is what one sync did.
type SyncResult struct {
	// Dirty reports a working tree with modified TRACKED files. Nothing was
	// attempted: Branches is empty, and the tree is untouched.
	Dirty bool

	// Branches is one entry per local branch that tracks an upstream, in git's
	// own order, including one whose upstream is gone. A branch that was never
	// given an upstream is absent rather than reported: nothing was asked of it.
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
// so it is safe to run across every repository on a machine without reading
// them first.
//
// It needs a working tree, so a bare repository is refused rather than fetched
// into: neither arm of advance can move a branch that no tree can follow.
func Sync(ctx context.Context, dir string) (SyncResult, error) {
	if _, err := Root(ctx, dir); err != nil {
		return SyncResult{}, err
	}

	// --prune drops remote-tracking refs for branches deleted upstream; --tags
	// brings tags that no fetched branch reaches. The transport hardening
	// applies here because this is the call that talks to a remote.
	_, err := gitWith(ctx, dir, transportFlags(), ErrFetchFailed,
		"fetch", "--all", "--prune", "--tags")
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

	return syncBranches(ctx, dir)
}

// syncBranches walks the repository's branches once the run has been cleared to
// touch it.
func syncBranches(ctx context.Context, dir string) (SyncResult, error) {
	branches, err := trackingBranches(ctx, dir)
	if err != nil {
		return SyncResult{}, err
	}

	current, err := currentBranch(ctx, dir)
	if err != nil {
		return SyncResult{}, err
	}

	var result SyncResult

	for _, branch := range branches {
		if branch.upstream == "" {
			continue
		}

		if branch.gone {
			result.Branches = append(result.Branches, BranchResult{
				Branch:   branch.name,
				Upstream: branch.upstream,
				State:    BranchUpstreamGone,
			})

			continue
		}

		advanced, err := advance(ctx, dir, branch.name, branch.upstream, current)
		if err != nil {
			// A failure to ASK is not an answer. Returning here is what stops a
			// Ctrl-C from being reported as a dozen branches that would not
			// fast-forward, with an exit code of 0 over the top of it.
			return SyncResult{}, err
		}

		result.Branches = append(result.Branches, advanced)
	}

	return result, nil
}

// advance moves one branch to its upstream, and reports what happened.
//
// It asks first whether there is anything to do. Without that question the two
// arms disagree about the same branch state: for a branch that is AHEAD of its
// upstream, `merge --ff-only` says "Already up to date" and succeeds, while
// `fetch . <up>:<br>` refuses the rewind — so an identical repository would be
// reported as advanced or as refused depending only on which branch happened to
// be checked out.
//
// current is the checked-out branch, empty under a detached HEAD. It is the
// name rather than a bool, so the call reads `advance(…, current)` instead of a
// bare `true` whose meaning lives only in the signature.
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
// commits, and reporting it as a failure would make a sweep across many
// repositories fail whenever one of them was mid-work. A failure to RUN git IS
// an error, and is returned as one — which is the difference between a branch
// git declined to move and a branch nobody ever managed to ask about.
func advance(
	ctx context.Context,
	dir, branch, upstream, current string,
) (BranchResult, error) {
	result := BranchResult{Branch: branch, Upstream: upstream, State: BranchUpToDate}

	nothingToDo, err := isAncestor(ctx, dir, upstream, branch)
	if err != nil {
		return BranchResult{}, err
	}

	if nothingToDo {
		return result, nil
	}

	args := []string{"fetch", "--quiet", ".", upstream + ":" + branch}
	if branch == current {
		args = []string{"merge", "--ff-only", "--quiet", upstream}
	}

	if _, err := git(ctx, dir, ErrGitRefused, args...); err != nil {
		if !errors.Is(err, ErrGitRefused) {
			return BranchResult{}, err
		}

		result.State, result.Refusal = BranchRefused, err

		return result, nil
	}

	result.State = BranchAdvanced

	return result, nil
}

// isAncestor reports whether one ref is already reachable from another, which
// is what "there is nothing to fast-forward" means.
func isAncestor(ctx context.Context, dir, ancestor, descendant string) (bool, error) {
	return gitStatus(ctx, dir, "merge-base", "--is-ancestor", ancestor, descendant)
}

// isDirty reports whether the working tree has modified TRACKED files.
//
// --untracked-files=no is deliberate: a fast-forward does not touch a file you
// have not changed, and a real working tree almost always holds untracked ones
// — build output, a scratch file, an .env — so counting them would make sync a
// permanent no-op. It does not promise that untracked files are never in the
// way: git still refuses a merge whose incoming commit would overwrite one, and
// that refusal is reported like any other.
func isDirty(ctx context.Context, dir string) (bool, error) {
	out, err := git(ctx, dir, ErrGitRefused, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return false, err
	}

	return out != "", nil
}

// trackingBranch is one local branch as for-each-ref describes it.
type trackingBranch struct {
	name string

	// upstream is `<remote>/<branch>`, read from the branch's configuration, so
	// it is set even when the ref it names has been pruned. Empty means the
	// branch was never given one.
	upstream string

	// gone reports an upstream that is configured but no longer exists.
	gone bool
}

// trackingBranches lists the repository's own branches with their upstreams, in
// git's order, in ONE call.
//
// One call rather than a `rev-parse <branch>@{upstream}` per branch, because
// that command exits 128 both for a branch with no upstream and for one whose
// upstream was deleted. Reading that refusal as "no upstream" made a pruned
// branch, possibly holding unpushed work, vanish from the report. for-each-ref answers all three questions
// without a refusal to interpret: the upstream comes from configuration, and
// `upstream:track` says "gone" when its ref does not exist.
func trackingBranches(ctx context.Context, dir string) ([]trackingBranch, error) {
	out, err := git(ctx, dir, ErrGitRefused, "for-each-ref",
		"--format=%(refname:short)%09%(upstream:short)%09%(upstream:track,nobracket)",
		"refs/heads")
	if err != nil {
		return nil, err
	}

	if out == "" {
		return nil, nil
	}

	lines := strings.Split(out, "\n")
	branches := make([]trackingBranch, 0, len(lines))

	for _, line := range lines {
		// A tab cannot appear in a ref name, so the split is unambiguous. The
		// trailing fields are empty rather than missing, but git's output was
		// trimmed, so the last line may have lost them.
		name, rest, _ := strings.Cut(line, "\t")
		upstream, track, _ := strings.Cut(rest, "\t")

		branches = append(branches, trackingBranch{
			name:     name,
			upstream: upstream,
			gone:     track == "gone",
		})
	}

	return branches, nil
}

// currentBranch is the checked-out branch, or the empty string under a detached
// HEAD.
//
// `symbolic-ref -q` exits 1 with no output when HEAD names a commit rather than
// a branch, and that refusal is the detached answer. Every other failure is
// returned: a cancelled context reaching here as "no branch is checked out"
// would silently route every branch through the wrong arm of advance.
//
// An empty answer is correct for the detached case: with no branch checked out,
// no working tree has to move.
func currentBranch(ctx context.Context, dir string) (string, error) {
	branch, err := git(ctx, dir, ErrGitRefused, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		if errors.Is(err, ErrGitRefused) {
			return "", nil
		}

		return "", err
	}

	return branch, nil
}
