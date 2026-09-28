// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package testrepo builds the hermetic git repository the reminders tests run
// against. It is imported only by tests.
//
// It exists because the reminders tests live in three packages (internal/cli,
// cmd/host/canga and cmd/sandbox/canga), and a helper in a _test.go file cannot be shared
// across packages. Three copies of the environment it sets would drift, and a
// copy that forgot CANGA_REMINDERS_DIR would read the developer's real store.
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
// sandbox configured can reach a test, and points CANGA_REMINDERS_DIR at a
// directory of the test's own.
//
// It calls t.Setenv, so a test using it cannot be parallel. That is deliberate:
// the alternative is reading the developer's real reminders.
func New(t *testing.T, origin string) string {
	t.Helper()

	Hermetic(t)

	dir := filepath.Join(t.TempDir(), "repo")
	Init(t, dir, origin)

	return dir
}

// Hermetic is the environment New sets, for a test that places its
// repositories itself: git's system and global config emptied, the reminders
// store in a directory of the test's own, and the clone base cleared so the
// developer's own CANGA_SRC_DIR (or its pre-rename name) cannot reach a test.
func Hermetic(t *testing.T) {
	t.Helper()

	global := filepath.Join(t.TempDir(), "gitconfig")
	require.NoError(t, os.WriteFile(global, nil, 0o600))

	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("CANGA_REMINDERS_DIR", filepath.Join(t.TempDir(), "reminders"))
	t.Setenv("CANGA_SRC_DIR", "")
	t.Setenv("CANGA_HOST_BASE_DIR", "")
}

// Init makes a git repository at dir, with origin as its origin remote, or
// with no remote at all when origin is empty. Call Hermetic (or New) first.
func Init(t *testing.T, dir, origin string) {
	t.Helper()

	commands := [][]string{{"init", "-q", "-b", "main", dir}}
	if origin != "" {
		commands = append(commands, []string{"-C", dir, "remote", "add", "origin", origin})
	}

	for _, args := range commands {
		//nolint:gosec // the program name is a constant and every argument is passed
		// separately, so no shell parses the fixture. gosec is off for _test.go
		// files, and this is test code that cannot live in one.
		out, err := exec.CommandContext(t.Context(), "git", args...).CombinedOutput()
		require.NoErrorf(t, err, "git %v: %s", args, out)
	}
}
