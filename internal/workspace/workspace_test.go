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

// layout points the clone base and the envs root at directories of the test's
// own and returns both. It calls t.Setenv, so its callers cannot be parallel.
func layout(t *testing.T) (base, envs string) {
	t.Helper()

	base = t.TempDir()
	envs = t.TempDir()

	t.Setenv("CANGA_HOST_BASE_DIR", base)
	t.Setenv(EnvsDirVar, envs)

	return base, envs
}

// mkdirs creates each directory, parents included.
func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()

	for _, dir := range dirs {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}
}

//nolint:paralleltest // t.Setenv forbids it
func TestResolve(t *testing.T) {
	base, envs := layout(t)
	repoDir := filepath.Join(base, "github.com", "Acme", "Widget")
	envDir := filepath.Join(envs, "github.com", "Acme", "Widget")
	mkdirs(t, repoDir, envDir)

	got, err := Resolve(widgetURL)
	require.NoError(t, err)
	assert.Equal(t, Target{Name: "Acme/Widget", EnvDir: envDir, RepoDir: repoDir}, got)
}

// A nested group keeps every group in the name: two repositories under
// same-named subgroups of different groups must not open under one name.
//
//nolint:paralleltest // t.Setenv forbids it
func TestResolve_NestedGroupName(t *testing.T) {
	base, envs := layout(t)
	tail := filepath.Join("gitlab.com", "acme", "platform", "widget")
	mkdirs(t, filepath.Join(base, tail), filepath.Join(envs, tail))

	got, err := Resolve("https://gitlab.com/acme/platform/widget.git")
	require.NoError(t, err)
	assert.Equal(t, "acme/platform/widget", got.Name)
}

// A relative root is resolved against the current directory once, here,
// rather than handed to cmux, which would resolve it against its own.
func TestResolve_RelativeEnvsDir(t *testing.T) {
	base, _ := layout(t)
	cwd := t.TempDir()
	t.Chdir(cwd)
	t.Setenv(EnvsDirVar, "envs")

	// t.Chdir may land on a symlink's target (/var and /private/var on macOS);
	// compare against what the process itself calls this directory.
	here, err := os.Getwd()
	require.NoError(t, err)

	envDir := filepath.Join(here, "envs", "github.com", "Acme", "Widget")
	mkdirs(t, filepath.Join(base, "github.com", "Acme", "Widget"), envDir)

	got, err := Resolve(widgetURL)
	require.NoError(t, err)
	assert.Equal(t, envDir, got.EnvDir)
	assert.True(t, filepath.IsAbs(got.EnvDir))
}

func TestResolve_Refusals(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		unset   bool
		mkRepo  bool
		mkEnv   bool
		wantErr error
		wantMsg string
	}{
		{
			name: "envs root unset", url: widgetURL, unset: true, mkRepo: true, mkEnv: true,
			wantErr: ErrNoEnvsDir, wantMsg: EnvsDirVar,
		},
		{
			name: "not cloned", url: widgetURL, mkEnv: true,
			wantErr: ErrNotCloned, wantMsg: "canga git clone <url>",
		},
		{
			name: "no environment", url: widgetURL, mkRepo: true,
			wantErr: ErrNoEnv, wantMsg: filepath.Join("github.com", "Acme", "Widget"),
		},
		// The refusal says how to resolve it, not only what is missing.
		{
			name: "no environment, hint", url: widgetURL, mkRepo: true,
			wantErr: ErrNoEnv, wantMsg: "update the checkout that holds the environments",
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
			base, envs := layout(t)
			if tt.mkRepo {
				mkdirs(t, filepath.Join(base, "github.com", "Acme", "Widget"))
			}

			if tt.mkEnv {
				mkdirs(t, filepath.Join(envs, "github.com", "Acme", "Widget"))
			}

			if tt.unset {
				t.Setenv(EnvsDirVar, "")
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
	base, envs := layout(t)
	mkdirs(t, filepath.Join(base, "github.com", "Acme", "Widget"), filepath.Join(envs, "github.com", "Acme"))
	require.NoError(t, os.WriteFile(filepath.Join(envs, "github.com", "Acme", "Widget"), nil, 0o600))

	_, err := Resolve(widgetURL)
	require.ErrorIs(t, err, ErrNoEnv)
	assert.ErrorIs(t, err, errNotADirectory)
}
