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
		{name: "path with too many ids", args: []string{remindersCmd, "path", "a", "b"}},
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
	assert.ElementsMatch(t, []string{"add", "list", "rm", "path", "reorder"}, commandNames(reminders))
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
	t.Setenv("CANGA_HOST_BASE_DIR", t.TempDir())

	missing := filepath.Join(t.TempDir(), "missing")
	target := filepath.Join(t.TempDir(), "target")

	tests := []struct {
		name string
		args []string
	}{
		{name: "clone", args: []string{"clone", missing, target}},
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
	assert.ElementsMatch(t, []string{gitCmd, remindersCmd, "upgrade"}, commandNames(root))

	git, _, err := root.Find([]string{gitCmd})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"clone", "setup-hooks", "sync"}, commandNames(git))
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
