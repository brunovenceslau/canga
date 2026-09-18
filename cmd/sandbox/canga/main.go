// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

// Command canga, in its sandbox build, is what an agent inside a sandbox runs.
//
// It is the same binary name as the host build and a different program: this
// package imports only the reminders commands, so what an agent can do is
// decided by what was compiled in. It cannot clone, sync, install hooks or
// replace a binary, and it can read and add reminders but not remove or
// reorder them. Choosing the role at runtime instead would hand those commands
// to anyone who could set a variable.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/brunovenceslau/canga/internal/cli"
)

// Stamped in by ldflags at build time; see the Makefile and .goreleaser.yml.
var (
	version = "dev"
	commit  = "none"
)

func main() {
	// os.Exit skips deferred calls, so the whole run lives in a function that
	// returns a code and main does nothing but hand it over.
	os.Exit(run())
}

func run() int {
	// The cancelled context reaches the git subprocess that resolves the
	// origin, so a Ctrl-C or a hook timeout does not leave one behind.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := newRootCmd().ExecuteContext(ctx); err != nil {
		// SilenceErrors is on, so this is the ONLY place an error is printed,
		// and it goes to stderr — stdout stays a clean, pipeable record stream.
		fmt.Fprintf(os.Stderr, "canga: %v\n", err)

		return cli.ExitCode(err)
	}

	return cli.ExitOK
}
