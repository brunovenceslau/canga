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
			"the left, the repository's sandbox environment directory, running\n" +
			"`sbx env run --clone`; on the right, focused, its clone, where\n" +
			"`canga git clone` puts it. The sandbox command is typed into the left\n" +
			"pane's shell, so the pane stays open when sbx exits, and sbx must be on\n" +
			"that shell's PATH.\n\n" +
			"The environment directory is envs/<host>/<owner>/<repo> inside the\n" +
			"repository that holds the environments, itself a clone under the same\n" +
			"base directory: by default <host>/<owner>/docker-sbx, the same owner's\n" +
			"docker-sbx. Set " + workspace.EnvsRepoVar + " to another path under the\n" +
			"base directory, such as github.com/acme/sandboxes, to use another one.\n\n" +
			"Nothing is created: a missing clone or environment directory is refused\n" +
			"before cmux is called. Run it from a terminal inside cmux, which only\n" +
			"accepts commands from its own terminals by default.",
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
