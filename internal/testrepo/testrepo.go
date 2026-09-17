// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package testrepo builds the hermetic git repository the reminders tests run
// against. It is imported only by tests.
//
// It exists because the reminders tests live in three packages (internal/cli,
// cmd/devctl and cmd/agtctl), and a helper in a _test.go file cannot be shared
// across packages. Three copies of the environment it sets would drift, and a
// copy that forgot DEVCTL_REMINDERS_DIR would read the developer's real store.
package testrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// New makes a git repository whose origin is origin, and returns its path.
//
// It empties git's system and global config, so nothing the developer or the
// sandbox configured can reach a test, and points DEVCTL_REMINDERS_DIR at a
// directory of the test's own.
//
// It calls t.Setenv, so a test using it cannot be parallel. That is deliberate:
// the alternative is reading the developer's real reminders.
func New(t *testing.T, origin string) string {
	t.Helper()

	global := filepath.Join(t.TempDir(), "gitconfig")
	require.NoError(t, os.WriteFile(global, nil, 0o600))

	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("DEVCTL_REMINDERS_DIR", filepath.Join(t.TempDir(), "reminders"))

	dir := filepath.Join(t.TempDir(), "repo")

	for _, args := range [][]string{
		{"init", "-q", "-b", "main", dir},
		{"-C", dir, "remote", "add", "origin", origin},
	} {
		//nolint:gosec // the program name is a constant and every argument is passed
		// separately, so no shell parses the fixture. gosec is off for _test.go
		// files, and this is test code that cannot live in one.
		out, err := exec.CommandContext(t.Context(), "git", args...).CombinedOutput()
		require.NoErrorf(t, err, "git %v: %s", args, out)
	}

	return dir
}
