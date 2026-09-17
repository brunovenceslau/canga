// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/brunovenceslau/devctl/internal/testrepo"

	"github.com/brunovenceslau/devctl/internal/cli"
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
			assert.Equal(t, cli.ExitUsage, exitCode(err))
		})
	}
}

// TestRoot_OutsideARepository: a directory with no origin is the caller
// pointing devctl somewhere wrong, which is a usage error, not a failure.
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
	for _, args := range [][]string{{}, {remindersCmd}} {
		out, err := execute(t, args...)
		require.NoError(t, err)
		assert.Contains(t, out, "Usage:")
	}
}

// TestRoot_RegistersEveryRemindersVerb: the verbs' behaviour is tested where
// they live, in internal/cli. What is devctl's own is WHICH of them it exposes,
// and on the host that is all five.
func TestRoot_RegistersEveryRemindersVerb(t *testing.T) {
	t.Parallel()

	reminders, _, err := newRootCmd().Find([]string{remindersCmd})
	require.NoError(t, err)

	names := make([]string, 0, len(reminders.Commands()))
	for _, sub := range reminders.Commands() {
		names = append(names, sub.Name())
	}

	assert.ElementsMatch(t, []string{"add", "list", "rm", "path", "reorder"}, names)
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
		{shell: "zsh", marker: "#compdef devctl"},
		{shell: "bash", marker: "__devctl"},
	}

	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			out, err := execute(t, "completion", tt.shell)
			require.NoError(t, err)
			require.Contains(t, out, tt.marker)
			require.Contains(t, out, "__complete",
				"the script must call back into devctl, which is what makes the ids real")

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
