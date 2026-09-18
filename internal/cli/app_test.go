// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brunovenceslau/canga/internal/testrepo"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConfig_KeysTheStoreByTheRepository is the join between the two packages:
// the store directory a command resolves must be the origin-derived path, not
// anything about where the process happens to be running.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestConfig_KeysTheStoreByTheRepository(t *testing.T) {
	dir := testrepo.New(t, "git@github.com:acme/widget.git")

	cfg, err := (&App{RepoDir: dir}).Config(t.Context())
	require.NoError(t, err)

	assert.Equal(t, "github.com/acme/widget", cfg.Repo)
	assert.Equal(t, ScopeRepo, cfg.Scope)
	assert.Equal(t,
		filepath.Join(os.Getenv(ReminderDirVar), "github.com", "acme", "widget", ScopeRepo),
		cfg.Dir)
}

// TestConfig_EncodesCaseInTheDirectory: the directory has to mean the same
// thing on a case-folding APFS and on a sandbox filesystem that does not fold,
// which is the boundary a shared store spans. The readable spelling stays in
// Repo, because that is what each reminder's header records.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestConfig_EncodesCaseInTheDirectory(t *testing.T) {
	dir := testrepo.New(t, "git@github.com:Acme/Widget.git")

	cfg, err := (&App{RepoDir: dir}).Config(t.Context())
	require.NoError(t, err)

	assert.Equal(t, "github.com/Acme/Widget", cfg.Repo)
	assert.Equal(t,
		filepath.Join(os.Getenv(ReminderDirVar), "github.com", "!acme", "!widget", ScopeRepo),
		cfg.Dir)
}

func TestRemindersBaseDir(t *testing.T) {
	t.Run("the shared variable wins", func(t *testing.T) {
		// Set explicitly because $HOME is not the same on both sides of the
		// sandbox boundary: a sandbox is handed the path, never left to derive
		// a different one from its own environment.
		t.Setenv(ReminderDirVar, "/explicit")
		t.Setenv("XDG_DATA_HOME", "/xdg")

		got, err := RemindersBaseDir()
		require.NoError(t, err)
		assert.Equal(t, "/explicit", got)
	})

	t.Run("falls back to the project directory under XDG", func(t *testing.T) {
		t.Setenv(ReminderDirVar, "")
		t.Setenv("XDG_DATA_HOME", "/xdg")

		got, err := RemindersBaseDir()
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/xdg", Project, "reminders"), got)
	})

	t.Run("falls back to the XDG default", func(t *testing.T) {
		t.Setenv(ReminderDirVar, "")
		t.Setenv("XDG_DATA_HOME", "")
		t.Setenv("HOME", "/home/someone")

		got, err := RemindersBaseDir()
		require.NoError(t, err)
		assert.Equal(t,
			filepath.Join("/home/someone", ".local", "share", Project, "reminders"), got)
	})

	t.Run("the pre-canga variables are not read", func(t *testing.T) {
		// A sandbox still set up with them must not half-work: the README
		// names CANGA_REMINDERS_DIR as the only one.
		t.Setenv(ReminderDirVar, "")
		t.Setenv("DEVCTL_REMINDERS_DIR", "/old-devctl")
		t.Setenv("AGTCTL_REMINDERS_DIR", "/old-agtctl")
		t.Setenv("XDG_DATA_HOME", "/xdg")

		got, err := RemindersBaseDir()
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/xdg", Project, "reminders"), got)
	})
}

// TestLegacyState pins what happens to a store left behind by the rename. It
// is moved only when the new root does not exist; when both exist and the old
// one still holds files, it is reported and never moved; everything else is
// left alone, so no command nags after the move.
//
//nolint:paralleltest // setup calls t.Setenv, which the hermetic environment needs
func TestLegacyState(t *testing.T) {
	setup := func(t *testing.T, legacyFile, current bool) string {
		t.Helper()

		data := t.TempDir()
		t.Setenv("XDG_DATA_HOME", data)
		t.Setenv(ReminderDirVar, "")

		legacyDir := filepath.Join(data, "devctl", "reminders")
		require.NoError(t, os.MkdirAll(filepath.Join(legacyDir, "github.com"), 0o700))

		if legacyFile {
			require.NoError(t, os.WriteFile(filepath.Join(legacyDir, "github.com", "item.md"), nil, 0o600))
		}

		if current {
			require.NoError(t, os.MkdirAll(filepath.Join(data, Project, "reminders"), 0o700))
		}

		return data
	}

	tests := []struct {
		name       string
		legacyFile bool
		current    bool
		want       legacy
	}{
		{name: "old store and no new root is moved", legacyFile: true, want: legacyMovable},
		{name: "an empty old store with no new root is moved too", want: legacyMovable},
		{name: "both, with files in the old one, is stranded", legacyFile: true, current: true, want: legacyStranded},
		{name: "both, the old one emptied by hand, is left alone", current: true, want: legacyNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := setup(t, tt.legacyFile, tt.current)

			legacyDir, currentDir, state := legacyState()
			assert.Equal(t, tt.want, state)
			assert.Equal(t, filepath.Join(data, "devctl", "reminders"), legacyDir)
			assert.Equal(t, filepath.Join(data, Project, "reminders"), currentDir)
		})
	}

	t.Run("a fresh machine has nothing to do", func(t *testing.T) {
		data := t.TempDir()
		t.Setenv("XDG_DATA_HOME", data)
		t.Setenv(ReminderDirVar, "")

		_, _, state := legacyState()
		assert.Equal(t, legacyNone, state)
	})

	t.Run("an override means neither default is in use", func(t *testing.T) {
		setup(t, true, false)
		t.Setenv(ReminderDirVar, t.TempDir())

		_, _, state := legacyState()
		assert.Equal(t, legacyNone, state)
	})
}
