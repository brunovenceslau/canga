// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunovenceslau/devctl/internal/testrepo"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConfig_KeysTheStoreByTheRepository is the join between the two packages:
// the store directory a command resolves must be the origin-derived path, not
// anything about where the process happens to be running.
//
// toolDevctl is the tool name every case here drives, spelled once.
const toolDevctl = "devctl"

//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestConfig_KeysTheStoreByTheRepository(t *testing.T) {
	dir := testrepo.New(t, "git@github.com:acme/widget.git")

	cfg, err := (&App{Tool: toolDevctl, RepoDir: dir}).Config(t.Context())
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

	cfg, err := (&App{Tool: toolDevctl, RepoDir: dir}).Config(t.Context())
	require.NoError(t, err)

	assert.Equal(t, "github.com/Acme/Widget", cfg.Repo)
	assert.Equal(t,
		filepath.Join(os.Getenv("DEVCTL_REMINDERS_DIR"), "github.com", "!acme", "!widget", ScopeRepo),
		cfg.Dir)
}

func TestRemindersBaseDir(t *testing.T) {
	// devctl runs on the host and agtctl inside a sandbox, so each is
	// configured under its own name and neither inherits the other's store by
	// accident. A sandbox is pointed at the host's store through AGTCTL_
	// REMINDERS_DIR, which its environment file sets.
	for _, tool := range []string{"devctl", "agtctl"} {
		t.Run(tool, func(t *testing.T) {
			t.Run("its own variable wins", func(t *testing.T) {
				t.Setenv(strings.ToUpper(tool)+"_REMINDERS_DIR", "/explicit")
				t.Setenv("XDG_DATA_HOME", "/xdg")

				got, err := RemindersBaseDir(tool)
				require.NoError(t, err)
				assert.Equal(t, "/explicit", got)
			})

			t.Run("falls back to XDG data, under its own name", func(t *testing.T) {
				t.Setenv(strings.ToUpper(tool)+"_REMINDERS_DIR", "")
				t.Setenv("XDG_DATA_HOME", "/xdg")

				got, err := RemindersBaseDir(tool)
				require.NoError(t, err)
				assert.Equal(t, filepath.Join("/xdg", tool, "reminders"), got)
			})

			t.Run("falls back to the XDG default", func(t *testing.T) {
				t.Setenv(strings.ToUpper(tool)+"_REMINDERS_DIR", "")
				t.Setenv("XDG_DATA_HOME", "")
				t.Setenv("HOME", "/home/someone")

				got, err := RemindersBaseDir(tool)
				require.NoError(t, err)
				assert.Equal(t,
					filepath.Join("/home/someone", ".local", "share", tool, "reminders"), got)
			})
		})
	}

	t.Run("one binary's variable does not configure the other", func(t *testing.T) {
		t.Setenv("DEVCTL_REMINDERS_DIR", "/devctl-store")
		t.Setenv("AGTCTL_REMINDERS_DIR", "")
		t.Setenv("XDG_DATA_HOME", "/xdg")

		got, err := RemindersBaseDir("agtctl")
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/xdg", "agtctl", "reminders"), got)
	})

	t.Run("a nameless app is a programming error", func(t *testing.T) {
		t.Parallel()

		_, err := RemindersBaseDir("")
		require.ErrorIs(t, err, ErrNoTool)
	})
}
