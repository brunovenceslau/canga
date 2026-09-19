// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
)

// ErrCmuxMissing reports a cmux binary that is not on PATH.
var ErrCmuxMissing = errors.New("cmux is not installed or not on PATH")

// envRunCommand starts the repository's sandbox from the environment pane.
//
// cmux types a surface's command into that pane's shell, followed by a newline
// (dequeueInitialTerminalInput and sendInputWhenReady in its
// Sources/Workspace+CustomLayout.swift, 0.64.25), rather than running it as the
// pane's process. So the pane outlives the sandbox: when sbx exits, the shell
// is still there, in the environment directory, for the next run.
const envRunCommand = "sbx env run --clone"

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
		// Command is typed into the terminal once its shell is ready.
		Command string `json:"command,omitempty"`
		Focus   bool   `json:"focus,omitempty"`
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
	args, err := cmuxArgs(t)
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
func cmuxArgs(t Target) ([]string, error) {
	layout := layoutNode{
		Direction: "horizontal",
		Children: []layoutNode{
			{Pane: &layoutPane{Surfaces: []layoutSurface{
				{Type: "terminal", Cwd: t.EnvDir, Command: envRunCommand},
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
