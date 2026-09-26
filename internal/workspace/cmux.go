// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// ErrCmuxMissing reports a cmux binary that is not on PATH.
var ErrCmuxMissing = errors.New("cmux is not installed or not on PATH")

// gitCeilingVar is the variable git itself reads: a colon-separated list of
// absolute directories it refuses to walk up past while looking for a
// repository. See Target.EnvsCeilingDir for why the environment pane sets it.
const gitCeilingVar = "GIT_CEILING_DIRECTORIES"

// envRunCommand starts the repository's sandbox from the environment pane.
//
// cmux types a surface's command into that pane's shell, followed by a newline
// (dequeueInitialTerminalInput and sendInputWhenReady in its
// Sources/Workspace+CustomLayout.swift, 0.64.25), rather than running it as the
// pane's process. So the pane outlives the sandbox: when sbx exits, the shell
// is still there, in the environment directory, for the next run.
//
// --auto-approve is there because the workspace opens focused on the clone
// pane, not this one: a confirmation prompt left in an unfocused pane would
// sit unseen and the sandbox would never start. sbx env run --help (the
// command is marked EXPERIMENTAL there) describes the flag as "-y,
// --auto-approve   Apply the environment plan without asking".
const envRunCommand = "sbx env run --clone --auto-approve"

// The layout document `cmux new-workspace --layout` takes, verified against
// cmux 0.64.23 and 0.64.25 (CmuxLayoutNode and CmuxSurfaceDefinition in its
// Sources/CmuxConfig.swift). A node is either a split or a pane, never both,
// which is why every field here is omitempty.
type (
	layoutNode struct {
		// "horizontal" puts the children side by side, the first on the left.
		Direction string       `json:"direction,omitempty"`
		Children  []layoutNode `json:"children,omitempty"`
		Pane      *layoutPane  `json:"pane,omitempty"`
	}

	layoutPane struct {
		Surfaces []layoutSurface `json:"surfaces"`
	}

	layoutSurface struct {
		Type string `json:"type"`
		// An absolute cwd is used as is; cmux resolves a relative one against
		// the workspace's --cwd.
		Cwd string `json:"cwd"`
		// Command is typed into the terminal once cmux has created it. cmux
		// waits a few seconds for that (its debug log says 3s) and drops the
		// command, silently in release builds, if the terminal is still missing.
		Command string `json:"command,omitempty"`
		// Env is set in the pty's environment before the shell starts, not
		// typed like Command (CmuxSurfaceDefinition.env, Sources/CmuxConfig.swift;
		// passed on as startupEnvironment, Sources/Workspace+CustomLayout.swift:177,239
		// at v0.64.25). Only the environment pane's surface sets it.
		Env   map[string]string `json:"env,omitempty"`
		Focus bool              `json:"focus,omitempty"`
	}
)

// OpenCmux opens t as a new, focused cmux workspace: the environment on the
// left, running its sandbox, the clone on the right, and the cursor in the
// clone, where the work happens.
//
// cmux's own output is passed through: its stdout (the new workspace's ref) to
// out and its diagnostics to errOut. That includes its refusal when canga runs
// outside a cmux terminal, which cmux's default socket mode requires.
func OpenCmux(ctx context.Context, t Target, out, errOut io.Writer) error {
	args, err := cmuxArgs(t, os.Getenv(gitCeilingVar))
	if err != nil {
		return err
	}

	//nolint:gosec // the program name is a constant and every argument is passed
	// separately, so no shell ever parses a path or the layout.
	cmd := exec.CommandContext(ctx, "cmux", args...)
	cmd.Stdout = out
	cmd.Stderr = errOut

	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return ErrCmuxMissing
		}

		return fmt.Errorf("cmux new-workspace: %w", err)
	}

	return nil
}

// cmuxArgs builds the one `cmux new-workspace` invocation that opens t.
//
// The workspace's own --cwd is the clone, so a pane split off later starts
// there rather than in the home directory.
//
// inheritedCeiling is whatever canga's own environment already carries in
// gitCeilingVar, taken as a parameter (rather than read here from os.Environ)
// so the merge logic stays reachable from a table-driven test without
// t.Setenv.
func cmuxArgs(t Target, inheritedCeiling string) ([]string, error) {
	var envPaneEnv map[string]string
	if t.EnvsCeilingDir != "" {
		envPaneEnv = map[string]string{gitCeilingVar: gitCeilingEnv(t.EnvsCeilingDir, inheritedCeiling)}
	}

	layout := layoutNode{
		Direction: "horizontal",
		Children: []layoutNode{
			{Pane: &layoutPane{Surfaces: []layoutSurface{
				{Type: "terminal", Cwd: t.EnvDir, Command: envRunCommand, Env: envPaneEnv},
			}}},
			{Pane: &layoutPane{Surfaces: []layoutSurface{{Type: "terminal", Cwd: t.RepoDir, Focus: true}}}},
		},
	}

	// Marshalled, never formatted into a string: a path holding a quote or a
	// backslash would otherwise break the document, or change it.
	encoded, err := json.Marshal(layout)
	if err != nil {
		return nil, fmt.Errorf("encoding the cmux layout: %w", err)
	}

	return []string{
		"new-workspace",
		"--name", t.Name,
		"--cwd", t.RepoDir,
		"--focus", "true",
		"--layout", string(encoded),
	}, nil
}

// gitCeilingEnv is the value the environment pane's gitCeilingVar gets:
// ceiling, put ahead of whatever canga's own process already inherited in
// that variable, unless an entry before git's own empty-entry marker already
// equals ceiling. Inherited's own entries, and the marker's position among
// them, are never reordered or dropped; only ceiling is ever added, ahead of
// everything else.
//
// The marker matters because git only resolves symlinks for the entries
// before it: every entry after is compared to the current directory
// literally instead, an opt-out git(1) documents for slow or unreliable
// symlink resolution. So a ceiling that appears only after the marker was
// never meant to be resolved the way ours is, and does not count as already
// present; ceiling gets prepended anyway rather than relying on that entry.
func gitCeilingEnv(ceiling, inherited string) string {
	if inherited == "" {
		return ceiling
	}

	for entry := range strings.SplitSeq(inherited, ":") {
		if entry == "" {
			break
		}

		if strings.TrimRight(entry, "/") == ceiling {
			return inherited
		}
	}

	return ceiling + ":" + inherited
}
