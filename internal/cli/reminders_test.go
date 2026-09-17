// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunovenceslau/devctl/internal/testrepo"

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

// TestNewRemindersCmd_RegistersOnlyTheVerbsGiven is what agtctl's narrower
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

	base := os.Getenv("DEVCTL_REMINDERS_DIR")
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
