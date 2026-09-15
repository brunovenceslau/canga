// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"

	"github.com/brunovenceslau/devctl/internal/hooks"
	"github.com/brunovenceslau/devctl/internal/repo"
	"github.com/brunovenceslau/devctl/internal/store"
	"github.com/brunovenceslau/devctl/internal/upgrade"
	"github.com/spf13/cobra"
)

// Exit codes are part of devctl's contract with the shell and with any script
// that calls it, so they live in one place rather than at each return.
const (
	exitOK      = 0
	exitFailure = 1 // a runtime failure, or an id with nothing behind it
	exitUsage   = 2 // a bad invocation, or a directory with no usable origin
)

// usageError marks an error caused by how devctl was CALLED, not by something
// that went wrong while it ran. The distinction has to be carried in the error
// itself, because cobra reports a mistyped flag and a failed write through the
// same return value.
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }

func (e *usageError) Unwrap() error { return e.err }

// errUsage marks err as caused by the invocation.
func errUsage(err error) error {
	return &usageError{err: err}
}

// usageArgs wraps a cobra argument validator so a wrong argument count, or an
// unknown subcommand, exits 2 rather than 1.
func usageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := validate(cmd, args); err != nil {
			return errUsage(err)
		}

		return nil
	}
}

// runHelp is the Run of a command that only groups others. It exists so cobra
// treats the command as Runnable and therefore validates its arguments; see the
// comment at the root command.
func runHelp(cmd *cobra.Command, _ []string) error {
	return cmd.Help()
}

// exitCode maps an error to devctl's exit code.
func exitCode(err error) int {
	var usage *usageError

	switch {
	case err == nil:
		return exitOK
	case errors.As(err, &usage),
		// A directory that is not a repository, or a URL nothing can be derived
		// from, is the caller pointing devctl somewhere wrong: there is nothing
		// to retry, which is what separates it from a runtime failure.
		errors.Is(err, repo.ErrNoOrigin),
		errors.Is(err, repo.ErrBadURL),
		errors.Is(err, repo.ErrNotARepository),
		// An id that is not one ordinary path segment is a typo on the command
		// line. Exit 1 would tell a caller to retry, and retrying a typo never
		// stops.
		errors.Is(err, store.ErrInvalidID),
		// A conflict is resolved by passing --force or moving a file, never by
		// running the same command again, which is the same test that puts a
		// malformed id here.
		errors.Is(err, hooks.ErrConflict),
		// An upgrade that cannot tell which release the running binary came
		// from, and a --tag that is not a version, are both answered by naming
		// a release on the command line. Same test again: nothing to retry.
		errors.Is(err, upgrade.ErrNotRelease),
		errors.Is(err, upgrade.ErrBadTag):
		return exitUsage
	default:
		return exitFailure
	}
}
