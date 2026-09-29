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
	"github.com/brunovenceslau/canga/internal/upgrade"
	"github.com/spf13/cobra"
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

// TestSandbox_RefusesHostOnlyCommands pins the surface. Every host-only command
// refuses with exit 2 and says where it lives, whatever flags it is given, and
// none of them shows up in help.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestSandbox_RefusesHostOnlyCommands(t *testing.T) {
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
		{"completion", "zsh"},
		{"workspace", "https://github.com/acme/widget"},
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

		for _, name := range []string{gitCmd, "completion", "reorder", "path", "workspace"} {
			// Anchored at a line start, where help lists a command: "git"
			// also appears mid-line, in the description of -C.
			assert.NotContains(t, out, "\n  "+name+" ", "help must not list %q", name)
		}
	}
}

// TestSandbox_Upgrade: the sandbox build carries a real upgrade, listed in
// help, and its help says what differs in a sandbox: the binary is root's,
// and the provisioned release comes back when the sandbox is provisioned
// again. GH_TOKEN keeps
// `gh auth token` out of the run.
func TestSandbox_Upgrade(t *testing.T) {
	t.Setenv("GH_TOKEN", "token")

	out, _, err := execute(t, "--help")
	require.NoError(t, err)
	assert.Contains(t, out, "\n  upgrade ", "help must list upgrade")

	out, _, err = execute(t, "upgrade", "--help")
	require.NoError(t, err)
	assert.Contains(t, out, "release's sandbox build", "the sandbox must upgrade into the sandbox build")
	assert.Contains(t, out, "sudo canga upgrade")
	assert.Contains(t, out, "the tag passed to install_sandbox.sh")
	assert.Contains(t, out, "is provisioned again")
	assert.Contains(t, out, "sudo canga upgrade --tag "+upgrade.MinReleaseTag, "the example must name a release the floor accepts")
	// The release floor refuses every release whose sandbox build had no
	// upgrade command, so help must not describe --tag installing one.
	assert.NotContains(t, out, "v0.6.0")

	// A test binary reports "dev", which is refused before any request: that
	// the refusal is the upgrade's own, and not errHostOnly, is the point.
	_, _, err = execute(t, "upgrade")
	require.ErrorIs(t, err, upgrade.ErrNotRelease)
	require.NotErrorIs(t, err, errHostOnly)
	assert.Equal(t, cli.ExitUsage, cli.ExitCode(err))
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

// TestSandbox_OriginLessIsKeyedByItsHostPath is the arrangement the fallback
// exists for: HOME is the sandbox's own, the repository is mounted at its HOST
// path, and the environment file hands over the host's base directory the way
// it hands over CANGA_REMINDERS_DIR. The key is the path below that base, and
// the store is the one the host build resolves for the same tree.
func TestSandbox_OriginLessIsKeyedByItsHostPath(t *testing.T) {
	testrepo.Hermetic(t)

	hostBase, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	dir := filepath.Join(hostBase, "local", "OS")
	testrepo.Init(t, dir, "")

	// $HOME/src must play no part: nothing is mounted under the sandbox's home.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CANGA_SRC_DIR", hostBase)

	out, _, err := execute(t, remindersCmd, "add", "-C", dir, "from", "the", "sandbox")
	require.NoError(t, err)

	id := strings.TrimSpace(out)

	out, _, err = execute(t, remindersCmd, "list", "-C", dir)
	require.NoError(t, err)
	assert.Equal(t, id+"\tfrom the sandbox\n", out)

	_, err = os.Stat(filepath.Join(os.Getenv(cli.ReminderDirVar), "local", "!o!s", cli.ScopeRepo, "items", id+".md"))
	require.NoError(t, err, "the item must land under the path-derived key, case escaped")
}

// TestSandbox_OriginLessWithoutTheBaseIsRefused: the sandbox build never
// defaults the base to its own $HOME/src, which would key a tree by a path the
// host does not have. Unset, an origin-less repository keeps today's usage
// error, and the message names the variable to set.
func TestSandbox_OriginLessWithoutTheBaseIsRefused(t *testing.T) {
	testrepo.Hermetic(t)

	home := t.TempDir()
	t.Setenv("HOME", home)

	// Exactly where a $HOME/src default would find it.
	dir := filepath.Join(home, "src", "local", "OS")
	testrepo.Init(t, dir, "")

	_, _, err := execute(t, remindersCmd, "add", "-C", dir, "nope")
	require.Error(t, err)
	assert.Equal(t, cli.ExitUsage, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "CANGA_SRC_DIR")
}

// TestSandbox_RenamedBaseVariableIsAUsageError: an environment file still
// setting CANGA_HOST_BASE_DIR must fail loudly in the sandbox too, not quietly
// key nothing, even with CANGA_SRC_DIR set beside it.
func TestSandbox_RenamedBaseVariableIsAUsageError(t *testing.T) {
	testrepo.Hermetic(t)

	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	dir := filepath.Join(base, "local", "OS")
	testrepo.Init(t, dir, "")

	t.Setenv("CANGA_SRC_DIR", base)
	t.Setenv("CANGA_HOST_BASE_DIR", base)

	_, _, err = execute(t, remindersCmd, "add", "-C", dir, "nope")
	require.Error(t, err)
	assert.Equal(t, cli.ExitUsage, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "CANGA_HOST_BASE_DIR was renamed to CANGA_SRC_DIR")
}

// TestSandbox_RelativeBaseIsAUsageError: a relative CANGA_SRC_DIR would be
// resolved against a working directory the host never had, so the sandbox
// build refuses it, exit 2, and names the variable (ship-gate round 2,
// test-engineer Low: covered only below the CLI before).
func TestSandbox_RelativeBaseIsAUsageError(t *testing.T) {
	testrepo.Hermetic(t)

	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	dir := filepath.Join(base, "local", "OS")
	testrepo.Init(t, dir, "")

	// Relative, and naming the right directory from where the test stands:
	// only the refusal can make it fail.
	t.Chdir(base)
	t.Setenv("CANGA_SRC_DIR", ".")

	_, _, err = execute(t, remindersCmd, "add", "-C", dir, "nope")
	require.Error(t, err)
	assert.Equal(t, cli.ExitUsage, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "CANGA_SRC_DIR")
}

// TestSandbox_EveryLeafCommandHasAnExample: help shows a command before it
// explains it, so every visible command that does the work carries an
// Examples section. Groups print help and are left out, as are the hidden
// host-only stubs.
func TestSandbox_EveryLeafCommandHasAnExample(t *testing.T) {
	t.Parallel()

	assert.Empty(t, commandsWithoutExample(newRootCmd()), "commands without an Example")
}

// commandsWithoutExample walks cmd's tree and returns the path of every leaf
// command with no Example.
func commandsWithoutExample(cmd *cobra.Command) []string {
	var missing []string

	for _, sub := range cmd.Commands() {
		if sub.Hidden || sub.Name() == "help" {
			continue
		}

		if sub.HasAvailableSubCommands() {
			missing = append(missing, commandsWithoutExample(sub)...)

			continue
		}

		if strings.TrimSpace(sub.Example) == "" {
			missing = append(missing, sub.CommandPath())
		}
	}

	return missing
}
