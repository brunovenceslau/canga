package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/brunovenceslau/devctl/internal/repo"
	"github.com/brunovenceslau/devctl/internal/store"
	"github.com/spf13/cobra"
)

// scopeRepo is the only scope that ships. The <scope> path segment exists from
// the first release so that adding a second one later needs no migration of an
// already-populated store — not because there is a second one now.
const scopeRepo = "repo"

// app is what every subcommand needs in order to find the right store.
type app struct {
	// repoDir is the working tree whose origin identifies the repository. It is
	// a flag rather than always the process's directory so a caller — a hook, a
	// script, a test — can name the repository without chdir'ing into it.
	repoDir string
}

func newRootCmd() *cobra.Command {
	a := &app{}

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

	root.PersistentFlags().StringVarP(&a.repoDir, "repo", "C", ".",
		"git repository whose reminders to operate on")

	root.AddCommand(newRemindersCmd(a))

	return root
}

// config derives the store's identity for the repository devctl was pointed at.
// It touches the filesystem only to ask git for the origin URL.
func (a *app) config(ctx context.Context) (store.Config, error) {
	origin, err := repo.Origin(ctx, a.repoDir)
	if err != nil {
		return store.Config{}, err
	}

	name, err := repo.Path(origin)
	if err != nil {
		return store.Config{}, err
	}

	base, err := remindersBaseDir()
	if err != nil {
		return store.Config{}, err
	}

	return store.Config{
		// EscapePath on the DIRECTORY only. The store has to mean the same
		// thing on a case-insensitive APFS and a case-sensitive sandbox
		// filesystem, which is the boundary it is meant to be shared across.
		// Repo below keeps the readable spelling, because that is what lands in
		// each reminder's header and what a clone is named after.
		Dir:   filepath.Join(base, filepath.FromSlash(repo.EscapePath(name)), scopeRepo),
		Repo:  name,
		Scope: scopeRepo,
	}, nil
}

// withStore hands fn an already-open store, and fails with store.ErrNoStore if
// the repository has none yet.
func (a *app) withStore(cmd *cobra.Command, fn func(*store.Store) error) error {
	return a.open(cmd, store.OpenExisting, fn)
}

// withNewStore is withStore for the one operation that may bring a store into
// existence.
func (a *app) withNewStore(cmd *cobra.Command, fn func(*store.Store) error) error {
	return a.open(cmd, store.Open, fn)
}

func (a *app) open(
	cmd *cobra.Command,
	opener func(store.Config, ...store.Option) (*store.Store, error),
	fn func(*store.Store) error,
) error {
	cfg, err := a.config(cmd.Context())
	if err != nil {
		return err
	}

	reminders, err := opener(cfg)
	if err != nil {
		return err
	}

	defer func() { _ = reminders.Close() }()

	return fn(reminders)
}

// remindersBaseDir resolves the root every repository's store hangs under.
//
// DEVCTL_REMINDERS_DIR comes first because $HOME is not the same on both sides
// of the sandbox boundary (/Users/bvenceslau on the host, /home/agent inside):
// a sandbox is HANDED the path rather than re-deriving a different one from its
// own environment. Otherwise it follows XDG, under the DATA directory rather
// than a cache or state one — a reminder is the user's own data, and an
// uninstall must not be allowed to take it.
func remindersBaseDir() (string, error) {
	if dir := os.Getenv("DEVCTL_REMINDERS_DIR"); dir != "" {
		return dir, nil
	}

	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "devctl", "reminders"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve the reminders directory: %w", err)
	}

	return filepath.Join(home, ".local", "share", "devctl", "reminders"), nil
}
