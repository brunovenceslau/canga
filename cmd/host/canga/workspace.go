// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"github.com/brunovenceslau/canga/internal/cli"
	"github.com/brunovenceslau/canga/internal/workspace"
	"github.com/spf13/cobra"
)

func newWorkspaceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "workspace <url>",
		Short: "Open a repository beside its sandbox environment in cmux",
		Long: "workspace opens a new cmux workspace with two panes side by side: on\n" +
			"the left, the repository's sandbox environment directory,\n" +
			"$" + workspace.EnvsDirVar + "/<host>/<owner>/<repo>; on the right, focused, its\n" +
			"clone, where `canga git clone` puts it.\n\n" +
			workspace.EnvsDirVar + " has no default: set it to the directory that holds\n" +
			"the environments. Nothing is created: a missing clone or environment\n" +
			"directory is refused before cmux is called.\n\n" +
			"Run it from a terminal inside cmux, which only accepts commands from its\n" +
			"own terminals by default.",
		Args:              cli.UsageArgs(cobra.ExactArgs(1)),
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := workspace.Resolve(args[0])
			if err != nil {
				return err
			}

			return workspace.OpenCmux(cmd.Context(), target, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}
