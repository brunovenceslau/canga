// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/brunovenceslau/canga/internal/repo"
	"github.com/brunovenceslau/canga/internal/store"
	"github.com/brunovenceslau/canga/internal/testrepo"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConfig_KeysTheStoreByTheRepository is the join between the two packages:
// the store directory a command resolves must be the origin-derived path, not
// anything about where the process happens to be running.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestConfig_KeysTheStoreByTheRepository(t *testing.T) {
	dir := testrepo.New(t, "git@github.com:acme/widget.git")

	cfg, err := (&App{RepoDir: dir}).Config(t.Context())
	require.NoError(t, err)

	assert.Equal(t, "github.com/acme/widget", cfg.Repo)
	assert.Equal(t, ScopeRepo, cfg.Scope)
	assert.Equal(t,
		filepath.Join(os.Getenv(ReminderDirVar), "github.com", "acme", "widget", ScopeRepo),
		cfg.Dir)
}

// TestConfig_EncodesCaseInTheDirectory: the directory has to mean the same
// thing on a case-folding APFS and on a sandbox filesystem that does not fold,
// which is the boundary a shared store spans. The readable spelling stays in
// Repo, because that is what each reminder's header records.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestConfig_EncodesCaseInTheDirectory(t *testing.T) {
	dir := testrepo.New(t, "git@github.com:Acme/Widget.git")

	cfg, err := (&App{RepoDir: dir}).Config(t.Context())
	require.NoError(t, err)

	assert.Equal(t, "github.com/Acme/Widget", cfg.Repo)
	assert.Equal(t,
		filepath.Join(os.Getenv(ReminderDirVar), "github.com", "!acme", "!widget", ScopeRepo),
		cfg.Dir)
}

// originLessLayout makes a hermetic base directory holding the given
// repositories, each with origin, or with no remote when origin is empty, and
// returns the base resolved through symbolic links.
func originLessLayout(t *testing.T, origin string, repos ...string) string {
	t.Helper()

	testrepo.Hermetic(t)

	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	for _, name := range repos {
		testrepo.Init(t, filepath.Join(base, filepath.FromSlash(name)), origin)
	}

	return base
}

// gitIn runs one git command in dir, under whatever hermetic environment the
// test set.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoErrorf(t, err, "git %v: %s", args, out)
}

// fixedBase is an App.BaseDir that answers base, whatever the environment.
func fixedBase(base string) func() (string, error) {
	return func() (string, error) { return base, nil }
}

// TestConfig_OriginLessKeyIsThePathUnderTheBase is the operator's rule: a
// repository with no origin is keyed by its path below the clone base, so
// ~/src/local/OS keys as local/OS. The directory escapes case exactly as an
// origin-derived one does.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestConfig_OriginLessKeyIsThePathUnderTheBase(t *testing.T) {
	base := originLessLayout(t, "", "local/OS")
	dir := filepath.Join(base, "local", "OS")

	// From the top and from a subdirectory: the key is the tree's, not the
	// directory the command was run in.
	sub := filepath.Join(dir, "docs")
	require.NoError(t, os.Mkdir(sub, 0o755))

	for _, from := range []string{dir, sub} {
		cfg, err := (&App{RepoDir: from, BaseDir: fixedBase(base)}).Config(t.Context())
		require.NoError(t, err)

		assert.Equal(t, "local/OS", cfg.Repo)
		assert.Equal(t,
			filepath.Join(os.Getenv(ReminderDirVar), "local", "!o!s", ScopeRepo),
			cfg.Dir)
	}
}

// TestConfig_OriginLessAtTheCloneLayoutSharesTheOriginKey: the collision
// between the two derivations is the rule, not an accident. A tree at exactly
// the place `canga git clone` puts github.com/acme/widget IS that repository
// by the layout's own definition, so with or without its origin it reads one
// list.
func TestConfig_OriginLessAtTheCloneLayoutSharesTheOriginKey(t *testing.T) {
	base := originLessLayout(t, "", "github.com/acme/widget")
	withOrigin := testrepo.New(t, "git@github.com:acme/widget.git")
	t.Setenv(ReminderDirVar, filepath.Join(base, "store"))

	pathKeyed, err := (&App{
		RepoDir: filepath.Join(base, "github.com", "acme", "widget"),
		BaseDir: fixedBase(base),
	}).Config(t.Context())
	require.NoError(t, err)

	originKeyed, err := (&App{RepoDir: withOrigin, BaseDir: fixedBase(base)}).Config(t.Context())
	require.NoError(t, err)

	assert.Equal(t, originKeyed, pathKeyed)
}

// TestConfig_OriginWinsOverLocation: an origin, when there is one, is the key,
// wherever the tree sits. The fallback never changes an existing key.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestConfig_OriginWinsOverLocation(t *testing.T) {
	base := originLessLayout(t, "git@github.com:acme/widget.git", "local/OS")

	cfg, err := (&App{RepoDir: filepath.Join(base, "local", "OS"), BaseDir: fixedBase(base)}).Config(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "github.com/acme/widget", cfg.Repo)
}

// TestConfig_OriginLessRefusals: every case where the location gives no key
// keeps today's answer, ErrNoOrigin and exit 2, and says why.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestConfig_OriginLessRefusals(t *testing.T) {
	t.Run("outside the base", func(t *testing.T) {
		base := originLessLayout(t, "")
		outside := filepath.Join(t.TempDir(), "repo")
		testrepo.Init(t, outside, "")

		_, err := (&App{RepoDir: outside, BaseDir: fixedBase(base)}).Config(t.Context())
		require.ErrorIs(t, err, repo.ErrNoOrigin)
		require.ErrorIs(t, err, repo.ErrNotUnderBase)
		assert.Contains(t, err.Error(), base)
		assert.Equal(t, ExitUsage, ExitCode(err))
	})

	t.Run("no base for this build", func(t *testing.T) {
		base := originLessLayout(t, "", "local/OS")

		_, err := (&App{RepoDir: filepath.Join(base, "local", "OS")}).Config(t.Context())
		require.ErrorIs(t, err, repo.ErrNoOrigin, "an App with no BaseDir keeps the origin-only rule")
		assert.Equal(t, ExitUsage, ExitCode(err))
	})

	t.Run("the base could not be resolved", func(t *testing.T) {
		base := originLessLayout(t, "", "local/OS")
		t.Setenv(repo.BaseDirVar, "")

		_, err := (&App{RepoDir: filepath.Join(base, "local", "OS"), BaseDir: repo.HandedBaseDir}).Config(t.Context())
		require.ErrorIs(t, err, repo.ErrNoOrigin)
		require.ErrorIs(t, err, repo.ErrNoBaseDir)
		assert.Contains(t, err.Error(), repo.BaseDirVar)
		assert.Equal(t, ExitUsage, ExitCode(err))
	})

	t.Run("an unusable origin is not an absent one", func(t *testing.T) {
		base := originLessLayout(t, "", "local/OS")
		dir := filepath.Join(base, "local", "OS")
		// An origin with a pushurl and no url. Measured on git 2.53: `git
		// remote get-url origin` then prints the remote's NAME, which no path
		// can be derived from. The repository names an upstream, just not
		// usably, so it must fail rather than fall back to its location.
		gitIn(t, dir, "config", "remote.origin.pushurl", "https://example.com/a/b")

		_, err := (&App{RepoDir: dir, BaseDir: fixedBase(base)}).Config(t.Context())
		require.ErrorIs(t, err, repo.ErrBadURL)
		assert.Equal(t, ExitUsage, ExitCode(err))
	})

	t.Run("core.worktree cannot choose the key", func(t *testing.T) {
		base := originLessLayout(t, "", "local/attacker", "local/victim")
		dir := filepath.Join(base, "local", "attacker")
		gitIn(t, dir, "config", "core.worktree", filepath.Join(base, "local", "victim"))

		_, err := (&App{RepoDir: dir, BaseDir: fixedBase(base)}).Config(t.Context())
		require.ErrorIs(t, err, repo.ErrNoOrigin)
		require.ErrorIs(t, err, repo.ErrWorktreeElsewhere)
	})

	// Ship-gate round 2, test-engineer Medium: ErrWorktreeUnverified is a
	// refusal, exit 2, end to end.
	t.Run("a forged worktree", func(t *testing.T) {
		base := originLessLayout(t, "", "local/OS")
		main := filepath.Join(base, "local", "OS")
		gitIn(t, main, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false",
			"commit", "-q", "--allow-empty", "-m", "init")

		worktree := filepath.Join(base, "local", "OS-wt")
		gitIn(t, main, "worktree", "add", "-q", "-b", "wt", worktree)
		require.NoError(t, os.WriteFile(filepath.Join(main, ".git", "worktrees", "OS-wt", "gitdir"),
			[]byte(filepath.Join(base, "local", "elsewhere", ".git")+"\n"), 0o600))

		_, err := (&App{RepoDir: worktree, BaseDir: fixedBase(base)}).Config(t.Context())
		require.ErrorIs(t, err, repo.ErrNoOrigin)
		require.ErrorIs(t, err, repo.ErrWorktreeUnverified)
		assert.Equal(t, ExitUsage, ExitCode(err))
	})

	// Ship-gate round 2, security-auditor Low: a .git linked to another
	// worktree's .git is refused, exit 2, and keys nothing.
	t.Run("a .git that is a link", func(t *testing.T) {
		base := originLessLayout(t, "", "local/B")
		main := filepath.Join(base, "local", "B")
		gitIn(t, main, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false",
			"commit", "-q", "--allow-empty", "-m", "init")

		worktree := filepath.Join(base, "local", "B-wt")
		gitIn(t, main, "worktree", "add", "-q", "-b", "wt", worktree)

		claimant := filepath.Join(base, "local", "A")
		require.NoError(t, os.MkdirAll(claimant, 0o755))
		require.NoError(t, os.Symlink(filepath.Join(worktree, ".git"), filepath.Join(claimant, ".git")))

		cfg, err := (&App{RepoDir: claimant, BaseDir: fixedBase(base)}).Config(t.Context())
		require.ErrorIs(t, err, repo.ErrDotGitLink, "keyed as %q", cfg.Repo)
		require.ErrorIs(t, err, repo.ErrNoOrigin)
		assert.Equal(t, ExitUsage, ExitCode(err))
	})

	// Ship-gate round 2, test-engineer Medium: git answers "no origin" while
	// `git remote` lists one. Measured on git 2.53: a url of only whitespace
	// makes `git remote get-url origin` print a blank line, which Origin
	// reads as no origin. The repository still NAMES an origin, so it must
	// not fall back to its location.
	t.Run("an origin git lists but gives no url for", func(t *testing.T) {
		base := originLessLayout(t, "", "local/OS")
		dir := filepath.Join(base, "local", "OS")
		gitIn(t, dir, "config", "remote.origin.url", " ")

		cfg, err := (&App{RepoDir: dir, BaseDir: fixedBase(base)}).Config(t.Context())
		require.ErrorIs(t, err, repo.ErrNoOrigin, "keyed as %q", cfg.Repo)
		require.NotErrorIs(t, err, repo.ErrNotUnderBase)
		assert.NotContains(t, err.Error(), "location", "the fallback was consulted")
		assert.Equal(t, ExitUsage, ExitCode(err))
	})

	t.Run("not a repository is unchanged", func(t *testing.T) {
		base := originLessLayout(t, "")
		plain := filepath.Join(base, "local", "plain")
		require.NoError(t, os.MkdirAll(plain, 0o755))

		_, err := (&App{RepoDir: plain, BaseDir: fixedBase(base)}).Config(t.Context())
		require.ErrorIs(t, err, repo.ErrNoOrigin)
		require.NotErrorIs(t, err, repo.ErrNotUnderBase)
	})
}

// TestConfig_LinkedWorktreeSharesTheMainTreesKey: every worktree of one
// origin-less repository reads one list, the main working tree's (operator
// decision, 2026-09-28).
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestConfig_LinkedWorktreeSharesTheMainTreesKey(t *testing.T) {
	base := originLessLayout(t, "", "local/OS")
	main := filepath.Join(base, "local", "OS")
	gitIn(t, main, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false",
		"commit", "-q", "--allow-empty", "-m", "init")

	for _, worktree := range []string{
		filepath.Join(main, ".claude", "worktrees", "x"),
		filepath.Join(base, "local", "OS-wt"),
	} {
		gitIn(t, main, "worktree", "add", "-q", "-b", filepath.Base(worktree), worktree)

		cfg, err := (&App{RepoDir: worktree, BaseDir: fixedBase(base)}).Config(t.Context())
		require.NoError(t, err, worktree)
		assert.Equal(t, "local/OS", cfg.Repo, worktree)
	}
}

// TestConfig_RuntimeFailuresAreNotUsageErrors: only the refusals that mean
// "this location gives no key" become ErrNoOrigin and exit 2. An I/O failure
// or a cancellation is a runtime failure, exit 1, and is not dressed up as
// the caller's mistake.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestConfig_RuntimeFailuresAreNotUsageErrors(t *testing.T) {
	base := originLessLayout(t, "", "local/OS")
	dir := filepath.Join(base, "local", "OS")

	//nolint:paralleltest // the parent's t.Setenv forbids parallel subtests
	for name, fail := range map[string]error{
		"permission denied": &fs.PathError{Op: "open", Path: base, Err: syscall.EACCES},
		"i/o error":         &fs.PathError{Op: "read", Path: base, Err: syscall.EIO},
		"cancelled":         context.Canceled,
		"git missing":       repo.ErrGitMissing,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := (&App{RepoDir: dir, BaseDir: func() (string, error) { return "", fail }}).Config(t.Context())
			require.ErrorIs(t, err, fail)
			require.NotErrorIs(t, err, repo.ErrNoOrigin)
			assert.Equal(t, ExitFailure, ExitCode(err))
		})
	}

	// git failing to read the repository is not "no origin" (ship-gate round
	// 2, security-auditor Info), and never reaches the fallback.
	t.Run("a directory git cannot enter", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root enters a mode-000 directory")
		}

		parent := filepath.Join(base, "local")
		require.NoError(t, os.Chmod(parent, 0))
		t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

		_, err := (&App{RepoDir: dir, BaseDir: fixedBase(base)}).Config(t.Context())
		require.ErrorIs(t, err, repo.ErrGitRefused)
		require.NotErrorIs(t, err, repo.ErrNoOrigin)
		assert.Equal(t, ExitFailure, ExitCode(err))
	})

	t.Run("a cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := (&App{RepoDir: dir, BaseDir: fixedBase(base)}).Config(ctx)
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, ExitFailure, ExitCode(err))
	})
}

// TestReservedSegmentsCoverTheStore: a location-derived key may not contain a
// name the store uses inside a key's directory. The list lives in
// internal/repo, which does not import the store, so this pins that it covers
// the scope and every data directory.
func TestReservedSegmentsCoverTheStore(t *testing.T) {
	t.Parallel()

	for _, name := range append([]string{ScopeRepo}, store.DataDirs()...) {
		assert.True(t, repo.IsReservedSegment(name), name)
	}
}

// TestConfig_KeysCannotNest: two origin-less repositories, one inside the
// other's would-be scope data, cannot share a directory tree. The inner one
// is refused rather than keyed into the outer store.
//
//nolint:paralleltest // t.Setenv, which the hermetic environment needs, forbids it
func TestConfig_KeysCannotNest(t *testing.T) {
	base := originLessLayout(t, "", "local/OS", "local/OS/repo/items")

	outer, err := (&App{RepoDir: filepath.Join(base, "local", "OS"), BaseDir: fixedBase(base)}).Config(t.Context())
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(os.Getenv(ReminderDirVar), "local", "!o!s", ScopeRepo), outer.Dir)

	_, err = (&App{RepoDir: filepath.Join(base, "local", "OS", "repo", "items"), BaseDir: fixedBase(base)}).
		Config(t.Context())
	require.ErrorIs(t, err, repo.ErrBadPath)
	assert.Equal(t, ExitUsage, ExitCode(err))
}

func TestRemindersBaseDir(t *testing.T) {
	t.Run("the shared variable wins", func(t *testing.T) {
		// Set explicitly because $HOME is not the same on both sides of the
		// sandbox boundary: a sandbox is handed the path, never left to derive
		// a different one from its own environment.
		t.Setenv(ReminderDirVar, "/explicit")
		t.Setenv("XDG_DATA_HOME", "/xdg")

		got, err := RemindersBaseDir()
		require.NoError(t, err)
		assert.Equal(t, "/explicit", got)
	})

	t.Run("falls back to the project directory under XDG", func(t *testing.T) {
		t.Setenv(ReminderDirVar, "")
		t.Setenv("XDG_DATA_HOME", "/xdg")

		got, err := RemindersBaseDir()
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/xdg", Project, "reminders"), got)
	})

	t.Run("falls back to the XDG default", func(t *testing.T) {
		t.Setenv(ReminderDirVar, "")
		t.Setenv("XDG_DATA_HOME", "")
		t.Setenv("HOME", "/home/someone")

		got, err := RemindersBaseDir()
		require.NoError(t, err)
		assert.Equal(t,
			filepath.Join("/home/someone", ".local", "share", Project, "reminders"), got)
	})

	t.Run("the pre-canga variables are not read", func(t *testing.T) {
		// A sandbox still set up with them must not half-work: the README
		// names CANGA_REMINDERS_DIR as the only one.
		t.Setenv(ReminderDirVar, "")
		t.Setenv("DEVCTL_REMINDERS_DIR", "/old-devctl")
		t.Setenv("AGTCTL_REMINDERS_DIR", "/old-agtctl")
		t.Setenv("XDG_DATA_HOME", "/xdg")

		got, err := RemindersBaseDir()
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/xdg", Project, "reminders"), got)
	})
}

// TestLegacyState pins what happens to a store left behind by the rename. It
// is moved only when the new root does not exist; when both exist and the old
// one still holds files, it is reported and never moved; everything else is
// left alone, so no command nags after the move.
//
//nolint:paralleltest // setup calls t.Setenv, which the hermetic environment needs
func TestLegacyState(t *testing.T) {
	setup := func(t *testing.T, legacyFile, current bool) string {
		t.Helper()

		data := t.TempDir()
		t.Setenv("XDG_DATA_HOME", data)
		t.Setenv(ReminderDirVar, "")

		legacyDir := filepath.Join(data, "devctl", "reminders")
		require.NoError(t, os.MkdirAll(filepath.Join(legacyDir, "github.com"), 0o700))

		if legacyFile {
			require.NoError(t, os.WriteFile(filepath.Join(legacyDir, "github.com", "item.md"), nil, 0o600))
		}

		if current {
			require.NoError(t, os.MkdirAll(filepath.Join(data, Project, "reminders"), 0o700))
		}

		return data
	}

	tests := []struct {
		name       string
		legacyFile bool
		current    bool
		want       legacy
	}{
		{name: "old store and no new root is moved", legacyFile: true, want: legacyMovable},
		{name: "an empty old store with no new root is moved too", want: legacyMovable},
		{name: "both, with files in the old one, is stranded", legacyFile: true, current: true, want: legacyStranded},
		{name: "both, the old one emptied by hand, is left alone", current: true, want: legacyNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := setup(t, tt.legacyFile, tt.current)

			legacyDir, currentDir, state := legacyState()
			assert.Equal(t, tt.want, state)
			assert.Equal(t, filepath.Join(data, "devctl", "reminders"), legacyDir)
			assert.Equal(t, filepath.Join(data, Project, "reminders"), currentDir)
		})
	}

	t.Run("a fresh machine has nothing to do", func(t *testing.T) {
		data := t.TempDir()
		t.Setenv("XDG_DATA_HOME", data)
		t.Setenv(ReminderDirVar, "")

		_, _, state := legacyState()
		assert.Equal(t, legacyNone, state)
	})

	t.Run("an override means neither default is in use", func(t *testing.T) {
		setup(t, true, false)
		t.Setenv(ReminderDirVar, t.TempDir())

		_, _, state := legacyState()
		assert.Equal(t, legacyNone, state)
	})
}
