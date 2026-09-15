// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"github.com/brunovenceslau/devctl/internal/upgrade"
	"github.com/spf13/cobra"
)

func newUpgradeCmd() *cobra.Command {
	var options upgrade.Options

	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Replace this binary with a published release",
		Long: "upgrade downloads the newest devctl release and replaces the running\n" +
			"binary with it, in place.\n\n" +
			"This is NOT dotfiles-upgrade. That one fetches git and updates a\n" +
			"checkout; this one swaps an executable file for a release artifact.\n\n" +
			"The archive is checked against the SHA-256 the release publishes in\n" +
			"checksums.txt. That proves the download is the file the release names\n" +
			"— not that the release is genuine, since the same account publishes\n" +
			"both. The new binary is written beside the old one, run once to\n" +
			"confirm it reports the version it was downloaded as, and only then\n" +
			"renamed over it, so an interrupted upgrade leaves the working binary\n" +
			"untouched.\n\n" +
			"The repository is private, so a GitHub token is required: GH_TOKEN or\n" +
			"GITHUB_TOKEN, or whatever `gh auth token` answers.",
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			token, err := upgrade.Token(cmd.Context())
			if err != nil {
				return err
			}

			options.Current = version
			options.Token = token

			result, err := upgrade.Run(cmd.Context(), options)

			// Reported BEFORE the error is returned: a run that failed after
			// resolving a release has still learned something worth saying, and
			// a run that failed during the swap has to name the file it was
			// working on.
			reportUpgrade(cmd, options, result, err)

			return err
		},
	}

	cmd.Flags().BoolVar(&options.Check, "check", false,
		"report which release is available and change nothing")
	cmd.Flags().StringVar(&options.Tag, "tag", "",
		"install this exact release instead of the newest one")

	return cmd
}

// reportUpgrade prints what a run did. Only the release tag goes to stdout, one
// line, so `v=$(devctl upgrade)` is the version that is now installed;
// everything else is a diagnostic and belongs on stderr.
func reportUpgrade(cmd *cobra.Command, options upgrade.Options, result upgrade.Result, failure error) {
	// Nothing was resolved, so there is nothing to say that the error itself
	// does not already say.
	if result.Release == "" {
		return
	}

	errOut := cmd.ErrOrStderr()

	// Said whenever the two differ, because the file being replaced is then not
	// the path the user typed — ~/.local/bin is full of symlinks.
	if result.Invoked != "" {
		fprintf(errOut, "devctl: %s resolves to %s\n", result.Invoked, result.Path)
	}

	switch {
	case failure != nil:
		// The error itself follows, from main. What this adds is how far the run
		// got, which the error does not carry.
		fprintf(errOut, "devctl: stopped while installing %s over %s\n", result.Release, result.Path)

		return
	case result.Installed:
		fprintf(errOut, "devctl: installed %s over %s at %s\n", result.Release, result.Current, result.Path)
	case options.Check && result.Newer:
		fprintf(errOut, "devctl: %s is available, %s is installed; run `devctl upgrade`\n",
			result.Release, result.Current)
	case options.Check:
		fprintf(errOut, "devctl: %s is the newest release, %s is installed; it would replace %s\n",
			result.Release, result.Current, result.Path)
	default:
		fprintf(errOut, "devctl: %s is the newest release; nothing to do\n", result.Release)
	}

	printf(cmd, "%s\n", result.Release)
}
