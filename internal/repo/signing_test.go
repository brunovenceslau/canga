// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// localValue reads a key from a repository's OWN config file, never the
// effective value. Every assertion here is about what was written INTO the
// clone, so a global key leaking in would otherwise pass the test.
func localValue(t *testing.T, dir, key string) string {
	t.Helper()

	value, err := git(t.Context(), dir, ErrNotARepository, "config", "--local", "--get", key)
	if err != nil {
		return ""
	}

	return value
}

func TestStampSigning(t *testing.T) {
	t.Run("the environment wins over the global config", func(t *testing.T) {
		hermeticGit(t)

		dir := filepath.Join(t.TempDir(), "repo")
		runGit(t, "init", "-q", "-b", "main", dir)
		runGit(t, "config", "--global", "user.signingkey", "global-key")
		t.Setenv(envSigningKey, "explicit-key")
		t.Setenv(envAllowedSigners, "/etc/allowed_signers")

		stamped, err := StampSigning(t.Context(), dir)
		require.NoError(t, err)
		assert.True(t, stamped.On())
		assert.Equal(t, "explicit-key", stamped.Key)

		assert.Equal(t, "explicit-key", localValue(t, dir, "user.signingkey"))
		assert.Equal(t, "true", localValue(t, dir, "commit.gpgsign"))
		assert.Equal(t, "true", localValue(t, dir, "tag.gpgsign"))

		// Without this the clone signs with git's default format, openpgp, and
		// an SSH key fails every commit on a machine that does not set
		// gpg.format=ssh globally.
		assert.Equal(t, "ssh", localValue(t, dir, "gpg.format"))
		assert.Equal(t, "/etc/allowed_signers", localValue(t, dir, "gpg.ssh.allowedSignersFile"))
	})

	// The key must be the MACHINE's, which is why the fallback reads the global
	// config rather than the effective one: a repo-local key belonging to
	// whatever repository canga was invoked in is the wrong answer.
	t.Run("falls back to the global config", func(t *testing.T) {
		hermeticGit(t)

		dir := filepath.Join(t.TempDir(), "repo")
		runGit(t, "init", "-q", "-b", "main", dir)
		runGit(t, "config", "--global", "user.signingkey", "global-key")
		runGit(t, "config", "--global", "gpg.ssh.allowedSignersFile", "/global/signers")
		t.Setenv(envSigningKey, "")
		t.Setenv(envAllowedSigners, "")

		stamped, err := StampSigning(t.Context(), dir)
		require.NoError(t, err)
		assert.Equal(t, "global-key", stamped.Key)
		assert.Equal(t, "/global/signers", stamped.AllowedSigners)
		assert.Equal(t, "global-key", localValue(t, dir, "user.signingkey"))
	})

	// Nothing resolves: the clone is left ALONE rather than pointed at a file
	// that does not exist, which would fail every `git log --show-signature`.
	t.Run("no key leaves signing off", func(t *testing.T) {
		hermeticGit(t)

		dir := filepath.Join(t.TempDir(), "repo")
		runGit(t, "init", "-q", "-b", "main", dir)
		t.Setenv(envSigningKey, "")
		t.Setenv(envAllowedSigners, "")

		stamped, err := StampSigning(t.Context(), dir)
		require.NoError(t, err)
		assert.False(t, stamped.On())
		assert.Empty(t, localValue(t, dir, "user.signingkey"))
		assert.Empty(t, localValue(t, dir, "commit.gpgsign"))
		assert.Empty(t, localValue(t, dir, "gpg.format"))
		assert.Empty(t, localValue(t, dir, "gpg.ssh.allowedSignersFile"))
	})

	// A sandbox signs through /etc/gitconfig: commit.gpgsign and a key command,
	// but no user.signingkey for the fallback to read. Nothing is stamped, and
	// the result must still say the clone signs, or the user is told to fix
	// something that works.
	t.Run("signing turned on outside the repository is reported as inherited", func(t *testing.T) {
		hermeticGit(t)

		system := filepath.Join(t.TempDir(), "gitconfig")
		require.NoError(t, os.WriteFile(system, []byte("[commit]\n\tgpgSign = yes\n"), 0o600))
		t.Setenv("GIT_CONFIG_SYSTEM", system)

		dir := filepath.Join(t.TempDir(), "repo")
		runGit(t, "init", "-q", "-b", "main", dir)
		t.Setenv(envSigningKey, "")
		t.Setenv(envAllowedSigners, "")

		stamped, err := StampSigning(t.Context(), dir)
		require.NoError(t, err)
		assert.True(t, stamped.Inherited)
		assert.True(t, stamped.On())
		assert.Empty(t, stamped.Key)
		assert.Empty(t, localValue(t, dir, "commit.gpgsign"), "nothing is stamped")
	})

	// An allowed-signers file without a key is a repository that can VERIFY
	// signatures without making any, which is a useful state and not an
	// accident: the two resolve independently.
	t.Run("allowed signers alone", func(t *testing.T) {
		hermeticGit(t)

		dir := filepath.Join(t.TempDir(), "repo")
		runGit(t, "init", "-q", "-b", "main", dir)
		t.Setenv(envSigningKey, "")
		t.Setenv(envAllowedSigners, "/only/signers")

		stamped, err := StampSigning(t.Context(), dir)
		require.NoError(t, err)
		assert.False(t, stamped.On())
		assert.Equal(t, "/only/signers", localValue(t, dir, "gpg.ssh.allowedSignersFile"))
		assert.Empty(t, localValue(t, dir, "commit.gpgsign"))
		assert.Empty(t, localValue(t, dir, "gpg.format"), "nothing to sign with, nothing to declare")
	})

	// Writes are LOCAL. A tool that stamps a machine's global config while
	// cloning would be changing how every other repository commits.
	t.Run("writes nothing globally", func(t *testing.T) {
		hermeticGit(t)

		dir := filepath.Join(t.TempDir(), "repo")
		runGit(t, "init", "-q", "-b", "main", dir)
		t.Setenv(envSigningKey, "explicit-key")

		_, err := StampSigning(t.Context(), dir)
		require.NoError(t, err)

		global, err := GlobalConfig(t.Context(), "user.signingkey")
		require.NoError(t, err)
		assert.Empty(t, global)
	})

	t.Run("not a repository", func(t *testing.T) {
		hermeticGit(t)

		t.Setenv(envSigningKey, "explicit-key")

		_, err := StampSigning(t.Context(), t.TempDir())
		require.ErrorIs(t, err, ErrNotARepository)
	})
}

//nolint:paralleltest // t.Setenv, which the hermetic git config needs, forbids it
func TestGlobalConfig(t *testing.T) {
	// git answers an unset key by exiting 1, which is not a failure: most
	// machines set neither of the keys this reads.
	t.Run("an unset key is empty, not an error", func(t *testing.T) {
		hermeticGit(t)

		value, err := GlobalConfig(t.Context(), "user.signingkey")
		require.NoError(t, err)
		assert.Empty(t, value)
	})

	t.Run("reads a set key", func(t *testing.T) {
		hermeticGit(t)

		runGit(t, "config", "--global", "user.signingkey", "the-key")

		value, err := GlobalConfig(t.Context(), "user.signingkey")
		require.NoError(t, err)
		assert.Equal(t, "the-key", value)
	})

	// The scope is the point: Config reports the EFFECTIVE value, so inside a
	// repository it would answer with that repository's own key. This must not.
	t.Run("ignores a repository's own key", func(t *testing.T) {
		hermeticGit(t)

		dir := filepath.Join(t.TempDir(), "repo")
		runGit(t, "init", "-q", "-b", "main", dir)
		runGit(t, "-C", dir, "config", "--local", "user.signingkey", "repo-key")
		t.Chdir(dir)

		value, err := GlobalConfig(t.Context(), "user.signingkey")
		require.NoError(t, err)
		assert.NotEqual(t, "repo-key", value)
	})

	t.Run("git not installed", func(t *testing.T) {
		hermeticGit(t)

		t.Setenv("PATH", "")

		_, err := GlobalConfig(t.Context(), "user.signingkey")
		require.ErrorIs(t, err, ErrGitMissing)
	})
}
