// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"

	"github.com/brunovenceslau/canga/internal/cli"
	"github.com/brunovenceslau/canga/internal/hooks"
	"github.com/brunovenceslau/canga/internal/upgrade"
)

// exitCode maps an error to canga's exit code: canga's own sentinels first,
// then the contract every binary shares.
func exitCode(err error) int {
	switch {
	case
		// A conflict is resolved by passing --force or moving a file, never by
		// running the same command again, which is the same test that makes a
		// malformed id a usage error.
		errors.Is(err, hooks.ErrConflict),
		// An upgrade that cannot tell which release the running binary came
		// from, and a --tag that is not a version, are both answered by naming
		// a release on the command line. Same test again: nothing to retry.
		errors.Is(err, upgrade.ErrNotRelease),
		errors.Is(err, upgrade.ErrBadTag):
		return cli.ExitUsage
	default:
		return cli.ExitCode(err)
	}
}
