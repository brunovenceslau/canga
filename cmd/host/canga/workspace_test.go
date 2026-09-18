// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/brunovenceslau/canga/internal/cli"
	"github.com/brunovenceslau/canga/internal/workspace"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// workspaceCmd is the command every test below drives.
const workspaceCmd = "workspace"

// workspaceURL is the repository the tests below open.
const workspaceURL = "https://github.com/acme/widget"

// workspaceLayout points the clone base and the envs root at directories of
// the test's own, creates the directories named, and puts a `cmux` that prints
// what the real one prints on success first on PATH.
func workspaceLayout(t *testing.T, clone, env bool) {
	t.Helper()

	base, envs, bin := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("CANGA_HOST_BASE_DIR", base)
	t.Setenv(workspace.EnvsDirVar, envs)
	t.Setenv("PATH", bin)

	require.NoError(t, os.WriteFile(filepath.Join(bin, "cmux"),
		[]byte("#!/bin/sh\necho OK workspace:3\n"), 0o755))

	for dir, want := range map[string]bool{base: clone, envs: env} {
		if want {
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "github.com", "acme", "widget"), 0o755))
		}
	}
}

//nolint:paralleltest // t.Setenv forbids it
func TestWorkspaceCmd_Opens(t *testing.T) {
	workspaceLayout(t, true, true)

	stdout, stderr, err := executeSplit(t, workspaceCmd, workspaceURL)
	require.NoError(t, err)
	assert.Equal(t, "OK workspace:3\n", stdout)
	assert.Empty(t, stderr)
}

// An unset root and a bad URL are fixed by the caller before anything can
// work; a missing directory is a state of the disk the user resolves.
func TestWorkspaceCmd_ExitCodes(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		clone, env  bool
		unsetEnvDir bool
		want        int
	}{
		{
			name: "envs root unset", args: []string{workspaceURL}, clone: true, env: true,
			unsetEnvDir: true, want: cli.ExitUsage,
		},
		{name: "bad url", args: []string{"not-a-url"}, clone: true, env: true, want: cli.ExitUsage},
		{name: "no url", args: nil, want: cli.ExitUsage},
		{name: "two urls", args: []string{workspaceURL, workspaceURL}, want: cli.ExitUsage},
		{name: "not cloned", args: []string{workspaceURL}, env: true, want: cli.ExitFailure},
		{name: "no environment", args: []string{workspaceURL}, clone: true, want: cli.ExitFailure},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspaceLayout(t, tt.clone, tt.env)

			if tt.unsetEnvDir {
				t.Setenv(workspace.EnvsDirVar, "")
			}

			stdout, _, err := executeSplit(t, append([]string{workspaceCmd}, tt.args...)...)
			require.Error(t, err)
			assert.Equal(t, tt.want, exitCode(err))
			assert.Empty(t, stdout, "cmux must not run")
		})
	}
}

// Driven through `__complete` for the reason TestCloneCmd_Completion gives.
func TestWorkspaceCmd_Completion(t *testing.T) {
	t.Parallel()

	out, err := execute(t, "__complete", workspaceCmd, "")
	require.NoError(t, err)
	assert.Contains(t, out, ":"+strconv.Itoa(int(cobra.ShellCompDirectiveNoFileComp)),
		"the url position offers nothing, not files")
}
