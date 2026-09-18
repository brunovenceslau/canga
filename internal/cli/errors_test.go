// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"testing"

	"github.com/brunovenceslau/canga/internal/repo"
	"github.com/brunovenceslau/canga/internal/store"
	"github.com/stretchr/testify/assert"
)

// TestExitCode pins the contract a script calling either binary relies on. The
// codes are deliberately coarse: 2 means the invocation was wrong and retrying
// it unchanged is pointless, 1 means it was right and something failed anyway.
func TestExitCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "success", err: nil, want: ExitOK},
		{name: "usage", err: Usage(errors.New("unknown flag")), want: ExitUsage},
		{name: "wrapped usage", err: fmt.Errorf("outer: %w", Usage(errors.New("bad"))), want: ExitUsage},
		{name: "no origin", err: fmt.Errorf("x: %w", repo.ErrNoOrigin), want: ExitUsage},
		{name: "unparseable url", err: fmt.Errorf("x: %w", repo.ErrBadURL), want: ExitUsage},
		{name: "not a repository", err: fmt.Errorf("x: %w", repo.ErrNotARepository), want: ExitUsage},
		{name: "id not found", err: fmt.Errorf("x: %w", store.ErrNotFound), want: ExitFailure},
		// A missing git binary is a runtime failure, not a bad invocation: the
		// command was right, the machine is not set up.
		{name: "git missing", err: fmt.Errorf("x: %w", repo.ErrGitMissing), want: ExitFailure},
		{name: "interrupted", err: fmt.Errorf("x: %w", context.Canceled), want: ExitFailure},
		{name: "no store", err: fmt.Errorf("x: %w", store.ErrNoStore), want: ExitFailure},
		// A typo in an id is a bad invocation. Exit 1 would tell a caller to
		// retry, and retrying a typo never stops.
		{name: "malformed id", err: fmt.Errorf("x: %w", store.ErrInvalidID), want: ExitUsage},
		{name: "runtime failure", err: fs.ErrPermission, want: ExitFailure},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, ExitCode(tt.err))
		})
	}
}
