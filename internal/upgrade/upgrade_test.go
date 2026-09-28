// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixture is one release a fake GitHub serves: a tag and the files attached to
// it, keyed by asset name.
type fixture struct {
	tag   string
	files map[string][]byte
}

// releaseFixture builds the release GoReleaser would publish for tag: one
// archive per build for this platform, each holding a canga that reports tag
// and its own role, plus the checksums.txt covering both. Carrying both is
// what makes picking the wrong one observable.
func releaseFixture(t *testing.T, tag string, extra ...tarEntry) fixture {
	t.Helper()

	files := map[string][]byte{}

	for _, role := range []string{RoleHost, RoleSandbox} {
		entries := append([]tarEntry{{name: licenseName, body: licenseBody}}, extra...)
		entries = append(entries, tarEntry{name: binaryName, body: string(fakeBuild(tag, role))})
		files[archiveName(role, tag)] = tarGz(t, entries...)
	}

	files[checksumsName] = checksumsFile(files)

	return fixture{tag: tag, files: files}
}

// archiveName is what GoReleaser calls role's archive for this platform.
func archiveName(role, tag string) string {
	return "canga-" + role + "_" + strings.TrimPrefix(tag, "v") + assetSuffix()
}

// fakeGitHub serves the three endpoints canga reads, and nothing else. The
// first release given is what /releases/latest answers with.
func fakeGitHub(t *testing.T, releases ...fixture) string {
	t.Helper()

	documents := map[string]release{}
	bodies := map[int64][]byte{}

	var nextID int64

	for _, given := range releases {
		document := release{Tag: given.tag}

		for name, body := range given.files {
			nextID++
			bodies[nextID] = body
			document.Assets = append(document.Assets, asset{ID: nextID, Name: name, Size: int64(len(body))})
		}

		documents[given.tag] = document
	}

	mux := http.NewServeMux()
	prefix := "/repos/" + apiOwner + "/" + apiRepo

	write := func(w http.ResponseWriter, document release, ok bool) {
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))

			return
		}

		require.NoError(t, json.NewEncoder(w).Encode(document))
	}

	mux.HandleFunc("GET "+prefix+"/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		if len(releases) == 0 {
			write(w, release{}, false)

			return
		}

		write(w, documents[releases[0].tag], true)
	})

	mux.HandleFunc("GET "+prefix+"/releases/tags/{tag}", func(w http.ResponseWriter, r *http.Request) {
		document, ok := documents[r.PathValue("tag")]
		write(w, document, ok)
	})

	mux.HandleFunc("GET "+prefix+"/releases/assets/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || bodies[id] == nil {
			w.WriteHeader(http.StatusNotFound)

			return
		}

		_, _ = w.Write(bodies[id])
	})

	server := httptest.NewTestServer(t, mux)
	server.Start()

	return server.URL
}

// runOptions is the shape every test below starts from: a real staging
// directory, a fake GitHub, and a token that is never checked by either.
func runOptions(t *testing.T, current string, releases ...fixture) Options {
	t.Helper()

	return Options{
		Role:    RoleHost,
		Current: current,
		Token:   "token",
		baseURL: fakeGitHub(t, releases...),
		path:    installedBinary(t, t.TempDir(), current),
	}
}

func TestRunInstalls(t *testing.T) {
	t.Parallel()

	opts := runOptions(t, installedVersion, releaseFixture(t, newerVersion))

	result, err := Run(t.Context(), opts)
	require.NoError(t, err)

	assert.True(t, result.Installed)
	assert.True(t, result.Newer)
	assert.Equal(t, newerVersion, result.Release)
	assert.Equal(t, installedVersion, result.Current)
	assert.Equal(t, opts.path, result.Path)
	assert.Equal(t, string(fakeBinary(newerVersion)), readFile(t, opts.path))
	assert.Empty(t, stagingLeftovers(t, filepath.Dir(opts.path)))
}

// TestRunInstallsItsOwnBuild: from a release carrying both builds, each one
// installs the build it is. A sandbox that installed the host build would get
// the commands the sandbox build leaves out.
func TestRunInstallsItsOwnBuild(t *testing.T) {
	t.Parallel()

	for _, role := range []string{RoleHost, RoleSandbox} {
		t.Run(role, func(t *testing.T) {
			t.Parallel()

			opts := runOptions(t, installedVersion, releaseFixture(t, newerVersion))
			opts.Role = role

			result, err := Run(t.Context(), opts)
			require.NoError(t, err)

			assert.True(t, result.Installed)
			assert.Equal(t, string(fakeBuild(newerVersion, role)), readFile(t, opts.path))
		})
	}
}

// TestRunRefusesAnUnknownRole: a caller that forgot the role is refused
// before any request, rather than upgraded into a build it did not name.
func TestRunRefusesAnUnknownRole(t *testing.T) {
	t.Parallel()

	for _, role := range []string{"", "guest"} {
		t.Run("role "+role, func(t *testing.T) {
			t.Parallel()

			opts := runOptions(t, installedVersion, releaseFixture(t, newerVersion))
			opts.Role = role

			server := httptest.NewTestServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Error("an unknown role must be refused before any request")
			}))
			server.Start()

			opts.baseURL = server.URL

			result, err := Run(t.Context(), opts)
			require.ErrorContains(t, err, "unknown canga build role")
			assert.Empty(t, result.Release)
			assert.Equal(t, string(fakeBinary(installedVersion)), readFile(t, opts.path))
		})
	}
}

func TestRunIsANoOpWhenNothingIsNewer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		current string
	}{
		{name: "the newest release is installed", current: newerVersion},
		{name: "something newer than any release is installed", current: "v9.0.0"},
		// The published v0.10.7 reports itself without the leading v. Read
		// literally it is not the tag, and the run would reinstall the release
		// it is already on, every time.
		{name: "the goreleaser spelling of the newest release", current: "0.10.7"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := runOptions(t, tt.current, releaseFixture(t, newerVersion))

			result, err := Run(t.Context(), opts)
			require.NoError(t, err)

			assert.False(t, result.Installed)
			assert.False(t, result.Newer)
			assert.Equal(t, string(fakeBinary(tt.current)), readFile(t, opts.path))
		})
	}
}

func TestRunCheckChangesNothing(t *testing.T) {
	t.Parallel()

	opts := runOptions(t, installedVersion, releaseFixture(t, newerVersion))
	opts.Check = true

	result, err := Run(t.Context(), opts)
	require.NoError(t, err)

	assert.False(t, result.Installed)
	assert.True(t, result.Newer)
	assert.Equal(t, newerVersion, result.Release)
	assert.Equal(t, opts.path, result.Path, "--check still says which file it would replace")
	assert.Equal(t, string(fakeBinary(installedVersion)), readFile(t, opts.path))
}

func TestRunWithATag(t *testing.T) {
	t.Parallel()

	t.Run("installs a release that is not the newest", func(t *testing.T) {
		t.Parallel()

		opts := runOptions(t, newerVersion, releaseFixture(t, newerVersion), releaseFixture(t, installedVersion))
		opts.Tag = installedVersion

		result, err := Run(t.Context(), opts)
		require.NoError(t, err)

		assert.True(t, result.Installed, "a tag is an instruction, not a comparison")
		assert.False(t, result.Newer)
		assert.Equal(t, string(fakeBinary(installedVersion)), readFile(t, opts.path))
	})

	// This is the only way out for a `go install` build, so it has to work
	// without a version to compare against.
	t.Run("upgrades a build that reports no release", func(t *testing.T) {
		t.Parallel()

		opts := runOptions(t, goInstallVersion, releaseFixture(t, newerVersion))
		opts.Tag = newerVersion

		result, err := Run(t.Context(), opts)
		require.NoError(t, err)
		assert.True(t, result.Installed)
	})

	t.Run("a tag with no release behind it", func(t *testing.T) {
		t.Parallel()

		opts := runOptions(t, installedVersion, releaseFixture(t, newerVersion))
		opts.Tag = "v9.9.9"

		_, err := Run(t.Context(), opts)
		require.ErrorIs(t, err, ErrNoRelease)
	})

	t.Run("a tag that is not a version", func(t *testing.T) {
		t.Parallel()

		opts := runOptions(t, installedVersion, releaseFixture(t, newerVersion))
		opts.Tag = "main"

		_, err := Run(t.Context(), opts)
		require.ErrorIs(t, err, ErrBadTag)
	})

	// A tag below MinReleaseTag names a release that could have been
	// recreated with arbitrary bytes by anyone with contents: write
	// (docs/HANDOFF.md, "Immutability is not retroactive", Path B), and this
	// package's own checksum check cannot tell that apart from the real
	// thing, so it is refused before the network is ever asked, the same
	// way an unknown role is (TestRunRefusesAnUnknownRole).
	t.Run("a tag below the release floor", func(t *testing.T) {
		t.Parallel()

		for _, tag := range []string{"v0.10.4", "v0.1.0"} {
			t.Run(tag, func(t *testing.T) {
				t.Parallel()

				opts := runOptions(t, installedVersion, releaseFixture(t, newerVersion))
				opts.Tag = tag

				server := httptest.NewTestServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					t.Error("a tag below the release floor must be refused before any request")
				}))
				server.Start()

				opts.baseURL = server.URL

				_, err := Run(t.Context(), opts)
				require.ErrorIs(t, err, ErrBelowFloor)
				assert.ErrorContains(t, err, MinReleaseTag)
			})
		}
	})

	// The floor's complement: MinReleaseTag itself, and releases above it,
	// all install normally.
	t.Run("a tag at or above the release floor", func(t *testing.T) {
		t.Parallel()

		for _, tag := range []string{MinReleaseTag, "v0.10.6", "v0.11.0", "v1.0.0"} {
			t.Run(tag, func(t *testing.T) {
				t.Parallel()

				opts := runOptions(t, goInstallVersion, releaseFixture(t, tag))
				opts.Tag = tag

				result, err := Run(t.Context(), opts)
				require.NoError(t, err)
				assert.True(t, result.Installed)
				assert.Equal(t, tag, result.Release)
			})
		}
	})
}

// TestRunAppliesTheReleaseFloorToTheNewestRelease is the floor's defense in
// depth for the path with no --tag at all: Path B (docs/HANDOFF.md,
// "Immutability is not retroactive") can recreate an OLD tag's release with
// today's publish date, which is what GitHub's own /releases/latest would
// then resolve to, so the floor is checked against whatever tag is
// resolved, not only against an explicit --tag.
func TestRunAppliesTheReleaseFloorToTheNewestRelease(t *testing.T) {
	t.Parallel()

	opts := runOptions(t, installedVersion, releaseFixture(t, "v0.10.4"))

	_, err := Run(t.Context(), opts)
	require.ErrorIs(t, err, ErrBelowFloor)
}

// TestRunRefusesANewestReleaseTagThatIsNotCanonical closes round-1 ship-gate
// finding 2 (docs/HANDOFF.md, "Release floor for installers and canga
// upgrade (PR #41)"): the repository's ruleset only protects tags matching
// refs/tags/v*, so a release published under the tag "1.0.0" (no "v") is
// reachable by any contents: write actor, no admin rights needed. Before
// this fix, normalizeTag silently read that as "v1.0.0" and let it clear
// the floor; checkFloor no longer normalizes found.Tag, so it is refused
// for not being canonical vX.Y.Z, before any archive is downloaded.
func TestRunRefusesANewestReleaseTagThatIsNotCanonical(t *testing.T) {
	t.Parallel()

	server := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/assets/1") {
			t.Error("a non-canonical release tag must be refused before any asset is downloaded")

			return
		}

		_, _ = w.Write([]byte(`{"tag_name":"1.0.0","assets":[{"id":1,"name":"checksums.txt","size":9}]}`))
	}))
	server.Start()

	opts := runOptions(t, installedVersion)
	opts.baseURL = server.URL

	_, err := Run(t.Context(), opts)
	require.ErrorIs(t, err, ErrBelowFloor)
	assert.ErrorContains(t, err, "1.0.0")
}

// TestRunRefusesWhenGitHubsReleaseDocumentNamesAnotherTag is defense in
// depth for round-1 ship-gate finding 2's second half: an explicit --tag is
// looked up by that exact string (byTag, github.go), so a release document
// naming any other tag means something upstream of Run disagrees with
// itself about which release this is. Not reachable through the real
// GitHub API today - a mismatched /releases/tags/<tag> lookup 404s rather
// than serve another release's document - so this drives the client
// against a server that deliberately breaks that assumption.
func TestRunRefusesWhenGitHubsReleaseDocumentNamesAnotherTag(t *testing.T) {
	t.Parallel()

	server := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v0.10.9","assets":[]}`))
	}))
	server.Start()

	opts := runOptions(t, installedVersion)
	opts.baseURL = server.URL
	opts.Tag = "v0.10.6"

	_, err := Run(t.Context(), opts)
	require.ErrorIs(t, err, ErrTagMismatch)
	assert.ErrorContains(t, err, "v0.10.6")
	assert.ErrorContains(t, err, "v0.10.9")
}

func TestRunRefusesABuildItCannotPlace(t *testing.T) {
	t.Parallel()

	for _, current := range []string{goInstallVersion, describedVersion, dirtyVersion, ""} {
		t.Run("version "+current, func(t *testing.T) {
			t.Parallel()

			_, err := Run(t.Context(), runOptions(t, current, releaseFixture(t, newerVersion)))
			require.ErrorIs(t, err, ErrNotRelease)
			assert.ErrorContains(t, err, "--tag", "the refusal has to name the way out")
		})
	}
}

// The repository is public, so an anonymous run is a supported one rather than
// a refusal. A token only raises the rate limit.
func TestRunWithoutAToken(t *testing.T) {
	t.Parallel()

	opts := runOptions(t, installedVersion, releaseFixture(t, newerVersion))
	opts.Token = ""

	result, err := Run(t.Context(), opts)
	require.NoError(t, err)
	assert.Equal(t, newerVersion, result.Release)
	assert.True(t, result.Installed)
}

// TestRunRefusesAnArchiveThatIsNotTheOnePublished is the gate the whole command
// exists behind: bytes that do not hash to what the release says are never
// written anywhere, and the working binary is untouched.
func TestRunRefusesAnArchiveThatIsNotTheOnePublished(t *testing.T) {
	t.Parallel()

	tampered := releaseFixture(t, newerVersion)

	for name, body := range tampered.files {
		if name != checksumsName {
			tampered.files[name] = append(body, " tampered"...)
		}
	}

	opts := runOptions(t, installedVersion, tampered)

	_, err := Run(t.Context(), opts)
	require.ErrorIs(t, err, ErrChecksum)

	assert.Equal(t, string(fakeBinary(installedVersion)), readFile(t, opts.path))
	assert.Empty(t, stagingLeftovers(t, filepath.Dir(opts.path)))
}

// TestRunCapsChecksumsSeparatelyFromTheArchive pins which cap reaches which
// download. checksums.txt is one sha256 line per asset — a few hundred bytes —
// and giving it the archive's 64 MiB ceiling is no ceiling at all.
func TestRunCapsChecksumsSeparatelyFromTheArchive(t *testing.T) {
	t.Parallel()

	oversized := releaseFixture(t, newerVersion)
	oversized.files[checksumsName] = []byte(strings.Repeat("A", maxChecksumsBytes+1))

	_, err := Run(t.Context(), runOptions(t, installedVersion, oversized))
	require.ErrorIs(t, err, ErrTooLarge)
}

// TestRunDoesNotCapTheArchiveAtTheChecksumsCeiling is the other half of the
// pair above, and it is the half that matters in production: the two caps are
// arguments at one call site, so passing them the wrong way round would cap
// every real 2 MiB release archive at 64 KiB and break upgrade against every
// published release. Fixtures are a few hundred bytes, so nothing else in this
// suite would notice.
func TestRunDoesNotCapTheArchiveAtTheChecksumsCeiling(t *testing.T) {
	t.Parallel()

	big := releaseFixture(t, newerVersion, tarEntry{name: licenseName, body: string(incompressible(t, 128<<10))})

	opts := runOptions(t, installedVersion, big)

	archive := big.files[archiveName(RoleHost, newerVersion)]
	require.Greater(t, len(archive), maxChecksumsBytes,
		"the fixture only tests anything if its archive is past the checksums ceiling")

	result, err := Run(t.Context(), opts)
	require.NoError(t, err)
	assert.True(t, result.Installed)
}

// incompressible returns n bytes gzip cannot shrink, so a fixture archive can
// be made to exceed a size cap. Deterministic, so a failure reproduces.
func incompressible(t *testing.T, n int) []byte {
	t.Helper()

	noise := make([]byte, n)
	random := rand.New(rand.NewPCG(1, 2))

	for i := range noise {
		noise[i] = byte(random.UintN(256))
	}

	return noise
}

func TestRunRefusesAReleaseWithNothingForThisPlatform(t *testing.T) {
	t.Parallel()

	empty := fixture{tag: newerVersion, files: map[string][]byte{checksumsName: []byte("")}}

	_, err := Run(t.Context(), runOptions(t, installedVersion, empty))
	require.ErrorIs(t, err, ErrNoAsset)
}
