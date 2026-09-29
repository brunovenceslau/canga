// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package install_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The README's by-hand install blocks are the third copy of the checksum
// check install_host.sh and install_sandbox.sh make, and nothing else ties
// them together. This test pastes each block into a real shell, unchanged,
// against a fake curl serving a fake release and a fake tar that only records
// that it ran: extraction must happen on a match and on nothing else.

// readmeFences returns the body of the sh fence in README.md that downloads
// a canga-host_ archive and of the one that downloads a canga-sandbox_ one,
// each verified against checksums.txt. They are selected by that name, not by
// their order in the file.
func readmeFences(t *testing.T) (host, sandbox string) {
	t.Helper()

	raw, err := os.ReadFile("README.md")
	require.NoError(t, err)

	var (
		body []string
		in   bool
	)

	for l := range strings.SplitSeq(string(raw), "\n") {
		l = strings.TrimRight(l, "\r")

		switch {
		case !in && l == "```sh":
			in, body = true, nil
		case in && l == "```":
			in = false

			text := strings.Join(body, "\n")
			if !strings.Contains(text, "checksums.txt") {
				continue
			}

			switch {
			case strings.Contains(text, "canga-host_"):
				require.Empty(t, host, "two host blocks in README.md")

				host = text
			case strings.Contains(text, "canga-sandbox_"):
				require.Empty(t, sandbox, "two sandbox blocks in README.md")

				sandbox = text
			}
		case in:
			body = append(body, l)
		}
	}

	return host, sandbox
}

// A block is pasted into an interactive shell too, so it must stay clear of
// what only an interactive zsh or bash rewrites: history expansion on "!", and
// a "#" comment that some interactive shells read as part of the command.
var _comment = regexp.MustCompile(`(^|[ \t])#`)

func assertPasteSafe(t *testing.T, name, fence string) {
	t.Helper()

	assert.NotContains(t, fence, "!", "the %s block holds a '!', which interactive shells expand", name)

	for l := range strings.SplitSeq(fence, "\n") {
		assert.NotRegexp(t, _comment, l, "the %s block holds a comment on a command line: %s", name, l)
	}
}

const (
	_badShape = "not a lowercase sha256"
	_listed   = "@HASH@  @ASSET@\n"
)

const (
	// The fake curl fails on the file named in FAKE_CURL_FAIL, writes nothing
	// for the one named in FAKE_CURL_SKIP yet exits 0, and refuses to
	// overwrite a file that already exists, as a block that never left the
	// caller's directory would otherwise clobber the planted pair there.
	_readmeCurl = `#!/bin/sh
# The tag lookup: -w prints the effective URL of /releases/latest.
for a in "$@"; do
	if [ "$a" = -w ]; then echo "https://github.com/brunovenceslau/canga/releases/tag/v0.10.5"; exit 0; fi
done
for last; do :; done
name=${last##*/}
if [ "$name" = "${FAKE_CURL_FAIL:-}" ]; then echo "fake curl: 22 for $name" >&2; exit 22; fi
if [ "$name" = "${FAKE_CURL_SKIP:-}" ]; then exit 0; fi
if [ -e "$name" ]; then echo "fake curl: $name already exists in $(pwd)" >&2; exit 23; fi
cat "$FAKE_RELEASE/$name" >"$name"
`
	// The fake tar records where it ran and what it was asked to do.
	_readmeTar = `#!/bin/sh
echo "cwd $(pwd -P)" >>"$FAKE_TAR_LOG"
echo "$@" >>"$FAKE_TAR_LOG"
`
)

// A shell the block is pasted into: through -c, or through stdin into an
// interactive shell, which is how a person pastes it.
type readmeShell struct {
	argv  []string
	stdin bool
}

// fenceCase is one release the fake curl serves to a block.
type fenceCase struct {
	checksums string
	failName  string // the file the fake curl fails to download
	skipName  string // the file the fake curl claims to download but never writes
}

type fenceRun struct {
	code      int
	out       string
	extracted bool
}

var _rc = regexp.MustCompile(`(?m)(?:^|\s)rc=(\d+)\s*$`)

// runFence runs one README block under shell, in a scratch working directory
// that holds a decoy archive and checksums.txt the block must not trust.
func runFence(t *testing.T, shell readmeShell, fence, asset string, c fenceCase) fenceRun {
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
	// perl is linked because macOS shasum is a perl script.
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
	hash := hex.EncodeToString(sum[:])

	checksums := strings.NewReplacer("@HASH@", hash, "@UPPER@", strings.ToUpper(hash), "@ASSET@", asset).Replace(c.checksums)

	require.NoError(t, os.WriteFile(filepath.Join(release, asset), archive, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(release, "checksums.txt"), []byte(checksums), 0o644))

	// A planted pair in the working directory, with a checksum that matches:
	// a block that verified what was lying there would extract it.
	require.NoError(t, os.WriteFile(filepath.Join(work, asset), []byte("planted\n"), 0o644))

	planted := sha256.Sum256([]byte("planted\n"))
	require.NoError(t, os.WriteFile(filepath.Join(work, "checksums.txt"),
		[]byte(hex.EncodeToString(planted[:])+"  "+asset+"\n"), 0o644))

	tarLog := filepath.Join(root, "tar.log")

	// rc is the block's own status; "alive" proves a refusal leaves the
	// calling shell running.
	script := fence + "\nrc=$?\necho \"rc=$rc\"\necho alive\n"
	args := shell.argv[1:]

	if !strings.HasSuffix(script, "\n") {
		script += "\n"
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	var cmd *exec.Cmd

	if shell.stdin {
		cmd = exec.CommandContext(ctx, shell.argv[0], args...)
		cmd.Stdin = strings.NewReader("PROMPT=''\nPS2=''\nRPROMPT=''\n" + script + "exit\n")
	} else {
		cmd = exec.CommandContext(ctx, shell.argv[0], append(args, "-c", script)...)
	}

	cmd.WaitDelay = 5 * time.Second
	cmd.Dir = work
	cmd.Env = []string{"PATH=" + bin, "HOME=" + home, "TMPDIR=" + root, "FAKE_RELEASE=" + release, "FAKE_TAR_LOG=" + tarLog}

	if c.failName != "" {
		cmd.Env = append(cmd.Env, "FAKE_CURL_FAIL="+strings.ReplaceAll(c.failName, "@ASSET@", asset))
	}

	if c.skipName != "" {
		cmd.Env = append(cmd.Env, "FAKE_CURL_SKIP="+strings.ReplaceAll(c.skipName, "@ASSET@", asset))
	}

	var out bytes.Buffer

	cmd.Stdout, cmd.Stderr = &out, &out

	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError

		require.ErrorAs(t, err, &ee, out.String())
	}

	m := _rc.FindStringSubmatch(out.String())
	require.NotNil(t, m, "the block's status was not printed: %s", out.String())

	code, err := strconv.Atoi(m[1])
	require.NoError(t, err)

	logged, _ := os.ReadFile(tarLog)
	extracted := strings.Contains(string(logged), "-xzf")

	assert.Equal(t, extracted, strings.Contains(string(logged), asset), "tar was handed another file")

	if extracted {
		// The block must have left the caller's directory for its own.
		realRoot, err := filepath.EvalSymlinks(root)
		require.NoError(t, err)
		assert.Contains(t, string(logged), "cwd "+filepath.Join(realRoot, "tmp."), "tar ran outside the block's temporary directory")
	}

	// The temporary directory the block made must be gone.
	left, err := filepath.Glob(filepath.Join(root, "tmp.*"))
	require.NoError(t, err)
	assert.Empty(t, left, "the block left its temporary directory behind")

	return fenceRun{code: code, out: out.String(), extracted: extracted}
}

func TestReadmeInstallBlocks(t *testing.T) {
	t.Parallel()

	host, sandbox := readmeFences(t)
	require.NotEmpty(t, host, "README.md should hold the host by-hand block")
	require.NotEmpty(t, sandbox, "README.md should hold the sandbox by-hand block")

	// Each block runs on the system its archive is published for: the host
	// block with the hasher of a Mac, the sandbox block with that of Linux.
	// The other is checked for paste safety only.
	blocks := []struct {
		name, asset, hasher, goos string
		fence                     string
	}{
		{"host", "canga-host_0.10.5_darwin_arm64.tar.gz", "shasum", "darwin", host},
		{"sandbox", "canga-sandbox_X.Y.Z_linux_arm64.tar.gz", "sha256sum", "linux", sandbox},
	}

	cases := []struct {
		name    string
		c       fenceCase
		wantOK  bool
		wantMsg string
		wantNot string
	}{
		{"match", fenceCase{checksums: _listed}, true, "", ""},
		{"match among other lines", fenceCase{checksums: "0000  other.tar.gz\n@HASH@  @ASSET@\n"}, true, "", ""},
		{"match beside a longer name", fenceCase{checksums: "0000  x-@ASSET@\n@HASH@  @ASSET@\n@HASH@  @ASSET@.sig\n"}, true, "", ""},
		{"only a longer name", fenceCase{checksums: "@HASH@  @ASSET@.sig\n@HASH@  x-@ASSET@\n"}, false, "0 times", ""},
		{"mismatch", fenceCase{checksums: strings.Repeat("0", 64) + "  @ASSET@\n"}, false, "but checksums.txt lists", ""},
		{"duplicate", fenceCase{checksums: "@HASH@  @ASSET@\n@HASH@  @ASSET@\n"}, false, "2 times", ""},
		{"missing", fenceCase{checksums: "@HASH@  other.tar.gz\n"}, false, "0 times", ""},
		{"empty checksums", fenceCase{}, false, "0 times", ""},
		{"not hex", fenceCase{checksums: "zzzz  @ASSET@\n"}, false, _badShape, ""},
		{"short hash", fenceCase{checksums: "abc  @ASSET@\n"}, false, _badShape, ""},
		{"long hash", fenceCase{checksums: "@HASH@0  @ASSET@\n"}, false, _badShape, ""},
		{"uppercase", fenceCase{checksums: "@UPPER@  @ASSET@\n"}, false, _badShape, ""},
		{"one space", fenceCase{checksums: "@HASH@ @ASSET@\n"}, false, _badShape, ""},
		{"the archive fails to download", fenceCase{checksums: _listed, failName: "@ASSET@"}, false, "fake curl: 22", "sha256"},
		{"checksums.txt fails to download", fenceCase{checksums: _listed, failName: "checksums.txt"}, false, "fake curl: 22", "times"},
		{"an archive that is never written", fenceCase{checksums: _listed, skipName: "@ASSET@"}, false, "could not compute the sha256", ""},
	}

	dash := ""
	if p, err := exec.LookPath("dash"); err == nil {
		dash = p
	}

	// bash is required, and so is the OS's own hasher: a run with either
	// missing would prove nothing.
	bash, err := exec.LookPath("bash")
	require.NoError(t, err, "bash is required to run the README blocks")

	shellSet := map[string]readmeShell{"sh": {argv: shells(t)["sh"]}, "bash-posix": {argv: []string{bash, "--posix"}}, "bash": {argv: []string{bash}}}
	if dash != "" {
		shellSet["dash"] = readmeShell{argv: []string{dash}}
	}

	if zsh, err := exec.LookPath("zsh"); err == nil {
		shellSet["zsh-interactive"] = readmeShell{argv: []string{zsh, "-fi"}, stdin: true}
	}

	ran := 0

	t.Run("blocks", func(t *testing.T) {
		for _, b := range blocks {
			assertPasteSafe(t, b.name, b.fence)

			if runtime.GOOS != b.goos {
				t.Run(b.name, func(t *testing.T) {
					t.Parallel()
					t.Skipf("the %s block is for %s and this is %s", b.name, b.goos, runtime.GOOS)
				})

				continue
			}

			_, err := exec.LookPath(b.hasher)
			require.NoError(t, err, "%s is required to run the %s block", b.hasher, b.name)

			for _, missing := range []string{"dash", "zsh-interactive"} {
				if _, ok := shellSet[missing]; !ok {
					t.Run(b.name+"/"+missing, func(t *testing.T) {
						t.Parallel()
						t.Skipf("%s is not installed", missing)
					})
				}
			}

			for shellName, shell := range shellSet {
				for _, c := range cases {
					ran++

					t.Run(b.name+"/"+shellName+"/"+c.name, func(t *testing.T) {
						t.Parallel()

						res := runFence(t, shell, b.fence, b.asset, c.c)

						assert.Contains(t, res.out, "alive", "the calling shell did not survive: %s", res.out)
						assert.Equal(t, c.wantOK, res.extracted, res.out)

						if c.wantOK {
							assert.Zero(t, res.code, res.out)
						} else {
							assert.NotZero(t, res.code, res.out)
						}

						if c.wantMsg != "" {
							assert.Contains(t, res.out, c.wantMsg)
						}

						if c.wantNot != "" {
							assert.NotContains(t, res.out, c.wantNot)
						}
					})
				}
			}
		}
	})

	assert.Positive(t, ran, "no README block ran on %s", runtime.GOOS)
}
