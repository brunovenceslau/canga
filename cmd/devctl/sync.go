// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"github.com/brunovenceslau/devctl/internal/repo"
	"github.com/spf13/cobra"
)

func newSyncCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Fetch and fast-forward every tracking branch",
		Long: "sync fetches every remote with --prune and --tags, then fast-forwards\n" +
			"each local branch that tracks an upstream.\n\n" +
			"It never resets, forces, merges non-linearly or deletes. A branch that\n" +
			"has diverged from its upstream is reported and left where it is, and a\n" +
			"working tree with modified tracked files stops the run before any branch\n" +
			"is touched. Untracked files do not count as modifications: a\n" +
			"fast-forward never touches one.\n\n" +
			"Everything it does is therefore recoverable, which is what makes it safe\n" +
			"to run across every repository on a machine without reading them first.\n\n" +
			"Use -C to name a repository other than the current directory.",
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := repo.Sync(cmd.Context(), a.repoDir)
			if err != nil {
				return err
			}

			reportSync(cmd, a.repoDir, result)

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
		fprintf(errOut, "devctl: skip (dirty working tree): %s\n", dir)

		return
	}

	for _, branch := range result.Branches {
		if !branch.Advanced {
			// Named rather than silent: this is the one case where sync found
			// something to do and deliberately did not do it.
			fprintf(errOut, "devctl: skip %s (not a fast-forward from %s)\n",
				branch.Branch, branch.Upstream)

			continue
		}

		printf(cmd, "%s\t%s\n", branch.Branch, branch.Upstream)
	}
}
