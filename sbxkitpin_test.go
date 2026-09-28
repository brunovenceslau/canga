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
	"syscall"
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

	// A published tag older than every other fixture tag in this file,
	// used only to pad out a tag list and to exercise numeric (not string)
	// ordering ("versions compare as numbers, not strings" below). Its own
	// name, rather than install_test.go's _decoyTag: that one is the release
	// newEnv builds a fixture for, pinned at the release floor
	// (install_host.sh/install_sandbox.sh refuse anything below it), which
	// this file's fixtures have no reason to sit above.
	_decoyTag = "v0.9.0"
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
// repository named outright, never taken from the checkout's remotes. It
// also insists GH_HOST is pinned to github.com on every call: the script is
// meant to export that itself, so every case in this file doubles as a
// regression test for that pin, not only the cases that mention it by name.
// FAKE_GH_SLEEP, when set, creates $FAKE_GH/release-list.started and then
// sleeps that many seconds before answering "release list" - the one hook
// TestSbxKitPin_CheckPrevious_SignalCleanup uses to signal the script only
// once it is genuinely blocked on gh, not at a moment guessed with a timer.
//
// The build provenance calls (see check_published's attestation step in
// the script): `gh --version` answers $FAKE_GH/version, or _fakeGhVersion
// when absent. `gh release download <tag> -R brunovenceslau/canga -D <dir>
// -p <name>...` copies each named file from $FAKE_GH/<tag>.files/, failing
// the way gh does when one is missing (unless $FAKE_GH/download-partial
// exists: then it copies what it has and succeeds, the one way to reach the
// script's own "the download carries no ..." check). `gh attestation verify <file> ...`
// appends its whole argument list to $FAKE_GH/attest.log, then succeeds
// only when the file's sha256 is a line of $FAKE_GH/attested: an
// attestation, as GitHub stores one, is bound to the artifact's digest.
// It accepts any flags; the cases that matter assert the exact argument
// list from attest.log instead, so a flag the script drops is a failing
// assertion, not a silently lenient fake.
const _fakeGh = `#!/bin/sh
[ "${GH_HOST:-}" = "github.com" ] || { echo "fake gh: GH_HOST is '${GH_HOST:-}', not pinned to github.com" >&2; exit 98; }
case "$1 $2" in
"--version ")
	if [ -f "$FAKE_GH/version" ]; then cat "$FAKE_GH/version"; else echo "gh version ` + _fakeGhVersion + ` (2026-09-01)"; fi
	echo "https://github.com/cli/cli/releases/latest"
	;;
"release download")
	case " $* " in *" -R brunovenceslau/canga "*) ;; *) echo "fake gh: no -R brunovenceslau/canga" >&2; exit 99 ;; esac
	tag="$3" dest="" prev="" names=""
	for a in "$@"; do
		case "$prev" in
		-D) dest="$a" ;;
		-p) names="$names $a" ;;
		esac
		prev="$a"
	done
	[ -n "$dest" ] && [ -n "$names" ] || { echo "fake gh: release download without -D or -p: $*" >&2; exit 99; }
	for n in $names; do
		if [ ! -f "$FAKE_GH/$tag.files/$n" ]; then
			[ -f "$FAKE_GH/download-partial" ] && continue
			echo "no assets match the file pattern" >&2
			exit 1
		fi
		cat "$FAKE_GH/$tag.files/$n" >"$dest/$n" || exit 1
	done
	;;
"attestation verify")
	printf '%s\n' "$*" >>"$FAKE_GH/attest.log"
	sum=$( (sha256sum "$3" 2>/dev/null || shasum -a 256 "$3") | awk '{ print $1 }')
	[ -f "$FAKE_GH/attested" ] && grep -qx "$sum" "$FAKE_GH/attested" || {
		echo "fake gh: no attestation found for $3 (sha256:$sum)" >&2
		exit 1
	}
	echo "fake gh: verified $3"
	;;
"api repos/brunovenceslau/canga")
	[ "$3 $4" = "--jq .full_name" ] || { echo "fake gh: unexpected: $*" >&2; exit 99; }
	[ ! -f "$FAKE_GH/repo-404" ] || { echo "gh: Not Found (HTTP 404)" >&2; exit 1; }
	if [ -f "$FAKE_GH/full_name" ]; then cat "$FAKE_GH/full_name"; else echo brunovenceslau/canga; fi
	;;
"release list")
	[ -z "${FAKE_GH_SLEEP:-}" ] || { : >"$FAKE_GH/release-list.started"; sleep "${FAKE_GH_SLEEP}"; }
	case " $* " in *" -R brunovenceslau/canga "*) ;; *) echo "fake gh: no -R brunovenceslau/canga" >&2; exit 99 ;; esac
	case " $* " in *" --exclude-drafts "*) ;; *) echo "fake gh: no --exclude-drafts" >&2; exit 99 ;; esac
	case " $* " in *" --exclude-pre-releases "*) ;; *) echo "fake gh: no --exclude-pre-releases" >&2; exit 99 ;; esac
	[ -f "$FAKE_GH/tags" ] || { echo "gh: HTTP 401" >&2; exit 1; }
	cat "$FAKE_GH/tags"
	;;
"api repos/brunovenceslau/canga/immutable-releases")
	[ "$3 $4" = "--jq .enabled" ] || { echo "fake gh: unexpected: $*" >&2; exit 99; }
	[ -f "$FAKE_GH/immutable" ] || { echo "gh: Not Found (HTTP 404)" >&2; exit 1; }
	cat "$FAKE_GH/immutable"
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

// _botUploader is the only uploader check_published accepts for a
// canga-sandbox_ asset: the identity every workflow's default GITHUB_TOKEN
// uploads under in this repository - not proof it was specifically the
// Release workflow (see check_published's own comment in the script).
const _botUploader = "github-actions[bot]"

// _noUploader is a table-driven case's sentinel for "GitHub reports no
// uploader for this asset", distinct from that same case's zero value
// (which instead means "default to _botUploader, the passing case").
const _noUploader = "\x00 no uploader"

// Refusal messages goconst would otherwise ask to be named, repeated across
// several tables in this file: check_published's digest-mismatch and
// uploader-mismatch wording, and sandbox_sum's "found the wrong number of
// matching lines" wording (shared by a missing arch, another version's
// lines, and a checksums.txt built for an entirely different release).
const (
	_wantDigestMismatch   = "not the ones pinned"
	_wantUploaderMismatch = "not github-actions[bot]"
	_wantSandboxSumZero   = "found 0"
)

// naOr is s, or the "-" release_assets's own --jq prints for a field GitHub
// did not report (see assets_jq in the script), when s is empty. Tests pass
// "" for "GitHub did not report this", exactly as before this field grew a
// third column; naOr is what keeps that convention working now that a
// missing field must never collapse into its neighbour under awk's default
// whitespace splitting.
func naOr(s string) string {
	if s == "" {
		return "-"
	}

	return s
}

// assetsBody is one release's assets in release_assets's own shape: a
// "_meta false false" line (not a draft, not a prerelease - see
// assetsBodyMeta for the cases that need something else there), then one
// "<name> <digest> <uploader>" line per asset (see release_jq in the
// script). An empty amd64, arm64 or uploader is one GitHub did not report.
func assetsBody(version, amd64, arm64, uploader string) string {
	return assetsBodyMeta(version, amd64, arm64, uploader, false, false)
}

// metaLine is release_jq's own leading "_meta <draft> <prerelease>" line,
// built from the two booleans rather than written out literally: a literal
// "_meta false false" reads as an accidental duplicate word to a linter,
// and this is the one spelling every caller (assetsBodyMeta, the two raw
// fixtures in TestSbxKitPin_Rewrite_MalformedAssetName, and
// TestSbxKitPin_ReleaseJQ's own assertions) shares.
func metaLine(draft, prerelease bool) string {
	return fmt.Sprintf("_meta %t %t", draft, prerelease)
}

// assetsBodyMeta is assetsBody with the release's own draft/prerelease
// state given explicitly, for the cases that exercise release_assets's
// draft/prerelease refusal itself.
func assetsBodyMeta(version, amd64, arm64, uploader string, draft, prerelease bool) string {
	var b strings.Builder

	b.WriteString(metaLine(draft, prerelease) + "\n")
	fmt.Fprintf(&b, "canga-host_%s_linux_amd64.tar.gz sha256:%s %s\n", version, strings.Repeat("0", 64), naOr(uploader))

	for arch, digest := range map[string]string{_amd64: amd64, _arm64: arm64} {
		fmt.Fprintf(&b, "canga-sandbox_%s_linux_%s.tar.gz %s %s\n", version, arch, naOr(digest), naOr(uploader))
	}

	fmt.Fprintf(&b, "checksums.txt sha256:%s %s\n", strings.Repeat("1", 64), naOr(uploader))

	return b.String()
}

// setAssets makes the fake gh serve tag's release with the canga-sandbox_
// archives of version at those digests, uploaded by uploader; an empty
// digest or uploader is one GitHub did not report.
func (w pinWorld) setAssets(t *testing.T, tag, version, amd64, arm64, uploader string) {
	t.Helper()
	writeFile(t, filepath.Join(w.gh, tag+".assets"), assetsBody(version, amd64, arm64, uploader))
}

// _attestedFrom and _ghAttestFloor mirror the script's attested_from and
// gh_attest_floor: the first release that must carry an attestation, and
// the oldest gh trusted to verify one.
const (
	_attestedFrom  = "0.10.5"
	_ghAttestFloor = "2.93.0"
)

// _fakeGhVersion is what the fake gh reports for `gh --version` unless a
// case writes $FAKE_GH/version: new enough for the script's attestation
// floor (gh_attest_floor in the script).
const _fakeGhVersion = "2.101.0"

// _releaseWorkflow is the one workflow whose build provenance attestation
// the script accepts, as the signing certificate's identity spells it
// (`gh attestation verify --cert-identity`), without the @ref the script
// appends.
const _releaseWorkflow = "https://github.com/brunovenceslau/canga/.github/workflows/release.yml"

// serveArchives makes the fake gh at gh serve tag's two canga-sandbox_
// archives of version for `gh release download`, with bodies of their own,
// and returns each one's sha256 as bare hex. Nothing is attested yet; see
// attest.
func serveArchives(t *testing.T, gh, tag, version string) (amd64, arm64 string) {
	t.Helper()

	dir := filepath.Join(gh, tag+".files")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	sums := make(map[string]string, 2)

	for _, arch := range []string{_amd64, _arm64} {
		body := "the " + arch + " archive of " + tag
		writeFile(t, filepath.Join(dir, "canga-sandbox_"+version+"_linux_"+arch+".tar.gz"), body)

		sum := sha256.Sum256([]byte(body))
		sums[arch] = hex.EncodeToString(sum[:])
	}

	return sums[_amd64], sums[_arm64]
}

// attest makes the fake gh at gh verify a build provenance attestation for
// every file whose sha256 is one of sums.
func attest(t *testing.T, gh string, sums ...string) {
	t.Helper()

	f, err := os.OpenFile(filepath.Join(gh, "attested"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	require.NoError(t, err)

	defer func() { require.NoError(t, f.Close()) }()

	for _, sum := range sums {
		_, err := fmt.Fprintln(f, sum)
		require.NoError(t, err)
	}
}

// attestCalls is every `gh attestation verify` the fake gh at gh answered,
// in order, with the artifact's path cut down to its file name (the script
// downloads it into a scratch directory of its own). None is nil.
func attestCalls(t *testing.T, gh string) []string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(gh, "attest.log"))
	if os.IsNotExist(err) {
		return nil
	}

	require.NoError(t, err)

	var calls []string

	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		require.GreaterOrEqual(t, len(fields), 3, "attest.log line %q", line)
		fields[2] = filepath.Base(fields[2])
		calls = append(calls, strings.Join(fields, " "))
	}

	return calls
}

// wantAttestCalls is the exact `gh attestation verify` pair check_published
// must make for tag's two canga-sandbox_ archives of version: amd64 first,
// then arm64, each bound to this repository, to exactly release.yml as run
// for that very tag, and to a GitHub-hosted runner.
func wantAttestCalls(tag, version string) []string {
	calls := make([]string, 0, 2)
	for _, arch := range []string{_amd64, _arm64} {
		calls = append(calls, wantAttestCall(tag, "canga-sandbox_"+version+"_linux_"+arch+".tar.gz"))
	}

	return calls
}

// wantAttestCall is the exact `gh attestation verify` the script makes for
// one file named name, as attestCalls reports it.
func wantAttestCall(tag, name string) string {
	return "attestation verify " + name +
		" --repo brunovenceslau/canga" +
		" --cert-identity " + _releaseWorkflow + "@refs/tags/" + tag +
		" --source-ref refs/tags/" + tag +
		" --deny-self-hosted-runners"
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
			want: _wantSandboxSumZero,
		},
		{
			name: "amd64 line twice", version: _bumpVersion,
			sums: checksums(sandboxLine(_sumAMD64, _amd64), sandboxLine(_sumAMD64, _amd64), sandboxLine(_sumARM64, _arm64)),
			want: "found 2",
		},
		{
			name: "another version's lines", version: "9.8.8", sums: good,
			want: _wantSandboxSumZero,
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

// rewriteGhEnv is the environment one TestSbxKitPin_Rewrite case runs
// under: a fresh fake gh serving "v"+version's release with exactly those
// canga-sandbox_ archs' sha256 (bare hex, as sandbox_sum itself prints; an
// empty one is a digest GitHub did not report) and uploader (rewrite always
// checks against them; see cmd_rewrite in the script).
func rewriteGhEnv(t *testing.T, bin, version, amd64, arm64, uploader string) scrubbedEnv {
	t.Helper()

	sha256Prefixed := func(sum string) string {
		if sum == "" {
			return ""
		}

		return "sha256:" + sum
	}

	return rewriteGhEnvRaw(t, bin, version, assetsBody(version, sha256Prefixed(amd64), sha256Prefixed(arm64), uploader))
}

// rewriteGhEnvRaw is rewriteGhEnv with the release's ".assets" fixture body
// given verbatim, for a case assetsBody's own shape cannot express (a
// duplicate asset name, an asset name with an embedded space, a
// draft/prerelease _meta line - see assetsBodyMeta for that one instead).
func rewriteGhEnvRaw(t *testing.T, bin, version, body string) scrubbedEnv {
	t.Helper()

	gh := t.TempDir()
	writeFile(t, filepath.Join(gh, "v"+version+".assets"), body)

	return childEnv(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_GH="+gh,
	)
}

// rewriteGhEnvAttested is rewriteGhEnv for a release that passes every
// check_published step, build provenance included: the fake gh serves
// "v"+version's two canga-sandbox_ archives, their digests as uploaded by
// github-actions[bot], and an attestation for each. It returns each
// archive's sha256 as bare hex, the sums a checksums.txt must carry.
func rewriteGhEnvAttested(t *testing.T, bin, version string) (env scrubbedEnv, amd64, arm64 string) {
	t.Helper()

	gh := t.TempDir()
	amd64, arm64 = serveArchives(t, gh, "v"+version, version)
	attest(t, gh, amd64, arm64)
	writeFile(t, filepath.Join(gh, "v"+version+".assets"), assetsBody(version, "sha256:"+amd64, "sha256:"+arm64, _botUploader))

	return childEnv(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_GH="+gh,
	), amd64, arm64
}

// copyKit writes body, or the committed kit, to dir/sbx-kit/spec.yaml.
func copyKit(t *testing.T, dir, body string) string {
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

// TestSbxKitPin_Rewrite moves a copy of the kit to a fixture release, and
// checks the refusals leave the kit byte-for-byte untouched. rewrite always
// verifies the sums it is given against the release's published digests
// (cmd_rewrite calls check_published before writing anything), so every
// case here runs against a fake gh, the same one TestSbxKitPin_CheckPrevious
// and TestSbxKitPin_Bump use. TestSbxKitPin_Rewrite_PublishedDigest is this
// test's sibling for the published-digest and uploader refusals themselves.
//
// fakeGhBin runs before t.Parallel: see its comment.
func TestSbxKitPin_Rewrite(t *testing.T) {
	bin := fakeGhBin(t)
	t.Parallel()

	// The fixture every success case and every kit-refusal case (all of
	// which use sums: good, matching this release's own digests and
	// attested archives) runs against. A case whose checksums.txt or
	// version is itself malformed (sandbox_sum fails first) never reaches
	// check_published, so it does not need a matching release and this
	// fixture is harmless for it too.
	env, sumAMD64, sumARM64 := rewriteGhEnvAttested(t, bin, _bumpVersion)
	good := checksums(sandboxLine(sumAMD64, _amd64), sandboxLine(sumARM64, _arm64))

	t.Run("rewrites the version and both sums, and nothing else", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		kit := copyKit(t, dir, "")
		before, err := os.ReadFile(kit)
		require.NoError(t, err)

		sums := writeFile(t, filepath.Join(dir, "checksums.txt"), good)
		res := runPin(t, dir, env, "rewrite", _bumpVersion, sums)
		require.Equal(t, 0, res.exit, res.stderr)
		assert.Contains(t, res.stdout, "verified against release v"+_bumpVersion+"'s published digests")

		after, err := os.ReadFile(kit)
		require.NoError(t, err)

		pin := parseKitPin(kitInstallScript(t, kit))
		assert.Equal(t, []string{_bumpVersion}, pin.versions)
		assert.Equal(t, map[string][]string{_amd64: {sumAMD64}, _arm64: {sumARM64}}, pin.sums)
		assert.Equal(t, pinKit(t, string(before), _bumpVersion, sumAMD64, sumARM64), string(after),
			"only the three pin values change")

		info, err := os.Stat(kit)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())

		// Idempotent: a second run changes nothing.
		require.Equal(t, 0, runPin(t, dir, env, "rewrite", _bumpVersion, sums).exit)

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
			res := runPin(t, dir, env, "rewrite", tt.version, sums)
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

// TestSbxKitPin_Rewrite_PublishedDigest is TestSbxKitPin_Rewrite's sibling
// for the check cmd_rewrite's check_published call itself adds: each case
// below is a published-digest or uploader state rewrite must refuse, given
// checksums.txt inputs (good, from TestSbxKitPin_Rewrite) that pass every
// pre-existing local check on their own - proving check_published actually
// runs and actually blocks the write, not only the local checks that test
// covers.
//
// fakeGhBin runs before t.Parallel: see its comment.
func TestSbxKitPin_Rewrite_PublishedDigest(t *testing.T) {
	bin := fakeGhBin(t)
	t.Parallel()

	good := checksums(sandboxLine(_sumAMD64, _amd64), sandboxLine(_sumARM64, _arm64))

	for _, tt := range []struct {
		name, amd64, arm64, uploader, want, wantNot string
	}{
		{
			name: "a digest GitHub does not confirm", amd64: strings.Repeat("c3", 32), arm64: _sumARM64, uploader: _botUploader,
			want: _wantDigestMismatch,
		},
		{
			name: "a digest GitHub does not report", amd64: "", arm64: _sumARM64, uploader: _botUploader,
			want: "reports no digest",
		},
		{
			name: "an asset uploaded by someone other than github-actions[bot]", amd64: _sumAMD64, arm64: _sumARM64, uploader: "a-human",
			want: _wantUploaderMismatch,
		},
		{
			name: "an asset GitHub reports no uploader for", amd64: _sumAMD64, arm64: _sumARM64, uploader: "",
			want: "reports no uploader",
		},
		{
			// Both wrong at once: check_published's digest check runs
			// before its uploader check (see its own comment), so this
			// must refuse for the digest, deterministically, never for the
			// uploader - pinning that order down, not just its existence.
			name: "a digest AND an uploader both wrong (the digest error wins)", amd64: strings.Repeat("c3", 32), arm64: _sumARM64, uploader: "a-human",
			want: _wantDigestMismatch, wantNot: _wantUploaderMismatch,
		},
	} {
		t.Run("refuses "+tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			kit := copyKit(t, dir, "")
			before, err := os.ReadFile(kit)
			require.NoError(t, err)

			env := rewriteGhEnv(t, bin, _bumpVersion, tt.amd64, tt.arm64, tt.uploader)
			sums := writeFile(t, filepath.Join(dir, "checksums.txt"), good)
			res := runPin(t, dir, env, "rewrite", _bumpVersion, sums)
			assert.Equal(t, 1, res.exit)
			assert.Contains(t, res.stderr, tt.want)

			if tt.wantNot != "" {
				assert.NotContains(t, res.stderr, tt.wantNot)
			}

			after, err := os.ReadFile(kit)
			require.NoError(t, err)
			assert.Equal(t, string(before), string(after), "a refused rewrite must not touch the kit")

			leftovers, err := filepath.Glob(kit + ".*")
			require.NoError(t, err)
			assert.Empty(t, leftovers, "a refused rewrite must not leave its temp file")
		})
	}
}

// TestSbxKitPin_Rewrite_ReleaseState refuses a draft or a prerelease
// outright, before any digest or uploader is even looked at: rewrite and
// bump take a version/tag directly from the caller (unlike check-previous,
// which only ever compares against published_tags's own list, already
// excluding both), so release_assets's own check is the only thing that
// catches this for them.
//
// fakeGhBin runs before t.Parallel: see its comment.
func TestSbxKitPin_Rewrite_ReleaseState(t *testing.T) {
	bin := fakeGhBin(t)
	t.Parallel()

	good := checksums(sandboxLine(_sumAMD64, _amd64), sandboxLine(_sumARM64, _arm64))

	for _, tt := range []struct {
		name          string
		draft, prerel bool
		want          string
	}{
		{name: "a draft release", draft: true, want: "is a draft"},
		{name: "a prerelease", prerel: true, want: "is a prerelease"},
	} {
		t.Run("refuses "+tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			kit := copyKit(t, dir, "")
			before, err := os.ReadFile(kit)
			require.NoError(t, err)

			body := assetsBodyMeta(_bumpVersion, "sha256:"+_sumAMD64, "sha256:"+_sumARM64, _botUploader, tt.draft, tt.prerel)
			env := rewriteGhEnvRaw(t, bin, _bumpVersion, body)
			sums := writeFile(t, filepath.Join(dir, "checksums.txt"), good)
			res := runPin(t, dir, env, "rewrite", _bumpVersion, sums)
			assert.Equal(t, 1, res.exit)
			assert.Contains(t, res.stderr, tt.want)

			after, err := os.ReadFile(kit)
			require.NoError(t, err)
			assert.Equal(t, string(before), string(after), "a refused rewrite must not touch the kit")
		})
	}
}

// TestSbxKitPin_Rewrite_MalformedAssetName covers check_published's own
// NF == 3 guard: a release_jq line is only ever treated as naming a known
// asset when the WHOLE line splits into exactly three fields, so a name
// that itself somehow contains whitespace (which real GitHub does not
// allow today, but this check does not lean on that alone) can never be
// misread as if trailing words inside it were the digest and uploader.
//
// fakeGhBin runs before t.Parallel: see its comment.
func TestSbxKitPin_Rewrite_MalformedAssetName(t *testing.T) {
	bin := fakeGhBin(t)
	t.Parallel()

	good := checksums(sandboxLine(_sumAMD64, _amd64), sandboxLine(_sumARM64, _arm64))
	arm64Line := "canga-sandbox_" + _bumpVersion + "_linux_arm64.tar.gz sha256:" + _sumARM64 + " " + _botUploader

	t.Run("refuses an asset name with an embedded space, as if it were missing", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		kit := copyKit(t, dir, "")
		before, err := os.ReadFile(kit)
		require.NoError(t, err)

		// A name containing a space, followed by what would be the real
		// digest and uploader if this line's first field alone ($1) were
		// trusted as the match: NF == 3 must refuse this line as a match
		// for the clean name entirely, not read "evil" as the digest.
		amd64Line := "canga-sandbox_" + _bumpVersion + "_linux_amd64.tar.gz evil sha256:" + _sumAMD64 + " " + _botUploader
		body := metaLine(false, false) + "\n" + amd64Line + "\n" + arm64Line + "\n"
		env := rewriteGhEnvRaw(t, bin, _bumpVersion, body)
		sums := writeFile(t, filepath.Join(dir, "checksums.txt"), good)
		res := runPin(t, dir, env, "rewrite", _bumpVersion, sums)
		assert.Equal(t, 1, res.exit)
		assert.Contains(t, res.stderr, "carries no canga-sandbox_"+_bumpVersion+"_linux_amd64.tar.gz at all")

		after, err := os.ReadFile(kit)
		require.NoError(t, err)
		assert.Equal(t, string(before), string(after), "a refused rewrite must not touch the kit")
	})

	t.Run("refuses a release that lists the same asset name twice", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		kit := copyKit(t, dir, "")
		before, err := os.ReadFile(kit)
		require.NoError(t, err)

		amd64Line := "canga-sandbox_" + _bumpVersion + "_linux_amd64.tar.gz sha256:" + _sumAMD64 + " " + _botUploader
		duplicateLine := "canga-sandbox_" + _bumpVersion + "_linux_amd64.tar.gz sha256:" + strings.Repeat("c3", 32) + " " + _botUploader
		body := metaLine(false, false) + "\n" + amd64Line + "\n" + duplicateLine + "\n" + arm64Line + "\n"
		env := rewriteGhEnvRaw(t, bin, _bumpVersion, body)
		sums := writeFile(t, filepath.Join(dir, "checksums.txt"), good)
		res := runPin(t, dir, env, "rewrite", _bumpVersion, sums)
		assert.Equal(t, 1, res.exit)
		assert.Contains(t, res.stderr, "lists 2 assets named canga-sandbox_"+_bumpVersion+"_linux_amd64.tar.gz")

		after, err := os.ReadFile(kit)
		require.NoError(t, err)
		assert.Equal(t, string(before), string(after), "a refused rewrite must not touch the kit")
	})
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
		// The assets' uploader; empty means _botUploader, the passing case.
		uploader  string
		olderLine string // SBX_KIT_OLDER_LINE
		noTags    bool
		exit      int
	}{
		{name: "kit pins the previous release", kit: _prevVersion, tags: []string{_prevTag, _olderTag, _decoyTag}, tag: _nextTag, want: "pins " + _prevTag},
		{name: "the tag's own release is already published", kit: _prevVersion, tags: []string{_nextTag, _prevTag, _decoyTag}, tag: _nextTag, want: "pins " + _prevTag},
		{name: "versions compare as numbers, not strings", kit: "0.10.0", tags: []string{_decoyTag, _olderTag, "v0.8.9"}, tag: _prevTag, assetsTag: _olderTag, want: "pins v0.10.0"},
		{name: "a non-release tag is ignored", kit: _prevVersion, tags: []string{_prevTag, "v1.0", "nightly", "v0.09.9", _decoyTag}, tag: _nextTag, want: "pins " + _prevTag},
		{
			name: "a release on an older line, allowed for it", kit: "0.9.0", tags: []string{_prevTag, _decoyTag}, tag: _olderLineTag, assetsTag: _decoyTag, olderLine: _olderLineTag,
			want: "WARNING: " + _olderLineTag + " is below",
		},
		{name: "a release on an older line, not allowed", kit: "0.9.0", tags: []string{_prevTag, _decoyTag}, tag: _olderLineTag, assetsTag: _decoyTag, exit: 1, want: "set SBX_KIT_OLDER_LINE=" + _olderLineTag},
		{
			name: "a release on an older line, allowed for another tag", kit: "0.9.0", tags: []string{_prevTag, _decoyTag}, tag: _olderLineTag, assetsTag: _decoyTag, olderLine: "v0.9.2", exit: 1,
			want: "set SBX_KIT_OLDER_LINE=" + _olderLineTag,
		},
		{name: "the previous bump never merged", kit: "0.10.0", tags: []string{_prevTag, _olderTag}, tag: _nextTag, exit: 1, want: "chore/sbx-kit-" + _prevTag},
		{name: "the kit already names this tag", kit: _nextVersion, tags: []string{_prevTag}, tag: _nextTag, exit: 1, want: "newest published\nrelease below v0.10.2 is v0.10.1"},
		{name: "an older line pinning an unpublished release", kit: "0.9.5", tags: []string{_prevTag, _decoyTag}, tag: "v0.9.6", olderLine: "v0.9.6", exit: 1, want: "not a published release"},
		{name: "a pinned sum GitHub serves otherwise", kit: _prevVersion, tags: []string{_prevTag}, tag: _nextTag, arm64: "sha256:" + strings.Repeat("c3", 32), exit: 1, want: _wantDigestMismatch},
		{name: "a pinned sum GitHub reports no digest for", kit: _prevVersion, tags: []string{_prevTag}, tag: _nextTag, amd64: " ", exit: 1, want: "reports no digest"},
		{name: "no release below this one", kit: _prevVersion, tags: []string{_nextTag}, tag: _nextTag, exit: 1, want: "no published release below"},
		{name: "gh fails", kit: _prevVersion, tag: _nextTag, noTags: true, exit: 1, want: "could not list"},
		{name: _notATag, kit: _prevVersion, tags: []string{_prevTag}, tag: _nextVersion, exit: 1, want: _notATag},
		{name: "a tag with a leading zero", kit: _prevVersion, tags: []string{_prevTag}, tag: "v0.010.2", exit: 1, want: _notATag},
		{name: "a malformed kit version", kit: "0.10", tags: []string{_prevTag}, tag: _nextTag, exit: 1, want: "is not X.Y.Z"},
		{
			name: "a pinned release uploaded by someone other than github-actions[bot]", kit: _prevVersion, tags: []string{_prevTag}, tag: _nextTag,
			uploader: "a-human", exit: 1, want: _wantUploaderMismatch,
		},
		{
			name: "a pinned release GitHub reports no uploader for", kit: _prevVersion, tags: []string{_prevTag}, tag: _nextTag,
			uploader: _noUploader, exit: 1, want: "reports no uploader",
		},
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

			uploader := _botUploader
			switch tt.uploader {
			case "":
			case _noUploader:
				uploader = ""
			default:
				uploader = tt.uploader
			}

			w.setAssets(t, assetsTag, strings.TrimPrefix(assetsTag, "v"), amd64, arm64, uploader)

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
		w.setAssets(t, _prevTag, _prevVersion, digestAMD64, digestARM64, _botUploader)
		w.writeKit(t, realKit(t, _prevVersion, _sumAMD64, _sumARM64))

		res := w.run(t, "check-previous", _nextTag)
		assert.Equal(t, 1, res.exit, "an uncommitted bump must not satisfy the check")
		assert.Contains(t, res.stderr, `pins CANGA_VERSION="0.10.0"`)
	})
}

// attestedWorld is a pinWorld whose kit, committed at HEAD, pins release
// "v"+version by the real sha256 of two archives the fake gh serves, as
// uploaded by github-actions[bot] - everything check_published asks of a
// release except, until a case calls attest, a build provenance
// attestation.
type attestedWorld struct {
	pinWorld

	tag, version, amd64, arm64 string
}

func newAttestedWorld(t *testing.T, bin, version string) attestedWorld {
	t.Helper()

	gh := t.TempDir()
	tag := "v" + version
	amd64, arm64 := serveArchives(t, gh, tag, version)
	w := attestedWorld{
		pinWorld: newPinWorld(t, bin, realKit(t, version, amd64, arm64), false),
		tag:      tag, version: version, amd64: amd64, arm64: arm64,
	}
	// newPinWorld made w.gh a directory of its own; the archives were
	// served from a scratch one because their sums had to exist before the
	// kit that pins them could be committed. Move them into place.
	require.NoError(t, os.Rename(filepath.Join(gh, tag+".files"), filepath.Join(w.gh, tag+".files")))
	w.setTags(t, tag)
	w.setAssets(t, tag, version, "sha256:"+amd64, "sha256:"+arm64, _botUploader)

	return w
}

// attestBoth attests both archives the kit pins.
func (w attestedWorld) attestBoth(t *testing.T) {
	t.Helper()
	attest(t, w.gh, w.amd64, w.arm64)
}

// nextPatch is the release tag right after version: X.Y.(Z+1).
func nextPatch(t *testing.T, version string) string {
	t.Helper()

	parts := strings.Split(version, ".")
	require.Len(t, parts, 3)

	var patch int

	_, err := fmt.Sscanf(parts[2], "%d", &patch)
	require.NoError(t, err)

	return fmt.Sprintf("v%s.%s.%d", parts[0], parts[1], patch+1)
}

// TestSbxKitPin_Attestation is the build provenance cutover: from
// v0.10.5 on (attested_from in the script), check_published requires each
// canga-sandbox_ archive to carry a build provenance attestation from
// release.yml as run for that release's own tag; a release below it is
// checked by digest and uploader alone, as before. check-previous drives
// these cases, because its kit is the one the preflight trusts; rewrite and
// bump share the same check_published (TestSbxKitPin_Bump's own cases run
// above the cutover, attested, and TestSbxKitPin_Attestation_Rewrite
// closes the loop). TestSbxKitPin_Attestation_Refusals has the ways an
// attested release can still be refused.
//
// fakeGhBin runs before t.Parallel: see its comment.
func TestSbxKitPin_Attestation(t *testing.T) {
	bin := fakeGhBin(t)
	t.Parallel()

	// Compared as numbers, never as strings: "0.10.10" sorts below
	// "0.10.5" as text, and "1.0.0" below "0.9.99" is what a
	// component-by-component string compare gets wrong the other way.
	for _, tt := range []struct {
		version  string
		required bool
	}{
		{version: "0.9.99"},
		{version: "0.10.4"},
		{version: _attestedFrom, required: true},
		{version: "0.10.10", required: true},
		{version: "0.11.0", required: true},
		{version: "1.0.0", required: true},
	} {
		next := nextPatch(t, tt.version)

		t.Run(tt.version+" attested passes, with exactly the expected verification", func(t *testing.T) {
			t.Parallel()

			w := newAttestedWorld(t, bin, tt.version)
			w.attestBoth(t)

			res := w.run(t, "check-previous", next)
			require.Equal(t, 0, res.exit, res.stderr)
			assert.Contains(t, res.stdout, "pins "+w.tag+", the newest published release below "+next)

			if tt.required {
				assert.Equal(t, wantAttestCalls(w.tag, tt.version), attestCalls(t, w.gh))
				assert.Contains(t, res.stdout, "carries a build provenance attestation")
			} else {
				assert.Empty(t, attestCalls(t, w.gh), "a release below the cutover is never asked for an attestation")
				assert.Contains(t, res.stdout, "below v0.10.5")
			}

			assert.Empty(t, w.leftovers(t))
		})

		t.Run(tt.version+" without an attestation", func(t *testing.T) {
			t.Parallel()

			w := newAttestedWorld(t, bin, tt.version)

			res := w.run(t, "check-previous", next)
			if !tt.required {
				require.Equal(t, 0, res.exit, res.stderr)
				assert.Empty(t, attestCalls(t, w.gh))

				return
			}

			assert.Equal(t, 1, res.exit, res.stdout)
			assert.Contains(t, res.stderr, "release "+w.tag+"'s canga-sandbox_"+tt.version+"_linux_amd64.tar.gz has no build provenance attestation")
			assert.Equal(t, wantAttestCalls(w.tag, tt.version)[:1], attestCalls(t, w.gh),
				"the refusal comes from gh attestation verify itself, on the first archive")
			assert.Empty(t, w.leftovers(t))
		})
	}

	t.Run("a release below the cutover never asks gh its version", func(t *testing.T) {
		t.Parallel()

		w := newAttestedWorld(t, bin, "0.10.4")
		// A gh too old to verify anything: irrelevant below the cutover.
		writeFile(t, filepath.Join(w.gh, "version"), "gh version 2.46.0 (2025-12-13 Ubuntu 2.46.0-4)\n")

		res := w.run(t, "check-previous", "v0.10.5")
		require.Equal(t, 0, res.exit, res.stderr)
		assert.Empty(t, attestCalls(t, w.gh))
	})

	t.Run("a gh exactly at the attestation floor passes", func(t *testing.T) {
		t.Parallel()

		w := newAttestedWorld(t, bin, _attestedFrom)
		w.attestBoth(t)
		writeFile(t, filepath.Join(w.gh, "version"), "gh version 2.93.0 (2026-06-01)\n")

		res := w.run(t, "check-previous", "v0.10.6")
		require.Equal(t, 0, res.exit, res.stderr)
		assert.Equal(t, wantAttestCalls(w.tag, w.version), attestCalls(t, w.gh))
	})
}

// TestSbxKitPin_Attestation_Refusals is every way a release at the
// cutover is refused around the attestation itself: gh's own verdict on
// one archive of two, a gh too old or unreadable to trust, archives the
// release cannot serve, and downloaded bytes that are not the pinned ones.
//
// fakeGhBin runs before t.Parallel: see its comment.
func TestSbxKitPin_Attestation_Refusals(t *testing.T) {
	bin := fakeGhBin(t)
	// A mkdir that always fails, for the one case that puts it first on
	// PATH: the script's only mkdir is check_attested's (mktemp makes its
	// own directories). Written before t.Parallel, like fakeGhBin.
	failingMkdir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(failingMkdir, "mkdir"), []byte("#!/bin/sh\necho 'mkdir: refused' >&2\nexit 1\n"), 0o755))
	t.Parallel()

	for _, tt := range []struct {
		name, want string
		prepare    func(t *testing.T, w attestedWorld)
	}{
		{
			name: "only one archive attested", want: "canga-sandbox_0.10.5_linux_arm64.tar.gz has no build provenance attestation",
			prepare: func(t *testing.T, w attestedWorld) {
				t.Helper()
				attest(t, w.gh, w.amd64)
			},
		},
		{
			name: "a gh just below the attestation floor", want: "gh 2.92.99 is older than 2.93.0",
			prepare: func(t *testing.T, w attestedWorld) {
				t.Helper()
				w.attestBoth(t)
				writeFile(t, filepath.Join(w.gh, "version"), "gh version 2.92.99 (2026-01-01)\n")
			},
		},
		{
			name: "a partial download gh reports as a failure", want: "could not download",
			prepare: func(t *testing.T, w attestedWorld) {
				t.Helper()
				w.attestBoth(t)
				require.NoError(t, os.Remove(filepath.Join(w.gh, w.tag+".files", "canga-sandbox_0.10.5_linux_arm64.tar.gz")))
			},
		},
		{
			name: "a partial download gh reports as a success", want: "the download carries no canga-sandbox_0.10.5_linux_arm64.tar.gz",
			prepare: func(t *testing.T, w attestedWorld) {
				t.Helper()
				w.attestBoth(t)
				require.NoError(t, os.Remove(filepath.Join(w.gh, w.tag+".files", "canga-sandbox_0.10.5_linux_arm64.tar.gz")))
				writeFile(t, filepath.Join(w.gh, "download-partial"), "")
			},
		},
		{
			name: "a download directory that cannot be created", want: "could not create",
			prepare: func(t *testing.T, w attestedWorld) {
				t.Helper()
				w.attestBoth(t)
				w.env[0] = "PATH=" + failingMkdir + string(os.PathListSeparator) + strings.TrimPrefix(w.env[0], "PATH=")
			},
		},
		{
			name: "a gh whose version cannot be read", want: "could not read gh's version",
			prepare: func(t *testing.T, w attestedWorld) {
				t.Helper()
				w.attestBoth(t)
				writeFile(t, filepath.Join(w.gh, "version"), "gh version DEV\n")
			},
		},
		{
			name: "archives the release cannot serve", want: "could not download",
			prepare: func(t *testing.T, w attestedWorld) {
				t.Helper()
				w.attestBoth(t)
				require.NoError(t, os.RemoveAll(filepath.Join(w.gh, w.tag+".files")))
			},
		},
		{
			// GitHub's reported digest matches the pin, but the bytes it
			// actually serves do not: the download is checked against the
			// pin itself, never trusted because the metadata agreed.
			name: "downloaded bytes that are not the pinned ones", want: "is sha256:",
			prepare: func(t *testing.T, w attestedWorld) {
				t.Helper()
				w.attestBoth(t)

				other := "other bytes"
				sum := sha256.Sum256([]byte(other))
				attest(t, w.gh, hex.EncodeToString(sum[:]))
				writeFile(t, filepath.Join(w.gh, w.tag+".files", "canga-sandbox_0.10.5_linux_amd64.tar.gz"), other)
			},
		},
	} {
		t.Run("refuses "+tt.name, func(t *testing.T) {
			t.Parallel()

			w := newAttestedWorld(t, bin, _attestedFrom)
			tt.prepare(t, w)

			res := w.run(t, "check-previous", "v0.10.6")
			assert.Equal(t, 1, res.exit, res.stdout)
			assert.Contains(t, res.stderr, tt.want)
			assert.Empty(t, w.leftovers(t))
		})
	}
}

// TestSbxKitPin_Attestation_Rewrite is rewrite at the cutover: it writes
// the pin only for attested archives, and leaves the kit untouched
// otherwise.
//
// fakeGhBin runs before t.Parallel: see its comment.
func TestSbxKitPin_Attestation_Rewrite(t *testing.T) {
	bin := fakeGhBin(t)
	t.Parallel()

	for _, attested := range []bool{true, false} {
		t.Run(fmt.Sprintf("attested=%t", attested), func(t *testing.T) {
			t.Parallel()

			gh := t.TempDir()
			amd64, arm64 := serveArchives(t, gh, "v0.10.5", _attestedFrom)
			writeFile(t, filepath.Join(gh, "v0.10.5.assets"), assetsBody(_attestedFrom, "sha256:"+amd64, "sha256:"+arm64, _botUploader))

			if attested {
				attest(t, gh, amd64, arm64)
			}

			env := childEnv(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "FAKE_GH="+gh)
			dir := t.TempDir()
			kit := copyKit(t, dir, "")
			before, err := os.ReadFile(kit)
			require.NoError(t, err)

			line := func(sum, arch string) string {
				return sum + "  canga-sandbox_0.10.5_linux_" + arch + ".tar.gz"
			}
			sums := writeFile(t, filepath.Join(dir, "checksums.txt"), line(amd64, _amd64)+"\n"+line(arm64, _arm64)+"\n")
			res := runPin(t, dir, env, "rewrite", _attestedFrom, sums)

			after, err := os.ReadFile(kit)
			require.NoError(t, err)

			if attested {
				require.Equal(t, 0, res.exit, res.stderr)
				assert.Equal(t, pinKit(t, string(before), _attestedFrom, amd64, arm64), string(after))
				assert.Equal(t, wantAttestCalls("v0.10.5", _attestedFrom), attestCalls(t, gh))
			} else {
				assert.Equal(t, 1, res.exit, res.stdout)
				assert.Contains(t, res.stderr, "has no build provenance attestation")
				assert.Equal(t, string(before), string(after), "a refused rewrite must not touch the kit")
			}
		})
	}
}

// TestSbxKitPin_CheckAttestation is the Release workflow's pre-publish
// check: each local file must verify against release.yml as run for the
// tag, with exactly the flags the kit pin checks use, and under the same
// gh floor.
//
// fakeGhBin runs before t.Parallel: see its comment.
func TestSbxKitPin_CheckAttestation(t *testing.T) {
	bin := fakeGhBin(t)
	t.Parallel()

	for _, tt := range []struct {
		name, version, want string
		attestSecond        bool
		missing             bool
		args                []string
		exit                int
	}{
		{name: "every file attested", attestSecond: true, want: "checksums.txt carries a build provenance attestation"},
		{name: "one file not attested", exit: 1, want: "checksums.txt has no build provenance attestation"},
		{name: "a gh below the floor", attestSecond: true, version: "gh version 2.46.0 (2025-12-13)\n", exit: 1, want: "gh 2.46.0 is older than 2.93.0"},
		{name: "a file that does not exist", attestSecond: true, missing: true, exit: 1, want: "not found"},
		{name: "no file at all", args: []string{"check-attestation", "v0.10.5"}, exit: 1, want: "usage: check-attestation"},
		{name: "not a tag", args: []string{"check-attestation", _attestedFrom, "x"}, exit: 1, want: "not a release tag"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := newPinWorld(t, bin, realKit(t, _prevVersion, _sumAMD64, _sumARM64), false)
			dist := t.TempDir()
			first := writeFile(t, filepath.Join(dist, "canga-sandbox_0.10.5_linux_amd64.tar.gz"), "first")
			second := writeFile(t, filepath.Join(dist, "checksums.txt"), "second")

			for _, pair := range []struct {
				body string
				on   bool
			}{{"first", true}, {"second", tt.attestSecond}} {
				if pair.on {
					sum := sha256.Sum256([]byte(pair.body))
					attest(t, w.gh, hex.EncodeToString(sum[:]))
				}
			}

			if tt.version != "" {
				writeFile(t, filepath.Join(w.gh, "version"), tt.version)
			}

			if tt.missing {
				require.NoError(t, os.Remove(second))
			}

			args := tt.args
			if args == nil {
				args = []string{"check-attestation", "v0.10.5", first, second}
			}

			res := w.run(t, args...)
			assert.Equal(t, tt.exit, res.exit, res.stderr)
			assert.Contains(t, res.stdout+res.stderr, tt.want)
			assert.Empty(t, w.leftovers(t))

			if tt.exit == 0 {
				assert.Equal(t, []string{
					wantAttestCall("v0.10.5", "canga-sandbox_0.10.5_linux_amd64.tar.gz"),
					wantAttestCall("v0.10.5", "checksums.txt"),
				}, attestCalls(t, w.gh))
			}
		})
	}
}

// TestSbxKitPin_CheckImmutable is release-preflight's immutable-releases
// check: on passes, off refuses, and a token GitHub will not answer (a
// 404, which is what a non-admin token gets) refuses too, never read as on.
//
// fakeGhBin runs before t.Parallel: see its comment.
func TestSbxKitPin_CheckImmutable(t *testing.T) {
	bin := fakeGhBin(t)
	t.Parallel()

	for _, tt := range []struct {
		name, answer, want string
		noAnswer           bool
		args               []string
		exit               int
	}{
		{name: "on", answer: "true\n", want: "has immutable releases on"},
		{name: "off", answer: "false\n", exit: 1, want: "does not have immutable releases on (GitHub says enabled=false)"},
		{name: "an empty answer", answer: "", exit: 1, want: "enabled=nothing"},
		{name: "a token GitHub will not answer", noAnswer: true, exit: 1, want: "only to a token with admin access"},
		{name: "an argument", answer: "true\n", args: []string{"extra"}, exit: 1, want: "usage: check-immutable"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := newPinWorld(t, bin, realKit(t, _prevVersion, _sumAMD64, _sumARM64), false)
			if !tt.noAnswer {
				writeFile(t, filepath.Join(w.gh, "immutable"), tt.answer)
			}

			res := w.run(t, append([]string{"check-immutable"}, tt.args...)...)
			assert.Equal(t, tt.exit, res.exit, res.stderr)
			assert.Contains(t, res.stdout+res.stderr, tt.want)
		})
	}
}

// scriptFunc is the text of one shell function from scripts/sbx-kit-pin.sh,
// from its "name() {" line to the first line that is exactly "}", so a
// test can run that very function, not a copy that could drift from it.
func scriptFunc(t *testing.T, name string) string {
	t.Helper()

	data, err := os.ReadFile(_pinScript)
	require.NoError(t, err)

	text := string(data)
	start := strings.Index(text, "\n"+name+"() {\n")
	require.NotEqual(t, -1, start, "no function %s in the script", name)

	end := strings.Index(text[start:], "\n}\n")
	require.NotEqual(t, -1, end, "function %s never ends", name)

	return text[start+1 : start+end+3]
}

// TestSbxKitPin_VersionAtLeast runs the script's own version_at_least (and
// the die it refuses through) directly: numeric, per component, and closed
// on failure - an awk that crashes or answers nothing is a refusal, never
// "below", which is what would skip the attestation check.
//
// The fake awks are written before t.Parallel: see fakeGhBin's comment.
func TestSbxKitPin_VersionAtLeast(t *testing.T) {
	badAwks := []struct{ name, awk, dir string }{
		{name: "an awk that crashes", awk: "#!/bin/sh\nexit 2\n"},
		{name: "an awk that answers nothing", awk: "#!/bin/sh\nexit 0\n"},
		{name: "an awk that answers something else", awk: "#!/bin/sh\necho maybe\n"},
	}
	for i := range badAwks {
		badAwks[i].dir = t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(badAwks[i].dir, "awk"), []byte(badAwks[i].awk), 0o755))
	}

	t.Parallel()

	lib := scriptFunc(t, "die") + scriptFunc(t, "version_at_least")

	run := func(t *testing.T, path, a, b string) pinRun {
		t.Helper()

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "sh", "-c", lib+`version_at_least "$1" "$2"`, "sh", a, b)
		cmd.Env = childEnv(os.Environ(), "PATH="+path).asEnv()

		var stdout, stderr bytes.Buffer

		cmd.Stdout, cmd.Stderr = &stdout, &stderr

		var res pinRun

		if err := cmd.Run(); err != nil {
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit, stderr.String())
			res.exit = exit.ExitCode()
		}

		res.stdout, res.stderr = stdout.String(), stderr.String()

		return res
	}

	for _, tt := range []struct {
		a, b string
		ge   bool
	}{
		{a: _attestedFrom, b: _attestedFrom, ge: true},
		{a: "0.10.4", b: _attestedFrom},
		{a: "0.10.10", b: _attestedFrom, ge: true},
		{a: "0.9.99", b: _attestedFrom},
		{a: "0.11.0", b: _attestedFrom, ge: true},
		{a: "1.0.0", b: "0.99.99", ge: true},
		{a: "0.99.99", b: "1.0.0"},
		{a: _ghAttestFloor, b: _ghAttestFloor, ge: true},
		{a: "2.92.99", b: _ghAttestFloor},
		{a: "2.101.0", b: _ghAttestFloor, ge: true},
	} {
		t.Run(tt.a+" vs "+tt.b, func(t *testing.T) {
			t.Parallel()

			want := 1
			if tt.ge {
				want = 0
			}

			res := run(t, os.Getenv("PATH"), tt.a, tt.b)
			assert.Equal(t, want, res.exit, res.stderr)
			assert.Empty(t, res.stderr)
		})
	}

	for _, tt := range badAwks {
		t.Run("refuses "+tt.name, func(t *testing.T) {
			t.Parallel()

			// 0.10.4 vs 0.10.5 is "below": a fail-open version_at_least
			// would return 1 here too, silently, which is exactly what
			// skips the attestation; only the refusal message tells the
			// two apart.
			res := run(t, tt.dir+string(os.PathListSeparator)+os.Getenv("PATH"), "0.10.4", _attestedFrom)
			assert.Equal(t, 1, res.exit)
			assert.Contains(t, res.stderr, "could not compare versions 0.10.4 and 0.10.5")
		})
	}
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
				w.setAssets(t, _nextTag, _nextVersion, digestAMD64, digestARM64, _botUploader)
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
	w.setAssets(t, _bumpTag, _bumpVersion, "sha256:"+w.amd64, "sha256:"+w.arm64, _botUploader)

	// _bumpVersion is above the script's attestation cutover, so bump
	// downloads both archives from the release and verifies their build
	// provenance too: serve the same bytes dist/ holds, attested.
	files := filepath.Join(w.gh, _bumpTag+".files")
	require.NoError(t, os.MkdirAll(files, 0o755))

	for _, arch := range []string{_amd64, _arm64} {
		body, err := os.ReadFile(w.archive(arch))
		require.NoError(t, err)
		writeFile(t, filepath.Join(files, filepath.Base(w.archive(arch))), string(body))
	}

	attest(t, w.gh, w.amd64, w.arm64)

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
		{name: "a checksums.txt missing an arch", want: _wantSandboxSumZero, prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			writeFile(t, w.sums, checksums(sandboxLine(w.amd64, _amd64)))
		}},
		{
			// Cross-version confusion: a checksums.txt genuinely built for
			// a different release (well-formed, both archs present) fed to
			// bump under _bumpTag/_bumpVersion. sandbox_sum matches by
			// filename equality against the version bump was actually
			// invoked with, so a file naming only 9.8.8 has zero lines for
			// 9.8.7 and is refused the same way a missing arch is - never
			// silently accepted as if it were this release's own file.
			name: "a checksums.txt built for a different version", want: _wantSandboxSumZero, prepare: func(t *testing.T, w *bumpWorld) {
				t.Helper()

				otherVersion := func(sum, arch string) string {
					return fmt.Sprintf("%s  canga-sandbox_9.8.8_linux_%s.tar.gz", sum, arch)
				}
				writeFile(t, w.sums, checksums(otherVersion(w.amd64, _amd64), otherVersion(w.arm64, _arm64)))
			},
		},
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
		{name: "a release serving other bytes", want: _wantDigestMismatch, prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			w.setAssets(t, _bumpTag, _bumpVersion, "sha256:"+w.amd64, "sha256:"+strings.Repeat("c3", 32), _botUploader)
		}},
		{name: "a release reporting no digest", want: "reports no digest", prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			w.setAssets(t, _bumpTag, _bumpVersion, "", "sha256:"+w.arm64, _botUploader)
		}},
		{name: "a release uploaded by someone other than github-actions[bot]", want: _wantUploaderMismatch, prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			w.setAssets(t, _bumpTag, _bumpVersion, "sha256:"+w.amd64, "sha256:"+w.arm64, "a-human")
		}},
		{name: "a release GitHub reports no uploader for", want: "reports no uploader", prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			w.setAssets(t, _bumpTag, _bumpVersion, "sha256:"+w.amd64, "sha256:"+w.arm64, "")
		}},
		{name: "a release without a build provenance attestation", want: "no build provenance attestation", prepare: func(t *testing.T, w *bumpWorld) {
			t.Helper()
			require.NoError(t, os.Remove(filepath.Join(w.gh, "attested")))
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
		assert.Equal(t, wantAttestCalls(_bumpTag, _bumpVersion), attestCalls(t, w.gh),
			"bump verifies both archives' build provenance, with exactly these flags")
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

// scriptJQVars prints scripts/sbx-kit-pin.sh's own assets_jq and release_jq,
// exactly as gh --jq would receive them: it sources everything in the
// script up to (not including) its final dispatch line - which never runs a
// command and so cannot fail for lacking one - into a real sh, and asks
// that sh to print the two variables. This is the actual shell resolving
// or_dash_def's substitution and every escape the same way the script
// itself does, so the test can never silently drift from what gh --jq
// actually runs, the way a hand-copied jq string could.
func scriptJQVars(t *testing.T) (assetsJQ, releaseJQ string) {
	t.Helper()

	data, err := os.ReadFile(_pinScript)
	require.NoError(t, err)

	src := string(data)
	marker := `[ $# -ge 1 ] || die "usage:`
	idx := strings.Index(src, marker)
	require.NotEqual(t, -1, idx, "sbx-kit-pin.sh's dispatch line moved; update this test's marker")

	dir := t.TempDir()
	path := filepath.Join(dir, "prefix.sh")
	require.NoError(t, os.WriteFile(path, []byte(src[:idx]), 0o644))

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", `. "$1"; printf '%s\0%s' "$assets_jq" "$release_jq"`, "sh", path)

	var stdout, stderr bytes.Buffer

	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	require.NoError(t, cmd.Run(), "sourcing sbx-kit-pin.sh's prefix: %s", stderr.String())

	parts := strings.SplitN(stdout.String(), "\x00", 2)
	require.Len(t, parts, 2, "expected assets_jq\\0release_jq, got: %q", stdout.String())

	return parts[0], parts[1]
}

// runJQ runs jqBin -r <filter>, feeding it doc, and returns its output
// split into lines (empty on empty output, never a slice holding one empty
// string).
func runJQ(t *testing.T, jqBin, filter, doc string) []string {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, jqBin, "-r", filter)
	cmd.Stdin = strings.NewReader(doc)

	var stdout, stderr bytes.Buffer

	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	require.NoError(t, cmd.Run(), "jq -r %q: %s", filter, stderr.String())

	out := strings.TrimSuffix(stdout.String(), "\n")
	if out == "" {
		return nil
	}

	return strings.Split(out, "\n")
}

// TestSbxKitPin_ReleaseJQ pipes a realistic release document through the
// real assets_jq and release_jq strings scripts/sbx-kit-pin.sh actually
// uses (via scriptJQVars, never a hand-copied string), through a real jq
// binary. Every other test in this package feeds the fake gh canned text
// directly and never runs jq at all, so a syntax or semantic mistake in
// either --jq expression would only have surfaced against the real GitHub
// API - this is what closes that gap.
func TestSbxKitPin_ReleaseJQ(t *testing.T) {
	t.Parallel()

	jqBin, err := exec.LookPath("jq")
	require.NoError(t, err, "jq is required for this test; install it (see .github/workflows/test.yml)")

	assetsJQ, releaseJQ := scriptJQVars(t)

	// One asset with a null digest, one with a null uploader.login (the
	// realistic shape: GitHub omits digest/uploader as JSON null, not as
	// ""), one with an empty-string digest AND uploader.login (the shape
	// orDash's "or \"\"" branch exists for), and an unrelated extra asset,
	// all in the one release document both expressions read.
	doc := `{
		"draft": false,
		"prerelease": false,
		"assets": [
			{"name": "canga-sandbox_9.8.7_linux_amd64.tar.gz", "digest": "sha256:aaaa", "uploader": {"login": "github-actions[bot]"}},
			{"name": "canga-sandbox_9.8.7_linux_arm64.tar.gz", "digest": null, "uploader": {"login": "github-actions[bot]"}},
			{"name": "canga-host_9.8.7_linux_amd64.tar.gz", "digest": "", "uploader": {"login": ""}},
			{"name": "checksums.txt", "digest": "sha256:cccc", "uploader": null}
		]
	}`

	want := []string{
		"canga-sandbox_9.8.7_linux_amd64.tar.gz sha256:aaaa github-actions[bot]",
		"canga-sandbox_9.8.7_linux_arm64.tar.gz - github-actions[bot]",
		"canga-host_9.8.7_linux_amd64.tar.gz - -",
		"checksums.txt sha256:cccc -",
	}

	t.Run("assets_jq: null, empty and missing fields all print as -, never empty", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, want, runJQ(t, jqBin, assetsJQ, doc))
	})

	t.Run("release_jq: the same asset lines, behind one _meta line", func(t *testing.T) {
		t.Parallel()

		out := runJQ(t, jqBin, releaseJQ, doc)
		require.NotEmpty(t, out)
		assert.Equal(t, metaLine(false, false), out[0])
		assert.Equal(t, want, out[1:])
	})

	for _, tt := range []struct {
		name          string
		draft, prerel bool
	}{
		{name: "draft", draft: true},
		{name: "prerelease", prerel: true},
		{name: "neither", draft: false, prerel: false},
		{name: "both", draft: true, prerel: true},
	} {
		t.Run("release_jq's _meta line reflects "+tt.name, func(t *testing.T) {
			t.Parallel()

			doc := fmt.Sprintf(`{"draft": %t, "prerelease": %t, "assets": []}`, tt.draft, tt.prerel)
			out := runJQ(t, jqBin, releaseJQ, doc)
			require.Len(t, out, 1)
			assert.Equal(t, metaLine(tt.draft, tt.prerel), out[0])
		})
	}
}

// TestSbxKitPin_CheckPrevious_SignalCleanup demonstrates the INT/TERM trap
// fix end to end: check-previous is killed while genuinely blocked on a
// deliberately slow `gh release list` (FAKE_GH_SLEEP), after its scratch
// directory already exists on disk, and that scratch directory must be
// gone once the killed script has exited. Before trap_cleanup_dir gave
// check-previous (and check-clobber, and bump - all three use the same
// helper) traps on HUP, INT and TERM, only a bare `trap ... EXIT` guarded
// this directory, which does not fire for these signals in every /bin/sh.
//
// fakeGhBin runs before t.Parallel: see its comment. Each case gets its own
// pinWorld (its own TMPDIR, its own bare origin), so running SIGINT and
// SIGTERM in parallel races nothing shared between them.
func TestSbxKitPin_CheckPrevious_SignalCleanup(t *testing.T) {
	bin := fakeGhBin(t)
	t.Parallel()

	for _, tt := range []struct {
		name     string
		sig      syscall.Signal
		wantExit int
	}{
		{name: "SIGINT", sig: syscall.SIGINT, wantExit: 130},
		{name: "SIGTERM", sig: syscall.SIGTERM, wantExit: 143},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := newPinWorld(t, bin, realKit(t, _prevVersion, _sumAMD64, _sumARM64), false)
			w.setTags(t, _prevTag, _olderTag, _decoyTag)
			w.setAssets(t, _prevTag, _prevVersion, "sha256:"+_sumAMD64, "sha256:"+_sumARM64, _botUploader)
			w.env = append(w.env, "FAKE_GH_SLEEP=5")

			script, err := filepath.Abs(_pinScript)
			require.NoError(t, err)

			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, "sh", script, "check-previous", _nextTag)
			cmd.Dir = w.repo
			cmd.Env = w.childEnv().asEnv()

			var stdout, stderr bytes.Buffer

			cmd.Stdout, cmd.Stderr = &stdout, &stderr

			require.NoError(t, cmd.Start())

			// Waits until the script is blocked inside the slow gh call
			// before signalling. The scratch directory existing is not
			// enough: it appears the moment mktemp -d returns, a few
			// commands BEFORE trap_cleanup_dir installs the traps, and a
			// signal landing in between kills the script by its default
			// disposition (exit -1, the directory left behind) - a
			// failure of the test's timing, not of the traps. By the time
			// gh runs, the traps are in place.
			require.Eventually(t, func() bool {
				_, err := os.Stat(filepath.Join(w.gh, "release-list.started"))

				return err == nil
			}, 5*time.Second, 20*time.Millisecond, "the script never reached gh release list")
			require.NotEmpty(t, w.leftovers(t), "the scratch directory exists while gh runs")

			require.NoError(t, cmd.Process.Signal(tt.sig))

			err = cmd.Wait()

			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr, "stdout: %s\nstderr: %s", stdout.String(), stderr.String())
			assert.Equal(t, tt.wantExit, exitErr.ExitCode())
			assert.Empty(t, w.leftovers(t), "the scratch directory must not survive the signal")
		})
	}
}
