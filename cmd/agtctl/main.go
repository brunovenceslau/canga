// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

// Command agtctl is devctl's counterpart for the agents inside a sandbox.
//
// It is a separate binary rather than devctl with some commands hidden, so
// that what an agent can do is decided by what was compiled in: a sandbox that
// only has agtctl cannot clone, sync, install hooks or replace a binary, and
// can read and add reminders but not remove or reorder them.
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
		fmt.Fprintf(os.Stderr, "agtctl: %v\n", err)

		return cli.ExitCode(err)
	}

	return cli.ExitOK
}
