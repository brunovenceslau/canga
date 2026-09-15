// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"testing"

	"github.com/brunovenceslau/devctl/internal/upgrade"
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

	resolved := upgrade.Result{Current: "v0.1.0", Release: "v0.2.0", Path: "/opt/bin/devctl", Newer: true}

	// --check has to name the file it would replace on BOTH branches. The
	// README promises it, and it used to appear only when nothing was newer —
	// which is the case where it matters least.
	t.Run("check names the path whether or not something is newer", func(t *testing.T) {
		t.Parallel()

		_, stderr := report(t, upgrade.Options{Check: true}, resolved, nil)
		assert.Contains(t, stderr, "v0.2.0 is available")
		assert.Contains(t, stderr, "it would replace /opt/bin/devctl")

		uptodate := resolved
		uptodate.Newer = false

		_, stderr = report(t, upgrade.Options{Check: true}, uptodate, nil)
		assert.Contains(t, stderr, "newest release")
		assert.Contains(t, stderr, "it would replace /opt/bin/devctl")
	})

	t.Run("the tag is the only thing on stdout", func(t *testing.T) {
		t.Parallel()

		installed := resolved
		installed.Installed = true

		stdout, stderr := report(t, upgrade.Options{}, installed, nil)
		assert.Equal(t, "v0.2.0\n", stdout, "`v=$(devctl upgrade)` has to be the version")
		assert.Contains(t, stderr, "/opt/bin/devctl")
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
		assert.Contains(t, stderr, "stopped while installing v0.2.0 over /opt/bin/devctl")
	})
}
