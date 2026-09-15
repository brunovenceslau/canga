package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunovenceslau/devctl/internal/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetupHooks drives the command end to end, because the thing worth
// asserting is what a caller sees: the hook names on stdout so the output
// pipes, and everything else on stderr.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestSetupHooks(t *testing.T) {
	dir := scratchRepo(t, "git@github.com:acme/widget.git")

	source := filepath.Join(dir, filepath.FromSlash(hooks.SourceDir))
	require.NoError(t, os.MkdirAll(source, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(source, "pre-commit"), []byte("#!/bin/sh\n"), 0o755))

	out, err := execute(t, "setup", "hooks", "-C", dir)
	require.NoError(t, err)
	assert.Contains(t, out, "pre-commit")
}

//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestSetupHooks_OutsideARepositoryIsAUsageError(t *testing.T) {
	scratchRepo(t, "git@github.com:acme/widget.git")

	_, err := execute(t, "setup", "hooks", "-C", t.TempDir())
	require.Error(t, err)
	assert.Equal(t, exitUsage, exitCode(err))
	assert.Contains(t, err.Error(), "not inside a git repository")
}

//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestSetupHooks_NothingToInstallIsARuntimeFailure(t *testing.T) {
	dir := scratchRepo(t, "git@github.com:acme/widget.git")

	_, err := execute(t, "setup", "hooks", "-C", dir)
	require.Error(t, err)
	assert.Equal(t, exitFailure, exitCode(err))
	assert.Contains(t, err.Error(), hooks.SourceDir)
}

//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestSetupHooks_UsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"setup", "bogus"},
		{"setup", "hooks", "extra"},
		{"setup", "hooks", "--nope"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, err := execute(t, args...)
			require.Error(t, err)
			assert.Equal(t, exitUsage, exitCode(err))
		})
	}
}
