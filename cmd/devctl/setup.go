// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"github.com/brunovenceslau/devctl/internal/cli"
	"github.com/brunovenceslau/devctl/internal/hooks"
	"github.com/spf13/cobra"
)

func newSetupCmd(a *cli.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Wire devctl into a repository",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE:  cli.RunHelp,
	}

	cmd.AddCommand(newSetupHooksCmd(a))

	return cmd
}

// reportInstall prints what an install did. Warnings and the summary go to
// stderr; stdout carries only the hook names, one per line, so the result stays
// pipeable.
func reportInstall(cmd *cobra.Command, report hooks.Report, failure error) {
	errOut := cmd.ErrOrStderr()

	for _, warning := range report.Warnings {
		cli.Fprintf(errOut, "devctl: %s\n", warning)
	}

	for _, backup := range report.BackedUp {
		cli.Fprintf(errOut, "devctl: moved aside to %s\n", backup)
	}

	// Nothing touched, nothing to summarize. Mode alone is not the test: it is
	// stamped before any work happens, so a plain refusal would otherwise claim
	// the repository was left half-changed when it was not touched at all.
	changed := len(report.Installed) > 0 || len(report.BackedUp) > 0
	if report.Mode == "" || (failure != nil && !changed) {
		return
	}

	// The summary must not claim success when the run failed part way. What was
	// already installed is still worth naming, and the error itself follows.
	if failure != nil {
		cli.Fprintf(errOut, "devctl: stopped part way through %s in %s\n", report.Mode, report.Root)
	} else {
		cli.Fprintf(errOut, "devctl: installed via %s in %s\n", report.Mode, report.Root)
	}

	for _, name := range report.Installed {
		cli.Printf(cmd, "%s\n", name)
	}
}

func newSetupHooksCmd(a *cli.App) *cobra.Command {
	var options hooks.Options

	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "Install this repository's own git hooks",
		Long: "hooks installs the git hooks a repository keeps under .devctl/hooks.\n\n" +
			"By default it points git's core.hooksPath at that directory, which\n" +
			"covers every hook at once and is undone with `git config --unset\n" +
			"core.hooksPath`. git reads hooks from only ONE directory, so anything\n" +
			"already in .git/hooks stops running; that is reported, and --symlink\n" +
			"links each hook individually instead, which keeps them.\n\n" +
			"The hooks are the repository's own tracked files, so installing them\n" +
			"means its content runs on every commit. Install them in repositories\n" +
			"whose contents you would run anyway.",
		Args: cli.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			report, err := hooks.Install(cmd.Context(), a.RepoDir, options)

			// Reported BEFORE the error is returned, and on both paths: a run
			// that moved a file aside and then failed has already changed the
			// repository, and the user has to hear about it.
			reportInstall(cmd, report, err)

			return err
		},
	}

	cmd.Flags().BoolVar(&options.Symlink, "symlink", false,
		"link each hook into .git/hooks instead of setting core.hooksPath")
	cmd.Flags().BoolVar(&options.Force, "force", false,
		"replace a conflicting setting, moving any file in the way to <name>.bak")

	return cmd
}
