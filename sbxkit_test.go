// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package install_test

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

const (
	_kitSpec = "sbx-kit/spec.yaml"
	_arm64   = "arm64"

	// The two lines of the kit's install command that name /usr/local/bin;
	// TestSbxKit_Install redirects both into the case's own directory.
	_kitInstallLine = `install -m 0755 "${work}/canga" /usr/local/bin/canga`
	_kitVersionCall = "\ncanga --version\n"
)

var (
	_kitVersionLine = regexp.MustCompile(`(?m)^[ \t]*CANGA_VERSION="([^"]*)"$`)
	_kitArchLine    = regexp.MustCompile(`^[ \t]*arch="([^"]*)"$`)
	_kitSumLine     = regexp.MustCompile(`^[ \t]*sha256="([^"]*)"$`)
	_quoted         = regexp.MustCompile(`"[^"]*"`)
	_semver         = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	_sha256Hex      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// kitInstallScript is the kit's one install command, read as sbx reads it:
// through a YAML parser, so a rewrite that broke the YAML fails here too.
func kitInstallScript(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var kit struct {
		Setup struct {
			Install []struct {
				Command string `yaml:"command"`
				User    string `yaml:"user"`
			} `yaml:"install"`
		} `yaml:"setup"`
	}
	require.NoError(t, yaml.Unmarshal(data, &kit), "%s is not valid YAML", path)
	require.Len(t, kit.Setup.Install, 1, "%s: want one install step", path)

	return kit.Setup.Install[0].Command
}

// kitPin is what the install script pins: its version, and the sha256 under
// each arch's case branch, in the order they appear.
type kitPin struct {
	versions []string
	sums     map[string][]string
}

func parseKitPin(script string) kitPin {
	pin := kitPin{sums: map[string][]string{}}

	for _, m := range _kitVersionLine.FindAllStringSubmatch(script, -1) {
		pin.versions = append(pin.versions, m[1])
	}

	arch := ""

	for line := range strings.SplitSeq(script, "\n") {
		if m := _kitArchLine.FindStringSubmatch(line); m != nil {
			arch = m[1]

			continue
		}

		if m := _kitSumLine.FindStringSubmatch(line); m != nil {
			pin.sums[arch] = append(pin.sums[arch], m[1])
		}
	}

	return pin
}

// pinKit returns body with its pin replaced, the way scripts/sbx-kit-pin.sh
// replaces it: the first quoted value on the CANGA_VERSION line and on the
// sha256 line after each arch line. It fails unless each is found once.
func pinKit(t *testing.T, body, version, amd64, arm64 string) string {
	t.Helper()

	lines := strings.Split(body, "\n")
	arch, found := "", map[string]int{}

	for i, line := range lines {
		value := ""

		switch {
		case _kitVersionLine.MatchString(line):
			arch, value = "version", version
		case _kitArchLine.MatchString(line):
			arch = _kitArchLine.FindStringSubmatch(line)[1]

			continue
		case _kitSumLine.MatchString(line) && arch == _amd64:
			value = amd64
		case _kitSumLine.MatchString(line) && arch == _arm64:
			value = arm64
		default:
			continue
		}

		found[arch]++
		lines[i] = strings.Replace(line, _quoted.FindString(line), `"`+value+`"`, 1)
	}

	require.Equal(t, map[string]int{"version": 1, _amd64: 1, _arm64: 1}, found, "the kit's pin lines")

	return strings.Join(lines, "\n")
}

// realKit is the committed kit, pinned to version and the two sums.
func realKit(t *testing.T, version, amd64, arm64 string) string {
	t.Helper()

	data, err := os.ReadFile(_kitSpec)
	require.NoError(t, err)

	return pinKit(t, string(data), version, amd64, arm64)
}

// TestSbxKit_Pin guards the kit as committed: one well-formed version, and
// exactly one 64-hex sha256 per arch the kit installs, and no other.
func TestSbxKit_Pin(t *testing.T) {
	t.Parallel()

	pin := parseKitPin(kitInstallScript(t, _kitSpec))

	require.Len(t, pin.versions, 1, "want exactly one CANGA_VERSION line")
	assert.Regexp(t, _semver, pin.versions[0], "CANGA_VERSION is X.Y.Z, without the tag's v or leading zeros")

	assert.ElementsMatch(
		t,
		[]string{_amd64, _arm64},
		slices.Collect(maps.Keys(pin.sums)),
		"a sha256 outside an arch branch, or an arch missing one",
	)

	for arch, sums := range pin.sums {
		require.Len(t, sums, 1, "%s: want exactly one pinned sha256", arch)
		assert.Regexp(t, _sha256Hex, sums[0], "%s: the pinned sha256 is 64 lowercase hex", arch)
	}
}

// TestSbxKit_Shellcheck runs shellcheck over the install script the kit
// embeds and over the script that rewrites its pin. Without shellcheck it
// skips locally and fails under CI, where its absence would be a gate that
// quietly stopped checking.
func TestSbxKit_Shellcheck(t *testing.T) {
	t.Parallel()

	shellcheck, err := exec.LookPath("shellcheck")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("shellcheck is not installed, and CI must run it")
		}

		t.Skip("shellcheck is not installed; install it to lint the kit's shell")
	}

	embedded := filepath.Join(t.TempDir(), "install.sh")
	require.NoError(t, os.WriteFile(embedded, []byte(kitInstallScript(t, _kitSpec)), 0o600))

	for _, path := range []string{embedded, _pinScript} {
		cmd := exec.CommandContext(t.Context(), shellcheck, "--shell=sh", path)
		cmd.Env = childEnv(os.Environ())
		out, err := cmd.CombinedOutput()
		assert.NoError(t, err, "shellcheck %s:\n%s", path, out)
	}
}

// releaseSums reads a checksums.txt into name -> sha256.
func releaseSums(t *testing.T, path string) map[string]string {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	sums := map[string]string{}

	for line := range strings.Lines(string(data)) {
		sum, name, ok := strings.Cut(strings.TrimSuffix(line, "\n"), "  ")
		if ok {
			sums[name] = sum
		}
	}

	return sums
}

// TestSbxKit_Install runs the kit's own install command, as extracted from
// the YAML, against a release served by the fake curl, the same harness the
// install_*.sh tests use. Only its pin (version and sums, taken from the
// fixture's checksums.txt) and its two /usr/local/bin lines are rewritten.
//
//nolint:paralleltest // serial so newBins writes the fakes while no other test in this package forks
func TestSbxKit_Install(t *testing.T) {
	if _, err := exec.LookPath(_sha256sum); err != nil {
		t.Skip("the kit requires sha256sum, which this machine lacks; CI runs these cases")
	}

	asset := func(arch string) string {
		return requested("download/" + _tag + "/canga-sandbox_" + _version + "_linux_" + arch + ".tar.gz")
	}

	tests := []installCase{
		{name: "kit on arm64", os: _linux, arch: _aarch64, wantRequests: []string{asset(_arm64)}},
		{name: "kit on amd64", os: _linux, arch: "x86_64", wantRequests: []string{asset(_amd64)}},
		{name: "kit, checksum mismatch", os: _linux, arch: _aarch64, checksums: _wrong, wantExit: 1, wantRequests: []string{asset(_arm64)}, wantStderr: "did NOT match"},
		{name: "kit, unsupported architecture", os: _linux, arch: "riscv64", wantExit: 1, wantStderr: "canga: unsupported architecture: riscv64"},
	}
	for i := range tests {
		tests[i].sha = _sha256sum
	}

	runCases(t, "sandbox", tests, func(t *testing.T, e env) (string, string) {
		t.Helper()

		dir := filepath.Join(e.root, "usr-local-bin")
		sums := releaseSums(t, filepath.Join(e.releases, _tag, "checksums.txt"))
		body := pinKit(t, kitInstallScript(t, _kitSpec), _version,
			sums["canga-sandbox_"+_version+"_linux_amd64.tar.gz"],
			sums["canga-sandbox_"+_version+"_linux_arm64.tar.gz"])

		for _, line := range []string{_kitInstallLine, _kitVersionCall} {
			require.Equal(
				t,
				1,
				strings.Count(body, line),
				"the kit must carry %q exactly once for this test to redirect it",
				line,
			)
		}

		body = strings.Replace(body, _kitInstallLine, `install -m 0755 "${work}/canga" `+dir+"/canga", 1)
		body = strings.Replace(body, _kitVersionCall, "\n"+dir+"/canga --version\n", 1)

		path := filepath.Join(e.root, "kit-install.sh")
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))

		return path, filepath.Join(dir, "canga")
	})
}
