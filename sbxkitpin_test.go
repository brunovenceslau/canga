// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package install_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// Tests for scripts/sbx-kit-pin.sh. Each case runs the real script under sh,
// in a throwaway git repository whose HEAD holds a kit, with a bare origin,
// and a fake gh that serves the release list and the release digests from
// files the case wrote.

// The leading underscore marks a fixture constant shared across this
// package's test files (install_test.go, sbxkit_test.go, and this one), as
// opposed to a local variable, parameter or struct field of the same short
// name: setAssets's own tag, version, amd64 and arm64 parameters, pinKit's
// version, amd64 and arm64 parameters, and bumpWorld's amd64 and arm64
// fields all reuse those exact words. The prefix lets a fixture and a
// same-named local coexist without one shadowing the other.
const (
	_pinScript = "scripts/sbx-kit-pin.sh"

	// The release every bump fixture moves the kit to, and the one before.
	_bumpTag     = "v9.8.7"
	_bumpVersion = "9.8.7"
	_bumpPrevTag = "v9.8.6"

	// The check-previous fixtures: the kit on the release before the one
	// being cut.
	_prevVersion = "0.10.1"
	_prevTag     = "v0.10.1"
	_nextTag     = "v0.10.2"
	_notATag     = "not a release tag"
	_nextVersion = "0.10.2"
	_olderTag    = "v0.10.0"

	_notAVersion = "not a version"

	// A release on an older line than _prevTag.
	_olderLineTag = "v0.9.1"
)

var (
	// Distinct, recognizable sums, so a test can tell which arch got which.
	_sumAMD64 = strings.Repeat("a1", 32)
	_sumARM64 = strings.Repeat("b2", 32)
)

// _fakeGh serves `gh release list -R brunovenceslau/canga` from
// $FAKE_GH/tags (absent: gh fails) and
// `gh api repos/brunovenceslau/canga/releases/tags/<tag>` from
// $FAKE_GH/<tag>.assets (absent: a 404; $FAKE_GH/api-500 present: a server
// error), both already in the --jq output shape the script asks for.
// `gh api repos/brunovenceslau/canga --jq .full_name` answers with the
// repository's name, or $FAKE_GH/full_name when present, or a 404 when
// $FAKE_GH/repo-404 is present (a token that cannot see the repository). It
// insists on the flags that make the list "published releases", and on the
// repository named outright, never taken from the checkout's remotes.
const _fakeGh = `#!/bin/sh
case "$1 $2" in
"api repos/brunovenceslau/canga")
	[ "$3 $4" = "--jq .full_name" ] || { echo "fake gh: unexpected: $*" >&2; exit 99; }
	[ ! -f "$FAKE_GH/repo-404" ] || { echo "gh: Not Found (HTTP 404)" >&2; exit 1; }
	if [ -f "$FAKE_GH/full_name" ]; then cat "$FAKE_GH/full_name"; else echo brunovenceslau/canga; fi
	;;
"release list")
	case " $* " in *" -R brunovenceslau/canga "*) ;; *) echo "fake gh: no -R brunovenceslau/canga" >&2; exit 99 ;; esac
	case " $* " in *" --exclude-drafts "*) ;; *) echo "fake gh: no --exclude-drafts" >&2; exit 99 ;; esac
	case " $* " in *" --exclude-pre-releases "*) ;; *) echo "fake gh: no --exclude-pre-releases" >&2; exit 99 ;; esac
	[ -f "$FAKE_GH/tags" ] || { echo "gh: HTTP 401" >&2; exit 1; }
	cat "$FAKE_GH/tags"
	;;
"api repos/brunovenceslau/canga/releases/tags/"*)
	[ ! -f "$FAKE_GH/api-500" ] || { echo "gh: Internal Server Error (HTTP 500)" >&2; exit 1; }
	f="$FAKE_GH/${2##*/}.assets"
	[ -f "$f" ] || { echo "gh: Not Found (HTTP 404)" >&2; exit 1; }
	cat "$f"
	;;
*)
	echo "fake gh: unexpected: $*" >&2
	exit 99
	;;
esac
`

// fakeGhBin writes the fake gh into a directory of its own. Call it before
// t.Parallel, while no other test in this package runs: a file this process
// is still writing can be held open by another goroutine's fork, and
// executing it then fails with "text file busy".
func fakeGhBin(t *testing.T) string {
	t.Helper()

	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "gh"), []byte(_fakeGh), 0o755))

	return bin
}

// scrubbedEnv is a subprocess environment known to carry no GIT_* entry.
// childEnv is its constructor by convention. A named slice type would not do
// here: Go lets a plain []string, such as a raw os.Environ(), stand in for a
// named slice type with no conversion at all, so runPin(t, dir, os.Environ(),
// ...) would still compile and silently skip the scrub. Wrapping the slice
// in a struct with an unexported field closes that for the realistic
// mistake: env is only reachable from this package (not just this file),
// and asEnv refuses the one value inside the package that would still slip
// past it, the zero value.
type scrubbedEnv struct {
	env []string
}

// asEnv is env in the shape exec.Cmd.Env wants. It panics on the zero value:
// exec.Cmd treats a nil Env as "inherit the parent's whole environment", the
// opposite of what scrubbedEnv promises, so a scrubbedEnv{} built by hand
// instead of through childEnv must never reach a Cmd this quietly.
func (e scrubbedEnv) asEnv() []string {
	if e.env == nil {
		panic("scrubbedEnv zero value passed as an environment; build it with childEnv")
	}

	return e.env
}

// childEnv is environ without any GIT_* entry, then extra. A git hook
// exports GIT_DIR (absolute, in a linked worktree), GIT_INDEX_FILE and more;
// inherited, they would point every git a case runs, and the script's own,
// at the real repository instead of the case's throwaway one. The result is
// never nil, even when environ and extra both are: append(env, extra...)
// with env non-nil and extra empty returns env unchanged, and env starts
// from the non-nil []string{}, not from slices.Clone(environ), which would
// itself be nil when environ is.
func childEnv(environ []string, extra ...string) scrubbedEnv {
	env := slices.DeleteFunc(append([]string{}, environ...), func(entry string) bool {
		return strings.HasPrefix(entry, "GIT_")
	})

	return scrubbedEnv{env: append(env, extra...)}
}

// pinWorld is one case's repository, origin, fake gh state and TMPDIR.
type pinWorld struct {
	root, repo, gh, tmp string
	// inherited is the environment the case's subprocesses start from,
	// before childEnv scrubs it; env is added on top.
	inherited, env []string
}

// newPinWorld commits kit as sbx-kit/spec.yaml on main, pushes it to a bare
// origin, and, when signed, gives the repository an SSH signing key of its
// own that git verify-commit trusts.
func newPinWorld(t *testing.T, bin, kit string, signed bool) pinWorld {
	t.Helper()

	return newPinWorldFrom(t, os.Environ(), bin, kit, signed)
}

// newPinWorldFrom is newPinWorld with its subprocesses starting from
// inherited instead of this process's environment.
func newPinWorldFrom(t *testing.T, inherited []string, bin, kit string, signed bool) pinWorld {
	t.Helper()

	root := t.TempDir()
	w := pinWorld{
		inherited: inherited,
		root:      root,
		repo:      filepath.Join(root, "repo"),
		gh:        filepath.Join(root, "gh"),
		tmp:       filepath.Join(root, "tmp"),
	}
	w.env = []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"FAKE_GH=" + w.gh,
		// Every mktemp -d lands here, so a case can see what was left behind.
		"TMPDIR=" + w.tmp,
		"HOME=" + root,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"SBX_KIT_ALLOW_CLOBBER=",
		"SBX_KIT_OLDER_LINE=",
	}

	for _, dir := range []string{w.repo, w.gh, w.tmp} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}

	w.git(t, "init", "-q", "-b", "main")
	w.git(t, "config", "user.name", "Test")
	w.git(t, "config", "user.email", "test@example.invalid")

	if signed {
		keygen, err := exec.LookPath("ssh-keygen")
		if err != nil {
			t.Skip("ssh-keygen is not installed; the bump commit is signed with an SSH key")
		}

		key := filepath.Join(root, "key")
		keygenArgs := []string{"-q", "-t", "ed25519", "-N", "", "-C", "test", "-f", key}
		keygenCmd := exec.CommandContext(t.Context(), keygen, keygenArgs...)
		// Scrubbed on principle, like every other subprocess here, though
		// ssh-keygen itself never reads GIT_DIR/GIT_WORK_TREE/GIT_INDEX_FILE,
		// so the GIT_* env tests below cannot exercise a regression at this
		// one call site.
		keygenCmd.Env = w.childEnv().asEnv()
		out, err := keygenCmd.CombinedOutput()
		require.NoError(t, err, string(out))

		pub, err := os.ReadFile(key + ".pub")
		require.NoError(t, err)

		signers := writeFile(t, filepath.Join(root, "allowed_signers"), "test@example.invalid "+string(pub))
		w.git(t, "config", "gpg.format", "ssh")
		w.git(t, "config", "user.signingkey", key)
		w.git(t, "config", "gpg.ssh.allowedSignersFile", signers)
	}

	w.writeKit(t, kit)
	w.git(t, "add", ".")
	w.git(t, "commit", "-q", "-m", "kit")

	origin := filepath.Join(root, "origin.git")
	initCmd := exec.CommandContext(t.Context(), "git", "init", "-q", "--bare", origin)
	initCmd.Env = w.childEnv().asEnv()
	out, err := initCmd.CombinedOutput()
	require.NoError(t, err, string(out))
	w.git(t, "remote", "add", "origin", origin)
	w.git(t, "push", "-q", "origin", "main")

	return w
}

func (w pinWorld) writeKit(t *testing.T, kit string) {
	t.Helper()

	path := filepath.Join(w.repo, _kitSpec)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(kit), 0o644))
}

// childEnv is the whole environment of the case's subprocesses.
func (w pinWorld) childEnv() scrubbedEnv {
	return childEnv(w.inherited, w.env...)
}

func (w pinWorld) git(t *testing.T, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = w.repo
	cmd.Env = w.childEnv().asEnv()
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)

	return strings.TrimSpace(string(out))
}

// setTags makes the fake gh list tags as the published releases.
func (w pinWorld) setTags(t *testing.T, tags ...string) {
	t.Helper()
	writeFile(t, filepath.Join(w.gh, "tags"), strings.Join(tags, "\n")+"\n")
}

// setAssets makes the fake gh serve tag's release with the canga-sandbox_
// archives of version at those digests; an empty digest is one GitHub
// did not report.
func (w pinWorld) setAssets(t *testing.T, tag, version, amd64, arm64 string) {
	t.Helper()

	var b strings.Builder

	fmt.Fprintf(&b, "canga-host_%s_linux_amd64.tar.gz sha256:%s\n", version, strings.Repeat("0", 64))

	for arch, digest := range map[string]string{_amd64: amd64, _arm64: arm64} {
		fmt.Fprintf(&b, "canga-sandbox_%s_linux_%s.tar.gz %s\n", version, arch, digest)
	}

	b.WriteString("checksums.txt sha256:" + strings.Repeat("1", 64) + "\n")
	writeFile(t, filepath.Join(w.gh, tag+".assets"), b.String())
}

// leftovers is what the script left in TMPDIR.
func (w pinWorld) leftovers(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(w.tmp)
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	return names
}

// pinRun is one run of the pin script.
type pinRun struct {
	exit   int
	stdout string
	stderr string
}

// runPin runs the pin script under sh, in dir, with env as its whole
// environment.
func runPin(t *testing.T, dir string, env scrubbedEnv, args ...string) pinRun {
	t.Helper()

	script, err := filepath.Abs(_pinScript)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", append([]string{script}, args...)...)
	cmd.Dir = dir
	cmd.Env = env.asEnv()

	var stdout, stderr bytes.Buffer

	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	var res pinRun

	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		require.ErrorAs(t, err, &exit, "the script did not run: %s", stderr.String())
		res.exit = exit.ExitCode()
	}

	res.stdout, res.stderr = stdout.String(), stderr.String()

	return res
}

func (w pinWorld) run(t *testing.T, args ...string) pinRun {
	t.Helper()

	return runPin(t, w.repo, w.childEnv(), args...)
}

// checksums is a checksums.txt as GoReleaser writes it, with the given
// canga-sandbox_ lines after the host ones.
func checksums(sandbox ...string) string {
	var b strings.Builder
	for _, platform := range []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64"} {
		fmt.Fprintf(&b, "%s  canga-host_%s_%s.tar.gz\n", strings.Repeat("0", 64), _bumpVersion, platform)
	}

	for _, line := range sandbox {
		b.WriteString(line + "\n")
	}

	return b.String()
}

func sandboxLine(sum, arch string) string {
	return fmt.Sprintf("%s  canga-sandbox_%s_linux_%s.tar.gz", sum, _bumpVersion, arch)
}

func writeFile(t *testing.T, path, body string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))

	return path
}

// kitLines is a hand-made kit: each line, ended by a newline.
func kitLines(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

// rewriteRefusal is one input rewrite must refuse, and what it must say.
type rewriteRefusal struct {
	name, version, sums, kit, want string
}

func rewriteRefusals(good string) []rewriteRefusal {
	return []rewriteRefusal{
		{
			name: "arm64 line missing", version: _bumpVersion,
			sums: checksums(sandboxLine(_sumAMD64, _amd64)),
			want: "found 0",
		},
		{
			name: "amd64 line twice", version: _bumpVersion,
			sums: checksums(sandboxLine(_sumAMD64, _amd64), sandboxLine(_sumAMD64, _amd64), sandboxLine(_sumARM64, _arm64)),
			want: "found 2",
		},
		{
			name: "another version's lines", version: "9.8.8", sums: good,
			want: "found 0",
		},
		{
			name: "a sum that is not hex", version: _bumpVersion,
			sums: checksums(sandboxLine(strings.Repeat("Z", 64), _amd64), sandboxLine(_sumARM64, _arm64)),
			want: "is not a sha256",
		},
		{
			name: "a short sum", version: _bumpVersion,
			sums: checksums(sandboxLine("abc", _amd64), sandboxLine(_sumARM64, _arm64)),
			want: "is not a sha256",
		},
		{name: "a tag instead of a version", version: _bumpTag, sums: good, want: _notAVersion},
		{name: "a version with a leading zero", version: "09.8.7", sums: good, want: _notAVersion},
		{name: "a two-line version", version: "9.8.7\n1.2.3", sums: good, want: _notAVersion},
		{name: "a version with two parts", version: "9.8", sums: good, want: _notAVersion},
		{
			name: "a kit without the arm64 sum", version: _bumpVersion, sums: good,
			kit: kitLines(
				`        CANGA_VERSION="1.2.3"`,
				`            arch="amd64"`,
				`            sha256="x"`,
				`            arch="arm64"`,
			),
			want: "refusing to rewrite",
		},
		{
			name: "a kit with two versions", version: _bumpVersion, sums: good,
			kit: kitLines(
				`        CANGA_VERSION="1.2.3"`,
				`        CANGA_VERSION="1.2.4"`,
			),
			want: "found 2",
		},
		{
			name: "a kit whose version has a leading zero", version: _bumpVersion, sums: good,
			kit: kitLines(
				`        CANGA_VERSION="1.02.3"`,
				`            arch="amd64"`,
				`            sha256="x"`,
				`            arch="arm64"`,
				`            sha256="x"`,
			),
			want: "is not X.Y.Z",
		},
		{
			name: "a sum outside an arch branch", version: _bumpVersion, sums: good,
			kit: kitLines(
				`        CANGA_VERSION="1.2.3"`,
				`            sha256="x"`,
				`            arch="amd64"`,
				`            sha256="x"`,
				`            arch="arm64"`,
				`            sha256="x"`,
			),
			want: "refusing to rewrite",
		},
	}
}

// TestSbxKitPin_Version reads the working tree's kit, and refuses one it
// cannot read a version from.
func TestSbxKitPin_Version(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, kit, want string
		args            []string
		exit            int
	}{
		{name: "prints the kit's version", kit: realKit(t, _prevVersion, _sumAMD64, _sumARM64), want: _prevVersion + "\n"},
		{name: "no kit", exit: 1, want: _kitSpec + ": not found"},
		{name: "a malformed version", kit: realKit(t, "0.10", _sumAMD64, _sumARM64), exit: 1, want: "is not X.Y.Z"},
		{name: "an argument", kit: realKit(t, _prevVersion, _sumAMD64, _sumARM64), args: []string{"extra"}, exit: 1, want: "usage: version"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			if tt.kit != "" {
				require.NoError(t, os.MkdirAll(filepath.Join(dir, "sbx-kit"), 0o755))
				writeFile(t, filepath.Join(dir, _kitSpec), tt.kit)
			}

			res := runPin(t, dir, childEnv(os.Environ()), append([]string{"version"}, tt.args...)...)
			assert.Equal(t, tt.exit, res.exit, res.stderr)

			if tt.exit == 0 {
				assert.Equal(t, tt.want, res.stdout)
			} else {
				assert.Contains(t, res.stderr, tt.want)
			}
		})
	}
}

// TestSbxKitPin_Rewrite moves a copy of the kit to a fixture release, and
// checks the refusals leave the kit byte-for-byte untouched.
func TestSbxKitPin_Rewrite(t *testing.T) {
	t.Parallel()

	good := checksums(sandboxLine(_sumAMD64, _amd64), sandboxLine(_sumARM64, _arm64))

	// copyKit writes body, or the committed kit, to dir/sbx-kit/spec.yaml.
	copyKit := func(t *testing.T, dir, body string) string {
		t.Helper()

		if body == "" {
			data, err := os.ReadFile(_kitSpec)
			require.NoError(t, err)

			body = string(data)
		}

		path := filepath.Join(dir, _kitSpec)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))

		return writeFile(t, path, body)
	}

	t.Run("rewrites the version and both sums, and nothing else", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		kit := copyKit(t, dir, "")
		before, err := os.ReadFile(kit)
		require.NoError(t, err)

		sums := writeFile(t, filepath.Join(dir, "checksums.txt"), good)
		res := runPin(t, dir, childEnv(os.Environ()), "rewrite", _bumpVersion, sums)
		require.Equal(t, 0, res.exit, res.stderr)

		after, err := os.ReadFile(kit)
		require.NoError(t, err)

		pin := parseKitPin(kitInstallScript(t, kit))
		assert.Equal(t, []string{_bumpVersion}, pin.versions)
		assert.Equal(t, map[string][]string{_amd64: {_sumAMD64}, _arm64: {_sumARM64}}, pin.sums)
		assert.Equal(t, pinKit(t, string(before), _bumpVersion, _sumAMD64, _sumARM64), string(after),
			"only the three pin values change")

		info, err := os.Stat(kit)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())

		// Idempotent: a second run changes nothing.
		require.Equal(t, 0, runPin(t, dir, childEnv(os.Environ()), "rewrite", _bumpVersion, sums).exit)

		again, err := os.ReadFile(kit)
		require.NoError(t, err)
		assert.Equal(t, string(after), string(again))
	})

	for _, tt := range rewriteRefusals(good) {
		t.Run("refuses "+tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			kit := copyKit(t, dir, tt.kit)
			before, err := os.ReadFile(kit)
			require.NoError(t, err)

			sums := writeFile(t, filepath.Join(dir, "checksums.txt"), tt.sums)
			res := runPin(t, dir, childEnv(os.Environ()), "rewrite", tt.version, sums)
			assert.Equal(t, 1, res.exit)
			assert.Contains(t, res.stderr, tt.want)

			after, err := os.ReadFile(kit)
			require.NoError(t, err)
			assert.Equal(t, string(before), string(after), "a refused rewrite must not touch the kit")

			leftovers, err := filepath.Glob(kit + ".*")
			require.NoError(t, err)
			assert.Empty(t, leftovers, "a refused rewrite must not leave its temp file")
		})
	}
}

// TestSbxKitPin_CheckPrevious is the preflight rule: the kit at HEAD must pin
// the newest published release below the tag being released, and the
// digests GitHub serves for it.
//
// fakeGhBin runs before t.Parallel: see its comment.
func TestSbxKitPin_CheckPrevious(t *testing.T) {
	bin := fakeGhBin(t)
	t.Parallel()

	digestAMD64, digestARM64 := "sha256:"+_sumAMD64, "sha256:"+_sumARM64

	for _, tt := range []struct {
		name, kit, tag, want string
		tags                 []string
		// The release whose digests the fake gh serves, and which ones.
		assetsTag, amd64, arm64 string
		olderLine               string // SBX_KIT_OLDER_LINE
		noTags                  bool
		exit                    int
	}{
		{name: "kit pins the previous release", kit: _prevVersion, tags: []string{_prevTag, _olderTag, _tag}, tag: _nextTag, want: "pins " + _prevTag},
		{name: "the tag's own release is already published", kit: _prevVersion, tags: []string{_nextTag, _prevTag, _tag}, tag: _nextTag, want: "pins " + _prevTag},
		{name: "versions compare as numbers, not strings", kit: "0.10.0", tags: []string{_tag, _olderTag, "v0.8.9"}, tag: _prevTag, assetsTag: _olderTag, want: "pins v0.10.0"},
		{name: "a non-release tag is ignored", kit: _prevVersion, tags: []string{_prevTag, "v1.0", "nightly", "v0.09.9", _tag}, tag: _nextTag, want: "pins " + _prevTag},
		{
			name: "a release on an older line, allowed for it", kit: "0.9.0", tags: []string{_prevTag, _tag}, tag: _olderLineTag, assetsTag: _tag, olderLine: _olderLineTag,
			want: "WARNING: " + _olderLineTag + " is below",
		},
		{name: "a release on an older line, not allowed", kit: "0.9.0", tags: []string{_prevTag, _tag}, tag: _olderLineTag, assetsTag: _tag, exit: 1, want: "set SBX_KIT_OLDER_LINE=" + _olderLineTag},
		{
			name: "a release on an older line, allowed for another tag", kit: "0.9.0", tags: []string{_prevTag, _tag}, tag: _olderLineTag, assetsTag: _tag, olderLine: "v0.9.2", exit: 1,
			want: "set SBX_KIT_OLDER_LINE=" + _olderLineTag,
		},
		{name: "the previous bump never merged", kit: "0.10.0", tags: []string{_prevTag, _olderTag}, tag: _nextTag, exit: 1, want: "chore/sbx-kit-" + _prevTag},
		{name: "the kit already names this tag", kit: _nextVersion, tags: []string{_prevTag}, tag: _nextTag, exit: 1, want: "newest published\nrelease below v0.10.2 is v0.10.1"},
		{name: "an older line pinning an unpublished release", kit: "0.9.5", tags: []string{_prevTag, _tag}, tag: "v0.9.6", olderLine: "v0.9.6", exit: 1, want: "not a published release"},
		{name: "a pinned sum GitHub serves otherwise", kit: _prevVersion, tags: []string{_prevTag}, tag: _nextTag, arm64: "sha256:" + strings.Repeat("c3", 32), exit: 1, want: "not the ones pinned"},
		{name: "a pinned sum GitHub reports no digest for", kit: _prevVersion, tags: []string{_prevTag}, tag: _nextTag, amd64: " ", exit: 1, want: "serves no digest"},
		{name: "no release below this one", kit: _prevVersion, tags: []string{_nextTag}, tag: _nextTag, exit: 1, want: "no published release below"},
		{name: "gh fails", kit: _prevVersion, tag: _nextTag, noTags: true, exit: 1, want: "could not list"},
		{name: _notATag, kit: _prevVersion, tags: []string{_prevTag}, tag: _nextVersion, exit: 1, want: _notATag},
		{name: "a tag with a leading zero", kit: _prevVersion, tags: []string{_prevTag}, tag: "v0.010.2", exit: 1, want: _notATag},
		{name: "a malformed kit version", kit: "0.10", tags: []string{_prevTag}, tag: _nextTag, exit: 1, want: "is not X.Y.Z"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := newPinWorld(t, bin, realKit(t, tt.kit, _sumAMD64, _sumARM64), false)
			w.env = append(w.env, "SBX_KIT_OLDER_LINE="+tt.olderLine)

			if !tt.noTags {
				w.setTags(t, tt.tags...)
			}

			assetsTag, amd64, arm64 := _prevTag, digestAMD64, digestARM64
			if tt.assetsTag != "" {
				assetsTag = tt.assetsTag
			}

			if tt.amd64 != "" {
				amd64 = strings.TrimSpace(tt.amd64)
			}

			if tt.arm64 != "" {
				arm64 = tt.arm64
			}

			w.setAssets(t, assetsTag, strings.TrimPrefix(assetsTag, "v"), amd64, arm64)

			res := w.run(t, "check-previous", tt.tag)
			assert.Equal(t, tt.exit, res.exit, res.stderr)
			assert.Contains(t, res.stdout+res.stderr, tt.want)
			assert.Empty(t, w.leftovers(t))
		})
	}

	t.Run("reads the kit at HEAD, not the working tree", func(t *testing.T) {
		t.Parallel()

		w := newPinWorld(t, bin, realKit(t, "0.10.0", _sumAMD64, _sumARM64), false)
		w.setTags(t, _prevTag, _olderTag)
		w.setAssets(t, _prevTag, _prevVersion, digestAMD64, digestARM64)
		w.writeKit(t, realKit(t, _prevVersion, _sumAMD64, _sumARM64))

		res := w.run(t, "check-previous", _nextTag)
		assert.Equal(t, 1, res.exit, "an uncommitted bump must not satisfy the check")
		assert.Contains(t, res.stderr, `pins CANGA_VERSION="0.10.0"`)
	})
}

// TestSbxKitPin_CheckClobber refuses to rebuild a release whose archives a
// pushed or merged kit bump already pins.
//
// fakeGhBin runs before t.Parallel: see its comment.
func TestSbxKitPin_CheckClobber(t *testing.T) {
	bin := fakeGhBin(t)
	t.Parallel()

	digestAMD64, digestARM64 := "sha256:"+_sumAMD64, "sha256:"+_sumARM64

	for _, tt := range []struct {
		name, mainKit, want string
		allow               string // SBX_KIT_ALLOW_CLOBBER
		noAssets, noSandbox bool
		serverError         bool
		pushBranch          bool
		// What gh sees of the repository itself: hidden is a 404, and a
		// fullName, when set, is the name it answers under.
		repoHidden bool
		fullName   string
		exit       int
	}{
		{name: "a release without archives yet", mainKit: _prevVersion, noSandbox: true, want: "nothing to clobber"},
		{name: "archives nothing pins yet", mainKit: _prevVersion, want: "rebuilding them is safe"},
		{name: "the bump branch is pushed", mainKit: _prevVersion, pushBranch: true, exit: 1, want: "chore/sbx-kit-" + _nextTag + " is on origin"},
		{name: "main pins this release", mainKit: _nextVersion, exit: 1, want: "origin's main already pins v0.10.2"},
		{name: "main pins a newer release", mainKit: "0.11.0", exit: 1, want: "origin's main already pins v0.11.0"},
		{name: "main pins it, and the override names this tag", mainKit: _nextVersion, allow: _nextTag, want: "WARNING"},
		{name: "main pins it, and the override is 0", mainKit: _nextVersion, allow: "0", exit: 1, want: "SBX_KIT_ALLOW_CLOBBER=" + _nextTag},
		{name: "main pins it, and the override is 1", mainKit: _nextVersion, allow: "1", exit: 1, want: "SBX_KIT_ALLOW_CLOBBER=" + _nextTag},
		{name: "main pins it, and the override is yes", mainKit: _nextVersion, allow: "yes", exit: 1, want: "SBX_KIT_ALLOW_CLOBBER=" + _nextTag},
		{name: "main pins it, and the override names another tag", mainKit: _nextVersion, allow: _prevTag, exit: 1, want: "SBX_KIT_ALLOW_CLOBBER=" + _nextTag},
		{name: "no release yet, in a repository gh can see", mainKit: _nextVersion, noAssets: true, want: "no release yet"},
		{
			name: "no release, in a repository gh cannot see", mainKit: _nextVersion, noAssets: true, repoHidden: true, exit: 1,
			want: "gh does not see brunovenceslau/canga as itself; a 404 for " + _nextTag + " proves nothing (gh sees: nothing)",
		},
		{
			name: "no release, in a repository gh sees under another name", mainKit: _nextVersion, noAssets: true, fullName: "brunovenceslau/canga-fork\n", exit: 1,
			want: "gh does not see brunovenceslau/canga as itself; a 404 for " + _nextTag + " proves nothing (gh sees: brunovenceslau/canga-fork)",
		},
		{name: "the release cannot be read", mainKit: _prevVersion, serverError: true, exit: 1, want: "could not read the assets"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := newPinWorld(t, bin, realKit(t, tt.mainKit, _sumAMD64, _sumARM64), false)

			switch {
			case tt.noAssets:
			case tt.serverError:
				writeFile(t, filepath.Join(w.gh, "api-500"), "")
			case tt.noSandbox:
				hostOnly := "canga-host_" + _nextVersion + "_linux_amd64.tar.gz sha256:" + strings.Repeat("0", 64) + "\n"
				writeFile(t, filepath.Join(w.gh, _nextTag+".assets"), hostOnly)
			default:
				w.setAssets(t, _nextTag, _nextVersion, digestAMD64, digestARM64)
			}

			if tt.pushBranch {
				w.git(t, "push", "-q", "origin", "HEAD:refs/heads/chore/sbx-kit-"+_nextTag)
			}

			if tt.repoHidden {
				writeFile(t, filepath.Join(w.gh, "repo-404"), "")
			}

			if tt.fullName != "" {
				writeFile(t, filepath.Join(w.gh, "full_name"), tt.fullName)
			}

			w.env = append(w.env, "SBX_KIT_ALLOW_CLOBBER="+tt.allow)

			res := w.run(t, "check-clobber", _nextTag)
			assert.Equal(t, tt.exit, res.exit, res.stderr)
			assert.Contains(t, res.stdout+res.stderr, tt.want)
			assert.Empty(t, w.leftovers(t))
		})
	}
}

// bumpWorld is a signed pinWorld at HEAD tagged _bumpTag, whose kit pins
// _bumpPrevTag, with a dist/ built for _bumpTag and a release serving it.
type bumpWorld struct {
	pinWorld

	dist, sums     string
	amd64, arm64   string
	branch, before string
}

func newBumpWorld(t *testing.T, bin string) bumpWorld {
	t.Helper()

	return newBumpWorldFrom(t, os.Environ(), bin)
}

// newBumpWorldFrom is newBumpWorld with its subprocesses starting from
// inherited instead of this process's environment.
func newBumpWorldFrom(t *testing.T, inherited []string, bin string) bumpWorld {
	t.Helper()

	kit := realKit(t, strings.TrimPrefix(_bumpPrevTag, "v"), _sumAMD64, _sumARM64)
	w := bumpWorld{pinWorld: newPinWorldFrom(t, inherited, bin, kit, true)}
	w.git(t, "tag", _bumpTag)
	w.branch = "chore/sbx-kit-" + _bumpTag
	w.before = w.git(t, "rev-parse", "HEAD")

	// dist/ holds two archives and the checksums.txt naming them, as
	// GoReleaser leaves it. It sits outside the repository, so the tree
	// stays clean, as /dist/ being ignored keeps it in canga.
	w.dist = filepath.Join(w.root, "dist")
	require.NoError(t, os.MkdirAll(w.dist, 0o755))

	var lines []string

	for _, arch := range []string{_amd64, _arm64} {
		body := "the " + arch + " archive"
		writeFile(t, w.archive(arch), body)

		sum := sha256.Sum256([]byte(body))
		lines = append(lines, sandboxLine(hex.EncodeToString(sum[:]), arch))
	}

	w.amd64, w.arm64 = strings.Fields(lines[0])[0], strings.Fields(lines[1])[0]
	w.sums = writeFile(t, filepath.Join(w.dist, "checksums.txt"), checksums(lines...))
	w.setTags(t, _bumpTag, _bumpPrevTag)
	w.setAssets(t, _bumpTag, _bumpVersion, "sha256:"+w.amd64, "sha256:"+w.arm64)

	return w
}

func (w bumpWorld) archive(arch string) string {
	return filepath.Join(w.dist, "canga-sandbox_"+_bumpVersion+"_linux_"+arch+".tar.gz")
}

// want is the kit the bump must commit.
func (w bumpWorld) want(t *testing.T) string {
	t.Helper()

	return realKit(t, _bumpVersion, w.amd64, w.arm64)
}

func (w bumpWorld) bump(t *testing.T) pinRun {
	t.Helper()

	return w.run(t, "bump", _bumpTag, w.sums)
}

// assertUntouched checks a run left the checkout, the worktrees and TMPDIR
// as they were.
func (w bumpWorld) assertUntouched(t *testing.T) {
	t.Helper()

	assert.Equal(t, w.before, w.git(t, "rev-parse", "HEAD"))
	assert.Equal(t, "main", w.git(t, "branch", "--show-current"))
	assert.Empty(t, w.git(t, "status", "--porcelain"))
	assert.Equal(t, 1, strings.Count(w.git(t, "worktree", "list"), "\n")+1, "no worktree left behind")
	assert.Empty(t, w.leftovers(t), "no temp directory left behind")
}

// makeBranch commits kit (and, when extra is set, one more file) on the bump
// branch by hand, on top of base, signed or not, and returns to main.
func (w bumpWorld) makeBranch(t *testing.T, base, kit string, extra, signed bool) {
	t.Helper()

	w.git(t, "switch", "-q", "-c", w.branch, base)
	w.writeKit(t, kit)

	if extra {
		writeFile(t, filepath.Join(w.repo, "extra"), "x")
	}

	w.git(t, "add", ".")

	sign := "--no-gpg-sign"
	if signed {
		sign = "-S"
	}

	w.git(t, "commit", "-q", sign, "-m", "hand-made bump")
	w.git(t, "switch", "-q", "main")
	w.git(t, "reset", "-q", "--hard", w.before)
}

// bumpRefusal is one state bump must refuse, and what it must say.
type bumpRefusal struct {
	name, want string
	prepare    func(t *testing.T, w *bumpWorld)
	// Whether the case made the bump branch itself, so it must survive.
	ownBranch bool
}

func bumpRefusals() []bumpRefusal {
	return []bumpRefusal{
		{name: "a dirty tree", want: "dirty", prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			writeFile(t, filepath.Join(w.repo, "stray"), "x")
		}},
		{name: "a checksums.txt missing an arch", want: "found 0", prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			writeFile(t, w.sums, checksums(sandboxLine(w.amd64, _amd64)))
		}},
		{name: "a second tag on HEAD", want: "exactly the tag " + _bumpTag, prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			w.git(t, "tag", "v9.8.8")
		}},
		{name: "HEAD past the tag", want: "no tag", prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			w.git(t, "commit", "-q", "--allow-empty", "-m", "later")
			w.before = w.git(t, "rev-parse", "HEAD")
		}},
		{name: "an archive missing from dist", want: "not found", prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			require.NoError(t, os.Remove(w.archive(_arm64)))
		}},
		{name: "an archive checksums.txt does not describe", want: "says", prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			writeFile(t, w.archive(_amd64), "rebuilt after the upload")
		}},
		{name: "a release serving other bytes", want: "not the ones pinned", prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			w.setAssets(t, _bumpTag, _bumpVersion, "sha256:"+w.amd64, "sha256:"+strings.Repeat("c3", 32))
		}},
		{name: "a release reporting no digest", want: "serves no digest", prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			w.setAssets(t, _bumpTag, _bumpVersion, "", "sha256:"+w.arm64)
		}},
		{name: "a release list gh cannot read", want: "could not list", prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			require.NoError(t, os.Remove(filepath.Join(w.gh, "tags")))
		}},
		{name: "a release on an older line", want: "set SBX_KIT_OLDER_LINE=" + _bumpTag, prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			w.setTags(t, "v9.9.0", _bumpTag, _bumpPrevTag)
		}},
		{name: "a release on an older line, allowed for another tag", want: "set SBX_KIT_OLDER_LINE=" + _bumpTag, prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			w.setTags(t, "v9.9.0", _bumpTag, _bumpPrevTag)
			w.env = append(w.env, "SBX_KIT_OLDER_LINE=v9.8.8")
		}},
		{name: "a fresh commit that does not verify", want: "allowedSignersFile must list the key", prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			writeFile(t, filepath.Join(w.root, "allowed_signers"), "")
		}},
		{name: "a release that cannot be read", want: "could not read the assets", prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			require.NoError(t, os.Remove(filepath.Join(w.gh, _bumpTag+".assets")))
		}},
		{name: "a HEAD tagged without the v", want: "exactly the tag " + _bumpTag, prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			w.git(t, "tag", "-d", _bumpTag)
			w.git(t, "tag", _bumpVersion)
		}},
		{name: "a branch without the kit", want: "has no " + _kitSpec, ownBranch: true, prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			w.git(t, "switch", "-q", "-c", w.branch)
			w.git(t, "rm", "-q", _kitSpec)
			w.git(t, "commit", "-q", "-m", "no kit")
			w.git(t, "switch", "-q", "main")
		}},
		{name: "a branch pinning other hashes", want: "does not pin these", ownBranch: true, prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			w.makeBranch(t, w.before, realKit(t, _bumpVersion, _sumAMD64, _sumARM64), false, true)
		}},
		{name: "a branch whose commit does not verify", want: "does not verify", ownBranch: true, prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			w.makeBranch(t, w.before, w.want(t), false, false)
		}},
		{name: "a branch not on top of the tag", want: "not one commit on top of", ownBranch: true, prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			w.git(t, "commit", "-q", "--allow-empty", "-m", "elsewhere")
			elsewhere := w.git(t, "rev-parse", "HEAD")
			w.git(t, "reset", "-q", "--hard", w.before)
			w.makeBranch(t, elsewhere, w.want(t), false, true)
		}},
		{name: "a branch changing more than the kit", want: "changes more than", ownBranch: true, prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			w.makeBranch(t, w.before, w.want(t), true, true)
		}},
		{name: "a commit that cannot be signed", want: "could not commit the bump", prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			w.git(t, "config", "user.signingkey", filepath.Join(w.root, "no-such-key"))
		}},
	}
}

// TestSbxKitPin_Bump commits the rewrite on its own branch, signed, only once
// the archives in dist/ and the release's digests both match it, and leaves
// the checkout it ran in untouched.
//
// fakeGhBin runs before t.Parallel: see its comment.
func TestSbxKitPin_Bump(t *testing.T) {
	bin := fakeGhBin(t)
	t.Parallel()

	t.Run("commits a signed bump on a branch, then verifies it and changes nothing", func(t *testing.T) {
		t.Parallel()

		w := newBumpWorld(t, bin)

		res := w.bump(t)
		require.Equal(t, 0, res.exit, res.stderr)

		arm64Asset := "canga-sandbox_" + _bumpVersion + "_linux_arm64.tar.gz"
		assert.Contains(t, res.stdout, "release "+_bumpTag+" serves "+arm64Asset+" as sha256:"+w.arm64)
		assert.Contains(t, res.stdout, "git push -u origin "+w.branch)
		assert.Contains(t, res.stdout, "gh pr create --base main --head "+w.branch)
		w.assertUntouched(t)

		// One commit on top of HEAD, touching only the kit, signed.
		assert.Equal(t, w.before, w.git(t, "rev-parse", w.branch+"^"))
		assert.Equal(t, _kitSpec, w.git(t, "diff", "--name-only", w.before, w.branch))
		assert.Equal(t, "G", w.git(t, "log", "-1", "--format=%G?", w.branch))
		assert.Equal(t, "chore(sbx-kit): pin canga "+_bumpTag, w.git(t, "log", "-1", "--format=%s", w.branch))
		assert.Equal(t, strings.TrimSuffix(w.want(t), "\n"), w.git(t, "show", w.branch+":"+_kitSpec))

		tip := w.git(t, "rev-parse", w.branch)
		again := w.bump(t)
		require.Equal(t, 0, again.exit, again.stderr)
		assert.Contains(t, again.stdout, "already pins "+_bumpTag+" in a verified commit")
		assert.Equal(t, tip, w.git(t, "rev-parse", w.branch))
		w.assertUntouched(t)
	})

	t.Run("a release on an older line, allowed for it, leaves the kit alone", func(t *testing.T) {
		t.Parallel()

		w := newBumpWorld(t, bin)
		w.setTags(t, "v9.9.0", _bumpTag, _bumpPrevTag)
		w.env = append(w.env, "SBX_KIT_OLDER_LINE="+_bumpTag)

		res := w.bump(t)
		require.Equal(t, 0, res.exit, res.stderr)
		assert.Contains(t, res.stderr, "WARNING: "+_bumpTag+" is below the newest published release v9.9.0")
		assert.Contains(t, res.stdout, "does not move backwards")
		assert.Empty(t, w.git(t, "branch", "--list", "chore/*"))
		w.assertUntouched(t)
	})

	t.Run("a kit that already pins the release needs no branch", func(t *testing.T) {
		t.Parallel()

		w := newBumpWorld(t, bin)
		w.git(t, "tag", "-d", _bumpTag)
		w.writeKit(t, w.want(t))
		w.git(t, "commit", "-q", "-a", "-m", "already bumped")
		w.git(t, "tag", _bumpTag)
		w.before = w.git(t, "rev-parse", "HEAD")

		res := w.bump(t)
		require.Equal(t, 0, res.exit, res.stderr)
		assert.Contains(t, res.stdout, "no branch needed")
		assert.Empty(t, w.git(t, "branch", "--list", "chore/*"))
		w.assertUntouched(t)
	})

	for _, tt := range bumpRefusals() {
		t.Run("refuses "+tt.name, func(t *testing.T) {
			t.Parallel()

			w := newBumpWorld(t, bin)
			tt.prepare(t, &w)

			branches := func() string {
				return w.git(t, "branch", "--list", "chore/*", "--format=%(objectname)")
			}
			branchBefore := branches()

			res := w.bump(t)
			assert.Equal(t, 1, res.exit, res.stdout)
			assert.Contains(t, res.stderr, tt.want)

			if tt.ownBranch {
				assert.Equal(t, branchBefore, branches(), "a refusal leaves the existing branch as it was")
			} else {
				assert.Empty(t, w.git(t, "branch", "--list", "chore/*"), "a refused bump leaves no branch")
			}

			assert.Empty(t, w.leftovers(t), "no temp directory left behind")
			assert.Equal(t, 1, strings.Count(w.git(t, "worktree", "list"), "\n")+1, "no worktree left behind")
		})
	}
}

// gitEnvDecoy is a pinWorld created before its caller poisons any GIT_*
// variable, plus the means to prove it was left untouched. Shared by
// TestSbxKitPin_IgnoresInheritedGitEnv and TestSbxKitPin_IgnoresRealGitEnv,
// which differ only in how they poison GIT_*.
type gitEnvDecoy struct {
	pinWorld

	gitDir                              string
	configBefore, logBefore, refsBefore string
}

// newGitEnvDecoy creates a pinWorld and snapshots it before anything poisons
// GIT_*, and registers a t.Cleanup check, so a leaked git that corrupts it
// fails the case even if it ends some other way first.
func newGitEnvDecoy(t *testing.T, bin string) gitEnvDecoy {
	t.Helper()

	d := gitEnvDecoy{pinWorld: newPinWorld(t, bin, realKit(t, _prevVersion, _sumAMD64, _sumARM64), false)}
	d.gitDir = filepath.Join(d.repo, ".git")
	d.configBefore, d.logBefore, d.refsBefore = d.snapshot(t)

	t.Cleanup(func() {
		config, err := os.ReadFile(filepath.Join(d.gitDir, "config"))
		if assert.NoError(t, err) {
			assert.Equal(t, d.configBefore, string(config), "the decoy's config, at cleanup")
		}
	})

	return d
}

// snapshot is the decoy's config, full history and every ref.
func (d gitEnvDecoy) snapshot(t *testing.T) (string, string, string) {
	t.Helper()

	config, err := os.ReadFile(filepath.Join(d.gitDir, "config"))
	require.NoError(t, err)

	return string(config),
		d.git(t, "log", "--all", "--format=%H %s"),
		d.git(t, "for-each-ref", "--format=%(refname) %(objectname)")
}

// assertUntouched checks the decoy is byte-for-byte as newGitEnvDecoy left it.
func (d gitEnvDecoy) assertUntouched(t *testing.T) {
	t.Helper()

	config, log, refs := d.snapshot(t)
	assert.Equal(t, d.configBefore, config, "the decoy's config")
	assert.Equal(t, d.logBefore, log, "the decoy's history")
	assert.Equal(t, d.refsBefore, refs, "the decoy's refs")
}

// TestSbxKitPin_IgnoresInheritedGitEnv runs a bump that inherits the GIT_*
// variables a git hook exports, aimed at a decoy repository, and checks the
// decoy is left byte-for-byte as it was: every git the case and the script
// run must work on the case's own repository. The variables reach the case
// through newBumpWorldFrom, not t.Setenv, so the test stays parallel.
func TestSbxKitPin_IgnoresInheritedGitEnv(t *testing.T) {
	bin := fakeGhBin(t)
	t.Parallel()

	decoy := newGitEnvDecoy(t, bin)

	inherited := append(os.Environ(),
		"GIT_DIR="+decoy.gitDir,
		"GIT_WORK_TREE="+decoy.repo,
		"GIT_INDEX_FILE="+filepath.Join(decoy.gitDir, "index"),
	)
	w := newBumpWorldFrom(t, inherited, bin)

	res := w.bump(t)
	require.Equal(t, 0, res.exit, res.stderr)
	assert.NotEmpty(t, w.git(t, "branch", "--list", w.branch), "the bump lands in the case's own repository")

	decoy.assertUntouched(t)
}

// TestSbxKitPin_IgnoresRealGitEnv is TestSbxKitPin_IgnoresInheritedGitEnv's
// serial twin. That test passes the hostile GIT_* variables through
// newBumpWorldFrom's inherited argument, a plain local slice: it proves
// childEnv scrubs whatever it is handed, but it cannot prove anything about a
// pinWorld method that stopped calling childEnv and read the real process
// environment directly (for example, w.git reverted to building its Cmd.Env
// as append(os.Environ(), w.env...)), because in that case os.Environ() is
// still clean. This case sets the variables on the real process environment
// instead, so that regression leaks the decoy's GIT_DIR into every git call
// pinWorld's own setup makes and corrupts the decoy, which assertUntouched
// catches. Serial: it calls t.Setenv directly, which paralleltest excuses.
// That, and not t.Parallel, is also what keeps the poisoned GIT_* vars from
// a parallel sibling: go test runs every non-parallel top-level test to
// completion before any parallel one resumes past its own t.Parallel call,
// so this only needs to stay a plain, non-parallel top-level test - it does
// not need to be declared last, and does not depend on file or declaration
// order.
func TestSbxKitPin_IgnoresRealGitEnv(t *testing.T) {
	bin := fakeGhBin(t)

	decoy := newGitEnvDecoy(t, bin)

	t.Setenv("GIT_DIR", decoy.gitDir)
	t.Setenv("GIT_WORK_TREE", decoy.repo)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(decoy.gitDir, "index"))

	w := newBumpWorld(t, bin)

	res := w.bump(t)
	require.Equal(t, 0, res.exit, res.stderr)
	assert.NotEmpty(t, w.git(t, "branch", "--list", w.branch), "the bump lands in the case's own repository")

	decoy.assertUntouched(t)
}
