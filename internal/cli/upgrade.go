// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"errors"

	"github.com/brunovenceslau/canga/internal/upgrade"
	"github.com/spf13/cobra"
)

// NewUpgradeCmd is `canga upgrade` for the build named by role, which replaces
// itself with the same build of a newer release. version is what the running
// binary was stamped with.
//
// It lives here rather than in either main package because both builds carry
// it, and the two must not drift in how they choose and verify a release.
func NewUpgradeCmd(role, version string) *cobra.Command {
	options := upgrade.Options{Role: role, Current: version}

	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Replace this binary with a published release",
		// The role is spelled out, not described: it is the one thing that
		// differs between the builds, and the help is where a test can see
		// which role a build actually passed in.
		Long: "upgrade downloads the newest canga release and replaces the running\n" +
			"binary with that release's " + role + " build, in place.\n\n" +
			"This is NOT dotfiles-upgrade. That one fetches git and updates a\n" +
			"checkout; this one swaps an executable file for a release artifact.\n\n" +
			"The archive is checked against the SHA-256 the release publishes in\n" +
			"checksums.txt. That proves the download is the file the release names\n" +
			"— not that the release is genuine, since the same account publishes\n" +
			"both. The new binary is written beside the old one, run once to\n" +
			"confirm it reports the version and build it was downloaded as, and\n" +
			"only then renamed over it, so an interrupted upgrade leaves the\n" +
			"working binary untouched.\n\n" +
			"A GitHub token is optional. GH_TOKEN, GITHUB_TOKEN or whatever `gh\n" +
			"auth token` answers is used when one is there, which raises GitHub's\n" +
			"rate limit; without one the release is read anonymously.",
		Args: UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			options.Token = upgrade.Token(cmd.Context())

			result, err := upgrade.Run(cmd.Context(), options)

			// Reported BEFORE the error is returned: a run that failed after
			// resolving a release has still learned something worth saying, and
			// a run that failed during the swap has to name the file it was
			// working on.
			reportUpgrade(cmd, options, result, err)

			// Both are answered by naming a release on the command line, so
			// they are bad invocations: running the same command again cannot
			// fix them.
			if errors.Is(err, upgrade.ErrNotRelease) || errors.Is(err, upgrade.ErrBadTag) {
				return Usage(err)
			}

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
// line, so `v=$(canga upgrade)` is the version that is now installed;
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
		Fprintf(errOut, "canga: %s resolves to %s\n", result.Invoked, result.Path)
	}

	switch {
	case failure != nil:
		// The error itself follows, from main. What this adds is how far the run
		// got, which the error does not carry.
		Fprintf(errOut, "canga: stopped while installing %s over %s\n", result.Release, result.Path)

		return
	case result.Installed:
		Fprintf(errOut, "canga: installed %s over %s at %s\n", result.Release, result.Current, result.Path)
	case options.Check:
		Fprintf(errOut, "canga: %s\n", checkSummary(result))
		// On its own line, and on BOTH branches. Saying what would happen
		// without saying where is half an answer, and the file replaced is
		// resolved through symlinks, so it is not always the path that was typed.
		Fprintf(errOut, "canga: it would replace %s\n", result.Path)
	default:
		Fprintf(errOut, "canga: %s is the newest release; nothing to do\n", result.Release)
	}

	Printf(cmd, "%s\n", result.Release)
}

// checkSummary is the line --check prints above the path.
func checkSummary(result upgrade.Result) string {
	if result.Newer {
		return result.Release + " is available, " + result.Current + " is installed; run `canga upgrade`"
	}

	return result.Release + " is the newest release, " + result.Current + " is installed"
}
