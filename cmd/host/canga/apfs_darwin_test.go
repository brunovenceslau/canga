// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brunovenceslau/canga/internal/cli"
	"github.com/brunovenceslau/canga/internal/repo"
	"github.com/brunovenceslau/canga/internal/testrepo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHost_APFSWrongCaseKeysAsStored is the operator's Mac measurement
// (docs/HANDOFF.md, "macOS case: measured, then fixed") on a REAL volume, no
// simulated filesystem: an origin-less repository created as src/local/OS and
// entered as SRC/LOCAL/os, with CANGA_SRC_DIR spelled SRC, keys as local/OS.
// The file name makes it darwin-only; it runs on the macOS legs of the Test
// workflow. It skips only when the temporary volume is case-sensitive, which
// APFS is not by default.
func TestHost_APFSWrongCaseKeysAsStored(t *testing.T) {
	testrepo.Hermetic(t)

	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	stored := filepath.Join(root, "src", "local", "OS")
	testrepo.Init(t, stored, "")

	typed := filepath.Join(root, "SRC", "LOCAL", "os")

	same, err := os.Stat(typed)
	if err != nil {
		t.Skipf("%s is on a case-sensitive volume: %v", root, err)
	}

	original, err := os.Stat(stored)
	require.NoError(t, err)
	require.True(t, os.SameFile(same, original), "the wrong-case path is another directory")

	t.Setenv("CANGA_SRC_DIR", filepath.Join(root, "SRC"))
	t.Chdir(typed)

	want := filepath.Join(os.Getenv(cli.ReminderDirVar), "local", "!o!s", cli.ScopeRepo) + "\n"

	for _, args := range [][]string{
		{remindersCmd, pathCmd},
		{remindersCmd, pathCmd, "-C", typed},
		{remindersCmd, pathCmd, "-C", filepath.Join(typed, ".")},
	} {
		out, err := execute(t, args...)
		require.NoError(t, err, "%v", args)
		assert.Equal(t, want, out, "%v", args)
	}
}

// TestHost_APFSUnreadableSpellingFailsClosed: on a folding volume, a
// wrong-case component whose parent cannot be read has no spelling canga can
// establish, and the typed one would open a second store. It fails closed,
// repo.ErrUnreadableSpelling, exit 2, on a REAL volume (ship-gate round 2,
// test-engineer Medium: only the simulated filesystem covered it).
//
// The parent is made execute-only rather than mode 000: with no execute bit
// even the lookup of the typed name fails, which is a plain permission error
// and never reaches the spelling at all.
func TestHost_APFSUnreadableSpellingFailsClosed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a directory whatever its mode")
	}

	testrepo.Hermetic(t)

	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	stored := filepath.Join(root, "src", "local", "OS")
	testrepo.Init(t, stored, "")

	typed := filepath.Join(root, "src", "local", "os")
	if _, err := os.Stat(typed); err != nil {
		t.Skipf("%s is on a case-sensitive volume: %v", root, err)
	}

	parent := filepath.Join(root, "src", "local")
	require.NoError(t, os.Chmod(parent, 0o100))
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	t.Setenv("CANGA_SRC_DIR", filepath.Join(root, "src"))

	_, err = execute(t, remindersCmd, pathCmd, "-C", typed)
	require.ErrorIs(t, err, repo.ErrUnreadableSpelling)
	assert.Equal(t, cli.ExitUsage, exitCode(err))
}
