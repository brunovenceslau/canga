// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/brunovenceslau/devctl/internal/repo"
	"github.com/brunovenceslau/devctl/internal/store"
	"github.com/spf13/cobra"
)

// ScopeRepo is the only scope that ships. The <scope> path segment exists from
// the first release so that adding a second one later needs no migration of an
// already-populated store — not because there is a second one now.
const ScopeRepo = "repo"

// App is what every subcommand needs in order to find the right store.
type App struct {
	// Tool is the binary's own name, lowercase: "devctl" or "agtctl". It is
	// what RemindersBaseDir names the environment variable and the XDG
	// directory after, so each binary is configured under its own name. The
	// two never run in the same place — devctl on the host, agtctl inside a
	// sandbox — so nothing is shared by default; a sandbox is HANDED the
	// host's store path through its own variable.
	Tool string

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
	// Before the git subprocess: a nameless App is a programming error, and
	// reporting it after I/O would bury it under whatever git said.
	base, err := RemindersBaseDir(a.Tool)
	if err != nil {
		return store.Config{}, err
	}

	origin, err := repo.Origin(ctx, a.RepoDir)
	if err != nil {
		return store.Config{}, err
	}

	name, err := repo.Path(origin)
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
	return a.open(cmd, store.OpenExisting, fn)
}

// WithNewStore is WithStore for the one operation that may bring a store into
// existence.
func (a *App) WithNewStore(cmd *cobra.Command, fn func(*store.Store) error) error {
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

// ErrNoTool reports an App built without a tool name. It is a programming
// error, not a user one: without a name there is no variable to read and no
// directory to fall back to.
var ErrNoTool = errors.New("no tool name")

// RemindersBaseDir resolves the root every repository's store hangs under, for
// the binary named by tool.
//
// <TOOL>_REMINDERS_DIR comes first because $HOME is not the same on both sides
// of the sandbox boundary (/Users/bvenceslau on the host, /home/agent inside):
// a sandbox is HANDED the path rather than re-deriving a different one from its
// own environment. That variable is how agtctl inside a sandbox is pointed at
// the host store devctl owns; without it each binary keeps a store of its own,
// under its own name. Otherwise it follows XDG, under the DATA directory rather
// than a cache or state one — a reminder is the user's own data, and an
// uninstall must not be allowed to take it.
func RemindersBaseDir(tool string) (string, error) {
	if tool == "" {
		return "", fmt.Errorf("resolve the reminders directory: %w", ErrNoTool)
	}

	if dir := os.Getenv(strings.ToUpper(tool) + "_REMINDERS_DIR"); dir != "" {
		return dir, nil
	}

	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, tool, "reminders"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve the reminders directory: %w", err)
	}

	return filepath.Join(home, ".local", "share", tool, "reminders"), nil
}

// Printf writes one record to the command's stdout.
//
// The write error is deliberately dropped. The only realistic failure is a
// closed pipe, which Go's runtime already turns into the conventional SIGPIPE
// death; reporting it instead would make `devctl reminders list | head` look
// like a broken command rather than a finished one.
func Printf(cmd *cobra.Command, format string, args ...any) {
	Fprintf(cmd.OutOrStdout(), format, args...)
}

// Fprintf writes to any of a command's streams, dropping the write error for
// the reason given above Printf.
func Fprintf(out io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(out, format, args...)
}
