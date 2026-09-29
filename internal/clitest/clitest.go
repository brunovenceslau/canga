// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package clitest holds the help-text checks both builds' command tests share.
// It is imported only by tests.
//
// It exists for the same reason as internal/testrepo: cmd/host/canga and
// cmd/sandbox/canga are separate packages, and a helper in a _test.go file
// cannot be shared across them.
package clitest

import (
	"strings"

	"github.com/spf13/cobra"
)

// CommandsWithoutExample walks root's tree and returns the path of every
// visible leaf command with no Example.
//
// Pass a tree that has not been executed. cobra adds its own help and
// completion commands only when a command runs (InitDefaultHelpCmd and
// InitDefaultCompletionCmd, called from Execute), so an unexecuted tree holds
// only the program's own commands, and nothing is excluded by name: a command
// the program defines called "help" or "completion" is checked like any
// other. Hidden commands are skipped, since help never shows them.
func CommandsWithoutExample(root *cobra.Command) []string {
	var missing []string

	for _, sub := range root.Commands() {
		if sub.Hidden {
			continue
		}

		if sub.HasAvailableSubCommands() {
			missing = append(missing, CommandsWithoutExample(sub)...)

			continue
		}

		if strings.TrimSpace(sub.Example) == "" {
			missing = append(missing, sub.CommandPath())
		}
	}

	return missing
}

// Flatten joins text's words with single spaces, so a test can look for a
// phrase in help text without depending on where it wraps.
func Flatten(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
