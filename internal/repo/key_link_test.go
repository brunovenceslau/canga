// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// worktreeLayout makes <base>/local/OS with a linked worktree at
// <base>/local/OS-wt, and returns base, the main tree, the worktree, and
// git's per-worktree directory for it.
func worktreeLayout(t *testing.T) (base, main, worktree, gitDir string) {
	t.Helper()
	hermeticGit(t)

	base = mkdirs(t, t.TempDir(), "local")
	main = filepath.Join(base, "local", "OS")
	worktree = filepath.Join(base, "local", "OS-wt")

	runGit(t, "init", "-q", "-b", "main", main)
	commitIn(t, main)
	runGit(t, "-C", main, "worktree", "add", "-q", "-b", "wt", worktree)

	return base, main, worktree, filepath.Join(main, ".git", "worktrees", "OS-wt")
}

// keyTreeWithin fails the test instead of hanging it: a FIFO read without
// O_NONBLOCK never returns.
func keyTreeWithin(t *testing.T, dir string) (string, error) {
	t.Helper()

	type result struct {
		key string
		err error
	}

	done := make(chan result, 1)

	go func() {
		key, err := KeyTree(t.Context(), dir)
		done <- result{key, err}
	}()

	select {
	case r := <-done:
		return r.key, r.err
	case <-time.After(20 * time.Second):
		t.Fatalf("KeyTree(%s) did not return", dir)

		return "", nil
	}
}

// TestKeyTree_GitdirRecord: the worktree's gitdir record is repository
// content a sandbox can write, read by the host build. It is read only as a
// small regular file, and what it says is never echoed raw (ship-gate round
// 2, security-auditor Medium).
//
//nolint:paralleltest // hermeticGit sets environment variables
func TestKeyTree_GitdirRecord(t *testing.T) {
	t.Run("a link to another file is refused unread", func(t *testing.T) {
		_, _, worktree, gitDir := worktreeLayout(t)

		secret := filepath.Join(t.TempDir(), "secret")
		require.NoError(t, os.WriteFile(secret, []byte("TOPSECRET\x1b]52;c;AAAA\x07"), 0o600))

		record := filepath.Join(gitDir, "gitdir")
		require.NoError(t, os.Remove(record))
		require.NoError(t, os.Symlink(secret, record))

		_, err := keyTreeWithin(t, worktree)
		require.ErrorIs(t, err, ErrWorktreeUnverified)
		assert.NotContains(t, err.Error(), "TOPSECRET")
	})

	t.Run("a link to the right record is refused too", func(t *testing.T) {
		_, _, worktree, gitDir := worktreeLayout(t)

		// The content would check out: only the link is wrong.
		copied := filepath.Join(t.TempDir(), "gitdir")
		require.NoError(t, os.WriteFile(copied, []byte(filepath.Join(worktree, ".git")+"\n"), 0o600))

		record := filepath.Join(gitDir, "gitdir")
		require.NoError(t, os.Remove(record))
		require.NoError(t, os.Symlink(copied, record))

		_, err := keyTreeWithin(t, worktree)
		require.ErrorIs(t, err, ErrWorktreeUnverified)
	})

	t.Run("a FIFO is refused without hanging", func(t *testing.T) {
		_, _, worktree, gitDir := worktreeLayout(t)

		record := filepath.Join(gitDir, "gitdir")
		require.NoError(t, os.Remove(record))
		require.NoError(t, syscall.Mkfifo(record, 0o600))

		_, err := keyTreeWithin(t, worktree)
		require.ErrorIs(t, err, ErrWorktreeUnverified)
	})

	t.Run("an oversized record is refused", func(t *testing.T) {
		_, _, worktree, gitDir := worktreeLayout(t)

		// A valid pointer followed by padding past the limit: refused on
		// size, not on what it says.
		body := filepath.Join(worktree, ".git") + "\n" + strings.Repeat(" ", maxGitdirRecord)
		require.NoError(t, os.WriteFile(filepath.Join(gitDir, "gitdir"), []byte(body), 0o600))

		_, err := keyTreeWithin(t, worktree)
		require.ErrorIs(t, err, ErrWorktreeUnverified)
	})

	t.Run("what the record says is quoted, never echoed raw", func(t *testing.T) {
		_, _, worktree, gitDir := worktreeLayout(t)

		pointer := "/nowhere/\x1b]52;c;QUFBQQ==\x07" + strings.Repeat("x", 400)
		require.NoError(t, os.WriteFile(filepath.Join(gitDir, "gitdir"), []byte(pointer+"\n"), 0o600))

		_, err := keyTreeWithin(t, worktree)
		require.ErrorIs(t, err, ErrWorktreeUnverified)
		assert.NotContains(t, err.Error(), "\x1b", "an escape sequence reaches the terminal")
		assert.NotContains(t, err.Error(), strings.Repeat("x", 400), "the pointer is truncated")
	})
}

// TestKeyTree_DotGitLinks: a .git that is a symbolic link lets one directory
// pass for another repository's worktree, so it is refused before any link
// is followed (ship-gate round 2, security-auditor Low, reproduced: a .git
// linked to another worktree's .git read and wrote that repository's list).
//
//nolint:paralleltest // hermeticGit sets environment variables
func TestKeyTree_DotGitLinks(t *testing.T) {
	t.Run("a .git linked to another worktree's .git", func(t *testing.T) {
		base, _, worktree, _ := worktreeLayout(t)

		claimant := filepath.Join(base, "local", "A")
		require.NoError(t, os.MkdirAll(claimant, 0o755))
		require.NoError(t, os.Symlink(filepath.Join(worktree, ".git"), filepath.Join(claimant, ".git")))

		got, err := keyTreeWithin(t, claimant)
		require.ErrorIs(t, err, ErrDotGitLink, "keyed as %q", got)
	})

	t.Run("a .git linked to another main tree's .git", func(t *testing.T) {
		base, main, _, _ := worktreeLayout(t)

		claimant := filepath.Join(base, "local", "A")
		require.NoError(t, os.MkdirAll(claimant, 0o755))
		require.NoError(t, os.Symlink(filepath.Join(main, ".git"), filepath.Join(claimant, ".git")))

		got, err := keyTreeWithin(t, claimant)
		require.ErrorIs(t, err, ErrDotGitLink, "keyed as %q", got)
	})

	t.Run("a worktree whose .git is a directory is not a linked worktree", func(t *testing.T) {
		_, main, _, gitDir := worktreeLayout(t)

		// keyTree reached with git's answers for a linked worktree, but a top
		// whose .git is a directory: it is not the file a linked worktree has.
		_, err := keyTree(osFS{}, main, gitDir, filepath.Join(main, ".git"))
		require.ErrorIs(t, err, ErrWorktreeUnverified)
	})
}

// TestKeyTree_ThirdCheck: the per-worktree directory sits in the claimed
// common directory's worktrees/ (check 1) and its record names this tree
// (check 2), but the common directory is not a working tree's .git (check 3).
// Only the third check stands between this layout and a key (ship-gate round
// 2, test-engineer High: that check had no coverage).
func TestKeyTree_ThirdCheck(t *testing.T) {
	t.Parallel()

	t.Run("a common directory that is no tree's .git", func(t *testing.T) {
		t.Parallel()

		root := mkdirs(t, t.TempDir(), "planted/common/worktrees/n", "attacker")
		common := filepath.Join(root, "planted", "common")
		gitDir := filepath.Join(common, "worktrees", "n")
		attacker := filepath.Join(root, "attacker")

		require.NoError(t, os.WriteFile(filepath.Join(attacker, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(gitDir, "gitdir"),
			[]byte(filepath.Join(attacker, ".git")+"\n"), 0o600))

		got, err := keyTree(osFS{}, attacker, gitDir, common)
		require.ErrorIs(t, err, ErrWorktreeUnverified, "keyed as %q", got)
	})

	t.Run("a main tree whose .git only links to the common directory", func(t *testing.T) {
		t.Parallel()

		root := mkdirs(t, t.TempDir(), "planted/common/worktrees/n", "victim", "attacker")
		common := filepath.Join(root, "planted", "common")
		gitDir := filepath.Join(common, "worktrees", "n")
		attacker := filepath.Join(root, "attacker")

		require.NoError(t, os.WriteFile(filepath.Join(attacker, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(gitDir, "gitdir"),
			[]byte(filepath.Join(attacker, ".git")+"\n"), 0o600))
		// planted/.git -> planted/common: Stat would call it the same file.
		require.NoError(t, os.Symlink(common, filepath.Join(root, "planted", ".git")))

		got, err := keyTree(osFS{}, attacker, gitDir, common)
		require.ErrorIs(t, err, ErrWorktreeUnverified, "keyed as %q", got)
	})
}

// TestKeyTree_NoMainTree: a worktree of a bare repository, or of one made
// with --separate-git-dir, has no main working tree to be keyed by. It is
// refused, and the message says why and what to do (ship-gate round 2,
// code-reviewer Optional).
//
//nolint:paralleltest // hermeticGit sets environment variables
func TestKeyTree_NoMainTree(t *testing.T) {
	t.Run("a bare repository's worktree", func(t *testing.T) {
		hermeticGit(t)

		base := mkdirs(t, t.TempDir(), "local")
		seed := filepath.Join(t.TempDir(), "seed")
		runGit(t, "init", "-q", "-b", "main", seed)
		commitIn(t, seed)

		bare := filepath.Join(base, "local", "OS.git")
		runGit(t, "clone", "-q", "--bare", seed, bare)

		worktree := filepath.Join(base, "local", "OS")
		runGit(t, "-C", bare, "worktree", "add", "-q", "-b", "wt", worktree)

		_, err := keyTreeWithin(t, worktree)
		require.ErrorIs(t, err, ErrWorktreeUnverified)
		assert.Contains(t, err.Error(), "no main working tree")
		assert.Contains(t, err.Error(), "origin")
	})

	t.Run("a --separate-git-dir repository's worktree", func(t *testing.T) {
		hermeticGit(t)

		base := mkdirs(t, t.TempDir(), "local")
		main := filepath.Join(base, "local", "OS")
		runGit(t, "init", "-q", "-b", "main", "--separate-git-dir", filepath.Join(base, "OS.git"), main)
		commitIn(t, main)

		worktree := filepath.Join(base, "local", "OS-wt")
		runGit(t, "-C", main, "worktree", "add", "-q", "-b", "wt", worktree)

		_, err := keyTreeWithin(t, worktree)
		require.ErrorIs(t, err, ErrWorktreeUnverified)
		assert.Contains(t, err.Error(), "no main working tree")

		// The main tree itself still keys by where it is.
		got, err := keyTreeWithin(t, main)
		require.NoError(t, err)
		assert.Equal(t, main, got)
	})
}
