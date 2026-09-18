// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"github.com/brunovenceslau/canga/internal/cli"
	"github.com/brunovenceslau/canga/internal/repo"
	"github.com/spf13/cobra"
)

func newSyncCmd(a *cli.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Fetch and fast-forward every tracking branch",
		Long: "sync fetches every remote with --prune and --tags, then fast-forwards\n" +
			"each local branch that tracks an upstream.\n\n" +
			"It never resets, forces, merges non-linearly or deletes. A branch that\n" +
			"has diverged from its upstream is reported and left where it is, and a\n" +
			"working tree with modified tracked files stops the run before any branch\n" +
			"is touched. Untracked files do not make a tree dirty, because a\n" +
			"fast-forward does not touch a file you have not changed; git still\n" +
			"refuses a branch whose incoming commit would overwrite one, and that\n" +
			"refusal is reported in git's own words.\n\n" +
			"Everything it does is therefore recoverable, which is what makes it safe\n" +
			"to run across every repository on a machine without reading them first.\n\n" +
			"Use -C to name a repository other than the current directory.",
		Args: cli.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := repo.Sync(cmd.Context(), a.RepoDir)
			if err != nil {
				return err
			}

			reportSync(cmd, a.RepoDir, result)

			return nil
		},
	}

	return cmd
}

// reportSync prints what a sync did. Branches that MOVED are records on stdout,
// one `<branch><TAB><upstream>` per line, so a sweep across many repositories
// pipes; everything else is a diagnostic on stderr.
func reportSync(cmd *cobra.Command, dir string, result repo.SyncResult) {
	errOut := cmd.ErrOrStderr()

	if result.Dirty {
		cli.Fprintf(errOut, "canga: skip (dirty working tree): %s\n", dir)

		return
	}

	for _, branch := range result.Branches {
		switch branch.State {
		case repo.BranchAdvanced:
			cli.Printf(cmd, "%s\t%s\n", branch.Branch, branch.Upstream)

		case repo.BranchRefused:
			// git's own words, not a guess at them. The reason is not always
			// divergence — a branch checked out in a linked worktree and an
			// incoming commit that would overwrite an untracked file both land
			// here, and "not a fast-forward" would send the user looking for
			// something that is not there.
			cli.Fprintf(errOut, "canga: skip %s: %v\n", branch.Branch, branch.Refusal)

		case repo.BranchUpstreamGone:
			// Reported, not dropped: the upstream was usually deleted after a
			// merge, and the branch may still hold commits that were not.
			cli.Fprintf(errOut, "canga: skip %s: upstream %s is gone\n", branch.Branch, branch.Upstream)

		case repo.BranchUpToDate:
			// Silent on purpose: a branch with nothing to bring in is the
			// common case, and a sweep across a machine would be all noise.
		}
	}
}
