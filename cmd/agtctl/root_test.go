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

// execute runs one agtctl invocation against a FRESH command tree, keeping the
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
func TestAgtctl_AddThenList(t *testing.T) {
	dir := testrepo.New(t, "https://github.com/acme/widget.git")

	out, _, err := execute(t, "reminders", "list", "-C", dir)
	require.NoError(t, err, "a repository with no store yet lists nothing and succeeds")
	assert.Empty(t, out)

	out, _, err = execute(t, "reminders", "add", "-C", dir, "wire", "the", "stop", "hook")
	require.NoError(t, err)

	id := strings.TrimSpace(out)
	require.NotEmpty(t, id)

	out, stderr, err := execute(t, "reminders", "list", "-C", dir)
	require.NoError(t, err)
	assert.Equal(t, id+"\twire the stop hook\n", out)
	assert.Empty(t, stderr)
}

// TestAgtctl_SharesDevctlsStore: agtctl must land on the directory devctl
// resolves, or the host and the sandbox would each be looking at a list the
// other never sees. Both go through cli.App.Config, so this pins that agtctl
// does not grow a derivation of its own.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestAgtctl_SharesDevctlsStore(t *testing.T) {
	dir := testrepo.New(t, "git@github.com:Acme/Widget.git")

	_, _, err := execute(t, "reminders", "add", "-C", dir, "shared")
	require.NoError(t, err)

	cfg, err := (&cli.App{RepoDir: dir}).Config(t.Context())
	require.NoError(t, err)

	entries, err := os.ReadDir(filepath.Join(cfg.Dir, "items"))
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

// TestAgtctl_ExposesOnlyListAndAdd pins the surface. A verb that is not
// compiled in is an unknown command, which exits 2 like any other bad
// invocation, rather than a hidden command that still runs.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestAgtctl_ExposesOnlyListAndAdd(t *testing.T) {
	dir := testrepo.New(t, "https://github.com/acme/widget.git")

	tests := [][]string{
		{"reminders", "rm", "-C", dir, "x"},
		{"reminders", "reorder", "-C", dir, "x"},
		{"reminders", "path", "-C", dir},
		{"clone", "https://github.com/acme/widget"},
		{"sync"},
		{"setup", "hooks"},
		{"upgrade"},
		{"completion", "zsh"},
	}

	for _, args := range tests {
		t.Run(strings.Join(args[:min(2, len(args))], " "), func(t *testing.T) {
			_, _, err := execute(t, args...)
			require.Error(t, err)
			assert.Equal(t, cli.ExitUsage, cli.ExitCode(err))
		})
	}
}

// TestAgtctl_OutsideARepository is the case a Stop hook meets in a session
// that is not in a repository: a usage error, so the hook can tell it apart
// from a store it failed to read.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestAgtctl_OutsideARepository(t *testing.T) {
	testrepo.New(t, "https://github.com/acme/widget.git")

	_, _, err := execute(t, "reminders", "list", "-C", t.TempDir())
	require.Error(t, err)
	assert.Equal(t, cli.ExitUsage, cli.ExitCode(err))
}
