// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noToolsPath empties PATH for the test, so that a `gh` installed on the
// developer's machine cannot answer and turn a hermetic test into one whose
// result depends on whose laptop it runs on.
func noToolsPath(t *testing.T) {
	t.Helper()

	t.Setenv("PATH", t.TempDir())
}

// fakeGH puts a `gh` on PATH that prints answer and exits 0, or exits 1 when
// answer is empty.
func fakeGH(t *testing.T, answer string) {
	t.Helper()

	dir := t.TempDir()

	script := "#!/bin/sh\nexit 1\n"
	if answer != "" {
		script = "#!/bin/sh\nprintf '%s\\n' '" + answer + "'\n"
	}

	require.NoError(t, os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755))
	t.Setenv("PATH", dir)
}

func TestToken(t *testing.T) {
	t.Run("GH_TOKEN comes first", func(t *testing.T) {
		noToolsPath(t)
		t.Setenv("GH_TOKEN", "from-gh-token")
		t.Setenv("GITHUB_TOKEN", "from-github-token")

		token, err := Token(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "from-gh-token", token)
	})

	t.Run("GITHUB_TOKEN is the second look", func(t *testing.T) {
		noToolsPath(t)
		t.Setenv("GH_TOKEN", "")
		t.Setenv("GITHUB_TOKEN", "  from-github-token\n")

		token, err := Token(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "from-github-token", token, "surrounding whitespace never reaches a header")
	})

	// The reason the fallback exists: on the mac the token is in the keychain
	// and never in the environment, so without this `devctl upgrade` would not
	// work on the machine it is for.
	t.Run("gh auth token answers when the environment does not", func(t *testing.T) {
		t.Setenv("GH_TOKEN", "")
		t.Setenv("GITHUB_TOKEN", "")
		fakeGH(t, "from-the-keychain")

		token, err := Token(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "from-the-keychain", token)
	})

	t.Run("no gh and no environment", func(t *testing.T) {
		noToolsPath(t)
		t.Setenv("GH_TOKEN", "")
		t.Setenv("GITHUB_TOKEN", "")

		_, err := Token(t.Context())
		require.ErrorIs(t, err, ErrNoToken)
		require.ErrorContains(t, err, "GH_TOKEN", "the refusal has to name both ways to supply one")
		require.ErrorContains(t, err, "gh auth login")
	})

	t.Run("gh present but signed out", func(t *testing.T) {
		t.Setenv("GH_TOKEN", "")
		t.Setenv("GITHUB_TOKEN", "")
		fakeGH(t, "")

		_, err := Token(t.Context())
		require.ErrorIs(t, err, ErrNoToken)
	})
}

func TestClientStatusErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		status    int
		headers   map[string]string
		expected  error
		mustState string
	}{
		{
			name:      "a token github rejects",
			status:    http.StatusUnauthorized,
			expected:  ErrUnauthorized,
			mustState: "Bad credentials",
		},
		{
			name:      "a token without the scope",
			status:    http.StatusForbidden,
			expected:  ErrForbidden,
			mustState: "Bad credentials",
		},
		{
			name:      "the rate limit",
			status:    http.StatusForbidden,
			headers:   map[string]string{"X-RateLimit-Remaining": "0"},
			expected:  ErrForbidden,
			mustState: "rate limit",
		},
		{
			// On a private repository these are one answer, and the message has
			// to say so: 404 is also what a token with no access is told, so
			// that the API does not confirm the repository exists.
			name:      "no release, or no access",
			status:    http.StatusNotFound,
			expected:  ErrNoRelease,
			mustState: "cannot see",
		},
		{
			name:      "something else entirely",
			status:    http.StatusInternalServerError,
			expected:  nil,
			mustState: "500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				for key, value := range tt.headers {
					w.Header().Set(key, value)
				}

				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
			}))
			t.Cleanup(server.Close)

			_, err := newClient("token", server.URL, installedVersion).latest(t.Context())
			require.Error(t, err)

			if tt.expected != nil {
				require.ErrorIs(t, err, tt.expected)
			}

			assert.ErrorContains(t, err, tt.mustState)
		})
	}
}

func TestClientLatestSendsWhatGitHubExpects(t *testing.T) {
	t.Parallel()

	var got *http.Request

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(r.Context())
		_, _ = w.Write([]byte(`{"tag_name":"v0.2.0","assets":[{"id":7,"name":"checksums.txt","size":9}]}`))
	}))
	t.Cleanup(server.Close)

	found, err := newClient("s3cret", server.URL, "0.1.0").latest(t.Context())
	require.NoError(t, err)
	assert.Equal(t, newerVersion, found.Tag)
	assert.Equal(t, []asset{{ID: 7, Name: checksumsName, Size: 9}}, found.Assets)

	require.NotNil(t, got)
	assert.Equal(t, "/repos/"+apiOwner+"/"+apiRepo+"/releases/latest", got.URL.Path)
	assert.Equal(t, "Bearer s3cret", got.Header.Get("Authorization"))
	assert.Equal(t, "application/vnd.github+json", got.Header.Get("Accept"))
	assert.Equal(t, apiVersion, got.Header.Get("X-GitHub-Api-Version"))
	assert.Equal(t, "devctl/v0.1.0", got.Header.Get("User-Agent"))
}

func TestClientRejectsAReleaseWithNoTag(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"assets":[]}`))
	}))
	t.Cleanup(server.Close)

	_, err := newClient("token", server.URL, installedVersion).latest(t.Context())
	require.ErrorIs(t, err, ErrNoRelease)
}

// TestClientDownloadDropsTheTokenOnRedirect proves the behaviour the asset
// download depends on: GitHub answers the API with a redirect to a signed URL
// on another host, and that URL REJECTS a request still carrying an
// Authorization header. net/http documents dropping sensitive headers when the
// redirect target is not a subdomain match; this is that promise, exercised.
func TestClientDownloadDropsTheTokenOnRedirect(t *testing.T) {
	t.Parallel()

	var authOnSignedURL string

	signed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authOnSignedURL = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("the archive"))
	}))
	t.Cleanup(signed.Close)

	// 127.0.0.1 and [::1] are different hosts to the redirect policy, which is
	// what makes this a cross-host redirect without needing real DNS.
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(signed.URL, "127.0.0.1", "localhost", 1), http.StatusFound)
	}))
	t.Cleanup(api.Close)

	body, err := newClient("s3cret", api.URL, installedVersion).
		download(t.Context(), asset{ID: 1, Name: "devctl.tar.gz"})
	require.NoError(t, err)
	assert.Equal(t, "the archive", string(body))
	assert.Empty(t, authOnSignedURL, "the token must not travel to the signed URL")
}

func TestClientDownloadRefusesMoreThanTheReleaseDeclares(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("A", 100)))
	}))
	t.Cleanup(server.Close)

	_, err := newClient("token", server.URL, installedVersion).
		download(t.Context(), asset{ID: 1, Name: "devctl.tar.gz", Size: 10})
	require.ErrorIs(t, err, ErrTooLarge)
}
