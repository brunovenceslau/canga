// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/brunovenceslau/canga/internal/cli"
	"github.com/brunovenceslau/canga/internal/repo"
	"github.com/brunovenceslau/canga/internal/upgrade"
	"github.com/spf13/cobra"
)

// role names this build in `canga --version`, so a report says which of the
// two builds it came from. The release is the FIRST field of that output and
// the role the second: upgrade runs a downloaded binary with --version and
// compares both, so a sandbox never replaces itself with the host build.
const role = "sandbox"

// hostOnly is every command the host build has and this one does not, by
// parent. They are registered as hidden stubs, so running one says where it
// lives instead of reporting an unknown command, and no host code is compiled
// in to back them.
var hostOnly = map[string][]string{
	"":          {"completion", "git", "workspace"},
	"reminders": {"path", "reorder", "rm"},
}

func newRootCmd() *cobra.Command {
	a := &cli.App{BaseDir: repo.HandedBaseDir}

	root := &cobra.Command{
		Use:   "canga",
		Short: "Reminders for an agent in a sandbox",
		Long: "canga, in its sandbox build, reads and adds to the per-repository\n" +
			"reminders that the host build manages, keyed by the repository the\n" +
			"current directory belongs to, and upgrades itself to another release.\n" +
			"The host build keeps git, workspace, completion, and reminders rm,\n" +
			"reorder and path.",
		Version: fmt.Sprintf("%s (%s, %s, %s)", version, role, commit, runtime.Version()),
		// An error is a line on stderr, not a wall of help text, and main is the
		// only thing that prints it.
		SilenceUsage:  true,
		SilenceErrors: true,
		// Both are load-bearing; see the same two fields on the host root.
		Args: cli.UsageArgs(cobra.NoArgs),
		RunE: cli.RunHelp,
		// Nothing in a sandbox loads a completion script, and every command
		// compiled in is one more thing an agent can run.
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}

	root.SetVersionTemplate("{{.Version}}\n")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return cli.Usage(err) })

	a.BindRepoFlag(root)

	// list and add only. rm and reorder stay with the host build: an agent may
	// surface the list and record an idea, but the list is the person's, and
	// an agent must not delete or reorder it.
	reminders := cli.NewRemindersCmd(a, cli.RemindersAdd, cli.RemindersList)
	root.AddCommand(reminders)

	root.AddCommand(newUpgradeCmd())

	for _, name := range hostOnly[""] {
		root.AddCommand(hostOnlyCmd(name))
	}

	for _, name := range hostOnly["reminders"] {
		reminders.AddCommand(hostOnlyCmd(name))
	}

	return root
}

// newUpgradeCmd is the shared upgrade, told what is different about a sandbox:
// who owns the file it replaces, and how long the replacement lasts.
func newUpgradeCmd() *cobra.Command {
	cmd := cli.NewUpgradeCmd(role, version)

	cmd.Long += "\n\n" +
		"In a sandbox, the sbx kit and install_sandbox.sh both install\n" +
		"/usr/local/bin/canga owned by root, so run `sudo canga upgrade`.\n" +
		"The kit's pin, or the tag passed to install_sandbox.sh, picks the\n" +
		"release a sandbox starts with; an upgrade lasts until the sandbox\n" +
		"is provisioned again."
	cmd.Example = "  sudo canga upgrade                # install the newest release\n" +
		"  canga upgrade --check             # say what is available, change nothing\n" +
		"  sudo canga upgrade --tag " + upgrade.MinReleaseTag + "  # install exactly that release"

	return cmd
}

// hostOnlyCmd is a hidden command that exists only to refuse, with exit 2. It
// takes any arguments and flags unparsed, so the refusal is about the command
// and never about how it was spelled.
func hostOnlyCmd(name string) *cobra.Command {
	return &cobra.Command{
		Use:                name,
		Hidden:             true,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// "reminders rm", not "canga reminders rm": main already prefixes
			// every error with the binary's name.
			path := strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()+" ")

			return cli.Usage(fmt.Errorf("%s is %w", path, errHostOnly))
		},
	}
}

// errHostOnly is why a host-only command refuses in this build.
var errHostOnly = errors.New("available in the host build only, not in the sandbox build")
