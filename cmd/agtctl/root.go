// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"runtime"

	"github.com/brunovenceslau/devctl/internal/cli"
	"github.com/spf13/cobra"
)

func newRootCmd() *cobra.Command {
	a := &cli.App{Tool: "agtctl"}

	root := &cobra.Command{
		Use:   "agtctl",
		Short: "Agent control tool",
		Long: "agtctl is devctl's counterpart for agents inside a sandbox. It reads and\n" +
			"adds to the same per-repository reminders devctl manages on the host,\n" +
			"keyed by the repository the current directory belongs to.",
		Version: fmt.Sprintf("%s (%s, %s)", version, commit, runtime.Version()),
		// An error is a line on stderr, not a wall of help text, and main is the
		// only thing that prints it.
		SilenceUsage:  true,
		SilenceErrors: true,
		// Both are load-bearing; see the same two fields on devctl's root.
		Args: cli.UsageArgs(cobra.NoArgs),
		RunE: cli.RunHelp,
		// Nothing in a sandbox loads a completion script, and every command
		// compiled in is one more thing an agent can run.
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}

	root.SetVersionTemplate("{{.Version}}\n")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return cli.Usage(err) })

	a.BindRepoFlag(root)

	// list and add only. rm and reorder stay with devctl on the host: an agent
	// may surface the list and record an idea, but the list is the person's,
	// and an agent must not delete or reorder it.
	root.AddCommand(cli.NewRemindersCmd(a, cli.RemindersAdd, cli.RemindersList))

	return root
}
