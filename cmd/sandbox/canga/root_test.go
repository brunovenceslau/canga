// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunovenceslau/canga/internal/testrepo"

	"github.com/brunovenceslau/canga/internal/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// remindersCmd is the subcommand most cases here drive, spelled once.
const remindersCmd = "reminders"

// gitCmd is the group that holds clone, sync and setup-hooks, spelled once.
const gitCmd = "git"

// execute runs one sandbox-build invocation against a FRESH command tree, keeping the
// two streams apart so a test can prove records land on stdout alone.
func execute(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	var out, errOut bytes.Buffer

	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)

	err = cmd.ExecuteContext(t.Context())

	return out.String(), errOut.String(), err
}

//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestSandbox_AddThenList(t *testing.T) {
	dir := testrepo.New(t, "https://github.com/acme/widget.git")

	out, _, err := execute(t, remindersCmd, "list", "-C", dir)
	require.NoError(t, err, "a repository with no store yet lists nothing and succeeds")
	assert.Empty(t, out)

	out, _, err = execute(t, remindersCmd, "add", "-C", dir, "wire", "the", "stop", "hook")
	require.NoError(t, err)

	id := strings.TrimSpace(out)
	require.NotEmpty(t, id)

	out, stderr, err := execute(t, remindersCmd, "list", "-C", dir)
	require.NoError(t, err)
	assert.Equal(t, id+"\twire the stop hook\n", out)
	assert.Empty(t, stderr)
}

// TestSandbox_SharesTheHostStore: the sandbox build must land on the directory
// the host build resolves, or the host and the sandbox would each be looking
// at a list the other never sees. Both go through cli.App.Config, so this pins
// that the sandbox build does not grow a derivation of its own.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestSandbox_SharesTheHostStore(t *testing.T) {
	dir := testrepo.New(t, "git@github.com:Acme/Widget.git")

	_, _, err := execute(t, remindersCmd, "add", "-C", dir, "shared")
	require.NoError(t, err)

	cfg, err := (&cli.App{RepoDir: dir}).Config(t.Context())
	require.NoError(t, err)

	entries, err := os.ReadDir(filepath.Join(cfg.Dir, "items"))
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

// TestSandbox_ExposesOnlyListAndAdd pins the surface. Every host-only command
// refuses with exit 2 and says where it lives, whatever flags it is given, and
// none of them shows up in help.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestSandbox_ExposesOnlyListAndAdd(t *testing.T) {
	dir := testrepo.New(t, "https://github.com/acme/widget.git")

	tests := [][]string{
		{remindersCmd, "rm", "-C", dir, "x"},
		{remindersCmd, "reorder", "-C", dir, "x"},
		{remindersCmd, "path", "-C", dir},
		{gitCmd},
		{gitCmd, "clone", "https://github.com/acme/widget"},
		{gitCmd, "clone", "--no-such-flag"},
		{gitCmd, "sync", "-C", dir},
		{gitCmd, "setup-hooks", "--symlink"},
		{"upgrade", "--check"},
		{"completion", "zsh"},
	}

	for _, args := range tests {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, _, err := execute(t, args...)
			require.Error(t, err)
			assert.Equal(t, cli.ExitUsage, cli.ExitCode(err))
			assert.ErrorIs(t, err, errHostOnly)
		})
	}

	for _, args := range [][]string{{"--help"}, {remindersCmd, "--help"}} {
		out, _, err := execute(t, args...)
		require.NoError(t, err)

		for _, name := range []string{gitCmd, "upgrade", "completion", "reorder", "path"} {
			// Anchored at a line start, where help lists a command: "git"
			// also appears mid-line, in the description of -C.
			assert.NotContains(t, out, "\n  "+name+" ", "help must not list %q", name)
		}
	}
}

// TestSandbox_VersionNamesTheRole: the release comes first, which is what the
// host build's upgrade compares, and the role is named so a report says which
// build it came from.
func TestSandbox_VersionNamesTheRole(t *testing.T) {
	t.Parallel()

	out, _, err := execute(t, "--version")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(out, version+" (sandbox, "), out)
}

// TestSandbox_OutsideARepository is the case a Stop hook meets in a session
// that is not in a repository: a usage error, so the hook can tell it apart
// from a store it failed to read.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestSandbox_OutsideARepository(t *testing.T) {
	testrepo.New(t, "https://github.com/acme/widget.git")

	_, _, err := execute(t, remindersCmd, "list", "-C", t.TempDir())
	require.Error(t, err)
	assert.Equal(t, cli.ExitUsage, cli.ExitCode(err))
}
