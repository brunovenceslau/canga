// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package install_test

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/brunovenceslau/canga/internal/upgrade"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// published is what each build is published for (operator decision,
// 2026-09-28): the host build for the Macs, the sandbox build for the linux
// sandboxes, both architectures each, and nothing else.
var published = map[string][]string{
	upgrade.RoleHost:    {"darwin/amd64", "darwin/arm64"},
	upgrade.RoleSandbox: {"linux/amd64", "linux/arm64"},
}

// TestPlatforms_ReleaseMatchesTheRule pins every place that decides what is
// published to the one rule: .goreleaser.yml builds exactly it, the
// Makefile's cross gate compiles exactly it, and `canga upgrade` refuses to
// run anywhere else. Three copies that could drift, checked in one place.
func TestPlatforms_ReleaseMatchesTheRule(t *testing.T) {
	t.Parallel()

	release := goreleaserPlatforms(t)
	gate := makefilePlatforms(t)

	for role, want := range published {
		assert.Equal(t, want, release[role], ".goreleaser.yml, build %s", role)
		assert.Equal(t, want, gate[role], "Makefile cross gate, build %s", role)

		for _, platform := range want {
			goos, _, _ := strings.Cut(platform, "/")
			assert.Equal(t, goos, upgrade.PlatformFor(role), "upgrade.PlatformFor(%s)", role)
		}
	}
}

// goreleaserPlatforms reads each build's goos and goarch lists. A line scan
// rather than a YAML parser, so the test adds no dependency; the file's
// shape is fixed by this repository.
func goreleaserPlatforms(t *testing.T) map[string][]string {
	t.Helper()

	data, err := os.ReadFile(".goreleaser.yml")
	require.NoError(t, err)

	list := regexp.MustCompile(`^\s+(?:- )?(id|goos|goarch):\s*(.+)$`)
	platforms := map[string][]string{}

	var id string

	var goos []string

	for line := range strings.Lines(string(data)) {
		match := list.FindStringSubmatch(strings.TrimRight(line, "\n"))
		if match == nil {
			continue
		}

		values := strings.Split(strings.Trim(match[2], "[] "), ",")
		for i := range values {
			values[i] = strings.TrimSpace(values[i])
		}

		switch match[1] {
		case "id":
			id = values[0]
		case "goos":
			goos = values
		case "goarch":
			for _, system := range goos {
				for _, arch := range values {
					platforms[id] = append(platforms[id], system+"/"+arch)
				}
			}
		}
	}

	// Only the builds: the archives section repeats the ids with no platforms.
	for role := range platforms {
		slices.Sort(platforms[role])
	}

	require.NotEmpty(t, platforms, "no builds found in .goreleaser.yml")

	return platforms
}

// makefilePlatforms reads HOST_PLATFORMS and SANDBOX_PLATFORMS.
func makefilePlatforms(t *testing.T) map[string][]string {
	t.Helper()

	data, err := os.ReadFile("Makefile")
	require.NoError(t, err)

	platforms := map[string][]string{}

	for role, name := range map[string]string{upgrade.RoleHost: "HOST_PLATFORMS", upgrade.RoleSandbox: "SANDBOX_PLATFORMS"} {
		match := regexp.MustCompile(`(?m)^` + name + ` \?= (.+)$`).FindStringSubmatch(string(data))
		require.NotNil(t, match, "Makefile sets no %s", name)

		platforms[role] = strings.Fields(match[1])
		slices.Sort(platforms[role])
	}

	return platforms
}
