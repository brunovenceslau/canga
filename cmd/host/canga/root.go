// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"runtime"

	"github.com/brunovenceslau/canga/internal/cli"
	"github.com/spf13/cobra"
)

// role names this build in `canga --version`, so a report says which of the
// two builds it came from. The release stays the FIRST field of that output:
// upgrade runs a downloaded binary with --version and compares that field.
const role = "host"

func newRootCmd() *cobra.Command {
	a := &cli.App{}

	root := &cobra.Command{
		Use:   "canga",
		Short: "Repositories, sandboxes and reminders, from the host",
		Long: "canga runs small, deterministic operations on the repositories and\n" +
			"sandboxes of a working day. Every subcommand is keyed by the repository\n" +
			"the current directory belongs to, derived from its origin remote.",
		Version: fmt.Sprintf("%s (%s, %s, %s)", version, role, commit, runtime.Version()),
		// An error is a line on stderr, not a wall of help text, and main is the
		// only thing that prints it, so its format stays canga's own.
		SilenceUsage:  true,
		SilenceErrors: true,
		// NoArgs is what turns `canga bogus` into "unknown command", and the
		// wrapper makes that exit 2. It only runs if the command is Runnable,
		// which is the whole reason for the RunE below: cobra short-circuits a
		// command with no Run to its help text BEFORE validating arguments, so a
		// bare parent would answer an unknown subcommand with help and exit 0.
		Args: cli.UsageArgs(cobra.NoArgs),
		RunE: cli.RunHelp,
	}

	root.SetVersionTemplate("{{.Version}}\n")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return cli.Usage(err) })

	a.BindRepoFlag(root)

	root.AddCommand(newCloneCmd(), cli.NewRemindersCmd(a,
		cli.RemindersAdd, cli.RemindersList, cli.RemindersRemove, cli.RemindersPath, cli.RemindersReorder,
	), newSetupCmd(a), newSyncCmd(a), newUpgradeCmd())

	return root
}
