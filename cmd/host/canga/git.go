// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"github.com/brunovenceslau/canga/internal/cli"
	"github.com/spf13/cobra"
)

// newGitCmd groups the commands that act on a repository through git. The
// sandbox build refuses the whole group through one stub named "git", so a
// command added here is host-only without touching that build.
func newGitCmd(a *cli.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "git",
		Short: "Clone, sync and wire up git repositories",
		// Both are load-bearing; see the same two fields on the root.
		Args: cli.UsageArgs(cobra.NoArgs),
		RunE: cli.RunHelp,
	}

	cmd.AddCommand(newCloneCmd(), newSetupHooksCmd(a), newSyncCmd(a))

	return cmd
}
