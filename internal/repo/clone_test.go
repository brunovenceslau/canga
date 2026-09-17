// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every case here is one of dotfiles-host's tests/dev_test.sh, so the two
// implementations cannot drift while both exist.

// quiet is the option set every clone test uses: git's output is of no interest
// to a test, and its progress meter would be written into the test log.
func quiet(dir string) CloneOptions {
	return CloneOptions{Dir: dir, NoProgress: true, Out: io.Discard, Err: io.Discard}
}

// sourceRepo is a repository with one commit on main, for a clone to land on.
func sourceRepo(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "source")
	runGit(t, "init", "-q", "-b", "main", dir)
	commitFile(t, dir, "file", "one\n")

	return dir
}

// commitFile writes a file and commits it with an inline identity, so nothing
// depends on the machine's git config — which the hermetic one has emptied.
func commitFile(t *testing.T, dir, name, content string) {
	t.Helper()

	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
	runGit(t, "-C", dir, "add", name)
	runGit(t, "-C", dir,
		"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false",
		"commit", "-qm", "commit "+name)
}

//nolint:paralleltest // t.Setenv, which the hermetic git config needs, forbids it
func TestClone(t *testing.T) {
	t.Run("into an explicit directory", func(t *testing.T) {
		hermeticGit(t)

		source := sourceRepo(t)
		target := filepath.Join(t.TempDir(), "nested", "clone")

		result, err := Clone(t.Context(), source, quiet(target))
		require.NoError(t, err)
		assert.Equal(t, target, result.Dir)
		assert.FileExists(t, filepath.Join(target, "file"), "the clone carries the source's content")
		assert.DirExists(t, filepath.Join(target, ".git"))
	})

	// The resolved path is printed for a caller to use — `cd $(devctl clone …)`
	// — so a relative one, meaningless in any other directory, is resolved here.
	t.Run("a relative directory is made absolute", func(t *testing.T) {
		hermeticGit(t)

		source := sourceRepo(t)
		work := t.TempDir()
		t.Chdir(work)

		result, err := Clone(t.Context(), source, quiet("clone"))
		require.NoError(t, err)
		assert.True(t, filepath.IsAbs(result.Dir))
		assert.Equal(t, resolve(t, filepath.Join(work, "clone")), resolve(t, result.Dir))
	})

	t.Run("refuses a non-empty target", func(t *testing.T) {
		hermeticGit(t)

		source := sourceRepo(t)
		target := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(target, "occupied"), []byte("x"), 0o600))

		_, err := Clone(t.Context(), source, quiet(target))
		require.ErrorIs(t, err, ErrTargetNotEmpty)
		assert.NoDirExists(t, filepath.Join(target, ".git"), "nothing was written into it")
		assert.FileExists(t, filepath.Join(target, "occupied"), "and nothing was taken out")
	})

	// `mkdir ~/src/thing && devctl clone … ~/src/thing` is a normal thing to do,
	// and git accepts it, so this refusal is about CONTENT, not existence.
	t.Run("accepts an existing empty directory", func(t *testing.T) {
		hermeticGit(t)

		source := sourceRepo(t)
		target := t.TempDir()

		result, err := Clone(t.Context(), source, quiet(target))
		require.NoError(t, err)
		assert.Equal(t, target, result.Dir)
	})

	t.Run("refuses a target that is a file", func(t *testing.T) {
		hermeticGit(t)

		source := sourceRepo(t)
		target := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(target, []byte("x"), 0o600))

		_, err := Clone(t.Context(), source, quiet(target))
		require.ErrorIs(t, err, ErrTargetNotEmpty)
	})

	// The whole point of the command: with no directory named, the URL decides
	// where the repository lands, and the parents are created along the way.
	t.Run("derives the path and creates its parents", func(t *testing.T) {
		hermeticGit(t)

		source := sourceRepo(t)
		base := t.TempDir()
		t.Setenv(envBaseDir, base)

		// insteadOf rewrites a real-looking URL onto the local source, so the
		// derivation is exercised end to end without reaching the network. The
		// hermetic global config is the only place it is set.
		const url = "https://github.com/owner/repo.git"

		runGit(t, "config", "--global", "url."+source+".insteadOf", url)

		result, err := Clone(t.Context(), url, quiet(""))
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(base, "github.com", "owner", "repo"), result.Dir)
		assert.FileExists(t, filepath.Join(result.Dir, "file"))
	})

	t.Run("an unparseable url is refused before anything is created", func(t *testing.T) {
		hermeticGit(t)

		base := t.TempDir()
		t.Setenv(envBaseDir, base)

		_, err := Clone(t.Context(), "not-a-url", quiet(""))
		require.ErrorIs(t, err, ErrBadURL)

		entries, err := os.ReadDir(base)
		require.NoError(t, err)
		assert.Empty(t, entries, "a refused url leaves no directory behind")
	})
}

// TestClone_RefusesTheExtTransport is the behavioural proof that the hardening
// in transportFlags reaches git: the ext remote helper RUNS the rest of the URL
// as a command, and a machine whose git config allows it must not change that.
//
// It needs an explicit target, because a URL this shape never survives Path.
//
//nolint:paralleltest // t.Setenv, which the hermetic git config needs, forbids it
func TestClone_RefusesTheExtTransport(t *testing.T) {
	hermeticGit(t)

	// The configuration the flags exist to override, set exactly as a host might.
	runGit(t, "config", "--global", "protocol.ext.allow", "always")

	work := t.TempDir()
	marker := filepath.Join(work, "executed")
	helper := filepath.Join(work, "helper.sh")
	require.NoError(t, os.WriteFile(helper, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o700))

	// git's own stderr is captured here rather than discarded, so the test
	// asserts WHY the clone failed. Any mistake in the target or the source
	// would fail it too, and would pass a test that only checked for an error.
	var said bytes.Buffer

	options := quiet(filepath.Join(work, "clone"))
	options.Err = &said

	_, err := Clone(t.Context(), "ext::"+helper, options)
	require.Error(t, err)
	assert.Contains(t, said.String(), "transport 'ext' not allowed")
	assert.NoFileExists(t, marker, "the ext helper must not have run")
}

func TestTargetDir(t *testing.T) {
	t.Run("honours DEVCTL_BASE_DIR", func(t *testing.T) {
		t.Setenv(envBaseDir, "/tmp/elsewhere")

		got, err := TargetDir("git@github.com:owner/repo.git")
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/tmp/elsewhere", "github.com", "owner", "repo"), got)
	})

	t.Run("defaults to ~/src", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv(envBaseDir, "")
		t.Setenv("HOME", home)

		got, err := TargetDir("https://github.com/owner/repo")
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(home, "src", "github.com", "owner", "repo"), got)
	})

	// The path is printed for `cd $(devctl clone …)` to consume, so a relative
	// DEVCTL_BASE_DIR must not produce a relative answer.
	t.Run("absolutizes a relative base", func(t *testing.T) {
		t.Chdir(t.TempDir())
		t.Setenv(envBaseDir, "relative-base")

		got, err := TargetDir("https://github.com/owner/repo")
		require.NoError(t, err)
		require.True(t, filepath.IsAbs(got), "got %q", got)

		// Against the working directory as the process sees it: t.Chdir's
		// argument can be a symlink, and filepath.Abs joins onto the resolved
		// one that os.Getwd reports.
		cwd, err := os.Getwd()
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(cwd, "relative-base", "github.com", "owner", "repo"), got)
	})

	// The clone layout is the one a human types, so it keeps the repository's
	// own spelling. Only the reminder store, which is shared between a
	// case-insensitive and a case-sensitive filesystem, is escaped.
	t.Run("keeps the readable spelling", func(t *testing.T) {
		t.Setenv(envBaseDir, "/base")

		got, err := TargetDir("git@github.com:Acme/Widget.git")
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/base", "github.com", "Acme", "Widget"), got)
	})
}

// TestTransportFlags pins the hardening itself. The clone tests prove it reaches
// git; this proves nobody quietly dropped one of the four while refactoring, in
// a form a reviewer can compare against zsh/dev.zsh line by line.
func TestTransportFlags(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{
		"-c", "protocol.ext.allow=never",
		"-c", "protocol.fd.allow=never",
		"-c", "transfer.fsckObjects=true",
		"-c", "fetch.fsckObjects=true",
	}, transportFlags())
}

func TestGitArgs(t *testing.T) {
	t.Parallel()

	// One stand-in for the real hardening, which TestTransportFlags pins.
	const flag = "a=b"

	// -C first, then the -c options, then the subcommand: a -c after the
	// subcommand is an argument OF the subcommand, and the hardening is lost.
	assert.Equal(t,
		[]string{"-C", "/repo", "-c", flag, "fetch", "--all"},
		gitArgs("/repo", []string{"-c", flag}, []string{"fetch", "--all"}))

	// No dir, which is what clone needs: there is no repository to run in yet.
	assert.Equal(t,
		[]string{"-c", flag, "clone", "--", "url", "dir"},
		gitArgs("", []string{"-c", flag}, []string{"clone", "--", "url", "dir"}))
}
