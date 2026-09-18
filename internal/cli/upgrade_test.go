// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"testing"

	"github.com/brunovenceslau/canga/internal/upgrade"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewUpgradeCmd_UsageErrors: the two refusals that are answered by naming
// a release on the command line exit 2 in BOTH builds, because the mapping
// lives in the shared command rather than in one binary's exit code table.
func TestNewUpgradeCmd_UsageErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version string
		args    []string
		wantErr error
	}{
		{name: "a build that is not a release", version: "dev", wantErr: upgrade.ErrNotRelease},
		{name: "a tag that is not a version", version: "v0.1.0", args: []string{"--tag", "main"}, wantErr: upgrade.ErrBadTag},
	}

	for _, role := range []string{upgrade.RoleHost, upgrade.RoleSandbox} {
		for _, tt := range tests {
			t.Run(role+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				cmd := NewUpgradeCmd(role, tt.version)
				cmd.SetArgs(tt.args)
				cmd.SetOut(&bytes.Buffer{})
				cmd.SetErr(&bytes.Buffer{})

				err := cmd.ExecuteContext(t.Context())
				require.ErrorIs(t, err, tt.wantErr)
				assert.Equal(t, ExitUsage, ExitCode(err))
			})
		}
	}
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
