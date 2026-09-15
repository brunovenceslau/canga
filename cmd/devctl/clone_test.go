// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cloneSource is a repository for a clone to land on, with the machine's git
// config neutralized so nothing the developer configured reaches the test.
func cloneSource(t *testing.T) string {
	t.Helper()

	global := filepath.Join(t.TempDir(), "gitconfig")
	require.NoError(t, os.WriteFile(global, nil, 0o600))

	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_GLOBAL", global)

	dir := filepath.Join(t.TempDir(), "source")

	run := func(args ...string) {
		out, err := exec.CommandContext(t.Context(), "git", args...).CombinedOutput()
		require.NoErrorf(t, err, "git %v: %s", args, out)
	}

	run("init", "-q", "-b", "main", dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file"), []byte("one\n"), 0o600))
	run("-C", dir, "add", "file")
	run("-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com",
		"-c", "commit.gpgsign=false", "commit", "-qm", "one")

	return dir
}

// The path is printed so that `cd $(devctl clone <url>)` works, which it only
// does if NOTHING else reaches stdout — not git's progress, not the signing
// line, not a warning.
func TestCloneCmd_PrintsOnlyThePathOnStdout(t *testing.T) {
	source := cloneSource(t)
	target := filepath.Join(t.TempDir(), "clone")

	t.Setenv("DEVCTL_SIGNING_KEY", "a-key")

	stdout, stderr, err := executeSplit(t, "clone", source, target)
	require.NoError(t, err)
	assert.Equal(t, target+"\n", stdout)
	assert.Contains(t, stderr, "signing on")
	assert.FileExists(t, filepath.Join(target, "file"))
}

func TestCloneCmd_SaysWhenSigningIsOff(t *testing.T) {
	source := cloneSource(t)
	target := filepath.Join(t.TempDir(), "clone")

	t.Setenv("DEVCTL_SIGNING_KEY", "")
	t.Setenv("DEVCTL_ALLOWED_SIGNERS", "")

	_, stderr, err := executeSplit(t, "clone", source, target)
	require.NoError(t, err)
	assert.Contains(t, stderr, "signing OFF")
	assert.Contains(t, stderr, "DEVCTL_SIGNING_KEY",
		"the message must name what to set to fix it")
}

// The two refusals exit differently, and the difference is the contract: a
// non-empty target is a runtime failure the user resolves, an unparseable URL
// is a typo on the command line.
func TestCloneCmd_ExitCodes(t *testing.T) {
	source := cloneSource(t)

	occupied := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(occupied, "x"), []byte("x"), 0o600))

	_, err := execute(t, "clone", source, occupied)
	require.Error(t, err)
	assert.Equal(t, exitFailure, exitCode(err))

	t.Setenv("DEVCTL_BASE_DIR", t.TempDir())

	_, err = execute(t, "clone", "not-a-url")
	require.Error(t, err)
	assert.Equal(t, exitUsage, exitCode(err))

	// Wrong argument count is cobra's own refusal, and must exit 2 as well.
	_, err = execute(t, "clone")
	require.Error(t, err)
	assert.Equal(t, exitUsage, exitCode(err))

	_, err = execute(t, "clone", "a", "b", "c")
	require.Error(t, err)
	assert.Equal(t, exitUsage, exitCode(err))
}

// A repository is a directory, so neither position offers file completion: the
// URL offers nothing at all, the target offers directories only.
func TestCloneCmd_Completion(t *testing.T) {
	t.Parallel()

	_, directive := completeCloneArgs(nil, nil, "")
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)

	_, directive = completeCloneArgs(nil, []string{"https://example.com/o/r"}, "")
	assert.Equal(t, cobra.ShellCompDirectiveFilterDirs, directive)
}

// The command must be reachable by name, with the help text a user reads before
// running something that writes to their disk.
func TestCloneCmd_IsRegistered(t *testing.T) {
	t.Parallel()

	out, err := execute(t, "help", "clone")
	require.NoError(t, err)
	assert.Contains(t, out, "DEVCTL_BASE_DIR")
	assert.Contains(t, out, "clone <url> [dir]")
}
