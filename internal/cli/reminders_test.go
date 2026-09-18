// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunovenceslau/canga/internal/testrepo"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// execute runs `reminders <args>` through a FRESH tree carrying every verb —
// cobra accumulates flag state across Execute calls, so reusing one would leak
// the previous run's flags into this one.
func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	a := &App{}
	root := &cobra.Command{Use: "test", SilenceUsage: true, SilenceErrors: true}
	a.BindRepoFlag(root)
	root.AddCommand(NewRemindersCmd(a,
		RemindersAdd, RemindersList, RemindersRemove, RemindersPath, RemindersReorder))
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"reminders"}, args...))

	err := root.ExecuteContext(t.Context())

	return out.String(), err
}

// records splits a listing into its <id>\t<text> records.
func records(t *testing.T, out string) [][2]string {
	t.Helper()

	var parsed [][2]string

	for line := range strings.SplitSeq(strings.TrimSuffix(out, "\n"), "\n") {
		if line == "" {
			continue
		}

		id, text, ok := strings.Cut(line, "\t")
		require.Truef(t, ok, "listing line is not a record: %q", line)

		parsed = append(parsed, [2]string{id, text})
	}

	return parsed
}

// TestNewRemindersCmd_RegistersOnlyTheVerbsGiven is what the sandbox build's narrower
// surface rests on: a verb left out is not reachable, not merely undocumented.
func TestNewRemindersCmd_RegistersOnlyTheVerbsGiven(t *testing.T) {
	t.Parallel()

	cmd := NewRemindersCmd(&App{}, RemindersAdd, RemindersList)

	var names []string
	for _, sub := range cmd.Commands() {
		names = append(names, sub.Name())
	}

	assert.ElementsMatch(t, []string{"add", "list"}, names)
}

//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestReminders_AddListRemove(t *testing.T) {
	dir := testrepo.New(t, "git@github.com:acme/widget.git")

	out, err := execute(t, "list", "-C", dir)
	require.NoError(t, err, "a repository with no store yet lists nothing and succeeds")
	assert.Empty(t, out)

	// The text is joined from the arguments, so it needs no quoting.
	out, err = execute(t, "add", "-C", dir, "wire", "the", "purge", "warning")
	require.NoError(t, err)

	id := strings.TrimSpace(out)
	require.NotEmpty(t, id)

	out, err = execute(t, "list", "-C", dir)
	require.NoError(t, err)
	assert.Equal(t, [][2]string{{id, "wire the purge warning"}}, records(t, out))

	out, err = execute(t, "path", "-C", dir, id)
	require.NoError(t, err)

	body, readErr := os.ReadFile(strings.TrimSpace(out))
	require.NoError(t, readErr)
	assert.Contains(t, string(body), "repo: github.com/acme/widget")
	assert.Contains(t, string(body), "wire the purge warning")

	_, err = execute(t, "rm", "-C", dir, id)
	require.NoError(t, err)

	out, err = execute(t, "list", "-C", dir)
	require.NoError(t, err)
	assert.Empty(t, out)
}

//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestReminders_RmReportsEveryFailure(t *testing.T) {
	dir := testrepo.New(t, "git@github.com:acme/widget.git")

	out, err := execute(t, "add", "-C", dir, "keep me")
	require.NoError(t, err)

	id := strings.TrimSpace(out)

	// The good id is still removed even though the other two fail, and both
	// failures are reported rather than the first one masking the second.
	_, err = execute(t, "rm", "-C", dir, "missing", id, "alsomissing")
	require.Error(t, err)
	assert.Equal(t, ExitFailure, ExitCode(err))
	assert.Contains(t, err.Error(), "missing")
	assert.Contains(t, err.Error(), "alsomissing")

	out, err = execute(t, "list", "-C", dir)
	require.NoError(t, err)
	assert.Empty(t, out)
}

// TestReminders_PathCreatesNothing: asking where something would live must not
// bring it into being.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestReminders_PathCreatesNothing(t *testing.T) {
	dir := testrepo.New(t, "git@github.com:acme/widget.git")

	out, err := execute(t, "path", "-C", dir)
	require.NoError(t, err)

	base := os.Getenv(ReminderDirVar)
	assert.Equal(t, filepath.Join(base, "github.com", "acme", "widget", ScopeRepo), strings.TrimSpace(out))

	_, err = os.Stat(base)
	require.ErrorIs(t, err, os.ErrNotExist, "path must not create the store")

	// Listing must not create it either.
	_, err = execute(t, "list", "-C", dir)
	require.NoError(t, err)

	_, err = os.Stat(base)
	require.ErrorIs(t, err, os.ErrNotExist, "list must not create the store")
}

//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestReminders_Reorder(t *testing.T) {
	dir := testrepo.New(t, "git@github.com:acme/widget.git")

	ids := make([]string, 0, 3)

	for _, text := range []string{"first", "second", "third"} {
		out, err := execute(t, "add", "-C", dir, text)
		require.NoError(t, err)

		ids = append(ids, strings.TrimSpace(out))
	}

	_, err := execute(t, "reorder", "-C", dir, ids[2])
	require.NoError(t, err)

	out, err := execute(t, "list", "-C", dir)
	require.NoError(t, err)

	got := make([]string, 0, 3)
	for _, record := range records(t, out) {
		got = append(got, record[1])
	}

	assert.Equal(t, []string{"third", "first", "second"}, got)
}

// TestCompleteIDs covers the claim that moved the completion into the binary:
// it offers REAL stored ids, each with the reminder's own first line as the
// description a shell renders beside it.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestCompleteIDs(t *testing.T) {
	dir := testrepo.New(t, "git@github.com:acme/widget.git")
	application := &App{RepoDir: dir}

	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())

	completions, directive := application.completeIDs(cmd, nil, "")
	assert.Empty(t, completions, "a repository with no store offers nothing")
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)

	ids := make([]string, 0, 2)

	for _, text := range []string{"headline one\nand a second line", "headline two"} {
		out, err := execute(t, "add", "-C", dir, text)
		require.NoError(t, err)

		ids = append(ids, strings.TrimSpace(out))
	}

	completions, directive = application.completeIDs(cmd, nil, "")
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
	assert.ElementsMatch(t, []string{
		ids[0] + "\theadline one",
		ids[1] + "\theadline two",
	}, completions, "the description must be the reminder's first line only")

	// An id already on the command line is not offered again.
	completions, _ = application.completeIDs(cmd, []string{ids[0]}, "")
	assert.Equal(t, []string{ids[1] + "\theadline two"}, completions)

	// A prefix narrows the candidates.
	completions, _ = application.completeIDs(cmd, nil, ids[0])
	assert.Equal(t, []string{ids[0] + "\theadline one"}, completions)

	completions, _ = application.completeIDs(cmd, nil, "nosuchprefix")
	assert.Empty(t, completions)
}

// TestReminders_MovesALegacyStore drives a real command against a store left
// at the pre-rename default: the first command moves it, the reminder in it is
// listed, stdout carries only records, and the next command says nothing.
func TestReminders_MovesALegacyStore(t *testing.T) {
	dir := testrepo.New(t, "git@github.com:acme/widget.git")

	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv(ReminderDirVar, "")

	// Record a reminder at the OLD default, the way devctl <= v0.5 would.
	legacy := filepath.Join(data, "devctl", "reminders")
	t.Setenv(ReminderDirVar, legacy)

	out, err := execute(t, "add", "-C", dir, "from before the rename")
	require.NoError(t, err)

	id := strings.TrimSpace(out)

	t.Setenv(ReminderDirVar, "")

	run := func() (string, string) {
		var stdout, stderr bytes.Buffer

		a := &App{}
		root := &cobra.Command{Use: "test", SilenceUsage: true, SilenceErrors: true}
		a.BindRepoFlag(root)
		root.AddCommand(NewRemindersCmd(a, RemindersList))
		root.SetOut(&stdout)
		root.SetErr(&stderr)
		root.SetArgs([]string{"reminders", "list", "-C", dir})
		require.NoError(t, root.ExecuteContext(t.Context()))

		return stdout.String(), stderr.String()
	}

	stdout, stderr := run()
	assert.Equal(t, [][2]string{{id, "from before the rename"}}, records(t, stdout))
	assert.Contains(t, stderr, "moved the reminders store from "+legacy)
	assert.NoDirExists(t, filepath.Dir(legacy), "the emptied devctl directory is tidied up")
	assert.DirExists(t, filepath.Join(data, Project, "reminders"))

	stdout, stderr = run()
	assert.Len(t, records(t, stdout), 1)
	assert.Empty(t, stderr, "a store already moved is not mentioned again")
}

// TestReminders_AFailedMoveStopsTheCommand: if the old store cannot be moved,
// the command must not go on to create an empty store at the new root, after
// which the old one would never be looked at again.
func TestReminders_AFailedMoveStopsTheCommand(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the directory permission this case relies on")
	}

	dir := testrepo.New(t, "git@github.com:acme/widget.git")

	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv(ReminderDirVar, "")

	legacy := filepath.Join(data, "devctl", "reminders")
	require.NoError(t, os.MkdirAll(legacy, 0o700))

	// The data directory refuses new entries, so neither the new root nor the
	// rename into it can happen.
	require.NoError(t, os.Chmod(data, 0o500))
	t.Cleanup(func() { _ = os.Chmod(data, 0o700) })

	_, err := execute(t, "add", "-C", dir, "must not land in a fresh store")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "move the reminders store")

	assert.DirExists(t, legacy)
	assert.NoDirExists(t, filepath.Join(data, Project))
}

// legacyFixture records one reminder at the pre-rename default and returns the
// repository, the reminder's id, and the old and new store roots.
func legacyFixture(t *testing.T) (dir, id, legacyRoot, currentRoot string) {
	t.Helper()

	dir = testrepo.New(t, "git@github.com:acme/widget.git")

	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)

	legacyRoot = filepath.Join(data, "devctl", "reminders")
	currentRoot = filepath.Join(data, Project, "reminders")

	t.Setenv(ReminderDirVar, legacyRoot)

	out, err := execute(t, "add", "-C", dir, "from before the rename")
	require.NoError(t, err)

	t.Setenv(ReminderDirVar, "")

	return dir, strings.TrimSpace(out), legacyRoot, currentRoot
}

// TestReminders_WarnsAboutAStrandedLegacyStore: when the new root already
// exists, the old store is never moved over it (it may be a directory a
// running sandbox has mounted), and every command says so on stderr.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestReminders_WarnsAboutAStrandedLegacyStore(t *testing.T) {
	dir, _, legacyRoot, currentRoot := legacyFixture(t)
	require.NoError(t, os.MkdirAll(currentRoot, 0o700))

	for range 2 {
		var stdout, stderr bytes.Buffer

		a := &App{}
		root := &cobra.Command{Use: "test", SilenceUsage: true, SilenceErrors: true}
		a.BindRepoFlag(root)
		root.AddCommand(NewRemindersCmd(a, RemindersList))
		root.SetOut(&stdout)
		root.SetErr(&stderr)
		root.SetArgs([]string{"reminders", "list", "-C", dir})
		require.NoError(t, root.ExecuteContext(t.Context()))

		assert.Empty(t, stdout.String())
		assert.Contains(t, stderr.String(), legacyRoot)
		assert.Contains(t, stderr.String(), "are not read")
	}

	assert.DirExists(t, legacyRoot, "a stranded store is reported, never moved")
}

// TestReminders_PathMovesALegacyStore: `path` prints where the reminders are,
// so it moves a store the rename left behind before answering.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestReminders_PathMovesALegacyStore(t *testing.T) {
	dir, _, legacyRoot, currentRoot := legacyFixture(t)

	out, err := execute(t, "path", "-C", dir)
	require.NoError(t, err)

	assert.Contains(t, out, "moved the reminders store")
	// execute merges both streams: the notice first, then the path on stdout.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	assert.True(t, strings.HasPrefix(lines[len(lines)-1], currentRoot), out)
	assert.NoDirExists(t, legacyRoot)
}

// TestCompleteIDs_NeverMovesALegacyStore: completion runs with stderr
// discarded, so a move there would happen unannounced, and a failure unseen.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestCompleteIDs_NeverMovesALegacyStore(t *testing.T) {
	dir, _, legacyRoot, currentRoot := legacyFixture(t)

	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())

	completions, _ := (&App{RepoDir: dir}).completeIDs(cmd, nil, "")
	assert.Empty(t, completions, "the new root is still empty")
	assert.DirExists(t, legacyRoot)
	assert.NoDirExists(t, currentRoot)
}
