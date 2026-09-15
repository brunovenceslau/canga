// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// remindersCmd is the subcommand every test below drives.
const remindersCmd = "reminders"

// execute runs one devctl invocation against a FRESH command tree — cobra
// accumulates flag state across Execute calls, so reusing one would leak the
// previous test's flags into this one.
func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)

	// Run BEFORE reading the buffer: Go evaluates return operands left to
	// right, so returning out.String() alongside the call would capture the
	// buffer as it was before the command ever wrote to it.
	err := cmd.ExecuteContext(t.Context())

	return out.String(), err
}

// executeSplit is execute with the two streams kept apart, so a test can prove
// the split rather than assume it: records belong on stdout and everything else
// on stderr, which is what lets a listing be piped.
func executeSplit(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	var out, errOut bytes.Buffer

	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)

	err = cmd.ExecuteContext(t.Context())

	return out.String(), errOut.String(), err
}

// scratchRepo makes a git repository with a known origin, and points devctl's
// store at a directory of this test's own.
//
// It calls t.Setenv, so a test using it cannot be parallel. That is deliberate:
// the alternative is reading the developer's real reminders.
func scratchRepo(t *testing.T, origin string) string {
	t.Helper()

	global := filepath.Join(t.TempDir(), "gitconfig")
	require.NoError(t, os.WriteFile(global, nil, 0o600))

	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("DEVCTL_REMINDERS_DIR", filepath.Join(t.TempDir(), "reminders"))

	dir := filepath.Join(t.TempDir(), "repo")

	run := func(args ...string) {
		out, err := exec.CommandContext(t.Context(), "git", args...).CombinedOutput()
		require.NoErrorf(t, err, "git %v: %s", args, out)
	}

	run("init", "-q", "-b", "main", dir)
	run("-C", dir, "remote", "add", "origin", origin)

	return dir
}

func TestRemindersBaseDir(t *testing.T) {
	t.Run("explicit directory wins", func(t *testing.T) {
		// Set explicitly because $HOME is not the same on both sides of the
		// sandbox boundary: a sandbox is handed the path, never left to derive
		// a different one from its own environment.
		t.Setenv("DEVCTL_REMINDERS_DIR", "/explicit")
		t.Setenv("XDG_DATA_HOME", "/xdg")

		got, err := remindersBaseDir()
		require.NoError(t, err)
		assert.Equal(t, "/explicit", got)
	})

	t.Run("falls back to XDG data", func(t *testing.T) {
		t.Setenv("DEVCTL_REMINDERS_DIR", "")
		t.Setenv("XDG_DATA_HOME", "/xdg")

		got, err := remindersBaseDir()
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/xdg", "devctl", "reminders"), got)
	})

	t.Run("falls back to the XDG default", func(t *testing.T) {
		t.Setenv("DEVCTL_REMINDERS_DIR", "")
		t.Setenv("XDG_DATA_HOME", "")
		t.Setenv("HOME", "/home/someone")

		got, err := remindersBaseDir()
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/home/someone", ".local", "share", "devctl", "reminders"), got)
	})
}

// TestConfig_KeysTheStoreByTheRepository is the join between the two packages:
// the store directory a command resolves must be the origin-derived path, not
// anything about where the process happens to be running.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestConfig_KeysTheStoreByTheRepository(t *testing.T) {
	dir := scratchRepo(t, "git@github.com:acme/widget.git")

	cfg, err := (&app{repoDir: dir}).config(t.Context())
	require.NoError(t, err)

	assert.Equal(t, "github.com/acme/widget", cfg.Repo)
	assert.Equal(t, scopeRepo, cfg.Scope)
	assert.Equal(t,
		filepath.Join(os.Getenv("DEVCTL_REMINDERS_DIR"), "github.com", "acme", "widget", scopeRepo),
		cfg.Dir)
}

// TestConfig_EncodesCaseInTheDirectory: the directory has to mean the same
// thing on a case-folding APFS and on a sandbox filesystem that does not fold,
// which is the boundary a shared store spans. The readable spelling stays in
// Repo, because that is what each reminder's header records.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestConfig_EncodesCaseInTheDirectory(t *testing.T) {
	dir := scratchRepo(t, "git@github.com:Acme/Widget.git")

	cfg, err := (&app{repoDir: dir}).config(t.Context())
	require.NoError(t, err)

	assert.Equal(t, "github.com/Acme/Widget", cfg.Repo)
	assert.Equal(t,
		filepath.Join(os.Getenv("DEVCTL_REMINDERS_DIR"), "github.com", "!acme", "!widget", scopeRepo),
		cfg.Dir)
}

//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestRoot_UsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "unknown command", args: []string{"bogus"}},
		{name: "unknown subcommand", args: []string{remindersCmd, "bogus"}},
		{name: "unknown flag", args: []string{remindersCmd, "list", "--nope"}},
		{name: "add with no text", args: []string{remindersCmd, "add"}},
		{name: "rm with no id", args: []string{remindersCmd, "rm"}},
		{name: "path with too many ids", args: []string{remindersCmd, "path", "a", "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := execute(t, tt.args...)
			require.Error(t, err)
			assert.Equal(t, exitUsage, exitCode(err))
		})
	}
}

// TestRoot_OutsideARepository: a directory with no origin is the caller
// pointing devctl somewhere wrong, which is a usage error, not a failure.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestRoot_OutsideARepository(t *testing.T) {
	scratchRepo(t, "git@github.com:acme/widget.git")

	_, err := execute(t, remindersCmd, "list", "-C", t.TempDir())
	require.Error(t, err)
	assert.Equal(t, exitUsage, exitCode(err))
}

//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestRoot_BareCommandsPrintHelp(t *testing.T) {
	for _, args := range [][]string{{}, {remindersCmd}} {
		out, err := execute(t, args...)
		require.NoError(t, err)
		assert.Contains(t, out, "Usage:")
	}
}
