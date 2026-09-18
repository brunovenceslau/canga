// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"

	"github.com/brunovenceslau/canga/internal/cli"
	"github.com/brunovenceslau/canga/internal/repo"
	"github.com/spf13/cobra"
)

func newCloneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clone <url> [dir]",
		Short: "Clone a repository into the deterministic layout",
		Long: "clone puts a repository at ${DEVCTL_BASE_DIR:-$HOME/src} followed by the\n" +
			"<host>/<owner>/<repo> derived from its URL, so a repository lands at the\n" +
			"same path whatever protocol it was cloned with, on every machine. Name a\n" +
			"directory to override that; a relative one is resolved against the\n" +
			"current directory.\n\n" +
			"The resolved path is printed on stdout and nothing else is, so `cd\n" +
			"$(canga clone <url>)` works. It refuses a target that already holds\n" +
			"anything, and never merges into or overwrites an existing tree.\n\n" +
			"The clone is hardened at the transport level: the ext and fd remote\n" +
			"helpers, which run a command, are turned off on the command line, so no\n" +
			"git configuration can turn them back on, and are removed from\n" +
			"GIT_ALLOW_PROTOCOL, which would otherwise outrank that. Objects are\n" +
			"checked on both sides of the fetch.\n\n" +
			"Afterwards SSH signing is written into the clone's own config, from\n" +
			"DEVCTL_SIGNING_KEY and DEVCTL_ALLOWED_SIGNERS, or from the machine's\n" +
			"global git config. When neither resolves nothing is stamped, and the\n" +
			"command says whether git configuration outside the clone signs anyway.",
		Args:              cli.UsageArgs(cobra.RangeArgs(1, 2)),
		ValidArgsFunction: completeCloneArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			options := repo.CloneOptions{
				// git's own output goes to stderr, INCLUDING what it writes on
				// stdout, so the path this prints is the only thing on stdout and
				// stays safe to substitute into a command.
				Out: cmd.ErrOrStderr(),
				Err: cmd.ErrOrStderr(),
				// Under scripted provisioning the `\r` progress meter garbles
				// whatever pane is collecting the log, and nobody is watching it.
				NoProgress: os.Getenv("CI") != "",
			}

			if len(args) == 2 {
				options.Dir = args[1]
			}

			result, err := repo.Clone(cmd.Context(), args[0], options)
			if err != nil {
				return err
			}

			reportSigning(cmd, result)
			cli.Printf(cmd, "%s\n", result.Dir)

			return nil
		},
	}

	return cmd
}

// reportSigning says what the clone was left configured to do. All of it goes to
// stderr: stdout carries the path alone.
func reportSigning(cmd *cobra.Command, result repo.CloneResult) {
	errOut := cmd.ErrOrStderr()

	// A clone that exists but could not be stamped is still a clone. It is
	// reported rather than returned for that reason, and named precisely enough
	// that one `git config` in that directory finishes the job.
	if result.StampErr != nil {
		cli.Fprintf(errOut, "canga: warning: could not stamp signing config in %s: %v\n",
			result.Dir, result.StampErr)

		return
	}

	if result.Signing.Key != "" {
		cli.Fprintf(errOut, "canga: signing on (commit.gpgsign=true)\n")

		return
	}

	if result.Signing.Inherited {
		cli.Fprintf(errOut, "canga: signing on (commit.gpgsign=true, inherited from git config "+
			"outside the clone)\n")

		return
	}

	cli.Fprintf(errOut, "canga: signing OFF — set DEVCTL_SIGNING_KEY or a global "+
		"user.signingkey to sign here\n")
}

// completeCloneArgs offers directories for the optional target and nothing at
// all for the URL: a half-typed URL is not a path, and a shell that falls back
// to file completion on it would offer the contents of the current directory.
func completeCloneArgs(
	_ *cobra.Command,
	args []string,
	_ string,
) ([]string, cobra.ShellCompDirective) {
	if len(args) == 1 {
		return nil, cobra.ShellCompDirectiveFilterDirs
	}

	// The URL, and anything past the target, which the command refuses anyway.
	return nil, cobra.ShellCompDirectiveNoFileComp
}
