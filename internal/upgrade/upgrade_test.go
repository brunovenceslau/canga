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
// archive for this platform, holding a devctl that reports tag, plus the
// checksums.txt covering it.
func releaseFixture(t *testing.T, tag string, extra ...tarEntry) fixture {
	t.Helper()

	entries := append([]tarEntry{{name: licenseName, body: licenseBody}}, extra...)
	entries = append(entries, tarEntry{name: binaryName, body: string(fakeBinary(tag))})

	files := map[string][]byte{archiveName(tag): tarGz(t, entries...)}
	files[checksumsName] = checksumsFile(files)

	return fixture{tag: tag, files: files}
}

// archiveName is what GoReleaser calls the archive for this platform.
func archiveName(tag string) string {
	return "devctl_" + strings.TrimPrefix(tag, "v") + assetSuffix()
}

// fakeGitHub serves the three endpoints devctl reads, and nothing else. The
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

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return server.URL
}

// runOptions is the shape every test below starts from: a real staging
// directory, a fake GitHub, and a token that is never checked by either.
func runOptions(t *testing.T, current string, releases ...fixture) Options {
	t.Helper()

	return Options{
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

func TestRunIsANoOpWhenNothingIsNewer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		current string
	}{
		{name: "the newest release is installed", current: newerVersion},
		{name: "something newer than any release is installed", current: "v9.0.0"},
		// The published v0.1.0 reports itself without the leading v. Read
		// literally it is not the tag, and the run would reinstall the release
		// it is already on, every time.
		{name: "the goreleaser spelling of the newest release", current: "0.2.0"},
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

	archive := big.files[archiveName(newerVersion)]
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
