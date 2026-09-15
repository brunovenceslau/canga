package repo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPath is the cross-check against dotfiles-host's tests/dev_test.sh. Every
// case below is one of that suite's, so the two implementations cannot drift on
// the path they both key off — which is the whole reason the derivation is
// allowed to exist twice at all.
// ownerRepo is the tail every canonical spelling below must collapse to.
const ownerRepo = "github.com/owner/repo"

func TestPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "scp-like", url: "git@github.com:owner/repo.git", want: ownerRepo},
		{name: "https", url: "https://github.com/owner/repo.git", want: ownerRepo},
		{name: "ssh", url: "ssh://git@github.com/owner/repo.git", want: ownerRepo},
		{name: "ssh with port dropped", url: "ssh://git@github.com:22/owner/repo.git", want: ownerRepo},
		{name: "no .git suffix", url: "https://github.com/owner/repo", want: ownerRepo},
		{name: "https userinfo stripped", url: "https://me@github.com/owner/repo.git", want: ownerRepo},
		{name: "trailing slash", url: "https://github.com/owner/repo.git/", want: ownerRepo},
		{name: "nested subgroups kept", url: "git@gitlab.com:group/sub/proj.git", want: "gitlab.com/group/sub/proj"},
		{name: "self-hosted host kept", url: "git@git.corp.example:team/svc.git", want: "git.corp.example/team/svc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Path(tt.url)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestPath_Determinism is the claim the layout rests on: how a repository was
// cloned must not change where it lands.
func TestPath_Determinism(t *testing.T) {
	t.Parallel()

	spellings := []string{
		"git@github.com:o/r.git",
		"https://github.com/o/r.git",
		"ssh://git@github.com/o/r.git",
		"https://github.com/o/r",
	}

	for _, url := range spellings {
		got, err := Path(url)
		require.NoErrorf(t, err, "url %q", url)
		assert.Equalf(t, "github.com/o/r", got, "url %q", url)
	}
}

func TestPath_Rejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
	}{
		{name: "not a url at all", url: "not-a-url"},
		{name: "host with no repo", url: "https://github.com/onlyhost"},
		// A '.'/'..' segment would escape the store root once the tail is joined
		// onto it. Valid owner and repo names never contain one.
		{name: "parent traversal", url: "git@github.com:../../etc/repo.git"},
		{name: "dot segment", url: "https://github.com/./repo.git"},
		{name: "empty segment", url: "https://github.com//repo.git"},
		{name: "separator smuggled in", url: "https://github.com/owner/re po"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := Path(tt.url)
			require.ErrorIs(t, err, ErrBadURL)
		})
	}
}

// TestPath_RedactsCredentials guards the diagnostic, not the parse: a token
// mistyped into a URL must not reach the terminal scrollback or a CI log just
// because the URL failed to parse.
func TestPath_RedactsCredentials(t *testing.T) {
	t.Parallel()

	_, err := Path("https://u:s3cr3ttoken@github.com/onlyhost")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "s3cr3ttoken")
	assert.Contains(t, err.Error(), "github.com")
}

func TestRedact(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "password", url: "https://u:p@github.com/o/r.git", want: "https://github.com/o/r.git"},
		{name: "user only", url: "ssh://git@github.com/o/r", want: "ssh://github.com/o/r"},
		{name: "scp-like", url: "git@github.com:o/r.git", want: "github.com:o/r.git"},
		{name: "nothing to redact", url: "https://github.com/o/r", want: "https://github.com/o/r"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, Redact(tt.url))
		})
	}
}

// TestOrigin cannot run in parallel: it neutralizes the developer's real git
// config through the environment, and t.Setenv forbids a parallel test. Without
// that the suite is not host-independent — this sandbox's own ~/.gitconfig
// carries `url.https://github.com/.insteadOf = git@github.com:`, which silently
// rewrote the expected URL until the test was made hermetic.
//
//nolint:paralleltest // t.Setenv, which the hermetic git config needs, forbids it
func TestOrigin(t *testing.T) {
	hermeticGit(t)

	t.Run("reads the origin url", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "repo")
		git(t, "init", "-q", "-b", "main", dir)
		git(t, "-C", dir, "remote", "add", "origin", "git@github.com:acme/widget.git")

		got, err := Origin(t.Context(), dir)
		require.NoError(t, err)
		assert.Equal(t, "git@github.com:acme/widget.git", got)
	})

	// git applies url.<base>.insteadOf to `remote get-url`, so what devctl sees
	// is the EFFECTIVE url, not the configured one. That is harmless precisely
	// because both spellings collapse to the same path — which is what this
	// asserts, rather than assuming it.
	t.Run("an insteadOf rewrite does not move the repository", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "repo")
		git(t, "init", "-q", "-b", "main", dir)
		git(t, "-C", dir, "remote", "add", "origin", "git@github.com:acme/widget.git")
		git(t, "-C", dir, "config", "url.https://github.com/.insteadOf", "git@github.com:")

		origin, err := Origin(t.Context(), dir)
		require.NoError(t, err)
		assert.Equal(t, "https://github.com/acme/widget.git", origin)

		rewritten, err := Path(origin)
		require.NoError(t, err)

		direct, err := Path("git@github.com:acme/widget.git")
		require.NoError(t, err)
		assert.Equal(t, direct, rewritten)
	})

	// git's own words travel with the error. Without them "no origin remote"
	// is the only thing a user sees when git refuses the repository for a
	// reason that has nothing to do with remotes — dubious ownership on a
	// host directory mounted into a sandbox being the case that matters here.
	t.Run("not a repository", func(t *testing.T) {
		_, err := Origin(t.Context(), t.TempDir())
		require.ErrorIs(t, err, ErrNoOrigin)
		assert.Contains(t, err.Error(), "not a git repository",
			"git's own diagnostic must reach the user")
	})

	t.Run("repository with no origin", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "repo")
		git(t, "init", "-q", "-b", "main", dir)

		_, err := Origin(t.Context(), dir)
		require.ErrorIs(t, err, ErrNoOrigin)
	})

	// A Ctrl-C and a missing git binary are NOT "no origin remote": answering
	// either with that would send the user looking for a remote that is not the
	// problem, and would exit 2 for what is a runtime failure.
	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := Origin(ctx, t.TempDir())
		require.ErrorIs(t, err, context.Canceled)
		require.NotErrorIs(t, err, ErrNoOrigin)
	})

	t.Run("git not installed", func(t *testing.T) {
		t.Setenv("PATH", "")

		_, err := Origin(t.Context(), t.TempDir())
		require.ErrorIs(t, err, ErrGitMissing)
		require.NotErrorIs(t, err, ErrNoOrigin)
	})
}

// hermeticGit points git at empty system and global config files, so nothing
// the developer or the sandbox configured can reach a test.
func hermeticGit(t *testing.T) {
	t.Helper()

	global := filepath.Join(t.TempDir(), "gitconfig")
	require.NoError(t, os.WriteFile(global, nil, 0o600))

	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_GLOBAL", global)
}

func git(t *testing.T, args ...string) {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "git", args...).CombinedOutput()
	require.NoErrorf(t, err, "git %v: %s", args, out)
}
