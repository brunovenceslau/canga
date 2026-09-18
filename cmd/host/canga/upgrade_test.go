// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"testing"

	"github.com/brunovenceslau/canga/internal/cli"
	"github.com/brunovenceslau/canga/internal/upgrade"
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
	assert.Equal(t, cli.ExitUsage, exitCode(err),
		"the fix is to pass --tag, so this is a bad invocation rather than a runtime failure")
}

func TestUpgradeRefusesATagThatIsNotAVersion(t *testing.T) {
	t.Setenv("GH_TOKEN", "token")

	_, err := execute(t, upgradeCmd, "--tag", "main")
	require.ErrorIs(t, err, upgrade.ErrBadTag)
	assert.Equal(t, cli.ExitUsage, exitCode(err))
}

// A machine with neither a token in the environment nor gh installed is no
// longer refused: the repository is public, so the run proceeds anonymously and
// stops at the next real question, which for this test binary is that `dev` is
// not a release. Nothing here reaches the network.
func TestUpgradeWithoutAToken(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	_, err := execute(t, upgradeCmd)
	require.ErrorIs(t, err, upgrade.ErrNotRelease)
	assert.NotContains(t, err.Error(), "token",
		"a missing credential is no longer a reason to refuse")
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
			assert.Equal(t, cli.ExitUsage, exitCode(err))
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
	assert.Contains(t, out, "release's host build", "the host must upgrade into the host build")
}
