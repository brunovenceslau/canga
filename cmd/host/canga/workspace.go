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
			"`sbx env run --clone --auto-approve`; on the right, focused, its\n" +
			"clone, where `canga git clone` puts it. The sandbox command is typed\n" +
			"into the left pane's shell, so the pane stays open when sbx exits,\n" +
			"and sbx must be on that shell's PATH. `--auto-approve` applies the\n" +
			"environment plan without the confirmation prompt, since the workspace\n" +
			"opens focused on the clone pane and a prompt left in the other one\n" +
			"would go unnoticed.\n\n" +
			"The environment directory is envs/<host>/<owner>/<repo>-env inside the\n" +
			"repository that holds the environments, itself a clone under the same\n" +
			"base directory: by default <host>/<owner>/docker-sbx, the same owner's\n" +
			"docker-sbx. The \"-env\" suffix keeps the environment directory's\n" +
			"basename apart from the clone's, so the two are never confused. Set " +
			workspace.EnvsRepoVar + " to another path under the\n" +
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
