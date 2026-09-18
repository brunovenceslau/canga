// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/brunovenceslau/canga/internal/repo"
	"github.com/brunovenceslau/canga/internal/store"
	"github.com/spf13/cobra"
)

// ScopeRepo is the only scope that ships. The <scope> path segment exists from
// the first release so that adding a second one later needs no migration of an
// already-populated store — not because there is a second one now.
const ScopeRepo = "repo"

// App is what every subcommand needs in order to find the right store.
type App struct {
	// RepoDir is the working tree whose origin identifies the repository. It is
	// a flag rather than always the process's directory so a caller — a hook, a
	// script, a test — can name the repository without chdir'ing into it.
	RepoDir string
}

// BindRepoFlag registers -C/--repo on root, persistent so every subcommand
// accepts it in the same place.
func (a *App) BindRepoFlag(root *cobra.Command) {
	root.PersistentFlags().StringVarP(&a.RepoDir, "repo", "C", ".",
		"git repository to operate on")

	// A repository is a directory, so file completion on this flag offers mostly
	// wrong answers. Registration cannot fail for a flag declared one line above,
	// which is the only error this returns.
	_ = root.RegisterFlagCompletionFunc("repo",
		func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveFilterDirs
		})
}

// Config derives the store's identity for the repository the App points at.
// It touches the filesystem only to ask git for the origin URL.
func (a *App) Config(ctx context.Context) (store.Config, error) {
	origin, err := repo.Origin(ctx, a.RepoDir)
	if err != nil {
		return store.Config{}, err
	}

	name, err := repo.Path(origin)
	if err != nil {
		return store.Config{}, err
	}

	base, err := RemindersBaseDir()
	if err != nil {
		return store.Config{}, err
	}

	return store.Config{
		// EscapePath on the DIRECTORY only. The store has to mean the same
		// thing on a case-insensitive APFS and a case-sensitive sandbox
		// filesystem, which is the boundary it is meant to be shared across.
		// Repo below keeps the readable spelling, because that is what lands in
		// each reminder's header and what a clone is named after.
		Dir:   filepath.Join(base, filepath.FromSlash(repo.EscapePath(name)), ScopeRepo),
		Repo:  name,
		Scope: ScopeRepo,
	}, nil
}

// WithStore hands fn an already-open store, and fails with store.ErrNoStore if
// the repository has none yet.
func (a *App) WithStore(cmd *cobra.Command, fn func(*store.Store) error) error {
	if err := a.ReconcileLegacyStore(cmd); err != nil {
		return err
	}

	return a.open(cmd, store.OpenExisting, fn)
}

// WithNewStore is WithStore for the one operation that may bring a store into
// existence.
func (a *App) WithNewStore(cmd *cobra.Command, fn func(*store.Store) error) error {
	if err := a.ReconcileLegacyStore(cmd); err != nil {
		return err
	}

	return a.open(cmd, store.Open, fn)
}

func (a *App) open(
	cmd *cobra.Command,
	opener func(store.Config, ...store.Option) (*store.Store, error),
	fn func(*store.Store) error,
) error {
	cfg, err := a.Config(cmd.Context())
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

// Project names the directory both builds keep their data under: the tool's
// own name, shared by the host and the sandbox build.
const Project = "canga"

// ReconcileLegacyStore deals with a store the rename left behind, before a
// command reads or writes the new one. Every reminders command calls it, and
// shell completion never does: completion runs with stderr discarded, where
// neither the notice nor a failure would ever be seen.
//
// When only the old store exists, it is moved onto the new default root in one
// rename, so no one has to. A rename, not a copy: one atomic step on one
// filesystem (both roots sit under the same XDG data directory), with nothing
// half-moved if it is interrupted. Losing a race to another process moving the
// same store is fine — the store is then where it should be. Any other failure
// stops the command: carrying on would create an empty store at the new root,
// after which the old one is never looked at again.
//
// When BOTH exist and the old one still holds files, nothing is moved: the new
// root may already hold reminders, or be a directory a running sandbox has
// mounted, and renaming over it would swap the directory out from under that
// mount. Every command then says so on stderr instead, until the old store is
// merged by hand and removed. That order - a sandbox created before the host
// ran any reminders command - is the only way to reach it.
func (a *App) ReconcileLegacyStore(cmd *cobra.Command) error {
	legacy, current, state := legacyState()

	switch state {
	case legacyNone:
		return nil
	case legacyStranded:
		Fprintf(cmd.ErrOrStderr(),
			"%s: reminders from before the rename are still in %s and are not read,\n"+
				"%s: because %s already exists. Move each repository's items into the\n"+
				"%s: matching directory there, then remove %s.\n",
			cmd.Root().Name(), legacy, cmd.Root().Name(), current, cmd.Root().Name(), legacy)

		return nil
	case legacyMovable:
	}

	if err := os.MkdirAll(filepath.Dir(current), 0o700); err != nil {
		return fmt.Errorf("move the reminders store to %s: %w", current, err)
	}

	if err := os.Rename(legacy, current); err != nil {
		if errors.Is(err, fs.ErrNotExist) && exists(current) {
			return nil
		}

		return fmt.Errorf("move the reminders store from %s to %s: %w", legacy, current, err)
	}

	// The old project directory is left empty unless something else lives in
	// it. os.Remove refuses a non-empty directory, so this can only ever tidy
	// up, and a refusal is not worth reporting.
	_ = os.Remove(filepath.Dir(legacy))

	Fprintf(cmd.ErrOrStderr(), "%s: moved the reminders store from %s to %s\n",
		cmd.Root().Name(), legacy, current)

	return nil
}

// legacy is what legacyState found about the pre-rename store.
type legacy int

const (
	legacyNone     legacy = iota // nothing to do
	legacyMovable                // the old store exists and the new one does not
	legacyStranded               // both exist, and the old one still holds files
)

// ReminderDirVar is the one variable that points both builds at a store root.
// One name for both because the store is shared and the two never run in the
// same place: on the host it is normally unset, and a sandbox's environment
// file sets it to the host's root, which the sandbox cannot derive itself.
const ReminderDirVar = "CANGA_REMINDERS_DIR"

// RemindersBaseDir resolves the root every repository's store hangs under.
//
// CANGA_REMINDERS_DIR comes first because $HOME is not the same on both sides of
// the sandbox boundary (/Users/bvenceslau on the host, /home/agent inside): a
// sandbox is HANDED the path rather than re-deriving a different one from its
// own environment. Otherwise it is the project directory under XDG, the DATA
// one rather than a cache or state one — a reminder is the user's own data, and
// an uninstall must not be allowed to take it.
func RemindersBaseDir() (string, error) {
	if dir := os.Getenv(ReminderDirVar); dir != "" {
		return dir, nil
	}

	data, err := dataHome()
	if err != nil {
		return "", fmt.Errorf("resolve the reminders directory: %w", err)
	}

	return filepath.Join(data, Project, "reminders"), nil
}

// legacyProject is the directory the store lived under before the tool was
// named canga, when its host half was called devctl.
const legacyProject = "devctl"

// legacyState reports the pre-rename store's path, the new default root, and
// what to do about the first. Both paths are "" when a variable overrides the
// default, because then neither default is the store in use.
//
// It exists because nothing else would notice. `list` treats a missing store
// as an empty one, so after the rename the old reminders would simply vanish
// from the listing; and the first `add` creates the new directory, after which
// a plain `mv` of the old one would land INSIDE it and still not be read.
func legacyState() (legacyDir, currentDir string, state legacy) {
	if os.Getenv(ReminderDirVar) != "" {
		return "", "", legacyNone
	}

	data, err := dataHome()
	if err != nil {
		return "", "", legacyNone
	}

	legacyDir = filepath.Join(data, legacyProject, "reminders")
	currentDir = filepath.Join(data, Project, "reminders")

	switch {
	case !isDir(legacyDir):
		return legacyDir, currentDir, legacyNone
	case !exists(currentDir):
		return legacyDir, currentDir, legacyMovable
	case holdsFiles(legacyDir):
		return legacyDir, currentDir, legacyStranded
	default:
		return legacyDir, currentDir, legacyNone
	}
}

// holdsFiles reports whether anything other than directories lives under dir.
// An old store emptied by hand is nothing to warn about.
func holdsFiles(dir string) bool {
	found := errors.New("found")

	err := filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable parts are skipped, not reported
		}

		if !entry.IsDir() {
			return found
		}

		return nil
	})

	return errors.Is(err, found)
}

// dataHome is the XDG data directory: $XDG_DATA_HOME, or its default.
func dataHome() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return dir, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, ".local", "share"), nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.IsDir()
}

func exists(path string) bool {
	_, err := os.Lstat(path)

	return err == nil
}

// Printf writes one record to the command's stdout.
//
// The write error is deliberately dropped. The only realistic failure is a
// closed pipe, which Go's runtime already turns into the conventional SIGPIPE
// death; reporting it instead would make `canga reminders list | head` look
// like a broken command rather than a finished one.
func Printf(cmd *cobra.Command, format string, args ...any) {
	Fprintf(cmd.OutOrStdout(), format, args...)
}

// Fprintf writes to any of a command's streams, dropping the write error for
// the reason given above Printf.
func Fprintf(out io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(out, format, args...)
}
