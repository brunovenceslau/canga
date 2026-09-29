// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package install_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The README's by-hand install blocks are the third copy of the checksum
// check install_host.sh and install_sandbox.sh make, and nothing else ties
// them together. This test pastes each block into a real shell, unchanged,
// against a fake curl serving a fake release and a fake tar that only records
// that it ran: extraction must happen on a match and on nothing else.

// readmeFences returns the body of every sh fence in README.md that verifies
// a download against checksums.txt.
func readmeFences(t *testing.T) []string {
	t.Helper()

	raw, err := os.ReadFile("README.md")
	require.NoError(t, err)

	var (
		fences []string
		body   []string
		in     bool
	)

	for l := range strings.SplitSeq(string(raw), "\n") {
		switch {
		case !in && l == "```sh":
			in, body = true, nil
		case in && l == "```":
			in = false

			if text := strings.Join(body, "\n"); strings.Contains(text, "checksums.txt") {
				fences = append(fences, text)
			}
		case in:
			body = append(body, l)
		}
	}

	return fences
}

const _badShape = "not a lowercase sha256"

const (
	_readmeCurl = `#!/bin/sh
# The tag lookup: -w prints the effective URL of /releases/latest.
for a in "$@"; do
	if [ "$a" = -w ]; then echo "https://github.com/brunovenceslau/canga/releases/tag/v0.10.5"; exit 0; fi
done
[ -z "${FAKE_CURL_FAIL:-}" ] || exit 22
for last; do :; done
name=${last##*/}
cat "$FAKE_RELEASE/$name" >"$name"
`
	_readmeTar = `#!/bin/sh
echo "$@" >>"$FAKE_TAR_LOG"
`
)

// runFence runs one README block under shell, in a scratch working directory
// that holds a decoy archive and checksums.txt the block must not trust.
func runFence(t *testing.T, shell []string, fence, asset, checksums string, curlFails bool) (int, string, bool) {
	t.Helper()

	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	release := filepath.Join(root, "release")
	work := filepath.Join(root, "work")
	home := filepath.Join(root, "home")

	for _, d := range []string{bin, release, work, home} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}

	// The tools the block runs come from the real system; curl and tar do not.
	for _, tool := range []string{"awk", "grep", "printf", "basename", "mktemp", "rm", "mkdir", "cat", "sha256sum", "shasum", "perl", "dirname", "uname", "env"} {
		if p, err := exec.LookPath(tool); err == nil {
			require.NoError(t, os.Symlink(p, filepath.Join(bin, tool)))
		}
	}

	for name, body := range map[string]string{"curl": _readmeCurl, "tar": _readmeTar} {
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755))
	}

	archive := []byte("the archive\n")
	sum := sha256.Sum256(archive)

	require.NoError(t, os.WriteFile(filepath.Join(release, asset), archive, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(release, "checksums.txt"),
		[]byte(strings.ReplaceAll(strings.ReplaceAll(checksums, "@HASH@", hex.EncodeToString(sum[:])), "@ASSET@", asset)), 0o644))

	// A planted pair in the working directory, with a checksum that matches:
	// a block that verified what was lying there would extract it.
	require.NoError(t, os.WriteFile(filepath.Join(work, asset), []byte("planted\n"), 0o644))

	planted := sha256.Sum256([]byte("planted\n"))
	require.NoError(t, os.WriteFile(filepath.Join(work, "checksums.txt"),
		[]byte(hex.EncodeToString(planted[:])+"  "+asset+"\n"), 0o644))

	tarLog := filepath.Join(root, "tar.log")

	// "; echo alive" proves a refusal leaves the calling shell running.
	cmd := exec.CommandContext(t.Context(), shell[0], append(shell[1:], "-c", fence+"\necho alive")...)
	cmd.Dir = work
	cmd.Env = []string{"PATH=" + bin, "HOME=" + home, "TMPDIR=" + root, "FAKE_RELEASE=" + release, "FAKE_TAR_LOG=" + tarLog}

	if curlFails {
		cmd.Env = append(cmd.Env, "FAKE_CURL_FAIL=1")
	}

	var out bytes.Buffer

	cmd.Stdout, cmd.Stderr = &out, &out

	err := cmd.Run()
	code := 0

	if ee, ok := err.(*exec.ExitError); ok { //nolint:errorlint // an ExitError is never wrapped here
		code = ee.ExitCode()
	} else {
		require.NoError(t, err)
	}

	logged, _ := os.ReadFile(tarLog)

	// The temporary directory the block made must be gone.
	left, err := filepath.Glob(filepath.Join(root, "tmp.*"))
	require.NoError(t, err)
	assert.Empty(t, left, "the block left its temporary directory behind")

	extracted := strings.Contains(string(logged), "-xzf")
	assert.Equal(t, extracted, strings.Contains(string(logged), asset), "tar was handed another file")

	return code, out.String(), extracted
}

func TestReadmeInstallBlocks(t *testing.T) {
	t.Parallel()

	fences := readmeFences(t)
	require.Len(t, fences, 2, "README.md should hold the host and the sandbox by-hand blocks")

	blocks := []struct {
		name, asset, hasher string
		fence               string
	}{
		{"host", "canga-host_0.10.5_darwin_arm64.tar.gz", "shasum", fences[0]},
		{"sandbox", "canga-sandbox_X.Y.Z_linux_arm64.tar.gz", "sha256sum", fences[1]},
	}

	cases := []struct {
		name      string
		checksums string
		curlFails bool
		wantOK    bool
		wantMsg   string
	}{
		{"match", "@HASH@  @ASSET@\n", false, true, ""},
		{"match among other lines", "0000  other.tar.gz\n@HASH@  @ASSET@\n", false, true, ""},
		{"mismatch", strings.Repeat("0", 64) + "  @ASSET@\n", false, false, "but checksums.txt lists"},
		{"duplicate", "@HASH@  @ASSET@\n@HASH@  @ASSET@\n", false, false, "2 times"},
		{"missing", "@HASH@  other.tar.gz\n", false, false, "0 times"},
		{"empty checksums", "", false, false, "0 times"},
		{"not hex", "zzzz  @ASSET@\n", false, false, _badShape},
		{"short hash", "abc  @ASSET@\n", false, false, _badShape},
		{"uppercase", "@UPPER@  @ASSET@\n", false, false, _badShape},
		{"one space", "@HASH@ @ASSET@\n", false, false, _badShape},
		{"a download that fails", "@HASH@  @ASSET@\n", true, false, ""},
	}

	for _, b := range blocks {
		if _, err := exec.LookPath(b.hasher); err != nil {
			t.Logf("%s is not installed, skipping the %s block", b.hasher, b.name)

			continue
		}

		for shellName, shell := range readmeShells(t) {
			for _, c := range cases {
				t.Run(b.name+"/"+shellName+"/"+c.name, func(t *testing.T) {
					t.Parallel()

					checksums := c.checksums
					if strings.Contains(checksums, "@UPPER@") {
						sum := sha256.Sum256([]byte("the archive\n"))
						checksums = strings.ReplaceAll(checksums, "@UPPER@", strings.ToUpper(hex.EncodeToString(sum[:])))
					}

					code, out, extracted := runFence(t, shell, b.fence, b.asset, checksums, c.curlFails)

					assert.Contains(t, out, "alive", "the calling shell did not survive: %s", out)
					assert.Equal(t, c.wantOK, extracted, out)

					if c.wantOK {
						assert.Zero(t, code, out)
					} else if c.wantMsg != "" {
						assert.Contains(t, out, c.wantMsg)
					}
				})
			}
		}
	}
}

// readmeShells are the shells the block is pasted into: sh, bash, bash in
// POSIX mode and dash where each exists.
func readmeShells(t *testing.T) map[string][]string {
	t.Helper()

	found := shells(t)

	for _, name := range []string{"bash", "dash"} {
		if p, err := exec.LookPath(name); err == nil {
			found[name+"-plain"] = []string{p}
		}
	}

	return found
}
