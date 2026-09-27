// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package install_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for the Makefile's release-kit-bump target.
//
// TestReleaseKitBump_TagGuard needs no git repository at all: the guard it
// exercises is the recipe's very first line, before gh, git or mktemp ever
// run. Its cases reproduce the injection this target used to carry, when
// TAG was spliced into the recipe as make's own $(TAG) (raw text, expanded
// before the shell parses anything) instead of read as the shell variable
// $$TAG: a value like `v1'; touch sentinel; echo '` closed the quote it was
// pasted into and ran as shell. Every case checks that a sentinel file
// never appears, not only that make exits non-zero.
//
// The other cases build a full offline fixture: a bare "origin", a fake gh
// answering exactly the calls this target makes, and a repository carrying
// (or, for one case, not carrying) a real copy of scripts/sbx-kit-pin.sh at
// the tag. They run `make` itself, under -C and an absolute -f, so the
// target's own git and gh calls never leave localhost, and `make ci` runs
// them with no network at all.

// absMakefile is the repository's Makefile. Every case runs it against a
// throwaway repository via `-C <dir> -f absMakefile`, never against the
// real checkout's own working tree.
func absMakefile(t *testing.T) string {
	t.Helper()

	abs, err := filepath.Abs("Makefile")
	require.NoError(t, err)

	return abs
}

// runMake runs `make -C dir -f absMakefile release-kit-bump ...args`, with
// env as the whole environment, and reports its exit code the same way
// runPin does for the script.
func runMake(t *testing.T, dir string, env []string, args ...string) pinRun {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	full := append([]string{"-C", dir, "-f", absMakefile(t)}, args...)
	cmd := exec.CommandContext(ctx, "make", full...)
	cmd.Env = env

	var stdout, stderr strings.Builder

	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	var res pinRun

	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		require.ErrorAs(t, err, &exit, "make did not run: %s", stderr.String())
		res.exit = exit.ExitCode()
	}

	res.stdout, res.stderr = stdout.String(), stderr.String()

	return res
}

// TestReleaseKitBump_TagGuard refuses every malformed or malicious TAG
// before running a single command, whether TAG arrives as a command-line
// variable (`make release-kit-bump TAG=...`) or as a plain environment
// variable (`TAG=... make release-kit-bump`): both put TAG in the recipe's
// shell environment the same way, so both must be refused the same way.
//
// The command-line form has one quirk this test works around rather than
// asserts on: make itself parses a $(...) inside a command-line VAR=value
// argument as make's own variable-reference syntax, before the recipe ever
// runs, and silently expands an undefined one to nothing. A dollar-paren
// payload given that way never reaches the guard as written; given as a
// plain environment variable it does, unmangled, which is where this test
// exercises it (see "as a plain environment variable" below).
func TestReleaseKitBump_TagGuard(t *testing.T) {
	t.Parallel()

	sentinelPayload := func(sentinel string) string {
		return "v1.0.0'; touch " + sentinel + "; echo '"
	}
	// newlinePayload reproduces the ship-gate round-2 finding: the guard used
	// to be `printf '%s\n' "$TAG" | grep -Eq '^v...$'`, and grep -q reports
	// success as soon as ANY line of a multi-line value matches, not every
	// line. A value whose first line looks like a real version and whose
	// second line is shell-meaningful text used to sail through on that
	// first line alone. The fixed guard rejects any embedded newline
	// outright, before the line content is even considered.
	newlinePayload := func(sentinel string) string {
		return "v1.0.0\n'; touch " + sentinel + "; echo '"
	}

	cases := []struct {
		name, tag string
	}{
		{name: "empty", tag: ""},
		{name: "missing the v", tag: "1.2.3"},
		{name: "missing the patch", tag: "v1.2"},
		{name: "a pre-release suffix", tag: "v1.2.3-rc.1"},
		{name: "trailing garbage", tag: "v1.2.3 && echo pwned"},
		{name: "a single-quote breakout", tag: sentinelPayload("SENTINEL")},
		{name: "a backtick payload", tag: "v1.0.0`touch SENTINEL`"},
		{name: "an embedded newline before a shell breakout", tag: newlinePayload("SENTINEL")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			sentinel := filepath.Join(dir, "PWNED")
			tag := strings.ReplaceAll(tc.tag, "SENTINEL", sentinel)

			run := runMake(t, dir, os.Environ(), "release-kit-bump", "TAG="+tag)

			assert.NotEqual(t, 0, run.exit, "stdout: %s\nstderr: %s", run.stdout, run.stderr)
			assert.Contains(t, run.stderr, "usage: make release-kit-bump TAG=vX.Y.Z")
			assert.NoFileExists(t, sentinel, "the payload ran instead of being refused as data")
		})
	}

	// The same guard, with TAG passed as a real environment variable
	// instead of a make command-line assignment: both forms put TAG in the
	// recipe's $$TAG the same way (verified against a throwaway Makefile
	// before this fix), so both must be refused the same way. This is also
	// where the dollar-paren payload belongs: env vars are not scanned for
	// make's own $(...) syntax, so it reaches the guard unmangled here.
	envCases := []struct {
		name, tag string
	}{
		{name: "a single-quote breakout", tag: sentinelPayload("SENTINEL")},
		{name: "a dollar-paren payload", tag: "v1.0.0$(touch SENTINEL)"},
		{name: "an embedded newline before a shell breakout", tag: newlinePayload("SENTINEL")},
	}

	for _, tc := range envCases {
		t.Run("as a plain environment variable/"+tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			sentinel := filepath.Join(dir, "PWNED")
			tag := strings.ReplaceAll(tc.tag, "SENTINEL", sentinel)
			env := append(os.Environ(), "TAG="+tag)

			run := runMake(t, dir, env, "release-kit-bump")

			assert.NotEqual(t, 0, run.exit, "stdout: %s\nstderr: %s", run.stdout, run.stderr)
			assert.Contains(t, run.stderr, "usage: make release-kit-bump TAG=vX.Y.Z")
			assert.NoFileExists(t, sentinel, "the payload ran instead of being refused as data")
		})
	}
}

// _fakeGhForMake answers exactly the gh calls release-kit-bump, and the real
// scripts/sbx-kit-pin.sh bump it runs, make: resolving the tag's own object
// (and, when it is an annotated tag, dereferencing it) at
// repos/.../git/ref/tags and repos/.../git/tags, downloading the release's
// checksums.txt and canga-sandbox_ archives, serving the release's asset
// digests for check_published, and listing FAKE_TAG as the only published
// release, so bump's own "does the kit move backwards" check finds nothing
// newer. Every case's env pins exactly one FAKE_TAG, so a call about any
// other tag is a fixture bug, not a real refusal, and fails loudly.
const _fakeGhForMake = `#!/bin/sh
case "$1 $2" in
"api repos/brunovenceslau/canga/git/ref/tags/"*)
	tag="${2##*/tags/}"
	[ "$tag" = "$FAKE_TAG" ] || { echo "fake gh: unexpected tag: $tag" >&2; exit 99; }
	echo "$FAKE_REF_TYPE $FAKE_REF_SHA"
	;;
"api repos/brunovenceslau/canga/git/tags/"*)
	sha="${2##*/tags/}"
	[ "$sha" = "$FAKE_REF_SHA" ] || { echo "fake gh: unexpected tag object: $sha" >&2; exit 99; }
	echo "$FAKE_COMMIT_SHA"
	;;
"api repos/brunovenceslau/canga/releases/tags/"*)
	tag="${2##*/tags/}"
	[ "$tag" = "$FAKE_TAG" ] || { echo "fake gh: unexpected tag: $tag" >&2; exit 99; }
	cat "$FAKE_RELEASE_DIR/assets.txt"
	;;
"release download")
	tag="$3"
	[ "$tag" = "$FAKE_TAG" ] || { echo "fake gh: unexpected tag: $tag" >&2; exit 99; }
	dest="" prev=""
	for a in "$@"; do
		[ "$prev" = "-D" ] && dest="$a"
		prev="$a"
	done
	[ -n "$dest" ] || { echo "fake gh: release download with no -D" >&2; exit 99; }
	cp "$FAKE_RELEASE_DIR"/checksums.txt "$dest"/ || exit 1
	cp "$FAKE_RELEASE_DIR"/canga-sandbox_*.tar.gz "$dest"/ || exit 1
	;;
"release list")
	echo "$FAKE_TAG"
	;;
*)
	echo "fake gh (release-kit-bump): unexpected: $*" >&2
	exit 99
	;;
esac
`

// fakeGhForMakeBin writes _fakeGhForMake into a directory of its own. Like
// fakeGhBin, called before t.Parallel, while no other test in this package
// runs, so a file this process is still writing is never "text file busy"
// for a goroutine's fork.
func fakeGhForMakeBin(t *testing.T) string {
	t.Helper()

	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "gh"), []byte(_fakeGhForMake), 0o755))

	return bin
}

// buildReleaseDir writes a fixture release's checksums.txt and both
// canga-sandbox_ archives for _bumpVersion into dir, plus dir/assets.txt in
// release_assets's own "<name> <digest>" shape, and returns each arch's
// sha256.
func buildReleaseDir(t *testing.T, dir string) (amd64, arm64 string) {
	t.Helper()

	sums := make(map[string]string, 2)

	var lines []string

	for _, arch := range []string{_amd64, _arm64} {
		body := "the " + arch + " archive, from the release-kit-bump Makefile test"
		archive := filepath.Join(dir, "canga-sandbox_"+_bumpVersion+"_linux_"+arch+".tar.gz")
		require.NoError(t, os.WriteFile(archive, []byte(body), 0o644))

		sum := sha256.Sum256([]byte(body))
		hexSum := hex.EncodeToString(sum[:])
		sums[arch] = hexSum
		lines = append(lines, sandboxLine(hexSum, arch))
	}

	require.NoError(t, os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(checksums(lines...)), 0o644))

	var assets strings.Builder
	for _, arch := range []string{_amd64, _arm64} {
		fmt.Fprintf(&assets, "canga-sandbox_%s_linux_%s.tar.gz sha256:%s\n", _bumpVersion, arch, sums[arch])
	}

	require.NoError(t, os.WriteFile(filepath.Join(dir, "assets.txt"), []byte(assets.String()), 0o644))

	return sums[_amd64], sums[_arm64]
}

// makeBumpFixture is one case's repository, bare origin, fake gh and
// scratch release directory, and the tag's own commit and (annotated) tag
// object sha, ready for `make -C fixture.repo -f absMakefile
// release-kit-bump TAG=_bumpTag`.
type makeBumpFixture struct {
	pinWorld

	releaseDir           string
	commitSHA, tagObjSHA string
	amd64, arm64         string
}

// newMakeBumpFixture commits the previous release's kit (and, when
// withScript, a real copy of scripts/sbx-kit-pin.sh) on main, tags HEAD
// _bumpTag as a real annotated tag (so the fixture exercises the same
// tag-object dereference release-kit-bump does for a real `git tag -s`),
// pushes the tag to a bare origin, and builds _bumpTag's release assets.
func newMakeBumpFixture(t *testing.T, ghBin string, withScript bool) makeBumpFixture {
	t.Helper()

	kit := realKit(t, strings.TrimPrefix(_bumpPrevTag, "v"), _sumAMD64, _sumARM64)
	f := makeBumpFixture{pinWorld: newPinWorld(t, ghBin, kit, true)}

	if withScript {
		script, err := os.ReadFile(_pinScript)
		require.NoError(t, err)

		dst := filepath.Join(f.repo, _pinScript)
		require.NoError(t, os.MkdirAll(filepath.Dir(dst), 0o755))
		require.NoError(t, os.WriteFile(dst, script, 0o755))
		f.git(t, "add", _pinScript)
		f.git(t, "commit", "-q", "-m", "carry scripts/sbx-kit-pin.sh at the tag")
	}

	f.git(t, "tag", "-a", _bumpTag, "-m", _bumpTag)
	f.git(t, "push", "-q", "origin", _bumpTag)

	f.commitSHA = f.git(t, "rev-parse", _bumpTag+"^{commit}")
	f.tagObjSHA = f.git(t, "rev-parse", _bumpTag)

	f.releaseDir = filepath.Join(f.root, "release")
	require.NoError(t, os.MkdirAll(f.releaseDir, 0o755))
	f.amd64, f.arm64 = buildReleaseDir(t, f.releaseDir)

	return f
}

// envFor is the fixture's whole subprocess environment, with the fake gh
// pinned to this fixture's tag, ref and release. (Named envFor, not env: a
// method named env would shadow the env field pinWorld already promotes
// into makeBumpFixture, which this method itself needs to read.)
func (f makeBumpFixture) envFor(refType, refSHA, commitSHA string) []string {
	extra := append(append([]string{}, f.env...),
		"FAKE_TAG="+_bumpTag,
		"FAKE_REF_TYPE="+refType,
		"FAKE_REF_SHA="+refSHA,
		"FAKE_COMMIT_SHA="+commitSHA,
		"FAKE_RELEASE_DIR="+f.releaseDir,
	)

	return childEnv(f.inherited, extra...).asEnv()
}

// run invokes release-kit-bump against this fixture, with gh reporting the
// tag as an annotated tag object dereferencing to commitSHA.
func (f makeBumpFixture) run(t *testing.T, commitSHA string) pinRun {
	t.Helper()

	env := f.envFor("tag", f.tagObjSHA, commitSHA)

	return runMake(t, f.repo, env, "release-kit-bump", "TAG="+_bumpTag)
}

// TestReleaseKitBump_CrossCheckMismatch refuses before downloading anything
// when the tag this checkout fetched resolves to a different commit than
// the one GitHub's own API resolves it to.
func TestReleaseKitBump_CrossCheckMismatch(t *testing.T) {
	bin := fakeGhForMakeBin(t)
	t.Parallel()

	f := newMakeBumpFixture(t, bin, true)

	run := f.run(t, strings.Repeat("f", 40))

	assert.NotEqual(t, 0, run.exit, "stdout: %s\nstderr: %s", run.stdout, run.stderr)
	assert.Contains(t, run.stderr, "GitHub resolves it to")
	assert.Empty(t, f.leftovers(t), "no temp directory left behind")
	assert.Equal(t, 1, strings.Count(f.git(t, "worktree", "list"), "\n")+1, "no worktree left behind")
}

// TestReleaseKitBump_PredatesScript refuses, after the download, when the
// tag's own tree carries no scripts/sbx-kit-pin.sh to run bump from.
func TestReleaseKitBump_PredatesScript(t *testing.T) {
	bin := fakeGhForMakeBin(t)
	t.Parallel()

	f := newMakeBumpFixture(t, bin, false)

	run := f.run(t, f.commitSHA)

	assert.NotEqual(t, 0, run.exit, "stdout: %s\nstderr: %s", run.stdout, run.stderr)
	assert.Contains(t, run.stderr, "predates scripts/sbx-kit-pin.sh")
	assert.Empty(t, f.leftovers(t), "no temp directory left behind")
	assert.Equal(t, 1, strings.Count(f.git(t, "worktree", "list"), "\n")+1, "no worktree left behind")
}

// TestReleaseKitBump_CommitsFromValidTag runs release-kit-bump end to end,
// entirely offline: it downloads the fixture release's assets, checks them
// against GitHub's own served digests, runs scripts/sbx-kit-pin.sh bump from
// the tag's own worktree, and leaves a signed chore/sbx-kit-<tag> branch
// pinning the release, with nothing left behind.
func TestReleaseKitBump_CommitsFromValidTag(t *testing.T) {
	bin := fakeGhForMakeBin(t)
	t.Parallel()

	f := newMakeBumpFixture(t, bin, true)

	run := f.run(t, f.commitSHA)

	require.Equal(t, 0, run.exit, "stdout: %s\nstderr: %s", run.stdout, run.stderr)
	assert.Contains(t, run.stdout,
		"sbx-kit-pin: committed the "+_bumpTag+" kit pin on branch chore/sbx-kit-"+_bumpTag)

	got := f.git(t, "show", "chore/sbx-kit-"+_bumpTag+":"+_kitSpec)
	// f.git trims trailing whitespace from `git show`'s output; f.want
	// carries the kit template's own trailing newline, so it is trimmed the
	// same way before comparing.
	assert.Equal(t, strings.TrimSpace(f.want(t)), got)

	assert.Empty(t, f.git(t, "status", "--porcelain"), "the checkout release-kit-bump ran from is untouched")
	assert.Equal(t, 1, strings.Count(f.git(t, "worktree", "list"), "\n")+1, "no worktree left behind")
	assert.Empty(t, f.leftovers(t), "no temp directory left behind")
}

// want is the kit release-kit-bump must have committed on the bump branch:
// _bumpVersion, pinned to the sums buildReleaseDir wrote into the fixture's
// own release directory.
func (f makeBumpFixture) want(t *testing.T) string {
	t.Helper()

	return realKit(t, _bumpVersion, f.amd64, f.arm64)
}
