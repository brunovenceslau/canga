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
// would get wrong.
var target = Target{
	Name:    "acme/widget",
	EnvDir:  `/envs/github.com/acme/wid"get\`,
	RepoDir: "/src/github.com/acme/widget",
}

//nolint:paralleltest // t.Setenv forbids it
func TestOpenCmux(t *testing.T) {
	argsFile := fakeCmux(t, "echo OK workspace:7")

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
				{Type: "terminal", Cwd: target.EnvDir, Command: "sbx env run --clone"},
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
	} {
		assert.Contains(t, args[8], key)
	}
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
