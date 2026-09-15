package main

import (
	"errors"

	"github.com/spf13/cobra"
)

// Exit codes are part of devctl's contract with the shell and with any script
// that calls it, so they live in one place rather than at each return.
const (
	exitOK      = 0
	exitFailure = 1 // a runtime failure
	exitUsage   = 2 // a bad invocation
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
	case errors.As(err, &usage):
		return exitUsage
	default:
		return exitFailure
	}
}
