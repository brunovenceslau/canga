// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Where a release comes from. These are CONSTANTS on purpose: an environment
// variable pointing devctl at another host would be a downgrade vector, since
// whoever set it would decide which code this binary replaces itself with. The
// only seam is Options.baseURL, which nothing outside this package's own tests
// can reach.
const (
	apiBase    = "https://api.github.com"
	apiOwner   = "brunovenceslau"
	apiRepo    = "devctl"
	apiVersion = "2022-11-28"
)

// Response size caps. Everything is read into memory, so every read is bounded:
// the archives published so far are under 2 MiB, and a release document lists
// five assets.
const (
	maxJSONBytes      = 1 << 20  // 1 MiB — a release document listing its assets
	maxChecksumsBytes = 64 << 10 // 64 KiB — one sha256 line per asset
	maxArchiveBytes   = 64 << 20 // 64 MiB — the archives published so far are under 2 MiB
	maxErrorBytes     = 8 << 10  // 8 KiB — GitHub's own "message" on a refusal
)

// httpTimeout bounds one request end to end, body included. The caller's
// context still cancels earlier on a Ctrl-C; this is the ceiling for a request
// that neither fails nor finishes.
const httpTimeout = 2 * time.Minute

// tokenTimeout bounds the `gh auth token` fallback. It reads a config file or
// a keychain entry, so it is either fast or broken.
const tokenTimeout = 10 * time.Second

var (
	// ErrUnauthorized reports a token GitHub rejected outright.
	ErrUnauthorized = errors.New("github rejected the token")

	// ErrForbidden reports a request GitHub refused, including a rate limit.
	ErrForbidden = errors.New("github refused the request")

	// ErrNoRelease reports a release GitHub does not serve: no release carries
	// that tag, and the message names the repository it asked.
	ErrNoRelease = errors.New("no such release")

	// ErrNoAsset reports a release carrying nothing for this platform.
	ErrNoAsset = errors.New("no release asset for this platform")

	// ErrTooLarge reports a response past its size cap. It is its own sentinel
	// because it means something is wrong with the release, not with the
	// network, and retrying will not help.
	ErrTooLarge = errors.New("release artifact is larger than expected")
)

// asset is one file attached to a release. The id is what the API serves it by;
// see download for why that path is used rather than the browser URL.
type asset struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// release is the subset of GitHub's release document devctl reads.
type release struct {
	Tag    string  `json:"tag_name"`
	Assets []asset `json:"assets"`
}

// client reads releases of the one repository devctl upgrades itself from.
type client struct {
	http    *http.Client
	token   string
	baseURL string
	agent   string
}

func newClient(token, baseURL, current string) *client {
	if baseURL == "" {
		baseURL = apiBase
	}

	return &client{
		http:    &http.Client{Timeout: httpTimeout},
		token:   token,
		baseURL: strings.TrimSuffix(baseURL, "/"),
		agent:   "devctl/" + normalizeTag(current),
	}
}

// latest returns the most recent published release. GitHub's "latest" excludes
// drafts and pre-releases, which is the behaviour wanted here: an upgrade
// offers a finished release or nothing.
func (c *client) latest(ctx context.Context) (release, error) {
	return c.releaseAt(ctx, "/releases/latest")
}

// byTag returns one named release, which is how a re-install or a deliberate
// step backwards is expressed.
func (c *client) byTag(ctx context.Context, tag string) (release, error) {
	return c.releaseAt(ctx, "/releases/tags/"+tag)
}

func (c *client) releaseAt(ctx context.Context, path string) (release, error) {
	body, err := c.fetch(ctx, path, "application/vnd.github+json", maxJSONBytes)
	if err != nil {
		return release{}, err
	}

	var found release
	if err := json.Unmarshal(body, &found); err != nil {
		return release{}, fmt.Errorf("read the release document: %w", err)
	}

	if found.Tag == "" {
		return release{}, fmt.Errorf("%w: the release document names no tag", ErrNoRelease)
	}

	return found, nil
}

// download returns one asset's bytes, reading at most limit of them.
//
// The limit is the caller's, because the caller knows what it asked for: a
// checksums.txt and a release archive differ by three orders of magnitude, and
// a single cap generous enough for the archive is no cap at all for the text
// file beside it.
//
// The request goes to the API, not to the browser download URL, so that one
// code path serves the release whatever the repository's visibility and whether
// or not a token was supplied. GitHub answers with a
// redirect to a signed URL on another host, which the http.Client follows and —
// as net/http.Client documents — does NOT carry the Authorization header to,
// since it is not a subdomain match. That is load-bearing rather than
// incidental: the signed URL rejects a request that still carries one.
func (c *client) download(ctx context.Context, want asset, limit int64) ([]byte, error) {
	path := fmt.Sprintf("/releases/assets/%d", want.ID)

	if want.Size > 0 && want.Size < limit {
		// The release itself says how big the file is, so a body that outgrows
		// it is refused before the caller's ceiling. Size is only ever allowed
		// to NARROW the limit: a release declaring a huge one, or none at all,
		// does not get to raise it.
		limit = want.Size
	}

	body, err := c.fetch(ctx, path, "application/octet-stream", limit)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", want.Name, err)
	}

	return body, nil
}

// fetch performs one request and returns at most limit bytes of its body.
//
// A token GitHub rejects is dropped and the request retried anonymously, once.
// The token is optional, so a stale one (expired, revoked, or a sandbox secret
// that outlived its purpose) must not refuse an upgrade that works without it.
// The client forgets the token for every later request too, rather than paying
// a 401 per asset. When the anonymous retry fails as well, both failures are
// reported, so a rate limit hit without the token still names the token as the
// thing to fix.
func (c *client) fetch(ctx context.Context, path, accept string, limit int64) ([]byte, error) {
	body, err := c.fetchOnce(ctx, path, accept, limit)
	if !errors.Is(err, ErrUnauthorized) || c.token == "" {
		return body, err
	}

	c.token = ""

	body, retryErr := c.fetchOnce(ctx, path, accept, limit)
	if retryErr != nil {
		return nil, fmt.Errorf("%w; retried without the token: %w", err, retryErr)
	}

	return body, nil
}

// fetchOnce performs one request. It owns the response from end to end — no
// caller has to remember to close it, and no partially read body escapes.
func (c *client) fetchOnce(ctx context.Context, path, accept string, limit int64) ([]byte, error) {
	url := c.baseURL + "/repos/" + apiOwner + "/" + apiRepo + path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build the request for %s: %w", path, err)
	}

	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", c.agent)

	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach %s: %w", c.baseURL, redactQuery(err))
	}

	defer func() { _ = resp.Body.Close() }()

	if err := statusError(resp); err != nil {
		return nil, err
	}

	// limit+1 so that a body of exactly limit bytes is accepted and one byte
	// more is caught, which a plain LimitReader cannot tell apart.
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read the response from %s: %w", path, err)
	}

	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%w: %s is over %d bytes", ErrTooLarge, path, limit)
	}

	return body, nil
}

// statusError maps a non-OK response onto a sentinel, so that the command can
// say which of four different problems this is instead of printing a number.
func statusError(resp *http.Response) error {
	if resp.StatusCode == http.StatusOK {
		return nil
	}

	said := githubSaid(resp.Body)

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("%w%s", ErrUnauthorized, said)
	case http.StatusForbidden:
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return fmt.Errorf("%w: rate limit exhausted%s", ErrForbidden, said)
		}

		return fmt.Errorf("%w%s", ErrForbidden, said)
	case http.StatusNotFound:
		return fmt.Errorf("%w in %s/%s%s", ErrNoRelease, apiOwner, apiRepo, said)
	default:
		return fmt.Errorf("github answered %s%s", resp.Status, said)
	}
}

// redactQuery strips the query string from a failed request's URL.
//
// It matters because of the redirect the asset download depends on: by the time
// a connection fails mid-download, the *url.Error carries GitHub's SIGNED asset
// URL, and net/url prints it whole — signature parameters included. Those are a
// short-lived credential, and the rule is the same one Token is written to:
// what reaches a terminal scrollback or a CI log is not recoverable.
//
// The wrapped cause is preserved, so errors.Is still finds a cancelled context
// underneath.
func redactQuery(err error) error {
	urlErr, ok := errors.AsType[*url.Error](err)
	if !ok {
		return err
	}

	parsed, parseErr := url.Parse(urlErr.URL)
	if parseErr != nil || parsed.RawQuery == "" {
		return err
	}

	parsed.RawQuery = ""

	return fmt.Errorf("%s %s?<redacted>: %w", urlErr.Op, parsed, urlErr.Err)
}

// githubSaid renders GitHub's own explanation, which the error body carries as
// a "message" field. Without it a refusal is a bare status code, and "Bad
// credentials" versus "Resource not accessible by personal access token" is the
// whole difference between two fixes.
func githubSaid(body io.Reader) string {
	raw, err := io.ReadAll(io.LimitReader(body, maxErrorBytes))
	if err != nil {
		return ""
	}

	var said struct {
		Message string `json:"message"`
	}

	if err := json.Unmarshal(raw, &said); err != nil || said.Message == "" {
		return ""
	}

	return ": " + said.Message
}

// Token finds a GitHub credential for the API, or returns the empty string.
//
// A credential is OPTIONAL, and that rests on one assumption: that a release
// can be read without one. What a token buys is GitHub's authenticated rate
// limit, 5000 requests an hour against 60 for an anonymous client sharing one
// outbound address with everything else behind it. If releases ever stop being
// readable anonymously, this is the function that has to start refusing again.
//
// Not finding one is therefore not a failure, and this reports none. Nor is
// finding a bad one: a token GitHub rejects is dropped, see fetch. The
// environment comes first, in gh's own precedence order, because that is how a
// sandbox is handed one; `gh auth token` is the fallback for the mac, where the
// token lives in the keychain and never reaches the environment. gh stays an
// optional dependency.
//
// The token never appears in an error, here or anywhere below: a credential
// that reaches a terminal scrollback or a CI log is not recoverable.
func Token(ctx context.Context) string {
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if token := strings.TrimSpace(os.Getenv(name)); token != "" {
			return token
		}
	}

	ctx, cancel := context.WithTimeout(ctx, tokenTimeout)
	defer cancel()

	// literals, so nothing the caller controls is ever parsed as a command.
	out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(out))
}
