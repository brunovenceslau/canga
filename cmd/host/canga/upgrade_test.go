// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"testing"

	"github.com/brunovenceslau/canga/internal/cli"
	"github.com/brunovenceslau/canga/internal/upgrade"
	"github.com/spf13/cobra"
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
}

// report drives reportUpgrade alone, which is the only way to see its output:
// every path through it needs a resolved release, and the cmd tests cannot
// point the client at a fake GitHub.
func report(t *testing.T, options upgrade.Options, result upgrade.Result, failure error) (stdout, stderr string) {
	t.Helper()

	var out, errOut bytes.Buffer

	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)

	reportUpgrade(cmd, options, result, failure)

	return out.String(), errOut.String()
}

func TestReportUpgrade(t *testing.T) {
	t.Parallel()

	resolved := upgrade.Result{Current: "v0.1.0", Release: "v0.2.0", Path: "/opt/bin/canga", Newer: true}

	// --check has to name the file it would replace on BOTH branches. The
	// README promises it, and it used to appear only when nothing was newer —
	// which is the case where it matters least.
	t.Run("check names the path whether or not something is newer", func(t *testing.T) {
		t.Parallel()

		_, stderr := report(t, upgrade.Options{Check: true}, resolved, nil)
		assert.Contains(t, stderr, "v0.2.0 is available")
		assert.Contains(t, stderr, "it would replace /opt/bin/canga")

		uptodate := resolved
		uptodate.Newer = false

		_, stderr = report(t, upgrade.Options{Check: true}, uptodate, nil)
		assert.Contains(t, stderr, "newest release")
		assert.Contains(t, stderr, "it would replace /opt/bin/canga")
	})

	t.Run("the tag is the only thing on stdout", func(t *testing.T) {
		t.Parallel()

		installed := resolved
		installed.Installed = true

		stdout, stderr := report(t, upgrade.Options{}, installed, nil)
		assert.Equal(t, "v0.2.0\n", stdout, "`v=$(canga upgrade)` has to be the version")
		assert.Contains(t, stderr, "/opt/bin/canga")
	})

	// A run that failed before resolving anything has nothing to add to the
	// error main is about to print.
	t.Run("nothing resolved, nothing said", func(t *testing.T) {
		t.Parallel()

		stdout, stderr := report(t, upgrade.Options{}, upgrade.Result{}, assert.AnError)
		assert.Empty(t, stdout)
		assert.Empty(t, stderr)
	})

	// A failure mid-install must not print a version on stdout: nothing was
	// installed, and a caller reading stdout would record that it was.
	t.Run("a failed install says how far it got and prints no version", func(t *testing.T) {
		t.Parallel()

		stdout, stderr := report(t, upgrade.Options{}, resolved, assert.AnError)
		assert.Empty(t, stdout)
		assert.Contains(t, stderr, "stopped while installing v0.2.0 over /opt/bin/canga")
	})
}
