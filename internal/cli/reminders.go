// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"errors"
	"strings"

	"github.com/brunovenceslau/canga/internal/store"
	"github.com/spf13/cobra"
)

// Subcommand builds one `reminders` verb against an App.
//
// Each build passes the verbs it exposes to NewRemindersCmd, so the set is a
// decision made in one visible place per build. The sandbox build leaves out
// rm, reorder and path: an agent in a sandbox may read the list and add to it,
// but only the person on the host removes or reorders their own reminders.
type Subcommand func(*App) *cobra.Command

// NewRemindersCmd builds the `reminders` command with the given verbs.
func NewRemindersCmd(a *App, verbs ...Subcommand) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "reminders",
		Aliases: []string{"todo"},
		Short:   "Per-repo TODOs that outlive a session",
		Long: "reminders is a per-repository TODO store that survives the session that\n" +
			"wrote it. Every session working on the same repository, in any sandbox,\n" +
			"sees the same list, and each item is a plain file meant to be edited by\n" +
			"hand as readily as by this command.",
		Args: UsageArgs(cobra.NoArgs),
		RunE: RunHelp,
	}

	for _, verb := range verbs {
		cmd.AddCommand(verb(a))
	}

	return cmd
}

// RemindersAdd is `reminders add`.
func RemindersAdd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "add <text>...",
		Short: "Record a reminder and print its id",
		Long: "add records a reminder. The arguments are joined with spaces, so the\n" +
			"text needs no quoting. This is the only subcommand that creates the\n" +
			"store; the rest refuse to bring one into existence just by asking.",
		Args: UsageArgs(cobra.MinimumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.WithNewStore(cmd, func(reminders *store.Store) error {
				item, err := reminders.Add(strings.Join(args, " "))
				if err != nil {
					return err
				}

				Printf(cmd, "%s\n", item.ID)

				return nil
			})
		},
	}
}

// RemindersList is `reminders list`.
func RemindersList(a *App) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List this repository's reminders",
		Long: "list prints one <id><TAB><text> record per line, ordered items first.\n" +
			"The text is the reminder's FIRST LINE: a multi-line reminder keeps the\n" +
			"rest in its file.\n\n" +
			"A repository with no store yet lists nothing and succeeds — having\n" +
			"recorded nothing is not an error.",
		Args: UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := a.WithStore(cmd, func(reminders *store.Store) error {
				items, err := reminders.List()
				if err != nil {
					return err
				}

				for _, item := range items {
					Printf(cmd, "%s\t%s\n", item.ID, item.Summary())
				}

				return nil
			})

			if errors.Is(err, store.ErrNoStore) {
				return nil
			}

			return err
		},
	}
}

// RemindersRemove is `reminders rm`.
func RemindersRemove(a *App) *cobra.Command {
	return &cobra.Command{
		Use:     "rm <id>...",
		Aliases: []string{"remove"},
		Short:   "Remove reminders by id",
		Long: "rm removes each named reminder. Every id is attempted even if an\n" +
			"earlier one fails, so one stale id on the line does not silently skip\n" +
			"the rest; the failures are reported together.",
		Args:              UsageArgs(cobra.MinimumNArgs(1)),
		ValidArgsFunction: a.completeIDs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.WithStore(cmd, func(reminders *store.Store) error {
				var failures []error

				for _, id := range args {
					if err := reminders.Remove(id); err != nil {
						failures = append(failures, err)
					}
				}

				return errors.Join(failures...)
			})
		},
	}
}

// RemindersPath is `reminders path`.
func RemindersPath(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "path [<id>]",
		Short: "Print the store directory, or one reminder's file",
		Long: "path prints where the reminders live, so an editor or a script can be\n" +
			"pointed at them. It creates nothing: with no argument it reports the\n" +
			"store directory whether or not anything has been recorded there yet.",
		Args:              UsageArgs(cobra.MaximumNArgs(1)),
		ValidArgsFunction: a.completeIDs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				// Deliberately does not open the store: asking where something
				// would live must not bring it into being.
				cfg, err := a.Config(cmd.Context())
				if err != nil {
					return err
				}

				Printf(cmd, "%s\n", cfg.Dir)

				return nil
			}

			return a.WithStore(cmd, func(reminders *store.Store) error {
				path, err := reminders.ItemPath(args[0])
				if err != nil {
					return err
				}

				Printf(cmd, "%s\n", path)

				return nil
			})
		},
	}
}

// RemindersReorder is `reminders reorder`.
func RemindersReorder(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "reorder <id>...",
		Short: "Move reminders to the front of the listing",
		Long: "reorder moves the named reminders to the front, in the order given,\n" +
			"and leaves everything else behind them in its existing relative order.\n" +
			"Bumping one item to the top is a single argument; a full reordering is\n" +
			"the same verb with every id listed.\n\n" +
			"It takes no lock. A reorder that loses a race against another writer\n" +
			"recomputes against the winner's result rather than overwriting it.",
		Args:              UsageArgs(cobra.MinimumNArgs(1)),
		ValidArgsFunction: a.completeIDs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.WithStore(cmd, func(reminders *store.Store) error {
				return reminders.Reorder(args)
			})
		},
	}
}

// completeIDs completes a stored id and shows the reminder's first line beside
// the candidate, which is what zsh renders as the description. Producing that
// is the thing a hand-written completion cannot do without reimplementing the
// store, and the reason the completion moved into the binary.
func (a *App) completeIDs(
	cmd *cobra.Command,
	args []string,
	toComplete string,
) ([]string, cobra.ShellCompDirective) {
	var completions []string

	// A completion must never be noisy: outside a repository, or with no store
	// yet, it offers nothing rather than printing a diagnostic into the line the
	// user is still typing. The error is dropped for that reason alone.
	_ = a.WithStore(cmd, func(reminders *store.Store) error {
		items, err := reminders.List()
		if err != nil {
			return err
		}

		named := make(map[string]bool, len(args))
		for _, id := range args {
			named[id] = true
		}

		for _, item := range items {
			if named[item.ID] || !strings.HasPrefix(item.ID, toComplete) {
				continue
			}

			completions = append(completions, item.ID+"\t"+item.Summary())
		}

		return nil
	})

	return completions, cobra.ShellCompDirectiveNoFileComp
}
