// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brunovenceslau/canga/internal/repo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// widgetURL is the repository most cases here resolve. Its mixed case is the
// point: both directories must keep it.
const widgetURL = "git@github.com:Acme/Widget.git"

// widgetEnv is where widgetURL's environment lives by default: under envs/ in
// the docker-sbx clone of the same owner.
var widgetEnv = filepath.Join("github.com", "Acme", "docker-sbx", "envs", "github.com", "Acme", "Widget")

// layout points the clone base at a directory of the test's own, clears any
// environments repository the caller's shell set, and returns the base. It
// calls t.Setenv, so its callers cannot be parallel.
func layout(t *testing.T) string {
	t.Helper()

	base := t.TempDir()
	t.Setenv("CANGA_HOST_BASE_DIR", base)
	t.Setenv(EnvsRepoVar, "")

	return base
}

// mkdirs creates each directory, parents included.
func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()

	for _, dir := range dirs {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}
}

// With nothing configured, the environment is found in the same owner's
// docker-sbx clone, under the same base as the repository's own clone.
//
//nolint:paralleltest // t.Setenv forbids it
func TestResolve(t *testing.T) {
	base := layout(t)
	repoDir := filepath.Join(base, "github.com", "Acme", "Widget")
	envDir := filepath.Join(base, widgetEnv)
	mkdirs(t, repoDir, envDir)

	got, err := Resolve(widgetURL)
	require.NoError(t, err)
	assert.Equal(t, Target{Name: "Acme/Widget", EnvDir: envDir, RepoDir: repoDir}, got)
}

// A nested group keeps every group in the name: two repositories under
// same-named subgroups of different groups must not open under one name. The
// default environments repository sits beside the repository, in its group.
//
//nolint:paralleltest // t.Setenv forbids it
func TestResolve_NestedGroup(t *testing.T) {
	base := layout(t)
	tail := filepath.Join("gitlab.com", "acme", "platform", "widget")
	envDir := filepath.Join(base, "gitlab.com", "acme", "platform", "docker-sbx", "envs", tail)
	mkdirs(t, filepath.Join(base, tail), envDir)

	got, err := Resolve("https://gitlab.com/acme/platform/widget.git")
	require.NoError(t, err)
	assert.Equal(t, "acme/platform/widget", got.Name)
	assert.Equal(t, envDir, got.EnvDir)
}

// EnvsRepoVar points at a repository other than the owner's docker-sbx, still
// under the clone base.
func TestResolve_EnvsRepoOverride(t *testing.T) {
	tests := []struct {
		name string
		give string
	}{
		{name: "another owner's repository", give: "github.com/brunovenceslau/sandboxes"},
		{name: "trailing slash", give: "github.com/brunovenceslau/sandboxes/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := layout(t)
			t.Setenv(EnvsRepoVar, tt.give)

			envDir := filepath.Join(base, "github.com", "brunovenceslau", "sandboxes",
				"envs", "github.com", "Acme", "Widget")
			mkdirs(t, filepath.Join(base, "github.com", "Acme", "Widget"), envDir)

			got, err := Resolve(widgetURL)
			require.NoError(t, err)
			assert.Equal(t, envDir, got.EnvDir)
		})
	}
}

func TestResolve_Refusals(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		envsRepo string
		mkRepo   bool
		mkEnv    bool
		wantErr  error
		wantMsg  string
	}{
		{
			name: "envs repo absolute", url: widgetURL, envsRepo: "/srv/sandboxes",
			mkRepo: true, mkEnv: true, wantErr: ErrBadEnvsRepo, wantMsg: "absolute path",
		},
		// "/" trims to nothing; it must still be refused, not fall back to
		// the default.
		{
			name: "envs repo is the root", url: widgetURL, envsRepo: "///",
			mkRepo: true, mkEnv: true, wantErr: ErrBadEnvsRepo, wantMsg: "absolute path",
		},
		{
			name: "envs repo leaves the base", url: widgetURL, envsRepo: "../sandboxes",
			mkRepo: true, mkEnv: true, wantErr: ErrBadEnvsRepo, wantMsg: "not a URL",
		},
		{
			name: "envs repo with a dot segment", url: widgetURL, envsRepo: "github.com/acme/.",
			mkRepo: true, mkEnv: true, wantErr: ErrBadEnvsRepo,
		},
		{
			name: "envs repo as an scp-style url", url: widgetURL,
			envsRepo: "git@github.com:acme/docker-sbx", mkRepo: true, mkEnv: true,
			wantErr: ErrBadEnvsRepo, wantMsg: "not a URL",
		},
		// A credential in the variable must not reach the message.
		{
			name: "envs repo as a url with a credential", url: widgetURL,
			envsRepo: "https://bob:s3cret@github.com/acme/docker-sbx", mkRepo: true, mkEnv: true,
			wantErr: ErrBadEnvsRepo,
		},
		{
			name: "envs repo with a dot-dot segment", url: widgetURL,
			envsRepo: "github.com/../../etc", mkRepo: true, mkEnv: true, wantErr: ErrBadEnvsRepo,
		},
		{
			name: "envs repo with an empty segment", url: widgetURL,
			envsRepo: "github.com//sandboxes", mkRepo: true, mkEnv: true, wantErr: ErrBadEnvsRepo,
		},
		{
			name: "not cloned", url: widgetURL, mkEnv: true,
			wantErr: ErrNotCloned, wantMsg: "canga git clone <url>",
		},
		{
			name: "no environment", url: widgetURL, mkRepo: true,
			wantErr: ErrNoEnv, wantMsg: widgetEnv,
		},
		// The refusal says how to resolve it, not only what is missing.
		{
			name: "no environment, clone hint", url: widgetURL, mkRepo: true,
			wantErr: ErrNoEnv, wantMsg: "canga git clone <url>",
		},
		{
			name: "no environment, sync hint", url: widgetURL, mkRepo: true,
			wantErr: ErrNoEnv, wantMsg: "canga git sync",
		},
		{
			name: "no environment, variable hint", url: widgetURL, mkRepo: true,
			wantErr: ErrNoEnv, wantMsg: EnvsRepoVar,
		},
		{
			name: "bad url", url: "not-a-url", mkRepo: true, mkEnv: true,
			wantErr: repo.ErrBadURL,
		},
		// A credential in the URL must not reach the message.
		{
			name: "not cloned, credential kept out", url: "https://user:s3cret@github.com/Acme/Widget",
			mkEnv: true, wantErr: ErrNotCloned,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := layout(t)
			t.Setenv(EnvsRepoVar, tt.envsRepo)

			if tt.mkRepo {
				mkdirs(t, filepath.Join(base, "github.com", "Acme", "Widget"))
			}

			if tt.mkEnv {
				mkdirs(t, filepath.Join(base, widgetEnv))
			}

			_, err := Resolve(tt.url)
			require.ErrorIs(t, err, tt.wantErr)
			assert.Contains(t, err.Error(), tt.wantMsg)
			assert.NotContains(t, err.Error(), "s3cret")
		})
	}
}

// A file where a directory belongs is refused too: cmux would start the pane
// somewhere else without saying so.
//
//nolint:paralleltest // t.Setenv forbids it
func TestResolve_FileInsteadOfDirectory(t *testing.T) {
	base := layout(t)
	envFile := filepath.Join(base, widgetEnv)
	mkdirs(t, filepath.Join(base, "github.com", "Acme", "Widget"), filepath.Dir(envFile))
	require.NoError(t, os.WriteFile(envFile, nil, 0o600))

	_, err := Resolve(widgetURL)
	require.ErrorIs(t, err, ErrNoEnv)
	assert.ErrorIs(t, err, errNotADirectory)
}
