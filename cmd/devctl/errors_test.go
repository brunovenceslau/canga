package main

import (
	"errors"
	"fmt"
	"io/fs"
	"testing"

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
		{name: "runtime failure", err: fs.ErrPermission, want: exitFailure},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, exitCode(tt.err))
		})
	}
}
