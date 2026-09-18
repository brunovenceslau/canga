// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"

	"github.com/brunovenceslau/canga/internal/cli"
	"github.com/brunovenceslau/canga/internal/hooks"
)

// exitCode maps an error to canga's exit code: canga's own sentinels first,
// then the contract every binary shares.
func exitCode(err error) int {
	// A conflict is resolved by passing --force or moving a file, never by
	// running the same command again, which is the same test that makes a
	// malformed id a usage error.
	if errors.Is(err, hooks.ErrConflict) {
		return cli.ExitUsage
	}

	return cli.ExitCode(err)
}
