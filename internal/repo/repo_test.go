// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPath is the cross-check against dotfiles-host's tests/dev_test.sh. Every
// case below is one of that suite's, so the two implementations cannot drift on
// the path they both key off — which is the whole reason the derivation is
// allowed to exist twice at all.
const (
	// ownerRepo is the tail every canonical spelling below must collapse to.
	ownerRepo = "github.com/owner/repo"

	// shortHTTPS is a canonical https spelling reused across cases.
	shortHTTPS = "https://github.com/o/r"
)

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
		shortHTTPS,
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

	// Every spelling must be safe, and the whole secret must be gone, not the
	// half before the slash. Redact was fixed once and the message still leaked,
	// because the OFFENDING SEGMENT was quoted from the raw url beside the
	// redacted one: "refusing path segment \"SECONDHALF@github.com\"". The test
	// then passed because it only looked for the first half.
	// The half AFTER the slash is what used to escape, so it is what every case
	// hides and what every assertion looks for.
	const secret = "SECONDHALF"

	tests := []struct {
		name string
		url  string
	}{
		{name: "plain token", url: "https://u:FIRSTHALF" + secret + "@github.com/onlyhost"},
		{name: "slash inside the token", url: "https://u:FIRSTHALF/" + secret + "@github.com/o/r.git"},
		{name: "slash early in the token", url: "https://u:x/" + secret + "@github.com/o/r.git"},
		{name: "no password", url: "https://" + secret + "@github.com/onlyhost"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := Path(tt.url)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), secret, "the diagnostic carried the credential")
		})
	}
}

func TestEscapePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "already lowercase is untouched", path: ownerRepo, want: ownerRepo},
		{name: "each capital gains a bang", path: "github.com/Acme/Widget", want: "github.com/!acme/!widget"},
		{name: "several in one segment", path: "github.com/BurntSushi/toml", want: "github.com/!burnt!sushi/toml"},
		{name: "digits and punctuation survive", path: "git.corp.example/team-2/svc.v3", want: "git.corp.example/team-2/svc.v3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, EscapePath(tt.path))
		})
	}
}

// TestEscapePath_SurvivesACaseInsensitiveFilesystem is the property the
// encoding exists for. A store is shared between a mac, whose APFS folds case,
// and a linux sandbox, whose filesystem does not. Two spellings must therefore
// never be case-insensitively equal after encoding, or the two sides would
// disagree about whether they are looking at one list or two.
func TestEscapePath_SurvivesACaseInsensitiveFilesystem(t *testing.T) {
	t.Parallel()

	spellings := []string{
		"github.com/acme/widget",
		"github.com/Acme/widget",
		"github.com/acme/Widget",
		"github.com/ACME/WIDGET",
		"github.com/AcMe/WiDgEt",
	}

	seen := make(map[string]string, len(spellings))

	for _, spelling := range spellings {
		escaped := EscapePath(spelling)

		assert.Equal(t, strings.ToLower(escaped), escaped,
			"an encoded path must carry no capital for a filesystem to fold")

		folded := strings.ToLower(escaped)
		if previous, clash := seen[folded]; clash {
			t.Errorf("%q and %q collide on a case-insensitive filesystem", previous, spelling)
		}

		seen[folded] = spelling
	}
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
		{name: "nothing to redact", url: shortHTTPS, want: shortHTTPS},
		// The one that used to leak: a credential holding a "/" put the
		// authority split before the "@", so the "@" was never found and the
		// secret was echoed verbatim into a diagnostic.
		{name: "slash inside the credential", url: "https://user:pa/ss@github.com/o/r.git", want: "https://github.com/o/r.git"},
		{name: "slash and no password", url: "https://to/ken@github.com/o/r", want: shortHTTPS},
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
		runGit(t, "init", "-q", "-b", "main", dir)
		runGit(t, "-C", dir, "remote", "add", "origin", "git@github.com:acme/widget.git")

		got, err := Origin(t.Context(), dir)
		require.NoError(t, err)
		assert.Equal(t, "git@github.com:acme/widget.git", got)
	})

	// git applies url.<base>.insteadOf to `remote get-url`, so what canga sees
	// is the EFFECTIVE url, not the configured one. That is harmless precisely
	// because both spellings collapse to the same path — which is what this
	// asserts, rather than assuming it.
	t.Run("an insteadOf rewrite does not move the repository", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "repo")
		runGit(t, "init", "-q", "-b", "main", dir)
		runGit(t, "-C", dir, "remote", "add", "origin", "git@github.com:acme/widget.git")
		runGit(t, "-C", dir, "config", "url.https://github.com/.insteadOf", "git@github.com:")

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
		runGit(t, "init", "-q", "-b", "main", dir)

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

	// classify is shared by every git call, so its cancellation message must
	// not name one of them. A Ctrl-C during `canga setup hooks`, which never
	// asks for a remote, used to report "reading the origin".
	t.Run("cancellation does not name an operation that never ran", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := Root(ctx, t.TempDir())
		require.ErrorIs(t, err, context.Canceled)
		assert.NotContains(t, err.Error(), "origin")
	})

	t.Run("git not installed", func(t *testing.T) {
		t.Setenv("PATH", "")

		_, err := Origin(t.Context(), t.TempDir())
		require.ErrorIs(t, err, ErrGitMissing)
		require.NotErrorIs(t, err, ErrNoOrigin)
	})
}

//nolint:paralleltest // t.Setenv, which the hermetic git config needs, forbids it
func TestRootAndCommonDir(t *testing.T) {
	hermeticGit(t)

	dir := filepath.Join(t.TempDir(), "repo")
	runGit(t, "init", "-q", "-b", "main", dir)

	sub := filepath.Join(dir, "a", "b")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	// Resolved from a SUBDIRECTORY: every canga command is keyed by a
	// repository, and a caller is rarely standing at its top level.
	root, err := Root(t.Context(), sub)
	require.NoError(t, err)
	assert.Equal(t, resolve(t, dir), resolve(t, root))

	common, err := CommonDir(t.Context(), sub)
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(common), "hooks are written by absolute path, so this must be one")
	assert.Equal(t, resolve(t, filepath.Join(dir, ".git")), resolve(t, common))

	_, err = Root(t.Context(), t.TempDir())
	require.ErrorIs(t, err, ErrNotARepository)
}

//nolint:paralleltest // t.Setenv, which the hermetic git config needs, forbids it
func TestConfigRoundTrip(t *testing.T) {
	hermeticGit(t)

	dir := filepath.Join(t.TempDir(), "repo")
	runGit(t, "init", "-q", "-b", "main", dir)

	// An unset key is an ordinary state, reported as "" rather than an error.
	// git signals it by exiting 1, which has to be told apart from the 128 a
	// directory that is not a repository exits with.
	value, set, err := Config(t.Context(), dir, "core.hooksPath")
	require.NoError(t, err)
	assert.Empty(t, value)
	assert.False(t, set)

	require.NoError(t, SetConfig(t.Context(), dir, "core.hooksPath", ".canga/hooks"))

	value, set, err = Config(t.Context(), dir, "core.hooksPath")
	require.NoError(t, err)
	assert.Equal(t, ".canga/hooks", value)
	assert.True(t, set)

	// The distinction the bool exists for: git answers an unset key and a key
	// set to the empty string identically apart from its exit code, and for
	// core.hooksPath they mean opposite things. Unset leaves git reading
	// $GIT_DIR/hooks; set-to-empty makes it read no hooks at all.
	require.NoError(t, SetConfig(t.Context(), dir, "core.hooksPath", ""))

	value, set, err = Config(t.Context(), dir, "core.hooksPath")
	require.NoError(t, err)
	assert.Empty(t, value)
	assert.True(t, set, "an empty value must not read as unset")

	// `git config --get` needs no repository, so outside one this reads the
	// global configuration rather than failing. Pinned because it is surprising:
	// callers that mean the repository's own setting resolve Root first.
	outside, set, err := Config(t.Context(), t.TempDir(), "core.hooksPath")
	require.NoError(t, err)
	assert.Empty(t, outside, "the hermetic global config sets nothing")
	assert.False(t, set)
}

// resolve follows symlinks so an assertion does not depend on /var being a link
// to /private/var, which is what macOS makes of a temporary directory.
func resolve(t *testing.T, path string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)

	return resolved
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

func runGit(t *testing.T, args ...string) {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "git", args...).CombinedOutput()
	require.NoErrorf(t, err, "git %v: %s", args, out)
}
