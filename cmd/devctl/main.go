// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

// Command devctl is a developer control tool: small, deterministic operations
// on the repositories and sandboxes a working day is spent in.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// Stamped in by ldflags at build time; see the Makefile and .goreleaser.yml.
// `devctl upgrade` compares a published release against version, so a build
// that reported a hardcoded string would upgrade itself in circles.
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
	// The cancelled context reaches every git subprocess devctl starts, so a
	// Ctrl-C does not leave one behind.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := newRootCmd().ExecuteContext(ctx); err != nil {
		// SilenceErrors is on, so this is the ONLY place an error is printed,
		// and it goes to stderr — stdout stays a clean, pipeable record stream.
		fmt.Fprintf(os.Stderr, "devctl: %v\n", err)

		return exitCode(err)
	}

	return exitOK
}
