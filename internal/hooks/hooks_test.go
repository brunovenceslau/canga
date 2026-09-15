package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/brunovenceslau/devctl/internal/repo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scratchRepo builds a git repository with the given executable hooks under
// .devctl/hooks, and points git at empty system and global config so nothing
// the developer or the sandbox configured can reach the test.
func scratchRepo(t *testing.T, names ...string) string {
	t.Helper()

	global := filepath.Join(t.TempDir(), "gitconfig")
	require.NoError(t, os.WriteFile(global, nil, 0o600))

	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_GLOBAL", global)

	root := filepath.Join(t.TempDir(), "repo")
	out, err := exec.CommandContext(t.Context(), "git", "init", "-q", "-b", "main", root).CombinedOutput()
	require.NoErrorf(t, err, "git init: %s", out)

	// Canonicalized, because git reports the RESOLVED top level. On macOS a
	// temporary directory lives under /var, which is a symlink to /private/var,
	// so an uncanonicalized path compares unequal to git's answer for the same
	// directory and the test fails only on the mac.
	root, err = filepath.EvalSymlinks(root)
	require.NoError(t, err)

	if len(names) > 0 {
		require.NoError(t, os.MkdirAll(filepath.Join(root, SourceDir), 0o755))
	}

	for _, name := range names {
		write(t, filepath.Join(root, SourceDir, name), 0o755)
	}

	return root
}

func write(t *testing.T, path string, mode os.FileMode) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), mode))
}

//nolint:paralleltest // t.Setenv, which the hermetic git config needs, forbids it
func TestInstall_Rejects(t *testing.T) {
	t.Run("outside a repository", func(t *testing.T) {
		// The requirement in the caller's words: fail with the reason, not a
		// silent no-op, when the directory is not in a git repository.
		_, err := Install(t.Context(), t.TempDir(), Options{})
		require.ErrorIs(t, err, repo.ErrNotARepository)
	})

	t.Run("repository with no hooks directory", func(t *testing.T) {
		_, err := Install(t.Context(), scratchRepo(t), Options{})
		require.ErrorIs(t, err, ErrNoHooks)
	})

	t.Run("hooks directory with nothing executable", func(t *testing.T) {
		root := scratchRepo(t)
		// A non-executable hook is skipped rather than installed: git would
		// silently never run it, and reporting it as installed would lie.
		write(t, filepath.Join(root, SourceDir, "pre-commit"), 0o644)

		_, err := Install(t.Context(), root, Options{})
		require.ErrorIs(t, err, ErrNoHooks)
	})
}

//nolint:paralleltest // t.Setenv, which the hermetic git config needs, forbids it
func TestInstall_HooksPath(t *testing.T) {
	t.Run("sets the config and reports the hooks", func(t *testing.T) {
		root := scratchRepo(t, "pre-commit", "commit-msg")

		report, err := Install(t.Context(), root, Options{})
		require.NoError(t, err)

		assert.Equal(t, "core.hooksPath", report.Mode)
		assert.Equal(t, []string{"commit-msg", "pre-commit"}, report.Installed)
		assert.Empty(t, report.Warnings)

		value, err := repo.Config(t.Context(), root, "core.hooksPath")
		require.NoError(t, err)
		assert.Equal(t, SourceDir, value)
	})

	// The requirement that the command works from anywhere inside the tree,
	// not only from its top level.
	t.Run("works from a subdirectory", func(t *testing.T) {
		root := scratchRepo(t, "pre-commit")
		sub := filepath.Join(root, "a", "b")
		require.NoError(t, os.MkdirAll(sub, 0o755))

		report, err := Install(t.Context(), sub, Options{})
		require.NoError(t, err)
		assert.Equal(t, root, report.Root)
	})

	// The cost of this mode is stated rather than discovered: git reads hooks
	// from one directory, so anything already in the default one stops running.
	t.Run("warns about hooks it will shadow", func(t *testing.T) {
		root := scratchRepo(t, "pre-commit")
		write(t, filepath.Join(root, ".git", "hooks", "pre-push"), 0o755)

		report, err := Install(t.Context(), root, Options{})
		require.NoError(t, err)
		require.Len(t, report.Warnings, 1)
		assert.Contains(t, report.Warnings[0], "pre-push")
	})

	t.Run("ignores git's own samples", func(t *testing.T) {
		root := scratchRepo(t, "pre-commit")
		write(t, filepath.Join(root, ".git", "hooks", "pre-push.sample"), 0o755)

		report, err := Install(t.Context(), root, Options{})
		require.NoError(t, err)
		assert.Empty(t, report.Warnings)
	})

	t.Run("refuses a foreign setting, replaces it with force", func(t *testing.T) {
		root := scratchRepo(t, "pre-commit")
		require.NoError(t, repo.SetConfig(t.Context(), root, "core.hooksPath", "/somewhere/else"))

		_, err := Install(t.Context(), root, Options{})
		require.ErrorIs(t, err, ErrConflict)

		value, err := repo.Config(t.Context(), root, "core.hooksPath")
		require.NoError(t, err)
		assert.Equal(t, "/somewhere/else", value, "a refusal must change nothing")

		_, err = Install(t.Context(), root, Options{Force: true})
		require.NoError(t, err)

		value, err = repo.Config(t.Context(), root, "core.hooksPath")
		require.NoError(t, err)
		assert.Equal(t, SourceDir, value)
	})

	t.Run("re-running is not a conflict with itself", func(t *testing.T) {
		root := scratchRepo(t, "pre-commit")

		_, err := Install(t.Context(), root, Options{})
		require.NoError(t, err)

		_, err = Install(t.Context(), root, Options{})
		require.NoError(t, err)
	})
}

//nolint:paralleltest // t.Setenv, which the hermetic git config needs, forbids it
func TestInstall_Symlink(t *testing.T) {
	t.Run("links relatively, so moving the repository does not break it", func(t *testing.T) {
		root := scratchRepo(t, "pre-commit")

		report, err := Install(t.Context(), root, Options{Symlink: true})
		require.NoError(t, err)
		assert.Equal(t, "symlink", report.Mode)
		assert.Equal(t, []string{"pre-commit"}, report.Installed)

		link := filepath.Join(root, ".git", "hooks", "pre-commit")

		target, err := os.Readlink(link)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("..", "..", SourceDir, "pre-commit"), target)
		assert.False(t, filepath.IsAbs(target), "an absolute link breaks when the repository moves")

		// core.hooksPath is left alone in this mode, which is the point of it.
		value, err := repo.Config(t.Context(), root, "core.hooksPath")
		require.NoError(t, err)
		assert.Empty(t, value)
	})

	t.Run("re-running is idempotent", func(t *testing.T) {
		root := scratchRepo(t, "pre-commit")

		_, err := Install(t.Context(), root, Options{Symlink: true})
		require.NoError(t, err)

		report, err := Install(t.Context(), root, Options{Symlink: true})
		require.NoError(t, err)
		assert.Equal(t, []string{"pre-commit"}, report.Installed)
		assert.Empty(t, report.BackedUp, "an already-correct link must not be backed up")
	})

	t.Run("refuses a file in the way, backs it up with force", func(t *testing.T) {
		root := scratchRepo(t, "pre-commit")
		existing := filepath.Join(root, ".git", "hooks", "pre-commit")
		require.NoError(t, os.MkdirAll(filepath.Dir(existing), 0o755))
		require.NoError(t, os.WriteFile(existing, []byte("mine\n"), 0o755))

		_, err := Install(t.Context(), root, Options{Symlink: true})
		require.ErrorIs(t, err, ErrConflict)

		kept, err := os.ReadFile(existing)
		require.NoError(t, err)
		assert.Equal(t, "mine\n", string(kept), "a refusal must change nothing")

		report, err := Install(t.Context(), root, Options{Symlink: true, Force: true})
		require.NoError(t, err)
		require.Len(t, report.BackedUp, 1)

		backed, err := os.ReadFile(existing + backupSuffix)
		require.NoError(t, err)
		assert.Equal(t, "mine\n", string(backed), "force moves the file aside, it never deletes it")
	})

	// The first .bak is the pristine one. Losing it to a second run would be the
	// one unrecoverable mistake this code could make.
	t.Run("force never overwrites an existing backup", func(t *testing.T) {
		root := scratchRepo(t, "pre-commit")
		hook := filepath.Join(root, ".git", "hooks", "pre-commit")
		require.NoError(t, os.MkdirAll(filepath.Dir(hook), 0o755))
		require.NoError(t, os.WriteFile(hook+backupSuffix, []byte("pristine\n"), 0o755))
		require.NoError(t, os.WriteFile(hook, []byte("later\n"), 0o755))

		_, err := Install(t.Context(), root, Options{Symlink: true, Force: true})
		require.ErrorIs(t, err, ErrConflict)

		kept, err := os.ReadFile(hook + backupSuffix)
		require.NoError(t, err)
		assert.Equal(t, "pristine\n", string(kept))
	})
}
