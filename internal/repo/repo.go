// Package repo derives the deterministic identity of a git working tree: the
// URL of its origin remote, and the "<host>/<owner>/<repo>" path tail that
// every per-repo artifact devctl owns is keyed by.
//
// The tail MUST agree with what dotfiles-host's `dev clone` produces
// (zsh/dev.zsh), because the same three segments address the clone on disk, the
// reminder store, and the planned `dev open`. The two implementations are
// cross-checked against the same cases as tests/dev_test.sh; they stop being
// two once `clone`/`sync` move into this repo.
package repo

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

var (
	// ErrNoOrigin reports a directory that is not a git repository, or one with
	// no usable `origin` remote. Callers map it to a usage exit code: there is
	// nothing to retry, the command was pointed at the wrong place.
	ErrNoOrigin = errors.New("no origin remote")

	// ErrBadURL reports a remote URL no deterministic path can be derived from.
	// Deriving anything from an unparseable URL would silently scatter a repo's
	// data across two locations, so this fails loudly instead of guessing.
	ErrBadURL = errors.New("unparseable repository URL")

	// ErrGitMissing reports a git binary that is not on PATH. It is separate
	// from ErrNoOrigin because it is a different problem with a different
	// answer: install git, rather than run the command somewhere else.
	ErrGitMissing = errors.New("git is not installed")
)

// segmentPattern is what a host, owner, or repository segment may look like.
// It is deliberately narrower than what a URL can express: the derived tail is
// joined onto a store root, so a segment carrying a separator, a space, or a
// shell metacharacter would address a directory the caller never meant.
// Compiled once, at package scope, because compiling a regexp allocates.
var segmentPattern = regexp.MustCompile(`^[A-Za-z0-9._~+-]+$`)

// isSafeSegment reports whether one path segment may be joined onto a root.
//
// "." and ".." are checked SEPARATELY from segmentPattern rather than excluded
// by it: a dot is perfectly legal inside a segment — every hostname has one,
// and so does a repo named "dotfiles.git" — so the pattern has to admit it, and
// only these two exact spellings are traversal.
func isSafeSegment(segment string) bool {
	if segment == "." || segment == ".." {
		return false
	}

	return segmentPattern.MatchString(segment)
}

// Origin returns the fetch URL of dir's `origin` remote.
//
// git is invoked with separate arguments and no shell, so neither dir nor
// anything git echoes back can be interpreted as a command.
func Origin(ctx context.Context, dir string) (string, error) {
	//nolint:gosec // the program name is a constant and every argument is passed
	// separately, so no shell ever parses dir — which is the whole point of not
	// building a command string.
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", classify(ctx, dir, err)
	}

	url := strings.TrimSpace(string(out))
	if url == "" {
		return "", fmt.Errorf("%w in %s", ErrNoOrigin, dir)
	}

	return url, nil
}

// classify says what actually went wrong when git could not be asked.
//
// Collapsing every failure into ErrNoOrigin would answer a missing git binary,
// or a Ctrl-C, with "no origin remote in <dir>" — which is not just misleading
// but the wrong exit code, since those are runtime failures rather than the
// caller having pointed devctl at the wrong directory.
func classify(ctx context.Context, dir string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("reading the origin of %s: %w", dir, ctxErr)
	}

	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%w, so no repository can be identified", ErrGitMissing)
	}

	// git ran and refused: not a repository, no such remote, or a repository it
	// will not read. All three are fixed by pointing devctl somewhere else or by
	// changing git's configuration, never by running the same command again.
	if exit, ran := errors.AsType[*exec.ExitError](err); ran {
		return fmt.Errorf("%w in %s%s", ErrNoOrigin, dir, gitSaid(exit))
	}

	return fmt.Errorf("running git in %s: %w", dir, err)
}

// gitSaid renders the first line of git's own diagnostic, which Output()
// captures into Stderr and would otherwise discard.
//
// It matters most for the arrangement this tool exists for: a host repository
// mounted into a sandbox trips git's dubious-ownership check, and without git's
// own words the user is told only "no origin remote" and sent looking for a
// remote that was never the problem. Only the first line travels, so a long
// hint block cannot bury the command's own message.
func gitSaid(exit *exec.ExitError) string {
	first, _, _ := strings.Cut(strings.TrimSpace(string(exit.Stderr)), "\n")
	if first == "" {
		return ""
	}

	return ": " + first
}

// Path derives the "<host>/<owner>/<repo>" tail from a git remote URL.
//
// The scp-like (git@host:owner/repo.git), ssh:// and https:// spellings of one
// repository — with or without a port, with or without a .git suffix or
// trailing slash, with or without userinfo — all collapse to the same value;
// that collapse IS the guarantee, since it is what makes a repo's path
// independent of the protocol it was cloned with. Nested owner segments
// (GitLab subgroups) are preserved.
//
// The returned value always uses forward slashes; a caller joining it onto a
// filesystem path converts with filepath.FromSlash.
//
// The value is case-PRESERVING, because it is also what a human reads: it is
// the name a clone lands under and the name stamped into each reminder's
// header. A caller using it as a DIRECTORY name must pass it through
// EscapePath first; see there for why.
func Path(rawURL string) (string, error) {
	trimmed := strings.TrimSuffix(strings.TrimSuffix(rawURL, "/"), ".git")

	host, rest, err := split(trimmed)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrBadURL, Redact(rawURL))
	}

	segments := append([]string{host}, strings.Split(rest, "/")...)
	// host + owner + repo at minimum; a URL naming only a host has no repo in it.
	if len(segments) < 3 {
		return "", fmt.Errorf("%w: %s", ErrBadURL, Redact(rawURL))
	}

	for _, segment := range segments {
		if !isSafeSegment(segment) {
			return "", fmt.Errorf("%w: refusing path segment %q in %s", ErrBadURL, segment, Redact(rawURL))
		}
	}

	return strings.Join(segments, "/"), nil
}

// split separates a remote URL into its host and its path, dropping the scheme,
// any userinfo, and any port.
func split(url string) (host, rest string, err error) {
	if _, authorityAndPath, ok := strings.Cut(url, "://"); ok {
		authority, path, ok := strings.Cut(authorityAndPath, "/")
		if !ok {
			return "", "", ErrBadURL
		}

		return hostOf(authority), path, nil
	}

	// scp-like [user@]host:path. A port is not expressible in this form, so the
	// FIRST colon separates the host from the path.
	authority, path, ok := strings.Cut(url, ":")
	if !ok {
		return "", "", ErrBadURL
	}

	return hostOf(authority), path, nil
}

// hostOf strips userinfo and any port from a URL authority.
//
// Userinfo is split at the LAST "@" rather than the first: RFC 3986 percent-
// encodes a literal "@" inside userinfo, so the last one is the separator, and
// net/url reads it the same way.
//
// dotfiles-host's zsh helper cuts at the FIRST "@", so the two genuinely
// disagree on a URL carrying two of them: "https://a@b@github.com/owner/repo"
// yields "b@github.com/owner/repo" there and "github.com/owner/repo" here.
// Neither spelling is a real remote and this reading is the correct one, but
// the disagreement is real rather than theoretical, and it disappears when
// `dev clone` moves into this repo and only one implementation is left.
func hostOf(authority string) string {
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		authority = authority[at+1:]
	}

	host, _, _ := strings.Cut(authority, ":")

	return host
}

// EscapePath encodes a derived repository path so that two spellings differing
// only in case cannot collide on a case-insensitive filesystem.
//
// Each uppercase letter becomes "!" followed by its lowercase form, so
// "github.com/Acme/Widget" encodes to "github.com/!acme/!widget". This is Go's
// own encoding, the one the module cache uses for exactly this problem: the
// "!burnt!sushi" entries under ~/go/pkg/mod/github.com are it.
//
// Why it is load-bearing rather than cosmetic: a reminder store is meant to be
// shared between a mac, whose APFS is case-insensitive, and a linux sandbox,
// whose filesystem is not. Unencoded, "Acme/Widget" and "acme/widget" are two
// directories on one side and one directory on the other, so the two sides
// disagree about whether they are looking at the same list. Encoded, every
// character that survives is lowercase, so the two sides always agree.
//
// It deliberately does not FOLD case. Two spellings remain two stores, because
// nothing here can know whether a given host treats them as one repository.
// What this guarantees is that every machine answers that question identically.
func EscapePath(path string) string {
	// Only ASCII can reach here: segmentPattern admits nothing else, and
	// restricting to A-Z keeps this byte-for-byte the encoding Go uses.
	if !strings.ContainsFunc(path, func(r rune) bool { return r >= 'A' && r <= 'Z' }) {
		return path
	}

	var escaped strings.Builder

	escaped.Grow(len(path) + 8)

	for _, char := range []byte(path) {
		if char >= 'A' && char <= 'Z' {
			escaped.WriteByte('!')
			escaped.WriteByte(char + ('a' - 'A'))

			continue
		}

		escaped.WriteByte(char)
	}

	return escaped.String()
}

// Redact returns url with any "user[:secret]@" userinfo removed, for echoing in
// a diagnostic. A personal access token mistyped into a URL must not reach the
// terminal scrollback or a CI log just because the URL failed to parse.
func Redact(url string) string {
	scheme := ""
	if before, after, ok := strings.Cut(url, "://"); ok {
		scheme, url = before+"://", after
	}

	authority, path := url, ""
	if before, after, ok := strings.Cut(url, "/"); ok {
		authority, path = before, "/"+after
	}

	if at := strings.LastIndex(authority, "@"); at >= 0 {
		authority = authority[at+1:]
	}

	return scheme + authority + path
}
