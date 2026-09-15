package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "devctl",
		Short: "Developer control tool",
		Long: "devctl runs small, deterministic operations on the repositories and\n" +
			"sandboxes of a working day. Every subcommand is keyed by the repository\n" +
			"the current directory belongs to, derived from its origin remote.",
		Version: fmt.Sprintf("%s (%s, %s)", version, commit, runtime.Version()),
		// An error is a line on stderr, not a wall of help text, and main is the
		// only thing that prints it, so its format stays devctl's own.
		SilenceUsage:  true,
		SilenceErrors: true,
		// NoArgs is what turns `devctl bogus` into "unknown command", and the
		// wrapper makes that exit 2. It only runs if the command is Runnable,
		// which is the whole reason for the RunE below: cobra short-circuits a
		// command with no Run to its help text BEFORE validating arguments, so a
		// bare parent would answer an unknown subcommand with help and exit 0.
		Args: usageArgs(cobra.NoArgs),
		RunE: runHelp,
	}

	root.SetVersionTemplate("{{.Version}}\n")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return errUsage(err) })

	return root
}
