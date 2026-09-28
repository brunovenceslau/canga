// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/brunovenceslau/canga/internal/testrepo"

	"github.com/brunovenceslau/canga/internal/cli"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// remindersCmd is the subcommand every test below drives.
const remindersCmd = "reminders"

// gitCmd is the group that holds clone, sync and setup-hooks, spelled once.
const gitCmd = "git"

// cloneCmd is the git subcommand most spelled out below.
const cloneCmd = "clone"

// pathCmd is `reminders path`, which prints the store directory a key resolves.
const pathCmd = "path"

// execute runs one canga invocation against a FRESH command tree — cobra
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
		{name: "path with too many ids", args: []string{remindersCmd, pathCmd, "a", "b"}},
		{name: "unknown git subcommand", args: []string{gitCmd, "bogus"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := execute(t, tt.args...)
			require.Error(t, err)
			assert.Equal(t, cli.ExitUsage, exitCode(err))
		})
	}
}

// TestRoot_OutsideARepository: a directory with no origin is the caller
// pointing canga somewhere wrong, which is a usage error, not a failure.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestRoot_OutsideARepository(t *testing.T) {
	testrepo.New(t, "git@github.com:acme/widget.git")

	_, err := execute(t, remindersCmd, "list", "-C", t.TempDir())
	require.Error(t, err)
	assert.Equal(t, cli.ExitUsage, exitCode(err))
}

//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestRoot_BareCommandsPrintHelp(t *testing.T) {
	for _, args := range [][]string{{}, {remindersCmd}, {gitCmd}} {
		out, err := execute(t, args...)
		require.NoError(t, err)
		assert.Contains(t, out, "Usage:")
	}
}

// TestRoot_RegistersEveryRemindersVerb: the verbs' behaviour is tested where
// they live, in internal/cli. What is canga's own is WHICH of them it exposes,
// and on the host that is all five.
func TestRoot_RegistersEveryRemindersVerb(t *testing.T) {
	t.Parallel()

	reminders, _, err := newRootCmd().Find([]string{remindersCmd})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"add", "list", "rm", pathCmd, "reorder"}, commandNames(reminders))
}

// TestRoot_OldGitNamesAreGone: the names the git commands had before the
// group are removed, not aliased, so each is an unknown command. Every
// argument points into a temporary directory and git is isolated, so a
// regression that brought one back fails here without cloning, fetching or
// touching the developer's own repositories.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestRoot_OldGitNamesAreGone(t *testing.T) {
	hermeticGit(t)
	t.Setenv("CANGA_SRC_DIR", t.TempDir())

	missing := filepath.Join(t.TempDir(), "missing")
	target := filepath.Join(t.TempDir(), "target")

	tests := []struct {
		name string
		args []string
	}{
		{name: cloneCmd, args: []string{cloneCmd, missing, target}},
		{name: "sync", args: []string{"sync", "-C", t.TempDir()}},
		{name: "setup", args: []string{"setup", "hooks", "-C", t.TempDir()}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := execute(t, tt.args...)
			require.Error(t, err)
			assert.Equal(t, cli.ExitUsage, exitCode(err))
			assert.Contains(t, err.Error(), `unknown command "`+tt.name+`" for "canga"`)
			assert.NoDirExists(t, target)
		})
	}
}

// TestRoot_CommandTree pins the root's commands and the git group's. The
// sandbox build refuses host-only commands by name, one stub per root
// command, so a command added to the root here needs a stub there; adding it
// under git needs nothing. Both halves are checked so neither drifts silently.
func TestRoot_CommandTree(t *testing.T) {
	t.Parallel()

	root := newRootCmd()
	assert.ElementsMatch(t, []string{gitCmd, remindersCmd, "upgrade", "workspace"}, commandNames(root))

	git, _, err := root.Find([]string{gitCmd})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{cloneCmd, "setup-hooks", "sync"}, commandNames(git))
}

// commandNames lists a command's direct subcommands by name.
func commandNames(cmd *cobra.Command) []string {
	names := make([]string, 0, len(cmd.Commands()))
	for _, sub := range cmd.Commands() {
		names = append(names, sub.Name())
	}

	return names
}

// TestCompletionScript asserts the generated completion rather than eyeballing
// it. The shape check runs everywhere; the parse runs wherever the shell is
// installed, which on the CI matrix is at least the macOS leg for zsh.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestCompletionScript(t *testing.T) {
	tests := []struct {
		shell  string
		marker string
	}{
		{shell: "zsh", marker: "#compdef canga"},
		{shell: "bash", marker: "__canga"},
	}

	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			out, err := execute(t, "completion", tt.shell)
			require.NoError(t, err)
			require.Contains(t, out, tt.marker)
			require.Contains(t, out, "__complete",
				"the script must call back into canga, which is what makes the ids real")

			shell, err := exec.LookPath(tt.shell)
			if err != nil {
				t.Skipf("%s is not installed; the generated script was still checked for shape", tt.shell)
			}

			script := filepath.Join(t.TempDir(), "completion."+tt.shell)
			require.NoError(t, os.WriteFile(script, []byte(out), 0o600))

			parsed, err := exec.CommandContext(t.Context(), shell, "-n", script).CombinedOutput()
			require.NoErrorf(t, err, "%s -n: %s", tt.shell, parsed)
		})
	}
}

// TestRoot_OriginLessIsKeyedByItsPathUnderTheBase: the host build takes the
// base from the same place `canga git clone` does, $HOME/src unless
// CANGA_SRC_DIR says otherwise, so ~/src/local/OS keys as local/OS.
func TestRoot_OriginLessIsKeyedByItsPathUnderTheBase(t *testing.T) {
	testrepo.Hermetic(t)

	home, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	t.Setenv("HOME", home)

	dir := filepath.Join(home, "src", "local", "OS")
	testrepo.Init(t, dir, "")

	out, err := execute(t, remindersCmd, pathCmd, "-C", dir)
	require.NoError(t, err)
	assert.Equal(t,
		filepath.Join(os.Getenv(cli.ReminderDirVar), "local", "!o!s", cli.ScopeRepo)+"\n", out)

	// And outside the base, today's usage error, now saying why.
	outside := filepath.Join(t.TempDir(), "repo")
	testrepo.Init(t, outside, "")

	_, err = execute(t, remindersCmd, "list", "-C", outside)
	require.Error(t, err)
	assert.Equal(t, cli.ExitUsage, exitCode(err))
	assert.Contains(t, err.Error(), "not below")
}

// TestRoot_RenamedBaseVariableIsAUsageError: the old name is a configuration
// the caller has to fix, so it exits 2 wherever the base is read.
func TestRoot_RenamedBaseVariableIsAUsageError(t *testing.T) {
	testrepo.Hermetic(t)
	t.Setenv("CANGA_HOST_BASE_DIR", t.TempDir())

	dir := filepath.Join(t.TempDir(), "repo")
	testrepo.Init(t, dir, "")

	for _, args := range [][]string{
		{gitCmd, cloneCmd, workspaceURL},
		{workspaceCmd, workspaceURL},
		{remindersCmd, "list", "-C", dir},
	} {
		_, err := execute(t, args...)
		require.Error(t, err, "%v", args)
		assert.Equal(t, cli.ExitUsage, exitCode(err), "%v: %v", args, err)
		assert.Contains(t, err.Error(), "CANGA_SRC_DIR", "%v", args)
	}
}

// TestRoot_RefusesALinkPlantedInTheSharedStore is the auditor's PoC 11 through
// the host command: a sandbox, which shares the store root, replaces an
// intermediate key directory with a link to a host path. The host's `add`
// must refuse, and write nothing there.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestRoot_RefusesALinkPlantedInTheSharedStore(t *testing.T) {
	dir := testrepo.New(t, "git@github.com:acme/widget.git")
	store := os.Getenv(cli.ReminderDirVar)
	hostPath := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(store, "github.com"), 0o700))
	require.NoError(t, os.Symlink(hostPath, filepath.Join(store, "github.com", "acme")))

	_, err := execute(t, remindersCmd, "add", "-C", dir, "escaped")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symbolic link")

	entries, err := os.ReadDir(hostPath)
	require.NoError(t, err)
	assert.Empty(t, entries, "the refused add wrote through the planted link")
}
