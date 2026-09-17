// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"testing"

	"github.com/brunovenceslau/devctl/internal/cli"
	"github.com/brunovenceslau/devctl/internal/hooks"
	"github.com/brunovenceslau/devctl/internal/upgrade"
	"github.com/stretchr/testify/assert"
)

// TestExitCode pins what devctl adds to the shared contract, which
// internal/cli's own test pins: its own sentinels are usage errors, and
// everything else is decided by cli.ExitCode.
func TestExitCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "success", err: nil, want: cli.ExitOK},
		{name: "hook conflict", err: fmt.Errorf("x: %w", hooks.ErrConflict), want: cli.ExitUsage},
		{name: "not a release", err: fmt.Errorf("x: %w", upgrade.ErrNotRelease), want: cli.ExitUsage},
		{name: "bad tag", err: fmt.Errorf("x: %w", upgrade.ErrBadTag), want: cli.ExitUsage},
		{name: "shared usage", err: cli.Usage(errors.New("unknown flag")), want: cli.ExitUsage},
		{name: "shared runtime failure", err: fs.ErrPermission, want: cli.ExitFailure},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, exitCode(tt.err))
		})
	}
}
