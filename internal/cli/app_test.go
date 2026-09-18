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
		filepath.Join(os.Getenv("DEVCTL_REMINDERS_DIR"), "github.com", "acme", "widget", ScopeRepo),
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
		filepath.Join(os.Getenv("DEVCTL_REMINDERS_DIR"), "github.com", "!acme", "!widget", ScopeRepo),
		cfg.Dir)
}

func TestRemindersBaseDir(t *testing.T) {
	t.Run("explicit directory wins", func(t *testing.T) {
		// Set explicitly because $HOME is not the same on both sides of the
		// sandbox boundary: a sandbox is handed the path, never left to derive
		// a different one from its own environment.
		t.Setenv("DEVCTL_REMINDERS_DIR", "/explicit")
		t.Setenv("XDG_DATA_HOME", "/xdg")

		got, err := RemindersBaseDir()
		require.NoError(t, err)
		assert.Equal(t, "/explicit", got)
	})

	t.Run("falls back to XDG data", func(t *testing.T) {
		t.Setenv("DEVCTL_REMINDERS_DIR", "")
		t.Setenv("XDG_DATA_HOME", "/xdg")

		got, err := RemindersBaseDir()
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/xdg", "devctl", "reminders"), got)
	})

	t.Run("falls back to the XDG default", func(t *testing.T) {
		t.Setenv("DEVCTL_REMINDERS_DIR", "")
		t.Setenv("XDG_DATA_HOME", "")
		t.Setenv("HOME", "/home/someone")

		got, err := RemindersBaseDir()
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/home/someone", ".local", "share", "devctl", "reminders"), got)
	})
}
