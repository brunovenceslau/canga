// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package workspace

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCmux puts a `cmux` on a PATH of its own that records its arguments, one
// per line, runs body, and returns the file the arguments land in. The layout
// is compact JSON, so no argument spans two lines.
func fakeCmux(t *testing.T, body string) (argsFile string) {
	t.Helper()

	bin := t.TempDir()
	argsFile = filepath.Join(t.TempDir(), "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argsFile + "'\n" + body + "\n"

	require.NoError(t, os.WriteFile(filepath.Join(bin, "cmux"), []byte(script), 0o755))
	t.Setenv("PATH", bin)

	return argsFile
}

// target is a Target whose paths hold the characters a hand-built JSON string
// would get wrong. EnvsCeilingDir is EnvDir's grandparent's parent, the same
// three levels (host, owner, repo-env) Resolve would strip to derive it.
var target = Target{
	Name:           "acme/widget",
	EnvDir:         `/envs/github.com/acme/wid"get\`,
	EnvsCeilingDir: "/envs",
	RepoDir:        "/src/github.com/acme/widget",
}

func TestOpenCmux(t *testing.T) {
	argsFile := fakeCmux(t, "echo OK workspace:7")
	t.Setenv("GIT_CEILING_DIRECTORIES", "")

	var out, errOut bytes.Buffer
	require.NoError(t, OpenCmux(t.Context(), target, &out, &errOut))
	assert.Equal(t, "OK workspace:7\n", out.String(), "cmux's stdout is passed through")
	assert.Empty(t, errOut.String())

	recorded, err := os.ReadFile(argsFile)
	require.NoError(t, err)

	args := strings.Split(strings.TrimSuffix(string(recorded), "\n"), "\n")
	require.Len(t, args, 9)
	assert.Equal(t, []string{
		"new-workspace",
		"--name", "acme/widget",
		"--cwd", "/src/github.com/acme/widget",
		"--focus", "true",
		"--layout",
	}, args[:8])

	var got layoutNode
	require.NoError(t, json.Unmarshal([]byte(args[8]), &got))
	assert.Equal(t, layoutNode{
		Direction: "horizontal",
		Children: []layoutNode{
			{Pane: &layoutPane{Surfaces: []layoutSurface{
				{
					Type: "terminal", Cwd: target.EnvDir, Command: "sbx env run --clone",
					Env: map[string]string{"GIT_CEILING_DIRECTORIES": target.EnvsCeilingDir},
				},
			}}},
			{Pane: &layoutPane{Surfaces: []layoutSurface{{Type: "terminal", Cwd: target.RepoDir, Focus: true}}}},
		},
	}, got, "env on the left running its sandbox, clone on the right and focused")

	// The keys cmux reads, spelled as it spells them; a struct-to-struct round
	// trip would pass with any tag.
	for _, key := range []string{
		`"direction":"horizontal"`, `"children":`, `"pane":`,
		`"surfaces":`, `"type":"terminal"`, `"cwd":`, `"focus":true`,
		`"command":"sbx env run --clone"`,
		`"env":`, `"GIT_CEILING_DIRECTORIES":"/envs"`,
	} {
		assert.Contains(t, args[8], key)
	}
}

// TestOpenCmux_GitCeilingInherited covers the os.Getenv wiring in OpenCmux
// itself: TestCmuxArgs_GitCeiling below exercises the merge through cmuxArgs
// directly, but nothing else proves OpenCmux reads the real environment and
// passes it on.
func TestOpenCmux_GitCeilingInherited(t *testing.T) {
	argsFile := fakeCmux(t, "echo OK workspace:7")
	t.Setenv("GIT_CEILING_DIRECTORIES", "/inherited/a:/inherited/b")

	var out, errOut bytes.Buffer
	require.NoError(t, OpenCmux(t.Context(), target, &out, &errOut))

	recorded, err := os.ReadFile(argsFile)
	require.NoError(t, err)

	args := strings.Split(strings.TrimSuffix(string(recorded), "\n"), "\n")
	require.Len(t, args, 9)

	var got layoutNode
	require.NoError(t, json.Unmarshal([]byte(args[8]), &got))

	left := got.Children[0].Pane.Surfaces[0]
	assert.Equal(t,
		map[string]string{gitCeilingVar: target.EnvsCeilingDir + ":/inherited/a:/inherited/b"},
		left.Env, "OpenCmux must pass what it inherited on to cmuxArgs, not drop it")
}

//nolint:paralleltest // t.Setenv forbids it
func TestOpenCmux_Failure(t *testing.T) {
	fakeCmux(t, "echo 'socket refused' >&2\nexit 3")

	var out, errOut bytes.Buffer

	err := OpenCmux(t.Context(), target, &out, &errOut)
	require.Error(t, err)

	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, 3, exit.ExitCode())
	assert.Equal(t, "socket refused\n", errOut.String(), "cmux's diagnostics reach the user")
}

func TestOpenCmux_Missing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	err := OpenCmux(t.Context(), target, &bytes.Buffer{}, &bytes.Buffer{})
	require.ErrorIs(t, err, ErrCmuxMissing)
}

// TestCmuxArgs_GitCeiling drives cmuxArgs directly, bypassing fakeCmux, since
// what varies here is the merge between the target's ceiling directory and
// whatever cmuxArgs is told canga's own process inherited.
func TestCmuxArgs_GitCeiling(t *testing.T) {
	t.Parallel()

	const ceiling = "/envs"

	tests := []struct {
		name       string
		ceilingDir string
		inherited  string
		wantEnv    map[string]string
	}{
		{
			name: "no inherited value", ceilingDir: ceiling, inherited: "",
			wantEnv: map[string]string{gitCeilingVar: ceiling},
		},
		{
			name: "inherited value", ceilingDir: ceiling, inherited: "/a:/b",
			wantEnv: map[string]string{gitCeilingVar: ceiling + ":/a:/b"},
		},
		{
			name: "inherited already contains the ceiling", ceilingDir: ceiling,
			inherited: "/a:" + ceiling + ":/b",
			wantEnv:   map[string]string{gitCeilingVar: "/a:" + ceiling + ":/b"},
		},
		{
			// Resolve always fills EnvsCeilingDir, but cmuxArgs must not turn
			// a zero-value Target into a nonsensical empty ceiling entry.
			name: "no ceiling directory", ceilingDir: "", inherited: "/a", wantEnv: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tgt := Target{
				Name: "acme/widget", EnvDir: "/envs/github.com/acme/widget-env",
				EnvsCeilingDir: tt.ceilingDir, RepoDir: "/src/github.com/acme/widget",
			}

			args, err := cmuxArgs(tgt, tt.inherited)
			require.NoError(t, err)

			var got layoutNode
			require.NoError(t, json.Unmarshal([]byte(args[8]), &got))

			left := got.Children[0].Pane.Surfaces[0]
			assert.Equal(t, tt.wantEnv, left.Env)

			right := got.Children[1].Pane.Surfaces[0]
			assert.Nil(t, right.Env, "the repository pane never gets a ceiling")

			wantEnvKeys := 0
			if tt.wantEnv != nil {
				wantEnvKeys = 1
			}

			assert.Equal(t, wantEnvKeys, strings.Count(args[8], `"env":`),
				"omitempty must drop the key entirely, on both surfaces, when there is nothing to set")
		})
	}
}

// TestGitCeilingEnv exercises the merge function directly, including git's
// empty-entry marker: an inherited ceiling occurring only after it is not
// resolved for symlinks the way ours is, so it must not count as already
// present.
func TestGitCeilingEnv(t *testing.T) {
	t.Parallel()

	const ceiling = "/envs"

	tests := []struct {
		name      string
		inherited string
		want      string
	}{
		{name: "no inherited value", inherited: "", want: ceiling},
		{name: "inherited value without the ceiling", inherited: "/a:/b", want: ceiling + ":/a:/b"},
		{
			name:      "ceiling already present before any marker",
			inherited: "/a:" + ceiling + ":/b", want: "/a:" + ceiling + ":/b",
		},
		{
			name:      "ceiling present with a trailing slash still counts as present",
			inherited: ceiling + "/", want: ceiling + "/",
		},
		{
			name:      "ceiling present only after the marker does not count",
			inherited: ":" + ceiling, want: ceiling + "::" + ceiling,
		},
		{
			name:      "ceiling right before the marker still counts",
			inherited: ceiling + ":", want: ceiling + ":",
		},
		{
			name:      "ceiling before the marker, more entries after",
			inherited: ceiling + "::/x", want: ceiling + "::/x",
		},
		{
			name:      "leading marker, unrelated entry after it",
			inherited: ":/a", want: ceiling + "::/a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, gitCeilingEnv(ceiling, tt.inherited))
		})
	}
}
