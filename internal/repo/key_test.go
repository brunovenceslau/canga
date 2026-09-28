// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mkdirs creates each directory under base and returns base's resolved path,
// so a comparison does not depend on whether the temporary directory itself
// sits behind a symbolic link (it does on macOS: /var is /private/var).
func mkdirs(t *testing.T, base string, dirs ...string) string {
	t.Helper()

	for _, dir := range dirs {
		require.NoError(t, os.MkdirAll(filepath.Join(base, filepath.FromSlash(dir)), 0o755))
	}

	resolved, err := filepath.EvalSymlinks(base)
	require.NoError(t, err)

	return resolved
}

// TestPathUnder pins the key an origin-less repository gets: its path below
// the base directory, and nothing else.
func TestPathUnder(t *testing.T) {
	t.Parallel()

	t.Run("a repository below the base is keyed by its relative path", func(t *testing.T) {
		t.Parallel()

		base := mkdirs(t, t.TempDir(), "local/OS", "github.com/acme/widget")

		got, err := PathUnder(base, filepath.Join(base, "local", "OS"))
		require.NoError(t, err)
		assert.Equal(t, "local/OS", got)

		got, err = PathUnder(base, filepath.Join(base, "github.com", "acme", "widget"))
		require.NoError(t, err)
		assert.Equal(t, "github.com/acme/widget", got,
			"an origin-less tree where `canga git clone` would put a repository gets that repository's key")
	})

	t.Run("one segment is enough", func(t *testing.T) {
		t.Parallel()

		base := mkdirs(t, t.TempDir(), "scratch")

		got, err := PathUnder(base, filepath.Join(base, "scratch"))
		require.NoError(t, err)
		assert.Equal(t, "scratch", got)
	})
}

// TestPathUnder_Boundaries: where the base ends. Symbolic links are resolved
// on both sides, and every refusal is a key that would either escape the
// store root or address a store other than the one the directory names.
func TestPathUnder_Boundaries(t *testing.T) {
	t.Parallel()

	t.Run("outside the base is refused", func(t *testing.T) {
		t.Parallel()

		root := mkdirs(t, t.TempDir(), "src", "elsewhere/repo", "src2/repo")
		base := filepath.Join(root, "src")

		for _, top := range []string{
			filepath.Join(root, "elsewhere", "repo"),
			// A sibling sharing the base's name as a PREFIX is still outside:
			// the comparison is by path segment, not by string.
			filepath.Join(root, "src2", "repo"),
			root,
		} {
			_, err := PathUnder(base, top)
			require.ErrorIs(t, err, ErrNotUnderBase, "top %s", top)
			assert.Contains(t, err.Error(), base)
		}
	})

	t.Run("the base itself is refused", func(t *testing.T) {
		t.Parallel()

		base := mkdirs(t, t.TempDir())

		_, err := PathUnder(base, base)
		require.ErrorIs(t, err, ErrNotUnderBase, "an empty key would put the store at the root")
	})

	t.Run("a symlinked base resolves before comparing", func(t *testing.T) {
		t.Parallel()

		target := mkdirs(t, t.TempDir(), "local/OS")
		link := filepath.Join(t.TempDir(), "src")
		require.NoError(t, os.Symlink(target, link))

		// The base through its link, the tree through its real path: the same
		// directory, so the same key.
		got, err := PathUnder(link, filepath.Join(target, "local", "OS"))
		require.NoError(t, err)
		assert.Equal(t, "local/OS", got)

		// And the other way round.
		got, err = PathUnder(target, filepath.Join(link, "local", "OS"))
		require.NoError(t, err)
		assert.Equal(t, "local/OS", got)
	})

	t.Run("a symlinked tree is keyed by where it really is", func(t *testing.T) {
		t.Parallel()

		base := mkdirs(t, t.TempDir(), "local/OS")
		alias := filepath.Join(base, "alias")
		require.NoError(t, os.Symlink(filepath.Join(base, "local", "OS"), alias))

		got, err := PathUnder(base, alias)
		require.NoError(t, err)
		assert.Equal(t, "local/OS", got, "a link is another name for a tree, never another key")
	})

	t.Run("a link inside the base that points out of it is refused", func(t *testing.T) {
		t.Parallel()

		base := mkdirs(t, t.TempDir())
		outside := mkdirs(t, t.TempDir(), "repo")
		escape := filepath.Join(base, "escape")
		require.NoError(t, os.Symlink(filepath.Join(outside, "repo"), escape))

		_, err := PathUnder(base, escape)
		require.ErrorIs(t, err, ErrNotUnderBase)
	})

	t.Run("dot-dot in the given path cannot climb out", func(t *testing.T) {
		t.Parallel()

		root := mkdirs(t, t.TempDir(), "src/local", "other")
		base := filepath.Join(root, "src")

		_, err := PathUnder(base, filepath.Join(base, "local", "..", "..", "other"))
		require.ErrorIs(t, err, ErrNotUnderBase)
	})

	t.Run("a segment the store could not hold is refused", func(t *testing.T) {
		t.Parallel()

		base := mkdirs(t, t.TempDir(), "local/has space", "local/semi;colon")

		for _, top := range []string{"local/has space", "local/semi;colon"} {
			_, err := PathUnder(base, filepath.Join(base, filepath.FromSlash(top)))
			require.ErrorIs(t, err, ErrBadPath, "top %s", top)
		}
	})

	t.Run("a base that does not exist is refused", func(t *testing.T) {
		t.Parallel()

		repo := mkdirs(t, t.TempDir(), "repo")

		_, err := PathUnder(filepath.Join(t.TempDir(), "missing"), filepath.Join(repo, "repo"))
		require.ErrorIs(t, err, ErrNoSuchBase, "its own sentinel: the fix is to the base, not the tree")
		require.NotErrorIs(t, err, ErrNotUnderBase)
	})
}

// TestPathUnder_Errors: which error a failure is, and which names are not
// keys at all.
func TestPathUnder_Errors(t *testing.T) {
	t.Parallel()

	t.Run("a tree that cannot be resolved is not blamed on the base", func(t *testing.T) {
		t.Parallel()

		base := mkdirs(t, t.TempDir())

		_, err := PathUnder(base, filepath.Join(base, "gone"))
		require.ErrorIs(t, err, fs.ErrNotExist)
		require.NotErrorIs(t, err, ErrNoSuchBase)
		require.NotErrorIs(t, err, ErrNotUnderBase)
		assert.Contains(t, err.Error(), "working tree")
	})

	t.Run("a trailing slash on the base changes nothing", func(t *testing.T) {
		t.Parallel()

		base := mkdirs(t, t.TempDir(), "local/OS")

		got, err := PathUnder(base+string(filepath.Separator), filepath.Join(base, "local", "OS"))
		require.NoError(t, err)
		assert.Equal(t, "local/OS", got)
	})

	// The store keeps its own directories inside a key's directory: the scope
	// (repo) and its data (items, order, tmp). A key containing one of those
	// names would put one repository's store inside another's data.
	t.Run("a segment the store uses inside a key is refused", func(t *testing.T) {
		t.Parallel()

		base := mkdirs(t, t.TempDir(), "local/OS/repo/items", "local/repo", "local/x/order", "local/x/tmp")

		for _, top := range []string{"local/OS/repo/items", "local/OS/repo", "local/repo", "local/x/order", "local/x/tmp"} {
			_, err := PathUnder(base, filepath.Join(base, filepath.FromSlash(top)))
			require.ErrorIs(t, err, ErrBadPath, "top %s", top)
			assert.Contains(t, err.Error(), "reminder store uses", "top %s", top)
		}

		// Only whole segments: a name merely containing one is fine.
		base = mkdirs(t, t.TempDir(), "local/repository")
		got, err := PathUnder(base, filepath.Join(base, "local", "repository"))
		require.NoError(t, err)
		assert.Equal(t, "local/repository", got)
	})
}

// TestPathUnder_NamesTheVariableOnlyWhenItIsSet: blaming CANGA_SRC_DIR for a
// base it did not set sends the reader to fix the wrong thing.
func TestPathUnder_NamesTheVariableOnlyWhenItIsSet(t *testing.T) {
	root := mkdirs(t, t.TempDir(), "src", "elsewhere/repo")
	base, outside := filepath.Join(root, "src"), filepath.Join(root, "elsewhere", "repo")

	t.Setenv(BaseDirVar, "")

	_, err := PathUnder(base, outside)
	require.ErrorIs(t, err, ErrNotUnderBase)
	assert.NotContains(t, err.Error(), BaseDirVar)

	_, err = PathUnder(filepath.Join(root, "missing"), outside)
	require.ErrorIs(t, err, ErrNoSuchBase)
	assert.NotContains(t, err.Error(), BaseDirVar)

	t.Setenv(BaseDirVar, base)

	_, err = PathUnder(base, outside)
	require.ErrorIs(t, err, ErrNotUnderBase)
	assert.Contains(t, err.Error(), BaseDirVar)

	_, err = PathUnder(filepath.Join(root, "missing"), outside)
	require.ErrorIs(t, err, ErrNoSuchBase)
	assert.Contains(t, err.Error(), BaseDirVar)
}

// TestToplevel: the tree an origin-less key is derived from is found on the
// filesystem, by the .git entry at its top, and git has to agree. A
// repository whose configuration relocates its working tree is refused rather
// than keyed by a path its configuration chose.
//
//nolint:paralleltest // hermeticGit sets environment variables
func TestToplevel(t *testing.T) {
	t.Run("from the top and from a subdirectory", func(t *testing.T) {
		hermeticGit(t)

		top := mkdirs(t, t.TempDir(), "repo/sub/deeper")
		repo := filepath.Join(top, "repo")
		runGit(t, "init", "-q", "-b", "main", repo)

		for _, dir := range []string{repo, filepath.Join(repo, "sub", "deeper")} {
			got, err := Toplevel(t.Context(), dir)
			require.NoError(t, err)
			assert.Equal(t, repo, got)
		}
	})

	t.Run("through a symlink, the real path", func(t *testing.T) {
		hermeticGit(t)

		top := mkdirs(t, t.TempDir(), "repo")
		repo := filepath.Join(top, "repo")
		runGit(t, "init", "-q", "-b", "main", repo)

		link := filepath.Join(t.TempDir(), "link")
		require.NoError(t, os.Symlink(repo, link))

		got, err := Toplevel(t.Context(), link)
		require.NoError(t, err)
		assert.Equal(t, repo, got)
	})

	t.Run("core.worktree pointing elsewhere is refused", func(t *testing.T) {
		hermeticGit(t)

		top := mkdirs(t, t.TempDir(), "repo", "victim")
		repo := filepath.Join(top, "repo")
		runGit(t, "init", "-q", "-b", "main", repo)
		runGit(t, "-C", repo, "config", "core.worktree", filepath.Join(top, "victim"))

		_, err := Toplevel(t.Context(), repo)
		require.ErrorIs(t, err, ErrWorktreeElsewhere)
	})

	t.Run("GIT_WORK_TREE pointing elsewhere is refused", func(t *testing.T) {
		hermeticGit(t)

		top := mkdirs(t, t.TempDir(), "repo", "victim")
		repo := filepath.Join(top, "repo")
		runGit(t, "init", "-q", "-b", "main", repo)
		t.Setenv("GIT_WORK_TREE", filepath.Join(top, "victim"))

		_, err := Toplevel(t.Context(), repo)
		require.ErrorIs(t, err, ErrWorktreeElsewhere)
	})

	t.Run("not a repository", func(t *testing.T) {
		hermeticGit(t)

		_, err := Toplevel(t.Context(), t.TempDir())
		require.ErrorIs(t, err, ErrNotARepository)
	})
}

// commitIn makes one empty commit, which `git worktree add` needs.
func commitIn(t *testing.T, repo string) {
	t.Helper()

	runGit(t, "-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.com",
		"-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "init")
}

// TestKeyTree: a linked worktree is keyed by its MAIN working tree (operator
// decision, 2026-09-28), wherever the worktree sits, and only when the link
// checks out in both directions. A forged link is refused, never keyed by the
// worktree's own path instead.
//
//nolint:paralleltest // hermeticGit sets environment variables
func TestKeyTree(t *testing.T) {
	layout := func(t *testing.T) (base, main string) {
		t.Helper()
		hermeticGit(t)

		base = mkdirs(t, t.TempDir(), "local")
		main = filepath.Join(base, "local", "OS")
		runGit(t, "init", "-q", "-b", "main", main)
		commitIn(t, main)

		return base, main
	}

	t.Run("the main tree is its own", func(t *testing.T) {
		_, main := layout(t)

		got, err := KeyTree(t.Context(), main)
		require.NoError(t, err)
		assert.Equal(t, main, got)
	})

	for name, where := range map[string]func(base, main string) string{
		"a worktree inside the main tree": func(_, main string) string {
			return filepath.Join(main, ".claude", "worktrees", "x")
		},
		"a sibling worktree": func(base, _ string) string {
			return filepath.Join(base, "local", "OS-wt")
		},
	} {
		t.Run(name+" is keyed by the main tree", func(t *testing.T) {
			base, main := layout(t)
			worktree := where(base, main)
			runGit(t, "-C", main, "worktree", "add", "-q", "-b", "wt", worktree)

			for _, dir := range []string{worktree, filepath.Join(worktree, ".")} {
				got, err := KeyTree(t.Context(), dir)
				require.NoError(t, err)
				assert.Equal(t, main, got)
			}

			key, err := PathUnder(base, main)
			require.NoError(t, err)
			assert.Equal(t, "local/OS", key)
		})
	}

	t.Run("a forged gitdir is refused", func(t *testing.T) {
		base, main := layout(t)
		worktree := filepath.Join(base, "local", "OS-wt")
		runGit(t, "-C", main, "worktree", "add", "-q", "-b", "wt", worktree)

		// The main repository's record now names another tree's .git, so it
		// no longer vouches for this one.
		other := mkdirs(t, t.TempDir(), "other")
		require.NoError(t, os.WriteFile(filepath.Join(other, "other", ".git"), nil, 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(main, ".git", "worktrees", "OS-wt", "gitdir"),
			[]byte(filepath.Join(other, "other", ".git")+"\n"), 0o600))

		_, err := KeyTree(t.Context(), worktree)
		require.ErrorIs(t, err, ErrWorktreeUnverified)
	})

	t.Run("a forged commondir is refused", func(t *testing.T) {
		base, victim := layout(t)

		// An attacker-made tree whose .git points at a directory it made,
		// which claims the victim's .git as its common directory and names
		// the attacker's .git back. Only the victim's own worktrees/ could
		// vouch for it, and it does not.
		attacker := filepath.Join(base, "local", "attacker")
		fake := filepath.Join(base, "local", "fake", "worktrees", "n")
		require.NoError(t, os.MkdirAll(fake, 0o755))
		require.NoError(t, os.MkdirAll(attacker, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(attacker, ".git"), []byte("gitdir: "+fake+"\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(fake, "gitdir"),
			[]byte(filepath.Join(attacker, ".git")+"\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(fake, "commondir"),
			[]byte(filepath.Join(victim, ".git")+"\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(fake, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600))

		got, err := KeyTree(t.Context(), attacker)
		require.ErrorIs(t, err, ErrWorktreeUnverified, "keyed as %q", got)
	})

	t.Run("a submodule is keyed by where it is", func(t *testing.T) {
		base, main := layout(t)

		source := filepath.Join(t.TempDir(), "source")
		runGit(t, "init", "-q", "-b", "main", source)
		commitIn(t, source)
		runGit(t, "-C", main, "-c", "protocol.file.allow=always", "submodule", "add", "-q", source, "sub")

		sub := filepath.Join(main, "sub")
		info, err := os.Lstat(filepath.Join(sub, ".git"))
		require.NoError(t, err)
		require.True(t, info.Mode().IsRegular(), "a submodule's .git is a file")

		got, err := KeyTree(t.Context(), sub)
		require.NoError(t, err)
		assert.Equal(t, sub, got)

		key, err := PathUnder(base, got)
		require.NoError(t, err)
		assert.Equal(t, "local/OS/sub", key)
	})
}

//nolint:paralleltest // hermeticGit sets environment variables
func TestHasRemote(t *testing.T) {
	hermeticGit(t)

	repo := filepath.Join(t.TempDir(), "repo")
	runGit(t, "init", "-q", "-b", "main", repo)

	has, err := HasRemote(t.Context(), repo, "origin")
	require.NoError(t, err)
	assert.False(t, has)

	runGit(t, "-C", repo, "remote", "add", "originals", "https://example.com/a/b")

	has, err = HasRemote(t.Context(), repo, "origin")
	require.NoError(t, err)
	assert.False(t, has, "a remote whose name merely starts with origin is not origin")

	runGit(t, "-C", repo, "remote", "add", "origin", "https://example.com/a/b")

	has, err = HasRemote(t.Context(), repo, "origin")
	require.NoError(t, err)
	assert.True(t, has)

	_, err = HasRemote(t.Context(), t.TempDir(), "origin")
	require.ErrorIs(t, err, ErrNotARepository)
}

// TestHandedBaseDir: a process whose $HOME is not the host's is HANDED the
// base directory, never left to derive one of its own, and a relative one
// means nothing there.
func TestHandedBaseDir(t *testing.T) {
	t.Run("unset is refused, not defaulted", func(t *testing.T) {
		t.Setenv(BaseDirVar, "")
		t.Setenv("HOME", t.TempDir())

		_, err := HandedBaseDir()
		require.ErrorIs(t, err, ErrNoBaseDir)
		assert.Contains(t, err.Error(), BaseDirVar)
	})

	t.Run("relative is refused", func(t *testing.T) {
		t.Setenv(BaseDirVar, "relative/src")

		_, err := HandedBaseDir()
		require.ErrorIs(t, err, ErrNoBaseDir)
	})

	t.Run("an absolute path is used as given", func(t *testing.T) {
		t.Setenv(BaseDirVar, "/Users/someone/src")

		got, err := HandedBaseDir()
		require.NoError(t, err)
		assert.Equal(t, "/Users/someone/src", got)
	})
}
