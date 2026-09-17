// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/brunovenceslau/devctl/internal/cli"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hermeticGit points git at empty system and global config files, so nothing
// the developer or the sandbox configured can reach a test.
func hermeticGit(t *testing.T) {
	t.Helper()

	global := filepath.Join(t.TempDir(), "gitconfig")
	require.NoError(t, os.WriteFile(global, nil, 0o600))

	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_GLOBAL", global)
}

// gitRun runs one git command for a fixture, failing the test if it refuses.
func gitRun(t *testing.T, args ...string) {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "git", args...).CombinedOutput()
	require.NoErrorf(t, err, "git %v: %s", args, out)
}

// gitCommit writes a file and commits it with an inline identity, so nothing
// depends on the machine's git config, which hermeticGit has emptied.
func gitCommit(t *testing.T, dir, name, content string) {
	t.Helper()

	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
	gitRun(t, "-C", dir, "add", name)
	gitRun(t, "-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com",
		"-c", "commit.gpgsign=false", "commit", "-qm", "commit "+name)
}

// cloneSource is a repository for a clone to land on.
func cloneSource(t *testing.T) string {
	t.Helper()

	hermeticGit(t)

	dir := filepath.Join(t.TempDir(), "source")
	gitRun(t, "init", "-q", "-b", "main", dir)
	gitCommit(t, dir, "file", "one\n")

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

// A sandbox signs through /etc/gitconfig with no user.signingkey to find. The
// clone must not be reported as unsigned, which would send the user to fix
// something that works.
func TestCloneCmd_SaysWhenSigningIsInherited(t *testing.T) {
	source := cloneSource(t)
	target := filepath.Join(t.TempDir(), "clone")

	system := filepath.Join(t.TempDir(), "gitconfig")
	require.NoError(t, os.WriteFile(system, []byte("[commit]\n\tgpgSign = true\n"), 0o600))
	t.Setenv("GIT_CONFIG_SYSTEM", system)
	t.Setenv("DEVCTL_SIGNING_KEY", "")
	t.Setenv("DEVCTL_ALLOWED_SIGNERS", "")

	_, stderr, err := executeSplit(t, "clone", source, target)
	require.NoError(t, err)
	assert.Contains(t, stderr, "signing on")
	assert.Contains(t, stderr, "inherited")
	assert.NotContains(t, stderr, "signing OFF")
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
	assert.Equal(t, cli.ExitFailure, exitCode(err))

	t.Setenv("DEVCTL_BASE_DIR", t.TempDir())

	_, err = execute(t, "clone", "not-a-url")
	require.Error(t, err)
	assert.Equal(t, cli.ExitUsage, exitCode(err))

	// Wrong argument count is cobra's own refusal, and must exit 2 as well.
	_, err = execute(t, "clone")
	require.Error(t, err)
	assert.Equal(t, cli.ExitUsage, exitCode(err))

	_, err = execute(t, "clone", "a", "b", "c")
	require.Error(t, err)
	assert.Equal(t, cli.ExitUsage, exitCode(err))
}

// Completion is driven through cobra's own `__complete`, not by calling the
// function directly: a direct call proves what the function returns, and
// deleting the ValidArgsFunction that wires it to the command would leave that
// test green while the shell fell back to offering files on the URL position.
func TestCloneCmd_Completion(t *testing.T) {
	t.Parallel()

	// cobra renders the directive as the last line, as ":<bitmask>".
	noFiles := ":" + strconv.Itoa(int(cobra.ShellCompDirectiveNoFileComp))
	dirsOnly := ":" + strconv.Itoa(int(cobra.ShellCompDirectiveFilterDirs))

	out, err := execute(t, "__complete", "clone", "")
	require.NoError(t, err)
	assert.Contains(t, out, noFiles, "the url position offers nothing, not files")

	out, err = execute(t, "__complete", "clone", "https://example.com/o/r", "")
	require.NoError(t, err)
	assert.Contains(t, out, dirsOnly, "the target position offers directories")

	// -C names a repository, so it completes directories too.
	out, err = execute(t, "__complete", "--repo", "")
	require.NoError(t, err)
	assert.Contains(t, out, dirsOnly)
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
