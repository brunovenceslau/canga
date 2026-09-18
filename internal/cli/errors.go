// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package cli holds what canga's two builds share: the exit code contract, the
// store a command resolves for a repository, and the reminders and upgrade
// subcommands themselves.
//
// It exists so the host and sandbox builds cannot drift. They run on opposite
// sides of the sandbox boundary against ONE store; if each derived the store
// directory on its own, a disagreement between them would split a list
// silently.
package cli

import (
	"errors"

	"github.com/brunovenceslau/canga/internal/repo"
	"github.com/brunovenceslau/canga/internal/store"
	"github.com/spf13/cobra"
)

// Exit codes are part of the contract with the shell and with any script that
// calls either binary, so they live in one place rather than at each return.
const (
	ExitOK      = 0
	ExitFailure = 1 // a runtime failure, or an id with nothing behind it
	ExitUsage   = 2 // a bad invocation, or a directory with no usable origin
)

// usageError marks an error caused by how a command was CALLED, not by
// something that went wrong while it ran. The distinction has to be carried in
// the error itself, because cobra reports a mistyped flag and a failed write
// through the same return value.
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }

func (e *usageError) Unwrap() error { return e.err }

// Usage marks err as caused by the invocation.
func Usage(err error) error {
	return &usageError{err: err}
}

// UsageArgs wraps a cobra argument validator so a wrong argument count, or an
// unknown subcommand, exits 2 rather than 1.
func UsageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := validate(cmd, args); err != nil {
			return Usage(err)
		}

		return nil
	}
}

// RunHelp is the Run of a command that only groups others. It exists so cobra
// treats the command as Runnable and therefore validates its arguments: cobra
// short-circuits a command with no Run to its help text BEFORE validating
// arguments, so a bare parent would answer an unknown subcommand with help and
// exit 0.
func RunHelp(cmd *cobra.Command, _ []string) error {
	return cmd.Help()
}

// ExitCode maps an error from the shared commands to an exit code. A binary
// with sentinels of its own checks those first and defers to this for the
// rest.
func ExitCode(err error) int {
	var usage *usageError

	switch {
	case err == nil:
		return ExitOK
	case errors.As(err, &usage),
		// A directory that is not a repository, or a URL nothing can be derived
		// from, is the caller pointing the command somewhere wrong: there is
		// nothing to retry, which is what separates it from a runtime failure.
		errors.Is(err, repo.ErrNoOrigin),
		errors.Is(err, repo.ErrBadURL),
		errors.Is(err, repo.ErrNotARepository),
		// An id that is not one ordinary path segment is a typo on the command
		// line. Exit 1 would tell a caller to retry, and retrying a typo never
		// stops.
		errors.Is(err, store.ErrInvalidID):
		return ExitUsage
	default:
		return ExitFailure
	}
}
