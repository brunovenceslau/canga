// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package install_test runs install_host.sh and install_sandbox.sh, the real
// scripts under a real shell, against a release served by a fake curl.
//
// PATH holds only a throwaway bin directory: fakes for curl, uname and id, and
// symlinks to the real tools the scripts need. The real curl is therefore out
// of reach, and no case can touch the network.
package install_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// The release every fixture is, shared with sbxkit_test.go's own
	// TestSbxKit_Install (same newEnv, same fake release). Its archives
	// carry the version without the "v", as .goreleaser.yml names them.
	// Exactly the release floor (install_host.sh and install_sandbox.sh
	// refuse any tag below it), so a fixture below it would be refused by
	// that check alone, before any case here got to exercise anything else -
	// "pinned tag" and "newest release" below double as the "at the floor"
	// case.
	//
	// sbxkitpin_test.go, a different script entirely, has its own
	// unrelated fixture tags below v0.10.5 (_decoyTag among them) and does
	// not call newEnv, so it is unaffected by this value.
	_tag     = "v0.10.5"
	_version = "0.10.5"

	// The one URL prefix the fake curl serves. A script asking for anything
	// else fails the run, so a typo in a script's URL fails a test too.
	_releases = "https://github.com/brunovenceslau/canga/releases"

	// What a pre-seeded canga prints: it must still print it after a refused
	// install, and must be gone after a good one.
	_oldVersion = "OLD"

	_sandboxDirLine = "\tdir=/usr/local/bin\n"

	// Roles, and what the fake uname answers.
	_host    = "host"
	_linux   = "Linux"
	_darwin  = "Darwin"
	_amd64   = "amd64"
	_aarch64 = "aarch64"

	// The two checksum tools, a third bin directory whose sha256sum accepts
	// empty input (see _lenientSha256sum), and the three ways mangleChecksums
	// breaks checksums.txt.
	_sha256sum = "sha256sum"
	_shasum    = "shasum"
	_lenient   = "lenient sha256sum"
	_wrong     = "wrong"
	_absent    = "absent"
	_twice     = "twice"

	// The release floor's two refusal messages (install_host.sh,
	// install_sandbox.sh): below it, or not canonical vX.Y.Z at all.
	_wantBelowFloor   = "is older than v0.10.5"
	_wantNotCanonical = "is not vX.Y.Z"

	// Both installers' refusal of an asset checksums.txt lists zero times
	// or more than once.
	_wantNotOnce = "not exactly once"
)

// _fakeCurl serves $FAKE_RELEASES/<tag>/<file> for
// <releases>/download/<tag>/<file>. A missing file is a 404: with -f it exits
// 22 as real curl does, and without -f it saves the error page and exits 0,
// also as real curl does, so a script that drops -f fails a test. It answers <releases>/latest with the newest tag's
// URL ONLY when -L was passed, as real curl does: without -L the effective URL
// is the one asked for. That is the bug the README block had, and why dropping
// -L from install_host.sh fails the newest-release case. Every request is
// logged as "<followed redirects: 0|1> <url>".
const _fakeCurl = `#!/bin/sh
follow=0 fail=0 out= format= url=
while [ $# -gt 0 ]; do
	case "$1" in
	-o) out=$2; shift ;;
	-w) format=$2; shift ;;
	-*)
		case "$1" in *L*) follow=1 ;; esac
		case "$1" in *f*) fail=1 ;; esac
		;;
	*) url=$1 ;;
	esac
	shift
done
echo "$follow $url" >>"$FAKE_LOG"
case "$url" in
"$FAKE_BASE/latest")
	effective=$url
	if [ "$follow" = 1 ]; then effective="$FAKE_BASE/tag/$FAKE_LATEST"; fi
	if [ "$format" = '%{url_effective}' ]; then printf '%s' "$effective"; fi
	;;
"$FAKE_BASE/download/"*)
	file="$FAKE_RELEASES/${url#"$FAKE_BASE/download/"}"
	if [ -f "$file" ]; then cp "$file" "$out"; exit 0; fi
	if [ "$fail" = 1 ]; then echo "curl: (22) The requested URL returned error: 404" >&2; exit 22; fi
	echo "Not Found" >"$out"
	;;
*)
	echo "fake curl: unexpected URL $url" >&2
	exit 99
	;;
esac
`

const _fakeUname = `#!/bin/sh
case "$1" in
-s) echo "$FAKE_OS" ;;
-m) echo "$FAKE_ARCH" ;;
*) exit 1 ;;
esac
`

const _fakeID = `#!/bin/sh
[ "$1" = -u ] && echo "$FAKE_UID"
`

// _lenientSha256sum is a sha256sum that exits 0 when handed no checksum
// lines at all, as the one on the macos-26 CI runners does (PR #42's first
// macOS run), and otherwise hands its input to the real sha256sum, whose
// path replaces REAL. GNU and uutils sha256sum, and shasum, refuse empty
// input, so without this stand-in only a Mac could show an installer that
// leaves the refusal of a missing checksum line to the checker. It uses
// shell builtins only: PATH holds the bin directory alone.
const _lenientSha256sum = `#!/bin/sh
input=
while IFS= read -r line; do input="$input$line
"; done
[ -n "$input" ] || exit 0
printf '%s' "$input" | 'REAL' "$@"
`

// _tools are the real programs the scripts (and the fake curl) run, linked
// into the bin directory. A checksum tool is linked separately, per case.
var _tools = []string{"tar", "gzip", "awk", "mktemp", "mkdir", "rm", "cp", "install"}

// env is one case's throwaway world.
type env struct {
	root     string
	bin      string // shared by every case with the same checksum tool
	releases string
	log      string
}

// result is what one run of a script produced.
type result struct {
	exit     int
	stdout   string
	stderr   string
	requests []string
}

// newBins builds one bin directory per checksum tool this machine has: the
// fakes, and symlinks to the real tools with that checksum tool as the only
// one. It runs before any parallel subtest starts, on purpose: a file this
// process is still writing can be held open by another goroutine's fork, and
// executing it then fails with "text file busy" (golang/go#22315).
func newBins(t *testing.T) map[string]string {
	t.Helper()

	bins := map[string]string{}

	for _, sha := range []string{_sha256sum, _shasum} {
		shaPath, err := exec.LookPath(sha)
		if err != nil {
			continue
		}

		bin := t.TempDir()
		for name, body := range map[string]string{"curl": _fakeCurl, "uname": _fakeUname, "id": _fakeID} {
			require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755))
		}

		require.NoError(t, os.Symlink(shaPath, filepath.Join(bin, sha)))

		for _, tool := range _tools {
			path, err := exec.LookPath(tool)
			require.NoError(t, err, "these tests run the real %s", tool)
			require.NoError(t, os.Symlink(path, filepath.Join(bin, tool)))
		}

		bins[sha] = bin
	}

	// The lenient checker: the sha256sum bin's tools, its sha256sum swapped
	// for _lenientSha256sum around the real one.
	if strict, ok := bins[_sha256sum]; ok {
		shaPath, err := exec.LookPath(_sha256sum)
		require.NoError(t, err)

		bin := t.TempDir()

		entries, err := os.ReadDir(strict)
		require.NoError(t, err)

		for _, entry := range entries {
			if entry.Name() == _sha256sum {
				continue
			}

			target, err := os.Readlink(filepath.Join(strict, entry.Name()))
			if err != nil { // a fake: a regular file, copied
				body, err := os.ReadFile(filepath.Join(strict, entry.Name()))
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(bin, entry.Name()), body, 0o755))

				continue
			}

			require.NoError(t, os.Symlink(target, filepath.Join(bin, entry.Name())))
		}

		require.NotContains(t, shaPath, "'", "the real sha256sum's path is quoted into the lenient one")
		lenient := strings.Replace(_lenientSha256sum, "REAL", shaPath, 1)
		require.NoError(t, os.WriteFile(filepath.Join(bin, _sha256sum), []byte(lenient), 0o755))

		bins[_lenient] = bin
	}

	return bins
}

// newEnv lays out one case: a release of _tag with every archive the scripts
// may ask for, and the request log, with bin on PATH.
func newEnv(t *testing.T, bin string) env {
	t.Helper()

	root := t.TempDir()
	e := env{
		root:     root,
		bin:      bin,
		releases: filepath.Join(root, "releases"),
		log:      filepath.Join(root, "requests.log"),
	}
	writeRelease(t, filepath.Join(e.releases, _tag))

	return e
}

// writeRelease writes every archive a release of _tag publishes into dir (the
// host build for darwin only, the sandbox build for linux only), and a
// checksums.txt listing their real sha256.
func writeRelease(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))

	var sums strings.Builder

	for _, asset := range []struct{ role, platform string }{
		{_host, "darwin_amd64"},
		{_host, "darwin_arm64"},
		{"sandbox", "linux_amd64"},
		{"sandbox", "linux_arm64"},
	} {
		name := fmt.Sprintf("canga-%s_%s_%s.tar.gz", asset.role, _version, asset.platform)
		archive := buildArchive(t, asset.role)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), archive, 0o644))

		sum := sha256.Sum256(archive)
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}

	require.NoError(t, os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(sums.String()), 0o644))
}

// buildArchive returns a .tar.gz laid out like a release archive: LICENSE,
// README.md, and a canga that reports its version the way the real one does.
func buildArchive(t *testing.T, role string) []byte {
	t.Helper()

	var buf bytes.Buffer

	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	files := []struct {
		name, body string
		mode       int64
	}{
		{"LICENSE", "license\n", 0o644},
		{"README.md", "readme\n", 0o644},
		{"canga", fmt.Sprintf("#!/bin/sh\necho '%s (%s, test, go)'\n", _tag, role), 0o755},
	}

	for _, f := range files {
		hdr := &tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.body)), Typeflag: tar.TypeReg}
		require.NoError(t, tw.WriteHeader(hdr))
		_, err := tw.Write([]byte(f.body))
		require.NoError(t, err)
	}

	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())

	return buf.Bytes()
}

// mangleChecksums makes checksums.txt refuse the archives: "wrong" swaps every
// hash for another, "absent" leaves no line for any archive, and "twice"
// lists every archive twice, each time with its real hash.
func (e env) mangleChecksums(t *testing.T, how string) {
	t.Helper()

	path := filepath.Join(e.releases, _tag, "checksums.txt")

	switch how {
	case "":
		return
	case _wrong:
		data, err := os.ReadFile(path)
		require.NoError(t, err)

		var out strings.Builder

		for line := range strings.Lines(string(data)) {
			_, name, _ := strings.Cut(line, "  ")
			fmt.Fprintf(&out, "%s  %s", strings.Repeat("0", 64), name)
		}

		require.NoError(t, os.WriteFile(path, []byte(out.String()), 0o644))
	case _absent:
		require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("0", 64)+"  other.tar.gz\n"), 0o644))
	case _twice:
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, slices.Concat(data, data), 0o644))
	default:
		t.Fatalf("unknown checksums mangling %q", how)
	}
}

// seed puts a canga that prints _oldVersion at path, standing in for an
// earlier install.
func seed(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho "+_oldVersion+"\n"), 0o755))
}

// run executes script under shell with args, in an environment holding
// nothing but vars, PATH (the bin directory alone) and the fake curl's
// settings.
func (e env) run(t *testing.T, shell []string, script string, args []string, vars ...string) result {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, shell[0], slices.Concat(shell[1:], []string{script}, args)...)
	cmd.Dir = e.root
	cmd.Env = append([]string{
		"PATH=" + e.bin,
		// The scripts' mktemp -d lands in the case's directory, not /tmp.
		"TMPDIR=" + e.root,
		"FAKE_BASE=" + _releases,
		"FAKE_LATEST=" + _tag,
		"FAKE_RELEASES=" + e.releases,
		"FAKE_LOG=" + e.log,
	}, vars...)

	var stdout, stderr bytes.Buffer

	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// The timeout kills the shell only; a hung child holding the output pipes
	// would otherwise keep Run waiting.
	cmd.WaitDelay = 5 * time.Second

	var res result

	err := cmd.Run()
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		res.exit = exitErr.ExitCode()
	} else {
		require.NoError(t, err, "the shell could not be run")
	}

	res.stdout, res.stderr = stdout.String(), stderr.String()

	if data, err := os.ReadFile(e.log); err == nil {
		res.requests = strings.Split(strings.TrimSpace(string(data)), "\n")
	}

	return res
}

// shells are the ones every case runs under: sh (dash on the CI runner), and
// bash in POSIX mode, which is what /bin/sh is on macOS.
func shells(t *testing.T) map[string][]string {
	t.Helper()

	sh, err := exec.LookPath("sh")
	require.NoError(t, err)

	found := map[string][]string{"sh": {sh}}
	if bash, err := exec.LookPath("bash"); err == nil {
		found["bash"] = []string{bash, "--posix"}
	}

	return found
}

// requested is the log line of one request the scripts make, all with -L.
func requested(path string) string { return "1 " + _releases + "/" + path }

// downloads are the log lines of fetching asset and its checksums.txt.
func downloads(tag, asset string) []string {
	return []string{requested("download/" + tag + "/" + asset), requested("download/" + tag + "/checksums.txt")}
}

// assertInstalled checks the outcome at path: the new build when the run
// succeeded, the seeded one untouched when it did not.
func assertInstalled(t *testing.T, res result, path, role string, wantExit int) {
	t.Helper()

	// Through sh, not executed directly: the seeded canga is a file this
	// process wrote, and executing it could hit "text file busy".
	out, err := exec.CommandContext(t.Context(), "sh", path).Output()
	require.NoError(t, err, "the canga at %s must still run", path)

	if wantExit != 0 {
		assert.Equal(t, _oldVersion+"\n", string(out), "a refused install must leave the old canga alone")

		return
	}

	want := fmt.Sprintf("%s (%s, test, go)", _tag, role)
	assert.Equal(t, want+"\n", string(out))

	lines := strings.Split(strings.TrimSpace(res.stdout), "\n")
	assert.Equal(t, want, lines[len(lines)-1], "stdout ends with the installed version")

	info, err := os.Stat(path)
	require.NoError(t, err)

	// install -m 0755 sets the mode exactly. tar keeps the archive's 0755 minus
	// the umask, so only the owner's execute bit is certain on the host.
	if role == _host {
		assert.NotZero(t, info.Mode().Perm()&0o100, "canga must be executable by its owner")
	} else {
		assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
	}
}

// installCase is one run of an install script and what it must leave behind.
type installCase struct {
	name      string
	args      []string
	os, arch  string
	uid       string // what `id -u` says; only install_sandbox.sh asks
	sha       string // the one checksum tool on PATH: sha256sum or shasum
	checksums string // "", "wrong" or "absent": see mangleChecksums
	wantExit  int
	// Every request, in order, or nil for a run refused before any.
	wantRequests []string
	wantStderr   string   // a line stderr must contain, when set
	vars         []string // extra environment, appended after the case's own
}

// script is where a case's script lives and where it installs canga.
type script func(t *testing.T, e env) (path, installed string)

//nolint:paralleltest // serial so newBins writes the fakes while no other test in this package forks
func TestInstallHost(t *testing.T) {
	sha := _sha256sum // macOS has only shasum, which has a case of its own
	if _, err := exec.LookPath(sha); err != nil {
		sha = _shasum
	}

	host := func(asset string) string { return fmt.Sprintf("canga-host_%s_%s.tar.gz", _version, asset) }
	newest := append([]string{requested("latest")}, downloads(_tag, host("darwin_amd64"))...)
	tests := []installCase{
		{name: "newest release", os: _darwin, arch: "x86_64", sha: sha, wantRequests: newest, wantStderr: "is not on your PATH"},
		{name: "pinned tag on a mac", args: []string{_tag}, os: "Darwin", arch: "arm64", sha: sha, wantRequests: downloads(_tag, host("darwin_arm64"))},
		{name: "shasum only", args: []string{_tag}, os: _darwin, arch: _aarch64, sha: _shasum, wantRequests: downloads(_tag, host("darwin_arm64"))},
		{name: "checksum mismatch", args: []string{_tag}, os: _darwin, arch: _amd64, sha: sha, checksums: _wrong, wantExit: 1, wantRequests: downloads(_tag, host("darwin_amd64"))},
		{name: "no checksum line", args: []string{_tag}, os: _darwin, arch: _amd64, sha: sha, checksums: _absent, wantExit: 1, wantRequests: downloads(_tag, host("darwin_amd64"))},
		{name: "checksum mismatch, shasum", args: []string{_tag}, os: _darwin, arch: _amd64, sha: _shasum, checksums: _wrong, wantExit: 1, wantRequests: downloads(_tag, host("darwin_amd64"))},
		{name: "no checksum line, shasum", args: []string{_tag}, os: _darwin, arch: _amd64, sha: _shasum, checksums: _absent, wantExit: 1, wantRequests: downloads(_tag, host("darwin_amd64"))},
		// The installer itself refuses an asset checksums.txt does not list
		// exactly once, whatever the checker does with what it is handed:
		// macOS's own sha256sum accepts empty input, and a checker handed two
		// matching lines passes both.
		{name: "no checksum line, lenient sha256sum", args: []string{_tag}, os: _darwin, arch: _aarch64, sha: _lenient, checksums: _absent, wantExit: 1, wantRequests: downloads(_tag, host("darwin_arm64")), wantStderr: _wantNotOnce},
		{name: "checksum line twice", args: []string{_tag}, os: _darwin, arch: _amd64, sha: sha, checksums: _twice, wantExit: 1, wantRequests: downloads(_tag, host("darwin_amd64")), wantStderr: _wantNotOnce},
		{name: "checksum line twice, shasum", args: []string{_tag}, os: _darwin, arch: _aarch64, sha: _shasum, checksums: _twice, wantExit: 1, wantRequests: downloads(_tag, host("darwin_arm64")), wantStderr: _wantNotOnce},
		{name: "lenient sha256sum still installs a listed asset", args: []string{_tag}, os: _darwin, arch: _amd64, sha: _lenient, wantRequests: downloads(_tag, host("darwin_amd64"))},
		{name: "lenient sha256sum still refuses a mismatch", args: []string{_tag}, os: _darwin, arch: _amd64, sha: _lenient, checksums: _wrong, wantExit: 1, wantRequests: downloads(_tag, host("darwin_amd64"))},
		{name: "release that does not exist", args: []string{"v9.9.9"}, os: _darwin, arch: _amd64, sha: sha, wantExit: 22, wantRequests: []string{requested("download/v9.9.9/canga-host_9.9.9_darwin_amd64.tar.gz")}, wantStderr: "404"},
		{name: "not a release tag", args: []string{"latest"}, os: _darwin, arch: _amd64, sha: sha, wantExit: 1, wantStderr: `"latest" is not a release tag`},
		// A version above the release floor is accepted: it reaches the
		// network, exactly like "release that does not exist" above, since
		// neither has a fixture on disk. Reaching (and failing) the download
		// is how "accepted" is told apart from "refused before any request".
		{name: "a version above the release floor", args: []string{"v0.10.6"}, os: _darwin, arch: _amd64, sha: sha, wantExit: 22, wantRequests: []string{requested("download/v0.10.6/canga-host_0.10.6_darwin_amd64.tar.gz")}, wantStderr: "404"},
		{name: "a version well above the release floor", args: []string{"v1.0.0"}, os: _darwin, arch: _amd64, sha: sha, wantExit: 22, wantRequests: []string{requested("download/v1.0.0/canga-host_1.0.0_darwin_amd64.tar.gz")}, wantStderr: "404"},
		// Below the release floor is refused before any request: the release
		// could have been recreated with unverified bytes (docs/HANDOFF.md,
		// "Immutability is not retroactive").
		{name: "a version below the release floor", args: []string{"v0.10.4"}, os: _darwin, arch: _amd64, sha: sha, wantExit: 1, wantStderr: _wantBelowFloor},
		{name: "a version well below the release floor", args: []string{"v0.1.0"}, os: _darwin, arch: _amd64, sha: sha, wantExit: 1, wantStderr: _wantBelowFloor},
		// Defense in depth: the newest-release path (no tag on the command
		// line) applies the floor too, to whichever tag /releases/latest
		// resolves to. The redirect itself still has to be followed to learn
		// that tag, so exactly one request is made before the refusal.
		{
			name: "the newest release resolves below the release floor", os: _darwin, arch: _amd64, sha: sha,
			wantExit: 1, wantStderr: _wantBelowFloor, wantRequests: []string{requested("latest")},
			vars: []string{"FAKE_LATEST=v0.10.4"},
		},
		// Round-1 ship-gate finding 1 (docs/HANDOFF.md, "Release floor for
		// installers and canga upgrade (PR #41)"): awk's numeric coercion
		// truncated a dash-suffixed field, so a pre-release of the floor
		// itself compared equal to it and was wrongly accepted; a leading
		// zero was likewise read as if it were not there. Both are refused
		// before any request, the same way a version below the floor is.
		{name: "a pre-release of the floor itself", args: []string{"v0.10.5-rc1"}, os: _darwin, arch: _amd64, sha: sha, wantExit: 1, wantStderr: _wantNotCanonical},
		{name: "a leading zero", args: []string{"v00.10.5"}, os: _darwin, arch: _amd64, sha: sha, wantExit: 1, wantStderr: _wantNotCanonical},
		{name: "unsupported architecture", os: _darwin, arch: "riscv64", sha: sha, wantExit: 1, wantStderr: "unsupported architecture riscv64"},
		{name: "unsupported system", os: "FreeBSD", arch: _amd64, sha: sha, wantExit: 1, wantStderr: "unsupported system FreeBSD"},
		// The host build is published for macOS only (operator decision,
		// 2026-09-28): on Linux, which only ever runs the sandbox build, the
		// script says so and where to go instead, before any request.
		{name: "linux is refused", args: []string{_tag}, os: _linux, arch: _amd64, sha: sha, wantExit: 1, wantStderr: "published for macOS only"},
		{name: "linux is refused, newest release", os: _linux, arch: _aarch64, sha: sha, wantExit: 1, wantStderr: "install_sandbox.sh"},
	}

	runCases(t, _host, tests, func(t *testing.T, e env) (string, string) {
		t.Helper()

		script, err := filepath.Abs("install_host.sh")
		require.NoError(t, err)

		// The script installs into $HOME/.local/bin; run sets HOME to root.
		return script, filepath.Join(e.root, ".local", "bin", "canga")
	})
}

//nolint:paralleltest // serial so newBins writes the fakes while no other test in this package forks
func TestInstallSandbox(t *testing.T) {
	if _, err := exec.LookPath(_sha256sum); err != nil {
		t.Skip("install_sandbox.sh requires sha256sum, which this machine lacks; CI runs these cases")
	}

	sandbox := "canga-sandbox_" + _version + "_linux_arm64.tar.gz"

	tests := []installCase{
		{name: "pinned tag as root", args: []string{_tag}, os: _linux, arch: _aarch64, uid: "0", wantRequests: downloads(_tag, sandbox)},
		{name: "checksum mismatch", args: []string{_tag}, os: _linux, arch: _aarch64, uid: "0", checksums: _wrong, wantExit: 1, wantRequests: downloads(_tag, sandbox)},
		// The same refusals as install_host.sh's: the installer, not the
		// checker, refuses an asset checksums.txt does not list exactly once.
		{name: "no checksum line", args: []string{_tag}, os: _linux, arch: _aarch64, uid: "0", checksums: _absent, wantExit: 1, wantRequests: downloads(_tag, sandbox), wantStderr: _wantNotOnce},
		{name: "no checksum line, lenient sha256sum", args: []string{_tag}, os: _linux, arch: _aarch64, uid: "0", sha: _lenient, checksums: _absent, wantExit: 1, wantRequests: downloads(_tag, sandbox), wantStderr: _wantNotOnce},
		{name: "checksum line twice", args: []string{_tag}, os: _linux, arch: _aarch64, uid: "0", checksums: _twice, wantExit: 1, wantRequests: downloads(_tag, sandbox), wantStderr: _wantNotOnce},
		{name: "lenient sha256sum still installs a listed asset", args: []string{_tag}, os: _linux, arch: _aarch64, uid: "0", sha: _lenient, wantRequests: downloads(_tag, sandbox)},
		{name: "no tag", os: _linux, arch: _aarch64, uid: "0", wantExit: 2, wantStderr: "usage: install_sandbox.sh"},
		{name: "two arguments", args: []string{_tag, "extra"}, os: _linux, arch: _aarch64, uid: "0", wantExit: 2, wantStderr: "usage: install_sandbox.sh"},
		{name: "not a release tag", args: []string{"0.9"}, os: _linux, arch: _aarch64, uid: "0", wantExit: 2, wantStderr: `"0.9" is not a release tag`},
		{name: "not root", args: []string{_tag}, os: _linux, arch: _aarch64, uid: "1000", wantExit: 1, wantStderr: "run this as root"},
		{name: "not linux", args: []string{_tag}, os: "Darwin", arch: "arm64", uid: "0", wantExit: 1, wantStderr: "linux only, not Darwin"},
		// A version above the release floor is accepted: it reaches the
		// network and fails there, for lack of a fixture, rather than being
		// refused up front.
		{name: "a version above the release floor", args: []string{"v0.10.6"}, os: _linux, arch: _aarch64, uid: "0", wantExit: 22, wantRequests: []string{requested("download/v0.10.6/canga-sandbox_0.10.6_linux_arm64.tar.gz")}, wantStderr: "404"},
		{name: "a version well above the release floor", args: []string{"v1.0.0"}, os: _linux, arch: _aarch64, uid: "0", wantExit: 22, wantRequests: []string{requested("download/v1.0.0/canga-sandbox_1.0.0_linux_arm64.tar.gz")}, wantStderr: "404"},
		// Below the release floor is refused before any request, root or
		// not: the release could have been recreated with unverified bytes
		// (docs/HANDOFF.md, "Immutability is not retroactive").
		{name: "a version below the release floor", args: []string{"v0.10.4"}, os: _linux, arch: _aarch64, uid: "0", wantExit: 1, wantStderr: _wantBelowFloor},
		{name: "a version well below the release floor", args: []string{"v0.1.0"}, os: _linux, arch: _aarch64, uid: "0", wantExit: 1, wantStderr: _wantBelowFloor},
		// Round-1 ship-gate finding 1 (docs/HANDOFF.md, "Release floor for
		// installers and canga upgrade (PR #41)"): the same two shapes as
		// install_host.sh's own cases above, refused before any request.
		{name: "a pre-release of the floor itself", args: []string{"v0.10.5-rc1"}, os: _linux, arch: _aarch64, uid: "0", wantExit: 1, wantStderr: _wantNotCanonical},
		{name: "a leading zero", args: []string{"v00.10.5"}, os: _linux, arch: _aarch64, uid: "0", wantExit: 1, wantStderr: _wantNotCanonical},
	}
	for i := range tests {
		if tests[i].sha == "" {
			tests[i].sha = _sha256sum
		}
	}

	runCases(t, "sandbox", tests, func(t *testing.T, e env) (string, string) {
		t.Helper()

		// The script installs into /usr/local/bin. A copy of it installs into
		// the case's own directory instead, with that one line rewritten.
		dir := filepath.Join(e.root, "usr-local-bin")
		source, err := os.ReadFile("install_sandbox.sh")
		require.NoError(t, err)
		require.Equal(t, 1, strings.Count(string(source), _sandboxDirLine),
			"install_sandbox.sh must set dir=/usr/local/bin exactly once for this test to redirect it")

		copied := filepath.Join(e.root, "install_sandbox.sh")
		body := strings.Replace(string(source), _sandboxDirLine, "\tdir="+dir+"\n", 1)
		require.NoError(t, os.WriteFile(copied, []byte(body), 0o644))

		return copied, filepath.Join(dir, "canga")
	})
}

// runCases runs every case under every shell, each in an env of its own, with
// an earlier canga seeded where the script installs.
func runCases(t *testing.T, role string, tests []installCase, locate script) {
	t.Helper()

	bins := newBins(t)

	for shellName, shell := range shells(t) {
		for _, tt := range tests {
			t.Run(shellName+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				bin, ok := bins[tt.sha]
				if !ok {
					t.Skipf("%s is not installed here", tt.sha)
				}

				e := newEnv(t, bin)
				e.mangleChecksums(t, tt.checksums)
				path, installed := locate(t, e)
				seed(t, installed)

				res := e.run(t, shell, path, tt.args,
					append([]string{"HOME=" + e.root, "FAKE_OS=" + tt.os, "FAKE_ARCH=" + tt.arch, "FAKE_UID=" + tt.uid}, tt.vars...)...)

				require.Equal(t, tt.wantExit, res.exit, "stderr:\n%s", res.stderr)
				assert.Equal(t, tt.wantRequests, res.requests)
				assert.Contains(t, res.stderr, tt.wantStderr)
				assertInstalled(t, res, installed, role, tt.wantExit)
			})
		}
	}
}
