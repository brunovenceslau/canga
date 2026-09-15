package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"testing"

	"github.com/brunovenceslau/devctl/internal/repo"
	"github.com/brunovenceslau/devctl/internal/store"
	"github.com/stretchr/testify/assert"
)

// TestExitCode pins the contract a script calling devctl relies on. The codes
// are deliberately coarse: 2 means the invocation was wrong and retrying it
// unchanged is pointless, 1 means it was right and something failed anyway.
func TestExitCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "success", err: nil, want: exitOK},
		{name: "usage", err: errUsage(errors.New("unknown flag")), want: exitUsage},
		{name: "wrapped usage", err: fmt.Errorf("outer: %w", errUsage(errors.New("bad"))), want: exitUsage},
		{name: "no origin", err: fmt.Errorf("x: %w", repo.ErrNoOrigin), want: exitUsage},
		{name: "unparseable url", err: fmt.Errorf("x: %w", repo.ErrBadURL), want: exitUsage},
		{name: "id not found", err: fmt.Errorf("x: %w", store.ErrNotFound), want: exitFailure},
		// A missing git binary is a runtime failure, not a bad invocation: the
		// command was right, the machine is not set up.
		{name: "git missing", err: fmt.Errorf("x: %w", repo.ErrGitMissing), want: exitFailure},
		{name: "interrupted", err: fmt.Errorf("x: %w", context.Canceled), want: exitFailure},
		{name: "no store", err: fmt.Errorf("x: %w", store.ErrNoStore), want: exitFailure},
		// A typo in an id is a bad invocation. Exit 1 would tell a caller to
		// retry, and retrying a typo never stops.
		{name: "malformed id", err: fmt.Errorf("x: %w", store.ErrInvalidID), want: exitUsage},
		{name: "runtime failure", err: fs.ErrPermission, want: exitFailure},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, exitCode(tt.err))
		})
	}
}
