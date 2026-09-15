// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"testing"

	"github.com/brunovenceslau/devctl/internal/upgrade"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// upgradeCmd is the subcommand every test below drives.
const upgradeCmd = "upgrade"

func TestUpgradeRefusesATestBuild(t *testing.T) {
	// A token so that the run gets past looking for one, and reaches the
	// decision being tested. Nothing is ever sent: a build reporting "dev" —
	// which is what every test binary reports — is refused before any request.
	t.Setenv("GH_TOKEN", "token")

	_, err := execute(t, upgradeCmd)
	require.ErrorIs(t, err, upgrade.ErrNotRelease)
	assert.Equal(t, exitUsage, exitCode(err),
		"the fix is to pass --tag, so this is a bad invocation rather than a runtime failure")
}

func TestUpgradeRefusesATagThatIsNotAVersion(t *testing.T) {
	t.Setenv("GH_TOKEN", "token")

	_, err := execute(t, upgradeCmd, "--tag", "main")
	require.ErrorIs(t, err, upgrade.ErrBadTag)
	assert.Equal(t, exitUsage, exitCode(err))
}

// The command reaches for a token before anything else, so a machine with
// neither the environment nor gh is told which of the two to fix.
//

func TestUpgradeWithoutAToken(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	_, err := execute(t, upgradeCmd)
	require.ErrorIs(t, err, upgrade.ErrNoToken)
	assert.Equal(t, exitFailure, exitCode(err),
		"a missing credential is a runtime failure, like a missing git, not a bad invocation")
}

func TestUpgradeUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "positional arguments", args: []string{upgradeCmd, "v0.2.0"}},
		{name: "unknown flag", args: []string{upgradeCmd, "--latest"}},
		{name: "tag with no value", args: []string{upgradeCmd, "--tag"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := execute(t, tt.args...)
			require.Error(t, err)
			assert.Equal(t, exitUsage, exitCode(err))
		})
	}
}

// TestUpgradeHelpSeparatesItFromDotfilesUpgrade keeps the one distinction that
// is easy to lose: dotfiles-upgrade fetches git and updates a checkout, and
// this replaces a binary. Two mechanisms that share a verb.
func TestUpgradeHelpSeparatesItFromDotfilesUpgrade(t *testing.T) {
	t.Parallel()

	out, err := execute(t, upgradeCmd, "--help")
	require.NoError(t, err)
	assert.Contains(t, out, "dotfiles-upgrade")
	assert.Contains(t, out, "checksums.txt")
}
